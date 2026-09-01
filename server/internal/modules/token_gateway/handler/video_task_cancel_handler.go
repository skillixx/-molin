package handler

import (
	"io"
	"net/http"
)

func (h *VideoHandler) CancelTask(w http.ResponseWriter, r *http.Request) {
	h.cancelTask(w, r, r.PathValue("task_id"))
}
func (h *VideoHandler) CancelTaskByVideo(w http.ResponseWriter, r *http.Request) {
	h.cancelTask(w, r, r.PathValue("video_id"))
}

// 用户取消不接收reason、金额、Provider结果或客户端归属；版本锁由原G5事务处理。
func (h *VideoHandler) cancelTask(w http.ResponseWriter, r *http.Request, id string) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "取消任务不接受查询参数")
		return
	}
	key, ok := videoUploadKey(w, r)
	if !ok {
		return
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 1)
		raw, err := io.ReadAll(r.Body)
		if err != nil || len(raw) != 0 {
			writeVideoContentError(w, r, 400, "invalid_request_error", "取消任务不接受请求正文")
			return
		}
	}
	result, err := h.app.CancelTask(r.Context(), caller, id, key)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	w.Header().Set("X-Molin-Request-ID", result.RequestID)
	status := http.StatusOK
	if result.CancellationResult == "cancel_requested" {
		status = http.StatusAccepted
	}
	writeVideoPlatformJSON(w, r, status, result)
}
