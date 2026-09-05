package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	auditmodel "molin/server/internal/modules/audit/model"
)

var (
	ErrVideoCapacityRecoveryUnavailable = errors.New("视频容量恢复协调暂不可用")
	ErrVideoCapacityRecoveryConflict    = errors.New("视频容量恢复代次冲突")
	ErrVideoCapacityRecoveryBusy        = errors.New("视频容量恢复已被占用")
	ErrVideoCapacityRecoveryLost        = errors.New("视频容量恢复持有者已失效")
	ErrVideoCapacityRecoveryExhausted   = errors.New("视频容量恢复版本已耗尽")
)

// 当前仅允许关闭态恢复与失败阻断；不暴露ready发布或Provider调用授权。
type VideoCapacityRecoveryState struct {
	Epoch                   uint64
	State                   string
	Version                 uint64
	PolicyHash, RedisRunID  string
	HeartbeatAt, LeaseUntil time.Time
	SnapshotHash            string
	SnapshotCount           uint32
	ReadyAt                 time.Time
}

type VideoCapacityRecoveryLease struct {
	epoch                            uint64
	owner, nonce, policy, redisRunID string
	until                            time.Time
}

func (p *VideoCapacityRecoveryLease) Epoch() uint64 {
	if p == nil {
		return 0
	}
	return p.epoch
}
func (p *VideoCapacityRecoveryLease) Deadline() time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.until
}

func (VideoCapacityRecoveryLease) String() string   { return "[video capacity recovery lease]" }
func (VideoCapacityRecoveryLease) GoString() string { return "[video capacity recovery lease]" }
func (VideoCapacityRecoveryLease) MarshalJSON() ([]byte, error) {
	return []byte(`{"redacted":true}`), nil
}

type videoCapacityRecoveryRow struct {
	ID                                                                                   uint8
	VersionNo                                                                            uint64
	UpdatedAt                                                                            time.Time
	CapacityEpoch                                                                        uint64
	CapacityState                                                                        string
	CapacityPolicySHA256, CapacityRedisRunID, CapacityRecoveryOwner, CapacityTokenSHA256 *string
	CapacityHeartbeatAt, CapacityLeaseUntil                                              *time.Time
	CapacitySnapshotSHA256                                                               *string
	CapacitySnapshotCount                                                                *uint32
	CapacityReadyAt                                                                      *time.Time
}

func (videoCapacityRecoveryRow) TableName() string { return "ai_video_queue_admission_guard" }

type VideoCapacityRecoveryRepository struct{ db *gorm.DB }

func NewVideoCapacityRecoveryRepository(db *gorm.DB) *VideoCapacityRecoveryRepository {
	return &VideoCapacityRecoveryRepository{db: db}
}

func (r *VideoCapacityRecoveryRepository) Current(ctx context.Context) (*VideoCapacityRecoveryState, error) {
	var view *VideoCapacityRecoveryState
	err := r.withGuard(ctx, "SHARE", func(_ *gorm.DB, row *videoCapacityRecoveryRow, _ time.Time) error {
		view = &VideoCapacityRecoveryState{Epoch: row.CapacityEpoch, State: row.CapacityState, Version: row.VersionNo}
		if row.CapacityEpoch != 0 {
			view.PolicyHash, view.RedisRunID = *row.CapacityPolicySHA256, *row.CapacityRedisRunID
			view.HeartbeatAt, view.LeaseUntil = *row.CapacityHeartbeatAt, *row.CapacityLeaseUntil
			if row.CapacityState == "ready" {
				view.SnapshotHash, view.SnapshotCount, view.ReadyAt = *row.CapacitySnapshotSHA256, *row.CapacitySnapshotCount, *row.CapacityReadyAt
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}
func (r *VideoCapacityRecoveryRepository) Begin(ctx context.Context, expectedEpoch uint64, owner, policyHash, redisRunID string) (*VideoCapacityRecoveryLease, error) {
	if !videoWorkerLeaseOwner.MatchString(owner) || !videoCapacitySHA256.MatchString(policyHash) || !videoCapacityRedisID.MatchString(redisRunID) {
		return nil, ErrVideoCapacityRecoveryUnavailable
	}
	var proof *VideoCapacityRecoveryLease
	err := r.withGuard(ctx, "UPDATE", func(tx *gorm.DB, row *videoCapacityRecoveryRow, now time.Time) error {
		if row.CapacityEpoch != expectedEpoch {
			return ErrVideoCapacityRecoveryConflict
		}
		if row.CapacityState == "recovering" && row.CapacityLeaseUntil.After(now) {
			return ErrVideoCapacityRecoveryBusy
		}
		if row.CapacityEpoch == math.MaxUint64 || row.VersionNo == math.MaxUint64 {
			return ErrVideoCapacityRecoveryExhausted
		}
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return ErrVideoCapacityRecoveryUnavailable
		}
		nonce := hex.EncodeToString(secret)
		tokenHash, until := videoCapacityTokenHash(nonce), now.Add(30*time.Second)
		updated := tx.Model(&videoCapacityRecoveryRow{}).Where("id=1 AND version_no=? AND capacity_epoch=?", row.VersionNo, row.CapacityEpoch).Updates(map[string]any{
			"version_no": gorm.Expr("version_no+1"), "updated_at": now, "capacity_epoch": row.CapacityEpoch + 1, "capacity_state": "recovering",
			"capacity_policy_sha256": policyHash, "capacity_redis_run_id": redisRunID, "capacity_recovery_owner": owner, "capacity_token_sha256": tokenHash, "capacity_heartbeat_at": now, "capacity_lease_until": until,
			"capacity_snapshot_sha256": nil, "capacity_snapshot_count": nil, "capacity_ready_at": nil,
		})
		if err := videoCapacityCAS(updated); err != nil {
			return err
		}
		row.VersionNo++
		row.CapacityEpoch++
		row.CapacityState = "recovering"
		row.CapacityPolicySHA256 = &policyHash
		row.CapacityRedisRunID = &redisRunID
		row.CapacityRecoveryOwner = &owner
		row.CapacityTokenSHA256 = &tokenHash
		row.CapacityHeartbeatAt = &now
		row.CapacityLeaseUntil = &until
		row.CapacitySnapshotSHA256, row.CapacitySnapshotCount, row.CapacityReadyAt = nil, nil, nil
		if err := appendVideoCapacityAudit(tx, row, "claimed", now); err != nil {
			return err
		}
		if err := videoCapacityDeadline(tx, row); err != nil {
			return err
		}
		proof = &VideoCapacityRecoveryLease{epoch: row.CapacityEpoch, owner: owner, nonce: nonce, policy: policyHash, redisRunID: redisRunID, until: until}
		return nil
	})
	// COMMIT未知时不借出证明，也不补偿清除可能已经提交的恢复占用。
	if err != nil {
		return nil, err
	}
	return proof, nil
}
func (r *VideoCapacityRecoveryRepository) Renew(ctx context.Context, proof *VideoCapacityRecoveryLease) (*VideoCapacityRecoveryLease, error) {
	var renewed *VideoCapacityRecoveryLease
	err := r.withGuard(ctx, "UPDATE", func(tx *gorm.DB, row *videoCapacityRecoveryRow, now time.Time) error {
		if !videoCapacityProofMatches(row, proof) || row.CapacityState != "recovering" || !row.CapacityLeaseUntil.After(now) {
			return ErrVideoCapacityRecoveryLost
		}
		if row.VersionNo == math.MaxUint64 {
			return ErrVideoCapacityRecoveryExhausted
		}
		if !now.After(*row.CapacityHeartbeatAt) {
			return ErrVideoCapacityRecoveryUnavailable
		}
		until := now.Add(30 * time.Second)
		if err := videoCapacityCAS(tx.Model(&videoCapacityRecoveryRow{}).Where("id=1 AND version_no=? AND capacity_epoch=? AND capacity_token_sha256=?", row.VersionNo, row.CapacityEpoch, *row.CapacityTokenSHA256).Updates(map[string]any{"version_no": gorm.Expr("version_no+1"), "updated_at": now, "capacity_heartbeat_at": now, "capacity_lease_until": until})); err != nil {
			return err
		}
		row.CapacityHeartbeatAt = &now
		row.CapacityLeaseUntil = &until
		if err := videoCapacityDeadline(tx, row); err != nil {
			return err
		}
		copy := *proof
		copy.until = until
		renewed = &copy
		return nil
	})
	if err != nil {
		return nil, err
	}
	return renewed, nil
}
func (r *VideoCapacityRecoveryRepository) Block(ctx context.Context, proof *VideoCapacityRecoveryLease) error {
	return r.withGuard(ctx, "UPDATE", func(tx *gorm.DB, row *videoCapacityRecoveryRow, now time.Time) error {
		if !videoCapacityProofMatches(row, proof) {
			return ErrVideoCapacityRecoveryLost
		}
		// 已结束重放只读；必须仍属于当前完整证明，不允许旧epoch影响新恢复。
		if row.CapacityState == "blocked" {
			return nil
		}
		if row.CapacityState != "recovering" || !row.CapacityLeaseUntil.After(now) {
			return ErrVideoCapacityRecoveryLost
		}
		if row.VersionNo == math.MaxUint64 {
			return ErrVideoCapacityRecoveryExhausted
		}
		if err := videoCapacityCAS(tx.Model(&videoCapacityRecoveryRow{}).Where("id=1 AND version_no=? AND capacity_epoch=?", row.VersionNo, row.CapacityEpoch).Updates(map[string]any{"version_no": gorm.Expr("version_no+1"), "updated_at": now, "capacity_state": "blocked"})); err != nil {
			return err
		}
		row.VersionNo++
		row.CapacityState = "blocked"
		if err := appendVideoCapacityAudit(tx, row, "blocked", now); err != nil {
			return err
		}
		return videoCapacityDeadline(tx, row)
	})
}

// Validate只是当前持有者诊断；返回成功不发布ready，不为事务外的Provider操作授予权限。
func (r *VideoCapacityRecoveryRepository) Validate(ctx context.Context, proof *VideoCapacityRecoveryLease) error {
	return r.withGuard(ctx, "SHARE", func(_ *gorm.DB, row *videoCapacityRecoveryRow, now time.Time) error {
		if !videoCapacityProofMatches(row, proof) || row.CapacityState != "recovering" || !row.CapacityLeaseUntil.After(now) {
			return ErrVideoCapacityRecoveryLost
		}
		return nil
	})
}

// PublishReady把已stage的快照摘要与同一恢复epoch原子绑定；相同事实重放只读。
func (r *VideoCapacityRecoveryRepository) PublishReady(ctx context.Context, proof *VideoCapacityRecoveryLease, snapshotHash string, snapshotCount uint32) error {
	if !videoCapacitySHA256.MatchString(snapshotHash) || snapshotCount > 102 {
		return ErrVideoCapacityRecoveryUnavailable
	}
	return r.withGuard(ctx, "UPDATE", func(tx *gorm.DB, row *videoCapacityRecoveryRow, now time.Time) error {
		if !videoCapacityProofMatches(row, proof) {
			return ErrVideoCapacityRecoveryLost
		}
		if row.CapacityState == "ready" {
			if row.CapacitySnapshotSHA256 == nil || *row.CapacitySnapshotSHA256 != snapshotHash || row.CapacitySnapshotCount == nil || *row.CapacitySnapshotCount != snapshotCount || row.CapacityReadyAt == nil {
				return ErrVideoCapacityRecoveryConflict
			}
			return nil
		}
		if row.CapacityState != "recovering" || row.CapacityLeaseUntil == nil || !row.CapacityLeaseUntil.After(now) {
			return ErrVideoCapacityRecoveryLost
		}
		if row.VersionNo == math.MaxUint64 {
			return ErrVideoCapacityRecoveryExhausted
		}
		if err := videoCapacityCAS(tx.Model(&videoCapacityRecoveryRow{}).Where("id=1 AND version_no=? AND capacity_epoch=? AND capacity_state='recovering'", row.VersionNo, row.CapacityEpoch).Updates(map[string]any{"version_no": gorm.Expr("version_no+1"), "updated_at": now, "capacity_state": "ready", "capacity_snapshot_sha256": snapshotHash, "capacity_snapshot_count": snapshotCount, "capacity_ready_at": now})); err != nil {
			return err
		}
		row.VersionNo++
		row.CapacityState = "ready"
		row.CapacitySnapshotSHA256, row.CapacitySnapshotCount, row.CapacityReadyAt = &snapshotHash, &snapshotCount, &now
		if err := appendVideoCapacityAudit(tx, row, "ready", now); err != nil {
			return err
		}
		return videoCapacityDeadline(tx, row)
	})
}

// ValidateReady只核对低敏持久状态；调用方仍须独立确认Redis同快照已经activate。
func (r *VideoCapacityRecoveryRepository) ValidateReady(ctx context.Context, epoch uint64, policyHash, redisRunID, snapshotHash string, snapshotCount uint32) error {
	if epoch == 0 || !videoCapacitySHA256.MatchString(policyHash) || !videoCapacityRedisID.MatchString(redisRunID) || !videoCapacitySHA256.MatchString(snapshotHash) || snapshotCount > 102 {
		return ErrVideoCapacityRecoveryUnavailable
	}
	return r.withGuard(ctx, "SHARE", func(_ *gorm.DB, row *videoCapacityRecoveryRow, _ time.Time) error {
		if row.CapacityState != "ready" || row.CapacityEpoch != epoch || row.CapacityPolicySHA256 == nil || *row.CapacityPolicySHA256 != policyHash || row.CapacityRedisRunID == nil || *row.CapacityRedisRunID != redisRunID || row.CapacitySnapshotSHA256 == nil || *row.CapacitySnapshotSHA256 != snapshotHash || row.CapacitySnapshotCount == nil || *row.CapacitySnapshotCount != snapshotCount || row.CapacityReadyAt == nil {
			return ErrVideoCapacityRecoveryLost
		}
		return nil
	})
}

var videoCapacitySHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)
var videoCapacityRedisID = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (r *VideoCapacityRecoveryRepository) withGuard(ctx context.Context, strength string, apply func(*gorm.DB, *videoCapacityRecoveryRow, time.Time) error) error {
	if r == nil || r.db == nil || ctx == nil || r.db.Statement == nil || r.db.Statement.ConnPool == nil {
		return ErrVideoCapacityRecoveryUnavailable
	}
	// sql.Tx和GORM PreparedStmtTX都实现TxCommitter；WithContext/Session不能把事务包装成根连接。
	if _, nested := r.db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return ErrVideoCapacityRecoveryUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := r.db.WithContext(bounded).Transaction(func(tx *gorm.DB) error {
		var row videoCapacityRecoveryRow
		if err := tx.Clauses(clause.Locking{Strength: strength}).First(&row, 1).Error; err != nil {
			return ErrVideoCapacityRecoveryUnavailable
		}
		if !validVideoCapacityRecoveryRow(&row) {
			return ErrVideoCapacityRecoveryUnavailable
		}
		if err := verifyVideoCapacityAudit(tx, &row); err != nil {
			return err
		}
		now, err := videoWorkerNow(tx)
		if err != nil {
			return ErrVideoCapacityRecoveryUnavailable
		}
		return apply(tx, &row, now)
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	for _, known := range []error{ErrVideoCapacityRecoveryUnavailable, ErrVideoCapacityRecoveryConflict, ErrVideoCapacityRecoveryBusy, ErrVideoCapacityRecoveryLost, ErrVideoCapacityRecoveryExhausted} {
		if errors.Is(err, known) {
			return known
		}
	}
	if err != nil {
		return ErrVideoCapacityRecoveryUnavailable
	}
	return nil
}

func validVideoCapacityRecoveryRow(row *videoCapacityRecoveryRow) bool {
	if row.ID != 1 || row.VersionNo == 0 {
		return false
	}
	if row.CapacityEpoch == 0 {
		return row.CapacityState == "uninitialized" && row.CapacityPolicySHA256 == nil && row.CapacityRedisRunID == nil && row.CapacityRecoveryOwner == nil && row.CapacityTokenSHA256 == nil && row.CapacityHeartbeatAt == nil && row.CapacityLeaseUntil == nil && row.CapacitySnapshotSHA256 == nil && row.CapacitySnapshotCount == nil && row.CapacityReadyAt == nil
	}
	base := row.CapacityPolicySHA256 != nil && videoCapacitySHA256.MatchString(*row.CapacityPolicySHA256) && row.CapacityRedisRunID != nil && videoCapacityRedisID.MatchString(*row.CapacityRedisRunID) && row.CapacityRecoveryOwner != nil && videoWorkerLeaseOwner.MatchString(*row.CapacityRecoveryOwner) && row.CapacityTokenSHA256 != nil && videoCapacitySHA256.MatchString(*row.CapacityTokenSHA256) && row.CapacityHeartbeatAt != nil && row.CapacityLeaseUntil != nil && row.CapacityLeaseUntil.Equal(row.CapacityHeartbeatAt.Add(30*time.Second))
	if !base {
		return false
	}
	if row.CapacityState == "ready" {
		return row.CapacitySnapshotSHA256 != nil && videoCapacitySHA256.MatchString(*row.CapacitySnapshotSHA256) && row.CapacitySnapshotCount != nil && *row.CapacitySnapshotCount <= 102 && row.CapacityReadyAt != nil
	}
	return (row.CapacityState == "recovering" || row.CapacityState == "blocked") && row.CapacitySnapshotSHA256 == nil && row.CapacitySnapshotCount == nil && row.CapacityReadyAt == nil
}

func videoCapacityTokenHash(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}
func videoCapacityProofMatches(row *videoCapacityRecoveryRow, p *VideoCapacityRecoveryLease) bool {
	return p != nil && p.epoch != 0 && videoCapacitySHA256.MatchString(p.nonce) && row.CapacityEpoch == p.epoch && row.CapacityRecoveryOwner != nil && *row.CapacityRecoveryOwner == p.owner && row.CapacityPolicySHA256 != nil && *row.CapacityPolicySHA256 == p.policy && row.CapacityRedisRunID != nil && *row.CapacityRedisRunID == p.redisRunID && row.CapacityTokenSHA256 != nil && *row.CapacityTokenSHA256 == videoCapacityTokenHash(p.nonce)
}
func videoCapacityCAS(result *gorm.DB) error {
	if result.Error != nil {
		return ErrVideoCapacityRecoveryUnavailable
	}
	if result.RowsAffected != 1 {
		return ErrVideoCapacityRecoveryConflict
	}
	return nil
}
func videoCapacityDeadline(tx *gorm.DB, row *videoCapacityRecoveryRow) error {
	now, err := videoWorkerNow(tx)
	if err != nil {
		return ErrVideoCapacityRecoveryUnavailable
	}
	if row.CapacityLeaseUntil == nil || !row.CapacityLeaseUntil.After(now) {
		return ErrVideoCapacityRecoveryLost
	}
	return nil
}

type videoCapacityAuditSummary struct {
	Schema        int     `json:"schema"`
	Epoch         string  `json:"epoch"`
	Owner         string  `json:"owner"`
	Policy        string  `json:"policy_sha256"`
	RedisRunID    string  `json:"redis_run_id"`
	TokenHash     string  `json:"token_sha256"`
	Result        string  `json:"result"`
	SnapshotHash  *string `json:"snapshot_sha256,omitempty"`
	SnapshotCount *string `json:"snapshot_count,omitempty"`
}

func videoCapacityAuditData(row *videoCapacityRecoveryRow, result string) videoCapacityAuditSummary {
	data := videoCapacityAuditSummary{Schema: 1, Epoch: strconv.FormatUint(row.CapacityEpoch, 10), Owner: *row.CapacityRecoveryOwner, Policy: *row.CapacityPolicySHA256, RedisRunID: *row.CapacityRedisRunID, TokenHash: *row.CapacityTokenSHA256, Result: result}
	if result == "ready" && row.CapacitySnapshotSHA256 != nil && row.CapacitySnapshotCount != nil {
		count := strconv.FormatUint(uint64(*row.CapacitySnapshotCount), 10)
		data.SnapshotHash = row.CapacitySnapshotSHA256
		data.SnapshotCount = &count
	}
	return data
}
func appendVideoCapacityAudit(tx *gorm.DB, row *videoCapacityRecoveryRow, result string, now time.Time) error {
	body, err := json.Marshal(videoCapacityAuditData(row, result))
	if err != nil {
		return ErrVideoCapacityRecoveryUnavailable
	}
	targetType, targetID, summary := "video_capacity_domain", "video-capacity:"+strconv.FormatUint(row.CapacityEpoch, 10), string(body)
	// 复用原审计表，只写token摘要；审计失败必须撤销同事务的恢复代次变化。
	entry := auditmodel.AuditLog{Module: "token_gateway", Action: "video_capacity_recovery_" + result, TargetType: &targetType, TargetID: &targetID, RequestSummary: &summary, CreatedAt: now.Truncate(time.Second)}
	if err := tx.Create(&entry).Error; err != nil {
		return ErrVideoCapacityRecoveryUnavailable
	}
	return nil
}
func verifyVideoCapacityAudit(tx *gorm.DB, row *videoCapacityRecoveryRow) error {
	if row.CapacityEpoch == 0 {
		return nil
	}
	results := []string{"claimed", "blocked", "ready"}
	for _, result := range results {
		var entries []auditmodel.AuditLog
		target := "video-capacity:" + strconv.FormatUint(row.CapacityEpoch, 10)
		action := "video_capacity_recovery_" + result
		if err := tx.Where("module=? AND action=? AND target_type=? AND target_id=?", "token_gateway", action, "video_capacity_domain", target).Find(&entries).Error; err != nil {
			return ErrVideoCapacityRecoveryUnavailable
		}
		expected := 0
		if result == "claimed" || (result == "blocked" && row.CapacityState == "blocked") || (result == "ready" && row.CapacityState == "ready") {
			expected = 1
		}
		if len(entries) != expected {
			return ErrVideoCapacityRecoveryUnavailable
		}
		if expected == 0 {
			continue
		}
		entry := entries[0]
		if entry.OperatorID != nil || entry.Module != "token_gateway" || entry.Action != action || entry.TargetType == nil || *entry.TargetType != "video_capacity_domain" || entry.TargetID == nil || *entry.TargetID != target || entry.RequestSummary == nil {
			return ErrVideoCapacityRecoveryUnavailable
		}
		var data videoCapacityAuditSummary
		var fields map[string]json.RawMessage
		wantFields := 7
		if result == "ready" {
			wantFields = 9
		}
		if json.Unmarshal([]byte(*entry.RequestSummary), &data) != nil || json.Unmarshal([]byte(*entry.RequestSummary), &fields) != nil || len(fields) != wantFields || !reflect.DeepEqual(data, videoCapacityAuditData(row, result)) {
			return ErrVideoCapacityRecoveryUnavailable
		}
	}
	return nil
}
