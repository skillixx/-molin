package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
)

func TestChatCompletionsRejectsMissingRequestIDMiddleware(t *testing.T) {
	handler := NewChatHandler(nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"molin/qwen-turbo"}`))
	recorder := httptest.NewRecorder()

	handler.ChatCompletions(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("缺少 Request ID 中间件必须失败关闭: status=%d", recorder.Code)
	}
	var body response.Body
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("解析装配错误响应失败: %v", err)
	}
	if body.Code != 50000 || body.Message != "请求标识初始化失败" {
		t.Fatalf("装配错误响应不符合契约: %+v", body)
	}
}

func TestWriteOrchestratorErrorIncludesStableGovernanceContract(t *testing.T) {
	tests := []struct {
		name, requestID string
		err             error
		status, code    int
		errorType       string
		retryAfter      string
		limitScope      string
	}{
		{name: "内容违规", requestID: "req-policy", err: service.ErrContentPolicyViolation, status: http.StatusForbidden, code: 40310, errorType: "content_policy_violation"},
		{name: "治理不可用", requestID: "req-unavailable", err: service.ErrResourceUnavailable, status: http.StatusServiceUnavailable, code: 50321, errorType: "governance_unavailable"},
		{name: "并发限制", requestID: "req-limit", err: &service.ResourceLimitError{Cause: service.ErrConcurrencyExceeded, LimitScope: "project", RetryAfter: 1500 * time.Millisecond}, status: http.StatusTooManyRequests, code: 42922, errorType: "concurrency_limit_exceeded", retryAfter: "2", limitScope: "project"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeOrchestratorError(recorder, tt.err, tt.requestID)
			if recorder.Code != tt.status || recorder.Header().Get("Retry-After") != tt.retryAfter {
				t.Fatalf("HTTP 契约错误: status=%d retry=%s", recorder.Code, recorder.Header().Get("Retry-After"))
			}
			var body response.Body
			if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tt.code || body.ErrorType != tt.errorType || body.RequestID != tt.requestID {
				t.Fatalf("错误响应不稳定: %+v", body)
			}
			if tt.limitScope != "" {
				data, ok := body.Data.(map[string]interface{})
				if !ok || data["limit_scope"] != tt.limitScope {
					t.Fatalf("限流范围缺失或泄露内部键: %+v", body.Data)
				}
			}
			if errors.Is(tt.err, service.ErrContentPolicyViolation) && body.Message != service.DefaultSafetyRefusal {
				t.Fatalf("内容违规必须使用稳定拒绝文案: %s", body.Message)
			}
		})
	}
}
