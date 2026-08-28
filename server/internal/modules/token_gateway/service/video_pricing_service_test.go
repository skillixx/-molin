package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func TestVideoPricingQuoteAndSettlementGolden(t *testing.T) {
	now := time.Date(2026, 8, 28, 4, 0, 0, 0, time.UTC)
	reader, variant := videoPriceFixture(t, now)
	pricing := NewVideoPricingService(reader)
	pricing.now = func() time.Time { return now }

	quote, err := pricing.QuoteVideo(context.Background(), VideoQuoteCommand{LogicalModelCode: "molin/runway-gen4.5", Variant: variant})
	if err != nil {
		t.Fatal(err)
	}
	if got := quote.QuotedAmount.StringFixed(8); got != "0.50000000" {
		t.Fatalf("5秒视频报价金额错误: %s", got)
	}
	settlement, err := pricing.CalculateVideoFinal("vid-request", quote.SnapshotJSON, decimal.RequireFromString("3.250"))
	if err != nil {
		t.Fatal(err)
	}
	if settlement.SettledAmount.StringFixed(8) != "0.32500000" || settlement.ReleaseAmount.StringFixed(8) != "0.17500000" {
		t.Fatalf("视频结算或释放金额金样错误: %+v", settlement)
	}
}

func TestVideoQuoteFingerprintBindsOperationAndInputSnapshot(t *testing.T) {
	secret := []byte("vid-g2-quote-fingerprint-secret-32bytes")
	prompt := sha256.Sum256([]byte("固定提示词摘要"))
	base := VideoQuoteFingerprintInput{
		UserID: 7, ProjectID: 9, LogicalModelCode: "molin/runway-gen4.5", PromptHash: fmt.Sprintf("%x", prompt),
		Variant: VideoPriceVariant{Operation: model.AIVideoOperationImageToVideo, Resolution: "1280x720", DurationSeconds: 5, AspectRatio: "16:9", FrameRate: 24},
		Input:   &VideoQuoteInputBinding{InputAssetID: "vin_asset_1", NormalizedSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Version: 1},
	}
	first, err := BuildVideoQuoteFingerprint(secret, base)
	if err != nil {
		t.Fatal(err)
	}
	changed := base
	changed.Input = &VideoQuoteInputBinding{InputAssetID: "vin_asset_1", NormalizedSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Version: 1}
	second, err := BuildVideoQuoteFingerprint(secret, changed)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("替换输入图片后请求指纹不得保持不变")
	}
	text := base
	text.Variant.Operation = model.AIVideoOperationTextToVideo
	text.Input = nil
	if _, err := BuildVideoQuoteFingerprint(secret, text); err != nil {
		t.Fatalf("文生视频零输入应通过: %v", err)
	}
	text.Input = base.Input
	if _, err := BuildVideoQuoteFingerprint(secret, text); !errors.Is(err, ErrVideoInputMismatch) {
		t.Fatalf("文生视频携带输入必须失败关闭: %v", err)
	}
}

func TestVideoQuoteOneHundredConcurrentConsumeHasOneWinner(t *testing.T) {
	now := time.Date(2026, 8, 28, 4, 0, 0, 0, time.UTC)
	reader, variant := videoPriceFixture(t, now)
	store := newFakeVideoQuoteStore()
	pricing := NewVideoPricingService(reader)
	pricing.now = func() time.Time { return now }
	quotes := NewVideoQuoteService(pricing, store, []byte("vid-g2-quote-fingerprint-secret-32bytes"))
	quotes.now = func() time.Time { return now }
	quotes.newPublicID = func() (string, error) { return "vid_quote_fixed", nil }
	input := VideoQuoteFingerprintInput{UserID: 7, ProjectID: 9, LogicalModelCode: "molin/runway-gen4.5", PromptHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Variant: variant}
	quote, existing, err := quotes.CreateQuote(context.Background(), VideoCreateQuoteCommand{CommandKind: VideoQuoteCommandKindExplicit, IdempotencyKey: "quote-key", FingerprintInput: input})
	if err != nil || existing {
		t.Fatalf("首次创建报价失败: existing=%v err=%v", existing, err)
	}

	var winners atomic.Int32
	var consumed atomic.Int32
	var group sync.WaitGroup
	for index := 0; index < 100; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, replay, consumeErr := quotes.ConsumeQuote(context.Background(), quote.PublicID, input, fmt.Sprintf("vid_req_%03d", index))
			if consumeErr == nil {
				consumed.Add(1)
				if !replay {
					winners.Add(1)
				}
			}
		}(index)
	}
	group.Wait()
	if winners.Load() != 1 || consumed.Load() != 1 {
		t.Fatalf("100并发消费必须只有一个成功赢家: winners=%d consumed=%d", winners.Load(), consumed.Load())
	}
}

func TestVideoQuoteFacadesShareSnapshotHoldAndTaskReservation(t *testing.T) {
	now := time.Date(2026, 8, 28, 4, 0, 0, 0, time.UTC)
	reader, variant := videoPriceFixture(t, now)
	store := newFakeVideoQuoteStore()
	pricing := NewVideoPricingService(reader)
	pricing.now = func() time.Time { return now }
	quoteService := NewVideoQuoteService(pricing, store, []byte("vid-g2-quote-fingerprint-secret-32bytes"))
	quoteService.now = func() time.Time { return now }
	var id atomic.Int32
	quoteService.newPublicID = func() (string, error) { return fmt.Sprintf("vid_quote_%d", id.Add(1)), nil }
	reservation := &fakeVideoReservation{quotes: quoteService}
	facade := NewVideoQuoteFacade(quoteService, reservation)
	request := VideoFacadeRequest{
		IdempotencyKey: "same-intent", RequestID: "vid_req_explicit", TaskID: "vid_task_explicit",
		FingerprintInput: VideoQuoteFingerprintInput{UserID: 7, ProjectID: 9, LogicalModelCode: "molin/runway-gen4.5", PromptHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Variant: variant},
	}
	explicit, err := facade.CreateTokenQuote(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	preparedExplicit, err := facade.GenerateWithTokenQuote(context.Background(), request, explicit.Quote.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	autoRequest := request
	autoRequest.IdempotencyKey = "auto-intent"
	autoRequest.RequestID = "vid_req_auto"
	autoRequest.TaskID = "vid_task_auto"
	preparedAuto, err := facade.CreateOpenAIVideo(context.Background(), autoRequest)
	if err != nil {
		t.Fatal(err)
	}
	if preparedExplicit.Quote.PriceVersionID != preparedAuto.Quote.PriceVersionID ||
		string(preparedExplicit.Quote.PriceSnapshotJSON) != string(preparedAuto.Quote.PriceSnapshotJSON) ||
		!preparedExplicit.HeldAmount.Equal(preparedAuto.HeldAmount) {
		t.Fatalf("两个门面必须共享价格版本、快照和Hold: explicit=%+v auto=%+v", preparedExplicit, preparedAuto)
	}
	explicitSettlement, err := quoteServiceSettlement(t, NewVideoPricingService(reader), preparedExplicit.Quote.PriceSnapshotJSON)
	if err != nil {
		t.Fatal(err)
	}
	autoSettlement, err := quoteServiceSettlement(t, NewVideoPricingService(reader), preparedAuto.Quote.PriceSnapshotJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !explicitSettlement.SettledAmount.Equal(autoSettlement.SettledAmount) || !explicitSettlement.ReleaseAmount.Equal(autoSettlement.ReleaseAmount) {
		t.Fatalf("两个门面最终结算与释放金样必须一致: explicit=%+v auto=%+v", explicitSettlement, autoSettlement)
	}
	if reservation.calls.Load() != 2 {
		t.Fatalf("显式生成和自动Quote都必须恰好进入一次原子预占/任务入口: %d", reservation.calls.Load())
	}
}

func quoteServiceSettlement(t *testing.T, pricing *VideoPricingService, snapshot json.RawMessage) (*VideoSettlement, error) {
	t.Helper()
	return pricing.CalculateVideoFinal("vid-facade-parity", snapshot, decimal.RequireFromString("3.250"))
}

func TestVideoPricingRequiresIndependentPriceForEveryOperation(t *testing.T) {
	now := time.Date(2026, 8, 28, 4, 0, 0, 0, time.UTC)
	reader, textVariant := videoPriceFixture(t, now)
	pricing := NewVideoPricingService(reader)
	pricing.now = func() time.Time { return now }
	textQuote, err := pricing.QuoteVideo(context.Background(), VideoQuoteCommand{LogicalModelCode: "molin/runway-gen4.5", Variant: textVariant})
	if err != nil {
		t.Fatal(err)
	}
	imageVariant := textVariant
	imageVariant.Operation = model.AIVideoOperationImageToVideo
	imageQuote, err := pricing.QuoteVideo(context.Background(), VideoQuoteCommand{LogicalModelCode: "molin/runway-gen4.5", Variant: imageVariant})
	if err != nil {
		t.Fatal(err)
	}
	if textQuote.VariantHash == imageQuote.VariantHash {
		t.Fatal("文生和图生即使单价相同也必须形成两个独立variant价格项")
	}

	reader.skus = reader.skus[:1]
	if _, err := pricing.QuoteVideo(context.Background(), VideoQuoteCommand{LogicalModelCode: "molin/runway-gen4.5", Variant: textVariant}); !errors.Is(err, ErrVideoPriceUnavailable) {
		t.Fatalf("缺少图生价格时整个冻结矩阵必须失败关闭: %v", err)
	}
}

func TestVideoPricingFailsClosedForInvalidFactsAndUnsupportedSpecs(t *testing.T) {
	now := time.Date(2026, 8, 28, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		edit func(*fakeActivePriceReader, *VideoPriceVariant)
		want error
	}{
		{name: "成本过期", edit: func(r *fakeActivePriceReader, _ *VideoPriceVariant) { r.version.CostExpiresAt = now }, want: ErrVideoPriceExpired},
		{name: "销售价为零", edit: func(r *fakeActivePriceReader, _ *VideoPriceVariant) { r.skus[0].SaleUnitPrice = decimal.Zero }, want: ErrVideoPriceUnavailable},
		{name: "币种不一致", edit: func(r *fakeActivePriceReader, _ *VideoPriceVariant) { r.skus[0].Currency = "USD" }, want: ErrVideoPriceUnavailable},
		{name: "正式售价未批准", edit: func(r *fakeActivePriceReader, _ *VideoPriceVariant) { r.version.PricePurpose = "commercial" }, want: ErrVideoPriceUnavailable},
		{name: "错误启用像素秒", edit: func(r *fakeActivePriceReader, _ *VideoPriceVariant) {
			r.version.PricingTemplate = "video_megapixel_seconds"
		}, want: ErrVideoPriceUnavailable},
		{name: "重复价格项", edit: func(r *fakeActivePriceReader, _ *VideoPriceVariant) { r.skus = append(r.skus, r.skus[0]) }, want: ErrVideoPriceUnavailable},
		{name: "矩阵缺少图生", edit: func(r *fakeActivePriceReader, _ *VideoPriceVariant) {
			var limits VideoPricingLimits
			_ = json.Unmarshal(r.version.LimitsJSON, &limits)
			limits.Variants = limits.Variants[:1]
			r.version.LimitsJSON, _ = json.Marshal(limits)
			r.skus = r.skus[:1]
		}, want: ErrVideoPriceUnavailable},
		{name: "禁止规格", edit: func(_ *fakeActivePriceReader, v *VideoPriceVariant) { v.DurationSeconds = 6 }, want: ErrVideoOptionUnsupported},
		{name: "禁止分辨率", edit: func(_ *fakeActivePriceReader, v *VideoPriceVariant) { v.Resolution = "1920x1080" }, want: ErrVideoOptionUnsupported},
		{name: "禁止比例", edit: func(_ *fakeActivePriceReader, v *VideoPriceVariant) { v.AspectRatio = "9:16" }, want: ErrVideoOptionUnsupported},
		{name: "禁止帧率", edit: func(_ *fakeActivePriceReader, v *VideoPriceVariant) { v.FrameRate = 30 }, want: ErrVideoOptionUnsupported},
		{name: "禁止音频", edit: func(_ *fakeActivePriceReader, v *VideoPriceVariant) { v.Audio = true }, want: ErrVideoOptionUnsupported},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader, variant := videoPriceFixture(t, now)
			tc.edit(reader, &variant)
			pricing := NewVideoPricingService(reader)
			pricing.now = func() time.Time { return now }
			_, err := pricing.QuoteVideo(context.Background(), VideoQuoteCommand{LogicalModelCode: "molin/runway-gen4.5", Variant: variant})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got=%v want=%v", err, tc.want)
			}
		})
	}
}

func TestVideoPricingDecimalRoundingMinimumAndImmutableSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 28, 4, 0, 0, 0, time.UTC)
	reader, variant := videoPriceFixture(t, now)
	for index := range reader.skus {
		reader.skus[index].SaleUnitPrice = decimal.RequireFromString("0.02000000")
		reader.skus[index].CostUnitPrice = decimal.Zero
		reader.skus[index].Scale = decimal.NewFromInt(3)
	}
	reader.version.MinimumCharge = decimal.RequireFromString("0.01000000")
	pricing := NewVideoPricingService(reader)
	pricing.now = func() time.Time { return now }
	quote, err := pricing.QuoteVideo(context.Background(), VideoQuoteCommand{LogicalModelCode: "molin/runway-gen4.5", Variant: variant})
	if err != nil {
		t.Fatal(err)
	}
	if quote.QuotedAmount.StringFixed(8) != "0.03333334" {
		t.Fatalf("Decimal ceil_8金样错误: %s", quote.QuotedAmount.StringFixed(8))
	}
	// 报价后修改活动价格，结算仍必须只读取旧快照。
	reader.skus[0].SaleUnitPrice = decimal.RequireFromString("99")
	settlement, err := pricing.CalculateVideoFinal("vid-immutable", quote.SnapshotJSON, decimal.Zero)
	if err != nil {
		t.Fatal(err)
	}
	if !settlement.SettledAmount.IsZero() || settlement.ReleaseAmount.StringFixed(8) != "0.03333334" {
		t.Fatalf("失败释放或不可变快照金样错误: %+v", settlement)
	}
}

func TestVideoPricingPositiveMinimumChargeAndSnapshotTampering(t *testing.T) {
	now := time.Date(2026, 8, 28, 4, 0, 0, 0, time.UTC)
	reader, variant := videoPriceFixture(t, now)
	reader.version.MinimumCharge = decimal.RequireFromString("0.05000000")
	for index := range reader.skus {
		reader.skus[index].SaleUnitPrice = decimal.RequireFromString("0.02000000")
		reader.skus[index].CostUnitPrice = decimal.RequireFromString("0.01000000")
	}
	pricing := NewVideoPricingService(reader)
	pricing.now = func() time.Time { return now }
	quote, err := pricing.QuoteVideo(context.Background(), VideoQuoteCommand{LogicalModelCode: "molin/runway-gen4.5", Variant: variant})
	if err != nil {
		t.Fatal(err)
	}
	settlement, err := pricing.CalculateVideoFinal("vid-minimum", quote.SnapshotJSON, decimal.NewFromInt(1))
	if err != nil {
		t.Fatal(err)
	}
	if settlement.SettledAmount.StringFixed(8) != "0.05000000" || settlement.ReleaseAmount.StringFixed(8) != "0.05000000" {
		t.Fatalf("正用量最低收费和释放金样错误: %+v", settlement)
	}
	for _, mutate := range []func(*VideoPriceSnapshot){
		func(snapshot *VideoPriceSnapshot) { snapshot.QuotedAmount = "9.00000000" },
		func(snapshot *VideoPriceSnapshot) { snapshot.HeldAmount = "9.00000000" },
		func(snapshot *VideoPriceSnapshot) { snapshot.SelectedLines[0].VariantHash = strings.Repeat("f", 64) },
		func(snapshot *VideoPriceSnapshot) { snapshot.SelectedLines[0].SaleUnitPrice = "9.00000000" },
	} {
		snapshot := quote.Snapshot
		snapshot.SelectedLines = append([]MetricPriceLineSnapshot(nil), quote.Snapshot.SelectedLines...)
		mutate(&snapshot)
		raw, _ := json.Marshal(snapshot)
		if _, err := DecodeVideoPriceSnapshot(raw); !errors.Is(err, ErrVideoPriceUnavailable) {
			t.Fatalf("价格快照篡改必须失败关闭: %v", err)
		}
	}
}

func TestVideoQuoteIdempotencyExpiryAndImageReplacement(t *testing.T) {
	now := time.Date(2026, 8, 28, 4, 0, 0, 0, time.UTC)
	reader, variant := videoPriceFixture(t, now)
	variant.Operation = model.AIVideoOperationImageToVideo
	store := newFakeVideoQuoteStore()
	resolver := &fakeVideoInputResolver{items: map[string]VideoQuoteInputBinding{
		"vin_asset_1": {InputAssetID: "vin_asset_1", NormalizedSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Version: 1},
		"vin_asset_2": {InputAssetID: "vin_asset_2", NormalizedSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Version: 2},
	}}
	pricing := NewVideoPricingService(reader)
	pricing.now = func() time.Time { return now }
	quotes := NewVideoQuoteService(pricing, store, []byte("vid-g2-quote-fingerprint-secret-32bytes")).WithInputSnapshotResolver(resolver)
	quotes.now = func() time.Time { return now }
	var sequence atomic.Int32
	quotes.newPublicID = func() (string, error) { return fmt.Sprintf("vid_quote_%d", sequence.Add(1)), nil }
	input := VideoQuoteFingerprintInput{UserID: 7, ProjectID: 9, LogicalModelCode: "molin/runway-gen4.5", PromptHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Variant: variant, Input: &VideoQuoteInputBinding{InputAssetID: "vin_asset_1", NormalizedSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Version: 1}}
	command := VideoCreateQuoteCommand{CommandKind: VideoQuoteCommandKindExplicit, IdempotencyKey: "same-key", FingerprintInput: input}
	quote, existing, err := quotes.CreateQuote(context.Background(), command)
	if err != nil || existing {
		t.Fatalf("首次Quote失败: existing=%v err=%v", existing, err)
	}
	reader.version.CostExpiresAt = now
	replayed, existing, err := quotes.CreateQuote(context.Background(), command)
	if err != nil || !existing || replayed.PublicID != quote.PublicID {
		t.Fatalf("同键同指纹必须返回原Quote: quote=%+v existing=%v err=%v", replayed, existing, err)
	}
	changed := command
	changed.FingerprintInput.Input = &VideoQuoteInputBinding{InputAssetID: "vin_asset_2", NormalizedSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Version: 2}
	if _, _, err := quotes.CreateQuote(context.Background(), changed); !errors.Is(err, ErrVideoQuoteConflict) {
		t.Fatalf("同幂等键不同图片必须稳定冲突: %v", err)
	}
	resolver.items["vin_asset_1"] = VideoQuoteInputBinding{InputAssetID: "vin_asset_1", NormalizedSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", Version: 2}
	if _, _, err := quotes.ConsumeQuote(context.Background(), quote.PublicID, input, "vid_req_replace"); !errors.Is(err, ErrVideoInputMismatch) {
		t.Fatalf("Quote后可信图片快照变化不得消费旧Quote: %v", err)
	}
	resolver.items["vin_asset_1"] = *input.Input
	quotes.now = func() time.Time { return now.Add(videoQuoteTTL) }
	if _, _, err := quotes.ConsumeQuote(context.Background(), quote.PublicID, input, "vid_req_expired"); !errors.Is(err, ErrVideoQuoteExpired) {
		t.Fatalf("到期Quote必须失败关闭: %v", err)
	}
}

func TestVideoQuoteRejectsCrossAPIKeyConsumptionAndUnknownVariantDimension(t *testing.T) {
	now := time.Date(2026, 8, 28, 4, 0, 0, 0, time.UTC)
	reader, variant := videoPriceFixture(t, now)
	pricing := NewVideoPricingService(reader)
	pricing.now = func() time.Time { return now }
	store := newFakeVideoQuoteStore()
	quotes := NewVideoQuoteService(pricing, store, []byte("vid-g2-quote-fingerprint-secret-32bytes"))
	quotes.now = func() time.Time { return now }
	quotes.newPublicID = func() (string, error) { return "vid_quote_key_scope", nil }
	input := VideoQuoteFingerprintInput{UserID: 7, ProjectID: 9, APIKeyID: 11, LogicalModelCode: "molin/runway-gen4.5", PromptHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Variant: variant}
	quote, _, err := quotes.CreateQuote(context.Background(), VideoCreateQuoteCommand{CommandKind: VideoQuoteCommandKindExplicit, IdempotencyKey: "key-scope", FingerprintInput: input})
	if err != nil {
		t.Fatal(err)
	}
	otherKey := input
	otherKey.APIKeyID = 12
	if _, _, err := quotes.ConsumeQuote(context.Background(), quote.PublicID, otherKey, "vid_req_cross_key"); err == nil {
		t.Fatal("同一用户Project下另一Project SK不得消费原Quote")
	}

	reader, variant = videoPriceFixture(t, now)
	tampered := append([]byte(nil), reader.skus[0].VariantJSON...)
	tampered = []byte(strings.TrimSuffix(string(tampered), "}") + `,"provider_quality":"turbo"}`)
	reader.skus[0].VariantJSON = tampered
	pricing = NewVideoPricingService(reader)
	pricing.now = func() time.Time { return now }
	if _, err := pricing.QuoteVideo(context.Background(), VideoQuoteCommand{LogicalModelCode: "molin/runway-gen4.5", Variant: variant}); !errors.Is(err, ErrVideoPriceUnavailable) {
		t.Fatalf("未冻结的新variant维度不得静默忽略: %v", err)
	}
	reader, variant = videoPriceFixture(t, now)
	missingAudio := strings.Replace(string(reader.skus[0].VariantJSON), `,"audio":false`, "", 1)
	if missingAudio == string(reader.skus[0].VariantJSON) {
		missingAudio = strings.Replace(string(reader.skus[0].VariantJSON), `"audio":false,`, "", 1)
	}
	reader.skus[0].VariantJSON = json.RawMessage(missingAudio)
	pricing = NewVideoPricingService(reader)
	pricing.now = func() time.Time { return now }
	if _, err := pricing.QuoteVideo(context.Background(), VideoQuoteCommand{LogicalModelCode: "molin/runway-gen4.5", Variant: variant}); !errors.Is(err, ErrVideoPriceUnavailable) {
		t.Fatalf("variant缺少显式audio维度不得按false接受: %v", err)
	}
}

func TestVideoProjectMonthStartUsesProjectTimezone(t *testing.T) {
	now := time.Date(2026, 8, 31, 16, 30, 0, 0, time.UTC)
	start, err := projectMonthStartUTC("Asia/Shanghai", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC)
	if !start.Equal(want) {
		t.Fatalf("上海项目月界转换错误: got=%s want=%s", start, want)
	}
	if _, err := projectMonthStartUTC("invalid/timezone", now); !errors.Is(err, ErrVideoReservationState) {
		t.Fatalf("非法项目时区必须失败关闭: %v", err)
	}
}

func videoPriceFixture(t *testing.T, now time.Time) (*fakeActivePriceReader, VideoPriceVariant) {
	t.Helper()
	variant := VideoPriceVariant{Operation: model.AIVideoOperationTextToVideo, Resolution: "1280x720", DurationSeconds: 5, AspectRatio: "16:9", FrameRate: 24, Audio: false}
	other := variant
	other.Operation = model.AIVideoOperationImageToVideo
	variants := []VideoPriceVariant{variant, other}
	limits, _ := json.Marshal(VideoPricingLimits{MeterType: VideoMeterSeconds, Variants: variants})
	skus := make([]model.AIPriceSKU, 0, len(variants))
	for _, candidate := range variants {
		raw, hash, err := CanonicalVideoPriceVariant(candidate)
		if err != nil {
			t.Fatal(err)
		}
		skus = append(skus, model.AIPriceSKU{PriceVersionID: 301, MeterType: VideoMeterSeconds, VariantJSON: raw, VariantHash: hash, CostUnitPrice: decimal.RequireFromString("0.06000000"), SaleUnitPrice: decimal.RequireFromString("0.10000000"), Scale: decimal.NewFromInt(1), Currency: "CNY"})
	}
	return &fakeActivePriceReader{version: &model.AIPriceVersion{ID: 301, LogicalModelCode: "molin/runway-gen4.5", Capability: model.AIVideoCapability, PricingTemplate: VideoMeterSeconds, VersionNo: 1, Currency: "CNY", ExchangeRate: decimal.NewFromInt(1), Status: model.AIPriceActive, MinMarginRate: decimal.RequireFromString("0.20"), LimitsJSON: limits, MinimumCharge: decimal.RequireFromString("0.10000000"), CostSource: VideoPricePurposeNonCommercialFixture, CostSourceVersion: "vid-g2-fixture-v1", PricePurpose: VideoPricePurposeNonCommercialFixture, FailureChargePolicy: "confirmed_usage", RoundingMode: "ceil_8", CostExpiresAt: now.Add(time.Hour), EffectiveAt: now.Add(-time.Hour)}, skus: skus}, variant
}

type fakeVideoQuoteStore struct {
	mu      sync.Mutex
	byID    map[string]*model.AIGatewayQuote
	byScope map[string]*model.AIGatewayQuote
}

type fakeVideoInputResolver struct {
	items map[string]VideoQuoteInputBinding
}

func (r *fakeVideoInputResolver) ResolveReadyInput(_ context.Context, _, _ uint64, inputAssetID string) (*VideoQuoteInputBinding, error) {
	item, ok := r.items[inputAssetID]
	if !ok {
		return nil, ErrVideoInputMismatch
	}
	copyItem := item
	return &copyItem, nil
}

type fakeVideoReservation struct {
	quotes *VideoQuoteService
	calls  atomic.Int32
}

func (r *fakeVideoReservation) ReserveAndCreate(ctx context.Context, command VideoReservationCommand) (*VideoPreparedGeneration, error) {
	r.calls.Add(1)
	quote, replay, err := r.quotes.ConsumeQuote(ctx, command.QuotePublicID, command.FingerprintInput, command.RequestID)
	if err != nil {
		return nil, err
	}
	return &VideoPreparedGeneration{Quote: quote, RequestID: command.RequestID, TaskID: command.TaskID, HeldAmount: quote.QuotedAmount, Existing: replay}, nil
}

func newFakeVideoQuoteStore() *fakeVideoQuoteStore {
	return &fakeVideoQuoteStore{byID: make(map[string]*model.AIGatewayQuote), byScope: make(map[string]*model.AIGatewayQuote)}
}

func (s *fakeVideoQuoteStore) FindIdempotent(_ context.Context, userID, projectID uint64, commandKind, idempotencyKey, fingerprint string) (*model.AIGatewayQuote, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%d:%d:%s:%s", userID, projectID, commandKind, idempotencyKey)
	existing := s.byScope[key]
	if existing == nil {
		return nil, false, nil
	}
	if existing.RequestFingerprint != fingerprint {
		return nil, false, repository.ErrVideoQuoteConflict
	}
	copyQuote := *existing
	return &copyQuote, true, nil
}

func (s *fakeVideoQuoteStore) CreateIdempotent(_ context.Context, quote *model.AIGatewayQuote) (*model.AIGatewayQuote, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%d:%d:%s:%s", quote.UserID, quote.ProjectID, *quote.CommandKind, *quote.IdempotencyKey)
	if existing := s.byScope[key]; existing != nil {
		if existing.RequestFingerprint != quote.RequestFingerprint {
			return nil, false, repository.ErrVideoQuoteConflict
		}
		copyQuote := *existing
		return &copyQuote, true, nil
	}
	copyQuote := *quote
	copyQuote.ID = uint64(len(s.byID) + 1)
	s.byID[quote.PublicID] = &copyQuote
	s.byScope[key] = &copyQuote
	returned := copyQuote
	return &returned, false, nil
}

func (s *fakeVideoQuoteStore) Consume(_ context.Context, publicID string, userID, projectID uint64, apiKeyID *uint64, operation, fingerprint, requestID string, now time.Time) (*model.AIGatewayQuote, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	quote := s.byID[publicID]
	if quote == nil || quote.UserID != userID || quote.ProjectID != projectID || !equalOptionalUint64(quote.APIKeyID, apiKeyID) || quote.Operation == nil || *quote.Operation != operation {
		return nil, false, repository.ErrVideoQuoteNotFound
	}
	if quote.RequestFingerprint != fingerprint {
		return nil, false, repository.ErrVideoQuoteConflict
	}
	if quote.ConsumedRequestID != nil {
		if *quote.ConsumedRequestID != requestID {
			return nil, false, repository.ErrVideoQuoteConsumed
		}
		copyQuote := *quote
		return &copyQuote, true, nil
	}
	if !quote.ExpiresAt.After(now) {
		return nil, false, repository.ErrVideoQuoteExpired
	}
	quote.ConsumedRequestID = &requestID
	quote.ConsumedAt = &now
	copyQuote := *quote
	return &copyQuote, false, nil
}
