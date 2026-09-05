package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
)

var (
	ErrVideoWorkerLeaseBusy        = errors.New("视频任务执行租约已被占用")
	ErrVideoWorkerLeaseLost        = errors.New("视频任务执行租约已失效")
	ErrVideoWorkerLeaseUnavailable = errors.New("视频任务执行租约不可用")
)

const VideoWorkerLeaseDuration = 30 * time.Second

var videoWorkerLeaseOwner = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// 证明只能由成功认领/续期返回，不能通过消息或HTTP字段构造；业务版本与租约代次相互独立。
type VideoWorkerLease struct {
	taskID        uint64
	publicID      string
	owner         VideoOwner
	worker, stage string
	version       uint64
	until         time.Time
}

func (p *VideoWorkerLease) Version() uint64 {
	if p == nil {
		return 0
	}
	return p.version
}
func (p *VideoWorkerLease) Deadline() time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.until
}
func (VideoWorkerLease) String() string   { return "[video-worker-lease]" }
func (VideoWorkerLease) GoString() string { return "[video-worker-lease]" }

type videoWorkerLeaseContextKey struct{}

// WithVideoWorkerLease仅传播仓储授予的内部证明，取消隔离的派生context仍保留原fencing身份。
func WithVideoWorkerLease(ctx context.Context, p *VideoWorkerLease) context.Context {
	if ctx == nil || p == nil {
		return ctx
	}
	return context.WithValue(ctx, videoWorkerLeaseContextKey{}, p)
}

type videoWorkerLeaseEvent struct {
	model.AIGatewayTaskEvent `gorm:"embedded"`
	WorkerLeaseVersion       uint64 `gorm:"column:worker_lease_version" json:"-"`
	WorkerLeaseOwner         string `gorm:"column:worker_lease_owner" json:"-"`
	WorkerLeaseStage         string `gorm:"column:worker_lease_stage" json:"-"`
}

func (videoWorkerLeaseEvent) TableName() string { return "ai_gateway_task_events" }

type VideoWorkerLeaseRepository struct{ db *gorm.DB }

func NewVideoWorkerLeaseRepository(db *gorm.DB) *VideoWorkerLeaseRepository {
	return &VideoWorkerLeaseRepository{db: db}
}

// 数据库时钟在Task锁之后取得，不能用请求到达时间或各实例本机时钟越过实际期限。
func videoWorkerNow(tx *gorm.DB) (time.Time, error) {
	var row struct {
		ClockNow time.Time `gorm:"column:clock_now"`
	}
	if err := tx.Raw("SELECT UTC_TIMESTAMP(6) AS clock_now").Scan(&row).Error; err != nil || row.ClockNow.IsZero() {
		return time.Time{}, ErrVideoWorkerLeaseUnavailable
	}
	return row.ClockNow.UTC(), nil
}

func videoWorkerEventID(taskID string, version uint64, kind string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", taskID, version, kind)))
	return "vg7_worker_" + hex.EncodeToString(sum[:])
}

func videoWorkerHistory(tx *gorm.DB, t *VideoTaskRecord) error {
	if t.WorkerLeaseVersion == 0 {
		if t.WorkerLeaseActive || t.WorkerLeaseOwner != nil || t.WorkerHeartbeatAt != nil || t.WorkerLeaseUntil != nil || t.WorkerStage != nil {
			return ErrVideoWorkerLeaseLost
		}
		return nil
	}
	if t.WorkerLeaseOwner == nil || !videoWorkerLeaseOwner.MatchString(*t.WorkerLeaseOwner) || t.WorkerStage == nil || t.WorkerHeartbeatAt == nil || t.WorkerLeaseUntil == nil || !t.WorkerLeaseUntil.Equal(t.WorkerHeartbeatAt.Add(VideoWorkerLeaseDuration)) {
		return ErrVideoWorkerLeaseLost
	}
	for _, kind := range []string{"video_worker_lease_claimed", "video_worker_lease_released"} {
		if kind == "video_worker_lease_released" && t.WorkerLeaseActive {
			continue
		}
		var event videoWorkerLeaseEvent
		expectedID := videoWorkerEventID(t.PublicID, t.WorkerLeaseVersion, kind)
		if err := tx.Where("event_id=?", expectedID).Take(&event).Error; err != nil {
			return ErrVideoWorkerLeaseLost
		}
		// MySQL查询可能使用不区分大小写的排序规则，返回后仍须逐字节核对冻结身份。
		if event.EventID != expectedID || event.TaskID != t.ID || event.UserID != t.UserID || event.ProjectID != t.ProjectID || event.EventType != kind || event.Source != "worker" || event.WorkerLeaseVersion != t.WorkerLeaseVersion || event.WorkerLeaseOwner != *t.WorkerLeaseOwner || event.WorkerLeaseStage != *t.WorkerStage {
			return ErrVideoWorkerLeaseLost
		}
	}
	return nil
}

func videoWorkerProofMatches(t *VideoTaskRecord, p *VideoWorkerLease, now time.Time) bool {
	return p != nil && t != nil && t.WorkerLeaseActive && t.WorkerLeaseOwner != nil && t.WorkerStage != nil && t.WorkerLeaseUntil != nil && t.WorkerLeaseUntil.After(now) && p.taskID == t.ID && p.publicID == t.PublicID && p.owner.UserID == t.UserID && p.owner.ProjectID == t.ProjectID && equalVideoWorkerKey(p.owner.APIKeyID, t.APIKeyID) && p.worker == *t.WorkerLeaseOwner && p.stage == *t.WorkerStage && p.version == t.WorkerLeaseVersion
}
func equalVideoWorkerKey(a, b *uint64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func videoWorkerProof(t *VideoTaskRecord) *VideoWorkerLease {
	owner := VideoOwner{UserID: t.UserID, ProjectID: t.ProjectID}
	if t.APIKeyID != nil {
		id := *t.APIKeyID
		owner.APIKeyID = &id
	}
	return &VideoWorkerLease{taskID: t.ID, publicID: t.PublicID, owner: owner, worker: *t.WorkerLeaseOwner, stage: *t.WorkerStage, version: t.WorkerLeaseVersion, until: *t.WorkerLeaseUntil}
}

func appendVideoWorkerEvent(tx *gorm.DB, t *VideoTaskRecord, kind string, now time.Time) error {
	e := videoWorkerLeaseEvent{AIGatewayTaskEvent: model.AIGatewayTaskEvent{EventID: videoWorkerEventID(t.PublicID, t.WorkerLeaseVersion, kind), TaskID: t.ID, UserID: t.UserID, ProjectID: t.ProjectID, EventType: kind, Source: "worker", SafeDetailJSON: json.RawMessage(`{"reason":"state_advanced"}`), CreatedAt: now}, WorkerLeaseVersion: t.WorkerLeaseVersion, WorkerLeaseOwner: *t.WorkerLeaseOwner, WorkerLeaseStage: *t.WorkerStage}
	return tx.Create(&e).Error
}

func (r *VideoWorkerLeaseRepository) bounded(ctx context.Context, fn func(*gorm.DB) error) error {
	if r == nil || r.db == nil || ctx == nil {
		return ErrVideoWorkerLeaseUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := r.db.WithContext(ctx).Transaction(fn)
	if err == nil || errors.Is(err, ErrVideoWorkerLeaseBusy) || errors.Is(err, ErrVideoWorkerLeaseLost) || errors.Is(err, ErrVideoTaskNotFound) {
		return err
	}
	return ErrVideoWorkerLeaseUnavailable
}

// Claim只占用原Task的操作性租约，绝不修改业务version_no、执行状态、Provider尝试数或财务。
func (r *VideoWorkerLeaseRepository) Claim(ctx context.Context, id string, owner VideoOwner, worker, stage string) (*VideoWorkerLease, error) {
	if !videoWorkerLeaseOwner.MatchString(worker) || (stage != "submit" && stage != "poll" && stage != "fetch") {
		return nil, ErrVideoWorkerLeaseUnavailable
	}
	var proof *VideoWorkerLease
	err := r.bounded(ctx, func(tx *gorm.DB) error {
		t, err := findVideoTaskRecord(tx, id, owner, true)
		if err != nil {
			return err
		}
		if t.PublicID != id {
			return ErrVideoTaskNotFound
		}
		var intent struct{ CommandKind string }
		if tx.Table("ai_requests").Select("command_kind").Where("request_id=?", t.RequestID).Take(&intent).Error != nil || intent.CommandKind != "create_video" {
			return ErrVideoTaskNotFound
		}
		if err := videoWorkerHistory(tx, t); err != nil {
			return err
		}
		now, err := videoWorkerNow(tx)
		if err != nil {
			return err
		}
		if t.ArchiveTokenHash != nil || (t.WorkerLeaseActive && t.WorkerLeaseUntil.After(now)) {
			return ErrVideoWorkerLeaseBusy
		}
		version := t.WorkerLeaseVersion + 1
		if version == 0 {
			return ErrVideoWorkerLeaseLost
		}
		until := now.Add(VideoWorkerLeaseDuration)
		change := tx.Table("ai_gateway_tasks").Where("id=? AND version_no=? AND lease_version=?", t.ID, t.VersionNo, t.WorkerLeaseVersion).Updates(map[string]interface{}{"lease_owner": worker, "lease_version": version, "heartbeat_at": now, "lease_until": until, "worker_stage": stage, "worker_lease_active": 1})
		if change.Error != nil {
			return change.Error
		}
		if change.RowsAffected != 1 {
			return ErrVideoWorkerLeaseLost
		}
		t.WorkerLeaseOwner = &worker
		t.WorkerLeaseVersion = version
		t.WorkerHeartbeatAt = &now
		t.WorkerLeaseUntil = &until
		t.WorkerStage = &stage
		t.WorkerLeaseActive = true
		if err := appendVideoWorkerEvent(tx, t, "video_worker_lease_claimed", now); err != nil {
			return err
		}
		proof = videoWorkerProof(t)
		end, err := videoWorkerNow(tx)
		if err != nil {
			return err
		}
		if !videoWorkerProofMatches(t, proof, end) {
			return ErrVideoWorkerLeaseLost
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return proof, nil
}

func (r *VideoWorkerLeaseRepository) withProof(ctx context.Context, p *VideoWorkerLease, fn func(*gorm.DB, *VideoTaskRecord, time.Time) error) error {
	if p == nil {
		return ErrVideoWorkerLeaseLost
	}
	return r.bounded(ctx, func(tx *gorm.DB) error {
		t, err := findVideoTaskRecord(tx, p.publicID, p.owner, true)
		if err != nil {
			return err
		}
		now, err := videoWorkerNow(tx)
		if err != nil {
			return err
		}
		if !videoWorkerProofMatches(t, p, now) {
			return ErrVideoWorkerLeaseLost
		}
		if err := videoWorkerHistory(tx, t); err != nil {
			return err
		}
		return fn(tx, t, now)
	})
}

// Renew保持代次与owner不变，心跳只延长当前持有者的30秒租期，不与业务CAS争用version_no。
func (r *VideoWorkerLeaseRepository) Renew(ctx context.Context, p *VideoWorkerLease) (*VideoWorkerLease, error) {
	var result *VideoWorkerLease
	err := r.withProof(ctx, p, func(tx *gorm.DB, t *VideoTaskRecord, now time.Time) error {
		until := now.Add(VideoWorkerLeaseDuration)
		change := tx.Table("ai_gateway_tasks").Where("id=? AND lease_version=? AND lease_owner=? AND worker_lease_active=1", t.ID, p.version, p.worker).Updates(map[string]interface{}{"heartbeat_at": now, "lease_until": until})
		if change.Error != nil {
			return change.Error
		}
		// 同一数据库微秒的重复心跳允许零变化；行已被本事务锁定并验证，不能据此复活失效租约。
		t.WorkerHeartbeatAt = &now
		t.WorkerLeaseUntil = &until
		result = videoWorkerProof(t)
		end, err := videoWorkerNow(tx)
		if err != nil {
			return err
		}
		if !videoWorkerProofMatches(t, result, end) {
			return ErrVideoWorkerLeaseLost
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Release只退出当前技术阶段，保留owner/代次/心跳和截止历史，不释放TaskInput执行租约或Hold。
func (r *VideoWorkerLeaseRepository) Release(ctx context.Context, p *VideoWorkerLease) error {
	return r.withProof(ctx, p, func(tx *gorm.DB, t *VideoTaskRecord, now time.Time) error {
		change := tx.Table("ai_gateway_tasks").Where("id=? AND lease_version=? AND lease_owner=? AND worker_lease_active=1", t.ID, p.version, p.worker).Update("worker_lease_active", 0)
		if change.Error != nil {
			return change.Error
		}
		if change.RowsAffected != 1 {
			return ErrVideoWorkerLeaseLost
		}
		if err := appendVideoWorkerEvent(tx, t, "video_worker_lease_released", now); err != nil {
			return err
		}
		end, err := videoWorkerNow(tx)
		if err != nil {
			return err
		}
		if !t.WorkerLeaseUntil.After(end) {
			return ErrVideoWorkerLeaseLost
		}
		return nil
	})
}

func (r *VideoWorkerLeaseRepository) Validate(ctx context.Context, p *VideoWorkerLease) error {
	return r.withProof(ctx, p, func(_ *gorm.DB, _ *VideoTaskRecord, _ time.Time) error { return nil })
}

// CheckVideoWorkerLeaseTx供已锁定Task的Worker写入边界调用；未进入G7租约的历史任务继续兼容。
func CheckVideoWorkerLeaseTx(ctx context.Context, tx *gorm.DB, t *VideoTaskRecord) error {
	if ctx == nil || tx == nil || t == nil {
		return ErrVideoWorkerLeaseLost
	}
	p, _ := ctx.Value(videoWorkerLeaseContextKey{}).(*VideoWorkerLease)
	if t.WorkerLeaseVersion == 0 && p == nil {
		return nil
	}
	now, err := videoWorkerNow(tx)
	if err != nil {
		return err
	}
	if !videoWorkerProofMatches(t, p, now) {
		return ErrVideoWorkerLeaseLost
	}
	return videoWorkerHistory(tx, t)
}

// CheckVideoWorkerContextLeaseTx仅供已完成原控制面授权及Task锁定的共享入口附加Worker围栏。
// 无私有证明仅表示不附加此限制，不授予用户/管理权限；普通Worker专属写入仍必须调用强制Check。
func CheckVideoWorkerContextLeaseTx(ctx context.Context, tx *gorm.DB, t *VideoTaskRecord) error {
	if ctx == nil || tx == nil || t == nil {
		return ErrVideoWorkerLeaseLost
	}
	raw := ctx.Value(videoWorkerLeaseContextKey{})
	if raw == nil {
		return nil
	}
	proof, ok := raw.(*VideoWorkerLease)
	// 有key但值无效不能降级为控制面无证明路径，尤其不能借零值证明放行历史零代次任务。
	if !ok || proof == nil || proof.version == 0 || proof.taskID == 0 {
		return ErrVideoWorkerLeaseLost
	}
	return CheckVideoWorkerLeaseTx(ctx, tx, t)
}
