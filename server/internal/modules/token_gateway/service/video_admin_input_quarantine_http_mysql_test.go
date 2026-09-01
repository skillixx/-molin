package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

func TestVideoG6AdminInputQuarantineHTTPMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	adminCancelI2VFixture(t, &f)
	var original model.AIGatewayInputAsset
	if err := f.f.DB.Where("id=(SELECT input_asset_id FROM ai_gateway_task_inputs WHERE task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?))", f.task).Take(&original).Error; err != nil {
		t.Fatal(err)
	}
	var binding model.AIGatewayTaskInput
	if err := f.f.DB.Where("input_asset_id=?", original.ID).Take(&binding).Error; err != nil || binding.LeaseReleasedAt != nil {
		t.Fatal("隔离前必须存在原执行租约")
	}
	owner := service.VideoCaller{UserID: f.f.ProjectID, ProjectID: f.f.ProjectID, APIKeyID: f.f.ProjectID}
	legal, err := f.f.App.ImportImageInput(context.Background(), service.VideoInputImportCommand{Caller: owner, IdempotencyKey: "g6-admin-quarantine-legal-import", SourceAssetID: f.f.SourceID})
	if err != nil || legal.InputAssetID == nil {
		t.Fatal("必须建立独立保全输入")
	}
	if err := f.f.DB.Table("ai_gateway_input_assets").Where("public_id=?", *legal.InputAssetID).Updates(map[string]any{"legal_hold": true, "version_no": 2}).Error; err != nil {
		t.Fatal(err)
	}
	srv := f.server(t, "g6-input-quarantine-v1", f.secret)
	client := srv.Client()
	client.Timeout = 35 * time.Second
	const reason = "合成输入安全隔离原因"
	call := func(id, key, why string, version uint64, token string, status, code int) *service.VideoAdminInputQuarantineReply {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"reason": why, "version_no": version})
		r, _ := http.NewRequest("POST", srv.URL+"/api/admin/token/video-input-assets/"+id+"/quarantine", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
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
		if bytes.Contains(raw, []byte(reason)) {
			t.Fatal("隔离原因不得回显")
		}
		var e struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &e) != nil || resp.StatusCode != status || e.Code != code {
			t.Fatalf("隔离应%d/%d实际%d/%d", status, code, resp.StatusCode, e.Code)
		}
		if status >= 400 {
			if string(e.Data) != "null" {
				t.Fatal("失败不返回部分资源")
			}
			return nil
		}
		var reply service.VideoAdminInputQuarantineReply
		var fields map[string]json.RawMessage
		if json.Unmarshal(e.Data, &reply) != nil || json.Unmarshal(e.Data, &fields) != nil || len(fields) != 22 || reply.VideoAdminInputDetails == nil || reply.InputAssetID != id || reply.UserID != f.f.ProjectID {
			t.Fatal("隔离应返回原21字段元数据及幂等标记")
		}
		if resp.Header.Get("X-Molin-Request-ID") != "" || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("共享输入不能冒称单个任务request_id或公开缓存")
		}
		return &reply
	}
	key := "g6-admin-input-quarantine"
	finance := f.f.FinancialSnapshot()
	call(original.PublicID, key, reason, original.VersionNo, "", 401, 40001)
	call(original.PublicID, key, reason, original.VersionNo, f.f.Key, 401, 40001)
	call(original.PublicID, key, reason, original.VersionNo, f.f.Token, 403, 40003)
	call(original.PublicID, key, reason, original.VersionNo, f.token, 403, 40003)
	if err := f.f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:safety_review'", f.actor).Error; err != nil {
		t.Fatal(err)
	}
	call("vin_unknown_admin_input", key, reason, 1, f.token, 404, 40400)
	call(original.PublicID, key, reason, original.VersionNo+1, f.token, 409, 40900)
	first := call(original.PublicID, key, reason, original.VersionNo, f.token, 200, 0)
	if first.LifecycleState != "quarantined" || first.VersionNo != original.VersionNo+1 || first.Idempotent || first.ModerationStatus != original.ModerationStatus {
		t.Fatal("隔离只能推进生命周期与CAS，不能伪造审核结果")
	}
	var after model.AIGatewayInputAsset
	if err := f.f.DB.First(&after, original.ID).Error; err != nil {
		t.Fatal(err)
	}
	after.LifecycleState, after.VersionNo, after.UpdatedAt = original.LifecycleState, original.VersionNo, original.UpdatedAt
	if !reflect.DeepEqual(original, after) {
		t.Fatal("隔离不能改hash、规格、来源、期限、审核或保全事实")
	}
	var afterBinding model.AIGatewayTaskInput
	if err := f.f.DB.First(&afterBinding, binding.ID).Error; err != nil || !reflect.DeepEqual(binding, afterBinding) {
		t.Fatal("输入隔离不能释放或替换TaskInput")
	}
	if !f.f.InputPresent(original.PublicID) || !bytes.Equal(finance, f.f.FinancialSnapshot()) {
		t.Fatal("隔离不能删除正文或改资金")
	}
	if err := f.f.TrySubmit(f.task); err == nil || f.f.SubmitCalls() != 0 {
		t.Fatal("隔离后的原任务输入复验必须在Provider提交前失败关闭")
	}
	if !bytes.Equal(finance, f.f.FinancialSnapshot()) {
		t.Fatal("失败关闭不能释放Hold")
	}
	replay := call(original.PublicID, key, reason, original.VersionNo, f.token, 200, 0)
	if !replay.Idempotent || replay.VersionNo != first.VersionNo {
		t.Fatal("原版本重放不能重复隔离或增加版本")
	}
	call(original.PublicID, key, "另一隔离原因", original.VersionNo, f.token, 409, 40900)
	call(original.PublicID, key, reason, original.VersionNo+1, f.token, 409, 40900)
	call(original.PublicID, "g6-admin-input-new-command", reason, first.VersionNo, f.token, 409, 40900)
	call(*legal.InputAssetID, key, reason, 2, f.token, 409, 40900)
	kept := call(*legal.InputAssetID, "g6-admin-input-legal-quarantine", reason, 2, f.token, 200, 0)
	if !kept.LegalHold || kept.LifecycleState != "quarantined" || !f.f.InputPresent(*legal.InputAssetID) {
		t.Fatal("隔离必须保留保全及对象")
	}
	if err := f.f.DB.Table("users").Where("id=?", f.f.ProjectID).Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.DB.Table("api_keys").Where("id=?", f.f.ProjectID).Update("status", "revoked").Error; err != nil {
		t.Fatal(err)
	}
	if replay := call(original.PublicID, key, reason, original.VersionNo, f.token, 200, 0); !replay.Idempotent {
		t.Fatal("目标停用不影响有权管理员的原回执查询")
	}
	var rows int64
	if err := f.f.DB.Table("ai_video_admin_input_quarantines").Where("actor_user_id=?", f.actor).Count(&rows).Error; err != nil || rows != 2 {
		t.Fatal("两个目标各一份隔离回执")
	}
	if err := f.f.DB.Table("audit_logs").Where("operator_id=?", f.actor).Count(&rows).Error; err != nil || rows != 4 {
		t.Fatal("只保留两个命令各自前后审计")
	}
	var stored struct {
		ActorUserID    uint64
		CommandKeyHash string
		InitialVersion uint64
		service.VideoAdminReasonEnvelope
	}
	if err := f.f.DB.Table("ai_video_admin_input_quarantines").Where("actor_user_id=? AND input_asset_id=?", f.actor, original.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	p, err := service.NewVideoAdminReasonProtector("g6-input-quarantine-v1", f.secret)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := p.Open(service.VideoAdminReasonIdentity{ActorID: stored.ActorUserID, InputAssetID: original.PublicID, CommandKeyHash: stored.CommandKeyHash, VersionNo: stored.InitialVersion}, stored.VideoAdminReasonEnvelope); err != nil || string(plain) != reason {
		t.Fatal("输入隔离原因必须在专用AAD下可审阅")
	}
	if err := f.f.DB.Table("ai_video_admin_input_quarantines").Where("actor_user_id=?", f.actor).Update("initial_state", "pending").Error; err == nil {
		t.Fatal("隔离回执不可改写")
	}
	if err := f.f.DB.Table("users").Where("id=?", f.actor).Update("admin_email_verified_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	call(original.PublicID, key, reason, original.VersionNo, f.token, 403, 40031)
	if !bytes.Equal(finance, f.f.FinancialSnapshot()) {
		t.Fatal("管理输入隔离全程不得写钱包")
	}
}
