package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/model"
)

var (
	ErrImageTaskNotFound    = errors.New("图片任务不存在")
	ErrImageTaskConflict    = errors.New("图片任务状态已变化")
	ErrImageTaskTransition  = errors.New("图片任务状态流转不允许")
	ErrImageAssetNotFound   = errors.New("图片资产不存在")
	ErrImageAssetConflict   = errors.New("图片资产状态已变化")
	ErrImageAssetAccess     = errors.New("图片资产当前不可访问")
	ErrImageAssetTransition = errors.New("图片资产状态流转不允许")
)

type ImageOwner struct {
	UserID    uint64
	ProjectID uint64
	APIKeyID  *uint64
}

type ImageTaskRepository struct {
	db *gorm.DB
}

func NewImageTaskRepository(db *gorm.DB) *ImageTaskRepository {
	return &ImageTaskRepository{db: db}
}

func (r *ImageTaskRepository) Create(ctx context.Context, task *model.AIImageTask) error {
	if r == nil || r.db == nil || task == nil || task.UserID == 0 || task.ProjectID == 0 {
		return ErrImageTaskNotFound
	}
	return r.db.WithContext(ctx).Create(task).Error
}

func (r *ImageTaskRepository) FindForOwner(ctx context.Context, publicID string, owner ImageOwner) (*model.AIImageTask, error) {
	if r == nil || r.db == nil || owner.UserID == 0 || owner.ProjectID == 0 {
		return nil, ErrImageTaskNotFound
	}
	query := r.db.WithContext(ctx).Where("public_id = ? AND user_id = ? AND project_id = ?", publicID, owner.UserID, owner.ProjectID)
	if owner.APIKeyID != nil {
		query = query.Where("api_key_id = ?", *owner.APIKeyID)
	}
	var task model.AIImageTask
	if err := query.First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrImageTaskNotFound
		}
		return nil, err
	}
	return &task, nil
}

// Transition 使用版本号和原状态双重 CAS，避免并发 worker 把任务推进到两个终态。
func (r *ImageTaskRepository) Transition(ctx context.Context, publicID string, owner ImageOwner, expectedVersion uint64, toStatus string, progress uint8, now time.Time) (*model.AIImageTask, error) {
	task, err := r.FindForOwner(ctx, publicID, owner)
	if err != nil {
		return nil, err
	}
	if task.VersionNo != expectedVersion {
		return nil, ErrImageTaskConflict
	}
	if !imageTaskTransitionAllowed(task.Status, toStatus) || progress < task.Progress || progress > 100 {
		return nil, ErrImageTaskTransition
	}
	updates := map[string]interface{}{
		"status": toStatus, "progress": progress, "version_no": gorm.Expr("version_no + 1"), "updated_at": now,
	}
	if imageTaskTerminal(toStatus) {
		updates["completed_at"] = now
	} else {
		updates["completed_at"] = nil
	}
	result := r.db.WithContext(ctx).Model(&model.AIImageTask{}).
		Where("id = ? AND user_id = ? AND project_id = ? AND status = ? AND version_no = ?", task.ID, owner.UserID, owner.ProjectID, task.Status, expectedVersion).
		Updates(updates)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrImageTaskConflict
	}
	return r.FindForOwner(ctx, publicID, owner)
}

func imageTaskTerminal(status string) bool {
	switch status {
	case model.AIImageTaskSucceeded, model.AIImageTaskFailed, model.AIImageTaskCancelled, model.AIImageTaskExpired:
		return true
	default:
		return false
	}
}

func imageTaskTransitionAllowed(from, to string) bool {
	allowed := map[string]map[string]bool{
		model.AIImageTaskCreated:          {model.AIImageTaskReserved: true, model.AIImageTaskFailed: true, model.AIImageTaskCancelled: true},
		model.AIImageTaskReserved:         {model.AIImageTaskSubmitted: true, model.AIImageTaskFailed: true, model.AIImageTaskCancelled: true},
		model.AIImageTaskSubmitted:        {model.AIImageTaskProcessing: true, model.AIImageTaskPendingReconcile: true, model.AIImageTaskFailed: true, model.AIImageTaskCancelled: true},
		model.AIImageTaskProcessing:       {model.AIImageTaskStoring: true, model.AIImageTaskPendingReconcile: true, model.AIImageTaskFailed: true, model.AIImageTaskCancelled: true},
		model.AIImageTaskStoring:          {model.AIImageTaskModerating: true, model.AIImageTaskPendingReconcile: true, model.AIImageTaskFailed: true},
		model.AIImageTaskModerating:       {model.AIImageTaskSucceeded: true, model.AIImageTaskPendingReconcile: true, model.AIImageTaskFailed: true},
		model.AIImageTaskPendingReconcile: {model.AIImageTaskStoring: true, model.AIImageTaskModerating: true, model.AIImageTaskSucceeded: true, model.AIImageTaskFailed: true, model.AIImageTaskCancelled: true},
	}
	return allowed[from][to]
}
