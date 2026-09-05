package config

import (
	"os"
	"strings"
	"testing"
)

func TestVideoG7ConfigLoadsTenIndependentSecretsLinux(t *testing.T) {
	p := videoSecretTestPaths(t)
	if err := os.Mkdir(p.RepositoryRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	files := p.files()
	for i, spec := range files {
		value := strings.Repeat(string(rune('a'+i)), 32)
		if err := os.WriteFile(spec.Path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{AppEnv: "test", VideoGateway: VideoGatewayConfig{
		Enabled:         true,
		ExecutionDriver: "native_async",
		Secrets:         p,
		// Linux专属凭据测试也必须提供完整低敏基础设施，避免绕过生产装配校验。
		Infrastructure: VideoInfrastructureConfig{
			MinIOEndpoint:             "minio:9000",
			MinIOPublicUploadEndpoint: "http://127.0.0.1:9000",
			FakeProviderEndpoint:      "http://127.0.0.1:18080",
			RabbitEndpoint:            "rabbitmq:5672",
			RabbitUser:                "molin_video",
			RabbitVHost:               "/molin-video",
			RedisAddr:                 "redis:6379",
			WorkerID:                  "video-config-test",
		},
	}}
	bundle, err := cfg.LoadVideoSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 10 {
		t.Fatal("G7多进程容量运行时需要十类独立凭据")
	}
	for i, spec := range files {
		value, ok := bundle.Bytes(spec.Purpose)
		if !ok || string(value) != strings.Repeat(string(rune('a'+i)), 32) {
			t.Fatal("用途对应的凭据未正确加载")
		}
	}
	if err := os.WriteFile(p.CapacityNonce, []byte(strings.Repeat("z", 31)), 0o600); err != nil {
		t.Fatal(err)
	}
	if partial, err := cfg.LoadVideoSecrets(); partial != nil || err == nil {
		t.Fatal("容量HMAC密钥必须精确32字节")
	}
	if err := os.WriteFile(p.CapacityNonce, []byte(strings.Repeat("z", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.RedisPassword, []byte(strings.Repeat("z", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	if partial, err := cfg.LoadVideoSecrets(); partial != nil || err == nil {
		t.Fatal("容量HMAC密钥不得与Redis密码同值复用")
	}
	if err := os.WriteFile(p.RedisPassword, []byte(strings.Repeat("i", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Download, []byte(strings.Repeat("c", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	if partial, err := cfg.LoadVideoSecrets(); partial != nil || err == nil {
		t.Fatal("下载签名与回调同值必须整包拒绝")
	}
	if err := os.Remove(p.Payload); err != nil {
		t.Fatal(err)
	}
	if partial, err := cfg.LoadVideoSecrets(); partial != nil || err == nil {
		t.Fatal("必需凭据缺失不能降级或返回部分包")
	}
	// 关闭模块后即使存在错误或缺失引用，也不得再次读取文件。
	cfg.VideoGateway.Enabled = false
	if off, err := cfg.LoadVideoSecrets(); off != nil || err != nil {
		t.Fatal("模块关闭必须跳过文件读取")
	}
}
