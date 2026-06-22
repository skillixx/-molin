package service

import (
	"context"
	"errors"
	"testing"
)

// ——— D-M2-01：prepaid 转发前置余额闸 ———

// fakeBalanceChecker 内存桩，模拟 asset entitlement-balance 查询。
type fakeBalanceChecker struct {
	bal   EntitlementBalance
	err   error
	calls int
}

func (f *fakeBalanceChecker) GetBalance(_ context.Context, _, _ uint64) (EntitlementBalance, error) {
	f.calls++
	return f.bal, f.err
}

// TestPrepaidPreGate 覆盖 D-M2-01 prepaid 转发前置闸各分支。
func TestPrepaidPreGate(t *testing.T) {
	prepaidBill := billDecision{mode: billingModePrepaid, sourceID: 88}

	cases := []struct {
		name    string
		svc     *ForwardService
		bill    billDecision
		wantErr error
	}{
		{
			name:    "usable=true → 放行（nil）",
			svc:     &ForwardService{balanceChecker: &fakeBalanceChecker{bal: EntitlementBalance{Usable: true, Status: "active"}}},
			bill:    prepaidBill,
			wantErr: nil,
		},
		{
			name:    "usable=false → 拒绝 60005（ErrQuotaExhausted），不转发",
			svc:     &ForwardService{balanceChecker: &fakeBalanceChecker{bal: EntitlementBalance{Usable: false, Status: "active"}}},
			bill:    prepaidBill,
			wantErr: ErrQuotaExhausted,
		},
		{
			name:    "查询失败 → fail-safe 拒绝（ErrQuotaExhausted），不放行白嫖",
			svc:     &ForwardService{balanceChecker: &fakeBalanceChecker{err: errors.New("网络超时")}},
			bill:    prepaidBill,
			wantErr: ErrQuotaExhausted,
		},
		{
			name:    "归属不符/权益不存在 → ErrEntitlementDenied（40003）",
			svc:     &ForwardService{balanceChecker: &fakeBalanceChecker{err: ErrEntitlementDenied}},
			bill:    prepaidBill,
			wantErr: ErrEntitlementDenied,
		},
		{
			name:    "非 prepaid（postpaid）→ 跳过前置闸（nil）",
			svc:     &ForwardService{balanceChecker: &fakeBalanceChecker{bal: EntitlementBalance{Usable: false}}},
			bill:    billDecision{mode: billingModePostpaid},
			wantErr: nil,
		},
		{
			name:    "balanceChecker 为 nil → 退化为不拦截（nil）",
			svc:     &ForwardService{balanceChecker: nil},
			bill:    prepaidBill,
			wantErr: nil,
		},
		{
			name:    "source_id==0 → 跳过（nil）",
			svc:     &ForwardService{balanceChecker: &fakeBalanceChecker{bal: EntitlementBalance{Usable: false}}},
			bill:    billDecision{mode: billingModePrepaid, sourceID: 0},
			wantErr: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.svc.checkPrepaidPreGate(context.Background(), ForwardInput{UserID: 3, RequestID: "req"}, tc.bill)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("checkPrepaidPreGate() = %v, 期望 %v", err, tc.wantErr)
			}
		})
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

// TestPrepaidPreGate_UnusableDoesNotConsume 验证 usable=false 时仅查一次余额、不触发任何扣减/转发副作用。
func TestPrepaidPreGate_UnusableDoesNotConsume(t *testing.T) {
	checker := &fakeBalanceChecker{bal: EntitlementBalance{Usable: false}}
	ent := &fakeEntConsumer{}
	s := &ForwardService{balanceChecker: checker, entConsumer: ent}
	bill := billDecision{mode: billingModePrepaid, sourceID: 88}

	err := s.checkPrepaidPreGate(context.Background(), ForwardInput{UserID: 3, RequestID: "req"}, bill)
	if !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("usable=false 应拒 ErrQuotaExhausted，实际 %v", err)
	}
	if checker.calls != 1 {
		t.Fatalf("前置闸应只查一次余额，实际 %d", checker.calls)
	}
	if ent.calls != 0 {
		t.Fatalf("前置拒绝不应触发额度扣减，实际 %d", ent.calls)
	}
}
