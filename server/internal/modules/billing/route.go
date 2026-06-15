package billing

import (
	"log"
	"net/http"
	"os"
	"strings"

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
	// 支付验签公钥从环境变量注入（PEM 文本或文件路径，二选一）：
	//   WECHAT_PLATFORM_PUBLIC_KEY —— 微信支付平台证书公钥（APIv3 RSA-SHA256 验签）
	//   ALIPAY_PUBLIC_KEY          —— 支付宝公钥（RSA2 验签）
	// 未配置时对应渠道验签器 fail-closed：所有回调一律拒绝（资金回调无法验签必须拒绝）。
	wechatVerifier := service.NewWechatVerifier(loadPEMFromEnv("WECHAT_PLATFORM_PUBLIC_KEY"))
	alipayVerifier := service.NewAlipayVerifier(loadPEMFromEnv("ALIPAY_PUBLIC_KEY"))
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

// loadPEMFromEnv 从环境变量读取 PEM 公钥配置，支持两种形式（二选一）：
//   - 直接的 PEM 文本（以 "-----BEGIN" 开头）；
//   - 指向 PEM 文件的绝对/相对路径。
// 未配置或读取失败时返回空串（验签器据此 fail-closed）；仅记录是否注入成功，不打印密钥内容。
func loadPEMFromEnv(envKey string) string {
	raw := strings.TrimSpace(os.Getenv(envKey))
	if raw == "" {
		log.Printf("[WARN] %s 未配置，对应支付渠道回调验签将 fail-closed（全部拒绝）", envKey)
		return ""
	}
	// PEM 文本形式：直接返回。
	if strings.HasPrefix(raw, "-----BEGIN") {
		return raw
	}
	// 否则视为文件路径，读取文件内容。
	data, err := os.ReadFile(raw)
	if err != nil {
		log.Printf("[WARN] 读取 %s 指定的公钥文件失败，验签将 fail-closed", envKey)
		return ""
	}
	return string(data)
}

// NewWalletService 供外部模块（product/finance_consumer）注入，返回独立的 WalletService 实例。
// 用于在 bootstrap/app.go 中注入到 product 模块的 BillingService 接口。
func NewWalletService(db *gorm.DB) *service.WalletService {
	walletRepo := repository.NewWalletRepository(db)
	txRepo := repository.NewTransactionRepository(db)
	return service.NewWalletService(db, walletRepo, txRepo)
}
