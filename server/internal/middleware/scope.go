package middleware

import (
	"context"
	"net/http"
)

const scopeCtxKey contextKey = "data_scope"

// DataScope 当前请求的数据范围（管理员可见的用户集合）。
// All=true 表示超管，不受任何限制；
// All=false 时只能访问 UserIDs 中的用户，空集合表示无管辖范围。
type DataScope struct {
	All     bool
	UserIDs []uint64
}

// Contains 判断目标用户是否在当前数据范围内。
func (s DataScope) Contains(userID uint64) bool {
	if s.All {
		return true
	}
	for _, id := range s.UserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

// ScopeResolver 数据范围解析接口，由 iam.ScopeService 实现。
// 在 middleware 包中定义以避免循环导入（与 IAMChecker 同模式）。
type ScopeResolver interface {
	ResolveScope(ctx context.Context, adminUserID uint64) DataScope
}

// InjectScope 解析当前管理员的数据范围并注入 context，在 RequireAuth 之后使用。
// 解析失败时静默降级为 All=false + 空 UserIDs（最小权限原则）。
func InjectScope(resolver ScopeResolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := UserIDFromContext(r.Context())
		scope := resolver.ResolveScope(r.Context(), userID)
		ctx := context.WithValue(r.Context(), scopeCtxKey, scope)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ScopeFromContext 从 context 读取数据范围。
// 未注入时（路由未套 InjectScope）默认返回 All=true，保证现有无 scope 路由行为不变。
func ScopeFromContext(ctx context.Context) DataScope {
	s, ok := ctx.Value(scopeCtxKey).(DataScope)
	if !ok {
		return DataScope{All: true}
	}
	return s
}
