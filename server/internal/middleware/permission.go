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
