package service

import (
	"context"
	"database/sql"
	"math"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// ValidateProviderSubmission 是实际Provider调用紧前的统一持久门；不修改Task、钱包或容量。
// uninitialized仅兼容关闭态下的旧G4-G6路径；恢复开始后必须由后续G7 ready+容量许可入口取代。
func (l *VideoRepositoryTaskLedger) ValidateProviderSubmission(ctx context.Context, taskID string, version uint64) error {
	if l == nil || l.db == nil || ctx == nil || version == 0 || l.db.Statement == nil {
		return ErrVideoGovernanceUnavailable
	}
	if _, nested := l.db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return ErrVideoGovernanceUnavailable
	}
	return l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, l.owner)
		if err != nil {
			return err
		}
		if task.VersionNo != version || task.VersionNo == math.MaxUint64 || task.Status != model.AIImageTaskSubmitting || task.ProviderCode != nil || task.ProviderTaskID != nil || task.AttemptCount != 0 || task.CancelRequestedAt != nil || task.ArchiveTokenHash != nil {
			return ErrVideoBillingState
		}
		if err := ensureLegacyVideoCapacityTx(tx); err != nil {
			return err
		}
		if task.WorkerLeaseVersion > 0 {
			if err := repository.CheckVideoWorkerLeaseTx(ctx, tx, task); err != nil {
				return err
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}
