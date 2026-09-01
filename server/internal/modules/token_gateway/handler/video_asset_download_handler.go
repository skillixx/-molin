package handler

import (
	"net/http"
	"net/url"
)

// 地址签发只接受路径中的原资产，客户端不能指定bucket、URL、期限或签名输入。
func (h *VideoHandler) AssetDownloadURL(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeVideoContentError(w, r, 405, "method_not_allowed", "下载地址仅支持GET")
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "下载地址不接受查询参数")
		return
	}
	result, err := h.app.AssetDownloadURL(r.Context(), caller, r.PathValue("asset_id"))
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	writeVideoPlatformJSON(w, r, 200, result)
}

// 兑换始终使用原认证主体；短签名不能替代JWT或Project SK。
func (h *VideoHandler) AssetContent(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeVideoContentError(w, r, 405, "method_not_allowed", "签名兑换仅支持GET")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 2 || len(q["expires"]) != 1 || len(q["signature"]) != 1 {
		writeVideoContentError(w, r, 400, "invalid_request_error", "下载参数无效")
		return
	}
	content, err := h.app.GetSignedAssetContent(r.Context(), caller, r.PathValue("asset_id"), q.Get("expires"), q.Get("signature"))
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	defer content.Close()
	w.Header().Set("Referrer-Policy", "no-referrer")
	serveVideoContent(w, r, VideoHTTPContent{Size: content.Size, SHA256: content.SHA256, OpenRange: content.OpenRange, BeforeWrite: content.BeforeWrite}, content.MIMEType)
}
