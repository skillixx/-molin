package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"
)

// TestEntitlementClient_FailClosedWithoutToken 未配置内部密钥时直接 fail-closed，不发请求。
func TestEntitlementClient_FailClosedWithoutToken(t *testing.T) {
	c := NewEntitlementClient("http://127.0.0.1:8080", "")
	_, err := c.Consume(context.Background(), 1, 2, decimal.NewFromInt(10), "k:quota")
	if !errors.Is(err, ErrInternalAuth) {
		t.Fatalf("未配置 token 应 fail-closed 返回 ErrInternalAuth，实际 %v", err)
	}
}

// TestEntitlementClient_Success 成功扣减返回权益快照，且请求带 X-Internal-Token。
func TestEntitlementClient_Success(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Internal-Token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"entitlement_id":88,"quota_used":"300","status":"active"}}`))
	}))
	defer srv.Close()

	c := NewEntitlementClient(srv.URL, "secret-token")
	res, err := c.Consume(context.Background(), 88, 2, decimal.NewFromInt(300), "req:quota")
	if err != nil {
		t.Fatalf("成功扣减不应报错: %v", err)
	}
	if res.EntitlementID != 88 || res.Status != "active" {
		t.Fatalf("响应解析异常: %+v", res)
	}
	if gotToken != "secret-token" {
		t.Fatalf("应携带 X-Internal-Token=secret-token，实际 %q", gotToken)
	}
}

// TestEntitlementClient_QuotaExceeded 业务码 60005 → ErrQuotaExceeded。
func TestEntitlementClient_QuotaExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":60005,"message":"权益额度不足"}`))
	}))
	defer srv.Close()
	c := NewEntitlementClient(srv.URL, "t")
	_, err := c.Consume(context.Background(), 1, 2, decimal.NewFromInt(10), "k:quota")
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("60005 应映射 ErrQuotaExceeded，实际 %v", err)
	}
}

// TestEntitlementClient_Forbidden 业务码 40003 → ErrEntitlementForbidden。
func TestEntitlementClient_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":40003,"message":"权益不属于该用户"}`))
	}))
	defer srv.Close()
	c := NewEntitlementClient(srv.URL, "t")
	_, err := c.Consume(context.Background(), 1, 2, decimal.NewFromInt(10), "k:quota")
	if !errors.Is(err, ErrEntitlementForbidden) {
		t.Fatalf("40003 应映射 ErrEntitlementForbidden，实际 %v", err)
	}
}

// ——— D-M2-01：GetBalance 查询余额 ———

// TestGetBalance_FailClosedWithoutToken 未配置内部密钥时 fail-closed，不发请求。
func TestGetBalance_FailClosedWithoutToken(t *testing.T) {
	c := NewEntitlementClient("http://127.0.0.1:8080", "")
	_, err := c.GetBalance(context.Background(), 1, 2)
	if !errors.Is(err, ErrInternalAuth) {
		t.Fatalf("未配置 token 应 fail-closed 返回 ErrInternalAuth，实际 %v", err)
	}
}

// TestGetBalance_Usable 成功查询返回 usable=true，请求为 GET 且带 X-Internal-Token + 查询参数。
func TestGetBalance_Usable(t *testing.T) {
	var gotToken, gotMethod, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Internal-Token")
		gotMethod = r.Method
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"entitlement_id":88,"user_id":2,"quota_used":"10","status":"active","usable":true}}`))
	}))
	defer srv.Close()

	c := NewEntitlementClient(srv.URL, "secret-token")
	res, err := c.GetBalance(context.Background(), 88, 2)
	if err != nil {
		t.Fatalf("成功查询不应报错: %v", err)
	}
	if !res.Usable || res.EntitlementID != 88 || res.Status != "active" {
		t.Fatalf("响应解析异常: %+v", res)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("应为 GET 请求，实际 %s", gotMethod)
	}
	if gotToken != "secret-token" {
		t.Fatalf("应携带 X-Internal-Token=secret-token，实际 %q", gotToken)
	}
	if gotQuery != "entitlement_id=88&user_id=2" {
		t.Fatalf("查询参数异常: %q", gotQuery)
	}
}

// TestGetBalance_Unusable 额度耗尽时返回 usable=false（HTTP 200）。
func TestGetBalance_Unusable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"entitlement_id":88,"user_id":2,"quota_used":"100","status":"active","usable":false}}`))
	}))
	defer srv.Close()
	c := NewEntitlementClient(srv.URL, "t")
	res, err := c.GetBalance(context.Background(), 88, 2)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if res.Usable {
		t.Fatalf("额度耗尽应返回 usable=false")
	}
}

// TestGetBalance_NotFound 业务码 40400 → ErrEntitlementNotFound。
func TestGetBalance_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":40400,"message":"权益不存在"}`))
	}))
	defer srv.Close()
	c := NewEntitlementClient(srv.URL, "t")
	_, err := c.GetBalance(context.Background(), 1, 2)
	if !errors.Is(err, ErrEntitlementNotFound) {
		t.Fatalf("40400 应映射 ErrEntitlementNotFound，实际 %v", err)
	}
}

// TestGetBalance_Forbidden 业务码 40003 → ErrEntitlementForbidden。
func TestGetBalance_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":40003,"message":"权益不属于该用户"}`))
	}))
	defer srv.Close()
	c := NewEntitlementClient(srv.URL, "t")
	_, err := c.GetBalance(context.Background(), 1, 2)
	if !errors.Is(err, ErrEntitlementForbidden) {
		t.Fatalf("40003 应映射 ErrEntitlementForbidden，实际 %v", err)
	}
}
