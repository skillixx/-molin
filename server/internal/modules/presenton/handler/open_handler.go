// Package handler presenton 应用「打开入口」HTTP 层（D1）。
package handler

import (
	"errors"
	"net/http"
	"strings"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/presenton/service"
	"molin/server/pkg/response"
)

// OpenHandler 处理 GET /api/app/presenton/open（仅登录态）。
type OpenHandler struct {
	svc *service.OpenService
}

// NewOpenHandler 创建打开入口 handler。
func NewOpenHandler(svc *service.OpenService) *OpenHandler {
	return &OpenHandler{svc: svc}
}

// Open GET /api/app/presenton/open
// 校验本人对 presenton 应用的有效开通 → 返回可嵌入的入口 URL（含短期票据）。
func (h *OpenHandler) Open(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}

	// 用户所选模型（F-D，墨灵 logical_model_code）；前端从 token_gateway 模型目录取，可选。
	model := strings.TrimSpace(r.URL.Query().Get("model"))

	result, err := h.svc.Open(r.Context(), userID, model)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNoAccess):
			response.Error(w, http.StatusForbidden, 40300, "未开通 PPT 生成器，请先购买")
		case errors.Is(err, service.ErrModelNotAllowed):
			response.Error(w, http.StatusBadRequest, 40000, "所选模型不可用")
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "打开应用失败")
		}
		return
	}
	response.JSON(w, http.StatusOK, result)
}

// presentonModel /models 返回的单个模型项。
type presentonModel struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Models GET /api/app/presenton/models（仅登录态）
// 返回 presenton 可用模型白名单，供前端「打开」时的模型下拉。
func (h *OpenHandler) Models(w http.ResponseWriter, r *http.Request) {
	if middleware.UserIDFromContext(r.Context()) == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	codes := h.svc.AllowedModels()
	items := make([]presentonModel, 0, len(codes))
	for _, c := range codes {
		// v1 用 code 作为展示名；后续需要友好名称可从 token_models 富化。
		items = append(items, presentonModel{Code: c, Name: c})
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": items})
}
