package image

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/shopspring/decimal"
)

const (
	openRouterImagesEndpoint        = "https://openrouter.ai/api/v1/images"
	openRouterMaxResponse           = int64(32 << 20)
	openRouterMetadataHeader        = "X-OpenRouter-Experimental-Metadata"
	openRouterMetadataHeaderValue   = "enabled"
	openRouterForbiddenCredit       = "403:credit_limit"
	openRouterForbiddenBudget       = "403:workspace_budget"
	openRouterForbiddenModel        = "403:model_policy"
	openRouterForbiddenProvider     = "403:provider_policy"
	openRouterForbiddenData         = "403:data_policy"
	openRouterForbiddenContent      = "403:content_guardrail"
	openRouterForbiddenKey          = "403:key_permission"
	openRouterForbiddenUpstream     = "403:upstream_permission"
	openRouterForbiddenUnclassified = "403:unknown"
)

type OpenRouterImageAdapterConfig struct {
	APIKey      string
	ProviderTag string
	MaxCostUSD  string
	Timeout     time.Duration
	ModelMap    map[string]string
}

type OpenRouterImageAdapter struct {
	apiKey      string
	providerTag string
	endpoint    string
	client      *http.Client
	modelMap    map[string]string
	maxCostUSD  decimal.Decimal
}

// NewOpenRouterImageAdapter 只允许固定OpenRouter Images HTTPS入口；构造不会发送网络请求。
func NewOpenRouterImageAdapter(config OpenRouterImageAdapterConfig) (*OpenRouterImageAdapter, error) {
	client := &http.Client{
		Timeout: config.Timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("OpenRouter图片接口禁止重定向")
		},
	}
	return newOpenRouterImageAdapter(config, openRouterImagesEndpoint, client, false)
}

func newOpenRouterImageAdapter(config OpenRouterImageAdapterConfig, endpoint string, client *http.Client, allowTestEndpoint bool) (*OpenRouterImageAdapter, error) {
	key := strings.TrimSpace(config.APIKey)
	providerTag := strings.TrimSpace(config.ProviderTag)
	maxCostText := strings.TrimSpace(config.MaxCostUSD)
	maxCostUSD, costErr := decimal.NewFromString(maxCostText)
	if key != config.APIKey || len(key) < 16 || providerTag == "" || maxCostText != config.MaxCostUSD || costErr != nil || !maxCostUSD.IsPositive() || config.Timeout <= 0 || config.Timeout > 3*time.Minute || client == nil || len(config.ModelMap) == 0 {
		return nil, ErrImageResultInvalid
	}
	for _, value := range key {
		if unicode.IsSpace(value) || unicode.IsControl(value) {
			return nil, ErrImageResultInvalid
		}
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, ErrImageResultInvalid
	}
	if !allowTestEndpoint && endpoint != openRouterImagesEndpoint {
		return nil, ErrImageResultInvalid
	}
	if allowTestEndpoint && parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, ErrImageResultInvalid
	}
	modelMap := make(map[string]string, len(config.ModelMap))
	for logical, upstream := range config.ModelMap {
		normalizedLogical, normalizedUpstream := strings.TrimSpace(logical), strings.TrimSpace(upstream)
		if normalizedLogical == "" || normalizedUpstream == "" || logical != normalizedLogical || upstream != normalizedUpstream {
			return nil, ErrImageResultInvalid
		}
		if _, duplicate := modelMap[normalizedLogical]; duplicate {
			return nil, ErrImageResultInvalid
		}
		modelMap[normalizedLogical] = normalizedUpstream
	}
	return &OpenRouterImageAdapter{apiKey: key, providerTag: providerTag, endpoint: endpoint, client: client, modelMap: modelMap, maxCostUSD: maxCostUSD}, nil
}

func (a *OpenRouterImageAdapter) Name() string { return "openrouter-images" }

// Generate 严格一次POST且零重试；Prompt和Base64只存在于请求/响应内存，不写日志或持久化。
func (a *OpenRouterImageAdapter) Generate(ctx context.Context, request ProviderImageRequest) (ProviderImageResult, error) {
	result := ProviderImageResult{ProviderCode: a.Name()}
	if a == nil || request.RequestID == "" || request.ModelCode == "" || request.Prompt == "" || request.Count == 0 {
		return result, ErrImageResultInvalid
	}
	upstreamModel, ok := a.modelMap[request.ModelCode]
	if !ok {
		return result, ErrImageResultInvalid
	}
	body := map[string]interface{}{
		"model": upstreamModel, "prompt": request.Prompt, "resolution": request.Resolution,
		"aspect_ratio": request.AspectRatio, "n": request.Count, "stream": false,
		"provider": map[string]interface{}{"only": []string{a.providerTag}, "allow_fallbacks": false},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return result, ErrImageResultInvalid
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(raw))
	if err != nil {
		return result, ErrImageResultInvalid
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+a.apiKey)
	httpRequest.Header.Set("User-Agent", "Molin-Image-Gateway/1.0")
	// 请求OpenRouter返回路由阶段元数据；本地只把403压缩成固定分类，原始元数据不会进入日志或持久化。
	httpRequest.Header.Set(openRouterMetadataHeader, openRouterMetadataHeaderValue)
	result.ProviderAttempted = true
	response, err := a.client.Do(httpRequest)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return result, ErrProviderTimeout
		}
		if errors.Is(err, context.Canceled) {
			return result, ErrProviderDisconnected
		}
		// 请求是否已到达上游无法证明时必须进入结果未知，禁止自动重试。
		result.ResultUnknown = true
		return result, ErrProviderUnknown
	}
	defer response.Body.Close()
	result.ProviderHTTPStatus = response.StatusCode
	limited := io.LimitReader(response.Body, openRouterMaxResponse+1)
	responseRaw, err := io.ReadAll(limited)
	if err != nil {
		result.ResultUnknown = true
		return result, ErrProviderUnknown
	}
	if int64(len(responseRaw)) > openRouterMaxResponse {
		result.ResultUnknown = true
		return result, ErrProviderUnknown
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.ProviderErrorCode = parseOpenRouterFailureCode(response.StatusCode, responseRaw)
		return result, ErrProviderFailed
	}
	var payload struct {
		ID        string `json:"id"`
		RequestID string `json:"request_id"`
		Data      []struct {
			B64JSON   string `json:"b64_json"`
			MediaType string `json:"media_type"`
		} `json:"data"`
		Usage struct {
			Cost json.Number `json:"cost"`
		} `json:"usage"`
	}
	decoder := json.NewDecoder(bytes.NewReader(responseRaw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil || len(payload.Data) == 0 || uint64(len(payload.Data)) > request.Count {
		result.ResultUnknown = true
		return result, ErrProviderUnknown
	}
	providerRequestID := payload.ID
	if providerRequestID == "" {
		providerRequestID = payload.RequestID
	}
	result.ProviderRequestID = sanitizeProviderRequestID(providerRequestID)
	result.Images = make([]ProviderImage, 0, len(payload.Data))
	for index, item := range payload.Data {
		if item.B64JSON == "" || int64(len(item.B64JSON)) > openRouterMaxResponse {
			result.ResultUnknown = true
			return result, ErrProviderUnknown
		}
		result.Images = append(result.Images, ProviderImage{Index: uint64(index), Base64: item.B64JSON, MediaType: item.MediaType})
	}
	// OpenRouter官方Images响应必须携带usage.cost；缺失、非正数或越过本次授权上限都进入结果未知，禁止交付或重试。
	providerCost, costErr := decimal.NewFromString(payload.Usage.Cost.String())
	if costErr != nil || !providerCost.IsPositive() {
		result.ResultUnknown = true
		return result, ErrProviderUnknown
	}
	result.ProviderCostUSD = providerCost.String()
	if providerCost.GreaterThan(a.maxCostUSD) {
		result.ResultUnknown = true
		return result, ErrProviderUnknown
	}
	return result, nil
}

// parseOpenRouterFailureCode 对403只返回固定低敏分类，其他状态继续保留经过封闭字符集校验的上游错误码。
func parseOpenRouterFailureCode(statusCode int, raw []byte) string {
	if statusCode == http.StatusForbidden {
		return classifyOpenRouterForbidden(raw)
	}
	return parseOpenRouterErrorCode(raw)
}

type openRouterProviderResponse struct {
	Status     json.RawMessage `json:"status"`
	StatusCode json.RawMessage `json:"status_code"`
	HTTPStatus json.RawMessage `json:"http_status"`
}

// classifyOpenRouterForbidden 仅在内存中读取有限诊断字段，并把任何未知形态失败关闭为403:unknown。
func classifyOpenRouterForbidden(raw []byte) string {
	var payload struct {
		Error struct {
			Code     json.RawMessage            `json:"code"`
			Message  string                     `json:"message"`
			Metadata map[string]json.RawMessage `json:"metadata"`
		} `json:"error"`
		OpenRouterMetadata struct {
			Pipeline []struct {
				Type    string `json:"type"`
				Name    string `json:"name"`
				Summary string `json:"summary"`
			} `json:"pipeline"`
			ProviderResponses []openRouterProviderResponse `json:"provider_responses"`
		} `json:"openrouter_metadata"`
		ProviderResponses []openRouterProviderResponse `json:"provider_responses"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return openRouterForbiddenUnclassified
	}

	// 分类器只保留最多64个短期内存信号，避免异常错误体造成额外内存放大。
	signals := make([]string, 0, 16)
	appendOpenRouterSignal(&signals, parseOpenRouterErrorCode(raw))
	appendOpenRouterSignal(&signals, payload.Error.Message)
	for index, stage := range payload.OpenRouterMetadata.Pipeline {
		if index >= 32 {
			break
		}
		appendOpenRouterSignal(&signals, stage.Type)
		appendOpenRouterSignal(&signals, stage.Name)
		appendOpenRouterSignal(&signals, stage.Summary)
	}
	metadataKeys := 0
	for key := range payload.Error.Metadata {
		if metadataKeys >= 32 {
			break
		}
		appendOpenRouterSignal(&signals, key)
		metadataKeys++
	}
	joined := strings.Join(signals, " ")

	// 顺序从确定性最高的控制面策略到上游权限，避免泛化词覆盖更具体的类别。
	switch {
	case containsAnyOpenRouterSignal(joined, "workspace budget", "budget exceeded", "budget limit"):
		return openRouterForbiddenBudget
	case containsAnyOpenRouterSignal(joined, "insufficient_credits", "insufficient credits", "credit limit", "key limit", "not enough credits"):
		return openRouterForbiddenCredit
	case containsAnyOpenRouterSignal(joined, "model allowlist", "model is not allowed", "requested model is not allowed", "model access denied"):
		return openRouterForbiddenModel
	case containsAnyOpenRouterSignal(joined, "zero data retention", "zdr", "data policy", "data_collection", "data collection"):
		return openRouterForbiddenData
	case containsAnyOpenRouterSignal(joined, "provider allowlist", "provider is not allowed", "requested provider is not allowed", "no eligible provider", "no endpoints"):
		return openRouterForbiddenProvider
	case containsAnyOpenRouterSignal(joined, "api key does not have permission", "api key permission", "key_permission"):
		return openRouterForbiddenKey
	case containsAnyOpenRouterSignal(joined, "guardrail", "prompt injection", "jailbreak", "sensitive info", "content filter", "safety", "patterns"):
		return openRouterForbiddenContent
	case openRouterResponsesContainForbidden(payload.ProviderResponses) || openRouterResponsesContainForbidden(payload.OpenRouterMetadata.ProviderResponses) || openRouterMetadataNamesProvider(payload.Error.Metadata):
		return openRouterForbiddenUpstream
	default:
		return openRouterForbiddenUnclassified
	}
}

func openRouterMetadataNamesProvider(metadata map[string]json.RawMessage) bool {
	providerName, exists := metadata["provider_name"]
	if !exists {
		return false
	}
	var value string
	return json.Unmarshal(providerName, &value) == nil && strings.TrimSpace(value) != ""
}

func appendOpenRouterSignal(signals *[]string, value string) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 4096 || len(*signals) >= 64 {
		return
	}
	*signals = append(*signals, strings.ToLower(value))
}

func containsAnyOpenRouterSignal(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func openRouterResponsesContainForbidden(responses []openRouterProviderResponse) bool {
	for _, response := range responses {
		if openRouterStatusIsForbidden(response.Status) || openRouterStatusIsForbidden(response.StatusCode) || openRouterStatusIsForbidden(response.HTTPStatus) {
			return true
		}
	}
	return false
}

func openRouterStatusIsForbidden(raw json.RawMessage) bool {
	return strings.Trim(strings.TrimSpace(string(raw)), `"`) == "403"
}

// parseOpenRouterErrorCode 只提取封闭字符集错误码；错误消息和原始响应一律不落库、不写日志。
func parseOpenRouterErrorCode(raw []byte) string {
	var payload struct {
		Error struct {
			Code json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Error.Code) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(payload.Error.Code, &text); err != nil {
		var number json.Number
		if numberErr := json.Unmarshal(payload.Error.Code, &number); numberErr != nil {
			return ""
		}
		text = number.String()
	}
	return sanitizeProviderErrorCode(text)
}

func sanitizeProviderErrorCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return ""
	}
	for _, char := range value {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._:-", char)) {
			return ""
		}
	}
	return value
}

func sanitizeProviderRequestID(value string) string {
	if value == "" || len(value) > 191 {
		return ""
	}
	for _, char := range value {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._:-", char)) {
			return ""
		}
	}
	return value
}

var _ ImageProviderAdapter = (*OpenRouterImageAdapter)(nil)

func (a *OpenRouterImageAdapter) String() string {
	return fmt.Sprintf("OpenRouterImageAdapter{endpoint=%s,provider=%s}", a.endpoint, a.providerTag)
}
