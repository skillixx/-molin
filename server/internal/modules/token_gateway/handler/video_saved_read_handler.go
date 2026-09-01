package handler

import (
	"net/http"
	"net/url"
	"strconv"
)

// 长期资产ID使用已保存UserAsset编号，拒绝数值别名，角色仅由服务白名单判定。
func savedReadID(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	raw := r.PathValue("user_asset_id")
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != raw {
		writeVideoContentError(w, r, 404, "video_not_found", "视频资源不存在")
		return 0, false
	}
	return id, true
}
func (h *VideoHandler) SavedDownloadURL(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeVideoContentError(w, r, 405, "method_not_allowed", "仅支持GET")
		return
	}
	id, ok := savedReadID(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "长期下载地址不接受查询参数")
		return
	}
	result, err := h.app.SavedVideoDownloadURL(r.Context(), caller, id, r.PathValue("role"))
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	writeVideoPlatformJSON(w, r, 200, result)
}
func (h *VideoHandler) SavedContent(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeVideoContentError(w, r, 405, "method_not_allowed", "仅支持GET")
		return
	}
	id, ok := savedReadID(w, r)
	if !ok {
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 2 || len(q["expires"]) != 1 || len(q["signature"]) != 1 {
		writeVideoContentError(w, r, 400, "invalid_request_error", "长期下载参数无效")
		return
	}
	content, err := h.app.GetSavedVideoContent(r.Context(), caller, id, r.PathValue("role"), q.Get("expires"), q.Get("signature"))
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	defer content.Close()
	w.Header().Set("Referrer-Policy", "no-referrer")
	serveVideoContent(w, r, VideoHTTPContent{Size: content.Size, SHA256: content.SHA256, OpenRange: content.OpenRange, BeforeWrite: content.BeforeWrite}, content.MIMEType)
}
