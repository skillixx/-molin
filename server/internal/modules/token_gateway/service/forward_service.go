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
type UsageReporter interface {
	Report(ctx context.Context, event UsageEvent) error
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
	httpClient    *http.Client
}

// NewForwardService 构造转发服务。assetGate/reporter/scopeResolver 由 bootstrap 注入具体适配器。
// scopeResolver 可为 nil（sk 系统未就绪时退化为不做 model_scope 越界校验）。
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
		httpClient:    &http.Client{Timeout: defaultUpstreamTimeout},
	}
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
		return s.forwardStream(ctx, w, resp, in, tm, requestID)
	}
	return s.forwardJSON(ctx, w, resp, in, tm, requestID)
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

// forwardJSON 非流式：读取完整响应回写，解析 usage 落库 + 计费。
func (s *ForwardService) forwardJSON(ctx context.Context, w http.ResponseWriter, resp *http.Response, in ForwardInput, tm *model.TokenModel, requestID string) error {
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
	s.reportBilling(ctx, requestID, in.UserID, tm, u.PromptTokens, u.CompletionTokens)
	return nil
}

// forwardStream 流式：SSE 逐行透传不缓冲 body，同时嗅探含 usage 的末尾 chunk。
func (s *ForwardService) forwardStream(ctx context.Context, w http.ResponseWriter, resp *http.Response, in ForwardInput, tm *model.TokenModel, requestID string) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	reader := bufio.NewReader(resp.Body)

	var usage upstreamUsage
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			// 直接透传该行（不缓冲整体 body），立即 flush 保证实时性。
			if _, werr := w.Write(line); werr != nil {
				// 客户端断开：停止透传，仍尽力落库已知 usage。
				break
			}
			if flusher != nil {
				flusher.Flush()
			}
			// 嗅探 usage（OpenAI 流式末尾 chunk，data: {...,"usage":{...}}）。
			if u, ok := parseSSEUsage(line); ok {
				usage = u
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			// 上游中途出错：已开始透传，无法回退错误码，记日志即可。
			s.logUsage(ctx, requestID, in, tm, usage.PromptTokens, usage.CompletionTokens, "failed", errCodePtr("upstream_stream_error"))
			return nil
		}
	}

	s.logUsage(ctx, requestID, in, tm, usage.PromptTokens, usage.CompletionTokens, "success", nil)
	s.reportBilling(ctx, requestID, in.UserID, tm, usage.PromptTokens, usage.CompletionTokens)
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

// reportBilling 按量计费上报（best-effort）：input/output 各一条，幂等键 request_id:usage_type。
// TODO：真正扣费依赖后端乙为 product_type=token 商品配置 product_billing_rules，
//
//	且 token_models.product_id 已正确关联；否则 finance_consumer 会返回「未找到计费规则」，
//	此处仅记日志、不影响已返回给用户的对话结果（best-effort）。
func (s *ForwardService) reportBilling(ctx context.Context, requestID string, userID uint64, tm *model.TokenModel, inputTok, outputTok int64) {
	if s.reporter == nil || tm.ProductID == nil {
		// 未注入上报器或模型未关联 token 商品：本期跳过扣费（按量计费规则尚未配置）。
		return
	}
	productID := *tm.ProductID
	if inputTok > 0 {
		s.reportOne(ctx, requestID, userID, productID, "input_tokens", inputTok)
	}
	if outputTok > 0 {
		s.reportOne(ctx, requestID, userID, productID, "output_tokens", outputTok)
	}
	// 按次计费（S2-丁4）：已发起上游并成功结算，本次透传请求记 1 次 calls。
	// 前置失败（鉴权/门禁/余额闸/未发起上游）不会走到这里，故不会误计次。
	// finance_consumer 无 calls 规则时返回「无匹配规则」→ reportOne 静默记日志跳过，不影响主流程。
	s.reportOne(ctx, requestID, userID, productID, "calls", 1)
}

// reportOne 上报单条用量事件，失败仅记日志。
func (s *ForwardService) reportOne(ctx context.Context, requestID string, userID, productID uint64, usageType string, amount int64) {
	evt := UsageEvent{
		RequestID:      requestID,
		UserID:         userID,
		ProductID:      productID,
		UsageType:      usageType,
		UsageAmount:    decimal.NewFromInt(amount),
		IdempotencyKey: requestID + ":" + usageType,
	}
	if err := s.reporter.Report(ctx, evt); err != nil {
		// best-effort：上报失败不影响用户已拿到的结果，仅记日志告警。
		log.Printf("[token_gateway] 计费上报失败 request_id=%s usage_type=%s: %v", requestID, usageType, err)
	}
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
