package token_gateway

import (
	"net/http"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/handler"
	"molin/server/internal/modules/token_gateway/service"
)

// RegisterRoutes 将 token_gateway 管理端路由注册到 mux。
// 管理端接口统一用 RequireAuth + RequirePerm("token:manage") + RequireAdminVerified 包裹。
//   - iamChecker：权限校验（token:manage）
//   - banChecker：封禁黑名单检查
//   - adminChecker：管理员双重认证有效期校验
//   - jwtSecret：JWT 校验密钥
//
// 由 bootstrap 统一装配并传入已构造的 channelSvc / catalogSvc（含 AES-256-GCM cipher）。
func RegisterRoutes(
	mux *http.ServeMux,
	channelSvc *service.ChannelService,
	catalogSvc *service.CatalogService,
	jwtSecret string,
	iamChecker middleware.IAMChecker,
	banChecker middleware.BanChecker,
	adminChecker middleware.AdminVerifiedChecker,
) {
	ch := handler.NewChannelHandler(channelSvc)
	mh := handler.NewModelHandler(catalogSvc)

	// 管理端中间件链：登录 + token:manage + 管理员双重认证
	admin := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamChecker, "token:manage",
				middleware.RequireAdminVerified(adminChecker, http.HandlerFunc(next))))
	}

	// 渠道管理
	mux.Handle("GET /api/admin/token/channels", admin(ch.ListChannels))
	mux.Handle("POST /api/admin/token/channels", admin(ch.CreateChannel))
	mux.Handle("GET /api/admin/token/channels/{id}", admin(ch.GetChannel))
	mux.Handle("PATCH /api/admin/token/channels/{id}", admin(ch.UpdateChannel))
	mux.Handle("DELETE /api/admin/token/channels/{id}", admin(ch.DeleteChannel))

	// 对外模型目录管理
	mux.Handle("GET /api/admin/token/models", admin(mh.ListModels))
	mux.Handle("POST /api/admin/token/models", admin(mh.CreateModel))
	mux.Handle("GET /api/admin/token/models/{id}", admin(mh.GetModel))
	mux.Handle("PATCH /api/admin/token/models/{id}", admin(mh.UpdateModel))
	mux.Handle("DELETE /api/admin/token/models/{id}", admin(mh.DeleteModel))
}

// RegisterUserRoutes 注册 token_gateway 用户端路由（网页登录态）。
// 本期仅 chat 转发：RequireAuth(jwtSecret, banChecker) 注入 userID，门面内做门禁/额度/转发。
//   - forwardSvc：核心转发服务（选渠道 + 转发上游 + 读 usage + 计费编排）
//   - jwtSecret：JWT 校验密钥
//   - banChecker：封禁/吊销黑名单检查
//
// 说明：本期不做平台 sk（外部程序用 API Key）鉴权，待后端甲 sk 系统就绪后再扩展。
func RegisterUserRoutes(
	mux *http.ServeMux,
	forwardSvc *service.ForwardService,
	jwtSecret string,
	banChecker middleware.BanChecker,
) {
	chatH := handler.NewChatHandler(forwardSvc)

	// 用户端中间件链：仅登录态（含封禁/吊销检查）。
	user := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}

	// OpenAI 兼容对话转发（支持非流式 + SSE 流式）。
	mux.Handle("POST /api/token/chat/completions", user(chatH.ChatCompletions))
}
