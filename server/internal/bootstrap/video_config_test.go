package bootstrap

import (
	"strings"
	"testing"
)

func TestVideoG7NewAppRejectsInvalidSwitchBeforeDependencies(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("SMS_ENABLED", "false")
	t.Setenv("IMAGE_GATEWAY_ENABLED", "false")
	t.Setenv("IMAGE_GATEWAY_TRAFFIC_ENABLED", "false")
	t.Setenv("IMAGE_GATEWAY_OPENROUTER_ENABLED", "false")
	t.Setenv("VIDEO_GATEWAY_ENABLED", "not-a-valid-switch")
	// 后置代理校验故意无效：即使视频校验尚未接线，红灯测试也不会连接基础设施或触发密钥fatal。
	t.Setenv("TRUSTED_PROXY_IPS", "invalid,,proxy")
	app, err := NewApp()
	if app != nil || err == nil || !strings.Contains(err.Error(), "视频网关配置无效") {
		t.Fatalf("非法视频开关必须在依赖初始化前拒绝：err=%v", err)
	}
	if strings.Contains(err.Error(), "not-a-valid-switch") {
		t.Fatal("启动错误不得泄漏配置原值")
	}
}

func TestVideoG7NewAppRejectsMissingSecretsBeforeDependencies(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("SMS_ENABLED", "false")
	t.Setenv("IMAGE_GATEWAY_ENABLED", "false")
	t.Setenv("IMAGE_GATEWAY_TRAFFIC_ENABLED", "false")
	t.Setenv("IMAGE_GATEWAY_OPENROUTER_ENABLED", "false")
	t.Setenv("VIDEO_GATEWAY_ENABLED", "true")
	t.Setenv("VIDEO_GATEWAY_TRAFFIC_ENABLED", "false")
	t.Setenv("REAL_PROVIDER", "false")
	t.Setenv("VIDEO_GATEWAY_LOCAL_FAKE_TEST", "false")
	t.Setenv("VIDEO_EXECUTION_DRIVER", "native_async")
	t.Setenv("VIDEO_GATEWAY_REPOSITORY_ROOT", "")
	t.Setenv("TRUSTED_PROXY_IPS", "invalid,,proxy")
	app, err := NewApp()
	if app != nil || err == nil || !strings.Contains(err.Error(), "视频网关配置无效") {
		t.Fatalf("启用视频时缺少凭据边界必须先拒绝启动：err=%v", err)
	}
}
