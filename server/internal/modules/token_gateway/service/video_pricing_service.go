package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
)

const (
	// VideoMeterSeconds 是 VID-G2 唯一启用的第一版视频计量，禁止与像素秒叠加收费。
	VideoMeterSeconds = "video_seconds"
	// VideoPricePurposeNonCommercialFixture 明确隔离非商业测试价格，避免测试金额被误作正式售价。
	VideoPricePurposeNonCommercialFixture = "non_commercial_test_fixture"
	videoPriceSnapshotSchemaV3            = 3
)

var (
	ErrVideoOptionUnsupported = errors.New("视频规格不受支持")
	ErrVideoPriceUnavailable  = errors.New("视频价格不可用")
	ErrVideoPriceExpired      = errors.New("视频价格已过期")
	ErrVideoBillingAmount     = errors.New("视频计费金额异常")
)

// VideoPriceVariant 是文生和图生视频共用的完整定价维度；operation 永远参与哈希。
type VideoPriceVariant struct {
	Operation       string `json:"operation"`
	Resolution      string `json:"resolution"`
	DurationSeconds uint64 `json:"duration_seconds"`
	AspectRatio     string `json:"aspect_ratio"`
	FrameRate       uint32 `json:"frame_rate"`
	Audio           bool   `json:"audio"`
}

// VideoPricingLimits 冻结一个价格版本允许接单的完整规格矩阵。
type VideoPricingLimits struct {
	MeterType string              `json:"meter_type"`
	Variants  []VideoPriceVariant `json:"variants"`
}

// VideoQuoteCommand 只携带选价所需模型与规范化variant，不包含Prompt正文。
type VideoQuoteCommand struct {
	LogicalModelCode string
	Variant          VideoPriceVariant
}

// VideoPriceSnapshot 保存结算所需的不可变价格事实，后续调价不能改变已创建 Quote。
type VideoPriceSnapshot struct {
	SchemaVersion       int                       `json:"schema_version"`
	PriceVersionID      uint64                    `json:"price_version_id"`
	LogicalModelCode    string                    `json:"logical_model_code"`
	VersionNo           uint64                    `json:"version_no"`
	Capability          string                    `json:"capability"`
	Operation           string                    `json:"operation"`
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

// VideoPriceQuote 返回不可变快照、variant哈希和本次最大预占金额。
type VideoPriceQuote struct {
	Snapshot     VideoPriceSnapshot
	SnapshotJSON json.RawMessage
	VariantHash  string
	QuotedAmount decimal.Decimal
	HeldAmount   decimal.Decimal
}

// VideoSettlement 返回Usage、销售、成本和释放金额金样；真实写账属于后续阶段。
type VideoSettlement struct {
	UsageFact     model.AIUsageItem
	SaleLine      model.AIUsageItem
	CostLine      model.AIUsageItem
	SettledAmount decimal.Decimal
	ProviderCost  decimal.Decimal
	ReleaseAmount decimal.Decimal
}

// VideoPricingService 从唯一活动价格版本生成视频报价并仅按冻结快照计算金额。
type VideoPricingService struct {
	repo activePriceReader
	now  func() time.Time
}

// NewVideoPricingService 装配只读活动价格仓储。
func NewVideoPricingService(repo activePriceReader) *VideoPricingService {
	return &VideoPricingService{repo: repo, now: time.Now}
}

// QuoteVideo 在一个一致性价格版本内校验完整规格矩阵，并按请求时长形成一次不可漂移的人民币快照。
func (s *VideoPricingService) QuoteVideo(ctx context.Context, command VideoQuoteCommand) (*VideoPriceQuote, error) {
	if s == nil || s.repo == nil || strings.TrimSpace(command.LogicalModelCode) == "" {
		return nil, ErrVideoPriceUnavailable
	}
	requestedJSON, requestedHash, err := CanonicalVideoPriceVariant(command.Variant)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	version, skus, err := s.repo.FindActiveVersion(ctx, strings.TrimSpace(command.LogicalModelCode), now)
	if err != nil || version == nil {
		return nil, ErrVideoPriceUnavailable
	}
	if !version.CostExpiresAt.After(now) {
		return nil, ErrVideoPriceExpired
	}
	if version.Capability != model.AIVideoCapability || version.PricingTemplate != VideoMeterSeconds ||
		version.PricePurpose != VideoPricePurposeNonCommercialFixture || version.CostSource != VideoPricePurposeNonCommercialFixture ||
		version.Currency != "CNY" || !version.ExchangeRate.Equal(decimal.NewFromInt(1)) ||
		version.RoundingMode != "ceil_8" || version.FailureChargePolicy != "confirmed_usage" ||
		version.MinimumCharge.LessThanOrEqual(decimal.Zero) {
		return nil, ErrVideoPriceUnavailable
	}
	limits, err := decodeVideoPricingLimits(version.LimitsJSON)
	if err != nil || limits.MeterType != VideoMeterSeconds || len(limits.Variants) == 0 {
		return nil, ErrVideoPriceUnavailable
	}

	// 对整个冻结矩阵逐项核对，确保文生与图生每个允许规格都恰好存在一个独立价格项。
	allowedHashes := make(map[string]VideoPriceVariant, len(limits.Variants))
	allowedOperations := map[string]bool{
		model.AIVideoOperationTextToVideo:  false,
		model.AIVideoOperationImageToVideo: false,
	}
	for _, variant := range limits.Variants {
		_, hash, canonicalErr := CanonicalVideoPriceVariant(variant)
		if canonicalErr != nil {
			return nil, ErrVideoPriceUnavailable
		}
		if _, duplicate := allowedHashes[hash]; duplicate {
			return nil, ErrVideoPriceUnavailable
		}
		allowedHashes[hash] = variant
		allowedOperations[variant.Operation] = true
	}
	if !allowedOperations[model.AIVideoOperationTextToVideo] || !allowedOperations[model.AIVideoOperationImageToVideo] {
		return nil, ErrVideoPriceUnavailable
	}
	if _, allowed := allowedHashes[requestedHash]; !allowed {
		return nil, ErrVideoOptionUnsupported
	}
	priceByHash := make(map[string]*model.AIPriceSKU, len(skus))
	for i := range skus {
		sku := &skus[i]
		if sku.MeterType != VideoMeterSeconds || sku.Currency != "CNY" || sku.Scale.LessThanOrEqual(decimal.Zero) ||
			sku.SaleUnitPrice.LessThanOrEqual(decimal.Zero) || sku.CostUnitPrice.LessThan(decimal.Zero) {
			return nil, ErrVideoPriceUnavailable
		}
		storedJSON, storedHash, canonicalErr := canonicalStoredVideoVariant(sku.VariantJSON)
		if canonicalErr != nil || storedHash != sku.VariantHash {
			return nil, ErrVideoPriceUnavailable
		}
		_ = storedJSON
		if _, allowed := allowedHashes[storedHash]; !allowed {
			return nil, ErrVideoPriceUnavailable
		}
		if _, duplicate := priceByHash[storedHash]; duplicate {
			return nil, ErrVideoPriceUnavailable
		}
		margin := sku.SaleUnitPrice.Sub(sku.CostUnitPrice).Div(sku.SaleUnitPrice)
		if margin.LessThan(version.MinMarginRate) {
			return nil, ErrMarginBelowMinimum
		}
		priceByHash[storedHash] = sku
	}
	if len(priceByHash) != len(allowedHashes) {
		return nil, ErrVideoPriceUnavailable
	}
	selected := priceByHash[requestedHash]
	if selected == nil {
		return nil, ErrVideoPriceUnavailable
	}
	usage := decimal.NewFromInt(int64(command.Variant.DurationSeconds))
	quoted := usage.Mul(selected.SaleUnitPrice).Div(selected.Scale).RoundCeil(8)
	if quoted.LessThan(version.MinimumCharge) {
		quoted = version.MinimumCharge
	}
	line := MetricPriceLineSnapshot{
		MeterType: VideoMeterSeconds, VariantHash: requestedHash, VariantJSON: requestedJSON,
		UsageUnit: "seconds", UnitSize: selected.Scale.String(), QuotedUsage: usage.String(),
		CostUnitPrice: selected.CostUnitPrice.String(), SaleUnitPrice: selected.SaleUnitPrice.String(), Currency: "CNY",
	}
	snapshot := VideoPriceSnapshot{
		SchemaVersion: videoPriceSnapshotSchemaV3, PriceVersionID: version.ID, LogicalModelCode: version.LogicalModelCode,
		VersionNo: version.VersionNo, Capability: version.Capability, Operation: command.Variant.Operation,
		PricingTemplate: version.PricingTemplate, PricePurpose: version.PricePurpose, Currency: version.Currency,
		ExchangeRate: version.ExchangeRate.String(), RoundingMode: version.RoundingMode,
		FailureChargePolicy: version.FailureChargePolicy, MinimumCharge: version.MinimumCharge.String(),
		QuotedAmount: quoted.StringFixed(8), HeldAmount: quoted.StringFixed(8), SelectedLines: []MetricPriceLineSnapshot{line},
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	return &VideoPriceQuote{Snapshot: snapshot, SnapshotJSON: raw, VariantHash: requestedHash, QuotedAmount: quoted, HeldAmount: quoted}, nil
}

// CalculateVideoFinal 只读取 Quote 冻结快照；失败或零可交付时销售额为零并释放全部预占。
func (s *VideoPricingService) CalculateVideoFinal(requestID string, snapshotJSON json.RawMessage, actualSeconds decimal.Decimal) (*VideoSettlement, error) {
	snapshot, err := DecodeVideoPriceSnapshot(snapshotJSON)
	if err != nil || actualSeconds.IsNegative() {
		return nil, ErrVideoBillingAmount
	}
	line := snapshot.SelectedLines[0]
	quotedUsage, err := decimal.NewFromString(line.QuotedUsage)
	if err != nil || actualSeconds.GreaterThan(quotedUsage) {
		return nil, ErrVideoBillingAmount
	}
	unitSize, salePrice, costPrice, held, err := snapshotLineAmountsForVideo(snapshot, line)
	if err != nil {
		return nil, err
	}
	settled := actualSeconds.Mul(salePrice).Div(unitSize).RoundCeil(8)
	providerCost := actualSeconds.Mul(costPrice).Div(unitSize).RoundCeil(8)
	if actualSeconds.GreaterThan(decimal.Zero) {
		minimum, parseErr := decimal.NewFromString(snapshot.MinimumCharge)
		if parseErr != nil || minimum.LessThanOrEqual(decimal.Zero) {
			return nil, ErrVideoPriceUnavailable
		}
		if settled.LessThan(minimum) {
			settled = minimum
		}
	}
	if settled.GreaterThan(held) {
		return nil, ErrVideoBillingAmount
	}
	release := held.Sub(settled).Round(8)
	currency := "CNY"
	priceVersionID := snapshot.PriceVersionID
	operation := snapshot.Operation
	variantJSON := append(json.RawMessage(nil), line.VariantJSON...)
	usage := model.AIUsageItem{RequestID: requestID, MeterType: VideoMeterSeconds, Operation: &operation, Source: "gateway", RecordKind: model.AIUsageFact, VariantHash: line.VariantHash, VariantJSON: variantJSON, Quantity: actualSeconds, UsageUnit: "seconds", UnitSize: unitSize}
	sale := model.AIUsageItem{RequestID: requestID, MeterType: VideoMeterSeconds, Operation: &operation, Source: "gateway", RecordKind: model.AIUsageSaleLine, PriceVersionID: &priceVersionID, VariantHash: line.VariantHash, VariantJSON: variantJSON, Quantity: actualSeconds, UsageUnit: "seconds", UnitSize: unitSize, UnitPrice: &salePrice, Amount: &settled, Currency: &currency}
	cost := model.AIUsageItem{RequestID: requestID, MeterType: VideoMeterSeconds, Operation: &operation, Source: "provider_cost", RecordKind: model.AIUsageCostLine, PriceVersionID: &priceVersionID, VariantHash: line.VariantHash, VariantJSON: variantJSON, Quantity: actualSeconds, UsageUnit: "seconds", UnitSize: unitSize, UnitPrice: &costPrice, Amount: &providerCost, Currency: &currency}
	return &VideoSettlement{UsageFact: usage, SaleLine: sale, CostLine: cost, SettledAmount: settled, ProviderCost: providerCost, ReleaseAmount: release}, nil
}

// DecodeVideoPriceSnapshot 对快照金额、variant 和 operation 做独立重算，发现篡改即失败关闭。
func DecodeVideoPriceSnapshot(raw json.RawMessage) (*VideoPriceSnapshot, error) {
	var snapshot VideoPriceSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.SchemaVersion != videoPriceSnapshotSchemaV3 ||
		snapshot.PriceVersionID == 0 || snapshot.Capability != model.AIVideoCapability || snapshot.PricingTemplate != VideoMeterSeconds ||
		snapshot.PricePurpose != VideoPricePurposeNonCommercialFixture || snapshot.Currency != "CNY" || snapshot.ExchangeRate != "1" ||
		snapshot.RoundingMode != "ceil_8" || snapshot.FailureChargePolicy != "confirmed_usage" || len(snapshot.SelectedLines) != 1 {
		return nil, ErrVideoPriceUnavailable
	}
	line := snapshot.SelectedLines[0]
	_, hash, err := canonicalStoredVideoVariant(line.VariantJSON)
	if err != nil || hash != line.VariantHash || line.MeterType != VideoMeterSeconds || line.UsageUnit != "seconds" || line.Currency != "CNY" {
		return nil, ErrVideoPriceUnavailable
	}
	var variant VideoPriceVariant
	if err := json.Unmarshal(line.VariantJSON, &variant); err != nil || variant.Operation != snapshot.Operation {
		return nil, ErrVideoPriceUnavailable
	}
	unitSize, sale, _, held, err := snapshotLineAmountsForVideo(&snapshot, line)
	if err != nil {
		return nil, err
	}
	quotedUsage, err := decimal.NewFromString(line.QuotedUsage)
	if err != nil || quotedUsage.LessThanOrEqual(decimal.Zero) || !quotedUsage.Equal(decimal.NewFromInt(int64(variant.DurationSeconds))) {
		return nil, ErrVideoPriceUnavailable
	}
	quoted := quotedUsage.Mul(sale).Div(unitSize).RoundCeil(8)
	minimum, err := decimal.NewFromString(snapshot.MinimumCharge)
	if err != nil || minimum.LessThanOrEqual(decimal.Zero) {
		return nil, ErrVideoPriceUnavailable
	}
	if quoted.LessThan(minimum) {
		quoted = minimum
	}
	storedQuoted, err := decimal.NewFromString(snapshot.QuotedAmount)
	if err != nil || !storedQuoted.Equal(quoted) || !held.Equal(storedQuoted) {
		return nil, ErrVideoPriceUnavailable
	}
	return &snapshot, nil
}

func snapshotLineAmountsForVideo(snapshot *VideoPriceSnapshot, line MetricPriceLineSnapshot) (decimal.Decimal, decimal.Decimal, decimal.Decimal, decimal.Decimal, error) {
	unitSize, err := decimal.NewFromString(line.UnitSize)
	if err != nil || unitSize.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, ErrVideoPriceUnavailable
	}
	sale, err := decimal.NewFromString(line.SaleUnitPrice)
	if err != nil || sale.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, ErrVideoPriceUnavailable
	}
	cost, err := decimal.NewFromString(line.CostUnitPrice)
	if err != nil || cost.LessThan(decimal.Zero) {
		return decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, ErrVideoPriceUnavailable
	}
	held, err := decimal.NewFromString(snapshot.HeldAmount)
	if err != nil || held.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, ErrVideoPriceUnavailable
	}
	return unitSize, sale, cost, held, nil
}

// CanonicalVideoPriceVariant 输出固定键序的 JSON 与 SHA-256，避免接口门面或 JSON 空白造成两套价格。
func CanonicalVideoPriceVariant(variant VideoPriceVariant) (json.RawMessage, string, error) {
	variant.Operation = strings.TrimSpace(variant.Operation)
	variant.Resolution = strings.TrimSpace(variant.Resolution)
	variant.AspectRatio = strings.TrimSpace(variant.AspectRatio)
	if (variant.Operation != model.AIVideoOperationTextToVideo && variant.Operation != model.AIVideoOperationImageToVideo) ||
		variant.Resolution == "" || variant.DurationSeconds == 0 || variant.AspectRatio == "" || variant.FrameRate == 0 {
		return nil, "", ErrVideoOptionUnsupported
	}
	raw, err := json.Marshal(variant)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

func canonicalStoredVideoVariant(raw json.RawMessage) (json.RawMessage, string, error) {
	variant, err := decodeStrictVideoVariant(raw)
	if err != nil {
		return nil, "", ErrVideoPriceUnavailable
	}
	canonical, hash, err := CanonicalVideoPriceVariant(*variant)
	if err != nil {
		return nil, "", ErrVideoPriceUnavailable
	}
	if len(hash) != 64 {
		return nil, "", ErrVideoPriceUnavailable
	}
	return canonical, hash, nil
}

func decodeVideoPricingLimits(raw json.RawMessage) (*VideoPricingLimits, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope) != 2 || envelope["meter_type"] == nil || envelope["variants"] == nil {
		return nil, ErrVideoPriceUnavailable
	}
	var meterType string
	var variantRaw []json.RawMessage
	if err := json.Unmarshal(envelope["meter_type"], &meterType); err != nil || strings.TrimSpace(meterType) == "" {
		return nil, ErrVideoPriceUnavailable
	}
	if err := json.Unmarshal(envelope["variants"], &variantRaw); err != nil || len(variantRaw) == 0 {
		return nil, ErrVideoPriceUnavailable
	}
	limits := &VideoPricingLimits{MeterType: meterType, Variants: make([]VideoPriceVariant, 0, len(variantRaw))}
	for _, rawVariant := range variantRaw {
		variant, err := decodeStrictVideoVariant(rawVariant)
		if err != nil {
			return nil, err
		}
		limits.Variants = append(limits.Variants, *variant)
	}
	return limits, nil
}

func decodeStrictVideoVariant(raw json.RawMessage) (*VideoPriceVariant, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 6 {
		return nil, ErrVideoPriceUnavailable
	}
	for _, key := range []string{"operation", "resolution", "duration_seconds", "aspect_ratio", "frame_rate", "audio"} {
		if _, ok := fields[key]; !ok {
			return nil, ErrVideoPriceUnavailable
		}
	}
	var variant VideoPriceVariant
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&variant); err != nil {
		return nil, ErrVideoPriceUnavailable
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrVideoPriceUnavailable
	}
	return &variant, nil
}
