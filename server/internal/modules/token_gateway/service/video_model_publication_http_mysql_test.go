package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"molin/server/internal/modules/token_gateway/handler"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG6ModelPublicationHTTPMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	if err := f.f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:model_manage'", f.actor).Error; err != nil {
		t.Fatal(err)
	}
	protector, err := service.NewVideoAdminReasonProtector("model-publication-test", f.secret)
	if err != nil {
		t.Fatal(err)
	}
	provider := video.NewFakeAsyncVideoAdapter(video.FakeVideoSuccess)
	admin, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: protector, ModelDrafts: &service.VideoModelDraftOptions{}, ModelPublishing: &service.VideoModelPublishOptions{Provider: provider, ConfigVersion: "runware-fixture-v1", Models: map[string]string{f.f.Model: "runway:1@2"}}})
	if err != nil {
		t.Fatal(err)
	}
	caller, err := f.f.JWT.Authenticate(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	details, err := admin.GetModelDraft(context.Background(), caller, f.f.ProjectID)
	if err != nil || details.SourceSHA256 == nil {
		t.Fatal("发布夹具必须先读取历史草稿摘要")
	}
	definition := details.Definition
	docs, quick := "https://docs.example.invalid/video", "https://docs.example.invalid/video/quick"
	definition.DisplayName = "准备发布的视频模型"
	definition.DocsURL = &docs
	definition.QuickStartURL = &quick
	definition.DocsURLHealthStatus = "healthy"
	definition.QuickStartURLHealthStatus = "healthy"
	definition.VideoContract = json.RawMessage(`{"schema_version":1,"purpose":"non_commercial_test_fixture","supported_operations":["text_to_video"],"default_model":false,"asset_required":false,"required_entitlement_type":null,"required_membership_levels":[]}`)
	draft, err := admin.SaveModelDraft(context.Background(), service.VideoModelDraftCommand{Caller: caller, ModelID: f.f.ProjectID, VersionNo: 0, SourceSHA256: *details.SourceSHA256, IdempotencyKey: "video-publish-adopt-001", Reason: "发布前受控接管", Definition: definition})
	if err != nil || draft.VersionNo != 1 {
		t.Fatal("发布前草稿接管失败")
	}
	g5 := handler.NewG5AdminHandler(service.NewG5AdminService(repository.NewG5AdminRepository(f.f.DB), repository.NewG3PricingRepository(f.f.DB)), nil).WithVideoPublications(handler.NewVideoAdminHandler(admin, f.f.JWT, true))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/admin/token/models/{id}/publish", g5.PublishModel)
	mux.HandleFunc("POST /api/admin/token/models/{id}/unpublish", g5.UnpublishModel)
	mux.HandleFunc("POST /api/admin/token/models/{id}/rollback", g5.RollbackModel)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	call := func(action, key string, version, target uint64) (int, service.VideoModelPublicationReply, []byte) {
		t.Helper()
		body := map[string]any{"version_no": version, "reason": "模型发布的私有原因"}
		if action == "rollback" {
			body["target_version_no"] = target
		}
		raw, _ := json.Marshal(body)
		r, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/admin/token/models/%d/%s", srv.URL, f.f.ProjectID, action), bytes.NewReader(raw))
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
		if bytes.Contains(responseRaw, []byte("模型发布的私有原因")) || bytes.Contains(responseRaw, []byte("ciphertext")) {
			t.Fatal("发布响应泄露原因")
		}
		var envelope struct {
			Data service.VideoModelPublicationReply `json:"data"`
		}
		_ = json.Unmarshal(responseRaw, &envelope)
		return res.StatusCode, envelope.Data, responseRaw
	}
	finance := f.f.FinancialSnapshot()
	status, published, _ := call("publish", "video-model-publish-001", 1, 0)
	if status != 201 || published.ModelID != f.f.ProjectID || published.VersionNo != 2 || published.ReleaseVersionNo != 2 || published.PublicationStatus != "active" || published.Idempotent {
		t.Fatalf("首次发布结果异常 status=%d result=%+v", status, published)
	}
	status, replay, _ := call("publish", "video-model-publish-001", 1, 0)
	if status != 200 || !replay.Idempotent || replay.ReleaseVersionNo != 2 {
		t.Fatal("发布重放没有返回原版本")
	}
	if provider.SubmitCalls() != 0 {
		t.Fatal("模型发布调用了Fake Provider Submit")
	}
	var release struct {
		SnapshotJSON json.RawMessage `gorm:"column:snapshot_json"`
		Reason       string
	}
	if err := f.f.DB.Table("ai_model_release_versions").Where("model_id=? AND version_no=2 AND status='active'", f.f.ProjectID).Take(&release).Error; err != nil {
		t.Fatal("发布版本不存在")
	}
	var snapshot map[string]json.RawMessage
	if json.Unmarshal(release.SnapshotJSON, &snapshot) != nil || len(snapshot["video_execution"]) == 0 || len(snapshot["video_contract"]) == 0 || len(snapshot["channel_id"]) != 0 || len(snapshot["upstream_model"]) != 0 || bytes.Contains([]byte(release.Reason), []byte("私有")) {
		t.Fatal("视频发布快照或低敏Reason不符合合同")
	}
	status, unpublished, _ := call("unpublish", "video-model-unpublish-001", 2, 0)
	if status != 200 || unpublished.VersionNo != 3 || unpublished.ReleaseVersionNo != 2 || unpublished.PublicationStatus != "inactive" {
		t.Fatal("下架没有保留发布指针")
	}
	status, _, _ = call("rollback", "video-model-rollback-legacy", 3, 1)
	if status != 409 {
		t.Fatal("缺native证明的旧版本被回滚")
	}
	status, rolled, _ := call("rollback", "video-model-rollback-002", 3, 2)
	if status != 201 || rolled.VersionNo != 4 || rolled.ReleaseVersionNo != 3 || rolled.PublicationStatus != "active" {
		t.Fatal("受控回滚没有创建新发布版本")
	}
	status, again, _ := call("rollback", "video-model-rollback-002", 3, 2)
	if status != 200 || !again.Idempotent || again.ReleaseVersionNo != 3 {
		t.Fatal("回滚重放不一致")
	}
	if provider.SubmitCalls() != 0 || !bytes.Equal(finance, f.f.FinancialSnapshot()) {
		t.Fatal("发布链改变Provider调用或原财务事实")
	}
	var commands, audits int64
	_ = f.f.DB.Table("ai_video_model_draft_commands").Where("model_id=? AND action IN ('publish','unpublish','rollback')", f.f.ProjectID).Count(&commands).Error
	_ = f.f.DB.Table("audit_logs").Where("operator_id=? AND action LIKE 'video_model_%'", f.actor).Count(&audits).Error
	if commands != 3 || audits != 8 {
		t.Fatalf("命令或审计数量异常 commands=%d audits=%d", commands, audits)
	} // 接管2条审计+3动作各2条。
	if err := repository.NewG5AdminRepository(f.f.DB).UnpublishModel(context.Background(), f.f.ProjectID, f.actor); err == nil {
		t.Fatal("旧G5入口绕过受控视频下架")
	}
}
