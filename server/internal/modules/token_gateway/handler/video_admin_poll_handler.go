package handler

import (
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
	"net/http"
)

// 管理轮询不接受Provider ID或URL，只能沿已绑定的原业务任务查询。
func (h *VideoAdminHandler) PollTask(w http.ResponseWriter, r *http.Request) {
	if h == nil || !h.enabled || h.app == nil || !h.app.PollReady() {
		response.Error(w, 503, 50300, "视频管理轮询未启用")
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
	result, err := h.app.PollTask(r.Context(), service.VideoAdminPollCommand{Caller: caller, TaskID: r.PathValue("task_id"), IdempotencyKey: key, Reason: reason, VersionNo: version})
	if err != nil {
		writeVideoAdminError(w, err)
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
