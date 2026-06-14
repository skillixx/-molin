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

// RegisterRoutes 将 auth 模块路由注册到 mux。
// iamChecker 用于权限校验；scopeResolver 用于数据范围注入（管理员用户列表/详情接口）；
// redisClient 用于 D-22 修改绑定信息接口的限流。
func RegisterRoutes(mux *http.ServeMux, authSvc *service.AuthService, verifySvc *service.VerificationService, cfg config.Config, iamChecker middleware.IAMChecker, scopeResolver middleware.ScopeResolver, redisClient *redis.Client) {
	h := handler.NewAuthHandler(authSvc, verifySvc, cfg)

	// D-51：对验证码发送和密码重置接口按 IP 限流（每 IP 每分钟最多 10 次），防止短信轰炸和暴力枚举 OTP
	const sendCodeIPLimit = 10
	const sendCodeIPWindow = time.Minute
	rateLimitByIP := func(next http.HandlerFunc) http.Handler {
		return middleware.RateLimitByIP(redisClient, "send_code", sendCodeIPLimit, sendCodeIPWindow, http.HandlerFunc(next))
	}

	// 无需鉴权的接口
	mux.Handle("POST /api/auth/verification-codes/email", rateLimitByIP(h.SendEmailCode))
	mux.Handle("POST /api/auth/verification-codes/phone", rateLimitByIP(h.SendPhoneCode))
	mux.HandleFunc("POST /api/auth/register", h.Register)
	mux.HandleFunc("POST /api/auth/login/email", h.LoginEmail)
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

	// D-22：修改手机号/邮箱接口存在账号枚举风险，在 RequireAuth 之后叠加
	// 每用户每分钟最多 bindUpdateLimit 次的限流。
	const bindUpdateLimit = 5
	const bindUpdateWindow = time.Minute
	mux.Handle("PATCH /api/me/phone", middleware.RequireAuth(cfg.JWTSecret, authSvc,
		middleware.RateLimitByUser(redisClient, "update_phone", bindUpdateLimit, bindUpdateWindow, http.HandlerFunc(h.UpdatePhone))))
	mux.Handle("PATCH /api/me/email", middleware.RequireAuth(cfg.JWTSecret, authSvc,
		middleware.RateLimitByUser(redisClient, "update_email", bindUpdateLimit, bindUpdateWindow, http.HandlerFunc(h.UpdateEmail))))

	// 管理员双重认证接口（需登录 + user:manage 权限；本身不校验双重认证，这是完成认证的入口）
	adminAuth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(cfg.JWTSecret, authSvc,
			middleware.RequirePerm(iamChecker, "user:manage", http.HandlerFunc(next)))
	}
	mux.Handle("POST /api/admin/auth/verify-phone", adminAuth(h.AdminVerifyPhone))
	mux.Handle("POST /api/admin/auth/verify-email", adminAuth(h.AdminVerifyEmail))

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
}
