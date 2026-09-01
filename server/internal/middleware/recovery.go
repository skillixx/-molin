package middleware

import (
	"net/http"

	"molin/server/pkg/response"
)

func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				// 流式正文开始后只能中断连接；让net/http处理专用哨兵，禁止向媒体或SSE追加JSON。
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				response.Error(w, http.StatusInternalServerError, 50000, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
