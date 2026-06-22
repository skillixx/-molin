package workbench

import (
	"net/http"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/workbench/handler"
	"molin/server/internal/modules/workbench/service"
)

// RegisterRoutes 注册聊天工作台管理端路由（运营配置官方 Agent/Skill/Plugin）。
// 管理端统一用 RequireAuth + RequirePerm(对应权限码) + RequireAdminVerified 包裹。
//   - agents/{id}/skills|plugins 绑定接口用 agent:manage（契约 §3.1）。
func RegisterRoutes(
	mux *http.ServeMux,
	agentSvc *service.AgentService,
	skillSvc *service.SkillService,
	pluginSvc *service.PluginService,
	jwtSecret string,
	iamChecker middleware.IAMChecker,
	banChecker middleware.BanChecker,
	adminChecker middleware.AdminVerifiedChecker,
) {
	ah := handler.NewAgentHandler(agentSvc)
	sh := handler.NewSkillHandler(skillSvc)
	ph := handler.NewPluginHandler(pluginSvc)

	// 管理端中间件链工厂：登录 + 指定权限码 + 管理员双重认证。
	admin := func(perm string, next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamChecker, perm,
				middleware.RequireAdminVerified(adminChecker, http.HandlerFunc(next))))
	}

	// Agent 管理（agent:manage）
	mux.Handle("GET /api/admin/agents", admin("agent:manage", ah.AdminList))
	mux.Handle("POST /api/admin/agents", admin("agent:manage", ah.AdminCreate))
	mux.Handle("GET /api/admin/agents/{id}", admin("agent:manage", ah.AdminGet))
	mux.Handle("PATCH /api/admin/agents/{id}", admin("agent:manage", ah.AdminUpdate))
	mux.Handle("DELETE /api/admin/agents/{id}", admin("agent:manage", ah.AdminDelete))
	mux.Handle("POST /api/admin/agents/{id}/skills", admin("agent:manage", ah.BindSkills))
	mux.Handle("POST /api/admin/agents/{id}/plugins", admin("agent:manage", ah.BindPlugins))

	// Skill 管理（skill:manage）
	mux.Handle("GET /api/admin/skills", admin("skill:manage", sh.List))
	mux.Handle("POST /api/admin/skills", admin("skill:manage", sh.Create))
	mux.Handle("GET /api/admin/skills/{id}", admin("skill:manage", sh.Get))
	mux.Handle("PATCH /api/admin/skills/{id}", admin("skill:manage", sh.Update))
	mux.Handle("DELETE /api/admin/skills/{id}", admin("skill:manage", sh.Delete))

	// Plugin 管理（plugin:manage）
	mux.Handle("GET /api/admin/plugins", admin("plugin:manage", ph.List))
	mux.Handle("POST /api/admin/plugins", admin("plugin:manage", ph.Create))
	mux.Handle("GET /api/admin/plugins/{id}", admin("plugin:manage", ph.Get))
	mux.Handle("PATCH /api/admin/plugins/{id}", admin("plugin:manage", ph.Update))
	mux.Handle("DELETE /api/admin/plugins/{id}", admin("plugin:manage", ph.Delete))
}

// RegisterUserRoutes 注册聊天工作台用户端路由（仅登录态 JWT）。
// 用户选用/自建 Agent + 查看可绑定的 skill/plugin 列表；编排 chat 端点（§3.3）属 W8，不在此注册。
func RegisterUserRoutes(
	mux *http.ServeMux,
	agentSvc *service.AgentService,
	skillSvc *service.SkillService,
	pluginSvc *service.PluginService,
	jwtSecret string,
	banChecker middleware.BanChecker,
) {
	ah := handler.NewAgentHandler(agentSvc)
	sh := handler.NewSkillHandler(skillSvc)
	ph := handler.NewPluginHandler(pluginSvc)

	// 用户端中间件链：仅登录态 JWT（含封禁/吊销检查）。
	user := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}

	// Agent：列可用 + 详情 + 自建 + 改/删本人自建
	mux.Handle("GET /api/agents", user(ah.UserList))
	mux.Handle("POST /api/agents", user(ah.UserCreate))
	mux.Handle("GET /api/agents/{id}", user(ah.UserGet))
	mux.Handle("PATCH /api/agents/{id}", user(ah.UserUpdate))
	mux.Handle("DELETE /api/agents/{id}", user(ah.UserDelete))

	// 可绑定能力只读列表（供自建 Agent 选择）
	mux.Handle("GET /api/skills", user(sh.ListPublic))
	mux.Handle("GET /api/plugins", user(ph.ListPublic))
}
