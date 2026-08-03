package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
)

var (
	ErrPriceUnavailable       = errors.New("价格不可用")
	ErrPriceExpired           = errors.New("价格成本已过期")
	ErrMarginBelowMinimum     = errors.New("价格毛利低于下限")
	ErrUnquotableRequest      = errors.New("请求无法计算最坏成本")
	ErrBillingAmountException = errors.New("实际金额超过预占金额")
)

const minimumSuccessfulCharge = "0.000001"

type activePriceReader interface {
	FindActiveVersion(ctx context.Context, modelCode string, at time.Time) (*model.AIPriceVersion, []model.AIPriceSKU, error)
}

// PriceSnapshot 是逐请求冻结的价格事实，后续结算只读快照，不重新读取当前活动价格。
type PriceSnapshot struct {
	PriceVersionID      uint64                      `json:"price_version_id"`
	LogicalModelCode    string                      `json:"logical_model_code"`
	VersionNo           uint64                      `json:"version_no"`
	Currency            string                      `json:"currency"`
	ExchangeRate        string                      `json:"exchange_rate"`
	RoundingMode        string                      `json:"rounding_mode"`
	FailureChargePolicy string                      `json:"failure_charge_policy"`
	MinimumCharge       string                      `json:"minimum_charge"`
	MaxInputTokens      uint64                      `json:"max_input_tokens"`
	MaxOutputTokens     uint64                      `json:"max_output_tokens"`
	SKUs                map[string]PriceSKUSnapshot `json:"skus"`
}

type PriceSKUSnapshot struct {
	MeterType     string `json:"meter_type"`
	VariantHash   string `json:"variant_hash"`
	CostUnitPrice string `json:"cost_unit_price"`
	SaleUnitPrice string `json:"sale_unit_price"`
	Scale         string `json:"scale"`
	Currency      string `json:"currency"`
}

type PriceQuote struct {
	Snapshot     PriceSnapshot
	SnapshotJSON json.RawMessage
	QuotedAmount decimal.Decimal
	HeldAmount   decimal.Decimal
	MaxTokens    uint64
}

// BilledUsage 是结算后的互斥计量项；input/cache 和 output/reasoning 不重复收费。
type BilledUsage struct {
	Items       []model.AIUsageItem
	FinalAmount decimal.Decimal
}

type PricingService struct {
	repo             activePriceReader
	now              func() time.Time
	defaultMaxTokens uint64
}

func NewPricingService(repo activePriceReader, defaultMaxTokens ...uint64) *PricingService {
	fallback := uint64(4096)
	if len(defaultMaxTokens) > 0 && defaultMaxTokens[0] > 0 {
		fallback = defaultMaxTokens[0]
	}
	return &PricingService{repo: repo, now: time.Now, defaultMaxTokens: fallback}
}

func (s *PricingService) Quote(ctx context.Context, modelCode string, body map[string]interface{}) (*PriceQuote, error) {
	if s == nil || s.repo == nil {
		return nil, ErrPriceUnavailable
	}
	if err := validateSingleChoice(body); err != nil {
		// G3 只支持单候选；n>1 会放大最大输出成本，必须在查价、预占和调用上游前失败关闭。
		return nil, err
	}
	now := s.now()
	version, skus, err := s.repo.FindActiveVersion(ctx, strings.TrimSpace(modelCode), now)
	if err != nil || version == nil {
		return nil, ErrPriceUnavailable
	}
	if !version.CostExpiresAt.After(now) {
		return nil, ErrPriceExpired
	}
	if version.Currency != "CNY" || !version.ExchangeRate.Equal(decimal.NewFromInt(1)) || version.RoundingMode != "ceil_8" || version.FailureChargePolicy != "confirmed_usage" {
		return nil, ErrPriceUnavailable
	}
	// 兼容既有 OpenAI 客户端：未传 max_tokens 时使用平台配置的保守上限报价，显式非法值仍失败关闭。
	fallbackMaxTokens := min(s.defaultMaxTokens, version.MaxOutputTokens)
	maxTokens, err := requestMaxTokens(body, fallbackMaxTokens)
	if err != nil || maxTokens == 0 || maxTokens > version.MaxOutputTokens || version.MaxInputTokens == 0 {
		return nil, ErrUnquotableRequest
	}
	snapshot := PriceSnapshot{
		PriceVersionID: version.ID, LogicalModelCode: version.LogicalModelCode, VersionNo: version.VersionNo,
		Currency: version.Currency, ExchangeRate: version.ExchangeRate.String(), RoundingMode: version.RoundingMode,
		FailureChargePolicy: version.FailureChargePolicy, MinimumCharge: minimumSuccessfulCharge, MaxInputTokens: version.MaxInputTokens,
		MaxOutputTokens: version.MaxOutputTokens, SKUs: make(map[string]PriceSKUSnapshot, len(skus)),
	}
	for _, sku := range skus {
		if sku.Currency != version.Currency || sku.Scale.LessThanOrEqual(decimal.Zero) || sku.SaleUnitPrice.LessThanOrEqual(decimal.Zero) || sku.CostUnitPrice.LessThan(decimal.Zero) {
			return nil, ErrPriceUnavailable
		}
		margin := sku.SaleUnitPrice.Sub(sku.CostUnitPrice).Div(sku.SaleUnitPrice)
		if margin.LessThan(version.MinMarginRate) {
			return nil, ErrMarginBelowMinimum
		}
		if _, exists := snapshot.SKUs[sku.MeterType]; exists {
			return nil, ErrPriceUnavailable
		}
		snapshot.SKUs[sku.MeterType] = PriceSKUSnapshot{
			MeterType: sku.MeterType, VariantHash: sku.VariantHash,
			CostUnitPrice: sku.CostUnitPrice.String(), SaleUnitPrice: sku.SaleUnitPrice.String(),
			Scale: sku.Scale.String(), Currency: sku.Currency,
		}
	}
	for _, meter := range []string{"input_tokens", "output_tokens", "cached_tokens", "reasoning_tokens"} {
		if _, ok := snapshot.SKUs[meter]; !ok {
			return nil, ErrPriceUnavailable
		}
	}
	inputRate := maxSKUPrice(snapshot.SKUs["input_tokens"], snapshot.SKUs["cached_tokens"])
	outputRate := maxSKUPrice(snapshot.SKUs["output_tokens"], snapshot.SKUs["reasoning_tokens"])
	held := priceAmount(decimal.NewFromBigInt(new(big.Int).SetUint64(version.MaxInputTokens), 0), inputRate).
		Add(priceAmount(decimal.NewFromBigInt(new(big.Int).SetUint64(maxTokens), 0), outputRate))
	held = held.RoundCeil(8)
	minimum, err := decimal.NewFromString(snapshot.MinimumCharge)
	if err != nil || minimum.LessThanOrEqual(decimal.Zero) {
		return nil, ErrPriceUnavailable
	}
	if held.LessThan(minimum) {
		held = minimum
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	return &PriceQuote{Snapshot: snapshot, SnapshotJSON: raw, QuotedAmount: held, HeldAmount: held, MaxTokens: maxTokens}, nil
}

// CalculateFinal 按冻结快照计算唯一最终金额，禁止读取后来发布的新价格。
func (s *PricingService) CalculateFinal(requestID string, snapshotJSON json.RawMessage, usage ExecutionUsage) (*BilledUsage, error) {
	return s.calculateFinal(requestID, snapshotJSON, usage, true)
}

// CalculateFinalWithPolicy 仅在成功且存在正用量时应用逐请求最低收费。
func (s *PricingService) CalculateFinalWithPolicy(requestID string, snapshotJSON json.RawMessage, usage ExecutionUsage, applyMinimum bool) (*BilledUsage, error) {
	return s.calculateFinal(requestID, snapshotJSON, usage, applyMinimum)
}

func (s *PricingService) calculateFinal(requestID string, snapshotJSON json.RawMessage, usage ExecutionUsage, applyMinimum bool) (*BilledUsage, error) {
	if !usage.Present || usage.PromptTokens < 0 || usage.CompletionTokens < 0 || usage.CachedTokens < 0 || usage.ReasoningTokens < 0 {
		return nil, ErrUnquotableRequest
	}
	if usage.CachedTokens > usage.PromptTokens || usage.ReasoningTokens > usage.CompletionTokens {
		return nil, ErrUnquotableRequest
	}
	var snapshot PriceSnapshot
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		return nil, ErrPriceUnavailable
	}
	input := usage.PromptTokens - usage.CachedTokens
	output := usage.CompletionTokens - usage.ReasoningTokens
	values := []struct {
		meter    string
		quantity int64
	}{
		{meter: "input_tokens", quantity: input},
		{meter: "cached_tokens", quantity: usage.CachedTokens},
		{meter: "output_tokens", quantity: output},
		{meter: "reasoning_tokens", quantity: usage.ReasoningTokens},
	}
	items := make([]model.AIUsageItem, 0, len(values))
	total := decimal.Zero
	for _, value := range values {
		sku, ok := snapshot.SKUs[value.meter]
		if !ok {
			return nil, ErrPriceUnavailable
		}
		unitPrice, err := decimal.NewFromString(sku.SaleUnitPrice)
		if err != nil {
			return nil, ErrPriceUnavailable
		}
		amount := priceAmount(decimal.NewFromInt(value.quantity), sku).RoundCeil(8)
		total = total.Add(amount)
		items = append(items, model.AIUsageItem{
			// 序号 0 保留上游原始 Usage，序号 1 保存可审计计费拆分，禁止改写原始数量。
			RequestID: requestID, MeterType: value.meter, Source: "provider", SequenceNo: 1,
			Quantity: decimal.NewFromInt(value.quantity), UnitPrice: &unitPrice, Amount: &amount,
		})
	}
	minimum, err := decimal.NewFromString(snapshot.MinimumCharge)
	if err != nil || minimum.LessThanOrEqual(decimal.Zero) {
		return nil, ErrPriceUnavailable
	}
	if applyMinimum && usage.PromptTokens+usage.CompletionTokens > 0 && total.LessThan(minimum) {
		delta := minimum.Sub(total)
		for i := range items {
			if items[i].MeterType == "output_tokens" {
				adjusted := items[i].Amount.Add(delta)
				items[i].Amount = &adjusted
				break
			}
		}
		total = minimum
	}
	return &BilledUsage{Items: items, FinalAmount: total.RoundCeil(8)}, nil
}

func maxSKUPrice(left, right PriceSKUSnapshot) PriceSKUSnapshot {
	leftPrice, leftErr := decimal.NewFromString(left.SaleUnitPrice)
	rightPrice, rightErr := decimal.NewFromString(right.SaleUnitPrice)
	if leftErr != nil || rightErr != nil {
		return PriceSKUSnapshot{}
	}
	leftScale, _ := decimal.NewFromString(left.Scale)
	rightScale, _ := decimal.NewFromString(right.Scale)
	leftPerUnit := leftPrice.Div(leftScale)
	rightPerUnit := rightPrice.Div(rightScale)
	if rightPerUnit.GreaterThan(leftPerUnit) {
		return right
	}
	return left
}

func priceAmount(quantity decimal.Decimal, sku PriceSKUSnapshot) decimal.Decimal {
	price, priceErr := decimal.NewFromString(sku.SaleUnitPrice)
	scale, scaleErr := decimal.NewFromString(sku.Scale)
	if priceErr != nil || scaleErr != nil || scale.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero
	}
	return quantity.Mul(price).Div(scale)
}

func requestMaxTokens(body map[string]interface{}, fallback uint64) (uint64, error) {
	raw, ok := body["max_tokens"]
	if !ok {
		if fallback == 0 {
			return 0, ErrUnquotableRequest
		}
		return fallback, nil
	}
	var text string
	switch value := raw.(type) {
	case json.Number:
		text = value.String()
	case string:
		text = value
	case int:
		text = fmt.Sprintf("%d", value)
	case int64:
		text = fmt.Sprintf("%d", value)
	case uint64:
		return value, nil
	default:
		return 0, ErrUnquotableRequest
	}
	parsed, err := decimal.NewFromString(text)
	if err != nil || !parsed.IsInteger() || parsed.LessThanOrEqual(decimal.Zero) || !parsed.BigInt().IsUint64() {
		return 0, ErrUnquotableRequest
	}
	return parsed.BigInt().Uint64(), nil
}

func validateSingleChoice(body map[string]interface{}) error {
	raw, ok := body["n"]
	if !ok {
		return nil
	}
	switch value := raw.(type) {
	case json.Number:
		if value.String() == "1" {
			return nil
		}
	case int:
		if value == 1 {
			return nil
		}
	case int64:
		if value == 1 {
			return nil
		}
	case uint64:
		if value == 1 {
			return nil
		}
	}
	// 字符串、浮点、指数写法和其他类型均不属于 API 契约中的整数 1。
	return ErrUnquotableRequest
}
