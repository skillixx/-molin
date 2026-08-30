package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

var (
	ErrVideoBillingAccess     = errors.New("视频请求不存在或不可访问")
	ErrVideoBillingConflict   = errors.New("视频生成幂等意图冲突")
	ErrVideoBillingState      = errors.New("视频财务事实不完整或状态冲突")
	errVideoReservationExists = errors.New("视频生成幂等竞争已由另一事务完成")
	videoBillingPublicID      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

// VideoWalletTransactions 只暴露既有钱包的事务内方法，不提供另开事务或静默封顶入口。
type VideoWalletTransactions interface {
	CreateHoldTx(*gorm.DB, uint64, decimal.Decimal, string, string) (*billingservice.HoldTxResult, error)
	SettleHoldTx(*gorm.DB, uint64, decimal.Decimal, string) (*billingservice.SettleTxResult, error)
	ReleaseHoldTx(*gorm.DB, uint64, string) (*billingservice.SettleTxResult, error)
}

type VideoBillingOptions struct {
	QuoteSecret     []byte                            `json:"-"`
	PromptSecret    []byte                            `json:"-"`
	IntentSecret    []byte                            `json:"-"`
	Protector       *VideoTaskPayloadProtector        `json:"-"`
	Visibility      modelVisibilityChecker            `json:"-"`
	Safety          *videogateway.VideoSafetyPipeline `json:"-"`
	ReferenceLoader VideoReferenceLoader              `json:"-"`
}

// VideoBillingService 是共享财务账本的视频协调器，不持有Provider、HTTP客户端或消息队列。
type VideoBillingService struct {
	db                                      *gorm.DB
	holds                                   VideoWalletTransactions
	quoteSecret, promptSecret, intentSecret []byte
	protector                               *VideoTaskPayloadProtector
	visibility                              modelVisibilityChecker
	safety                                  *videogateway.VideoSafetyPipeline
	referenceLoader                         VideoReferenceLoader
	now                                     func() time.Time
	// fault仅用于包内故障注入，正常装配为nil；错误不进入Outbox或普通任务字段。
	fault func(string) error
}

func NewVideoBillingService(db *gorm.DB, holds VideoWalletTransactions, options VideoBillingOptions) (*VideoBillingService, error) {
	if db == nil || holds == nil || options.Protector == nil || options.Visibility == nil || options.Safety == nil {
		return nil, ErrVideoBillingState
	}
	secrets := [][]byte{options.QuoteSecret, options.PromptSecret, options.IntentSecret, options.Protector.key}
	for i, secret := range secrets {
		if len(secret) < 32 {
			return nil, ErrVideoBillingState
		}
		for j := 0; j < i; j++ {
			if bytes.Equal(secret, secrets[j]) {
				return nil, ErrVideoBillingState
			}
		}
	}
	return &VideoBillingService{db: db, holds: holds, quoteSecret: append([]byte(nil), options.QuoteSecret...), promptSecret: append([]byte(nil), options.PromptSecret...), intentSecret: append([]byte(nil), options.IntentSecret...), protector: options.Protector, visibility: options.Visibility, safety: options.Safety, referenceLoader: options.ReferenceLoader, now: time.Now}, nil
}

// ReserveAndCreate 在唯一事务完成生成幂等、Quote消费、Hold与流水、Task/Input租约、密文和held事件。
func (s *VideoBillingService) ReserveAndCreate(ctx context.Context, c VideoReservationCommand) (*VideoPreparedGeneration, error) {
	if s == nil {
		return nil, ErrVideoBillingState
	}
	p, err := s.prepareVideoReservationIntent(c)
	if err != nil {
		return nil, err
	}
	input, owner, prompt, intent, keyHash := p.input, p.owner, p.prompt, p.fingerprint, p.keyHash
	now := s.now().UTC()
	// 重放首先读取原事实，不消费新Quote，也不重新使用已经释放或删除的旧输入。
	if old, found, err := s.lookupVideoReservation(ctx, p); found || err != nil {
		return old, err
	}
	var reference *videogateway.NormalizedReferenceImage
	if input.Input != nil {
		asset, findErr := repository.NewVideoInputAssetRepository(s.db).FindReadyForBinding(ctx, input.Input.InputAssetID, owner, now)
		if findErr != nil {
			return nil, findErr
		}
		if asset.NormalizedSHA256 == nil || *asset.NormalizedSHA256 != input.Input.NormalizedSHA256 || asset.VersionNo != input.Input.Version || s.referenceLoader == nil {
			return nil, repository.ErrVideoInputSnapshotDrift
		}
		reference, err = s.referenceLoader(ctx, *asset)
		if err != nil || reference == nil || reference.NormalizedSHA256 != *asset.NormalizedSHA256 || videoPayloadSHA256(reference.Bytes) != *asset.NormalizedSHA256 {
			return nil, repository.ErrVideoInputSnapshotDrift
		}
		input.Input.InternalID = asset.ID
	}
	if err := s.safety.Preflight(ctx, videogateway.VideoSafetyRequest{Operation: input.Variant.Operation, Prompt: prompt, Reference: reference}); err != nil {
		return nil, err
	}
	quoteFingerprint, err := BuildVideoQuoteFingerprint(s.quoteSecret, input)
	if err != nil {
		return nil, err
	}
	var prepared *VideoPreparedGeneration
	err = retryVideoBillingTransaction(ctx, func() error {
		prepared = nil
		// RC避免旧Hold仓储对缺失唯一键的间隙锁与同钱包串行写锁成环；正确性仍由显式行锁、唯一键及CAS保证。
		// 隔离级别仅作用于本次G5事务，不修改全局连接配置或Chat/Image事务。
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			now := s.now().UTC()
			if err := s.authorizeVideo(ctx, tx, owner, input.LogicalModelCode, now); err != nil {
				return err
			}
			op := input.Variant.Operation
			request := model.VideoBillingRequest{AIRequest: model.AIRequest{RequestID: c.RequestID, UserID: owner.UserID, ProjectID: &owner.ProjectID, APIKeyID: owner.APIKeyID, LogicalModelCode: strings.TrimSpace(input.LogicalModelCode), Modality: "video", Capability: model.AIVideoCapability, Operation: &op, RequestFingerprint: &intent, ModerationStatus: model.AIModerationPassed, ExecutionStatus: model.AIExecutionPending, BillingStatus: model.AIBillingUnquoted, DeliveryStatus: model.AIDeliveryPending, VersionNo: 1, CreatedAt: now, UpdatedAt: now}, CommandKind: "create_video", IntentKeyHash: keyHash, IntentVersion: VideoGenerationIntentVersion, RightsPolicyVersion: c.RightsPolicyVersion}
			// 让唯一键裁决缺失请求的竞争，不先锁不存在的索引范围，避免100并发gap lock互锁。
			if err := tx.Create(&request).Error; err != nil {
				var mysqlErr *drivermysql.MySQLError
				if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1062 {
					return err
				}
				// 重复INSERT持有共享记录锁；先回滚再读，避免锁升级死锁和旧RR快照漏读关联事实。
				return errVideoReservationExists
			}
			if err := s.injectVideoFault("request"); err != nil {
				return err
			}
			quote, _, err := repository.NewVideoQuoteRepository(tx).ConsumeTx(tx, c.QuotePublicID, owner.UserID, owner.ProjectID, owner.APIKeyID, op, quoteFingerprint, c.RequestID, now)
			if err != nil {
				return err
			}
			// ConsumeTx可能等待Quote行锁；取得锁后用新时钟复核，不让入口时间冻结有效期。
			if !quote.ExpiresAt.After(s.now().UTC()) {
				return repository.ErrVideoQuoteExpired
			}
			snapshot, err := DecodeVideoPriceSnapshot(quote.PriceSnapshotJSON)
			_, variantHash, variantErr := CanonicalVideoPriceVariant(input.Variant)
			if err != nil || variantErr != nil || quote.CommandKind == nil || *quote.CommandKind != c.QuoteCommandKind || quote.LogicalModelCode != request.LogicalModelCode || snapshot.LogicalModelCode != request.LogicalModelCode || snapshot.Operation != op || snapshot.PriceVersionID != quote.PriceVersionID || snapshot.SelectedLines[0].VariantHash != variantHash || quote.RequestVariantHash != variantHash || quote.Currency != "CNY" || !quote.QuotedAmount.Equal(decimal.RequireFromString(snapshot.QuotedAmount)) {
				return ErrVideoBillingState
			}
			if err := s.injectVideoFault("quote"); err != nil {
				return err
			}
			var asset *model.AIGatewayInputAsset
			if input.Input != nil {
				asset, err = repository.NewVideoInputAssetRepository(tx).FindReadyForBindingTx(tx, input.Input.InputAssetID, owner, now)
				if err != nil {
					return err
				}
				if asset.ID != input.Input.InternalID || asset.NormalizedSHA256 == nil || *asset.NormalizedSHA256 != input.Input.NormalizedSHA256 || asset.VersionNo != input.Input.Version {
					return repository.ErrVideoInputSnapshotDrift
				}
			}
			if err := videoBillingCASResult(tx.Model(&model.VideoBillingRequest{}).Where("id=? AND version_no=1", request.ID).Updates(map[string]interface{}{"billing_status": model.AIBillingQuoted, "price_snapshot_json": quote.PriceSnapshotJSON, "quoted_amount": quote.QuotedAmount, "version_no": 2})); err != nil {
				return err
			}
			hold, err := s.holds.CreateHoldTx(tx, owner.UserID, quote.QuotedAmount, c.RequestID+":video-hold", "视频生成预占")
			if err != nil {
				return err
			}
			if err := s.injectVideoFault("hold"); err != nil {
				return err
			}
			link := model.AIRequestWalletLink{RequestID: c.RequestID, WalletID: hold.WalletID, WalletHoldID: hold.HoldID, HoldTransactionID: hold.FreezeTransaction, QuotedAmount: quote.QuotedAmount, HeldAmount: quote.QuotedAmount}
			if err := tx.Create(&link).Error; err != nil {
				return err
			}
			if err := s.injectVideoFault("wallet_link"); err != nil {
				return err
			}
			inputJSON, _, _ := CanonicalVideoPriceVariant(input.Variant)
			task := model.AIImageTask{PublicID: c.TaskID, RequestID: c.RequestID, QuoteID: quote.ID, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, LogicalModelCode: input.LogicalModelCode, Capability: model.AIVideoCapability, Operation: &op, Status: model.AIImageTaskReserved, Progress: 5, InputJSON: inputJSON, VersionNo: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&task).Error; err != nil {
				return err
			}
			if err := s.injectVideoFault("task"); err != nil {
				return err
			}
			if asset != nil {
				binding := model.AIGatewayTaskInput{TaskID: task.ID, InputAssetID: asset.ID, UserID: owner.UserID, ProjectID: owner.ProjectID, Role: model.AITaskInputReferenceImage, Ordinal: 0, NormalizedSHA256: *asset.NormalizedSHA256, InputVersion: asset.VersionNo, CreatedAt: now}
				if err := tx.Create(&binding).Error; err != nil {
					return err
				}
				if err := s.injectVideoFault("input"); err != nil {
					return err
				}
			}
			payload, err := s.protector.Seal(task.ID, owner.UserID, owner.ProjectID, model.AITaskPayloadPrompt, []byte(prompt))
			if err != nil {
				return err
			}
			if err := repository.NewVideoTaskPayloadRepository(tx, s.protector).Create(ctx, task.PublicID, owner, payload); err != nil {
				return err
			}
			if err := s.injectVideoFault("payload"); err != nil {
				return err
			}
			if err := videoBillingCASResult(tx.Model(&model.VideoBillingRequest{}).Where("id=? AND version_no=2", request.ID).Updates(map[string]interface{}{"billing_status": model.AIBillingHeld, "held_amount": quote.QuotedAmount, "version_no": 3})); err != nil {
				return err
			}
			from, to := model.AIImageTaskCreated, model.AIImageTaskReserved
			event := model.AIGatewayTaskEvent{EventID: "vg5_" + videoBillingDigest(c.RequestID+":held"), TaskID: task.ID, UserID: owner.UserID, ProjectID: owner.ProjectID, EventType: "video_billing_held", FromStatus: &from, ToStatus: &to, Source: "system", SafeDetailJSON: json.RawMessage(`{"reason":"state_advanced"}`), CreatedAt: now}
			if err := tx.Create(&event).Error; err != nil {
				return err
			}
			if err := s.injectVideoFault("held_state"); err != nil {
				return err
			}
			if err := createVideoBillingOutboxTx(tx, c.RequestID, "video_billing_held", model.AIBillingHeld, op, quote.QuotedAmount, now); err != nil {
				return err
			}
			if err := s.injectVideoFault("held_outbox"); err != nil {
				return err
			}
			// 钱包与输入锁可能等待很久；提交前统一复核有时效的门禁，失败仍回滚全部事实。
			commitNow := s.now().UTC()
			if !quote.ExpiresAt.After(commitNow) {
				return repository.ErrVideoQuoteExpired
			}
			if asset != nil && !asset.ExpiresAt.After(commitNow) {
				return repository.ErrVideoInputSnapshotDrift
			}
			if err := s.authorizeVideo(ctx, tx, owner, input.LogicalModelCode, commitNow); err != nil {
				return err
			}
			prepared = &VideoPreparedGeneration{Quote: quote, RequestID: c.RequestID, TaskID: c.TaskID, HeldAmount: quote.QuotedAmount, ExecutionStatus: model.AIImageTaskReserved, BillingStatus: model.AIBillingHeld, DeliveryStatus: model.AIDeliveryPending}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if errors.Is(err, errVideoReservationExists) {
		existing, found, readErr := s.lookupVideoReservation(ctx, p)
		if readErr != nil {
			return nil, readErr
		}
		if !found {
			return nil, ErrVideoBillingConflict
		}
		return existing, nil
	}
	if err != nil {
		return nil, err
	}
	return prepared, nil
}

// authorizeVideo 每次重放和创建都查询当前权限；模型可见性沿用已有目录边界，不引入新权限真相源。
func (s *VideoBillingService) authorizeVideo(ctx context.Context, db *gorm.DB, owner repository.VideoOwner, code string, now time.Time) error {
	if owner.UserID == 0 || owner.ProjectID == 0 {
		return ErrVideoBillingAccess
	}
	visible, err := s.visibility.VisibleToUser(ctx, owner.UserID, code)
	if err != nil || !visible {
		return ErrVideoBillingAccess
	}
	var access struct{ UserStatus, ProjectStatus, RealNameStatus string }
	if err := db.WithContext(ctx).Raw("SELECT u.status AS user_status,p.status AS project_status,u.real_name_status FROM users u JOIN ai_projects p ON p.user_id=u.id WHERE u.id=? AND p.id=? FOR SHARE", owner.UserID, owner.ProjectID).Scan(&access).Error; err != nil {
		return err
	}
	if access.UserStatus != "active" || access.ProjectStatus != "active" || access.RealNameStatus != "verified" {
		return ErrVideoBillingAccess
	}
	var item model.TokenModel
	if err := db.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("logical_model_code=?", code).First(&item).Error; err != nil {
		return ErrVideoBillingAccess
	}
	if item.Status != "active" || item.Modality != "video" || item.PublishedAt == nil || item.PublishedAt.After(now) || item.ReleaseVersionNo == 0 || !capabilityEnabled(item.CapabilitiesJSON, model.AIVideoCapability) {
		return ErrVideoBillingAccess
	}
	if owner.APIKeyID != nil {
		var key struct {
			Status, ScopeMode, BillingMode string
			ExpiresAt                      *time.Time
		}
		if err := db.WithContext(ctx).Raw("SELECT status,scope_mode,billing_mode,expires_at FROM api_keys WHERE id=? AND user_id=? AND project_id=? FOR SHARE", *owner.APIKeyID, owner.UserID, owner.ProjectID).Scan(&key).Error; err != nil {
			return err
		}
		if key.Status != "active" || key.ScopeMode == "legacy_all" || key.BillingMode != "postpaid" || (key.ExpiresAt != nil && !key.ExpiresAt.After(now)) {
			return ErrVideoBillingAccess
		}
		var count int64
		if err := db.WithContext(ctx).Table("api_key_model_scopes").Where("api_key_id=? AND user_id=? AND project_id=? AND logical_model_code=?", *owner.APIKeyID, owner.UserID, owner.ProjectID, code).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return ErrVideoBillingAccess
		}
	}
	return nil
}

func (s *VideoBillingService) findVideoReservation(db *gorm.DB, owner repository.VideoOwner, keyHash, intent string) (*VideoPreparedGeneration, bool, error) {
	var request model.VideoBillingRequest
	query := db.Where("user_id=? AND project_id=? AND command_kind='create_video' AND intent_key_hash=?", owner.UserID, owner.ProjectID, keyHash)
	if err := query.First(&request).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !equalOptionalUint64(request.APIKeyID, owner.APIKeyID) {
		return nil, true, ErrVideoBillingAccess
	}
	if request.RequestFingerprint == nil || subtle.ConstantTimeCompare([]byte(*request.RequestFingerprint), []byte(intent)) != 1 {
		return nil, true, ErrVideoBillingConflict
	}
	var task model.AIImageTask
	var quote model.AIGatewayQuote
	var link model.AIRequestWalletLink
	if err := db.Where("request_id=? AND user_id=? AND project_id=? AND capability=?", request.RequestID, owner.UserID, owner.ProjectID, model.AIVideoCapability).First(&task).Error; err != nil {
		return nil, true, ErrVideoBillingState
	}
	if !equalOptionalUint64(task.APIKeyID, owner.APIKeyID) || task.Operation == nil || request.Operation == nil || *task.Operation != *request.Operation {
		return nil, true, ErrVideoBillingState
	}
	if err := db.First(&quote, task.QuoteID).Error; err != nil {
		return nil, true, ErrVideoBillingState
	}
	if err := db.Where("request_id=?", request.RequestID).First(&link).Error; err != nil {
		return nil, true, ErrVideoBillingState
	}
	if quote.UserID != owner.UserID || quote.ProjectID != owner.ProjectID || !equalOptionalUint64(quote.APIKeyID, owner.APIKeyID) || quote.ConsumedRequestID == nil || *quote.ConsumedRequestID != request.RequestID || !link.HeldAmount.Equal(quote.QuotedAmount) {
		return nil, true, ErrVideoBillingState
	}
	// 自动报价外层使用RC，嵌套savepoint不能升级为RR；三轴必须来自同一条JOIN，不能拼接早先Request状态。
	state, err := repository.NewVideoTaskRepository(db).FindForOwner(db.Statement.Context, task.PublicID, owner)
	if err != nil {
		return nil, true, err
	}
	return &VideoPreparedGeneration{Quote: &quote, RequestID: request.RequestID, TaskID: task.PublicID, HeldAmount: link.HeldAmount, Existing: true, ExecutionStatus: state.Status, BillingStatus: state.BillingStatus, DeliveryStatus: state.DeliveryStatus}, true, nil
}

func (s *VideoBillingService) injectVideoFault(step string) error {
	if s.fault != nil {
		return s.fault(step)
	}
	return nil
}

// videoBillingCASResult 将零行更新视为冲突，禁止把未推进的状态当成成功继续写关联事实。
func videoBillingCASResult(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return billingservice.ErrConcurrentUpdate
	}
	return nil
}

func videoBillingDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// retryVideoBillingTransaction 只重试已回滚的完整数据库事务，最多三次且服从调用方取消。
func retryVideoBillingTransaction(ctx context.Context, fn func() error) error {
	for attempt := 0; attempt < 3; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		var mysqlErr *drivermysql.MySQLError
		retryable := errors.Is(err, billingservice.ErrConcurrentUpdate) || (errors.As(err, &mysqlErr) && (mysqlErr.Number == 1213 || mysqlErr.Number == 1205))
		if !retryable || attempt == 2 {
			return err
		}
		timer := time.NewTimer(time.Duration(10*(1<<attempt)) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ErrVideoBillingState
}

func createVideoBillingOutboxTx(tx *gorm.DB, requestID, eventType, status, operation string, amount decimal.Decimal, now time.Time) error {
	if tx == nil || amount.IsNegative() || (operation != model.AIVideoOperationTextToVideo && operation != model.AIVideoOperationImageToVideo) {
		return ErrVideoBillingState
	}
	payload, err := json.Marshal(struct {
		RequestID string `json:"request_id"`
		Status    string `json:"status"`
		Amount    string `json:"amount"`
		Currency  string `json:"currency"`
		Operation string `json:"operation"`
		Version   int    `json:"version"`
	}{requestID, status, amount.StringFixed(8), "CNY", operation, 1})
	if err != nil {
		return err
	}
	return tx.Create(&model.AIOutboxEvent{EventID: "vg5_" + videoBillingDigest(requestID+":"+eventType), AggregateType: "video_request", AggregateID: requestID, EventType: eventType, PayloadJSON: payload, Status: model.AIOutboxPending, NextRetryAt: now, CreatedAt: now, UpdatedAt: now}).Error
}
