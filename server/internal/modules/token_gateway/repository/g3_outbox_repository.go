package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/token_gateway/model"
)

var ErrOutboxLeaseLost = errors.New("Outbox 事件租约已失效")

// G3OutboxRepository 通过数据库行锁认领事件，RabbitMQ 不可用时事件仍保留在 MySQL。
type G3OutboxRepository struct {
	db *gorm.DB
}

func NewG3OutboxRepository(db *gorm.DB) *G3OutboxRepository { return &G3OutboxRepository{db: db} }

func (r *G3OutboxRepository) ClaimBatch(ctx context.Context, now, lockBefore time.Time, limit int) ([]model.AIOutboxEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	// 数据库列为秒精度，租约时间必须截断后才能作为后续 CAS 令牌。
	now = now.Truncate(time.Second)
	lockBefore = lockBefore.Truncate(time.Second)
	var events []model.AIOutboxEvent
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("ai_outbox_events AS current").Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			// VID-G5仅形成MySQL事实，尚未授权视频发布器；旧Chat/Image事件仍按原顺序领取。
			Where("current.aggregate_type <> ?", "video_request").
			// 错误聚合类型也不能让视频事实绕过关闭边界；LEFT按字面前缀匹配，不把下划线当通配符。
			Where("LEFT(current.event_type, 6) <> ?", "video_").
			Where("((current.status = ? AND current.next_retry_at <= ?) OR (current.status = ? AND current.locked_at < ?))", model.AIOutboxPending, now, model.AIOutboxPublishing, lockBefore).
			Where(`NOT EXISTS (
				SELECT 1 FROM ai_outbox_events AS predecessor
				WHERE predecessor.aggregate_type = current.aggregate_type
				  AND predecessor.aggregate_id = current.aggregate_id
				  AND predecessor.id < current.id
				  AND predecessor.status <> ?
			)`, model.AIOutboxPublished).
			Order("id ASC").Limit(limit).Find(&events).Error; err != nil {
			return err
		}
		if len(events) == 0 {
			return nil
		}
		ids := make([]uint64, 0, len(events))
		for i := range events {
			ids = append(ids, events[i].ID)
			events[i].Status = model.AIOutboxPublishing
			events[i].LockedAt = &now
		}
		return tx.Model(&model.AIOutboxEvent{}).Where("id IN ?", ids).
			Updates(map[string]interface{}{"status": model.AIOutboxPublishing, "locked_at": now}).Error
	})
	return events, err
}

func (r *G3OutboxRepository) MarkPublished(ctx context.Context, id uint64, lease time.Time, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&model.AIOutboxEvent{}).
		Where("id = ? AND status = ? AND locked_at = ?", id, model.AIOutboxPublishing, lease).
		Updates(map[string]interface{}{"status": model.AIOutboxPublished, "processed_at": now, "locked_at": nil, "last_error_class": nil})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrOutboxLeaseLost
	}
	return nil
}

func (r *G3OutboxRepository) MarkRetry(ctx context.Context, id uint64, lease time.Time, next time.Time, errorClass string, dead bool) error {
	status := model.AIOutboxPending
	if dead {
		status = model.AIOutboxDead
	}
	result := r.db.WithContext(ctx).Model(&model.AIOutboxEvent{}).
		Where("id = ? AND status = ? AND locked_at = ?", id, model.AIOutboxPublishing, lease).
		Updates(map[string]interface{}{
			"status": status, "retry_count": gorm.Expr("retry_count + 1"),
			"next_retry_at": next, "locked_at": nil, "last_error_class": errorClass,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrOutboxLeaseLost
	}
	return nil
}

// RequeueDead 仅供受控运维/对账流程重入 dead 事件；事件 ID 不变，消费者继续按 event_id 幂等。
func (r *G3OutboxRepository) RequeueDead(ctx context.Context, eventID string, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&model.AIOutboxEvent{}).
		Where("event_id = ? AND status = ?", eventID, model.AIOutboxDead).
		Updates(map[string]interface{}{
			"status": model.AIOutboxPending, "retry_count": 0, "next_retry_at": now,
			"locked_at": nil, "processed_at": nil, "last_error_class": "manual_requeue",
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrOutboxLeaseLost
	}
	return nil
}
