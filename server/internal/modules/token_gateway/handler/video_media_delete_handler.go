package handler

import (
	"errors"
	"io"
	"molin/server/internal/modules/token_gateway/service"
	"net/http"
	"net/url"
)

// 兼容DELETE只处理媒体，拒绝通过正文提供任何对象位置或取消/退款指令。
func (h *VideoHandler) DeleteMedia(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "媒体删除不接受查询参数")
		return
	}
	key, ok := videoUploadKey(w, r)
	if !ok {
		return
	}
	if r.Body != nil {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
		if err != nil || len(raw) > 0 {
			writeVideoContentError(w, r, 400, "invalid_request_error", "媒体删除不接受正文")
			return
		}
	}
	id := r.PathValue("video_id")
	result, err := h.app.DeleteMedia(r.Context(), caller, id, key)
	if err != nil {
		if errors.Is(err, service.ErrVideoMediaRunning) {
			w.Header().Set("Link", "</api/token/video-tasks/by-video/"+url.PathEscape(id)+">; rel=\"cancel\"")
		}
		writeVideoAPIError(w, r, err)
		return
	}
	w.Header().Set("X-Molin-Request-ID", result.RequestID)
	writeVideoJob(w, result)
}
