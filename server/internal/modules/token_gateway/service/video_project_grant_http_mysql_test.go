package service_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/service"
)

func TestVideoG6ProjectGrantHTTPMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	if err := f.f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:model_manage'", f.actor).Error; err != nil {
		t.Fatal(err)
	}
	projectID := service.NextVideoFixtureUserID()
	if err := f.f.DB.Exec("INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(?,?,'视频授权管理Project','active','disabled','UTC')", projectID, f.f.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	protector, err := service.NewVideoAdminReasonProtector("project-grant-test", f.secret)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: protector})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	token_gateway.RegisterVideoAdminRoutes(mux, admin, f.f.JWT, true)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	type result struct {
		ProjectID  uint64 `json:"project_id"`
		VersionNo  uint64 `json:"version_no"`
		Model      string `json:"model"`
		Status     string `json:"status"`
		Idempotent bool   `json:"idempotent"`
	}
	bodyWithReason := func(action string, version uint64, extra bool, reason string) []byte {
		fields := map[string]any{"action": action, "project_id": projectID, "model": f.f.Model, "version_no": version, "reason": reason}
		if extra {
			fields["actor_id"] = f.actor
		}
		raw, _ := json.Marshal(fields)
		return raw
	}
	body := func(action string, version uint64, extra bool) []byte {
		return bodyWithReason(action, version, extra, "Project授权私有原因")
	}
	call := func(key string, raw []byte) (int, result) {
		t.Helper()
		r, _ := http.NewRequest("POST", srv.URL+"/api/admin/token/video-project-grants", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+f.token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		res, err := srv.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		responseRaw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(responseRaw, []byte("Project授权私有原因")) || bytes.Contains(responseRaw, []byte("ciphertext")) {
			t.Fatal("授权响应泄露原因")
		}
		var envelope struct {
			Data result `json:"data"`
		}
		_ = json.Unmarshal(responseRaw, &envelope)
		return res.StatusCode, envelope.Data
	}
	snapshot := func() []byte {
		t.Helper()
		facts := map[string]any{"finance": json.RawMessage(f.f.FinancialSnapshot())}
		for _, table := range []string{"ai_project_model_capability_grants", "ai_video_project_grant_commands", "audit_logs"} {
			query := f.f.DB.Table(table)
			switch table {
			case "ai_project_model_capability_grants", "ai_video_project_grant_commands":
				query = query.Where("project_id=?", projectID)
			case "audit_logs":
				query = query.Where("operator_id=? AND action LIKE 'video_project_grant_%'", f.actor)
			}
			var rows []map[string]any
			if err := query.Order("id").Find(&rows).Error; err != nil {
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
	zeroFacts := snapshot()
	for index, reason := range []string{"", "包含\n控制字符"} {
		status, _ := call(fmt.Sprintf("video-project-invalid-reason-%02d", index), bodyWithReason("grant", 0, false, reason))
		if status != http.StatusBadRequest || !bytes.Equal(zeroFacts, snapshot()) {
			t.Fatalf("非法reason必须400且零事实：case=%d status=%d", index, status)
		}
	}
	if err := f.f.DB.Table("users").Where("id=?", f.actor).Updates(map[string]any{"admin_phone_verified_at": nil, "admin_email_verified_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	status, _ := call("video-project-grant-mfa-expired", body("grant", 0, false))
	if status != http.StatusForbidden || !bytes.Equal(zeroFacts, snapshot()) {
		t.Fatalf("MFA失效必须403且零事实：status=%d", status)
	}
	validMFA := time.Now().UTC().Add(-time.Minute)
	if err := f.f.DB.Table("users").Where("id=?", f.actor).Updates(map[string]any{"admin_phone_verified_at": validMFA, "admin_email_verified_at": validMFA}).Error; err != nil {
		t.Fatal(err)
	}
	for _, point := range []string{"before", "after"} {
		hook := "g6-project-grant-audit-" + point
		var hits atomic.Int64
		if err := f.f.DB.Callback().Create().Before("gorm:create").Register(hook, func(tx *gorm.DB) {
			if tx.Statement.Table != "audit_logs" {
				return
			}
			raw, _ := json.Marshal(tx.Statement.Dest)
			if bytes.Contains(raw, []byte("video_project_grant_grant_"+point)) {
				hits.Add(1)
				tx.AddError(errors.New("合成Project授权审计写入失败"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		status, _ = call("video-project-grant-audit-"+point, body("grant", 0, false))
		f.f.DB.Callback().Create().Remove(hook)
		if status != http.StatusServiceUnavailable || hits.Load() != 1 || !bytes.Equal(zeroFacts, snapshot()) {
			t.Fatalf("%s审计失败必须503并整笔回滚：status=%d hits=%d", point, status, hits.Load())
		}
	}
	finance := f.f.FinancialSnapshot()
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan result, 100)
	failures := make(chan string, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			status, item := call("video-project-grant-0001", body("grant", 0, false))
			if status != 200 || item.VersionNo != 1 || item.Status != "active" {
				failures <- fmt.Sprintf("status=%d result=%+v", status, item)
				return
			}
			results <- item
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if t.Failed() {
		return
	}
	first, replays := 0, 0
	for item := range results {
		if item.Idempotent {
			replays++
		} else {
			first++
		}
	}
	if first != 1 || replays != 99 {
		t.Fatalf("100并发未收敛 first=%d replay=%d", first, replays)
	}
	status, _ = call("video-project-grant-0001", body("grant", 1, false))
	if status != 409 {
		t.Fatal("同键异版本未冲突")
	}
	status, _ = call("video-project-grant-bad01", body("grant", 1, true))
	if status != 400 {
		t.Fatal("客户端actor字段未拒绝")
	}
	status, revoked := call("video-project-revoke-001", body("revoke", 1, false))
	if status != 200 || revoked.Status != "revoked" || revoked.VersionNo != 2 {
		t.Fatal("授权撤销失败")
	}
	status, _ = call("video-project-revoke-stale", body("revoke", 1, false))
	if status != 409 {
		t.Fatal("旧版本重复撤销未冲突")
	}
	status, granted := call("video-project-regrant-01", body("grant", 2, false))
	if status != 200 || granted.Status != "active" || granted.VersionNo != 3 {
		t.Fatal("授权恢复失败")
	}
	var grant struct {
		UserID, GrantedBy, VersionNo uint64
		Status                       string
	}
	if err := f.f.DB.Table("ai_project_model_capability_grants").Where("project_id=? AND logical_model_code=?", projectID, f.f.Model).Take(&grant).Error; err != nil || grant.UserID != f.f.ProjectID || grant.GrantedBy != f.actor || grant.VersionNo != 3 || grant.Status != "active" {
		t.Fatal("授权事实与Project归属不一致")
	}
	if err := f.f.DB.Exec("UPDATE ai_projects SET status='suspended' WHERE id=?", projectID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.DB.Exec("UPDATE token_models SET status='inactive' WHERE logical_model_code=?", f.f.Model).Error; err != nil {
		t.Fatal(err)
	}
	status, closed := call("video-project-revoke-closed", body("revoke", 3, false))
	if status != 200 || closed.Status != "revoked" || closed.VersionNo != 4 {
		t.Fatal("停用态无法撤销授权")
	}
	if err := f.f.DB.Exec("UPDATE ai_projects SET status='active' WHERE id=?", projectID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.DB.Exec("UPDATE token_models SET status='active' WHERE logical_model_code=?", f.f.Model).Error; err != nil {
		t.Fatal(err)
	}
	var commands, audits int64
	_ = f.f.DB.Table("ai_video_project_grant_commands").Where("project_id=?", projectID).Count(&commands).Error
	_ = f.f.DB.Table("audit_logs").Where("operator_id=? AND action LIKE 'video_project_grant_%'", f.actor).Count(&audits).Error
	if commands != 4 || audits != 8 {
		t.Fatalf("命令或审计数量异常 commands=%d audits=%d", commands, audits)
	}
	if !bytes.Equal(finance, f.f.FinancialSnapshot()) {
		t.Fatal("授权管理改变原请求或财务事实")
	}
	if err := f.f.DB.Exec("UPDATE user_permission_overrides SET effect='deny' WHERE user_id=? AND permission_code='ai_gateway:model_manage'", f.actor).Error; err != nil {
		t.Fatal(err)
	}
	status, _ = call("video-project-grant-0001", body("grant", 0, false))
	if status != 403 {
		t.Fatal("撤权后仍能重放授权")
	}
	r, _ := http.NewRequest("POST", srv.URL+"/api/admin/token/video-project-grants", bytes.NewReader(body("grant", 3, false)))
	r.Header.Set("Authorization", "Bearer "+f.f.Key)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "video-project-sk-denied")
	res, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("Project SK进入管理授权接口")
	}
}
