package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 证明仅由仓储在成功认领后返回；字段不可由HTTP DTO构造，原始随机令牌不持久化也不序列化。
type VideoArchiveFenceProof struct {
	taskID            uint64
	userID, projectID uint64
	generation        uint64
	token             [32]byte
}

// 代次不是授权令牌；只供已持有仓储证明的协调器与存储围栏同步。
func (p *VideoArchiveFenceProof) Generation() uint64 {
	if p == nil {
		return 0
	}
	return p.generation
}

type VideoArchiveFenceClaim struct {
	TaskPublicID    string
	Owner           VideoOwner
	ExpectedVersion uint64
	InitialPhase    string
	Now             time.Time
	LeaseDuration   time.Duration
}

func archiveTokenDigest(token [32]byte) string {
	sum := sha256.Sum256(token[:])
	return hex.EncodeToString(sum[:])
}

// 围栏过期并不授权普通Worker继续写入；须由新认领者递增代次，旧令牌永远失效。
func CheckVideoArchiveFence(record *VideoTaskRecord, proof *VideoArchiveFenceProof, now time.Time) error {
	if record == nil {
		return ErrVideoTaskConflict
	}
	if record.ArchiveTokenHash == nil {
		if proof != nil {
			return ErrVideoTaskConflict
		}
		return nil
	}
	if proof == nil || now.IsZero() || record.ArchiveGeneration == nil || record.ArchiveLeaseUntil == nil || record.ArchivePhase == nil || !record.ArchiveLeaseUntil.After(now) || proof.taskID != record.ID || proof.userID != record.UserID || proof.projectID != record.ProjectID || proof.generation != *record.ArchiveGeneration || subtle.ConstantTimeCompare([]byte(*record.ArchiveTokenHash), []byte(archiveTokenDigest(proof.token))) != 1 {
		return ErrVideoTaskConflict
	}
	return nil
}

// 认领只更新原Task上的技术围栏及version，不改变执行/计费/交付，不创建第二个任务。
// 管理员权限、原结果证据、原因和前后审计由上层归档协调器在同一事务中校验。
func (r *VideoTaskRepository) ClaimArchiveFence(ctx context.Context, c VideoArchiveFenceClaim) (*VideoArchiveFenceProof, *VideoTaskRecord, error) {
	if r == nil || r.db == nil || c.Now.IsZero() || c.ExpectedVersion == 0 {
		return nil, nil, ErrVideoTaskConflict
	}
	switch c.InitialPhase {
	case "fetching", "storing", "moderating", "labeling":
	default:
		return nil, nil, ErrVideoTaskTransition
	}
	duration := c.LeaseDuration
	if duration == 0 {
		duration = 2 * time.Minute
	}
	if duration < 100*time.Millisecond || duration > 2*time.Minute {
		return nil, nil, ErrVideoTaskConflict
	}
	var grant *VideoArchiveFenceProof
	var result *VideoTaskRecord
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, err := findVideoTaskRecord(tx, c.TaskPublicID, c.Owner, true)
		if err != nil {
			return err
		}
		if record.VersionNo != c.ExpectedVersion || record.AttemptCount != 1 || record.ProviderTaskID == nil || record.ProviderCode == nil {
			return ErrVideoTaskConflict
		}
		switch record.Status {
		case "fetching", "storing", "moderating", "labeling", "pending_reconcile":
		default:
			return ErrVideoTaskTransition
		}
		if record.Status != "pending_reconcile" && record.Status != c.InitialPhase {
			return ErrVideoTaskTransition
		}
		leaseNow := r.archiveNow()
		if leaseNow.IsZero() {
			return ErrVideoTaskConflict
		}
		if record.ArchiveTokenHash != nil && (record.ArchiveLeaseUntil == nil || record.ArchiveLeaseUntil.After(leaseNow)) {
			return ErrVideoTaskConflict
		}
		generation := uint64(1)
		if record.ArchiveGeneration != nil {
			generation = *record.ArchiveGeneration + 1
		}
		if generation == 0 {
			return ErrVideoTaskConflict
		}
		grant = &VideoArchiveFenceProof{taskID: record.ID, userID: record.UserID, projectID: record.ProjectID, generation: generation}
		if _, err := rand.Read(grant.token[:]); err != nil {
			return err
		}
		changed := tx.Table("ai_gateway_tasks").Where("id=? AND version_no=?", record.ID, c.ExpectedVersion).Updates(map[string]any{"archive_generation": generation, "archive_token_hash": archiveTokenDigest(grant.token), "archive_lease_until": leaseNow.Add(duration), "archive_phase": c.InitialPhase, "version_no": gorm.Expr("version_no+1"), "updated_at": c.Now})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrVideoTaskConflict
		}
		if err := appendArchiveFenceEvent(tx, record, c.Owner, generation, record.VersionNo+1, "archive_fence_claimed", c.Now); err != nil {
			return err
		}
		result, err = findVideoTaskRecord(tx, c.TaskPublicID, c.Owner, true)
		if err == nil {
			err = CheckVideoArchiveFence(result, grant, r.archiveNow())
		}
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return grant, result, nil
}

// 技术phase只用于恢复步骤，不能直接将Task标记成功；最终状态仍由原三轴仓储和媒体/成本门禁决定。
func (r *VideoTaskRepository) AdvanceArchivePhase(ctx context.Context, id string, owner VideoOwner, expected uint64, proof *VideoArchiveFenceProof, to string, now time.Time) (*VideoTaskRecord, error) {
	var result *VideoTaskRecord
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, err := findVideoTaskRecord(tx, id, owner, true)
		if err != nil {
			return err
		}
		if proof == nil || record.ArchiveTokenHash == nil || record.VersionNo != expected || CheckVideoArchiveFence(record, proof, r.archiveNow()) != nil {
			return ErrVideoTaskConflict
		}
		allowed := map[string]string{"fetching": "storing", "storing": "moderating", "moderating": "labeling", "labeling": "verified"}
		if record.ArchivePhase == nil || allowed[*record.ArchivePhase] != to {
			return ErrVideoTaskTransition
		}
		changed := tx.Table("ai_gateway_tasks").Where("id=? AND version_no=? AND archive_generation=? AND archive_token_hash=?", record.ID, expected, proof.generation, archiveTokenDigest(proof.token)).Updates(map[string]any{"archive_phase": to, "version_no": gorm.Expr("version_no+1"), "updated_at": now})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrVideoTaskConflict
		}
		if err := appendArchiveFenceEvent(tx, record, owner, proof.generation, expected+1, "archive_phase_advanced", now); err != nil {
			return err
		}
		result, err = findVideoTaskRecord(tx, id, owner, true)
		if err == nil {
			err = CheckVideoArchiveFence(result, proof, r.archiveNow())
		}
		return err
	})
	return result, err
}

// 只能在原任务已经进入安全终态或待核对后退让，不能解除围栏让旧执行中Worker复活。
func (r *VideoTaskRepository) ReleaseArchiveFence(ctx context.Context, id string, owner VideoOwner, expected uint64, proof *VideoArchiveFenceProof, now time.Time) (*VideoTaskRecord, error) {
	var result *VideoTaskRecord
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, err := findVideoTaskRecord(tx, id, owner, true)
		if err != nil {
			return err
		}
		if proof == nil || record.ArchiveTokenHash == nil || record.VersionNo != expected || CheckVideoArchiveFence(record, proof, r.archiveNow()) != nil {
			return ErrVideoTaskConflict
		}
		if !videoExecutionTerminal(record.Status) && record.Status != "pending_reconcile" {
			return ErrVideoTaskTransition
		}
		changed := tx.Table("ai_gateway_tasks").Where("id=? AND version_no=? AND archive_generation=? AND archive_token_hash=?", record.ID, expected, proof.generation, archiveTokenDigest(proof.token)).Updates(map[string]any{"archive_token_hash": nil, "archive_lease_until": nil, "archive_phase": nil, "version_no": gorm.Expr("version_no+1"), "updated_at": now})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrVideoTaskConflict
		}
		if err := appendArchiveFenceEvent(tx, record, owner, proof.generation, expected+1, "archive_fence_released", now); err != nil {
			return err
		}
		result, err = findVideoTaskRecord(tx, id, owner, true)
		if err == nil {
			err = CheckVideoArchiveFence(record, proof, r.archiveNow())
		}
		return err
	})
	return result, err
}

func appendArchiveFenceEvent(tx *gorm.DB, record *VideoTaskRecord, owner VideoOwner, generation, version uint64, kind string, now time.Time) error {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%s", record.PublicID, generation, version, kind)))
	return appendVideoTaskEventTx(tx, record.ID, owner, "vg6_archive_"+hex.EncodeToString(hash[:]), kind, "", "", "worker", json.RawMessage(`{"reason":"state_advanced"}`), now, "")
}
