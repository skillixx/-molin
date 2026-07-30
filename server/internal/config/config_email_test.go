package config

import (
	"net/netip"
	"os"
	"strings"
	"testing"
)

func TestEmailEnvironmentIsNormalizedAndFailClosed(t *testing.T) {
	t.Setenv("DIRECTMAIL_FROM_ALIAS", "")
	t.Setenv("APP_ENV", "  PrOdUcTiOn  ")
	if got := Load(); got.AppEnv != "production" || got.IsSafeNonProduction() {
		t.Fatalf("生产环境规范化错误: %q", got.AppEnv)
	}
	t.Setenv("APP_ENV", "unknown-environment")
	if Load().IsSafeNonProduction() {
		t.Fatal("未知环境不得开放 Mock 或验证码调试回传")
	}
	t.Setenv("APP_ENV", " TEST ")
	if got := Load(); !got.IsSafeNonProduction() || got.DirectMailFromAlias != "墨灵" {
		t.Fatalf("显式测试环境或默认发件人别名错误: %#v", got)
	}
}

func TestLoadOnlyEnablesEmailDebugCodeInExplicitSafeEnvironment(t *testing.T) {
	tests := []struct {
		name       string
		appEnv     *string
		debugValue string
		wantEnv    string
		wantDebug  bool
	}{
		{name: "未设置环境", debugValue: "true", wantEnv: "local", wantDebug: false},
		{name: "空值环境", appEnv: stringPointer(""), debugValue: "true", wantEnv: "local", wantDebug: false},
		{name: "空白环境", appEnv: stringPointer("   "), debugValue: "true", wantEnv: "", wantDebug: false},
		{name: "未知环境", appEnv: stringPointer("preview"), debugValue: "true", wantEnv: "preview", wantDebug: false},
		{name: "生产环境", appEnv: stringPointer("production"), debugValue: "true", wantEnv: "production", wantDebug: false},
		{name: "预发布环境不允许", appEnv: stringPointer("staging"), debugValue: "true", wantEnv: "staging", wantDebug: false},
		{name: "显式本地环境", appEnv: stringPointer("local"), debugValue: "true", wantEnv: "local", wantDebug: true},
		{name: "显式开发环境全称", appEnv: stringPointer("development"), debugValue: "true", wantEnv: "development", wantDebug: true},
		{name: "显式开发环境简称", appEnv: stringPointer("dev"), debugValue: "true", wantEnv: "dev", wantDebug: true},
		{name: "显式测试环境", appEnv: stringPointer(" test "), debugValue: "true", wantEnv: "test", wantDebug: true},
		{name: "显式扩展测试环境", appEnv: stringPointer("TESTING"), debugValue: "true", wantEnv: "testing", wantDebug: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.appEnv == nil {
				old, present := os.LookupEnv("APP_ENV")
				_ = os.Unsetenv("APP_ENV")
				t.Cleanup(func() {
					if present {
						_ = os.Setenv("APP_ENV", old)
						return
					}
					_ = os.Unsetenv("APP_ENV")
				})
			} else {
				t.Setenv("APP_ENV", *tc.appEnv)
			}
			t.Setenv("EMAIL_DEBUG_RETURN_CODE", tc.debugValue)

			got := Load()
			if got.AppEnv != tc.wantEnv || got.EmailDebugReturnCode != tc.wantDebug {
				t.Fatalf("邮件调试回码环境门禁错误: app_env=%q debug=%t", got.AppEnv, got.EmailDebugReturnCode)
			}
		})
	}
}

func TestLoadEmailDebugCodeRequiresExactLowercaseTrue(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	tests := []struct {
		value string
		want  bool
	}{
		{value: "true", want: true},
		{value: " true ", want: true},
		{value: ""},
		{value: "1"},
		{value: "t"},
		{value: "y"},
		{value: "yes"},
		{value: "on"},
		{value: "TRUE"},
		{value: "True"},
		{value: "false"},
	}
	for _, tc := range tests {
		t.Run("值_"+tc.value, func(t *testing.T) {
			t.Setenv("EMAIL_DEBUG_RETURN_CODE", tc.value)
			if got := Load().EmailDebugReturnCode; got != tc.want {
				t.Fatalf("调试回码开关必须仅接受去空白后的精确小写 true: value=%q got=%t", tc.value, got)
			}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}

func TestEmailAdminVerifyBootstrapConfigFailsClosed(t *testing.T) {
	keys := []string{"EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED", "EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN", "EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS", "EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS"}
	for _, key := range keys {
		old, present := os.LookupEnv(key)
		_ = os.Unsetenv(key)
		t.Cleanup(func() {
			if present {
				_ = os.Setenv(key, old)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
	cfg, err := LoadEmailAdminVerifyBootstrapConfig()
	if err != nil || cfg.Enabled {
		t.Fatalf("enabled 键缺失必须默认关闭: %#v %v", cfg, err)
	}

	for _, invalid := range []string{"", "1", "yes", " true ", "on"} {
		t.Run("拒绝_"+invalid, func(t *testing.T) {
			t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED", invalid)
			if _, err := LoadEmailAdminVerifyBootstrapConfig(); err == nil {
				t.Fatalf("显式非法 enabled 必须启动失败: %q", invalid)
			}
		})
	}
	t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED", "TrUe")
	t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN", "bootstrap-"+strings.Repeat("ab", 16))
	t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS", "192.0.2.0/24")
	t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS", "198.51.100.10")
	cfg, err = LoadEmailAdminVerifyBootstrapConfig()
	if err != nil || !cfg.Enabled || len(cfg.AllowedIPs) != 1 || len(cfg.TrustedProxyIPs) != 1 {
		t.Fatalf("合法 bootstrap 配置解析失败: %#v %v", cfg, err)
	}
}

func TestEmailAdminVerifyBootstrapRequiresIndependentStrongValues(t *testing.T) {
	t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED", "true")
	t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS", "192.0.2.1")
	t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS", "198.51.100.1")
	for _, token := range []string{
		"", "short", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "change-me-change-me-change-me-change-me",
		strings.Repeat("default", 5), strings.Repeat("secret", 6), strings.Repeat("test", 8), strings.Repeat("ab", 16),
	} {
		t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN", token)
		if _, err := LoadEmailAdminVerifyBootstrapConfig(); err == nil {
			t.Fatalf("弱 Token 必须启动失败: %q", token)
		}
	}
	t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN", "bootstrap-"+strings.Repeat("ab", 16))
	t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS", "")
	if _, err := LoadEmailAdminVerifyBootstrapConfig(); err == nil {
		t.Fatal("enabled=true 时 allowed IP 显式空值必须启动失败")
	}
}

func TestEmailBootstrapRejectsFullAddressFamilyCIDRs(t *testing.T) {
	setValidEmailBootstrapEnvironment(t)
	for _, tc := range []struct {
		key   string
		value string
	}{
		{key: "EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS", value: "0.0.0.0/0"},
		{key: "EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS", value: "192.0.2.1/0"},
		{key: "EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS", value: "0.0.0.0/1,128.0.0.0/1"},
		{key: "EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS", value: "::/0"},
		{key: "EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS", value: "2001:db8::1/0"},
		{key: "EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS", value: "::/1,8000::/1"},
	} {
		t.Run(tc.key+"_"+tc.value, func(t *testing.T) {
			setValidEmailBootstrapEnvironment(t)
			t.Setenv(tc.key, tc.value)
			if _, err := LoadEmailAdminVerifyBootstrapConfig(); err == nil {
				t.Fatalf("覆盖完整地址空间的配置必须启动失败: %s=%s", tc.key, tc.value)
			}
		})
	}
}

func TestPrefixesCoverAddressFamilyUsesBoundedCandidateTree(t *testing.T) {
	tests := []struct {
		name     string
		ipv4     bool
		prefixes []netip.Prefix
		want     bool
	}{
		{name: "IPv4 单地址", ipv4: true, prefixes: mustPrefixes("192.0.2.1/32")},
		{name: "IPv4 子网", ipv4: true, prefixes: mustPrefixes("192.0.2.0/24")},
		{name: "IPv4 两半完整覆盖", ipv4: true, prefixes: mustPrefixes("0.0.0.0/1", "128.0.0.0/1"), want: true},
		{name: "IPv4 上半区存在缺口", ipv4: true, prefixes: mustPrefixes("0.0.0.0/1", "128.0.0.0/2")},
		{name: "IPv4 首尾边界不能填补中间缺口", ipv4: true, prefixes: mustPrefixes("0.0.0.0/32", "255.255.255.255/32")},
		{name: "IPv6 单地址", prefixes: mustPrefixes("2001:db8::1/128")},
		{name: "IPv6 子网", prefixes: mustPrefixes("2001:db8::/32")},
		{name: "IPv6 两半完整覆盖", prefixes: mustPrefixes("::/1", "8000::/1"), want: true},
		{name: "IPv6 下半区存在缺口", prefixes: mustPrefixes("::/2", "8000::/1")},
		{name: "IPv6 首尾边界不能填补中间缺口", prefixes: mustPrefixes("::/128", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/128")},
		{name: "地址族隔离", ipv4: true, prefixes: mustPrefixes("::/0")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := prefixesCoverAddressFamily(tc.prefixes, tc.ipv4); got != tc.want {
				t.Fatalf("地址空间覆盖判断错误: got=%t want=%t", got, tc.want)
			}
		})
	}
}

func TestPrefixCoverageCandidateIndexHasLinearDepthBound(t *testing.T) {
	prefixes := make([]netip.Prefix, 0, 256)
	for i := 0; i < 256; i++ {
		bytes := [16]byte{0x20, 0x01, 0x0d, 0xb8}
		bytes[14], bytes[15] = byte(i>>8), byte(i)
		prefixes = append(prefixes, netip.PrefixFrom(netip.AddrFrom16(bytes), 128))
	}
	exact, candidates := buildPrefixCoverageIndex(prefixes, false)
	if len(exact) != len(prefixes) {
		t.Fatalf("精确前缀索引数量错误: got=%d want=%d", len(exact), len(prefixes))
	}
	// 每个 IPv6 候选最多贡献自身和 128 个祖先，索引规模因此受输入数乘地址位数约束。
	if upper := len(prefixes) * 129; len(candidates) > upper {
		t.Fatalf("候选树索引超过线性深度上界: got=%d upper=%d", len(candidates), upper)
	}
	if prefixesCoverAddressFamily(prefixes, false) {
		t.Fatal("稀疏 IPv6 单地址集合不得误判为完整覆盖")
	}
}

func mustPrefixes(values ...string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefixes = append(prefixes, netip.MustParsePrefix(value))
	}
	return prefixes
}

func TestEmailBootstrapRejectsReusedSecurityConfiguration(t *testing.T) {
	setValidEmailBootstrapEnvironment(t)
	t.Setenv("INTERNAL_API_TOKEN", "bootstrap-"+strings.Repeat("ab", 16))
	if _, err := LoadEmailAdminVerifyBootstrapConfig(); err == nil {
		t.Fatal("bootstrap Token 不得复用 INTERNAL_API_TOKEN")
	}

	t.Run("allowed 与 trusted 不得复用等价网段", func(t *testing.T) {
		setValidEmailBootstrapEnvironment(t)
		t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS", "192.0.2.7/24")
		if _, err := LoadEmailAdminVerifyBootstrapConfig(); err == nil {
			t.Fatal("allowed 与 trusted 的等价网段必须启动失败")
		}
	})
}

func TestEmailBootstrapAllowsExplicitReuseOfExistingCIDRs(t *testing.T) {
	setValidEmailBootstrapEnvironment(t)
	// 独立环境变量可以显式声明相同网段；独立性不等于必须使用不同的地址值。
	t.Setenv("INTERNAL_ALLOWED_IPS", "192.0.2.0/24")
	t.Setenv("INTERNAL_TRUSTED_PROXY_IPS", "198.51.100.10/32")
	t.Setenv("TRUSTED_PROXY_IPS", "192.0.2.7/24")
	if _, err := LoadEmailAdminVerifyBootstrapConfig(); err != nil {
		t.Fatalf("已有 CIDR 的显式同值复用应允许: %v", err)
	}
}

func TestAdminVerifyExpireHoursRejectsNegativeValue(t *testing.T) {
	t.Setenv("ADMIN_VERIFY_EXPIRE_HOURS", "-1")
	cfg := Load()
	if err := cfg.ValidateAdminVerifyConfig(); err == nil {
		t.Fatal("负管理员认证有效期必须配置校验失败")
	}
}

func setValidEmailBootstrapEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED", "true")
	t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN", "bootstrap-"+strings.Repeat("ab", 16))
	t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS", "192.0.2.0/24")
	t.Setenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS", "198.51.100.10")
	t.Setenv("INTERNAL_API_TOKEN", "")
	t.Setenv("INTERNAL_ALLOWED_IPS", "")
	t.Setenv("INTERNAL_TRUSTED_PROXY_IPS", "")
	t.Setenv("TRUSTED_PROXY_IPS", "")
}

func TestTrustedProxyIPsStrictStartupParsing(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantLen int
		wantErr bool
	}{
		{name: "空值为直连模式", raw: "", wantLen: 0},
		{name: "纯空白为直连模式", raw: "   ", wantLen: 0},
		{name: "精确地址与网段", raw: " 192.0.2.10 , 198.51.100.0/24 , 2001:db8::1 , 2001:db8:1::/48 ", wantLen: 4},
		{name: "空项", raw: "192.0.2.10,,198.51.100.1", wantErr: true},
		{name: "非法地址", raw: "192.0.2.999", wantErr: true},
		{name: "非法网段", raw: "192.0.2.0/99", wantErr: true},
		{name: "IPv6 zone", raw: "fe80::1%eth0", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			networks, err := ParseTrustedProxyIPs(tc.raw)
			if (err != nil) != tc.wantErr || (!tc.wantErr && len(networks) != tc.wantLen) {
				t.Fatalf("严格解析结果错误: len=%d err=%v", len(networks), err)
			}
		})
	}

	t.Setenv("TRUSTED_PROXY_IPS", "")
	t.Setenv("INTERNAL_TRUSTED_PROXY_IPS", "203.0.113.10")
	loaded := Load()
	if loaded.TrustedProxyIPs != "" || loaded.InternalTrustedProxyIPs == "" {
		t.Fatal("公开可信代理配置不得回退或复用 metrics 独立配置")
	}
	networks, err := ParseTrustedProxyIPs("192.0.2.7/24")
	if err != nil || len(networks) != 1 || networks[0] != netip.MustParsePrefix("192.0.2.0/24") {
		t.Fatalf("CIDR 必须规范化保存: %#v %v", networks, err)
	}
}
