package handler

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
)

type videoPlatformRequest struct {
	ProjectID           uint64  `json:"project_id"`
	Model               string  `json:"model"`
	Prompt              string  `json:"prompt"`
	Operation           string  `json:"operation"`
	Seconds             *string `json:"seconds"`
	Size                *string `json:"size"`
	QuoteID             string  `json:"quote_id"`
	InputAssetID        string  `json:"input_asset_id"`
	RightsConfirmed     bool    `json:"rights_confirmed"`
	RightsPolicyVersion string  `json:"rights_policy_version"`
	RightsAttestation   bool    `json:"rights_attestation"`
}

// 平台JSON和兼容multipart最终构造同一VideoCommand；不接受金额、bucket或Provider字段。
func (h *VideoHandler) platformCommand(w http.ResponseWriter, r *http.Request) (service.VideoCommand, bool) {
	caller, ok := h.caller(w, r)
	if !ok {
		return service.VideoCommand{}, false
	}
	types := r.Header.Values("Content-Type")
	if len(types) != 1 {
		writeVideoContentError(w, r, 415, "invalid_request_error", "请使用application/json")
		return service.VideoCommand{}, false
	}
	media, params, err := mime.ParseMediaType(types[0])
	if err != nil || media != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		writeVideoContentError(w, r, 415, "invalid_request_error", "请使用UTF-8 JSON")
		return service.VideoCommand{}, false
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !videoIdempotencyHeader.MatchString(keys[0]) {
		writeVideoContentError(w, r, 400, "invalid_idempotency_key", "必须提供16至128字节单值Idempotency-Key")
		return service.VideoCommand{}, false
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "该写接口不接受查询参数")
		return service.VideoCommand{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	raw, err := io.ReadAll(r.Body)
	var req videoPlatformRequest
	if err != nil || decodeStrictJSONObject(raw, &req) != nil || strings.TrimSpace(req.Model) == "" || strings.TrimSpace(req.Prompt) == "" || (req.Seconds != nil && *req.Seconds != "5") || (req.Size != nil && *req.Size != "1280x720") {
		writeVideoContentError(w, r, 400, "invalid_request_error", "视频请求字段无效")
		return service.VideoCommand{}, false
	}
	var present map[string]json.RawMessage
	if json.Unmarshal(raw, &present) != nil {
		writeVideoContentError(w, r, 400, "invalid_request_error", "视频JSON无效")
		return service.VideoCommand{}, false
	}
	for _, field := range []string{"seconds", "size", "quote_id", "input_asset_id", "rights_confirmed", "rights_policy_version", "rights_attestation"} {
		if value, ok := present[field]; ok && strings.TrimSpace(string(value)) == "null" {
			writeVideoContentError(w, r, 400, "invalid_request_error", "视频字段不能为null")
			return service.VideoCommand{}, false
		}
	}
	if _, exists := present["input_asset_id"]; exists && req.Operation == "text_to_video" {
		writeVideoContentError(w, r, 400, "video_input_forbidden", "文生视频不能携带输入资产")
		return service.VideoCommand{}, false
	}
	if caller.APIKeyID == 0 && req.ProjectID == 0 {
		writeVideoContentError(w, r, 400, "project_required", "登录调用必须指定Project")
		return service.VideoCommand{}, false
	}
	caller.ProjectID = req.ProjectID
	return service.VideoCommand{Caller: caller, IdempotencyKey: keys[0], Model: req.Model, Prompt: req.Prompt, Operation: req.Operation, Seconds: "5", Size: "1280x720", QuoteID: req.QuoteID, InputAssetID: req.InputAssetID, RightsConfirmed: req.RightsConfirmed, RightsPolicyVersion: req.RightsPolicyVersion, RightsAttestation: req.RightsAttestation, Facade: "platform", HTTPRequestID: middleware.RequestIDFromContext(r.Context())}, true
}

func (h *VideoHandler) Quote(w http.ResponseWriter, r *http.Request) {
	command, ok := h.platformCommand(w, r)
	if !ok {
		return
	}
	if command.QuoteID != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "报价不能携带quote_id")
		return
	}
	quote, err := h.app.Quote(r.Context(), command)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	writeVideoPlatformJSON(w, r, 201, quote)
}
func (h *VideoHandler) PlatformCreate(w http.ResponseWriter, r *http.Request) {
	command, ok := h.platformCommand(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(command.QuoteID) == "" {
		writeVideoContentError(w, r, 400, "quote_required", "平台生成必须提供quote_id")
		return
	}
	result, err := h.app.Create(r.Context(), command)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	w.Header().Set("X-Molin-Request-ID", result.RequestID)
	writeVideoPlatformJSON(w, r, 202, result)
}
func writeVideoPlatformJSON(w http.ResponseWriter, r *http.Request, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response.Body{Code: 0, Message: "ok", Data: value, RequestID: middleware.RequestIDFromContext(r.Context())})
}
