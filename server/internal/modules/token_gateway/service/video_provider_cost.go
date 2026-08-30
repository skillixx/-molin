package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

func (l *VideoRepositoryTaskLedger) RecordProviderConfirmation(ctx context.Context, taskID string, c videogateway.ProviderCostConfirmation) error {
	if !l.deferDelivery {
		return nil
	}
	return recordVideoConfirmationWithConflict(ctx, l.db, taskID, l.owner, c, l.now().UTC())
}

// RecordProviderConfirmation 只接受已绑定Fake Adapter的确认，不能从Quote成本推测Provider账单。
func (s *VideoBillingService) RecordProviderConfirmation(ctx context.Context, taskID string, owner repository.VideoOwner, c videogateway.ProviderCostConfirmation) error {
	return recordVideoConfirmationWithConflict(ctx, s.db, taskID, owner, c, s.now().UTC())
}

// 确认写入的所有公开入口统一保留异值/相反回执，不能仅返回错误后把矛盾观察一起回滚。
// 始终持有同一Task锁；成本子事务回滚、冲突事件提交后才向调用方返回原冲突错误。
func recordVideoConfirmationWithConflict(ctx context.Context, db *gorm.DB, taskID string, owner repository.VideoOwner, c videogateway.ProviderCostConfirmation, now time.Time) error {
	if db == nil {
		return ErrVideoBillingState
	}
	var rejected error
	err := retryVideoBillingTransaction(ctx, func() error {
		rejected = nil
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, owner)
			if err != nil {
				return err
			}
			if err := recordVideoProviderConfirmation(ctx, tx, taskID, owner, c, now); err != nil {
				if !isVideoConfirmationConflict(err) {
					return err
				}
				rejected = err
				return appendVideoProviderConflictEventTx(tx, task, c, now)
			}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		return err
	}
	return rejected
}

func videoProviderConfirmationHash(requestID string, c videogateway.ProviderCostConfirmation) (string, error) {
	if c.ProviderCode != "fake-native-async" || !videoBillingPublicID.MatchString(c.ProviderTaskID) || !videoBillingPublicID.MatchString(c.ExternalEventID) || c.Currency != "CNY" || (c.Operation != model.AIVideoOperationTextToVideo && c.Operation != model.AIVideoOperationImageToVideo) || (c.Outcome != videogateway.ProviderTaskSucceeded && c.Outcome != videogateway.ProviderTaskFailed && c.Outcome != videogateway.ProviderTaskCancelled) || c.Quantity.IsNegative() || !c.Quantity.Equal(c.Quantity.Round(10)) || c.Quantity.GreaterThan(decimal.NewFromInt(86400)) || c.UnitPrice.IsNegative() || !c.UnitPrice.Equal(c.UnitPrice.Round(8)) || c.UnitPrice.GreaterThan(decimal.NewFromInt(1000000)) || c.Amount.IsNegative() || !c.Amount.Equal(c.Quantity.Mul(c.UnitPrice).RoundCeil(8)) {
		return "", ErrVideoBillingState
	}
	return videogateway.ProviderCostFactSHA256(requestID, c), nil
}

func recordVideoProviderConfirmation(ctx context.Context, db *gorm.DB, taskID string, owner repository.VideoOwner, c videogateway.ProviderCostConfirmation, now time.Time) error {
	if db == nil {
		return ErrVideoBillingState
	}
	return retryVideoBillingTransaction(ctx, func() error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, owner)
			if err != nil {
				return err
			}
			if task.ProviderCode == nil || task.ProviderTaskID == nil || *task.ProviderCode != c.ProviderCode || *task.ProviderTaskID != c.ProviderTaskID || task.Operation == nil || *task.Operation != c.Operation || task.AttemptCount != 1 {
				return ErrVideoBillingState
			}
			hash, err := videoProviderConfirmationHash(task.RequestID, c)
			if err != nil {
				return err
			}
			eventID := "vg5_" + videoBillingDigest(c.ProviderCode+"\x00"+c.ProviderTaskID+"\x00"+c.ExternalEventID)
			var event model.VideoFinancialEvent
			err = tx.Where("event_id=?", eventID).First(&event).Error
			if err == nil {
				if event.TaskID != task.ID || event.UserID != owner.UserID || event.ProjectID != owner.ProjectID || event.FactSHA256 != hash || event.EventType != "provider_cost_"+string(c.Outcome) {
					return ErrVideoBillingConflict
				}
			} else {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				event = model.VideoFinancialEvent{AIGatewayTaskEvent: model.AIGatewayTaskEvent{EventID: eventID, TaskID: task.ID, UserID: owner.UserID, ProjectID: owner.ProjectID, EventType: "provider_cost_" + string(c.Outcome), Source: "worker", SafeDetailJSON: json.RawMessage(`{"reason":"state_advanced"}`), CreatedAt: now}, FactSHA256: hash}
				if err := tx.Create(&event).Error; err != nil {
					return err
				}
			}
			zero := decimal.Zero
			usage := model.AIUsageItem{RecordKind: model.AIUsageFact, Source: "provider", Quantity: c.Quantity, UnitSize: decimal.NewFromInt(1), UnitPrice: &zero, Amount: &zero, Currency: &c.Currency}
			cost := usage
			cost.RecordKind, cost.Source, cost.UnitPrice, cost.Amount = model.AIUsageCostLine, "provider_cost", &c.UnitPrice, &c.Amount
			for _, fact := range []model.AIUsageItem{usage, cost} {
				if _, _, err := repository.NewVideoUsageRepository(tx).AppendEvidenceTx(tx, taskID, owner, fact, now, event.ID); err != nil {
					return err
				}
			}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
}
