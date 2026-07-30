package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"molin/server/internal/config"
)

func TestEmailBootstrapRouteIsAbsentByDefault(t *testing.T) {
	mux := http.NewServeMux()
	RegisterEmailBootstrapRoute(mux, nil, config.Config{}, nil, nil, nil)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodHead, http.MethodPut} {
		req := httptest.NewRequest(method, "/api/internal/email/bootstrap/admin-verify", nil)
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusNotFound {
			t.Fatalf("默认关闭时 %s 必须404，实际%d", method, resp.Code)
		}
	}
}
