package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

const videoSubmissionLeaseDuration = 2 * time.Minute

// 提交授权依据原始不可变事件，不借用后续updated_at、轮询时间或取消时间延长租期。
func videoSubmissionClaimTx(tx *gorm.DB, task *repository.VideoTaskRecord, claimVersion uint64) (time.Time, error) {
	var rows []model.AIGatewayTaskEvent
	if err := tx.Where("task_id=? AND event_type='execution_status_changed' AND to_status='submitting'", task.ID).Find(&rows).Error; err != nil {
		return time.Time{}, err
	}
	if len(rows) != 1 {
		return time.Time{}, ErrVideoBillingState
	}
	e := rows[0]
	if e.UserID != task.UserID || e.ProjectID != task.ProjectID || e.Source != "worker" || e.FromStatus == nil || *e.FromStatus != model.AIImageTaskQueued {
		return time.Time{}, ErrVideoBillingState
	}
	if claimVersion != 0 && (claimVersion < 2 || e.EventID != fmt.Sprintf("vid_g4_%s_submitting_%d", task.PublicID, claimVersion-1) || task.VersionNo < claimVersion) {
		return time.Time{}, repository.ErrVideoTaskConflict
	}
	return e.CreatedAt.Add(videoSubmissionLeaseDuration), nil
}

// RecoverExpiredSubmission 不持有Provider，仅把已过期且仍submitting的请求原子安排为待核对。
func (s *VideoBillingService) RecoverExpiredSubmission(ctx context.Context, taskID string, owner repository.VideoOwner) (string, error) {
	if s == nil || s.db == nil {
		return "", ErrVideoBillingState
	}
	var disposition string
	err := retryVideoBillingTransaction(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			tasks := repository.NewVideoTaskRepository(tx)
			task, err := tasks.LockForOwnerTx(tx, taskID, owner)
			if err != nil {
				return err
			}
			if task.Status == model.AIImageTaskPendingReconcile {
				disposition, err = s.reconcileVideoExecutionTx(ctx, tx, task, owner)
				return err
			}
			if task.Status != model.AIImageTaskSubmitting {
				disposition = "not_required"
				return nil
			}
			deadline, err := videoSubmissionClaimTx(tx, task, 0)
			if err != nil {
				return err
			}
			if s.now().UTC().Before(deadline) {
				disposition = "inflight"
				return nil
			}
			task, err = tasks.TransitionExecution(ctx, repository.VideoStateTransition{TaskPublicID: taskID, Owner: owner, ExpectedVersion: task.VersionNo, ToStatus: model.AIImageTaskPendingReconcile, Progress: task.Progress, EventID: "vg5_" + videoBillingDigest(task.RequestID+":submission_expired"), Source: "reconciler", Now: s.now().UTC()})
			if err != nil {
				return err
			}
			disposition, err = s.reconcileVideoExecutionTx(ctx, tx, task, owner)
			return err
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	return disposition, err
}
