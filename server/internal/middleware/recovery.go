package middleware

import (
	"net/http"

	"molin/server/pkg/response"
)

func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				response.Error(w, http.StatusInternalServerError, 50000, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
