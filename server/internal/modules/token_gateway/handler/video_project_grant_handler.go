package handler

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
)

// 操作者、owner和capability均由认证及数据库确定，客户端只提交目标、动作、版本和原因。
func (h *VideoAdminHandler) ManageProjectGrant(w http.ResponseWriter, r *http.Request) {
	if h == nil || !h.enabled || h.app == nil || !h.app.WritesReady() {
		response.Error(w, 503, 50300, "视频Project授权管理未启用")
		return
	}
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	keys, types := r.Header.Values("Idempotency-Key"), r.Header.Values("Content-Type")
	if r.URL.RawQuery != "" || len(keys) != 1 || !videoIdempotencyHeader.MatchString(keys[0]) {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return
	}
	if len(types) != 1 || len(r.Header.Values("Content-Encoding")) != 0 {
		response.Error(w, 415, 40000, "请使用未编码UTF-8 JSON")
		return
	}
	media, params, err := mime.ParseMediaType(types[0])
	if err != nil || media != "application/json" {
		response.Error(w, 415, 40000, "请使用UTF-8 JSON")
		return
	}
	for k, v := range params {
		if k != "charset" || !strings.EqualFold(v, "utf-8") {
			response.Error(w, 415, 40000, "请使用UTF-8 JSON")
			return
		}
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
	var fields map[string]json.RawMessage
	c := service.VideoProjectGrantCommand{Caller: caller, IdempotencyKey: keys[0]}
	if err != nil || !utf8.Valid(raw) || decodeStrictJSONObject(raw, &fields) != nil || len(fields) != 5 || json.Unmarshal(fields["action"], &c.Action) != nil || json.Unmarshal(fields["project_id"], &c.ProjectID) != nil || json.Unmarshal(fields["model"], &c.Model) != nil || json.Unmarshal(fields["version_no"], &c.VersionNo) != nil || json.Unmarshal(fields["reason"], &c.Reason) != nil {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return
	}
	result, err := h.app.ManageProjectGrant(r.Context(), c)
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w, 200, result)
}
