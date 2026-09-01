package service

import (
	"context"
	"errors"
	"math"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/repository"
)

// 调用者已锁原Task；旧命令精确读取原尝试，新命令加入唯一有效尝试或在完整清理后创建后继。
func (s *VideoHTTPService) selectVideoSaveAttemptTx(ctx context.Context, tx *gorm.DB, task *repository.VideoTaskRecord, owner repository.VideoOwner, command *videoAssetSaveCommand) (*videoAssetSave, uint64, *string, error) {
	var op videoAssetSave
	q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id=?", task.ID)
	if command != nil {
		if command.SavePublicID == "" {
			return nil, 0, nil, ErrVideoSaveConflict
		}
		q = q.Where("public_id=?", command.SavePublicID)
	} else {
		q = q.Where("status<>'aborted'")
	}
	err := q.Take(&op).Error
	if err == nil {
		if !sameVideoSaveOwner(&op, task, owner) {
			return nil, 0, nil, repository.ErrVideoTaskNotFound
		}
		return &op, 0, nil, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, 0, nil, err
	}
	if command != nil {
		return nil, 0, nil, ErrVideoSaveConflict
	}
	var history []videoAssetSave
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id=?", task.ID).Order("attempt_no").Find(&history).Error; err != nil {
		return nil, 0, nil, err
	}
	next := uint64(1)
	var previous *string
	for i := range history {
		old := &history[i]
		if !sameVideoSaveOwner(old, task, owner) || old.Status != "aborted" || old.AttemptNo != next || !sameVideoSavePrevious(old.PreviousSaveID, previous) || old.AttemptNo == math.MaxUint64 {
			return nil, 0, nil, ErrVideoSaveConflict
		}
		if err := verifyVideoSaveCleanupTx(ctx, tx, old, s.saveStore); err != nil {
			return nil, 0, nil, err
		}
		previous = &old.PublicID
		next++
	}
	return nil, next, previous, nil
}

// 首次尝试没有前驱，其余尝试必须逐字匹配上一条不可变身份，不能把空串当成NULL。
func sameVideoSavePrevious(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// 恢复只能沿已冻结的执行政策；缺失类型不能由当前配置补证，prepare与finish必须使用同一判断。
func matchesVideoSaveExecutionPolicy(op *videoAssetSave, policy *VideoAssetSavePolicy) bool {
	return op != nil && policy != nil && op.PolicyVersion == policy.Version && op.StorageProductID == policy.StorageProductID && op.QuotaUnit == policy.QuotaUnit && op.StorageEntitlementType != "" && op.StorageEntitlementType == policy.EntitlementType
}
