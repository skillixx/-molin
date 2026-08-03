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
	orchestrator service.RequestOrchestrator
}

// NewChatHandler 创建对话转发 handler。
func NewChatHandler(_ *service.ForwardService) *ChatHandler {
	return &ChatHandler{}
}

// WithOrchestrator 为公开文字接口装配 G2 唯一编排器；旧 ForwardService 仅保留给工作台内部调用。
func (h *ChatHandler) WithOrchestrator(orchestrator service.RequestOrchestrator) *ChatHandler {
	h.orchestrator = orchestrator
	return h
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
	if !validTextMessages(body["messages"]) {
		response.Error(w, http.StatusBadRequest, 40000, "messages 必须包含至少一条非空文字消息")
		return
	}
	idempotencyKey, ok := singleIdempotencyKey(r.Header.Values("Idempotency-Key"))
	if !ok {
		response.Error(w, http.StatusBadRequest, 40000, "Idempotency-Key 必须是长度不超过 191 的单值 Header")
		return
	}
	stream, _ := body["stream"].(bool)
	if h.orchestrator == nil {
		response.Error(w, http.StatusInternalServerError, 50000, "AI 请求编排服务未装配")
		return
	}
	prepared, err := h.orchestrator.Prepare(r.Context(), service.PrepareCommand{
		RequestID: requestID, IdempotencyKey: idempotencyKey,
		UserID: userID, APIKeyID: apiKeyID, LogicalModel: modelCode, Stream: stream, Body: body,
	})
	if err != nil {
		writeOrchestratorError(w, err)
		return
	}
	if prepared.Existing {
		response.JSON(w, http.StatusAccepted, prepared)
		return
	}
	sink := &httpStreamSink{writer: w}
	if err := h.orchestrator.Execute(r.Context(), prepared.RequestID, sink); err != nil && !sink.started {
		writeOrchestratorError(w, err)
	}
}

func singleIdempotencyKey(values []string) (string, bool) {
	if len(values) == 0 {
		return "", true
	}
	if len(values) != 1 {
		return "", false
	}
	value := values[0]
	if value == "" || value != strings.TrimSpace(value) || len(value) > 191 || strings.Contains(value, ",") {
		return "", false
	}
	return value, true
}

// validTextMessages 拒绝空消息和多模态内容，确保 G2 只把有效文字请求写入账本并发往上游。
func validTextMessages(raw interface{}) bool {
	messages, ok := raw.([]interface{})
	if !ok || len(messages) == 0 {
		return false
	}
	meaningful := false
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]interface{})
		if !ok || strings.TrimSpace(stringValue(message["role"])) == "" {
			return false
		}
		switch content := message["content"].(type) {
		case string:
			meaningful = meaningful || strings.TrimSpace(content) != ""
		case []interface{}:
			if len(content) == 0 {
				continue
			}
			for _, rawPart := range content {
				part, ok := rawPart.(map[string]interface{})
				if !ok || stringValue(part["type"]) != "text" {
					return false
				}
				meaningful = meaningful || strings.TrimSpace(stringValue(part["text"])) != ""
			}
		case nil:
			continue
		default:
			return false
		}
	}
	return meaningful
}

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return text
}

type httpStreamSink struct {
	writer  http.ResponseWriter
	started bool
}

func (s *httpStreamSink) SetHeader(key, value string) { s.writer.Header().Set(key, value) }
func (s *httpStreamSink) WriteHeader(status int) error {
	if !s.started {
		s.started = true
		s.writer.WriteHeader(status)
	}
	return nil
}
func (s *httpStreamSink) Write(data []byte) error {
	s.started = true
	_, err := s.writer.Write(data)
	return err
}
func (s *httpStreamSink) Flush() error {
	if flusher, ok := s.writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func writeOrchestratorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrProjectKeyRequired):
		response.Error(w, http.StatusUnauthorized, 40001, "请使用有效的 Project SK 调用")
	case errors.Is(err, service.ErrUserUnavailable):
		response.Error(w, http.StatusForbidden, 40300, "账号不可用")
	case errors.Is(err, service.ErrRealNameRequired):
		response.Error(w, http.StatusBadRequest, 70001, "请先完成实名认证")
	case errors.Is(err, service.ErrProjectAccessDenied):
		response.Error(w, http.StatusForbidden, 40300, "Project 或 SK 不可用")
	case errors.Is(err, service.ErrG2ModelNotAllowed):
		response.Error(w, http.StatusForbidden, 40300, "该 Project SK 未授权调用此模型")
	case errors.Is(err, service.ErrG2ModelUnavailable), errors.Is(err, service.ErrModelNotConfigured):
		response.Error(w, http.StatusBadRequest, 40000, "模型不可用或未配置")
	case errors.Is(err, service.ErrIdempotencyConflict):
		response.Error(w, http.StatusConflict, 40901, "幂等键对应的请求内容不一致")
	case errors.Is(err, service.ErrUpstream):
		response.Error(w, http.StatusBadGateway, 50200, "上游服务调用失败")
	case errors.Is(err, service.ErrChannelUnavailable):
		response.Error(w, http.StatusServiceUnavailable, 50300, "上游渠道不可用")
	default:
		response.Error(w, http.StatusInternalServerError, 50000, "AI 请求编排失败")
	}
}
