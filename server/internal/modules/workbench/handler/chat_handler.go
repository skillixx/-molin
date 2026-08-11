package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"molin/server/internal/middleware"
	tokengatewaysvc "molin/server/internal/modules/token_gateway/service"
	"molin/server/internal/modules/workbench/service"
	"molin/server/pkg/idgen"
	"molin/server/pkg/response"
)

// ChatHandler 处理站内聊天工作台 tool-use 编排对话（POST /api/agents/{id}/chat，D2 仅登录态）。
// 安全约定：对话内容绝不落明文日志；插件凭证绝不出现在响应。
type ChatHandler struct {
	svc *service.ChatService
}

// NewChatHandler 创建编排对话 handler。
func NewChatHandler(svc *service.ChatService) *ChatHandler {
	return &ChatHandler{svc: svc}
}

// chatReqBody 编排请求体。
type chatReqBody struct {
	Messages []json.RawMessage `json:"messages"`
	Model    string            `json:"model"`
	Stream   bool              `json:"stream"`
}

// Chat POST /api/agents/{id}/chat
func (h *ChatHandler) Chat(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var body chatReqBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体不是合法 JSON")
		return
	}
	if len(body.Messages) == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "messages 不能为空")
		return
	}

	req := service.ChatRequest{
		AgentID:   id,
		UserID:    userID,
		RequestID: idgen.NewRequestID(),
		Model:     body.Model,
		Stream:    body.Stream,
		Messages:  body.Messages,
	}
	// 返回 error 表示尚未写出任何响应，可安全返回 JSON 错误；nil 表示已开始 SSE/JSON 输出。
	if err := h.svc.ChatWithAgent(r.Context(), w, req); err != nil {
		writeChatError(w, err)
	}
}

// writeChatError 将编排前置错误映射为 HTTP 状态码 + 中文 message。
func writeChatError(w http.ResponseWriter, err error) {
	switch {
	case isForbidden(err):
		response.Error(w, http.StatusForbidden, 40003, "无权访问该 Agent")
	case isNotFound(err):
		response.Error(w, http.StatusNotFound, 40400, "agent 不存在")
	case isValidationErr(err):
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	case errors.Is(err, tokengatewaysvc.ErrModelNotConfigured):
		response.Error(w, http.StatusForbidden, 40300, "无可用模型")
	case errors.Is(err, tokengatewaysvc.ErrAccessDenied):
		response.Error(w, http.StatusForbidden, 40300, "未开通 token 服务，无法调用")
	case errors.Is(err, tokengatewaysvc.ErrWalletInsufficient):
		response.Error(w, http.StatusPaymentRequired, 60001, "钱包余额不足")
	case errors.Is(err, tokengatewaysvc.ErrQuotaExhausted):
		response.Error(w, http.StatusPaymentRequired, 60005, "套餐额度不足")
	case errors.Is(err, tokengatewaysvc.ErrEntitlementDenied):
		response.Error(w, http.StatusForbidden, 40003, "套餐额度归属不符")
	case errors.Is(err, tokengatewaysvc.ErrTrafficClosed):
		response.Error(w, http.StatusServiceUnavailable, 50330, "AI 网关商业流量暂未开放")
	case errors.Is(err, tokengatewaysvc.ErrSystemBusy):
		response.Error(w, http.StatusServiceUnavailable, 50301, "系统繁忙，请稍后重试")
	case errors.Is(err, tokengatewaysvc.ErrChannelUnavailable):
		response.Error(w, http.StatusServiceUnavailable, 50300, "上游渠道不可用")
	case errors.Is(err, tokengatewaysvc.ErrUpstream):
		response.Error(w, http.StatusBadGateway, 50200, "上游服务调用失败")
	default:
		response.Error(w, http.StatusInternalServerError, 50000, "对话失败")
	}
}
