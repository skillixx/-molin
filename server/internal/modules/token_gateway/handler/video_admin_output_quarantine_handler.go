package handler

import (
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
	"net/http"
)

// 行政隔离沿用当前管理员认证与严格原因请求，不接受客户端指定命令指针或审核结果。
func (h *VideoAdminHandler) QuarantineOutput(w http.ResponseWriter, r *http.Request) {
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
	result, err := h.app.QuarantineOutput(r.Context(), service.VideoAdminOutputQuarantineCommand{Caller: caller, AssetID: r.PathValue("asset_id"), IdempotencyKey: key, Reason: reason, VersionNo: version})
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Molin-Request-ID", result.RequestID)
	response.JSON(w, 200, result)
}
