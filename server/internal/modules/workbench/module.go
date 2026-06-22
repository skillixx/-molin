package workbench

import (
	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/crypto"
	"molin/server/internal/modules/workbench/repository"
	"molin/server/internal/modules/workbench/service"
)

// Module 聚合聊天工作台（Agent/Skill/Plugin）对外暴露的服务，便于 bootstrap 统一装配。
type Module struct {
	AgentService  *service.AgentService
	SkillService  *service.SkillService
	PluginService *service.PluginService
}

// New 构造 workbench 模块依赖。
// pluginSecretKey 为 32 字节 AES-256-GCM 密钥（来自 config.PluginSecretKey / PLUGIN_SECRET_KEY，
// 可复用 TOKEN_PROVIDER_KEY）；用于 plugin auth_config 加解密。密钥非法时返回错误，由 bootstrap 决定是否致命。
func New(db *gorm.DB, pluginSecretKey string) (*Module, error) {
	cipher, err := crypto.New([]byte(pluginSecretKey))
	if err != nil {
		return nil, err
	}

	agentRepo := repository.NewAgentRepository(db)
	skillRepo := repository.NewSkillRepository(db)
	pluginRepo := repository.NewPluginRepository(db)

	return &Module{
		AgentService:  service.NewAgentService(agentRepo, skillRepo, pluginRepo),
		SkillService:  service.NewSkillService(skillRepo),
		PluginService: service.NewPluginService(pluginRepo, cipher),
	}, nil
}
