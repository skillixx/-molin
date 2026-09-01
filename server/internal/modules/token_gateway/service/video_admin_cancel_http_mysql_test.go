package service_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	authmodel "molin/server/internal/modules/auth/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/service"
)

func TestVideoG6AdminCancelHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	owner := service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	create := func(key string) string {
		t.Helper()
		job, err := f.App.Create(context.Background(), service.VideoCommand{Caller: owner, IdempotencyKey: key, Model: f.Model, Prompt: "合成管理取消任务Prompt", Operation: "text_to_video"})
		if err != nil {
			t.Fatal(err)
		}
		return job.Job.ID
	}
	taskID := create("g6-admin-cancel-first")
	submittedID := create("g6-admin-cancel-submitted")
	f.Submit(submittedID)
	rollbackID := create("g6-admin-cancel-rollback")
	version := func(id string) uint64 {
		t.Helper()
		var v uint64
		if err := f.DB.Table("ai_gateway_tasks").Select("version_no").Where("public_id=?", id).Scan(&v).Error; err != nil || v == 0 {
			t.Fatal("必须读取原Task版本")
		}
		return v
	}
	initial, submittedVersion, rollbackVersion := version(taskID), version(submittedID), version(rollbackID)
	verified := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	admin := authmodel.User{ID: service.NextVideoFixtureUserID(), PasswordHash: "synthetic-only", Status: "active", AdminPhoneVerifiedAt: &verified, AdminEmailVerifiedAt: &verified}
	if err := f.DB.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	protector, err := service.NewVideoAdminReasonProtector("g6-admin-http-v1", secret)
	if err != nil {
		t.Fatal(err)
	}
	app, err := service.NewVideoAdminService(f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: protector})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gateway.RegisterVideoAdminRoutes(mux, app, f.JWT, true)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 35 * time.Second
	token := f.TokenForUser(admin.ID)
	const reason = "合成原因：管理员按用户申请取消"
	body := func(v uint64, why string) string {
		raw, _ := json.Marshal(map[string]any{"version_no": v, "reason": why})
		return string(raw)
	}
	call := func(id, key, payload, credential string, status, code int) *service.VideoAdminCancellationReply {
		t.Helper()
		r, _ := http.NewRequest("POST", srv.URL+"/api/admin/token/video-tasks/"+id+"/cancel", strings.NewReader(payload))
		r.Header.Set("Content-Type", "application/json")
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(reason)) || bytes.Contains(raw, []byte("合成管理取消任务Prompt")) {
			t.Fatal("管理响应不得回显原因或Prompt")
		}
		var e struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &e) != nil || resp.StatusCode != status || e.Code != code {
			t.Fatalf("管理取消应%d/%d，实际%d/%d", status, code, resp.StatusCode, e.Code)
		}
		if status >= 400 {
			if string(e.Data) != "null" {
				t.Fatal("失败不能返回部分取消结果")
			}
			return nil
		}
		var reply service.VideoAdminCancellationReply
		var fields map[string]json.RawMessage
		if json.Unmarshal(e.Data, &reply) != nil || json.Unmarshal(e.Data, &fields) != nil || len(fields) != 31 || reply.UserID != f.ProjectID || reply.TaskID != id {
			t.Fatal("管理取消应31字段并保留目标归属")
		}
		if resp.Header.Get("X-Molin-Request-ID") != reply.RequestID {
			t.Fatal("取消重放必须保留业务请求ID")
		}
		return &reply
	}
	key := "g6-admin-cancel-command"
	call(taskID, key, body(initial, reason), "", 401, 40001)
	call(taskID, key, body(initial, reason), f.Key, 401, 40001)
	call(taskID, key, body(initial, reason), f.Token, 403, 40003)
	call(taskID, key, body(initial, reason), token, 403, 40003)
	if err := f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:task_manage'", admin.ID).Error; err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{}`, `{"reason":"","version_no":1}`, `{"reason":"x","reason":"y","version_no":1}`, `{"reason":"x","version_no":1,"checker_id":1}`, `{"Reason":"x","version_no":1}`, `{"reason":"x","version_no":0}`} {
		call(taskID, key, bad, token, 400, 40000)
	}
	call(taskID, key, body(initial+1, reason), token, 409, 40900)
	call(taskID, "", body(initial, reason), token, 400, 40000)
	// 管理权限不借用目标用户生成资格；被停用的目标仍可安全取消未提交任务。
	if err := f.DB.Table("users").Where("id=?", f.ProjectID).Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Table("api_keys").Where("id=?", f.ProjectID).Update("status", "revoked").Error; err != nil {
		t.Fatal(err)
	}
	submits := f.SubmitCalls()
	first := call(taskID, key, body(initial, reason), token, 200, 0)
	if first.CancellationResult != "cancelled" || first.BillingStatus != "released" || first.Idempotent || first.CancelRequestedAt == nil {
		t.Fatal("未提交任务必须原子释放并保留取消事实")
	}
	finance := f.FinancialSnapshot()
	replay := call(taskID, key, body(initial, reason), token, 200, 0)
	if !replay.Idempotent || replay.RequestID != first.RequestID || replay.VersionNo != first.VersionNo {
		t.Fatal("原版本重放不能被自身CAS变化误拒绝")
	}
	call(taskID, key, body(initial, "另一合成原因"), token, 409, 40900)
	call(submittedID, key, body(submittedVersion, reason), token, 409, 40900)
	if !bytes.Equal(finance, f.FinancialSnapshot()) {
		t.Fatal("幂等和冲突不得二次释放")
	}
	var auditBodies []string
	if err := f.DB.Table("audit_logs").Where("operator_id=? AND target_id=?", admin.ID, taskID).Order("id").Pluck("request_summary", &auditBodies).Error; err != nil || len(auditBodies) != 2 {
		t.Fatal("必须恰有原操作者前后两条审计，重放不新增")
	}
	for _, raw := range auditBodies {
		if strings.Contains(raw, reason) || !strings.Contains(raw, "reason_hmac") {
			t.Fatal("普通审计只允许原因摘要及引用")
		}
	}
	var record struct {
		ActorUserID    uint64
		CommandKeyHash string
		InitialVersion uint64
		service.VideoAdminReasonEnvelope
	}
	if err := f.DB.Table("ai_video_admin_cancellation_commands").Where("actor_user_id=? AND task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", admin.ID, taskID).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	plain, err := protector.Open(service.VideoAdminReasonIdentity{ActorID: record.ActorUserID, TaskID: taskID, CommandKeyHash: record.CommandKeyHash, VersionNo: record.InitialVersion}, record.VideoAdminReasonEnvelope)
	if err != nil || string(plain) != reason {
		t.Fatal("原因只能通过原绑定的AES-GCM信封审阅")
	}
	if err := f.DB.Table("ai_video_admin_cancellation_commands").Where("actor_user_id=?", admin.ID).Update("initial_result", "already_terminal").Error; err == nil {
		t.Fatal("管理回执必须不可修改")
	}
	pending := call(submittedID, "g6-admin-cancel-pending", body(submittedVersion, reason), token, 202, 0)
	if pending.CancellationResult != "cancel_requested" || pending.BillingStatus != "held" || pending.HoldStatus == nil || *pending.HoldStatus != "holding" {
		t.Fatal("已提交只记录意图，不得释放Hold")
	}
	if !bytes.Equal(finance, f.FinancialSnapshot()) {
		t.Fatal("已提交取消意图不允许改财务")
	}
	// 事后审计写失败必须回滚前置审计、G5退款和命令，不能返回无审计的成功。
	const hook = "g6-admin-cancel-after-audit-failure"
	if err := f.DB.Callback().Create().Before("gorm:create").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_logs" {
			raw, _ := json.Marshal(tx.Statement.Dest)
			if bytes.Contains(raw, []byte("video_admin_cancel_after")) {
				tx.AddError(fmt.Errorf("合成事后审计失败"))
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.DB.Callback().Create().Remove(hook) })
	call(rollbackID, "g6-admin-cancel-audit-rollback", body(rollbackVersion, reason), token, 503, 50300)
	f.DB.Callback().Create().Remove(hook)
	if version(rollbackID) != rollbackVersion || !bytes.Equal(finance, f.FinancialSnapshot()) {
		t.Fatal("审计失败必须回滚G5状态与资金")
	}
	if err := f.DB.Table("users").Where("id=?", admin.ID).Update("admin_phone_verified_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	call(taskID, key, body(initial, reason), token, 403, 40031)
	if f.SubmitCalls() != submits {
		t.Fatal("管理取消不能调用或重新提交Provider")
	}
}
