package middleware

import (
	"context"
	"net/http"

	"molin/server/pkg/response"
)

// IAMChecker 是权限校验接口，由 iam.IAMService 实现。
// 在 middleware 包中定义以避免循环导入。
type IAMChecker interface {
	CheckPermission(ctx context.Context, userID uint64, permCode string) bool
}

// AdminVerifiedChecker 管理员双重认证状态校验接口，由 auth.AuthService 实现。
// 在 middleware 包中定义以避免循环导入。
type AdminVerifiedChecker interface {
	IsAdminVerified(ctx context.Context, userID uint64) bool
}

// DirectRoleChecker 只认用户直接关联的系统角色，不接受分组继承或权限覆盖。
type DirectRoleChecker interface {
	HasDirectRole(ctx context.Context, userID uint64, roleCode string) bool
}

// AdminPhoneVerifiedChecker 校验管理员当前手机 MFA 状态。
type AdminPhoneVerifiedChecker interface {
	IsAdminPhoneVerified(ctx context.Context, userID uint64) bool
}

// RequirePerm 在 RequireAuth 之后使用，校验当前用户是否拥有 permCode 权限。
func RequirePerm(iamSvc IAMChecker, permCode string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := UserIDFromContext(r.Context())
		if !iamSvc.CheckPermission(r.Context(), userID, permCode) {
			response.Error(w, http.StatusForbidden, 40003, "无操作权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireEmailPerm 为邮件模块冻结独立权限文案，不改变全局 RequirePerm 的历史响应。
func RequireEmailPerm(iamSvc IAMChecker, permCode string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := UserIDFromContext(r.Context())
		if !iamSvc.CheckPermission(r.Context(), userID, permCode) {
			response.Error(w, http.StatusForbidden, 40003, "无权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireDirectAdminRole 要求当前用户直接关联平台 admin 角色。
func RequireDirectAdminRole(checker DirectRoleChecker, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if checker == nil || !checker.HasDirectRole(r.Context(), UserIDFromContext(r.Context()), "admin") {
			response.Error(w, http.StatusForbidden, 40003, "无权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdminPhoneVerified 要求手机 MFA 当前有效，但不要求邮箱 MFA。
func RequireAdminPhoneVerified(checker AdminPhoneVerifiedChecker, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if checker == nil || !checker.IsAdminPhoneVerified(r.Context(), UserIDFromContext(r.Context())) {
			response.Error(w, http.StatusForbidden, 40003, "请先完成手机号认证")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdminVerified 在 RequireAuth + RequirePerm 之后使用，
// 要求管理员已完成双重认证（手机+邮箱均在 ADMIN_VERIFY_EXPIRE_HOURS 有效期内）。
// verify-phone / verify-email 接口本身不应套此中间件。
func RequireAdminVerified(checker AdminVerifiedChecker, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := UserIDFromContext(r.Context())
		if !checker.IsAdminVerified(r.Context(), userID) {
			response.Error(w, http.StatusForbidden, 40031, "请先完成管理员双重认证（手机+邮箱）")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireEmailAdminVerified 为邮件管理契约提供固定错误码与文案，避免改变其他模块仍依赖的历史响应。
func RequireEmailAdminVerified(checker AdminVerifiedChecker, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := UserIDFromContext(r.Context())
		if !checker.IsAdminVerified(r.Context(), userID) {
			response.Error(w, http.StatusForbidden, 40003, "请先完成管理员双重认证")
			return
		}
		next.ServeHTTP(w, r)
	})
}
