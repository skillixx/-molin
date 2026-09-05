package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	auditmodel "molin/server/internal/modules/audit/model"
	auditrepo "molin/server/internal/modules/audit/repository"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// 只暴露原任务查询，管理轮询的依赖面没有Submit、上传、删除或媒体抓取能力。
type VideoAdminPollProvider interface {
	Name() string
	Query(context.Context, video.QueryRequest) (video.QueryResult, error)
}
type VideoAdminPollCommand struct {
	Caller                         VideoCaller `json:"-"`
	TaskID, IdempotencyKey, Reason string      `json:"-"`
	VersionNo                      uint64      `json:"-"`
}
type VideoAdminPollReply struct {
	CommandID       string `json:"command_id"`
	TaskID          string `json:"task_id"`
	RequestID       string `json:"request_id"`
	Status          string `json:"status"`
	ExecutionStatus string `json:"execution_status"`
	VersionNo       uint64 `json:"version_no"`
	Idempotent      bool   `json:"idempotent"`
}
type videoAdminPollRecord struct {
	ID                                     uint64 `gorm:"primaryKey"`
	PublicID, CommandKeyHash               string
	ActorUserID, TaskID, UserID, ProjectID uint64
	RequestID                              string
	APIKeyID                               *uint64
	InitialVersion                         uint64
	BindingSHA256                          string `gorm:"column:binding_sha256"`
	Status, ResultCode                     string
	VersionNo                              uint64
	VideoAdminReasonEnvelope               `gorm:"embedded"`
	BeforeAuditID                          uint64
	AfterAuditID                           *uint64
	CreatedAt, DeadlineAt                  time.Time
}

// 导出别名仅用于GORM识别嵌入字段，不作为HTTP DTO；原因信封仍禁止JSON序列化。
type VideoAdminOperationFields = videoAdminPollRecord

func (videoAdminPollRecord) TableName() string { return "ai_video_admin_poll_commands" }
func (s *VideoAdminService) PollReady() bool {
	return s.WritesReady() && s.pollProvider != nil && s.pollProvider.Name() == "fake-native-async"
}
func pollReasonID(c videoAdminPollRecord, id string) VideoAdminReasonIdentity {
	return VideoAdminReasonIdentity{ActorID: c.ActorUserID, PollTaskID: id, CommandKeyHash: c.CommandKeyHash, VersionNo: c.InitialVersion}
}
func pollBinding(t *repository.VideoTaskRecord) string {
	if t.ProviderCode == nil || t.ProviderTaskID == nil {
		return ""
	}
	return videoBillingDigest(*t.ProviderCode + ":" + *t.ProviderTaskID)
}

func writeAdminPollAudit(ctx context.Context, tx *gorm.DB, c videoAdminPollRecord, taskID string, before bool) (uint64, error) {
	return writeAdminOperationAudit(ctx, tx, c, taskID, before, "poll")
}

func writeAdminOperationAudit(ctx context.Context, tx *gorm.DB, c videoAdminPollRecord, taskID string, before bool, operation string) (uint64, error) {
	action, result := "video_admin_"+operation+"_after", c.ResultCode
	if before {
		action, result = "video_admin_"+operation+"_before", "requested"
	}
	raw, err := json.Marshal(map[string]any{"command_id": c.PublicID, "command_key_hash": c.CommandKeyHash, "task_id": taskID, "request_id": c.RequestID, "reason_hmac": c.ReasonHMAC, "initial_version": c.InitialVersion, "result": result})
	if err != nil {
		return 0, err
	}
	target, summary := "video_task", string(raw)
	a := auditmodel.AuditLog{OperatorID: &c.ActorUserID, Module: "token_gateway", Action: action, TargetType: &target, TargetID: &taskID, RequestSummary: &summary, CreatedAt: time.Now().UTC()}
	if err := auditrepo.NewAuditLogRepository(tx).CreateWithTx(ctx, tx, &a); err != nil {
		return 0, err
	}
	return a.ID, nil
}

// 同键只能读取原完整审计事实；原密文有效不代表前后审计仍然一致。
func verifyAdminPollAudits(tx *gorm.DB, c videoAdminPollRecord, taskID string) error {
	return verifyAdminOperationAudits(tx, c, taskID, "poll")
}

func verifyAdminOperationAudits(tx *gorm.DB, c videoAdminPollRecord, taskID, operation string) error {
	for _, before := range []bool{true, false} {
		if !before && c.Status == "running" {
			continue
		}
		id, action, result := c.BeforeAuditID, "video_admin_"+operation+"_before", "requested"
		if !before {
			if c.AfterAuditID == nil {
				return ErrVideoAccessUnavailable
			}
			id, action, result = *c.AfterAuditID, "video_admin_"+operation+"_after", c.ResultCode
		}
		var a auditmodel.AuditLog
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=?", id).Take(&a).Error; err != nil {
			return errors.Join(ErrVideoAccessUnavailable, err)
		}
		if a.OperatorID == nil || *a.OperatorID != c.ActorUserID || a.Module != "token_gateway" || a.Action != action || a.TargetType == nil || *a.TargetType != "video_task" || a.TargetID == nil || *a.TargetID != taskID || a.RequestSummary == nil {
			return ErrVideoAccessUnavailable
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(*a.RequestSummary), &fields) != nil || len(fields) != 7 {
			return ErrVideoAccessUnavailable
		}
		for key, value := range map[string]any{"command_id": c.PublicID, "command_key_hash": c.CommandKeyHash, "task_id": taskID, "request_id": c.RequestID, "reason_hmac": c.ReasonHMAC, "initial_version": c.InitialVersion, "result": result} {
			want, _ := json.Marshal(value)
			if string(fields[key]) != string(want) {
				return ErrVideoAccessUnavailable
			}
		}
	}
	return nil
}

// 外部查询只发生在running命令及前审计提交之后，不在SQL重试闭包内重新调用Provider。
func (s *VideoAdminService) PollTask(ctx context.Context, c VideoAdminPollCommand) (*VideoAdminPollReply, error) {
	if !s.PollReady() {
		return nil, ErrVideoAccessUnavailable
	}
	reason := strings.TrimSpace(c.Reason)
	if reason == "" || !utf8.ValidString(reason) || len(reason) > 1024 || utf8.RuneCountInString(reason) > 256 || strings.IndexFunc(reason, unicode.IsControl) >= 0 || c.VersionNo == 0 || !videoHTTPIdempotency.MatchString(c.IdempotencyKey) {
		return nil, ErrVideoAdminCommandInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var command videoAdminPollRecord
	var task *repository.VideoTaskRecord
	var owner repository.VideoOwner
	claimed := false
	err := retryVideoBillingTransaction(ctx, func() error {
		claimed = false
		command = videoAdminPollRecord{}
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:task_manage"); err != nil {
				return err
			}
			if !videoBillingPublicID.MatchString(c.TaskID) {
				return repository.ErrVideoTaskNotFound
			}
			var identity struct {
				UserID, ProjectID uint64
				APIKeyID          *uint64
			}
			if err := tx.Table("ai_gateway_tasks t").Select("t.user_id,t.project_id,t.api_key_id").Joins("JOIN ai_requests r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id AND r.api_key_id <=> t.api_key_id AND r.operation=t.operation AND r.logical_model_code=t.logical_model_code").Where("t.public_id=? AND t.capability='video.generate' AND r.capability='video.generate' AND r.modality='video'", c.TaskID).Take(&identity).Error; err != nil {
				return videoAccessReadError(err, repository.ErrVideoTaskNotFound)
			}
			owner = repository.VideoOwner{UserID: identity.UserID, ProjectID: identity.ProjectID, APIKeyID: identity.APIKeyID}
			var err error
			task, err = repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, c.TaskID, owner)
			if err != nil {
				return err
			}
			hash := videoBillingDigest(fmt.Sprintf("video-admin-poll:%d:%s", c.Caller.UserID, c.IdempotencyKey))
			err = tx.Where("actor_user_id=? AND command_key_hash=?", c.Caller.UserID, hash).Take(&command).Error
			if err == nil {
				if command.TaskID != task.ID || command.RequestID != task.RequestID || command.InitialVersion != c.VersionNo || command.BindingSHA256 != pollBinding(task) {
					return ErrVideoAdminCommandConflict
				}
				if _, err := s.reasons.Open(pollReasonID(command, c.TaskID), command.VideoAdminReasonEnvelope); err != nil {
					return ErrVideoAccessUnavailable
				}
				if command.ReasonHMAC != s.reasons.digest("reason", reason) {
					return ErrVideoAdminCommandConflict
				}
				if err := verifyAdminPollAudits(tx, command, c.TaskID); err != nil {
					return err
				}
				return s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:task_manage")
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if task.VersionNo != c.VersionNo || task.ProviderCode == nil || *task.ProviderCode != "fake-native-async" || task.ProviderTaskID == nil || !videoBillingPublicID.MatchString(*task.ProviderTaskID) || task.AttemptCount != 1 {
				return ErrVideoAdminCommandConflict
			}
			switch task.Status {
			case "submitted", "processing", "pending_reconcile":
			default:
				return ErrVideoAdminCommandConflict
			}
			var active int64
			if err := tx.Table("ai_video_admin_poll_commands").Where("task_id=? AND status='running'", task.ID).Count(&active).Error; err != nil {
				return err
			}
			if active != 0 {
				return ErrVideoAdminCommandConflict
			}
			var entropy [24]byte
			if _, err := rand.Read(entropy[:]); err != nil {
				return err
			}
			now := time.Now().UTC()
			command = videoAdminPollRecord{PublicID: "vpoll_" + hex.EncodeToString(entropy[:]), CommandKeyHash: hash, ActorUserID: c.Caller.UserID, TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, APIKeyID: task.APIKeyID, RequestID: task.RequestID, InitialVersion: task.VersionNo, BindingSHA256: pollBinding(task), Status: "running", ResultCode: "requested", VersionNo: 1, CreatedAt: now, DeadlineAt: now.Add(30 * time.Second)}
			sealed, err := s.reasons.Seal(pollReasonID(command, c.TaskID), []byte(reason))
			if err != nil {
				return ErrVideoAccessUnavailable
			}
			command.VideoAdminReasonEnvelope = *sealed
			command.BeforeAuditID, err = writeAdminPollAudit(ctx, tx, command, c.TaskID, true)
			if err != nil {
				return err
			}
			if err := tx.Create(&command).Error; err != nil {
				return err
			}
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:task_manage"); err != nil {
				return err
			}
			claimed = true
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		if repository.IsDuplicateKeyForHandler(err) {
			return nil, ErrVideoAdminCommandConflict
		}
		return nil, err
	}
	if !claimed {
		if command.Status == "running" && !command.DeadlineAt.After(time.Now().UTC()) {
			s.markAdminPollUnknown(ctx, command, c.TaskID)
			// 善后可能因数据库故障未提交，必须重新读取真实状态，不能在内存伪造完成或继续返回旧running。
			if err := s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := tx.Where("id=?", command.ID).Take(&command).Error; err != nil {
					return err
				}
				if err := verifyAdminPollAudits(tx, command, c.TaskID); err != nil {
					return err
				}
				return s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:task_manage")
			}); err != nil {
				return nil, err
			}
		}
		return &VideoAdminPollReply{CommandID: command.PublicID, TaskID: task.PublicID, RequestID: task.RequestID, Status: command.Status, ExecutionStatus: task.Status, VersionNo: task.VersionNo, Idempotent: true}, nil
	}
	// 这里没有Submit能力；输入和Prompt也不进入管理恢复读取器。
	if err := s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:task_manage") }); err != nil {
		s.markAdminPollUnknown(ctx, command, c.TaskID)
		return nil, err
	}
	deadline := command.DeadlineAt
	if c.Caller.credential != nil && c.Caller.credential.expiresAt.Before(deadline) {
		deadline = c.Caller.credential.expiresAt
	}
	queryCtx, queryCancel := context.WithDeadline(ctx, deadline)
	observed, queryErr := s.pollProvider.Query(queryCtx, video.QueryRequest{ProviderTaskID: *task.ProviderTaskID, Operation: *task.Operation})
	queryCancel()
	var result *VideoAdminPollReply
	err = retryVideoBillingTransaction(ctx, func() error {
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			current, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, c.TaskID, owner)
			if err != nil {
				return err
			}
			var locked videoAdminPollRecord
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", command.ID).Take(&locked).Error; err != nil {
				return err
			}
			if locked.Status != "running" || locked.VersionNo != 1 || locked.BindingSHA256 != pollBinding(current) {
				return ErrVideoAdminCommandConflict
			}
			if err := verifyAdminPollAudits(tx, locked, c.TaskID); err != nil {
				return err
			}
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:task_manage"); err != nil {
				return err
			}
			// 已取得的观察可重放数据库应用，不可在内部保存点重试整笔已失效事务。
			applyCtx := context.WithValue(ctx, videoBillingOuterTransactionKey{}, true)
			ledger := s.app.NewTaskLedger(owner, nil).withDB(tx.WithContext(applyCtx))
			ledger.recoveryTaskID = c.TaskID
			g := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: ledger})
			_, applyErr := g.ApplyPolledResult(applyCtx, c.TaskID, observed, queryErr)
			var dbErr *drivermysql.MySQLError
			if errors.As(applyErr, &dbErr) {
				return applyErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Provider观察和原G5冲突事实不因应用层错误被隐去；返回低敏待核对而非假成功。
			locked.Status, locked.ResultCode = "completed", "observed"
			if applyErr != nil || queryErr != nil {
				locked.Status, locked.ResultCode = "unknown", "needs_reconcile"
			}
			current, err = repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, c.TaskID, owner)
			if err != nil {
				return err
			}
			after, err := writeAdminPollAudit(ctx, tx, locked, c.TaskID, false)
			if err != nil {
				return err
			}
			locked.AfterAuditID = &after
			if err := verifyAdminPollAudits(tx, locked, c.TaskID); err != nil {
				return err
			}
			updated := tx.Model(&videoAdminPollRecord{}).Where("id=? AND status='running' AND version_no=1", command.ID).Updates(map[string]any{"status": locked.Status, "result_code": locked.ResultCode, "version_no": 2, "after_audit_id": after})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return ErrVideoAdminCommandConflict
			}
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:task_manage"); err != nil {
				return err
			}
			result = &VideoAdminPollReply{CommandID: command.PublicID, TaskID: current.PublicID, RequestID: current.RequestID, Status: locked.Status, ExecutionStatus: current.Status, VersionNo: current.VersionNo}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		s.markAdminPollUnknown(ctx, command, c.TaskID)
		return nil, err
	}
	return result, nil
}

// RPC后失去认证或连接时只关闭已有管理命令，不能借系统善后执行Provider、状态推进或退款。
func (s *VideoAdminService) markAdminPollUnknown(parent context.Context, c videoAdminPollRecord, taskID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	_ = retryVideoBillingTransaction(ctx, func() error {
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if _, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, repository.VideoOwner{UserID: c.UserID, ProjectID: c.ProjectID, APIKeyID: c.APIKeyID}); err != nil {
				return err
			}
			var row videoAdminPollRecord
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", c.ID).Take(&row).Error; err != nil {
				return err
			}
			if row.Status != "running" {
				return nil
			}
			row.ResultCode = "needs_reconcile"
			after, err := writeAdminPollAudit(ctx, tx, row, taskID, false)
			if err != nil {
				return err
			}
			return tx.Model(&videoAdminPollRecord{}).Where("id=? AND status='running' AND version_no=1", c.ID).Updates(map[string]any{"status": "unknown", "result_code": "needs_reconcile", "version_no": 2, "after_audit_id": after}).Error
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
}
