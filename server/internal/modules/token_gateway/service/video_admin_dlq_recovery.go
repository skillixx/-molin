package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	auditmodel "molin/server/internal/modules/audit/model"
	auditrepo "molin/server/internal/modules/audit/repository"
	"molin/server/internal/modules/token_gateway/model"
	video "molin/server/internal/modules/token_gateway/video"
)

type VideoAdminDLQRecoveryCommand struct {
	Caller         VideoCaller     `json:"-"`
	TaskID         string          `json:"-"`
	Stage          video.TaskStage `json:"-"`
	IdempotencyKey string          `json:"-"`
	Reason         string          `json:"-"`
	VersionNo      uint64          `json:"-"`
}

type VideoAdminDLQRecoveryReply struct {
	TaskID    string `json:"task_id"`
	RequestID string `json:"request_id"`
	Stage     string `json:"stage"`
	Attempt   uint32 `json:"attempt"`
	Recovered bool   `json:"recovered"`
	Existing  bool   `json:"existing"`
	Pending   bool   `json:"pending"`
}

type VideoAdminPoisonDiscardCommand struct {
	Caller         VideoCaller     `json:"-"`
	Stage          video.TaskStage `json:"-"`
	IdempotencyKey string          `json:"-"`
	Reason         string          `json:"-"`
	FuseAuditID    uint64          `json:"-"`
}

type VideoAdminPoisonDiscardReply struct {
	Stage       string `json:"stage"`
	BodySHA256  string `json:"body_sha256"`
	FuseAuditID uint64 `json:"fuse_audit_id"`
	Discarded   bool   `json:"discarded"`
	Existing    bool   `json:"existing"`
}

type videoPoisonDiscardCoordinator struct {
	service     *VideoAdminService
	command     VideoAdminPoisonDiscardCommand
	commandHash string
	reasonHMAC  string
	reply       *VideoAdminPoisonDiscardReply
}

type videoDLQRecoveryCoordinator struct {
	service       *VideoAdminService
	command       VideoAdminDLQRecoveryCommand
	commandHash   string
	reasonHMAC    string
	requestEvent  string
	publishEvent  string
	dispatchEvent string
	reply         *VideoAdminDLQRecoveryReply
}

func (s *VideoAdminService) RecoverDeadLetter(ctx context.Context, c VideoAdminDLQRecoveryCommand) (*VideoAdminDLQRecoveryReply, error) {
	if !s.DLQRecoveryReady() {
		return nil, ErrVideoAccessUnavailable
	}
	reason := strings.TrimSpace(c.Reason)
	if !videoBillingPublicID.MatchString(c.TaskID) || !videoHTTPIdempotency.MatchString(c.IdempotencyKey) || c.VersionNo == 0 ||
		(c.Stage != video.TaskSubmit && c.Stage != video.TaskPoll && c.Stage != video.TaskFetch) || reason == "" ||
		!utf8.ValidString(reason) || len(reason) > 1024 || utf8.RuneCountInString(reason) > 256 || strings.IndexFunc(reason, unicode.IsControl) >= 0 {
		return nil, ErrVideoAdminCommandInvalid
	}
	coordinator := &videoDLQRecoveryCoordinator{
		service:     s,
		command:     c,
		commandHash: videoBillingDigest(fmt.Sprintf("video-admin-recovery:%d:%s", c.Caller.UserID, c.IdempotencyKey)),
		reasonHMAC:  s.reasons.digest("reason", reason),
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.dlqConsumer.RecoverDeadOne(bounded, c.Stage, coordinator); err != nil {
		if errors.Is(err, video.ErrTaskRecoveryUncertain) && coordinator.reply != nil && coordinator.reply.Pending {
			return coordinator.reply, nil
		}
		if errors.Is(err, video.ErrTaskRecoveryUncertain) {
			return nil, ErrVideoAdminCommandConflict
		}
		return nil, errors.Join(ErrVideoAccessUnavailable, err)
	}
	if coordinator.reply == nil {
		return nil, ErrVideoAccessUnavailable
	}
	return coordinator.reply, nil
}

func (c *videoDLQRecoveryCoordinator) PrepareDeadLetterRecovery(ctx context.Context, stage video.TaskStage, message video.TaskMessage) (video.TaskDeadLetterDecision, error) {
	if c == nil || c.service == nil || stage != c.command.Stage || message.TaskID != c.command.TaskID {
		return video.TaskDeadLetterHold, ErrVideoAdminCommandConflict
	}
	decision := video.TaskDeadLetterHold
	err := c.service.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockVideoAdminRecoveryActor(tx, c.command.Caller.UserID); err != nil {
			return err
		}
		if err := c.service.authorizeTx(ctx, tx, c.command.Caller, "ai_gateway:reconcile_manage"); err != nil {
			return err
		}
		var task model.AIImageTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("public_id=? AND capability='video.generate' AND operation IN ('text_to_video','image_to_video')", message.TaskID).Take(&task).Error; err != nil {
			return videoAccessReadError(err, ErrVideoAdminCommandConflict)
		}
		identity := fmt.Sprintf("%s|%s|%s|%d|%d", message.TaskID, message.RequestID, stage, message.Attempt, c.command.VersionNo)
		c.requestEvent = "vg7_dlq_request_" + videoBillingDigest(identity)
		c.publishEvent = "vg7_dlq_publish_" + videoBillingDigest(identity)
		c.dispatchEvent = "vg7_dlq_dispatch_" + videoBillingDigest(identity)
		var requested, published int64
		if err := tx.Model(&model.AIGatewayTaskEvent{}).Where("event_id=? AND task_id=?", c.requestEvent, task.ID).Count(&requested).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.AIGatewayTaskEvent{}).Where("event_id=? AND task_id=?", c.publishEvent, task.ID).Count(&published).Error; err != nil {
			return err
		}
		c.reply = &VideoAdminDLQRecoveryReply{TaskID: task.PublicID, RequestID: task.RequestID, Stage: string(stage), Attempt: message.Attempt}
		if requested == 1 && published == 1 {
			c.reply.Recovered, c.reply.Existing = true, true
			decision = video.TaskDeadLetterAckExisting
			return c.service.authorizeTx(ctx, tx, c.command.Caller, "ai_gateway:reconcile_manage")
		}
		if requested == 1 && published == 0 {
			var dispatch model.AIOutboxEvent
			if err := tx.Where("event_id=? AND aggregate_type='video_request' AND aggregate_id=? AND event_type='video_dlq_recovery_dispatch'", c.dispatchEvent, task.RequestID).Take(&dispatch).Error; err != nil {
				return ErrVideoAdminCommandConflict
			}
			switch dispatch.Status {
			case model.AIOutboxPublished:
				if err := writeVideoDLQAudit(ctx, tx, c.command.Caller.UserID, "video_dlq_recovery_after", task, stage, message.Attempt, c.command.VersionNo, c.commandHash, c.reasonHMAC, "published"); err != nil {
					return err
				}
				event := model.AIGatewayTaskEvent{EventID: c.publishEvent, TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "video_dlq_recovery_published", Source: "reconciler", CreatedAt: time.Now().UTC()}
				if err := tx.Create(&event).Error; err != nil {
					return err
				}
				c.reply.Recovered, c.reply.Existing = true, true
				decision = video.TaskDeadLetterAckExisting
				return c.service.authorizeTx(ctx, tx, c.command.Caller, "ai_gateway:reconcile_manage")
			case model.AIOutboxPending, model.AIOutboxPublishing, model.AIOutboxDead:
				c.reply.Pending = true
				decision = video.TaskDeadLetterHold
				return nil
			default:
				return ErrVideoAdminCommandConflict
			}
		}
		if requested != 0 || published != 0 || task.RequestID != message.RequestID || task.VersionNo != c.command.VersionNo || !videoDLQStageAllowed(stage, task) {
			return ErrVideoAdminCommandConflict
		}
		if err := validateVideoDLQInput(tx, task, message); err != nil {
			return err
		}
		if err := lockVideoAdminRecoveryIntent(ctx, tx, c.command.Caller.UserID, c.commandHash, "dlq", map[string]any{"task_id": task.PublicID, "request_id": task.RequestID, "stage": stage, "attempt": message.Attempt, "task_version": c.command.VersionNo}); err != nil {
			return err
		}
		if err := writeVideoDLQAudit(ctx, tx, c.command.Caller.UserID, "video_dlq_recovery_before", task, stage, message.Attempt, c.command.VersionNo, c.commandHash, c.reasonHMAC, "requested"); err != nil {
			return err
		}
		event := model.AIGatewayTaskEvent{EventID: c.requestEvent, TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "video_dlq_recovery_requested", Source: "reconciler", CreatedAt: time.Now().UTC()}
		if err := tx.Create(&event).Error; err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{"version": 1, "task_id": task.PublicID, "request_id": task.RequestID, "input_asset_id": message.InputAssetID, "attempt": message.Attempt, "stage": stage, "task_version": c.command.VersionNo, "recovery_event_id": c.requestEvent})
		if err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Second)
		dispatch := model.AIOutboxEvent{EventID: c.dispatchEvent, AggregateType: "video_request", AggregateID: task.RequestID, EventType: "video_dlq_recovery_dispatch", PayloadJSON: payload, Status: model.AIOutboxPending, NextRetryAt: now, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&dispatch).Error; err != nil {
			return err
		}
		if err := c.service.authorizeTx(ctx, tx, c.command.Caller, "ai_gateway:reconcile_manage"); err != nil {
			return err
		}
		c.reply.Pending = true
		decision = video.TaskDeadLetterHold
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return decision, err
}

func (c *videoDLQRecoveryCoordinator) CompleteDeadLetterRecovery(ctx context.Context, stage video.TaskStage, message video.TaskMessage) error {
	if c == nil || c.service == nil || c.reply == nil || stage != c.command.Stage || message.TaskID != c.command.TaskID || c.requestEvent == "" || c.publishEvent == "" {
		return ErrVideoAdminCommandConflict
	}
	return c.service.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockVideoAdminRecoveryActor(tx, c.command.Caller.UserID); err != nil {
			return err
		}
		if err := c.service.authorizeTx(ctx, tx, c.command.Caller, "ai_gateway:reconcile_manage"); err != nil {
			return err
		}
		var task model.AIImageTask
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("public_id=? AND request_id=? AND capability='video.generate'", message.TaskID, message.RequestID).Take(&task).Error; err != nil {
			return ErrVideoAdminCommandConflict
		}
		var requested, published int64
		if err := tx.Model(&model.AIGatewayTaskEvent{}).Where("event_id=? AND task_id=?", c.requestEvent, task.ID).Count(&requested).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.AIGatewayTaskEvent{}).Where("event_id=? AND task_id=?", c.publishEvent, task.ID).Count(&published).Error; err != nil {
			return err
		}
		if requested != 1 {
			return ErrVideoAdminCommandConflict
		}
		if published == 0 {
			if err := writeVideoDLQAudit(ctx, tx, c.command.Caller.UserID, "video_dlq_recovery_after", task, stage, message.Attempt, c.command.VersionNo, c.commandHash, c.reasonHMAC, "published"); err != nil {
				return err
			}
			event := model.AIGatewayTaskEvent{EventID: c.publishEvent, TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "video_dlq_recovery_published", Source: "reconciler", CreatedAt: time.Now().UTC()}
			if err := tx.Create(&event).Error; err != nil {
				return err
			}
		} else if published != 1 {
			return ErrVideoAdminCommandConflict
		}
		if err := c.service.authorizeTx(ctx, tx, c.command.Caller, "ai_gateway:reconcile_manage"); err != nil {
			return err
		}
		c.reply.Recovered = true
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

func videoDLQStageAllowed(stage video.TaskStage, task model.AIImageTask) bool {
	switch stage {
	case video.TaskSubmit:
		return (task.Status == model.AIImageTaskCreated || task.Status == model.AIImageTaskReserved || task.Status == model.AIImageTaskQueued) && task.ProviderTaskID == nil && task.AttemptCount == 0
	case video.TaskPoll:
		return (task.Status == model.AIImageTaskSubmitted || task.Status == model.AIImageTaskProcessing || task.Status == model.AIImageTaskPendingReconcile) && task.ProviderTaskID != nil
	case video.TaskFetch:
		return (task.Status == model.AIImageTaskFetching || task.Status == model.AIImageTaskStoring || task.Status == model.AIImageTaskModerating || task.Status == model.AIImageTaskLabeling || task.Status == model.AIImageTaskPendingReconcile) && task.ProviderTaskID != nil
	default:
		return false
	}
}

func validateVideoDLQInput(tx *gorm.DB, task model.AIImageTask, message video.TaskMessage) error {
	if task.Operation == nil {
		return ErrVideoAdminCommandConflict
	}
	if *task.Operation == model.AIVideoOperationTextToVideo {
		if message.InputAssetID != "" {
			return ErrVideoAdminCommandConflict
		}
		return nil
	}
	if *task.Operation != model.AIVideoOperationImageToVideo || message.InputAssetID == "" {
		return ErrVideoAdminCommandConflict
	}
	var count int64
	err := tx.Table("ai_gateway_task_inputs i").Joins("JOIN ai_gateway_input_assets a ON a.id=i.input_asset_id AND a.user_id=i.user_id AND a.project_id=i.project_id").Where("i.task_id=? AND i.user_id=? AND i.project_id=? AND a.public_id=?", task.ID, task.UserID, task.ProjectID, message.InputAssetID).Count(&count).Error
	if err != nil || count != 1 {
		return ErrVideoAdminCommandConflict
	}
	return nil
}

func writeVideoDLQAudit(ctx context.Context, tx *gorm.DB, actor uint64, action string, task model.AIImageTask, stage video.TaskStage, attempt uint32, taskVersion uint64, commandHash, reasonHMAC, result string) error {
	summary, err := json.Marshal(map[string]any{"schema": 1, "kind": "dlq", "task_id": task.PublicID, "request_id": task.RequestID, "stage": stage, "attempt": attempt, "task_version": taskVersion, "command_key_hash": commandHash, "reason_hmac": reasonHMAC, "result": result})
	if err != nil {
		return err
	}
	target, targetID, text := "video_task", task.PublicID, string(summary)
	row := auditmodel.AuditLog{OperatorID: &actor, Module: "token_gateway", Action: action, TargetType: &target, TargetID: &targetID, RequestSummary: &text, CreatedAt: time.Now().UTC()}
	return auditrepo.NewAuditLogRepository(tx).CreateWithTx(ctx, tx, &row)
}

func (s *VideoAdminService) DiscardPoisonMessage(ctx context.Context, c VideoAdminPoisonDiscardCommand) (*VideoAdminPoisonDiscardReply, error) {
	if !s.DLQRecoveryReady() {
		return nil, ErrVideoAccessUnavailable
	}
	reason := strings.TrimSpace(c.Reason)
	if !videoHTTPIdempotency.MatchString(c.IdempotencyKey) || c.FuseAuditID == 0 ||
		(c.Stage != video.TaskSubmit && c.Stage != video.TaskPoll && c.Stage != video.TaskFetch) || reason == "" ||
		!utf8.ValidString(reason) || len(reason) > 1024 || utf8.RuneCountInString(reason) > 256 || strings.IndexFunc(reason, unicode.IsControl) >= 0 {
		return nil, ErrVideoAdminCommandInvalid
	}
	coordinator := &videoPoisonDiscardCoordinator{
		service: s, command: c,
		commandHash: videoBillingDigest(fmt.Sprintf("video-admin-recovery:%d:%s", c.Caller.UserID, c.IdempotencyKey)),
		reasonHMAC:  s.reasons.digest("reason", reason),
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.dlqConsumer.DiscardPoisonOne(bounded, c.Stage, coordinator); err != nil {
		if errors.Is(err, video.ErrTaskRecoveryUncertain) {
			return nil, ErrVideoAdminCommandConflict
		}
		return nil, errors.Join(ErrVideoAccessUnavailable, err)
	}
	if coordinator.reply == nil {
		return nil, ErrVideoAccessUnavailable
	}
	return coordinator.reply, nil
}

func (c *videoPoisonDiscardCoordinator) AuthorizeTaskPoisonDiscard(ctx context.Context, stage video.TaskStage, bodySHA256 string) error {
	if c == nil || c.service == nil || stage != c.command.Stage || len(bodySHA256) != 64 {
		return ErrVideoAdminCommandConflict
	}
	return c.service.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockVideoAdminRecoveryActor(tx, c.command.Caller.UserID); err != nil {
			return err
		}
		if err := c.service.authorizeTx(ctx, tx, c.command.Caller, "ai_gateway:reconcile_manage"); err != nil {
			return err
		}
		var original struct {
			ID             uint64
			RequestSummary *string
		}
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Table("audit_logs").Select("id,request_summary").Where("id=? AND module='token_gateway' AND action='video_rabbit_poison_blocked' AND target_type='video_queue' AND target_id=?", c.command.FuseAuditID, string(stage)).Take(&original).Error; err != nil || original.RequestSummary == nil {
			return ErrVideoAdminCommandConflict
		}
		var originalSummary struct {
			BodySHA256 string `json:"body_sha256"`
			Result     string `json:"result"`
		}
		if json.Unmarshal([]byte(*original.RequestSummary), &originalSummary) != nil || originalSummary.BodySHA256 != bodySHA256 || originalSummary.Result != "blocked" {
			return ErrVideoAdminCommandConflict
		}
		var fuse struct {
			Stage            string
			Status           string
			BodySHA256       *string
			BlockedAuditID   *uint64
			RecoveredAuditID *uint64
			VersionNo        uint64
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Table("ai_video_rabbit_poison_fuses").Where("stage=?", string(stage)).Take(&fuse).Error; err != nil || fuse.BodySHA256 == nil || *fuse.BodySHA256 != bodySHA256 || fuse.BlockedAuditID == nil || *fuse.BlockedAuditID != c.command.FuseAuditID || fuse.VersionNo == 0 {
			return ErrVideoAdminCommandConflict
		}
		if (fuse.Status == "blocked" && fuse.RecoveredAuditID != nil) || (fuse.Status == "ready" && fuse.RecoveredAuditID == nil) || (fuse.Status != "blocked" && fuse.Status != "ready") {
			return ErrVideoAdminCommandConflict
		}
		if err := lockVideoAdminRecoveryIntent(ctx, tx, c.command.Caller.UserID, c.commandHash, "poison", map[string]any{"stage": stage, "body_sha256": bodySHA256, "fuse_audit_id": c.command.FuseAuditID}); err != nil {
			return err
		}
		c.reply = &VideoAdminPoisonDiscardReply{Stage: string(stage), BodySHA256: bodySHA256, FuseAuditID: c.command.FuseAuditID, Existing: fuse.Status == "ready"}
		if c.reply.Existing {
			return c.service.authorizeTx(ctx, tx, c.command.Caller, "ai_gateway:reconcile_manage")
		}
		_, err := writeVideoPoisonAudit(ctx, tx, &c.command.Caller.UserID, "video_rabbit_poison_discard_before", stage, bodySHA256, c.command.FuseAuditID, c.commandHash, c.reasonHMAC, "authorized")
		return err
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

func (c *videoPoisonDiscardCoordinator) CompleteTaskPoisonDiscard(ctx context.Context, stage video.TaskStage, bodySHA256 string) error {
	if c == nil || c.service == nil || c.reply == nil || stage != c.command.Stage || bodySHA256 != c.reply.BodySHA256 {
		return ErrVideoAdminCommandConflict
	}
	return c.service.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockVideoAdminRecoveryActor(tx, c.command.Caller.UserID); err != nil {
			return err
		}
		if err := c.service.authorizeTx(ctx, tx, c.command.Caller, "ai_gateway:reconcile_manage"); err != nil {
			return err
		}
		if !c.reply.Existing {
			if _, err := writeVideoPoisonAudit(ctx, tx, &c.command.Caller.UserID, "video_rabbit_poison_discard_after", stage, bodySHA256, c.command.FuseAuditID, c.commandHash, c.reasonHMAC, "discarded"); err != nil {
				return err
			}
			recoveredAuditID, err := writeVideoPoisonAudit(ctx, tx, &c.command.Caller.UserID, "video_rabbit_poison_recovered", stage, bodySHA256, c.command.FuseAuditID, c.commandHash, c.reasonHMAC, "recovered")
			if err != nil {
				return err
			}
			result := tx.Table("ai_video_rabbit_poison_fuses").Where("stage=? AND status='blocked' AND body_sha256=? AND blocked_audit_id=?", string(stage), bodySHA256, c.command.FuseAuditID).Updates(map[string]any{"status": "ready", "recovered_audit_id": recoveredAuditID, "version_no": gorm.Expr("version_no+1"), "updated_at": time.Now().UTC()})
			if result.Error != nil || result.RowsAffected != 1 {
				return ErrVideoAdminCommandConflict
			}
		}
		if err := c.service.authorizeTx(ctx, tx, c.command.Caller, "ai_gateway:reconcile_manage"); err != nil {
			return err
		}
		c.reply.Discarded = true
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

func writeVideoPoisonAudit(ctx context.Context, tx *gorm.DB, actor *uint64, action string, stage video.TaskStage, bodySHA256 string, fuseAuditID uint64, commandHash, reasonHMAC, result string) (uint64, error) {
	summary, err := json.Marshal(map[string]any{"schema": 1, "kind": "poison", "stage": stage, "body_sha256": bodySHA256, "fuse_audit_id": fuseAuditID, "command_key_hash": commandHash, "reason_hmac": reasonHMAC, "result": result})
	if err != nil {
		return 0, err
	}
	target, targetID, text := "video_queue", string(stage), string(summary)
	row := auditmodel.AuditLog{OperatorID: actor, Module: "token_gateway", Action: action, TargetType: &target, TargetID: &targetID, RequestSummary: &text, CreatedAt: time.Now().UTC()}
	err = auditrepo.NewAuditLogRepository(tx).CreateWithTx(ctx, tx, &row)
	return row.ID, err
}

// lockVideoAdminRecoveryIntent以管理员用户行为互斥点，冻结同一Key只能表达同一恢复意图。
func lockVideoAdminRecoveryIntent(ctx context.Context, tx *gorm.DB, actor uint64, commandHash, kind string, expected map[string]any) error {
	var summaries []string
	if err := tx.Table("audit_logs").Where("operator_id=? AND action IN ('video_dlq_recovery_before','video_rabbit_poison_discard_before') AND JSON_UNQUOTE(JSON_EXTRACT(request_summary,'$.command_key_hash'))=?", actor, commandHash).Order("id").Pluck("request_summary", &summaries).Error; err != nil {
		return err
	}
	for _, raw := range summaries {
		var actual map[string]any
		if json.Unmarshal([]byte(raw), &actual) != nil || actual["kind"] != kind {
			return ErrVideoAdminCommandConflict
		}
		for key, value := range expected {
			if fmt.Sprint(actual[key]) != fmt.Sprint(value) {
				return ErrVideoAdminCommandConflict
			}
		}
	}
	return nil
}

func lockVideoAdminRecoveryActor(tx *gorm.DB, actor uint64) error {
	var locked struct{ ID uint64 }
	if tx == nil || actor == 0 {
		return ErrVideoAdminForbidden
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Table("users").Select("id").Where("id=?", actor).Take(&locked).Error; err != nil || locked.ID != actor {
		return ErrVideoAdminForbidden
	}
	return nil
}
