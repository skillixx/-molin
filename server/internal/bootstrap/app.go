package bootstrap

import (
	"fmt"
	"net/http"

	"molin/server/internal/config"
	"molin/server/internal/httpserver"
	"molin/server/internal/middleware"
	authmod "molin/server/internal/modules/auth"
	authrep "molin/server/internal/modules/auth/repository"
	authsvc "molin/server/internal/modules/auth/service"
	billingmod "molin/server/internal/modules/billing"
	billingsvc "molin/server/internal/modules/billing/service"
	billingrepo "molin/server/internal/modules/billing/repository"
	financemod "molin/server/internal/modules/finance_consumer"
	iammod "molin/server/internal/modules/iam"
	iamrep "molin/server/internal/modules/iam/repository"
	iamsvc "molin/server/internal/modules/iam/service"
	identitymod "molin/server/internal/modules/identity"
	identityrep "molin/server/internal/modules/identity/repository"
	identitysvc "molin/server/internal/modules/identity/service"
	ordermod "molin/server/internal/modules/order"
	productmod "molin/server/internal/modules/product"
	productrep "molin/server/internal/modules/product/repository"
	"molin/server/pkg/cache"
	"molin/server/pkg/db"
	"molin/server/pkg/response"
)

// App 是应用实例，持有配置和 HTTP 服务器。
type App struct {
	Config config.Config
	Server *http.Server
}

// NewApp 初始化所有基础设施和模块，完成依赖注入，返回可启动的 App。
func NewApp() (*App, error) {
	cfg := config.Load()

	// 初始化数据库连接
	gormDB, err := db.New(cfg.MySQLHost, cfg.MySQLPort, cfg.MySQLUser, cfg.MySQLPassword, cfg.MySQLDatabase)
	if err != nil {
		return nil, fmt.Errorf("数据库初始化失败: %w", err)
	}

	// 初始化 Redis 连接
	redisClient, err := cache.New(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		return nil, fmt.Errorf("Redis 初始化失败: %w", err)
	}

	// ——— Auth 模块 ———
	userRepo := authrep.NewUserRepository(gormDB)
	sessionRepo := authrep.NewSessionRepository(gormDB)
	verificationRepo := authrep.NewVerificationRepository(gormDB)
	loginLogRepo := authrep.NewLoginLogRepository(gormDB)
	verifySvc := authsvc.NewVerificationService(verificationRepo)
	// 传入 redisClient，用于封禁用户黑名单（P1-01 修复）
	authService := authsvc.NewAuthService(userRepo, sessionRepo, verifySvc, loginLogRepo, cfg, redisClient)

	// ——— IAM 模块 ———
	roleRepo := iamrep.NewRoleRepository(gormDB)
	permRepo := iamrep.NewPermissionRepository(gormDB)
	userRoleRepo := iamrep.NewUserRoleRepository(gormDB)
	overrideRepo := iamrep.NewOverrideRepository(gormDB)
	cacheSvc := iamsvc.NewCacheService(redisClient)
	iamService := iamsvc.NewIAMService(roleRepo, permRepo, userRoleRepo, overrideRepo, cacheSvc)

	// ——— Identity 模块 ———
	identityRepo := identityrep.NewIdentityRepository(gormDB)
	identityService := identitysvc.NewIdentityService(identityRepo, userRepo, gormDB, cfg)

	// ——— Billing 模块（WalletService 供 product 购买接口注入）———
	walletRepo := billingrepo.NewWalletRepository(gormDB)
	txRepo := billingrepo.NewTransactionRepository(gormDB)
	walletService := billingsvc.NewWalletService(gormDB, walletRepo, txRepo)

	// ——— Product 模块（用户信息适配器，用于实名校验）———
	userRepoAdapter := productrep.NewUserRepoAdapter(gormDB)

	// ——— 构建路由 ———
	mux := http.NewServeMux()

	// 健康检查（无需鉴权）
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/ready", func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, http.StatusOK, map[string]string{"version": "0.1.0"})
	})

	// 注册各模块路由（authService 实现 BanChecker 接口，用于封禁黑名单检查）
	authmod.RegisterRoutes(mux, authService, verifySvc, cfg, iamService)
	iammod.RegisterRoutes(mux, iamService, cfg.JWTSecret, authService)
	identitymod.RegisterRoutes(mux, identityService, iamService, cfg.JWTSecret, authService)

	// 注册 billing 模块（钱包、充值、支付回调）
	billingmod.RegisterRoutes(mux, gormDB, cfg.JWTSecret, authService, iamService)

	// 注册 order 模块（订单查询）
	ordermod.RegisterRoutes(mux, gormDB, cfg.JWTSecret, authService, iamService)

	// 注册 product 模块（商品、套餐、价格、购买）
	// walletService 实现 BillingService 接口（提供 Deduct 方法）
	productmod.RegisterRoutes(mux, gormDB, cfg.JWTSecret, authService, iamService, walletService, userRepoAdapter)

	// 注册 finance_consumer 模块（内部消费事件接口，IP 白名单保护）
	financemod.RegisterRoutes(mux, gormDB)

	// 全局中间件（最外层）
	handler := middleware.RequestID(middleware.Recovery(middleware.Logger(mux)))

	srv := httpserver.New(cfg, handler)
	return &App{Config: cfg, Server: srv}, nil
}

func (a *App) Run() error {
	fmt.Printf("API server 启动，监听 %s\n", a.Server.Addr)
	return a.Server.ListenAndServe()
}
