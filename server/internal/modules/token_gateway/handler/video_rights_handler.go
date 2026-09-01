package handler

import (
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/service"
)

func (h *VideoHandler) RightsPolicy(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "政策查询不接受附加参数")
		return
	}
	policy, err := h.app.CurrentRightsPolicy(r.Context(), caller)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	writeVideoPlatformJSON(w, r, 200, policy)
}

// Project身份只取路径且与认证主体交给服务端复核，不能接受客户端指定签署人。
func (h *VideoHandler) rightsProject(w http.ResponseWriter, r *http.Request) (service.VideoCaller, bool) {
	caller, ok := h.caller(w, r)
	if !ok {
		return service.VideoCaller{}, false
	}
	value := r.PathValue("project_id")
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != value || r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "Project参数无效")
		return service.VideoCaller{}, false
	}
	caller.ProjectID = id
	return caller, true
}

func (h *VideoHandler) GetRightsAcceptance(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.rightsProject(w, r)
	if !ok {
		return
	}
	result, err := h.app.ProjectRightsAcceptance(r.Context(), caller)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	writeVideoPlatformJSON(w, r, 200, result)
}

func (h *VideoHandler) AcceptRights(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.rightsProject(w, r)
	if !ok {
		return
	}
	if caller.APIKeyID != 0 {
		writeVideoAPIError(w, r, service.ErrVideoRightsOwnerJWTRequired)
		return
	}
	keys, contentTypes := r.Header.Values("Idempotency-Key"), r.Header.Values("Content-Type")
	if len(keys) != 1 || !videoIdempotencyHeader.MatchString(keys[0]) {
		writeVideoContentError(w, r, 400, "invalid_idempotency_key", "必须提供16至128字节单值Idempotency-Key")
		return
	}
	if len(contentTypes) != 1 {
		writeVideoContentError(w, r, 415, "invalid_request_error", "请使用UTF-8 JSON")
		return
	}
	media, params, err := mime.ParseMediaType(contentTypes[0])
	if err != nil || media != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		writeVideoContentError(w, r, 415, "invalid_request_error", "请使用UTF-8 JSON")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	raw, err := io.ReadAll(r.Body)
	var request struct {
		Version   string `json:"rights_policy_version"`
		Confirmed *bool  `json:"rights_confirmed"`
	}
	if err != nil || decodeStrictJSONObject(raw, &request) != nil {
		writeVideoContentError(w, r, 400, "invalid_request_error", "权利接受字段无效")
		return
	}
	confirmed := request.Confirmed != nil && *request.Confirmed
	result, err := h.app.AcceptProjectRights(r.Context(), service.VideoRightsAcceptCommand{Caller: caller, PolicyVersion: request.Version, Confirmed: confirmed, IdempotencyKey: keys[0], RequestID: middleware.RequestIDFromContext(r.Context())})
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
