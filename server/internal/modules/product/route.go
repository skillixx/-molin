package product

import (
	"net/http"

	"gorm.io/gorm"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/product/handler"
	"molin/server/internal/modules/product/repository"
	"molin/server/internal/modules/product/service"
	"molin/server/internal/modules/product/stub"
	orderrepo "molin/server/internal/modules/order/repository"
	ordersvc "molin/server/internal/modules/order/service"
)

// RegisterRoutes 将 product 模块路由注册到 mux。
// iamSvc 同时实现 service.IAMService（GetUserRoleIDs + CheckPermission）。
// billingSvc 用于购买时扣费，由 billing 模块注入（实现 service.BillingService）。
// userRepo 用于实名校验，由 product/repository/user_repo_adapter.go 提供。
func RegisterRoutes(
	mux *http.ServeMux,
	db *gorm.DB,
	jwtSecret string,
	banChecker middleware.BanChecker,
	iamSvc service.IAMService,
	billingSvc service.BillingService,
	userRepo service.UserRepository,
) {
	// 初始化仓库
	productRepo := repository.NewProductRepository(db)
	planRepo := repository.NewPlanRepository(db)
	priceRepo := repository.NewPriceRepository(db)
	accessRepo := repository.NewAccessRepository(db)

	// Week 2 使用 stub 实现，Week 3/4 后端丙接入时替换
	provisionStub := stub.NewProvisionStub()
	membershipStub := stub.NewMembershipStub()

	// 初始化服务
	pricingSvc := service.NewPricingService(priceRepo, iamSvc, membershipStub)
	productSvc := service.NewProductService(productRepo, accessRepo, planRepo, priceRepo, iamSvc)

	// 初始化 order 相关（购买接口依赖）
	orderRepo := orderrepo.NewOrderRepository(db)
	orderService := ordersvc.NewOrderService(db, orderRepo)

	purchaseSvc := service.NewPurchaseService(
		db, accessRepo, orderRepo, orderService,
		pricingSvc, billingSvc, provisionStub, iamSvc, userRepo,
	)

	// 初始化处理器
	userHandler := handler.NewProductHandler(productSvc, pricingSvc, purchaseSvc)
	adminHandler := handler.NewAdminProductHandler(productSvc)

	// 认证中间件（需要登录 + 封禁检查）
	auth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}
	// 管理端中间件（需要登录 + 对应权限）
	// iamSvc 实现 middleware.IAMChecker（包含 CheckPermission 方法）
	adminAuth := func(permCode string, next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamSvc, permCode, http.HandlerFunc(next)))
	}

	// 用户端路由（需要登录）
	mux.Handle("GET /api/products", auth(userHandler.ListProducts))
	mux.Handle("GET /api/products/{id}", auth(userHandler.GetProduct))
	mux.Handle("GET /api/products/{id}/plans", auth(userHandler.GetProductPlans))
	mux.Handle("POST /api/products/{id}/purchase", auth(userHandler.Purchase))

	// 管理端路由（需要登录 + 对应权限）
	// 只读接口使用 product:view 权限码，创建接口使用 product:create
	mux.Handle("GET /api/admin/products", adminAuth("product:view", adminHandler.ListProducts))
	mux.Handle("POST /api/admin/products", adminAuth("product:create", adminHandler.CreateProduct))
	mux.Handle("GET /api/admin/products/{id}", adminAuth("product:view", adminHandler.GetProduct))
	mux.Handle("PATCH /api/admin/products/{id}", adminAuth("product:edit", adminHandler.UpdateProduct))
	mux.Handle("PATCH /api/admin/products/{id}/status", adminAuth("product:edit", adminHandler.UpdateProductStatus))
	mux.Handle("GET /api/admin/products/{id}/plans", adminAuth("product:create", adminHandler.ListPlans))
	mux.Handle("POST /api/admin/products/{id}/plans", adminAuth("product:create", adminHandler.CreatePlan))
	mux.Handle("PATCH /api/admin/products/{id}/plans/{plan_id}", adminAuth("product:edit", adminHandler.UpdatePlan))
	mux.Handle("PATCH /api/admin/products/{id}/access", adminAuth("product:edit", adminHandler.ReplaceAccess))
	mux.Handle("PATCH /api/admin/products/{id}/prices", adminAuth("product:edit", adminHandler.ReplacePrices))
}
