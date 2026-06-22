package asset

import (
	"net/http"
	"os"
	"strings"

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

	// 内部接口（不对外公开，沿用 /api/internal/* 机制：X-Internal-Token 主闸 + IP 白名单辅助防线）。
	// S2-丙2：套餐预付额度扣减，由 token 网关门面在 prepaid 模式结算时调用。
	// S2-丙3：权益余额只读查询，供门面 prepaid 转发前置闸（修 D-M2-01）。
	allowedIPs := parseAllowedIPs(os.Getenv("INTERNAL_ALLOWED_IPS"))
	internalToken := strings.TrimSpace(os.Getenv("INTERNAL_API_TOKEN"))
	ih := handler.NewInternalAssetHandler(svc, allowedIPs, internalToken)
	mux.HandleFunc("POST /api/internal/entitlement-consume", ih.ConsumeEntitlement)
	mux.HandleFunc("GET /api/internal/entitlement-balance", ih.GetEntitlementBalance)
	// S2-丙4：prepaid 额度预占/结算/释放（方案 B 根治 D-M2-01）。门面转发前 reserve，结算时 settle（多退少补），异常时 release。
	mux.HandleFunc("POST /api/internal/entitlement-reserve", ih.ReserveEntitlement)
	mux.HandleFunc("POST /api/internal/entitlement-settle", ih.SettleEntitlement)
	mux.HandleFunc("POST /api/internal/entitlement-release", ih.ReleaseEntitlement)
}

// parseAllowedIPs 解析逗号分隔的 IP 白名单字符串（与 finance_consumer 一致）。
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

// NewService 创建资产服务（供 bootstrap/app.go 使用）。
func NewService(db *gorm.DB) *service.AssetService {
	return service.NewAssetService(db)
}
