package identity

import (
	"net/http"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/identity/handler"
	"molin/server/internal/modules/identity/service"
)

// RegisterRoutes 将 identity 模块路由注册到 mux。
// banChecker 用于封禁黑名单检查，adminChecker 用于校验管理员双重认证有效期。
func RegisterRoutes(mux *http.ServeMux, identitySvc *service.IdentityService, iamSvc middleware.IAMChecker, jwtSecret string, banChecker middleware.BanChecker, adminChecker middleware.AdminVerifiedChecker) {
	h := handler.NewIdentityHandler(identitySvc)

	// 需要登录的用户接口（同时检查封禁黑名单）
	requireAuth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}
	mux.Handle("POST /api/identity/verifications", requireAuth(h.Submit))
	mux.Handle("GET /api/identity/verifications/me", requireAuth(h.GetMyVerification))

	// 需要登录 + identity:review 权限 + 管理员双重认证的审核接口
	adminAuth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamSvc, "identity:review",
				middleware.RequireAdminVerified(adminChecker, http.HandlerFunc(next))))
	}
	mux.Handle("GET /api/admin/identity-verifications", adminAuth(h.ListPending))
	mux.Handle("GET /api/admin/identity-verifications/{id}", adminAuth(h.GetDetail))
	mux.Handle("PATCH /api/admin/identity-verifications/{id}/review", adminAuth(h.Review))
}
