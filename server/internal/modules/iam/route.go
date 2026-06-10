package iam

import (
	"net/http"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/iam/handler"
	"molin/server/internal/modules/iam/service"
)

// RegisterRoutes 将 iam 模块路由注册到 mux（所有接口需要登录 + role:manage 权限 + 管理员双重认证）。
// banChecker 用于封禁黑名单检查，adminChecker 用于校验管理员双重认证有效期。
func RegisterRoutes(mux *http.ServeMux, iamSvc *service.IAMService, jwtSecret string, banChecker middleware.BanChecker, adminChecker middleware.AdminVerifiedChecker) {
	h := handler.NewIAMHandler(iamSvc)

	// 需要登录 + role:manage 权限 + 管理员双重认证（手机+邮箱均在有效期内）
	admin := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamSvc, "role:manage",
				middleware.RequireAdminVerified(adminChecker, http.HandlerFunc(next))))
	}

	mux.Handle("GET /api/admin/roles", admin(h.ListRoles))
	mux.Handle("POST /api/admin/roles", admin(h.CreateRole))
	mux.Handle("PUT /api/admin/roles/{id}", admin(h.UpdateRole))
	mux.Handle("DELETE /api/admin/roles/{id}", admin(h.DeleteRole))
	mux.Handle("GET /api/admin/permissions", admin(h.ListPermissions))
	mux.Handle("GET /api/admin/users/{id}/roles", admin(h.GetUserRoles))
	mux.Handle("POST /api/admin/users/{id}/roles", admin(h.AssignRole))
	mux.Handle("DELETE /api/admin/users/{id}/roles/{role_id}", admin(h.RevokeRole))
	mux.Handle("GET /api/admin/users/{id}/permission-overrides", admin(h.GetPermissionOverrides))
	mux.Handle("POST /api/admin/users/{id}/permission-overrides", admin(h.SetPermissionOverride))
	mux.Handle("DELETE /api/admin/users/{id}/permission-overrides/{override_id}", admin(h.DeletePermissionOverride))
}
