package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// VideoFinancialResult 只返回低敏财务终态；不携带Prompt、对象位置、凭据或Provider正文。
type VideoFinancialResult struct {
	RequestID       string          `json:"request_id"`
	TaskID          string          `json:"task_id"`
	ExecutionStatus string          `json:"execution_status"`
	BillingStatus   string          `json:"billing_status"`
	DeliveryStatus  string          `json:"delivery_status"`
	HeldAmount      decimal.Decimal `json:"held_amount"`
	SettledAmount   decimal.Decimal `json:"settled_amount"`
	ReleasedAmount  decimal.Decimal `json:"released_amount"`
	Existing        bool            `json:"existing"`
}

// CancelBeforeSubmit 仅处理可证明尚未进入submitting的任务；它没有Provider依赖，也不猜测取消结果。
// Task、Hold、解冻流水、Usage、租约和Outbox全部在一个事务内形成相互一致的取消终态。
func (s *VideoBillingService) CancelBeforeSubmit(ctx context.Context, taskID string, owner repository.VideoOwner) (*VideoFinancialResult, error) {
	return s.cancelBeforeSubmit(ctx, taskID, owner, nil)
}

// G6外层还持有取消命令回执，必须共用其事务且只让最外层重试，禁止死锁后重试失效保存点。
func (s *VideoBillingService) cancelBeforeSubmit(ctx context.Context, taskID string, owner repository.VideoOwner, transaction *gorm.DB) (*VideoFinancialResult, error) {
	return s.cancelBeforeSubmitAuthorized(ctx, taskID, owner, transaction, nil)
}

// 管理取消仅通过绑定本事务、操作者、目标及原版本的私有授权进入；原用户入口始终走原准入。
func (s *VideoBillingService) cancelBeforeSubmitAuthorized(ctx context.Context, taskID string, owner repository.VideoOwner, transaction *gorm.DB, grant *videoAdminCancellationGrant) (*VideoFinancialResult, error) {
	if s == nil || s.db == nil {
		return nil, ErrVideoBillingState
	}
	var result *VideoFinancialResult
	apply := func(tx *gorm.DB) error {
		result = nil
		tasks := repository.NewVideoTaskRepository(tx)
		task, err := tasks.LockForOwnerTx(tx, taskID, owner)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if grant == nil {
			if err := s.authorizeVideo(ctx, tx, owner, task.LogicalModelCode, now); err != nil {
				return err
			}
		} else {
			if err := grant.authorize(ctx, tx, task, owner); err != nil {
				return err
			}
		}
		request, quote, link, hold, err := loadVideoFinancialFactsTx(tx, task, owner)
		if err != nil {
			return err
		}
		if task.Status == model.AIImageTaskCancelled && request.BillingStatus == model.AIBillingReleased {
			if err := validateVideoCancelledFactsTx(tx, task, *request, *link, *hold); err != nil {
				return err
			}
			result = videoCancelledResult(task, hold.HoldAmount, true)
			return nil
		}
		if (task.Status != model.AIImageTaskReserved && task.Status != model.AIImageTaskQueued) || request.BillingStatus != model.AIBillingHeld || request.DeliveryStatus != model.AIDeliveryPending || hold.Status != billingmodel.HoldStatusHolding || hold.SettledAmount != nil || link.SettledAmount != nil || link.SettleTransactionID != nil || link.ReleaseTransactionID != nil {
			return ErrVideoBillingState
		}
		if err := verifyVideoNeverSubmittedTx(tx, task); err != nil {
			return err
		}
		task, err = tasks.RequestCancellation(ctx, taskID, owner, now)
		if err != nil {
			return err
		}
		// 先取得任务取消CAS。Worker若先进入submitting，此路径必定拒绝，不能释放可能已计费的Hold。
		task, err = tasks.TransitionExecution(ctx, repository.VideoStateTransition{TaskPublicID: taskID, Owner: owner, ExpectedVersion: task.VersionNo, ToStatus: model.AIImageTaskCancelled, Progress: task.Progress, EventID: "vg5_" + videoBillingDigest(task.RequestID+":cancel_before_submit"), Source: "api", SafeDetailJSON: json.RawMessage(`{"reason":"state_advanced"}`), Now: now})
		if err != nil {
			return err
		}
		if err := s.injectVideoFault("cancel_task"); err != nil {
			return err
		}
		task, err = tasks.TransitionBilling(ctx, videoCancelTransition(task, owner, model.AIBillingSettlementPending, "release_pending", now))
		if err != nil {
			return err
		}
		if err := s.injectVideoFault("cancel_pending"); err != nil {
			return err
		}
		released, err := s.holds.ReleaseHoldTx(tx, hold.ID, task.RequestID+":video-release")
		if err != nil {
			return err
		}
		if released == nil || released.Status != billingmodel.HoldStatusReleased || !released.SettledAmount.IsZero() || released.SettleTransaction != nil || released.ReleaseTransaction == 0 {
			return ErrVideoBillingState
		}
		if err := s.injectVideoFault("cancel_hold"); err != nil {
			return err
		}
		zero := decimal.Zero
		if err := videoBillingCASResult(tx.Model(&model.AIRequestWalletLink{}).Where("id=? AND settled_amount IS NULL AND settle_transaction_id IS NULL AND release_transaction_id IS NULL", link.ID).Updates(map[string]interface{}{"settled_amount": zero, "release_transaction_id": released.ReleaseTransaction, "updated_at": now})); err != nil {
			return err
		}
		if err := s.injectVideoFault("cancel_link"); err != nil {
			return err
		}
		pricing, err := NewVideoPricingService(nil).CalculateVideoFinal(task.RequestID, quote.PriceSnapshotJSON, zero)
		if err != nil {
			return err
		}
		// G2快照计算仅提供销售规格，不把快照成本伪装为Provider已确认成本。零成本源于“从未提交”证明。
		currency := "CNY"
		usage := pricing.UsageFact
		usage.PriceVersionID, usage.UnitPrice, usage.Amount, usage.Currency = &quote.PriceVersionID, &zero, &zero, &currency
		cost := usage
		cost.RecordKind = model.AIUsageCostLine
		for _, fact := range []model.AIUsageItem{usage, pricing.SaleLine, cost} {
			if _, old, err := repository.NewVideoUsageRepository(tx).AppendTx(tx, taskID, owner, fact, now); err != nil {
				return err
			} else if old {
				return ErrVideoBillingState
			}
			if err := s.injectVideoFault("cancel_" + fact.RecordKind); err != nil {
				return err
			}
		}
		task, err = tasks.TransitionBilling(ctx, videoCancelTransition(task, owner, model.AIBillingReleased, "released", now))
		if err != nil {
			return err
		}
		if err := videoBillingCASResult(tx.Model(&model.VideoBillingRequest{}).Where("request_id=? AND version_no=? AND settled_amount IS NULL", task.RequestID, task.RequestVersionNo).Updates(map[string]interface{}{"settled_amount": zero, "version_no": gorm.Expr("version_no+1"), "updated_at": now})); err != nil {
			return err
		}
		task.RequestVersionNo++
		task, err = tasks.TransitionDelivery(ctx, videoCancelTransition(task, owner, model.AIDeliveryRejected, "delivery_rejected", now))
		if err != nil {
			return err
		}
		if err := s.injectVideoFault("cancel_final_state"); err != nil {
			return err
		}
		if s.budget != nil {
			if err := s.budget.SyncTx(ctx, tx, task.RequestID, s.now); err != nil {
				return err
			}
			if err := s.injectVideoFault("cancel_budget"); err != nil {
				return err
			}
		}
		if err := repository.NewVideoInputAssetRepository(tx).ReleaseTaskLeases(ctx, taskID, owner, now); err != nil {
			return err
		}
		if err := s.injectVideoFault("cancel_lease"); err != nil {
			return err
		}
		if err := createVideoBillingOutboxTx(tx, task.RequestID, "video_billing_released", model.AIBillingReleased, *task.Operation, hold.HoldAmount, now); err != nil {
			return err
		}
		if err := s.injectVideoFault("cancel_released_outbox"); err != nil {
			return err
		}
		if err := createVideoBillingOutboxTx(tx, task.RequestID, "video_delivery_rejected", model.AIDeliveryRejected, *task.Operation, zero, now); err != nil {
			return err
		}
		if err := s.injectVideoFault("cancel_rejected_outbox"); err != nil {
			return err
		}
		// 首次提交与幂等重放执行相同的局部一致性核验，禁止只在重放时发现半成品事实。
		finalRequest, _, finalLink, finalHold, err := loadVideoFinancialFactsTx(tx, task, owner)
		if err != nil {
			return err
		}
		if err := validateVideoCancelledFactsTx(tx, task, *finalRequest, *finalLink, *finalHold); err != nil {
			return err
		}
		result = videoCancelledResult(task, hold.HoldAmount, false)
		return nil
	}
	var err error
	if transaction != nil {
		err = apply(transaction)
	} else {
		err = retryVideoBillingTransaction(ctx, func() error {
			return s.db.WithContext(ctx).Transaction(apply, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		})
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// loadVideoFinancialFactsTx 使用已锁定的Task解析唯一财务链，不允许把另一请求的Hold或Quote带入释放。
func loadVideoFinancialFactsTx(tx *gorm.DB, task *repository.VideoTaskRecord, owner repository.VideoOwner) (*model.VideoBillingRequest, *model.AIGatewayQuote, *model.AIRequestWalletLink, *billingmodel.WalletHold, error) {
	var request model.VideoBillingRequest
	var quote model.AIGatewayQuote
	var link model.AIRequestWalletLink
	var hold billingmodel.WalletHold
	if err := tx.Where("request_id=? AND command_kind='create_video'", task.RequestID).First(&request).Error; err != nil {
		return nil, nil, nil, nil, ErrVideoBillingState
	}
	if err := tx.First(&quote, task.QuoteID).Error; err != nil {
		return nil, nil, nil, nil, ErrVideoBillingState
	}
	if err := tx.Where("request_id=?", task.RequestID).First(&link).Error; err != nil {
		return nil, nil, nil, nil, ErrVideoBillingState
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&hold, link.WalletHoldID).Error; err != nil {
		return nil, nil, nil, nil, ErrVideoBillingState
	}
	if request.UserID != owner.UserID || request.ProjectID == nil || *request.ProjectID != owner.ProjectID || !equalOptionalUint64(request.APIKeyID, owner.APIKeyID) || request.Operation == nil || task.Operation == nil || quote.Operation == nil || *request.Operation != *task.Operation || *quote.Operation != *task.Operation || request.LogicalModelCode != task.LogicalModelCode || quote.LogicalModelCode != task.LogicalModelCode || quote.UserID != owner.UserID || quote.ProjectID != owner.ProjectID || !equalOptionalUint64(quote.APIKeyID, owner.APIKeyID) || quote.ConsumedRequestID == nil || *quote.ConsumedRequestID != task.RequestID || request.HeldAmount == nil || request.QuotedAmount == nil || !request.HeldAmount.Equal(quote.QuotedAmount) || !request.QuotedAmount.Equal(quote.QuotedAmount) || !link.HeldAmount.Equal(quote.QuotedAmount) || !link.QuotedAmount.Equal(quote.QuotedAmount) || !hold.HoldAmount.Equal(quote.QuotedAmount) || hold.WalletID != link.WalletID || hold.UserID != owner.UserID || hold.FreezeTxnID == nil || *hold.FreezeTxnID != link.HoldTransactionID || hold.IdempotencyKey != task.RequestID+":video-hold" {
		return nil, nil, nil, nil, ErrVideoBillingState
	}
	if _, err := DecodeVideoPriceSnapshot(quote.PriceSnapshotJSON); err != nil {
		return nil, nil, nil, nil, err
	}
	return &request, &quote, &link, &hold, nil
}

func verifyVideoNeverSubmittedTx(tx *gorm.DB, task *repository.VideoTaskRecord) error {
	err := repository.VerifyVideoNeverSubmittedTx(tx, task)
	if errors.Is(err, repository.ErrVideoUsageInvalid) {
		return ErrVideoBillingState
	}
	return err
}

func videoCancelTransition(task *repository.VideoTaskRecord, owner repository.VideoOwner, status, kind string, now time.Time) repository.VideoStateTransition {
	return repository.VideoStateTransition{TaskPublicID: task.PublicID, Owner: owner, ExpectedVersion: task.RequestVersionNo, ToStatus: status, EventID: "vg5_" + videoBillingDigest(task.RequestID+":"+kind), Source: "system", SafeDetailJSON: json.RawMessage(`{"reason":"state_advanced"}`), Now: now}
}

func videoCancelledResult(task *repository.VideoTaskRecord, held decimal.Decimal, existing bool) *VideoFinancialResult {
	return &VideoFinancialResult{RequestID: task.RequestID, TaskID: task.PublicID, ExecutionStatus: model.AIImageTaskCancelled, BillingStatus: model.AIBillingReleased, DeliveryStatus: model.AIDeliveryRejected, HeldAmount: held, SettledAmount: decimal.Zero, ReleasedAmount: held, Existing: existing}
}

// validateVideoCancelledFactsTx 幂等重放必须读到真实的释放流水和终态，不因看到cancelled就虚构成功。
func validateVideoCancelledFactsTx(tx *gorm.DB, task *repository.VideoTaskRecord, r model.VideoBillingRequest, link model.AIRequestWalletLink, hold billingmodel.WalletHold) error {
	if err := validateVideoAdjustmentsTx(tx, task); err != nil {
		return err
	}
	submitted := task.ProviderTaskID != nil
	if submitted {
		var quote model.AIGatewayQuote
		if tx.First(&quote, task.QuoteID).Error != nil {
			return ErrVideoBillingState
		}
		if _, err := loadVideoProviderReleaseProofTx(tx, task, &quote); err != nil {
			return err
		}
	} else {
		if err := verifyVideoNeverSubmittedTx(tx, task); err != nil {
			return err
		}
	}
	if r.ExecutionStatus != task.Status || (!submitted && task.Status != model.AIImageTaskCancelled) || r.BillingStatus != model.AIBillingReleased || r.DeliveryStatus != model.AIDeliveryRejected || r.SettledAmount == nil || !r.SettledAmount.IsZero() || hold.Status != billingmodel.HoldStatusReleased || hold.SettledAmount == nil || !hold.SettledAmount.IsZero() || hold.SettleTxnID != nil || link.SettledAmount == nil || !link.SettledAmount.IsZero() || link.SettleTransactionID != nil || link.ReleaseTransactionID == nil {
		return ErrVideoBillingState
	}
	var release billingmodel.WalletTransaction
	if err := tx.First(&release, *link.ReleaseTransactionID).Error; err != nil {
		return ErrVideoBillingState
	}
	if release.WalletID != hold.WalletID || release.UserID != hold.UserID || release.Type != "unfreeze" || release.Direction != "in" || !release.Amount.Equal(hold.HoldAmount) {
		return ErrVideoBillingState
	}
	var freeze billingmodel.WalletTransaction
	if err := tx.First(&freeze, link.HoldTransactionID).Error; err != nil {
		return ErrVideoBillingState
	}
	if freeze.WalletID != hold.WalletID || freeze.UserID != hold.UserID || freeze.Type != "freeze" || freeze.Direction != "out" || !freeze.Amount.Equal(hold.HoldAmount) || freeze.ID >= release.ID {
		return ErrVideoBillingState
	}
	var usage []model.VideoUsageItem
	if err := tx.Where("request_id=? AND record_kind<>'adjustment'", task.RequestID).Find(&usage).Error; err != nil {
		return err
	}
	wantFacts := 3
	if submitted {
		wantFacts = 4
	}
	if len(usage) != wantFacts {
		return ErrVideoBillingState
	}
	snapshot, err := DecodeVideoPriceSnapshot(r.PriceSnapshotJSON)
	if err != nil {
		return err
	}
	salePrice, err := decimal.NewFromString(snapshot.SelectedLines[0].SaleUnitPrice)
	if err != nil {
		return ErrVideoBillingState
	}
	unitSize, err := decimal.NewFromString(snapshot.SelectedLines[0].UnitSize)
	if err != nil {
		return ErrVideoBillingState
	}
	kinds := map[string]bool{model.AIUsageFact: false, model.AIUsageSaleLine: false, model.AIUsageCostLine: false}
	if submitted {
		delete(kinds, model.AIUsageCostLine)
	}
	for _, item := range usage {
		// Provider的两条计量/成本已通过确认摘要核验，不能套用用户零销售规则抹去安全成本。
		if submitted && (item.Source == "provider" || item.Source == "provider_cost") {
			continue
		}
		seen, valid := kinds[item.RecordKind]
		if !valid || seen || item.Source != "gateway" || item.SequenceNo != 0 || item.TaskID != task.ID || item.QuoteID != task.QuoteID || item.UserID != task.UserID || item.ProjectID != task.ProjectID || !equalOptionalUint64(item.APIKeyID, task.APIKeyID) || item.LogicalModelCode != task.LogicalModelCode || item.Capability != model.AIVideoCapability || item.Operation == nil || *item.Operation != *task.Operation || item.MeterType != VideoMeterSeconds || item.UsageUnit != "seconds" || !item.UnitSize.Equal(unitSize) || item.PriceVersionID == nil || *item.PriceVersionID != snapshot.PriceVersionID || item.VariantHash != snapshot.SelectedLines[0].VariantHash || !item.Quantity.IsZero() || item.Amount == nil || !item.Amount.IsZero() || item.Currency == nil || *item.Currency != "CNY" || item.UnitPrice == nil {
			return ErrVideoBillingState
		}
		if item.RecordKind == model.AIUsageSaleLine {
			if !item.UnitPrice.Equal(salePrice) {
				return ErrVideoBillingState
			}
		} else if !item.UnitPrice.IsZero() {
			return ErrVideoBillingState
		}
		kinds[item.RecordKind] = true
	}
	for _, seen := range kinds {
		if !seen {
			return ErrVideoBillingState
		}
	}
	var events []model.AIOutboxEvent
	// 释放终态同样核对事件全集；错误聚合类型不能通过先过滤变成不存在。
	if err := tx.Where("aggregate_id=? AND event_type<>'video_adjustment_recorded'", task.RequestID).Find(&events).Error; err != nil {
		return err
	}
	expected := map[string]string{"video_billing_held": model.AIBillingHeld, "video_billing_released": model.AIBillingReleased, "video_delivery_rejected": model.AIDeliveryRejected}
	var jobs []model.VideoCompensationTask
	if err := tx.Where("task_type='video_reconcile' AND aggregate_id=?", task.RequestID).Find(&jobs).Error; err != nil {
		return err
	}
	if len(jobs) == 1 {
		if videoCompensationNeedsPending(&jobs[0]) {
			expected["video_settlement_pending"] = model.AIBillingSettlementPending
		}
		expected["video_compensation_required"] = "pending"
	} else if len(jobs) != 0 {
		return ErrVideoBillingState
	}
	if len(events) != len(expected) {
		return ErrVideoBillingState
	}
	for _, event := range events {
		status, ok := expected[event.EventType]
		if !ok || event.AggregateType != "video_request" || event.EventID != "vg5_"+videoBillingDigest(task.RequestID+":"+event.EventType) || event.Status != model.AIOutboxPending || event.LockedAt != nil {
			return ErrVideoBillingState
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal(event.PayloadJSON, &payload) != nil || len(payload) != 6 {
			return ErrVideoBillingState
		}
		amount := hold.HoldAmount
		if event.EventType == "video_delivery_rejected" {
			amount = decimal.Zero
		}
		values := map[string]string{"request_id": task.RequestID, "status": status, "amount": amount.StringFixed(8), "currency": "CNY", "operation": *task.Operation}
		for key, want := range values {
			var got string
			if json.Unmarshal(payload[key], &got) != nil || got != want {
				return ErrVideoBillingState
			}
		}
		var version int
		if json.Unmarshal(payload["version"], &version) != nil || version != 1 {
			return ErrVideoBillingState
		}
		delete(expected, event.EventType)
	}
	var bindings []model.AIGatewayTaskInput
	if err := tx.Where("task_id=?", task.ID).Find(&bindings).Error; err != nil {
		return err
	}
	if (*task.Operation == model.AIVideoOperationTextToVideo && len(bindings) != 0) || (*task.Operation == model.AIVideoOperationImageToVideo && len(bindings) != 1) {
		return ErrVideoBillingState
	}
	for _, b := range bindings {
		if b.UserID != task.UserID || b.ProjectID != task.ProjectID || b.LeaseReleasedAt == nil {
			return ErrVideoBillingState
		}
	}
	return nil
}
