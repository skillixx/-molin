// Package handler presenton 应用「打开入口」HTTP 层（D1）。
package handler

import (
	"errors"
	"net/http"

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

	result, err := h.svc.Open(r.Context(), userID)
	if err != nil {
		if errors.Is(err, service.ErrNoAccess) {
			response.Error(w, http.StatusForbidden, 40300, "未开通 PPT 生成器，请先购买")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "打开应用失败")
		return
	}
	response.JSON(w, http.StatusOK, result)
}
