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
	openRouterImagesEndpoint = "https://openrouter.ai/api/v1/images"
	openRouterMaxResponse    = int64(32 << 20)
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
	if a == nil || request.RequestID == "" || request.ModelCode == "" || request.Prompt == "" || request.Count == 0 {
		return ProviderImageResult{}, ErrImageResultInvalid
	}
	upstreamModel, ok := a.modelMap[request.ModelCode]
	if !ok {
		return ProviderImageResult{}, ErrImageResultInvalid
	}
	body := map[string]interface{}{
		"model": upstreamModel, "prompt": request.Prompt, "resolution": request.Resolution,
		"aspect_ratio": request.AspectRatio, "n": request.Count, "stream": false,
		"provider": map[string]interface{}{"only": []string{a.providerTag}, "allow_fallbacks": false},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return ProviderImageResult{}, ErrImageResultInvalid
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(raw))
	if err != nil {
		return ProviderImageResult{}, ErrImageResultInvalid
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+a.apiKey)
	httpRequest.Header.Set("User-Agent", "Molin-Image-Gateway/1.0")
	response, err := a.client.Do(httpRequest)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return ProviderImageResult{}, ErrProviderTimeout
		}
		if errors.Is(err, context.Canceled) {
			return ProviderImageResult{}, ErrProviderDisconnected
		}
		// 请求是否已到达上游无法证明时必须进入结果未知，禁止自动重试。
		return ProviderImageResult{ResultUnknown: true}, ErrProviderUnknown
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, openRouterMaxResponse+1)
	responseRaw, err := io.ReadAll(limited)
	if err != nil {
		return ProviderImageResult{ResultUnknown: true}, ErrProviderUnknown
	}
	if int64(len(responseRaw)) > openRouterMaxResponse {
		return ProviderImageResult{ResultUnknown: true}, ErrProviderUnknown
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ProviderImageResult{}, ErrProviderFailed
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
		return ProviderImageResult{ResultUnknown: true}, ErrProviderUnknown
	}
	providerRequestID := payload.ID
	if providerRequestID == "" {
		providerRequestID = payload.RequestID
	}
	result := ProviderImageResult{ProviderRequestID: sanitizeProviderRequestID(providerRequestID), Images: make([]ProviderImage, 0, len(payload.Data))}
	for index, item := range payload.Data {
		if item.B64JSON == "" || int64(len(item.B64JSON)) > openRouterMaxResponse {
			return ProviderImageResult{ProviderRequestID: result.ProviderRequestID, ResultUnknown: true}, ErrProviderUnknown
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
