package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	auditmodel "molin/server/internal/modules/audit/model"
	auditrepo "molin/server/internal/modules/audit/repository"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var ErrVideoAdminCommandInvalid = errors.New("视频管理命令参数无效")
var ErrVideoAdminCommandConflict = errors.New("视频管理命令版本或幂等意图冲突")

type VideoAdminCancelCommand struct {
	Caller                         VideoCaller `json:"-"`
	TaskID, IdempotencyKey, Reason string      `json:"-"`
	VersionNo                      uint64      `json:"-"`
}
type VideoAdminCancellationReply struct {
	*VideoAdminTaskDetails
	CancelRequestedAt  *time.Time `json:"cancel_requested_at"`
	CancellationResult string     `json:"cancellation_result"`
	Idempotent         bool       `json:"idempotent"`
}

// 私有权限能力只能由管理服务在本事务内构造；消费者重新核验身份，不能被布尔参数代替。
type videoAdminCancellationGrant struct {
	admin   *VideoAdminService
	tx      *gorm.DB
	actor   VideoCaller
	taskID  string
	owner   repository.VideoOwner
	version uint64
}

func (g *videoAdminCancellationGrant) authorize(ctx context.Context, tx *gorm.DB, t *repository.VideoTaskRecord, owner repository.VideoOwner) error {
	if g == nil || g.admin == nil || g.tx != tx || t == nil || g.taskID != t.PublicID || g.version != t.VersionNo || g.owner.UserID != owner.UserID || g.owner.ProjectID != owner.ProjectID || !equalOptionalUint64(g.owner.APIKeyID, owner.APIKeyID) {
		return ErrVideoAdminForbidden
	}
	return g.admin.authorizeTx(ctx, tx, g.actor, "ai_gateway:task_manage")
}

type videoAdminCancellationRecord struct {
	ActorUserID                  uint64  `gorm:"primaryKey" json:"-"`
	CommandKeyHash               string  `gorm:"primaryKey" json:"-"`
	TaskID                       uint64  `json:"-"`
	RequestID                    string  `json:"-"`
	UserID, ProjectID            uint64  `json:"-"`
	APIKeyID                     *uint64 `json:"-"`
	InitialVersion, FinalVersion uint64  `json:"-"`
	InitialResult                string  `json:"-"`
	VideoAdminReasonEnvelope     `gorm:"embedded" json:"-"`
	BeforeAuditID, AfterAuditID  uint64    `json:"-"`
	CreatedAt                    time.Time `json:"-"`
}

func (videoAdminCancellationRecord) TableName() string { return "ai_video_admin_cancellation_commands" }

type videoAdminCancelAuditSummary struct {
	CommandKeyHash string `json:"command_key_hash"`
	ReasonHMAC     string `json:"reason_hmac"`
	ReasonLength   uint32 `json:"reason_length"`
	RequestID      string `json:"request_id"`
	InitialVersion uint64 `json:"initial_version"`
	CurrentVersion uint64 `json:"current_version"`
	Result         string `json:"result"`
}

func videoAdminCancelAuditData(c videoAdminCancellationRecord, before bool) videoAdminCancelAuditSummary {
	d := videoAdminCancelAuditSummary{CommandKeyHash: c.CommandKeyHash, ReasonHMAC: c.ReasonHMAC, ReasonLength: c.ReasonLength, RequestID: c.RequestID, InitialVersion: c.InitialVersion, CurrentVersion: c.FinalVersion, Result: c.InitialResult}
	if before {
		d.CurrentVersion = c.InitialVersion
		d.Result = "requested"
	}
	return d
}
func writeVideoAdminCancelAudit(ctx context.Context, tx *gorm.DB, c videoAdminCancellationRecord, taskID string, before bool) (uint64, error) {
	body, err := json.Marshal(videoAdminCancelAuditData(c, before))
	if err != nil {
		return 0, err
	}
	action := "video_admin_cancel_after"
	if before {
		action = "video_admin_cancel_before"
	}
	target, summary := "video_task", string(body)
	log := auditmodel.AuditLog{OperatorID: &c.ActorUserID, Module: "token_gateway", Action: action, TargetType: &target, TargetID: &taskID, RequestSummary: &summary, CreatedAt: time.Now().UTC()}
	if err := auditrepo.NewAuditLogRepository(tx).CreateWithTx(ctx, tx, &log); err != nil {
		return 0, err
	}
	return log.ID, nil
}
func verifyVideoAdminCancelAudit(tx *gorm.DB, c videoAdminCancellationRecord, taskID string) error {
	for _, before := range []bool{true, false} {
		id, action := c.AfterAuditID, "video_admin_cancel_after"
		if before {
			id, action = c.BeforeAuditID, "video_admin_cancel_before"
		}
		var row auditmodel.AuditLog
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=?", id).Take(&row).Error; err != nil {
			// 保留驱动错误链，1213/1205只交给最外层完整事务重试，不能在此吞掉或重试保存点。
			return errors.Join(ErrVideoCancelUnavailable, err)
		}
		if row.OperatorID == nil || *row.OperatorID != c.ActorUserID || row.Module != "token_gateway" || row.Action != action || row.TargetType == nil || *row.TargetType != "video_task" || row.TargetID == nil || *row.TargetID != taskID || row.RequestSummary == nil {
			return ErrVideoCancelUnavailable
		}
		var data videoAdminCancelAuditSummary
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(*row.RequestSummary), &data) != nil || json.Unmarshal([]byte(*row.RequestSummary), &fields) != nil || len(fields) != 7 || !reflect.DeepEqual(data, videoAdminCancelAuditData(c, before)) {
			return ErrVideoCancelUnavailable
		}
	}
	return nil
}

func (s *VideoAdminService) CancelTask(ctx context.Context, c VideoAdminCancelCommand) (*VideoAdminCancellationReply, error) {
	if !s.WritesReady() {
		return nil, ErrVideoAccessUnavailable
	}
	reason := strings.TrimSpace(c.Reason)
	if !utf8.ValidString(reason) || reason == "" || len(reason) > 1024 || utf8.RuneCountInString(reason) > 256 || strings.IndexFunc(reason, unicode.IsControl) >= 0 || c.VersionNo == 0 || !videoHTTPIdempotency.MatchString(c.IdempotencyKey) {
		return nil, ErrVideoAdminCommandInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var reply *VideoAdminCancellationReply
	err := retryVideoBillingTransaction(ctx, func() error {
		reply = nil
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:task_manage"); err != nil {
				return err
			}
			if !videoBillingPublicID.MatchString(c.TaskID) {
				return repository.ErrVideoTaskNotFound
			}
			var owner repository.VideoOwner
			if err := tx.Table("ai_gateway_tasks t").Select("t.user_id,t.project_id,t.api_key_id").Joins("JOIN ai_requests r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id AND r.api_key_id <=> t.api_key_id AND r.logical_model_code=t.logical_model_code AND r.operation=t.operation").Where("t.public_id=? AND t.capability='video.generate' AND r.modality='video' AND r.capability='video.generate'", c.TaskID).Take(&owner).Error; err != nil {
				return videoAccessReadError(err, repository.ErrVideoTaskNotFound)
			}
			tasks := repository.NewVideoTaskRepository(tx)
			task, err := tasks.LockForOwnerTx(tx, c.TaskID, owner)
			if err != nil {
				return err
			}
			// 命令身份不依赖原因密钥版本；换密钥时旧回执须失败关闭，不能被当成全新取消命令。
			hash := videoBillingDigest(fmt.Sprintf("video-admin-cancel:%d:%s", c.Caller.UserID, c.IdempotencyKey))
			var command videoAdminCancellationRecord
			err = tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("actor_user_id=? AND command_key_hash=?", c.Caller.UserID, hash).Take(&command).Error
			replayed := err == nil
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.Join(ErrVideoCancelUnavailable, err)
			}
			identity := VideoAdminReasonIdentity{ActorID: c.Caller.UserID, TaskID: c.TaskID, CommandKeyHash: hash, VersionNo: c.VersionNo}
			if replayed {
				if command.TaskID != task.ID || command.RequestID != task.RequestID || command.UserID != owner.UserID || command.ProjectID != owner.ProjectID || !equalOptionalUint64(command.APIKeyID, owner.APIKeyID) || command.InitialVersion != c.VersionNo {
					return ErrVideoAdminCommandConflict
				}
				// 先证明当前密钥能验证原信封，再比较原因意图；密钥配置错误不是客户端改了原因。
				if _, err := s.reasons.Open(identity, command.VideoAdminReasonEnvelope); err != nil {
					return ErrVideoCancelUnavailable
				}
				if command.ReasonHMAC != s.reasons.digest("reason", reason) {
					return ErrVideoAdminCommandConflict
				}
			} else {
				if task.VersionNo != c.VersionNo {
					return ErrVideoAdminCommandConflict
				}
				initial, err := videoCancellationAction(task)
				if err != nil {
					return err
				}
				sealed, err := s.reasons.Seal(identity, []byte(reason))
				if err != nil {
					return ErrVideoAccessUnavailable
				}
				command = videoAdminCancellationRecord{ActorUserID: c.Caller.UserID, CommandKeyHash: hash, TaskID: task.ID, RequestID: task.RequestID, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, InitialVersion: c.VersionNo, InitialResult: initial, VideoAdminReasonEnvelope: *sealed, CreatedAt: time.Now().UTC()}
				command.BeforeAuditID, err = writeVideoAdminCancelAudit(ctx, tx, command, task.PublicID, true)
				if err != nil {
					return errors.Join(ErrVideoCancelUnavailable, err)
				}
				switch initial {
				case "cancelled":
					grant := &videoAdminCancellationGrant{admin: s, tx: tx, actor: c.Caller, taskID: task.PublicID, owner: owner, version: task.VersionNo}
					if _, err := s.app.billing.cancelBeforeSubmitAuthorized(ctx, task.PublicID, owner, tx, grant); err != nil {
						return errors.Join(ErrVideoCancelUnavailable, err)
					}
				case "cancel_requested":
					if _, err := tasks.RequestCancellation(ctx, task.PublicID, owner, time.Now().UTC()); err != nil {
						return errors.Join(ErrVideoCancelUnavailable, err)
					}
				case "already_terminal":
					// 终态只记录管理员无操作事实，绝不退款、删除资产或覆盖原成功结论。
				}
				task, err = tasks.LockForOwnerTx(tx, task.PublicID, owner)
				if err != nil {
					return err
				}
				command.FinalVersion = task.VersionNo
				command.AfterAuditID, err = writeVideoAdminCancelAudit(ctx, tx, command, task.PublicID, false)
				if err != nil {
					return errors.Join(ErrVideoCancelUnavailable, err)
				}
				if err := tx.Create(&command).Error; err != nil {
					if repository.IsDuplicateKeyForHandler(err) {
						return ErrVideoAdminCommandConflict
					}
					return errors.Join(ErrVideoCancelUnavailable, err)
				}
			}
			if command.FinalVersion > task.VersionNo || command.FinalVersion < command.InitialVersion {
				return ErrVideoCancelUnavailable
			}
			if err := verifyVideoAdminCancelAudit(tx, command, task.PublicID); err != nil {
				return err
			}
			result := command.InitialResult
			if result == "cancel_requested" {
				if task.CancelRequestedAt == nil {
					return ErrVideoCancelUnavailable
				}
				if task.Status == model.AIImageTaskCancelled && task.BillingStatus == model.AIBillingReleased {
					result = "cancelled"
				} else if videoG4TerminalStatus(task.Status) {
					result = "already_terminal"
				}
			}
			if result == "cancelled" {
				if task.Status != model.AIImageTaskCancelled || task.CancelRequestedAt == nil {
					return ErrVideoCancelUnavailable
				}
				r, _, link, hold, err := loadVideoFinancialFactsTx(tx, task, owner)
				if err != nil {
					return err
				}
				if err := validateVideoCancelledFactsTx(tx, task, *r, *link, *hold); err != nil {
					return err
				}
			} else if result == "already_terminal" && !videoG4TerminalStatus(task.Status) {
				return ErrVideoCancelUnavailable
			}
			detail, err := s.app.taskDetailsTx(ctx, tx, task, owner)
			if err != nil {
				return err
			}
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:task_manage"); err != nil {
				return err
			}
			reply = &VideoAdminCancellationReply{VideoAdminTaskDetails: &VideoAdminTaskDetails{VideoTaskDetails: detail, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID}, CancelRequestedAt: task.CancelRequestedAt, CancellationResult: result, Idempotent: replayed}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		return nil, err
	}
	return reply, nil
}
