package service

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"gorm.io/gorm"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// markVideoSettlementPending 在结算回滚后原子保存待处理状态、唯一补偿和Outbox；不覆盖财务终态。
func (s *VideoBillingService) markVideoSettlementPending(ctx context.Context, taskID string, owner repository.VideoOwner) error {
	return s.markVideoFinancialPending(ctx, taskID, owner, false)
}

func (s *VideoBillingService) markVideoReleasePending(ctx context.Context, taskID string, owner repository.VideoOwner) error {
	return s.markVideoFinancialPending(ctx, taskID, owner, true)
}

func (s *VideoBillingService) markVideoFinancialPending(ctx context.Context, taskID string, owner repository.VideoOwner, release bool) error {
	return retryVideoBillingTransaction(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			tasks := repository.NewVideoTaskRepository(tx)
			task, err := tasks.LockForOwnerTx(tx, taskID, owner)
			if errors.Is(err, repository.ErrVideoTaskNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			eligible := task.Status == model.AIImageTaskSucceeded
			if release {
				eligible = task.Status == model.AIImageTaskFailed || task.Status == model.AIImageTaskCancelled
			}
			if !eligible || (task.BillingStatus != model.AIBillingHeld && task.BillingStatus != model.AIBillingSettlementPending) {
				return nil
			}
			// 这是普通结算/退款失败后的新补记事务，不继承已回滚事务的执行许可。
			// 原context取消可被隔离，但旧证明或租约到期不能借补记创建P/C及补偿任务。
			if err := repository.CheckVideoWorkerLeaseTx(ctx, tx, task); err != nil {
				return err
			}
			_, quote, _, hold, err := loadVideoFinancialFactsTx(tx, task, owner)
			if err != nil {
				return err
			}
			if hold.Status != billingmodel.HoldStatusHolding {
				return ErrVideoBillingState
			}
			if release {
				// 只有已证实可释放的事务故障才建立release_failed；未知成本由独立核对路径处理。
				if _, err := loadVideoProviderReleaseProofTx(tx, task, quote); err != nil {
					return nil
				}
			}
			code := "settlement_failed"
			if release {
				code = "release_failed"
			}
			_, err = s.ensureVideoRecoveryTx(ctx, tx, task, owner, code)
			if err == nil {
				err = repository.CheckVideoWorkerLeaseTx(ctx, tx, task)
			}
			return err
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
}

type VideoCompensationRunResult struct {
	Status    string                `json:"status"`
	Financial *VideoFinancialResult `json:"financial,omitempty"`
	Existing  bool                  `json:"existing"`
}

// VideoCompensationWorker 仅持有数据库财务服务和补偿仓储，构造参数不存在Provider、抓取或消息客户端。
type VideoCompensationWorker struct {
	billing  *VideoBillingService
	repo     *repository.VideoCompensationRepository
	workerID string
}

func NewVideoCompensationWorker(billing *VideoBillingService, workerID string) (*VideoCompensationWorker, error) {
	if billing == nil || billing.db == nil || !videoBillingPublicID.MatchString(workerID) || len(workerID) > 64 {
		return nil, repository.ErrVideoCompensationNotReady
	}
	return &VideoCompensationWorker{billing: billing, repo: repository.NewVideoCompensationRepository(billing.db).WithClock(func() time.Time { return billing.now() }), workerID: workerID}, nil
}

// RunOne 按持久化Task/Quote/Hold/确认成本/媒体恢复财务；缺少证据不猜测释放，也不重新Submit。
func (w *VideoCompensationWorker) RunOne(ctx context.Context, requestID string) (*VideoCompensationRunResult, error) {
	var task model.AIImageTask
	if err := w.billing.db.WithContext(ctx).Where("request_id=? AND capability=?", requestID, model.AIVideoCapability).First(&task).Error; err != nil {
		return nil, err
	}
	owner := repository.VideoOwner{UserID: task.UserID, ProjectID: task.ProjectID, APIKeyID: task.APIKeyID}
	lease, err := w.repo.Claim(ctx, requestID, w.workerID)
	if err != nil {
		if errors.Is(err, repository.ErrVideoCompensationNotReady) {
			job, readErr := w.repo.GetForTask(ctx, task.PublicID, owner)
			if readErr == nil && job.Status == "completed" {
				reconciler := NewVideoReconciliationService(w.billing.db)
				reconciler.now = w.billing.now
				report, e := reconciler.Reconcile(ctx, task.PublicID, owner)
				if e != nil {
					return nil, e
				}
				if !report.Passed {
					return nil, ErrVideoReconciliation
				}
				return &VideoCompensationRunResult{Status: "completed", Existing: true}, nil
			}
		}
		return nil, err
	}
	var financial *VideoFinancialResult
	var recoverErr error
	// 分流依据真实Task终态；恢复入口仍逐项复核资产、确认成本和钱包，不信任补偿错误码。
	if task.Status == model.AIImageTaskFailed || task.Status == model.AIImageTaskCancelled {
		financial, recoverErr = w.billing.RecoverRelease(ctx, task.PublicID, owner, *lease)
		if recoverErr == nil {
			return &VideoCompensationRunResult{Status: "completed", Financial: financial}, nil
		}
	} else {
		financial, recoverErr = w.billing.RecoverSettlement(ctx, task.PublicID, owner, *lease)
	}
	if errors.Is(recoverErr, repository.ErrVideoCompensationLeaseLost) || ctx.Err() != nil {
		return nil, errors.Join(recoverErr, ctx.Err())
	}
	code := "facts_missing"
	if recoverErr == nil {
		// 财务已经单独提交；发布失败只回滚交付事务，不重复结算。完成标记由发布事务原子写入。
		if _, err := w.billing.RecoverDelivery(ctx, task.PublicID, owner, *lease); err == nil {
			financial.DeliveryStatus = model.AIDeliveryAvailable
			return &VideoCompensationRunResult{Status: "completed", Financial: financial}, nil
		} else if errors.Is(err, repository.ErrVideoCompensationLeaseLost) || ctx.Err() != nil {
			return nil, errors.Join(err, ctx.Err())
		}
		code = "delivery_failed"
	}
	err = w.billing.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return w.repo.FinishTx(tx, *lease, "retry", code) })
	if err != nil {
		return nil, err
	}
	job, err := w.repo.GetForTask(ctx, task.PublicID, owner)
	if err != nil {
		return nil, err
	}
	return &VideoCompensationRunResult{Status: job.Status, Financial: financial}, nil
}
