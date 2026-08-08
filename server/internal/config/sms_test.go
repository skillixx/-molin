package config

import (
	"strings"
	"testing"
)

func TestLoadSMSDefaultsToDisabled(t *testing.T) {
	t.Setenv("SMS_ENABLED", "")
	t.Setenv("SMS_PROVIDER", "")

	cfg := Load()
	if cfg.SMSEnabled {
		t.Fatal("短信功能必须默认关闭")
	}
}

func TestLoadParsesTestSceneAllowlist(t *testing.T) {
	t.Setenv("SMS_TEST_SCENE_ALLOWLIST", "login, reset_password")

	cfg := Load()
	if len(cfg.SMSTestSceneAllowlist) != 2 || cfg.SMSTestSceneAllowlist[0] != "login" || cfg.SMSTestSceneAllowlist[1] != "reset_password" {
		t.Fatalf("测试场景白名单解析错误: %#v", cfg.SMSTestSceneAllowlist)
	}
}

func TestValidateSMSFailsClosedWhenRequiredConfigMissing(t *testing.T) {
	cfg := Config{SMSEnabled: true, SMSProvider: "aliyun"}

	err := cfg.ValidateSMS()
	if err == nil {
		t.Fatal("短信开启但必要配置缺失时必须拒绝")
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatal("配置错误不得泄露密钥值")
	}
}

func TestValidateSMSAcceptsCompleteAliyunConfig(t *testing.T) {
	cfg := Config{
		SMSEnabled:               true,
		SMSProvider:              "aliyun",
		SMSAliyunAccessKeyID:     "test-access-key-id",
		SMSAliyunAccessKeySecret: "secret-value",
		SMSAliyunSignName:        "test-sign",
		SMSAliyunEndpoint:        "dysmsapi.aliyuncs.com",
		SMSPhoneHMACSecret:       strings.Repeat("x", 32),
	}

	if err := cfg.ValidateSMS(); err != nil {
		t.Fatalf("完整阿里云短信配置应通过校验: %v", err)
	}
}

func TestValidateSMSRejectsLegacyKeysWithoutReadingTheirValues(t *testing.T) {
	t.Setenv("SMS_ENABLED", "true")
	t.Setenv("SMS_PROVIDER", "aliyun")
	t.Setenv("SMS_ALIYUN_ACCESS_KEY_ID", "test-access-key-id")
	t.Setenv("SMS_ALIYUN_ACCESS_KEY_SECRET", "secret-value")
	t.Setenv("SMS_ALIYUN_SIGN_NAME", "test-sign")
	t.Setenv("SMS_PHONE_HMAC_SECRET", strings.Repeat("x", 32))
	t.Setenv("SMS_TEST_MODE", "false")
	t.Setenv("SMS_ACCESS_KEY", "legacy-private-value")

	cfg := Load()
	err := cfg.ValidateSMS()
	if err == nil || !strings.Contains(err.Error(), "旧短信配置键") {
		t.Fatalf("检测到旧短信键时必须 fail-closed，实际 %v", err)
	}
	if strings.Contains(err.Error(), "legacy-private-value") {
		t.Fatal("旧配置检查不得传播私密值")
	}
}

func TestValidateSMSRequiresWhitelistInTestMode(t *testing.T) {
	cfg := Config{
		SMSEnabled:               true,
		SMSProvider:              "aliyun",
		SMSAliyunAccessKeyID:     "test-access-key-id",
		SMSAliyunAccessKeySecret: "secret-value",
		SMSAliyunSignName:        "test-sign",
		SMSAliyunEndpoint:        "dysmsapi.aliyuncs.com",
		SMSPhoneHMACSecret:       strings.Repeat("x", 32),
		SMSTestMode:              true,
	}

	if err := cfg.ValidateSMS(); err == nil {
		t.Fatal("测试模式白名单为空时必须拒绝启动")
	}
}

func TestValidateSMSRequiresSceneAllowlistInTestMode(t *testing.T) {
	cfg := completeTestSMSConfig()
	cfg.SMSTestSceneAllowlist = nil

	err := cfg.ValidateSMS()
	if err == nil || !strings.Contains(err.Error(), "SMS_TEST_SCENE_ALLOWLIST") {
		t.Fatalf("测试模式场景白名单为空时必须拒绝启动，实际 %v", err)
	}
}

func TestValidateSMSRejectsUnknownTestScene(t *testing.T) {
	cfg := completeTestSMSConfig()
	cfg.SMSTestSceneAllowlist = []string{"login", "marketing"}

	err := cfg.ValidateSMS()
	if err == nil || !strings.Contains(err.Error(), "SMS_TEST_SCENE_ALLOWLIST") {
		t.Fatalf("测试模式场景白名单包含未知场景时必须拒绝启动，实际 %v", err)
	}
}

func TestValidateSMSRejectsDuplicateTestScene(t *testing.T) {
	cfg := completeTestSMSConfig()
	cfg.SMSTestSceneAllowlist = []string{"login", "login"}

	err := cfg.ValidateSMS()
	if err == nil || !strings.Contains(err.Error(), "重复场景") {
		t.Fatalf("测试模式场景白名单包含重复场景时必须拒绝启动，实际 %v", err)
	}
}

func TestValidateSMSAcceptsLoginOnlyTestScene(t *testing.T) {
	cfg := completeTestSMSConfig()
	cfg.SMSTestSceneAllowlist = []string{"login"}

	if err := cfg.ValidateSMS(); err != nil {
		t.Fatalf("仅放行登录场景的测试配置应通过校验: %v", err)
	}
}

func completeTestSMSConfig() Config {
	return Config{
		SMSEnabled:               true,
		SMSProvider:              "aliyun",
		SMSAliyunAccessKeyID:     "test-access-key-id",
		SMSAliyunAccessKeySecret: "secret-value",
		SMSAliyunSignName:        "test-sign",
		SMSAliyunEndpoint:        "dysmsapi.aliyuncs.com",
		SMSPhoneHMACSecret:       strings.Repeat("x", 32),
		SMSTestMode:              true,
		SMSTestPhoneWhitelist:    []string{"phone-test-value"},
	}
}
