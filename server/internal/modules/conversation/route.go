package conversation

import (
	"net/http"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/conversation/handler"
	"molin/server/internal/modules/conversation/service"
)

// RegisterRoutes 注册有状态会话用户端路由（仅登录态 JWT，含封禁/吊销检查）。
// 依赖编排引擎（workbench.ChatService）+ 摘要器（token_gateway.ForwardService），
// 二者在 token 网关启用时才可用；bootstrap 据此决定是否调用本函数。
func RegisterRoutes(mux *http.ServeMux, svc *service.ConversationService, jwtSecret string, banChecker middleware.BanChecker) {
	h := handler.NewConversationHandler(svc)
	user := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}
	mux.Handle("POST /api/conversations", user(h.Create))
	mux.Handle("GET /api/conversations", user(h.List))
	mux.Handle("GET /api/conversations/{id}", user(h.Get))
	mux.Handle("PATCH /api/conversations/{id}", user(h.Rename))
	mux.Handle("DELETE /api/conversations/{id}", user(h.Delete))
	mux.Handle("GET /api/conversations/{id}/messages", user(h.ListMessages))
	mux.Handle("POST /api/conversations/{id}/chat", user(h.Chat))
}
