package iam

import (
	"net/http"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/iam/handler"
	"molin/server/internal/modules/iam/service"
)

// RegisterRoutes 将 iam 模块路由注册到 mux。
// banChecker 用于封禁黑名单检查，adminChecker 用于校验管理员双重认证有效期。
func RegisterRoutes(mux *http.ServeMux, iamSvc *service.IAMService, groupSvc *service.GroupService, jwtSecret string, banChecker middleware.BanChecker, adminChecker middleware.AdminVerifiedChecker) {
	h := handler.NewIAMHandler(iamSvc)
	gh := handler.NewGroupHandler(groupSvc)

	// 需要登录 + role:manage 权限 + 管理员双重认证
	admin := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamSvc, "role:manage",
				middleware.RequireAdminVerified(adminChecker, http.HandlerFunc(next))))
	}

	// 需要登录 + group:manage 权限 + 管理员双重认证
	adminGroup := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamSvc, "group:manage",
				middleware.RequireAdminVerified(adminChecker, http.HandlerFunc(next))))
	}

	// 角色/权限/用户角色分配（原有路由，不变）
	mux.Handle("GET /api/admin/roles", admin(h.ListRoles))
	mux.Handle("POST /api/admin/roles", admin(h.CreateRole))
	// BUG-04 修复：补充注册 GET /api/admin/roles/{id}，此前未注册导致请求落到其他路由返回 405
	mux.Handle("GET /api/admin/roles/{id}", admin(h.GetRole))
	mux.Handle("PUT /api/admin/roles/{id}", admin(h.UpdateRole))
	mux.Handle("DELETE /api/admin/roles/{id}", admin(h.DeleteRole))
	mux.Handle("GET /api/admin/permissions", admin(h.ListPermissions))
	// BUG-05 修复：补充注册 GET /api/admin/audit-logs，此前未注册导致 404
	mux.Handle("GET /api/admin/audit-logs", admin(h.ListAuditLogs))
	mux.Handle("GET /api/admin/users/{id}/roles", admin(h.GetUserRoles))
	mux.Handle("POST /api/admin/users/{id}/roles", admin(h.AssignRole))
	mux.Handle("DELETE /api/admin/users/{id}/roles/{role_id}", admin(h.RevokeRole))
	mux.Handle("GET /api/admin/users/{id}/permission-overrides", admin(h.GetPermissionOverrides))
	mux.Handle("POST /api/admin/users/{id}/permission-overrides", admin(h.SetPermissionOverride))
	mux.Handle("DELETE /api/admin/users/{id}/permission-overrides/{override_id}", admin(h.DeletePermissionOverride))

	// 用户分组管理（Phase 1，需 group:manage 权限）
	mux.Handle("GET /api/admin/user-groups", adminGroup(gh.ListGroups))
	mux.Handle("POST /api/admin/user-groups", adminGroup(gh.CreateGroup))
	mux.Handle("GET /api/admin/user-groups/{id}", adminGroup(gh.GetGroup))
	mux.Handle("PUT /api/admin/user-groups/{id}", adminGroup(gh.UpdateGroup))
	mux.Handle("DELETE /api/admin/user-groups/{id}", adminGroup(gh.DeleteGroup))

	// 成员管理
	mux.Handle("GET /api/admin/user-groups/{id}/members", adminGroup(gh.ListMembers))
	mux.Handle("POST /api/admin/user-groups/{id}/members", adminGroup(gh.AddMember))
	mux.Handle("PATCH /api/admin/user-groups/{id}/members/{uid}", adminGroup(gh.UpdateMemberRole))
	mux.Handle("DELETE /api/admin/user-groups/{id}/members/{uid}", adminGroup(gh.RemoveMember))

	// 用户所在分组（也用 group:manage，未来 Phase 3 可按需放开）
	mux.Handle("GET /api/admin/users/{id}/groups", adminGroup(gh.GetUserGroups))

	// 组权限
	mux.Handle("GET /api/admin/user-groups/{id}/permissions", adminGroup(gh.ListGroupPermissions))
	mux.Handle("POST /api/admin/user-groups/{id}/permissions", adminGroup(gh.AddGroupPermission))
	mux.Handle("DELETE /api/admin/user-groups/{id}/permissions/{code}", adminGroup(gh.RemoveGroupPermission))

	// 邀请码
	mux.Handle("GET /api/admin/user-groups/{id}/invite-codes", adminGroup(gh.ListInviteCodes))
	mux.Handle("POST /api/admin/user-groups/{id}/invite-codes", adminGroup(gh.CreateInviteCode))
	mux.Handle("PATCH /api/admin/user-groups/{id}/invite-codes/{invite_id}/disable", adminGroup(gh.DisableInviteCode))
}
