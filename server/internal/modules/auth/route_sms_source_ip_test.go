package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"molin/server/internal/config"
	"molin/server/internal/middleware"
)

type rejectedVerificationSourceResolver struct{ err error }

func (r rejectedVerificationSourceResolver) Resolve(*http.Request) (netip.Addr, error) {
	return netip.Addr{}, r.err
}

// TestSMSPublicRoutesFailBeforeBusinessOnUntrustedSource 冻结手机发码和密码重置的可信来源前置门禁。
// 服务对象和 Redis 故意为 nil；若路由没有先执行来源判定，本测试会进入业务层并暴露装配错误。
func TestSMSPublicRoutesFailBeforeBusinessOnUntrustedSource(t *testing.T) {
	tests := []struct {
		name       string
		resolver   middleware.PublicSourceIPResolver
		wantStatus int
		wantCode   int
	}{
		{name: "可信代理来源非法", resolver: rejectedVerificationSourceResolver{err: middleware.ErrPublicSourceIPForbidden}, wantStatus: http.StatusForbidden, wantCode: 40003},
		{name: "来源解析不可用", resolver: rejectedVerificationSourceResolver{err: middleware.ErrPublicSourceIPUnavailable}, wantStatus: http.StatusServiceUnavailable, wantCode: 50300},
		{name: "解析器未装配", resolver: nil, wantStatus: http.StatusServiceUnavailable, wantCode: 50300},
	}
	paths := []string{"/api/auth/verification-codes/phone", "/api/auth/password/reset"}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mux := http.NewServeMux()
			RegisterRoutes(mux, nil, nil, nil, config.Config{}, nil, nil, nil, test.resolver, nil)
			for _, path := range paths {
				recorder := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, path, nil)
				mux.ServeHTTP(recorder, req)
				var body struct {
					Code int `json:"code"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
					t.Fatalf("%s 来源门禁响应必须是 JSON: %v", path, err)
				}
				if recorder.Code != test.wantStatus || body.Code != test.wantCode {
					t.Fatalf("%s 来源门禁契约错误: status=%d code=%d", path, recorder.Code, body.Code)
				}
			}
		})
	}
}
