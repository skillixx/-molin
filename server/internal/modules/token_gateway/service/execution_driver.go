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
	StartedAt     time.Time
	FinishedAt    time.Time
	Outcome       string
	ErrorClass    string
	ResultUnknown bool
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
	Done       bool
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
	providerModel, ok := d.models[in.LogicalModel]
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
	public, usage, valid := normalizeExecutionJSON(data, true, true, logicalModel)
	if !valid {
		return ExecutionStreamChunk{Usage: usage}, errors.New("执行驱动 SSE 返回业务错误或非法数据")
	}
	return ExecutionStreamChunk{PublicLine: append(append([]byte("data: "), public...), '\n'), Usage: usage}, nil
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
	sanitizeExecutionChoices(value["choices"])
	redactExecutionInternalFields(value)
}

// sanitizeExecutionChoices 只公开 OpenAI 兼容的 choice 字段，避免供应商私有字符串绕过跨分块连续审核。
// message、delta 和 tool_calls 内的兼容扩展仍会递归进入内容审核，不在此处截断其结构。
func sanitizeExecutionChoices(value interface{}) {
	choices, ok := value.([]interface{})
	if !ok {
		return
	}
	allowed := map[string]struct{}{
		"index": {}, "message": {}, "delta": {}, "text": {}, "finish_reason": {}, "logprobs": {},
	}
	for _, rawChoice := range choices {
		choice, ok := rawChoice.(map[string]interface{})
		if !ok {
			continue
		}
		for key := range choice {
			if _, exists := allowed[key]; !exists {
				delete(choice, key)
			}
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
