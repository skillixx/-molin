package security

import "testing"

// TestValidateOutboundURL_Block 校验非 https / 内网 / 回环 / localhost 一律拒绝（不解析 DNS 的配置时模式）。
func TestValidateOutboundURL_Block(t *testing.T) {
	blocked := []string{
		"http://api.example.com",       // 非 https
		"ftp://api.example.com",        // 非 https
		"https://localhost/x",          // localhost
		"https://foo.local/x",          // .local
		"https://svc.internal/x",       // .internal
		"https://127.0.0.1/x",          // 回环
		"https://10.0.0.1/x",           // 私有 A
		"https://172.16.0.1/x",         // 私有 B
		"https://192.168.1.1/x",        // 私有 C
		"https://169.254.169.254/meta", // 链路本地（云元数据）
		"https://[::1]/x",              // IPv6 回环
		"",                             // 空
	}
	for _, u := range blocked {
		if err := ValidateOutboundURL(u, nil, false); err == nil {
			t.Errorf("应拒绝: %q", u)
		}
	}
}

// TestValidateOutboundURL_AllowPublic 校验公网 https 字面量放行（不解析 DNS）。
func TestValidateOutboundURL_AllowPublic(t *testing.T) {
	if err := ValidateOutboundURL("https://8.8.8.8/x", nil, false); err != nil {
		t.Errorf("公网 IP 应放行，实际 %v", err)
	}
	// 域名在不解析 DNS 时放行（运行时再解析校验）。
	if err := ValidateOutboundURL("https://api.example.com/v1", nil, false); err != nil {
		t.Errorf("公网域名（配置时不解析）应放行，实际 %v", err)
	}
}

// TestValidateOutboundURL_Whitelist 校验白名单：非空时仅命中域名（含子域）放行。
func TestValidateOutboundURL_Whitelist(t *testing.T) {
	allow := []string{"weather.com", "docs.example.com"}
	if err := ValidateOutboundURL("https://api.weather.com/now", allow, false); err != nil {
		t.Errorf("白名单子域应放行，实际 %v", err)
	}
	if err := ValidateOutboundURL("https://weather.com/now", allow, false); err != nil {
		t.Errorf("白名单根域应放行，实际 %v", err)
	}
	if err := ValidateOutboundURL("https://evil.com/x", allow, false); err == nil {
		t.Errorf("非白名单域名应拒绝")
	}
}
