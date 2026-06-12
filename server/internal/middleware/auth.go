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

// BanChecker 由 auth.AuthService 实现，在 middleware 包中定义以避免循环导入。
// 职责包括：
//   - IsUserBlocked：查询用户是否处于封禁黑名单（按 user_id 维度）
//   - IsAccessTokenRevoked：查询当前 Access Token 是否因退出登录被吊销（按单个 token 维度，
//     不影响该用户其他设备/会话的 Access Token）
type BanChecker interface {
	IsUserBlocked(ctx context.Context, userID uint64) bool
	IsAccessTokenRevoked(ctx context.Context, rawToken string) bool
}

// RequireAuth 解析 Bearer Token，将 user_id 注入 context；校验失败返回 401。
// banChecker 可为 nil（不检查封禁黑名单/吊销黑名单，用于不需要封禁保护的路由）。
// 当 banChecker 不为 nil 时，Token 解析成功后依次额外查询：
//  1. 用户封禁黑名单：命中则返回 401 40101，确保封禁用户存量 Access Token 立即失效；
//  2. Access Token 吊销黑名单：命中则返回 401 40001，确保退出登录后该 Token 立即失效。
func RequireAuth(secret string, banChecker BanChecker, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			response.Error(w, http.StatusUnauthorized, 40001, "未登录")
			return
		}
		rawToken := strings.TrimPrefix(auth, "Bearer ")
		claims, err := pkgjwt.Parse(rawToken, secret)
		if err != nil {
			response.Error(w, http.StatusUnauthorized, 40001, "token 无效或已过期")
			return
		}
		// 查询封禁黑名单：封禁用户的存量 Access Token 在 Redis TTL 内立即被拦截
		if banChecker != nil && banChecker.IsUserBlocked(r.Context(), claims.UserID) {
			response.Error(w, http.StatusUnauthorized, 40101, "账号已被封禁")
			return
		}
		// 查询吊销黑名单：退出登录后的 Access Token 立即失效
		if banChecker != nil && banChecker.IsAccessTokenRevoked(r.Context(), rawToken) {
			response.Error(w, http.StatusUnauthorized, 40001, "token 已失效，请重新登录")
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
