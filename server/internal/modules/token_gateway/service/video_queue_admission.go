package service

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var (
	ErrVideoQueueFull             = errors.New("视频生成排队容量已满")
	ErrVideoGovernanceUnavailable = errors.New("视频生成治理暂不可用")
)

// VideoQueueLimitError只公开稳定scope，不返回当前深度或内部策略编号。
type VideoQueueLimitError struct{ Scope string }

func (e *VideoQueueLimitError) Error() string { return ErrVideoQueueFull.Error() }
func (e *VideoQueueLimitError) Unwrap() error { return ErrVideoQueueFull }

type videoQueueAdmission interface {
	AdmitTx(*gorm.DB, repository.VideoOwner, string) error
}

type videoQueueGuardRow struct {
	ID        uint8
	VersionNo uint64
	UpdatedAt time.Time
	// 旧库没有该列时扫描为空，继续兼容；111之后只有uninitialized允许旧G6路径。
	CapacityState string `gorm:"column:capacity_state"`
}

func (videoQueueGuardRow) TableName() string { return "ai_video_queue_admission_guard" }

// MySQLVideoQueueAdmission只实现G6关闭态queued容量，不实现G7 running/Provider/Redis租约。
// 单行门闩序列化全局计数，数量事实始终来自原Task账本，不建立平行计数器。
type videoQueueLimits struct{ User, Project, Global uint64 }

const (
	videoG6QueueUserLimit    uint64 = 2
	videoG6QueueProjectLimit uint64 = 10
	videoG6QueueGlobalLimit  uint64 = 100
)

type MySQLVideoQueueAdmission struct{ limits videoQueueLimits }

func NewMySQLVideoQueueAdmission() *MySQLVideoQueueAdmission {
	return &MySQLVideoQueueAdmission{limits: videoQueueLimits{User: videoG6QueueUserLimit, Project: videoG6QueueProjectLimit, Global: videoG6QueueGlobalLimit}}
}

func (a MySQLVideoQueueAdmission) AdmitTx(tx *gorm.DB, owner repository.VideoOwner, taskID string) error {
	if tx == nil || owner.UserID == 0 || owner.ProjectID == 0 || !videoBillingPublicID.MatchString(taskID) || a.limits.User == 0 || a.limits.Project == 0 || a.limits.Global == 0 {
		return ErrVideoGovernanceUnavailable
	}
	var guard videoQueueGuardRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=1").Take(&guard).Error; err != nil {
		return errors.Join(ErrVideoGovernanceUnavailable, err)
	}
	if guard.VersionNo == 0 {
		return ErrVideoGovernanceUnavailable
	}
	if !videoLegacyCapacityOpen(guard.CapacityState) {
		return ErrVideoGovernanceUnavailable
	}
	statuses := []string{model.AIImageTaskCreated, model.AIImageTaskReserved, model.AIImageTaskQueued}
	count := func(where string, args ...any) (int64, error) {
		var value int64
		err := tx.Model(&model.AIImageTask{}).Where("capability=? AND status IN ?", model.AIVideoCapability, statuses).Where(where, args...).Count(&value).Error
		return value, err
	}
	user, err := count("user_id=?", owner.UserID)
	if err != nil {
		return errors.Join(ErrVideoGovernanceUnavailable, err)
	}
	// 门闩在Task暂态写入后取得；当前事务能看到自身Task，因此只在严格超过上限时拒绝。
	if uint64(user) > a.limits.User {
		return &VideoQueueLimitError{Scope: "user"}
	}
	project, err := count("project_id=?", owner.ProjectID)
	if err != nil {
		return errors.Join(ErrVideoGovernanceUnavailable, err)
	}
	if uint64(project) > a.limits.Project {
		return &VideoQueueLimitError{Scope: "project"}
	}
	global, err := count("1=1")
	if err != nil {
		return errors.Join(ErrVideoGovernanceUnavailable, err)
	}
	if uint64(global) > a.limits.Global {
		return &VideoQueueLimitError{Scope: "global"}
	}
	var current int64
	if err := tx.Model(&model.AIImageTask{}).Where("public_id=? AND user_id=? AND project_id=? AND capability=? AND status IN ?", taskID, owner.UserID, owner.ProjectID, model.AIVideoCapability, statuses).Count(&current).Error; err != nil {
		return errors.Join(ErrVideoGovernanceUnavailable, err)
	}
	if current != 1 {
		return ErrVideoGovernanceUnavailable
	}
	return nil
}

func videoLegacyCapacityOpen(state string) bool { return state == "" || state == "uninitialized" }

// Task已锁定后再读门闩，与创建事务的Task→guard顺序一致；恢复Begin只锁门闩，不反向锁Task。
func ensureLegacyVideoCapacityTx(tx *gorm.DB) error {
	if tx == nil {
		return ErrVideoGovernanceUnavailable
	}
	var guard videoQueueGuardRow
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=1").Take(&guard).Error; err != nil {
		return ErrVideoGovernanceUnavailable
	}
	if guard.VersionNo == 0 || !videoLegacyCapacityOpen(guard.CapacityState) {
		return ErrVideoGovernanceUnavailable
	}
	return nil
}
