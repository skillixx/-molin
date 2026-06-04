package middleware

import (
	"context"
	"net/http"
	"strings"

	pkgjwt "molin/server/pkg/jwt"
	"molin/server/pkg/response"
)

// userIDKey 避免与 requestIDKey 冲突，使用同类型不同值。
const userIDKey contextKey = "user_id"

// RequireAuth 解析 Bearer Token，将 user_id 注入 context；校验失败返回 401。
func RequireAuth(secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			response.Error(w, http.StatusUnauthorized, 40001, "未登录")
			return
		}
		claims, err := pkgjwt.Parse(strings.TrimPrefix(auth, "Bearer "), secret)
		if err != nil {
			response.Error(w, http.StatusUnauthorized, 40001, "token 无效或已过期")
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, claims.UserID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserIDFromContext 从 context 中取出已认证的用户 ID。
func UserIDFromContext(ctx context.Context) uint64 {
	id, _ := ctx.Value(userIDKey).(uint64)
	return id
}
