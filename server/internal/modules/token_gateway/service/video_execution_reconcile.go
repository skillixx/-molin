package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// ReconcileExecution 只读持久化执行/安全/冲突事实安排恢复，不接受调用者提供的金额、原因或Provider结果。
func (s *VideoBillingService) ReconcileExecution(ctx context.Context, taskID string, owner repository.VideoOwner) (string, error) {
	if s == nil || s.db == nil {
		return "", ErrVideoBillingState
	}
	var disposition string
	err := retryVideoBillingTransaction(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, owner)
			if err != nil {
				return err
			}
			disposition, err = s.reconcileVideoExecutionTx(ctx, tx, task, owner)
			return err
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	return disposition, err
}

func (s *VideoBillingService) reconcileVideoExecutionTx(ctx context.Context, tx *gorm.DB, task *repository.VideoTaskRecord, owner repository.VideoOwner) (string, error) {
	conflict, err := videoHasProviderConflictTx(tx, task.ID)
	if err != nil {
		return "", err
	}
	code := ""
	if conflict {
		code = "facts_conflict"
	} else if task.Status == model.AIImageTaskPendingReconcile {
		code = "provider_unknown"
		var root model.AIImageAsset
		err := tx.Where("task_id=? AND asset_role='content'", task.ID).First(&root).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return "", err
		}
		if err == nil {
			code = "media_unavailable"
			var quote model.AIGatewayQuote
			if err := tx.First(&quote, task.QuoteID).Error; err != nil {
				return "", err
			}
			cost, err := loadVideoConfirmedCostTx(tx, task, &quote)
			if err == nil && root.DurationSeconds != nil && !cost.Quantity.Equal(*root.DurationSeconds) {
				code = "facts_conflict"
			}
		}
	} else if task.Status == model.AIImageTaskFailed || task.Status == model.AIImageTaskCancelled {
		request, quote, link, hold, err := loadVideoFinancialFactsTx(tx, task, owner)
		if err != nil {
			return "", err
		}
		// 未提交且已原子释放的取消由网关事实证明闭合，不需要也不能伪造Provider证明。
		if task.Status == model.AIImageTaskCancelled && task.ProviderTaskID == nil && task.BillingStatus == model.AIBillingReleased {
			if err := validateVideoCancelledFactsTx(tx, task, *request, *link, *hold); err != nil {
				return "", err
			}
			return "not_required", nil
		}
		if _, err := loadVideoProviderReleaseProofTx(tx, task, quote); err == nil {
			return "release_ready", nil
		}
		code = "media_unavailable"
		if task.Status == model.AIImageTaskCancelled {
			code = "provider_unknown"
		}
	}
	if code == "" {
		return "not_required", nil
	}
	return s.ensureVideoRecoveryTx(ctx, tx, task, owner, code)
}

func (s *VideoBillingService) ensureVideoRecoveryTx(ctx context.Context, tx *gorm.DB, task *repository.VideoTaskRecord, owner repository.VideoOwner, code string) (string, error) {
	_, _, _, hold, err := loadVideoFinancialFactsTx(tx, task, owner)
	if err != nil {
		return "", err
	}
	if task.BillingStatus != model.AIBillingHeld && task.BillingStatus != model.AIBillingSettlementPending && task.BillingStatus != model.AIBillingSettled && task.BillingStatus != model.AIBillingReleased {
		return "", ErrVideoBillingState
	}
	if (task.BillingStatus == model.AIBillingHeld || task.BillingStatus == model.AIBillingSettlementPending) && hold.Status != billingmodel.HoldStatusHolding {
		return "", ErrVideoBillingState
	}
	now := s.now().UTC()
	// 必须先冻结初始计费态，再推进pending，避免把终态后来发生的核对误认为丢失P事件。
	job, existing, err := repository.NewVideoCompensationRepository(tx).WithClock(s.now).EnsureTx(tx, task.PublicID, owner, code)
	if err != nil {
		return "", err
	}
	if err := s.injectVideoFault("execution_compensation"); err != nil {
		return "", err
	}
	if job.Status == "completed" || job.Status == "dead" || job.Status == "manual_review" {
		// 这是新的人工核对请求，不是假造审核通过或重开旧补偿；原次数、围栏和C事件都保留。
		id := "vg5_" + videoBillingDigest(task.RequestID+":review_required")
		var event model.AIGatewayTaskEvent
		err := tx.Where("event_id=?", id).First(&event).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			event = model.AIGatewayTaskEvent{EventID: id, TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "video_reconciliation_review_required", Source: "system", SafeDetailJSON: json.RawMessage(`{"reason":"state_advanced"}`), CreatedAt: now}
			if err := tx.Create(&event).Error; err != nil {
				return "", err
			}
		} else if err != nil {
			return "", err
		} else if event.TaskID != task.ID || event.UserID != owner.UserID || event.ProjectID != owner.ProjectID || event.EventType != "video_reconciliation_review_required" {
			return "", ErrVideoBillingState
		}
		return "review_required", nil
	}
	if task.BillingStatus == model.AIBillingHeld {
		task, err = repository.NewVideoTaskRepository(tx).TransitionBilling(ctx, videoCancelTransition(task, owner, model.AIBillingSettlementPending, "execution_reconcile_pending", now))
		if err != nil {
			return "", err
		}
	}
	if err := s.injectVideoFault("execution_pending"); err != nil {
		return "", err
	}
	if videoCompensationNeedsPending(job) {
		if err := ensureVideoRecoveryOutboxTx(tx, task, "video_settlement_pending", model.AIBillingSettlementPending, hold.HoldAmount, now); err != nil {
			return "", err
		}
	}
	if err := s.injectVideoFault("execution_pending_outbox"); err != nil {
		return "", err
	}
	if err := s.injectVideoFault("compensation_pending_outbox"); err != nil {
		return "", err
	}
	if err := ensureVideoRecoveryOutboxTx(tx, task, "video_compensation_required", "pending", hold.HoldAmount, now); err != nil {
		return "", err
	}
	if err := s.injectVideoFault("execution_required_outbox"); err != nil {
		return "", err
	}
	if err := s.injectVideoFault("compensation_required_outbox"); err != nil {
		return "", err
	}
	if existing {
		return "existing_active", nil
	}
	return "created", nil
}

func videoCompensationNeedsPending(job *model.VideoCompensationTask) bool {
	if job.InitialBillingStatus == "" {
		return job.OriginErrorCode != "delivery_failed"
	}
	return job.InitialBillingStatus == model.AIBillingHeld || job.InitialBillingStatus == model.AIBillingSettlementPending
}

func ensureVideoRecoveryOutboxTx(tx *gorm.DB, task *repository.VideoTaskRecord, kind, status string, amount decimal.Decimal, now time.Time) error {
	var event model.AIOutboxEvent
	id := "vg5_" + videoBillingDigest(task.RequestID+":"+kind)
	err := tx.Where("event_id=?", id).First(&event).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return createVideoBillingOutboxTx(tx, task.RequestID, kind, status, *task.Operation, amount, now)
	}
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]interface{}{"request_id": task.RequestID, "status": status, "amount": amount.StringFixed(8), "currency": "CNY", "operation": *task.Operation, "version": 1})
	if event.AggregateType != "video_request" || event.AggregateID != task.RequestID || event.EventType != kind || event.Status != model.AIOutboxPending || event.LockedAt != nil || !equalVideoFinancialJSON(event.PayloadJSON, payload) {
		return ErrVideoBillingState
	}
	return nil
}
