package config

import (
	"os"
)

type Config struct {
	AppEnv  string
	AppName string
	APIHost string
	APIPort string
}

func Load() Config {
	return Config{
		AppEnv:  getenv("APP_ENV", "local"),
		AppName: getenv("APP_NAME", "molin-cloud-platform"),
		APIHost: getenv("API_HOST", "0.0.0.0"),
		APIPort: getenv("API_PORT", "8080"),
	}
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
