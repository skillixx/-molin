package service_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"molin/server/internal/middleware"
	iamrepo "molin/server/internal/modules/iam/repository"
	iamservice "molin/server/internal/modules/iam/service"
	"molin/server/internal/modules/token_gateway/handler"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	pkgjwt "molin/server/pkg/jwt"
)

func TestVideoG6ModelDraftAdoptionHTTPMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	if err := f.f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:model_manage'", f.actor).Error; err != nil {
		t.Fatal(err)
	}
	p, err := service.NewVideoAdminReasonProtector("model-adoption-test", f.secret)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: p, ModelDrafts: &service.VideoModelDraftOptions{}})
	if err != nil {
		t.Fatal(err)
	}
	h := handler.NewModelHandler(service.NewCatalogService(repository.NewTokenModelRepository(f.f.DB))).WithVideoDrafts(handler.NewVideoAdminHandler(admin, f.f.JWT, true), nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/admin/token/models/{id}", h.GetModel)
	mux.HandleFunc("PATCH /api/admin/token/models/{id}", h.UpdateModel)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	path := fmt.Sprintf("/api/admin/token/models/%d", f.f.ProjectID)
	call := func(method, path, key, token string, body []byte) (int, []byte) {
		t.Helper()
		r, e := http.NewRequest(method, srv.URL+path, bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", key)
		}
		res, e := srv.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(raw, []byte("DO_NOT_EXPOSE_CATALOG_SIGNATURE")) || bytes.Contains(raw, []byte("接管的私有原因")) {
			t.Fatal("草稿响应泄露受保护内容")
		}
		return res.StatusCode, raw
	}
	read := func() service.VideoModelDraftDetails {
		t.Helper()
		status, raw := call("GET", path+"?view=video_draft", "", f.token, nil)
		if status != 200 {
			t.Fatalf("草稿读取状态%d", status)
		}
		var envelope struct {
			Data service.VideoModelDraftDetails `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	finance := f.f.FinancialSnapshot()
	// 构造遗留不安全文档URL，验证读取仅返回红线字段名和摘要，不返回签名值。
	if err := f.f.DB.Exec("UPDATE token_models SET docs_url=?,docs_url_health_status='healthy' WHERE id=?", "https://docs.example.invalid/guide?signature=DO_NOT_EXPOSE_CATALOG_SIGNATURE", f.f.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	original := read()
	if original.Managed || original.VersionNo != 0 || original.SourceSHA256 == nil || len(*original.SourceSHA256) != 64 || original.ModelID != f.f.ProjectID || original.Definition.DocsURL != nil || len(original.RedactedFields) != 1 || original.RedactedFields[0] != "docs_url" {
		t.Fatal("历史草稿读取没有安全接管摘要或未脱敏")
	}
	definition := original.Definition
	definition.VideoContract = json.RawMessage(`{"schema_version":1,"purpose":"non_commercial_test_fixture","supported_operations":["text_to_video","image_to_video"],"default_model":false,"asset_required":false,"required_entitlement_type":null,"required_membership_levels":[]}`)
	definition.DisplayName = "显式接管后的草稿"
	body := func(source *string) []byte {
		t.Helper()
		fields := map[string]any{"version_no": 0, "reason": "接管的私有原因", "video_definition": definition}
		if source != nil {
			fields["source_sha256"] = *source
		}
		raw, e := json.Marshal(fields)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	status, _ := call("PATCH", path, "video-model-adopt-missing", f.token, body(nil))
	if status != 400 {
		t.Fatalf("历史接管缺摘要应400，实际%d", status)
	}
	if err := f.f.DB.Exec("UPDATE token_models SET display_name='读取后发生变化' WHERE id=?", f.f.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	status, _ = call("PATCH", path, "video-model-adopt-stale", f.token, body(original.SourceSHA256))
	if status != 409 {
		t.Fatalf("旧摘要应409，实际%d", status)
	}
	var count int64
	if err := f.f.DB.Table("ai_video_model_draft_commands").Where("model_id=?", f.f.ProjectID).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("被拒绝的接管产生了命令")
	}
	fresh := read()
	if fresh.SourceSHA256 == nil || *fresh.SourceSHA256 == *original.SourceSHA256 {
		t.Fatal("模型变化未改变接管摘要")
	}
	acceptedBody := body(fresh.SourceSHA256)
	status, raw := call("PATCH", path, "video-model-adopt-once", f.token, acceptedBody)
	var accepted struct {
		Data service.VideoModelDraftReply `json:"data"`
	}
	if status != 200 || json.Unmarshal(raw, &accepted) != nil || accepted.Data.ModelID != f.f.ProjectID || accepted.Data.VersionNo != 1 || accepted.Data.ReleaseVersionNo != fresh.ReleaseVersionNo || accepted.Data.Idempotent {
		t.Fatalf("接管应保留模型/发布指针并建立版本1，实际%d", status)
	}
	managed := read()
	if !managed.Managed || managed.VersionNo != 1 || managed.SourceSHA256 != nil || managed.Definition.DisplayName != definition.DisplayName {
		t.Fatal("接管后读取状态不一致")
	}
	status, raw = call("PATCH", path, "video-model-adopt-once", f.token, acceptedBody)
	if status != 200 || json.Unmarshal(raw, &accepted) != nil || !accepted.Data.Idempotent || accepted.Data.VersionNo != 1 {
		t.Fatal("接管重放没有返回原命令")
	}
	status, _ = call("PATCH", path, "video-model-adopt-again", f.token, acceptedBody)
	if status != 409 {
		t.Fatal("已受控草稿被再次接管")
	}
	var recorded struct {
		SourceSHA256 string `gorm:"column:source_sha256"`
	}
	if err := f.f.DB.Table("ai_video_model_draft_commands").Where("model_id=?", f.f.ProjectID).Take(&recorded).Error; err != nil || recorded.SourceSHA256 != *fresh.SourceSHA256 {
		t.Fatal("接管命令未冻结源摘要")
	}
	if !bytes.Equal(finance, f.f.FinancialSnapshot()) {
		t.Fatal("草稿接管改变了原任务或财务事实")
	}
	status, _ = call("GET", path+"?view=video_draft", "", f.f.Key, nil)
	if status != 401 {
		t.Fatal("草稿编辑视图接受了Project SK")
	}
	status, _ = call("GET", path+"?view=video_draft&extra=1", "", f.token, nil)
	if status != 400 {
		t.Fatal("草稿视图忽略未知查询")
	}
	if err := f.f.DB.Exec("UPDATE user_permission_overrides SET effect='deny' WHERE user_id=? AND permission_code='ai_gateway:model_manage'", f.actor).Error; err != nil {
		t.Fatal(err)
	}
	status, _ = call("GET", path+"?view=video_draft", "", f.token, nil)
	if status != 403 {
		t.Fatal("撤权后仍可读取草稿")
	}
	status, _ = call("PATCH", path, "video-model-adopt-once", f.token, acceptedBody)
	if status != 403 {
		t.Fatal("撤权后仍可重放接管")
	}
}

// 使用真实JWT和IAM验证旧Chat详情路径；视频可选装配不能截获无关查询参数。
func TestVideoG6ModelDraftLegacyQueryHTTPMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	if err := f.f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='token:manage'", f.actor).Error; err != nil {
		t.Fatal(err)
	}
	iam := iamservice.NewIAMService(iamrepo.NewRoleRepository(f.f.DB), iamrepo.NewPermissionRepository(f.f.DB), iamrepo.NewUserRoleRepository(f.f.DB), iamrepo.NewOverrideRepository(f.f.DB), iamrepo.NewGroupRepository(f.f.DB), nil, nil)
	var secretBytes [32]byte
	if _, err := rand.Read(secretBytes[:]); err != nil {
		t.Fatal(err)
	}
	secret := hex.EncodeToString(secretBytes[:])
	token, err := pkgjwt.Generate(f.actor, "", secret, 3600)
	if err != nil {
		t.Fatal(err)
	}
	id := service.NextVideoFixtureUserID()
	chat := model.TokenModel{ID: id, LogicalModelCode: fmt.Sprintf("molin/chat-query-%d", id), DisplayName: "旧Chat详情", Modality: "chat", Status: "inactive"}
	if err := f.f.DB.Create(&chat).Error; err != nil {
		t.Fatal(err)
	}
	for _, configured := range []bool{false, true} {
		h := handler.NewModelHandler(service.NewCatalogService(repository.NewTokenModelRepository(f.f.DB)))
		if configured {
			h.WithVideoDrafts(handler.NewVideoAdminHandler(nil, f.f.JWT, false), iam)
		}
		mux := http.NewServeMux()
		mux.Handle("GET /api/admin/token/models/{id}", middleware.RequireAuth(secret, nil, http.HandlerFunc(h.GetModel)))
		srv := httptest.NewServer(mux)
		for _, query := range []string{"", "?_=123"} {
			r, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/admin/token/models/%d%s", srv.URL, id, query), nil)
			r.Header.Set("Authorization", "Bearer "+token)
			res, err := srv.Client().Do(r)
			if err != nil {
				srv.Close()
				t.Fatal(err)
			}
			var envelope struct {
				Data struct {
					ID       uint64 `json:"id"`
					Modality string `json:"modality"`
				} `json:"data"`
			}
			decodeErr := json.NewDecoder(res.Body).Decode(&envelope)
			_ = res.Body.Close()
			if decodeErr != nil || res.StatusCode != 200 || envelope.Data.ID != id || envelope.Data.Modality != "chat" {
				srv.Close()
				t.Fatalf("旧查询被视频视图截获 configured=%v query=%s status=%d", configured, query, res.StatusCode)
			}
		}
		srv.Close()
	}
}
