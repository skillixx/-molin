package handler

import (
	"encoding/json"
	"io"
	"math"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
)

// 同一路径严格区分申请与复核，不能把未知checker_id等字段吞掉。
func (h *VideoAdminHandler) ReleaseOutput(w http.ResponseWriter, r *http.Request) {
	if h == nil || !h.enabled || h.app == nil || !h.app.WritesReady() {
		response.Error(w, 503, 50300, "视频管理写接口未启用")
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
	for key, value := range params {
		if key != "charset" || !strings.EqualFold(value, "utf-8") {
			response.Error(w, 415, 40000, "请使用UTF-8 JSON")
			return
		}
	}
	if r.Body == nil {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	raw, err := io.ReadAll(r.Body)
	var fields map[string]json.RawMessage
	c := service.VideoAdminOutputReleaseCommand{Caller: caller, AssetID: r.PathValue("asset_id"), IdempotencyKey: keys[0]}
	if err != nil || !utf8.Valid(raw) || decodeStrictJSONObject(raw, &fields) != nil || json.Unmarshal(fields["action"], &c.Action) != nil || json.Unmarshal(fields["reason"], &c.Reason) != nil || json.Unmarshal(fields["version_no"], &c.VersionNo) != nil || c.VersionNo == 0 || c.VersionNo > math.MaxUint64-8 {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return
	}
	if (c.Action == "request" && len(fields) != 3) || (c.Action == "approve" && (len(fields) != 4 || json.Unmarshal(fields["approval_id"], &c.ApprovalID) != nil)) || (c.Action != "request" && c.Action != "approve") {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return
	}
	result, err := h.app.ReleaseOutput(r.Context(), c)
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Molin-Request-ID", result.RequestID)
	status := 200
	if result.Status == "pending" {
		status = 202
	}
	response.JSON(w, status, result)
}
