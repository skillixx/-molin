package handler

import (
	"molin/server/internal/modules/token_gateway/service"
	"net/http"
)

// 仅接收公开来源资产ID；内部归属、版本、hash和对象位置全部由服务端冻结。
func (h *VideoHandler) ImportImageInput(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "来源导入不接受查询参数")
		return
	}
	key, ok := videoUploadKey(w, r)
	if !ok {
		return
	}
	var body struct {
		ProjectID     uint64 `json:"project_id"`
		SourceAssetID string `json:"source_asset_id"`
	}
	if !videoUploadJSON(w, r, &body) {
		return
	}
	if caller.APIKeyID == 0 && body.ProjectID == 0 {
		writeVideoContentError(w, r, 400, "project_required", "登录调用必须指定Project")
		return
	}
	caller.ProjectID = body.ProjectID
	result, err := h.app.ImportImageInput(r.Context(), service.VideoInputImportCommand{Caller: caller, IdempotencyKey: key, SourceAssetID: body.SourceAssetID})
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	status := 201
	if result.Idempotent {
		status = 200
	}
	if result.Status == "processing" {
		status = 202
		w.Header().Set("Retry-After", "1")
	}
	writeVideoPlatformJSON(w, r, status, result)
}
