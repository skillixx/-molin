package handler

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"molin/server/internal/config"
	"molin/server/internal/modules/auth/service"
	"molin/server/pkg/response"
)

var rejectedInternalTokens = map[string]struct{}{
	"": {}, "REPLACE_WITH_INTERNAL_API_TOKEN": {}, "CHANGE_ME": {}, "CHANGEME": {},
	"DEFAULT": {}, "SECRET": {}, "TEST": {},
}

type MetricsHandler struct {
	emailSvc       *service.EmailService
	smsMetrics     SMSMetricsReader
	aiMetrics      AIGatewayMetricsReader
	token          string
	allowed        []netip.Prefix
	trustedProxies []netip.Prefix
	ready          bool
}

// SMSMetricsReader 是短信模块向统一内部指标端点提供的最小只读契约，避免观测层接触手机号或请求明细。
type SMSMetricsReader interface {
	SMSProviderMetricValue(scene, result string) uint64
	SMSProviderDuration(scene string) (count uint64, totalNanoseconds uint64)
}

// AIGatewayMetricsReader 只返回已经完成低基数约束和脱敏处理的 Prometheus 文本。
// 统一指标处理器不接触请求明细、用户身份、Project 或任何密钥。
type AIGatewayMetricsReader interface {
	AIGatewayPrometheus(ctx context.Context) (string, error)
}

func NewMetricsHandler(emailSvc *service.EmailService, cfg config.Config, smsReaders ...SMSMetricsReader) *MetricsHandler {
	allowed, allowedOK := parseInternalNetworks(cfg.InternalAllowedIPs)
	trusted, trustedOK := parseInternalNetworks(cfg.InternalTrustedProxyIPs)
	var smsMetrics SMSMetricsReader
	if len(smsReaders) == 1 {
		smsMetrics = smsReaders[0]
	}
	return &MetricsHandler{
		emailSvc:       emailSvc,
		smsMetrics:     smsMetrics,
		token:          cfg.InternalAPIToken,
		allowed:        allowed,
		trustedProxies: trusted,
		ready:          emailSvc != nil && validInternalToken(cfg.InternalAPIToken) && allowedOK && trustedOK,
	}
}

// WithAIGatewayMetrics 在 Token 网关完成延迟装配后接入统一指标端点。
// Handler 在路由注册时已经创建，因此这里保留链式注入，避免新增第二个 metrics 路径。
func (h *MetricsHandler) WithAIGatewayMetrics(reader AIGatewayMetricsReader) *MetricsHandler {
	h.aiMetrics = reader
	return h
}

func validInternalToken(token string) bool {
	if !utf8.ValidString(token) || len([]byte(token)) < 32 || strings.TrimSpace(token) != token {
		return false
	}
	_, rejected := rejectedInternalTokens[strings.ToUpper(token)]
	return !rejected
}

func parseInternalNetworks(raw string) ([]netip.Prefix, bool) {
	if raw == "" {
		return nil, false
	}
	parts := strings.Split(raw, ",")
	out := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			return nil, false
		}
		if addr, err := netip.ParseAddr(item); err == nil && addr.Zone() == "" {
			bits := 128
			if addr.Is4() {
				bits = 32
			}
			out = append(out, netip.PrefixFrom(addr, bits))
			continue
		}
		prefix, err := netip.ParsePrefix(item)
		if err != nil || prefix.Addr().Zone() != "" {
			return nil, false
		}
		out = append(out, prefix.Masked())
	}
	return out, len(out) > 0
}

func internalTokenEqual(got, expected string) bool {
	gotHash, expectedHash := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(gotHash[:], expectedHash[:]) == 1
}

func networkContains(networks []netip.Prefix, addr netip.Addr) bool {
	for _, network := range networks {
		if network.Contains(addr) {
			return true
		}
	}
	return false
}

func parseRemoteIP(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	return addr, err == nil && addr.Zone() == ""
}

func (h *MetricsHandler) authorized(r *http.Request) bool {
	if !h.ready {
		return false
	}
	tokens := r.Header.Values("X-Internal-Token")
	if len(tokens) != 1 || !internalTokenEqual(tokens[0], h.token) {
		return false
	}
	remote, ok := parseRemoteIP(r.RemoteAddr)
	if !ok {
		return false
	}
	source := remote
	if networkContains(h.trustedProxies, remote) {
		values := r.Header.Values("X-Real-IP")
		if len(values) != 1 || values[0] == "" || strings.Contains(values[0], ",") {
			return false
		}
		realIP, err := netip.ParseAddr(values[0])
		if err != nil || realIP.Zone() != "" {
			return false
		}
		source = realIP
	}
	return networkContains(h.allowed, source)
}

func (h *MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		response.Error(w, http.StatusForbidden, 40003, "无权限")
		return
	}
	aiMetricsText := ""
	if h.aiMetrics != nil {
		var err error
		aiMetricsText, err = h.aiMetrics.AIGatewayPrometheus(r.Context())
		if err != nil {
			// 指标事实不可用时失败关闭，避免监控把缺失的财务差额误判为正常零值。
			response.Error(w, http.StatusServiceUnavailable, 50300, "指标服务暂不可用")
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintln(w, "# HELP email_adapter_calls_total 邮件供应商 Adapter 调用总数。")
	_, _ = fmt.Fprintln(w, "# TYPE email_adapter_calls_total counter")
	operations := []struct {
		operation string
		scenes    []string
	}{
		{operation: "query_templates", scenes: []string{"template_sync"}},
		{operation: "describe_template", scenes: []string{"template_sync"}},
		{operation: "send_mail", scenes: []string{"register", "login", "reset_password", "bind_email", "admin_verify"}},
	}
	results := []string{"accepted", "failed", "timeout"}
	for _, item := range operations {
		for _, scene := range item.scenes {
			for _, result := range results {
				value := h.emailSvc.AdapterCallCount(item.operation, scene, result)
				_, _ = fmt.Fprintf(w, "email_adapter_calls_total{operation=%q,scene=%q,result=%q} %d\n", item.operation, scene, result, value)
			}
		}
	}
	if h.smsMetrics != nil {
		_, _ = fmt.Fprintln(w, "# HELP sms_provider_calls_total 短信供应商提交调用总数，accepted 仅表示供应商受理。")
		_, _ = fmt.Fprintln(w, "# TYPE sms_provider_calls_total counter")
		smsScenes := []string{"register", "login", "reset_password", "bind_phone", "admin_verify"}
		smsResults := []string{"accepted", "timeout", "rate_limit", "signature", "template", "arrears", "network", "rejected"}
		for _, scene := range smsScenes {
			for _, result := range smsResults {
				value := h.smsMetrics.SMSProviderMetricValue(scene, result)
				_, _ = fmt.Fprintf(w, "sms_provider_calls_total{scene=%q,result=%q} %d\n", scene, result, value)
			}
		}
		_, _ = fmt.Fprintln(w, "# HELP sms_provider_request_duration_seconds 短信供应商提交调用累计耗时秒数。")
		_, _ = fmt.Fprintln(w, "# TYPE sms_provider_request_duration_seconds summary")
		for _, scene := range smsScenes {
			count, totalNanoseconds := h.smsMetrics.SMSProviderDuration(scene)
			_, _ = fmt.Fprintf(w, "sms_provider_request_duration_seconds_sum{scene=%q} %.9f\n", scene, float64(totalNanoseconds)/float64(time.Second))
			_, _ = fmt.Fprintf(w, "sms_provider_request_duration_seconds_count{scene=%q} %d\n", scene, count)
		}
	}
	if aiMetricsText != "" {
		_, _ = fmt.Fprint(w, aiMetricsText)
		if !strings.HasSuffix(aiMetricsText, "\n") {
			_, _ = fmt.Fprintln(w)
		}
	}
}
