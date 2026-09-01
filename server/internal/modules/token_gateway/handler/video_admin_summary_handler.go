package handler

import (
	"net/http"

	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
)

// 汇总是非列表只读接口，不接受分页、筛选或请求正文，不能暗中触发逐任务修复。
func (h *VideoAdminHandler) ReconciliationSummary(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeVideoAdminError(w, service.ErrVideoAdminQuery)
		return
	}
	item, err := h.app.ReconciliationSummary(r.Context(), caller)
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w, 200, item)
}
