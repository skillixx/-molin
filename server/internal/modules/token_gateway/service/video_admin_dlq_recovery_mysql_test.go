package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
	authmodel "molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7AdminDLQRecoveryPermissionAuditMySQL(t *testing.T) {
	db := openVideoG6MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	created, err := f.service.ReserveAndCreate(context.Background(), f.command)
	if err != nil || created == nil {
		t.Fatalf("必须通过原G5事务形成恢复目标: %v", err)
	}
	var task model.AIImageTask
	if err := db.Where("public_id=?", f.command.TaskID).Take(&task).Error; err != nil {
		t.Fatal(err)
	}
	verified := time.Now().UTC().Add(-time.Minute)
	actor := authmodel.User{ID: NextVideoFixtureUserID(), PasswordHash: "synthetic-only", Status: "active", AdminPhoneVerifiedAt: &verified, AdminEmailVerifiedAt: &verified}
	if err := db.Create(&actor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:reconcile_manage'", actor.ID).Error; err != nil {
		t.Fatal(err)
	}
	secret := []byte("12345678901234567890123456789012")
	protector, err := NewVideoAdminReasonProtector("g7-dlq-test-v1", secret)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := NewVideoAdminService(&VideoHTTPService{db: db, billing: f.service}, 24, VideoAdminWriteOptions{ReasonProtector: protector})
	if err != nil {
		t.Fatal(err)
	}
	caller := VideoCaller{UserID: actor.ID, credential: &videoReadCredential{userID: actor.ID, expiresAt: time.Now().Add(time.Hour), revalidate: func(context.Context) error { return nil }}}
	command := VideoAdminDLQRecoveryCommand{Caller: caller, TaskID: task.PublicID, Stage: video.TaskSubmit, IdempotencyKey: "g7-dlq-recover-0001", Reason: "已核对原任务和冻结资金", VersionNo: task.VersionNo}
	message := video.TaskMessage{TaskID: task.PublicID, RequestID: task.RequestID, Attempt: 1}
	coordinator := &videoDLQRecoveryCoordinator{service: admin, command: command, commandHash: videoBillingDigest(fmt.Sprintf("video-admin-recovery:%d:%s", actor.ID, command.IdempotencyKey)), reasonHMAC: protector.digest("reason", command.Reason)}
	decision, err := coordinator.PrepareDeadLetterRecovery(context.Background(), video.TaskSubmit, message)
	if err != nil || decision != video.TaskDeadLetterHold || coordinator.reply == nil || !coordinator.reply.Pending {
		t.Fatalf("首次恢复必须原子写入Outbox并保留DLQ: decision=%d reply=%+v err=%v", decision, coordinator.reply, err)
	}
	// 模拟Outbox发布后工作消息抢先推进Task版本；完成审计必须依赖冻结请求事件，而非旧版本。
	if err := db.Exec("UPDATE ai_gateway_tasks SET version_no=version_no+1,updated_at=UTC_TIMESTAMP(6) WHERE id=? AND version_no=?", task.ID, task.VersionNo).Error; err != nil {
		t.Fatal(err)
	}
	outbox := repository.NewVideoOutboxRepository(db)
	var dispatch model.AIOutboxEvent
	if err := db.Where("aggregate_id=? AND event_type='video_dlq_recovery_dispatch'", task.RequestID).Take(&dispatch).Error; err != nil {
		t.Fatal(err)
	}
	lease := time.Now().UTC().Truncate(time.Second)
	if result := db.Model(&model.AIOutboxEvent{}).Where("id=? AND status='pending'", dispatch.ID).Updates(map[string]any{"status": model.AIOutboxPublishing, "locked_at": lease}); result.Error != nil || result.RowsAffected != 1 {
		t.Fatalf("恢复dispatch测试租约准备失败: %v", result.Error)
	}
	if err := outbox.MarkPublished(context.Background(), dispatch.ID, lease, lease); err != nil {
		t.Fatal(err)
	}
	coordinator = &videoDLQRecoveryCoordinator{service: admin, command: command, commandHash: coordinator.commandHash, reasonHMAC: coordinator.reasonHMAC}
	decision, err = coordinator.PrepareDeadLetterRecovery(context.Background(), video.TaskSubmit, message)
	if err != nil || decision != video.TaskDeadLetterAckExisting {
		t.Fatalf("Outbox已确认后必须补完成事实并允许ACK: decision=%d err=%v", decision, err)
	}
	var events, audits int64
	if err := db.Table("ai_gateway_task_events").Where("task_id=? AND event_type IN ('video_dlq_recovery_requested','video_dlq_recovery_published')", task.ID).Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("audit_logs").Where("operator_id=? AND action IN ('video_dlq_recovery_before','video_dlq_recovery_after')", actor.ID).Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if events != 2 || audits != 2 || coordinator.reply == nil || !coordinator.reply.Recovered {
		t.Fatalf("恢复必须留下请求、发布和前后审计: events=%d audits=%d reply=%+v", events, audits, coordinator.reply)
	}
	replay := &videoDLQRecoveryCoordinator{service: admin, command: command, commandHash: coordinator.commandHash, reasonHMAC: coordinator.reasonHMAC}
	decision, err = replay.PrepareDeadLetterRecovery(context.Background(), video.TaskSubmit, message)
	if err != nil || decision != video.TaskDeadLetterAckExisting || replay.reply == nil || !replay.reply.Existing {
		t.Fatalf("已发布恢复重放不得再次发布: decision=%d reply=%+v err=%v", decision, replay.reply, err)
	}
	secondFixture := newVideoG5ReservationFixture(t, db, "10")
	if _, err := secondFixture.service.ReserveAndCreate(context.Background(), secondFixture.command); err != nil {
		t.Fatal(err)
	}
	var secondTask model.AIImageTask
	if err := db.Where("public_id=?", secondFixture.command.TaskID).Take(&secondTask).Error; err != nil {
		t.Fatal(err)
	}
	changedIntent := command
	changedIntent.TaskID, changedIntent.VersionNo = secondTask.PublicID, secondTask.VersionNo
	changedMessage := video.TaskMessage{TaskID: secondTask.PublicID, RequestID: secondTask.RequestID, Attempt: 1}
	changedCoordinator := &videoDLQRecoveryCoordinator{service: admin, command: changedIntent, commandHash: coordinator.commandHash, reasonHMAC: coordinator.reasonHMAC}
	if _, err := changedCoordinator.PrepareDeadLetterRecovery(context.Background(), video.TaskSubmit, changedMessage); !errors.Is(err, ErrVideoAdminCommandConflict) {
		t.Fatalf("同一管理员Key不得改绑另一Task: %v", err)
	}
	wrong := message
	wrong.RequestID = "req_wrong"
	denied := &videoDLQRecoveryCoordinator{service: admin, command: command, commandHash: coordinator.commandHash, reasonHMAC: coordinator.reasonHMAC}
	if _, err := denied.PrepareDeadLetterRecovery(context.Background(), video.TaskSubmit, wrong); !errors.Is(err, ErrVideoAdminCommandConflict) {
		t.Fatalf("错Request不得取得恢复许可: %v", err)
	}
	bodySHA256 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	blockedAuditID, err := writeVideoPoisonAudit(context.Background(), db, nil, "video_rabbit_poison_blocked", video.TaskSubmit, bodySHA256, 0, "", "", "blocked")
	if err != nil {
		t.Fatal(err)
	}
	if result := db.Table("ai_video_rabbit_poison_fuses").Where("stage='submit' AND status='ready'").Updates(map[string]any{"status": "blocked", "body_sha256": bodySHA256, "blocked_audit_id": blockedAuditID, "recovered_audit_id": nil, "version_no": gorm.Expr("version_no+1"), "updated_at": time.Now().UTC()}); result.Error != nil || result.RowsAffected != 1 {
		t.Fatalf("合成熔断必须通过数据库绑定: %v", result.Error)
	}
	var fuse struct{ ID uint64 }
	if err := db.Table("audit_logs").Select("id").Where("action='video_rabbit_poison_blocked' AND target_id='submit'").Order("id DESC").Take(&fuse).Error; err != nil {
		t.Fatal(err)
	}
	poisonCommand := VideoAdminPoisonDiscardCommand{Caller: caller, Stage: video.TaskSubmit, IdempotencyKey: "g7-poison-discard", Reason: "已核对非法消息摘要", FuseAuditID: fuse.ID}
	crossKind := poisonCommand
	crossKind.IdempotencyKey = command.IdempotencyKey
	crossCoordinator := &videoPoisonDiscardCoordinator{service: admin, command: crossKind, commandHash: coordinator.commandHash, reasonHMAC: protector.digest("reason", crossKind.Reason)}
	if err := crossCoordinator.AuthorizeTaskPoisonDiscard(context.Background(), video.TaskSubmit, bodySHA256); !errors.Is(err, ErrVideoAdminCommandConflict) {
		t.Fatalf("同一管理员Key不得从DLQ恢复改成毒消息处置: %v", err)
	}
	poison := &videoPoisonDiscardCoordinator{service: admin, command: poisonCommand, commandHash: videoBillingDigest(fmt.Sprintf("video-admin-recovery:%d:%s", actor.ID, poisonCommand.IdempotencyKey)), reasonHMAC: protector.digest("reason", poisonCommand.Reason)}
	if err := poison.AuthorizeTaskPoisonDiscard(context.Background(), video.TaskSubmit, bodySHA256); err != nil {
		t.Fatal(err)
	}
	if err := poison.CompleteTaskPoisonDiscard(context.Background(), video.TaskSubmit, bodySHA256); err != nil || poison.reply == nil || !poison.reply.Discarded {
		t.Fatalf("毒消息处置必须写入恢复事实: reply=%+v err=%v", poison.reply, err)
	}
	var poisonAudits int64
	if err := db.Table("audit_logs").Where("target_type='video_queue' AND target_id='submit' AND action IN ('video_rabbit_poison_blocked','video_rabbit_poison_discard_before','video_rabbit_poison_discard_after','video_rabbit_poison_recovered')").Count(&poisonAudits).Error; err != nil || poisonAudits != 4 {
		t.Fatalf("毒消息必须保留熔断、前后审计和恢复四项事实: %d err=%v", poisonAudits, err)
	}
	replayedPoison := &videoPoisonDiscardCoordinator{service: admin, command: poisonCommand, commandHash: poison.commandHash, reasonHMAC: poison.reasonHMAC}
	if err := replayedPoison.AuthorizeTaskPoisonDiscard(context.Background(), video.TaskSubmit, bodySHA256); err != nil || replayedPoison.reply == nil || !replayedPoison.reply.Existing {
		t.Fatalf("ACK未知后的毒消息重放必须只补ACK: reply=%+v err=%v", replayedPoison.reply, err)
	}
	if err := db.Exec("UPDATE user_permission_overrides SET effect='deny' WHERE user_id=? AND permission_code='ai_gateway:reconcile_manage'", actor.ID).Error; err != nil {
		t.Fatal(err)
	}
	denied = &videoDLQRecoveryCoordinator{service: admin, command: command, commandHash: coordinator.commandHash, reasonHMAC: coordinator.reasonHMAC}
	if _, err := denied.PrepareDeadLetterRecovery(context.Background(), video.TaskSubmit, message); !errors.Is(err, ErrVideoAdminForbidden) {
		t.Fatalf("权限撤销后不能借已发布事实ACK死信: %v", err)
	}
}
