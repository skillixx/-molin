package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/idgen"
	"molin/server/pkg/response"
)

// ChatHandler 处理 OpenAI 兼容对话转发（用户端，网页登录态）。
// 安全约定：请求/响应中的对话内容绝不落明文日志；渠道 api_key 绝不出现在响应。
type ChatHandler struct {
	svc *service.ForwardService
}

// NewChatHandler 创建对话转发 handler。
func NewChatHandler(svc *service.ForwardService) *ChatHandler {
	return &ChatHandler{svc: svc}
}

// ChatCompletions POST /api/token/chat/completions
// 鉴权（RequireAuth 注入 userID）→ 门面校验/门禁/转发上游 → 透传响应（含 SSE 流式）。
func (h *ChatHandler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}

	// 以 map 原样解析请求体（薄转发器，近似纯透传，仅改写 model / stream_options）。
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体不是合法 JSON")
		return
	}

	modelCode, _ := body["model"].(string)
	if strings.TrimSpace(modelCode) == "" {
		response.Error(w, http.StatusBadRequest, 40000, "model 不能为空")
		return
	}
	stream, _ := body["stream"].(bool)

	in := service.ForwardInput{
		RequestID: idgen.NewRequestID(),
		UserID:    userID,
		Model:     modelCode,
		Stream:    stream,
		Body:      body,
	}

	// Forward 内部一旦开始透传上游响应即返回 nil；返回 error 表示尚未写出，可安全返回 JSON 错误。
	if err := h.svc.Forward(r.Context(), w, in); err != nil {
		writeForwardError(w, err)
	}
}

// writeForwardError 将转发前置错误映射为 HTTP 状态码 + 中文 message。
func writeForwardError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrModelNotConfigured):
		response.Error(w, http.StatusBadRequest, 40000, "模型不可用或未配置")
	case errors.Is(err, service.ErrAccessDenied):
		response.Error(w, http.StatusForbidden, 40300, "未开通 token 服务，无法调用")
	case errors.Is(err, service.ErrChannelUnavailable):
		response.Error(w, http.StatusServiceUnavailable, 50300, "上游渠道不可用")
	case errors.Is(err, service.ErrUpstream):
		response.Error(w, http.StatusBadGateway, 50200, "上游服务调用失败")
	default:
		response.Error(w, http.StatusInternalServerError, 50000, "对话转发失败")
	}
}
