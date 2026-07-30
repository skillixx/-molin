package bootstrap

import (
	"strings"
	"testing"
)

func TestNewAppRejectsInvalidTrustedProxyIPsBeforeDependencies(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_IPS", "192.0.2.10,,198.51.100.1")
	app, err := NewApp()
	if err == nil || app != nil || !strings.Contains(err.Error(), "TRUSTED_PROXY_IPS 配置无效") {
		t.Fatalf("非法公开可信代理配置必须在依赖初始化前阻断启动: app=%v err=%v", app, err)
	}
}

func TestNewAppRejectsNegativeAdminVerifyExpireHoursBeforeDependencies(t *testing.T) {
	t.Setenv("ADMIN_VERIFY_EXPIRE_HOURS", "-1")
	app, err := NewApp()
	if err == nil || app != nil || !strings.Contains(err.Error(), "ADMIN_VERIFY_EXPIRE_HOURS") {
		t.Fatalf("负管理员认证有效期必须在依赖初始化前阻断启动: app=%v err=%v", app, err)
	}
}
