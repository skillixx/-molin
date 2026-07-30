package middleware

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

var (
	ErrPublicSourceIPForbidden   = errors.New("公开流量来源无权限")
	ErrPublicSourceIPUnavailable = errors.New("公开流量来源解析器不可用")
)

// PublicSourceIPResolver 解析公开流量的唯一可信来源 IP，便于在限流计数前注入并测试安全门禁。
type PublicSourceIPResolver interface {
	Resolve(*http.Request) (netip.Addr, error)
}

type trustedProxySourceIPResolver struct {
	trusted []netip.Prefix
}

// NewPublicSourceIPResolver 使用启动阶段已严格校验的可信代理网段构造运行时解析器。
func NewPublicSourceIPResolver(trusted []netip.Prefix) PublicSourceIPResolver {
	return &trustedProxySourceIPResolver{trusted: append([]netip.Prefix(nil), trusted...)}
}

func (r *trustedProxySourceIPResolver) Resolve(req *http.Request) (netip.Addr, error) {
	if r == nil || req == nil {
		return netip.Addr{}, ErrPublicSourceIPUnavailable
	}
	remote, err := parsePublicRemoteAddr(req.RemoteAddr)
	if err != nil {
		return netip.Addr{}, ErrPublicSourceIPUnavailable
	}
	if !publicNetworkContains(r.trusted, remote) {
		// 非可信连接始终只认 RemoteAddr；所有来源 Header 都被有意忽略。
		return remote, nil
	}
	values := req.Header.Values("X-Real-IP")
	if len(values) != 1 || values[0] == "" || strings.Contains(values[0], ",") {
		return netip.Addr{}, ErrPublicSourceIPForbidden
	}
	realIP, err := netip.ParseAddr(values[0])
	if err != nil || realIP.Zone() != "" {
		return netip.Addr{}, ErrPublicSourceIPForbidden
	}
	return realIP, nil
}

func parsePublicRemoteAddr(remoteAddr string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || addr.Zone() != "" {
		return netip.Addr{}, ErrPublicSourceIPUnavailable
	}
	return addr, nil
}

func publicNetworkContains(networks []netip.Prefix, addr netip.Addr) bool {
	for _, network := range networks {
		if network.Contains(addr) {
			return true
		}
	}
	return false
}
