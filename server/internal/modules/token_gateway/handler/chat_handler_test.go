package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
