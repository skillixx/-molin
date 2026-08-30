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
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// SettleReady 只读取已落库的Quote、确认成本和媒体安全事实，不接受调用方金额，也不持有Provider。
// 财务事务不会把资产改为available；交付必须由后续独立门禁处理。
func (s *VideoBillingService) SettleReady(ctx context.Context, taskID string, owner repository.VideoOwner) (*VideoFinancialResult, error) {
	result, err := s.settleReady(ctx, taskID, owner, nil)
	if err != nil && s != nil && s.db != nil && !errors.Is(err, repository.ErrVideoCompensationBusy) && !errors.Is(err, repository.ErrVideoCompensationLeaseLost) {
		// 客户端断连不能丢掉财务恢复标记；只给已回滚的数据库补记最多5秒，不做任何Provider操作。
		markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = errors.Join(err, s.markVideoSettlementPending(markCtx, taskID, owner))
	}
	return result, err
}

// RecoverSettlement 是补偿Worker的租约化财务入口，租约必须属于当前请求，不触发额外Provider操作。
func (s *VideoBillingService) RecoverSettlement(ctx context.Context, taskID string, owner repository.VideoOwner, lease repository.VideoCompensationLease) (*VideoFinancialResult, error) {
	return s.settleReady(ctx, taskID, owner, &lease)
}

func (s *VideoBillingService) settleReady(ctx context.Context, taskID string, owner repository.VideoOwner, lease *repository.VideoCompensationLease) (*VideoFinancialResult, error) {
	if s == nil || s.db == nil {
		return nil, ErrVideoBillingState
	}
	var output *VideoFinancialResult
	err := retryVideoBillingTransaction(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			tasks := repository.NewVideoTaskRepository(tx)
			task, err := tasks.LockForOwnerTx(tx, taskID, owner)
			if err != nil {
				return err
			}
			comp := repository.NewVideoCompensationRepository(tx).WithClock(s.now)
			job, compErr := comp.FindRequestTx(tx, task.RequestID)
			if compErr != nil && !errors.Is(compErr, gorm.ErrRecordNotFound) {
				return compErr
			}
			if compErr == nil && job.Status != "completed" {
				if lease == nil {
					return repository.ErrVideoCompensationBusy
				}
				if lease.RequestID != task.RequestID || lease.ID != job.ID {
					return repository.ErrVideoCompensationLeaseLost
				}
				if _, err := comp.CheckLeaseTx(tx, *lease); err != nil {
					return err
				}
			} else if lease != nil {
				return repository.ErrVideoCompensationLeaseLost
			}
			if compErr == nil && job.Status == "completed" && task.BillingStatus != model.AIBillingSettled {
				return repository.ErrVideoCompensationLeaseLost
			}
			r, q, link, hold, err := loadVideoFinancialFactsTx(tx, task, owner)
			if err != nil {
				return err
			}
			if task.Status != model.AIImageTaskSucceeded || task.ProviderCode == nil || task.ProviderTaskID == nil || task.AttemptCount != 1 {
				return ErrVideoBillingState
			}
			existing := r.BillingStatus == model.AIBillingSettled
			if !existing && (r.BillingStatus != model.AIBillingHeld && r.BillingStatus != model.AIBillingSettlementPending) {
				return ErrVideoBillingState
			}
			media, err := loadVideoSettlementMediaTx(tx, task, !existing, s.now().UTC())
			if err != nil {
				return err
			}
			cost, err := loadVideoConfirmedCostTx(tx, task, q)
			if err != nil || !cost.Quantity.Equal(*media.DurationSeconds) {
				return ErrVideoBillingState
			}
			var variant VideoPriceVariant
			if json.Unmarshal(task.InputJSON, &variant) != nil {
				return ErrVideoBillingState
			}
			spec, err := parseVideoG4TaskSpec(task.InputJSON)
			if err != nil || media.Width == nil || media.Height == nil || *media.Width != spec.Width || *media.Height != spec.Height || media.FrameRate == nil || !media.FrameRate.Equal(decimal.NewFromInt(int64(spec.FrameRate))) || media.HasAudio == nil || *media.HasAudio != spec.Audio {
				return ErrVideoBillingState
			}
			price, err := NewVideoPricingService(nil).CalculateVideoFinal(task.RequestID, q.PriceSnapshotJSON, *media.DurationSeconds)
			if err != nil {
				return err
			}
			if existing {
				if err := validateVideoSettledFinanceTx(tx, task, *r, *link, *hold, price); err != nil {
					return err
				}
				if lease != nil {
					if _, err := comp.CheckLeaseTx(tx, *lease); err != nil {
						return err
					}
				}
				output = videoSettledResult(task, *r, price, true)
				return nil
			}
			if hold.Status != billingmodel.HoldStatusHolding || hold.SettledAmount != nil || link.SettledAmount != nil || link.SettleTransactionID != nil || link.ReleaseTransactionID != nil || r.DeliveryStatus != model.AIDeliveryPending {
				return ErrVideoBillingState
			}
			now := s.now().UTC()
			if r.BillingStatus == model.AIBillingHeld {
				task, err = tasks.TransitionBilling(ctx, videoCancelTransition(task, owner, model.AIBillingSettlementPending, "settle_pending", now))
				if err != nil {
					return err
				}
			}
			if err := s.injectVideoFault("settle_pending"); err != nil {
				return err
			}
			settled, err := s.holds.SettleHoldTx(tx, hold.ID, price.SettledAmount, task.RequestID+":video-settle")
			if err != nil {
				return err
			}
			if settled == nil || settled.Status != billingmodel.HoldStatusSettled || !settled.SettledAmount.Equal(price.SettledAmount) || settled.SettleTransaction == nil || settled.ReleaseTransaction == 0 {
				return ErrVideoBillingState
			}
			if err := s.injectVideoFault("settle_hold"); err != nil {
				return err
			}
			if err := videoBillingCASResult(tx.Model(&model.AIRequestWalletLink{}).Where("id=? AND settled_amount IS NULL", link.ID).Updates(map[string]interface{}{"settled_amount": price.SettledAmount, "settle_transaction_id": *settled.SettleTransaction, "release_transaction_id": settled.ReleaseTransaction, "updated_at": now})); err != nil {
				return err
			}
			if err := s.injectVideoFault("settle_link"); err != nil {
				return err
			}
			zero, currency := decimal.Zero, "CNY"
			usage := price.UsageFact
			usage.UnitPrice, usage.Amount, usage.Currency, usage.PriceVersionID = &zero, &zero, &currency, &q.PriceVersionID
			// 只追加用户计量与销售；成本已由Provider确认事务写入，严禁使用price.CostLine重算或覆盖。
			for _, fact := range []model.AIUsageItem{usage, price.SaleLine} {
				if _, old, err := repository.NewVideoUsageRepository(tx).AppendTx(tx, taskID, owner, fact, now); err != nil {
					return err
				} else if old {
					return ErrVideoBillingState
				}
				if err := s.injectVideoFault("settle_" + fact.RecordKind); err != nil {
					return err
				}
			}
			task, err = tasks.TransitionBilling(ctx, videoCancelTransition(task, owner, model.AIBillingSettled, "settled", now))
			if err != nil {
				return err
			}
			if err := videoBillingCASResult(tx.Model(&model.VideoBillingRequest{}).Where("request_id=? AND version_no=? AND settled_amount IS NULL", task.RequestID, task.RequestVersionNo).Updates(map[string]interface{}{"settled_amount": price.SettledAmount, "version_no": gorm.Expr("version_no+1"), "updated_at": now})); err != nil {
				return err
			}
			if err := s.injectVideoFault("settle_state"); err != nil {
				return err
			}
			if err := createVideoBillingOutboxTx(tx, task.RequestID, "video_billing_settled", model.AIBillingSettled, *task.Operation, price.SettledAmount, now); err != nil {
				return err
			}
			if err := s.injectVideoFault("settle_outbox"); err != nil {
				return err
			}
			if err := repository.NewVideoInputAssetRepository(tx).ReleaseTaskLeases(ctx, taskID, owner, now); err != nil {
				return err
			}
			if err := s.injectVideoFault("settle_lease"); err != nil {
				return err
			}
			final, _, finalLink, finalHold, err := loadVideoFinancialFactsTx(tx, task, owner)
			if err != nil {
				return err
			}
			if err := validateVideoSettledFinanceTx(tx, task, *final, *finalLink, *finalHold, price); err != nil {
				return err
			}
			// 初始资产锁与钱包事务可能等待到媒体过期，提交前必须按新时钟再验完整资产树。
			if _, err := loadVideoSettlementMediaTx(tx, task, true, s.now().UTC()); err != nil {
				return err
			}
			if lease != nil {
				if _, err := comp.CheckLeaseTx(tx, *lease); err != nil {
					return err
				}
			}
			output = videoSettledResult(task, *final, price, false)
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		return nil, err
	}
	return output, nil
}

func loadVideoSettlementMediaTx(tx *gorm.DB, task *repository.VideoTaskRecord, requireReady bool, now time.Time) (*model.AIImageAsset, error) {
	var assets []model.AIImageAsset
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id=?", task.RequestID).Order("id ASC").Find(&assets).Error; err != nil {
		return nil, err
	}
	if len(assets) != 6 {
		return nil, ErrVideoBillingState
	}
	roles := map[string]bool{"content": false, "cover": false, "preview": false, "thumbnail": false, "moderation_copy": false, "derived": false}
	var root *model.AIImageAsset
	for i := range assets {
		a := &assets[i]
		seen, ok := roles[a.AssetRole]
		if !ok || seen || a.TaskID != task.ID || a.UserID != task.UserID || a.ProjectID != task.ProjectID || a.Modality != "video" || a.ModerationStatus != model.AIModerationPassed || a.ExplicitLabelStatus != model.AIImageLabelApplied || a.ImplicitLabelStatus != model.AIImageLabelApplied || a.ModerationPolicyVersion == nil || *a.ModerationPolicyVersion == "" || a.ExplicitLabelVersion == nil || *a.ExplicitLabelVersion == "" || a.ImplicitLabelVersion == nil || *a.ImplicitLabelVersion == "" || a.SHA256 == nil || !lowerHex64.MatchString(*a.SHA256) || a.SizeBytes == nil || *a.SizeBytes == 0 || a.Bucket == nil || *a.Bucket == "" || a.ObjectKey == nil || *a.ObjectKey == "" {
			return nil, ErrVideoBillingState
		}
		if requireReady && (a.LifecycleState != model.AIImageAssetTemporary || a.MediaDeletedAt != nil || a.DeletedAt != nil || a.LegalHold || a.DisputeStatus == model.AIImageDisputeOpen || !a.ExpiresAt.After(now)) {
			return nil, ErrVideoBillingState
		}
		roles[a.AssetRole] = true
		if a.AssetRole == "content" {
			if a.ParentAssetID != nil || !a.IsBillableOutput {
				return nil, ErrVideoBillingState
			}
			root = a
		}
	}
	if root == nil || root.DurationSeconds == nil || !root.DurationSeconds.IsPositive() {
		return nil, ErrVideoBillingState
	}
	for i := range assets {
		a := &assets[i]
		if a.AssetRole != "content" && (a.ParentAssetID == nil || *a.ParentAssetID != root.ID || a.IsBillableOutput) {
			return nil, ErrVideoBillingState
		}
	}
	return root, nil
}

// loadVideoConfirmedCostTx 重新计算确认摘要，证明计量与成本不是从销售价推导，也没有被其他任务错绑。
func loadVideoConfirmedCostTx(tx *gorm.DB, task *repository.VideoTaskRecord, q *model.AIGatewayQuote) (*model.VideoUsageItem, error) {
	return loadVideoConfirmedOutcomeCostTx(tx, task, q, videogateway.ProviderTaskSucceeded)
}

// 确认终态必须由调用路径明确指定，不能把失败、取消或成功的成本证据互换。
func loadVideoConfirmedOutcomeCostTx(tx *gorm.DB, task *repository.VideoTaskRecord, q *model.AIGatewayQuote, outcome videogateway.ProviderTaskStatus) (*model.VideoUsageItem, error) {
	if task.ProviderCode == nil || task.ProviderTaskID == nil || task.Operation == nil || task.AttemptCount != 1 {
		return nil, ErrVideoBillingState
	}
	// 原账本不因晚到矛盾回执而改写，但任何确认冲突都必须阻断新扣费、退款及最终交付读取。
	if conflict, err := videoHasProviderConflictTx(tx, task.ID); err != nil {
		return nil, err
	} else if conflict {
		return nil, ErrVideoBillingState
	}
	var rows []model.VideoUsageItem
	if err := tx.Where("request_id=? AND source IN ?", task.RequestID, []string{"provider", "provider_cost"}).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) != 2 {
		return nil, ErrVideoBillingState
	}
	var usage, cost *model.VideoUsageItem
	for i := range rows {
		r := &rows[i]
		if !r.UnitSize.Equal(decimal.NewFromInt(1)) || r.MeterType != VideoMeterSeconds || r.UsageUnit != "seconds" {
			return nil, ErrVideoBillingState
		}
		if r.TaskID != task.ID || r.QuoteID != q.ID || r.UserID != task.UserID || r.ProjectID != task.ProjectID || !equalOptionalUint64(r.APIKeyID, task.APIKeyID) || r.Operation == nil || *r.Operation != *task.Operation || r.LogicalModelCode != task.LogicalModelCode || r.Capability != model.AIVideoCapability || r.PriceVersionID == nil || *r.PriceVersionID != q.PriceVersionID || r.VariantHash != q.RequestVariantHash || r.SequenceNo != 0 || r.UnitPrice == nil || r.Amount == nil || r.Currency == nil || *r.Currency != "CNY" || r.EvidenceEventID == nil {
			return nil, ErrVideoBillingState
		}
		if r.Source == "provider" && r.RecordKind == model.AIUsageFact {
			usage = r
		} else if r.Source == "provider_cost" && r.RecordKind == model.AIUsageCostLine {
			cost = r
		} else {
			return nil, ErrVideoBillingState
		}
	}
	if usage == nil || cost == nil || *usage.EvidenceEventID != *cost.EvidenceEventID || !usage.Quantity.Equal(cost.Quantity) || !usage.Amount.IsZero() || !usage.UnitPrice.IsZero() {
		return nil, ErrVideoBillingState
	}
	var event model.VideoFinancialEvent
	if err := tx.First(&event, *cost.EvidenceEventID).Error; err != nil {
		return nil, err
	}
	if event.TaskID != task.ID || event.UserID != task.UserID || event.ProjectID != task.ProjectID || event.EventType != "provider_cost_"+string(outcome) {
		return nil, ErrVideoBillingState
	}
	confirmation := videogateway.ProviderCostConfirmation{ProviderCode: *task.ProviderCode, ProviderTaskID: *task.ProviderTaskID, ExternalEventID: "persisted", Operation: *task.Operation, Outcome: outcome, Quantity: cost.Quantity, UnitPrice: *cost.UnitPrice, Amount: *cost.Amount, Currency: *cost.Currency}
	hash, err := videoProviderConfirmationHash(task.RequestID, confirmation)
	if err != nil || hash != event.FactSHA256 {
		return nil, ErrVideoBillingState
	}
	return cost, nil
}

func validateVideoSettledFinanceTx(tx *gorm.DB, task *repository.VideoTaskRecord, r model.VideoBillingRequest, link model.AIRequestWalletLink, hold billingmodel.WalletHold, price *VideoSettlement) error {
	if err := validateVideoAdjustmentsTx(tx, task); err != nil {
		return err
	}
	if r.BillingStatus != model.AIBillingSettled || r.SettledAmount == nil || !r.SettledAmount.Equal(price.SettledAmount) || hold.Status != billingmodel.HoldStatusSettled || hold.SettledAmount == nil || !hold.SettledAmount.Equal(price.SettledAmount) || link.SettledAmount == nil || !link.SettledAmount.Equal(price.SettledAmount) || link.SettleTransactionID == nil || link.ReleaseTransactionID == nil || hold.SettleTxnID == nil || *hold.SettleTxnID != *link.SettleTransactionID {
		return ErrVideoBillingState
	}
	var consume, unfreeze, freeze billingmodel.WalletTransaction
	if tx.First(&consume, *link.SettleTransactionID).Error != nil || tx.First(&unfreeze, *link.ReleaseTransactionID).Error != nil || tx.First(&freeze, link.HoldTransactionID).Error != nil {
		return ErrVideoBillingState
	}
	if consume.WalletID != hold.WalletID || unfreeze.WalletID != hold.WalletID || freeze.WalletID != hold.WalletID || consume.UserID != task.UserID || unfreeze.UserID != task.UserID || freeze.UserID != task.UserID || consume.Type != "consume" || consume.Direction != "out" || unfreeze.Type != "unfreeze" || unfreeze.Direction != "in" || freeze.Type != "freeze" || freeze.Direction != "out" || !consume.Amount.Equal(price.SettledAmount) || !unfreeze.Amount.Equal(hold.HoldAmount) || !freeze.Amount.Equal(hold.HoldAmount) || !(freeze.ID < unfreeze.ID && unfreeze.ID < consume.ID) || !consume.BalanceAfter.Equal(unfreeze.BalanceAfter.Sub(consume.Amount)) {
		return ErrVideoBillingState
	}
	var facts []model.VideoUsageItem
	if err := tx.Where("request_id=? AND record_kind<>'adjustment'", task.RequestID).Find(&facts).Error; err != nil {
		return err
	}
	if len(facts) != 4 {
		return ErrVideoBillingState
	}
	var sales, usages int
	for _, f := range facts {
		if f.Source != "gateway" {
			continue
		}
		if f.TaskID != task.ID || f.QuoteID != task.QuoteID || f.UserID != task.UserID || f.ProjectID != task.ProjectID || !equalOptionalUint64(f.APIKeyID, task.APIKeyID) || f.Amount == nil || f.UnitPrice == nil || f.SequenceNo != 0 || !f.Quantity.Equal(price.UsageFact.Quantity) {
			return ErrVideoBillingState
		}
		if f.RecordKind == model.AIUsageFact {
			usages++
			if !f.Amount.IsZero() || !f.UnitPrice.IsZero() {
				return ErrVideoBillingState
			}
		} else if f.RecordKind == model.AIUsageSaleLine {
			sales++
			if !f.Amount.Equal(price.SettledAmount) || !f.UnitPrice.Equal(*price.SaleLine.UnitPrice) {
				return ErrVideoBillingState
			}
		} else {
			return ErrVideoBillingState
		}
	}
	if sales != 1 || usages != 1 {
		return ErrVideoBillingState
	}
	var events []model.AIOutboxEvent
	// 先取得请求的全部普通财务事件，再校验聚合类型，避免额外坏事实被查询条件隐藏。
	if err := tx.Where("aggregate_id=? AND event_type<>'video_adjustment_recorded'", task.RequestID).Find(&events).Error; err != nil {
		return err
	}
	expected := map[string]string{"video_billing_held": model.AIBillingHeld, "video_billing_settled": model.AIBillingSettled}
	if r.DeliveryStatus == model.AIDeliveryAvailable {
		expected["video_delivery_available"] = model.AIDeliveryAvailable
	}
	// 有补偿记录时必须同时存在最初pending与required事实，不能靠忽略额外事件通过重放。
	var compensations []model.VideoCompensationTask
	if err := tx.Where("task_type='video_reconcile' AND aggregate_id=?", task.RequestID).Find(&compensations).Error; err != nil {
		return err
	}
	if len(compensations) == 1 {
		if videoCompensationNeedsPending(&compensations[0]) {
			expected["video_settlement_pending"] = model.AIBillingSettlementPending
		}
		expected["video_compensation_required"] = "pending"
	} else if len(compensations) != 0 {
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
		amount := price.SettledAmount
		if event.EventType != "video_billing_settled" && event.EventType != "video_delivery_available" {
			amount = hold.HoldAmount
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal(event.PayloadJSON, &payload) != nil || len(payload) != 6 {
			return ErrVideoBillingState
		}
		for key, want := range map[string]string{"request_id": task.RequestID, "status": status, "amount": amount.StringFixed(8), "currency": "CNY", "operation": *task.Operation} {
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
	return nil
}

func videoSettledResult(task *repository.VideoTaskRecord, r model.VideoBillingRequest, price *VideoSettlement, existing bool) *VideoFinancialResult {
	return &VideoFinancialResult{RequestID: task.RequestID, TaskID: task.PublicID, ExecutionStatus: task.Status, BillingStatus: r.BillingStatus, DeliveryStatus: r.DeliveryStatus, HeldAmount: *r.HeldAmount, SettledAmount: price.SettledAmount, ReleasedAmount: price.ReleaseAmount, Existing: existing}
}
