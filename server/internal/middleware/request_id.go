package middleware

import (
	"context"
	"net/http"

	"molin/server/pkg/idgen"
)

type contextKey string

const requestIDKey contextKey = "request_id"

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 公开请求 ID 同时是商业账本全局唯一键，必须由墨灵生成，不能信任客户端可重复值。
		requestID := idgen.NewRequestID()

		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext 返回 RequestID 中间件冻结的全链路请求 ID。
// 业务 Handler 必须复用该值，禁止再次生成与公开 X-Request-ID 不一致的内部请求 ID。
func RequestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey).(string)
	return requestID
}
