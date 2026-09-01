package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	drivermysql "github.com/go-sql-driver/mysql"

	"molin/server/internal/middleware"
	authservice "molin/server/internal/modules/auth/service"
	"molin/server/internal/modules/token_gateway/handler"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
)

// 与应用装配相同的签名适配，不替换真实Key哈希校验、用户状态或数据库读取。
type videoCatalogKeyResolver struct{ keys *authservice.APIKeyService }

func (a videoCatalogKeyResolver) ResolveKey(ctx context.Context, raw string) (uint64, uint64, bool) {
	return a.keys.ResolveKeyForAuth(ctx, raw)
}

// 通过真实Project SK和回环HTTP读取目录；只修改本轮隔离夹具的发布事实，不调用上游。
func TestVideoG6CatalogPublishedHTTPMySQL(t *testing.T) {
	f := service.NewVideoImportHTTPFixture(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if err := f.DB.Exec(query, args...).Error; err != nil {
			var mysqlErr *drivermysql.MySQLError
			if errors.As(err, &mysqlErr) {
				t.Fatalf("目录夹具写入失败，MySQL错误号%d", mysqlErr.Number)
			}
			t.Fatal("目录夹具写入失败")
		}
	}
	exec(`UPDATE ai_model_release_versions SET snapshot_json=JSON_SET(snapshot_json,'$.display_name','已发布的视频','$.provider_name','合成厂商','$.description','已发布说明','$.context_window',0,'$.docs_url','https://docs.example.invalid/video') WHERE model_id=? AND version_no=1`, f.ProjectID)
	exec(`UPDATE token_models SET display_name='不能泄露的草稿',provider_name='草稿厂商',description='草稿说明',capabilities_json=JSON_ARRAY('chat.completions') WHERE id=?`, f.ProjectID)
	// scope外键仍需指向真实模型；只创建本轮不可见的合成模型，不关闭数据库约束。
	exec("INSERT INTO token_models(id,logical_model_code,display_name,modality,status) VALUES(?,CONCAT('molin/catalog-denied-',?),'不可用合成模型','chat','inactive')", service.NextVideoFixtureUserID(), f.ProjectID)
	// 来源图片夹具的模型刻意处于inactive，不能冒充公开目录的正例；另建本轮active图片条目。
	exec("INSERT INTO token_models(id,logical_model_code,display_name,modality,status) VALUES(?,CONCAT('molin/catalog-image-',?),'公开合成图片模型','image','active')", service.NextVideoFixtureUserID(), f.ProjectID)
	catalog := service.NewCatalogService(repository.NewTokenModelRepository(f.DB)).WithVideoAccess(service.NewVideoAccessService(f.DB))
	h := handler.NewModelHandler(catalog)
	mux := http.NewServeMux()
	mux.Handle("GET /api/token/models", middleware.RequireUserAuth("unused-sk-only", nil, videoCatalogKeyResolver{f.Keys}, http.HandlerFunc(h.ListPublic)))
	closed := handler.NewModelHandler(service.NewCatalogService(repository.NewTokenModelRepository(f.DB)))
	mux.Handle("GET /api/token/models/closed", middleware.RequireUserAuth("unused-sk-only", nil, videoCatalogKeyResolver{f.Keys}, http.HandlerFunc(closed.ListPublic)))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	list := func(token, query string, status int) []map[string]any {
		t.Helper()
		req, err := http.NewRequest("GET", srv.URL+"/api/token/models"+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("目录状态=%d，期望%d", res.StatusCode, status)
		}
		if status != 200 {
			_, _ = io.Copy(io.Discard, res.Body)
			return nil
		}
		var body struct {
			Data struct {
				Items    []map[string]any `json:"items"`
				Total    int              `json:"total"`
				Page     int              `json:"page"`
				PageSize int              `json:"page_size"`
			} `json:"data"`
		}
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Data.Items == nil || body.Data.Page != 1 || body.Data.PageSize != 100 {
			t.Fatal("目录必须保留D-95分页和空数组")
		}
		if body.Data.Total != len(body.Data.Items) {
			t.Fatal("本夹具目录总数应与过滤后的当前页一致")
		}
		return body.Data.Items
	}
	find := func(items []map[string]any) map[string]any {
		t.Helper()
		for _, item := range items {
			if item["logical_model_code"] == f.Model {
				return item
			}
		}
		return nil
	}
	query := "?modality=video&page_size=100"
	item := find(list(f.Key, query, 200))
	if item == nil || item["capability"] != "video.generate" || !reflect.DeepEqual(item["supported_operations"], []any{"text_to_video", "image_to_video"}) {
		t.Fatal("视频目录必须明确返回已发布能力和两种操作")
	}
	if item["display_name"] != "已发布的视频" || item["provider_name"] != "合成厂商" || item["description"] != "已发布说明" {
		t.Fatal("目录泄露未发布工作副本")
	}
	for _, key := range []string{"channel_id", "upstream_model", "product_id", "video_contract", "snapshot_json", "target_audience_json"} {
		if _, exists := item[key]; exists {
			t.Fatalf("公开目录泄露内部字段%s", key)
		}
	}
	// 快照缺项、未来发布和退役均不可展示，不能从modality猜测能力。
	for _, change := range []string{
		`UPDATE ai_model_release_versions SET snapshot_json=JSON_REMOVE(snapshot_json,'$.video_contract.supported_operations') WHERE model_id=?`,
		`UPDATE ai_model_release_versions SET published_at=DATE_ADD(UTC_TIMESTAMP(),INTERVAL 1 HOUR) WHERE model_id=?`,
		`UPDATE ai_model_release_versions SET status='retired',retired_at=UTC_TIMESTAMP() WHERE model_id=?`,
	} {
		exec(change, f.ProjectID)
		if find(list(f.Key, query, 200)) != nil {
			t.Fatal("不可用视频快照仍在目录中")
		}
		exec(`UPDATE ai_model_release_versions SET snapshot_json=JSON_SET(snapshot_json,'$.video_contract.supported_operations',JSON_ARRAY('text_to_video','image_to_video')),published_at=DATE_SUB(UTC_TIMESTAMP(),INTERVAL 1 SECOND),status='active',retired_at=NULL WHERE model_id=?`, f.ProjectID)
	}
	// 当前视频位、Project授权、模型scope和实名任一撤销，不能靠旧的目录结果继续展示。
	for _, mutation := range []struct{ revoke, restore string }{
		{`UPDATE api_keys SET video_generate_allowed=0 WHERE id=?`, `UPDATE api_keys SET video_generate_allowed=1 WHERE id=?`},
		{`UPDATE ai_project_model_capability_grants SET status='revoked',version_no=version_no+1 WHERE project_id=?`, `UPDATE ai_project_model_capability_grants SET status='active',version_no=version_no+1 WHERE project_id=?`},
		{`UPDATE api_key_model_scopes SET logical_model_code=CONCAT('molin/catalog-denied-',api_key_id) WHERE api_key_id=? AND logical_model_code LIKE 'molin/video-%'`, `UPDATE api_key_model_scopes SET logical_model_code=(SELECT logical_model_code FROM token_models WHERE id=api_key_model_scopes.api_key_id) WHERE api_key_id=? AND logical_model_code LIKE 'molin/catalog-denied-%'`},
		{`UPDATE users SET real_name_status='unverified' WHERE id=?`, `UPDATE users SET real_name_status='verified' WHERE id=?`},
	} {
		exec(mutation.revoke, f.ProjectID)
		if find(list(f.Key, query, 200)) != nil {
			t.Fatal("撤销资格后仍然展示视频模型")
		}
		exec(mutation.restore, f.ProjectID)
		if find(list(f.Key, query, 200)) == nil {
			t.Fatal("恢复合成资格后未展示已发布模型")
		}
	}
	// 缺少专用准入依赖时必须关闭SK的视频目录，不静默套用旧Chat检查。
	if find(list(f.Key, "/closed"+query, 200)) != nil {
		t.Fatal("缺少视频准入依赖仍展示视频")
	}
	// 非视频响应没有新增能力猜测；此处验证无旧Chat过滤器时的图片DTO，不冒称实际应用路由验收。
	images := list(f.Key, "?modality=image&page_size=100", 200)
	if len(images) == 0 {
		t.Fatal("既有图片目录被视频改动清空")
	}
	for _, image := range images {
		if _, ok := image["capability"]; ok {
			t.Fatal("图片目录被附加视频能力")
		}
		if _, ok := image["supported_operations"]; ok {
			t.Fatal("图片目录字段发生不兼容变更")
		}
	}
	list("invalid", query, 401)
	// 已发布视频改成其他草稿模态，查询对应模态或全部模型均不能绕过发布隔离。
	for _, modality := range []string{"chat", "image"} {
		exec("UPDATE token_models SET modality=? WHERE id=?", modality, f.ProjectID)
		if find(list(f.Key, "?modality="+modality+"&page_size=100", 200)) != nil || find(list(f.Key, "?page_size=100", 200)) != nil {
			t.Fatal("草稿模态漂移绕过视频发布快照")
		}
		exec("UPDATE token_models SET modality='video' WHERE id=?", f.ProjectID)
	}
}
