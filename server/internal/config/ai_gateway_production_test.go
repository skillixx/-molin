package config

import "testing"

func validProductionGatewayConfig() Config {
	return Config{
		AppEnv: "production", AIGatewayTrafficEnabled: true,
		TokenProviderKey:   "12345678901234567890123456789012",
		APIKeyHMACSecret:   "api-key-hmac-secret-32-bytes-long",
		InternalAPIToken:   "internal-api-token-32-bytes-long",
		InternalAllowedIPs: "10.0.0.8/32",
		RabbitMQURL:        "amqp://redacted@rabbitmq/", AIOutboxExchange: "molin.ai.billing",
		TokenExecutionDriver: "bifrost", BifrostBaseURL: "http://bifrost-lb:8080",
		BifrostInternalToken:      "bifrost-internal-token-32-bytes-x",
		TokenHoldUnitPrice:        "0.00002",
		TokenHoldDefaultMaxTokens: 4096,
	}
}

func TestValidateAIGatewayProductionConfig(t *testing.T) {
	config := validProductionGatewayConfig()
	if err := config.ValidateAIGatewayProductionConfig(); err != nil {
		t.Fatalf("完整生产配置应通过: %v", err)
	}

	config.RabbitMQURL = ""
	if err := config.ValidateAIGatewayProductionConfig(); err == nil {
		t.Fatal("生产流量开启但 RabbitMQ 缺失时必须失败关闭")
	}

	config = validProductionGatewayConfig()
	config.TokenHoldUnitPrice = "NaN"
	if err := config.ValidateAIGatewayProductionConfig(); err == nil {
		t.Fatal("生产预占兜底单价不是正有限数值时必须失败关闭")
	}
}

func TestValidateAIGatewayProductionConfigTrafficClosed(t *testing.T) {
	config := Config{AppEnv: "production", AIGatewayTrafficEnabled: false}
	if err := config.ValidateAIGatewayProductionConfig(); err != nil {
		t.Fatalf("生产流量关闭时允许先部署关闭态制品: %v", err)
	}
}
