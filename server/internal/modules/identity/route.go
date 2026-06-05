package identity

import (
	"net/http"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/identity/handler"
	"molin/server/internal/modules/identity/service"
)

// RegisterRoutes 将 identity 模块路由注册到 mux。
// banChecker 用于封禁黑名单检查，防止被封禁用户使用存量 Access Token 访问接口。
func RegisterRoutes(mux *http.ServeMux, identitySvc *service.IdentityService, iamSvc middleware.IAMChecker, jwtSecret string, banChecker middleware.BanChecker) {
	h := handler.NewIdentityHandler(identitySvc)

	// 需要登录的用户接口（同时检查封禁黑名单）
	requireAuth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}
	mux.Handle("POST /api/identity/verifications", requireAuth(h.Submit))
	mux.Handle("GET /api/identity/verifications/me", requireAuth(h.GetMyVerification))

	// 需要登录 + identity:review 权限的管理员接口（同时检查封禁黑名单）
	adminAuth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamSvc, "identity:review", http.HandlerFunc(next)))
	}
	mux.Handle("GET /api/admin/identity-verifications", adminAuth(h.ListPending))
	mux.Handle("GET /api/admin/identity-verifications/{id}", adminAuth(h.GetDetail))
	mux.Handle("PATCH /api/admin/identity-verifications/{id}/review", adminAuth(h.Review))
}
