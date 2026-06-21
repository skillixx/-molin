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

// apiKeyIDKey sk（平台 API Key）调用时注入的 api_key_id；JWT 调用不注入（取值返回 0）。
const apiKeyIDKey contextKey = "api_key_id"

// BanChecker 由 auth.AuthService 实现，在 middleware 包中定义以避免循环导入。
// 职责包括：
//   - IsUserBlocked：查询用户是否处于封禁黑名单（按 user_id 维度）
//   - IsAccessTokenRevoked：查询当前 Access Token 是否因退出登录被吊销（按单个 token 维度，
//     不影响该用户其他设备/会话的 Access Token）
type BanChecker interface {
	IsUserBlocked(ctx context.Context, userID uint64) bool
	IsAccessTokenRevoked(ctx context.Context, rawToken string) bool
}

// APIKeyResolver 由 auth.APIKeyService（适配方法）实现，在 middleware 包定义以避免循环导入
// （与 BanChecker 同模式）。
// ResolveKey 校验平台 API Key（sk）明文，返回所属 userID 与 apiKeyID；
// 当 sk 无效 / 已吊销 / 关联用户被封禁时返回 ok=false（封禁联动在 service 侧 ResolveKey 内已处理）。
type APIKeyResolver interface {
	ResolveKey(ctx context.Context, rawSK string) (userID, apiKeyID uint64, ok bool)
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

// APIKeyIDFromContext 取 sk 调用注入的 api_key_id；登录态 JWT 调用未注入，返回 0。
// 供门面侧（如 token_gateway）区分本次请求来自 sk 还是 JWT，并归因用量。
func APIKeyIDFromContext(ctx context.Context) uint64 {
	id, _ := ctx.Value(apiKeyIDKey).(uint64)
	return id
}

// skTokenPrefix 平台 API Key 明文统一前缀（sk-molin-...，此处只判 sk- 即可区分 JWT）。
const skTokenPrefix = "sk-"

// RequireUserAuth 双模式鉴权：根据 Bearer Token 形态自动分流。
//   - Authorization: Bearer sk-...  → sk 路径：走 apiKeyResolver.ResolveKey，
//     成功则注入 user_id + api_key_id；无效返回 401。
//   - Authorization: Bearer <jwt>   → JWT 路径：复用 RequireAuth 的 JWT 校验 +
//     封禁/吊销黑名单逻辑，仅注入 user_id（api_key_id 缺省为 0）。
//
// apiKeyResolver 可为 nil（sk 系统未就绪时退化为纯 JWT，灰度安全）：
// 此时即便携带 sk- 前缀的 Token 也会被当作非法（resolver 缺失无法校验），返回 401。
// banChecker 语义与 RequireAuth 完全一致，仅作用于 JWT 路径。
func RequireUserAuth(secret string, banChecker BanChecker, apiKeyResolver APIKeyResolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			response.Error(w, http.StatusUnauthorized, 40001, "未登录")
			return
		}
		rawToken := strings.TrimPrefix(auth, "Bearer ")

		// sk 路径：前缀判别为 sk-，走 API Key 解析。
		if strings.HasPrefix(rawToken, skTokenPrefix) {
			// resolver 为 nil 时无法校验 sk，按未就绪处理，统一返回 401（避免误放行）。
			if apiKeyResolver == nil {
				response.Error(w, http.StatusUnauthorized, 40001, "sk 鉴权未启用")
				return
			}
			userID, apiKeyID, ok := apiKeyResolver.ResolveKey(r.Context(), rawToken)
			if !ok {
				response.Error(w, http.StatusUnauthorized, 40001, "sk 无效或已失效")
				return
			}
			ctx := context.WithValue(r.Context(), userIDKey, userID)
			ctx = context.WithValue(ctx, apiKeyIDKey, apiKeyID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// JWT 路径：与 RequireAuth 完全一致（解析 + 封禁/吊销黑名单）。
		claims, err := pkgjwt.Parse(rawToken, secret)
		if err != nil {
			response.Error(w, http.StatusUnauthorized, 40001, "token 无效或已过期")
			return
		}
		if banChecker != nil && banChecker.IsUserBlocked(r.Context(), claims.UserID) {
			response.Error(w, http.StatusUnauthorized, 40101, "账号已被封禁")
			return
		}
		if banChecker != nil && banChecker.IsAccessTokenRevoked(r.Context(), rawToken) {
			response.Error(w, http.StatusUnauthorized, 40001, "token 已失效，请重新登录")
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, claims.UserID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
