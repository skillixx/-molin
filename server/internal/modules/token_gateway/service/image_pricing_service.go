package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

const (
	priceSnapshotSchemaV2 = 2
	imageQuoteTTL         = 5 * time.Minute
	legacyVariantHash     = "0000000000000000000000000000000000000000000000000000000000000000"
)

var (
	ErrImageOptionUnsupported = errors.New("图片规格不受支持")
	ErrImageQuoteExpired      = errors.New("图片报价已过期")
	ErrImageQuoteConflict     = errors.New("图片报价请求指纹冲突")
	ErrImageQuoteConsumed     = errors.New("图片报价已被其他请求消费")
	ErrImageQuoteNotFound     = errors.New("图片报价不存在")
	ErrImageQuoteSecret       = errors.New("图片报价指纹密钥无效")
)

var lowerHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// MetricPriceSnapshotV2 只冻结本次选择的计价行，禁止结算时重新读取当前活动价格。
type MetricPriceSnapshotV2 struct {
	SchemaVersion       int                       `json:"schema_version"`
	PriceVersionID      uint64                    `json:"price_version_id"`
	LogicalModelCode    string                    `json:"logical_model_code"`
	VersionNo           uint64                    `json:"version_no"`
	Capability          string                    `json:"capability"`
	PricingTemplate     string                    `json:"pricing_template"`
	PricePurpose        string                    `json:"price_purpose"`
	Currency            string                    `json:"currency"`
	ExchangeRate        string                    `json:"exchange_rate"`
	RoundingMode        string                    `json:"rounding_mode"`
	FailureChargePolicy string                    `json:"failure_charge_policy"`
	MinimumCharge       string                    `json:"minimum_charge"`
	QuotedAmount        string                    `json:"quoted_amount"`
	HeldAmount          string                    `json:"held_amount"`
	SelectedLines       []MetricPriceLineSnapshot `json:"selected_lines"`
}

// MetricPriceLineSnapshot 保存一条由 meter_type 与 variant_hash 唯一定位的价格事实。
type MetricPriceLineSnapshot struct {
	MeterType     string          `json:"meter_type"`
	VariantHash   string          `json:"variant_hash"`
	VariantJSON   json.RawMessage `json:"variant_json"`
	UsageUnit     string          `json:"usage_unit"`
	UnitSize      string          `json:"unit_size"`
	QuotedUsage   string          `json:"quoted_usage"`
	CostUnitPrice string          `json:"cost_unit_price"`
	SaleUnitPrice string          `json:"sale_unit_price"`
	Currency      string          `json:"currency"`
}

// DecodedPriceSnapshot 显式区分历史 Chat V1 与图片 V2，未知版本必须失败关闭。
type DecodedPriceSnapshot struct {
	SchemaVersion int
	ChatV1        *PriceSnapshot
	MetricV2      *MetricPriceSnapshotV2
}

// ImagePriceVariant 是图片规格的规范化业务值，字段顺序不参与 hash，编码前会转换为有序键集合。
type ImagePriceVariant struct {
	Resolution   string `json:"resolution"`
	AspectRatio  string `json:"aspect_ratio"`
	Quality      string `json:"quality"`
	OutputFormat string `json:"output_format"`
	Delivery     string `json:"delivery"`
}

type imagePricingLimits struct {
	MaxCount uint64              `json:"max_count"`
	Variants []ImagePriceVariant `json:"variants"`
}

// ImageQuoteCommand 只包含报价所需的规范化规格；Prompt 明文不属于定价输入。
type ImageQuoteCommand struct {
	LogicalModelCode string
	Count            uint64
	Variant          ImagePriceVariant
}

type ImagePriceQuote struct {
	Snapshot       MetricPriceSnapshotV2
	SnapshotJSON   json.RawMessage
	VariantHash    string
	QuotedAmount   decimal.Decimal
	HeldAmount     decimal.Decimal
	RequestedCount uint64
}

type ImageSettlement struct {
	UsageFact     model.AIUsageItem
	SaleLine      model.AIUsageItem
	CostLine      model.AIUsageItem
	SettledAmount decimal.Decimal
	ProviderCost  decimal.Decimal
	ReleaseAmount decimal.Decimal
}

type ImagePricingService struct {
	repo activePriceReader
	now  func() time.Time
}

func NewImagePricingService(repo activePriceReader) *ImagePricingService {
	return &ImagePricingService{repo: repo, now: time.Now}
}

// QuoteImage 按唯一图片 variant 生成 V2 快照；缺价、重复价、零价和过期成本全部失败关闭。
func (s *ImagePricingService) QuoteImage(ctx context.Context, command ImageQuoteCommand) (*ImagePriceQuote, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(command.LogicalModelCode) == "" || command.Count == 0 {
		return nil, ErrUnquotableRequest
	}
	now := s.now()
	version, skus, err := s.repo.FindActiveVersion(ctx, strings.TrimSpace(command.LogicalModelCode), now)
	if err != nil || version == nil {
		return nil, ErrPriceUnavailable
	}
	if version.Capability != model.AIImageCapability || version.PricingTemplate != "image_variant" ||
		(version.PricePurpose != "commercial" && version.PricePurpose != "test_fixture") ||
		!version.CostExpiresAt.After(now) {
		if !version.CostExpiresAt.After(now) {
			return nil, ErrPriceExpired
		}
		return nil, ErrPriceUnavailable
	}
	if version.Currency != "CNY" || !version.ExchangeRate.Equal(decimal.NewFromInt(1)) ||
		version.RoundingMode != "ceil_8" || version.FailureChargePolicy != "confirmed_usage" ||
		version.MinimumCharge.LessThanOrEqual(decimal.Zero) {
		return nil, ErrPriceUnavailable
	}

	var limits imagePricingLimits
	if err := json.Unmarshal(version.LimitsJSON, &limits); err != nil || limits.MaxCount == 0 || command.Count > limits.MaxCount || len(limits.Variants) == 0 {
		return nil, ErrImageOptionUnsupported
	}
	variantJSON, variantHash, err := canonicalImageVariant(command.Variant)
	if err != nil {
		return nil, err
	}
	allowed := false
	for _, candidate := range limits.Variants {
		_, candidateHash, candidateErr := canonicalImageVariant(candidate)
		if candidateErr == nil && subtle.ConstantTimeCompare([]byte(candidateHash), []byte(variantHash)) == 1 {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, ErrImageOptionUnsupported
	}

	var selected *model.AIPriceSKU
	for i := range skus {
		sku := &skus[i]
		if sku.MeterType != "image_count" || subtle.ConstantTimeCompare([]byte(sku.VariantHash), []byte(variantHash)) != 1 {
			continue
		}
		if selected != nil {
			return nil, ErrPriceUnavailable
		}
		selected = sku
	}
	if selected == nil || selected.Currency != "CNY" || selected.Scale.LessThanOrEqual(decimal.Zero) ||
		selected.SaleUnitPrice.LessThanOrEqual(decimal.Zero) || selected.CostUnitPrice.LessThan(decimal.Zero) {
		return nil, ErrPriceUnavailable
	}
	storedVariant, storedHash, err := canonicalizeStoredVariant(selected.VariantJSON)
	if err != nil || storedHash != variantHash {
		return nil, ErrPriceUnavailable
	}
	// MySQL JSON 会重排空白；安全边界由规范化字段重算的SHA-256保证，不能依赖原始字节完全相同。
	_ = storedVariant
	_ = variantJSON
	margin := selected.SaleUnitPrice.Sub(selected.CostUnitPrice).Div(selected.SaleUnitPrice)
	if margin.LessThan(version.MinMarginRate) {
		return nil, ErrMarginBelowMinimum
	}

	usage := decimal.NewFromBigInt(new(big.Int).SetUint64(command.Count), 0)
	quoted := usage.Mul(selected.SaleUnitPrice).Div(selected.Scale).RoundCeil(8)
	if quoted.LessThan(version.MinimumCharge) {
		quoted = version.MinimumCharge
	}
	line := MetricPriceLineSnapshot{
		MeterType: "image_count", VariantHash: variantHash, VariantJSON: variantJSON,
		UsageUnit: "count", UnitSize: selected.Scale.String(), QuotedUsage: usage.String(),
		CostUnitPrice: selected.CostUnitPrice.String(), SaleUnitPrice: selected.SaleUnitPrice.String(), Currency: "CNY",
	}
	snapshot := MetricPriceSnapshotV2{
		SchemaVersion: priceSnapshotSchemaV2, PriceVersionID: version.ID, LogicalModelCode: version.LogicalModelCode,
		VersionNo: version.VersionNo, Capability: version.Capability, PricingTemplate: version.PricingTemplate,
		PricePurpose: version.PricePurpose, Currency: version.Currency, ExchangeRate: version.ExchangeRate.String(),
		RoundingMode: version.RoundingMode, FailureChargePolicy: version.FailureChargePolicy,
		MinimumCharge: version.MinimumCharge.String(), QuotedAmount: quoted.String(), HeldAmount: quoted.String(),
		SelectedLines: []MetricPriceLineSnapshot{line},
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	return &ImagePriceQuote{Snapshot: snapshot, SnapshotJSON: raw, VariantHash: variantHash, QuotedAmount: quoted, HeldAmount: quoted, RequestedCount: command.Count}, nil
}

// CalculateImageFinal 只按实际可交付主图数量结算，实际数量不能超过报价数量。
func (s *ImagePricingService) CalculateImageFinal(requestID string, snapshotJSON json.RawMessage, actualBillableCount uint64) (*ImageSettlement, error) {
	return s.CalculateImageFinalWithProviderCount(requestID, snapshotJSON, actualBillableCount, actualBillableCount)
}

// CalculateImageFinalWithProviderCount 分离用户可交付数量与Provider已确认产物数量，部分失败成本不转嫁给用户。
func (s *ImagePricingService) CalculateImageFinalWithProviderCount(requestID string, snapshotJSON json.RawMessage, actualBillableCount, providerGeneratedCount uint64) (*ImageSettlement, error) {
	decoded, err := DecodePriceSnapshot(snapshotJSON)
	if err != nil || decoded.MetricV2 == nil {
		return nil, ErrPriceUnavailable
	}
	snapshot := decoded.MetricV2
	if snapshot.Capability != model.AIImageCapability || snapshot.PricingTemplate != "image_variant" || len(snapshot.SelectedLines) != 1 {
		return nil, ErrPriceUnavailable
	}
	line := snapshot.SelectedLines[0]
	if line.MeterType != "image_count" || line.UsageUnit != "count" {
		return nil, ErrPriceUnavailable
	}
	requested, err := decimal.NewFromString(line.QuotedUsage)
	if err != nil || !requested.IsInteger() || requested.LessThanOrEqual(decimal.Zero) || !requested.BigInt().IsUint64() ||
		actualBillableCount > requested.BigInt().Uint64() || providerGeneratedCount > requested.BigInt().Uint64() || providerGeneratedCount < actualBillableCount {
		return nil, ErrBillingAmountException
	}
	unitSize, saleUnitPrice, costUnitPrice, held, err := snapshotLineAmounts(snapshot, line)
	if err != nil {
		return nil, err
	}
	actual := decimal.NewFromBigInt(new(big.Int).SetUint64(actualBillableCount), 0)
	providerQuantity := decimal.NewFromBigInt(new(big.Int).SetUint64(providerGeneratedCount), 0)
	sale := actual.Mul(saleUnitPrice).Div(unitSize).RoundCeil(8)
	cost := providerQuantity.Mul(costUnitPrice).Div(unitSize).RoundCeil(8)
	if actualBillableCount > 0 {
		minimum, parseErr := decimal.NewFromString(snapshot.MinimumCharge)
		if parseErr != nil || minimum.LessThanOrEqual(decimal.Zero) {
			return nil, ErrPriceUnavailable
		}
		if sale.LessThan(minimum) {
			sale = minimum
		}
	}
	if sale.GreaterThan(held) {
		return nil, ErrBillingAmountException
	}
	release := held.Sub(sale).Round(8)
	currency := "CNY"
	priceVersionID := snapshot.PriceVersionID
	variantJSON := append(json.RawMessage(nil), line.VariantJSON...)
	usageFact := model.AIUsageItem{
		RequestID: requestID, MeterType: line.MeterType, Source: "gateway", RecordKind: model.AIUsageFact,
		VariantHash: line.VariantHash, VariantJSON: variantJSON, Quantity: actual, UsageUnit: "count", UnitSize: unitSize,
	}
	saleLine := model.AIUsageItem{
		RequestID: requestID, MeterType: line.MeterType, Source: "gateway", RecordKind: model.AIUsageSaleLine,
		PriceVersionID: &priceVersionID, VariantHash: line.VariantHash, VariantJSON: variantJSON,
		Quantity: actual, UsageUnit: "count", UnitSize: unitSize, UnitPrice: &saleUnitPrice, Amount: &sale, Currency: &currency,
	}
	costLine := model.AIUsageItem{
		RequestID: requestID, MeterType: line.MeterType, Source: "provider_cost", RecordKind: model.AIUsageCostLine,
		PriceVersionID: &priceVersionID, VariantHash: line.VariantHash, VariantJSON: variantJSON,
		Quantity: providerQuantity, UsageUnit: "count", UnitSize: unitSize, UnitPrice: &costUnitPrice, Amount: &cost, Currency: &currency,
	}
	return &ImageSettlement{UsageFact: usageFact, SaleLine: saleLine, CostLine: costLine, SettledAmount: sale, ProviderCost: cost, ReleaseAmount: release}, nil
}

// CalculateImageRefund 按冻结快照计算已结算图片的退款差额，禁止退款数量超过原可交付数量。
func (s *ImagePricingService) CalculateImageRefund(snapshotJSON json.RawMessage, settledCount, refundCount uint64) (decimal.Decimal, error) {
	if refundCount > settledCount {
		return decimal.Zero, ErrBillingAmountException
	}
	original, err := s.CalculateImageFinal("refund-original", snapshotJSON, settledCount)
	if err != nil {
		return decimal.Zero, err
	}
	remaining, err := s.CalculateImageFinal("refund-remaining", snapshotJSON, settledCount-refundCount)
	if err != nil {
		return decimal.Zero, err
	}
	refund := original.SettledAmount.Sub(remaining.SettledAmount).Round(8)
	if refund.IsNegative() || refund.GreaterThan(original.SettledAmount) {
		return decimal.Zero, ErrBillingAmountException
	}
	return refund, nil
}

// CalculateImageProviderCost 记录已确认生成但因安全拒绝等原因不可交付的Provider成本，不形成用户销售金额。
func (s *ImagePricingService) CalculateImageProviderCost(requestID string, snapshotJSON json.RawMessage, generatedCount uint64) (model.AIUsageItem, decimal.Decimal, error) {
	decoded, err := DecodePriceSnapshot(snapshotJSON)
	if err != nil || decoded.MetricV2 == nil || len(decoded.MetricV2.SelectedLines) != 1 {
		return model.AIUsageItem{}, decimal.Zero, ErrPriceUnavailable
	}
	snapshot := decoded.MetricV2
	line := snapshot.SelectedLines[0]
	requested, err := decimal.NewFromString(line.QuotedUsage)
	if err != nil || !requested.IsInteger() || !requested.BigInt().IsUint64() || generatedCount > requested.BigInt().Uint64() {
		return model.AIUsageItem{}, decimal.Zero, ErrBillingAmountException
	}
	unitSize, _, costUnitPrice, _, err := snapshotLineAmounts(snapshot, line)
	if err != nil {
		return model.AIUsageItem{}, decimal.Zero, err
	}
	quantity := decimal.NewFromBigInt(new(big.Int).SetUint64(generatedCount), 0)
	cost := quantity.Mul(costUnitPrice).Div(unitSize).RoundCeil(8)
	currency := "CNY"
	priceVersionID := snapshot.PriceVersionID
	item := model.AIUsageItem{
		RequestID: requestID, MeterType: line.MeterType, Source: "provider_cost", RecordKind: model.AIUsageCostLine,
		PriceVersionID: &priceVersionID, VariantHash: line.VariantHash, VariantJSON: append(json.RawMessage(nil), line.VariantJSON...),
		Quantity: quantity, UsageUnit: line.UsageUnit, UnitSize: unitSize, UnitPrice: &costUnitPrice, Amount: &cost, Currency: &currency,
	}
	return item, cost, nil
}

// DecodePriceSnapshot 兼容无 schema_version 的历史 Chat V1，并拒绝未知或畸形 V2。
func DecodePriceSnapshot(raw json.RawMessage) (*DecodedPriceSnapshot, error) {
	if len(raw) == 0 {
		return nil, ErrPriceUnavailable
	}
	var envelope struct {
		SchemaVersion *int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, ErrPriceUnavailable
	}
	if envelope.SchemaVersion == nil {
		var legacy PriceSnapshot
		if err := json.Unmarshal(raw, &legacy); err != nil || legacy.PriceVersionID == 0 || legacy.Currency != "CNY" || len(legacy.SKUs) != 4 {
			return nil, ErrPriceUnavailable
		}
		for _, meter := range []string{"input_tokens", "output_tokens", "cached_tokens", "reasoning_tokens"} {
			if _, ok := legacy.SKUs[meter]; !ok {
				return nil, ErrPriceUnavailable
			}
		}
		return &DecodedPriceSnapshot{SchemaVersion: 1, ChatV1: &legacy}, nil
	}
	if *envelope.SchemaVersion != priceSnapshotSchemaV2 {
		return nil, ErrPriceUnavailable
	}
	var snapshot MetricPriceSnapshotV2
	if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.PriceVersionID == 0 || snapshot.Currency != "CNY" || len(snapshot.SelectedLines) == 0 ||
		snapshot.Capability != model.AIImageCapability || (snapshot.PricingTemplate != "image_variant" && snapshot.PricingTemplate != "image_megapixel") ||
		(snapshot.PricePurpose != "commercial" && snapshot.PricePurpose != "test_fixture") || snapshot.ExchangeRate != "1" ||
		snapshot.RoundingMode != "ceil_8" || snapshot.FailureChargePolicy != "confirmed_usage" {
		return nil, ErrPriceUnavailable
	}
	seen := make(map[string]struct{}, len(snapshot.SelectedLines))
	quotedTotal := decimal.Zero
	for _, line := range snapshot.SelectedLines {
		if line.MeterType != "image_count" && line.MeterType != "image_megapixels" {
			return nil, ErrPriceUnavailable
		}
		if !lowerHex64.MatchString(line.VariantHash) || line.VariantHash == legacyVariantHash || len(line.VariantJSON) == 0 {
			return nil, ErrPriceUnavailable
		}
		canonicalVariant, canonicalHash, canonicalErr := canonicalizeStoredVariant(line.VariantJSON)
		if canonicalErr != nil || canonicalHash != line.VariantHash {
			return nil, ErrPriceUnavailable
		}
		// 价格快照写入MySQL JSON后空白可能变化，只接受语义规范化后的哈希一致。
		_ = canonicalVariant
		key := line.MeterType + ":" + line.VariantHash
		if _, exists := seen[key]; exists {
			return nil, ErrPriceUnavailable
		}
		seen[key] = struct{}{}
		unitSize, saleUnitPrice, _, _, err := snapshotLineAmounts(&snapshot, line)
		if err != nil {
			return nil, err
		}
		quotedUsage, parseErr := decimal.NewFromString(line.QuotedUsage)
		if parseErr != nil || quotedUsage.LessThanOrEqual(decimal.Zero) {
			return nil, ErrPriceUnavailable
		}
		quotedTotal = quotedTotal.Add(quotedUsage.Mul(saleUnitPrice).Div(unitSize).RoundCeil(8))
	}
	minimum, err := decimal.NewFromString(snapshot.MinimumCharge)
	if err != nil || minimum.LessThanOrEqual(decimal.Zero) {
		return nil, ErrPriceUnavailable
	}
	if quotedTotal.LessThan(minimum) {
		quotedTotal = minimum
	}
	quotedAmount, err := decimal.NewFromString(snapshot.QuotedAmount)
	if err != nil || !quotedAmount.Equal(quotedTotal.RoundCeil(8)) {
		return nil, ErrPriceUnavailable
	}
	heldAmount, err := decimal.NewFromString(snapshot.HeldAmount)
	if err != nil || !heldAmount.Equal(quotedAmount) {
		return nil, ErrPriceUnavailable
	}
	return &DecodedPriceSnapshot{SchemaVersion: priceSnapshotSchemaV2, MetricV2: &snapshot}, nil
}

func snapshotLineAmounts(snapshot *MetricPriceSnapshotV2, line MetricPriceLineSnapshot) (decimal.Decimal, decimal.Decimal, decimal.Decimal, decimal.Decimal, error) {
	if snapshot == nil || snapshot.RoundingMode != "ceil_8" || snapshot.FailureChargePolicy != "confirmed_usage" || line.Currency != "CNY" {
		return decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, ErrPriceUnavailable
	}
	unitSize, err := decimal.NewFromString(line.UnitSize)
	if err != nil || unitSize.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, ErrPriceUnavailable
	}
	sale, err := decimal.NewFromString(line.SaleUnitPrice)
	if err != nil || sale.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, ErrPriceUnavailable
	}
	cost, err := decimal.NewFromString(line.CostUnitPrice)
	if err != nil || cost.LessThan(decimal.Zero) {
		return decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, ErrPriceUnavailable
	}
	held, err := decimal.NewFromString(snapshot.HeldAmount)
	if err != nil || held.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, ErrPriceUnavailable
	}
	return unitSize, sale, cost, held, nil
}

func canonicalImageVariant(variant ImagePriceVariant) (json.RawMessage, string, error) {
	normalized := map[string]string{
		"aspect_ratio":  strings.TrimSpace(variant.AspectRatio),
		"delivery":      strings.TrimSpace(variant.Delivery),
		"output_format": strings.TrimSpace(variant.OutputFormat),
		"quality":       strings.TrimSpace(variant.Quality),
		"resolution":    strings.TrimSpace(variant.Resolution),
	}
	for _, value := range normalized {
		if value == "" {
			return nil, "", ErrImageOptionUnsupported
		}
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

func canonicalizeStoredVariant(raw json.RawMessage) (json.RawMessage, string, error) {
	var variant ImagePriceVariant
	if err := json.Unmarshal(raw, &variant); err != nil {
		return nil, "", ErrPriceUnavailable
	}
	return canonicalImageVariant(variant)
}

// BuildImageQuoteFingerprint 使用专用 HMAC 密钥绑定用户、Project、SK、模型、Prompt摘要、数量和规格。
func BuildImageQuoteFingerprint(secret []byte, input ImageQuoteFingerprintInput) (string, error) {
	if len(secret) < 32 || !lowerHex64.MatchString(input.PromptHash) || input.UserID == 0 || input.ProjectID == 0 || input.Count == 0 || strings.TrimSpace(input.LogicalModelCode) == "" {
		return "", ErrImageQuoteSecret
	}
	_, variantHash, err := canonicalImageVariant(input.Variant)
	if err != nil {
		return "", err
	}
	payload := struct {
		UserID           uint64 `json:"user_id"`
		ProjectID        uint64 `json:"project_id"`
		APIKeyID         uint64 `json:"api_key_id"`
		LogicalModelCode string `json:"logical_model_code"`
		PromptHash       string `json:"prompt_hash"`
		Count            uint64 `json:"count"`
		VariantHash      string `json:"variant_hash"`
	}{
		UserID: input.UserID, ProjectID: input.ProjectID, APIKeyID: input.APIKeyID,
		LogicalModelCode: strings.TrimSpace(input.LogicalModelCode), PromptHash: input.PromptHash,
		Count: input.Count, VariantHash: variantHash,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

type ImageQuoteFingerprintInput struct {
	UserID           uint64
	ProjectID        uint64
	APIKeyID         uint64
	LogicalModelCode string
	PromptHash       string
	Count            uint64
	Variant          ImagePriceVariant
}

type imageQuoteStore interface {
	Create(ctx context.Context, quote *model.AIGatewayQuote) error
	Consume(ctx context.Context, publicID string, userID, projectID uint64, apiKeyID *uint64, fingerprint, requestID string, now time.Time) (*model.AIGatewayQuote, bool, error)
}

type ImageQuoteService struct {
	pricing     *ImagePricingService
	store       imageQuoteStore
	fingerprint []byte
	now         func() time.Time
	newPublicID func() (string, error)
}

func NewImageQuoteService(pricing *ImagePricingService, store imageQuoteStore, fingerprintSecret []byte) *ImageQuoteService {
	secretCopy := append([]byte(nil), fingerprintSecret...)
	return &ImageQuoteService{pricing: pricing, store: store, fingerprint: secretCopy, now: time.Now, newPublicID: newImageQuotePublicID}
}

// CreateQuote 在本地价格事实内生成一次性报价；它不预占钱包，也不触发 Provider。
func (s *ImageQuoteService) CreateQuote(ctx context.Context, input ImageQuoteFingerprintInput) (*model.AIGatewayQuote, error) {
	if s == nil || s.pricing == nil || s.store == nil || len(s.fingerprint) < 32 {
		return nil, ErrPriceUnavailable
	}
	fingerprint, err := BuildImageQuoteFingerprint(s.fingerprint, input)
	if err != nil {
		return nil, err
	}
	priceQuote, err := s.pricing.QuoteImage(ctx, ImageQuoteCommand{LogicalModelCode: input.LogicalModelCode, Count: input.Count, Variant: input.Variant})
	if err != nil {
		return nil, err
	}
	publicID, err := s.newPublicID()
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	apiKeyID := optionalUint64(input.APIKeyID)
	quote := &model.AIGatewayQuote{
		PublicID: publicID, UserID: input.UserID, ProjectID: input.ProjectID, APIKeyID: apiKeyID,
		LogicalModelCode: strings.TrimSpace(input.LogicalModelCode), Capability: model.AIImageCapability,
		RequestFingerprint: fingerprint, RequestVariantHash: priceQuote.VariantHash,
		PriceVersionID: priceQuote.Snapshot.PriceVersionID, PriceSnapshotJSON: priceQuote.SnapshotJSON,
		QuotedAmount: priceQuote.QuotedAmount, Currency: "CNY", ExpiresAt: now.Add(imageQuoteTTL), CreatedAt: now,
	}
	if err := s.store.Create(ctx, quote); err != nil {
		return nil, err
	}
	return quote, nil
}

func (s *ImageQuoteService) ConsumeQuote(ctx context.Context, publicID string, input ImageQuoteFingerprintInput, requestID string) (*model.AIGatewayQuote, bool, error) {
	if s == nil || s.store == nil || len(s.fingerprint) < 32 || strings.TrimSpace(publicID) == "" || strings.TrimSpace(requestID) == "" {
		return nil, false, ErrImageQuoteConflict
	}
	fingerprint, err := BuildImageQuoteFingerprint(s.fingerprint, input)
	if err != nil {
		return nil, false, err
	}
	quote, idempotent, err := s.store.Consume(ctx, strings.TrimSpace(publicID), input.UserID, input.ProjectID, optionalUint64(input.APIKeyID), fingerprint, strings.TrimSpace(requestID), s.now().UTC())
	if err == nil {
		return quote, idempotent, nil
	}
	switch {
	case errors.Is(err, repository.ErrImageQuoteNotFound):
		return nil, false, ErrImageQuoteNotFound
	case errors.Is(err, repository.ErrImageQuoteConflict):
		return nil, false, ErrImageQuoteConflict
	case errors.Is(err, repository.ErrImageQuoteExpired):
		return nil, false, ErrImageQuoteExpired
	case errors.Is(err, repository.ErrImageQuoteConsumed):
		return nil, false, ErrImageQuoteConsumed
	default:
		return nil, false, err
	}
}

func optionalUint64(value uint64) *uint64 {
	if value == 0 {
		return nil
	}
	return &value
}

func newImageQuotePublicID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成图片报价编号失败: %w", err)
	}
	return "quote_" + hex.EncodeToString(buf), nil
}
