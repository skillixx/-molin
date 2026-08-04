package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	billingmodel "molin/server/internal/modules/billing/model"
	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var (
	ErrWalletHoldFailed  = errors.New("钱包预占失败")
	ErrSettlementPending = errors.New("账单结算中")
	ErrBillingException  = errors.New("计费异常")
	ErrOutboxPending     = errors.New("事件等待发布")
)

const manualReconcileDeadline = 24 * time.Hour

const (
	ManualResolutionRelease = "release"
	ManualResolutionSettle  = "settle"
)

// BillingStatusError 表示财务状态已可靠提交，但调用方需要按明确状态响应客户端。
type BillingStatusError struct {
	RequestID string
	Cause     error
}

func (e *BillingStatusError) Error() string { return e.Cause.Error() }
func (e *BillingStatusError) Unwrap() error { return e.Cause }

type BillingPreparation struct {
	BillingStatus string
	QuotedAmount  decimal.Decimal
	HeldAmount    decimal.Decimal
	PriceVersion  uint64
	MaxTokens     uint64
}

type priceVersionSuspender interface {
	SuspendVersionTx(tx *gorm.DB, versionID uint64, reason string) error
}

// AIBillingService 是 G3 唯一财务入口，所有同步请求和补偿 Worker 都复用这些事务方法。
type AIBillingService struct {
	db          *gorm.DB
	pricing     *PricingService
	priceRepo   priceVersionSuspender
	walletHolds *billingservice.WalletHoldService
	now         func() time.Time
}

func NewAIBillingService(db *gorm.DB, pricing *PricingService, priceRepo priceVersionSuspender, walletHolds *billingservice.WalletHoldService) *AIBillingService {
	return &AIBillingService{db: db, pricing: pricing, priceRepo: priceRepo, walletHolds: walletHolds, now: time.Now}
}

// QuoteRequest 在预算与资源治理前生成确定性价格快照；后续钱包 hold 必须复用同一快照。
func (s *AIBillingService) QuoteRequest(ctx context.Context, modelCode string, body map[string]interface{}) (*PriceQuote, error) {
	if s == nil || s.pricing == nil {
		return nil, ErrPriceUnavailable
	}
	return s.pricing.Quote(ctx, modelCode, body)
}

// PrepareRequest 先完成确定报价，再在一个事务中创建请求、冻结钱包、写财务关联和 Outbox。
func (s *AIBillingService) PrepareRequest(ctx context.Context, request *model.AIRequest, body map[string]interface{}) (*BillingPreparation, error) {
	if s == nil || s.db == nil || s.pricing == nil || s.walletHolds == nil {
		return nil, ErrPriceUnavailable
	}
	quote, err := s.pricing.Quote(ctx, request.LogicalModelCode, body)
	if err != nil {
		return nil, err
	}
	return s.PrepareQuotedRequest(ctx, request, quote)
}

// PrepareQuotedRequest 使用治理阶段已校验的不可变价格快照完成钱包预占，避免预算与钱包采用不同价格版本。
func (s *AIBillingService) PrepareQuotedRequest(ctx context.Context, request *model.AIRequest, quote *PriceQuote) (*BillingPreparation, error) {
	if s == nil || s.db == nil || s.walletHolds == nil || quote == nil {
		return nil, ErrPriceUnavailable
	}
	err := retryFinancialTransaction(ctx, func() error {
		// 回滚后 GORM 仍可能保留已分配的自增 ID，重试前必须清空，避免误用未提交主键。
		request.ID = 0
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return s.prepareQuotedRequestTx(tx, request, quote) })
	})
	if err != nil {
		return nil, err
	}
	return &BillingPreparation{
		BillingStatus: model.AIBillingHeld, QuotedAmount: quote.QuotedAmount,
		HeldAmount: quote.HeldAmount, PriceVersion: quote.Snapshot.PriceVersionID, MaxTokens: quote.MaxTokens,
	}, nil
}

// PrepareRetryRequest 只允许明确未写出上游的失败请求复用幂等键；旧事实与新请求在同一事务中切换归属。
func (s *AIBillingService) PrepareRetryRequest(ctx context.Context, previousRequestID string, request *model.AIRequest, body map[string]interface{}) (*BillingPreparation, error) {
	if s == nil || s.db == nil || s.pricing == nil || s.walletHolds == nil || request.IdempotencyKey == nil {
		return nil, ErrBillingException
	}
	quote, err := s.pricing.Quote(ctx, request.LogicalModelCode, body)
	if err != nil {
		return nil, err
	}
	return s.PrepareRetryQuotedRequest(ctx, previousRequestID, request, quote)
}

// PrepareRetryQuotedRequest 复用治理阶段的价格快照执行明确未送达请求的安全重试。
func (s *AIBillingService) PrepareRetryQuotedRequest(ctx context.Context, previousRequestID string, request *model.AIRequest, quote *PriceQuote) (*BillingPreparation, error) {
	if s == nil || s.db == nil || s.walletHolds == nil || request.IdempotencyKey == nil || quote == nil {
		return nil, ErrBillingException
	}
	err := retryFinancialTransaction(ctx, func() error {
		request.ID = 0
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var previous model.AIRequest
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ?", previousRequestID).First(&previous).Error; err != nil {
				return err
			}
			if previous.IdempotencyKey == nil || *previous.IdempotencyKey != *request.IdempotencyKey ||
				previous.UserID != request.UserID || previous.BillingStatus != model.AIBillingReleased ||
				previous.ExecutionStatus != model.AIExecutionFailed || pointerValue(previous.ErrorCode) != "request_not_sent" ||
				previous.RequestFingerprint == nil || request.RequestFingerprint == nil || *previous.RequestFingerprint != *request.RequestFingerprint ||
				previous.ProjectID == nil || request.ProjectID == nil || *previous.ProjectID != *request.ProjectID ||
				previous.APIKeyID == nil || request.APIKeyID == nil || *previous.APIKeyID != *request.APIKeyID {
				return repository.ErrRequestStateConflict
			}
			if err := tx.Model(&model.AIRequest{}).Where("id = ? AND idempotency_key = ?", previous.ID, *request.IdempotencyKey).
				Update("idempotency_key", nil).Error; err != nil {
				return err
			}
			return s.prepareQuotedRequestTx(tx, request, quote)
		})
	})
	if err != nil {
		return nil, err
	}
	return &BillingPreparation{BillingStatus: model.AIBillingHeld, QuotedAmount: quote.QuotedAmount, HeldAmount: quote.HeldAmount, PriceVersion: quote.Snapshot.PriceVersionID, MaxTokens: quote.MaxTokens}, nil
}

func (s *AIBillingService) prepareQuotedRequestTx(tx *gorm.DB, request *model.AIRequest, quote *PriceQuote) error {
	request.BillingStatus = model.AIBillingHeld
	request.PriceSnapshotJSON = quote.SnapshotJSON
	request.QuotedAmount = decimalPointer(quote.QuotedAmount)
	request.HeldAmount = decimalPointer(quote.HeldAmount)
	if err := tx.Create(request).Error; err != nil {
		return err
	}
	hold, err := s.walletHolds.CreateHoldTx(tx, request.UserID, quote.HeldAmount, request.RequestID+":ai-hold", "AI 请求资金预占")
	if err != nil {
		if errors.Is(err, billingservice.ErrInsufficientBalance) {
			return ErrWalletInsufficient
		}
		if isRetryableMySQLTransactionError(err) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrWalletHoldFailed, err)
	}
	link := &model.AIRequestWalletLink{RequestID: request.RequestID, WalletID: hold.WalletID, WalletHoldID: hold.HoldID,
		HoldTransactionID: hold.FreezeTransaction, QuotedAmount: quote.QuotedAmount, HeldAmount: quote.HeldAmount}
	if err := tx.Create(link).Error; err != nil {
		return err
	}
	return createOutboxTx(tx, request.RequestID, "billing_held", model.AIBillingHeld, quote.HeldAmount, s.now())
}

// FinalizeRequest 在固定锁顺序“请求 -> hold -> 钱包”下形成唯一执行和财务终态。
func (s *AIBillingService) FinalizeRequest(ctx context.Context, requestID string, result ExecutionResult) error {
	if s == nil || s.db == nil || s.walletHolds == nil {
		return ErrBillingException
	}
	pendingCommitted := false
	exceptionCommitted := false
	err := retryFinancialTransaction(context.WithoutCancel(ctx), func() error {
		return s.db.WithContext(context.WithoutCancel(ctx)).Transaction(func(tx *gorm.DB) error {
			request, link, err := lockRequestAndLink(tx, requestID)
			if err != nil {
				return err
			}
			if isBillingTerminal(request.BillingStatus) {
				return nil
			}
			if request.BillingStatus != model.AIBillingHeld && request.BillingStatus != model.AIBillingSettlementPending {
				return repository.ErrRequestStateConflict
			}

			ledgerAttempt := result.Attempt.ToLedgerModel(requestID, result.Usage)
			if err := finalizeAttemptTx(tx, ledgerAttempt); err != nil {
				return err
			}
			executionStatus := result.Attempt.RequestExecutionStatus()
			errorClass := optionalString(result.Attempt.ErrorClass)
			errorCode := optionalString(result.ErrorCode)
			completed := s.now()

			// 平台输出审核未交付结果时，保留 Provider Usage 和执行成本事实，但用户销售金额固定为零。
			if result.CustomerChargeWaived {
				if err := createUsageTx(tx, usageModels(requestID, result.Usage)); err != nil {
					return err
				}
				if !result.Usage.Present {
					if err := updateRequestBillingTx(tx, request, executionStatus, model.AIBillingSettlementPending, result.ClientDisconnected, nil, errorClass, errorCode, ledgerAttempt.ExecutionModelCode, completed); err != nil {
						return err
					}
					if err := createOutboxTx(tx, requestID, "billing_reconcile_required", model.AIBillingSettlementPending, *request.HeldAmount, completed); err != nil {
						return err
					}
					pendingCommitted = true
					return nil
				}
				costed, err := s.pricing.CalculateProviderCost(requestID, request.PriceSnapshotJSON, result.Usage)
				if err != nil {
					return err
				}
				if err := createUsageTx(tx, costed.Items); err != nil {
					return err
				}
				released, err := s.walletHolds.ReleaseHoldTx(tx, link.WalletHoldID, requestID+":content-policy-release")
				if err != nil {
					return err
				}
				if err := updateWalletLinkTx(tx, link, released); err != nil {
					return err
				}
				zero := decimal.Zero
				if err := updateRequestBillingTx(tx, request, executionStatus, model.AIBillingReleased, result.ClientDisconnected, &zero, errorClass, errorCode, ledgerAttempt.ExecutionModelCode, completed); err != nil {
					return err
				}
				return createOutboxTx(tx, requestID, "billing_content_policy_waived", model.AIBillingReleased, zero, completed)
			}

			// 结果未知时即使已经看到 Usage 也不能证明它是最终事实，必须保留 hold 等待对账。
			mustWaitForReconcile := result.Attempt.ResultUnknown || executionStatus == model.AIExecutionUnknown ||
				(!result.Usage.Present && executionStatus == model.AIExecutionSucceeded) ||
				(!result.Usage.Present && executionStatus == model.AIExecutionFailed && result.Attempt.ErrorClass != "request_not_sent")
			if mustWaitForReconcile {
				if err := createUsageTx(tx, usageModels(requestID, result.Usage)); err != nil {
					return err
				}
				if err := updateRequestBillingTx(tx, request, executionStatus, model.AIBillingSettlementPending, result.ClientDisconnected, nil, errorClass, errorCode, ledgerAttempt.ExecutionModelCode, completed); err != nil {
					return err
				}
				if err := createOutboxTx(tx, requestID, "billing_reconcile_required", model.AIBillingSettlementPending, *request.HeldAmount, completed); err != nil {
					return err
				}
				pendingCommitted = true
				return nil
			}

			if executionStatus != model.AIExecutionSucceeded && !result.Usage.Present {
				settled, err := s.walletHolds.ReleaseHoldTx(tx, link.WalletHoldID, requestID+":release")
				if err != nil {
					return err
				}
				if err := updateWalletLinkTx(tx, link, settled); err != nil {
					return err
				}
				zero := decimal.Zero
				if err := updateRequestBillingTx(tx, request, executionStatus, model.AIBillingReleased, result.ClientDisconnected, &zero, errorClass, errorCode, ledgerAttempt.ExecutionModelCode, completed); err != nil {
					return err
				}
				return createOutboxTx(tx, requestID, "billing_released", model.AIBillingReleased, zero, completed)
			}

			applyMinimum := executionStatus == model.AIExecutionSucceeded && result.Usage.PromptTokens+result.Usage.CompletionTokens > 0
			billed, err := s.pricing.CalculateFinalWithPolicy(requestID, request.PriceSnapshotJSON, result.Usage, applyMinimum)
			if err != nil {
				return err
			}
			// sequence 0 永久保存上游原始 Usage；sequence 1 的计费拆分不能替代原始事实。
			if err := createUsageTx(tx, usageModels(requestID, result.Usage)); err != nil {
				return err
			}
			if request.HeldAmount == nil || billed.FinalAmount.GreaterThan(*request.HeldAmount) {
				var snapshot PriceSnapshot
				_ = json.Unmarshal(request.PriceSnapshotJSON, &snapshot)
				if snapshot.PriceVersionID != 0 && s.priceRepo != nil {
					if err := s.priceRepo.SuspendVersionTx(tx, snapshot.PriceVersionID, "实际金额超过预占金额"); err != nil {
						return err
					}
				}
				if err := createUsageTx(tx, billed.Items); err != nil {
					return err
				}
				if err := updateRequestBillingTx(tx, request, executionStatus, model.AIBillingException, result.ClientDisconnected, nil, stringPointer("billing_amount_exceeded"), stringPointer("billing_amount_exceeded"), ledgerAttempt.ExecutionModelCode, completed); err != nil {
					return err
				}
				if err := createOutboxTx(tx, requestID, "billing_p0_exception", model.AIBillingException, billed.FinalAmount, completed); err != nil {
					return err
				}
				exceptionCommitted = true
				return nil
			}
			settled, err := s.walletHolds.SettleHoldTx(tx, link.WalletHoldID, billed.FinalAmount, requestID+":settle")
			if err != nil {
				if errors.Is(err, billingservice.ErrActualExceedsHold) {
					return ErrBillingAmountException
				}
				return err
			}
			if settled.Status != billingmodel.HoldStatusSettled {
				return repository.ErrRequestStateConflict
			}
			if err := createUsageTx(tx, billed.Items); err != nil {
				return err
			}
			if err := updateWalletLinkTx(tx, link, settled); err != nil {
				return err
			}
			if err := updateRequestBillingTx(tx, request, executionStatus, model.AIBillingSettled, result.ClientDisconnected, &billed.FinalAmount, errorClass, errorCode, ledgerAttempt.ExecutionModelCode, completed); err != nil {
				return err
			}
			return createOutboxTx(tx, requestID, "billing_settled", model.AIBillingSettled, billed.FinalAmount, completed)
		})
	})
	if err != nil {
		return err
	}
	if exceptionCommitted {
		return &BillingStatusError{RequestID: requestID, Cause: ErrBillingException}
	}
	if pendingCommitted {
		return &BillingStatusError{RequestID: requestID, Cause: ErrSettlementPending}
	}
	return nil
}

// AbortBeforeExecution 在上游尚未发送、pending -> running 失败时原子释放钱包预占。
// 此路径不依赖 running attempt，避免启动事务失败后留下永久冻结金额。
func (s *AIBillingService) AbortBeforeExecution(ctx context.Context, requestID string, attempt ExecutionAttempt) error {
	if s == nil || s.db == nil || s.walletHolds == nil {
		return ErrBillingException
	}
	attempt.FinishedAt = s.now()
	attempt.Outcome = "failed"
	attempt.ErrorClass = "request_not_sent"
	attempt.ResultUnknown = false
	return retryFinancialTransaction(context.WithoutCancel(ctx), func() error {
		return s.db.WithContext(context.WithoutCancel(ctx)).Transaction(func(tx *gorm.DB) error {
			request, link, err := lockRequestAndLink(tx, requestID)
			if err != nil {
				return err
			}
			if isBillingTerminal(request.BillingStatus) {
				return nil
			}
			if request.ExecutionStatus != model.AIExecutionPending || request.BillingStatus != model.AIBillingHeld {
				return repository.ErrRequestStateConflict
			}
			ledgerAttempt := attempt.ToLedgerModel(requestID, ExecutionUsage{})
			if err := tx.Create(&ledgerAttempt).Error; err != nil {
				return err
			}
			released, err := s.walletHolds.ReleaseHoldTx(tx, link.WalletHoldID, requestID+":release")
			if err != nil {
				return err
			}
			if err := updateWalletLinkTx(tx, link, released); err != nil {
				return err
			}
			zero := decimal.Zero
			errorClass := stringPointer("request_not_sent")
			if err := updateRequestBillingTx(tx, request, model.AIExecutionFailed, model.AIBillingReleased, false, &zero, errorClass, errorClass, ledgerAttempt.ExecutionModelCode, s.now()); err != nil {
				return err
			}
			return createOutboxTx(tx, requestID, "billing_released", model.AIBillingReleased, zero, s.now())
		})
	})
}

// ReconcileInterrupted 收敛持有资金但进程中断的请求，不会重放任何上游调用。
func (s *AIBillingService) ReconcileInterrupted(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	now := s.now()
	var requestIDs []string
	err := s.db.WithContext(ctx).Model(&model.AIRequest{}).
		Where("billing_status IN ? AND updated_at < ?", []string{model.AIBillingHeld, model.AIBillingSettlementPending}, now.Add(-time.Minute)).
		Order("updated_at ASC").Limit(limit).Pluck("request_id", &requestIDs).Error
	if err != nil {
		return 0, err
	}
	changed := 0
	var failures []error
	for _, requestID := range requestIDs {
		resolved, err := s.reconcileOne(ctx, requestID, now)
		if err != nil {
			// 单条损坏记录不能形成队头阻塞，其余 hold 仍须继续收敛。
			failures = append(failures, fmt.Errorf("请求 %s 对账失败: %w", requestID, err))
			continue
		}
		if resolved {
			changed++
		}
	}
	return changed, errors.Join(failures...)
}

// ResolveException 为人工对账提供受控终结入口；G3 仅提供后端能力，管理 UI 和权限入口留给 G5。
func (s *AIBillingService) ResolveException(ctx context.Context, requestID, resolution string, usage ExecutionUsage) error {
	if resolution != ManualResolutionRelease && resolution != ManualResolutionSettle {
		return ErrBillingException
	}
	return retryFinancialTransaction(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			request, link, err := lockRequestAndLink(tx, requestID)
			if err != nil {
				return err
			}
			if resolution == ManualResolutionRelease && (usage.Present || usage.PromptTokens != 0 || usage.CompletionTokens != 0 || usage.CachedTokens != 0 || usage.ReasoningTokens != 0) {
				// 矛盾输入不能被终态幂等吞掉；即使请求已释放也必须明确拒绝。
				return ErrBillingAmountException
			}
			if resolution == ManualResolutionSettle && (!usage.Present || usage.PromptTokens+usage.CompletionTokens <= 0) {
				// 零用量不允许伪装为人工 settle；确认无成本时必须显式选择 release。
				return ErrBillingAmountException
			}
			if request.BillingStatus == model.AIBillingReleased || request.BillingStatus == model.AIBillingSettled {
				matchingTerminal := request.BillingStatus == model.AIBillingReleased && resolution == ManualResolutionRelease ||
					request.BillingStatus == model.AIBillingSettled && resolution == ManualResolutionSettle
				if matchingTerminal {
					if resolution == ManualResolutionRelease {
						return nil
					}
					matches, matchErr := s.manualSettlementMatchesTx(tx, request, usage)
					if matchErr != nil {
						return matchErr
					}
					if matches {
						return nil
					}
					return repository.ErrRequestStateConflict
				}
				return repository.ErrRequestStateConflict
			}
			if request.BillingStatus != model.AIBillingException {
				return repository.ErrRequestStateConflict
			}
			var result *billingservice.SettleTxResult
			var finalAmount decimal.Decimal
			if resolution == ManualResolutionRelease {
				result, err = s.walletHolds.ReleaseHoldTx(tx, link.WalletHoldID, requestID+":manual-release")
			} else {
				applyMinimum := request.ExecutionStatus == model.AIExecutionSucceeded && usage.PromptTokens+usage.CompletionTokens > 0
				billed, calculateErr := s.pricing.CalculateFinalWithPolicy(requestID, request.PriceSnapshotJSON, usage, applyMinimum)
				if calculateErr != nil || request.HeldAmount == nil || billed.FinalAmount.GreaterThan(*request.HeldAmount) {
					return ErrBillingAmountException
				}
				finalAmount = billed.FinalAmount
				for i := range billed.Items {
					// 人工核定结果作为独立事实保留，不能覆盖或静默忽略原始 Provider Usage。
					billed.Items[i].Source = "reconciled"
					billed.Items[i].SequenceNo = 1
				}
				if err := createUsageTx(tx, billed.Items); err != nil {
					return err
				}
				result, err = s.walletHolds.SettleHoldTx(tx, link.WalletHoldID, finalAmount, requestID+":manual-settle")
			}
			if err != nil {
				return err
			}
			if err := updateWalletLinkTx(tx, link, result); err != nil {
				return err
			}
			status := model.AIBillingReleased
			eventType := "billing_manual_released"
			if resolution == ManualResolutionSettle {
				status, eventType = model.AIBillingSettled, "billing_manual_settled"
			}
			updated := tx.Model(&model.AIRequest{}).
				Where("id = ? AND version_no = ? AND billing_status = ?", request.ID, request.VersionNo, model.AIBillingException).
				Updates(map[string]interface{}{
					"billing_status": status, "settled_amount": finalAmount, "error_class": nil,
					"error_code": "manual_reconciled", "completed_at": s.now(), "version_no": gorm.Expr("version_no + 1"),
				})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return repository.ErrRequestStateConflict
			}
			return createOutboxTx(tx, requestID, eventType, status, finalAmount, s.now())
		})
	})
}

// ResolveContentPolicyWaiver 为输出审核拦截请求补录可信 Provider Usage，并按冻结快照记录平台成本。
// 该流程只释放用户预占、绝不产生用户消费；重复提交相同 Usage 幂等成功，冲突 Usage 明确拒绝。
func (s *AIBillingService) ResolveContentPolicyWaiver(ctx context.Context, requestID string, usage ExecutionUsage) error {
	if s == nil || s.db == nil || s.pricing == nil || s.walletHolds == nil ||
		!usage.Present || usage.PromptTokens+usage.CompletionTokens <= 0 {
		return ErrBillingAmountException
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return retryFinancialTransaction(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			request, link, err := lockRequestAndLink(tx, requestID)
			if err != nil {
				return err
			}
			if pointerValue(request.ErrorCode) != "output_moderation_blocked" {
				return repository.ErrRequestStateConflict
			}
			costed, err := s.pricing.CalculateProviderCost(requestID, request.PriceSnapshotJSON, usage)
			if err != nil {
				return ErrBillingAmountException
			}
			rawItems := usageModels(requestID, usage)
			if request.BillingStatus == model.AIBillingReleased {
				rawMatches, matchErr := usageItemsMatchTx(tx, requestID, "provider", 0, rawItems)
				if matchErr != nil {
					return matchErr
				}
				costMatches, matchErr := usageItemsMatchTx(tx, requestID, "provider_cost", 0, costed.Items)
				if matchErr != nil {
					return matchErr
				}
				if rawMatches && costMatches && request.SettledAmount != nil && request.SettledAmount.IsZero() {
					return nil
				}
				return repository.ErrRequestStateConflict
			}
			if request.BillingStatus != model.AIBillingSettlementPending && request.BillingStatus != model.AIBillingException {
				return repository.ErrRequestStateConflict
			}
			if err := createUsageTx(tx, rawItems); err != nil {
				return err
			}
			if matches, matchErr := usageItemsMatchTx(tx, requestID, "provider", 0, rawItems); matchErr != nil {
				return matchErr
			} else if !matches {
				return repository.ErrRequestStateConflict
			}
			if err := createUsageTx(tx, costed.Items); err != nil {
				return err
			}
			released, err := s.walletHolds.ReleaseHoldTx(tx, link.WalletHoldID, requestID+":content-policy-release")
			if err != nil {
				return err
			}
			if err := updateWalletLinkTx(tx, link, released); err != nil {
				return err
			}
			zero := decimal.Zero
			updated := tx.Model(&model.AIRequest{}).
				Where("id = ? AND version_no = ? AND billing_status = ?", request.ID, request.VersionNo, request.BillingStatus).
				Updates(map[string]interface{}{
					"billing_status": model.AIBillingReleased, "settled_amount": zero,
					"completed_at": s.now(), "version_no": gorm.Expr("version_no + 1"),
				})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return repository.ErrRequestStateConflict
			}
			return createOutboxTx(tx, requestID, "billing_content_policy_waived", model.AIBillingReleased, zero, s.now())
		})
	})
}

// usageItemsMatchTx 校验同一来源的计量事实是否与本次补录完全一致，防止幂等键掩盖冲突数据。
func usageItemsMatchTx(tx *gorm.DB, requestID, source string, sequenceNo uint32, expected []model.AIUsageItem) (bool, error) {
	var stored []model.AIUsageItem
	if err := tx.Where("request_id = ? AND source = ? AND sequence_no = ?", requestID, source, sequenceNo).Find(&stored).Error; err != nil {
		return false, err
	}
	if len(stored) != len(expected) {
		return false, nil
	}
	byMeter := make(map[string]model.AIUsageItem, len(stored))
	for _, item := range stored {
		byMeter[item.MeterType] = item
	}
	for _, item := range expected {
		actual, ok := byMeter[item.MeterType]
		if !ok || !actual.Quantity.Equal(item.Quantity) || !optionalDecimalEqual(actual.UnitPrice, item.UnitPrice) || !optionalDecimalEqual(actual.Amount, item.Amount) {
			return false, nil
		}
	}
	return true, nil
}

func optionalDecimalEqual(left, right *decimal.Decimal) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

// manualSettlementMatchesTx 核对重复人工结算参数与已落库金额、逐项 Usage 完全一致。
func (s *AIBillingService) manualSettlementMatchesTx(tx *gorm.DB, request *model.AIRequest, usage ExecutionUsage) (bool, error) {
	applyMinimum := request.ExecutionStatus == model.AIExecutionSucceeded && usage.PromptTokens+usage.CompletionTokens > 0
	billed, err := s.pricing.CalculateFinalWithPolicy(request.RequestID, request.PriceSnapshotJSON, usage, applyMinimum)
	if err != nil {
		return false, err
	}
	if request.SettledAmount == nil || !request.SettledAmount.Equal(billed.FinalAmount) {
		return false, nil
	}
	var stored []model.AIUsageItem
	if err := tx.Where("request_id = ? AND source = ? AND sequence_no = ?", request.RequestID, "reconciled", 1).Find(&stored).Error; err != nil {
		return false, err
	}
	if len(stored) != len(billed.Items) {
		return false, nil
	}
	quantities := make(map[string]decimal.Decimal, len(stored))
	for _, item := range stored {
		quantities[item.MeterType] = item.Quantity
	}
	for _, item := range billed.Items {
		quantity, ok := quantities[item.MeterType]
		if !ok || !quantity.Equal(item.Quantity) {
			return false, nil
		}
	}
	return true, nil
}

func (s *AIBillingService) reconcileOne(ctx context.Context, requestID string, now time.Time) (bool, error) {
	var request model.AIRequest
	if err := s.db.WithContext(ctx).Where("request_id = ?", requestID).First(&request).Error; err != nil {
		return false, err
	}
	if request.BillingStatus == model.AIBillingHeld && request.ExecutionStatus == model.AIExecutionPending {
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			locked, link, err := lockRequestAndLink(tx, requestID)
			if err != nil {
				return err
			}
			if locked.BillingStatus != model.AIBillingHeld || locked.ExecutionStatus != model.AIExecutionPending || !locked.UpdatedAt.Before(now.Add(-time.Minute)) {
				return nil
			}
			settled, err := s.walletHolds.ReleaseHoldTx(tx, link.WalletHoldID, requestID+":recovery-release")
			if err != nil {
				return err
			}
			if err := updateWalletLinkTx(tx, link, settled); err != nil {
				return err
			}
			zero := decimal.Zero
			if err := updateRequestBillingTx(tx, locked, model.AIExecutionFailed, model.AIBillingReleased, false, &zero, stringPointer("execution_interrupted_before_start"), stringPointer("execution_interrupted_before_start"), "", now); err != nil {
				return err
			}
			return createOutboxTx(tx, requestID, "billing_released", model.AIBillingReleased, zero, now)
		})
		return err == nil, err
	}
	if request.BillingStatus == model.AIBillingHeld && request.ExecutionStatus == model.AIExecutionRunning {
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			locked, _, err := lockRequestAndLink(tx, requestID)
			if err != nil {
				return err
			}
			if locked.BillingStatus != model.AIBillingHeld || locked.ExecutionStatus != model.AIExecutionRunning || !locked.UpdatedAt.Before(now.Add(-defaultStreamExecutionTimeout-time.Minute)) {
				return nil
			}
			if err := tx.Model(&model.AIExecutionAttempt{}).Where("request_id = ? AND status = 'running'", requestID).
				Updates(map[string]interface{}{"status": "unknown", "result_unknown": true, "error_class": "reconcile_required", "finished_at": now}).Error; err != nil {
				return err
			}
			if err := updateRequestBillingTx(tx, locked, model.AIExecutionUnknown, model.AIBillingSettlementPending, false, nil, stringPointer("reconcile_required"), stringPointer("execution_interrupted"), "", now); err != nil {
				return err
			}
			return createOutboxTx(tx, requestID, "billing_reconcile_required", model.AIBillingSettlementPending, *locked.HeldAmount, now)
		})
		return err == nil, err
	}
	if request.BillingStatus != model.AIBillingSettlementPending {
		return false, nil
	}
	var attempt model.AIExecutionAttempt
	attemptFound := true
	if err := s.db.WithContext(ctx).Where("request_id = ?", requestID).Order("attempt_no DESC").First(&attempt).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return false, err
		}
		// 历史损坏数据即使缺少执行记录，超过对账期限后仍必须进入人工异常，不能永久冻结资金。
		attemptFound = false
	}
	if attemptFound {
		usage, complete := loadConfirmedUsage(ctx, s.db, requestID)
		if !attempt.ResultUnknown && (attempt.Status == "succeeded" || attempt.Status == "failed") && complete {
			waived := pointerValue(request.ErrorCode) == "output_moderation_blocked"
			result := ExecutionResult{
				Attempt: ledgerAttemptToExecution(attempt), Usage: usage, ClientDisconnected: request.ClientDisconnected,
				CustomerChargeWaived: waived, ErrorCode: pointerValue(request.ErrorCode),
			}
			return true, s.FinalizeRequest(ctx, requestID, result)
		}
		if attempt.Status == "failed" && !attempt.ResultUnknown && pointerValue(attempt.ErrorClass) == "request_not_sent" {
			result := ExecutionResult{Attempt: ledgerAttemptToExecution(attempt), ClientDisconnected: request.ClientDisconnected, ErrorCode: pointerValue(request.ErrorCode)}
			return true, s.FinalizeRequest(ctx, requestID, result)
		}
	}
	if request.UpdatedAt.Before(now.Add(-manualReconcileDeadline)) {
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			locked, _, err := lockRequestAndLink(tx, requestID)
			if err != nil {
				return err
			}
			if locked.BillingStatus != model.AIBillingSettlementPending || !locked.UpdatedAt.Before(now.Add(-manualReconcileDeadline)) {
				return nil
			}
			deadlineErrorCode := "reconcile_deadline_exceeded"
			if pointerValue(locked.ErrorCode) == "output_moderation_blocked" {
				// 内容安全免单请求必须保留原始分类，超期后专用对账入口才能安全识别并按零销售额收敛。
				deadlineErrorCode = "output_moderation_blocked"
			}
			if err := updateRequestBillingTx(tx, locked, locked.ExecutionStatus, model.AIBillingException, locked.ClientDisconnected, nil,
				stringPointer("manual_reconcile_required"), stringPointer(deadlineErrorCode), pointerValue(locked.ExecutionModelCode), now); err != nil {
				return err
			}
			return createOutboxTx(tx, requestID, "billing_manual_review_required", model.AIBillingException, *locked.HeldAmount, now)
		})
		return err == nil, err
	}
	return false, nil
}

func loadConfirmedUsage(ctx context.Context, db *gorm.DB, requestID string) (ExecutionUsage, bool) {
	var items []model.AIUsageItem
	if err := db.WithContext(ctx).Where("request_id = ? AND source IN ?", requestID, []string{"provider", "reconciled"}).Find(&items).Error; err != nil {
		return ExecutionUsage{}, false
	}
	usage := ExecutionUsage{}
	inputFound, outputFound := false, false
	for _, item := range items {
		value := item.Quantity.IntPart()
		switch item.MeterType {
		case "input_tokens":
			usage.PromptTokens, inputFound = value, true
		case "output_tokens":
			usage.CompletionTokens, outputFound = value, true
		case "cached_tokens":
			usage.CachedTokens = value
		case "reasoning_tokens":
			usage.ReasoningTokens = value
		}
	}
	usage.Present = inputFound && outputFound
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage, usage.Present
}

func ledgerAttemptToExecution(attempt model.AIExecutionAttempt) ExecutionAttempt {
	result := ExecutionAttempt{
		AttemptNo: attempt.AttemptNo, Driver: attempt.ExecutionDriver, ProviderCode: attempt.ProviderCode,
		ProviderModel: attempt.ExecutionModelCode, StartedAt: attempt.StartedAt,
		Outcome: attempt.Status, ResultUnknown: attempt.ResultUnknown,
	}
	if attempt.EndpointCode != nil {
		result.EndpointCode = *attempt.EndpointCode
	}
	if attempt.FinishedAt != nil {
		result.FinishedAt = *attempt.FinishedAt
	}
	if attempt.ErrorClass != nil {
		result.ErrorClass = *attempt.ErrorClass
	}
	return result
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func lockRequestAndLink(tx *gorm.DB, requestID string) (*model.AIRequest, *model.AIRequestWalletLink, error) {
	var request model.AIRequest
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ?", requestID).First(&request).Error; err != nil {
		return nil, nil, err
	}
	var link model.AIRequestWalletLink
	if err := tx.Where("request_id = ?", requestID).First(&link).Error; err != nil {
		return nil, nil, err
	}
	return &request, &link, nil
}

func finalizeAttemptTx(tx *gorm.DB, attempt model.AIExecutionAttempt) error {
	result := tx.Model(&model.AIExecutionAttempt{}).
		Where("request_id = ? AND attempt_no = ? AND status = 'running'", attempt.RequestID, attempt.AttemptNo).
		Updates(map[string]interface{}{
			"status": attempt.Status, "result_unknown": attempt.ResultUnknown, "latency_ms": attempt.LatencyMS,
			"prompt_tokens": attempt.PromptTokens, "completion_tokens": attempt.CompletionTokens,
			"reasoning_tokens": attempt.ReasoningTokens, "cached_tokens": attempt.CachedTokens,
			"error_class": attempt.ErrorClass, "finished_at": attempt.FinishedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	var existing model.AIExecutionAttempt
	if err := tx.Where("request_id = ? AND attempt_no = ?", attempt.RequestID, attempt.AttemptNo).First(&existing).Error; err != nil {
		return repository.ErrRequestStateConflict
	}
	if existing.Status != attempt.Status {
		return repository.ErrRequestStateConflict
	}
	return nil
}

func createUsageTx(tx *gorm.DB, items []model.AIUsageItem) error {
	for i := range items {
		item := &items[i]
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(item).Error; err != nil {
			return err
		}
		if item.UnitPrice == nil || item.Amount == nil {
			continue
		}
		// 待对账阶段可能先保存原始数量；结算时只允许补齐尚未定价的同一计费行。
		updated := tx.Model(&model.AIUsageItem{}).
			Where("request_id = ? AND meter_type = ? AND source = ? AND sequence_no = ? AND quantity = ? AND unit_price IS NULL AND amount IS NULL",
				item.RequestID, item.MeterType, item.Source, item.SequenceNo, item.Quantity).
			Updates(map[string]interface{}{"unit_price": item.UnitPrice, "amount": item.Amount})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 1 {
			continue
		}
		var existing model.AIUsageItem
		if err := tx.Where("request_id = ? AND meter_type = ? AND source = ? AND sequence_no = ?",
			item.RequestID, item.MeterType, item.Source, item.SequenceNo).First(&existing).Error; err != nil {
			return err
		}
		if existing.UnitPrice == nil || existing.Amount == nil || !existing.Quantity.Equal(item.Quantity) ||
			!existing.UnitPrice.Equal(*item.UnitPrice) || !existing.Amount.Equal(*item.Amount) {
			return repository.ErrRequestStateConflict
		}
	}
	return nil
}

func updateWalletLinkTx(tx *gorm.DB, link *model.AIRequestWalletLink, result *billingservice.SettleTxResult) error {
	updates := map[string]interface{}{"settled_amount": result.SettledAmount}
	if result.SettleTransaction != nil {
		updates["settle_transaction_id"] = *result.SettleTransaction
	}
	if result.ReleaseTransaction != 0 {
		updates["release_transaction_id"] = result.ReleaseTransaction
	}
	return tx.Model(&model.AIRequestWalletLink{}).Where("id = ?", link.ID).Updates(updates).Error
}

func updateRequestBillingTx(tx *gorm.DB, request *model.AIRequest, executionStatus, billingStatus string, disconnected bool, settled *decimal.Decimal, errorClass, errorCode *string, executionModel string, completed time.Time) error {
	result := tx.Model(&model.AIRequest{}).
		Where("id = ? AND version_no = ? AND billing_status IN ?", request.ID, request.VersionNo, []string{model.AIBillingHeld, model.AIBillingSettlementPending}).
		Updates(map[string]interface{}{
			"execution_status": executionStatus, "billing_status": billingStatus,
			"client_disconnected": disconnected, "settled_amount": settled,
			"error_class": errorClass, "error_code": errorCode,
			"execution_model_code": executionModel, "completed_at": completed,
			"version_no": gorm.Expr("version_no + 1"),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return repository.ErrRequestStateConflict
	}
	return nil
}

func createOutboxTx(tx *gorm.DB, requestID, eventType, billingStatus string, amount decimal.Decimal, now time.Time) error {
	payload, err := json.Marshal(map[string]string{
		"request_id": requestID, "billing_status": billingStatus, "amount": amount.StringFixed(8), "currency": "CNY",
	})
	if err != nil {
		return err
	}
	event := &model.AIOutboxEvent{
		EventID: requestID + ":" + eventType, AggregateType: "ai_request", AggregateID: requestID,
		EventType: eventType, PayloadJSON: payload, Status: model.AIOutboxPending, NextRetryAt: now,
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(event).Error
}

func isBillingTerminal(status string) bool {
	return status == model.AIBillingSettled || status == model.AIBillingReleased || status == model.AIBillingException
}

func decimalPointer(value decimal.Decimal) *decimal.Decimal { return &value }
func stringPointer(value string) *string                    { return &value }

// retryFinancialTransaction 只重试 MySQL 死锁和锁等待超时；业务拒绝、唯一键冲突和金额异常不得盲目重试。
func retryFinancialTransaction(ctx context.Context, operation func() error) error {
	const maxAttempts = 10
	for attempt := 0; attempt < maxAttempts; attempt++ {
		err := operation()
		if err == nil || !isRetryableMySQLTransactionError(err) {
			return err
		}
		delay := time.Duration(1<<attempt) * 5 * time.Millisecond
		if delay > 250*time.Millisecond {
			delay = 250 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return billingservice.ErrConcurrentUpdate
}

func isRetryableMySQLTransactionError(err error) bool {
	var mysqlError *gomysql.MySQLError
	return errors.As(err, &mysqlError) && (mysqlError.Number == 1213 || mysqlError.Number == 1205)
}
