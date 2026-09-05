package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var ErrVideoCancelConflict = errors.New("取消幂等键已绑定另一任务")
var ErrVideoCancelUnavailable = errors.New("取消任务事实暂不可确认")

// 复用平台任务详情；cancel_requested不代表Provider接受，already_terminal不代表本次取消成功。
type VideoCancellationReply struct {
	*VideoTaskDetails
	CancelRequestedAt  *time.Time `json:"cancel_requested_at"`
	CancellationResult string     `json:"cancellation_result"`
	Idempotent         bool       `json:"idempotent"`
}

type videoCancellationCommand struct {
	UserID         uint64    `json:"-"`
	ProjectID      uint64    `json:"-"`
	CommandKind    string    `json:"-"`
	CommandKeyHash string    `json:"-"`
	TaskID         uint64    `json:"-"`
	RequestID      string    `json:"-"`
	APIKeyID       *uint64   `json:"-"`
	InitialResult  string    `json:"-"`
	CreatedAt      time.Time `json:"-"`
}

func (videoCancellationCommand) TableName() string { return "ai_video_cancellation_commands" }

// 两条平台路径归一为同一命令域；所有作用在原任务/财务上的写入与回执同事务提交。
func (s *VideoHTTPService) CancelTask(ctx context.Context, caller VideoCaller, id, key string) (*VideoCancellationReply, error) {
	if s == nil || s.db == nil || s.access == nil || s.billing == nil {
		return nil, ErrVideoCancelUnavailable
	}
	if !videoHTTPIdempotency.MatchString(key) {
		return nil, ErrVideoGenerationIntent
	}
	var reply *VideoCancellationReply
	err := retryVideoBillingTransaction(ctx, func() error {
		reply = nil
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			task, owner, err := s.taskForPlatformTx(ctx, tx, caller, id, false)
			if err != nil {
				return err
			}
			if task.Operation == nil {
				return ErrVideoCancelUnavailable
			}
			if err := s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
				return err
			}
			// 不把Key或路由别名加入命令域；精确来源Key仍由任务授权和回执匹配独立核验。
			hash := videoBillingDigest(fmt.Sprintf("video-cancel:%d:%d:%s", owner.UserID, owner.ProjectID, key))
			var command videoCancellationCommand
			err = tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("user_id=? AND project_id=? AND command_kind='cancel' AND command_key_hash=?", owner.UserID, owner.ProjectID, hash).Take(&command).Error
			replayed := err == nil
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.Join(ErrVideoCancelUnavailable, err)
			}
			if replayed {
				if command.TaskID != task.ID || command.RequestID != task.RequestID || !equalOptionalUint64(command.APIKeyID, owner.APIKeyID) {
					return ErrVideoCancelConflict
				}
			} else {
				// 新命令可能写入意图或退款；原控制面授权之外，仅携带Worker证明时附加执行围栏。
				if err := repository.CheckVideoWorkerContextLeaseTx(ctx, tx, task); err != nil {
					return err
				}
				initial, err := videoCancellationAction(task)
				if err != nil {
					return err
				}
				command = videoCancellationCommand{UserID: owner.UserID, ProjectID: owner.ProjectID, CommandKind: "cancel", CommandKeyHash: hash, TaskID: task.ID, RequestID: task.RequestID, APIKeyID: owner.APIKeyID, InitialResult: initial, CreatedAt: time.Now().UTC()}
				if err := tx.Create(&command).Error; err != nil {
					if repository.IsDuplicateKeyForHandler(err) {
						return ErrVideoCancelConflict
					}
					return errors.Join(ErrVideoCancelUnavailable, err)
				}
				switch initial {
				case "cancelled":
					// 根据已锁状态选择原G5入口；不能把损坏账本的失败降级为只记录意图。
					if _, err := s.billing.cancelBeforeSubmit(ctx, task.PublicID, owner, tx); err != nil {
						return errors.Join(ErrVideoCancelUnavailable, err)
					}
				case "cancel_requested":
					if _, err := repository.NewVideoTaskRepository(tx).RequestCancellation(ctx, task.PublicID, owner, time.Now().UTC()); err != nil {
						return errors.Join(ErrVideoCancelUnavailable, err)
					}
				case "already_terminal":
					// 新取消请求不改已有成功/失败终态，不触碰媒体或退款。
				}
			}
			task, err = repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, task.PublicID, owner)
			if err != nil {
				return err
			}
			result := command.InitialResult
			switch result {
			case "cancelled":
				if task.Status != model.AIImageTaskCancelled || task.BillingStatus != model.AIBillingReleased || task.CancelRequestedAt == nil {
					return ErrVideoCancelUnavailable
				}
			case "cancel_requested":
				if task.CancelRequestedAt == nil {
					return ErrVideoCancelUnavailable
				}
				if task.Status == model.AIImageTaskCancelled && task.BillingStatus == model.AIBillingReleased {
					result = "cancelled"
				} else if videoG4TerminalStatus(task.Status) {
					result = "already_terminal"
				}
			case "already_terminal":
				if !videoG4TerminalStatus(task.Status) {
					return ErrVideoCancelUnavailable
				}
			default:
				return ErrVideoCancelUnavailable
			}
			if result == "cancelled" {
				r, _, link, hold, err := loadVideoFinancialFactsTx(tx, task, owner)
				if err != nil {
					return ErrVideoCancelUnavailable
				}
				if err := validateVideoCancelledFactsTx(tx, task, *r, *link, *hold); err != nil {
					return ErrVideoCancelUnavailable
				}
			}
			detail, err := s.taskDetailsTx(ctx, tx, task, owner)
			if err != nil {
				return err
			}
			if err := s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
				return err
			}
			// 内层退款返回后仍有详情与准入读取，必须在最外层事务结束前复核；只读重放不新增执行权要求。
			if !replayed {
				if err := repository.CheckVideoWorkerContextLeaseTx(ctx, tx, task); err != nil {
					return err
				}
			}
			reply = &VideoCancellationReply{VideoTaskDetails: detail, CancelRequestedAt: task.CancelRequestedAt, CancellationResult: result, Idempotent: replayed}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		return nil, err
	}
	return reply, nil
}

func videoCancellationAction(task *repository.VideoTaskRecord) (string, error) {
	if videoG4TerminalStatus(task.Status) {
		return "already_terminal", nil
	}
	switch task.Status {
	case model.AIImageTaskReserved, model.AIImageTaskQueued:
		return "cancelled", nil
	case model.AIImageTaskSubmitting, model.AIImageTaskSubmitted, model.AIImageTaskProcessing, model.AIImageTaskFetching, model.AIImageTaskStoring, model.AIImageTaskModerating, model.AIImageTaskLabeling, model.AIImageTaskPendingReconcile:
		return "cancel_requested", nil
	default:
		return "", ErrVideoCancelUnavailable
	}
}
