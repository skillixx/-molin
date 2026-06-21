package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/auth/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// APIKeyHandler 处理平台 API Key（sk）的用户端自助管理请求。
// 这些接口本身用登录态 JWT（不能用 sk 自助管理 sk），无需额外权限码。
type APIKeyHandler struct {
	svc *service.APIKeyService
}

// NewAPIKeyHandler 构造 sk 管理 handler。
func NewAPIKeyHandler(svc *service.APIKeyService) *APIKeyHandler {
	return &APIKeyHandler{svc: svc}
}

// issueKeyReq POST /api/keys 请求体。
// billing_mode / source_id 由后端按购买上下文决定，前端通常不传，故 handler 不接收，
// 一律走 IssueKey 默认（postpaid）。本接口只暴露 name 与 model_scope 给前端自助创建。
type issueKeyReq struct {
	Name       string   `json:"name"`
	ModelScope []string `json:"model_scope"`
}

// issueKeyResp POST /api/keys 响应：明文 secret_key 仅本次返回一次。
// 对齐 docs/frontend-api-reference.md §14.4。
type issueKeyResp struct {
	ID          uint64   `json:"id"`
	Name        string   `json:"name"`
	KeyPrefix   string   `json:"key_prefix"`
	SecretKey   string   `json:"secret_key"` // 明文，仅签发时返回一次
	BillingMode string   `json:"billing_mode"`
	ModelScope  []string `json:"model_scope"`
	Status      string   `json:"status"`
	CreatedAt   string   `json:"created_at"`
}

// IssueKey POST /api/keys — 创建 sk，响应含明文 secret_key（仅此一次）。
func (h *APIKeyHandler) IssueKey(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	var req issueKeyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	plaintext, view, err := h.svc.IssueKey(r.Context(), service.IssueKeyInput{
		UserID:     userID,
		Name:       req.Name,
		ModelScope: req.ModelScope,
		// billing_mode 缺省=postpaid（service 内默认），source_id=nil。
	})
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "创建 sk 失败")
		return
	}
	response.JSON(w, http.StatusCreated, issueKeyResp{
		ID:          view.ID,
		Name:        view.Name,
		KeyPrefix:   view.KeyPrefix,
		SecretKey:   plaintext,
		BillingMode: view.BillingMode,
		ModelScope:  view.ModelScope,
		Status:      view.Status,
		CreatedAt:   view.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// ListKeys GET /api/keys — 列出本人 sk，扁平分页，只回 key_prefix（绝不回明文/hash）。
func (h *APIKeyHandler) ListKeys(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	p := pagination.Parse(r)
	views, total, err := h.svc.ListKeys(r.Context(), userID, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, adminPagedResp{
		List:   views,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// RevokeKey DELETE /api/keys/{id} — 吊销本人 sk（立即失效）。
// keyID 不属于当前用户 → 40003 无权限（越权防护，不用 40004）。
func (h *APIKeyHandler) RevokeKey(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	keyID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "sk ID 不合法")
		return
	}
	if err := h.svc.RevokeKey(r.Context(), userID, keyID); err != nil {
		switch {
		case errors.Is(err, service.ErrKeyForbidden):
			// 越权吊销他人 sk → 40003
			response.Error(w, http.StatusForbidden, 40003, "无权操作该 sk")
		case errors.Is(err, service.ErrKeyInvalid):
			// sk 不存在
			response.Error(w, http.StatusNotFound, 40400, "sk 不存在")
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "吊销 sk 失败")
		}
		return
	}
	response.JSON(w, http.StatusOK, map[string]bool{"revoked": true})
}
