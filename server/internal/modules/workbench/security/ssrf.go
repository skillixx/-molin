// Package security 提供工作台外呼（插件转发 / skill 联网）的 SSRF 防护与 URL 校验。
// 配置时（运营建插件校验 endpoint_url）与运行时（实际外呼前）共用同一套规则，避免规则漂移。
package security

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateOutboundURL 校验外呼 URL 是否安全放行（契约 §5 SSRF 防护）。
//
// 规则：
//   - 必须为 https；
//   - 主机非 localhost / *.local / *.internal；
//   - 字面量 IP 不得为回环/私有/链路本地/未指定地址；
//   - resolveDNS=true（运行时）时额外解析主机名，要求所有解析到的 IP 均为公网，
//     防 DNS 解析绕过（域名指向内网）；
//   - allowedDomains 非空（运维注入白名单，S2-运2）时，主机须以其中某个域名（或其子域）匹配。
//
// 返回非 nil error 表示拒绝放行；error 已带中文原因，调用方可直接透出或包装为校验错误。
func ValidateOutboundURL(raw string, allowedDomains []string, resolveDNS bool) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("URL 不能为空")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("URL 非法")
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("仅允许 https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("缺少主机名")
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".internal") {
		return fmt.Errorf("不允许指向内网/本机")
	}

	// 域名白名单（可选）：非空则必须命中。
	if len(allowedDomains) > 0 && !hostAllowed(lower, allowedDomains) {
		return fmt.Errorf("域名 %s 不在白名单内", host)
	}

	// 字面量 IP：直接判定网段。
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("不允许指向内网/回环地址")
		}
		return nil
	}

	// 域名：运行时解析后逐个校验解析 IP（防 DNS 指向内网）。配置时不解析（域名尚未生效/可能临时不可达）。
	if resolveDNS {
		ips, lookupErr := net.LookupIP(host)
		if lookupErr != nil {
			return fmt.Errorf("主机名解析失败: %v", lookupErr)
		}
		if len(ips) == 0 {
			return fmt.Errorf("主机名无解析结果")
		}
		for _, ip := range ips {
			if isBlockedIP(ip) {
				return fmt.Errorf("主机名解析到内网/回环地址，拒绝外呼")
			}
		}
	}
	return nil
}

// hostAllowed 判断主机是否命中白名单（精确匹配或子域）。allowedDomains 元素已小写约定，这里也兜底转小写。
func hostAllowed(host string, allowedDomains []string) bool {
	for _, d := range allowedDomains {
		d = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(d, ".")))
		if d == "" {
			continue
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// isBlockedIP 判断 IP 是否属禁止外呼网段（回环/私有/链路本地/未指定/唯一本地 IPv6 等）。
func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	return false
}
