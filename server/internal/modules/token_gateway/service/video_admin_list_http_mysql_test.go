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
	"sync"
	"testing"
	"time"

	authmodel "molin/server/internal/modules/auth/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

func TestVideoG6AdminListHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	caller := service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	reserved, err := f.App.Create(context.Background(), service.VideoCommand{Caller: caller, IdempotencyKey: "g6-admin-list-reserved", Model: f.Model, Prompt: "仅用于管理员列表隔离测试", Operation: model.AIVideoOperationTextToVideo})
	if err != nil {
		t.Fatal(err)
	}
	doneID := f.CreateCompletedForKey(f.ProjectID)
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
	gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	token := f.TokenForUser(admin.ID)
	base := "/api/admin/token/video-tasks"
	query := fmt.Sprintf("?user_id=%d&project_id=%d", f.ProjectID, f.ProjectID)
	request := func(path, credential string) (int, json.RawMessage, error) {
		r, err := http.NewRequest("GET", srv.URL+path, nil)
		if err != nil {
			return 0, nil, err
		}
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		resp, err := client.Do(r)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return resp.StatusCode, nil, err
		}
		var e struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &e) != nil {
			return resp.StatusCode, nil, fmt.Errorf("响应非JSON")
		}
		if resp.StatusCode == 200 && (e.Code != 0 || resp.Header.Get("X-Molin-Request-ID") != "") {
			return 200, nil, fmt.Errorf("列表不能伪造单一业务request_id")
		}
		return resp.StatusCode, e.Data, nil
	}
	call := func(path, credential string, want int) *service.VideoAdminTaskPage {
		t.Helper()
		status, data, err := request(path, credential)
		if err != nil || status != want {
			t.Fatalf("列表应%d实际%d err=%v", want, status, err)
		}
		if want != 200 {
			return nil
		}
		var page service.VideoAdminTaskPage
		var keys map[string]json.RawMessage
		if json.Unmarshal(data, &page) != nil || json.Unmarshal(data, &keys) != nil || len(keys) != 4 {
			t.Fatal("管理列表必须精确D-95四字段")
		}
		for _, key := range []string{"items", "page", "page_size", "total"} {
			if _, ok := keys[key]; !ok {
				t.Fatal("D-95缺字段")
			}
		}
		if page.Items == nil {
			t.Fatal("空列表必须是数组")
		}
		return &page
	}
	before := f.FinancialSnapshot()
	heads, submits := f.HeadCalls(), f.SubmitCalls()
	call(base+query, "", 401)
	call(base+query, f.Key, 401)
	call(base+query, f.Token, 403)
	all := call(base+query, token, 200)
	if all.Total != 2 || len(all.Items) != 2 || all.Page != 1 || all.PageSize != 20 {
		t.Fatal("默认分页和目标过滤不符")
	}
	for _, item := range all.Items {
		if item.UserID != f.ProjectID || item.ProjectID != f.ProjectID || item.APIKeyID == nil || *item.APIKeyID != f.ProjectID {
			t.Fatal("列表目标归属不能变成管理员")
		}
	}
	if !sort.SliceIsSorted(all.Items, func(i, j int) bool {
		if all.Items[i].CreatedAt.Equal(all.Items[j].CreatedAt) {
			return all.Items[i].TaskID > all.Items[j].TaskID
		}
		return all.Items[i].CreatedAt.After(all.Items[j].CreatedAt)
	}) {
		t.Fatal("展示排序必须稳定倒序")
	}
	first := call(base+query+"&page_size=1", token, 200)
	second := call(base+query+"&page_size=1&page=2", token, 200)
	empty := call(base+query+"&page_size=1&page=3", token, 200)
	if first.Total != 2 || second.Total != 2 || empty.Total != 2 || len(first.Items) != 1 || len(second.Items) != 1 || len(empty.Items) != 0 || first.Items[0].TaskID == second.Items[0].TaskID {
		t.Fatal("分页不能重复或丢失total")
	}
	filtered := call(base+query+"&status=succeeded&operation=text_to_video&model="+url.QueryEscape(f.Model), token, 200)
	if filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].TaskID != doneID || !filtered.Items[0].CanDeliver {
		t.Fatal("组合过滤必须按原状态且保留真实完成事实")
	}
	filtered = call(base+query+"&status=reserved", token, 200)
	if filtered.Total != 1 || filtered.Items[0].TaskID != reserved.Job.ID {
		t.Fatal("状态过滤不能使用v1映射")
	}
	if p := call(base+query+"&operation=image_to_video", token, 200); p.Total != 0 || len(p.Items) != 0 {
		t.Fatal("I2V过滤不能混入T2V")
	}
	if p := call(base+fmt.Sprintf("?user_id=%d&project_id=%d", admin.ID, f.ProjectID), token, 200); p.Total != 0 {
		t.Fatal("归属组合必须AND")
	}
	for _, q := range []string{"?page=0", "?page=01", "?page=10001", "?page_size=101", "?page=1&page=2", "?user_id=0", "?project_id=abc", "?status=completed", "?status=in_progress", "?operation=other", "?model=", "?unexpected=x", "?page=1;page_size=2", "?status=reserved&status=reserved"} {
		call(base+q, token, 400)
	}
	// 与用户列表并发访问同钱包两任务，验证公开ID统一锁序，不依赖单一展示顺序。
	var wg sync.WaitGroup
	failures := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path, credential := base+query, token
			if i%2 != 0 {
				path = fmt.Sprintf("/api/token/video-tasks?project_id=%d", f.ProjectID)
				credential = f.Key
			}
			status, data, err := request(path, credential)
			if err != nil || status != 200 {
				failures <- fmt.Errorf("并发列表失败：%d %v", status, err)
				return
			}
			var page service.VideoAdminTaskPage
			if json.Unmarshal(data, &page) != nil || page.Total != 2 || len(page.Items) != 2 {
				failures <- fmt.Errorf("并发列表不能少项")
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if !bytes.Equal(before, f.FinancialSnapshot()) || heads != f.HeadCalls() || submits != f.SubmitCalls() {
		t.Fatal("列表不能写财务或访问Store/Provider")
	}
}
