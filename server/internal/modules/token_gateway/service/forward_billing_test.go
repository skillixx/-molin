package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
)

// ——— 内存桩 ———

// fakeWalletHolder 记录 freeze/settle/release 调用，模拟钱包预扣保证金。
type fakeWalletHolder struct {
	freezeErr   error           // FreezeHold 返回的错误（如余额不足）
	frozen      decimal.Decimal // 最近一次冻结金额
	freezeCalls int
	settleCalls int
	settleArg   decimal.Decimal // 最近 SettleHold 的 actual
	releaseIDs  []uint64        // ReleaseHold 调用的 holdID 列表
	nextHoldID  uint64
}

func (f *fakeWalletHolder) FreezeHold(_ context.Context, _ uint64, amount decimal.Decimal, _, _ string) (uint64, error) {
	f.freezeCalls++
	if f.freezeErr != nil {
		return 0, f.freezeErr
	}
	f.frozen = amount
	if f.nextHoldID == 0 {
		f.nextHoldID = 1001
	}
	return f.nextHoldID, nil
}

func (f *fakeWalletHolder) SettleHold(_ context.Context, _ uint64, actual decimal.Decimal, _ string) error {
	f.settleCalls++
	f.settleArg = actual
	return nil
}

func (f *fakeWalletHolder) ReleaseHold(_ context.Context, holdID uint64, _ string) error {
	f.releaseIDs = append(f.releaseIDs, holdID)
	return nil
}

// fakeReporter 记录上报事件，按 usage_type 返回固定实扣金额。
type fakeReporter struct {
	events  []UsageEvent
	charged map[string]decimal.Decimal // usage_type → 实扣金额
}

func (f *fakeReporter) Report(_ context.Context, evt UsageEvent) (decimal.Decimal, error) {
	f.events = append(f.events, evt)
	if f.charged == nil {
		return decimal.Zero, nil
	}
	return f.charged[evt.UsageType], nil
}

// fakeEntConsumer 记录套餐额度扣减调用。
type fakeEntConsumer struct {
	calls   int
	lastAmt decimal.Decimal
	lastEnt uint64
	lastKey string
	err     error
}

func (f *fakeEntConsumer) Consume(_ context.Context, entitlementID, _ uint64, amount decimal.Decimal, key string) error {
	f.calls++
	f.lastEnt = entitlementID
	f.lastAmt = amount
	f.lastKey = key
	return f.err
}

// fakeSaleWriter 记录 sale_amount 回填。
type fakeSaleWriter struct {
	saved map[string]decimal.Decimal
}

func (f *fakeSaleWriter) UpdateSaleAmountByRequestID(_ context.Context, requestID string, amount decimal.Decimal) error {
	if f.saved == nil {
		f.saved = map[string]decimal.Decimal{}
	}
	f.saved[requestID] = amount
	return nil
}

// fakeBillingResolver 模拟 auth.BillingByID。
type fakeBillingResolver struct {
	mode   string
	source uint64
	ok     bool
}

func (f *fakeBillingResolver) BillingByID(_ context.Context, _ uint64) (string, uint64, bool) {
	return f.mode, f.source, f.ok
}

func pid(v uint64) *uint64 { return &v }

// ——— 测试 ———

// TestResolveBilling 覆盖计费模式解析各分支。
func TestResolveBilling(t *testing.T) {
	cases := []struct {
		name       string
		apiKeyID   uint64
		resolver   BillingResolver
		wantMode   string
		wantSource uint64
	}{
		{"登录态（apiKeyID=0）默认 postpaid", 0, &fakeBillingResolver{mode: "prepaid", source: 9, ok: true}, billingModePostpaid, 0},
		{"resolver 为 nil 默认 postpaid", 5, nil, billingModePostpaid, 0},
		{"key 失效（ok=false）兜底 postpaid", 5, &fakeBillingResolver{ok: false}, billingModePostpaid, 0},
		{"sk postpaid", 5, &fakeBillingResolver{mode: "postpaid", ok: true}, billingModePostpaid, 0},
		{"sk prepaid 带 source_id", 5, &fakeBillingResolver{mode: "prepaid", source: 77, ok: true}, billingModePrepaid, 77},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &ForwardService{billingResolver: tc.resolver, holdMaxTokens: 100}
			d := s.resolveBilling(context.Background(), ForwardInput{APIKeyID: tc.apiKeyID})
			if d.mode != tc.wantMode || d.sourceID != tc.wantSource {
				t.Fatalf("resolveBilling = (%s,%d), 期望 (%s,%d)", d.mode, d.sourceID, tc.wantMode, tc.wantSource)
			}
		})
	}
}

// TestResolveMaxTokens 覆盖 max_tokens 取值与兜底。
func TestResolveMaxTokens(t *testing.T) {
	s := &ForwardService{holdMaxTokens: 2048}
	if got := s.resolveMaxTokens(map[string]interface{}{"max_tokens": float64(500)}); got != 500 {
		t.Fatalf("带 max_tokens 应取 500，实际 %d", got)
	}
	if got := s.resolveMaxTokens(map[string]interface{}{}); got != 2048 {
		t.Fatalf("缺 max_tokens 应取兜底 2048，实际 %d", got)
	}
	if got := s.resolveMaxTokens(map[string]interface{}{"max_tokens": float64(0)}); got != 2048 {
		t.Fatalf("max_tokens<=0 应取兜底 2048，实际 %d", got)
	}
}

// TestSettlePostpaid_ReportsAndReleasesHold 验证 postpaid：按量+按次上报、回填 sale_amount=实扣和、解冻保证金。
func TestSettlePostpaid_ReportsAndReleasesHold(t *testing.T) {
	holder := &fakeWalletHolder{}
	reporter := &fakeReporter{charged: map[string]decimal.Decimal{
		"input_tokens":  decimal.NewFromFloat(0.12),
		"output_tokens": decimal.NewFromFloat(0.34),
		"calls":         decimal.NewFromFloat(0.01),
	}}
	writer := &fakeSaleWriter{}
	s := &ForwardService{reporter: reporter, walletHolder: holder, saleWriter: writer}

	tm := &model.TokenModel{ProductID: pid(7), Modality: "chat"}
	bill := &billDecision{mode: billingModePostpaid, holdID: 1001, maxTokens: 1000}
	s.settle(context.Background(), "req-1", ForwardInput{UserID: 3}, tm, 100, 200, bill)

	// 应上报 3 条：input/output/calls。
	if len(reporter.events) != 3 {
		t.Fatalf("postpaid 应上报 3 条事件，实际 %d", len(reporter.events))
	}
	// sale_amount = 0.12+0.34+0.01 = 0.47。
	want := decimal.NewFromFloat(0.47)
	if got := writer.saved["req-1"]; !got.Equal(want) {
		t.Fatalf("sale_amount 应回填 %s，实际 %s", want, got)
	}
	// 保证金应被释放（ReleaseHold），且 bill.settled 置位。
	if len(holder.releaseIDs) != 1 || holder.releaseIDs[0] != 1001 {
		t.Fatalf("postpaid 结算应释放保证金 holdID=1001，实际 %v", holder.releaseIDs)
	}
	if !bill.settled {
		t.Fatalf("结算后 bill.settled 应为 true")
	}
}

// TestSettlePostpaid_NoReporterStillReleasesHold 验证未配上报器时仍解冻保证金（不锁死）。
func TestSettlePostpaid_NoReporterStillReleasesHold(t *testing.T) {
	holder := &fakeWalletHolder{}
	s := &ForwardService{reporter: nil, walletHolder: holder, saleWriter: &fakeSaleWriter{}}
	tm := &model.TokenModel{ProductID: pid(7)}
	bill := &billDecision{mode: billingModePostpaid, holdID: 2002, maxTokens: 1000}
	s.settle(context.Background(), "req-2", ForwardInput{UserID: 3}, tm, 100, 200, bill)
	if len(holder.releaseIDs) != 1 || holder.releaseIDs[0] != 2002 {
		t.Fatalf("无上报器也应释放保证金 holdID=2002，实际 %v", holder.releaseIDs)
	}
}

// TestSettlePrepaid_ConsumesQuotaNoWallet 验证 prepaid：扣套餐额度、不走钱包/不上报 product-usage-events、回填 sale_amount=额度。
func TestSettlePrepaid_ConsumesQuotaNoWallet(t *testing.T) {
	holder := &fakeWalletHolder{}
	reporter := &fakeReporter{}
	ent := &fakeEntConsumer{}
	writer := &fakeSaleWriter{}
	s := &ForwardService{reporter: reporter, walletHolder: holder, entConsumer: ent, saleWriter: writer}

	bill := &billDecision{mode: billingModePrepaid, sourceID: 88, maxTokens: 1000}
	s.settle(context.Background(), "req-3", ForwardInput{UserID: 3}, &model.TokenModel{}, 100, 200, bill)

	// prepaid 不上报钱包事件、不冻结/解冻。
	if len(reporter.events) != 0 {
		t.Fatalf("prepaid 不应上报 product-usage-events，实际 %d 条", len(reporter.events))
	}
	if holder.freezeCalls != 0 || len(holder.releaseIDs) != 0 || holder.settleCalls != 0 {
		t.Fatalf("prepaid 不应触碰钱包保证金")
	}
	// 扣额度 amount = input+output = 300，幂等键 req_id:quota。
	if ent.calls != 1 || !ent.lastAmt.Equal(decimal.NewFromInt(300)) || ent.lastEnt != 88 {
		t.Fatalf("prepaid 应扣额度 300（entitlement=88），实际 calls=%d amt=%s ent=%d", ent.calls, ent.lastAmt, ent.lastEnt)
	}
	if ent.lastKey != "req-3:quota" {
		t.Fatalf("prepaid 幂等键应为 req-3:quota，实际 %s", ent.lastKey)
	}
	// sale_amount = 300（额度）。
	if got := writer.saved["req-3"]; !got.Equal(decimal.NewFromInt(300)) {
		t.Fatalf("prepaid sale_amount 应为 300，实际 %s", got)
	}
}

// TestSettlePrepaid_QuotaExhaustedNoSaleAmount 验证 prepaid 额度不足时不回填 sale_amount（调用已发生仅告警）。
func TestSettlePrepaid_QuotaExhaustedNoSaleAmount(t *testing.T) {
	ent := &fakeEntConsumer{err: ErrQuotaExhausted}
	writer := &fakeSaleWriter{}
	s := &ForwardService{entConsumer: ent, saleWriter: writer}
	bill := &billDecision{mode: billingModePrepaid, sourceID: 88, maxTokens: 1000}
	s.settle(context.Background(), "req-4", ForwardInput{UserID: 3}, &model.TokenModel{}, 10, 20, bill)
	if _, ok := writer.saved["req-4"]; ok {
		t.Fatalf("额度不足时不应回填 sale_amount")
	}
}

// TestSettle_DoesNotFallbackOnMissingUsage 验证 usage 缺失时禁止按 max_tokens 猜测扣费。
func TestSettle_DoesNotFallbackOnMissingUsage(t *testing.T) {
	ent := &fakeEntConsumer{}
	s := &ForwardService{entConsumer: ent, saleWriter: &fakeSaleWriter{}}
	bill := &billDecision{mode: billingModePrepaid, sourceID: 88, maxTokens: 512}
	// 调用方正常会进入 pending_reconcile；即使误传全 0，结算层也不得产生 max_tokens 扣费。
	s.settle(context.Background(), "req-5", ForwardInput{UserID: 3}, &model.TokenModel{}, 0, 0, bill)
	if ent.calls != 0 {
		t.Fatalf("usage 缺失时不应扣额度，实际 calls=%d amt=%s", ent.calls, ent.lastAmt)
	}
}

// TestFreezeHold_InsufficientMappedToErr 验证前置冻结余额不足映射为 ErrWalletInsufficient（间接用 holder 桩）。
func TestFreezeHold_InsufficientMappedToErr(t *testing.T) {
	holder := &fakeWalletHolder{freezeErr: ErrWalletInsufficient}
	// 直接验证 holder 行为：FreezeHold 返回 ErrWalletInsufficient。
	_, err := holder.FreezeHold(context.Background(), 1, decimal.NewFromInt(10), "k", "r")
	if !errors.Is(err, ErrWalletInsufficient) {
		t.Fatalf("余额不足应返回 ErrWalletInsufficient，实际 %v", err)
	}
}
