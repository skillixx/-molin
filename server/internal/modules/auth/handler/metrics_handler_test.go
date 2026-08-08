package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"molin/server/internal/config"
	"molin/server/internal/modules/auth/service"
)

func validMetricsConfig() config.Config {
	return config.Config{
		InternalAPIToken:        strings.Repeat("t", 32),
		InternalAllowedIPs:      "192.0.2.10,2001:db8::/32",
		InternalTrustedProxyIPs: "198.51.100.7,2001:db8:ffff::/48",
	}
}

func emptyMetricsService() *service.EmailService {
	return service.NewEmailService(nil, nil, nil, nil, nil, "", "", "test", "mock")
}

type fakeSMSMetricsReader struct{}

func (fakeSMSMetricsReader) SMSProviderMetricValue(scene, result string) uint64 {
	if scene == "register" && result == "accepted" {
		return 2
	}
	return 0
}

func (fakeSMSMetricsReader) SMSProviderDuration(scene string) (uint64, uint64) {
	if scene == "register" {
		return 2, 1500000000
	}
	return 0, 0
}

func metricsRequest(method, remote, token string) *http.Request {
	req := httptest.NewRequest(method, "/api/internal/metrics", nil)
	req.RemoteAddr = remote
	if token != "" {
		req.Header.Set("X-Internal-Token", token)
	}
	return req
}

func TestMetricsSuccessOutputsOnlyClosedTwentyOneSeries(t *testing.T) {
	cfg := validMetricsConfig()
	h := NewMetricsHandler(emptyMetricsService(), cfg)
	req := metricsRequest(http.MethodGet, "192.0.2.10:4321", cfg.InternalAPIToken)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK || resp.Header().Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" || resp.Header().Get("Cache-Control") != "no-store" || resp.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("指标成功响应头错误: status=%d headers=%v", resp.Code, resp.Header())
	}
	body := resp.Body.String()
	if strings.Count(body, "\nemail_adapter_calls_total{") != 21 || !strings.Contains(body, "# HELP email_adapter_calls_total ") || !strings.Contains(body, "# TYPE email_adapter_calls_total counter") {
		t.Fatalf("必须输出完整 21 序列和唯一 HELP/TYPE: %s", body)
	}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if !strings.HasPrefix(line, "# HELP email_adapter_calls_total ") && line != "# TYPE email_adapter_calls_total counter" && !strings.HasPrefix(line, "email_adapter_calls_total{") {
			t.Fatalf("出现非白名单指标族: %s", line)
		}
		if strings.HasPrefix(line, "email_adapter_calls_total{") && !strings.HasSuffix(line, " 0") {
			t.Fatalf("启动零值序列必须为 0: %s", line)
		}
	}
}

func TestMetricsExportsClosedSMSSeriesWithoutSensitiveLabels(t *testing.T) {
	cfg := validMetricsConfig()
	h := NewMetricsHandler(emptyMetricsService(), cfg, fakeSMSMetricsReader{})
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, metricsRequest(http.MethodGet, "192.0.2.10:4321", cfg.InternalAPIToken))
	if resp.Code != http.StatusOK {
		t.Fatalf("短信指标导出失败: %s", resp.Body.String())
	}
	body := resp.Body.String()
	if strings.Count(body, "\nsms_provider_calls_total{") != 40 {
		t.Fatalf("五场景八结果必须固定导出 40 条序列")
	}
	if strings.Count(body, "\nsms_provider_request_duration_seconds_sum{") != 5 || strings.Count(body, "\nsms_provider_request_duration_seconds_count{") != 5 {
		t.Fatalf("五场景耗时 sum/count 序列不完整")
	}
	if !strings.Contains(body, `sms_provider_calls_total{scene="register",result="accepted"} 2`) ||
		!strings.Contains(body, `sms_provider_request_duration_seconds_sum{scene="register"} 1.500000000`) ||
		!strings.Contains(body, `sms_provider_request_duration_seconds_count{scene="register"} 2`) {
		t.Fatalf("短信指标值错误: %s", body)
	}
	for _, forbidden := range []string{"phone", "business_request", "provider_code", "template_code", "access_key", "otp"} {
		if strings.Contains(strings.ToLower(body), forbidden+"=") {
			t.Fatalf("短信指标出现禁止的高基数或敏感标签: %s", forbidden)
		}
	}
}

func TestMetricsRejectsEveryNonGETMethod(t *testing.T) {
	cfg := validMetricsConfig()
	h := NewMetricsHandler(emptyMetricsService(), cfg)
	for _, method := range []string{http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, metricsRequest(method, "192.0.2.10:1", cfg.InternalAPIToken))
		if resp.Code != http.StatusMethodNotAllowed || resp.Header().Get("Allow") != http.MethodGet || strings.Contains(resp.Body.String(), "email_adapter_calls_total") {
			t.Fatalf("非 GET 方法契约错误: method=%s status=%d body=%s", method, resp.Code, resp.Body.String())
		}
	}
}

func TestMetricsTokenAndConfigFailClosed(t *testing.T) {
	valid := validMetricsConfig()
	for _, token := range []string{"", "wrong", " " + strings.Repeat("t", 32), strings.Repeat("t", 32) + " ", "replace_with_internal_api_token", "change_me", "changeme", "default", "secret", "test"} {
		cfg := valid
		cfg.InternalAPIToken = token
		h := NewMetricsHandler(emptyMetricsService(), cfg)
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, metricsRequest(http.MethodGet, "192.0.2.10:1", token))
		assertMetricsForbidden(t, resp)
	}
	for _, mutate := range []func(*config.Config){
		func(c *config.Config) { c.InternalAllowedIPs = "" },
		func(c *config.Config) { c.InternalAllowedIPs = "192.0.2.10," },
		func(c *config.Config) { c.InternalAllowedIPs = "not-an-ip" },
		func(c *config.Config) { c.InternalTrustedProxyIPs = "" },
		func(c *config.Config) { c.InternalTrustedProxyIPs = ",198.51.100.7" },
		func(c *config.Config) { c.InternalTrustedProxyIPs = "198.51.100.999" },
	} {
		cfg := valid
		mutate(&cfg)
		resp := httptest.NewRecorder()
		NewMetricsHandler(emptyMetricsService(), cfg).ServeHTTP(resp, metricsRequest(http.MethodGet, "192.0.2.10:1", cfg.InternalAPIToken))
		assertMetricsForbidden(t, resp)
	}
}

func TestMetricsTokenUsesExactRawBytes(t *testing.T) {
	cfg := validMetricsConfig()
	h := NewMetricsHandler(emptyMetricsService(), cfg)
	for _, token := range []string{"", strings.Repeat("T", 32), " " + cfg.InternalAPIToken, cfg.InternalAPIToken + " ", "x" + cfg.InternalAPIToken[1:], cfg.InternalAPIToken[:31] + "x"} {
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, metricsRequest(http.MethodGet, "192.0.2.10:1", token))
		assertMetricsForbidden(t, resp)
	}
	req := metricsRequest(http.MethodGet, "192.0.2.10:1", cfg.InternalAPIToken)
	req.Header.Add("X-Internal-Token", cfg.InternalAPIToken)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	assertMetricsForbidden(t, resp)
}

func TestMetricsSourceIPTruthAndTrustedProxy(t *testing.T) {
	cfg := validMetricsConfig()
	h := NewMetricsHandler(emptyMetricsService(), cfg)

	// 非可信来源不能通过伪造的来源头改变 RemoteAddr 判定，XFF 永远无效。
	req := metricsRequest(http.MethodGet, "203.0.113.9:1", cfg.InternalAPIToken)
	req.Header.Set("X-Real-IP", "192.0.2.10")
	req.Header.Set("X-Forwarded-For", "192.0.2.10")
	req.Header.Set("Forwarded", "for=192.0.2.10")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	assertMetricsForbidden(t, resp)

	// 可信代理只能使用覆盖写入的单个合法 X-Real-IP。
	req = metricsRequest(http.MethodGet, "198.51.100.7:1", cfg.InternalAPIToken)
	req.Header.Set("X-Real-IP", "192.0.2.10")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	resp = httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("可信代理合法单值应通过: %s", resp.Body.String())
	}

	for _, values := range [][]string{nil, {""}, {"not-an-ip"}, {"192.0.2.10, 203.0.113.9"}, {"192.0.2.10", "192.0.2.11"}} {
		req = metricsRequest(http.MethodGet, "198.51.100.7:1", cfg.InternalAPIToken)
		for _, value := range values {
			req.Header.Add("X-Real-IP", value)
		}
		resp = httptest.NewRecorder()
		h.ServeHTTP(resp, req)
		assertMetricsForbidden(t, resp)
	}

	// 只提供 XFF 时不允许可信代理回退到 XFF 或代理自身地址。
	req = metricsRequest(http.MethodGet, "198.51.100.7:1", cfg.InternalAPIToken)
	req.Header.Set("X-Forwarded-For", "192.0.2.10")
	resp = httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	assertMetricsForbidden(t, resp)
}

func assertMetricsForbidden(t *testing.T, resp *httptest.ResponseRecorder) {
	t.Helper()
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body.String(), `"code":40003`) || !strings.Contains(resp.Body.String(), "无权限") || strings.Contains(resp.Body.String(), "email_adapter_calls_total") {
		t.Fatalf("安全闸失败必须使用统一 403 外观: status=%d body=%s", resp.Code, resp.Body.String())
	}
}
