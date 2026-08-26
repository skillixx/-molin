package config

import (
	"path/filepath"
	"testing"
)

func TestImageGatewayDefaultClosed(t *testing.T) {
	config := Config{}
	if err := config.ValidateImageGatewayConfig(); err != nil {
		t.Fatalf("默认关闭不应要求基础设施: %v", err)
	}
	config.ImageGatewayTrafficEnabled = true
	if err := config.ValidateImageGatewayConfig(); err == nil {
		t.Fatal("模块关闭时流量开启必须拒绝")
	}
}

func TestImageGatewayLocalFakeConfiguration(t *testing.T) {
	directory := t.TempDir()
	config := Config{
		AppEnv: "test", ImageGatewayEnabled: true, ImageGatewayTrafficEnabled: true, ImageGatewayLocalFakeTest: true, ImageGatewayProvider: "fake",
		TokenProviderKey: "0123456789abcdef0123456789abcdef", APIKeyHMACSecret: "abcdef0123456789abcdef0123456789",
		ImageGatewayQuoteSecretFile: filepath.Join(directory, "quote"), ImageGatewayPromptSecretFile: filepath.Join(directory, "prompt"),
		ImageGatewayMinIOEndpoint: "minio:9000", ImageGatewayMinIOPublicDownloadEndpoint: "http://127.0.0.1:19000", ImageGatewayMinIOAccessKeyFile: filepath.Join(directory, "access"), ImageGatewayMinIOSecretKeyFile: filepath.Join(directory, "secret"),
		ImageGatewayTempBucket: "ai-upload-temp", ImageGatewayResultBucket: "ai-result", ImageGatewayQuarantineBucket: "ai-quarantine",
		RabbitMQURL: "amqp://fake:rabbit@rabbit:5672/", ImageGatewayQueueExchange: "image", ImageGatewayQueueName: "image.generate", ImageGatewayQueueRoutingKey: "generate",
		ImageGatewayDeadExchange: "image.dead", ImageGatewayDeadQueue: "image.dead", ImageGatewayDeadRoutingKey: "generate.dead",
	}
	if err := config.ValidateImageGatewayConfig(); err != nil {
		t.Fatalf("本地Fake配置应通过: %v", err)
	}
	config.ImageGatewayProvider = "openrouter"
	if err := config.ValidateImageGatewayConfig(); err == nil {
		t.Fatal("G7未显式启用OpenRouter时必须拒绝")
	}
	config.ImageGatewayProvider = "fake"
	config.ImageGatewayMinIOPublicDownloadEndpoint = ""
	if err := config.ValidateImageGatewayConfig(); err == nil {
		t.Fatal("缺少浏览器公开下载端点必须拒绝")
	}
	config.ImageGatewayMinIOPublicDownloadEndpoint = "http://127.0.0.1:19000"
	config.ImageGatewayQuoteSecretFile = config.ImageGatewayPromptSecretFile
	if err := config.ValidateImageGatewayConfig(); err == nil {
		t.Fatal("Quote与Prompt Secret复用必须拒绝")
	}
}

func TestImageGatewayG9OpenRouterConfiguration(t *testing.T) {
	directory := t.TempDir()
	config := Config{
		AppEnv: "test", ImageGatewayEnabled: true, ImageGatewayProvider: "openrouter",
		TokenProviderKey: "0123456789abcdef0123456789abcdef", APIKeyHMACSecret: "abcdef0123456789abcdef0123456789",
		ImageGatewayOpenRouterModel: ImageGatewayG9OpenRouterModel, ImageGatewayOpenRouterProviderTag: ImageGatewayG9OpenRouterProviderTag,
		ImageGatewayOpenRouterMaxCostUSD: ImageGatewayG9MaxProviderCostUSD,
		ImageGatewayQuoteSecretFile:      filepath.Join(directory, "quote"), ImageGatewayPromptSecretFile: filepath.Join(directory, "prompt"),
		ImageGatewayMinIOEndpoint: "minio:9000", ImageGatewayMinIOPublicDownloadEndpoint: "http://127.0.0.1:19000",
		ImageGatewayMinIOAccessKeyFile: filepath.Join(directory, "access"), ImageGatewayMinIOSecretKeyFile: filepath.Join(directory, "secret"),
		ImageGatewayTempBucket: "ai-upload-temp", ImageGatewayResultBucket: "ai-result", ImageGatewayQuarantineBucket: "ai-quarantine",
		RabbitMQURL: "amqp://fake:rabbit@rabbit:5672/", ImageGatewayQueueExchange: "image", ImageGatewayQueueName: "image.generate", ImageGatewayQueueRoutingKey: "generate",
		ImageGatewayDeadExchange: "image.dead", ImageGatewayDeadQueue: "image.dead", ImageGatewayDeadRoutingKey: "generate.dead",
	}
	if err := config.ValidateImageGatewayConfig(); err != nil {
		t.Fatalf("IMG-G9无Key关闭态应通过: %v", err)
	}
	config.ImageGatewayTrafficEnabled = true
	if err := config.ValidateImageGatewayConfig(); err == nil {
		t.Fatal("流量开启但OpenRouter未启用必须拒绝")
	}
	config.ImageGatewayOpenRouterEnabled = true
	config.ImageGatewayOpenRouterKeyFile = filepath.Join(directory, "openrouter-key")
	if err := config.ValidateImageGatewayConfig(); err != nil {
		t.Fatalf("IMG-G9真实调用配置应通过: %v", err)
	}
	config.ImageGatewayOpenRouterProviderTag = "google-ai-studio/global"
	if err := config.ValidateImageGatewayConfig(); err == nil {
		t.Fatal("非冻结ProviderTag必须拒绝")
	}
	config.ImageGatewayOpenRouterProviderTag = ImageGatewayG9OpenRouterProviderTag
	config.ImageGatewayOpenRouterMaxCostUSD = "0.26"
	if err := config.ValidateImageGatewayConfig(); err == nil {
		t.Fatal("提高Provider费用上限必须拒绝")
	}
}
