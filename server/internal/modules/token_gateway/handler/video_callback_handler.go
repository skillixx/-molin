package handler

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"molin/server/internal/modules/token_gateway/service"
)

type VideoCallbackHandler struct {
	app     *service.VideoCallbackService
	enabled bool
}

func NewVideoCallbackHandler(app *service.VideoCallbackService, enabled bool) *VideoCallbackHandler {
	return &VideoCallbackHandler{app: app, enabled: enabled}
}

// Provider回调不接受JWT/SK作为验签替代；所有头和值都先严格校验，不记录正文或签名。
func (h *VideoCallbackHandler) Receive(w http.ResponseWriter, r *http.Request) {
	if h == nil || !h.enabled || h.app == nil {
		writeVideoContentError(w, r, 503, "video_callback_unavailable", "视频回调未启用")
		return
	}
	if r.PathValue("provider_code") != "fake-native-async" {
		writeVideoContentError(w, r, 404, "video_callback_not_found", "回调目标不存在")
		return
	}
	if r.URL.RawQuery != "" || r.URL.EscapedPath() != "/api/internal/ai/provider-callbacks/fake-native-async" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "回调路径无效")
		return
	}
	contentTypes := r.Header.Values("Content-Type")
	if len(contentTypes) != 1 {
		writeVideoContentError(w, r, 415, "invalid_request_error", "请使用UTF-8 JSON")
		return
	}
	kind, params, err := mime.ParseMediaType(contentTypes[0])
	if err != nil || kind != "application/json" || len(params) > 1 || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		writeVideoContentError(w, r, 415, "invalid_request_error", "请使用UTF-8 JSON")
		return
	}
	for name := range params {
		if name != "charset" {
			writeVideoContentError(w, r, 415, "invalid_request_error", "不支持的JSON内容参数")
			return
		}
	}
	// 此合同按收到的原始JSON字节验签，不接受未约定的压缩或内容转换声明。
	if len(r.Header.Values("Content-Encoding")) != 0 {
		writeVideoContentError(w, r, 415, "invalid_request_error", "回调不支持内容编码")
		return
	}
	values := make([]string, 3)
	for i, name := range []string{"X-Molin-Callback-Timestamp", "X-Molin-Callback-Nonce", "X-Molin-Callback-Signature"} {
		v := r.Header.Values(name)
		if len(v) == 0 {
			writeVideoContentError(w, r, 401, "video_callback_authentication", "回调认证失败")
			return
		}
		if len(v) != 1 {
			writeVideoContentError(w, r, 400, "invalid_request_error", "回调头必须为单值")
			return
		}
		values[i] = v[0]
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeVideoContentError(w, r, 400, "invalid_request_error", "回调正文无效")
		return
	}
	ack, err := h.app.Receive(r.Context(), service.VideoCallbackRequest{ProviderCode: r.PathValue("provider_code"), Method: r.Method, Path: r.URL.EscapedPath(), Timestamp: values[0], Nonce: values[1], Signature: values[2], Body: raw})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrVideoCallbackRequest):
			writeVideoContentError(w, r, 400, "invalid_request_error", "回调请求无效")
		case errors.Is(err, service.ErrVideoCallbackAuthentication):
			writeVideoContentError(w, r, 401, "video_callback_authentication", "回调认证失败")
		case errors.Is(err, service.ErrVideoCallbackNotFound):
			writeVideoContentError(w, r, 404, "video_callback_not_found", "回调目标不存在")
		case errors.Is(err, service.ErrVideoCallbackConflict):
			writeVideoContentError(w, r, 409, "video_callback_conflict", "回调重放冲突")
		default:
			writeVideoContentError(w, r, 503, "video_callback_unavailable", "回调处理暂不可确认")
		}
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ack)
}
