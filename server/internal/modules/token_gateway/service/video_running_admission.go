package service

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

const (
	videoG6RunningUserLimit    = 1
	videoG6RunningProjectLimit = 2
	videoG6RunningModelLimit   = 2
)

type videoRunningLimits struct {
	User    int64
	Project int64
	Model   int64
}

func videoG6RunningLimits() videoRunningLimits {
	return videoRunningLimits{User: videoG6RunningUserLimit, Project: videoG6RunningProjectLimit, Model: videoG6RunningModelLimit}
}

var videoRunningStatuses = []string{
	model.AIImageTaskSubmitting,
	model.AIImageTaskSubmitted,
	model.AIImageTaskProcessing,
	model.AIImageTaskFetching,
	model.AIImageTaskStoring,
	model.AIImageTaskModerating,
	model.AIImageTaskLabeling,
}

// ClaimRunning 在同一MySQL门闩和Task事务内裁决用户、Project及逻辑模型运行名额。
// 容量满只保持queued；数据库或门闩异常必须失败关闭，不能绕过后继续调用Provider。
func (l *VideoRepositoryTaskLedger) ClaimRunning(ctx context.Context, taskID string, expectedVersion uint64) (video.GatewayTask, error) {
	if l == nil || l.db == nil {
		return video.GatewayTask{}, video.ErrGatewayTaskNotFound
	}
	if !l.runningAdmission {
		return l.Advance(ctx, taskID, expectedVersion, video.TaskSubmitting, "worker", "state_advanced", nil)
	}
	limits := l.runningLimits
	if limits.User <= 0 || limits.Project <= 0 || limits.Model <= 0 {
		return video.GatewayTask{}, video.ErrGatewayRunningCapacity
	}
	var claimed video.GatewayTask
	err := l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var guard videoQueueGuardRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=1").Take(&guard).Error; err != nil {
			return err
		}
		record, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, l.owner)
		if err != nil {
			return err
		}
		if record.VersionNo != expectedVersion {
			return repository.ErrVideoTaskConflict
		}
		if record.Status != model.AIImageTaskQueued {
			return repository.ErrVideoTaskTransition
		}
		checks := []struct {
			where string
			args  []any
			limit int64
		}{
			{"user_id=?", []any{record.UserID}, limits.User},
			{"project_id=?", []any{record.ProjectID}, limits.Project},
			{"logical_model_code=?", []any{record.LogicalModelCode}, limits.Model},
		}
		for _, check := range checks {
			var count int64
			query := tx.Model(&model.AIImageTask{}).
				Where("capability=? AND operation IN ? AND status IN ?", model.AIVideoCapability, []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo}, videoRunningStatuses).
				Where(check.where, check.args...)
			if err := query.Count(&count).Error; err != nil {
				return err
			}
			if count >= check.limit {
				return video.ErrGatewayRunningCapacity
			}
		}
		claimed, err = l.withDB(tx).advanceOnce(ctx, taskID, expectedVersion, video.TaskSubmitting, "worker", "state_advanced", nil)
		return err
	})
	if err != nil {
		if errors.Is(err, video.ErrGatewayRunningCapacity) {
			return video.GatewayTask{}, video.ErrGatewayRunningCapacity
		}
		return video.GatewayTask{}, mapVideoRepositoryError(err)
	}
	return claimed, nil
}
