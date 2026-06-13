// Package httputil 提供 HTTP 请求处理相关的小工具函数。
package httputil

import (
	"net/http"
	"strings"
)

// ClientIP 提取客户端真实 IP：优先取 X-Forwarded-For 的第一段，
// 否则回退到 r.RemoteAddr（去掉端口号）。
// 注意：X-Forwarded-For 可由客户端伪造，仅用于日志/审计等非安全判定场景。
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		ip := strings.TrimSpace(parts[0])
		if ip != "" {
			return ip
		}
	}
	return stripPort(r.RemoteAddr)
}

// stripPort 去掉 "ip:port" 中的端口部分，兼容 IPv6（如 "[::1]:8080"）。
func stripPort(addr string) string {
	if idx := strings.LastIndex(addr, ":"); idx > 0 {
		// 处理 IPv6 形如 "[::1]:8080" 的情况，去掉端口后保留中括号原样返回也可接受
		return addr[:idx]
	}
	return addr
}
