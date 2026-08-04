package token_gateway

import (
	"net/http"

	"molin/server/internal/middleware"
	auditservice "molin/server/internal/modules/audit/service"
	"molin/server/internal/modules/token_gateway/handler"
	"molin/server/internal/modules/token_gateway/service"
)

// RegisterRoutes 将 token_gateway 管理端路由注册到 mux。
// 旧渠道/模型接口使用 token:manage，G4 治理接口使用 ai_gateway 细粒度权限，全部叠加管理员二次认证。
//   - iamChecker：按路由校验 token:manage 或对应 ai_gateway 权限
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
	billingSvc *service.AIBillingService,
	governanceSvc *service.GovernanceAdminService,
	g5AdminSvc *service.G5AdminService,
	auditSvc *auditservice.AuditService,
	jwtSecret string,
	iamChecker middleware.IAMChecker,
	banChecker middleware.BanChecker,
	adminChecker middleware.AdminVerifiedChecker,
) {
	ch := handler.NewChannelHandler(channelSvc).WithAudit(auditSvc)
	mh := handler.NewModelHandler(catalogSvc).WithAudit(auditSvc)
	uh := handler.NewUsageHandler(usageSvc)
	bh := handler.NewBillingHandler(billingSvc, auditSvc)
	gh := handler.NewGovernanceHandler(governanceSvc, auditSvc)
	g5h := handler.NewG5AdminHandler(g5AdminSvc, auditSvc)

	// 管理端中间件链：登录 + token:manage + 管理员双重认证
	admin := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamChecker, "token:manage",
				middleware.RequireAdminVerified(adminChecker, http.HandlerFunc(next))))
	}
	governanceAdmin := func(permission string, next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamChecker, permission,
				middleware.RequireAdminVerified(adminChecker, http.HandlerFunc(next))))
	}
	readAdmin := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequireAnyPerm(iamChecker, []string{"ai_gateway:view", "token:manage"},
				middleware.RequireAdminVerified(adminChecker, http.HandlerFunc(next))))
	}

	// G5 管理工作台。读取使用 view 权限，发布类写操作按模型、价格和路由拆分权限。
	mux.Handle("GET /api/admin/token/overview", governanceAdmin("ai_gateway:view", g5h.Dashboard))
	mux.Handle("GET /api/admin/token/models/{id}/versions", governanceAdmin("ai_gateway:view", g5h.ListModelReleases))
	mux.Handle("POST /api/admin/token/models/{id}/publish", governanceAdmin("ai_gateway:model_manage", g5h.PublishModel))
	mux.Handle("POST /api/admin/token/models/{id}/unpublish", governanceAdmin("ai_gateway:model_manage", g5h.UnpublishModel))
	mux.Handle("POST /api/admin/token/models/{id}/rollback", governanceAdmin("ai_gateway:model_manage", g5h.RollbackModel))
	mux.Handle("GET /api/admin/token/routes", governanceAdmin("ai_gateway:view", g5h.ListRoutes))
	mux.Handle("POST /api/admin/token/routes", governanceAdmin("ai_gateway:route_manage", g5h.CreateRoute))
	mux.Handle("PUT /api/admin/token/routes/{id}", governanceAdmin("ai_gateway:route_manage", g5h.UpdateRoute))
	mux.Handle("GET /api/admin/token/prices", governanceAdmin("ai_gateway:view", g5h.ListPrices))
	mux.Handle("POST /api/admin/token/prices", governanceAdmin("ai_gateway:price_manage", g5h.CreatePrice))
	mux.Handle("GET /api/admin/token/prices/{id}", governanceAdmin("ai_gateway:view", g5h.PriceDetail))
	mux.Handle("POST /api/admin/token/prices/{id}/approve", governanceAdmin("ai_gateway:price_manage", g5h.ApprovePrice))
	mux.Handle("POST /api/admin/token/prices/{id}/publish", governanceAdmin("ai_gateway:price_manage", g5h.PublishPrice))
	mux.Handle("POST /api/admin/token/prices/{id}/suspend", governanceAdmin("ai_gateway:price_manage", g5h.SuspendPrice))
	mux.Handle("POST /api/admin/token/prices/{id}/retire", governanceAdmin("ai_gateway:price_manage", g5h.RetirePrice))
	mux.Handle("POST /api/admin/token/prices/{id}/rollback", governanceAdmin("ai_gateway:price_manage", g5h.RollbackPrice))

	// 渠道管理
	mux.Handle("GET /api/admin/token/channels", readAdmin(ch.ListChannels))
	mux.Handle("POST /api/admin/token/channels", admin(ch.CreateChannel))
	mux.Handle("GET /api/admin/token/channels/{id}", readAdmin(ch.GetChannel))
	mux.Handle("PATCH /api/admin/token/channels/{id}", admin(ch.UpdateChannel))
	mux.Handle("POST /api/admin/token/channels/{id}/health-check", governanceAdmin("ai_gateway:route_manage", ch.CheckChannelHealth))
	mux.Handle("DELETE /api/admin/token/channels/{id}", admin(ch.DeleteChannel))

	// 对外模型目录管理
	mux.Handle("GET /api/admin/token/models", readAdmin(mh.ListModels))
	mux.Handle("POST /api/admin/token/models", admin(mh.CreateModel))
	mux.Handle("GET /api/admin/token/models/{id}", readAdmin(mh.GetModel))
	mux.Handle("PATCH /api/admin/token/models/{id}", admin(mh.UpdateModel))
	mux.Handle("DELETE /api/admin/token/models/{id}", admin(mh.DeleteModel))

	// 全量用量流水（S2-丁2，§14.7）：可按 user_id/api_key_id/model/start/end 筛选。
	mux.Handle("GET /api/admin/token/usage", admin(uh.ListAll))
	// 人工异常终结是高风险资金操作，必须经过 token:manage、管理员二次认证和前置审计。
	mux.Handle("POST /api/admin/token/billing/exceptions/{request_id}/resolve", admin(bh.ResolveException))
	// 内容违规免单补录只允许对账管理员执行：记录平台成本、释放用户预占，但绝不产生用户消费。
	mux.Handle("POST /api/admin/token/billing/content-policy/{request_id}/resolve", governanceAdmin("ai_gateway:reconcile_manage", bh.ResolveContentPolicyWaiver))

	// G4 内容安全、资源与预算治理，所有写操作由 Handler 在业务执行前写审计。
	mux.Handle("GET /api/admin/token/safety/policies", governanceAdmin("ai_gateway:view", gh.ListPolicies))
	mux.Handle("POST /api/admin/token/safety/policies", governanceAdmin("ai_gateway:safety_manage", gh.CreatePolicy))
	mux.Handle("POST /api/admin/token/safety/policies/{id}/publish", governanceAdmin("ai_gateway:safety_manage", gh.PublishPolicy))
	mux.Handle("POST /api/admin/token/safety/policies/{id}/rollback", governanceAdmin("ai_gateway:safety_manage", gh.RollbackPolicy))
	mux.Handle("GET /api/admin/token/safety/events", governanceAdmin("ai_gateway:view", gh.ListEvents))
	mux.Handle("GET /api/admin/token/safety/actions", governanceAdmin("ai_gateway:view", gh.ListSubjectActions))
	mux.Handle("POST /api/admin/token/safety/actions", governanceAdmin("ai_gateway:safety_manage", gh.SuspendSubject))
	mux.Handle("POST /api/admin/token/safety/actions/{id}/revoke", governanceAdmin("ai_gateway:safety_manage", gh.RevokeSubject))
	mux.Handle("GET /api/admin/token/safety/appeals", governanceAdmin("ai_gateway:view", gh.ListAppeals))
	mux.Handle("POST /api/admin/token/safety/appeals/{id}/resolve", governanceAdmin("ai_gateway:safety_manage", gh.ResolveAppeal))
	mux.Handle("GET /api/admin/token/resource-policies", governanceAdmin("ai_gateway:view", gh.ListResourcePolicies))
	mux.Handle("PUT /api/admin/token/resource-policies", governanceAdmin("ai_gateway:resource_manage", gh.PutResourcePolicy))
	mux.Handle("GET /api/admin/token/budget-policies", governanceAdmin("ai_gateway:view", gh.ListBudgetPolicies))
	mux.Handle("PUT /api/admin/token/budget-policies", governanceAdmin("ai_gateway:budget_manage", gh.PutBudgetPolicy))
	mux.Handle("GET /api/admin/token/budget-overrides", governanceAdmin("ai_gateway:view", gh.ListBudgetOverrides))
	mux.Handle("POST /api/admin/token/budget-overrides", governanceAdmin("ai_gateway:budget_manage", gh.CreateBudgetOverride))
	mux.Handle("GET /api/admin/token/budget-alerts", governanceAdmin("ai_gateway:view", gh.ListBudgetAlerts))
	mux.Handle("GET /api/admin/token/compensation-tasks", governanceAdmin("ai_gateway:view", gh.ListCompensationTasks))
	mux.Handle("POST /api/admin/token/compensation-tasks/{id}/resolve", governanceAdmin("ai_gateway:reconcile_manage", gh.ResolveCompensationTask))
	mux.Handle("POST /api/admin/token/outbox-events/{event_id}/requeue", governanceAdmin("ai_gateway:reconcile_manage", gh.RequeueDeadOutbox))
}

// RegisterUserRoutes 注册 token_gateway 用户端路由（Project SK + 网页登录态）。
// Project 管理接口使用 JWT；模型目录兼容 JWT 与 Project SK；公开对话接口必须使用 Project SK。
// G3 对话由 RequestOrchestrator 统一执行权限校验、上游调用、价格快照、钱包预占和可靠结算。
//   - forwardSvc：仅保留构造兼容性，公开对话不会回退到旧转发链路
//   - jwtSecret：JWT 校验密钥
//   - banChecker：封禁/吊销黑名单检查
//   - apiKeyResolver：平台 sk 解析器（S2-甲3）；可为 nil（sk 系统未就绪时退化为纯 JWT）。
//     bootstrap 须仅在 apiKeyService 非 nil 时传入具体适配器，否则传字面 nil 接口，
//     避免「非 nil 接口包 nil 指针」导致中间件 nil 判断失效。
func RegisterUserRoutes(
	mux *http.ServeMux,
	forwardSvc *service.ForwardService,
	orchestrator service.RequestOrchestrator,
	projectSvc *service.ProjectService,
	catalogSvc *service.CatalogService,
	usageSvc *service.UsageService,
	governanceSvc *service.GovernanceAdminService,
	auditSvc *auditservice.AuditService,
	jwtSecret string,
	banChecker middleware.BanChecker,
	apiKeyResolver middleware.APIKeyResolver,
) {
	chatH := handler.NewChatHandler(forwardSvc).WithOrchestrator(orchestrator)
	modelH := handler.NewModelHandler(catalogSvc)
	if access, ok := orchestrator.(handler.ModelAccessChecker); ok {
		modelH.WithAccess(access)
	}
	usageH := handler.NewUsageHandler(usageSvc)
	governanceH := handler.NewGovernanceHandler(governanceSvc, auditSvc)

	// 用户端中间件链：双模式鉴权（sk 或登录态 JWT，JWT 路径含封禁/吊销检查）。
	user := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireUserAuth(jwtSecret, banChecker, apiKeyResolver, http.HandlerFunc(next))
	}
	jwtUser := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}

	// Project 与 Project SK 只能由登录态管理，禁止使用 SK 自助轮换或扩大自身权限。
	if projectSvc != nil {
		projectH := handler.NewProjectHandler(projectSvc)
		mux.Handle("POST /api/token/projects", jwtUser(projectH.Create))
		mux.Handle("GET /api/token/projects", jwtUser(projectH.List))
		mux.Handle("GET /api/token/projects/{id}", jwtUser(projectH.Get))
		mux.Handle("PATCH /api/token/projects/{id}", jwtUser(projectH.Update))
		mux.Handle("POST /api/token/projects/{id}/keys", jwtUser(projectH.IssueKey))
		mux.Handle("GET /api/token/projects/{id}/keys", jwtUser(projectH.ListKeys))
		mux.Handle("POST /api/token/projects/{id}/keys/{key_id}/rotate", jwtUser(projectH.RotateKey))
		mux.Handle("DELETE /api/token/projects/{id}/keys/{key_id}", jwtUser(projectH.RevokeKey))
	}

	// 列出已上架（active）模型，供用户端选择（仅公开精简字段）。
	mux.Handle("GET /api/token/models", user(modelH.ListPublic))
	// OpenAI 兼容对话转发（仅 Project SK，支持非流式 + SSE 流式）。
	mux.Handle("POST /api/token/chat/completions", user(chatH.ChatCompletions))
	mux.Handle("GET /api/token/requests/{request_id}", user(chatH.RequestStatus))
	// 我的用量流水（S2-丁1，§14.3）：仅查本人，可选筛选 model/start/end。
	// 双模式：sk 调用按 sk 绑定的 user_id 过滤（与登录态一致只查本人）。
	mux.Handle("GET /api/token/usage", user(usageH.ListMine))
	mux.Handle("GET /api/token/safety/events", jwtUser(governanceH.ListUserEvents))
	mux.Handle("POST /api/token/safety/appeals", jwtUser(governanceH.CreateAppeal))

	// ---- OpenAI 兼容别名层（/v1/*）----
	// 让 Cline / Cherry Studio 等「OpenAI 兼容」客户端把 Base URL 填为 https://<域名>/v1，
	// 凭 Project SK 直接接入；复用同一套鉴权和模型权限校验，并由 RequestOrchestrator
	// 写入请求、执行、Usage 与财务事实；G3 先预占人民币钱包，再按可信 Usage 一次终态结算。
	// POST /v1/chat/completions：纯别名，复用现有 ChatCompletions handler（含 SSE 流式透传）。
	mux.Handle("POST /v1/chat/completions", user(chatH.ChatCompletions))
	mux.Handle("GET /v1/requests/{request_id}", user(chatH.RequestStatus))
	// GET /v1/models：返回 OpenAI 标准格式，供客户端自动拉取模型下拉列表。
	mux.Handle("GET /v1/models", user(modelH.ListOpenAIModels))
}
