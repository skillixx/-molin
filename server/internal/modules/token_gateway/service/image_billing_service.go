package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	billingmodel "molin/server/internal/modules/billing/model"
	billingservice "molin/server/internal/modules/billing/service"
	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var (
	ErrImageBillingState      = errors.New("图片请求计费状态不允许")
	ErrImageExecutionStarted  = errors.New("图片请求已经开始执行")
	ErrImagePendingReconcile  = errors.New("图片请求等待补偿或人工核对")
	ErrImageReconcileMismatch = errors.New("图片请求对账存在差异")
	ErrImageAdjustmentInvalid = errors.New("图片调账参数无效")
	ErrImageCleanupTombstone  = errors.New("图片对象已进入持久清理流程")
)

const (
	imageRecoveryUnknown = "unknown"
	imageRecoverySettle  = "settle"
	imageRecoveryRelease = "release"

	imagePostProviderActionTimeout = 5 * time.Second
)

// imageRecoveryFacts 只保存补偿所需的低敏确定事实，禁止写入Prompt、Provider原始响应或对象正文。
type imageRecoveryFacts struct {
	ProviderResultCount uint64 `json:"provider_result_count"`
	ProviderCostUSD     string `json:"provider_cost_usd,omitempty"`
	DeliverableCount    uint64 `json:"deliverable_count"`
	RecoveryAction      string `json:"recovery_action"`
	FinalErrorClass     string `json:"final_error_class,omitempty"`
}

type imageAssetPersistenceState string

const (
	imageAssetsPersistedZero     imageAssetPersistenceState = "zero"
	imageAssetsPersistedComplete imageAssetPersistenceState = "complete"
	imageAssetsPersistedPartial  imageAssetPersistenceState = "partial"
)

type imageWalletHoldService interface {
	CreateHoldTx(tx *gorm.DB, userID uint64, amount decimal.Decimal, idempotencyKey, remark string) (*billingservice.HoldTxResult, error)
	SettleHoldTx(tx *gorm.DB, holdID uint64, actual decimal.Decimal, idempotencyKey string) (*billingservice.SettleTxResult, error)
	ReleaseHoldTx(tx *gorm.DB, holdID uint64, idempotencyKey string) (*billingservice.SettleTxResult, error)
}

type imageGatewayRunner interface {
	Generate(ctx context.Context, command imagegateway.GenerateImageCommand) (imagegateway.GatewayResult, error)
}

type ImageReserveCommand struct {
	RequestID          string
	QuotePublicID      string
	Owner              repository.ImageOwner
	RequestFingerprint string
}

type ImageReservation struct {
	RequestID  string
	HoldID     uint64
	HeldAmount decimal.Decimal
}

type ImagePrepareAndReserveCommand struct {
	Request       model.AIRequest
	Task          model.AIImageTask
	InlineQuote   *model.AIGatewayQuote
	QuotePublicID string
	Owner         repository.ImageOwner
}

type ImagePreparedReservation struct {
	ImageReservation
	TaskPublicID string
	Existing     bool
}

type ImageBillingExecution struct {
	GatewayResult  imagegateway.GatewayResult
	BillingStatus  string
	DeliveryStatus string
}

type ImageReconciliationReport struct {
	RequestID            string
	BillingStatus        string
	RequestUsageDiff     decimal.Decimal
	RequestHoldDiff      decimal.Decimal
	RequestWalletDiff    decimal.Decimal
	SaleUsageCountDiff   decimal.Decimal
	UsageAssetCountDiff  decimal.Decimal
	CostQuantityDiff     decimal.Decimal
	CostAmountDiff       decimal.Decimal
	CostLineCount        int64
	AdjustmentCount      int64
	WalletFactMismatches int64
	ActiveCompensations  int64
	MissingOutboxEvents  int64
	OutboxFactMismatches int64
}

func (r ImageReconciliationReport) ZeroDifference() bool {
	return r.RequestUsageDiff.IsZero() && r.RequestHoldDiff.IsZero() && r.RequestWalletDiff.IsZero() &&
		r.SaleUsageCountDiff.IsZero() && r.UsageAssetCountDiff.IsZero() && r.CostQuantityDiff.IsZero() &&
		r.CostAmountDiff.IsZero() && r.CostLineCount == 1 && r.AdjustmentCount == 0 &&
		r.WalletFactMismatches == 0 && r.ActiveCompensations == 0 && r.MissingOutboxEvents == 0 && r.OutboxFactMismatches == 0
}

type ImageBillingService struct {
	db                *gorm.DB
	holds             imageWalletHoldService
	quotes            *repository.ImageQuoteRepository
	tasks             *repository.ImageTaskRepository
	assets            *repository.ImageAssetRepository
	compensations     *repository.ImageCompensationRepository
	pricing           *ImagePricingService
	gateway           imageGatewayRunner
	cleanup           imagegateway.ObjectCleanupRecorder
	now               func() time.Time
	beforeFinalize    func() error
	beforeMarkPending func() error
	afterAssetCommit  func() error
	inspectAssets     func(context.Context, string, repository.ImageOwner, imagegateway.GatewayResult) (imageAssetPersistenceState, error)
}

func NewImageBillingService(db *gorm.DB, holds imageWalletHoldService, pricing *ImagePricingService, gateway imageGatewayRunner, cleanup imagegateway.ObjectCleanupRecorder) (*ImageBillingService, error) {
	if db == nil || holds == nil || pricing == nil || gateway == nil || cleanup == nil {
		return nil, ErrImageBillingState
	}
	service := &ImageBillingService{
		db: db, holds: holds, quotes: repository.NewImageQuoteRepository(db), tasks: repository.NewImageTaskRepository(db),
		assets: repository.NewImageAssetRepository(db), compensations: repository.NewImageCompensationRepository(db),
		pricing: pricing, gateway: gateway, cleanup: cleanup, now: time.Now,
	}
	service.inspectAssets = service.inspectPersistedGatewayAssets
	return service, nil
}

// Reserve 在同一MySQL事务中消费Quote、创建钱包hold、写请求快照/关联和Outbox。
func (s *ImageBillingService) Reserve(ctx context.Context, command ImageReserveCommand) (*ImageReservation, error) {
	if s == nil || strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.QuotePublicID) == "" ||
		command.Owner.UserID == 0 || command.Owner.ProjectID == 0 || len(command.RequestFingerprint) != 64 {
		return nil, ErrImageBillingState
	}
	now := s.now().UTC()
	var reservation *ImageReservation
	err := retryImageBillingTransaction(ctx, func() error {
		reservation = nil
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var reserveErr error
			reservation, reserveErr = s.reserveTx(tx, command, now)
			return reserveErr
		})
	})
	return reservation, err
}

// PrepareAndReserve 在一个事务内创建请求和任务、消费Quote并冻结钱包；相同幂等键只返回原事实。
func (s *ImageBillingService) PrepareAndReserve(ctx context.Context, command ImagePrepareAndReserveCommand) (*ImagePreparedReservation, error) {
	request := command.Request
	if s == nil || request.RequestID == "" || request.IdempotencyKey == nil || request.RequestFingerprint == nil ||
		*request.IdempotencyKey == "" || len(*request.RequestFingerprint) != 64 || command.Task.PublicID == "" ||
		command.Owner.UserID == 0 || command.Owner.ProjectID == 0 || command.QuotePublicID == "" {
		return nil, ErrImageBillingState
	}
	now := s.now().UTC()
	var prepared *ImagePreparedReservation
	err := retryImageBillingTransaction(ctx, func() error {
		prepared = nil
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var existing model.AIRequest
			existingErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("user_id = ? AND idempotency_key = ?", request.UserID, *request.IdempotencyKey).First(&existing).Error
			if existingErr == nil {
				if existing.RequestFingerprint == nil || subtle.ConstantTimeCompare([]byte(*existing.RequestFingerprint), []byte(*request.RequestFingerprint)) != 1 ||
					existing.Modality != "image" || existing.ProjectID == nil || *existing.ProjectID != command.Owner.ProjectID || !equalOptionalUint64(existing.APIKeyID, command.Owner.APIKeyID) {
					return ErrImageQuoteConflict
				}
				var task model.AIImageTask
				if err := tx.Where("request_id = ?", existing.RequestID).First(&task).Error; err != nil {
					return err
				}
				var existingQuote model.AIGatewayQuote
				if err := tx.Where("id = ?", task.QuoteID).First(&existingQuote).Error; err != nil {
					return err
				}
				reservation, err := s.reserveTx(tx, ImageReserveCommand{
					RequestID: existing.RequestID, QuotePublicID: existingQuote.PublicID, Owner: command.Owner, RequestFingerprint: *request.RequestFingerprint,
				}, now)
				if err != nil {
					return err
				}
				prepared = &ImagePreparedReservation{ImageReservation: *reservation, TaskPublicID: task.PublicID, Existing: true}
				return nil
			}
			if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
				return existingErr
			}
			if command.InlineQuote != nil {
				quote := *command.InlineQuote
				if quote.PublicID != command.QuotePublicID || quote.ID != 0 {
					return ErrImageBillingState
				}
				if err := tx.Create(&quote).Error; err != nil {
					return err
				}
				command.Task.QuoteID = quote.ID
			} else {
				var quote model.AIGatewayQuote
				if err := tx.Where("public_id = ? AND user_id = ? AND project_id = ?", command.QuotePublicID, command.Owner.UserID, command.Owner.ProjectID).First(&quote).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return repository.ErrImageQuoteNotFound
					}
					return err
				}
				command.Task.QuoteID = quote.ID
			}
			if err := tx.Create(&request).Error; err != nil {
				return err
			}
			command.Task.RequestID = request.RequestID
			if err := tx.Create(&command.Task).Error; err != nil {
				return err
			}
			reservation, err := s.reserveTx(tx, ImageReserveCommand{
				RequestID: request.RequestID, QuotePublicID: command.QuotePublicID, Owner: command.Owner, RequestFingerprint: *request.RequestFingerprint,
			}, now)
			if err != nil {
				return err
			}
			prepared = &ImagePreparedReservation{ImageReservation: *reservation, TaskPublicID: command.Task.PublicID}
			return nil
		})
	})
	return prepared, err
}

func (s *ImageBillingService) reserveTx(tx *gorm.DB, command ImageReserveCommand, now time.Time) (*ImageReservation, error) {
	request, err := lockOwnedImageRequest(tx, command.RequestID, command.Owner)
	if err != nil {
		return nil, err
	}
	var existingLink model.AIRequestWalletLink
	if err := tx.Where("request_id = ?", request.RequestID).First(&existingLink).Error; err == nil {
		billingKnown := request.BillingStatus == model.AIBillingHeld || request.BillingStatus == model.AIBillingSettlementPending ||
			request.BillingStatus == model.AIBillingSettled || request.BillingStatus == model.AIBillingReleased
		if !billingKnown || !existingLink.HeldAmount.Equal(existingLink.QuotedAmount) {
			return nil, ErrImageBillingState
		}
		return &ImageReservation{RequestID: request.RequestID, HoldID: existingLink.WalletHoldID, HeldAmount: existingLink.HeldAmount}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if request.BillingStatus != model.AIBillingUnquoted || request.Modality != "image" || request.Capability != model.AIImageCapability {
		return nil, ErrImageBillingState
	}
	quote, _, err := s.quotes.ConsumeTx(tx, command.QuotePublicID, command.Owner.UserID, command.Owner.ProjectID, command.Owner.APIKeyID,
		command.RequestFingerprint, request.RequestID, now)
	if err != nil {
		return nil, err
	}
	hold, err := s.holds.CreateHoldTx(tx, request.UserID, quote.QuotedAmount, request.RequestID+":reserve", "图片生成预占")
	if err != nil {
		return nil, err
	}
	link := model.AIRequestWalletLink{
		RequestID: request.RequestID, WalletID: hold.WalletID, WalletHoldID: hold.HoldID, HoldTransactionID: hold.FreezeTransaction,
		QuotedAmount: quote.QuotedAmount, HeldAmount: quote.QuotedAmount,
	}
	if err := tx.Create(&link).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&model.AIGatewayQuote{}).Where("id = ?", quote.ID).Update("held_amount", quote.QuotedAmount).Error; err != nil {
		return nil, err
	}
	result := tx.Model(&model.AIRequest{}).Where("id = ? AND billing_status = ?", request.ID, model.AIBillingUnquoted).
		Updates(map[string]interface{}{
			"price_snapshot_json": quote.PriceSnapshotJSON, "quoted_amount": quote.QuotedAmount,
			"held_amount": quote.QuotedAmount, "billing_status": model.AIBillingHeld,
		})
	if result.Error != nil || result.RowsAffected != 1 {
		return nil, ErrImageBillingState
	}
	taskResult := tx.Model(&model.AIImageTask{}).Where("request_id = ? AND status = ?", request.RequestID, model.AIImageTaskCreated).
		Updates(map[string]interface{}{"status": model.AIImageTaskReserved, "version_no": gorm.Expr("version_no + 1")})
	if taskResult.Error != nil {
		return nil, taskResult.Error
	}
	if taskResult.RowsAffected != 1 {
		return nil, ErrImageBillingState
	}
	if err := createImageOutboxTx(tx, request.RequestID, "image_billing_held", model.AIBillingHeld, quote.QuotedAmount, now); err != nil {
		return nil, err
	}
	return &ImageReservation{RequestID: request.RequestID, HoldID: hold.HoldID, HeldAmount: quote.QuotedAmount}, nil
}

func equalOptionalUint64(left, right *uint64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// Execute 只在pending请求上取得一次执行权；任何重放都不得再次调用Provider。
func (s *ImageBillingService) Execute(ctx context.Context, requestID string, command imagegateway.GenerateImageCommand) (*ImageBillingExecution, error) {
	if s == nil || requestID == "" || command.RequestID != requestID {
		return nil, ErrImageBillingState
	}
	owner, err := s.claimExecution(ctx, requestID)
	if err != nil {
		return nil, err
	}
	result, gatewayErr := s.gateway.Generate(ctx, command)
	if len(result.Assets) > 0 {
		persistErr := runImagePostProviderAction(ctx, func(localCtx context.Context) error {
			return s.persistGatewayAssets(localCtx, requestID, owner, result)
		})
		if persistErr != nil {
			persistenceState := imageAssetsPersistedPartial
			inspectErr := runImagePostProviderAction(ctx, func(localCtx context.Context) error {
				if s.inspectAssets == nil {
					return ErrImageBillingState
				}
				var inspectStateErr error
				persistenceState, inspectStateErr = s.inspectAssets(localCtx, requestID, owner, result)
				return inspectStateErr
			})
			if inspectErr == nil && persistenceState == imageAssetsPersistedComplete {
				// 提交响应未知但完整资产事实已可见，继续后续结算，禁止把已引用对象登记为待删除。
			} else {
				pendingClass := "asset_metadata_partial"
				if inspectErr != nil {
					pendingClass = "asset_metadata_unknown"
				} else if persistenceState == imageAssetsPersistedZero {
					pendingClass = "asset_metadata_failed"
					if cleanupErr := s.recordUntrackedGatewayAssets(ctx, requestID, result.Assets); cleanupErr != nil {
						pendingClass = "asset_cleanup_unrecorded"
					}
				}
				pendingResult := result
				pendingResult.ErrorClass = pendingClass
				if pendingErr := runImagePostProviderAction(ctx, func(localCtx context.Context) error {
					return s.markPendingReconcile(localCtx, requestID, pendingResult, imageRecoveryUnknown, pendingClass)
				}); pendingErr != nil {
					return nil, errors.Join(persistErr, pendingErr)
				}
				return &ImageBillingExecution{GatewayResult: pendingResult, BillingStatus: model.AIBillingSettlementPending, DeliveryStatus: model.AIDeliveryPending}, ErrImagePendingReconcile
			}
		}
	}
	if gatewayErr == nil {
		if s.beforeFinalize != nil {
			if err := s.beforeFinalize(); err != nil {
				if pendingErr := runImagePostProviderAction(ctx, func(localCtx context.Context) error {
					return s.markPendingReconcile(localCtx, requestID, result, imageRecoverySettle, "settlement_injected_failure")
				}); pendingErr != nil {
					return nil, pendingErr
				}
				return &ImageBillingExecution{GatewayResult: result, BillingStatus: model.AIBillingSettlementPending, DeliveryStatus: model.AIDeliveryPending}, ErrImagePendingReconcile
			}
		}
		if err := runImagePostProviderAction(ctx, func(localCtx context.Context) error {
			return s.finalizeSuccess(localCtx, requestID, result.ProviderResultCount)
		}); err != nil {
			if pendingErr := runImagePostProviderAction(ctx, func(localCtx context.Context) error {
				return s.markPendingReconcile(localCtx, requestID, result, imageRecoverySettle, "settlement_failed")
			}); pendingErr != nil {
				return nil, pendingErr
			}
			return &ImageBillingExecution{GatewayResult: result, BillingStatus: model.AIBillingSettlementPending, DeliveryStatus: model.AIDeliveryPending}, ErrImagePendingReconcile
		}
		return &ImageBillingExecution{GatewayResult: result, BillingStatus: model.AIBillingSettled, DeliveryStatus: model.AIDeliveryAvailable}, nil
	}
	if result.Outcome == imagegateway.GatewayUnknown || result.Outcome == imagegateway.GatewayTimeout || result.Outcome == imagegateway.GatewayDisconnected ||
		result.ErrorClass == "asset_storage_failed" || result.ErrorClass == "asset_cleanup_unrecorded" || result.ErrorClass == "moderation_unavailable" {
		if err := runImagePostProviderAction(ctx, func(localCtx context.Context) error {
			return s.markPendingReconcile(localCtx, requestID, result, imageRecoveryUnknown, result.ErrorClass)
		}); err != nil {
			return nil, err
		}
		return &ImageBillingExecution{GatewayResult: result, BillingStatus: model.AIBillingSettlementPending, DeliveryStatus: model.AIDeliveryPending}, ErrImagePendingReconcile
	}
	if err := runImagePostProviderAction(ctx, func(localCtx context.Context) error {
		return s.finalizeRelease(localCtx, requestID, result.ProviderResultCount, result.ErrorClass)
	}); err != nil {
		// Provider结果已经明确，释放事务失败后只登记本地release补偿，禁止再次调用Provider。
		if pendingErr := runImagePostProviderAction(ctx, func(localCtx context.Context) error {
			return s.markPendingReconcile(localCtx, requestID, result, imageRecoveryRelease, "release_failed")
		}); pendingErr != nil {
			return nil, pendingErr
		}
		return &ImageBillingExecution{GatewayResult: result, BillingStatus: model.AIBillingSettlementPending, DeliveryStatus: model.AIDeliveryRejected}, ErrImagePendingReconcile
	}
	return &ImageBillingExecution{GatewayResult: result, BillingStatus: model.AIBillingReleased, DeliveryStatus: model.AIDeliveryRejected}, gatewayErr
}

// runImagePostProviderAction 为Provider后的本地事务提供独立有界上下文；客户端取消不能阻断资金与补偿事实落库。
func runImagePostProviderAction(parent context.Context, action func(context.Context) error) error {
	if action == nil {
		return ErrImageBillingState
	}
	localCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), imagePostProviderActionTimeout)
	defer cancel()
	return action(localCtx)
}

// RequestCancel 只对尚未调用Provider的reserved任务立即释放资金；已开始执行时仅记录取消意图，禁止擅自判定免费。
func (s *ImageBillingService) RequestCancel(ctx context.Context, taskPublicID string, owner repository.ImageOwner) (bool, error) {
	if s == nil || taskPublicID == "" || owner.UserID == 0 || owner.ProjectID == 0 {
		return false, ErrImageBillingState
	}
	pending := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("public_id = ? AND user_id = ? AND project_id = ?", taskPublicID, owner.UserID, owner.ProjectID)
		if owner.APIKeyID != nil {
			query = query.Where("api_key_id = ?", *owner.APIKeyID)
		}
		var task model.AIImageTask
		if err := query.First(&task).Error; err != nil {
			return repository.ErrImageTaskNotFound
		}
		if imageTaskTerminalForCancel(task.Status) {
			return nil
		}
		request, link, err := lockImageRequestAndLink(tx, task.RequestID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if task.Status != model.AIImageTaskReserved || request.ExecutionStatus != model.AIExecutionPending || request.BillingStatus != model.AIBillingHeld {
			pending = true
			return tx.Model(&model.AIImageTask{}).Where("id = ? AND cancel_requested_at IS NULL", task.ID).
				Updates(map[string]interface{}{"cancel_requested_at": now, "version_no": gorm.Expr("version_no + 1")}).Error
		}
		settlement, err := s.pricing.CalculateImageFinalWithProviderCount(task.RequestID, request.PriceSnapshotJSON, 0, 0)
		if err != nil {
			return err
		}
		if err := createImageUsageItemsTx(tx, []model.AIUsageItem{settlement.UsageFact, settlement.SaleLine, settlement.CostLine}); err != nil {
			return err
		}
		walletResult, err := s.holds.ReleaseHoldTx(tx, link.WalletHoldID, task.RequestID+":cancel")
		if err != nil {
			return err
		}
		zero := decimal.Zero
		if err := tx.Model(&model.AIRequestWalletLink{}).Where("id = ?", link.ID).Updates(map[string]interface{}{
			"settled_amount": zero, "release_transaction_id": walletResult.ReleaseTransaction,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.AIRequest{}).Where("id = ?", request.ID).Updates(map[string]interface{}{
			"execution_status": model.AIExecutionCancelled, "billing_status": model.AIBillingReleased,
			"delivery_status": model.AIDeliveryRejected, "settled_amount": zero, "completed_at": now,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.AIImageTask{}).Where("id = ? AND status = ?", task.ID, model.AIImageTaskReserved).Updates(map[string]interface{}{
			"status": model.AIImageTaskCancelled, "progress": 100, "cancel_requested_at": now,
			"completed_at": now, "version_no": gorm.Expr("version_no + 1"),
		}).Error; err != nil {
			return err
		}
		return createImageOutboxTx(tx, task.RequestID, "image_billing_released", model.AIBillingReleased, zero, now)
	})
	return pending, err
}

// CancelRequestBeforeExecution 供队列恢复路径按request_id释放未调用Provider的任务，不接受HTTP调用方直接使用。
func (s *ImageBillingService) CancelRequestBeforeExecution(ctx context.Context, requestID string) error {
	if s == nil || requestID == "" {
		return ErrImageBillingState
	}
	var task model.AIImageTask
	if err := s.db.WithContext(ctx).Where("request_id = ?", requestID).First(&task).Error; err != nil {
		return err
	}
	_, err := s.RequestCancel(ctx, task.PublicID, repository.ImageOwner{UserID: task.UserID, ProjectID: task.ProjectID, APIKeyID: task.APIKeyID})
	return err
}

func imageTaskTerminalForCancel(status string) bool {
	switch status {
	case model.AIImageTaskSucceeded, model.AIImageTaskFailed, model.AIImageTaskCancelled, model.AIImageTaskExpired:
		return true
	default:
		return false
	}
}

func (s *ImageBillingService) claimExecution(ctx context.Context, requestID string) (repository.ImageOwner, error) {
	var owner repository.ImageOwner
	err := retryImageBillingTransaction(ctx, func() error {
		owner = repository.ImageOwner{}
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var request model.AIRequest
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ?", requestID).First(&request).Error; err != nil {
				return err
			}
			if request.BillingStatus != model.AIBillingHeld || request.ExecutionStatus != model.AIExecutionPending {
				return ErrImageExecutionStarted
			}
			result := tx.Model(&model.AIRequest{}).Where("id = ? AND execution_status = ?", request.ID, model.AIExecutionPending).
				Updates(map[string]interface{}{"execution_status": model.AIExecutionRunning, "started_at": s.now().UTC()})
			if result.Error != nil || result.RowsAffected != 1 {
				return ErrImageExecutionStarted
			}
			var task model.AIImageTask
			if err := tx.Where("request_id = ?", requestID).First(&task).Error; err != nil {
				return err
			}
			if task.Status != model.AIImageTaskReserved {
				return ErrImageBillingState
			}
			taskResult := tx.Model(&model.AIImageTask{}).Where("id = ? AND status = ?", task.ID, model.AIImageTaskReserved).
				Updates(map[string]interface{}{"status": model.AIImageTaskProcessing, "progress": 40, "version_no": gorm.Expr("version_no + 1")})
			if taskResult.Error != nil {
				return taskResult.Error
			}
			if taskResult.RowsAffected != 1 {
				return ErrImageExecutionStarted
			}
			owner = repository.ImageOwner{UserID: request.UserID, ProjectID: dereferenceUint64(request.ProjectID), APIKeyID: request.APIKeyID}
			return nil
		})
	})
	return owner, err
}

func (s *ImageBillingService) persistGatewayAssets(ctx context.Context, requestID string, owner repository.ImageOwner, result imagegateway.GatewayResult) error {
	persistErr := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task model.AIImageTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ? AND user_id = ? AND project_id = ?", requestID, owner.UserID, owner.ProjectID).First(&task).Error; err != nil {
			return err
		}
		var existing int64
		if err := tx.Model(&model.AIImageAsset{}).Where("request_id = ?", requestID).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return ErrImageBillingState
		}
		parents := make(map[uint64]uint64)
		for _, gatewayAsset := range result.Assets {
			if gatewayAsset.AssetRole != model.AIImageAssetPrimaryOutput {
				continue
			}
			if err := rejectImageCleanupTombstoneTx(tx, gatewayAsset.StoredObject.Ref); err != nil {
				return err
			}
			asset, err := buildImageAsset(requestID, task.ID, owner, gatewayAsset, nil, s.now().UTC())
			if err != nil {
				return err
			}
			if err := tx.Create(asset).Error; err != nil {
				return err
			}
			parents[gatewayAsset.ResultIndex] = asset.ID
		}
		for _, gatewayAsset := range result.Assets {
			if gatewayAsset.AssetRole == model.AIImageAssetPrimaryOutput {
				continue
			}
			parentID, ok := parents[gatewayAsset.ResultIndex]
			if !ok {
				return ErrImageBillingState
			}
			if err := rejectImageCleanupTombstoneTx(tx, gatewayAsset.StoredObject.Ref); err != nil {
				return err
			}
			asset, err := buildImageAsset(requestID, task.ID, owner, gatewayAsset, &parentID, s.now().UTC())
			if err != nil {
				return err
			}
			if err := tx.Create(asset).Error; err != nil {
				return err
			}
		}
		resultSummary, _ := json.Marshal(map[string]interface{}{
			"provider_result_count": result.ProviderResultCount, "provider_cost_usd": result.ProviderCostUSD, "deliverable_count": result.DeliverableCount,
		})
		return tx.Model(&model.AIImageTask{}).Where("id = ?", task.ID).
			Updates(map[string]interface{}{"status": model.AIImageTaskModerating, "progress": 80, "result_json": resultSummary, "version_no": gorm.Expr("version_no + 1")}).Error
	})
	if persistErr == nil && s.afterAssetCommit != nil {
		return s.afterAssetCommit()
	}
	return persistErr
}

// rejectImageCleanupTombstoneTx 使用唯一task_key当前读锁定；清理任务任何状态都永久阻止后到的资产引用。
func rejectImageCleanupTombstoneTx(tx *gorm.DB, ref imagegateway.ObjectRef) error {
	if tx == nil {
		return ErrImageBillingState
	}
	taskKey, err := repository.ImageObjectCleanupTaskKey(ref)
	if err != nil {
		return err
	}
	var tombstone model.AICompensationTask
	err = tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("id").Where("task_key = ?", taskKey).Take(&tombstone).Error
	switch {
	case err == nil:
		return ErrImageCleanupTombstone
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil
	default:
		return err
	}
}

// inspectPersistedGatewayAssets 在原事务结果未知时只读取独立提交后的事实，不依据原错误猜测提交结果。
func (s *ImageBillingService) inspectPersistedGatewayAssets(ctx context.Context, requestID string, owner repository.ImageOwner, result imagegateway.GatewayResult) (imageAssetPersistenceState, error) {
	if s == nil || s.db == nil || requestID == "" || owner.UserID == 0 || owner.ProjectID == 0 || len(result.Assets) == 0 {
		return imageAssetsPersistedPartial, ErrImageBillingState
	}
	expected := make(map[string]imagegateway.GatewayAsset, len(result.Assets))
	for _, asset := range result.Assets {
		identity := fmt.Sprintf("%d:%s", asset.ResultIndex, asset.AssetRole)
		if previous, exists := expected[identity]; exists {
			if !sameGatewayAssetMetadata(previous, asset) {
				return imageAssetsPersistedPartial, nil
			}
			continue
		}
		expected[identity] = asset
	}
	var persisted []model.AIImageAsset
	if err := s.db.WithContext(ctx).
		Where("request_id = ? AND user_id = ? AND project_id = ?", requestID, owner.UserID, owner.ProjectID).
		Find(&persisted).Error; err != nil {
		return imageAssetsPersistedPartial, err
	}
	if len(persisted) == 0 {
		return imageAssetsPersistedZero, nil
	}
	if len(persisted) != len(expected) {
		return imageAssetsPersistedPartial, nil
	}
	seen := make(map[string]struct{}, len(persisted))
	for _, asset := range persisted {
		identity := fmt.Sprintf("%d:%s", asset.ResultIndex, asset.AssetRole)
		expectedAsset, exists := expected[identity]
		if !exists || !persistedAssetMatchesGateway(asset, expectedAsset) {
			return imageAssetsPersistedPartial, nil
		}
		if _, duplicate := seen[identity]; duplicate {
			return imageAssetsPersistedPartial, nil
		}
		seen[identity] = struct{}{}
	}
	return imageAssetsPersistedComplete, nil
}

func sameGatewayAssetMetadata(left, right imagegateway.GatewayAsset) bool {
	return left.ResultIndex == right.ResultIndex && left.AssetRole == right.AssetRole && left.Source == right.Source &&
		left.IsBillableOutput == right.IsBillableOutput && left.LifecycleState == right.LifecycleState &&
		left.ModerationStatus == right.ModerationStatus && left.ExplicitLabelState == right.ExplicitLabelState &&
		left.ImplicitLabelState == right.ImplicitLabelState && left.MIMEType == right.MIMEType &&
		left.Width == right.Width && left.Height == right.Height && left.StoredObject.Ref == right.StoredObject.Ref &&
		left.StoredObject.SizeBytes == right.StoredObject.SizeBytes && left.StoredObject.SHA256 == right.StoredObject.SHA256
}

func persistedAssetMatchesGateway(persisted model.AIImageAsset, expected imagegateway.GatewayAsset) bool {
	return uint64(persisted.ResultIndex) == expected.ResultIndex && persisted.AssetRole == expected.AssetRole &&
		persisted.IsBillableOutput == expected.IsBillableOutput && persisted.Bucket != nil && *persisted.Bucket == expected.StoredObject.Ref.Bucket &&
		persisted.ObjectKey != nil && *persisted.ObjectKey == expected.StoredObject.Ref.Key && persisted.MIMEType != nil && *persisted.MIMEType == expected.MIMEType &&
		persisted.SizeBytes != nil && *persisted.SizeBytes == expected.StoredObject.SizeBytes && persisted.SHA256 != nil && *persisted.SHA256 == expected.StoredObject.SHA256 &&
		persisted.Width != nil && uint64(*persisted.Width) == uint64(expected.Width) && persisted.Height != nil && uint64(*persisted.Height) == uint64(expected.Height) &&
		persisted.Source == expected.Source && persisted.ModerationStatus == expected.ModerationStatus &&
		persisted.ExplicitLabelStatus == expected.ExplicitLabelState && persisted.ImplicitLabelStatus == expected.ImplicitLabelState &&
		persisted.LifecycleState == expected.LifecycleState
}

// recordUntrackedGatewayAssets 在元数据事务回滚后逐项持久化对象清理事实，避免ai-result形成不可追踪孤儿。
func (s *ImageBillingService) recordUntrackedGatewayAssets(ctx context.Context, requestID string, assets []imagegateway.GatewayAsset) error {
	if s == nil || s.cleanup == nil || requestID == "" {
		return ErrImageBillingState
	}
	var recordErr error
	for _, asset := range assets {
		ref := asset.StoredObject.Ref
		if ref.Bucket == "" || ref.Key == "" {
			recordErr = errors.Join(recordErr, ErrImageBillingState)
			continue
		}
		// 请求即使已经取消，也必须给持久清理写入一个独立且有界的机会。
		recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err := s.cleanup.RecordObjectCleanup(recordCtx, imagegateway.ObjectCleanupTask{
			RequestID: requestID,
			Ref:       ref,
			Reason:    imagegateway.ObjectCleanupAfterMetadataPersistFailure,
		})
		cancel()
		if err != nil {
			recordErr = errors.Join(recordErr, err)
		}
	}
	return recordErr
}

func buildImageAsset(requestID string, taskID uint64, owner repository.ImageOwner, source imagegateway.GatewayAsset, parentID *uint64, now time.Time) (*model.AIImageAsset, error) {
	if source.Width <= 0 || source.Height <= 0 || source.StoredObject.Ref.Bucket == "" || source.StoredObject.Ref.Key == "" || source.MIMEType == "" {
		return nil, ErrImageBillingState
	}
	width, height := uint32(source.Width), uint32(source.Height)
	bucket, objectKey, mimeType := source.StoredObject.Ref.Bucket, source.StoredObject.Ref.Key, source.MIMEType
	size, digest := source.StoredObject.SizeBytes, source.StoredObject.SHA256
	return &model.AIImageAsset{
		PublicID: imageAssetPublicID(requestID, source.ResultIndex, source.AssetRole), UserID: owner.UserID, ProjectID: owner.ProjectID,
		RequestID: requestID, TaskID: taskID, ResultIndex: uint32(source.ResultIndex), AssetRole: source.AssetRole,
		ParentAssetID: parentID, IsBillableOutput: source.IsBillableOutput, Bucket: &bucket, ObjectKey: &objectKey,
		MIMEType: &mimeType, SizeBytes: &size, SHA256: &digest, Width: &width, Height: &height,
		Source: source.Source, ModerationStatus: source.ModerationStatus,
		ExplicitLabelStatus: source.ExplicitLabelState, ImplicitLabelStatus: source.ImplicitLabelState,
		LifecycleState: source.LifecycleState, RetentionPolicyID: retentionPolicy(source.LifecycleState),
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}, nil
}

func (s *ImageBillingService) finalizeSuccess(ctx context.Context, requestID string, providerResultCount uint64) error {
	return retryImageBillingTransaction(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			request, link, err := lockImageRequestAndLink(tx, requestID)
			if err != nil {
				return err
			}
			if request.BillingStatus == model.AIBillingSettled {
				return nil
			}
			if request.BillingStatus != model.AIBillingHeld && request.BillingStatus != model.AIBillingSettlementPending {
				return ErrImageBillingState
			}
			var deliverableCount int64
			if err := tx.Model(&model.AIImageAsset{}).
				Where("request_id = ? AND asset_role = ? AND is_billable_output = 1 AND lifecycle_state = ? AND moderation_status = ? AND explicit_label_status = ? AND implicit_label_status = ?",
					requestID, model.AIImageAssetPrimaryOutput, model.AIImageAssetTemporary, model.AIModerationPassed, model.AIImageLabelApplied, model.AIImageLabelApplied).
				Count(&deliverableCount).Error; err != nil {
				return err
			}
			if deliverableCount <= 0 {
				return ErrImagePendingReconcile
			}
			settlement, err := s.pricing.CalculateImageFinalWithProviderCount(requestID, request.PriceSnapshotJSON, uint64(deliverableCount), providerResultCount)
			if err != nil {
				return err
			}
			if err := createImageUsageItemsTx(tx, []model.AIUsageItem{settlement.UsageFact, settlement.SaleLine, settlement.CostLine}); err != nil {
				return err
			}
			walletResult, err := s.holds.SettleHoldTx(tx, link.WalletHoldID, settlement.SettledAmount, requestID+":settle")
			if err != nil {
				return err
			}
			updates := map[string]interface{}{
				"settled_amount": settlement.SettledAmount, "settle_transaction_id": walletResult.SettleTransaction,
				"release_transaction_id": walletResult.ReleaseTransaction,
			}
			if err := tx.Model(&model.AIRequestWalletLink{}).Where("id = ?", link.ID).Updates(updates).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.AIImageAsset{}).
				Where("request_id = ? AND lifecycle_state = ? AND moderation_status = ? AND explicit_label_status = ? AND implicit_label_status = ?",
					requestID, model.AIImageAssetTemporary, model.AIModerationPassed, model.AIImageLabelApplied, model.AIImageLabelApplied).
				Updates(map[string]interface{}{"lifecycle_state": model.AIImageAssetAvailable, "version_no": gorm.Expr("version_no + 1")}).Error; err != nil {
				return err
			}
			now := s.now().UTC()
			if err := tx.Model(&model.AIRequest{}).Where("id = ?", request.ID).Updates(map[string]interface{}{
				"moderation_status": model.AIModerationPassed, "execution_status": model.AIExecutionSucceeded, "billing_status": model.AIBillingSettled,
				"delivery_status": model.AIDeliveryAvailable, "settled_amount": settlement.SettledAmount, "completed_at": now,
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.AIImageTask{}).Where("request_id = ?", requestID).Updates(map[string]interface{}{
				"status": model.AIImageTaskSucceeded, "progress": 100, "completed_at": now, "version_no": gorm.Expr("version_no + 1"),
			}).Error; err != nil {
				return err
			}
			if err := createImageOutboxTx(tx, requestID, "image_billing_settled", model.AIBillingSettled, settlement.SettledAmount, now); err != nil {
				return err
			}
			return createImageOutboxTx(tx, requestID, "image_delivery_available", model.AIDeliveryAvailable, settlement.SettledAmount, now)
		})
	})
}

func (s *ImageBillingService) finalizeRelease(ctx context.Context, requestID string, providerResultCount uint64, errorClass string) error {
	return retryImageBillingTransaction(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			request, link, err := lockImageRequestAndLink(tx, requestID)
			if err != nil {
				return err
			}
			if request.BillingStatus == model.AIBillingReleased {
				return nil
			}
			if request.BillingStatus != model.AIBillingHeld && request.BillingStatus != model.AIBillingSettlementPending {
				return ErrImageBillingState
			}
			settlement, err := s.pricing.CalculateImageFinalWithProviderCount(requestID, request.PriceSnapshotJSON, 0, providerResultCount)
			if err != nil {
				return err
			}
			if err := createImageUsageItemsTx(tx, []model.AIUsageItem{settlement.UsageFact, settlement.SaleLine, settlement.CostLine}); err != nil {
				return err
			}
			walletResult, err := s.holds.ReleaseHoldTx(tx, link.WalletHoldID, requestID+":release")
			if err != nil {
				return err
			}
			zero := decimal.Zero
			moderationStatus := model.AIModerationPassed
			if errorClass == "content_policy_violation" || errorClass == "no_deliverable_image" {
				moderationStatus = model.AIModerationRejected
			}
			if errorClass == "moderation_unavailable" {
				moderationStatus = model.AIModerationError
			}
			if err := tx.Model(&model.AIRequestWalletLink{}).Where("id = ?", link.ID).Updates(map[string]interface{}{
				"settled_amount": zero, "release_transaction_id": walletResult.ReleaseTransaction,
			}).Error; err != nil {
				return err
			}
			now := s.now().UTC()
			if err := tx.Model(&model.AIRequest{}).Where("id = ?", request.ID).Updates(map[string]interface{}{
				"moderation_status": moderationStatus, "execution_status": model.AIExecutionFailed, "billing_status": model.AIBillingReleased,
				"delivery_status": model.AIDeliveryRejected, "settled_amount": zero, "error_class": errorClass, "completed_at": now,
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.AIImageTask{}).Where("request_id = ?", requestID).Updates(map[string]interface{}{
				"status": model.AIImageTaskFailed, "progress": 100, "error_code": errorClass,
				"completed_at": now, "version_no": gorm.Expr("version_no + 1"),
			}).Error; err != nil {
				return err
			}
			return createImageOutboxTx(tx, requestID, "image_billing_released", model.AIBillingReleased, zero, now)
		})
	})
}

func (s *ImageBillingService) markPendingReconcile(ctx context.Context, requestID string, result imagegateway.GatewayResult, recoveryAction, errorClass string) error {
	if errorClass == "" {
		errorClass = "result_unknown"
	}
	if recoveryAction != imageRecoveryUnknown && recoveryAction != imageRecoverySettle && recoveryAction != imageRecoveryRelease {
		return ErrImageBillingState
	}
	if s.beforeMarkPending != nil {
		if err := s.beforeMarkPending(); err != nil {
			return err
		}
	}
	recoveryJSON, err := json.Marshal(imageRecoveryFacts{
		ProviderResultCount: result.ProviderResultCount,
		ProviderCostUSD:     result.ProviderCostUSD,
		DeliverableCount:    result.DeliverableCount,
		RecoveryAction:      recoveryAction,
		FinalErrorClass:     result.ErrorClass,
	})
	if err != nil {
		return err
	}
	now := s.now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var request model.AIRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ?", requestID).First(&request).Error; err != nil {
			return err
		}
		if request.BillingStatus == model.AIBillingSettled || request.BillingStatus == model.AIBillingReleased {
			return nil
		}
		executionStatus, deliveryStatus := model.AIExecutionUnknown, model.AIDeliveryPending
		if recoveryAction == imageRecoverySettle {
			executionStatus = model.AIExecutionSucceeded
		}
		if recoveryAction == imageRecoveryRelease {
			executionStatus, deliveryStatus = model.AIExecutionFailed, model.AIDeliveryRejected
		}
		updates := map[string]interface{}{
			"execution_status": executionStatus, "billing_status": model.AIBillingSettlementPending,
			"delivery_status": deliveryStatus, "error_class": errorClass,
		}
		if errorClass == "moderation_unavailable" {
			updates["moderation_status"] = model.AIModerationError
		}
		if err := tx.Model(&model.AIRequest{}).Where("id = ?", request.ID).Updates(updates).Error; err != nil {
			return err
		}
		taskUpdate := tx.Model(&model.AIImageTask{}).Where("request_id = ?", requestID).Updates(map[string]interface{}{
			"status": model.AIImageTaskPendingReconcile, "error_code": errorClass,
			"result_json": recoveryJSON, "completed_at": nil, "version_no": gorm.Expr("version_no + 1"),
		})
		if taskUpdate.Error != nil {
			return taskUpdate.Error
		}
		if taskUpdate.RowsAffected != 1 {
			return ErrImageBillingState
		}
		if err := s.compensations.CreateTx(tx, requestID, errorClass, now); err != nil {
			return err
		}
		return createImageOutboxTx(tx, requestID, "image_settlement_pending", model.AIBillingSettlementPending, decimal.Zero, now)
	})
}

// ReconcilePending 只使用已持久化任务和资产事实重试settle/release，不再次调用Provider。
func (s *ImageBillingService) ReconcilePending(ctx context.Context, requestID string) error {
	var request model.AIRequest
	if err := s.db.WithContext(ctx).Where("request_id = ?", requestID).First(&request).Error; err != nil {
		return err
	}
	if request.BillingStatus == model.AIBillingSettled || request.BillingStatus == model.AIBillingReleased {
		return nil
	}
	if request.BillingStatus != model.AIBillingSettlementPending {
		return ErrImageBillingState
	}
	var task model.AIImageTask
	if err := s.db.WithContext(ctx).Where("request_id = ?", requestID).First(&task).Error; err != nil {
		return err
	}
	var facts imageRecoveryFacts
	if err := json.Unmarshal(task.ResultJSON, &facts); err != nil {
		return ErrImagePendingReconcile
	}
	switch facts.RecoveryAction {
	case imageRecoverySettle:
		if facts.DeliverableCount == 0 {
			return ErrImagePendingReconcile
		}
		return s.finalizeSuccess(ctx, requestID, facts.ProviderResultCount)
	case imageRecoveryRelease:
		if facts.FinalErrorClass == "" {
			return ErrImagePendingReconcile
		}
		return s.finalizeRelease(ctx, requestID, facts.ProviderResultCount, facts.FinalErrorClass)
	}
	return ErrImagePendingReconcile
}

// RecoverStaleActiveExecutions 把越过安全窗且仍为running/held的图片执行原子转为结果未知。
// 该恢复只依据数据库低敏事实，不调用Provider、不猜测结算或释放，也不改变Hold金额。
func (s *ImageBillingService) RecoverStaleActiveExecutions(ctx context.Context, staleBefore time.Time, limit int) (int, error) {
	if s == nil || s.db == nil || staleBefore.IsZero() {
		return 0, ErrImageBillingState
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	activeStatuses := []string{model.AIImageTaskProcessing, model.AIImageTaskStoring, model.AIImageTaskModerating}
	var requestIDs []string
	if err := s.db.WithContext(ctx).Table("ai_gateway_tasks AS task").
		Select("task.request_id").
		Joins("JOIN ai_requests AS request ON request.request_id = task.request_id").
		Where("task.status IN ? AND task.updated_at <= ? AND request.updated_at <= ?", activeStatuses, staleBefore, staleBefore).
		Where("request.modality = ? AND request.execution_status = ? AND request.billing_status = ?", "image", model.AIExecutionRunning, model.AIBillingHeld).
		Order("task.id ASC").Limit(limit).Scan(&requestIDs).Error; err != nil {
		return 0, err
	}
	recovered := 0
	var recoveryErr error
	for _, requestID := range requestIDs {
		changed := false
		err := retryImageBillingTransaction(ctx, func() error {
			return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				var request model.AIRequest
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ?", requestID).First(&request).Error; err != nil {
					return err
				}
				if request.ExecutionStatus != model.AIExecutionRunning || request.BillingStatus != model.AIBillingHeld || request.UpdatedAt.After(staleBefore) {
					return nil
				}
				var task model.AIImageTask
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ?", requestID).First(&task).Error; err != nil {
					return err
				}
				if !containsImageActiveTaskStatus(task.Status) || task.UpdatedAt.After(staleBefore) {
					return nil
				}
				facts := imageRecoveryFacts{RecoveryAction: imageRecoveryUnknown, FinalErrorClass: "result_unknown"}
				_ = json.Unmarshal(task.ResultJSON, &facts)
				facts.RecoveryAction = imageRecoveryUnknown
				facts.FinalErrorClass = "result_unknown"
				recoveryJSON, err := json.Marshal(facts)
				if err != nil {
					return err
				}
				now := s.now().UTC()
				requestUpdate := tx.Model(&model.AIRequest{}).
					Where("id = ? AND execution_status = ? AND billing_status = ? AND updated_at = ?", request.ID, model.AIExecutionRunning, model.AIBillingHeld, request.UpdatedAt).
					Updates(map[string]interface{}{
						"execution_status": model.AIExecutionUnknown, "billing_status": model.AIBillingSettlementPending,
						"delivery_status": model.AIDeliveryPending, "error_class": "result_unknown", "updated_at": now,
					})
				if requestUpdate.Error != nil {
					return requestUpdate.Error
				}
				if requestUpdate.RowsAffected != 1 {
					return ErrImageExecutionStarted
				}
				taskUpdate := tx.Model(&model.AIImageTask{}).
					Where("id = ? AND status = ? AND version_no = ? AND updated_at = ?", task.ID, task.Status, task.VersionNo, task.UpdatedAt).
					Updates(map[string]interface{}{
						"status": model.AIImageTaskPendingReconcile, "error_code": "result_unknown", "result_json": recoveryJSON,
						"completed_at": nil, "version_no": gorm.Expr("version_no + 1"), "updated_at": now,
					})
				if taskUpdate.Error != nil {
					return taskUpdate.Error
				}
				if taskUpdate.RowsAffected != 1 {
					return ErrImageExecutionStarted
				}
				if err := s.compensations.CreateTx(tx, requestID, "result_unknown", now); err != nil {
					return err
				}
				if err := createImageOutboxTx(tx, requestID, "image_settlement_pending", model.AIBillingSettlementPending, decimal.Zero, now); err != nil {
					return err
				}
				changed = true
				return nil
			})
		})
		if err != nil {
			recoveryErr = errors.Join(recoveryErr, err)
			continue
		}
		if changed {
			recovered++
		}
	}
	return recovered, recoveryErr
}

func containsImageActiveTaskStatus(status string) bool {
	return status == model.AIImageTaskProcessing || status == model.AIImageTaskStoring || status == model.AIImageTaskModerating
}

// ReconcilePendingAndCompleteCompensation 供人工核对按租约执行本地结算，并在财务终态后关闭对应补偿任务。
func (s *ImageBillingService) ReconcilePendingAndCompleteCompensation(ctx context.Context, requestID string) error {
	if s == nil || s.compensations == nil || requestID == "" {
		return ErrImageBillingState
	}
	now := s.now().UTC().Truncate(time.Second)
	var claim *repository.ImageCompensationRequestClaim
	claimErr := runImagePostProviderAction(ctx, func(localCtx context.Context) error {
		var err error
		claim, err = s.compensations.ClaimRequest(localCtx, requestID, now, now.Add(-2*time.Minute))
		return err
	})
	if claimErr != nil && !errors.Is(claimErr, gorm.ErrRecordNotFound) {
		return claimErr
	}
	// 没有补偿任务或任务已经完成时仍允许幂等核对财务状态，但不会制造新的补偿事实。
	if errors.Is(claimErr, gorm.ErrRecordNotFound) || claim == nil || claim.Completed {
		return runImagePostProviderAction(ctx, func(localCtx context.Context) error {
			return s.ReconcilePending(localCtx, requestID)
		})
	}
	restoreClaim := func() error {
		return runImagePostProviderAction(ctx, func(localCtx context.Context) error {
			return s.compensations.RestoreRequestClaim(localCtx, claim)
		})
	}
	if err := runImagePostProviderAction(ctx, func(localCtx context.Context) error {
		return s.ReconcilePending(localCtx, requestID)
	}); err != nil {
		if restoreErr := restoreClaim(); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return err
	}
	var billingStatus string
	if err := runImagePostProviderAction(ctx, func(localCtx context.Context) error {
		return s.db.WithContext(localCtx).Model(&model.AIRequest{}).
			Select("billing_status").Where("request_id = ?", requestID).Scan(&billingStatus).Error
	}); err != nil {
		if restoreErr := restoreClaim(); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return err
	}
	if billingStatus != model.AIBillingSettled && billingStatus != model.AIBillingReleased {
		if restoreErr := restoreClaim(); restoreErr != nil {
			return errors.Join(ErrImagePendingReconcile, restoreErr)
		}
		return ErrImagePendingReconcile
	}
	if err := runImagePostProviderAction(ctx, func(localCtx context.Context) error {
		return s.compensations.MarkCompleted(localCtx, claim.TaskID, claim.Lease, s.now().UTC())
	}); err != nil {
		if restoreErr := restoreClaim(); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return err
	}
	return nil
}

func (s *ImageBillingService) ReconcileRequest(ctx context.Context, requestID string) (ImageReconciliationReport, error) {
	report := ImageReconciliationReport{RequestID: requestID}
	var request model.AIRequest
	if err := s.db.WithContext(ctx).Where("request_id = ?", requestID).First(&request).Error; err != nil {
		return report, err
	}
	report.BillingStatus = request.BillingStatus
	settled := decimal.Zero
	if request.SettledAmount != nil {
		settled = *request.SettledAmount
	}
	var saleTotal decimal.Decimal
	if err := s.db.WithContext(ctx).Raw("SELECT COALESCE(SUM(amount),0) FROM ai_usage_items WHERE request_id = ? AND record_kind = 'sale_line'", requestID).Scan(&saleTotal).Error; err != nil {
		return report, err
	}
	report.RequestUsageDiff = settled.Sub(saleTotal)
	var link model.AIRequestWalletLink
	if err := s.db.WithContext(ctx).Where("request_id = ?", requestID).First(&link).Error; err != nil {
		return report, err
	}
	linkSettled := decimal.Zero
	if link.SettledAmount != nil {
		linkSettled = *link.SettledAmount
	}
	report.RequestHoldDiff = settled.Sub(linkSettled)
	walletAmount := decimal.Zero
	if link.SettleTransactionID != nil {
		var transaction billingmodel.WalletTransaction
		if err := s.db.WithContext(ctx).First(&transaction, *link.SettleTransactionID).Error; err != nil {
			return report, err
		}
		walletAmount = transaction.Amount
	}
	report.RequestWalletDiff = settled.Sub(walletAmount)
	var usageCount decimal.Decimal
	if err := s.db.WithContext(ctx).Raw("SELECT COALESCE(SUM(quantity),0) FROM ai_usage_items WHERE request_id = ? AND record_kind = 'usage_fact' AND meter_type = 'image_count'", requestID).Scan(&usageCount).Error; err != nil {
		return report, err
	}
	var saleCount decimal.Decimal
	if err := s.db.WithContext(ctx).Raw("SELECT COALESCE(SUM(quantity),0) FROM ai_usage_items WHERE request_id = ? AND record_kind = 'sale_line' AND meter_type = 'image_count'", requestID).Scan(&saleCount).Error; err != nil {
		return report, err
	}
	report.SaleUsageCountDiff = usageCount.Sub(saleCount)
	var assetCount int64
	if err := s.db.WithContext(ctx).Model(&model.AIImageAsset{}).
		Where("request_id = ? AND asset_role = ? AND is_billable_output = 1 AND lifecycle_state = ?", requestID, model.AIImageAssetPrimaryOutput, model.AIImageAssetAvailable).
		Count(&assetCount).Error; err != nil {
		return report, err
	}
	report.UsageAssetCountDiff = usageCount.Sub(decimal.NewFromInt(assetCount))
	var costLine model.AIUsageItem
	costQuery := s.db.WithContext(ctx).Where("request_id = ? AND record_kind = 'cost_line'", requestID)
	if err := costQuery.Model(&model.AIUsageItem{}).Count(&report.CostLineCount).Error; err != nil {
		return report, err
	}
	if report.CostLineCount == 1 {
		if err := costQuery.First(&costLine).Error; err != nil {
			return report, err
		}
		var task model.AIImageTask
		if err := s.db.WithContext(ctx).Where("request_id = ?", requestID).First(&task).Error; err != nil {
			return report, err
		}
		var summary struct {
			ProviderResultCount uint64 `json:"provider_result_count"`
		}
		_ = json.Unmarshal(task.ResultJSON, &summary)
		expectedCostLine, expectedCost, costErr := s.pricing.CalculateImageProviderCost(requestID, request.PriceSnapshotJSON, summary.ProviderResultCount)
		if costErr != nil || costLine.Amount == nil {
			report.CostQuantityDiff = decimal.NewFromInt(1)
			report.CostAmountDiff = decimal.NewFromInt(1)
		} else {
			report.CostQuantityDiff = expectedCostLine.Quantity.Sub(costLine.Quantity)
			report.CostAmountDiff = expectedCost.Sub(*costLine.Amount)
		}
	}
	report.WalletFactMismatches = reconcileImageWalletFacts(s.db.WithContext(ctx), request, &link, settled)
	if err := s.db.WithContext(ctx).Model(&model.AIUsageItem{}).Where("request_id = ? AND record_kind = 'adjustment'", requestID).Count(&report.AdjustmentCount).Error; err != nil {
		return report, err
	}
	if err := s.db.WithContext(ctx).Model(&model.AICompensationTask{}).
		Where("task_key = ? AND status IN ('pending','running','retry','dead','manual_review')", "image:"+requestID).
		Count(&report.ActiveCompensations).Error; err != nil {
		return report, err
	}
	requiredEvents := []string{"image_billing_held"}
	if request.BillingStatus == model.AIBillingSettled {
		requiredEvents = append(requiredEvents, "image_billing_settled", "image_delivery_available")
	} else if request.BillingStatus == model.AIBillingReleased {
		requiredEvents = append(requiredEvents, "image_billing_released")
	}
	for _, eventType := range requiredEvents {
		var count int64
		query := s.db.WithContext(ctx).Model(&model.AIOutboxEvent{}).
			Where("event_id = ? AND aggregate_id = ? AND event_type = ?", imageEventID(requestID, eventType), requestID, eventType).
			Count(&count)
		if query.Error != nil {
			return report, query.Error
		}
		if count != 1 {
			report.MissingOutboxEvents++
			continue
		}
		var event model.AIOutboxEvent
		if err := s.db.WithContext(ctx).Where("event_id = ?", imageEventID(requestID, eventType)).First(&event).Error; err != nil {
			return report, err
		}
		expectedStatus, expectedAmount := imageOutboxExpectation(eventType, &link, settled)
		if !validImageOutboxPayload(event.PayloadJSON, requestID, expectedStatus, expectedAmount) {
			report.OutboxFactMismatches++
		}
	}
	if !report.ZeroDifference() {
		return report, ErrImageReconcileMismatch
	}
	return report, nil
}

func reconcileImageWalletFacts(db *gorm.DB, request model.AIRequest, link *model.AIRequestWalletLink, settled decimal.Decimal) int64 {
	if db == nil || link == nil {
		return 1
	}
	mismatches := int64(0)
	var hold billingmodel.WalletHold
	if err := db.First(&hold, link.WalletHoldID).Error; err != nil {
		return 1
	}
	holdSettled := decimal.Zero
	if hold.SettledAmount != nil {
		holdSettled = *hold.SettledAmount
	}
	if hold.UserID != request.UserID || hold.WalletID != link.WalletID || !hold.HoldAmount.Equal(link.HeldAmount) || !holdSettled.Equal(settled) {
		mismatches++
	}
	expectedHoldStatus := billingmodel.HoldStatusSettled
	if request.BillingStatus == model.AIBillingReleased {
		expectedHoldStatus = billingmodel.HoldStatusReleased
	}
	if hold.Status != expectedHoldStatus {
		mismatches++
	}
	if !validImageWalletTransaction(db, link.HoldTransactionID, link.WalletID, request.UserID, "freeze", "out", link.HeldAmount) {
		mismatches++
	}
	if link.ReleaseTransactionID == nil || !validImageWalletTransaction(db, *link.ReleaseTransactionID, link.WalletID, request.UserID, "unfreeze", "in", link.HeldAmount) {
		mismatches++
	}
	if settled.IsZero() {
		if link.SettleTransactionID != nil || hold.SettleTxnID != nil {
			mismatches++
		}
	} else if link.SettleTransactionID == nil || hold.SettleTxnID == nil || *link.SettleTransactionID != *hold.SettleTxnID ||
		!validImageWalletTransaction(db, *link.SettleTransactionID, link.WalletID, request.UserID, "consume", "out", settled) {
		mismatches++
	}
	return mismatches
}

func validImageWalletTransaction(db *gorm.DB, id, walletID, userID uint64, transactionType, direction string, amount decimal.Decimal) bool {
	var transaction billingmodel.WalletTransaction
	if err := db.First(&transaction, id).Error; err != nil {
		return false
	}
	return transaction.WalletID == walletID && transaction.UserID == userID && transaction.Type == transactionType &&
		transaction.Direction == direction && transaction.Amount.Equal(amount)
}

func imageOutboxExpectation(eventType string, link *model.AIRequestWalletLink, settled decimal.Decimal) (string, decimal.Decimal) {
	switch eventType {
	case "image_billing_held":
		return model.AIBillingHeld, link.HeldAmount
	case "image_billing_settled":
		return model.AIBillingSettled, settled
	case "image_delivery_available":
		return model.AIDeliveryAvailable, settled
	default:
		return model.AIBillingReleased, decimal.Zero
	}
}

func validImageOutboxPayload(raw json.RawMessage, requestID, status string, amount decimal.Decimal) bool {
	var payload map[string]string
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload) != 4 {
		return false
	}
	return payload["request_id"] == requestID && payload["status"] == status && payload["amount"] == amount.StringFixed(8) && payload["currency"] == "CNY"
}

// AppendAdjustment 只追加maker/checker调账事实，不直接修改钱包；对账会保持非零，直到独立资金动作完整闭环。
func (s *ImageBillingService) AppendAdjustment(ctx context.Context, requestID, direction, reason string, amount decimal.Decimal, operatorID, reviewerID uint64, sequenceNo uint32) error {
	if s == nil || requestID == "" || (direction != "debit" && direction != "credit") || strings.TrimSpace(reason) == "" ||
		amount.LessThanOrEqual(decimal.Zero) || operatorID == 0 || reviewerID == 0 || operatorID == reviewerID {
		return ErrImageAdjustmentInvalid
	}
	var request model.AIRequest
	if err := s.db.WithContext(ctx).Where("request_id = ? AND billing_status IN (?,?)", requestID, model.AIBillingSettled, model.AIBillingReleased).First(&request).Error; err != nil {
		return err
	}
	decoded, err := DecodePriceSnapshot(request.PriceSnapshotJSON)
	if err != nil || decoded.MetricV2 == nil || len(decoded.MetricV2.SelectedLines) != 1 {
		return ErrImageAdjustmentInvalid
	}
	line := decoded.MetricV2.SelectedLines[0]
	unitSize, err := decimal.NewFromString(line.UnitSize)
	if err != nil {
		return err
	}
	currency := "CNY"
	priceVersionID := decoded.MetricV2.PriceVersionID
	reason = strings.TrimSpace(reason)
	item := model.AIUsageItem{
		RequestID: requestID, MeterType: line.MeterType, Source: "reconciled", RecordKind: model.AIUsageAdjustment,
		PriceVersionID: &priceVersionID, VariantHash: line.VariantHash, VariantJSON: append(json.RawMessage(nil), line.VariantJSON...),
		SequenceNo: sequenceNo, Quantity: decimal.Zero, UsageUnit: line.UsageUnit, UnitSize: unitSize, Amount: &amount, Currency: &currency,
		AdjustmentDirection: &direction, AdjustmentReason: &reason, AdjustmentOperatorID: &operatorID, AdjustmentReviewedBy: &reviewerID,
	}
	return s.db.WithContext(ctx).Create(&item).Error
}

func createImageUsageItemsTx(tx *gorm.DB, items []model.AIUsageItem) error {
	for index := range items {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&items[index]).Error; err != nil {
			return err
		}
	}
	return nil
}

func createImageOutboxTx(tx *gorm.DB, requestID, eventType, status string, amount decimal.Decimal, now time.Time) error {
	payload, err := json.Marshal(map[string]string{"request_id": requestID, "status": status, "amount": amount.StringFixed(8), "currency": "CNY"})
	if err != nil {
		return err
	}
	event := model.AIOutboxEvent{
		EventID: imageEventID(requestID, eventType), AggregateType: "image_request", AggregateID: requestID,
		EventType: eventType, PayloadJSON: payload, Status: model.AIOutboxPending, NextRetryAt: now,
	}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_id"}}, DoNothing: true}).Create(&event).Error
}

func lockOwnedImageRequest(tx *gorm.DB, requestID string, owner repository.ImageOwner) (*model.AIRequest, error) {
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ? AND user_id = ? AND project_id = ?", requestID, owner.UserID, owner.ProjectID)
	if owner.APIKeyID != nil {
		query = query.Where("api_key_id = ?", *owner.APIKeyID)
	}
	var request model.AIRequest
	if err := query.First(&request).Error; err != nil {
		return nil, err
	}
	return &request, nil
}

func lockImageRequestAndLink(tx *gorm.DB, requestID string) (*model.AIRequest, *model.AIRequestWalletLink, error) {
	var request model.AIRequest
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ?", requestID).First(&request).Error; err != nil {
		return nil, nil, err
	}
	var link model.AIRequestWalletLink
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ?", requestID).First(&link).Error; err != nil {
		return nil, nil, err
	}
	return &request, &link, nil
}

func imageEventID(requestID, eventType string) string {
	sum := sha256.Sum256([]byte(requestID + ":" + eventType))
	return "img_evt_" + hex.EncodeToString(sum[:16])
}

func imageAssetPublicID(requestID string, resultIndex uint64, role string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", requestID, resultIndex, role)))
	return "img_asset_" + hex.EncodeToString(sum[:16])
}

func retentionPolicy(lifecycle string) string {
	if lifecycle == model.AIImageAssetQuarantined {
		return "quarantine-30d"
	}
	return "result-30d"
}

func dereferenceUint64(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}

// retryImageBillingTransaction 只重试数据库死锁、锁等待和乐观锁冲突；事务整体回滚后再执行，不会触发Provider调用。
func retryImageBillingTransaction(ctx context.Context, operation func() error) error {
	const maxAttempts = 20
	for attempt := 0; attempt < maxAttempts; attempt++ {
		err := operation()
		if err == nil || !isRetryableImageBillingError(err) || attempt == maxAttempts-1 {
			return err
		}
		// 线性有界退避避免同一钱包上的并发请求持续同步碰撞，总等待上限约2.1秒。
		delay := time.Duration(attempt+1) * 10 * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ErrImageBillingState
}

func isRetryableImageBillingError(err error) bool {
	if errors.Is(err, billingservice.ErrConcurrentUpdate) {
		return true
	}
	var mysqlErr *drivermysql.MySQLError
	return errors.As(err, &mysqlErr) && (mysqlErr.Number == 1213 || mysqlErr.Number == 1205)
}
