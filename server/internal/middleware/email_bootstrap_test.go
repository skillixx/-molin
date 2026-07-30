package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestEmailBootstrapNetworkGateRequiresTokenAndApprovedSource(t *testing.T) {
	token := "bootstrap-" + strings.Repeat("ab", 16)
	allowed := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	trusted := []netip.Prefix{netip.MustParsePrefix("198.51.100.10/32")}
	called, auditedIP := false, ""
	h := EmailBootstrapNetworkGate(token, allowed, trusted, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
		auditedIP = EmailBootstrapSourceIP(r.Context())
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/internal/email/bootstrap/admin-verify", nil)
	req.RemoteAddr = "198.51.100.10:443"
	req.Header.Set("X-Email-Bootstrap-Token", token)
	req.Header.Set("X-Real-IP", "192.0.2.7")
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if !called || auditedIP != "192.0.2.7" {
		t.Fatalf("合法双闸请求应放行: status=%d body=%s", resp.Code, resp.Body.String())
	}

	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del("X-Email-Bootstrap-Token") },
		func(r *http.Request) {
			r.Header.Del("X-Email-Bootstrap-Token")
			r.Header.Add("X-Email-Bootstrap-Token", token)
			r.Header.Add("X-Email-Bootstrap-Token", token)
		},
		func(r *http.Request) { r.Header.Set("X-Email-Bootstrap-Token", token+",x") },
		func(r *http.Request) { r.Header.Set("X-Real-IP", "203.0.113.1") },
		func(r *http.Request) {
			r.Header.Del("X-Real-IP")
			r.Header.Add("X-Real-IP", "192.0.2.7")
			r.Header.Add("X-Real-IP", "192.0.2.8")
		},
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/internal/email/bootstrap/admin-verify", nil)
		r.RemoteAddr = "198.51.100.10:443"
		r.Header.Set("X-Email-Bootstrap-Token", token)
		r.Header.Set("X-Real-IP", "192.0.2.7")
		mutate(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":40003`) {
			t.Fatalf("双闸异常必须统一403: status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestRequireStrictAuthorizationRejectsRepeatedValues(t *testing.T) {
	h := RequireStrictAuthorization(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Add("Authorization", "Bearer a")
	req.Header.Add("Authorization", "Bearer b")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized || !strings.Contains(resp.Body.String(), `"code":40001`) {
		t.Fatalf("重复 Authorization 必须按标准401拒绝: %d %s", resp.Code, resp.Body.String())
	}
}

func TestEmailBootstrapNetworkGateIgnoresXRealIPFromUntrustedPeer(t *testing.T) {
	token := "bootstrap-" + strings.Repeat("ab", 16)
	h := EmailBootstrapNetworkGate(
		token,
		[]netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
		[]netip.Prefix{netip.MustParsePrefix("198.51.100.10/32")},
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/internal/email/bootstrap/admin-verify", nil)
	req.RemoteAddr = "203.0.113.9:443"
	req.Header.Set("X-Email-Bootstrap-Token", token)
	// 非可信直连方即使伪造获批 X-Real-IP，也必须仅按 RemoteAddr 判定并拒绝。
	req.Header.Set("X-Real-IP", "192.0.2.7")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body.String(), `"code":40003`) {
		t.Fatalf("非可信来源的 X-Real-IP 诱饵必须被忽略: status=%d body=%s", resp.Code, resp.Body.String())
	}
}
