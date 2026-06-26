package conversation

import (
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	convcache "molin/server/internal/modules/conversation/cache"
	"molin/server/internal/modules/conversation/repository"
	"molin/server/internal/modules/conversation/service"
)

// Module 聚合有状态会话对外服务，便于 bootstrap 统一装配。
type Module struct {
	Service *service.ConversationService
}

// New 构造会话模块。
//   - orch：编排引擎（workbench.ChatService），复用其模型路由/工具/计费/可见性。
//   - summarizer：单轮模型调用（token_gateway.ForwardService），用于上下文压缩；nil 则禁用压缩。
//   - rdb：Redis 客户端，用作会话上下文热缓存；nil 则退化为纯 MySQL（fail-open）。
func New(db *gorm.DB, orch service.Orchestrator, summarizer service.Summarizer, rdb *redis.Client) *Module {
	repo := repository.NewConversationRepository(db)
	cache := convcache.NewConversationCache(rdb)
	return &Module{
		Service: service.NewConversationService(repo, orch, summarizer, cache),
	}
}
