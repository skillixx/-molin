package auth

import (
	"net/http"

	"molin/server/internal/config"
	"molin/server/internal/middleware"
	"molin/server/internal/modules/auth/handler"
	"molin/server/internal/modules/auth/service"
)

// RegisterRoutes 将 auth 模块路由注册到 mux。
func RegisterRoutes(mux *http.ServeMux, authSvc *service.AuthService, verifySvc *service.VerificationService, cfg config.Config) {
	h := handler.NewAuthHandler(authSvc, verifySvc, cfg)

	// 无需鉴权的接口
	mux.HandleFunc("POST /api/auth/verification-codes/email", h.SendEmailCode)
	mux.HandleFunc("POST /api/auth/verification-codes/phone", h.SendPhoneCode)
	mux.HandleFunc("POST /api/auth/register/email", h.RegisterEmail)
	mux.HandleFunc("POST /api/auth/register/phone", h.RegisterPhone)
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

	// 管理员双重认证接口（需登录，角色校验由调用方判断）
	mux.Handle("POST /api/admin/auth/verify-phone", auth(h.AdminVerifyPhone))
	mux.Handle("POST /api/admin/auth/verify-email", auth(h.AdminVerifyEmail))
}
