package presenton

import (
	"net/http"
	"strings"

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
	// 可用模型白名单：供前端「打开」时的模型下拉。
	mux.Handle("GET /api/app/presenton/models",
		middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(oh.Models)))
}

// RegisterProxyRoutes 注册 D2 反代路由（浏览器面向，鉴权靠一次性票据 + 会话 cookie，
// 不挂 JWT 中间件——用户在 iframe 内可能不带墨灵 JWT）。
func RegisterProxyRoutes(mux *http.ServeMux, gw *handler.GatewayHandler, pathPrefix string) {
	prefix := strings.TrimRight(pathPrefix, "/")
	// launch 为 GET 精确路径（更具体，优先于下方子树）。
	mux.Handle("GET "+prefix+"/launch", http.HandlerFunc(gw.Launch))
	// 其余 {prefix}/* 全部方法走反代。
	mux.Handle(prefix+"/", http.HandlerFunc(gw.Proxy))
}
