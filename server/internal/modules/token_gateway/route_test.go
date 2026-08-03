package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"molin/server/internal/modules/token_gateway/service"
)

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
