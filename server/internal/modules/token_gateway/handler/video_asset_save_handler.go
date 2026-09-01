package handler

import (
	"io"
	"net/http"
)

// 保存整条视频只接受根资产路径与幂等键；容量、商品、位置及归属全部由服务端确定。
func (h *VideoHandler) SaveAsset(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "保存不接受查询参数")
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
			writeVideoContentError(w, r, 400, "invalid_request_error", "保存不接受请求正文")
			return
		}
	}
	result, err := h.app.SaveVideoAsset(r.Context(), caller, r.PathValue("asset_id"), key)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	w.Header().Set("X-Molin-Request-ID", result.RequestID)
	status := http.StatusCreated
	if result.Idempotent {
		status = http.StatusOK
	}
	writeVideoPlatformJSON(w, r, status, result)
}
