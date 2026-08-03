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
	ProjectService *service.ProjectService
	Orchestrator   *service.RequestOrchestratorService
}

// New 构造 token_gateway 模块依赖。
// tokenProviderKey 为 32 字节 AES-256-GCM 密钥（来自 config.TokenProviderKey / TOKEN_PROVIDER_KEY）；
// 密钥长度非法时返回错误，由 bootstrap 决定是否致命中断启动。
//
// 跨模块依赖通过接口注入，避免 import 环：
//   - assetGate：访问门禁（asset.AssetService.HasActiveTokenAsset 适配）
//   - reporter：按量计费上报（finance_consumer 适配，可为 nil → 本期跳过扣费）
//   - scopeResolver：sk model_scope 越界校验（auth.APIKeyService.ModelScopeByID 适配，
//     可为 nil → sk 系统未就绪时退化为不校验，仅登录态/不限模型场景）（S2-丁4b）
func New(db *gorm.DB, tokenProviderKey, apiKeyHMACSecret string, assetGate service.AssetGate, reporter service.UsageReporter, scopeResolver service.ModelScopeResolver) (*Module, error) {
	cipher, err := crypto.New([]byte(tokenProviderKey))
	if err != nil {
		return nil, err
	}

	channelRepo := repository.NewChannelRepository(db)
	modelRepo := repository.NewTokenModelRepository(db)
	usageRepo := repository.NewUsageLogRepository(db)
	g2Repo := repository.NewG2Repository(db)
	catalogService := service.NewCatalogService(modelRepo)
	orchestrator := service.NewRequestOrchestrator(g2Repo, channelRepo, cipher).WithVisibilityChecker(catalogService)

	module := &Module{
		ChannelService: service.NewChannelService(channelRepo, cipher),
		CatalogService: catalogService,
		ForwardService: service.NewForwardService(modelRepo, channelRepo, usageRepo, cipher, assetGate, reporter, scopeResolver),
		UsageService:   service.NewUsageService(usageRepo),
		Orchestrator:   orchestrator,
	}
	// 未配置 HMAC 密钥时不注册 Project SK 管理能力，防止生成不可安全校验的密钥。
	if apiKeyHMACSecret != "" {
		module.ProjectService = service.NewProjectService(g2Repo, apiKeyHMACSecret).WithVisibilityChecker(catalogService)
	}
	return module, nil
}
