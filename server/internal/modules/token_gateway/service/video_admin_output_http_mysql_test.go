package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"testing"
	"time"

	authmodel "molin/server/internal/modules/auth/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/service"
)

func TestVideoG6AdminOutputListHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	taskID := f.CreateCompletedForKey(f.ProjectID)
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
	path := "/api/admin/token/video-assets"
	base := fmt.Sprintf("%s?user_id=%d&project_id=%d", path, f.ProjectID, f.ProjectID)
	call := func(path, credential string, status, code int) *service.VideoAdminOutputPage {
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
		if json.Unmarshal(raw, &e) != nil || resp.StatusCode != status || e.Code != code {
			t.Fatalf("管理输出应%d/%d，实际%d/%d", status, code, resp.StatusCode, e.Code)
		}
		if status != 200 {
			if string(e.Data) != "null" {
				t.Fatal("失败不能返回部分资产")
			}
			return nil
		}
		var p service.VideoAdminOutputPage
		var keys map[string]json.RawMessage
		var fields struct {
			Items []map[string]json.RawMessage `json:"items"`
		}
		if json.Unmarshal(e.Data, &p) != nil || json.Unmarshal(e.Data, &keys) != nil || len(keys) != 4 || json.Unmarshal(e.Data, &fields) != nil || p.Items == nil {
			t.Fatal("输出列表必须精确D-95")
		}
		for _, row := range fields.Items {
			if len(row) != 28 {
				t.Fatalf("输出列表项必须28字段，实际%d", len(row))
			}
			for _, name := range []string{"asset_id", "video_id", "request_id", "user_id", "project_id", "api_key_id", "model", "operation", "role", "parent_asset_id", "lifecycle_state", "version_no", "mime_type", "size_bytes", "width", "height", "moderation_status", "moderation_policy_version", "explicit_label_status", "explicit_label_version", "implicit_label_status", "implicit_label_version", "legal_hold", "dispute_status", "expires_at", "deleted_at", "media_deleted_at", "created_at"} {
				if _, ok := row[name]; !ok {
					t.Fatalf("输出缺字段%s", name)
				}
			}
		}
		if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("X-Molin-Request-ID") != "" {
			t.Fatal("资产列表不能缓存或伪造单一业务请求")
		}
		return &p
	}
	capture := func() []byte {
		t.Helper()
		var rows []map[string]any
		if err := f.DB.Table("ai_gateway_assets").Where("user_id=?", f.ProjectID).Order("id").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before, finance := capture(), f.FinancialSnapshot()
	heads, submits := f.HeadCalls(), f.SubmitCalls()
	call(base, "", 401, 40001)
	call(base, f.Key, 401, 40001)
	call(base, f.Token, 403, 40003)
	p := call(base, token, 200, 0)
	if p.Total != 6 || len(p.Items) != 6 || p.Page != 1 || p.PageSize != 20 {
		t.Fatal("必须列出原任务六角色，不混入原图片资产")
	}
	roles := map[string]bool{}
	root := ""
	for _, a := range p.Items {
		roles[a.Role] = true
		if a.Role == "content" {
			root = a.AssetID
		}
	}
	for _, role := range []string{"content", "cover", "preview", "thumbnail", "moderation_copy", "derived"} {
		if !roles[role] {
			t.Fatalf("缺角色%s", role)
		}
	}
	for _, a := range p.Items {
		if a.UserID != f.ProjectID || a.ProjectID != f.ProjectID || a.APIKeyID == nil || *a.APIKeyID != f.ProjectID || a.VideoID != taskID || a.RequestID == "" || a.Model != f.Model || a.Operation != "text_to_video" {
			t.Fatal("输出原任务/归属追溯错误")
		}
		if a.Role == "content" {
			if a.ParentAssetID != nil {
				t.Fatal("根父ID必须null")
			}
		} else if a.ParentAssetID == nil || *a.ParentAssetID != root {
			t.Fatal("派生物必须绑定同任务公开根ID")
		}
		if a.DeletedAt != nil || a.MediaDeletedAt != nil {
			t.Fatal("未删除资产时间必须null")
		}
	}
	if !sort.SliceIsSorted(p.Items, func(i, j int) bool {
		if p.Items[i].CreatedAt.Equal(p.Items[j].CreatedAt) {
			return p.Items[i].AssetID > p.Items[j].AssetID
		}
		return p.Items[i].CreatedAt.After(p.Items[j].CreatedAt)
	}) {
		t.Fatal("输出排序不稳定")
	}
	for _, role := range []string{"content", "cover", "preview", "thumbnail", "moderation_copy", "derived"} {
		if p := call(base+"&role="+role, token, 200, 0); p.Total != 1 || len(p.Items) != 1 || p.Items[0].Role != role {
			t.Fatal("角色筛选不准确")
		}
	}
	if p := call(base+"&page_size=2&page=4", token, 200, 0); p.Total != 6 || len(p.Items) != 0 {
		t.Fatal("空页必须保留完整total")
	}
	if p := call(base+"&model="+url.QueryEscape(f.Model)+"&operation=text_to_video&lifecycle_state=available&moderation_status=passed&dispute_status=none", token, 200, 0); p.Total != 6 {
		t.Fatal("AND组合过滤错误")
	}
	if p := call(base+"&operation=image_to_video", token, 200, 0); p.Total != 0 {
		t.Fatal("I2V不能混入T2V")
	}
	if p := call(fmt.Sprintf("%s?user_id=%d&project_id=%d", path, admin.ID, f.ProjectID), token, 200, 0); p.Total != 0 {
		t.Fatal("跨用户Project条件必须AND")
	}
	for _, q := range []string{"&page=0", "&page_size=101", "&page=01", "&page=1&page=2", "&role=primary_output", "&role=", "&status=succeeded", "&moderation_status=failed", "&dispute_status=invalid", "&operation=other", "&model=", "&bucket=forged", "&object_key=forged", "&url=https://invalid.example", "&signature=x"} {
		call(base+q, token, 400, 40000)
	}
	if !bytes.Equal(before, capture()) || !bytes.Equal(finance, f.FinancialSnapshot()) || heads != f.HeadCalls() || submits != f.SubmitCalls() {
		t.Fatal("管理查询不得改变资产/财务或调用Store/Provider")
	}
	// 经原删除协调器真实删掉媒体，再核对管理历史；不直接篡改已完成资产的生命周期。
	caller := service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	if _, err := f.App.DeleteMedia(context.Background(), caller, taskID, "g6-admin-output-delete"); err != nil {
		t.Fatal(err)
	}
	afterDelete := capture()
	heads = f.HeadCalls()
	deleted := call(base+"&lifecycle_state=deleted", token, 200, 0)
	if deleted.Total != 5 || len(deleted.Items) != 5 {
		t.Fatal("媒体删除后保留五个普通资产历史")
	}
	for _, a := range deleted.Items {
		if a.MediaDeletedAt == nil || a.DeletedAt == nil {
			t.Fatal("已确认删除需保留两个原时间事实")
		}
	}
	if p := call(base+"&role=moderation_copy", token, 200, 0); p.Total != 1 {
		t.Fatal("安全保留副本仍可查元数据")
	}
	if err := f.DB.Table("users").Where("id=?", f.ProjectID).Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Table("api_keys").Where("id=?", f.ProjectID).Update("status", "revoked").Error; err != nil {
		t.Fatal(err)
	}
	if p := call(base, token, 200, 0); p.Total != 6 {
		t.Fatal("主体停用不能隐藏历史")
	}
	if err := f.DB.Table("users").Where("id=?", admin.ID).Update("admin_phone_verified_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	call(base, token, 403, 40031)
	if !bytes.Equal(afterDelete, capture()) || !bytes.Equal(finance, f.FinancialSnapshot()) || heads != f.HeadCalls() || submits != f.SubmitCalls() {
		t.Fatal("历史管理查询不得再次删除、改财务或访问外部系统")
	}
}
