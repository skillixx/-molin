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

// 取消与隔离共用严格原因/CAS合同；需要审批或金额的写接口不能用此函数吞掉额外字段。
func readVideoAdminReasonRequest(w http.ResponseWriter, r *http.Request) (string, string, uint64, bool) {
	keys := r.Header.Values("Idempotency-Key")
	if r.URL.RawQuery != "" || len(keys) != 1 || !videoIdempotencyHeader.MatchString(keys[0]) {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return "", "", 0, false
	}
	types := r.Header.Values("Content-Type")
	if len(types) != 1 || len(r.Header.Values("Content-Encoding")) != 0 {
		response.Error(w, 415, 40000, "请使用未编码UTF-8 JSON")
		return "", "", 0, false
	}
	media, params, err := mime.ParseMediaType(types[0])
	if err != nil || media != "application/json" {
		response.Error(w, 415, 40000, "请使用UTF-8 JSON")
		return "", "", 0, false
	}
	for key, value := range params {
		if key != "charset" || !strings.EqualFold(value, "utf-8") {
			response.Error(w, 415, 40000, "请使用UTF-8 JSON")
			return "", "", 0, false
		}
	}
	if r.Body == nil {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return "", "", 0, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	raw, err := io.ReadAll(r.Body)
	var fields map[string]json.RawMessage
	var reason string
	var version uint64
	if err != nil || !utf8.Valid(raw) || decodeStrictJSONObject(raw, &fields) != nil || len(fields) != 2 || json.Unmarshal(fields["reason"], &reason) != nil || json.Unmarshal(fields["version_no"], &version) != nil || version == 0 || version > math.MaxUint64-8 {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return "", "", 0, false
	}
	return keys[0], reason, version, true
}
