package billing

import (
	"net/http"

	"gorm.io/gorm"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/billing/handler"
	"molin/server/internal/modules/billing/repository"
	"molin/server/internal/modules/billing/service"
	orderrepo "molin/server/internal/modules/order/repository"
	ordersvc "molin/server/internal/modules/order/service"
)

// RegisterRoutes 将 billing 模块路由注册到 mux。
// notifyBodyKey 为支付回调报文 AES-256-GCM 加密密钥（32 字节），从 cfg.NotifyBodyKey 传入。
func RegisterRoutes(
	mux *http.ServeMux,
	db *gorm.DB,
	jwtSecret string,
	banChecker middleware.BanChecker,
	iamSvc middleware.IAMChecker,
	notifyBodyKey string,
) {
	// 初始化仓库
	walletRepo := repository.NewWalletRepository(db)
	txRepo := repository.NewTransactionRepository(db)
	paymentRepo := repository.NewPaymentRepository(db)
	orderRepo := orderrepo.NewOrderRepository(db)

	// 初始化服务
	walletSvc := service.NewWalletService(db, walletRepo, txRepo)
	orderSvc := ordersvc.NewOrderService(db, orderRepo)
	wechatVerifier := service.NewWechatVerifier()
	alipayVerifier := service.NewAlipayVerifier()
	paymentSvc := service.NewPaymentService(db, paymentRepo, walletSvc, orderRepo, orderSvc, wechatVerifier, alipayVerifier, notifyBodyKey)

	// 初始化处理器
	billingH := handler.NewBillingHandler(walletSvc, paymentSvc, txRepo)
	adminBillingH := handler.NewAdminBillingHandler(walletSvc, walletRepo, txRepo, paymentRepo, notifyBodyKey)
	paymentH := handler.NewPaymentHandler(paymentSvc)

	// 认证中间件
	auth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}
	// 管理端中间件
	adminAuth := func(permCode string, next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamSvc, permCode, http.HandlerFunc(next)))
	}

	// 用户端路由（需要登录）
	mux.Handle("GET /api/wallet", auth(billingH.GetWallet))
	mux.Handle("GET /api/wallet/transactions", auth(billingH.GetTransactions))
	mux.Handle("POST /api/recharge/orders", auth(billingH.CreateRechargeOrder))

	// 支付回调（无需登录，需签名校验）
	mux.HandleFunc("POST /api/payments/notify/{provider}", paymentH.HandleNotify)

	// 管理端路由（需要登录 + wallet:view 权限）
	mux.Handle("GET /api/admin/users/{id}/wallet", adminAuth("wallet:view", adminBillingH.GetUserWallet))
	mux.Handle("GET /api/admin/wallet-transactions", adminAuth("wallet:view", adminBillingH.ListAllTransactions))
	mux.Handle("PATCH /api/admin/users/{id}/wallet/freeze", adminAuth("wallet:manage", adminBillingH.FreezeUserWallet))
	mux.Handle("GET /api/admin/payment-callbacks", adminAuth("wallet:view", adminBillingH.ListPaymentCallbacks))
}

// NewWalletService 供外部模块（product/finance_consumer）注入，返回独立的 WalletService 实例。
// 用于在 bootstrap/app.go 中注入到 product 模块的 BillingService 接口。
func NewWalletService(db *gorm.DB) *service.WalletService {
	walletRepo := repository.NewWalletRepository(db)
	txRepo := repository.NewTransactionRepository(db)
	return service.NewWalletService(db, walletRepo, txRepo)
}
