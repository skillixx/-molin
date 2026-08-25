package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func TestImagePricingV2QuoteAndSettlementGolden(t *testing.T) {
	now := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	reader, variant := imagePriceFixture(t, now)
	pricing := NewImagePricingService(reader)
	pricing.now = func() time.Time { return now }
	quote, err := pricing.QuoteImage(context.Background(), ImageQuoteCommand{LogicalModelCode: "molin/image-test", Count: 1, Variant: variant})
	if err != nil {
		t.Fatal(err)
	}
	if got := quote.HeldAmount.StringFixed(8); got != "0.50000000" {
		t.Fatalf("图片报价金额错误: %s", got)
	}
	decoded, err := DecodePriceSnapshot(quote.SnapshotJSON)
	if err != nil || decoded.SchemaVersion != 2 || decoded.MetricV2 == nil || len(decoded.MetricV2.SelectedLines) != 1 {
		t.Fatalf("V2 快照解码失败: decoded=%+v err=%v", decoded, err)
	}
	settlement, err := pricing.CalculateImageFinal("image-request", quote.SnapshotJSON, 1)
	if err != nil {
		t.Fatal(err)
	}
	if settlement.SettledAmount.StringFixed(8) != "0.50000000" || settlement.ProviderCost.StringFixed(8) != "0.30000000" || !settlement.ReleaseAmount.IsZero() {
		t.Fatalf("图片结算金样错误: %+v", settlement)
	}
	if settlement.UsageFact.RecordKind != model.AIUsageFact || settlement.SaleLine.RecordKind != model.AIUsageSaleLine || settlement.CostLine.RecordKind != model.AIUsageCostLine {
		t.Fatalf("Usage 事实与计费行分类错误: %+v", settlement)
	}
}

func TestImagePricingPartialFailureAndReleaseGolden(t *testing.T) {
	now := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	reader, variant := imagePriceFixture(t, now)
	pricing := NewImagePricingService(reader)
	pricing.now = func() time.Time { return now }
	quote, err := pricing.QuoteImage(context.Background(), ImageQuoteCommand{LogicalModelCode: "molin/image-test", Count: 1, Variant: variant})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := quote.Snapshot
	snapshot.SelectedLines[0].QuotedUsage = "4"
	snapshot.QuotedAmount = "2.00000000"
	snapshot.HeldAmount = "2.00000000"
	raw, _ := json.Marshal(snapshot)

	partial, err := pricing.CalculateImageFinal("image-partial", raw, 3)
	if err != nil {
		t.Fatal(err)
	}
	if partial.SettledAmount.StringFixed(8) != "1.50000000" || partial.ProviderCost.StringFixed(8) != "0.90000000" || partial.ReleaseAmount.StringFixed(8) != "0.50000000" {
		t.Fatalf("部分成功金额金样错误: %+v", partial)
	}
	failed, err := pricing.CalculateImageFinal("image-failed", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !failed.SettledAmount.IsZero() || !failed.ProviderCost.IsZero() || failed.ReleaseAmount.StringFixed(8) != "2.00000000" {
		t.Fatalf("完全失败释放金样错误: %+v", failed)
	}
	if _, err := pricing.CalculateImageFinal("image-over", raw, 5); !errors.Is(err, ErrBillingAmountException) {
		t.Fatalf("实际张数超过报价必须失败关闭: %v", err)
	}
	refund, err := pricing.CalculateImageRefund(raw, 3, 1)
	if err != nil || refund.StringFixed(8) != "0.50000000" {
		t.Fatalf("部分退款金额金样错误: refund=%s err=%v", refund, err)
	}
	fullRefund, err := pricing.CalculateImageRefund(raw, 3, 3)
	if err != nil || fullRefund.StringFixed(8) != "1.50000000" {
		t.Fatalf("全额退款金额金样错误: refund=%s err=%v", fullRefund, err)
	}
	if _, err := pricing.CalculateImageRefund(raw, 3, 4); !errors.Is(err, ErrBillingAmountException) {
		t.Fatalf("超额退款必须失败关闭: %v", err)
	}
}

func TestImagePricingFailsClosedForInvalidPriceFacts(t *testing.T) {
	now := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		edit func(*fakeActivePriceReader, ImagePriceVariant)
		err  error
	}{
		{name: "成本过期", edit: func(r *fakeActivePriceReader, _ ImagePriceVariant) { r.version.CostExpiresAt = now }, err: ErrPriceExpired},
		{name: "缺少价格", edit: func(r *fakeActivePriceReader, _ ImagePriceVariant) { r.skus = nil }, err: ErrPriceUnavailable},
		{name: "销售价为零", edit: func(r *fakeActivePriceReader, _ ImagePriceVariant) { r.skus[0].SaleUnitPrice = decimal.Zero }, err: ErrPriceUnavailable},
		{name: "重复variant", edit: func(r *fakeActivePriceReader, _ ImagePriceVariant) { r.skus = append(r.skus, r.skus[0]) }, err: ErrPriceUnavailable},
		{name: "毛利不足", edit: func(r *fakeActivePriceReader, _ ImagePriceVariant) {
			r.skus[0].CostUnitPrice = decimal.RequireFromString("0.49")
		}, err: ErrMarginBelowMinimum},
		{name: "未知模板", edit: func(r *fakeActivePriceReader, _ ImagePriceVariant) { r.version.PricingTemplate = "token" }, err: ErrPriceUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader, variant := imagePriceFixture(t, now)
			tc.edit(reader, variant)
			pricing := NewImagePricingService(reader)
			pricing.now = func() time.Time { return now }
			_, err := pricing.QuoteImage(context.Background(), ImageQuoteCommand{LogicalModelCode: "molin/image-test", Count: 1, Variant: variant})
			if !errors.Is(err, tc.err) {
				t.Fatalf("got=%v want=%v", err, tc.err)
			}
		})
	}
}

func TestImagePricingRejectsUnsupportedVariantAndCount(t *testing.T) {
	now := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	reader, variant := imagePriceFixture(t, now)
	pricing := NewImagePricingService(reader)
	pricing.now = func() time.Time { return now }
	variant.AspectRatio = "16:9"
	if _, err := pricing.QuoteImage(context.Background(), ImageQuoteCommand{LogicalModelCode: "molin/image-test", Count: 1, Variant: variant}); !errors.Is(err, ErrImageOptionUnsupported) {
		t.Fatalf("未知规格必须失败关闭: %v", err)
	}
	_, allowed := imagePriceFixture(t, now)
	if _, err := pricing.QuoteImage(context.Background(), ImageQuoteCommand{LogicalModelCode: "molin/image-test", Count: 2, Variant: allowed}); !errors.Is(err, ErrImageOptionUnsupported) {
		t.Fatalf("超过允许张数必须失败关闭: %v", err)
	}
}

func TestDecodePriceSnapshotKeepsChatV1Compatibility(t *testing.T) {
	now := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	legacy := NewPricingService(validPriceFixture(now))
	legacy.now = func() time.Time { return now }
	quote, err := legacy.Quote(context.Background(), "qwen-plus", map[string]interface{}{"max_tokens": json.Number("10")})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePriceSnapshot(quote.SnapshotJSON)
	if err != nil || decoded.SchemaVersion != 1 || decoded.ChatV1 == nil || decoded.MetricV2 != nil {
		t.Fatalf("历史 Chat V1 快照兼容失败: decoded=%+v err=%v", decoded, err)
	}
	unknown := json.RawMessage(`{"schema_version":3,"selected_lines":[]}`)
	if _, err := DecodePriceSnapshot(unknown); !errors.Is(err, ErrPriceUnavailable) {
		t.Fatalf("未知快照版本必须失败关闭: %v", err)
	}
}

func TestDecodePriceSnapshotRejectsDuplicateSelectedLine(t *testing.T) {
	now := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	reader, variant := imagePriceFixture(t, now)
	pricing := NewImagePricingService(reader)
	pricing.now = func() time.Time { return now }
	quote, err := pricing.QuoteImage(context.Background(), ImageQuoteCommand{LogicalModelCode: "molin/image-test", Count: 1, Variant: variant})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := quote.Snapshot
	snapshot.SelectedLines = append(snapshot.SelectedLines, snapshot.SelectedLines[0])
	raw, _ := json.Marshal(snapshot)
	if _, err := DecodePriceSnapshot(raw); !errors.Is(err, ErrPriceUnavailable) {
		t.Fatalf("重复 selected_lines 必须失败关闭: %v", err)
	}
}

func TestDecodePriceSnapshotRejectsVariantAndAmountTampering(t *testing.T) {
	now := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	reader, variant := imagePriceFixture(t, now)
	pricing := NewImagePricingService(reader)
	pricing.now = func() time.Time { return now }
	quote, err := pricing.QuoteImage(context.Background(), ImageQuoteCommand{LogicalModelCode: "molin/image-test", Count: 1, Variant: variant})
	if err != nil {
		t.Fatal(err)
	}

	var variantTampered MetricPriceSnapshotV2
	if err := json.Unmarshal(quote.SnapshotJSON, &variantTampered); err != nil {
		t.Fatal(err)
	}
	variantTampered.SelectedLines[0].VariantJSON = json.RawMessage(`{"aspect_ratio":"16:9","delivery":"url","output_format":"provider_default","quality":"standard","resolution":"2K"}`)
	raw, _ := json.Marshal(variantTampered)
	if _, err := DecodePriceSnapshot(raw); !errors.Is(err, ErrPriceUnavailable) {
		t.Fatalf("variant_json 与 hash 不一致必须失败关闭: %v", err)
	}

	var amountTampered MetricPriceSnapshotV2
	if err := json.Unmarshal(quote.SnapshotJSON, &amountTampered); err != nil {
		t.Fatal(err)
	}
	amountTampered.HeldAmount = "9.99999999"
	raw, _ = json.Marshal(amountTampered)
	if _, err := DecodePriceSnapshot(raw); !errors.Is(err, ErrPriceUnavailable) {
		t.Fatalf("预占金额与选中行不一致必须失败关闭: %v", err)
	}
}

func TestDecodePriceSnapshotAcceptsMySQLJSONWhitespaceNormalization(t *testing.T) {
	now := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	reader, variant := imagePriceFixture(t, now)
	pricing := NewImagePricingService(reader)
	pricing.now = func() time.Time { return now }
	quote, err := pricing.QuoteImage(context.Background(), ImageQuoteCommand{LogicalModelCode: "molin/image-test", Count: 1, Variant: variant})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot MetricPriceSnapshotV2
	if err := json.Unmarshal(quote.SnapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, snapshot.SelectedLines[0].VariantJSON, "", "  "); err != nil {
		t.Fatal(err)
	}
	snapshot.SelectedLines[0].VariantJSON = pretty.Bytes()
	raw, _ := json.Marshal(snapshot)
	if _, err := DecodePriceSnapshot(raw); err != nil {
		t.Fatalf("MySQL JSON空白归一化不得破坏冻结快照: %v", err)
	}
}

func TestImageQuoteFingerprintIsStableAndBoundToPromptAndVariant(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	input := validFingerprintInput()
	left, err := BuildImageQuoteFingerprint(secret, input)
	if err != nil {
		t.Fatal(err)
	}
	right, err := BuildImageQuoteFingerprint(secret, input)
	if err != nil || left != right || !lowerHex64.MatchString(left) {
		t.Fatalf("HMAC 指纹必须稳定且为64位小写hex: %s %s %v", left, right, err)
	}
	input.PromptHash = stringsOf("b", 64)
	changed, _ := BuildImageQuoteFingerprint(secret, input)
	if changed == left {
		t.Fatal("Prompt摘要变化必须改变请求指纹")
	}
	if _, err := BuildImageQuoteFingerprint([]byte("short"), input); !errors.Is(err, ErrImageQuoteSecret) {
		t.Fatalf("弱指纹密钥必须失败关闭: %v", err)
	}
}

func TestImageQuoteServiceConcurrentConsumptionHasOneWinner(t *testing.T) {
	now := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	reader, _ := imagePriceFixture(t, now)
	store := &memoryImageQuoteStore{}
	pricing := NewImagePricingService(reader)
	pricing.now = func() time.Time { return now }
	svc := NewImageQuoteService(pricing, store, []byte("0123456789abcdef0123456789abcdef"))
	svc.now = func() time.Time { return now }
	svc.newPublicID = func() (string, error) { return "quote_concurrent", nil }
	input := validFingerprintInput()
	quote, err := svc.CreateQuote(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if quote.ExpiresAt.Sub(now) != imageQuoteTTL {
		t.Fatalf("Quote TTL 错误: %s", quote.ExpiresAt.Sub(now))
	}

	var winners atomic.Int64
	var consumed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, idempotent, consumeErr := svc.ConsumeQuote(context.Background(), quote.PublicID, input, fmt.Sprintf("req-%03d", index))
			switch {
			case consumeErr == nil && !idempotent:
				winners.Add(1)
			case errors.Is(consumeErr, ErrImageQuoteConsumed):
				consumed.Add(1)
			default:
				t.Errorf("并发消费返回异常: idempotent=%t err=%v", idempotent, consumeErr)
			}
		}(i)
	}
	wg.Wait()
	if winners.Load() != 1 || consumed.Load() != 99 {
		t.Fatalf("Quote并发消费必须只有一个胜者: winners=%d consumed=%d", winners.Load(), consumed.Load())
	}

	store.mu.Lock()
	winnerRequest := *store.quote.ConsumedRequestID
	store.mu.Unlock()
	_, idempotent, err := svc.ConsumeQuote(context.Background(), quote.PublicID, input, winnerRequest)
	if err != nil || !idempotent {
		t.Fatalf("相同请求重放必须返回原消费事实: idempotent=%t err=%v", idempotent, err)
	}
	svc.now = func() time.Time { return now.Add(10 * time.Minute) }
	_, idempotent, err = svc.ConsumeQuote(context.Background(), quote.PublicID, input, winnerRequest)
	if err != nil || !idempotent {
		t.Fatalf("已绑定相同请求的Quote过期后仍须幂等返回原事实: idempotent=%t err=%v", idempotent, err)
	}
}

func TestImageQuoteServiceRejectsExpiredAndFingerprintConflict(t *testing.T) {
	now := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	reader, _ := imagePriceFixture(t, now)
	pricing := NewImagePricingService(reader)
	pricing.now = func() time.Time { return now }
	input := validFingerprintInput()

	expiredStore := &memoryImageQuoteStore{}
	expiredService := NewImageQuoteService(pricing, expiredStore, []byte("0123456789abcdef0123456789abcdef"))
	expiredService.now = func() time.Time { return now }
	expiredService.newPublicID = func() (string, error) { return "quote_expired", nil }
	quote, err := expiredService.CreateQuote(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	expiredService.now = func() time.Time { return now.Add(6 * time.Minute) }
	if _, _, err := expiredService.ConsumeQuote(context.Background(), quote.PublicID, input, "req-expired"); !errors.Is(err, ErrImageQuoteExpired) {
		t.Fatalf("未消费Quote过期后必须拒绝: %v", err)
	}

	conflictStore := &memoryImageQuoteStore{}
	conflictService := NewImageQuoteService(pricing, conflictStore, []byte("0123456789abcdef0123456789abcdef"))
	conflictService.now = func() time.Time { return now }
	conflictService.newPublicID = func() (string, error) { return "quote_conflict", nil }
	quote, err = conflictService.CreateQuote(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	conflicting := input
	conflicting.PromptHash = stringsOf("d", 64)
	if _, _, err := conflictService.ConsumeQuote(context.Background(), quote.PublicID, conflicting, "req-conflict"); !errors.Is(err, ErrImageQuoteConflict) {
		t.Fatalf("Quote请求指纹变化必须拒绝: %v", err)
	}
}

func imagePriceFixture(t *testing.T, now time.Time) (*fakeActivePriceReader, ImagePriceVariant) {
	t.Helper()
	variant := ImagePriceVariant{Resolution: "2K", AspectRatio: "1:1", Quality: "standard", OutputFormat: "provider_default", Delivery: "url"}
	variantJSON, variantHash, err := canonicalImageVariant(variant)
	if err != nil {
		t.Fatal(err)
	}
	limits, _ := json.Marshal(imagePricingLimits{MaxCount: 1, Variants: []ImagePriceVariant{variant}})
	version := &model.AIPriceVersion{
		ID: 20, LogicalModelCode: "molin/image-test", Capability: model.AIImageCapability, PricingTemplate: "image_variant",
		VersionNo: 1, Currency: "CNY", ExchangeRate: decimal.NewFromInt(1), Status: model.AIPriceActive,
		MinMarginRate: decimal.RequireFromString("0.20"), LimitsJSON: limits, MinimumCharge: decimal.RequireFromString("0.01000000"),
		CostSource: "test_fixture", CostSourceVersion: "img-g2-v1", PricePurpose: "test_fixture",
		FailureChargePolicy: "confirmed_usage", RoundingMode: "ceil_8", CostUpdatedAt: now.Add(-time.Hour),
		CostExpiresAt: now.Add(time.Hour), EffectiveAt: now.Add(-time.Hour),
	}
	sku := model.AIPriceSKU{
		PriceVersionID: version.ID, MeterType: "image_count", VariantJSON: variantJSON, VariantHash: variantHash,
		CostUnitPrice: decimal.RequireFromString("0.30000000"), SaleUnitPrice: decimal.RequireFromString("0.50000000"),
		Scale: decimal.NewFromInt(1), Currency: "CNY",
	}
	return &fakeActivePriceReader{version: version, skus: []model.AIPriceSKU{sku}}, variant
}

func validFingerprintInput() ImageQuoteFingerprintInput {
	prompt := sha256.Sum256([]byte("仅用于测试的Prompt摘要输入"))
	return ImageQuoteFingerprintInput{
		UserID: 1, ProjectID: 2, APIKeyID: 3, LogicalModelCode: "molin/image-test",
		PromptHash: hex.EncodeToString(prompt[:]), Count: 1,
		Variant: ImagePriceVariant{Resolution: "2K", AspectRatio: "1:1", Quality: "standard", OutputFormat: "provider_default", Delivery: "url"},
	}
}

func stringsOf(value string, count int) string {
	result := ""
	for i := 0; i < count; i++ {
		result += value
	}
	return result
}

type memoryImageQuoteStore struct {
	mu    sync.Mutex
	quote *model.AIGatewayQuote
}

func (s *memoryImageQuoteStore) Create(_ context.Context, quote *model.AIGatewayQuote) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyQuote := *quote
	s.quote = &copyQuote
	quote.ID = 1
	s.quote.ID = 1
	return nil
}

func (s *memoryImageQuoteStore) Consume(_ context.Context, publicID string, userID, projectID uint64, apiKeyID *uint64, fingerprint, requestID string, now time.Time) (*model.AIGatewayQuote, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.quote == nil || s.quote.PublicID != publicID || s.quote.UserID != userID || s.quote.ProjectID != projectID || !sameOptionalUint64(s.quote.APIKeyID, apiKeyID) {
		return nil, false, repository.ErrImageQuoteNotFound
	}
	if s.quote.RequestFingerprint != fingerprint {
		return nil, false, repository.ErrImageQuoteConflict
	}
	if s.quote.ConsumedRequestID != nil {
		if *s.quote.ConsumedRequestID != requestID {
			return nil, false, repository.ErrImageQuoteConsumed
		}
		copyQuote := *s.quote
		return &copyQuote, true, nil
	}
	if !s.quote.ExpiresAt.After(now) {
		return nil, false, repository.ErrImageQuoteExpired
	}
	s.quote.ConsumedRequestID = &requestID
	s.quote.ConsumedAt = &now
	copyQuote := *s.quote
	return &copyQuote, false, nil
}

func sameOptionalUint64(left, right *uint64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
