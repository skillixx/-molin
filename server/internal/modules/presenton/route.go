package presenton

import (
	"net/http"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/presenton/handler"
	"molin/server/internal/modules/presenton/service"
)

// RegisterRoutes 注册 presenton 应用用户端路由（仅登录态 JWT）。
func RegisterRoutes(
	mux *http.ServeMux,
	openSvc *service.OpenService,
	jwtSecret string,
	banChecker middleware.BanChecker,
) {
	oh := handler.NewOpenHandler(openSvc)
	// 打开入口：校验开通 → 返回带票据的嵌入 URL。
	mux.Handle("GET /api/app/presenton/open",
		middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(oh.Open)))
}
