package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

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
	authdto "molin/server/internal/modules/auth/dto"
	authrep "molin/server/internal/modules/auth/repository"
	authsvc "molin/server/internal/modules/auth/service"
	billingmod "molin/server/internal/modules/billing"
	billingrepo "molin/server/internal/modules/billing/repository"
	billingsvc "molin/server/internal/modules/billing/service"
	contentmod "molin/server/internal/modules/content"
	financemod "molin/server/internal/modules/finance_consumer"
	financemodel "molin/server/internal/modules/finance_consumer/model"
	financerepo "molin/server/internal/modules/finance_consumer/repository"
	financesvc "molin/server/internal/modules/finance_consumer/service"
	iammod "molin/server/internal/modules/iam"
	iamrep "molin/server/internal/modules/iam/repository"
	iamsvc "molin/server/internal/modules/iam/service"
	identitymod "molin/server/internal/modules/identity"
	identityrep "molin/server/internal/modules/identity/repository"
	identitysvc "molin/server/internal/modules/identity/service"
	membershipmod "molin/server/internal/modules/membership"
	membershipsvc "molin/server/internal/modules/membership/service"
	ordermod "molin/server/internal/modules/order"
	ordersvc "molin/server/internal/modules/order/service"
	productmod "molin/server/internal/modules/product"
	productrep "molin/server/internal/modules/product/repository"
	productservice "molin/server/internal/modules/product/service"
	provisionmod "molin/server/internal/modules/provision"
	provisionhandler "molin/server/internal/modules/provision/handler"
	provisionsvc "molin/server/internal/modules/provision/service"
	tokengatewaymod "molin/server/internal/modules/token_gateway"
	tokengatewaysvc "molin/server/internal/modules/token_gateway/service"
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

// assetProvisionAdapter 将 *assetsvc.AssetService 适配为 provisionsvc.AssetManager 接口。
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

// GetAssetProductID 返回资产所属商品 ID（供 provision 按 product_type 路由生命周期操作）。
func (a *assetProvisionAdapter) GetAssetProductID(ctx context.Context, assetID uint64) (uint64, error) {
	asset, err := a.svc.GetAsset(ctx, assetID)
	if err != nil {
		return 0, err
	}
	return asset.ProductID, nil
}

func (a *assetProvisionAdapter) CancelAsset(ctx context.Context, assetID, operatorID uint64, reason string) error {
	return a.svc.CancelAsset(ctx, assetID, operatorID, reason)
}

func (a *assetProvisionAdapter) SuspendAsset(ctx context.Context, assetID, operatorID uint64, reason string) error {
	return a.svc.FreezeAsset(ctx, assetID, operatorID, reason)
}

func (a *assetProvisionAdapter) ResumeAsset(ctx context.Context, assetID, operatorID uint64) error {
	return a.svc.UnfreezeAsset(ctx, assetID, operatorID)
}

func (a *assetProvisionAdapter) RenewAsset(ctx context.Context, assetID uint64, durationDays *int) error {
	return a.svc.RenewAsset(ctx, assetID, durationDays)
}

// authAssetSummaryAdapter 将 *assetsvc.AssetService 适配为 authsvc.AssetSummaryFetcher 接口（D-86）。
// 把 asset 模块的资产摘要映射为 auth 模块的 DTO，避免 auth 直接依赖 asset。
type authAssetSummaryAdapter struct {
	svc *assetsvc.AssetService
}

func (a *authAssetSummaryAdapter) GetAssetSummary(ctx context.Context, userID uint64) (*authdto.AdminAssetSummary, error) {
	s, err := a.svc.GetUserAssetSummary(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &authdto.AdminAssetSummary{
		Total:     s.Total,
		Active:    s.Active,
		Suspended: s.Suspended,
		Expired:   s.Expired,
		Cancelled: s.Cancelled,
	}, nil
}

// entitlementCheckerAdapter 将 *assetsvc.AssetService 适配为 authsvc.EntitlementChecker 接口（S2-甲6）。
// 供 sk 签发 prepaid 时只读校验 source_id（entitlement_id）的归属与可用性，绝不扣减额度。
// 复用 asset 模块已有的只读查询 ListUserEntitlements（按 user_id 查），天然保证归属：
//   - 只有该 user 名下的权益才会被返回，越权的 entitlement_id 不在结果中 → 校验失败；
//   - 再过滤 status=active + entitlement_type=token_quota，确保是可用的 token 套餐额度。
type entitlementCheckerAdapter struct {
	svc *assetsvc.AssetService
}

func (a *entitlementCheckerAdapter) IsTokenQuotaUsable(ctx context.Context, userID, entitlementID uint64) bool {
	ents, err := a.svc.ListUserEntitlements(ctx, userID)
	if err != nil {
		return false
	}
	for i := range ents {
		e := &ents[i]
		if e.ID == entitlementID && e.Status == "active" && e.EntitlementType == "token_quota" {
			return true
		}
	}
	return false
}

// apiKeyResolverAdapter 将 auth.APIKeyService 适配为 middleware.APIKeyResolver 接口。
// *APIKeyService 仅暴露 ResolveKeyForAuth(ctx, rawSK)(userID, apiKeyID, ok)，
// 与中间件接口方法名 ResolveKey 不同，故包一层。
// 仅在 apiKeyService != nil 时构造该适配器，否则给中间件传字面 nil 接口，
// 避免「非 nil 接口包 nil 指针」导致中间件 == nil 判断失效、sk 调用 panic。
type apiKeyResolverAdapter struct {
	svc *authsvc.APIKeyService
}

func (a *apiKeyResolverAdapter) ResolveKey(ctx context.Context, rawSK string) (userID, apiKeyID uint64, ok bool) {
	return a.svc.ResolveKeyForAuth(ctx, rawSK)
}

// modelScopeResolverAdapter 将 auth.APIKeyService 适配为 token_gateway.ModelScopeResolver 接口（S2-丁4b）。
// 供 chat 门面在转发上游前做 sk 的 model_scope 越界校验（仅 sk 调用时）。
// 同 apiKeyResolverAdapter：仅在 apiKeyService != nil 时构造，否则给门面传字面 nil 接口，
// 使 ForwardService.scopeResolver==nil 判断生效、对 sk 调用安全退化为「不校验」而非 panic。
type modelScopeResolverAdapter struct {
	svc *authsvc.APIKeyService
}

func (a *modelScopeResolverAdapter) ModelScopeByID(ctx context.Context, apiKeyID uint64) (scope []string, ok bool) {
	return a.svc.ModelScopeByID(ctx, apiKeyID)
}

// tokenUsageReporterAdapter 将 finance_consumer.ConsumerService 适配为 token_gateway 的 UsageReporter 接口。
// 把 token 网关的 UsageEvent 映射为 finance_consumer 的 ProductUsageEvent 并调 Handle 扣费。
// 跨模块只走对方 service 暴露的 Handle，不直接碰其 repository。
type tokenUsageReporterAdapter struct {
	svc *financesvc.ConsumerService
}

// 编译期断言：billing 的 *WalletHoldService 满足 token_gateway 的 WalletHolder 接口（S2-乙0 / D1）。
// 门面 postpaid 预扣保证金的实际编排（FreezeHold/SettleHold）由后端丁 S2-丁5 接入 ForwardService。
var _ tokengatewaysvc.WalletHolder = (*billingsvc.WalletHoldService)(nil)

func (a *tokenUsageReporterAdapter) Report(ctx context.Context, evt tokengatewaysvc.UsageEvent) error {
	// usage_unit 仅作消费记录留痕（finance_consumer.FindRule 只按 usage_type 匹配规则，不参与匹配）：
	// calls 按次记 count，token 类记 tokens，与 product_billing_rules 的 usage_unit 保持一致。
	usageUnit := "tokens"
	if evt.UsageType == "calls" {
		usageUnit = "count"
	}
	_, err := a.svc.Handle(ctx, financemodel.ProductUsageEvent{
		EventID:        evt.RequestID,
		UserID:         evt.UserID,
		ProductID:      evt.ProductID,
		ProductType:    "token",
		UsageType:      evt.UsageType,
		UsageAmount:    evt.UsageAmount,
		UsageUnit:      usageUnit,
		OccurredAt:     time.Now(),
		IdempotencyKey: evt.IdempotencyKey,
	})
	return err
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

// orderBillingAdapter 将 *billingsvc.WalletService 适配为 ordersvc.BillingService 接口。
// 关键职责：把 billing 模块的业务错误（余额不足/乐观锁冲突）归一化为 order 模块的哨兵错误，
// 使 order 模块无需反向依赖 billing 包（避免 billing→order→billing 循环导入）即可识别这些语义。
type orderBillingAdapter struct {
	svc *billingsvc.WalletService
}

// DeductTx 在外部事务内扣费并返回钱包流水 ID；错误归一化为 order 模块语义。
func (a *orderBillingAdapter) DeductTx(tx *gorm.DB, userID uint64, amount decimal.Decimal, orderID uint64, remark string) (uint64, error) {
	txID, err := a.svc.DeductTx(tx, userID, amount, orderID, remark)
	if err != nil {
		if errors.Is(err, billingsvc.ErrInsufficientBalance) {
			return 0, ordersvc.ErrInsufficientBalance
		}
		if errors.Is(err, billingsvc.ErrConcurrentUpdate) {
			return 0, ordersvc.ErrConcurrentUpdate
		}
		return 0, err
	}
	return txID, nil
}

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
	groupService := iamsvc.NewGroupService(groupRepo, permRepo, roleRepo, gormDB, cacheSvc)
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
	// A-28：注入 RoleAssigner，用于管理员创建用户时分配角色
	authService.SetRoleAssigner(iamService)
	// D-85：注入 RolesFetcher，用于 ListUsers / GetUser 附带 roles 字段
	authService.SetRolesFetcher(iamService)
	// D-86：注入 PermissionOverridesFetcher，用于 GetUser 附带 permission_overrides 字段
	authService.SetPermissionOverridesFetcher(iamService)
	// 注入 GroupJoiner，用于注册时按邀请码/默认组落组
	authService.SetGroupJoiner(groupService)

	// S2-甲5：平台 API Key（sk）服务装配。
	// 仅当 API_KEY_HMAC_SECRET 配置时启用（灰度安全降级，未配置则 apiKeyService=nil）：
	//   - HMAC 密钥从 config 注入（DB 只存 HMAC，明文只签发时返回一次）；
	//   - banChecker=authService，使封禁用户名下 sk 在 ResolveKey 内立即失效（封禁联动方案 A）。
	var apiKeyService *authsvc.APIKeyService
	if cfg.APIKeyHMACSecret != "" {
		apiKeyRepo := authrep.NewAPIKeyRepository(gormDB)
		apiKeyService = authsvc.NewAPIKeyService(apiKeyRepo, cfg.APIKeyHMACSecret, authService)
	} else {
		log.Printf("[security] API_KEY_HMAC_SECRET 未配置，平台 sk 系统未启用（/api/keys 路由不注册，门面 sk 鉴权退化为纯 JWT）")
	}

	// ——— Identity 模块 ———
	// D-04：注入 auditSvc，用于审核操作写全局审计日志
	identityRepo := identityrep.NewIdentityRepository(gormDB)
	identityService := identitysvc.NewIdentityService(identityRepo, userRepo, gormDB, cfg, auditSvc)

	// ——— Billing 模块（WalletService 供 product 购买接口注入）———
	walletRepo := billingrepo.NewWalletRepository(gormDB)
	txRepo := billingrepo.NewTransactionRepository(gormDB)
	walletService := billingsvc.NewWalletService(gormDB, walletRepo, txRepo)

	// 预扣保证金服务（S2-乙0 / D1）：供 token_gateway 门面 postpaid 路径冻结/结算保证金。
	// *WalletHoldService 的方法签名直接满足 token_gateway 的 WalletHolder 接口（仅 decimal/primitive），无需额外适配。
	walletHoldRepo := billingrepo.NewWalletHoldRepository(gormDB)
	walletHoldService := billingsvc.NewWalletHoldService(gormDB, walletRepo, txRepo, walletHoldRepo)

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
	// 适配为 provision/service.AssetManager 接口（字段类型映射 + 生命周期操作）
	assetAdapt := &assetProvisionAdapter{svc: assetService}
	// D-86：注入资产摘要查询器，使管理端 GET /api/admin/users/:id 返回 asset_summary
	authService.SetAssetSummaryFetcher(&authAssetSummaryAdapter{svc: assetService})
	// S2-甲6：注入套餐权益只读校验器，使 sk 签发 prepaid 时校验 source_id 归属/可用（不扣减额度）。
	// apiKeyService 可能为 nil（API_KEY_HMAC_SECRET 未配置时灰度关闭），此时无需注入。
	if apiKeyService != nil {
		apiKeyService.SetEntitlementChecker(&entitlementCheckerAdapter{svc: assetService})
	}

	// ——— Provision 模块 ———
	provisionService := provisionmod.NewProvisionService(productRepo, planRepo, assetAdapt)
	// 注册应用类商品处理器（product_type = "application"）
	appProvisioner := provisionhandler.NewAppProvisioner(productRepo)
	provisionService.RegisterHandler("application", appProvisioner)
	// 注册 token 类商品处理器（product_type = "token"）：开通 token_service 资产（按量先行，不预置额度）
	provisionService.RegisterHandler("token", provisionhandler.NewTokenProvisioner(productRepo))

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
	authmod.RegisterRoutes(mux, authService, verifySvc, cfg, iamService, scopeService, redisClient, apiKeyService)
	iammod.RegisterRoutes(mux, iamService, groupService, cfg.JWTSecret, authService, authService)
	identitymod.RegisterRoutes(mux, identityService, iamService, cfg.JWTSecret, authService, authService)

	// 注册 billing 模块（钱包、充值、支付回调），传入 notify_body 加密密钥
	billingmod.RegisterRoutes(mux, gormDB, cfg.JWTSecret, authService, iamService, cfg.NotifyBodyKey)

	// 注册 order 模块（订单查询 + O3 支付 / O4 取消）
	// orderBillingAdapt：钱包扣费适配器（事务内扣费 + 错误归一化）；
	// provisionService：支付成功后异步开通（与 product 购买链路复用同一 provision 实现）。
	orderBillingAdapt := &orderBillingAdapter{svc: walletService}
	ordermod.RegisterRoutes(mux, gormDB, cfg.JWTSecret, authService, iamService, orderBillingAdapt, provisionService)

	// 注册 product 模块（商品、套餐、价格、购买）
	// provisionService 直接满足 productservice.ProvisionService 接口（Provision 方法签名完全匹配）
	// membershipAdapt 满足 productservice.MembershipService 接口
	productmod.RegisterRoutes(mux, gormDB, cfg.JWTSecret, authService, iamService, walletService, userRepoAdapter,
		provisionService, membershipAdapt)

	// 注册 finance_consumer 模块
	//   - 内部消费事件上报接口（IP 白名单保护）
	//   - 消费记录查询 F2（用户本人）/ F3（管理端 wallet:view）
	financemod.RegisterRoutes(mux, gormDB, cfg.JWTSecret, authService, iamService)

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

	// 注册 token 网关门面（管理端：渠道/模型目录；用户端：OpenAI 兼容 chat 转发）。
	// TOKEN_PROVIDER_KEY 未配置或非法（非 32 字节）时跳过装配，仅记日志，不阻断其他模块启动。
	if cfg.TokenProviderKey != "" {
		// 跨模块依赖通过接口注入，避免 import 环：
		//   - AssetGate：assetService 直接满足 HasActiveTokenAsset(ctx, userID) 签名。
		//   - UsageReporter：finance_consumer.ConsumerService 适配（按量上报扣钱包）。
		//     单独构造一个 ConsumerService 实例（复用其公开构造，不改乙模块代码）。
		tokenConsumptionRepo := financerepo.NewConsumptionRepository(gormDB)
		tokenBillingRuleRepo := productrep.NewBillingRuleRepository(gormDB)
		tokenConsumerSvc := financesvc.NewConsumerService(gormDB, tokenConsumptionRepo, tokenBillingRuleRepo, walletService)
		tokenReporter := &tokenUsageReporterAdapter{svc: tokenConsumerSvc}

		//   - WalletHolder（S2-乙0 / D1）：billing.WalletHoldService，供 postpaid 预扣保证金（FreezeHold/SettleHold）。
		//     billing 侧能力已就绪；门面侧编排调用由后端丁 S2-丁5 接入 ForwardService 后启用。
		var tokenWalletHolder tokengatewaysvc.WalletHolder = walletHoldService
		_ = tokenWalletHolder

		//   - ModelScopeResolver（S2-丁4b）：auth.APIKeyService 适配，供 chat 门面做 sk model_scope 越界校验。
		//     nil 接口陷阱：仅在 apiKeyService 非 nil 时构造适配器；否则传字面 nil 接口，
		//     使 ForwardService.scopeResolver==nil 判断生效、对 sk 调用安全退化为不校验而非 panic。
		var tokenScopeResolver tokengatewaysvc.ModelScopeResolver
		if apiKeyService != nil {
			tokenScopeResolver = &modelScopeResolverAdapter{svc: apiKeyService}
		}

		if tokenGatewayModule, tgErr := tokengatewaymod.New(gormDB, cfg.TokenProviderKey, assetService, tokenReporter, tokenScopeResolver); tgErr != nil {
			log.Printf("[token_gateway] 初始化失败，管理端/用户端未启用: %v", tgErr)
		} else {
			// 管理端：渠道 / 模型目录 / 全量用量（token:manage + 管理员双重认证）。
			tokengatewaymod.RegisterRoutes(mux, tokenGatewayModule.ChannelService, tokenGatewayModule.CatalogService,
				tokenGatewayModule.UsageService, cfg.JWTSecret, iamService, authService, authService)
			// 用户端：列模型 + OpenAI 兼容 chat 转发 + 我的用量（双模式鉴权：sk + 登录态）。
			// nil 接口陷阱：仅在 apiKeyService 非 nil 时构造适配器并传入；否则传字面 nil 接口，
			// 使中间件 apiKeyResolver==nil 判断生效、对 sk 调用安全退化为「sk 鉴权未启用」而非 panic。
			var tokenAPIKeyResolver middleware.APIKeyResolver
			if apiKeyService != nil {
				tokenAPIKeyResolver = &apiKeyResolverAdapter{svc: apiKeyService}
			}
			tokengatewaymod.RegisterUserRoutes(mux, tokenGatewayModule.ForwardService, tokenGatewayModule.CatalogService,
				tokenGatewayModule.UsageService, cfg.JWTSecret, authService, tokenAPIKeyResolver)
		}
	} else {
		log.Printf("[token_gateway] TOKEN_PROVIDER_KEY 未配置，token 网关门面未启用")
	}

	// 启动定时任务：到期资产处理（后台 goroutine，随应用生命周期运行）
	go jobs.NewExpireAssetsJob(gormDB).Start(context.Background())
	// 启动定时任务：到期会员处理（C-FIX-5，与资产到期任务对齐）
	go jobs.NewExpireMembershipsJob(gormDB).Start(context.Background())

	// 全局中间件（最外层）
	handler := middleware.RequestID(middleware.Recovery(middleware.Logger(mux)))

	srv := httpserver.New(cfg, handler)
	return &App{Config: cfg, Server: srv}, nil
}

func (a *App) Run() error {
	fmt.Printf("API server 启动，监听 %s\n", a.Server.Addr)
	return a.Server.ListenAndServe()
}
