package handler

import (
	"net/http"

	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
)

// 管理写必须显式装配原因加密器；不把任意客户端原因写入日志或普通审计字段。
func (h *VideoAdminHandler) CancelTask(w http.ResponseWriter, r *http.Request) {
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
	result, err := h.app.CancelTask(r.Context(), service.VideoAdminCancelCommand{Caller: caller, TaskID: r.PathValue("task_id"), IdempotencyKey: key, Reason: reason, VersionNo: version})
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	status := http.StatusOK
	if result.CancellationResult == "cancel_requested" {
		status = http.StatusAccepted
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Molin-Request-ID", result.RequestID)
	response.JSON(w, status, result)
}
