package handler

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
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
	token          string
	allowed        []netip.Prefix
	trustedProxies []netip.Prefix
	ready          bool
}

func NewMetricsHandler(emailSvc *service.EmailService, cfg config.Config) *MetricsHandler {
	allowed, allowedOK := parseInternalNetworks(cfg.InternalAllowedIPs)
	trusted, trustedOK := parseInternalNetworks(cfg.InternalTrustedProxyIPs)
	return &MetricsHandler{
		emailSvc:       emailSvc,
		token:          cfg.InternalAPIToken,
		allowed:        allowed,
		trustedProxies: trusted,
		ready:          emailSvc != nil && validInternalToken(cfg.InternalAPIToken) && allowedOK && trustedOK,
	}
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
}
