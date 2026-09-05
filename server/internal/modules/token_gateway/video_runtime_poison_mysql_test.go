package token_gateway

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	auditmodel "molin/server/internal/modules/audit/model"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7RuntimePoisonFenceMySQL(t *testing.T) {
	dsn := os.Getenv("MOLIN_VIDEO_G7_RUNTIME_MYSQL_DSN")
	if os.Getenv("MOLIN_VIDEO_G7_RUNTIME_ISOLATED") != "YES" || !strings.Contains(dsn, "/molin_video_g6_contract?") {
		t.Skip("仅在G7隔离运行时MySQL执行")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	digest := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	first := &VideoRuntime{db: db}
	if err := first.blockRabbitPoison(context.Background(), video.TaskFetch, digest); err != nil {
		t.Fatal(err)
	}
	second := &VideoRuntime{db: db}
	blocked, err := second.rabbitPoisonBlocked(context.Background(), video.TaskFetch)
	if err != nil || !blocked {
		t.Fatalf("新Runtime必须从同一MySQL恢复毒消息熔断: blocked=%v err=%v", blocked, err)
	}
	var fuse struct {
		BlockedAuditID uint64
		VersionNo      uint64
	}
	if err := db.Table("ai_video_rabbit_poison_fuses").Select("blocked_audit_id,version_no").Where("stage='fetch'").Take(&fuse).Error; err != nil || fuse.BlockedAuditID == 0 {
		t.Fatal("必须读回受约束熔断事实")
	}
	commandHash := strings.Repeat("d", 64)
	reasonHMAC := strings.Repeat("e", 64)
	raw, _ := json.Marshal(map[string]any{"schema": 1, "kind": "poison", "stage": video.TaskFetch, "body_sha256": digest, "fuse_audit_id": fuse.BlockedAuditID, "command_key_hash": commandHash, "reason_hmac": reasonHMAC, "result": "recovered"})
	target, targetID, summary := "video_queue", string(video.TaskFetch), string(raw)
	// 裸插恢复审计不得改变状态；只有随后满足Trigger绑定的CAS才能解除。
	bare := auditmodel.AuditLog{Module: "token_gateway", Action: "video_rabbit_poison_recovered", TargetType: &target, TargetID: &targetID, RequestSummary: &summary, CreatedAt: time.Now().UTC()}
	if err := db.Create(&bare).Error; err != nil {
		t.Fatal(err)
	}
	third := &VideoRuntime{db: db}
	blocked, err = third.rabbitPoisonBlocked(context.Background(), video.TaskFetch)
	if err != nil || !blocked {
		t.Fatalf("裸插普通审计不能解除熔断: blocked=%v err=%v", blocked, err)
	}
	if result := db.Table("ai_video_rabbit_poison_fuses").Where("stage='fetch' AND version_no=?", fuse.VersionNo).Updates(map[string]any{"status": "ready", "recovered_audit_id": bare.ID, "version_no": gorm.Expr("version_no+1"), "updated_at": time.Now().UTC()}); result.Error == nil {
		t.Fatal("无操作者的伪造恢复审计必须被数据库Trigger拒绝")
	}
	actor := uint64(980000001)
	if err := db.Exec("INSERT IGNORE INTO users(id,password_hash,real_name_status,status) VALUES(?,'synthetic-only','verified','active')", actor).Error; err != nil {
		t.Fatal("创建合成恢复主体失败")
	}
	recovered := auditmodel.AuditLog{OperatorID: &actor, Module: "token_gateway", Action: "video_rabbit_poison_recovered", TargetType: &target, TargetID: &targetID, RequestSummary: &summary, CreatedAt: time.Now().UTC()}
	if err := db.Create(&recovered).Error; err != nil {
		t.Fatal(err)
	}
	if result := db.Table("ai_video_rabbit_poison_fuses").Where("stage='fetch' AND status='blocked' AND version_no=?", fuse.VersionNo).Updates(map[string]any{"status": "ready", "recovered_audit_id": recovered.ID, "version_no": gorm.Expr("version_no+1"), "updated_at": time.Now().UTC()}); result.Error == nil {
		t.Fatal("缺少受控前后审计链的有操作者恢复事实也必须被拒绝")
	}
	beforeRaw, _ := json.Marshal(map[string]any{"schema": 1, "kind": "poison", "stage": video.TaskFetch, "body_sha256": digest, "fuse_audit_id": fuse.BlockedAuditID, "command_key_hash": commandHash, "reason_hmac": reasonHMAC, "result": "authorized"})
	afterRaw, _ := json.Marshal(map[string]any{"schema": 1, "kind": "poison", "stage": video.TaskFetch, "body_sha256": digest, "fuse_audit_id": fuse.BlockedAuditID, "command_key_hash": commandHash, "reason_hmac": reasonHMAC, "result": "discarded"})
	beforeSummary, afterSummary := string(beforeRaw), string(afterRaw)
	before := auditmodel.AuditLog{OperatorID: &actor, Module: "token_gateway", Action: "video_rabbit_poison_discard_before", TargetType: &target, TargetID: &targetID, RequestSummary: &beforeSummary, CreatedAt: time.Now().UTC()}
	after := auditmodel.AuditLog{OperatorID: &actor, Module: "token_gateway", Action: "video_rabbit_poison_discard_after", TargetType: &target, TargetID: &targetID, RequestSummary: &afterSummary, CreatedAt: time.Now().UTC()}
	if err := db.Create(&before).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&after).Error; err != nil {
		t.Fatal(err)
	}
	recovered = auditmodel.AuditLog{OperatorID: &actor, Module: "token_gateway", Action: "video_rabbit_poison_recovered", TargetType: &target, TargetID: &targetID, RequestSummary: &summary, CreatedAt: time.Now().UTC()}
	if err := db.Create(&recovered).Error; err != nil {
		t.Fatal(err)
	}
	if result := db.Table("ai_video_rabbit_poison_fuses").Where("stage='fetch' AND status='blocked' AND version_no=?", fuse.VersionNo).Updates(map[string]any{"status": "ready", "recovered_audit_id": recovered.ID, "version_no": gorm.Expr("version_no+1"), "updated_at": time.Now().UTC()}); result.Error != nil || result.RowsAffected != 1 {
		t.Fatalf("正确绑定的恢复事实必须解除熔断: %v", result.Error)
	}
	fourth := &VideoRuntime{db: db}
	blocked, err = fourth.rabbitPoisonBlocked(context.Background(), video.TaskFetch)
	if err != nil || blocked {
		t.Fatalf("受控状态迁移后新Runtime必须允许重新装配: blocked=%v err=%v", blocked, err)
	}
}
