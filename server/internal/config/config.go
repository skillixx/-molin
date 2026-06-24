package config

import (
	"os"
	"strconv"
	"strings"
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

	// 支付回调报文加密密钥（32 字节，AES-256-GCM），未配置时记录 warn 并降级为明文
	NotifyBodyKey string

	// Token 网关上游渠道 api_key 加密密钥（32 字节，AES-256-GCM）
	// 用于 token_channels.api_key_encrypted 加解密；通过 TOKEN_PROVIDER_KEY 注入。
	TokenProviderKey string

	// 平台 API Key（sk）HMAC 密钥（S2-甲5）。
	// DB 只存 HMAC-SHA256(sk 明文, APIKeyHMACSecret)，明文只在签发时返回一次。
	// 通过 API_KEY_HMAC_SECRET 注入；未配置时 sk 系统不装配（bootstrap 灰度降级，不 panic）。
	APIKeyHMACSecret string

	// 内部接口共享密钥（S2-丁5）。门面 prepaid 结算时调用 asset 的
	// POST /api/internal/entitlement-consume 须带 X-Internal-Token=该值；
	// 通过 INTERNAL_API_TOKEN 注入；与 asset/finance_consumer 校验侧同源。未配置时 prepaid 内部调用 fail-closed。
	InternalAPIToken string

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

	// presenton 应用接入（D1 打开入口）。
	// PresentonAppCode：presenton 应用在 applications 表的 code，用于 entitlement 闸门解析。
	PresentonAppCode string
	// PresentonGatewayBaseURL：墨灵 D2 反代基址，拼接前端嵌入 URL（如 https://molin.example.com）。
	PresentonGatewayBaseURL string
	// PresentonTicketTTLSeconds：SSO 票据有效期（秒）。
	PresentonTicketTTLSeconds int
}

func Load() Config {
	return Config{
		AppEnv:  getenv("APP_ENV", "local"),
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

		JWTSecret:        getenv("JWT_SECRET", ""),
		JWTExpireSeconds: int64(getenvInt("JWT_EXPIRE_SECONDS", 7200)),

		RefreshTokenSecret:     getenv("REFRESH_TOKEN_SECRET", ""),
		RefreshTokenExpireDays: getenvInt("REFRESH_TOKEN_EXPIRE_DAYS", 30),

		IDCardHMACSecret: getenv("ID_CARD_HMAC_SECRET", ""),

		AdminVerifyExpireHours: getenvInt("ADMIN_VERIFY_EXPIRE_HOURS", 24),

		NotifyBodyKey: getenv("NOTIFY_BODY_KEY", ""),

		TokenProviderKey: getenv("TOKEN_PROVIDER_KEY", ""),

		APIKeyHMACSecret: getenv("API_KEY_HMAC_SECRET", ""),

		InternalAPIToken:     getenv("INTERNAL_API_TOKEN", ""),
		AssetInternalBaseURL: getenv("ASSET_INTERNAL_BASE_URL", "http://127.0.0.1:8080"),
		// 兜底单价默认 0.00002 CNY/token（约 ¥0.02/千 token，保守上限；运营按真实档位下调）。
		TokenHoldUnitPrice:        getenv("TOKEN_HOLD_UNIT_PRICE", "0.00002"),
		TokenHoldDefaultMaxTokens: getenvInt("TOKEN_HOLD_DEFAULT_MAX_TOKENS", 4096),

		// 插件凭证密钥：优先 PLUGIN_SECRET_KEY，未配置时回退复用 TOKEN_PROVIDER_KEY（契约 §5 允许）。
		PluginSecretKey: getenv("PLUGIN_SECRET_KEY", getenv("TOKEN_PROVIDER_KEY", "")),

		MaxRounds:             getenvInt("MAX_ROUNDS", 5),
		PluginDomainWhitelist: splitCSV(getenv("PLUGIN_DOMAIN_WHITELIST", "")),

		PresentonAppCode:          getenv("PRESENTON_APP_CODE", "presenton-ppt"),
		PresentonGatewayBaseURL:   getenv("PRESENTON_GATEWAY_BASE_URL", ""),
		PresentonTicketTTLSeconds: getenvInt("PRESENTON_TICKET_TTL_SECONDS", 300),
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
