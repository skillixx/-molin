package service

import (
	"context"
	"database/sql"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// VideoCapacityTaskLedger只在G7完整依赖装配时替代旧Ledger能力面；缺少容量协调器不能静默降级。
type VideoCapacityTaskLedger struct {
	*VideoRepositoryTaskLedger
	execution *VideoCapacityExecutionCoordinator
}

func NewVideoCapacityTaskLedger(base *VideoRepositoryTaskLedger, recovery *repository.VideoCapacityRecoveryRepository, store *RedisVideoCapacityStore, nonceKey *VideoCapacityNonceKey) (*VideoCapacityTaskLedger, error) {
	if base == nil || base.db == nil || recovery == nil || store == nil || nonceKey == nil {
		return nil, ErrVideoGovernanceUnavailable
	}
	return &VideoCapacityTaskLedger{VideoRepositoryTaskLedger: base, execution: NewVideoCapacityExecutionCoordinator(base, recovery, store, nonceKey)}, nil
}

func (l *VideoCapacityTaskLedger) ClaimRunning(ctx context.Context, taskID string, expectedVersion uint64) (videogateway.GatewayTask, error) {
	if l == nil || l.execution == nil {
		return videogateway.GatewayTask{}, ErrVideoGovernanceUnavailable
	}
	return l.execution.PromoteAndPlan(ctx, taskID, expectedVersion)
}

func (l *VideoCapacityTaskLedger) ResumePlannedSubmission(ctx context.Context, taskID string, version uint64) error {
	if l == nil || l.execution == nil {
		return ErrVideoGovernanceUnavailable
	}
	return l.execution.ValidateSubmission(ctx, taskID, version)
}

func (l *VideoCapacityTaskLedger) ValidateProviderSubmission(ctx context.Context, taskID string, version uint64) error {
	if l == nil || l.execution == nil {
		return ErrVideoGovernanceUnavailable
	}
	return l.execution.ConsumeSendPermit(ctx, taskID, version)
}

// ValidateSubmissionClaim使用冻结claim版本计算原提交窗口；不能回退到只接受uninitialized的G5门闩。
func (l *VideoCapacityTaskLedger) ValidateSubmissionClaim(ctx context.Context, taskID string, claimVersion uint64) (time.Time, error) {
	if l == nil || l.execution == nil || claimVersion < 2 {
		return time.Time{}, ErrVideoGovernanceUnavailable
	}
	current, err := l.Load(ctx, taskID)
	if err != nil || current.SubmissionClaimVersion != claimVersion {
		return time.Time{}, ErrVideoGovernanceUnavailable
	}
	if err := l.execution.ValidateSubmission(ctx, taskID, current.Version); err != nil {
		return time.Time{}, err
	}
	var deadline time.Time
	err = l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, l.owner)
		if err != nil || task.SubmissionClaimVersion == nil || *task.SubmissionClaimVersion != claimVersion || !videoCapacityPlanMatches(task, ^uint64(0)) {
			return ErrVideoGovernanceUnavailable
		}
		if err := repository.CheckVideoWorkerLeaseTx(ctx, tx, task); err != nil {
			return err
		}
		deadline, err = videoSubmissionClaimTx(tx, task, claimVersion)
		if err != nil {
			return err
		}
		if task.WorkerLeaseUntil != nil && task.WorkerLeaseUntil.Before(deadline) {
			deadline = *task.WorkerLeaseUntil
		}
		if !l.now().UTC().Before(deadline) {
			return ErrVideoBillingState
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return deadline, err
}

func (l *VideoCapacityTaskLedger) ReleaseLeaseOnce(ctx context.Context, taskID string) (videogateway.GatewayTask, error) {
	if l == nil || l.VideoRepositoryTaskLedger == nil || l.execution == nil {
		return videogateway.GatewayTask{}, ErrVideoGovernanceUnavailable
	}
	// Worker执行租约与Provider容量租约相互独立；Task终态不等于财务、交付和输入均已安全闭合。
	// 容量只能由Rabbit终态协调器或取消/补偿流程在完整事实复核后显式ReleaseTerminal。
	return l.VideoRepositoryTaskLedger.ReleaseLeaseOnce(ctx, taskID)
}

var _ videogateway.VideoTaskLedger = (*VideoCapacityTaskLedger)(nil)
var _ videogateway.VideoRunningAdmissionLedger = (*VideoCapacityTaskLedger)(nil)
var _ videogateway.VideoProviderSubmissionGate = (*VideoCapacityTaskLedger)(nil)
var _ videogateway.VideoPlannedSubmissionResumer = (*VideoCapacityTaskLedger)(nil)
var _ videogateway.VideoSubmissionLedger = (*VideoCapacityTaskLedger)(nil)
