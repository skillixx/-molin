package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ExecutionRequest 是统一执行驱动接收的请求，逻辑模型与供应商模型必须同时保留，便于后续账本关联。
type ExecutionRequest struct {
	RequestID     string
	LogicalModel  string
	ProviderModel string
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
	Driver        string
	ProviderModel string
	StartedAt     time.Time
	FinishedAt    time.Time
	Outcome       string
	ResultUnknown bool
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
	NormalizeStreamLine(line []byte) (ExecutionStreamChunk, error)
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
	attempt := ExecutionAttempt{Driver: d.Name(), ProviderModel: in.ProviderModel, StartedAt: started}
	if err != nil {
		attempt.FinishedAt = time.Now()
		attempt.Outcome = statusFromErr(err)
		attempt.ResultUnknown = true
		return &ExecutionResponse{Attempt: attempt}, err
	}
	attempt.Outcome = "success"
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		attempt.Outcome = "failed"
	}
	if stream || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if !stream {
			attempt.FinishedAt = time.Now()
		}
		return &ExecutionResponse{Response: resp, Attempt: attempt}, nil
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		attempt.FinishedAt = time.Now()
		attempt.Outcome = "failed"
		attempt.ResultUnknown = true
		return &ExecutionResponse{Attempt: attempt}, err
	}
	attempt.FinishedAt = time.Now()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	return &ExecutionResponse{Response: resp, Usage: parseExecutionUsage(raw), Attempt: attempt}, nil
}

func (d *NativeOpenAICompatibleDriver) NormalizeStreamLine(line []byte) (ExecutionStreamChunk, error) {
	trimmed := bytes.TrimSpace(line)
	data := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
	usage := ExecutionUsage{}
	if bytes.HasPrefix(trimmed, []byte("data:")) && len(data) > 0 && !bytes.Equal(data, []byte("[DONE]")) {
		usage = parseExecutionUsage(data)
	}
	return ExecutionStreamChunk{PublicLine: line, Usage: usage, Done: isSSEDone(line)}, nil
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
		attempt := ExecutionAttempt{Driver: d.Name(), StartedAt: started, FinishedAt: time.Now(), Outcome: "failed"}
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
	attempt := ExecutionAttempt{Driver: d.Name(), ProviderModel: providerModel, StartedAt: started}
	if err != nil {
		attempt.FinishedAt = time.Now()
		attempt.Outcome = statusFromErr(err)
		attempt.ResultUnknown = true
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
		attempt.ResultUnknown = true
		return &ExecutionResponse{Attempt: attempt}, readErr
	}
	publicBody, usage, valid := normalizeBifrostJSON(raw, resp.StatusCode >= 200 && resp.StatusCode < 300, false)
	if !valid && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		resp.StatusCode = http.StatusBadGateway
		resp.Status = "502 Bad Gateway"
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		attempt.Outcome = "failed"
		publicBody = publicExecutionError("upstream_error", "上游服务调用失败")
	} else {
		attempt.Outcome = "success"
	}
	attempt.FinishedAt = time.Now()
	resp.Header.Set("Content-Type", "application/json")
	resp.Body = io.NopCloser(bytes.NewReader(publicBody))
	return &ExecutionResponse{Response: resp, Usage: usage, Attempt: attempt}, nil
}

func (d *BifrostDriver) NormalizeStreamLine(line []byte) (ExecutionStreamChunk, error) {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		// 部分上游会在流式请求失败时以 HTTP 200 返回普通 JSON 错误，不能把它当作 SSE 文本透传。
		if bytes.HasPrefix(trimmed, []byte("{")) {
			if _, _, valid := normalizeBifrostJSON(trimmed, true, false); !valid {
				return ExecutionStreamChunk{}, errors.New("Bifrost 流式响应返回业务错误或非法 JSON")
			}
			return ExecutionStreamChunk{}, errors.New("Bifrost 流式响应未使用 SSE 格式")
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
	public, usage, valid := normalizeBifrostJSON(data, true, true)
	if !valid {
		return ExecutionStreamChunk{}, errors.New("Bifrost SSE 返回业务错误或非法数据")
	}
	return ExecutionStreamChunk{PublicLine: append(append([]byte("data: "), public...), '\n'), Usage: usage}, nil
}

func executeHTTPRequest(ctx context.Context, client *http.Client, url, token, requestID string, body map[string]interface{}, stream bool) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("请求体序列化失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("构造上游请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return client.Do(req)
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

func normalizeBifrostJSON(raw []byte, requireChoices, allowUsageOnly bool) ([]byte, ExecutionUsage, bool) {
	var value map[string]interface{}
	if json.Unmarshal(raw, &value) != nil {
		return nil, ExecutionUsage{}, false
	}
	if hasBifrostError(value) {
		return nil, ExecutionUsage{}, false
	}
	usage := parseExecutionUsage(raw)
	choices, choicesPresent := value["choices"]
	if requireChoices && (!choicesPresent || emptyJSONList(choices)) && !(allowUsageOnly && usage.Present) {
		return nil, usage, false
	}
	sanitizeBifrostResponse(value)
	public, err := json.Marshal(value)
	return public, usage, err == nil
}

func hasBifrostError(value map[string]interface{}) bool {
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

// sanitizeBifrostResponse 顶层只允许 OpenAI 兼容字段，未知字段默认不对外公开。
func sanitizeBifrostResponse(value map[string]interface{}) {
	allowed := map[string]struct{}{
		"id": {}, "object": {}, "created": {}, "model": {}, "choices": {}, "usage": {},
		"system_fingerprint": {}, "service_tier": {},
	}
	for key := range value {
		if _, ok := allowed[key]; !ok {
			delete(value, key)
		}
	}
	redactBifrostFields(value)
}

func redactBifrostFields(value interface{}) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			switch strings.ToLower(key) {
			case "extra_fields", "routing_info", "provider_response_headers", "is_bifrost_error", "api_key", "api_key_name", "bifrost_key_name":
				delete(typed, key)
			default:
				redactBifrostFields(child)
			}
		}
	case []interface{}:
		for _, child := range typed {
			redactBifrostFields(child)
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
		if *u.TotalTokens < 0 {
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
