package handler

import (
	"errors"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
	"net/http"
)

// 归档入口只接受原任务、原因与版本；依赖必须显式装配，不能回落到新Provider提交。
func (h *VideoAdminHandler) RetryArchive(w http.ResponseWriter, r *http.Request) {
	if h == nil || !h.enabled || h.app == nil || !h.app.ArchiveReady() {
		response.Error(w, 503, 50300, "视频管理归档未启用")
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
	result, err := h.app.RetryArchive(r.Context(), service.VideoAdminArchiveCommand{Caller: caller, TaskID: r.PathValue("task_id"), IdempotencyKey: key, Reason: reason, VersionNo: version})
	if err != nil {
		if errors.Is(err, service.ErrVideoMediaProtected) {
			writeVideoAdminError(w, service.ErrVideoAdminCommandConflict)
		} else {
			writeVideoAdminError(w, err)
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Molin-Request-ID", result.RequestID)
	status := 200
	if result.Status == "running" {
		status = 202
	}
	response.JSON(w, status, result)
}
