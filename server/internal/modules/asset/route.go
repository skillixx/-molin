package asset

import (
	"net/http"

	"gorm.io/gorm"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/asset/handler"
	"molin/server/internal/modules/asset/service"
)

// RegisterRoutes 注册 asset 模块所有路由。
func RegisterRoutes(
	mux *http.ServeMux,
	db *gorm.DB,
	jwtSecret string,
	banChecker middleware.BanChecker,
	iamSvc middleware.IAMChecker,
) {
	svc := service.NewAssetService(db)
	h := handler.NewAssetHandler(svc)

	// 认证中间件（需要登录）
	auth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}
	// 管理端权限中间件
	adminAuth := func(permCode string, next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamSvc, permCode, http.HandlerFunc(next)))
	}

	// 用户端路由
	mux.Handle("GET /api/my/assets", auth(h.ListMyAssets))
	mux.Handle("GET /api/my/assets/{id}", auth(h.GetMyAsset))
	mux.Handle("GET /api/my/entitlements", auth(h.ListMyEntitlements))

	// 管理端路由
	mux.Handle("GET /api/admin/assets", adminAuth("asset:view", h.AdminListAssets))
	mux.Handle("GET /api/admin/users/{id}/assets", adminAuth("asset:view", h.AdminListUserAssets))
	mux.Handle("PATCH /api/admin/assets/{id}", adminAuth("asset:manage", h.AdminUpdateAsset))
}

// NewService 创建资产服务（供 bootstrap/app.go 使用）。
func NewService(db *gorm.DB) *service.AssetService {
	return service.NewAssetService(db)
}
