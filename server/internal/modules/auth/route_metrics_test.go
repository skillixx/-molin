package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"molin/server/internal/config"
	"molin/server/internal/middleware"
	"molin/server/internal/modules/auth/service"
)

func TestMetricsRouteDoesNotLetServeMuxPromoteHEADToGET(t *testing.T) {
	cfg := config.Config{
		InternalAPIToken:        strings.Repeat("t", 32),
		InternalAllowedIPs:      "192.0.2.10",
		InternalTrustedProxyIPs: "198.51.100.7",
	}
	emailSvc := service.NewEmailService(nil, nil, nil, nil, nil, "", "", "test", "mock")
	mux := http.NewServeMux()
	RegisterRoutes(mux, nil, nil, emailSvc, cfg, nil, nil, nil, middleware.NewPublicSourceIPResolver(nil), nil)
	req := httptest.NewRequest(http.MethodHead, "/api/internal/metrics", nil)
	req.RemoteAddr = "192.0.2.10:1"
	req.Header.Set("X-Internal-Token", cfg.InternalAPIToken)
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusMethodNotAllowed || resp.Header().Get("Allow") != http.MethodGet || strings.Contains(resp.Body.String(), "email_adapter_calls_total") {
		t.Fatalf("路由层必须显式拒绝 HEAD: status=%d headers=%v body=%s", resp.Code, resp.Header(), resp.Body.String())
	}
}
