package conversation

import (
	"gorm.io/gorm"

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
func New(db *gorm.DB, orch service.Orchestrator, summarizer service.Summarizer) *Module {
	repo := repository.NewConversationRepository(db)
	return &Module{
		Service: service.NewConversationService(repo, orch, summarizer),
	}
}
