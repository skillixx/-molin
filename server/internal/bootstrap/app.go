package bootstrap

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"molin/server/internal/config"
	"molin/server/internal/httpserver"
	"molin/server/internal/jobs"
	"molin/server/internal/middleware"
	appmod "molin/server/internal/modules/app"
	assetmod "molin/server/internal/modules/asset"
	assetdto "molin/server/internal/modules/asset/dto"
	assetsvc "molin/server/internal/modules/asset/service"
	auditrepository "molin/server/internal/modules/audit/repository"
	auditservice "molin/server/internal/modules/audit/service"
	authmod "molin/server/internal/modules/auth"
	authrep "molin/server/internal/modules/auth/repository"
	authsvc "molin/server/internal/modules/auth/service"
	billingmod "molin/server/internal/modules/billing"
	billingrepo "molin/server/internal/modules/billing/repository"
	billingsvc "molin/server/internal/modules/billing/service"
	contentmod "molin/server/internal/modules/content"
	financemod "molin/server/internal/modules/finance_consumer"
	iammod "molin/server/internal/modules/iam"
	iamrep "molin/server/internal/modules/iam/repository"
	iamsvc "molin/server/internal/modules/iam/service"
	identitymod "molin/server/internal/modules/identity"
	identityrep "molin/server/internal/modules/identity/repository"
	identitysvc "molin/server/internal/modules/identity/service"
	membershipmod "molin/server/internal/modules/membership"
	membershipsvc "molin/server/internal/modules/membership/service"
	ordermod "molin/server/internal/modules/order"
	productmod "molin/server/internal/modules/product"
	productrep "molin/server/internal/modules/product/repository"
	productservice "molin/server/internal/modules/product/service"
	provisionmod "molin/server/internal/modules/provision"
	provisionhandler "molin/server/internal/modules/provision/handler"
	provisionsvc "molin/server/internal/modules/provision/service"
	"molin/server/pkg/cache"
	"molin/server/pkg/db"
	"molin/server/pkg/response"
)

// App 是应用实例，持有配置和 HTTP 服务器。
type App struct {
	Config config.Config
	Server *http.Server
}

// ——— Adapter 私有类型，仅在 bootstrap 内使用 ———

// membershipAdapter 将 *membershipsvc.MembershipService 适配为 productservice.MembershipService 接口。
// 避免 membership 模块直接导入 product/service 包（循环导入）。
type membershipAdapter struct {
	svc *membershipsvc.MembershipService
}

func (a *membershipAdapter) GetActive(ctx context.Context, userID uint64) (*productservice.MembershipInfo, error) {
	m, err := a.svc.GetActiveMembership(ctx, userID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, nil
	}
	return &productservice.MembershipInfo{LevelID: m.LevelID}, nil
}

// HasActiveLevelIn 透传至 membership 模块，校验用户是否拥有命中给定等级集合的有效会员资格。
// 用于 product 模块"会员专属商品"购买门槛校验（修复 P1 缺陷：非会员可绕过会员专属门槛下单）。
func (a *membershipAdapter) HasActiveLevelIn(ctx context.Context, userID uint64, levelIDs []uint64) (bool, error) {
	return a.svc.HasActiveLevelIn(ctx, userID, levelIDs)
}

// assetProvisionAdapter 将 *assetsvc.AssetService 适配为 provisionsvc.AssetCreator 接口。
// provision 模块内定义了自己的 CreateAssetReq/Result 类型，此 adapter 做字段映射。
type assetProvisionAdapter struct {
	svc *assetsvc.AssetService
}

func (a *assetProvisionAdapter) CreateAsset(ctx context.Context, req provisionsvc.CreateAssetReq) (*provisionsvc.CreateAssetResult, error) {
	// 将 provision 模块的请求类型映射到 asset 模块的 DTO
	assetReq := assetdto.CreateAssetReq{
		UserID:             req.UserID,
		AssetType:          req.AssetType,
		ProductID:          req.ProductID,
		PlanID:             req.PlanID,
		OrderID:            req.OrderID,
		BusinessInstanceID: req.BusinessInstanceID,
		ExpiresAt:          req.ExpiresAt,
		QuotaConfig:        req.QuotaConfig,
	}
	result, err := a.svc.CreateAsset(ctx, assetReq)
	if err != nil {
		return nil, err
	}
	return &provisionsvc.CreateAssetResult{AssetID: result.AssetID}, nil
}

// iamRoleGetterAdapter 将 *iamsvc.IAMService 适配为 contenthandler.IAMRoleGetter 接口。
// IAMService 提供 GetUserRoles 返回 []Role（含 Code 字段），此 adapter 提取 code 列表。
type iamRoleGetterAdapter struct {
	svc *iamsvc.IAMService
}

func (a *iamRoleGetterAdapter) GetUserRoleCodes(ctx context.Context, userID uint64) ([]string, error) {
	roles, err := a.svc.GetUserRoles(ctx, userID)
	if err != nil {
		return nil, err
	}
	codes := make([]string, len(roles))
	for i, r := range roles {
		codes[i] = r.Code
	}
	return codes, nil
}

// membershipCheckerAdapter 将 *membershipsvc.MembershipService 适配为 contenthandler.MembershipChecker 接口。
type membershipCheckerAdapter struct {
	svc *membershipsvc.MembershipService
}

func (a *membershipCheckerAdapter) HasActiveMembership(ctx context.Context, userID uint64) (bool, error) {
	m, err := a.svc.GetActiveMembership(ctx, userID)
	if err != nil {
		return false, err
	}
	return m != nil, nil
}

// productRouteAdapter 将真实 provisionService 适配传入 product 模块路由。
// provision service 已经实现了 product/service.ProvisionService 接口（Provision 方法签名完全匹配），
// 所以可以直接传入，无需额外适配。

// NewApp 初始化所有基础设施和模块，完成依赖注入，返回可启动的 App。
func NewApp() (*App, error) {
	cfg := config.Load()

	// D-46：安全密钥必填校验，任一为空则拒绝启动，避免 HMAC 退化为无密钥 hash
	if cfg.IDCardHMACSecret == "" {
		log.Fatal("[security] ID_CARD_HMAC_SECRET 未配置，拒绝启动")
	}
	if cfg.JWTSecret == "" {
		log.Fatal("[security] JWT_SECRET 未配置，拒绝启动")
	}
	if cfg.RefreshTokenSecret == "" {
		log.Fatal("[security] REFRESH_TOKEN_SECRET 未配置，拒绝启动")
	}

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

	// ——— Audit 模块（独立审计日志服务，供 auth/iam/identity 等模块调用写入）———
	// A-04：审计日志读写能力从 iam 模块迁出，独立为 audit 模块
	auditRepo := auditrepository.NewAuditLogRepository(gormDB)
	auditSvc := auditservice.NewAuditService(auditRepo)

	// ——— IAM 模块 ———
	// 提前于 Auth 模块构建：A-10 需要将 iamService 作为 PermissionResolver 注入 AuthService，
	// 用于 GET /api/me/permissions 计算最终生效权限码。IAM 模块构建仅依赖 gormDB/redisClient/auditSvc，
	// 与 Auth 模块构建顺序互换不影响其他依赖关系。
	roleRepo := iamrep.NewRoleRepository(gormDB)
	permRepo := iamrep.NewPermissionRepository(gormDB)
	userRoleRepo := iamrep.NewUserRoleRepository(gormDB)
	overrideRepo := iamrep.NewOverrideRepository(gormDB)
	cacheSvc := iamsvc.NewCacheService(redisClient)
	groupRepo := iamrep.NewGroupRepository(gormDB)
	// Phase 2：IAMService 注入 groupRepo，使 CheckPermission 合并角色权限与组权限
	// A-04：审计日志查询能力改为注入独立的 auditSvc
	iamService := iamsvc.NewIAMService(roleRepo, permRepo, userRoleRepo, overrideRepo, groupRepo, auditSvc, cacheSvc)
	// D-62：注入 permRepo，供 AddGroupPermission 校验权限码存在性
	groupService := iamsvc.NewGroupService(groupRepo, permRepo, gormDB, cacheSvc)
	// Phase 3：ScopeService 解析管理员数据范围（scope:all 超管 / 组管理员可见集合）
	scopeService := iamsvc.NewScopeService(groupRepo, iamService, cacheSvc)

	// ——— Auth 模块 ———
	userRepo := authrep.NewUserRepository(gormDB)
	sessionRepo := authrep.NewSessionRepository(gormDB)
	verificationRepo := authrep.NewVerificationRepository(gormDB)
	loginLogRepo := authrep.NewLoginLogRepository(gormDB)
	verifySvc := authsvc.NewVerificationService(verificationRepo)
	// 传入 redisClient，用于封禁用户黑名单（P1-01 修复）；传入 auditSvc 用于封禁/解封审计记录（A-05）；
	// 传入 iamService 作为 PermissionResolver，用于 GET /api/me/permissions（A-10）
	authService := authsvc.NewAuthService(userRepo, sessionRepo, verifySvc, loginLogRepo, cfg, redisClient, auditSvc, iamService, gormDB)

	// ——— Identity 模块 ———
	// D-04：注入 auditSvc，用于审核操作写全局审计日志
	identityRepo := identityrep.NewIdentityRepository(gormDB)
	identityService := identitysvc.NewIdentityService(identityRepo, userRepo, gormDB, cfg, auditSvc)

	// ——— Billing 模块（WalletService 供 product 购买接口注入）———
	walletRepo := billingrepo.NewWalletRepository(gormDB)
	txRepo := billingrepo.NewTransactionRepository(gormDB)
	walletService := billingsvc.NewWalletService(gormDB, walletRepo, txRepo)

	// ——— Product 模块（用户信息适配器，用于实名校验）———
	userRepoAdapter := productrep.NewUserRepoAdapter(gormDB)
	productRepo := productrep.NewProductRepository(gormDB)
	planRepo := productrep.NewPlanRepository(gormDB)

	// ——— Membership 模块 ———
	membershipService := membershipsvc.NewMembershipService(gormDB)
	// 适配为 product/service.MembershipService 接口（避免循环导入）
	membershipAdapt := &membershipAdapter{svc: membershipService}

	// ——— Asset 模块 ———
	assetService := assetsvc.NewAssetService(gormDB)
	// 适配为 provision/service.AssetCreator 接口（字段类型映射）
	assetAdapt := &assetProvisionAdapter{svc: assetService}

	// ——— Provision 模块 ———
	provisionService := provisionmod.NewProvisionService(productRepo, planRepo, assetAdapt)
	// 注册应用类商品处理器（product_type = "application"）
	appProvisioner := provisionhandler.NewAppProvisioner(productRepo)
	provisionService.RegisterHandler("application", appProvisioner)

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
	authmod.RegisterRoutes(mux, authService, verifySvc, cfg, iamService, scopeService, redisClient)
	iammod.RegisterRoutes(mux, iamService, groupService, cfg.JWTSecret, authService, authService)
	identitymod.RegisterRoutes(mux, identityService, iamService, cfg.JWTSecret, authService, authService)

	// 注册 billing 模块（钱包、充值、支付回调），传入 notify_body 加密密钥
	billingmod.RegisterRoutes(mux, gormDB, cfg.JWTSecret, authService, iamService, cfg.NotifyBodyKey)

	// 注册 order 模块（订单查询）
	ordermod.RegisterRoutes(mux, gormDB, cfg.JWTSecret, authService, iamService)

	// 注册 product 模块（商品、套餐、价格、购买）
	// provisionService 直接满足 productservice.ProvisionService 接口（Provision 方法签名完全匹配）
	// membershipAdapt 满足 productservice.MembershipService 接口
	productmod.RegisterRoutes(mux, gormDB, cfg.JWTSecret, authService, iamService, walletService, userRepoAdapter,
		provisionService, membershipAdapt)

	// 注册 finance_consumer 模块（内部消费事件接口，IP 白名单保护）
	financemod.RegisterRoutes(mux, gormDB)

	// 注册 asset 模块（用户资产 + 管理端资产管理）
	assetmod.RegisterRoutes(mux, gormDB, cfg.JWTSecret, authService, iamService)

	// 注册 membership 模块（会员等级 + 用户会员查询/管理）
	membershipmod.RegisterRoutes(mux, gormDB, cfg.JWTSecret, authService, iamService)

	// content 模块所需的跨模块 adapter
	iamRoleGetter := &iamRoleGetterAdapter{svc: iamService}
	membershipChecker := &membershipCheckerAdapter{svc: membershipService}

	// 注册 content 模块（公告 + 帮助文档）
	contentmod.RegisterRoutes(mux, gormDB, cfg.JWTSecret, authService, iamService, iamRoleGetter, membershipChecker)

	// 注册 app 模块（应用业务详情 + 适配器管理；与 product 模块的商品/套餐解耦）
	appmod.RegisterRoutes(mux, gormDB, cfg.JWTSecret, authService, iamService)

	// 启动定时任务：到期资产处理（后台 goroutine，随应用生命周期运行）
	go jobs.NewExpireAssetsJob(gormDB).Start(context.Background())

	// 全局中间件（最外层）
	handler := middleware.RequestID(middleware.Recovery(middleware.Logger(mux)))

	srv := httpserver.New(cfg, handler)
	return &App{Config: cfg, Server: srv}, nil
}

func (a *App) Run() error {
	fmt.Printf("API server 启动，监听 %s\n", a.Server.Addr)
	return a.Server.ListenAndServe()
}
