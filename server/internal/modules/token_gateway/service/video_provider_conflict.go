package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// RecordProviderResultConflict 成本与矛盾观察同事务落库，避免另一在途响应抢在观察持久化前补出无产物证明。
func (l *VideoRepositoryTaskLedger) RecordProviderResultConflict(ctx context.Context, taskID string, c videogateway.ProviderCostConfirmation) error {
	if !l.deferDelivery {
		return nil
	}
	return retryVideoBillingTransaction(ctx, func() error {
		return l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, l.owner)
			if err != nil {
				return err
			}
			return recordVideoProviderConflictTx(ctx, tx, task, l.owner, c, l.now().UTC())
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
}

func recordVideoProviderConflictTx(ctx context.Context, tx *gorm.DB, task *repository.VideoTaskRecord, owner repository.VideoOwner, c videogateway.ProviderCostConfirmation, now time.Time) error {
	if err := appendVideoProviderConflictEventTx(tx, task, c, now); err != nil {
		return err
	}
	// 成本若与原确认不同，只保留原账本和新观察摘要；嵌套事务会撤销半份新成本，不覆盖旧Usage。
	if err := recordVideoProviderConfirmation(ctx, tx, task.PublicID, owner, c, now); err != nil && !isVideoConfirmationConflict(err) {
		return err
	}
	return nil
}

func isVideoConfirmationConflict(err error) bool {
	return errors.Is(err, ErrVideoBillingConflict) || errors.Is(err, repository.ErrVideoUsageConflict)
}

func appendVideoProviderConflictEventTx(tx *gorm.DB, task *repository.VideoTaskRecord, c videogateway.ProviderCostConfirmation, now time.Time) error {
	if task.ProviderCode == nil || task.ProviderTaskID == nil || task.Operation == nil || *task.ProviderCode != c.ProviderCode || *task.ProviderTaskID != c.ProviderTaskID || *task.Operation != c.Operation || task.AttemptCount != 1 {
		return ErrVideoBillingState
	}
	hash, err := videoProviderConfirmationHash(task.RequestID, c)
	if err != nil {
		return err
	}
	eventID := "vg5_" + videoBillingDigest(task.RequestID+":result_conflict:"+hash)
	var old model.VideoFinancialEvent
	err = tx.Where("event_id=?", eventID).First(&old).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		e := model.VideoFinancialEvent{AIGatewayTaskEvent: model.AIGatewayTaskEvent{EventID: eventID, TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "provider_result_conflict", Source: "worker", SafeDetailJSON: json.RawMessage(`{"reason":"state_advanced"}`), CreatedAt: now}, FactSHA256: hash}
		if err := tx.Create(&e).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if old.TaskID != task.ID || old.UserID != task.UserID || old.ProjectID != task.ProjectID || old.EventType != "provider_result_conflict" || old.Source != "worker" || old.FactSHA256 != hash {
		return ErrVideoBillingConflict
	}
	// 观察、首次补偿与Outbox同事务；已完成/耗尽的旧job仅追加人工核对请求，不重开旧任务。
	billing := &VideoBillingService{db: tx, now: func() time.Time { return now }}
	_, err = billing.ensureVideoRecoveryTx(tx.Statement.Context, tx, task, repository.VideoOwner{UserID: task.UserID, ProjectID: task.ProjectID, APIKeyID: task.APIKeyID}, "facts_conflict")
	return err
}

func videoHasProviderConflictTx(tx *gorm.DB, taskID uint64) (bool, error) {
	var count int64
	err := tx.Model(&model.AIGatewayTaskEvent{}).Where("task_id=? AND event_type='provider_result_conflict'", taskID).Count(&count).Error
	return count > 0, err
}
