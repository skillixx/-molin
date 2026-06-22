package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// ——— D-M2-01 方案 B：Reserve / Settle / Release ———

// TestReserve_FailClosedWithoutToken 未配置密钥时 fail-closed。
func TestReserve_FailClosedWithoutToken(t *testing.T) {
	c := NewEntitlementClient("http://127.0.0.1:8080", "")
	_, err := c.Reserve(context.Background(), 1, 2, decimal.NewFromInt(10), "k:quota_reserve")
	if !errors.Is(err, ErrInternalAuth) {
		t.Fatalf("未配置 token 应 fail-closed，实际 %v", err)
	}
}

// TestReserve_Success 成功预占返回 hold_id，请求为 POST 且带 X-Internal-Token + 正确 body。
func TestReserve_Success(t *testing.T) {
	var gotToken, gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Internal-Token")
		gotMethod = r.Method
		gotPath = r.URL.Path
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"hold_id":555,"reserved":"21","available":"79","status":"holding"}}`))
	}))
	defer srv.Close()

	c := NewEntitlementClient(srv.URL, "secret")
	res, err := c.Reserve(context.Background(), 88, 2, decimal.NewFromInt(21), "req:quota_reserve")
	if err != nil {
		t.Fatalf("成功预占不应报错: %v", err)
	}
	if res.HoldID != 555 || res.Status != "holding" {
		t.Fatalf("响应解析异常: %+v", res)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/internal/entitlement-reserve" {
		t.Fatalf("应 POST /api/internal/entitlement-reserve，实际 %s %s", gotMethod, gotPath)
	}
	if gotToken != "secret" {
		t.Fatalf("应携带 X-Internal-Token，实际 %q", gotToken)
	}
	if !contains(gotBody, `"idempotency_key":"req:quota_reserve"`) || !contains(gotBody, `"entitlement_id":88`) {
		t.Fatalf("请求体异常: %s", gotBody)
	}
}

// TestReserve_QuotaExceeded 60005（额度不足/不可用）→ ErrQuotaExceeded（门面据此拒 60005，根治白嫖）。
func TestReserve_QuotaExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":60005,"message":"权益额度不足"}`))
	}))
	defer srv.Close()
	c := NewEntitlementClient(srv.URL, "t")
	_, err := c.Reserve(context.Background(), 1, 2, decimal.NewFromInt(21), "k:quota_reserve")
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("60005 应映射 ErrQuotaExceeded，实际 %v", err)
	}
}

// TestReserve_NotFound 40400 → ErrEntitlementNotFound。
func TestReserve_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":40400,"message":"权益不存在"}`))
	}))
	defer srv.Close()
	c := NewEntitlementClient(srv.URL, "t")
	_, err := c.Reserve(context.Background(), 1, 2, decimal.NewFromInt(21), "k:quota_reserve")
	if !errors.Is(err, ErrEntitlementNotFound) {
		t.Fatalf("40400 应映射 ErrEntitlementNotFound，实际 %v", err)
	}
}

// TestReserve_ServerError 50000/5xx → 透传 error（门面 fail-safe 拒转发）。
func TestReserve_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":50000,"message":"预占权益额度失败"}`))
	}))
	defer srv.Close()
	c := NewEntitlementClient(srv.URL, "t")
	_, err := c.Reserve(context.Background(), 1, 2, decimal.NewFromInt(21), "k:quota_reserve")
	if err == nil || errors.Is(err, ErrQuotaExceeded) || errors.Is(err, ErrEntitlementNotFound) {
		t.Fatalf("50000 应透传普通 error（非额度不足/不存在），实际 %v", err)
	}
}

// TestSettle_Success 结算成功返回 settled_amount，请求 POST 到 settle 路径且携带 actual_amount。
func TestSettle_Success(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"hold_id":555,"status":"settled","settled_amount":"9","quota_used":"9","quota_reserved":"0"}}`))
	}))
	defer srv.Close()
	c := NewEntitlementClient(srv.URL, "t")
	res, err := c.Settle(context.Background(), 555, "req:quota_reserve", decimal.NewFromInt(9))
	if err != nil {
		t.Fatalf("结算不应报错: %v", err)
	}
	if !res.SettledAmount.Equal(decimal.NewFromInt(9)) || res.Status != "settled" {
		t.Fatalf("结算响应异常: %+v", res)
	}
	if gotPath != "/api/internal/entitlement-settle" {
		t.Fatalf("应 POST settle 路径，实际 %s", gotPath)
	}
	if !contains(gotBody, `"actual_amount":"9"`) || !contains(gotBody, `"hold_id":555`) {
		t.Fatalf("settle 请求体异常: %s", gotBody)
	}
}

// TestRelease_Success 释放成功返回 settled_amount=0，请求 POST 到 release 路径。
func TestRelease_Success(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"hold_id":555,"status":"released","settled_amount":"0","quota_used":"0","quota_reserved":"0"}}`))
	}))
	defer srv.Close()
	c := NewEntitlementClient(srv.URL, "t")
	res, err := c.Release(context.Background(), 555, "req:quota_reserve")
	if err != nil {
		t.Fatalf("释放不应报错: %v", err)
	}
	if !res.SettledAmount.Equal(decimal.Zero) || res.Status != "released" {
		t.Fatalf("释放响应异常: %+v", res)
	}
	if gotPath != "/api/internal/entitlement-release" {
		t.Fatalf("应 POST release 路径，实际 %s", gotPath)
	}
}

// contains 子串判断的薄封装（测试断言用）。
func contains(s, sub string) bool { return strings.Contains(s, sub) }
