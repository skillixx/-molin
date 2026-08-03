package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/service"
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
// 鉴权（RequireUserAuth 注入 userID，sk 调用另注入 api_key_id）→ 门面校验/门禁/转发上游 → 透传响应（含 SSE 流式）。
func (h *ChatHandler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.RequestIDFromContext(r.Context())
	if requestID == "" {
		// 缺少中间件身份属于服务装配错误，禁止 Handler 生成第二个账本 ID 掩盖问题。
		response.Error(w, http.StatusInternalServerError, 50000, "请求标识初始化失败")
		return
	}
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	// sk 调用返回非 0 的 api_key_id；登录态 JWT 调用为 0（用量归因到 sk 维度）。
	apiKeyID := middleware.APIKeyIDFromContext(r.Context())

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
		RequestID: requestID,
		UserID:    userID,
		APIKeyID:  apiKeyID,
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
	case errors.Is(err, service.ErrModelNotInScope):
		// S2-丁4b：sk 的 model_scope 越界（请求的 model 不在该 API Key 授权范围内）。
		response.Error(w, http.StatusForbidden, 40300, "该 API Key 未授权调用此模型")
	case errors.Is(err, service.ErrWalletInsufficient):
		// S2-丁5 / D1：postpaid 钱包余额不足以冻结预扣保证金（前置闸拒绝，未发起上游、不计费、无冻结）。
		response.Error(w, http.StatusPaymentRequired, 60001, "钱包余额不足")
	case errors.Is(err, service.ErrQuotaExhausted):
		// S2-丁5：prepaid 套餐额度不足（复用 60005，禁止新造 60002）。
		response.Error(w, http.StatusPaymentRequired, 60005, "套餐额度不足")
	case errors.Is(err, service.ErrEntitlementDenied):
		// S2-丁5：prepaid 套餐归属不符。
		response.Error(w, http.StatusForbidden, 40003, "套餐额度归属不符")
	case errors.Is(err, service.ErrSystemBusy):
		// D-M2-02：postpaid 预扣保证金乐观锁冲突重试耗尽（可重试），区别于真余额不足（60001）。
		// 全局错误码表无专用「系统繁忙/可重试」码，故沿用 token_gateway 5030x 服务不可用族新增 50301
		// （HTTP 503，客户端可稍后重试），与 50300「上游渠道不可用」区分，绝不映射为 60001。
		response.Error(w, http.StatusServiceUnavailable, 50301, "系统繁忙，请稍后重试")
	case errors.Is(err, service.ErrChannelUnavailable):
		response.Error(w, http.StatusServiceUnavailable, 50300, "上游渠道不可用")
	case errors.Is(err, service.ErrUpstream):
		response.Error(w, http.StatusBadGateway, 50200, "上游服务调用失败")
	default:
		response.Error(w, http.StatusInternalServerError, 50000, "对话转发失败")
	}
}
