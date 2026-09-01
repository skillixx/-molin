package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"molin/server/internal/modules/token_gateway/handler"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
)

func TestVideoG6ModelDraftHTTPMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	if err := f.f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:model_manage'", f.actor).Error; err != nil {
		t.Fatal(err)
	}
	p, err := service.NewVideoAdminReasonProtector("model-draft-test", f.secret)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: p, ModelDrafts: &service.VideoModelDraftOptions{}})
	if err != nil {
		t.Fatal(err)
	}
	catalog := service.NewCatalogService(repository.NewTokenModelRepository(f.f.DB))
	h := handler.NewModelHandler(catalog).WithVideoDrafts(handler.NewVideoAdminHandler(admin, f.f.JWT, true), nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/admin/token/models", h.CreateModel)
	mux.HandleFunc("PATCH /api/admin/token/models/{id}", h.UpdateModel)
	mux.HandleFunc("GET /api/admin/token/models/{id}", h.GetModel)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := srv.Client()
	call := func(method, path, key, token string, body []byte) (int, service.VideoModelDraftReply, error) {
		r, e := http.NewRequest(method, srv.URL+path, bytes.NewReader(body))
		if e != nil {
			return 0, service.VideoModelDraftReply{}, e
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Idempotency-Key", key)
		res, e := client.Do(r)
		if e != nil {
			return 0, service.VideoModelDraftReply{}, e
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(res.Body)
		if e != nil {
			return res.StatusCode, service.VideoModelDraftReply{}, e
		}
		if bytes.Contains(raw, []byte("模型变更的私有原因")) || bytes.Contains(raw, []byte("ciphertext")) || bytes.Contains(raw, []byte("reason_hmac")) {
			return res.StatusCode, service.VideoModelDraftReply{}, fmt.Errorf("管理响应泄露私有原因")
		}
		var envelope struct {
			Data service.VideoModelDraftReply `json:"data"`
		}
		if e = json.Unmarshal(raw, &envelope); e != nil {
			return res.StatusCode, envelope.Data, e
		}
		service.ReserveVideoFixtureIDsThrough(envelope.Data.ModelID)
		return res.StatusCode, envelope.Data, nil
	}
	definition := service.VideoModelDraftDefinition{LogicalModelCode: fmt.Sprintf("molin/video-admin-draft-%d", f.actor), DisplayName: "受控视频草稿", VisibleScope: "all", GroupIDs: []uint64{}, GroupRoles: []string{}, RoleCodes: []string{}, DocsURLHealthStatus: "unpublished", QuickStartURLHealthStatus: "unpublished", VideoContract: json.RawMessage(`{"schema_version":1,"purpose":"non_commercial_test_fixture","supported_operations":["text_to_video","image_to_video"],"default_model":false,"asset_required":false,"required_entitlement_type":null,"required_membership_levels":[]}`)}
	body := func(version uint64, d service.VideoModelDraftDefinition) []byte {
		b, e := json.Marshal(map[string]any{"version_no": version, "reason": "模型变更的私有原因", "video_definition": d})
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	createBody := body(0, definition)
	path := "/api/admin/token/models"
	key := "video-model-create-0001"
	finance := f.f.FinancialSnapshot()
	status, created, err := call("POST", path, key, f.token, createBody)
	if err != nil || status != 201 || created.ModelID == 0 || created.VersionNo != 1 || created.ReleaseVersionNo != 0 || created.Idempotent {
		t.Fatalf("草稿创建应201/version1，实际状态%d err=%v", status, err)
	}
	// 只读草稿详情必须返回实际版本和完整定义，不能用旧目录DTO冒充受控状态。
	readRequest, _ := http.NewRequest("GET", fmt.Sprintf("%s%s/%d?view=video_draft", srv.URL, path, created.ModelID), nil)
	readRequest.Header.Set("Authorization", "Bearer "+f.token)
	readResponse, readErr := client.Do(readRequest)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var details struct {
		Data struct {
			ModelID, VersionNo uint64
			Managed            bool `json:"managed"`
		} `json:"data"`
	}
	var detailBody struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	readRaw, readErr := io.ReadAll(readResponse.Body)
	_ = readResponse.Body.Close()
	if readErr != nil || readResponse.StatusCode != 200 || json.Unmarshal(readRaw, &detailBody) != nil {
		t.Fatal("草稿详情读取失败")
	}
	_ = json.Unmarshal(detailBody.Data["model_id"], &details.Data.ModelID)
	_ = json.Unmarshal(detailBody.Data["version_no"], &details.Data.VersionNo)
	_ = json.Unmarshal(detailBody.Data["managed"], &details.Data.Managed)
	if details.Data.ModelID != created.ModelID || details.Data.VersionNo != 1 || !details.Data.Managed {
		t.Fatal("详情没有实际受控草稿版本")
	}
	// 同键100并发只返回原草稿，不产生第二个模型、版本、命令或审计对。
	var wg sync.WaitGroup
	failures := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, result, e := call("POST", path, key, f.token, createBody)
			if e != nil || status != 200 || !result.Idempotent || result.ModelID != created.ModelID || result.VersionNo != 1 {
				failures <- fmt.Errorf("并发重放状态%d err=%v", status, e)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Error(e)
	}
	if t.Failed() {
		return
	}
	var count int64
	if err := f.f.DB.Table("ai_video_model_draft_commands").Where("model_id=?", created.ModelID).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("重复命令未收敛")
	}
	var stored model.TokenModel
	if err := f.f.DB.First(&stored, created.ModelID).Error; err != nil || stored.Status != "inactive" || stored.ReleaseVersionNo != 0 || stored.PublishedAt != nil {
		t.Fatal("草稿写入提前发布模型")
	}
	changed := definition
	changed.DisplayName = "改变后的工作副本"
	status, _, err = call("POST", path, key, f.token, body(0, changed))
	if err != nil || status != 409 {
		t.Fatalf("同键异定义必须409，实际%d", status)
	}
	updatePath := fmt.Sprintf("%s/%d", path, created.ModelID)
	status, updated, err := call("PATCH", updatePath, "video-model-update-0001", f.token, body(1, changed))
	if err != nil || status != 200 || updated.VersionNo != 2 || updated.ModelID != created.ModelID {
		t.Fatalf("模型更新应版本2，实际%d err=%v", status, err)
	}
	status, replay, err := call("PATCH", updatePath, "video-model-update-0001", f.token, body(1, changed))
	if err != nil || status != 200 || !replay.Idempotent || replay.VersionNo != 2 {
		t.Fatal("更新重放未保持原结果")
	}
	status, _, _ = call("PATCH", updatePath, "video-model-update-stale", f.token, body(1, definition))
	if status != 409 {
		t.Fatal("陈旧版本覆盖了草稿")
	}
	for _, bad := range [][]byte{bytes.Replace(createBody, []byte(`"display_name"`), []byte(`"DISPLAY_NAME"`), 1), bytes.Replace(createBody, []byte(`"default_model":false`), []byte(`"default_model":false,"default_model":true`), 1), bytes.Replace(createBody, []byte(`"reason":`), []byte(`"actor_id":1,"reason":`), 1)} {
		status, _, _ = call("POST", path, "video-model-bad-000001", f.token, bad)
		if status != 400 {
			t.Fatalf("严格字段应400，实际%d", status)
		}
	}
	// 完整定义不能靠字段遗漏扩大可见范围；拒绝后模型版本与命令数仍为2。
	for _, missing := range []string{"visible_scope", "group_ids", "group_roles", "role_codes"} {
		var envelope map[string]json.RawMessage
		_ = json.Unmarshal(body(2, changed), &envelope)
		var def map[string]json.RawMessage
		_ = json.Unmarshal(envelope["video_definition"], &def)
		delete(def, missing)
		envelope["video_definition"], _ = json.Marshal(def)
		bad, _ := json.Marshal(envelope)
		status, _, _ = call("PATCH", updatePath, "video-model-missing-"+missing, f.token, bad)
		if status != 400 {
			t.Fatalf("遗漏完整定义字段%s应400，实际%d", missing, status)
		}
	}
	var currentVersion uint64
	if err := f.f.DB.Table("ai_video_model_draft_states").Select("version_no").Where("model_id=?", created.ModelID).Scan(&currentVersion).Error; err != nil || currentVersion != 2 {
		t.Fatal("拒绝的定义修改了草稿版本")
	}
	if err := f.f.DB.Table("ai_video_model_draft_commands").Where("model_id=?", created.ModelID).Count(&count).Error; err != nil || count != 2 {
		t.Fatal("拒绝的定义新增了命令")
	}
	status, _, _ = call("POST", path, "video-model-noauth-001", f.f.Key, createBody)
	if status != 401 {
		t.Fatal("管理草稿接受了Project SK")
	}
	if err := f.f.DB.Exec("UPDATE user_permission_overrides SET effect='deny' WHERE user_id=? AND permission_code='ai_gateway:model_manage'", f.actor).Error; err != nil {
		t.Fatal(err)
	}
	status, _, _ = call("POST", path, key, f.token, createBody)
	if status != 403 {
		t.Fatal("撤销模型权限后仍能重放")
	}
	if !bytes.Equal(finance, f.f.FinancialSnapshot()) {
		t.Fatal("模型管理改变了原请求或财务事实")
	}
	// 原模型仓储的删除不能绕过专用视频管理；Chat保持旧行为。
	repo := repository.NewTokenModelRepository(f.f.DB)
	if err := repo.Delete(context.Background(), created.ModelID); err == nil {
		t.Fatal("旧删除接口删除受控视频模型")
	}
	legacyID := service.NextVideoFixtureUserID()
	legacy := model.TokenModel{ID: legacyID, LogicalModelCode: fmt.Sprintf("molin/video-legacy-draft-%d", legacyID), DisplayName: "无引用历史视频草稿", Modality: "video", Status: "inactive"}
	if err := repo.Create(context.Background(), &legacy); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(context.Background(), legacyID); err == nil {
		t.Fatal("旧删除接口删除历史视频草稿")
	}
	chatID := service.NextVideoFixtureUserID()
	chat := model.TokenModel{ID: chatID, LogicalModelCode: fmt.Sprintf("molin/chat-delete-fixture-%d", chatID), DisplayName: "隔离Chat删除正例", Modality: "chat", Status: "inactive"}
	if err := repo.Create(context.Background(), &chat); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(context.Background(), chatID); err != nil {
		t.Fatal("视频守卫破坏旧Chat删除")
	}
}
