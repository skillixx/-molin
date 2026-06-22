package config

import (
	"os"
	"strconv"
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
	}
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
