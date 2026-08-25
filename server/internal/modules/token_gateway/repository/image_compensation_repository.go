package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/token_gateway/model"
)

var (
	ErrImageCompensationLeaseLost = errors.New("图片补偿任务租约已失效")
	ErrImageCompensationBusy      = errors.New("图片补偿任务正在由其他执行器处理")
)

type imageCompensationRequestClaimPlan struct {
	Claim         bool
	Completed     bool
	RestoreStatus string
}

// ImageCompensationRequestClaim 保存人工核对取得的租约及原状态；核对仍未知时必须按该快照恢复。
type ImageCompensationRequestClaim struct {
	TaskID                uint64
	Lease                 time.Time
	RestoreStatus         string
	RestoreNextRetryAt    time.Time
	RestoreLastErrorClass *string
	Completed             bool
}

type ImageCompensationRepository struct {
	db *gorm.DB
}

func NewImageCompensationRepository(db *gorm.DB) *ImageCompensationRepository {
	return &ImageCompensationRepository{db: db}
}

func planImageCompensationRequestClaim(task model.AICompensationTask, staleBefore time.Time) (imageCompensationRequestClaimPlan, error) {
	switch task.Status {
	case "pending", "retry", "dead", "manual_review":
		return imageCompensationRequestClaimPlan{Claim: true, RestoreStatus: task.Status}, nil
	case "completed":
		return imageCompensationRequestClaimPlan{Completed: true}, nil
	case "running":
		if task.LockedAt != nil && !task.LockedAt.Before(staleBefore) {
			return imageCompensationRequestClaimPlan{}, ErrImageCompensationBusy
		}
		// 过期running不能恢复为无主运行态；人工核对仍未知时回到retry等待下一次有界领取。
		return imageCompensationRequestClaimPlan{Claim: true, RestoreStatus: "retry"}, nil
	default:
		return imageCompensationRequestClaimPlan{}, ErrImageCompensationLeaseLost
	}
}

// ClaimRequest 按request_id行锁领取人工核对租约；活跃Worker租约未过期时绝不抢占。
func (r *ImageCompensationRepository) ClaimRequest(ctx context.Context, requestID string, now, staleBefore time.Time) (*ImageCompensationRequestClaim, error) {
	if r == nil || r.db == nil || requestID == "" {
		return nil, ErrImageCompensationLeaseLost
	}
	now = now.UTC().Truncate(time.Second)
	staleBefore = staleBefore.UTC().Truncate(time.Second)
	var claim *ImageCompensationRequestClaim
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task model.AICompensationTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("task_key = ? AND task_type = ? AND aggregate_id = ?", "image:"+requestID, "image_reconcile", requestID).
			First(&task).Error; err != nil {
			return err
		}
		plan, err := planImageCompensationRequestClaim(task, staleBefore)
		if err != nil {
			return err
		}
		if plan.Completed {
			claim = &ImageCompensationRequestClaim{TaskID: task.ID, Completed: true}
			return nil
		}
		if !plan.Claim {
			return ErrImageCompensationLeaseLost
		}
		query := tx.Model(&model.AICompensationTask{}).
			Where("id = ? AND task_type = ? AND status = ?", task.ID, "image_reconcile", task.Status)
		if task.LockedAt == nil {
			query = query.Where("locked_at IS NULL")
		} else {
			query = query.Where("locked_at = ?", *task.LockedAt)
		}
		result := query.Updates(map[string]interface{}{"status": "running", "locked_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrImageCompensationLeaseLost
		}
		restoreNextRetryAt := task.NextRetryAt
		if task.Status == "running" {
			restoreNextRetryAt = now.Add(time.Minute)
		}
		claim = &ImageCompensationRequestClaim{
			TaskID: task.ID, Lease: now, RestoreStatus: plan.RestoreStatus,
			RestoreNextRetryAt: restoreNextRetryAt, RestoreLastErrorClass: task.LastErrorClass,
		}
		return nil
	})
	return claim, err
}

// RestoreRequestClaim 在人工核对仍未知或失败时按租约CAS恢复原状态，避免遗留永久running。
func (r *ImageCompensationRepository) RestoreRequestClaim(ctx context.Context, claim *ImageCompensationRequestClaim) error {
	if r == nil || r.db == nil || claim == nil || claim.TaskID == 0 || claim.Lease.IsZero() || claim.RestoreStatus == "" || claim.Completed {
		return ErrImageCompensationLeaseLost
	}
	result := r.db.WithContext(ctx).Model(&model.AICompensationTask{}).
		Where("id = ? AND task_type = ? AND status = 'running' AND locked_at = ?", claim.TaskID, "image_reconcile", claim.Lease).
		Updates(map[string]interface{}{
			"status": claim.RestoreStatus, "locked_at": nil, "next_retry_at": claim.RestoreNextRetryAt,
			"last_error_class": claim.RestoreLastErrorClass,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrImageCompensationLeaseLost
	}
	return nil
}

func (r *ImageCompensationRepository) CreateTx(tx *gorm.DB, requestID, errorClass string, now time.Time) error {
	if tx == nil || requestID == "" {
		return ErrImageCompensationLeaseLost
	}
	task := model.AICompensationTask{
		TaskKey: "image:" + requestID, TaskType: "image_reconcile", AggregateID: requestID,
		Status: "pending", NextRetryAt: now, LastErrorClass: &errorClass,
	}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "task_key"}}, DoNothing: true}).Create(&task).Error
}

func (r *ImageCompensationRepository) ClaimBatch(ctx context.Context, now, staleBefore time.Time, limit int) ([]model.AICompensationTask, error) {
	if limit <= 0 {
		limit = 50
	}
	now = now.Truncate(time.Second)
	staleBefore = staleBefore.Truncate(time.Second)
	var tasks []model.AICompensationTask
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("task_type = ?", "image_reconcile").
			Where("((status IN ('pending','retry') AND next_retry_at <= ?) OR (status = 'running' AND locked_at < ?))", now, staleBefore).
			Order("id ASC").Limit(limit).Find(&tasks).Error; err != nil {
			return err
		}
		for index := range tasks {
			result := tx.Model(&model.AICompensationTask{}).
				Where("id = ? AND status = ?", tasks[index].ID, tasks[index].Status).
				Updates(map[string]interface{}{"status": "running", "locked_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrImageCompensationLeaseLost
			}
			tasks[index].Status = "running"
			tasks[index].LockedAt = &now
		}
		return nil
	})
	return tasks, err
}

func (r *ImageCompensationRepository) MarkCompleted(ctx context.Context, id uint64, lease, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&model.AICompensationTask{}).
		Where("id = ? AND task_type = ? AND status = 'running' AND locked_at = ?", id, "image_reconcile", lease).
		Updates(map[string]interface{}{"status": "completed", "locked_at": nil, "next_retry_at": now, "last_error_class": nil})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrImageCompensationLeaseLost
	}
	return nil
}

func (r *ImageCompensationRepository) MarkFailure(ctx context.Context, id uint64, lease, next time.Time, errorClass string) error {
	// MySQL按SET顺序求值：先递增次数，再让状态读取新次数，确保第8次失败进入dead。
	result := r.db.WithContext(ctx).Exec(`UPDATE ai_compensation_tasks
SET retry_count = retry_count + 1,
    status = IF(retry_count >= 8, 'dead', 'retry'),
    next_retry_at = ?, locked_at = NULL, last_error_class = ?
WHERE id = ? AND status = 'running' AND locked_at = ?`, next, errorClass, id, lease)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrImageCompensationLeaseLost
	}
	return nil
}
