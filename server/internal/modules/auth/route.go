package auth

import (
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"molin/server/internal/config"
	"molin/server/internal/middleware"
	"molin/server/internal/modules/auth/handler"
	"molin/server/internal/modules/auth/service"
)

const emailLoginCodeIPLimit = 10
const emailLoginCodeIPWindow = time.Minute

// limitEmailCodeLoginByIP 为邮箱验证码登录建立独立 IP 桶，避免与发码次数互相挤占。
func limitEmailCodeLoginByIP(redisClient *redis.Client, resolver middleware.PublicSourceIPResolver, next http.Handler) http.Handler {
	return middleware.RateLimitEmailByIP(redisClient, resolver, "login_email_code", emailLoginCodeIPLimit, emailLoginCodeIPWindow, next)
}

// RegisterRoutes 将 auth 模块路由注册到 mux。
// iamChecker 用于权限校验；scopeResolver 用于数据范围注入（管理员用户列表/详情接口）；
// redisClient 用于 D-22 修改绑定信息接口的限流。
// apiKeySvc（S2-甲4）：平台 API Key（sk）管理服务，可为 nil（sk 系统未装配时不注册 /api/keys 路由，灰度安全）。
func RegisterRoutes(mux *http.ServeMux, authSvc *service.AuthService, verifySvc *service.VerificationService, emailSvc *service.EmailService, cfg config.Config, iamChecker middleware.IAMChecker, scopeResolver middleware.ScopeResolver, redisClient *redis.Client, publicSourceIP middleware.PublicSourceIPResolver, apiKeySvc *service.APIKeyService, smsMetrics ...handler.SMSMetricsReader) *handler.MetricsHandler {
	h := handler.NewAuthHandler(authSvc, verifySvc, cfg)
	// 内部指标使用无方法模式注册，由处理器显式拒绝 HEAD 等所有非 GET 方法。
	metricsHandler := handler.NewMetricsHandler(emailSvc, cfg, smsMetrics...)
	mux.Handle("/api/internal/metrics", metricsHandler)

	// D-51：对验证码发送和密码重置接口按 IP 限流（每 IP 每分钟最多 10 次），防止短信轰炸和暴力枚举 OTP
	const sendCodeIPLimit = 10
	const sendCodeIPWindow = time.Minute
	rateLimitByIP := func(next http.HandlerFunc) http.Handler {
		return middleware.RateLimitVerificationByIP(redisClient, publicSourceIP, "send_code", sendCodeIPLimit, sendCodeIPWindow, http.HandlerFunc(next))
	}
	rateLimitEmailByIP := func(next http.Handler) http.Handler {
		return middleware.RateLimitEmailByIP(redisClient, publicSourceIP, "send_email_code", sendCodeIPLimit, sendCodeIPWindow, next)
	}
	rateLimitEmailLoginByIP := func(next http.Handler) http.Handler {
		return limitEmailCodeLoginByIP(redisClient, publicSourceIP, next)
	}

	// 无需鉴权的接口
	mux.Handle("POST /api/auth/verification-codes/email", rateLimitEmailByIP(http.HandlerFunc(h.SendEmailCode)))
	mux.Handle("POST /api/auth/verification-codes/phone", rateLimitByIP(h.SendPhoneCode))
	mux.HandleFunc("POST /api/auth/register", h.Register)
	mux.HandleFunc("POST /api/auth/login/email", h.LoginEmail)
	// 邮箱验证码登录同样属于公开认证入口，按可信来源 IP 使用 Redis 原子计数限制为每分钟十次。
	// Redis 故障时沿用邮件入口的既有策略：IP 维度降级放行，由验证码的一次性消费和账户维度保护继续兜底。
	mux.Handle("POST /api/auth/login/email/code", rateLimitEmailLoginByIP(http.HandlerFunc(h.LoginEmailCode)))
	mux.HandleFunc("POST /api/auth/login/phone", h.LoginPhone)
	mux.HandleFunc("POST /api/auth/refresh", h.Refresh)
	mux.Handle("POST /api/auth/password/reset", rateLimitByIP(h.ResetPassword))

	// 需要登录的接口（同时检查封禁黑名单）
	auth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(cfg.JWTSecret, authSvc, http.HandlerFunc(next))
	}
	mux.Handle("POST /api/auth/logout", auth(h.Logout))
	mux.Handle("GET /api/me", auth(h.GetMe))
	// A-10：当前登录用户最终生效权限码集合（仅需登录，无需额外权限码）
	mux.Handle("GET /api/me/permissions", auth(h.GetMyPermissions))
	mux.Handle("PATCH /api/me/password", auth(h.ChangePassword))
	mux.Handle("PATCH /api/me/username", auth(h.UpdateUsername))
	// A-27：修改个人资料（昵称/头像），PATCH 语义
	mux.Handle("PATCH /api/me/profile", auth(h.UpdateProfile))

	// S2-甲4：平台 API Key（sk）用户端自助管理（登录态 JWT，不能用 sk 自助管理 sk）。
	// apiKeySvc 为 nil 时跳过注册（与 bootstrap 灰度装配保持一致）。
	if apiKeySvc != nil {
		keyH := handler.NewAPIKeyHandler(apiKeySvc)
		mux.Handle("POST /api/keys", auth(keyH.IssueKey))
		mux.Handle("GET /api/keys", auth(keyH.ListKeys))
		mux.Handle("DELETE /api/keys/{id}", auth(keyH.RevokeKey))
	}

	// D-22：修改手机号/邮箱接口存在账号枚举风险，在 RequireAuth 之后叠加
	// 每用户每分钟最多 bindUpdateLimit 次的限流。
	const bindUpdateLimit = 5
	const bindUpdateWindow = time.Minute
	mux.Handle("PATCH /api/me/phone", middleware.RequireAuth(cfg.JWTSecret, authSvc,
		middleware.RateLimitByUser(redisClient, "update_phone", bindUpdateLimit, bindUpdateWindow, http.HandlerFunc(h.UpdatePhone))))
	mux.Handle("PATCH /api/me/email", middleware.RequireAuth(cfg.JWTSecret, authSvc,
		middleware.RateLimitByUser(redisClient, "update_email", bindUpdateLimit, bindUpdateWindow, http.HandlerFunc(h.UpdateEmail))))

	// D-96：已登录用户更换手机号/邮箱前发送验证码（scene 固定为 bind_phone/bind_email，发送目标为新手机号/邮箱）。
	// 手机沿用每用户限流；邮件统一叠加每 IP 与服务层 HMAC 账户维度的每分钟十次限流。
	mux.Handle("POST /api/me/verification-codes/phone", middleware.RequireAuth(cfg.JWTSecret, authSvc,
		middleware.RateLimitByUser(redisClient, "send_bind_phone_code", bindUpdateLimit, bindUpdateWindow, http.HandlerFunc(h.SendBindPhoneCode))))
	mux.Handle("POST /api/me/verification-codes/email", middleware.RequireAuth(cfg.JWTSecret, authSvc,
		rateLimitEmailByIP(http.HandlerFunc(h.SendBindEmailCode))))

	// 管理员双重认证接口（需登录 + user:manage 权限；本身不校验双重认证，这是完成认证的入口）
	adminAuth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(cfg.JWTSecret, authSvc,
			middleware.RequirePerm(iamChecker, "user:manage", http.HandlerFunc(next)))
	}
	mux.Handle("POST /api/admin/auth/verify-phone", adminAuth(h.AdminVerifyPhone))
	mux.Handle("POST /api/admin/auth/verify-email", adminAuth(h.AdminVerifyEmail))
	// D-96：管理员双重认证发送验证码（scene=admin_verify，目标为管理员自己的手机号/邮箱）
	mux.Handle("POST /api/admin/auth/verification-codes/phone", adminAuth(h.SendAdminVerifyPhoneCode))
	mux.Handle("POST /api/admin/auth/verification-codes/email", middleware.RequireAuth(cfg.JWTSecret, authSvc,
		middleware.RequirePerm(iamChecker, "user:manage",
			rateLimitEmailByIP(http.HandlerFunc(h.SendAdminVerifyEmailCode)))))

	// 管理员封禁/解封用户（需登录 + user:manage 权限 + 双重认证）
	adminAuthVerified := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(cfg.JWTSecret, authSvc,
			middleware.RequirePerm(iamChecker, "user:manage",
				middleware.RequireAdminVerified(authSvc, http.HandlerFunc(next))))
	}
	mux.Handle("PATCH /api/admin/users/{id}/status", adminAuthVerified(h.UpdateUserStatus))

	// 管理员用户列表和详情（需登录 + user:list 权限 + 双重认证 + 数据范围注入）
	adminUserList := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(cfg.JWTSecret, authSvc,
			middleware.RequirePerm(iamChecker, "user:list",
				middleware.RequireAdminVerified(authSvc,
					middleware.InjectScope(scopeResolver, http.HandlerFunc(next)))))
	}
	mux.Handle("GET /api/admin/users", adminUserList(h.ListUsers))
	mux.Handle("GET /api/admin/users/{id}", adminUserList(h.GetUser))

	// A-28：管理员创建后台用户（需登录 + user:manage 权限 + 双重认证）
	mux.Handle("POST /api/admin/users", adminAuthVerified(h.CreateAdminUser))
	// A-29：管理员修改用户邮箱/手机号/状态（需登录 + user:manage 权限 + 双重认证）
	mux.Handle("PATCH /api/admin/users/{id}", adminAuthVerified(h.UpdateAdminUser))
	// A-30：管理员查看用户登录日志分页（需登录 + user:list 权限 + 双重认证）
	mux.Handle("GET /api/admin/users/{id}/login-logs", adminUserList(h.ListUserLoginLogs))

	// DirectMail 邮件模板管理接口统一要求登录、细分权限和管理员双重认证。
	if emailSvc != nil {
		emailH := handler.NewEmailHandler(emailSvc)
		emailAdmin := func(perm string, next http.HandlerFunc) http.Handler {
			return middleware.RequireAuth(cfg.JWTSecret, authSvc,
				middleware.RequireEmailPerm(iamChecker, perm,
					middleware.RequireEmailAdminVerified(authSvc, http.HandlerFunc(next))))
		}
		mux.Handle("GET /api/admin/email/summary", emailAdmin("email:template:view", emailH.Summary))
		mux.Handle("GET /api/admin/email/templates", emailAdmin("email:template:view", emailH.ListTemplates))
		mux.Handle("GET /api/admin/email/templates/{id}", emailAdmin("email:template:view", emailH.GetTemplate))
		mux.Handle("PATCH /api/admin/email/templates/{id}/status", emailAdmin("email:template:manage", emailH.SetTemplateStatus))
		mux.Handle("GET /api/admin/email/scenes", emailAdmin("email:template:view", emailH.ListScenes))
		mux.Handle("PUT /api/admin/email/scenes/{scene}", emailAdmin("email:template:manage", emailH.SetScene))
		mux.Handle("POST /api/admin/email/templates/sync", emailAdmin("email:template:sync", emailH.Sync))
		mux.Handle("GET /api/admin/email/template-sync-runs", emailAdmin("email:template:view", emailH.ListSyncRuns))
		mux.Handle("GET /api/admin/email/test-recipient-allowlist", emailAdmin("email:template:view", emailH.ListAllowlist))
		mux.Handle("POST /api/admin/email/test-recipient-allowlist", emailAdmin("email:template:manage", emailH.AddAllowlist))
		mux.Handle("DELETE /api/admin/email/test-recipient-allowlist/{id}", emailAdmin("email:template:manage", emailH.RevokeAllowlist))
		mux.Handle("POST /api/admin/email/templates/{id}/test-send", emailAdmin("email:template:test", emailH.TestSend))
		mux.Handle("GET /api/admin/email/send-logs", emailAdmin("email:template:view", emailH.ListSendLogs))
	}
	return metricsHandler
}

// RegisterEmailBootstrapRoute 只在启动期严格配置明确启用时注册一次性内部入口。
// 路由关闭时 ServeMux 中完全不存在该路径，因此所有方法统一返回 404。
func RegisterEmailBootstrapRoute(mux *http.ServeMux, svc *service.EmailBootstrapService, cfg config.Config, authSvc *service.AuthService, iamChecker middleware.IAMChecker, directRoleChecker middleware.DirectRoleChecker) {
	bootstrapCfg := cfg.EmailAdminVerifyBootstrap
	if !bootstrapCfg.Enabled || svc == nil {
		return
	}
	h := handler.NewEmailBootstrapHandler(svc)
	var endpoint http.Handler = http.HandlerFunc(h.ConfigureAdminVerify)
	endpoint = middleware.RequireAdminPhoneVerified(authSvc, endpoint)
	endpoint = middleware.RequireEmailPerm(iamChecker, "email:template:bootstrap", endpoint)
	endpoint = middleware.RequireDirectAdminRole(directRoleChecker, endpoint)
	endpoint = middleware.RequireAuth(cfg.JWTSecret, authSvc, endpoint)
	endpoint = middleware.RequireStrictAuthorization(endpoint)
	endpoint = middleware.EmailBootstrapNetworkGate(bootstrapCfg.Token, bootstrapCfg.AllowedIPs, bootstrapCfg.TrustedProxyIPs, endpoint)
	mux.Handle("/api/internal/email/bootstrap/admin-verify", endpoint)
}
