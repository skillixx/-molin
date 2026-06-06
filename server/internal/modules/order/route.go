package order

import (
	"net/http"

	"gorm.io/gorm"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/order/handler"
	"molin/server/internal/modules/order/repository"
	"molin/server/internal/modules/order/service"
)

// RegisterRoutes 将 order 模块路由注册到 mux。
func RegisterRoutes(
	mux *http.ServeMux,
	db *gorm.DB,
	jwtSecret string,
	banChecker middleware.BanChecker,
	iamSvc middleware.IAMChecker,
) {
	orderRepo := repository.NewOrderRepository(db)
	orderSvc := service.NewOrderService(db, orderRepo)
	h := handler.NewOrderHandler(orderSvc)

	// 认证中间件
	auth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}
	// 管理端中间件
	adminAuth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamSvc, "order:list", http.HandlerFunc(next)))
	}

	// 用户端（需要登录，只能查自己的订单）
	mux.Handle("GET /api/orders", auth(h.ListOrders))
	mux.Handle("GET /api/orders/{id}", auth(h.GetOrder))

	// 管理端（需要登录 + order:list 权限）
	mux.Handle("GET /api/admin/orders", adminAuth(h.AdminListOrders))
	mux.Handle("GET /api/admin/orders/{id}", adminAuth(h.AdminGetOrder))
}
