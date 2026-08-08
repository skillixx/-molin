package token_gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"molin/server/internal/modules/token_gateway/service"
	pkgjwt "molin/server/pkg/jwt"
)

type recordingIAMChecker struct{ permission string }

func (c *recordingIAMChecker) CheckPermission(_ context.Context, _ uint64, permission string) bool {
	c.permission = permission
	return true
}

type rejectingAdminVerifiedChecker struct{}

func (rejectingAdminVerifiedChecker) IsAdminVerified(context.Context, uint64) bool { return false }

// TestRegisterUserRoutes_OpenAIAlias 验证 OpenAI 兼容别名路由已注册，
// 且 /v1/chat/completions 与现有 /api/token/chat/completions 命中同一 handler（纯别名）。
// 用零值服务即可——此处只断言路由注册与匹配，不触发 handler 内部逻辑。
func TestRegisterUserRoutes_OpenAIAlias(t *testing.T) {
	mux := http.NewServeMux()
	// apiKeyResolver 传 nil（退化为纯 JWT），banChecker 传 nil；本测试只校验路由表，不发起鉴权。
	RegisterUserRoutes(mux,
		&service.ForwardService{},
		nil,
		nil,
		&service.CatalogService{},
		&service.UsageService{},
		nil,
		&service.G6UserService{},
		nil,
		"test-secret",
		nil,
		nil,
	)

	cases := []struct {
		name        string
		method      string
		path        string
		wantPattern string
	}{
		{"v1 chat 别名", http.MethodPost, "/v1/chat/completions", "POST /v1/chat/completions"},
		{"v1 请求状态", http.MethodGet, "/v1/requests/req-test", "GET /v1/requests/{request_id}"},
		{"v1 models 别名", http.MethodGet, "/v1/models", "GET /v1/models"},
		{"原 chat 路由保留", http.MethodPost, "/api/token/chat/completions", "POST /api/token/chat/completions"},
		{"原 models 路由保留", http.MethodGet, "/api/token/models", "GET /api/token/models"},
		{"本人安全事件", http.MethodGet, "/api/token/safety/events", "GET /api/token/safety/events"},
		{"提交安全申诉", http.MethodPost, "/api/token/safety/appeals", "POST /api/token/safety/appeals"},
		{"G6 模型市场", http.MethodGet, "/api/token/catalog/models", "GET /api/token/catalog/models"},
		{"G6 模型详情", http.MethodGet, "/api/token/catalog/models/molin%2Fqwen", "GET /api/token/catalog/models/{model_code}"},
		{"G6 用量总览", http.MethodGet, "/api/token/customer/usage/overview", "GET /api/token/customer/usage/overview"},
		{"G6 有效限制", http.MethodGet, "/api/token/customer/limits", "GET /api/token/customer/limits"},
		{"G6 请求账本", http.MethodGet, "/api/token/customer/requests", "GET /api/token/customer/requests"},
		{"G6 账本导出", http.MethodGet, "/api/token/customer/requests/export", "GET /api/token/customer/requests/export"},
		{"G6 请求详情", http.MethodGet, "/api/token/customer/requests/req-test", "GET /api/token/customer/requests/{request_id}"},
		{"G6 账单申诉", http.MethodPost, "/api/token/customer/requests/req-test/disputes", "POST /api/token/customer/requests/{request_id}/disputes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			h, pattern := mux.Handler(req)
			if h == nil {
				t.Fatalf("%s %s 未匹配到 handler", c.method, c.path)
			}
			if pattern != c.wantPattern {
				t.Fatalf("%s %s 命中模式期望 %q，实际 %q", c.method, c.path, c.wantPattern, pattern)
			}
		})
	}
}

func TestRegisterRoutes_G4PermissionMatrix(t *testing.T) {
	const secret = "route-permission-test-secret"
	token, err := pkgjwt.Generate(1, "admin@example.test", secret, 300)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		method     string
		path       string
		permission string
	}{
		{http.MethodGet, "/api/admin/token/safety/policies", "ai_gateway:view"},
		{http.MethodGet, "/api/admin/token/safety/events", "ai_gateway:view"},
		{http.MethodGet, "/api/admin/token/safety/appeals", "ai_gateway:view"},
		{http.MethodGet, "/api/admin/token/resource-policies", "ai_gateway:view"},
		{http.MethodGet, "/api/admin/token/budget-policies", "ai_gateway:view"},
		{http.MethodGet, "/api/admin/token/compensation-tasks", "ai_gateway:view"},
		{http.MethodPost, "/api/admin/token/safety/policies", "ai_gateway:safety_manage"},
		{http.MethodPut, "/api/admin/token/resource-policies", "ai_gateway:resource_manage"},
		{http.MethodPut, "/api/admin/token/budget-policies", "ai_gateway:budget_manage"},
		{http.MethodPost, "/api/admin/token/compensation-tasks/1/resolve", "ai_gateway:reconcile_manage"},
		{http.MethodPost, "/api/admin/token/outbox-events/req-1%3Abilling/requeue", "ai_gateway:reconcile_manage"},
		{http.MethodPost, "/api/admin/token/billing/content-policy/req-1/resolve", "ai_gateway:reconcile_manage"},
		{http.MethodGet, "/api/admin/token/overview", "ai_gateway:view"},
		{http.MethodGet, "/api/admin/token/models/1/versions", "ai_gateway:view"},
		{http.MethodPost, "/api/admin/token/models/1/rollback", "ai_gateway:model_manage"},
		{http.MethodPost, "/api/admin/token/models/1/publish", "ai_gateway:model_manage"},
		{http.MethodPost, "/api/admin/token/models/1/unpublish", "ai_gateway:model_manage"},
		{http.MethodGet, "/api/admin/token/routes", "ai_gateway:view"},
		{http.MethodPost, "/api/admin/token/routes", "ai_gateway:route_manage"},
		{http.MethodPost, "/api/admin/token/channels/1/health-check", "ai_gateway:route_manage"},
		{http.MethodPut, "/api/admin/token/routes/1", "ai_gateway:route_manage"},
		{http.MethodGet, "/api/admin/token/prices", "ai_gateway:view"},
		{http.MethodPost, "/api/admin/token/prices", "ai_gateway:price_manage"},
		{http.MethodPost, "/api/admin/token/prices/1/publish", "ai_gateway:price_manage"},
		{http.MethodPost, "/api/admin/token/prices/1/rollback", "ai_gateway:price_manage"},
	}
	for _, test := range cases {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			checker := &recordingIAMChecker{}
			mux := http.NewServeMux()
			RegisterRoutes(mux, nil, nil, nil, nil, nil, nil, nil, secret, checker, nil, rejectingAdminVerifiedChecker{})
			req := httptest.NewRequest(test.method, test.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, req)
			if checker.permission != test.permission {
				t.Fatalf("权限码不一致: got=%s want=%s", checker.permission, test.permission)
			}
			if response.Code != http.StatusForbidden {
				t.Fatalf("权限通过后必须继续要求管理员二次认证: status=%d", response.Code)
			}
		})
	}
}
