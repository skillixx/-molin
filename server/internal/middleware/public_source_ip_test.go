package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func publicSourceTestResolver(t *testing.T, raw ...string) PublicSourceIPResolver {
	t.Helper()
	networks := make([]netip.Prefix, 0, len(raw))
	for _, item := range raw {
		if prefix, err := netip.ParsePrefix(item); err == nil {
			networks = append(networks, prefix.Masked())
			continue
		}
		addr := netip.MustParseAddr(item)
		bits := 128
		if addr.Is4() {
			bits = 32
		}
		networks = append(networks, netip.PrefixFrom(addr, bits))
	}
	return NewPublicSourceIPResolver(networks)
}

func TestPublicSourceIPSecurityMatrix(t *testing.T) {
	tests := []struct {
		name       string
		resolver   PublicSourceIPResolver
		remote     string
		realIPs    []string
		xff        string
		forwarded  string
		wantStatus int
		wantIP     string
	}{
		{name: "空配置直连忽略全部来源头", resolver: publicSourceTestResolver(t), remote: "203.0.113.8:1234", realIPs: []string{"192.0.2.1"}, xff: "192.0.2.2", forwarded: "for=192.0.2.3", wantStatus: http.StatusNoContent, wantIP: "203.0.113.8"},
		{name: "精确可信代理使用单值", resolver: publicSourceTestResolver(t, "198.51.100.7"), remote: "198.51.100.7:443", realIPs: []string{"192.0.2.10"}, wantStatus: http.StatusNoContent, wantIP: "192.0.2.10"},
		{name: "CIDR可信代理使用IPv6单值", resolver: publicSourceTestResolver(t, "2001:db8:ffff::/48"), remote: "[2001:db8:ffff::8]:443", realIPs: []string{"2001:db8::10"}, wantStatus: http.StatusNoContent, wantIP: "2001:db8::10"},
		{name: "非可信代理伪造无效", resolver: publicSourceTestResolver(t, "198.51.100.0/24"), remote: "203.0.113.8:443", realIPs: []string{"192.0.2.10"}, xff: "192.0.2.11", forwarded: "for=192.0.2.12", wantStatus: http.StatusNoContent, wantIP: "203.0.113.8"},
		{name: "可信代理缺少真实地址", resolver: publicSourceTestResolver(t, "198.51.100.7"), remote: "198.51.100.7:443", wantStatus: http.StatusForbidden},
		{name: "可信代理真实地址为空", resolver: publicSourceTestResolver(t, "198.51.100.7"), remote: "198.51.100.7:443", realIPs: []string{""}, wantStatus: http.StatusForbidden},
		{name: "可信代理真实地址非法", resolver: publicSourceTestResolver(t, "198.51.100.7"), remote: "198.51.100.7:443", realIPs: []string{"192.0.2.999"}, wantStatus: http.StatusForbidden},
		{name: "可信代理拒绝IPv6 zone", resolver: publicSourceTestResolver(t, "198.51.100.7"), remote: "198.51.100.7:443", realIPs: []string{"fe80::1%eth0"}, wantStatus: http.StatusForbidden},
		{name: "可信代理拒绝逗号多值", resolver: publicSourceTestResolver(t, "198.51.100.7"), remote: "198.51.100.7:443", realIPs: []string{"192.0.2.10, 192.0.2.11"}, wantStatus: http.StatusForbidden},
		{name: "可信代理拒绝重复Header", resolver: publicSourceTestResolver(t, "198.51.100.7"), remote: "198.51.100.7:443", realIPs: []string{"192.0.2.10", "192.0.2.11"}, wantStatus: http.StatusForbidden},
		{name: "冲突XFF不改变合法真实地址", resolver: publicSourceTestResolver(t, "198.51.100.7"), remote: "198.51.100.7:443", realIPs: []string{"192.0.2.10"}, xff: "203.0.113.99", forwarded: "for=203.0.113.98", wantStatus: http.StatusNoContent, wantIP: "192.0.2.10"},
		{name: "运行时解析器不可用", resolver: nil, remote: "203.0.113.8:443", wantStatus: http.StatusServiceUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			counterCalls, serviceCalls := 0, 0
			var countedKey string
			counter := func(_ context.Context, key string, _ int, _ time.Duration) (bool, error) {
				counterCalls++
				countedKey = key
				return true, nil
			}
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				serviceCalls++
				w.WriteHeader(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "/api/auth/verification-codes/email", nil)
			req.RemoteAddr = tc.remote
			for _, value := range tc.realIPs {
				req.Header.Add("X-Real-IP", value)
			}
			req.Header.Set("X-Forwarded-For", tc.xff)
			req.Header.Set("Forwarded", tc.forwarded)
			resp := httptest.NewRecorder()

			rateLimitEmailByIP(tc.resolver, counter, "send_email_code", 10, time.Minute, next).ServeHTTP(resp, req)

			if resp.Code != tc.wantStatus {
				t.Fatalf("状态码错误: got=%d body=%s", resp.Code, resp.Body.String())
			}
			if tc.wantStatus == http.StatusNoContent {
				if counterCalls != 1 || serviceCalls != 1 || !strings.Contains(countedKey, tc.wantIP) {
					t.Fatalf("合法请求来源或副作用错误: key=%s counter=%d service=%d", countedKey, counterCalls, serviceCalls)
				}
				return
			}
			if counterCalls != 0 || serviceCalls != 0 {
				t.Fatalf("来源前置拒绝不得产生限流或服务副作用: counter=%d service=%d", counterCalls, serviceCalls)
			}
			if tc.wantStatus == http.StatusForbidden && (!strings.Contains(resp.Body.String(), `"code":40003`) || !strings.Contains(resp.Body.String(), "无权限")) {
				t.Fatalf("可信代理异常契约错误: %s", resp.Body.String())
			}
			if tc.wantStatus == http.StatusServiceUnavailable && (!strings.Contains(resp.Body.String(), `"code":51003`) || !strings.Contains(resp.Body.String(), "邮件发送服务未就绪")) {
				t.Fatalf("解析器不可用契约错误: %s", resp.Body.String())
			}
		})
	}
}

func TestEmailIPRateLimitEleventhRequestAndXFFBypass(t *testing.T) {
	counts := map[string]int{}
	counter := func(_ context.Context, key string, limit int, _ time.Duration) (bool, error) {
		counts[key]++
		return counts[key] <= limit, nil
	}
	serviceCalls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		serviceCalls++
		w.WriteHeader(http.StatusNoContent)
	})
	handler := rateLimitEmailByIP(publicSourceTestResolver(t), counter, "send_email_code", 10, time.Minute, next)
	for i := 1; i <= 11; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/verification-codes/email", nil)
		req.RemoteAddr = "203.0.113.8:443"
		// 每次伪造不同 XFF，验证应用不会把它作为限流键绕过每 IP 上限。
		req.Header.Set("X-Forwarded-For", netip.AddrFrom4([4]byte{192, 0, 2, byte(i)}).String())
		req.Header.Set("X-Real-IP", netip.AddrFrom4([4]byte{198, 51, 100, byte(i)}).String())
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if i <= 10 && resp.Code != http.StatusNoContent {
			t.Fatalf("第 %d 次请求应放行: %d %s", i, resp.Code, resp.Body.String())
		}
		if i == 11 && (resp.Code != http.StatusTooManyRequests || !strings.Contains(resp.Body.String(), `"code":42900`)) {
			t.Fatalf("第十一次请求必须限流: %d %s", resp.Code, resp.Body.String())
		}
	}
	if serviceCalls != 10 || len(counts) != 1 {
		t.Fatalf("XFF 或 X-Real-IP 伪造不得拆分直连限流桶: service=%d buckets=%d", serviceCalls, len(counts))
	}
}
