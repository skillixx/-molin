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
	"time"

	authmodel "molin/server/internal/modules/auth/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

func TestVideoG6AdminInputListHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	imported, err := f.App.ImportImageInput(context.Background(), service.VideoInputImportCommand{Caller: service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}, IdempotencyKey: "g6-admin-input-import", SourceAssetID: f.SourceID})
	if err != nil || imported.InputAssetID == nil {
		t.Fatalf("真实图片导入失败：%v", err)
	}
	verified := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	admin := authmodel.User{ID: service.NextVideoFixtureUserID(), PasswordHash: "synthetic-only", Status: "active", AdminPhoneVerifiedAt: &verified, AdminEmailVerifiedAt: &verified}
	if err := f.DB.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:view'", admin.ID).Error; err != nil {
		t.Fatal(err)
	}
	app, err := service.NewVideoAdminService(f.App, 24)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gateway.RegisterVideoAdminRoutes(mux, app, f.JWT, true)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 30 * time.Second
	token := f.TokenForUser(admin.ID)
	base := fmt.Sprintf("/api/admin/token/video-input-assets?user_id=%d&project_id=%d", f.ProjectID, f.ProjectID)
	call := func(path, credential string, want, code int) *service.VideoAdminInputPage {
		t.Helper()
		r, _ := http.NewRequest("GET", srv.URL+path, nil)
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
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
		if json.Unmarshal(raw, &e) != nil || resp.StatusCode != want || e.Code != code {
			t.Fatalf("管理输入列表应%d/%d，实际%d/%d", want, code, resp.StatusCode, e.Code)
		}
		if want != 200 {
			return nil
		}
		var page service.VideoAdminInputPage
		var keys map[string]json.RawMessage
		if json.Unmarshal(e.Data, &page) != nil || json.Unmarshal(e.Data, &keys) != nil || len(keys) != 4 || page.Items == nil {
			t.Fatal("输入管理分页必须精确D-95")
		}
		var items struct {
			Items []map[string]json.RawMessage `json:"items"`
		}
		if json.Unmarshal(e.Data, &items) != nil {
			t.Fatal("列表字段无法解析")
		}
		for _, item := range items.Items {
			if len(item) != 21 {
				t.Fatalf("输入管理必须21字段，实际%d", len(item))
			}
			for _, name := range []string{"input_asset_id", "user_id", "project_id", "api_key_id", "source_type", "upload_session_id", "source_asset_id", "lifecycle_state", "version_no", "mime_type", "size_bytes", "width", "height", "moderation_status", "moderation_policy_version", "expires_at", "legal_hold", "delete_requested_at", "pending_delete_at", "deleted_at", "created_at"} {
				if _, ok := item[name]; !ok {
					t.Fatalf("缺字段%s", name)
				}
			}
		}
		if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("X-Molin-Request-ID") != "" {
			t.Fatal("管理列表不能缓存或冒称单一业务请求")
		}
		return &page
	}
	before, heads, provider := f.FinancialSnapshot(), f.HeadCalls(), f.ProviderCalls()
	call(base, "", 401, 40001)
	call(base, f.Key, 401, 40001)
	call(base, f.Token, 403, 40003)
	p := call(base, token, 200, 0)
	if p.Total != 2 || len(p.Items) != 2 || p.Page != 1 || p.PageSize != 20 {
		t.Fatal("必须包含上传及真实导入两个输入")
	}
	for _, a := range p.Items {
		if a.UserID != f.ProjectID || a.ProjectID != f.ProjectID || a.APIKeyID == nil || *a.APIKeyID != f.ProjectID || a.DeletedAt != nil {
			t.Fatal("输入源归属或null语义错误")
		}
		if a.InputAssetID == *imported.InputAssetID {
			if a.SourceAssetID == nil || *a.SourceAssetID != f.SourceID || a.UploadSessionID != nil {
				t.Fatal("导入输入必须回溯原图片公开ID")
			}
		} else {
			if a.UploadSessionID == nil || a.SourceAssetID != nil {
				t.Fatal("上传输入来源必须二选一")
			}
		}
	}
	if p := call(base+"&page=3&page_size=1", token, 200, 0); p.Total != 2 || len(p.Items) != 0 {
		t.Fatal("空页必须保留total")
	}
	if p := call(base+"&source_type=gateway_asset_snapshot&lifecycle_state=ready&moderation_status=passed", token, 200, 0); p.Total != 1 || p.Items[0].InputAssetID != *imported.InputAssetID {
		t.Fatal("组合过滤不能混入上传来源")
	}
	for _, q := range []string{"&page=0", "&page=01", "&page_size=101", "&page=1&page=2", "&user_id=1", "&source_type=unknown", "&moderation_status=failed", "&lifecycle_state=available", "&source_type=", "&bucket=forged", "&api_key_id=1"} {
		call(base+q, token, 400, 40000)
	}
	if p := call(base+"&moderation_status=error", token, 200, 0); p.Total != 0 {
		t.Fatal("合法空过滤必须200")
	}
	// 只改变本测试合成主体状态，管理员仍应可查历史，不能冒用目标用户实时生成资格。
	if err := f.DB.Model(&authmodel.User{}).Where("id=?", f.ProjectID).Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Table("api_keys").Where("id=?", f.ProjectID).Update("status", "revoked").Error; err != nil {
		t.Fatal(err)
	}
	if p := call(base, token, 200, 0); p.Total != 2 {
		t.Fatal("目标停用不能隐藏历史")
	}
	if err := f.DB.Model(&model.AIGatewayInputAsset{}).Where("public_id=?", *imported.InputAssetID).Updates(map[string]any{"lifecycle_state": "quarantined", "moderation_status": "rejected", "version_no": 2}).Error; err != nil {
		t.Fatal(err)
	}
	if p := call(base+"&lifecycle_state=quarantined", token, 200, 0); p.Total != 1 || p.Items[0].VersionNo != 2 {
		t.Fatal("隔离历史必须可读")
	}
	if err := f.DB.Model(&authmodel.User{}).Where("id=?", admin.ID).Update("admin_email_verified_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	call(base, token, 403, 40031)
	if !bytes.Equal(before, f.FinancialSnapshot()) || heads != f.HeadCalls() || provider != f.ProviderCalls() {
		t.Fatal("管理输入列表不能改财务或请求Provider/对象存储")
	}
}
