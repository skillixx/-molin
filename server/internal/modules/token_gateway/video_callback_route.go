package token_gateway

import (
	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/handler"
	"molin/server/internal/modules/token_gateway/service"
	"net/http"
)

// 内部回调显式独立装配，默认bootstrap不挂载；缺失专用Fake依赖时不能退化为无鉴权接收。
func RegisterVideoInternalRoutes(mux *http.ServeMux, app *service.VideoCallbackService, enabled bool) {
	if mux == nil {
		return
	}
	h := handler.NewVideoCallbackHandler(app, enabled)
	mux.Handle("POST /api/internal/ai/provider-callbacks/{provider_code}", middleware.RequestID(http.HandlerFunc(h.Receive)))
}
