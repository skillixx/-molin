package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/asset/model"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func decPtr(s string) *decimal.Decimal { d := dec(s); return &d }

// TestCheckEntitlementConsumable 覆盖扣减前置校验：归属(40003) / 状态&过期(60005) / 额度不足越界(60005) / 不限量放行。
func TestCheckEntitlementConsumable(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	past := now.Add(-24 * time.Hour)

	base := func() *model.UserEntitlement {
		return &model.UserEntitlement{
			ID:         1,
			UserID:     45,
			Status:     "active",
			QuotaTotal: decPtr("1000000"),
			QuotaUsed:  dec("5000"),
			ExpiresAt:  &future,
		}
	}

	cases := []struct {
		name    string
		mutate  func(e *model.UserEntitlement)
		userID  uint64
		amount  string
		wantErr error
	}{
		{"正常扣减-额度充足", nil, 45, "232", nil},
		{"边界-恰好用尽-放行", func(e *model.UserEntitlement) { e.QuotaUsed = dec("999768") }, 45, "232", nil},
		{"越界-超出剩余一点点-60005", func(e *model.UserEntitlement) { e.QuotaUsed = dec("999769") }, 45, "232", ErrQuotaExceeded},
		{"越界-远超剩余-60005", nil, 45, "2000000", ErrQuotaExceeded},
		{"归属不符-40003", nil, 999, "1", ErrEntitlementNotOwned},
		{"状态非active(expired)-60005", func(e *model.UserEntitlement) { e.Status = "expired" }, 45, "1", ErrEntitlementInactive},
		{"状态非active(suspended)-60005", func(e *model.UserEntitlement) { e.Status = "suspended" }, 45, "1", ErrEntitlementInactive},
		{"已过期(expires_at<=now)-60005", func(e *model.UserEntitlement) { e.ExpiresAt = &past }, 45, "1", ErrEntitlementInactive},
		{"到期时刻(expires_at==now)视为过期-60005", func(e *model.UserEntitlement) { e.ExpiresAt = &now }, 45, "1", ErrEntitlementInactive},
		{"不限量(quota_total=NULL)-任意额度放行", func(e *model.UserEntitlement) { e.QuotaTotal = nil }, 45, "9999999999", nil},
		{"无到期时间(expires_at=NULL)-不限期放行", func(e *model.UserEntitlement) { e.ExpiresAt = nil }, 45, "1", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := base()
			if c.mutate != nil {
				c.mutate(e)
			}
			err := checkEntitlementConsumable(e, c.userID, dec(c.amount), now)
			if err != c.wantErr {
				t.Fatalf("checkEntitlementConsumable = %v, 期望 %v", err, c.wantErr)
			}
		})
	}
}

// TestBuildConsumeResult 校验响应快照组装：remaining = quota_total - quota_used；不限量时 remaining 为 nil。
func TestBuildConsumeResult(t *testing.T) {
	t.Run("有限额-remaining正确", func(t *testing.T) {
		e := &model.UserEntitlement{ID: 7, Status: "active", QuotaTotal: decPtr("1000000"), QuotaUsed: dec("5000")}
		// 扣减 232 后 used=5232
		usedAfter := dec("5232")
		res := buildConsumeResult(e, usedAfter)
		if res.EntitlementID != 7 || res.Status != "active" {
			t.Fatalf("基础字段错误: %+v", res)
		}
		if !res.QuotaUsed.Equal(dec("5232")) {
			t.Fatalf("quota_used = %s, 期望 5232", res.QuotaUsed)
		}
		if res.Remaining == nil || !res.Remaining.Equal(dec("994768")) {
			t.Fatalf("remaining = %v, 期望 994768", res.Remaining)
		}
	})
	t.Run("不限量-remaining为nil", func(t *testing.T) {
		e := &model.UserEntitlement{ID: 8, Status: "active", QuotaTotal: nil, QuotaUsed: dec("100")}
		res := buildConsumeResult(e, dec("332"))
		if res.Remaining != nil {
			t.Fatalf("不限量时 remaining 应为 nil，实际 %v", res.Remaining)
		}
		if res.QuotaTotal != nil {
			t.Fatalf("不限量时 quota_total 应为 nil")
		}
	})
}

// TestParseQuotaConfig_TokenPackagePlan 验证 S2-丙1：token-pkg-1m 套餐的 quota_json
// 能正确解析为 token_quota 权益配置（含 valid_days），故 ProvisionService 会据此生成 entitlement，
// TokenProvisioner 无需改动（套餐分支天然放行）。
func TestParseQuotaConfig_TokenPackagePlan(t *testing.T) {
	// 与 000037 seed JSON_OBJECT 等价的 quota_json（对象形态）。
	raw := `{"entitlement_type":"token_quota","quota_total":1000000,"quota_unit":"tokens","valid_days":365}`
	items, err := parseQuotaConfig(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("期望 1 条权益配置，实际 %d", len(items))
	}
	it := items[0]
	if it.EntitlementType != "token_quota" {
		t.Errorf("entitlement_type = %s, 期望 token_quota", it.EntitlementType)
	}
	if it.QuotaTotal == nil || !it.QuotaTotal.Equal(dec("1000000")) {
		t.Errorf("quota_total = %v, 期望 1000000", it.QuotaTotal)
	}
	if it.QuotaUnit == nil || *it.QuotaUnit != "tokens" {
		t.Errorf("quota_unit = %v, 期望 tokens", it.QuotaUnit)
	}
	if it.ValidDays == nil || *it.ValidDays != 365 {
		t.Errorf("valid_days = %v, 期望 365", it.ValidDays)
	}
}

// TestParseQuotaConfig_Empty 空/nil/null 时返回空列表（不生成权益，按量分支不预置额度）。
func TestParseQuotaConfig_Empty(t *testing.T) {
	for _, raw := range []interface{}{nil, "", "null", (*string)(nil)} {
		items, err := parseQuotaConfig(raw)
		if err != nil {
			t.Fatalf("raw=%v 解析报错: %v", raw, err)
		}
		if len(items) != 0 {
			t.Fatalf("raw=%v 期望空列表，实际 %d 条", raw, len(items))
		}
	}
}
