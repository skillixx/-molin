package token_gateway

import (
	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/crypto"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
)

// Module 聚合 token_gateway 模块对外暴露的服务，便于 bootstrap 统一装配。
type Module struct {
	ChannelService *service.ChannelService
	CatalogService *service.CatalogService
	ForwardService *service.ForwardService
	UsageService   *service.UsageService
}

// New 构造 token_gateway 模块依赖。
// tokenProviderKey 为 32 字节 AES-256-GCM 密钥（来自 config.TokenProviderKey / TOKEN_PROVIDER_KEY）；
// 密钥长度非法时返回错误，由 bootstrap 决定是否致命中断启动。
//
// 跨模块依赖通过接口注入，避免 import 环：
//   - assetGate：访问门禁（asset.AssetService.HasActiveTokenAsset 适配）
//   - reporter：按量计费上报（finance_consumer 适配，可为 nil → 本期跳过扣费）
func New(db *gorm.DB, tokenProviderKey string, assetGate service.AssetGate, reporter service.UsageReporter) (*Module, error) {
	cipher, err := crypto.New([]byte(tokenProviderKey))
	if err != nil {
		return nil, err
	}

	channelRepo := repository.NewChannelRepository(db)
	modelRepo := repository.NewTokenModelRepository(db)
	usageRepo := repository.NewUsageLogRepository(db)

	return &Module{
		ChannelService: service.NewChannelService(channelRepo, cipher),
		CatalogService: service.NewCatalogService(modelRepo),
		ForwardService: service.NewForwardService(modelRepo, channelRepo, usageRepo, cipher, assetGate, reporter),
		UsageService:   service.NewUsageService(usageRepo),
	}, nil
}
