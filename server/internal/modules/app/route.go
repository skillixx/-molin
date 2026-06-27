package app

import (
	"net/http"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/app/handler"
	"molin/server/internal/modules/app/service"
)

// RegisterRoutes 注册 app 模块所有路由（应用业务详情查询 + 管理端应用/适配器 CRUD + 进入应用 SSO 票据）。
func RegisterRoutes(
	mux *http.ServeMux,
	db *gorm.DB,
	jwtSecret string,
	banChecker middleware.BanChecker,
	iamSvc middleware.IAMChecker,
	redisClient *redis.Client,
) {
	appSvc := service.NewAppService(db)
	adapterSvc := service.NewAdapterService(db)
	h := handler.NewAppHandler(appSvc, adapterSvc)

	// 进入应用（阶段二 SSO 一次性票据）：用户端签发 + 内部接口校验消费。
	launchSvc := service.NewLaunchService(db, redisClient)
	allowedIPs := parseAllowedIPs(os.Getenv("INTERNAL_ALLOWED_IPS"))
	internalToken := strings.TrimSpace(os.Getenv("INTERNAL_API_TOKEN"))
	lh := handler.NewLaunchHandler(launchSvc, allowedIPs, internalToken)

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

	// 进入应用：用户端签发一次性票据（需登录，校验使用权）
	mux.Handle("POST /api/apps/{id}/launch", auth(lh.LaunchApp))
	// 进入应用：内部接口，应用后端用票据换身份（X-Internal-Token + IP 白名单，不对外公开）
	mux.HandleFunc("POST /api/internal/app-launch/verify", lh.VerifyLaunch)

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

// parseAllowedIPs 解析逗号分隔的 IP 白名单字符串（与 asset/finance_consumer 内部接口一致）。
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

// NewService 创建应用服务（供 bootstrap/app.go 使用，例如 provision 模块的 AppProvisioner）。
func NewService(db *gorm.DB) *service.AppService {
	return service.NewAppService(db)
}
