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

// BanChecker 封禁状态查询接口，由 auth.AuthService 实现。
// 在 middleware 包中定义以避免循环导入。
type BanChecker interface {
	IsUserBlocked(ctx context.Context, userID uint64) bool
}

// RequireAuth 解析 Bearer Token，将 user_id 注入 context；校验失败返回 401。
// banChecker 可为 nil（不检查封禁黑名单，用于不需要封禁保护的路由）。
// 当 banChecker 不为 nil 时，Token 解析成功后额外查询 Redis 黑名单；
// 命中则返回 401 40101，确保封禁用户存量 Access Token 立即失效。
func RequireAuth(secret string, banChecker BanChecker, next http.Handler) http.Handler {
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
		// 查询封禁黑名单：封禁用户的存量 Access Token 在 Redis TTL 内立即被拦截
		if banChecker != nil && banChecker.IsUserBlocked(r.Context(), claims.UserID) {
			response.Error(w, http.StatusUnauthorized, 40101, "账号已被封禁")
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
