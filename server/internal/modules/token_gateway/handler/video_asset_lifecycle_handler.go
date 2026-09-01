package handler

import "net/http"

// 仅读取路径中原资产的生命周期，禁止query覆盖归属或存储位置。
func (h *VideoHandler) AssetLifecycle(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "资产生命周期不接受查询参数")
		return
	}
	result, err := h.app.GetAssetLifecycle(r.Context(), caller, r.PathValue("asset_id"))
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	writeVideoPlatformJSON(w, r, 200, result)
}
