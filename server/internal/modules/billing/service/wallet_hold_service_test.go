package service

import (
	"testing"

	"github.com/shopspring/decimal"
)

// TestComputeSettle 校验预扣保证金结算的「多退少补 + 封顶」核心金额逻辑（S2-乙0 / D1）。
func TestComputeSettle(t *testing.T) {
	d := decimal.RequireFromString
	cases := []struct {
		name       string
		hold       string
		actual     string
		wantSettle string
		wantRefund string
	}{
		{"少补-实扣小于冻结-多退", "10.00", "3.50", "3.50", "6.50"},
		{"实扣等于冻结-不退", "10.00", "10.00", "10.00", "0"},
		{"封顶-实扣超过冻结按冻结封顶-不透支", "10.00", "15.00", "10.00", "0"},
		{"全退-实扣为0", "10.00", "0", "0", "10.00"},
		{"负数实扣归零-全退", "10.00", "-1.00", "0", "10.00"},
		{"高精度", "0.012345", "0.004321", "0.004321", "0.008024"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			settle, refund := computeSettle(d(c.hold), d(c.actual))
			if !settle.Equal(d(c.wantSettle)) {
				t.Errorf("settle = %s, 期望 %s", settle, c.wantSettle)
			}
			if !refund.Equal(d(c.wantRefund)) {
				t.Errorf("refund = %s, 期望 %s", refund, c.wantRefund)
			}
			// 不变量：settle + refund == hold（冻结金额必须被完整结清）。
			if !settle.Add(refund).Equal(d(c.hold)) {
				t.Errorf("settle+refund = %s, 应等于 hold %s", settle.Add(refund), c.hold)
			}
		})
	}
}
