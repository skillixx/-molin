package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"molin/server/internal/config/videosecrets"
)

// VideoGatewayConfig 将视频模块、业务流量与真实Provider的许可分离。
// 这里只保存低敏开关和驱动名称；凭据文件读取属于后续显式运行时装配。
type VideoGatewayConfig struct {
	Enabled         bool
	TrafficEnabled  bool
	RealProvider    bool
	LocalFakeTest   bool
	ExecutionDriver string
	Secrets         VideoSecretPaths `json:"-"`
	Infrastructure  VideoInfrastructureConfig
	invalidField    string
}

// VideoInfrastructureConfig只保存低敏连接位置和实例身份，密码仍来自独立受限文件。
type VideoInfrastructureConfig struct {
	MinIOEndpoint, MinIOPublicUploadEndpoint string
	MinIOUseSSL                              bool
	FakeProviderEndpoint                     string
	RabbitEndpoint, RabbitUser, RabbitVHost  string
	RabbitTLS                                bool
	RedisAddr                                string
	RedisDB                                  int
	WorkerID                                 string
}

// LoadVideoGatewayConfig 仅查询视频配置白名单，不接触Bifrost和Provider Key。
// lookup是环境变量读取边界，测试可独立验证关闭态没有越界读取。
func LoadVideoGatewayConfig(lookup func(string) (string, bool)) VideoGatewayConfig {
	cfg := VideoGatewayConfig{ExecutionDriver: "native_async"}
	if lookup == nil {
		cfg.invalidField = "environment_reader"
		return cfg
	}
	readBool := func(name string) bool {
		raw, present := lookup(name)
		if !present {
			return false
		}
		switch strings.TrimSpace(raw) {
		case "true":
			return true
		case "false":
			return false
		default:
			// 只保留固定字段名，绝不把错误配置原值带进日志。
			if cfg.invalidField == "" {
				cfg.invalidField = name
			}
			return false
		}
	}
	cfg.Enabled = readBool("VIDEO_GATEWAY_ENABLED")
	cfg.TrafficEnabled = readBool("VIDEO_GATEWAY_TRAFFIC_ENABLED")
	cfg.RealProvider = readBool("REAL_PROVIDER")
	cfg.LocalFakeTest = readBool("VIDEO_GATEWAY_LOCAL_FAKE_TEST")
	if driver, present := lookup("VIDEO_EXECUTION_DRIVER"); present {
		cfg.ExecutionDriver = strings.TrimSpace(driver)
	}
	if cfg.Enabled {
		readPath := func(name string) string { value, _ := lookup(name); return strings.TrimSpace(value) }
		cfg.Secrets = VideoSecretPaths{
			RepositoryRoot: readPath("VIDEO_GATEWAY_REPOSITORY_ROOT"),
			Quote:          readPath("VIDEO_GATEWAY_QUOTE_SECRET_FILE"), Payload: readPath("VIDEO_GATEWAY_PAYLOAD_SECRET_FILE"),
			Callback: readPath("VIDEO_GATEWAY_CALLBACK_SECRET_FILE"), AdminReason: readPath("VIDEO_GATEWAY_ADMIN_REASON_SECRET_FILE"),
			Download: readPath("VIDEO_GATEWAY_DOWNLOAD_SECRET_FILE"), MinIOAccess: readPath("VIDEO_GATEWAY_MINIO_ACCESS_KEY_FILE"),
			MinIOSecret: readPath("VIDEO_GATEWAY_MINIO_SECRET_KEY_FILE"), RabbitPassword: readPath("VIDEO_GATEWAY_RABBIT_PASSWORD_FILE"),
			RedisPassword: readPath("VIDEO_GATEWAY_REDIS_PASSWORD_FILE"),
			CapacityNonce: readPath("VIDEO_GATEWAY_CAPACITY_SECRET_FILE"),
		}
		cfg.Infrastructure = VideoInfrastructureConfig{
			MinIOEndpoint:             readPath("VIDEO_GATEWAY_MINIO_ENDPOINT"),
			MinIOPublicUploadEndpoint: readPath("VIDEO_GATEWAY_MINIO_PUBLIC_UPLOAD_ENDPOINT"),
			MinIOUseSSL:               readBool("VIDEO_GATEWAY_MINIO_USE_SSL"),
			FakeProviderEndpoint:      readPath("VIDEO_GATEWAY_FAKE_PROVIDER_ENDPOINT"),
			RabbitEndpoint:            readPath("VIDEO_GATEWAY_RABBIT_ENDPOINT"),
			RabbitUser:                readPath("VIDEO_GATEWAY_RABBIT_USER"),
			RabbitVHost:               readPath("VIDEO_GATEWAY_RABBIT_VHOST"),
			RabbitTLS:                 readBool("VIDEO_GATEWAY_RABBIT_TLS"),
			RedisAddr:                 readPath("VIDEO_GATEWAY_REDIS_ADDR"),
			WorkerID:                  readPath("VIDEO_GATEWAY_WORKER_ID"),
		}
		if cfg.Infrastructure.RabbitVHost == "" {
			cfg.Infrastructure.RabbitVHost = "/"
		}
		if raw, present := lookup("VIDEO_GATEWAY_REDIS_DB"); present {
			value, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil || value < 0 || value > 15 {
				if cfg.invalidField == "" {
					cfg.invalidField = "VIDEO_GATEWAY_REDIS_DB"
				}
			} else {
				cfg.Infrastructure.RedisDB = value
			}
		}
	}
	return cfg
}

// ValidateVideoGatewayConfig 在任何视频依赖初始化前拒绝矛盾或越权开关。
// G7仅允许显式test环境Fake流量，真实Provider即使配置为true也不能开启。
func (c Config) ValidateVideoGatewayConfig() error {
	v := c.VideoGateway
	if v.invalidField != "" {
		return fmt.Errorf("视频网关配置无效：%s", v.invalidField)
	}
	if v.RealProvider {
		return fmt.Errorf("VID-G7不允许开启真实Provider")
	}
	if !v.Enabled {
		if v.TrafficEnabled || v.LocalFakeTest {
			return fmt.Errorf("视频模块关闭时流量与Fake测试许可必须关闭")
		}
		return nil
	}
	if v.ExecutionDriver != "native_async" {
		return fmt.Errorf("视频执行驱动必须为已冻结的native_async")
	}
	if v.LocalFakeTest && strings.TrimSpace(c.AppEnv) != "test" {
		return fmt.Errorf("视频Fake许可仅允许显式test环境")
	}
	if v.TrafficEnabled && !v.LocalFakeTest {
		return fmt.Errorf("VID-G7视频流量必须具备显式Fake测试许可")
	}
	if err := validateVideoInfrastructure(v.Infrastructure); err != nil {
		return err
	}
	return videosecrets.ValidateReferences(v.Secrets.RepositoryRoot, v.Secrets.files())
}

var videoWorkerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,47}$`)

func validateVideoInfrastructure(v VideoInfrastructureConfig) error {
	for _, item := range []struct {
		name, value string
	}{{"VIDEO_GATEWAY_MINIO_ENDPOINT", v.MinIOEndpoint}, {"VIDEO_GATEWAY_RABBIT_ENDPOINT", v.RabbitEndpoint}, {"VIDEO_GATEWAY_REDIS_ADDR", v.RedisAddr}} {
		if item.value == "" || strings.Contains(item.value, "://") || strings.ContainsAny(item.value, "@/\\ \t\r\n") {
			return fmt.Errorf("视频网关基础设施配置无效：%s", item.name)
		}
	}
	if v.RabbitUser == "" || len(v.RabbitUser) > 64 || strings.ContainsAny(v.RabbitUser, ":@/\\ \t\r\n") || v.RabbitVHost == "" || len(v.RabbitVHost) > 128 || !strings.HasPrefix(v.RabbitVHost, "/") || strings.ContainsAny(v.RabbitVHost, "?#\x00\r\n") || !videoWorkerIDPattern.MatchString(v.WorkerID) {
		return fmt.Errorf("视频网关RabbitMQ或Worker配置无效")
	}
	parsed, err := url.Parse(v.MinIOPublicUploadEndpoint)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") {
		return fmt.Errorf("视频网关公开上传入口配置无效")
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "http" {
		address, parseErr := netip.ParseAddr(host)
		if host != "localhost" && (parseErr != nil || !address.IsLoopback()) {
			return fmt.Errorf("视频网关公开上传入口配置无效")
		}
	} else {
		if parsed.Scheme != "https" || strings.ContainsAny(host, "_ ") {
			return fmt.Errorf("视频网关公开上传入口配置无效")
		}
		if !strings.Contains(host, ".") {
			if _, parseErr := netip.ParseAddr(host); parseErr != nil {
				return fmt.Errorf("视频网关公开上传入口配置无效")
			}
		}
	}
	fakeURL, err := url.Parse(v.FakeProviderEndpoint)
	if err != nil || fakeURL.Scheme != "http" || fakeURL.User != nil || fakeURL.Hostname() == "" || fakeURL.RawQuery != "" || fakeURL.Fragment != "" || fakeURL.Path != "" {
		return fmt.Errorf("视频网关Fake Provider入口配置无效")
	}
	fakeHost := strings.ToLower(fakeURL.Hostname())
	fakeAddress, fakeParseErr := netip.ParseAddr(fakeHost)
	if fakeHost != "localhost" && (fakeParseErr != nil || !fakeAddress.IsLoopback()) {
		return fmt.Errorf("视频网关Fake Provider入口配置无效")
	}
	return nil
}

// VideoSecretPaths 只保存可信部署方提供的文件引用，各用途必须独立。
type VideoSecretPaths struct {
	RepositoryRoot                                          string
	Quote, Payload, Callback, AdminReason, Download         string
	MinIOAccess, MinIOSecret, RabbitPassword, RedisPassword string
	CapacityNonce                                           string
}

func (p VideoSecretPaths) files() []videosecrets.File {
	return []videosecrets.File{
		{Purpose: videosecrets.Quote, Path: p.Quote}, {Purpose: videosecrets.Payload, Path: p.Payload},
		{Purpose: videosecrets.Callback, Path: p.Callback}, {Purpose: videosecrets.AdminReason, Path: p.AdminReason},
		{Purpose: videosecrets.Download, Path: p.Download}, {Purpose: videosecrets.MinIOAccess, Path: p.MinIOAccess},
		{Purpose: videosecrets.MinIOSecret, Path: p.MinIOSecret}, {Purpose: videosecrets.RabbitPassword, Path: p.RabbitPassword},
		{Purpose: videosecrets.RedisPassword, Path: p.RedisPassword},
		{Purpose: videosecrets.CapacityNonce, Path: p.CapacityNonce},
	}
}

// LoadVideoSecrets 必须由视频运行时装配调用；模块关闭时不校验路径也不打开文件。
// 流量关闭而模块开启仍加载原任务恢复所需凭据，不能停掉在途任务的恢复能力。
func (c Config) LoadVideoSecrets() (*videosecrets.Bundle, error) {
	if err := c.ValidateVideoGatewayConfig(); err != nil {
		return nil, err
	}
	if !c.VideoGateway.Enabled {
		return nil, nil
	}
	return videosecrets.Load(c.VideoGateway.Secrets.RepositoryRoot, c.VideoGateway.Secrets.files())
}
