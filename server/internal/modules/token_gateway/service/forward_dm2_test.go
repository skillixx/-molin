package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
)

// ——— D-M2-01 方案 B：prepaid 转发前预占额度 reserve → 结算 settle / 释放 release ———

// fakeReserver 内存桩，模拟 asset entitlement-reserve/settle/release（丙4）。
type fakeReserver struct {
	// reserve
	reserveCalls int
	reserveHold  uint64 // 成功时返回的 holdID
	reserveErr   error
	lastResvAmt  decimal.Decimal
	lastResvEnt  uint64
	lastResvKey  string
	// settle
	settleCalls   int
	settledReturn decimal.Decimal // settle 返回的实扣净额
	settleErr     error
	lastSettleAmt decimal.Decimal
	lastSettleKey string
	// release
	releaseCalls int
	lastRelKey   string
}

func (f *fakeReserver) Reserve(_ context.Context, entitlementID, _ uint64, amount decimal.Decimal, key string) (uint64, error) {
	f.reserveCalls++
	f.lastResvAmt = amount
	f.lastResvEnt = entitlementID
	f.lastResvKey = key
	if f.reserveErr != nil {
		return 0, f.reserveErr
	}
	return f.reserveHold, nil
}

func (f *fakeReserver) Settle(_ context.Context, _ uint64, key string, actual decimal.Decimal) (decimal.Decimal, error) {
	f.settleCalls++
	f.lastSettleAmt = actual
	f.lastSettleKey = key
	if f.settleErr != nil {
		return decimal.Zero, f.settleErr
	}
	return f.settledReturn, nil
}

func (f *fakeReserver) Release(_ context.Context, _ uint64, key string) error {
	f.releaseCalls++
	f.lastRelKey = key
	return nil
}

// TestReservePrepaid 覆盖 D-M2-01 方案 B prepaid 转发前预占闸各分支。
func TestReservePrepaid(t *testing.T) {
	prepaidBill := func() billDecision {
		return billDecision{mode: billingModePrepaid, sourceID: 88, maxTokens: 21}
	}

	t.Run("预占成功 → 放行并记 holdID", func(t *testing.T) {
		r := &fakeReserver{reserveHold: 555}
		s := &ForwardService{entReserver: r}
		bill := prepaidBill()
		if err := s.reservePrepaid(context.Background(), ForwardInput{UserID: 3, RequestID: "req"}, &bill); err != nil {
			t.Fatalf("预占成功应放行，实际 %v", err)
		}
		if bill.holdID != 555 {
			t.Fatalf("应记 holdID=555，实际 %d", bill.holdID)
		}
		// reserve_amount 口径 = max_tokens = 21（token 数，不乘单价）。
		if !r.lastResvAmt.Equal(decimal.NewFromInt(21)) {
			t.Fatalf("reserve_amount 应为 max_tokens=21，实际 %s", r.lastResvAmt)
		}
		if r.lastResvKey != "req:quota_reserve" {
			t.Fatalf("reserve 幂等键应为 req:quota_reserve，实际 %s", r.lastResvKey)
		}
	})

	t.Run("D-M2-01 核心：串行低余额（remaining<单次）reserve 占不到 → 拒 60005，不转发不白嫖", func(t *testing.T) {
		// remaining=9 < 单次需 21：丙侧 reserve 返回 60005 → 适配器归 ErrQuotaExhausted。
		r := &fakeReserver{reserveErr: ErrQuotaExhausted}
		s := &ForwardService{entReserver: r}
		bill := prepaidBill()
		err := s.reservePrepaid(context.Background(), ForwardInput{UserID: 3, RequestID: "req"}, &bill)
		if !errors.Is(err, ErrQuotaExhausted) {
			t.Fatalf("额度不足应拒 ErrQuotaExhausted（60005），实际 %v", err)
		}
		if bill.holdID != 0 {
			t.Fatalf("占不到不应记 holdID，实际 %d", bill.holdID)
		}
	})

	t.Run("归属不符/权益不存在 → ErrEntitlementDenied（40003）", func(t *testing.T) {
		r := &fakeReserver{reserveErr: ErrEntitlementDenied}
		s := &ForwardService{entReserver: r}
		bill := prepaidBill()
		if err := s.reservePrepaid(context.Background(), ForwardInput{UserID: 3, RequestID: "req"}, &bill); !errors.Is(err, ErrEntitlementDenied) {
			t.Fatalf("归属不符应拒 ErrEntitlementDenied，实际 %v", err)
		}
	})

	t.Run("fail-safe：reserve 调用失败（系统繁忙）→ ErrSystemBusy（503），拒转发不白嫖、不混淆为 60005", func(t *testing.T) {
		r := &fakeReserver{reserveErr: ErrSystemBusy}
		s := &ForwardService{entReserver: r}
		bill := prepaidBill()
		err := s.reservePrepaid(context.Background(), ForwardInput{UserID: 3, RequestID: "req"}, &bill)
		if !errors.Is(err, ErrSystemBusy) {
			t.Fatalf("reserve 调用失败应 fail-safe 拒 ErrSystemBusy（503），实际 %v", err)
		}
		if errors.Is(err, ErrQuotaExhausted) {
			t.Fatalf("系统繁忙绝不能被映射为额度不足 60005（红线）")
		}
		if bill.holdID != 0 {
			t.Fatalf("调用失败不应记 holdID，实际 %d", bill.holdID)
		}
	})

	t.Run("非 prepaid（postpaid）→ 跳过预占（nil），不调 reserve", func(t *testing.T) {
		r := &fakeReserver{}
		s := &ForwardService{entReserver: r}
		bill := billDecision{mode: billingModePostpaid, maxTokens: 100}
		if err := s.reservePrepaid(context.Background(), ForwardInput{UserID: 3, RequestID: "req"}, &bill); err != nil {
			t.Fatalf("postpaid 应跳过，实际 %v", err)
		}
		if r.reserveCalls != 0 {
			t.Fatalf("postpaid 不应调 reserve，实际 %d", r.reserveCalls)
		}
	})

	t.Run("entReserver 为 nil → 退化为不预占（nil）", func(t *testing.T) {
		s := &ForwardService{entReserver: nil}
		bill := prepaidBill()
		if err := s.reservePrepaid(context.Background(), ForwardInput{UserID: 3, RequestID: "req"}, &bill); err != nil {
			t.Fatalf("entReserver 为 nil 应退化放行，实际 %v", err)
		}
	})

	t.Run("source_id==0 → 跳过（nil）", func(t *testing.T) {
		r := &fakeReserver{}
		s := &ForwardService{entReserver: r}
		bill := billDecision{mode: billingModePrepaid, sourceID: 0, maxTokens: 21}
		if err := s.reservePrepaid(context.Background(), ForwardInput{UserID: 3, RequestID: "req"}, &bill); err != nil {
			t.Fatalf("source_id==0 应跳过，实际 %v", err)
		}
		if r.reserveCalls != 0 {
			t.Fatalf("source_id==0 不应调 reserve")
		}
	})

	t.Run("reserve_amount 非正 → fail-safe 拒 ErrSystemBusy，不调 reserve", func(t *testing.T) {
		r := &fakeReserver{}
		s := &ForwardService{entReserver: r}
		bill := billDecision{mode: billingModePrepaid, sourceID: 88, maxTokens: 0}
		if err := s.reservePrepaid(context.Background(), ForwardInput{UserID: 3, RequestID: "req"}, &bill); !errors.Is(err, ErrSystemBusy) {
			t.Fatalf("reserve_amount 非正应拒 ErrSystemBusy，实际 %v", err)
		}
		if r.reserveCalls != 0 {
			t.Fatalf("amount 非正不应调 reserve")
		}
	})
}

// TestSettlePrepaid_ReserveSettleMultiRefund 验证方案 B 结算：已预占 → settle 多退少补 + sale_amount 回填净额。
func TestSettlePrepaid_ReserveSettleMultiRefund(t *testing.T) {
	// 预占 21，实际消耗 input=4+output=5=9（少补：settle 返回净额 9）。
	r := &fakeReserver{settledReturn: decimal.NewFromInt(9)}
	writer := &fakeSaleWriter{}
	s := &ForwardService{entReserver: r, saleWriter: writer}
	bill := &billDecision{mode: billingModePrepaid, sourceID: 88, holdID: 555, maxTokens: 21}

	s.settle(context.Background(), "req-s", ForwardInput{UserID: 3}, &model.TokenModel{}, 4, 5, bill)

	if r.settleCalls != 1 {
		t.Fatalf("应调 settle 一次，实际 %d", r.settleCalls)
	}
	// actual = input+output = 9 传给 settle。
	if !r.lastSettleAmt.Equal(decimal.NewFromInt(9)) {
		t.Fatalf("settle actual 应为 9，实际 %s", r.lastSettleAmt)
	}
	if r.lastSettleKey != "req-s:quota_reserve" {
		t.Fatalf("settle 幂等键应关联同一 hold（req-s:quota_reserve），实际 %s", r.lastSettleKey)
	}
	// sale_amount = settle 返回的实扣净额 9。
	if got := writer.saved["req-s"]; !got.Equal(decimal.NewFromInt(9)) {
		t.Fatalf("sale_amount 应为 settle 净额 9，实际 %s", got)
	}
	// 已 settle → 接管 hold，defer 不再 release。
	if !bill.settled {
		t.Fatalf("settle 后应置 bill.settled=true（防 defer 重复 release）")
	}
	if r.releaseCalls != 0 {
		t.Fatalf("settle 路径不应再 release")
	}
}

// TestSettlePrepaid_SettleFailureNoSaleAmount 验证 settle 失败时不回填 sale_amount、且接管 hold（不重复 release）。
func TestSettlePrepaid_SettleFailureNoSaleAmount(t *testing.T) {
	r := &fakeReserver{settleErr: errors.New("asset 5xx")}
	writer := &fakeSaleWriter{}
	s := &ForwardService{entReserver: r, saleWriter: writer}
	bill := &billDecision{mode: billingModePrepaid, sourceID: 88, holdID: 555, maxTokens: 21}

	s.settle(context.Background(), "req-f", ForwardInput{UserID: 3}, &model.TokenModel{}, 10, 11, bill)

	if _, ok := writer.saved["req-f"]; ok {
		t.Fatalf("settle 失败不应回填 sale_amount")
	}
	if !bill.settled {
		t.Fatalf("settle 失败也应接管 hold（settled=true），由 asset 幂等对账兜底")
	}
}

// TestSettlePrepaid_DegradedDirectConsume 验证退化路径：未预占（holdID==0）→ 回落旧直扣 entitlement-consume。
func TestSettlePrepaid_DegradedDirectConsume(t *testing.T) {
	r := &fakeReserver{} // 注入了 reserver，但本次未预占（holdID==0）
	ent := &fakeEntConsumer{}
	writer := &fakeSaleWriter{}
	s := &ForwardService{entReserver: r, entConsumer: ent, saleWriter: writer}
	bill := &billDecision{mode: billingModePrepaid, sourceID: 88, holdID: 0, maxTokens: 100}

	s.settle(context.Background(), "req-d", ForwardInput{UserID: 3}, &model.TokenModel{}, 6, 7, bill)

	if r.settleCalls != 0 {
		t.Fatalf("未预占不应调 settle，实际 %d", r.settleCalls)
	}
	if ent.calls != 1 || !ent.lastAmt.Equal(decimal.NewFromInt(13)) {
		t.Fatalf("退化路径应直扣 consume amount=13，实际 calls=%d amt=%s", ent.calls, ent.lastAmt)
	}
	if got := writer.saved["req-d"]; !got.Equal(decimal.NewFromInt(13)) {
		t.Fatalf("退化路径 sale_amount 应为 13，实际 %s", got)
	}
}

// TestForwardDeferReleasesPrepaidReserve 验证失败路径释放预占：
// holdID!=0 && !settled && prepaid → 调 entReserver.Release 全额释放（不计 used）。
func TestForwardDeferReleasesPrepaidReserve(t *testing.T) {
	r := &fakeReserver{}
	in := ForwardInput{UserID: 3, RequestID: "req-r"}
	bill := billDecision{mode: billingModePrepaid, sourceID: 88, holdID: 555}

	// 复刻 Forward defer 中 prepaid 分支的释放逻辑（未结算路径）。
	releasePrepaidIfUnsettled(r, in, &bill)

	if r.releaseCalls != 1 {
		t.Fatalf("失败路径应 release 一次释放预占，实际 %d", r.releaseCalls)
	}
	if r.lastRelKey != "req-r:quota_reserve" {
		t.Fatalf("release 应关联同一 hold（req-r:quota_reserve），实际 %s", r.lastRelKey)
	}

	// 已结算（settled=true）则 defer 不再 release（防重复）。
	r2 := &fakeReserver{}
	bill2 := billDecision{mode: billingModePrepaid, sourceID: 88, holdID: 555, settled: true}
	releasePrepaidIfUnsettled(r2, in, &bill2)
	if r2.releaseCalls != 0 {
		t.Fatalf("已 settle 不应再 release，实际 %d", r2.releaseCalls)
	}
}

// releasePrepaidIfUnsettled 复刻 Forward defer 中 prepaid 失败路径的释放语义（供单测验证；生产逻辑在 Forward 内联 defer）。
func releasePrepaidIfUnsettled(r EntitlementReserver, in ForwardInput, bill *billDecision) {
	if bill.holdID == 0 || bill.settled {
		return
	}
	if bill.mode == billingModePrepaid && r != nil {
		_ = r.Release(context.Background(), bill.holdID, in.RequestID+":quota_reserve")
	}
}

// ——— D-M2-02：postpaid 预扣保证金错误分流 ———

// TestClassifyFreezeError 验证 D-M2-02：余额不足返 60001、乐观锁冲突返 503（不混淆）。
func TestClassifyFreezeError(t *testing.T) {
	cases := []struct {
		name    string
		in      error
		wantErr error
	}{
		{"真余额不足 → ErrWalletInsufficient（60001）", ErrWalletInsufficient, ErrWalletInsufficient},
		{"乐观锁冲突重试耗尽 → ErrSystemBusy（503），不混淆为 60001", ErrSystemBusy, ErrSystemBusy},
		{"其它未知错误 → 保守归为 ErrSystemBusy（503）", errors.New("数据库连接断开"), ErrSystemBusy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyFreezeError("req", tc.in)
			if !errors.Is(got, tc.wantErr) {
				t.Fatalf("classifyFreezeError(%v) = %v, 期望 %v", tc.in, got, tc.wantErr)
			}
			// 关键红线：乐观锁冲突绝不能被映射为余额不足。
			if errors.Is(tc.in, ErrSystemBusy) && errors.Is(got, ErrWalletInsufficient) {
				t.Fatalf("乐观锁冲突被误映射为余额不足 60001（D-M2-02 红线）")
			}
		})
	}
}
