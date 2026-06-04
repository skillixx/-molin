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
