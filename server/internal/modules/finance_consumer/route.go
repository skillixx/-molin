package finance_consumer

import (
	"net/http"
	"os"
	"strings"

	"gorm.io/gorm"

	"molin/server/internal/modules/finance_consumer/handler"
	"molin/server/internal/modules/finance_consumer/repository"
	"molin/server/internal/modules/finance_consumer/service"
	billingrepo "molin/server/internal/modules/billing/repository"
	billingsvc "molin/server/internal/modules/billing/service"
	productrepo "molin/server/internal/modules/product/repository"
)

// RegisterRoutes 将 finance_consumer 模块路由注册到 mux。
// 内部接口（/api/internal/*）通过 IP 白名单保护，不对外公开。
func RegisterRoutes(mux *http.ServeMux, db *gorm.DB) {
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
