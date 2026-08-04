package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDPublishesAndReusesOneIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		supplied string
	}{
		{name: "服务端生成"},
		{name: "忽略客户端请求ID", supplied: "req_client_trace"},
		{name: "拒绝超长客户端请求ID", supplied: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var contextID string
			handler := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				contextID = RequestIDFromContext(r.Context())
			}))
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if tc.supplied != "" {
				req.Header.Set("X-Request-ID", tc.supplied)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)

			publicID := recorder.Header().Get("X-Request-ID")
			if publicID == "" || publicID != contextID {
				t.Fatalf("公开与业务 Request ID 必须一致: public=%q context=%q", publicID, contextID)
			}
			if tc.supplied != "" && publicID == tc.supplied {
				t.Fatalf("客户端 Request ID 不得占用全局唯一账本身份: %q", publicID)
			}
		})
	}
}
