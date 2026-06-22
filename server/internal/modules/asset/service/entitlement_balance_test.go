package service

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/asset/model"
)

// TestBuildBalanceResult 覆盖 usable 计算口径（S2-丙3，前置闸核心逻辑）：
// active + 未过期 + remaining>0 → usable=true；否则 false。不限量时只看 active+未过期。
func TestBuildBalanceResult(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	past := now.Add(-24 * time.Hour)

	base := func() *model.UserEntitlement {
		return &model.UserEntitlement{
			ID:         123,
			UserID:     45,
			Status:     "active",
			QuotaTotal: decPtr("1000000"),
			QuotaUsed:  dec("5232"),
			ExpiresAt:  &future,
		}
	}

	cases := []struct {
		name          string
		mutate        func(e *model.UserEntitlement)
		wantUsable    bool
		wantRemaining string // 空表示 remaining 应为 nil（不限量）
	}{
		{"active+未过期+有余额-usable", nil, true, "994768"},
		{"耗尽-remaining=0-不可用", func(e *model.UserEntitlement) { e.QuotaUsed = dec("1000000") }, false, "0"},
		{"超用-remaining<0-不可用", func(e *model.UserEntitlement) { e.QuotaUsed = dec("1000001") }, false, "-1"},
		{"已过期(expires_at<now)-不可用", func(e *model.UserEntitlement) { e.ExpiresAt = &past }, false, "994768"},
		{"到期时刻(expires_at==now)-不可用", func(e *model.UserEntitlement) { e.ExpiresAt = &now }, false, "994768"},
		{"状态suspended-不可用", func(e *model.UserEntitlement) { e.Status = "suspended" }, false, "994768"},
		{"状态expired-不可用", func(e *model.UserEntitlement) { e.Status = "expired" }, false, "994768"},
		{"状态cancelled-不可用", func(e *model.UserEntitlement) { e.Status = "cancelled" }, false, "994768"},
		{"不限量(quota_total=NULL)+active+未过期-usable且remaining=nil", func(e *model.UserEntitlement) { e.QuotaTotal = nil }, true, ""},
		{"不限量但已过期-不可用", func(e *model.UserEntitlement) { e.QuotaTotal = nil; e.ExpiresAt = &past }, false, ""},
		{"无到期时间(expires_at=NULL)+有余额-usable", func(e *model.UserEntitlement) { e.ExpiresAt = nil }, true, "994768"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := base()
			if c.mutate != nil {
				c.mutate(e)
			}
			res := buildBalanceResult(e, now)
			if res.EntitlementID != 123 || res.UserID != 45 {
				t.Fatalf("基础字段错误: %+v", res)
			}
			if res.Usable != c.wantUsable {
				t.Fatalf("usable = %v, 期望 %v", res.Usable, c.wantUsable)
			}
			if c.wantRemaining == "" {
				if res.Remaining != nil {
					t.Fatalf("不限量时 remaining 应为 nil，实际 %v", res.Remaining)
				}
			} else {
				if res.Remaining == nil || !res.Remaining.Equal(dec(c.wantRemaining)) {
					t.Fatalf("remaining = %v, 期望 %s", res.Remaining, c.wantRemaining)
				}
			}
			if res.Status != e.Status {
				t.Fatalf("status 应如实返回 %s，实际 %s", e.Status, res.Status)
			}
		})
	}
}

// TestGetEntitlementBalance_DB 真实库下覆盖：正常返回 remaining/usable / 归属不符(40003) /
// 不存在(40400) / 过期 usable=false / 耗尽 usable=false。仅 RUN_DB_TESTS=1 时运行。
func TestGetEntitlementBalance_DB(t *testing.T) {
	db, cleanup := setupDBTest(t)
	defer cleanup()
	svc := NewAssetService(db)
	ctx := context.Background()

	// 1. 正常：active + 未过期 + 有余额 → usable=true，remaining 正确
	entID := seedEntitlement(t, db, dbTestUserA, "1000000", "5232")
	res, err := svc.GetEntitlementBalance(ctx, entID, dbTestUserA)
	if err != nil {
		t.Fatalf("正常查询失败: %v", err)
	}
	if !res.Usable {
		t.Fatalf("active 未过期有余额应 usable=true")
	}
	if res.Remaining == nil || !res.Remaining.Equal(dec("994768")) {
		t.Fatalf("remaining = %v, 期望 994768", res.Remaining)
	}
	if res.UserID != dbTestUserA || res.Status != "active" {
		t.Fatalf("快照字段错误: %+v", res)
	}

	// 2. 归属不符 → ErrEntitlementNotOwned（对外 40003）
	_, err = svc.GetEntitlementBalance(ctx, entID, 9_900_000_999)
	if err != ErrEntitlementNotOwned {
		t.Fatalf("归属不符应返回 ErrEntitlementNotOwned，实际 %v", err)
	}

	// 3. 不存在 → ErrEntitlementNotFound（对外 40400）
	_, err = svc.GetEntitlementBalance(ctx, 9_999_999_999, dbTestUserA)
	if err != ErrEntitlementNotFound {
		t.Fatalf("不存在应返回 ErrEntitlementNotFound，实际 %v", err)
	}

	// 4. 耗尽 → usable=false（remaining=0）
	exhaustedID := seedEntitlement(t, db, dbTestUserA, "1000", "1000")
	res4, err := svc.GetEntitlementBalance(ctx, exhaustedID, dbTestUserA)
	if err != nil {
		t.Fatalf("查询耗尽权益失败: %v", err)
	}
	if res4.Usable {
		t.Fatalf("耗尽权益应 usable=false")
	}
	if res4.Remaining == nil || !res4.Remaining.Equal(dec("0")) {
		t.Fatalf("耗尽 remaining 应为 0，实际 %v", res4.Remaining)
	}

	// 5. 已过期 → usable=false
	expiredID := seedExpiredEntitlement(t, db, dbTestUserA, "1000000", "0")
	res5, err := svc.GetEntitlementBalance(ctx, expiredID, dbTestUserA)
	if err != nil {
		t.Fatalf("查询过期权益失败: %v", err)
	}
	if res5.Usable {
		t.Fatalf("过期权益应 usable=false（即使有剩余额度）")
	}
}

// seedExpiredEntitlement 写入一条 active 但 expires_at 已过去的权益，返回其 ID。
func seedExpiredEntitlement(t *testing.T, db *gorm.DB, userID uint64, total, used string) uint64 {
	t.Helper()
	now := time.Now()
	past := now.Add(-24 * time.Hour)
	e := &model.UserEntitlement{
		UserID:          userID,
		AssetID:         1,
		EntitlementType: "token_quota",
		ProductID:       1,
		QuotaTotal:      decPtr(total),
		QuotaUsed:       dec(used),
		QuotaUnit:       strPtr("tokens"),
		Status:          "active",
		StartedAt:       &past,
		ExpiresAt:       &past,
	}
	if err := db.Create(e).Error; err != nil {
		t.Fatalf("写过期权益失败: %v", err)
	}
	return e.ID
}
