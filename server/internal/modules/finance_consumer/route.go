package finance_consumer

import (
	"net/http"
	"os"
	"strings"

	"gorm.io/gorm"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/finance_consumer/handler"
	"molin/server/internal/modules/finance_consumer/repository"
	"molin/server/internal/modules/finance_consumer/service"
	billingrepo "molin/server/internal/modules/billing/repository"
	billingsvc "molin/server/internal/modules/billing/service"
	productrepo "molin/server/internal/modules/product/repository"
)

// RegisterRoutes 将 finance_consumer 模块路由注册到 mux。
// 内部上报接口（/api/internal/*）通过 IP 白名单保护，不对外公开；
// 消费记录查询接口（F2/F3）通过 JWT 登录 + 权限码保护：
//   - F2 GET /api/product-consumption-records       需登录（强制本人过滤）
//   - F3 GET /api/admin/product-consumption-records 需登录 + wallet:view 权限
//
// jwtSecret/banChecker/iamSvc 由 bootstrap 注入，用于装配 RequireAuth / RequirePerm 中间件。
func RegisterRoutes(
	mux *http.ServeMux,
	db *gorm.DB,
	jwtSecret string,
	banChecker middleware.BanChecker,
	iamSvc middleware.IAMChecker,
) {
	// 初始化仓库
	consumptionRepo := repository.NewConsumptionRepository(db)
	billingRuleRepo := productrepo.NewBillingRuleRepository(db)
	walletRepo := billingrepo.NewWalletRepository(db)
	txRepo := billingrepo.NewTransactionRepository(db)

	// 初始化服务
	walletSvc := billingsvc.NewWalletService(db, walletRepo, txRepo)
	consumerSvc := service.NewConsumerService(db, consumptionRepo, billingRuleRepo, walletSvc)

	// 解析 IP 白名单（从环境变量 INTERNAL_ALLOWED_IPS 读取，逗号分隔）
	allowedIPs := parseAllowedIPs(os.Getenv("INTERNAL_ALLOWED_IPS"))

	h := handler.NewConsumerHandler(consumerSvc, allowedIPs)

	// 内部接口（不对外，需 IP 白名单）
	mux.HandleFunc("POST /api/internal/product-usage-events", h.HandleUsageEvent)

	// 认证中间件：用户端仅需登录
	auth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker, http.HandlerFunc(next))
	}
	// 管理端中间件：登录 + wallet:view 权限
	adminAuth := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamSvc, "wallet:view", http.HandlerFunc(next)))
	}

	// F2 用户端：查本人消费记录（强制按登录用户过滤）
	mux.Handle("GET /api/product-consumption-records", auth(h.ListMyRecords))
	// F3 管理端：查全量消费记录（wallet:view，支持 user_id 过滤）
	mux.Handle("GET /api/admin/product-consumption-records", adminAuth(h.AdminListRecords))
}

// parseAllowedIPs 解析逗号分隔的 IP 白名单字符串。
func parseAllowedIPs(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	ips := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			ips = append(ips, p)
		}
	}
	return ips
}
