package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
)

type fakeActivePriceReader struct {
	version *model.AIPriceVersion
	skus    []model.AIPriceSKU
	err     error
}

func (f *fakeActivePriceReader) FindActiveVersion(context.Context, string, time.Time) (*model.AIPriceVersion, []model.AIPriceSKU, error) {
	return f.version, append([]model.AIPriceSKU(nil), f.skus...), f.err
}

func TestPricingGoldenQuoteAndSettlement(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	repo := validPriceFixture(now)
	pricing := NewPricingService(repo)
	pricing.now = func() time.Time { return now }
	quote, err := pricing.Quote(context.Background(), "qwen-plus", map[string]interface{}{"max_tokens": json.Number("100")})
	if err != nil {
		t.Fatalf("报价失败: %v", err)
	}
	if got, want := quote.HeldAmount.StringFixed(8), "0.01400000"; got != want {
		t.Fatalf("最坏成本不符: got=%s want=%s", got, want)
	}
	billed, err := pricing.CalculateFinal("req-golden", quote.SnapshotJSON, ExecutionUsage{
		PromptTokens: 100, CachedTokens: 20, CompletionTokens: 50, ReasoningTokens: 10, Present: true,
	})
	if err != nil {
		t.Fatalf("结算失败: %v", err)
	}
	if got, want := billed.FinalAmount.StringFixed(8), "0.00204000"; got != want {
		t.Fatalf("最终金额不符: got=%s want=%s", got, want)
	}
	if billed.FinalAmount.GreaterThan(quote.HeldAmount) {
		t.Fatal("最终金额不得超过预占金额")
	}
	assertMeterAmount(t, billed.Items, "input_tokens", "80", "0.00080000")
	assertMeterAmount(t, billed.Items, "cached_tokens", "20", "0.00004000")
	assertMeterAmount(t, billed.Items, "output_tokens", "40", "0.00080000")
	assertMeterAmount(t, billed.Items, "reasoning_tokens", "10", "0.00040000")
}

func TestPricingFailsClosedForInvalidCatalogAndRequest(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		mutate func(*fakeActivePriceReader)
		body   map[string]interface{}
		want   error
	}{
		{name: "max_tokens 为零", body: map[string]interface{}{"max_tokens": json.Number("0")}, want: ErrUnquotableRequest},
		{name: "max_tokens 超限", body: map[string]interface{}{"max_tokens": json.Number("101")}, want: ErrUnquotableRequest},
		{name: "多候选会放大最坏成本", body: map[string]interface{}{"max_tokens": json.Number("10"), "n": json.Number("2")}, want: ErrUnquotableRequest},
		{name: "字符串候选数不符合整数契约", body: map[string]interface{}{"max_tokens": json.Number("10"), "n": "1"}, want: ErrUnquotableRequest},
		{name: "小数候选数不符合整数契约", body: map[string]interface{}{"max_tokens": json.Number("10"), "n": json.Number("1.0")}, want: ErrUnquotableRequest},
		{name: "指数候选数不符合整数契约", body: map[string]interface{}{"max_tokens": json.Number("10"), "n": json.Number("1e0")}, want: ErrUnquotableRequest},
		{name: "成本过期", body: map[string]interface{}{"max_tokens": json.Number("10")}, want: ErrPriceExpired, mutate: func(f *fakeActivePriceReader) { f.version.CostExpiresAt = now.Add(-time.Second) }},
		{name: "毛利不足", body: map[string]interface{}{"max_tokens": json.Number("10")}, want: ErrMarginBelowMinimum, mutate: func(f *fakeActivePriceReader) { f.skus[0].CostUnitPrice = f.skus[0].SaleUnitPrice }},
		{name: "缺少推理 SKU", body: map[string]interface{}{"max_tokens": json.Number("10")}, want: ErrPriceUnavailable, mutate: func(f *fakeActivePriceReader) { f.skus = f.skus[:3] }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := validPriceFixture(now)
			if tc.mutate != nil {
				tc.mutate(repo)
			}
			pricing := NewPricingService(repo)
			pricing.now = func() time.Time { return now }
			_, err := pricing.Quote(context.Background(), "qwen-plus", tc.body)
			if !errors.Is(err, tc.want) {
				t.Fatalf("错误不符: got=%v want=%v", err, tc.want)
			}
		})
	}
}

func TestPricingUsesConfiguredFallbackWhenMaxTokensMissing(t *testing.T) {
	now := time.Now()
	repo := validPriceFixture(now)
	pricing := NewPricingService(repo, 64)
	pricing.now = func() time.Time { return now }
	quote, err := pricing.Quote(context.Background(), "qwen-plus", map[string]interface{}{"n": json.Number("1")})
	if err != nil || quote.MaxTokens != 64 {
		t.Fatalf("既有客户端缺少 max_tokens 时应使用配置兜底报价: quote=%+v err=%v", quote, err)
	}
}

func TestPricingSnapshotRemainsImmutableAfterCatalogChanges(t *testing.T) {
	now := time.Now()
	repo := validPriceFixture(now)
	pricing := NewPricingService(repo)
	pricing.now = func() time.Time { return now }
	quote, err := pricing.Quote(context.Background(), "qwen-plus", map[string]interface{}{"max_tokens": json.Number("100")})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟运营发布新价；既有请求必须继续使用原快照。
	repo.skus[0].SaleUnitPrice = decimal.NewFromInt(999)
	billed, err := pricing.CalculateFinal("req-snapshot", quote.SnapshotJSON, ExecutionUsage{PromptTokens: 100, CompletionTokens: 50, Present: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := billed.FinalAmount.StringFixed(8), "0.00200000"; got != want {
		t.Fatalf("价格快照被活动价格污染: got=%s want=%s", got, want)
	}
}

func TestPricingMinimumChargeAndRounding(t *testing.T) {
	now := time.Now()
	repo := validPriceFixture(now)
	for i := range repo.skus {
		repo.skus[i].CostUnitPrice = decimal.RequireFromString("0.00000001")
		repo.skus[i].SaleUnitPrice = decimal.RequireFromString("0.00000002")
	}
	pricing := NewPricingService(repo)
	pricing.now = func() time.Time { return now }
	quote, err := pricing.Quote(context.Background(), "qwen-plus", map[string]interface{}{"max_tokens": json.Number("1")})
	if err != nil {
		t.Fatal(err)
	}
	billed, err := pricing.CalculateFinal("req-min", quote.SnapshotJSON, ExecutionUsage{PromptTokens: 1, CompletionTokens: 0, Present: true})
	if err != nil {
		t.Fatal(err)
	}
	if billed.FinalAmount.String() != minimumSuccessfulCharge || quote.HeldAmount.LessThan(billed.FinalAmount) {
		t.Fatalf("最低收费或预占不正确: held=%s final=%s", quote.HeldAmount, billed.FinalAmount)
	}
}

func TestPricingDoesNotApplyMinimumToFailedUsage(t *testing.T) {
	now := time.Now()
	repo := validPriceFixture(now)
	pricing := NewPricingService(repo)
	pricing.now = func() time.Time { return now }
	quote, err := pricing.Quote(context.Background(), "qwen-plus", map[string]interface{}{"max_tokens": "1"})
	if err != nil {
		t.Fatal(err)
	}
	billed, err := pricing.CalculateFinalWithPolicy("req-failed-min", quote.SnapshotJSON, ExecutionUsage{Present: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !billed.FinalAmount.IsZero() {
		t.Fatalf("失败请求不得套用成功最低收费: %s", billed.FinalAmount)
	}
}

func TestPricingUint64BoundaryDoesNotUnderHold(t *testing.T) {
	now := time.Now()
	repo := validPriceFixture(now)
	repo.version.MaxInputTokens = math.MaxUint64
	repo.version.MaxOutputTokens = math.MaxUint64
	pricing := NewPricingService(repo)
	pricing.now = func() time.Time { return now }
	quote, err := pricing.Quote(context.Background(), "qwen-plus", map[string]interface{}{"max_tokens": uint64(math.MaxUint64)})
	if err != nil {
		t.Fatal(err)
	}
	if quote.HeldAmount.LessThan(decimal.RequireFromString("100000000000")) {
		t.Fatalf("uint64 边界报价疑似溢出: %s", quote.HeldAmount)
	}
}

func TestPricingMinimumChargeComesFromSnapshot(t *testing.T) {
	now := time.Now()
	repo := validPriceFixture(now)
	pricing := NewPricingService(repo)
	pricing.now = func() time.Time { return now }
	quote, err := pricing.Quote(context.Background(), "qwen-plus", map[string]interface{}{"max_tokens": "1"})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot PriceSnapshot
	if err := json.Unmarshal(quote.SnapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.MinimumCharge = "0.12345678"
	raw, _ := json.Marshal(snapshot)
	billed, err := pricing.CalculateFinal("req-min-snapshot", raw, ExecutionUsage{PromptTokens: 1, Present: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := billed.FinalAmount.StringFixed(8); got != "0.12345678" {
		t.Fatalf("结算未使用快照最低收费: %s", got)
	}
}

func validPriceFixture(now time.Time) *fakeActivePriceReader {
	version := &model.AIPriceVersion{
		ID: 8, LogicalModelCode: "qwen-plus", VersionNo: 3, Currency: "CNY",
		ExchangeRate: decimal.NewFromInt(1), Status: model.AIPriceActive,
		MinMarginRate: decimal.RequireFromString("0.20"), MaxInputTokens: 1000, MaxOutputTokens: 100,
		FailureChargePolicy: "confirmed_usage", RoundingMode: "ceil_8",
		CostUpdatedAt: now.Add(-time.Hour), CostExpiresAt: now.Add(time.Hour), EffectiveAt: now.Add(-time.Hour),
	}
	prices := map[string]string{
		"input_tokens": "10", "cached_tokens": "2", "output_tokens": "20", "reasoning_tokens": "40",
	}
	skus := make([]model.AIPriceSKU, 0, 4)
	for meter, price := range prices {
		sale := decimal.RequireFromString(price)
		skus = append(skus, model.AIPriceSKU{
			PriceVersionID: version.ID, MeterType: meter, VariantHash: meter + "-hash",
			CostUnitPrice: sale.Mul(decimal.RequireFromString("0.5")), SaleUnitPrice: sale,
			Scale: decimal.NewFromInt(1_000_000), Currency: "CNY",
		})
	}
	return &fakeActivePriceReader{version: version, skus: skus}
}

func assertMeterAmount(t *testing.T, items []model.AIUsageItem, meter, quantity, amount string) {
	t.Helper()
	for _, item := range items {
		if item.MeterType == meter {
			if item.Quantity.String() != quantity || item.Amount == nil || item.Amount.StringFixed(8) != amount {
				t.Fatalf("%s 计费不符: quantity=%s amount=%v", meter, item.Quantity, item.Amount)
			}
			return
		}
	}
	t.Fatalf("缺少计量项: %s", meter)
}
