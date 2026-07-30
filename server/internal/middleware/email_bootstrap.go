package middleware

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"molin/server/pkg/response"
)

const emailBootstrapSourceIPKey contextKey = "email_bootstrap_source_ip"

// EmailBootstrapNetworkGate 在任何 JWT、审计或数据库动作前校验独立 Token 与批准来源。
// 所有失败统一返回无权限，避免泄露具体配置状态。
func EmailBootstrapNetworkGate(token string, allowed, trusted []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values("X-Email-Bootstrap-Token")
		if len(values) != 1 || values[0] == "" || strings.Contains(values[0], ",") || !equalEmailBootstrapToken(values[0], token) {
			response.Error(w, http.StatusForbidden, 40003, "无权限")
			return
		}
		source, ok := resolveEmailBootstrapSource(r, trusted)
		if !ok || !prefixesContain(allowed, source) {
			response.Error(w, http.StatusForbidden, 40003, "无权限")
			return
		}
		ctx := context.WithValue(r.Context(), emailBootstrapSourceIPKey, source.String())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// equalEmailBootstrapToken 先将双方摘要为固定长度，再执行常量时间比较，避免长度差异直接暴露比较结果。
func equalEmailBootstrapToken(provided, expected string) bool {
	providedDigest := sha256.Sum256([]byte(provided))
	expectedDigest := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(providedDigest[:], expectedDigest[:]) == 1
}

// EmailBootstrapSourceIP 返回安全门禁已经解析并批准的来源 IP，审计不得重新读取 XFF。
func EmailBootstrapSourceIP(ctx context.Context) string {
	value, _ := ctx.Value(emailBootstrapSourceIPKey).(string)
	return value
}

// RequireStrictAuthorization 拒绝重复或逗号多值 Authorization，再交由现有 JWT 中间件处理具体状态。
func RequireStrictAuthorization(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values("Authorization")
		if len(values) != 1 || values[0] == "" || strings.Contains(values[0], ",") {
			response.Error(w, http.StatusUnauthorized, 40001, "未登录")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func resolveEmailBootstrapSource(r *http.Request, trusted []netip.Prefix) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote, err := netip.ParseAddr(host)
	if err != nil || remote.Zone() != "" {
		return netip.Addr{}, false
	}
	if !prefixesContain(trusted, remote) {
		return remote, true
	}
	values := r.Header.Values("X-Real-IP")
	if len(values) != 1 || values[0] == "" || strings.Contains(values[0], ",") {
		return netip.Addr{}, false
	}
	realIP, err := netip.ParseAddr(values[0])
	return realIP, err == nil && realIP.Zone() == ""
}

func prefixesContain(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
