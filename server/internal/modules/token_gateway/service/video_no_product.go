package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// RecordNoProductOutcome 仅供受信网关在核验回执无Content后调用，不是客户端可写的退款参数。
// 成本与无产物证据同事务追加，崩溃不能只留下可误用的半份取消证明。
func (l *VideoRepositoryTaskLedger) RecordNoProductOutcome(ctx context.Context, taskID string, c videogateway.ProviderCostConfirmation) error {
	if !l.deferDelivery {
		return nil
	}
	if (c.Outcome != videogateway.ProviderTaskFailed && c.Outcome != videogateway.ProviderTaskCancelled) || !c.Quantity.IsZero() || !c.Amount.IsZero() {
		return ErrVideoBillingState
	}
	rejected := false
	err := retryVideoBillingTransaction(ctx, func() error {
		rejected = false
		return l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, l.owner)
			if err != nil {
				return err
			}
			now := l.now().UTC()
			conflict, err := videoHasProviderConflictTx(tx, task.ID)
			if err != nil {
				return err
			}
			if conflict || task.Status == model.AIImageTaskPendingReconcile || task.Status == model.AIImageTaskSucceeded || ((task.Status == model.AIImageTaskCancelled || task.Status == model.AIImageTaskFailed) && task.Status != string(c.Outcome)) {
				// 普通在途回执不能替代待核对决策；保留该确认观察，事务提交后向调用方返回拒绝。
				rejected = true
				return recordVideoProviderConflictTx(ctx, tx, task, l.owner, c, now)
			}
			if err := recordVideoProviderConfirmation(ctx, tx, taskID, l.owner, c, now); err != nil {
				if isVideoConfirmationConflict(err) {
					rejected = true
					return appendVideoProviderConflictEventTx(tx, task, c, now)
				}
				return err
			}
			hash, err := videoProviderConfirmationHash(task.RequestID, c)
			if err != nil {
				return err
			}
			eventID := "vg5_" + videoBillingDigest(task.RequestID+":no_product")
			var old model.VideoFinancialEvent
			err = tx.Where("event_id=?", eventID).First(&old).Error
			if err == nil {
				if old.TaskID != task.ID || old.UserID != task.UserID || old.ProjectID != task.ProjectID || old.EventType != "provider_no_product_confirmed" || old.Source != "worker" || old.FactSHA256 != hash {
					return ErrVideoBillingConflict
				}
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			e := model.VideoFinancialEvent{AIGatewayTaskEvent: model.AIGatewayTaskEvent{EventID: eventID, TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "provider_no_product_confirmed", Source: "worker", SafeDetailJSON: json.RawMessage(`{"reason":"state_advanced"}`), CreatedAt: now}, FactSHA256: hash}
			return tx.Create(&e).Error
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err == nil && rejected {
		return ErrVideoBillingState
	}
	return err
}

func validateVideoNoProductProofTx(tx *gorm.DB, task *repository.VideoTaskRecord, cost *model.VideoUsageItem) error {
	if conflict, err := videoHasProviderConflictTx(tx, task.ID); err != nil {
		return err
	} else if conflict {
		return ErrVideoBillingState
	}
	if cost.EvidenceEventID == nil {
		return ErrVideoBillingState
	}
	var confirmation, proof model.VideoFinancialEvent
	if tx.First(&confirmation, *cost.EvidenceEventID).Error != nil || tx.Where("event_id=?", "vg5_"+videoBillingDigest(task.RequestID+":no_product")).First(&proof).Error != nil {
		return ErrVideoBillingState
	}
	if proof.TaskID != task.ID || proof.UserID != task.UserID || proof.ProjectID != task.ProjectID || proof.EventType != "provider_no_product_confirmed" || proof.Source != "worker" || proof.FactSHA256 != confirmation.FactSHA256 || proof.CreatedAt.Before(confirmation.CreatedAt) {
		return ErrVideoBillingState
	}
	return nil
}
