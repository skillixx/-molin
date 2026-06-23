package workbench

import (
	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/crypto"
	"molin/server/internal/modules/workbench/repository"
	"molin/server/internal/modules/workbench/service"
)

// Module 聚合聊天工作台（Agent/Skill/Plugin + tool-use 编排）对外暴露的服务，便于 bootstrap 统一装配。
type Module struct {
	AgentService         *service.AgentService
	AgentCategoryService *service.AgentCategoryService
	SkillService         *service.SkillService
	PluginService        *service.PluginService
	MCPService           *service.MCPService

	// 编排依赖（W8）：skill 内置注册表 + plugin 转发器 + 各 repo，供 BuildChatService 组装编排服务。
	registry   *service.SkillRegistry
	forwarder  *service.PluginForwarder
	agentRepo  *repository.AgentRepository
	skillRepo  *repository.SkillRepository
	pluginRepo *repository.PluginRepository

	// MCP 工具源依赖（第三阶段）：repo + client + cipher + 通用计数器 + 白名单，供编排注入。
	mcpRepo        *repository.MCPRepository
	mcpClient      *service.MCPClient
	cipher         *crypto.AESGCM
	toolCallRepo   *repository.ToolCallRepository
	allowedDomains []string
}

// New 构造 workbench 模块依赖。
// pluginSecretKey 为 32 字节 AES-256-GCM 密钥（PLUGIN_SECRET_KEY 或回退 TOKEN_PROVIDER_KEY），用于 plugin 凭证加解密。
// allowedDomains 为外呼域名白名单（可空）；用于 plugin 转发器 + skill 联网的 SSRF 白名单。
func New(db *gorm.DB, pluginSecretKey string, allowedDomains []string) (*Module, error) {
	cipher, err := crypto.New([]byte(pluginSecretKey))
	if err != nil {
		return nil, err
	}

	agentRepo := repository.NewAgentRepository(db)
	categoryRepo := repository.NewAgentCategoryRepository(db)
	skillRepo := repository.NewSkillRepository(db)
	pluginRepo := repository.NewPluginRepository(db)
	callRepo := repository.NewToolCallRepository(db)
	mcpRepo := repository.NewMCPRepository(db)
	mcpClient := service.NewMCPClient()

	return &Module{
		AgentService:         service.NewAgentService(agentRepo, skillRepo, pluginRepo, categoryRepo),
		AgentCategoryService: service.NewAgentCategoryService(categoryRepo),
		SkillService:         service.NewSkillService(skillRepo),
		PluginService:        service.NewPluginService(pluginRepo, cipher),
		MCPService:           service.NewMCPService(mcpRepo, agentRepo, cipher, mcpClient, allowedDomains),

		registry:   service.NewSkillRegistry(allowedDomains),
		forwarder:  service.NewPluginForwarder(cipher, callRepo, pluginRepo, allowedDomains),
		agentRepo:  agentRepo,
		skillRepo:  skillRepo,
		pluginRepo: pluginRepo,

		mcpRepo:        mcpRepo,
		mcpClient:      mcpClient,
		cipher:         cipher,
		toolCallRepo:   callRepo,
		allowedDomains: allowedDomains,
	}, nil
}

// BuildChatService 组装 tool-use 编排服务（W8）。需注入上游单轮调用器（token_gateway.ForwardService）。
// upstream 为 nil 时返回 nil（token 网关未启用 → 编排端点不可用，bootstrap 据此不注册 chat 路由）。
func (m *Module) BuildChatService(upstream service.UpstreamChat, maxRounds int) *service.ChatService {
	if upstream == nil {
		return nil
	}
	cs := service.NewChatService(m.agentRepo, m.skillRepo, m.pluginRepo, m.registry, m.forwarder, upstream, maxRounds)
	// 注入 MCP 工具源（第二种工具源），编排 assembleTools/runTool 据此汇总并路由 MCP 工具。
	cs.WithMCP(m.mcpRepo, m.mcpClient, m.cipher, m.toolCallRepo, m.allowedDomains)
	return cs
}
