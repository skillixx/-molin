package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/crypto"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// ——— 跨模块依赖接口（在 token_gateway 内定义，避免 import 环）———

// AssetGate token 访问门禁：判断用户是否持有 active 的 token 服务资产（买了才能调用）。
// 由 asset.AssetService.HasActiveTokenAsset 适配实现，bootstrap 注入。
type AssetGate interface {
	HasActiveTokenAsset(ctx context.Context, userID uint64) (bool, error)
}

// UsageEvent token 网关向计费侧上报的一条用量事件（input_tokens / output_tokens 各一条）。
// 故意与 finance_consumer.ProductUsageEvent 解耦，避免 token_gateway 直接依赖其包。
type UsageEvent struct {
	RequestID      string          // 请求唯一标识（幂等基准）
	UserID         uint64          // 调用用户
	ProductID      uint64          // token 商品 ID（来自 token_models.product_id）
	UsageType      string          // input_tokens / output_tokens
	UsageAmount    decimal.Decimal // 用量（token 数）
	IdempotencyKey string          // request_id:usage_type
}

// UsageReporter 按量计费上报接口。由 finance_consumer 适配实现，bootstrap 注入。
// best-effort：上报失败只记日志，不影响已返回给用户的对话结果。
//
// 返回 charged 为本条事件 finance_consumer 实扣金额（无匹配规则时为 0）；
// 门面 postpaid 结算时累加各条 charged 作为 sale_amount 回填（S2-丁5 修 M1 P3）。
type UsageReporter interface {
	Report(ctx context.Context, event UsageEvent) (charged decimal.Decimal, err error)
}

// saleAmountWriter 回填 token_usage_logs.sale_amount 的最小接口（S2-丁5）。
// 由 *repository.UsageLogRepository 满足；抽出接口便于结算逻辑单测注入内存桩。
type saleAmountWriter interface {
	UpdateSaleAmountByRequestID(ctx context.Context, requestID string, saleAmount decimal.Decimal) error
}

// BillingResolver 按 api_key_id 解析 sk 的计费模式与套餐来源（S2-丁5，sk 契约 §5）。
// 由 auth.APIKeyService 适配实现（BillingByID，与 ModelScopeByID 同款轻量只读），bootstrap 注入；可为 nil。
//
// 仅 sk 调用（apiKeyID != 0）时使用：门面据此分流 postpaid（钱包）/ prepaid（套餐额度）。
//   - apiKeyID == 0（登录态）：默认 postpaid（与 M1 行为一致，登录态不绑定套餐）。
//   - resolver == nil（auth 未暴露 BillingByID / 灰度退化）：一律按 postpaid 处理，安全不透支额度。
//   - ok=false（key 失效）：按 postpaid 兜底（鉴权中间件已先验过有效，正常不会到这）。
type BillingResolver interface {
	// BillingByID 返回该 api_key 的 billing_mode（postpaid/prepaid）与 source_id（prepaid=entitlement_id）。
	// ok=false 表示 key 无效 / 已吊销。
	BillingByID(ctx context.Context, apiKeyID uint64) (mode string, sourceID uint64, ok bool)
}

// EntitlementConsumer prepaid 套餐额度扣减接口（S2-丁5 / 丙2）。
// 由 token_gateway/client.EntitlementClient 适配实现（调 asset 内部接口 entitlement-consume），bootstrap 注入；可为 nil。
//
// amount = input_tokens + output_tokens（1:1，与 token_quota 单位一致）。
// 额度不足返回 ErrQuotaExhausted；归属不符返回 ErrEntitlementDenied；其他错误透传。
type EntitlementConsumer interface {
	Consume(ctx context.Context, entitlementID, userID uint64, amount decimal.Decimal, idempotencyKey string) error
}

// EntitlementBalance prepaid 权益余额快照（展示等只读场景用）。
// usable=false 表示权益不可用（非 active / 已过期 / 额度耗尽）。
//
// D-M2-01 方案 B 后，prepaid 主计费路径不再依赖 usable 判断（改用 reserve 预占结果），
// 该类型仅供 GetBalance（展示等只读场景）继续使用。
type EntitlementBalance struct {
	Usable bool   // 可用：active + 未过期 + remaining>0（不限量则 active+未过期即可）
	Status string // 权益状态（仅用于日志/告警，不落对话内容）
}

// EntitlementBalanceChecker prepaid 权益余额只读查询（展示等场景）。
// 由 token_gateway/client.EntitlementClient.GetBalance 适配实现，bootstrap 注入；可为 nil。
//
// 注意：D-M2-01 方案 B 后，prepaid 转发前置闸不再用本接口（旧方案 A 的 usable 粗筛有白嫖洞，已移除），
// 改为转发前调 EntitlementReserver.Reserve 预占额度。本接口保留仅供展示/可观测等只读用途。
type EntitlementBalanceChecker interface {
	GetBalance(ctx context.Context, entitlementID, userID uint64) (EntitlementBalance, error)
}

// EntitlementReserver prepaid 转发前预占额度（D-M2-01 方案 B，与 postpaid FreezeHold 对称）。
// 由 token_gateway/client.EntitlementClient 适配实现（Reserve/Settle/Release），bootstrap 注入；可为 nil。
//
// 时序（对称 postpaid 保证金）：
//   - 转发上游前（任何 WriteHeader 之前）调 Reserve 预占预估消耗（max_tokens）：占不到 → 拒 60005，不转发；
//   - 拿到 usage 后调 Settle 按实际消耗结算（多退少补，asset 侧封顶到预占额）；
//   - 失败/异常路径调 Release 全额释放预占（不计 used）。
//
// 预占已杜绝串行/并发白嫖：串行低余额（remaining<单次）在转发前被 Reserve 拒，
// 并发请求各自占额、不足者被 asset 侧 FOR UPDATE 串行挡住，无任何免费放行窗口。
type EntitlementReserver interface {
	// Reserve 预占一笔额度，返回 holdID（供 Settle/Release 关联）。
	// amount 不足 / 权益不可用 → ErrQuotaExhausted（对外 60005）；归属不符 / 权益不存在 → ErrEntitlementDenied（40003）；
	// 系统错误（asset 不可达 / 5xx / 鉴权失败 / 可重试冲突）→ ErrSystemBusy（对外 503），fail-safe 拒转发。
	Reserve(ctx context.Context, entitlementID, userID uint64, amount decimal.Decimal, idempotencyKey string) (holdID uint64, err error)
	// Settle 按实际消耗结算预占（多退少补）。holdID>0 优先，否则用 idempotencyKey 关联同一 hold。
	// 返回 settled 为实际计入 quota_used 的净额（用于 sale_amount 回填）。hold 已结算时幂等。
	Settle(ctx context.Context, holdID uint64, idempotencyKey string, actual decimal.Decimal) (settled decimal.Decimal, err error)
	// Release 全额释放预占（不计 used），用于失败/异常路径。holdID>0 优先，否则用 idempotencyKey 关联。
	Release(ctx context.Context, holdID uint64, idempotencyKey string) error
}

// ModelScopeResolver sk 模型白名单解析接口（S2-丁4b，sk 契约 §8 第4条 / §10）。
// 由 auth.APIKeyService.ModelScopeByID 适配实现，bootstrap 注入；可为 nil（退化为不校验）。
//
// 仅在 sk 调用（apiKeyID != 0）时使用：门面鉴权后据此校验请求 model 是否在该 sk 的 model_scope 范围内。
// 登录态调用（apiKeyID == 0）跳过，不调用本接口。
type ModelScopeResolver interface {
	// ModelScopeByID 返回该 api_key 的 model_scope（空切片=不限模型）；
	// ok=false 表示 key 无效 / 已吊销（按未授权处理）。
	ModelScopeByID(ctx context.Context, apiKeyID uint64) (scope []string, ok bool)
}

// WalletHolder postpaid 预扣保证金接口（D1）。由 billing 适配实现，bootstrap 注入。
//
// 门面 postpaid 路径在转发上游前调 FreezeHold 按 max_tokens×单价冻结保证金（占住额度、杜绝并发透支），
// 拿到实际 usage 后调 SettleHold 解冻并按实扣金额结算（多退少补）；异常路径调 ReleaseHold 全额释放。
// 由后端丁 S2-丁5 在 forward_service 中编排调用，本接口仅定义契约（与 UsageReporter 同款解耦风格）。
type WalletHolder interface {
	// FreezeHold 冻结一笔预扣保证金，返回 holdID。amount 不足返回余额不足错误。
	// idempotencyKey 幂等：同一 key 重复调用返回首次创建的 holdID，不重复冻结。
	FreezeHold(ctx context.Context, userID uint64, amount decimal.Decimal, idempotencyKey, remark string) (uint64, error)
	// SettleHold 解冻该 hold 全额保证金并按 actual 实扣（多退少补）。hold 已结算时幂等返回 nil。
	SettleHold(ctx context.Context, holdID uint64, actual decimal.Decimal, idempotencyKey string) error
	// ReleaseHold 全额释放预扣保证金（actual=0），用于异常路径。
	ReleaseHold(ctx context.Context, holdID uint64, idempotencyKey string) error
}

// ——— 服务层错误 ———

var (
	// ErrModelNotConfigured 逻辑模型不存在 / 未上架 / 未配置渠道路由。
	ErrModelNotConfigured = errors.New("模型不可用")
	// ErrChannelUnavailable 渠道不存在或已停用。
	ErrChannelUnavailable = errors.New("渠道不可用")
	// ErrAccessDenied 未持有 token 服务资产，门禁拒绝。
	ErrAccessDenied = errors.New("未开通 token 服务，无法调用")
	// ErrModelNotInScope sk 调用时请求的 model 不在该 sk 的 model_scope 白名单内（S2-丁4b）。
	// 转发上游前的前置拒绝（不计费、不发起上游），由 handler 映射为 40300。
	ErrModelNotInScope = errors.New("该 API Key 未授权调用此模型")
	// ErrUpstream 上游调用失败 / 超时。
	ErrUpstream = errors.New("上游服务调用失败")
	// ErrWalletInsufficient postpaid 钱包余额不足以冻结预扣保证金（S2-丁5 / D1）。
	// 转发上游前的前置拒绝，由 handler 映射为 60001；不发起上游、不计费、不留冻结。
	ErrWalletInsufficient = errors.New("钱包余额不足")
	// ErrQuotaExhausted prepaid 套餐额度不足（S2-丁5）。由 handler 映射为 60005。
	ErrQuotaExhausted = errors.New("套餐额度不足")
	// ErrEntitlementDenied prepaid 套餐归属不符（S2-丁5）。由 handler 映射为 40003。
	ErrEntitlementDenied = errors.New("套餐额度归属不符")
	// ErrSystemBusy 系统繁忙/可重试错误（D-M2-02）。
	// 典型来源：postpaid 预扣保证金时钱包乐观锁冲突重试耗尽（billing.ErrConcurrentUpdate）。
	// 由 handler 映射为 HTTP 503 + 业务码 50301（服务暂不可用，客户端可重试），
	// 严禁与「真余额不足」（ErrWalletInsufficient/60001）混淆——这是 D-M2-02 的核心。
	ErrSystemBusy = errors.New("系统繁忙，请稍后重试")
)

// 计费模式取值（与 auth/sk 契约一致）。
const (
	billingModePostpaid = "postpaid" // 后付·钱包（按量/按次），引入预扣保证金（D1）
	billingModePrepaid  = "prepaid"  // 预付·套餐 entitlement，扣额度不走钱包
)

// 默认上游调用超时（非流式整体超时；流式仅作用于建连+首字节阶段，由 http.Client.Timeout 控制连接建立）。
const defaultUpstreamTimeout = 120 * time.Second

// ForwardService Token 网关核心转发器：鉴权后的门禁 + 选渠道 + 转发上游 + 读 usage + 写日志 + 计费编排。
// 自写薄转发器（v3）：三家上游均 OpenAI 兼容，近似纯透传，仅改 body.model 为上游模型名 + 换渠道 key。
type ForwardService struct {
	modelRepo     *repository.TokenModelRepository
	channelRepo   *repository.ChannelRepository
	usageRepo     *repository.UsageLogRepository
	cipher        *crypto.AESGCM
	assetGate     AssetGate
	reporter      UsageReporter
	scopeResolver ModelScopeResolver // sk model_scope 越界校验（S2-丁4b）；可为 nil → 不校验

	// ——— S2-丁5：计费路由依赖（均可为 nil，nil 时安全退化）———
	billingResolver BillingResolver           // 按 apiKeyID 解析 billing_mode/source_id；nil → 一律 postpaid
	walletHolder    WalletHolder              // postpaid 预扣保证金（D1）；nil → 不预扣（退化为 M1 纯上报）
	entConsumer     EntitlementConsumer       // prepaid 套餐额度扣减（旧直扣，保留兜底）；nil → 跳过
	balanceChecker  EntitlementBalanceChecker // prepaid 权益余额只读查询（展示）；prepaid 主路径已不依赖；可为 nil
	entReserver     EntitlementReserver       // prepaid 转发前预占额度（D-M2-01 方案 B）；nil → 退化为不预占（前置不拦截、结算走旧直扣）
	holdUnitPrice   decimal.Decimal           // postpaid 预扣兜底单价（每 token）
	holdMaxTokens   int64                     // postpaid 预扣兜底 max_tokens
	saleWriter      saleAmountWriter          // sale_amount 回填（默认 = usageRepo；单测可注入内存桩）

	// 定向可见性解析器（bootstrap 注入）：用于转发前置闸，防止用户绕过列表直接报不可见模型名调用。
	// nil 时 groups/roles 定向模型一律不可见（fail-safe）；scope=all 模型不受影响。
	groupResolver GroupResolver
	roleResolver  RoleResolver

	httpClient *http.Client
}

// WithResolvers 注入定向可见性解析器（bootstrap 装配时调用），供转发前置闸判定模型可见性。
func (s *ForwardService) WithResolvers(gr GroupResolver, rr RoleResolver) *ForwardService {
	s.groupResolver = gr
	s.roleResolver = rr
	return s
}

// BillingDeps 计费路由依赖（S2-丁5），由 bootstrap 装配后通过 SetBillingDeps 注入。
// 单独成结构体而非加构造参数，避免改动 module.New / 既有 NewForwardService 签名（M1 已稳定）。
type BillingDeps struct {
	BillingResolver BillingResolver
	WalletHolder    WalletHolder
	EntConsumer     EntitlementConsumer
	BalanceChecker  EntitlementBalanceChecker // prepaid 权益余额只读查询（展示）；主路径已不依赖
	EntReserver     EntitlementReserver       // prepaid 转发前预占额度（D-M2-01 方案 B）
	HoldUnitPrice   decimal.Decimal
	HoldMaxTokens   int64
}

// NewForwardService 构造转发服务。assetGate/reporter/scopeResolver 由 bootstrap 注入具体适配器。
// scopeResolver 可为 nil（sk 系统未就绪时退化为不做 model_scope 越界校验）。
// 计费路由依赖（postpaid 预扣 / prepaid 扣额度）通过 SetBillingDeps 单独注入，默认全 nil（退化为 M1 纯上报）。
func NewForwardService(
	modelRepo *repository.TokenModelRepository,
	channelRepo *repository.ChannelRepository,
	usageRepo *repository.UsageLogRepository,
	cipher *crypto.AESGCM,
	assetGate AssetGate,
	reporter UsageReporter,
	scopeResolver ModelScopeResolver,
) *ForwardService {
	return &ForwardService{
		modelRepo:     modelRepo,
		channelRepo:   channelRepo,
		usageRepo:     usageRepo,
		cipher:        cipher,
		assetGate:     assetGate,
		reporter:      reporter,
		scopeResolver: scopeResolver,
		saleWriter:    usageRepo, // 默认用真实仓库回填 sale_amount
		httpClient:    &http.Client{Timeout: defaultUpstreamTimeout},
	}
}

// SetBillingDeps 注入计费路由依赖（S2-丁5）。bootstrap 在 Module.New 后调用；各字段可为 nil。
func (s *ForwardService) SetBillingDeps(d BillingDeps) {
	s.billingResolver = d.BillingResolver
	s.walletHolder = d.WalletHolder
	s.entConsumer = d.EntConsumer
	s.balanceChecker = d.BalanceChecker
	s.entReserver = d.EntReserver
	s.holdUnitPrice = d.HoldUnitPrice
	s.holdMaxTokens = d.HoldMaxTokens
}

// ForwardInput chat 转发入参。Body 为终端原始 OpenAI 格式 JSON（已解析的 map，便于改写 model 字段）。
type ForwardInput struct {
	RequestID string                 // 请求唯一标识（由 handler 生成，幂等 / 日志基准）
	UserID    uint64                 // 已鉴权用户 ID（登录态或 sk 绑定）
	APIKeyID  uint64                 // 平台 sk 调用的 api_key_id；登录态 JWT 调用为 0（落库时视为 nil）
	Model     string                 // 逻辑模型名（body.model）
	Stream    bool                   // 是否流式
	Body      map[string]interface{} // 原始请求体（透传，仅改写 model / stream_options）
}

// usage 上游 OpenAI 兼容响应中的 usage 字段。
type upstreamUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// billDecision 本次调用的计费决策（在前置闸阶段确定，结算阶段据此分流）。
type billDecision struct {
	mode      string // postpaid / prepaid
	sourceID  uint64 // prepaid=entitlement_id；postpaid=0
	maxTokens int64  // 本次预估上限（postpaid 预扣 / prepaid 预占 / R5 兜底计费用）
	holdID    uint64 // postpaid 已冻结的保证金 holdID / prepaid 已预占额度的 holdID（方案 B）；0=未冻结/未预占
	settled   bool   // 结算阶段已接管该 hold（已 settle/release）；用于 Forward 的 defer 兜底释放判定

	// skipCallBilling 结算时是否跳过「按次计 1」（postpaid calls 规则）。
	// 零值 false=正常计次（纯透传 Forward 一次调用即一次提问，保持 M1/M2 行为不变）。
	// W8 编排端点：一次提问含多轮上游调用，仅首轮计次（CountCall=true），其余轮置 true 跳过，避免重复计次。
	skipCallBilling bool
}

// resolveBilling 解析本次调用计费模式与 prepaid 套餐来源。
//   - 登录态（apiKeyID==0）/ resolver==nil / key 失效：一律 postpaid（与 M1 行为一致，安全不透支额度）。
//   - sk prepaid：取 source_id（entitlement_id）。
func (s *ForwardService) resolveBilling(ctx context.Context, in ForwardInput) billDecision {
	d := billDecision{mode: billingModePostpaid, maxTokens: s.resolveMaxTokens(in.Body)}
	if in.APIKeyID == 0 || s.billingResolver == nil {
		return d
	}
	mode, sourceID, ok := s.billingResolver.BillingByID(ctx, in.APIKeyID)
	if !ok {
		return d // 防御性兜底 postpaid（中间件已先验有效）
	}
	if mode == billingModePrepaid {
		d.mode = billingModePrepaid
		d.sourceID = sourceID
	}
	return d
}

// resolveMaxTokens 取请求 body 的 max_tokens（OpenAI 兼容字段），缺省回兜底上限。
// JSON 数字解析为 float64，做下界保护，避免 0/负数导致预扣过低。
func (s *ForwardService) resolveMaxTokens(body map[string]interface{}) int64 {
	if body != nil {
		if v, ok := body["max_tokens"]; ok {
			if f, ok := v.(float64); ok && f > 0 {
				return int64(f)
			}
		}
	}
	if s.holdMaxTokens > 0 {
		return s.holdMaxTokens
	}
	return 4096
}

// Forward 执行一次 chat 转发。
//   - 非流式：读取上游完整响应，回写给 w，读 usage 落库 + 计费。
//   - 流式：SSE 直接逐块透传 w（不缓冲整体 body），从含 usage 的末尾 chunk 取用量后落库 + 计费。
//
// 返回 error 时表示「尚未向 w 写入上游响应」，由 handler 决定 HTTP 错误码；
// 一旦开始透传/写出上游响应（流式或非流式成功），即返回 nil（用户已拿到结果）。
func (s *ForwardService) Forward(ctx context.Context, w http.ResponseWriter, in ForwardInput) error {
	// ① 模型校验：token_models 存在、active、配置了 channel_id + upstream_model。
	tm, err := s.modelRepo.FindByCode(ctx, in.Model)
	if err != nil {
		return ErrModelNotConfigured
	}
	if tm.Status != "active" || tm.ChannelID == nil || tm.UpstreamModel == nil || *tm.UpstreamModel == "" {
		return ErrModelNotConfigured
	}

	// ①.2 定向可见性前置闸：模型对该用户不可见 → 按「模型不可用」拒绝（不泄漏隐藏模型存在性）。
	// 防止用户绕过 GET /api/token/models 列表、直接报模型名调用不可见模型。scope=all 模型恒放行。
	if !modelVisibleTo(ctx, tm, in.UserID, s.groupResolver, s.roleResolver) {
		return ErrModelNotConfigured
	}

	// ①.5 sk model_scope 越界校验（S2-丁4b，sk 契约 §8 第4条 / §10）。
	// 仅 sk 调用（APIKeyID != 0）才校验；登录态调用（APIKeyID == 0）不限模型，直接放行。
	// 转发上游前的前置拒绝：越界 → ErrModelNotInScope（handler 映射 40300），不计费、不发起上游。
	if err := s.checkModelScope(ctx, in.APIKeyID, in.Model); err != nil {
		return err
	}

	// ② 访问门禁：必须持有 active 的 token 服务资产。
	ok, err := s.assetGate.HasActiveTokenAsset(ctx, in.UserID)
	if err != nil {
		return fmt.Errorf("门禁查询失败: %w", err)
	}
	if !ok {
		return ErrAccessDenied
	}

	// ②.5 计费路由前置闸（S2-丁5 / D-M2-01 方案 B）：解析 billing_mode，转发上游前预占额度。
	//   - postpaid：hold = max_tokens × 单价，冻结钱包保证金；余额不足 → ErrWalletInsufficient（60001），不发起上游。
	//   - prepaid：reserve = max_tokens（token 数，不乘单价），预占 entitlement 额度；
	//     占不到 → ErrQuotaExhausted（60005），不转发、不写回答案；归属不符 → ErrEntitlementDenied（40003）。
	// 注意：此闸必须在发起上游之前（即任何 WriteHeader 之前）完成。
	// D-M2-01 方案 B：reserve 预占已根治串行/并发白嫖——串行低余额（remaining<单次）在转发前被 reserve 拒，
	// 并发请求各自占额、不足者被 asset 侧 FOR UPDATE 串行挡住，无任何「额度耗尽继续免费放行」窗口。
	bill := s.resolveBilling(ctx, in)
	if bill.mode == billingModePostpaid && s.walletHolder != nil {
		holdAmount := s.holdUnitPrice.Mul(decimal.NewFromInt(bill.maxTokens))
		if holdAmount.GreaterThan(decimal.Zero) {
			holdID, ferr := s.walletHolder.FreezeHold(ctx, in.UserID,
				holdAmount, in.RequestID+":hold", "token 调用预扣保证金")
			if ferr != nil {
				return classifyFreezeError(in.RequestID, ferr)
			}
			bill.holdID = holdID
		}
	}
	if err := s.reservePrepaid(ctx, in, &bill); err != nil {
		return err
	}

	// 兜底释放保证金/预占额度：凡占额后任一路径未走到结算（上游失败、序列化失败、流式错、panic、客户端断开无 usage），
	// defer 统一全额释放，杜绝把用户保证金/额度锁死。结算阶段会先把 bill.settled 置 true，避免重复释放。
	// postpaid 释放钱包保证金（ReleaseHold）；prepaid 释放预占额度（entReserver.Release）——两者对称。
	defer func() {
		if bill.holdID == 0 || bill.settled {
			return
		}
		if bill.mode == billingModePrepaid {
			if s.entReserver != nil {
				if rerr := s.entReserver.Release(context.Background(), bill.holdID, in.RequestID+":quota_reserve"); rerr != nil {
					log.Printf("[token_gateway] 释放 prepaid 预占额度失败 request_id=%s hold_id=%d: %v", in.RequestID, bill.holdID, rerr)
				}
			}
			return
		}
		if s.walletHolder != nil {
			if rerr := s.walletHolder.ReleaseHold(context.Background(), bill.holdID, in.RequestID+":release"); rerr != nil {
				log.Printf("[token_gateway] 释放预扣保证金失败 request_id=%s hold_id=%d: %v", in.RequestID, bill.holdID, rerr)
			}
		}
	}()

	// ③ 选渠道并解密 key。
	ch, err := s.channelRepo.FindByID(ctx, *tm.ChannelID)
	if err != nil {
		return ErrChannelUnavailable
	}
	if ch.Status != "active" {
		return ErrChannelUnavailable
	}
	apiKey, err := s.cipher.Decrypt(ch.APIKeyEncrypted)
	if err != nil {
		return ErrChannelUnavailable
	}

	// ④ 改写请求体：model → 上游真实模型名；流式补 stream_options.include_usage=true 以拿到末尾 usage。
	in.Body["model"] = *tm.UpstreamModel
	if in.Stream {
		injectStreamUsage(in.Body)
	}
	payload, err := json.Marshal(in.Body)
	if err != nil {
		return fmt.Errorf("请求体序列化失败: %w", err)
	}

	upstreamURL := buildChatURL(ch.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("构造上游请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if in.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}

	requestID := in.RequestID

	// ⑤ 发起上游调用（超时 + 失败兜底）。
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logUsage(ctx, requestID, in, tm, 0, 0, statusFromErr(err), errCodePtr("upstream_error"))
		return ErrUpstream
	}
	defer resp.Body.Close()

	// 上游非 2xx：把状态码与 body 透传给用户（不缓冲解析；不落对话明文，仅记状态）。
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		s.logUsage(ctx, requestID, in, tm, 0, 0, "failed", errCodePtr(fmt.Sprintf("upstream_%d", resp.StatusCode)))
		return nil // 已把上游错误透传给用户
	}

	if in.Stream {
		return s.forwardStream(ctx, w, resp, in, tm, requestID, &bill)
	}
	return s.forwardJSON(ctx, w, resp, in, tm, requestID, &bill)
}

// checkModelScope 校验 sk 调用是否被授权调用该 model（S2-丁4b）。
//   - apiKeyID == 0（登录态）：不限模型，直接放行（返回 nil），不查 scope。
//   - scopeResolver == nil（sk 系统未就绪 / 灰度退化）：跳过校验，放行。
//   - key 失效（ok=false）：按未授权处理 → ErrModelNotInScope。
//   - scope 为空：不限模型，放行。
//   - scope 非空且 model 不在其中：越界 → ErrModelNotInScope。
//
// 轻量只读：中间件已验证过 sk 有效，这里只按 id 查一次 scope，不重复全量 ResolveKey。
func (s *ForwardService) checkModelScope(ctx context.Context, apiKeyID uint64, model string) error {
	if apiKeyID == 0 || s.scopeResolver == nil {
		return nil
	}
	scope, ok := s.scopeResolver.ModelScopeByID(ctx, apiKeyID)
	if !ok {
		// key 不存在 / 已吊销：按未授权处理（中间件刚验过有效，正常不会到这；防御性拒绝）。
		return ErrModelNotInScope
	}
	if len(scope) == 0 {
		return nil // 空=不限模型
	}
	for _, m := range scope {
		if m == model {
			return nil
		}
	}
	return ErrModelNotInScope
}

// classifyFreezeError postpaid 预扣保证金失败的错误分流（D-M2-02 修复）。
// 不再把所有非余额不足错误一律当 60001：
//   - 真余额不足（ErrWalletInsufficient，由 adapter 从 billing.ErrInsufficientBalance 归一化）→ 60001。
//   - 乐观锁冲突重试耗尽（ErrSystemBusy，由 adapter 从 billing.ErrConcurrentUpdate 归一化）→ 503（可重试），
//     绝不伪装成余额不足——这是 D-M2-02 的核心。
//   - 其它未知错误 → 保守归为系统繁忙（503），避免无保证金下透支并发调用。
func classifyFreezeError(requestID string, ferr error) error {
	switch {
	case errors.Is(ferr, ErrWalletInsufficient):
		return ErrWalletInsufficient
	case errors.Is(ferr, ErrSystemBusy):
		log.Printf("[token_gateway] 预扣保证金乐观锁冲突 request_id=%s: %v", requestID, ferr)
		return ErrSystemBusy
	default:
		log.Printf("[token_gateway] 预扣保证金失败 request_id=%s: %v", requestID, ferr)
		return ErrSystemBusy
	}
}

// reservePrepaid prepaid 转发前预占额度（D-M2-01 方案 B，与 postpaid FreezeHold 对称）。
// 必须在任何 WriteHeader 之前调用。成功后把预占 holdID 写入 bill.holdID，供结算 settle / 失败 release 关联。
//
// reserve_amount 口径：与 postpaid 冻结口径对齐 = max_tokens（请求带则用之，否则 TOKEN_HOLD_DEFAULT_MAX_TOKENS）。
// prepaid 单位是 token 数（token_quota 1:1），故 reserve_amount 即 token 数，不乘单价。
//
// 分流（fail-safe）：
//   - 非 prepaid / 未注入 entReserver / source_id==0：跳过（不预占；entReserver==nil 时退化为不拦截，结算走旧直扣）。
//   - 预占成功：bill.holdID = holdID，放行转发。
//   - 额度不足 / 权益不可用（ErrQuotaExhausted）：拒 60005，不转发、不写回答案——根治串行/并发白嫖。
//   - 归属不符 / 权益不存在（ErrEntitlementDenied）：拒 40003。
//   - reserve 调用自身失败（ErrSystemBusy：asset 不可达 / 5xx / 鉴权失败 / 可重试冲突）：
//     fail-safe 拒转发，归为系统繁忙（503），绝不放行白嫖；且不与「真额度不足 60005」混淆（避免把繁忙当额度耗尽）。
//
// source_id 必为 token_quota 权益（甲6 签发 prepaid sk 时已校验），前置以此为不变量。
func (s *ForwardService) reservePrepaid(ctx context.Context, in ForwardInput, bill *billDecision) error {
	if bill.mode != billingModePrepaid || s.entReserver == nil || bill.sourceID == 0 {
		return nil
	}
	reserveAmount := decimal.NewFromInt(bill.maxTokens)
	if reserveAmount.LessThanOrEqual(decimal.Zero) {
		// 预占额度非正（理论上 resolveMaxTokens 已有下界保护）：fail-safe 拒，不放行白嫖。
		log.Printf("[token_gateway] prepaid 预占额度非正，拒绝转发 request_id=%s entitlement_id=%d amount=%s",
			in.RequestID, bill.sourceID, reserveAmount)
		return ErrSystemBusy
	}
	holdID, rerr := s.entReserver.Reserve(ctx, bill.sourceID, in.UserID, reserveAmount, in.RequestID+":quota_reserve")
	switch {
	case rerr == nil:
		bill.holdID = holdID // 预占成功：记 holdID，供 settle/release 关联
		return nil
	case errors.Is(rerr, ErrQuotaExhausted):
		// 额度不足 / 权益不可用：转发前拒绝，不转发、不写回答案（根治白嫖）。
		log.Printf("[token_gateway] prepaid 预占额度不足，拒绝转发 request_id=%s entitlement_id=%d", in.RequestID, bill.sourceID)
		return ErrQuotaExhausted
	case errors.Is(rerr, ErrEntitlementDenied):
		// 归属不符 / 权益不存在（sk 绑定权益失效）：按越权拒绝。
		return ErrEntitlementDenied
	default:
		// fail-safe：reserve 调用失败（系统繁忙/asset 不可达/鉴权失败）一律拒转发，不放行白嫖。
		// 归为 ErrSystemBusy（503），不混淆为额度不足 60005。
		log.Printf("[token_gateway] prepaid 预占调用失败，fail-safe 拒绝 request_id=%s entitlement_id=%d: %v",
			in.RequestID, bill.sourceID, rerr)
		return ErrSystemBusy
	}
}

// forwardJSON 非流式：读取完整响应回写，解析 usage 落库 + 计费。
func (s *ForwardService) forwardJSON(ctx context.Context, w http.ResponseWriter, resp *http.Response, in ForwardInput, tm *model.TokenModel, requestID string, bill *billDecision) error {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logUsage(ctx, requestID, in, tm, 0, 0, "failed", errCodePtr("upstream_read_error"))
		return ErrUpstream
	}
	// 先把上游响应原样回写给用户（用户优先拿到结果）。
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)

	// 解析 usage（仅取元数据，不记对话内容）。
	var parsed struct {
		Usage upstreamUsage `json:"usage"`
	}
	_ = json.Unmarshal(raw, &parsed)
	u := parsed.Usage
	s.logUsage(ctx, requestID, in, tm, u.PromptTokens, u.CompletionTokens, "success", nil)
	// 结算：postpaid 实扣 + 解冻保证金 / prepaid 扣套餐额度；回填 sale_amount。
	// 用独立 context：即使请求 ctx 已随响应结束被取消，结算（含解冻保证金）仍须完成。
	s.settle(context.Background(), requestID, in, tm, u.PromptTokens, u.CompletionTokens, bill)
	return nil
}

// forwardStream 流式：SSE 逐行透传不缓冲 body，同时嗅探含 usage 的末尾 chunk。
//
// R5 兜底：客户端中断（写 w 失败）后，服务端必须继续读完上游流以拿到末尾 usage 再结算，
// 否则会漏计费 / 保证金无法按实扣解冻。因此客户端断开仅停止「透传」，不停止「读上游」。
func (s *ForwardService) forwardStream(ctx context.Context, w http.ResponseWriter, resp *http.Response, in ForwardInput, tm *model.TokenModel, requestID string, bill *billDecision) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	reader := bufio.NewReader(resp.Body)

	var usage upstreamUsage
	clientGone := false // 客户端已断开：停止透传，但继续读上游拿 usage（R5）
	streamErr := false  // 上游中途出错
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if !clientGone {
				// 直接透传该行（不缓冲整体 body），立即 flush 保证实时性。
				if _, werr := w.Write(line); werr != nil {
					// 客户端断开：标记后继续读上游（R5），不再向 w 写。
					clientGone = true
				} else if flusher != nil {
					flusher.Flush()
				}
			}
			// 无论是否已断开，都继续嗅探 usage（OpenAI 末尾 chunk，data: {...,"usage":{...}}）。
			if u, ok := parseSSEUsage(line); ok {
				usage = u
			}
		}
		if err != nil {
			if err != io.EOF {
				streamErr = true
			}
			break
		}
	}

	if streamErr {
		// 上游中途出错：已开始透传无法回退错误码，记 failed。
		// 仍按已知 usage 结算（可能为 0）；postpaid 的保证金由 settle 解冻，杜绝锁死。
		s.logUsage(ctx, requestID, in, tm, usage.PromptTokens, usage.CompletionTokens, "failed", errCodePtr("upstream_stream_error"))
		s.settle(context.Background(), requestID, in, tm, usage.PromptTokens, usage.CompletionTokens, bill)
		return nil
	}

	s.logUsage(ctx, requestID, in, tm, usage.PromptTokens, usage.CompletionTokens, "success", nil)
	s.settle(context.Background(), requestID, in, tm, usage.PromptTokens, usage.CompletionTokens, bill)
	return nil
}

// logUsage 写一条 token_usage_logs（仅 tokens/状态元数据，绝不落对话内容明文）。best-effort。
func (s *ForwardService) logUsage(ctx context.Context, requestID string, in ForwardInput, tm *model.TokenModel, inputTok, outputTok int64, status string, errCode *string) {
	// api_key_id：sk 调用落对应 id；登录态调用为 0，存 nil（便于按 sk 维度聚合用量）。
	var apiKeyID *uint64
	if in.APIKeyID != 0 {
		id := in.APIKeyID
		apiKeyID = &id
	}
	logEntry := &model.TokenUsageLog{
		RequestID:        requestID,
		UserID:           in.UserID,
		APIKeyID:         apiKeyID,
		LogicalModelCode: in.Model,
		Modality:         tm.Modality,
		InputTokens:      inputTok,
		OutputTokens:     outputTok,
		TotalTokens:      inputTok + outputTok,
		IsStream:         in.Stream,
		Status:           status,
		ErrorCode:        errCode,
	}
	if err := s.usageRepo.Create(ctx, logEntry); err != nil {
		log.Printf("[token_gateway] 写用量日志失败 request_id=%s: %v", requestID, err)
	}
}

// settle 结算一次成功调用（S2-丁5 计费路由总入口）。在拿到 usage 后调用，best-effort。
//
// R5 兜底：成功调用但 usage 缺失（上游未回 usage 或客户端中断未取到末尾 chunk）时，
// 按 max_tokens 兜底计 output_tokens（宁多计不漏计），避免漏计费 / 保证金无法按实扣解冻。
//
// 分流：
//   - postpaid：按量 input/output（+calls）上报 finance_consumer 实扣钱包；
//     再解冻预扣保证金（ReleaseHold——保证金仅占额防并发透支，实扣已由上报完成，全额释放即可）；
//     sale_amount = 各事件 finance_consumer 实扣金额之和。
//   - prepaid：不走钱包、不上报 product-usage-events；调 entitlement-settle 按实际消耗结算预占（多退少补，1:1）；
//     sale_amount = 实际计入 quota_used 的净额（settle 返回值）。
func (s *ForwardService) settle(ctx context.Context, requestID string, in ForwardInput, tm *model.TokenModel, inputTok, outputTok int64, bill *billDecision) {
	// R5：成功调用却无 usage → 按 max_tokens 兜底（计入 output，保守宁多勿漏）。
	if inputTok == 0 && outputTok == 0 && bill.maxTokens > 0 {
		outputTok = bill.maxTokens
	}

	if bill.mode == billingModePrepaid {
		s.settlePrepaid(ctx, requestID, in.UserID, bill, inputTok, outputTok)
		return
	}
	s.settlePostpaid(ctx, requestID, in.UserID, tm, bill, inputTok, outputTok)
}

// settlePostpaid 钱包路径结算：按量+按次上报实扣 → 回填 sale_amount → 解冻保证金。
func (s *ForwardService) settlePostpaid(ctx context.Context, requestID string, userID uint64, tm *model.TokenModel, bill *billDecision, inputTok, outputTok int64) {
	// 先接管保证金所有权，确保无论后续上报成败，defer 不再重复释放；本函数结尾统一解冻。
	bill.settled = true
	defer func() {
		if bill.holdID != 0 && s.walletHolder != nil {
			// 全额释放保证金：保证金仅用于前置占额防并发透支，实扣已由 product-usage-events 完成。
			if err := s.walletHolder.ReleaseHold(ctx, bill.holdID, requestID+":release"); err != nil {
				log.Printf("[token_gateway] 结算解冻保证金失败 request_id=%s hold_id=%d: %v", requestID, bill.holdID, err)
			}
		}
	}()

	if s.reporter == nil || tm.ProductID == nil {
		// 未注入上报器或模型未关联 token 商品：跳过扣费（按量规则未配置），保证金仍会被 defer 解冻。
		return
	}
	productID := *tm.ProductID
	total := decimal.Zero
	if inputTok > 0 {
		total = total.Add(s.reportOne(ctx, requestID, userID, productID, "input_tokens", inputTok))
	}
	if outputTok > 0 {
		total = total.Add(s.reportOne(ctx, requestID, userID, productID, "output_tokens", outputTok))
	}
	// 按次计费（S2-丁4）：已发起上游并成功结算，本次透传记 1 次 calls。
	// 前置失败（鉴权/门禁/余额闸）不会走到这里，故不误计次。无 calls 规则时上报返回 0、静默跳过。
	// W8 编排：一次提问多轮，仅首轮计次；非首轮 bill.skipCallBilling=true，跳过避免重复计次。
	if !bill.skipCallBilling {
		total = total.Add(s.reportOne(ctx, requestID, userID, productID, "calls", 1))
	}

	// 回填 sale_amount（修 M1 P3）：本次钱包实扣总额。
	s.backfillSaleAmount(ctx, requestID, total)
}

// settlePrepaid 套餐额度路径结算（D-M2-01 方案 B，对称 postpaid 解冻）。
// 转发前已 reserve 预占 → 此处调 entitlement-settle 按实际消耗结算（多退少补，asset 侧封顶到预占额）。
// 不走钱包、不上报 product-usage-events；sale_amount = 实际计入 quota_used 的净额（settle 返回值）。
//
// 接管 hold 所有权：settle 成功/失败都置 bill.settled=true，令 Forward 的 defer 不再重复 release
// （settle 已是终态结算，失败也不应再释放——已扣额度由 asset 侧幂等保证）。
//
// 退化路径（entReserver==nil 或转发前未预占 holdID==0，仅在灰度/未注入时出现）：
// 回落旧的 entitlement-consume 直扣（不预占，单价 1:1），保持向后兼容、不阻断调用。
func (s *ForwardService) settlePrepaid(ctx context.Context, requestID string, userID uint64, bill *billDecision, inputTok, outputTok int64) {
	// amount = input + output（1:1，与 token_quota 单位一致）。
	amount := decimal.NewFromInt(inputTok + outputTok)

	// 主路径：转发前已预占（holdID != 0）→ settle 多退少补。
	if bill.holdID != 0 && s.entReserver != nil {
		bill.settled = true // 接管 hold，defer 不再 release
		actual := amount
		if actual.LessThan(decimal.Zero) {
			actual = decimal.Zero
		}
		settled, err := s.entReserver.Settle(ctx, bill.holdID, requestID+":quota_reserve", actual)
		if err != nil {
			// 结算失败（用户已拿到结果）：仅记日志告警，不回滚结果。
			// asset 侧 settle 幂等；hold 仍处 holding 时下游对账可重放，不会把额度锁死成永久占用。
			log.Printf("[token_gateway] prepaid 结算预占失败 request_id=%s hold_id=%d: %v", requestID, bill.holdID, err)
			return
		}
		// 回填 sale_amount（修 M1 P3）：prepaid 记实际计入 quota_used 的净额（settle 返回值）。
		s.backfillSaleAmount(ctx, requestID, settled)
		return
	}

	// 退化路径：未预占（灰度/未注入 entReserver）→ 旧直扣 entitlement-consume。
	bill.settled = true
	if s.entConsumer == nil || bill.sourceID == 0 {
		log.Printf("[token_gateway] prepaid 结算跳过（未注入扣额度客户端或缺 source_id）request_id=%s", requestID)
		return
	}
	if amount.LessThanOrEqual(decimal.Zero) {
		return
	}
	idem := requestID + ":quota"
	if err := s.entConsumer.Consume(ctx, bill.sourceID, userID, amount, idem); err != nil {
		log.Printf("[token_gateway] prepaid 扣套餐额度失败 request_id=%s entitlement_id=%d: %v", requestID, bill.sourceID, err)
		return
	}
	s.backfillSaleAmount(ctx, requestID, amount)
}

// backfillSaleAmount 回填本次实扣金额/额度到 token_usage_logs.sale_amount（best-effort）。
func (s *ForwardService) backfillSaleAmount(ctx context.Context, requestID string, amount decimal.Decimal) {
	if amount.LessThanOrEqual(decimal.Zero) || s.saleWriter == nil {
		return
	}
	if err := s.saleWriter.UpdateSaleAmountByRequestID(ctx, requestID, amount); err != nil {
		log.Printf("[token_gateway] 回填 sale_amount 失败 request_id=%s: %v", requestID, err)
	}
}

// reportOne 上报单条用量事件，返回 finance_consumer 实扣金额（失败/无规则返回 0），失败仅记日志。
func (s *ForwardService) reportOne(ctx context.Context, requestID string, userID, productID uint64, usageType string, amount int64) decimal.Decimal {
	evt := UsageEvent{
		RequestID:      requestID,
		UserID:         userID,
		ProductID:      productID,
		UsageType:      usageType,
		UsageAmount:    decimal.NewFromInt(amount),
		IdempotencyKey: requestID + ":" + usageType,
	}
	charged, err := s.reporter.Report(ctx, evt)
	if err != nil {
		// best-effort：上报失败不影响用户已拿到的结果，仅记日志告警。
		log.Printf("[token_gateway] 计费上报失败 request_id=%s usage_type=%s: %v", requestID, usageType, err)
		return decimal.Zero
	}
	return charged
}

// ——— 辅助函数 ———

// injectStreamUsage 给流式请求体注入 stream_options.include_usage=true，以便末尾 chunk 携带 usage。
func injectStreamUsage(body map[string]interface{}) {
	opts, ok := body["stream_options"].(map[string]interface{})
	if !ok {
		opts = map[string]interface{}{}
	}
	opts["include_usage"] = true
	body["stream_options"] = opts
}

// buildChatURL 拼接上游 chat/completions 地址，兼容 base_url 是否已含 /v1、是否带尾斜杠。
func buildChatURL(baseURL string) string {
	b := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(b, "/chat/completions") {
		return b
	}
	if strings.HasSuffix(b, "/v1") {
		return b + "/chat/completions"
	}
	return b + "/v1/chat/completions"
}

// parseSSEUsage 从一行 SSE（data: {...}）中嗅探 usage；非 usage 行返回 false。
func parseSSEUsage(line []byte) (upstreamUsage, bool) {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return upstreamUsage{}, false
	}
	data := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return upstreamUsage{}, false
	}
	var chunk struct {
		Usage *upstreamUsage `json:"usage"`
	}
	if err := json.Unmarshal(data, &chunk); err != nil || chunk.Usage == nil {
		return upstreamUsage{}, false
	}
	return *chunk.Usage, true
}

// errCodePtr 构造错误码指针。
func errCodePtr(code string) *string { return &code }

// statusFromErr 区分超时与一般失败，用于 token_usage_logs.status。
func statusFromErr(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		return "timeout"
	}
	return "failed"
}

// isTimeout 判断错误是否为网络超时。
func isTimeout(err error) bool {
	type timeout interface{ Timeout() bool }
	var t timeout
	if errors.As(err, &t) {
		return t.Timeout()
	}
	return false
}
