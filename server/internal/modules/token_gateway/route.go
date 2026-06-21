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
	usageSvc *service.UsageService,
	jwtSecret string,
	iamChecker middleware.IAMChecker,
	banChecker middleware.BanChecker,
	adminChecker middleware.AdminVerifiedChecker,
) {
	ch := handler.NewChannelHandler(channelSvc)
	mh := handler.NewModelHandler(catalogSvc)
	uh := handler.NewUsageHandler(usageSvc)

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

	// 全量用量流水（S2-丁2，§14.7）：可按 user_id/api_key_id/model/start/end 筛选。
	mux.Handle("GET /api/admin/token/usage", admin(uh.ListAll))
}

// RegisterUserRoutes 注册 token_gateway 用户端路由（双模式鉴权：平台 sk + 网页登录态）。
// 中间件统一用 RequireUserAuth：sk- 前缀走 apiKeyResolver 注入 userID+api_key_id，
// 否则走 JWT（含封禁/吊销检查）仅注入 userID；门面内做门禁/额度/转发。
//   - forwardSvc：核心转发服务（选渠道 + 转发上游 + 读 usage + 计费编排）
//   - jwtSecret：JWT 校验密钥
//   - banChecker：封禁/吊销黑名单检查
//   - apiKeyResolver：平台 sk 解析器（S2-甲3）；可为 nil（sk 系统未就绪时退化为纯 JWT）。
//     bootstrap 须仅在 apiKeyService 非 nil 时传入具体适配器，否则传字面 nil 接口，
//     避免「非 nil 接口包 nil 指针」导致中间件 nil 判断失效。
func RegisterUserRoutes(
	mux *http.ServeMux,
	forwardSvc *service.ForwardService,
	catalogSvc *service.CatalogService,
	usageSvc *service.UsageService,
	jwtSecret string,
	banChecker middleware.BanChecker,
	apiKeyResolver middleware.APIKeyResolver,
) {
	chatH := handler.NewChatHandler(forwardSvc)
	modelH := handler.NewModelHandler(catalogSvc)
	usageH := handler.NewUsageHandler(usageSvc)

	// 用户端中间件链：双模式鉴权（sk 或登录态 JWT，JWT 路径含封禁/吊销检查）。
	user := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireUserAuth(jwtSecret, banChecker, apiKeyResolver, http.HandlerFunc(next))
	}

	// 列出已上架（active）模型，供用户端选择（仅公开精简字段）。
	mux.Handle("GET /api/token/models", user(modelH.ListPublic))
	// OpenAI 兼容对话转发（支持非流式 + SSE 流式）。
	mux.Handle("POST /api/token/chat/completions", user(chatH.ChatCompletions))
	// 我的用量流水（S2-丁1，§14.3）：仅查本人，可选筛选 model/start/end。
	// 双模式：sk 调用按 sk 绑定的 user_id 过滤（与登录态一致只查本人）。
	mux.Handle("GET /api/token/usage", user(usageH.ListMine))
}
