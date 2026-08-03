package sms

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"molin/server/internal/config"
	"molin/server/internal/modules/sms/service"
	pkgjwt "molin/server/pkg/jwt"
	"molin/server/pkg/response"
)

type routeSummaryRepository struct{}

func (routeSummaryRepository) GetAdminSummary(context.Context) (service.SMSAdminSummary, error) {
	return service.SMSAdminSummary{TemplateTotal: 1}, nil
}

type routeSecurityStub struct {
	permission     bool
	verified       bool
	lastPermission string
}

func (s *routeSecurityStub) CheckPermission(_ context.Context, _ uint64, permission string) bool {
	s.lastPermission = permission
	return s.permission
}

func TestSMSAdminRegistersNineRoutesWithLeastPermissions(t *testing.T) {
	const secret = "sms-phase2-nine-routes-secret"
	token, err := pkgjwt.Generate(9, "admin@example.test", secret, 600)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ method, path, body, permission string }{
		{http.MethodGet, "/api/admin/sms/summary", "", "sms:template:view"},
		{http.MethodGet, "/api/admin/sms/templates", "", "sms:template:view"},
		{http.MethodGet, "/api/admin/sms/templates/1", "", "sms:template:view"},
		{http.MethodPost, "/api/admin/sms/templates/sync", "", "sms:template:sync"},
		{http.MethodGet, "/api/admin/sms/scenes", "", "sms:template:view"},
		{http.MethodPut, "/api/admin/sms/scenes/register", `{"template_id":1,"enabled":true,"version":0}`, "sms:template:manage"},
		{http.MethodPatch, "/api/admin/sms/templates/1/status", `{"enabled":true,"version":1}`, "sms:template:manage"},
		{http.MethodPost, "/api/admin/sms/templates/1/test-send", `{"scene":"register","phone":"phone-test-a"}`, "sms:template:test"},
		{http.MethodGet, "/api/admin/sms/send-logs", "", "sms:template:view"},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			security := &routeSecurityStub{permission: true, verified: true}
			mux := http.NewServeMux()
			RegisterAdminRoutes(mux, service.NewSMSAdminService(routeSummaryRepository{}), config.Config{JWTSecret: secret}, security, security)
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			req.Header.Set("Authorization", "Bearer "+token)
			if strings.Contains(tc.path, "test-send") {
				req.Header.Set("Idempotency-Key", "route-key")
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
				t.Fatalf("路由未注册: status=%d", rec.Code)
			}
			if security.lastPermission != tc.permission {
				t.Fatalf("权限错误: got=%s want=%s", security.lastPermission, tc.permission)
			}
		})
	}
}
func (s *routeSecurityStub) IsAdminVerified(context.Context, uint64) bool      { return s.verified }
func (s *routeSecurityStub) IsUserBlocked(context.Context, uint64) bool        { return false }
func (s *routeSecurityStub) IsAccessTokenRevoked(context.Context, string) bool { return false }

func TestSMSAdminSummaryRouteEnforcesAuthPermissionAndMFA(t *testing.T) {
	const secret = "sms-phase2-route-test-secret"
	token, err := pkgjwt.Generate(9, "admin@example.test", secret, 600)
	if err != nil {
		t.Fatalf("生成测试 Token 失败: %v", err)
	}
	tests := []struct {
		name       string
		token      string
		permission bool
		verified   bool
		wantHTTP   int
		wantCode   int
	}{
		{name: "无 Token", wantHTTP: 401, wantCode: 40001},
		{name: "缺少查看权限", token: token, wantHTTP: 403, wantCode: 40003},
		{name: "未完成双重认证", token: token, permission: true, wantHTTP: 403, wantCode: 40031},
		{name: "全部安全门通过", token: token, permission: true, verified: true, wantHTTP: 200, wantCode: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			security := &routeSecurityStub{permission: tc.permission, verified: tc.verified}
			mux := http.NewServeMux()
			RegisterAdminRoutes(mux, service.NewSMSAdminService(routeSummaryRepository{}), config.Config{JWTSecret: secret}, security, security)
			req := httptest.NewRequest(http.MethodGet, "/api/admin/sms/summary", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.wantHTTP {
				t.Fatalf("HTTP 状态错误: got=%d want=%d body=%s", rec.Code, tc.wantHTTP, rec.Body.String())
			}
			var body response.Body
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("解析响应失败: %v", err)
			}
			if body.Code != tc.wantCode {
				t.Fatalf("业务码错误: got=%d want=%d body=%s", body.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}
