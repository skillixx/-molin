package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func videoSecretTestPaths(t *testing.T) VideoSecretPaths {
	t.Helper()
	dir := t.TempDir()
	return VideoSecretPaths{
		RepositoryRoot: filepath.Join(dir, "repo"), Quote: filepath.Join(dir, "quote"), Payload: filepath.Join(dir, "payload"),
		Callback: filepath.Join(dir, "callback"), AdminReason: filepath.Join(dir, "admin"), Download: filepath.Join(dir, "download"),
		MinIOAccess: filepath.Join(dir, "access"), MinIOSecret: filepath.Join(dir, "secret"),
		RabbitPassword: filepath.Join(dir, "rabbit"), RedisPassword: filepath.Join(dir, "redis"),
		CapacityNonce: filepath.Join(dir, "capacity"),
	}
}

func videoSecretTestEnvironment(t *testing.T) map[string]string {
	t.Helper()
	p := videoSecretTestPaths(t)
	return map[string]string{
		"VIDEO_GATEWAY_REPOSITORY_ROOT": p.RepositoryRoot, "VIDEO_GATEWAY_QUOTE_SECRET_FILE": p.Quote,
		"VIDEO_GATEWAY_PAYLOAD_SECRET_FILE": p.Payload, "VIDEO_GATEWAY_CALLBACK_SECRET_FILE": p.Callback,
		"VIDEO_GATEWAY_ADMIN_REASON_SECRET_FILE": p.AdminReason, "VIDEO_GATEWAY_DOWNLOAD_SECRET_FILE": p.Download,
		"VIDEO_GATEWAY_MINIO_ACCESS_KEY_FILE": p.MinIOAccess, "VIDEO_GATEWAY_MINIO_SECRET_KEY_FILE": p.MinIOSecret,
		"VIDEO_GATEWAY_RABBIT_PASSWORD_FILE": p.RabbitPassword, "VIDEO_GATEWAY_REDIS_PASSWORD_FILE": p.RedisPassword,
		"VIDEO_GATEWAY_CAPACITY_SECRET_FILE": p.CapacityNonce,
	}
}

func videoInfrastructureTestConfig() VideoInfrastructureConfig {
	return VideoInfrastructureConfig{MinIOEndpoint: "minio:9000", MinIOPublicUploadEndpoint: "http://127.0.0.1:19000", FakeProviderEndpoint: "http://127.0.0.1:18080", RabbitEndpoint: "rabbit:5672", RabbitUser: "video-worker", RabbitVHost: "/", RedisAddr: "redis:6379", RedisDB: 3, WorkerID: "video-worker-1"}
}

func videoInfrastructureTestEnvironment() map[string]string {
	return map[string]string{"VIDEO_GATEWAY_MINIO_ENDPOINT": "minio:9000", "VIDEO_GATEWAY_MINIO_PUBLIC_UPLOAD_ENDPOINT": "http://127.0.0.1:19000", "VIDEO_GATEWAY_MINIO_USE_SSL": "false", "VIDEO_GATEWAY_FAKE_PROVIDER_ENDPOINT": "http://127.0.0.1:18080", "VIDEO_GATEWAY_RABBIT_ENDPOINT": "rabbit:5672", "VIDEO_GATEWAY_RABBIT_USER": "video-worker", "VIDEO_GATEWAY_RABBIT_VHOST": "/", "VIDEO_GATEWAY_RABBIT_TLS": "false", "VIDEO_GATEWAY_REDIS_ADDR": "redis:6379", "VIDEO_GATEWAY_REDIS_DB": "3", "VIDEO_GATEWAY_WORKER_ID": "video-worker-1"}
}

func TestVideoG7EnabledRequiresSecretReferences(t *testing.T) {
	cfg := Config{AppEnv: "test", VideoGateway: VideoGatewayConfig{Enabled: true, ExecutionDriver: "native_async", Infrastructure: videoInfrastructureTestConfig()}}
	if err := cfg.ValidateVideoGatewayConfig(); err == nil {
		t.Fatal("启用模块即使流量关闭也不能遗漏恢复/回调所需凭据配置")
	}
}

func TestVideoG7ConfigDefaultClosed(t *testing.T) {
	// 空环境必须保持三层关闭，并且视频配置加载不得查询文字模型的Bifrost配置或真实Key。
	cfg := LoadVideoGatewayConfig(func(key string) (string, bool) {
		if key == "BIFROST_BASE_URL" || key == "BIFROST_INTERNAL_TOKEN" || key == "VIDEO_PROVIDER_KEY" {
			t.Fatalf("关闭态不应读取执行凭据：%s", key)
		}
		return "", false
	})
	if cfg.Enabled || cfg.TrafficEnabled || cfg.RealProvider || cfg.LocalFakeTest {
		t.Fatal("默认配置不能开启模块、流量、真实Provider或Fake测试许可")
	}
	if cfg.ExecutionDriver != "native_async" {
		t.Fatal("默认驱动必须遵守G0的native_async裁决")
	}
	if err := (Config{VideoGateway: cfg}).ValidateVideoGatewayConfig(); err != nil {
		t.Fatalf("关闭态不应要求外部依赖：%v", err)
	}
}

func TestVideoG7ConfigSwitchMatrix(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, traffic := range []bool{false, true} {
			for _, real := range []bool{false, true} {
				t.Run(fmt.Sprintf("module_%t_traffic_%t_real_%t", enabled, traffic, real), func(t *testing.T) {
					cfg := Config{AppEnv: "test", VideoGateway: VideoGatewayConfig{
						Enabled: enabled, TrafficEnabled: traffic, RealProvider: real,
						ExecutionDriver: "native_async", LocalFakeTest: enabled, Infrastructure: videoInfrastructureTestConfig(),
					}}
					cfg.VideoGateway.Secrets = videoSecretTestPaths(t)
					wantError := real || (!enabled && traffic)
					if err := cfg.ValidateVideoGatewayConfig(); (err != nil) != wantError {
						t.Fatalf("开关组合校验不符合三层关闭合同：err=%v", err)
					}
				})
			}
		}
	}
}

func TestVideoG7ConfigExplicitFakeAndDriver(t *testing.T) {
	for _, tc := range []struct {
		name, env, driver     string
		fake, traffic, reject bool
	}{
		{"显式测试Fake", "test", "native_async", true, true, false},
		{"未批准Fake", "test", "native_async", false, true, true},
		{"生产不能Fake", "production", "native_async", true, true, true},
		{"本地默认不能Fake", "local", "native_async", true, true, true},
		{"未知环境不能Fake", "unknown", "native_async", true, true, true},
		{"关闭流量不等于批准Fake", "production", "native_async", true, false, true},
		{"生产关闭态", "production", "native_async", false, false, false},
		{"Bifrost不可启用", "test", "bifrost", true, true, true},
		{"未知驱动不可回退", "test", "missing", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{AppEnv: tc.env, VideoGateway: VideoGatewayConfig{
				Enabled: true, TrafficEnabled: tc.traffic, LocalFakeTest: tc.fake, ExecutionDriver: tc.driver, Infrastructure: videoInfrastructureTestConfig(),
			}}
			cfg.VideoGateway.Secrets = videoSecretTestPaths(t)
			if err := cfg.ValidateVideoGatewayConfig(); (err != nil) != tc.reject {
				t.Fatalf("驱动/Fake环境配置校验错误：%v", err)
			}
		})
	}
}

func TestVideoG7ConfigStrictEnvironment(t *testing.T) {
	for _, name := range []string{"VIDEO_GATEWAY_ENABLED", "VIDEO_GATEWAY_TRAFFIC_ENABLED", "REAL_PROVIDER", "VIDEO_GATEWAY_LOCAL_FAKE_TEST"} {
		for _, raw := range []string{"", "TRUE", "1", "yes", "invalid-sensitive-value"} {
			t.Run(name+"_"+raw, func(t *testing.T) {
				v := LoadVideoGatewayConfig(func(key string) (string, bool) { return raw, key == name })
				err := (Config{VideoGateway: v}).ValidateVideoGatewayConfig()
				if err == nil || !strings.Contains(err.Error(), name) {
					t.Fatal("显式非法开关不得退回默认值后通过校验")
				}
				if strings.Contains(err.Error(), "invalid-sensitive-value") {
					t.Fatal("配置错误不得回显原始环境值")
				}
			})
		}
	}
	if err := (Config{VideoGateway: LoadVideoGatewayConfig(nil)}).ValidateVideoGatewayConfig(); err == nil {
		t.Fatal("环境读取依赖缺失必须失败关闭")
	}
}

func TestVideoG7ConfigLoadWiresIndependentVideoFlags(t *testing.T) {
	values := map[string]string{
		"APP_ENV": "test", "VIDEO_GATEWAY_ENABLED": "true",
		"VIDEO_GATEWAY_TRAFFIC_ENABLED": "true", "REAL_PROVIDER": "false",
		"VIDEO_GATEWAY_LOCAL_FAKE_TEST": "true", "VIDEO_EXECUTION_DRIVER": "native_async",
		"TOKEN_EXECUTION_DRIVER": "bifrost",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	for key, value := range videoSecretTestEnvironment(t) {
		t.Setenv(key, value)
	}
	for key, value := range videoInfrastructureTestEnvironment() {
		t.Setenv(key, value)
	}
	cfg := Load()
	if !cfg.VideoGateway.Enabled || !cfg.VideoGateway.TrafficEnabled || !cfg.VideoGateway.LocalFakeTest || cfg.VideoGateway.RealProvider {
		t.Fatal("主配置加载必须保留独立的视频开关，不受Chat驱动影响")
	}
	if err := cfg.ValidateVideoGatewayConfig(); err != nil {
		t.Fatalf("显式隔离Fake配置应通过开关校验：%v", err)
	}
	// 关闭态也必须严格解析显式false，避免字符串false被当作真值。
	for _, key := range []string{"VIDEO_GATEWAY_ENABLED", "VIDEO_GATEWAY_TRAFFIC_ENABLED", "VIDEO_GATEWAY_LOCAL_FAKE_TEST"} {
		t.Setenv(key, "false")
	}
	closed := Load()
	if closed.VideoGateway.Enabled || closed.VideoGateway.TrafficEnabled || closed.VideoGateway.LocalFakeTest {
		t.Fatal("显式false必须维持视频关闭")
	}
}

func TestVideoG7ConfigDisabledSkipsSecretReferencesAndFiles(t *testing.T) {
	v := LoadVideoGatewayConfig(func(key string) (string, bool) {
		if strings.HasSuffix(key, "_FILE") || key == "VIDEO_GATEWAY_REPOSITORY_ROOT" {
			t.Fatal("关闭模块不能读取凭据文件引用")
		}
		return "", false
	})
	v.Secrets = VideoSecretPaths{RepositoryRoot: "invalid-root", Payload: "invalid-file"}
	bundle, err := (Config{VideoGateway: v}).LoadVideoSecrets()
	if bundle != nil || err != nil {
		t.Fatal("关闭模块不能触发任何文件加载，包括非Linux平台的失败分支")
	}
}

func TestVideoG7ConfigSecretReferenceMatrix(t *testing.T) {
	base := videoSecretTestEnvironment(t)
	infra := videoInfrastructureTestEnvironment()
	for key := range base {
		for _, replacement := range []string{"", "relative-secret", filepath.Join(base["VIDEO_GATEWAY_REPOSITORY_ROOT"], "MUST_NOT_LEAK")} {
			// 另一个绝对仓库根是可信部署边界变化，不伪装成纯引用校验的否定用例。
			if key == "VIDEO_GATEWAY_REPOSITORY_ROOT" && filepath.IsAbs(replacement) {
				continue
			}
			t.Run(key+"_"+replacement, func(t *testing.T) {
				v := LoadVideoGatewayConfig(func(name string) (string, bool) {
					if name == "VIDEO_GATEWAY_ENABLED" {
						return "true", true
					}
					if name == key {
						return replacement, true
					}
					value, ok := base[name]
					if !ok {
						value, ok = infra[name]
					}
					return value, ok
				})
				err := (Config{AppEnv: "test", VideoGateway: v}).ValidateVideoGatewayConfig()
				if err == nil {
					t.Fatal("遗漏、相对或仓库内凭据路径必须拒绝")
				}
				if strings.Contains(err.Error(), "MUST_NOT_LEAK") {
					t.Fatal("配置错误不能回显内部路径")
				}
			})
		}
	}
	p := videoSecretTestPaths(t)
	p.Download = p.Callback
	if err := (Config{VideoGateway: VideoGatewayConfig{Enabled: true, ExecutionDriver: "native_async", Secrets: p, Infrastructure: videoInfrastructureTestConfig()}}).ValidateVideoGatewayConfig(); err == nil {
		t.Fatal("下载和回调凭据文件不能复用")
	}
}

func TestVideoG7InfrastructureConfigurationFailsClosed(t *testing.T) {
	base := VideoGatewayConfig{Enabled: true, ExecutionDriver: "native_async", Secrets: videoSecretTestPaths(t), Infrastructure: videoInfrastructureTestConfig()}
	mutations := []struct {
		name   string
		mutate func(*VideoInfrastructureConfig)
	}{
		{"MinIO缺失", func(v *VideoInfrastructureConfig) { v.MinIOEndpoint = "" }},
		{"MinIO包含协议", func(v *VideoInfrastructureConfig) { v.MinIOEndpoint = "http://minio:9000" }},
		{"公开明文非回环", func(v *VideoInfrastructureConfig) { v.MinIOPublicUploadEndpoint = "http://10.0.0.1:9000" }},
		{"公开入口带路径", func(v *VideoInfrastructureConfig) { v.MinIOPublicUploadEndpoint = "https://media.example.com/private" }},
		{"Fake Provider非回环", func(v *VideoInfrastructureConfig) { v.FakeProviderEndpoint = "https://api.runware.ai" }},
		{"Rabbit缺失", func(v *VideoInfrastructureConfig) { v.RabbitEndpoint = "" }},
		{"Rabbit用户注入", func(v *VideoInfrastructureConfig) { v.RabbitUser = "user:name" }},
		{"Rabbit虚拟主机注入", func(v *VideoInfrastructureConfig) { v.RabbitVHost = "/video?x=1" }},
		{"Redis缺失", func(v *VideoInfrastructureConfig) { v.RedisAddr = "" }},
		{"Worker非法", func(v *VideoInfrastructureConfig) { v.WorkerID = "../../worker" }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			tc.mutate(&candidate.Infrastructure)
			if err := (Config{AppEnv: "test", VideoGateway: candidate}).ValidateVideoGatewayConfig(); err == nil {
				t.Fatal("不安全基础设施配置必须拒绝")
			}
		})
	}
}

func TestVideoG7InfrastructureEnvironmentIsStrict(t *testing.T) {
	base := videoSecretTestEnvironment(t)
	for key, value := range videoInfrastructureTestEnvironment() {
		base[key] = value
	}
	base["VIDEO_GATEWAY_ENABLED"] = "true"
	for _, tc := range []struct{ key, value string }{
		{"VIDEO_GATEWAY_MINIO_USE_SSL", "TRUE"},
		{"VIDEO_GATEWAY_RABBIT_TLS", "1"},
		{"VIDEO_GATEWAY_REDIS_DB", "16"},
		{"VIDEO_GATEWAY_REDIS_DB", "not-a-number"},
	} {
		t.Run(tc.key+"_"+tc.value, func(t *testing.T) {
			v := LoadVideoGatewayConfig(func(name string) (string, bool) {
				if name == tc.key {
					return tc.value, true
				}
				value, ok := base[name]
				return value, ok
			})
			err := (Config{AppEnv: "test", VideoGateway: v}).ValidateVideoGatewayConfig()
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("非法基础设施环境值必须以字段名失败关闭: %v", err)
			}
		})
	}
}
