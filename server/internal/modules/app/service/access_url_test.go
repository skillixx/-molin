package service

import "testing"

// TestValidateAccessURL 覆盖 access_url 校验：仅放行空串与合法 https，拒绝危险/不安全 scheme。
func TestValidateAccessURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"空串放行(清空入口)", "", false},
		{"空白串放行", "   ", false},
		{"合法https", "https://ppt.example.com", false},
		{"合法https带路径", "https://ppt.example.com/app?x=1", false},
		{"大小写HTTPS放行", "HTTPS://ppt.example.com", false},
		{"http被拒", "http://ppt.example.com", true},
		{"javascript被拒(XSS)", "javascript:alert(1)", true},
		{"data被拒", "data:text/html,<script>1</script>", true},
		{"缺域名被拒", "https://", true},
		{"非法格式被拒", "https://例子 空格", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateAccessURL(c.raw)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateAccessURL(%q) err=%v, wantErr=%v", c.raw, err, c.wantErr)
			}
		})
	}
}

// TestNormalizeAccessURL 归一化：去首尾空白；纯空白归一为空串（视为未配置）。
func TestNormalizeAccessURL(t *testing.T) {
	cases := map[string]string{
		"   ":                        "",
		"":                           "",
		"  https://a.com  ":          "https://a.com",
		"https://a.com":              "https://a.com",
	}
	for in, want := range cases {
		if got := normalizeAccessURL(in); got != want {
			t.Fatalf("normalizeAccessURL(%q)=%q, want %q", in, got, want)
		}
	}
}

// TestValidateAccessURL_TooLong 超长校验。
func TestValidateAccessURL_TooLong(t *testing.T) {
	long := "https://example.com/"
	for len(long) <= accessURLMaxLen {
		long += "a"
	}
	if err := validateAccessURL(long); err == nil {
		t.Fatalf("超长 access_url 应被拒，got nil")
	}
}
