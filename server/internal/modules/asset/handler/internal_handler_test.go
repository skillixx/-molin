package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetEntitlementBalance_FailClosed 验证内部余额查询接口的鉴权 fail-closed：
//   - INTERNAL_API_TOKEN 未配置（internalToken="")时，任何请求一律 403（不放行）。
//   - 配置了 token 但请求未带 X-Internal-Token（或不匹配）时，亦 403。
//
// svc 传 nil：鉴权在调用 service 前完成，403 路径不会触达 svc，确保前置闸不可被绕过。
func TestGetEntitlementBalance_FailClosed(t *testing.T) {
	t.Run("未配置token-fail-closed-403", func(t *testing.T) {
		h := NewInternalAssetHandler(nil, nil, "")
		req := httptest.NewRequest(http.MethodGet, "/api/internal/entitlement-balance?entitlement_id=123&user_id=45", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()
		h.GetEntitlementBalance(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("未配置 token 应返回 403，实际 %d，body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("缺X-Internal-Token-403", func(t *testing.T) {
		h := NewInternalAssetHandler(nil, nil, "secret-token")
		req := httptest.NewRequest(http.MethodGet, "/api/internal/entitlement-balance?entitlement_id=123&user_id=45", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()
		h.GetEntitlementBalance(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("缺 X-Internal-Token 应返回 403，实际 %d", rec.Code)
		}
	})

	t.Run("token不匹配-403", func(t *testing.T) {
		h := NewInternalAssetHandler(nil, nil, "secret-token")
		req := httptest.NewRequest(http.MethodGet, "/api/internal/entitlement-balance?entitlement_id=123&user_id=45", nil)
		req.Header.Set("X-Internal-Token", "wrong-token")
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()
		h.GetEntitlementBalance(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("token 不匹配应返回 403，实际 %d", rec.Code)
		}
	})
}

// TestListUserEntitlements_FailClosed 验证按 user_id+product_id 解析权益的内部接口同样 fail-closed：
// 鉴权在触达 service 前完成（svc 传 nil 不会被调用），任何未配置/缺/错 token 一律 403。
func TestListUserEntitlements_FailClosed(t *testing.T) {
	t.Run("未配置token-fail-closed-403", func(t *testing.T) {
		h := NewInternalAssetHandler(nil, nil, "")
		req := httptest.NewRequest(http.MethodGet, "/api/internal/user-entitlements?user_id=45&product_id=7", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()
		h.ListUserEntitlements(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("未配置 token 应返回 403，实际 %d，body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("缺X-Internal-Token-403", func(t *testing.T) {
		h := NewInternalAssetHandler(nil, nil, "secret-token")
		req := httptest.NewRequest(http.MethodGet, "/api/internal/user-entitlements?user_id=45&product_id=7", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()
		h.ListUserEntitlements(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("缺 X-Internal-Token 应返回 403，实际 %d", rec.Code)
		}
	})

	t.Run("token不匹配-403", func(t *testing.T) {
		h := NewInternalAssetHandler(nil, nil, "secret-token")
		req := httptest.NewRequest(http.MethodGet, "/api/internal/user-entitlements?user_id=45&product_id=7", nil)
		req.Header.Set("X-Internal-Token", "wrong-token")
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()
		h.ListUserEntitlements(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("token 不匹配应返回 403，实际 %d", rec.Code)
		}
	})
}
