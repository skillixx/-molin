package handler

import (
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
	"net/http"
)

func (h *VideoAdminHandler) QuarantineInput(w http.ResponseWriter, r *http.Request) {
	if h == nil || !h.enabled || h.app == nil || !h.app.WritesReady() {
		response.Error(w, 503, 50300, "视频管理写接口未启用")
		return
	}
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	key, reason, version, ok := readVideoAdminReasonRequest(w, r)
	if !ok {
		return
	}
	result, err := h.app.QuarantineInput(r.Context(), service.VideoAdminInputQuarantineCommand{Caller: caller, InputAssetID: r.PathValue("input_asset_id"), IdempotencyKey: key, Reason: reason, VersionNo: version})
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w, 200, result)
}
