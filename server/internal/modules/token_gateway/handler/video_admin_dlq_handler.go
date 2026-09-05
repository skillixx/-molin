package handler

import (
	"net/http"

	"molin/server/internal/modules/token_gateway/service"
	video "molin/server/internal/modules/token_gateway/video"
	"molin/server/pkg/response"
)

// RecoverDeadLetter只允许管理员按DLQ头消息的原Task和阶段受控回流，不能指定新attempt或Provider。
func (h *VideoAdminHandler) RecoverDeadLetter(w http.ResponseWriter, r *http.Request) {
	if h == nil || !h.enabled || h.app == nil || !h.app.DLQRecoveryReady() {
		response.Error(w, 503, 50300, "视频死信恢复未启用")
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
	result, err := h.app.RecoverDeadLetter(r.Context(), service.VideoAdminDLQRecoveryCommand{
		Caller: caller, TaskID: r.PathValue("task_id"), Stage: video.TaskStage(r.PathValue("stage")),
		IdempotencyKey: key, Reason: reason, VersionNo: version,
	})
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Molin-Request-ID", result.RequestID)
	response.JSON(w, http.StatusOK, result)
}

// DiscardPoisonMessage要求精确熔断审计ID；只能确认丢弃当前非法队头，不能误删合法业务消息。
func (h *VideoAdminHandler) DiscardPoisonMessage(w http.ResponseWriter, r *http.Request) {
	if h == nil || !h.enabled || h.app == nil || !h.app.DLQRecoveryReady() {
		response.Error(w, 503, 50300, "视频队列毒消息处置未启用")
		return
	}
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	key, reason, fuseAuditID, ok := readVideoAdminReasonRequest(w, r)
	if !ok {
		return
	}
	result, err := h.app.DiscardPoisonMessage(r.Context(), service.VideoAdminPoisonDiscardCommand{
		Caller: caller, Stage: video.TaskStage(r.PathValue("stage")), IdempotencyKey: key, Reason: reason, FuseAuditID: fuseAuditID,
	})
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w, http.StatusOK, result)
}
