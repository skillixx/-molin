package auth

import (
	"net/http"

	"molin/server/internal/config"
	"molin/server/internal/middleware"
	"molin/server/internal/modules/auth/handler"
	"molin/server/internal/modules/auth/service"
)

// RegisterRoutes 将 auth 模块路由注册到 mux。
// iamChecker 用于管理员双重认证接口的角色权限校验。
func RegisterRoutes(mux *http.ServeMux, authSvc *service.AuthService, verifySvc *service.VerificationService, cfg config.Config, iamChecker middleware.IAMChecker) {
	h := handler.NewAuthHandler(authSvc, verifySvc, cfg)

	// 无需鉴权的接口
	mux.HandleFunc("POST /api/auth/verification-codes/email", h.SendEmailCode)
	mux.HandleFunc("POST /api/auth/verification-codes/phone", h.SendPhoneCode)
	mux.HandleFunc("POST /api/auth/register", h.Register)
	mux.HandleFunc("POST /api/auth/login/email", h.LoginEmail)
	mux.HandleFunc("POST /api/auth/login/phone", h.LoginPhone)
	mux.HandleFunc("POST /api/auth/refresh", h.Refresh)
	mux.HandleFunc("POST /api/auth/password/reset", h.ResetPassword)

	// 需要登录的接口（同时检查封禁黑名单）
	auth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(cfg.JWTSecret, authSvc, http.HandlerFunc(next))
	}
	mux.Handle("POST /api/auth/logout", auth(h.Logout))
	mux.Handle("GET /api/me", auth(h.GetMe))
	mux.Handle("PATCH /api/me/password", auth(h.ChangePassword))
	mux.Handle("PATCH /api/me/username", auth(h.UpdateUsername))
	mux.Handle("PATCH /api/me/phone", auth(h.UpdatePhone))
	mux.Handle("PATCH /api/me/email", auth(h.UpdateEmail))

	// 管理员双重认证接口（需登录 + user:manage 权限，仅限管理员账号）
	adminAuth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(cfg.JWTSecret, authSvc,
			middleware.RequirePerm(iamChecker, "user:manage", http.HandlerFunc(next)))
	}
	mux.Handle("POST /api/admin/auth/verify-phone", adminAuth(h.AdminVerifyPhone))
	mux.Handle("POST /api/admin/auth/verify-email", adminAuth(h.AdminVerifyEmail))

	// 管理员封禁/解封用户（需登录 + user:manage 权限）
	mux.Handle("PATCH /api/admin/users/{id}/status", adminAuth(h.UpdateUserStatus))
}
