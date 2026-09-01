package handler

import (
	"io"
	"mime"
	"net/http"
	"strings"

	"molin/server/internal/modules/token_gateway/service"
)

func videoUploadKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !videoIdempotencyHeader.MatchString(keys[0]) {
		writeVideoContentError(w, r, 400, "invalid_idempotency_key", "必须提供16至128字节单值Idempotency-Key")
		return "", false
	}
	return keys[0], true
}
func videoUploadJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	types := r.Header.Values("Content-Type")
	if len(types) != 1 {
		writeVideoContentError(w, r, 415, "invalid_request_error", "请使用UTF-8 JSON")
		return false
	}
	media, p, err := mime.ParseMediaType(types[0])
	if err != nil || media != "application/json" || (p["charset"] != "" && !strings.EqualFold(p["charset"], "utf-8")) {
		writeVideoContentError(w, r, 415, "invalid_request_error", "请使用UTF-8 JSON")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	raw, err := io.ReadAll(r.Body)
	if err != nil || decodeStrictJSONObject(raw, dst) != nil {
		writeVideoContentError(w, r, 400, "invalid_request_error", "视频输入请求字段无效")
		return false
	}
	return true
}

func (h *VideoHandler) CreateUpload(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "上传创建不接受查询参数")
		return
	}
	key, ok := videoUploadKey(w, r)
	if !ok {
		return
	}
	var body struct {
		ProjectID uint64 `json:"project_id"`
		Filename  string `json:"filename"`
		MIMEType  string `json:"mime_type"`
		SizeBytes uint64 `json:"size_bytes"`
		SHA256    string `json:"sha256"`
	}
	if !videoUploadJSON(w, r, &body) {
		return
	}
	if caller.APIKeyID == 0 && body.ProjectID == 0 {
		writeVideoContentError(w, r, 400, "project_required", "登录调用必须指定Project")
		return
	}
	caller.ProjectID = body.ProjectID
	result, err := h.app.CreateUpload(r.Context(), service.VideoUploadCreateCommand{Caller: caller, IdempotencyKey: key, Filename: body.Filename, MIMEType: body.MIMEType, SizeBytes: body.SizeBytes, SHA256: body.SHA256})
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	status := 201
	if result.Idempotent {
		status = 200
	}
	writeVideoPlatformJSON(w, r, status, result)
}

func (h *VideoHandler) GetUpload(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "上传查询不接受附加参数")
		return
	}
	result, err := h.app.GetUpload(r.Context(), caller, r.PathValue("session_id"))
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	writeVideoPlatformJSON(w, r, 200, result)
}

func (h *VideoHandler) CompleteUpload(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "上传完成不接受查询参数")
		return
	}
	key, ok := videoUploadKey(w, r)
	if !ok {
		return
	}
	var body struct{}
	if !videoUploadJSON(w, r, &body) {
		return
	}
	result, err := h.app.CompleteUpload(r.Context(), caller, r.PathValue("session_id"), key)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	status := 200
	if result.Status == "verifying" {
		status = 202
		w.Header().Set("Retry-After", "1")
	}
	writeVideoPlatformJSON(w, r, status, result)
}

func (h *VideoHandler) CancelUpload(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "取消上传不接受附加参数")
		return
	}
	key, ok := videoUploadKey(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(raw) != 0 {
		writeVideoContentError(w, r, 400, "invalid_request_error", "取消上传不接受正文")
		return
	}
	result, err := h.app.CancelUpload(r.Context(), caller, r.PathValue("session_id"), key)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	writeVideoPlatformJSON(w, r, 200, result)
}
