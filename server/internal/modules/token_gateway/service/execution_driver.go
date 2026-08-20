package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"time"

	"molin/server/internal/modules/token_gateway/model"
)

// ExecutionRequest 是统一执行驱动接收的请求，逻辑模型与供应商模型必须同时保留，便于后续账本关联。
type ExecutionRequest struct {
	RequestID     string
	LogicalModel  string
	ProviderModel string
	ProviderCode  string
	EndpointCode  string
	AttemptNo     uint32
	BaseURL       string
	APIKey        string
	Body          map[string]interface{}
}

// ExecutionUsage 是驱动归一化后的用量。Present=false 表示必须进入待对账，不能按 max_tokens 猜测扣费。
type ExecutionUsage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	ReasoningTokens  int64
	CachedTokens     int64
	Present          bool
}

// ExecutionAttempt 记录一次独立上游尝试的最小元数据，不保存提示词、密钥或供应商响应头。
type ExecutionAttempt struct {
	AttemptNo     uint32
	Driver        string
	ProviderCode  string
	EndpointCode  string
	ProviderModel string
	// UpstreamRequestID 只保存固定白名单响应 id，禁止保存响应正文、响应头或供应商凭据。
	UpstreamRequestID string
	StartedAt         time.Time
	FinishedAt        time.Time
	Outcome           string
	ErrorClass        string
	ResultUnknown     bool
}

// LedgerStatus 把运行态 Outcome 收敛为 ai_execution_attempts.status 的冻结枚举。
func (a ExecutionAttempt) LedgerStatus() string {
	switch a.Outcome {
	case "success", "succeeded":
		return "succeeded"
	case "running":
		return "running"
	case "timeout":
		return "timeout"
	case "pending_reconcile", "unknown":
		return "unknown"
	default:
		return "failed"
	}
}

// RequestExecutionStatus 把一次尝试映射为 ai_requests.execution_status。
// 超时或结果未知不得伪装成失败后自动重试，统一进入 unknown 对账路径。
func (a ExecutionAttempt) RequestExecutionStatus() string {
	status := a.LedgerStatus()
	if a.ResultUnknown || status == "timeout" || status == "unknown" {
		return model.AIExecutionUnknown
	}
	switch status {
	case "succeeded":
		return model.AIExecutionSucceeded
	case "running":
		return model.AIExecutionRunning
	default:
		return model.AIExecutionFailed
	}
}

// ToLedgerModel 形成 G2 可直接持久化的执行尝试快照，不包含提示词、密钥或响应正文。
func (a ExecutionAttempt) ToLedgerModel(requestID string, usage ExecutionUsage) model.AIExecutionAttempt {
	attemptNo := a.AttemptNo
	if attemptNo == 0 {
		attemptNo = 1
	}
	ledger := model.AIExecutionAttempt{
		RequestID:          requestID,
		AttemptNo:          attemptNo,
		ExecutionDriver:    a.Driver,
		ProviderCode:       a.ProviderCode,
		ExecutionModelCode: a.ProviderModel,
		Status:             a.LedgerStatus(),
		ResultUnknown:      a.ResultUnknown,
		StartedAt:          a.StartedAt,
		CreatedAt:          a.StartedAt,
	}
	if a.EndpointCode != "" {
		ledger.EndpointCode = &a.EndpointCode
	}
	if a.ErrorClass != "" {
		ledger.ErrorClass = &a.ErrorClass
	}
	if a.UpstreamRequestID != "" {
		ledger.UpstreamRequestID = &a.UpstreamRequestID
	}
	if !a.FinishedAt.IsZero() {
		ledger.FinishedAt = &a.FinishedAt
		latencyMilliseconds := a.FinishedAt.Sub(a.StartedAt).Milliseconds()
		if latencyMilliseconds < 0 {
			latencyMilliseconds = 0
		}
		latency := uint64(latencyMilliseconds)
		ledger.LatencyMS = &latency
	}
	if usage.Present {
		ledger.PromptTokens = nonNegativeTokenPointer(usage.PromptTokens)
		ledger.CompletionTokens = nonNegativeTokenPointer(usage.CompletionTokens)
		ledger.ReasoningTokens = nonNegativeTokenPointer(usage.ReasoningTokens)
		ledger.CachedTokens = nonNegativeTokenPointer(usage.CachedTokens)
	}
	return ledger
}

func nonNegativeTokenPointer(value int64) *uint64 {
	if value < 0 {
		return nil
	}
	converted := uint64(value)
	return &converted
}

func executionNetworkErrorClass(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "network_timeout"
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "network_timeout"
	}
	return "network_error"
}

func newExecutionAttempt(driver string, in ExecutionRequest, providerModel string, started time.Time) ExecutionAttempt {
	attemptNo := in.AttemptNo
	if attemptNo == 0 {
		attemptNo = 1
	}
	providerCode := in.ProviderCode
	if mappedProvider, _, ok := strings.Cut(providerModel, "/"); ok && mappedProvider != "" {
		providerCode = mappedProvider
	}
	endpointCode := in.EndpointCode
	if endpointCode == "" {
		endpointCode = providerCode
	}
	return ExecutionAttempt{
		AttemptNo: attemptNo, Driver: driver, ProviderCode: providerCode, EndpointCode: endpointCode,
		ProviderModel: providerModel, StartedAt: started,
	}
}

// ExecutionResponse 保留标准 HTTP 响应并附带归一化用量与尝试信息。
type ExecutionResponse struct {
	Response *http.Response
	Usage    ExecutionUsage
	Attempt  ExecutionAttempt
}

// ExecutionStreamChunk 是驱动清洗后的单行 SSE 数据。
type ExecutionStreamChunk struct {
	PublicLine []byte
	Usage      ExecutionUsage
	// UpstreamRequestID 仅承载当前 SSE data JSON 顶层 id，不承载完整事件或 Header。
	UpstreamRequestID string
	Done              bool
}

// ExecutionDriver 隔离上游执行差异，未来 RequestOrchestrator 只依赖该接口。
type ExecutionDriver interface {
	Name() string
	ChatCompletion(ctx context.Context, req ExecutionRequest) (*ExecutionResponse, error)
	ChatCompletionStream(ctx context.Context, req ExecutionRequest) (*ExecutionResponse, error)
	NormalizeStreamLine(line []byte, logicalModel string) (ExecutionStreamChunk, error)
}

type executionTransportError struct {
	cause       error
	requestSent bool
}

func (e *executionTransportError) Error() string { return e.cause.Error() }
func (e *executionTransportError) Unwrap() error { return e.cause }

func executionResultUnknown(err error) bool {
	var transportErr *executionTransportError
	if errors.As(err, &transportErr) {
		return transportErr.requestSent
	}
	// 非统一执行器产生的网络错误无法证明未发送，必须保守进入待对账。
	return true
}

// ExecutionDriverSelector 支持全局默认值，并为后续按模型选择驱动保留稳定扩展点。
type ExecutionDriverSelector interface {
	Select(logicalModel string) (ExecutionDriver, error)
}

type staticExecutionDriverSelector struct {
	driver ExecutionDriver
}

func (s staticExecutionDriverSelector) Select(_ string) (ExecutionDriver, error) {
	if s.driver == nil {
		return nil, errors.New("执行驱动未配置")
	}
	return s.driver, nil
}

// NativeOpenAICompatibleDriver 保留原生 Go 转发路径，作为默认执行方式和受控回退能力。
type NativeOpenAICompatibleDriver struct {
	client       *http.Client
	streamClient *http.Client
}

func NewNativeOpenAICompatibleDriver(client *http.Client) *NativeOpenAICompatibleDriver {
	return &NativeOpenAICompatibleDriver{client: client, streamClient: newStreamHTTPClient()}
}

func (d *NativeOpenAICompatibleDriver) Name() string { return "native" }

func (d *NativeOpenAICompatibleDriver) ChatCompletion(ctx context.Context, in ExecutionRequest) (*ExecutionResponse, error) {
	return d.execute(ctx, in, false)
}

func (d *NativeOpenAICompatibleDriver) ChatCompletionStream(ctx context.Context, in ExecutionRequest) (*ExecutionResponse, error) {
	return d.execute(ctx, in, true)
}

func (d *NativeOpenAICompatibleDriver) execute(ctx context.Context, in ExecutionRequest, stream bool) (*ExecutionResponse, error) {
	started := time.Now()
	body := cloneExecutionBody(in.Body)
	body["model"] = in.ProviderModel
	// 驱动方法决定是否流式，必须覆盖客户端请求体中的冲突值，避免流式链路误收普通 JSON 或反向误开流。
	body["stream"] = stream
	if stream {
		injectStreamUsage(body)
	}
	client := d.client
	if stream {
		client = d.streamClient
	}
	resp, err := executeHTTPRequest(ctx, client, buildChatURL(in.BaseURL), in.APIKey, in.RequestID, body, stream)
	attempt := newExecutionAttempt(d.Name(), in, in.ProviderModel, started)
	if err != nil {
		attempt.FinishedAt = time.Now()
		attempt.Outcome = statusFromErr(err)
		attempt.ErrorClass = executionNetworkErrorClass(err)
		attempt.ResultUnknown = executionResultUnknown(err)
		if !attempt.ResultUnknown {
			attempt.ErrorClass = "request_not_sent"
		}
		return &ExecutionResponse{Attempt: attempt}, err
	}
	if stream && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		attempt.Outcome = "success"
		return &ExecutionResponse{Response: resp, Attempt: attempt}, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	resp.Body.Close()
	if err != nil {
		attempt.FinishedAt = time.Now()
		attempt.Outcome = "failed"
		attempt.ErrorClass = "response_read_error"
		attempt.ResultUnknown = true
		return &ExecutionResponse{Attempt: attempt}, err
	}
	publicBody, usage, valid := normalizeExecutionJSON(raw, resp.StatusCode >= 200 && resp.StatusCode < 300, false, in.LogicalModel)
	if !valid && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		resp.StatusCode = http.StatusBadGateway
		resp.Status = "502 Bad Gateway"
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		attempt.Outcome = "failed"
		attempt.ErrorClass = "upstream_response_error"
		publicBody = publicExecutionError("upstream_error", "上游服务调用失败")
	} else {
		attempt.Outcome = "success"
	}
	attempt.FinishedAt = time.Now()
	resp.Header.Set("Content-Type", "application/json")
	resp.Body = io.NopCloser(bytes.NewReader(publicBody))
	return &ExecutionResponse{Response: resp, Usage: usage, Attempt: attempt}, nil
}

func (d *NativeOpenAICompatibleDriver) NormalizeStreamLine(line []byte, logicalModel string) (ExecutionStreamChunk, error) {
	return normalizeExecutionStreamLine(line, logicalModel)
}

// BifrostDriverConfig 仅接受环境注入值，禁止在代码中保存真实内部 Token。
type BifrostDriverConfig struct {
	BaseURL       string
	InternalToken string
	ModelMapping  map[string]string
	HTTPClient    *http.Client
	StreamClient  *http.Client
}

// BifrostDriver 负责显式模型映射、内部鉴权、业务错误识别及响应脱敏。
type BifrostDriver struct {
	baseURL      string
	token        string
	models       map[string]string
	client       *http.Client
	streamClient *http.Client
}

func NewBifrostDriver(cfg BifrostDriverConfig) *BifrostDriver {
	sourceModels := cfg.ModelMapping
	if sourceModels == nil {
		sourceModels = DefaultBifrostModelMapping()
	}
	// 驱动持有独立快照，避免调用方运行期修改 map 引发路由漂移或并发读写。
	models := make(map[string]string, len(sourceModels))
	for logicalModel, providerModel := range sourceModels {
		models[logicalModel] = providerModel
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultUpstreamTimeout}
	}
	streamClient := cfg.StreamClient
	if streamClient == nil {
		streamClient = newStreamHTTPClient()
	}
	return &BifrostDriver{baseURL: strings.TrimRight(cfg.BaseURL, "/"), token: cfg.InternalToken, models: models, client: client, streamClient: streamClient}
}

func (d *BifrostDriver) Name() string { return "bifrost" }

// DefaultBifrostModelMapping 冻结 Phase 1 首批逻辑模型，目标值必须显式包含 Bifrost Provider。
func DefaultBifrostModelMapping() map[string]string {
	return map[string]string{
		"molin/qwen-turbo":        "bailian/qwen-turbo",
		"molin/qwen-3.7-flash":    "bailian/qwen3.7-flash-2026-07-15",
		"molin/deepseek-v4-flash": "openrouter/deepseek/deepseek-v4-flash-0731",
	}
}

func (d *BifrostDriver) ChatCompletion(ctx context.Context, in ExecutionRequest) (*ExecutionResponse, error) {
	return d.execute(ctx, in, false)
}

func (d *BifrostDriver) ChatCompletionStream(ctx context.Context, in ExecutionRequest) (*ExecutionResponse, error) {
	return d.execute(ctx, in, true)
}

func (d *BifrostDriver) execute(ctx context.Context, in ExecutionRequest, stream bool) (*ExecutionResponse, error) {
	started := time.Now()
	// 只有 G5 数据库路由使用 route:{id} 端点标记时才直传 provider/model。
	// 旧 token_models.upstream_model 允许只保存供应商内模型名，必须继续使用冻结映射补全 Provider。
	providerModel, ok := d.models[in.LogicalModel]
	if strings.HasPrefix(strings.TrimSpace(in.EndpointCode), "route:") {
		providerModel = strings.TrimSpace(in.ProviderModel)
		ok = providerModel != ""
	}
	provider, providerModelName, mapped := strings.Cut(providerModel, "/")
	if !ok || !mapped || strings.TrimSpace(provider) == "" || strings.TrimSpace(providerModelName) == "" {
		attempt := newExecutionAttempt(d.Name(), in, providerModel, started)
		attempt.FinishedAt = time.Now()
		attempt.Outcome = "failed"
		// 映射校验发生在构造 HTTP 请求前，可确定未产生上游成本，统一进入安全释放分类。
		attempt.ErrorClass = "request_not_sent"
		attempt.ResultUnknown = false
		return &ExecutionResponse{Attempt: attempt}, fmt.Errorf("Bifrost 模型未显式映射: %s", in.LogicalModel)
	}
	body := cloneExecutionBody(in.Body)
	body["model"] = providerModel
	// 驱动方法决定是否流式，必须覆盖客户端请求体中的冲突值，确保 Bifrost 协议与本地读取方式一致。
	body["stream"] = stream
	if stream {
		injectStreamUsage(body)
	}
	client := d.client
	if stream {
		client = d.streamClient
	}
	resp, err := executeHTTPRequest(ctx, client, buildChatURL(d.baseURL), d.token, in.RequestID, body, stream)
	attempt := newExecutionAttempt(d.Name(), in, providerModel, started)
	if err != nil {
		attempt.FinishedAt = time.Now()
		attempt.Outcome = statusFromErr(err)
		attempt.ErrorClass = executionNetworkErrorClass(err)
		attempt.ResultUnknown = executionResultUnknown(err)
		if !attempt.ResultUnknown {
			attempt.ErrorClass = "request_not_sent"
		}
		return &ExecutionResponse{Attempt: attempt}, err
	}
	if stream && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		attempt.Outcome = "success"
		return &ExecutionResponse{Response: resp, Attempt: attempt}, nil
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	resp.Body.Close()
	if readErr != nil {
		attempt.FinishedAt = time.Now()
		attempt.Outcome = "failed"
		attempt.ErrorClass = "response_read_error"
		attempt.ResultUnknown = true
		return &ExecutionResponse{Attempt: attempt}, readErr
	}
	attempt.UpstreamRequestID = extractWhitelistedUpstreamRequestID(raw)
	publicBody, usage, valid := normalizeExecutionJSON(raw, resp.StatusCode >= 200 && resp.StatusCode < 300, false, in.LogicalModel)
	if !valid && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		resp.StatusCode = http.StatusBadGateway
		resp.Status = "502 Bad Gateway"
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		attempt.Outcome = "failed"
		attempt.ErrorClass = "upstream_response_error"
		publicBody = publicExecutionError("upstream_error", "上游服务调用失败")
	} else {
		attempt.Outcome = "success"
	}
	attempt.FinishedAt = time.Now()
	resp.Header.Set("Content-Type", "application/json")
	resp.Body = io.NopCloser(bytes.NewReader(publicBody))
	return &ExecutionResponse{Response: resp, Usage: usage, Attempt: attempt}, nil
}

func (d *BifrostDriver) NormalizeStreamLine(line []byte, logicalModel string) (ExecutionStreamChunk, error) {
	return normalizeExecutionStreamLine(line, logicalModel)
}

// normalizeExecutionStreamLine 为 Native 与 Bifrost 使用同一公开 SSE 白名单、错误识别和 Usage 语义。
func normalizeExecutionStreamLine(line []byte, logicalModel string) (ExecutionStreamChunk, error) {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		// 部分上游会在流式请求失败时以 HTTP 200 返回普通 JSON 错误，不能把它当作 SSE 文本透传。
		if bytes.HasPrefix(trimmed, []byte("{")) {
			if _, _, valid := normalizeExecutionJSON(trimmed, true, false, logicalModel); !valid {
				return ExecutionStreamChunk{}, errors.New("执行驱动流式响应返回业务错误或非法 JSON")
			}
			return ExecutionStreamChunk{}, errors.New("执行驱动流式响应未使用 SSE 格式")
		}
		if len(trimmed) == 0 {
			return ExecutionStreamChunk{PublicLine: line}, nil
		}
		// OpenAI 兼容流只公开 data 事件；Bifrost 的 comment/event/id 等扩展元数据默认丢弃。
		return ExecutionStreamChunk{}, nil
	}
	data := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
	if bytes.Equal(data, []byte("[DONE]")) {
		return ExecutionStreamChunk{PublicLine: line, Done: true}, nil
	}
	if len(data) == 0 {
		return ExecutionStreamChunk{PublicLine: line}, nil
	}
	upstreamRequestID := extractWhitelistedUpstreamRequestID(data)
	public, usage, valid := normalizeExecutionJSON(data, true, true, logicalModel)
	if !valid {
		return ExecutionStreamChunk{Usage: usage, UpstreamRequestID: upstreamRequestID}, errors.New("执行驱动 SSE 返回业务错误或非法数据")
	}
	return ExecutionStreamChunk{PublicLine: append(append([]byte("data: "), public...), '\n'), Usage: usage, UpstreamRequestID: upstreamRequestID}, nil
}

// extractWhitelistedUpstreamRequestID 只接受 OpenAI 兼容响应顶层 id，并限制为低敏安全字符集合。
// 该函数故意忽略 extra_fields、响应 Header 和嵌套对象，避免把 Bifrost 内部路由或凭据写入账本。
func extractWhitelistedUpstreamRequestID(raw []byte) string {
	var envelope struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ""
	}
	value := strings.TrimSpace(envelope.ID)
	if value == "" || len(value) > 191 {
		return ""
	}
	for _, current := range value {
		if current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' || current >= '0' && current <= '9' {
			continue
		}
		switch current {
		case '-', '_', '.', ':', '/':
			continue
		default:
			return ""
		}
	}
	return value
}

func executeHTTPRequest(ctx context.Context, client *http.Client, url, token, requestID string, body map[string]interface{}, stream bool) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &executionTransportError{cause: fmt.Errorf("请求体序列化失败: %w", err)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, &executionTransportError{cause: fmt.Errorf("构造上游请求失败: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	requestSent := false
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { requestSent = true }}
	resp, err := client.Do(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
	if err != nil {
		return nil, &executionTransportError{cause: err, requestSent: requestSent}
	}
	return resp, nil
}

// newStreamHTTPClient 只限制建连、TLS 和响应头等待，不对整个 SSE 响应体设置 http.Client.Timeout。
func newStreamHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	return &http.Client{Transport: transport}
}

func cloneExecutionBody(body map[string]interface{}) map[string]interface{} {
	cloned := make(map[string]interface{}, len(body)+2)
	for key, value := range body {
		cloned[key] = value
	}
	return cloned
}

func normalizeExecutionJSON(raw []byte, requireChoices, allowUsageOnly bool, logicalModel string) ([]byte, ExecutionUsage, bool) {
	var value map[string]interface{}
	if json.Unmarshal(raw, &value) != nil {
		return nil, ExecutionUsage{}, false
	}
	usage := parseExecutionUsage(raw)
	if hasExecutionError(value) {
		// 业务错误不能抹掉供应商同时返回的可信 Usage，结算层仍需按真实成本收费。
		return nil, usage, false
	}
	choices, choicesPresent := value["choices"]
	if choicesPresent && !validExecutionChoices(choices) {
		return nil, usage, false
	}
	if requireChoices && (!choicesPresent || emptyJSONList(choices)) && !(allowUsageOnly && usage.Present) {
		return nil, usage, false
	}
	sanitizeExecutionResponse(value, logicalModel)
	public, err := json.Marshal(value)
	return public, usage, err == nil
}

func validExecutionChoices(value interface{}) bool {
	items, ok := value.([]interface{})
	if !ok {
		return false
	}
	for _, item := range items {
		if _, ok := item.(map[string]interface{}); !ok {
			return false
		}
	}
	return true
}

func hasExecutionError(value map[string]interface{}) bool {
	if flag, ok := value["is_bifrost_error"].(bool); ok && flag {
		return true
	}
	if errValue, ok := value["error"]; ok && errValue != nil {
		if text, textOK := errValue.(string); !textOK || strings.TrimSpace(text) != "" {
			return true
		}
	}
	return false
}

func emptyJSONList(value interface{}) bool {
	items, ok := value.([]interface{})
	return !ok || len(items) == 0
}

// sanitizeExecutionResponse 顶层只允许 G1 已冻结的 OpenAI 兼容字段，未知字段默认不对外公开。
func sanitizeExecutionResponse(value map[string]interface{}, logicalModel string) {
	allowed := map[string]struct{}{
		"id": {}, "object": {}, "created": {}, "model": {}, "choices": {}, "usage": {},
		"system_fingerprint": {}, "service_tier": {},
	}
	for key := range value {
		if _, ok := allowed[key]; !ok {
			delete(value, key)
		}
	}
	if logicalModel != "" {
		value["model"] = logicalModel
	}
	for _, key := range []string{"id", "object", "model", "system_fingerprint", "service_tier"} {
		sanitizeExecutionString(value, key, true)
	}
	if created, exists := value["created"]; exists && !isExecutionNumber(created) {
		delete(value, "created")
	}
	if rawChoices, exists := value["choices"]; exists && !sanitizeExecutionChoices(rawChoices) {
		delete(value, "choices")
	}
	if rawUsage, exists := value["usage"]; exists {
		if _, ok := rawUsage.(map[string]interface{}); !ok {
			delete(value, "usage")
		} else {
			sanitizeExecutionUsage(rawUsage)
		}
	}
	redactExecutionInternalFields(value)
}

// sanitizeExecutionChoices 只公开冻结的 OpenAI 兼容字段，供应商私有扩展既不能旁路内容审核，也不能进入客户响应。
func sanitizeExecutionChoices(value interface{}) bool {
	choices, ok := value.([]interface{})
	if !ok {
		return false
	}
	allowed := map[string]struct{}{
		"index": {}, "message": {}, "delta": {}, "text": {}, "finish_reason": {}, "logprobs": {},
	}
	for _, rawChoice := range choices {
		choice, ok := rawChoice.(map[string]interface{})
		if !ok {
			return false
		}
		for key := range choice {
			if _, exists := allowed[key]; !exists {
				delete(choice, key)
			}
		}
		if index, exists := choice["index"]; exists && !isExecutionNumber(index) {
			delete(choice, "index")
		}
		sanitizeExecutionString(choice, "text", true)
		sanitizeExecutionString(choice, "finish_reason", true)
		if rawMessage, exists := choice["message"]; exists && !sanitizeExecutionMessage(rawMessage, false) {
			delete(choice, "message")
		}
		if rawDelta, exists := choice["delta"]; exists && !sanitizeExecutionMessage(rawDelta, true) {
			delete(choice, "delta")
		}
		if rawLogprobs, exists := choice["logprobs"]; exists {
			if _, ok := rawLogprobs.(map[string]interface{}); !ok {
				delete(choice, "logprobs")
			} else {
				sanitizeExecutionLogprobs(rawLogprobs)
			}
		}
	}
	return true
}

// sanitizeExecutionUsage 仅公开 OpenAI Usage 标准字段及冻结的明细字段，供应商成本和路由信息不得外泄。
func sanitizeExecutionUsage(value interface{}) {
	usage, ok := value.(map[string]interface{})
	if !ok {
		return
	}
	filterExecutionObject(usage, map[string]struct{}{
		"prompt_tokens": {}, "completion_tokens": {}, "total_tokens": {},
		"prompt_tokens_details": {}, "completion_tokens_details": {},
	})
	for _, key := range []string{"prompt_tokens", "completion_tokens", "total_tokens"} {
		if raw, exists := usage[key]; exists && !isExecutionNumber(raw) {
			delete(usage, key)
		}
	}
	detailFields := map[string][]string{
		"prompt_tokens_details":     {"cached_tokens", "audio_tokens"},
		"completion_tokens_details": {"reasoning_tokens", "audio_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens"},
	}
	for detailKey, allowedFields := range detailFields {
		raw, exists := usage[detailKey]
		if !exists {
			continue
		}
		details, ok := raw.(map[string]interface{})
		if !ok {
			delete(usage, detailKey)
			continue
		}
		allowed := make(map[string]struct{}, len(allowedFields))
		for _, field := range allowedFields {
			allowed[field] = struct{}{}
		}
		filterExecutionObject(details, allowed)
		for _, field := range allowedFields {
			if value, present := details[field]; present && !isExecutionNumber(value) {
				delete(details, field)
			}
		}
	}
}

// sanitizeExecutionLogprobs 对概率结果逐层使用白名单，避免供应商追踪字段混入兼容响应。
func sanitizeExecutionLogprobs(value interface{}) {
	logprobs, ok := value.(map[string]interface{})
	if !ok {
		return
	}
	filterExecutionObject(logprobs, map[string]struct{}{"content": {}, "refusal": {}, "tokens": {}, "token_logprobs": {}, "top_logprobs": {}, "text_offset": {}})
	sanitizeExecutionScalarList(logprobs, "tokens", func(value interface{}) bool { _, ok := value.(string); return ok })
	sanitizeExecutionScalarList(logprobs, "token_logprobs", func(value interface{}) bool { return value == nil || isExecutionNumber(value) })
	sanitizeExecutionScalarList(logprobs, "text_offset", isExecutionNumber)
	if rawTop, exists := logprobs["top_logprobs"]; exists {
		topItems, ok := rawTop.([]interface{})
		if !ok {
			delete(logprobs, "top_logprobs")
		} else {
			for _, rawItem := range topItems {
				item, ok := rawItem.(map[string]interface{})
				if !ok {
					delete(logprobs, "top_logprobs")
					break
				}
				for token, probability := range item {
					if token == "" || !isExecutionNumber(probability) {
						delete(item, token)
					}
				}
			}
		}
	}
	for _, key := range []string{"content", "refusal"} {
		rawItems, exists := logprobs[key]
		if !exists {
			continue
		}
		items, ok := rawItems.([]interface{})
		if !ok {
			delete(logprobs, key)
			continue
		}
		validItems := true
		for _, rawItem := range items {
			item, ok := rawItem.(map[string]interface{})
			if !ok {
				validItems = false
				break
			}
			filterExecutionObject(item, map[string]struct{}{"token": {}, "logprob": {}, "bytes": {}, "top_logprobs": {}})
			if token, exists := item["token"]; exists {
				if _, ok := token.(string); !ok {
					delete(item, "token")
				}
			}
			if probability, exists := item["logprob"]; exists && !isExecutionNumber(probability) {
				delete(item, "logprob")
			}
			sanitizeExecutionScalarList(item, "bytes", func(value interface{}) bool { return isExecutionNumber(value) })
			if topItems, ok := item["top_logprobs"].([]interface{}); ok {
				validTopItems := true
				for _, rawTop := range topItems {
					top, ok := rawTop.(map[string]interface{})
					if !ok {
						validTopItems = false
						break
					}
					filterExecutionObject(top, map[string]struct{}{"token": {}, "logprob": {}, "bytes": {}})
					if token, exists := top["token"]; exists {
						if _, ok := token.(string); !ok {
							delete(top, "token")
						}
					}
					if probability, exists := top["logprob"]; exists && !isExecutionNumber(probability) {
						delete(top, "logprob")
					}
					sanitizeExecutionScalarList(top, "bytes", isExecutionNumber)
				}
				if !validTopItems {
					delete(item, "top_logprobs")
				}
			} else if _, exists := item["top_logprobs"]; exists {
				delete(item, "top_logprobs")
			}
		}
		if !validItems {
			delete(logprobs, key)
		}
	}
}

func sanitizeExecutionScalarList(object map[string]interface{}, key string, valid func(interface{}) bool) {
	raw, exists := object[key]
	if !exists {
		return
	}
	items, ok := raw.([]interface{})
	if !ok {
		delete(object, key)
		return
	}
	for _, item := range items {
		if !valid(item) {
			delete(object, key)
			return
		}
	}
}

func isExecutionNumber(value interface{}) bool {
	switch value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return true
	default:
		return false
	}
}

// sanitizeExecutionMessage 冻结 message/delta 的嵌套兼容契约。
// G5 文字网关保留标准文本、工具调用、音频引用和 URL 引用字段，其余供应商私有字段默认删除。
func sanitizeExecutionMessage(value interface{}, delta bool) bool {
	message, ok := value.(map[string]interface{})
	if !ok {
		return false
	}
	allowed := map[string]struct{}{
		"role": {}, "content": {}, "refusal": {}, "tool_calls": {}, "function_call": {}, "audio": {},
	}
	if !delta {
		allowed["annotations"] = struct{}{}
	}
	filterExecutionObject(message, allowed)
	sanitizeExecutionString(message, "role", false)
	sanitizeExecutionString(message, "refusal", true)
	if !sanitizeExecutionContent(message["content"]) {
		delete(message, "content")
	}
	if !sanitizeExecutionToolCalls(message["tool_calls"]) {
		delete(message, "tool_calls")
	}
	if !sanitizeExecutionFunction(message["function_call"]) {
		delete(message, "function_call")
	}
	if !sanitizeExecutionAudio(message["audio"]) {
		delete(message, "audio")
	}
	if !sanitizeExecutionAnnotations(message["annotations"]) {
		delete(message, "annotations")
	}
	return true
}

func sanitizeExecutionContent(value interface{}) bool {
	if value == nil {
		return true
	}
	if _, ok := value.(string); ok {
		return true
	}
	parts, ok := value.([]interface{})
	if !ok {
		return false
	}
	allowed := map[string]struct{}{"type": {}, "text": {}, "refusal": {}}
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]interface{})
		if !ok {
			return false
		}
		filterExecutionObject(part, allowed)
		sanitizeExecutionString(part, "type", false)
		sanitizeExecutionString(part, "text", true)
		sanitizeExecutionString(part, "refusal", true)
	}
	return true
}

func sanitizeExecutionToolCalls(value interface{}) bool {
	if value == nil {
		return true
	}
	calls, ok := value.([]interface{})
	if !ok {
		return false
	}
	allowed := map[string]struct{}{"id": {}, "index": {}, "type": {}, "function": {}}
	for _, rawCall := range calls {
		call, ok := rawCall.(map[string]interface{})
		if !ok {
			return false
		}
		filterExecutionObject(call, allowed)
		sanitizeExecutionString(call, "id", false)
		sanitizeExecutionString(call, "type", false)
		if index, exists := call["index"]; exists && !isExecutionNumber(index) {
			delete(call, "index")
		}
		if !sanitizeExecutionFunction(call["function"]) {
			delete(call, "function")
		}
	}
	return true
}

func sanitizeExecutionFunction(value interface{}) bool {
	if value == nil {
		return true
	}
	function, ok := value.(map[string]interface{})
	if !ok {
		return false
	}
	filterExecutionObject(function, map[string]struct{}{"name": {}, "arguments": {}})
	sanitizeExecutionString(function, "name", false)
	sanitizeExecutionString(function, "arguments", false)
	return true
}

func sanitizeExecutionAudio(value interface{}) bool {
	if value == nil {
		return true
	}
	audio, ok := value.(map[string]interface{})
	if !ok {
		return false
	}
	filterExecutionObject(audio, map[string]struct{}{"id": {}, "data": {}, "expires_at": {}, "transcript": {}})
	sanitizeExecutionString(audio, "id", false)
	sanitizeExecutionString(audio, "data", false)
	sanitizeExecutionString(audio, "transcript", true)
	if expiresAt, exists := audio["expires_at"]; exists && !isExecutionNumber(expiresAt) {
		delete(audio, "expires_at")
	}
	return true
}

func sanitizeExecutionAnnotations(value interface{}) bool {
	if value == nil {
		return true
	}
	annotations, ok := value.([]interface{})
	if !ok {
		return false
	}
	for _, rawAnnotation := range annotations {
		annotation, ok := rawAnnotation.(map[string]interface{})
		if !ok {
			return false
		}
		filterExecutionObject(annotation, map[string]struct{}{"type": {}, "url_citation": {}})
		sanitizeExecutionString(annotation, "type", false)
		if citation, ok := annotation["url_citation"].(map[string]interface{}); ok {
			filterExecutionObject(citation, map[string]struct{}{"start_index": {}, "end_index": {}, "title": {}, "url": {}})
			for _, key := range []string{"start_index", "end_index"} {
				if index, exists := citation[key]; exists && !isExecutionNumber(index) {
					delete(citation, key)
				}
			}
			sanitizeExecutionString(citation, "title", false)
			sanitizeExecutionString(citation, "url", false)
		} else if _, exists := annotation["url_citation"]; exists {
			delete(annotation, "url_citation")
		}
	}
	return true
}

func sanitizeExecutionString(object map[string]interface{}, key string, nullable bool) {
	value, exists := object[key]
	if !exists || (nullable && value == nil) {
		return
	}
	if _, ok := value.(string); !ok {
		delete(object, key)
	}
}

func filterExecutionObject(value map[string]interface{}, allowed map[string]struct{}) {
	for key := range value {
		if _, ok := allowed[key]; !ok {
			delete(value, key)
		}
	}
}

func redactExecutionInternalFields(value interface{}) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			switch strings.ToLower(key) {
			case "extra_fields", "routing_info", "provider_response_headers", "is_bifrost_error", "api_key", "api_key_name", "bifrost_key_name":
				delete(typed, key)
			default:
				redactExecutionInternalFields(child)
			}
		}
	case []interface{}:
		for _, child := range typed {
			redactExecutionInternalFields(child)
		}
	}
}

func parseExecutionUsage(raw []byte) ExecutionUsage {
	var envelope struct {
		Usage *struct {
			PromptTokens     *int64 `json:"prompt_tokens"`
			CompletionTokens *int64 `json:"completion_tokens"`
			TotalTokens      *int64 `json:"total_tokens"`
			ReasoningTokens  int64  `json:"reasoning_tokens"`
			CachedTokens     int64  `json:"cached_tokens"`
			PromptDetails    struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionDetails struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Usage == nil {
		return ExecutionUsage{}
	}
	u := envelope.Usage
	// prompt/completion 是文字模型结算的最低完整口径；空 usage 或任一字段缺失必须进入待对账。
	if u.PromptTokens == nil || u.CompletionTokens == nil || *u.PromptTokens < 0 || *u.CompletionTokens < 0 {
		return ExecutionUsage{}
	}
	total := *u.PromptTokens + *u.CompletionTokens
	if u.TotalTokens != nil {
		if *u.TotalTokens < 0 || *u.TotalTokens != total {
			return ExecutionUsage{}
		}
		total = *u.TotalTokens
	}
	reasoning := u.ReasoningTokens
	if reasoning == 0 {
		reasoning = u.CompletionDetails.ReasoningTokens
	}
	cached := u.CachedTokens
	if cached == 0 {
		cached = u.PromptDetails.CachedTokens
	}
	if cached < 0 || reasoning < 0 || cached > *u.PromptTokens || reasoning > *u.CompletionTokens {
		return ExecutionUsage{}
	}
	return ExecutionUsage{PromptTokens: *u.PromptTokens, CompletionTokens: *u.CompletionTokens, TotalTokens: total, ReasoningTokens: reasoning, CachedTokens: cached, Present: true}
}

func isSSEDone(line []byte) bool {
	trimmed := bytes.TrimSpace(line)
	return bytes.Equal(bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:"))), []byte("[DONE]"))
}

func publicExecutionError(code, message string) []byte {
	body, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"code": code, "message": message, "type": "upstream_error"}})
	return body
}
