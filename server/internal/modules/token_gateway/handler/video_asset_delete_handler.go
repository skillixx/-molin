package handler

import (
	"encoding/json"
	"math"
	"net/http"
)

// 精确版本控制删除范围；客户端不能提供父子列表、存储位置或替代当前归属。
func (h *VideoHandler) DeleteAsset(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "资产删除不接受查询参数")
		return
	}
	key, ok := videoUploadKey(w, r)
	if !ok {
		return
	}
	var body map[string]json.RawMessage
	if !videoUploadJSON(w, r, &body) {
		return
	}
	var version uint64
	value, exists := body["version_no"]
	if len(body) != 1 || !exists || json.Unmarshal(value, &version) != nil || version == 0 || version > math.MaxUint64-4 {
		writeVideoContentError(w, r, 400, "invalid_request_error", "必须提供有效的资产版本")
		return
	}
	result, err := h.app.DeleteVideoAsset(r.Context(), caller, r.PathValue("asset_id"), version, key)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	w.Header().Set("X-Molin-Request-ID", result.RequestID)
	writeVideoPlatformJSON(w, r, 200, result)
}
