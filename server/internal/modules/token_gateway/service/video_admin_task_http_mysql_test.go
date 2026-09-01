package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	authmodel "molin/server/internal/modules/auth/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

func TestVideoG6AdminTaskHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	job, err := f.App.Create(context.Background(), service.VideoCommand{Caller: service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}, IdempotencyKey: "g6-admin-task-detail", Model: f.Model, Prompt: "仅用于管理员查询隔离测试", Operation: model.AIVideoOperationTextToVideo})
	if err != nil {
		t.Fatal(err)
	}
	completedID := f.CreateCompletedForKey(f.ProjectID)
	verified := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	admin := authmodel.User{ID: service.NextVideoFixtureUserID(), PasswordHash: "synthetic-only", Status: "active", AdminPhoneVerifiedAt: &verified, AdminEmailVerifiedAt: &verified}
	if err := f.DB.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:view'", admin.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.First(&admin, admin.ID).Error; err != nil || admin.AdminPhoneVerifiedAt == nil || admin.AdminEmailVerifiedAt == nil || !admin.AdminPhoneVerifiedAt.Equal(verified) || !admin.AdminEmailVerifiedAt.Equal(verified) {
		t.Fatal("必须读回真实秒级MFA事实")
	}
	app, err := service.NewVideoAdminService(f.App, 24)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gateway.RegisterVideoAdminRoutes(mux, app, f.JWT, true)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	token := f.TokenForUser(admin.ID)
	path := "/api/admin/token/video-tasks/" + job.Job.ID
	call := func(path, token string, wantStatus, wantCode int) json.RawMessage {
		t.Helper()
		r, err := http.NewRequest("GET", srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
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
		var e struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &e) != nil || resp.StatusCode != wantStatus || e.Code != wantCode {
			t.Fatalf("管理详情应%d/%d实际%d/%d", wantStatus, wantCode, resp.StatusCode, e.Code)
		}
		return e.Data
	}
	captureBusiness := func() []byte {
		t.Helper()
		facts := map[string]any{"finance": json.RawMessage(f.FinancialSnapshot())}
		for _, table := range []string{"ai_gateway_tasks", "ai_gateway_task_events", "ai_gateway_provider_callback_events", "ai_compensation_tasks", "ai_gateway_task_inputs", "ai_gateway_assets"} {
			q := f.DB.Table(table).Order("id")
			if table == "ai_compensation_tasks" {
				q = q.Where("aggregate_id IN (SELECT request_id FROM ai_requests WHERE user_id=?)", f.ProjectID)
			} else {
				q = q.Where("user_id=?", f.ProjectID)
			}
			var rows []map[string]any
			if err := q.Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			facts[table] = rows
		}
		raw, err := json.Marshal(facts)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := captureBusiness()
	heads, deletes, submits := f.HeadCalls(), f.MediaDeleteCalls(), f.SubmitCalls()
	call(path, "", 401, 40001)
	call(path, f.Key, 401, 40001)
	call(path, f.Token, 403, 40003)
	data := call(path, token, 200, 0)
	var fields map[string]json.RawMessage
	var detail service.VideoAdminTaskDetails
	if json.Unmarshal(data, &fields) != nil || len(fields) != 28 || json.Unmarshal(data, &detail) != nil {
		t.Fatal("管理详情必须为28个低敏字段")
	}
	for _, name := range []string{"task_id", "video_id", "request_id", "quote_id", "model", "operation", "execution_status", "billing_status", "delivery_status", "progress", "version_no", "request_version_no", "quoted_amount", "held_amount", "current_frozen_amount", "settled_amount", "net_released_amount", "hold_status", "currency", "created_at", "completed_at", "media_deleted", "media_partially_deleted", "media_deletion_pending", "can_deliver", "user_id", "project_id", "api_key_id"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("缺字段：%s", name)
		}
	}
	if detail.TaskID != job.Job.ID || detail.RequestID != job.RequestID || detail.UserID != f.ProjectID || detail.UserID == admin.ID || detail.ProjectID != f.ProjectID || detail.APIKeyID == nil || *detail.APIKeyID != f.ProjectID || detail.ExecutionStatus != "reserved" || detail.BillingStatus != "held" || detail.DeliveryStatus != "pending" || detail.CanDeliver {
		t.Fatal("不得把管理员身份拼接为目标归属或伪造交付")
	}
	var completed service.VideoAdminTaskDetails
	if json.Unmarshal(call("/api/admin/token/video-tasks/"+completedID, token, 200, 0), &completed) != nil || completed.ExecutionStatus != "succeeded" || completed.BillingStatus != "settled" || !completed.CanDeliver {
		t.Fatal("已完成任务必须真实读取原G5对账事实")
	}
	// 管理查看资格属于管理员，不借用已禁用目标用户/Key的终端调用资格。
	if err := f.DB.Table("users").Where("id=?", f.ProjectID).Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Table("api_keys").Where("id=?", f.ProjectID).Update("status", "revoked").Error; err != nil {
		t.Fatal(err)
	}
	call(path, token, 200, 0)
	if err := f.DB.Table("users").Where("id=?", f.ProjectID).Update("status", "active").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Table("api_keys").Where("id=?", f.ProjectID).Update("status", "active").Error; err != nil {
		t.Fatal(err)
	}
	call("/api/admin/token/video-tasks/vid-unknown", token, 404, 40400)
	call(path+"?user_id=1", token, 400, 40000)
	for _, tc := range []struct {
		name, column string
		value        any
	}{
		{"手机缺失", "admin_phone_verified_at", nil}, {"邮箱缺失", "admin_email_verified_at", nil},
		{"手机过期", "admin_phone_verified_at", verified.Add(-25 * time.Hour)}, {"邮箱过期", "admin_email_verified_at", verified.Add(-25 * time.Hour)},
		{"手机未来", "admin_phone_verified_at", verified.Add(2 * time.Hour)},
	} {
		if err := f.DB.Model(&authmodel.User{}).Where("id=?", admin.ID).Update(tc.column, tc.value).Error; err != nil {
			t.Fatal(err)
		}
		call(path, token, 403, 40031)
		if err := f.DB.Model(&authmodel.User{}).Where("id=?", admin.ID).Update(tc.column, verified).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.DB.Exec("UPDATE user_permission_overrides SET effect='deny' WHERE user_id=? AND permission_code='ai_gateway:view'", admin.ID).Error; err != nil {
		t.Fatal(err)
	}
	call(path, token, 403, 40003)
	if err := f.DB.Exec("UPDATE user_permission_overrides SET effect='allow' WHERE user_id=? AND permission_code='ai_gateway:view'", admin.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, captureBusiness()) || heads != f.HeadCalls() || deletes != f.MediaDeleteCalls() || submits != f.SubmitCalls() {
		t.Fatal("管理查询不得写业务事实或调用存储/Provider")
	}
	// 服务不能把普通用户JWT的有效性借给另一个管理员ID，未来写入口也必须保留此主体绑定。
	forged, err := f.JWT.Authenticate(context.Background(), f.Token)
	if err != nil {
		t.Fatal(err)
	}
	forged.UserID = admin.ID
	if result, err := app.GetTask(context.Background(), forged, job.Job.ID); err == nil || result != nil {
		t.Error("JWT认证主体不能被调用参数替换")
	}
	// 只让MFA权威服务的第二次完整用户读取失败，不能把数据库故障伪装为需要重新验证。
	var fault atomic.Bool
	const hook = "g6_admin_mfa_read_failure"
	if err := f.DB.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
		user, ok := tx.Statement.Dest.(*authmodel.User)
		if ok && user.ID == admin.ID && tx.Error == nil && len(tx.Statement.Selects) == 0 && fault.CompareAndSwap(false, true) {
			tx.AddError(errors.New("合成MFA读取故障"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.DB.Callback().Query().Remove(hook) })
	call(path, token, 503, 50300)
	if !fault.Load() {
		t.Fatal("必须真实命中MFA数据读取故障")
	}
	if !bytes.Equal(before, captureBusiness()) || heads != f.HeadCalls() || deletes != f.MediaDeleteCalls() || submits != f.SubmitCalls() {
		t.Fatal("主体错绑与MFA故障拒绝之后也不得遗留业务写入或外部调用")
	}
}
