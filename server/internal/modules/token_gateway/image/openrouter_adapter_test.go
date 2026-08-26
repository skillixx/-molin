package image

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenRouterImageAdapterStrictRequestAndResponse(t *testing.T) {
	pngRaw, err := fakePNG(0)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	expectedAuthorization := strings.Join([]string{"Bearer", "fake-openrouter-key-123456"}, " ")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/images" || r.Header.Get("Authorization") != expectedAuthorization || r.Header.Get("X-OpenRouter-Experimental-Metadata") != "enabled" {
			t.Fatalf("请求协议错误: method=%s path=%s auth=%s metadata=%s", r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-OpenRouter-Experimental-Metadata"))
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		provider := body["provider"].(map[string]interface{})
		only, _ := provider["only"].([]interface{})
		if body["model"] != "bytedance-seed/seedream-5-0-lite" || body["stream"] != false || provider["allow_fallbacks"] != false || len(only) != 1 || only[0] != "seed" || body["prompt"] != "内存测试" {
			t.Fatalf("请求门禁错误: %+v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "or-test-1", "data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngRaw), "media_type": "image/png"}},
			"usage": map[string]interface{}{"cost": 0.035},
		})
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return errors.New("禁止重定向") }
	adapter, err := newOpenRouterImageAdapter(OpenRouterImageAdapterConfig{APIKey: "fake-openrouter-key-123456", ProviderTag: "seed", MaxCostUSD: "0.25", Timeout: time.Second, ModelMap: map[string]string{"bytedance-seed/seedream-5-0-lite": "bytedance-seed/seedream-5-0-lite"}}, server.URL+"/api/v1/images", client, true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Generate(context.Background(), ProviderImageRequest{RequestID: "req-1", ModelCode: "bytedance-seed/seedream-5-0-lite", Prompt: "内存测试", Count: 1, Resolution: "2K", AspectRatio: "1:1", Quality: "standard"})
	if err != nil || calls.Load() != 1 || len(result.Images) != 1 || result.ProviderRequestID != "or-test-1" || result.ProviderCostUSD != "0.035" ||
		!result.ProviderAttempted || result.ProviderHTTPStatus != http.StatusOK || result.ProviderCode != "openrouter-images" {
		t.Fatalf("Adapter响应错误: result=%+v calls=%d err=%v", result, calls.Load(), err)
	}
}

func TestOpenRouterImageAdapterPreservesSafeFailureEvidence(t *testing.T) {
	testCases := []struct {
		name string
		body string
		want string
	}{
		{name: "费用额度", body: `{"error":{"code":403,"message":"Insufficient credits for this request"}}`, want: "403:credit_limit"},
		{name: "工作区预算", body: `{"error":{"code":403,"message":"Workspace monthly budget exceeded"}}`, want: "403:workspace_budget"},
		{name: "模型策略", body: `{"error":{"code":403,"message":"Requested model is not allowed by the model allowlist"}}`, want: "403:model_policy"},
		{name: "Provider策略", body: `{"error":{"code":403,"message":"Requested provider is not allowed by the provider allowlist"}}`, want: "403:provider_policy"},
		{name: "数据策略", body: `{"error":{"code":403,"message":"Request does not satisfy the ZDR data policy"}}`, want: "403:data_policy"},
		{name: "内容护栏", body: `{"error":{"code":"guardrail_blocked","message":"不得持久化的上游明文"},"openrouter_metadata":{"pipeline":[{"type":"guardrail","name":"regex_pi_detection","summary":"Blocked by content filter"}]}}`, want: "403:content_guardrail"},
		{name: "Key权限", body: `{"error":{"code":403,"message":"API key does not have permission to access the requested resource"}}`, want: "403:key_permission"},
		{name: "上游权限", body: `{"error":{"code":403,"message":"Provider request failed"},"openrouter_metadata":{"provider_responses":[{"provider":"Seed","status_code":403}]}}`, want: "403:upstream_permission"},
		{name: "上游名称证据", body: `{"error":{"code":403,"message":"Provider request failed","metadata":{"provider_name":"Seed","raw":"不得持久化的Provider原文"}}}`, want: "403:upstream_permission"},
		{name: "未知分类", body: `{"error":{"code":403,"message":"opaque failure sk-or-v1-secret-value AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}}`, want: "403:unknown"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-OpenRouter-Experimental-Metadata") != "enabled" {
					t.Fatal("403诊断请求必须显式请求安全路由元数据")
				}
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()
			adapter, err := newOpenRouterImageAdapter(OpenRouterImageAdapterConfig{
				APIKey: "fake-openrouter-key-123456", ProviderTag: "seed", MaxCostUSD: "0.25", Timeout: time.Second,
				ModelMap: map[string]string{"bytedance-seed/seedream-5-0-lite": "bytedance-seed/seedream-5-0-lite"},
			}, server.URL, server.Client(), true)
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Generate(context.Background(), ProviderImageRequest{RequestID: "req-safe-error", ModelCode: "bytedance-seed/seedream-5-0-lite", Prompt: "测试", Count: 1})
			if !errors.Is(err, ErrProviderFailed) || !result.ProviderAttempted || result.ProviderHTTPStatus != http.StatusForbidden || result.ProviderErrorCode != testCase.want {
				t.Fatalf("明确失败必须保留白名单分类: result=%+v err=%v", result, err)
			}
			encoded, _ := json.Marshal(result)
			for _, forbidden := range []string{"不得持久化", "sk-or-v1-secret-value", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("Provider原始错误内容不得进入低敏回执: %s", forbidden)
				}
			}
		})
	}
}

func TestOpenRouterImageAdapterPreservesNonForbiddenSafeErrorCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"不得持久化的限流明文"}}`))
	}))
	defer server.Close()
	adapter, err := newOpenRouterImageAdapter(OpenRouterImageAdapterConfig{
		APIKey: "fake-openrouter-key-123456", ProviderTag: "seed", MaxCostUSD: "0.25", Timeout: time.Second,
		ModelMap: map[string]string{"bytedance-seed/seedream-5-0-lite": "bytedance-seed/seedream-5-0-lite"},
	}, server.URL, server.Client(), true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Generate(context.Background(), ProviderImageRequest{RequestID: "req-rate-limit", ModelCode: "bytedance-seed/seedream-5-0-lite", Prompt: "测试", Count: 1})
	if !errors.Is(err, ErrProviderFailed) || result.ProviderHTTPStatus != http.StatusTooManyRequests || result.ProviderErrorCode != "rate_limited" {
		t.Fatalf("非403失败必须继续保留封闭字符集错误码: result=%+v err=%v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "不得持久化") {
		t.Fatal("非403原始错误消息不得进入低敏回执")
	}
}

func TestOpenRouterImageAdapterZeroRetryAndUnknown(t *testing.T) {
	var calls atomic.Int64
	client := &http.Client{Transport: openRouterRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("注入断连")
	}), Timeout: time.Second}
	adapter, err := newOpenRouterImageAdapter(OpenRouterImageAdapterConfig{APIKey: "fake-openrouter-key-123456", ProviderTag: "seed", MaxCostUSD: "0.25", Timeout: time.Second, ModelMap: map[string]string{"provider/image": "provider/image"}}, "https://test.invalid/images", client, true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Generate(context.Background(), ProviderImageRequest{RequestID: "req-1", ModelCode: "provider/image", Prompt: "测试", Count: 1})
	if !errors.Is(err, ErrProviderUnknown) || !result.ResultUnknown || calls.Load() != 1 {
		t.Fatalf("结果未知必须零重试: result=%+v calls=%d err=%v", result, calls.Load(), err)
	}
}

func TestOpenRouterImageAdapterRejectsUnsafeConfigurationAndBody(t *testing.T) {
	if _, err := NewOpenRouterImageAdapter(OpenRouterImageAdapterConfig{APIKey: "short", ProviderTag: "seed", MaxCostUSD: "0.25", Timeout: time.Second, ModelMap: map[string]string{"provider/image": "provider/image"}}); err == nil {
		t.Fatal("弱Key配置必须拒绝")
	}
	if _, err := newOpenRouterImageAdapter(OpenRouterImageAdapterConfig{APIKey: "fake-openrouter-key-123456", ProviderTag: "seed", MaxCostUSD: "0.25", Timeout: time.Second, ModelMap: map[string]string{"provider/image": "provider/image"}}, "http://127.0.0.1/images", http.DefaultClient, false); err == nil {
		t.Fatal("正式构造不得改写固定端点")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":"not-array"}`))
	}))
	defer server.Close()
	adapter, _ := newOpenRouterImageAdapter(OpenRouterImageAdapterConfig{APIKey: "fake-openrouter-key-123456", ProviderTag: "seed", MaxCostUSD: "0.25", Timeout: time.Second, ModelMap: map[string]string{"provider/image": "provider/image"}}, server.URL, server.Client(), true)
	result, err := adapter.Generate(context.Background(), ProviderImageRequest{RequestID: "req-1", ModelCode: "provider/image", Prompt: "测试", Count: 1})
	if !errors.Is(err, ErrProviderUnknown) || !result.ResultUnknown {
		t.Fatalf("畸形成功响应必须结果未知: result=%+v err=%v", result, err)
	}
	if strings.Contains(adapter.String(), "fake-openrouter-key") {
		t.Fatal("String不得泄露Key")
	}
}

func TestOpenRouterImageAdapterRejectsMissingOrExcessiveReceiptCost(t *testing.T) {
	pngRaw, err := fakePNG(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name string
		cost interface{}
	}{
		{name: "缺少费用", cost: nil},
		{name: "费用越权", cost: 0.25000001},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				payload := map[string]interface{}{"id": "or-cost-gate", "data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngRaw)}}}
				if testCase.cost != nil {
					payload["usage"] = map[string]interface{}{"cost": testCase.cost}
				}
				_ = json.NewEncoder(w).Encode(payload)
			}))
			defer server.Close()
			adapter, err := newOpenRouterImageAdapter(OpenRouterImageAdapterConfig{
				APIKey: "fake-openrouter-key-123456", ProviderTag: "seed", MaxCostUSD: "0.25", Timeout: time.Second,
				ModelMap: map[string]string{"bytedance-seed/seedream-5-0-lite": "bytedance-seed/seedream-5-0-lite"},
			}, server.URL, server.Client(), true)
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Generate(context.Background(), ProviderImageRequest{RequestID: "req-cost", ModelCode: "bytedance-seed/seedream-5-0-lite", Prompt: "测试", Count: 1})
			if !errors.Is(err, ErrProviderUnknown) || !result.ResultUnknown || len(result.Images) != 1 {
				t.Fatalf("缺失或越权费用必须保留结果未知且禁止交付: result=%+v err=%v", result, err)
			}
		})
	}
}

type openRouterRoundTripFunc func(*http.Request) (*http.Response, error)

func (f openRouterRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
