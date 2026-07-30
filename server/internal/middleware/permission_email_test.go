package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type rejectedEmailAdminVerifier struct{}

func (rejectedEmailAdminVerifier) IsAdminVerified(context.Context, uint64) bool { return false }

func TestRequireEmailAdminVerifiedUsesFrozenErrorContract(t *testing.T) {
	called := false
	h := RequireEmailAdminVerified(rejectedEmailAdminVerifier{}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/admin/email/summary", nil)
	resp := httptest.NewRecorder()

	h.ServeHTTP(resp, req)

	if called || resp.Code != http.StatusForbidden || !strings.Contains(resp.Body.String(), `"code":40003`) || !strings.Contains(resp.Body.String(), "请先完成管理员双重认证") {
		t.Fatalf("邮件管理 MFA 错误契约不正确: status=%d body=%s", resp.Code, resp.Body.String())
	}
}

type rejectedEmailPermission struct{}

func (rejectedEmailPermission) CheckPermission(context.Context, uint64, string) bool { return false }

func TestRequireEmailPermDoesNotChangeGlobalPermissionMessage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/admin/email/summary", nil)
	mailResp := httptest.NewRecorder()
	RequireEmailPerm(rejectedEmailPermission{}, "email:template:view", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(mailResp, req)
	if mailResp.Code != http.StatusForbidden || !strings.Contains(mailResp.Body.String(), `"message":"无权限"`) {
		t.Fatalf("邮件权限文案不正确: %d %s", mailResp.Code, mailResp.Body.String())
	}
	globalResp := httptest.NewRecorder()
	RequirePerm(rejectedEmailPermission{}, "user:manage", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(globalResp, req)
	if !strings.Contains(globalResp.Body.String(), `"message":"无操作权限"`) {
		t.Fatalf("全局历史权限文案被意外改变: %s", globalResp.Body.String())
	}
}
