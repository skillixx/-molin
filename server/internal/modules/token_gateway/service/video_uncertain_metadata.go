package service

import (
	"context"
	"database/sql"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// advanceUncertainMetadata 的整个重读/CAS/补偿链共用最多5秒的存活上下文。
// 它只操作数据库事实，不解密Prompt、不校验或读取参考图、不抓取媒体，也没有Provider依赖。
func (l *VideoRepositoryTaskLedger) advanceUncertainMetadata(parent context.Context, taskID string, expectedVersion uint64, source string) (videogateway.GatewayTask, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	var result videogateway.GatewayTask
	err := retryVideoBillingTransaction(ctx, func() error {
		return l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			tasks := repository.NewVideoTaskRepository(tx)
			task, err := tasks.LockForOwnerTx(tx, taskID, l.owner)
			if err != nil {
				return err
			}
			if task.VersionNo < expectedVersion {
				return repository.ErrVideoTaskConflict
			}
			if !videoG4TerminalStatus(task.Status) {
				if task.Status != model.AIImageTaskPendingReconcile {
					task, err = tasks.TransitionExecution(ctx, repository.VideoStateTransition{TaskPublicID: taskID, Owner: l.owner, ExpectedVersion: task.VersionNo, ToStatus: model.AIImageTaskPendingReconcile, Progress: task.Progress, EventID: "vg5_" + videoBillingDigest(task.RequestID+":uncertain_execution"), Source: normalizeVideoG4EventSource(source), Now: l.now().UTC()})
					if err != nil {
						return err
					}
				}
				billing := &VideoBillingService{db: tx, now: l.now, fault: l.financialFault}
				if _, err := billing.reconcileVideoExecutionTx(ctx, tx, task, l.owner); err != nil {
					return err
				}
			}
			result = videogateway.GatewayTask{DeferDelivery: true, TaskID: task.PublicID, RequestID: task.RequestID, Status: videogateway.TaskStatus(task.Status), Version: task.VersionNo, CancelRequestedAt: task.CancelRequestedAt}
			if task.Operation != nil {
				result.Operation = *task.Operation
			}
			if task.ProviderCode != nil {
				result.ProviderCode = *task.ProviderCode
			}
			if task.ProviderTaskID != nil {
				result.ProviderTaskID = *task.ProviderTaskID
			}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	return result, mapVideoRepositoryError(err)
}
