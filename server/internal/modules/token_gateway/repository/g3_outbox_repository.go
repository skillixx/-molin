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
	db        *gorm.DB
	videoOnly bool
}

func NewG3OutboxRepository(db *gorm.DB) *G3OutboxRepository { return &G3OutboxRepository{db: db} }

// NewVideoOutboxRepository 显式选择同一共享Outbox中的视频事实，不开启旧Chat/Image发布器的视频权限。
// 仅由视频模块装配调用；构造本身不领取、不发布，也不修改钱包或任务。
func NewVideoOutboxRepository(db *gorm.DB) *G3OutboxRepository {
	return &G3OutboxRepository{db: db, videoOnly: true}
}

// videoWriteScope 防止视频Worker凭其他聚合的ID或租约改写Chat/Image发布状态。
// 旧入口保留既有受控管理重排能力，其领取范围仍不包含视频。
func (r *G3OutboxRepository) videoWriteScope(tx *gorm.DB) *gorm.DB {
	if r.videoOnly {
		return tx.Where("BINARY aggregate_type = ? AND BINARY LEFT(event_type, 6) = ?", "video_request", "video_")
	}
	return tx
}

func (r *G3OutboxRepository) ClaimBatch(ctx context.Context, now, lockBefore time.Time, limit int) ([]model.AIOutboxEvent, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("Outbox存储未装配")
	}
	if limit <= 0 {
		limit = 50
	}
	// 数据库列为秒精度，租约时间必须截断后才能作为后续 CAS 令牌。
	now = now.Truncate(time.Second)
	lockBefore = lockBefore.Truncate(time.Second)
	if r.videoOnly && (now.IsZero() || !lockBefore.Before(now) || limit > 1000) {
		return nil, errors.New("视频Outbox领取参数无效")
	}
	var events []model.AIOutboxEvent
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Table("ai_outbox_events AS current").Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		if r.videoOnly {
			// 聚合与事件前缀必须同时精确匹配；大小写变体不能成为视频发布事实。
			query = query.Where("BINARY current.aggregate_type = ? AND BINARY LEFT(current.event_type, 6) = ?", "video_request", "video_")
		} else {
			// 默认入口继续排除视频；错误聚合不能通过视频前缀绕过关闭边界。
			query = query.Where("current.aggregate_type <> ?", "video_request").Where("LEFT(current.event_type, 6) <> ?", "video_")
		}
		if err := query.
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
		if r.videoOnly {
			for i := range events {
				lease := now
				// DATETIME只有秒精度。重试/人工重排保留上次令牌，时钟回退或同秒重领也必须严格递增。
				// 非publishing的locked_at仅为高水位，不表示仍有活动Worker；超时判定始终同时限定status。
				if previous := events[i].LockedAt; previous != nil && !lease.After(*previous) {
					lease = previous.Add(time.Second)
				}
				result := tx.Model(&model.AIOutboxEvent{}).Where("id = ?", events[i].ID).
					Updates(map[string]interface{}{"status": model.AIOutboxPublishing, "locked_at": lease})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return ErrOutboxLeaseLost
				}
				events[i].Status = model.AIOutboxPublishing
				events[i].LockedAt = &lease
			}
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
	result := r.videoWriteScope(r.db.WithContext(ctx).Model(&model.AIOutboxEvent{})).
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
	result := r.videoWriteScope(r.db.WithContext(ctx).Model(&model.AIOutboxEvent{})).
		Where("id = ? AND status = ? AND locked_at = ?", id, model.AIOutboxPublishing, lease).
		Updates(map[string]interface{}{
			"status": status, "retry_count": gorm.Expr("retry_count + 1"),
			"next_retry_at": next, "locked_at": outboxReleasedLeaseValue(), "last_error_class": errorClass,
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
	result := r.videoWriteScope(r.db.WithContext(ctx).Model(&model.AIOutboxEvent{})).
		Where("event_id = ? AND status = ?", eventID, model.AIOutboxDead).
		Updates(map[string]interface{}{
			"status": model.AIOutboxPending, "retry_count": 0, "next_retry_at": now,
			"locked_at": outboxReleasedLeaseValue(), "processed_at": nil, "last_error_class": "manual_requeue",
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrOutboxLeaseLost
	}
	return nil
}

// outboxReleasedLeaseValue 同时覆盖视频专用入口和旧管理员重排入口，防止任何一条路径清掉防重用令牌。
// 只影响精确视频事实；旧Chat/Image继续写NULL，与既有扫描和管理语义兼容。
func outboxReleasedLeaseValue() clause.Expr {
	return gorm.Expr("CASE WHEN BINARY aggregate_type = ? AND BINARY LEFT(event_type, 6) = ? THEN locked_at ELSE NULL END", "video_request", "video_")
}
