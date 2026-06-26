package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/conversation/service"
	tokengatewaysvc "molin/server/internal/modules/token_gateway/service"
	workbenchsvc "molin/server/internal/modules/workbench/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// ConversationHandler 有状态会话用户端 handler（仅登录态）。
type ConversationHandler struct {
	svc *service.ConversationService
}

// NewConversationHandler 创建 handler。
func NewConversationHandler(svc *service.ConversationService) *ConversationHandler {
	return &ConversationHandler{svc: svc}
}

// pagedResp 扁平分页响应（D-95）。
type pagedResp struct {
	Items    interface{} `json:"items"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
	Total    int64       `json:"total"`
}

// createBody 新建会话请求体。
type createBody struct {
	AgentID   *uint64 `json:"agent_id"`
	ModelCode string  `json:"model_code"`
	Title     string  `json:"title"`
}

// Create POST /api/conversations
func (h *ConversationHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	var body createBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体不是合法 JSON")
		return
	}
	conv, err := h.svc.Create(r.Context(), service.CreateInput{
		UserID: userID, AgentID: body.AgentID, ModelCode: body.ModelCode, Title: body.Title,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	response.JSON(w, http.StatusOK, conv)
}

// List GET /api/conversations?type=plain|agent&page=&page_size=
func (h *ConversationHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	scope := r.URL.Query().Get("type")
	p := pagination.Parse(r)
	items, total, err := h.svc.List(r.Context(), userID, scope, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, pagedResp{Items: items, Page: p.Page, PageSize: p.PageSize, Total: total})
}

// Get GET /api/conversations/{id}
func (h *ConversationHandler) Get(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	conv, err := h.svc.Get(r.Context(), id, userID)
	if err != nil {
		writeErr(w, err)
		return
	}
	response.JSON(w, http.StatusOK, conv)
}

// ListMessages GET /api/conversations/{id}/messages?page=&page_size=
func (h *ConversationHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p := pagination.Parse(r)
	items, total, err := h.svc.ListMessages(r.Context(), id, userID, p.Offset(), p.PageSize)
	if err != nil {
		writeErr(w, err)
		return
	}
	response.JSON(w, http.StatusOK, pagedResp{Items: items, Page: p.Page, PageSize: p.PageSize, Total: total})
}

// renameBody 重命名请求体。
type renameBody struct {
	Title string `json:"title"`
}

// Rename PATCH /api/conversations/{id}
func (h *ConversationHandler) Rename(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body renameBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体不是合法 JSON")
		return
	}
	if err := h.svc.Rename(r.Context(), id, userID, body.Title); err != nil {
		writeErr(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"id": id, "title": body.Title})
}

// Delete DELETE /api/conversations/{id}
func (h *ConversationHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), id, userID); err != nil {
		writeErr(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"id": id})
}

// chatBody 会话内发消息请求体。
type chatBody struct {
	Content string `json:"content"`
	Stream  bool   `json:"stream"`
}

// Chat POST /api/conversations/{id}/chat
func (h *ConversationHandler) Chat(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body chatBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体不是合法 JSON")
		return
	}
	// 返回 error 表示尚未写出任何响应，可安全返回 JSON 错误；nil 表示已开始 SSE/JSON 输出。
	if err := h.svc.Chat(r.Context(), w, id, userID, body.Content, body.Stream); err != nil {
		writeErr(w, err)
	}
}

// pathID 解析路径 {id}；非法时写出 400 并返回 false。
func pathID(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return 0, false
	}
	return id, true
}

// writeErr 将服务/编排/计费错误映射为 HTTP 状态码 + 中文 message。
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrConversationNotFound):
		response.Error(w, http.StatusNotFound, 40400, "会话不存在")
	case service.IsValidation(err):
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	case workbenchsvc.IsForbidden(err):
		response.Error(w, http.StatusForbidden, 40003, "无权访问该 Agent")
	case workbenchsvc.IsNotFound(err):
		response.Error(w, http.StatusNotFound, 40400, "agent 不存在")
	case workbenchsvc.IsValidation(err):
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
