package service

import (
	"context"
	"testing"
	"time"

	"molin/server/internal/modules/asset/model"
)

// TestListActiveEntitlementsByProduct_DB 覆盖第三方 prepaid 解析接口的 service 逻辑：
//   - 仅返回指定商品下的 active 权益；
//   - 他商品的权益被排除；
//   - 过期 / 额度耗尽的以 usable=false 返回（不被过滤掉，交调用方据 usable 取用）；
//   - 该用户在某商品下无权益时返回非 nil 的空切片（序列化为 []）。
//
// 仅在 RUN_DB_TESTS=1 时运行（与同包其它 *_DB 测试一致，CI 默认 SKIP）。
func TestListActiveEntitlementsByProduct_DB(t *testing.T) {
	db, cleanup := setupDBTest(t)
	defer cleanup()
	svc := NewAssetService(db)
	ctx := context.Background()

	now := time.Now()
	future := now.Add(365 * 24 * time.Hour)
	past := now.Add(-1 * time.Hour)

	mk := func(productID uint64, total, used string, expiresAt *time.Time) uint64 {
		e := &model.UserEntitlement{
			UserID:          dbTestUserA,
			AssetID:         1,
			EntitlementType: "token_quota",
			ProductID:       productID,
			QuotaTotal:      decPtr(total),
			QuotaUsed:       dec(used),
			QuotaUnit:       strPtr("tokens"),
			Status:          "active",
			StartedAt:       &now,
			ExpiresAt:       expiresAt,
		}
		if err := db.Create(e).Error; err != nil {
			t.Fatalf("写权益失败: %v", err)
		}
		return e.ID
	}

	id1 := mk(7, "100", "0", &future)     // product=7，可用
	id2 := mk(7, "10", "10", &future)     // product=7，额度耗尽 → usable=false
	idOther := mk(8, "100", "0", &future) // product=8，查询 product=7 时应被排除
	id3 := mk(7, "100", "0", &past)       // product=7，已过期 → usable=false

	got, err := svc.ListActiveEntitlementsByProduct(ctx, dbTestUserA, 7)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("product=7 应返回 3 条 active，实际 %d", len(got))
	}
	usableByID := map[uint64]bool{}
	for _, e := range got {
		usableByID[e.EntitlementID] = e.Usable
	}
	if _, ok := usableByID[idOther]; ok {
		t.Fatalf("他商品权益 %d 不应出现在 product=7 结果中", idOther)
	}
	if !usableByID[id1] {
		t.Fatalf("权益 %d 应 usable=true", id1)
	}
	if usableByID[id2] {
		t.Fatalf("额度耗尽权益 %d 应 usable=false", id2)
	}
	if usableByID[id3] {
		t.Fatalf("过期权益 %d 应 usable=false", id3)
	}

	// 该用户在不存在的商品下无权益 → 返回非 nil 空切片
	empty, err := svc.ListActiveEntitlementsByProduct(ctx, dbTestUserA, 999999)
	if err != nil {
		t.Fatalf("空查询失败: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("无权益应返回非 nil 空切片，实际 %v", empty)
	}
}
