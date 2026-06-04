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
