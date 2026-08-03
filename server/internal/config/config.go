package config

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Config 汇聚所有运行时配置，通过环境变量注入，无默认密钥。
type Config struct {
	AppEnv  string
	AppName string
	APIHost string
	APIPort string

	// 数据库
	MySQLHost     string
	MySQLPort     string
	MySQLDatabase string
	MySQLUser     string
	MySQLPassword string

	// Redis
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// RabbitMQ 仅用于发布事务 Outbox；连接不可用时事件保留在 MySQL 并重试。
	RabbitMQURL      string
	AIOutboxExchange string

	// JWT
	JWTSecret        string
	JWTExpireSeconds int64

	// Refresh Token（DB 只存 HMAC hash，不存明文）
	RefreshTokenSecret     string
	RefreshTokenExpireDays int

	// 身份证号 HMAC（禁止使用 SHA-256/MD5 直接哈希）
	IDCardHMACSecret string

	// 管理员双重认证有效期（小时），超时后需重新认证
	AdminVerifyExpireHours int

	// DirectMail 邮件发送配置。密钥仅从环境变量注入，不进入日志或接口响应。
	DirectMailAccessKeyID     string
	DirectMailAccessKeySecret string
	DirectMailRegion          string
	DirectMailAccountName     string
	DirectMailFromAlias       string
	DirectMailEndpoint        string
	EmailAdapter              string
	EmailAddressHMACSecret    string
	EmailIdempotencySecret    string
	EmailDebugReturnCode      bool
	// EmailAdminVerifyBootstrap 保存一次性管理员邮箱认证配置的启动期解析结果。
	// 默认关闭时不携带任何安全值，避免普通邮件接口意外复用该能力。
	EmailAdminVerifyBootstrap EmailAdminVerifyBootstrapConfig

	// 支付回调报文加密密钥（32 字节，AES-256-GCM），未配置时记录 warn 并降级为明文
	NotifyBodyKey string

	// Token 网关上游渠道 api_key 加密密钥（32 字节，AES-256-GCM）
	// 用于 token_channels.api_key_encrypted 加解密；通过 TOKEN_PROVIDER_KEY 注入。
	TokenProviderKey string
	// TokenExecutionDriver 选择文字模型执行层，默认 native；Bifrost 配置仅在显式启用时生效。
	TokenExecutionDriver string
	BifrostBaseURL       string
	BifrostInternalToken string

	// 平台 API Key（sk）HMAC 密钥（S2-甲5）。
	// DB 只存 HMAC-SHA256(sk 明文, APIKeyHMACSecret)，明文只在签发时返回一次。
	// 通过 API_KEY_HMAC_SECRET 注入；未配置时 sk 系统不装配（bootstrap 灰度降级，不 panic）。
	APIKeyHMACSecret string

	// 内部接口共享密钥（S2-丁5）。门面 prepaid 结算时调用 asset 的
	// POST /api/internal/entitlement-consume 须带 X-Internal-Token=该值；
	// 通过 INTERNAL_API_TOKEN 注入；与 asset/finance_consumer 校验侧同源。未配置时 prepaid 内部调用 fail-closed。
	InternalAPIToken string
	// 内部指标端点的来源 IP 与可信代理列表保留原始配置，由指标安全门完整解析并失败关闭。
	InternalAllowedIPs      string
	InternalTrustedProxyIPs string
	// TrustedProxyIPs 仅用于公开流量来源解析，与内部 metrics 的可信代理配置完全隔离。
	TrustedProxyIPs string

	// asset 模块内部接口基址（S2-丁5），门面 prepaid 结算调用 entitlement-consume 用。
	// 通过 ASSET_INTERNAL_BASE_URL 注入；默认本机回环（同进程部署时门面 → 本机 API 端口）。
	AssetInternalBaseURL string

	// postpaid 预扣保证金兜底单价（每 token，单位 CNY，S2-丁5 / D1）。
	// hold = max_tokens × 该单价（仅用于前置冻结占额防并发透支；实扣仍由 product_billing_rules 计算，结算时多退少补）。
	// 通过 TOKEN_HOLD_UNIT_PRICE 注入；取保守值，宁高勿低（高估只多冻结、结算时退回，绝不透支）。
	TokenHoldUnitPrice string

	// postpaid 预扣保证金兜底 max_tokens（S2-丁5）。
	// 请求未带 max_tokens 时按此上限冻结；通过 TOKEN_HOLD_DEFAULT_MAX_TOKENS 注入。
	TokenHoldDefaultMaxTokens int

	// 工作台插件凭证（plugins.auth_config_encrypted）加密密钥（32 字节，AES-256-GCM，S2-丁7 / 契约 §5）。
	// 通过 PLUGIN_SECRET_KEY 注入；未配置时回退复用 TOKEN_PROVIDER_KEY（契约允许复用）。
	PluginSecretKey string

	// tool-use 编排最大轮数（S2-丁10 / 契约 §4，默认 5，不硬编码）。通过 MAX_ROUNDS 注入。
	MaxRounds int

	// 工作台外呼（插件转发 / skill 联网）域名白名单（S2-运2 / 契约 §5）。
	// 通过 PLUGIN_DOMAIN_WHITELIST 注入（逗号分隔，如 "api.weather.com,docs.example.com"）；
	// 空=不启用白名单（仅按 SSRF 网段规则拦内网/回环）。
	PluginDomainWhitelist []string

	// TrustInternalOutbound 自建可信环境开关：开启后对外访问链接放开 http 协议与内网/IP 直连
	// （MCP / 插件 / Skill 外呼 / 应用 access_url）。默认 false（仅 https + 禁内网），生产环境保持默认。
	// 仅应在网络隔离的局域网自建可信部署中置 true（等价于关闭 SSRF 防护）。
	// 通过 TRUST_INTERNAL_OUTBOUND 注入。
	TrustInternalOutbound bool
}

// EmailAdminVerifyBootstrapConfig 是一次性 bootstrap 的独立安全配置。
// Token 只保存在进程内存中；网段在启动期完成严格解析，运行期不再容错。
type EmailAdminVerifyBootstrapConfig struct {
	Enabled         bool
	Token           string
	AllowedIPs      []netip.Prefix
	TrustedProxyIPs []netip.Prefix
}

func Load() Config {
	rawAppEnv, appEnvExplicit := os.LookupEnv("APP_ENV")
	appEnv := strings.ToLower(strings.TrimSpace(getenv("APP_ENV", "local")))
	emailDebugReturnCode := strings.TrimSpace(os.Getenv("EMAIL_DEBUG_RETURN_CODE")) == "true"
	// 普通本地启动仍保留 local 默认值，但调试回码必须同时具备显式安全环境声明。
	// APP_ENV 缺失、空白、未知或生产环境时，即使误开调试开关也强制关闭回码。
	if !appEnvExplicit || !(Config{AppEnv: rawAppEnv}).IsSafeNonProduction() {
		emailDebugReturnCode = false
	}

	return Config{
		AppEnv:  appEnv,
		AppName: getenv("APP_NAME", "molin-cloud-platform"),
		APIHost: getenv("API_HOST", "0.0.0.0"),
		APIPort: getenv("API_PORT", "8080"),

		MySQLHost:     getenv("MYSQL_HOST", "127.0.0.1"),
		MySQLPort:     getenv("MYSQL_PORT", "13306"),
		MySQLDatabase: getenv("MYSQL_DATABASE", "molin"),
		MySQLUser:     getenv("MYSQL_USER", "molin"),
		MySQLPassword: getenv("MYSQL_PASSWORD", ""),

		RedisAddr:     getenv("REDIS_ADDR", "127.0.0.1:16379"),
		RedisPassword: getenv("REDIS_PASSWORD", ""),
		RedisDB:       getenvInt("REDIS_DB", 0),

		RabbitMQURL:      getenv("RABBITMQ_URL", ""),
		AIOutboxExchange: getenv("AI_OUTBOX_EXCHANGE", "molin.ai.billing"),

		JWTSecret:        getenv("JWT_SECRET", ""),
		JWTExpireSeconds: int64(getenvInt("JWT_EXPIRE_SECONDS", 7200)),

		RefreshTokenSecret:     getenv("REFRESH_TOKEN_SECRET", ""),
		RefreshTokenExpireDays: getenvInt("REFRESH_TOKEN_EXPIRE_DAYS", 30),

		IDCardHMACSecret: getenv("ID_CARD_HMAC_SECRET", ""),

		AdminVerifyExpireHours: getenvInt("ADMIN_VERIFY_EXPIRE_HOURS", 24),

		DirectMailAccessKeyID:     getenv("DIRECTMAIL_ACCESS_KEY_ID", ""),
		DirectMailAccessKeySecret: getenv("DIRECTMAIL_ACCESS_KEY_SECRET", ""),
		DirectMailRegion:          getenv("DIRECTMAIL_REGION", ""),
		DirectMailAccountName:     getenv("DIRECTMAIL_ACCOUNT_NAME", ""),
		DirectMailFromAlias:       getenv("DIRECTMAIL_FROM_ALIAS", "墨灵"),
		DirectMailEndpoint:        getenv("DIRECTMAIL_ENDPOINT", "https://dm.aliyuncs.com/"),
		EmailAdapter:              strings.ToLower(strings.TrimSpace(getenv("EMAIL_ADAPTER", "production"))),
		EmailAddressHMACSecret:    getenv("EMAIL_ADDRESS_HMAC_SECRET", ""),
		EmailIdempotencySecret:    getenv("EMAIL_IDEMPOTENCY_SECRET", ""),
		EmailDebugReturnCode:      emailDebugReturnCode,

		NotifyBodyKey: getenv("NOTIFY_BODY_KEY", ""),

		TokenProviderKey:     getenv("TOKEN_PROVIDER_KEY", ""),
		TokenExecutionDriver: strings.ToLower(strings.TrimSpace(getenv("TOKEN_EXECUTION_DRIVER", "native"))),
		BifrostBaseURL:       getenv("BIFROST_BASE_URL", "http://127.0.0.1:18080"),
		BifrostInternalToken: getenv("BIFROST_INTERNAL_TOKEN", ""),

		APIKeyHMACSecret: getenv("API_KEY_HMAC_SECRET", ""),

		InternalAPIToken:        getenv("INTERNAL_API_TOKEN", ""),
		InternalAllowedIPs:      os.Getenv("INTERNAL_ALLOWED_IPS"),
		InternalTrustedProxyIPs: os.Getenv("INTERNAL_TRUSTED_PROXY_IPS"),
		TrustedProxyIPs:         os.Getenv("TRUSTED_PROXY_IPS"),
		AssetInternalBaseURL:    getenv("ASSET_INTERNAL_BASE_URL", "http://127.0.0.1:8080"),
		// 兜底单价默认 0.00002 CNY/token（约 ¥0.02/千 token，保守上限；运营按真实档位下调）。
		TokenHoldUnitPrice:        getenv("TOKEN_HOLD_UNIT_PRICE", "0.00002"),
		TokenHoldDefaultMaxTokens: getenvInt("TOKEN_HOLD_DEFAULT_MAX_TOKENS", 4096),

		// 插件凭证密钥：优先 PLUGIN_SECRET_KEY，未配置时回退复用 TOKEN_PROVIDER_KEY（契约 §5 允许）。
		PluginSecretKey: getenv("PLUGIN_SECRET_KEY", getenv("TOKEN_PROVIDER_KEY", "")),

		MaxRounds:             getenvInt("MAX_ROUNDS", 5),
		PluginDomainWhitelist: splitCSV(getenv("PLUGIN_DOMAIN_WHITELIST", "")),

		TrustInternalOutbound: getenvBool("TRUST_INTERNAL_OUTBOUND", false),
	}
}

// ValidateAdminVerifyConfig 在基础设施初始化前校验管理员认证安全配置。
// 负有效期不具备明确业务语义，必须拒绝启动，不能退化为永不过期。
func (c Config) ValidateAdminVerifyConfig() error {
	if c.AdminVerifyExpireHours < 0 {
		return fmt.Errorf("ADMIN_VERIFY_EXPIRE_HOURS 不得小于 0")
	}
	return nil
}

// LoadEmailAdminVerifyBootstrapConfig 从四个冻结环境变量加载一次性 bootstrap 配置。
// enabled 只有键缺失时默认 false；显式空值或非字面 true/false 均拒绝启动。
func LoadEmailAdminVerifyBootstrapConfig() (EmailAdminVerifyBootstrapConfig, error) {
	const enabledKey = "EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED"
	rawEnabled, present := os.LookupEnv(enabledKey)
	if !present {
		return EmailAdminVerifyBootstrapConfig{}, nil
	}

	var enabled bool
	switch {
	case strings.EqualFold(rawEnabled, "true"):
		enabled = true
	case strings.EqualFold(rawEnabled, "false"):
		return EmailAdminVerifyBootstrapConfig{}, nil
	default:
		return EmailAdminVerifyBootstrapConfig{}, fmt.Errorf("%s 只允许字面 true/false，且不得为空", enabledKey)
	}

	token, tokenPresent := os.LookupEnv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN")
	if !tokenPresent || !validEmailBootstrapToken(token) {
		return EmailAdminVerifyBootstrapConfig{}, fmt.Errorf("EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN 缺失或不符合安全要求")
	}
	allowed, err := parseRequiredBootstrapIPPrefixes(os.Getenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS"))
	if err != nil {
		return EmailAdminVerifyBootstrapConfig{}, fmt.Errorf("EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS 配置无效: %w", err)
	}
	trusted, err := parseRequiredBootstrapIPPrefixes(os.Getenv("EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS"))
	if err != nil {
		return EmailAdminVerifyBootstrapConfig{}, fmt.Errorf("EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS 配置无效: %w", err)
	}
	if err := validateEmailBootstrapIndependence(token, allowed, trusted); err != nil {
		return EmailAdminVerifyBootstrapConfig{}, err
	}
	return EmailAdminVerifyBootstrapConfig{Enabled: enabled, Token: token, AllowedIPs: allowed, TrustedProxyIPs: trusted}, nil
}

func parseRequiredBootstrapIPPrefixes(raw string) ([]netip.Prefix, error) {
	items, err := parseRequiredIPPrefixes(raw)
	if err != nil {
		return nil, err
	}
	if prefixesCoverAddressFamily(items, true) || prefixesCoverAddressFamily(items, false) {
		return nil, fmt.Errorf("配置不得覆盖完整 IPv4 或 IPv6 地址空间")
	}
	return items, nil
}

func parseRequiredIPPrefixes(raw string) ([]netip.Prefix, error) {
	if raw == "" {
		return nil, fmt.Errorf("配置不得为空")
	}
	items, err := ParseTrustedProxyIPs(raw)
	if err != nil || len(items) == 0 {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("配置不得为空")
	}
	return items, nil
}

func validEmailBootstrapToken(token string) bool {
	if len([]byte(token)) < 32 || !utf8.ValidString(token) || strings.TrimSpace(token) != token || strings.Contains(token, ",") {
		return false
	}
	for _, r := range token {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	lower := strings.ToLower(token)
	weakMarkers := []string{"change-me", "changeme", "replace-me", "placeholder", "your-token", "example-token", "default", "secret", "test"}
	for _, marker := range weakMarkers {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	// 少于八种不同字节属于明显低多样性值，不满足独立高强度 Token 要求。
	distinct := make(map[byte]struct{}, 8)
	for i := 0; i < len(token); i++ {
		distinct[token[i]] = struct{}{}
	}
	return len(distinct) >= 8
}

func validateEmailBootstrapIndependence(token string, allowed, trusted []netip.Prefix) error {
	if internal := os.Getenv("INTERNAL_API_TOKEN"); internal != "" && equalEmailBootstrapConfigValue(token, internal) {
		return fmt.Errorf("EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN 不得复用 INTERNAL_API_TOKEN")
	}
	if prefixesShareEquivalentNetwork(allowed, trusted) {
		return fmt.Errorf("bootstrap allowed 与 trusted proxy 网段不得复用")
	}
	return nil
}

func equalEmailBootstrapConfigValue(left, right string) bool {
	leftDigest := sha256.Sum256([]byte(left))
	rightDigest := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftDigest[:], rightDigest[:]) == 1
}

func prefixesShareEquivalentNetwork(left, right []netip.Prefix) bool {
	seen := make(map[netip.Prefix]struct{}, len(left))
	for _, prefix := range left {
		seen[prefix.Masked()] = struct{}{}
	}
	for _, prefix := range right {
		if _, ok := seen[prefix.Masked()]; ok {
			return true
		}
	}
	return false
}

func prefixesCoverAddressFamily(prefixes []netip.Prefix, ipv4 bool) bool {
	exact, candidates := buildPrefixCoverageIndex(prefixes, ipv4)
	if len(exact) == 0 {
		return false
	}
	if ipv4 {
		return prefixTreeCovered(netip.PrefixFrom(netip.IPv4Unspecified(), 0), 32, exact, candidates)
	}
	return prefixTreeCovered(netip.PrefixFrom(netip.IPv6Unspecified(), 0), 128, exact, candidates)
}

// buildPrefixCoverageIndex 同时建立精确前缀和候选祖先索引。
// 每个输入最多贡献地址位数加一个节点，使时间和空间上界保持为 O(输入数×地址位数)。
func buildPrefixCoverageIndex(prefixes []netip.Prefix, ipv4 bool) (map[netip.Prefix]struct{}, map[netip.Prefix]struct{}) {
	exact := make(map[netip.Prefix]struct{}, len(prefixes))
	candidates := make(map[netip.Prefix]struct{}, len(prefixes))
	for _, prefix := range prefixes {
		if prefix.Addr().Is4() != ipv4 {
			continue
		}
		prefix = prefix.Masked()
		exact[prefix] = struct{}{}
		for bits := 0; bits <= prefix.Bits(); bits++ {
			ancestor := netip.PrefixFrom(prefix.Addr(), bits).Masked()
			candidates[ancestor] = struct{}{}
		}
	}
	return exact, candidates
}

func prefixTreeCovered(prefix netip.Prefix, maxBits int, exact, candidates map[netip.Prefix]struct{}) bool {
	if _, ok := exact[prefix]; ok {
		return true
	}
	// 当前分支没有任何候选后代时立即剪枝，禁止向完整 IPv4/IPv6 地址树指数展开。
	if _, ok := candidates[prefix]; !ok {
		return false
	}
	if prefix.Bits() >= maxBits {
		return false
	}
	return prefixTreeCovered(prefixChild(prefix, false), maxBits, exact, candidates) &&
		prefixTreeCovered(prefixChild(prefix, true), maxBits, exact, candidates)
}

func prefixChild(prefix netip.Prefix, upper bool) netip.Prefix {
	bit := prefix.Bits()
	if prefix.Addr().Is4() {
		bytes := prefix.Addr().As4()
		if upper {
			bytes[bit/8] |= byte(1 << (7 - bit%8))
		}
		return netip.PrefixFrom(netip.AddrFrom4(bytes), bit+1)
	}
	bytes := prefix.Addr().As16()
	if upper {
		bytes[bit/8] |= byte(1 << (7 - bit%8))
	}
	return netip.PrefixFrom(netip.AddrFrom16(bytes), bit+1)
}

// ParseTrustedProxyIPs 严格解析公开流量可信代理列表；空配置表示合法直连模式。
func ParseTrustedProxyIPs(raw string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	networks := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			return nil, fmt.Errorf("可信代理列表包含空项")
		}
		if addr, err := netip.ParseAddr(item); err == nil {
			if addr.Zone() != "" {
				return nil, fmt.Errorf("可信代理地址不得包含 IPv6 zone")
			}
			bits := 128
			if addr.Is4() {
				bits = 32
			}
			networks = append(networks, netip.PrefixFrom(addr, bits))
			continue
		}
		prefix, err := netip.ParsePrefix(item)
		if err != nil || prefix.Addr().Zone() != "" {
			return nil, fmt.Errorf("可信代理地址或网段格式无效")
		}
		networks = append(networks, prefix.Masked())
	}
	return networks, nil
}

// IsSafeNonProduction 只认可明确列出的开发、测试环境；未知值按生产环境失败关闭。
func (c Config) IsSafeNonProduction() bool {
	switch strings.ToLower(strings.TrimSpace(c.AppEnv)) {
	case "local", "development", "dev", "test", "testing":
		return true
	default:
		return false
	}
}

// splitCSV 解析逗号分隔配置为去空白的非空切片（空输入返回 nil）。
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

// getenvBool 读取布尔环境变量，接受 1/t/true/y/yes/on（不区分大小写）为真，其余为 fallback。
func getenvBool(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

func getenvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}
