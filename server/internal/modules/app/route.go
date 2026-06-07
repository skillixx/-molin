package app

import (
	"net/http"

	"gorm.io/gorm"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/app/handler"
	"molin/server/internal/modules/app/service"
)

// RegisterRoutes 注册 app 模块所有路由（应用业务详情查询 + 管理端应用/适配器 CRUD）。
func RegisterRoutes(
	mux *http.ServeMux,
	db *gorm.DB,
	jwtSecret string,
	banChecker middleware.BanChecker,
	iamSvc middleware.IAMChecker,
) {
	appSvc := service.NewAppService(db)
	adapterSvc := service.NewAdapterService(db)
	h := handler.NewAppHandler(appSvc, adapterSvc)

	// 认证中间件（需要登录）
	auth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}
	// 管理端权限中间件
	adminAuth := func(permCode string, next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamSvc, permCode, http.HandlerFunc(next)))
	}

	// 用户端路由（需登录，仅展示已上架应用详情）
	mux.Handle("GET /api/marketplace/apps/{id}", auth(h.GetAppDetail))

	// 管理端路由：应用 CRUD
	mux.Handle("GET /api/admin/apps", adminAuth("app:manage", h.AdminListApps))
	mux.Handle("GET /api/admin/apps/{id}", adminAuth("app:manage", h.AdminGetApp))
	mux.Handle("POST /api/admin/apps", adminAuth("app:manage", h.AdminCreateApp))
	mux.Handle("PATCH /api/admin/apps/{id}", adminAuth("app:manage", h.AdminUpdateApp))

	// 管理端路由：适配器管理
	mux.Handle("GET /api/admin/app-adapters", adminAuth("app:manage", h.AdminListAdapters))
	mux.Handle("POST /api/admin/app-adapters", adminAuth("app:manage", h.AdminCreateAdapter))
	mux.Handle("PATCH /api/admin/app-adapters/{id}", adminAuth("app:manage", h.AdminUpdateAdapter))
}

// NewService 创建应用服务（供 bootstrap/app.go 使用，例如 provision 模块的 AppProvisioner）。
func NewService(db *gorm.DB) *service.AppService {
	return service.NewAppService(db)
}
