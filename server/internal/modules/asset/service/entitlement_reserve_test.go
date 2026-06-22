package service

import (
	"testing"
	"time"

	"molin/server/internal/modules/asset/model"
)

// TestCheckEntitlementReservable 覆盖预占前置校验：
// 归属(40003) / 状态&过期(60005) / available 不足(60005，纳入 quota_reserved) / 不限量恒过。
func TestCheckEntitlementReservable(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	past := now.Add(-24 * time.Hour)

	base := func() *model.UserEntitlement {
		return &model.UserEntitlement{
			ID:            1,
			UserID:        45,
			Status:        "active",
			QuotaTotal:    decPtr("100"),
			QuotaUsed:     dec("30"),
			QuotaReserved: dec("20"), // available = 100 - 30 - 20 = 50
			ExpiresAt:     &future,
		}
	}

	cases := []struct {
		name    string
		mutate  func(e *model.UserEntitlement)
		userID  uint64
		amount  string
		wantErr error
	}{
		{"available 充足-恰好等于", nil, 45, "50", nil},
		{"available 充足-小于", nil, 45, "10", nil},
		{"available 不足-超 1", nil, 45, "51", ErrQuotaExceeded},
		{"归属不符", nil, 999, "10", ErrEntitlementNotOwned},
		{"状态非 active", func(e *model.UserEntitlement) { e.Status = "suspended" }, 45, "10", ErrEntitlementInactive},
		{"已过期", func(e *model.UserEntitlement) { e.ExpiresAt = &past }, 45, "10", ErrEntitlementInactive},
		{"不限量恒过-大额", func(e *model.UserEntitlement) { e.QuotaTotal = nil }, 45, "999999999", nil},
		{"已预占占满-available=0 再占失败", func(e *model.UserEntitlement) { e.QuotaReserved = dec("70") }, 45, "1", ErrQuotaExceeded},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := base()
			if c.mutate != nil {
				c.mutate(e)
			}
			err := checkEntitlementReservable(e, c.userID, dec(c.amount), now)
			if err != c.wantErr {
				t.Fatalf("checkEntitlementReservable 期望错误 %v，实际 %v", c.wantErr, err)
			}
		})
	}
}
