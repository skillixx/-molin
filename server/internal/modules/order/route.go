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
// billingSvc 用于 O3 支付时在事务内扣费（实现 service.BillingService，由 billing 模块经 bootstrap 适配注入）。
// provisionSvc 用于支付成功后异步开通（实现 service.ProvisionService）；nil 时降级为空操作 stub。
func RegisterRoutes(
	mux *http.ServeMux,
	db *gorm.DB,
	jwtSecret string,
	banChecker middleware.BanChecker,
	iamSvc middleware.IAMChecker,
	billingSvc service.BillingService,
	provisionSvc service.ProvisionService,
) {
	orderRepo := repository.NewOrderRepository(db)
	orderSvc := service.NewOrderService(db, orderRepo)
	// 支付编排服务（O3/O4）：扣费 + 状态机流转 + 异步开通
	paySvc := service.NewPayService(db, orderRepo, billingSvc, provisionSvc)
	h := handler.NewOrderHandler(orderSvc, paySvc)

	// 认证中间件
	auth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}
	// 管理端中间件
	adminAuth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamSvc, "order:list", http.HandlerFunc(next)))
	}

	// 用户端（需要登录，只能操作自己的订单）
	mux.Handle("GET /api/orders", auth(h.ListOrders))
	mux.Handle("GET /api/orders/{id}", auth(h.GetOrder))
	// O3 支付存量 pending 订单（需 Idempotency-Key）；O4 取消 pending 订单
	mux.Handle("POST /api/orders/{id}/pay", auth(h.Pay))
	mux.Handle("POST /api/orders/{id}/cancel", auth(h.Cancel))

	// 管理端（需要登录 + order:list 权限）
	mux.Handle("GET /api/admin/orders", adminAuth(h.AdminListOrders))
	mux.Handle("GET /api/admin/orders/{id}", adminAuth(h.AdminGetOrder))
}
