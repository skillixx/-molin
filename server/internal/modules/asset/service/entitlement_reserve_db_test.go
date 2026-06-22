package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/asset/model"
)

// 预占 DB 集成测试专用高位 user_id（与 consume 测试不冲突）。
const (
	dbTestUserReserve     uint64 = 9_900_001_001
	dbTestUserReserveConc uint64 = 9_900_001_002
	dbTestUserReserveUnl  uint64 = 9_900_001_003
)

// setupReserveDBTest 复用 consume 测试的连库逻辑，额外清理 entitlement_holds。
// 仅在 RUN_DB_TESTS=1 时运行（默认 SKIP，保持 CI 无需 MySQL）。
func setupReserveDBTest(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	gdb, baseClean := setupDBTest(t) // setupDBTest 内部已处理 RUN_DB_TESTS skip
	users := []uint64{dbTestUserReserve, dbTestUserReserveConc, dbTestUserReserveUnl}
	clean := func() {
		gdb.Where("user_id IN ?", users).Delete(&model.EntitlementHold{})
		gdb.Where("user_id IN ?", users).Delete(&model.UserEntitlement{})
		baseClean()
	}
	clean()
	return gdb, clean
}

// seedReserveEntitlement 写入一条 active 有限额权益（含 quota_reserved 初值），返回其 ID。
func seedReserveEntitlement(t *testing.T, db *gorm.DB, userID uint64, total, used, reserved string) uint64 {
	t.Helper()
	now := time.Now()
	exp := now.Add(365 * 24 * time.Hour)
	e := &model.UserEntitlement{
		UserID:          userID,
		AssetID:         1,
		EntitlementType: "token_quota",
		ProductID:       1,
		QuotaTotal:      decPtr(total),
		QuotaUsed:       dec(used),
		QuotaReserved:   dec(reserved),
		QuotaUnit:       strPtr("tokens"),
		Status:          "active",
		StartedAt:       &now,
		ExpiresAt:       &exp,
	}
	if err := db.Create(e).Error; err != nil {
		t.Fatalf("写权益失败: %v", err)
	}
	return e.ID
}

// seedUnlimitedReserveEntitlement 写入一条不限量（quota_total=NULL）active 权益。
func seedUnlimitedReserveEntitlement(t *testing.T, db *gorm.DB, userID uint64) uint64 {
	t.Helper()
	now := time.Now()
	exp := now.Add(365 * 24 * time.Hour)
	e := &model.UserEntitlement{
		UserID:          userID,
		AssetID:         1,
		EntitlementType: "token_quota",
		ProductID:       1,
		QuotaTotal:      nil,
		QuotaUsed:       dec("0"),
		QuotaReserved:   dec("0"),
		Status:          "active",
		StartedAt:       &now,
		ExpiresAt:       &exp,
	}
	if err := db.Create(e).Error; err != nil {
		t.Fatalf("写不限量权益失败: %v", err)
	}
	return e.ID
}

// TestReserveSettle_DB 覆盖：预占占额 / available 不足(60005) / 归属(40003) / 幂等不重复占 /
// settle 多退少补（actual<reserve 退、actual=reserve 全扣）/ release 不计 used。
func TestReserveSettle_DB(t *testing.T) {
	db, cleanup := setupReserveDBTest(t)
	defer cleanup()
	svc := NewAssetService(db)
	ctx := context.Background()

	// total=1000, used=0, reserved=0 -> available=1000
	entID := seedReserveEntitlement(t, db, dbTestUserReserve, "1000", "0", "0")

	// 1. 预占 200 -> reserved=200, available=800
	r1, err := svc.ReserveEntitlement(ctx, entID, dbTestUserReserve, dec("200"), "req_r1:quota_reserve", "")
	if err != nil {
		t.Fatalf("预占失败: %v", err)
	}
	if !r1.Reserved.Equal(dec("200")) || r1.Available == nil || !r1.Available.Equal(dec("800")) {
		t.Fatalf("预占快照错误: reserved=%s available=%v", r1.Reserved, r1.Available)
	}
	assertEnt(t, db, entID, "0", "200")

	// 2. 幂等：同 key 重复预占不二次占（reserved 仍为 200）
	r1b, err := svc.ReserveEntitlement(ctx, entID, dbTestUserReserve, dec("200"), "req_r1:quota_reserve", "")
	if err != nil {
		t.Fatalf("幂等重复预占应成功返回首次结果: %v", err)
	}
	if r1b.HoldID != r1.HoldID {
		t.Fatalf("幂等应返回同一 hold_id，首次=%d 重复=%d", r1.HoldID, r1b.HoldID)
	}
	assertEnt(t, db, entID, "0", "200")
	var holdCount int64
	db.Model(&model.EntitlementHold{}).Where("idempotency_key = ?", "req_r1:quota_reserve").Count(&holdCount)
	if holdCount != 1 {
		t.Fatalf("幂等预占应只有 1 条 hold，实际 %d", holdCount)
	}

	// 3. settle 多退少补：actual=120 < reserve=200 -> used+=120, reserved-=200 -> used=120, reserved=0
	s1, err := svc.SettleEntitlementHold(ctx, r1.HoldID, "", dec("120"))
	if err != nil {
		t.Fatalf("结算失败: %v", err)
	}
	if !s1.SettledAmount.Equal(dec("120")) || s1.Status != model.HoldStatusSettled {
		t.Fatalf("结算快照错误: settled=%s status=%s", s1.SettledAmount, s1.Status)
	}
	assertEnt(t, db, entID, "120", "0")

	// 4. settle 幂等：重复结算不重复扣
	s1b, err := svc.SettleEntitlementHold(ctx, r1.HoldID, "", dec("120"))
	if err != nil {
		t.Fatalf("结算幂等应成功: %v", err)
	}
	if !s1b.SettledAmount.Equal(dec("120")) {
		t.Fatalf("结算幂等 settled 应为 120，实际 %s", s1b.SettledAmount)
	}
	assertEnt(t, db, entID, "120", "0")

	// 5. 预占 300 后结算 actual=500 > reserve=300 -> used 封顶 +300（多退少补封顶）
	r2, err := svc.ReserveEntitlement(ctx, entID, dbTestUserReserve, dec("300"), "req_r2:quota_reserve", "")
	if err != nil {
		t.Fatalf("预占2失败: %v", err)
	}
	assertEnt(t, db, entID, "120", "300")
	s2, err := svc.SettleEntitlementHold(ctx, r2.HoldID, "", dec("500"))
	if err != nil {
		t.Fatalf("结算2失败: %v", err)
	}
	if !s2.SettledAmount.Equal(dec("300")) {
		t.Fatalf("actual>reserve 应封顶到 300，实际 %s", s2.SettledAmount)
	}
	assertEnt(t, db, entID, "420", "0") // used=120+300=420, reserved=0

	// 6. release 不计 used：预占 100 后释放 -> reserved-100, used 不变
	r3, err := svc.ReserveEntitlement(ctx, entID, dbTestUserReserve, dec("100"), "req_r3:quota_reserve", "")
	if err != nil {
		t.Fatalf("预占3失败: %v", err)
	}
	assertEnt(t, db, entID, "420", "100")
	rel, err := svc.ReleaseEntitlementHold(ctx, r3.HoldID, "")
	if err != nil {
		t.Fatalf("释放失败: %v", err)
	}
	if rel.Status != model.HoldStatusReleased || !rel.SettledAmount.Equal(dec("0")) {
		t.Fatalf("释放快照错误: status=%s settled=%s", rel.Status, rel.SettledAmount)
	}
	assertEnt(t, db, entID, "420", "0") // used 仍 420，reserved 归 0

	// 7. available 不足 -> 60005：当前 available = 1000 - 420 - 0 = 580，占 581 失败
	_, err = svc.ReserveEntitlement(ctx, entID, dbTestUserReserve, dec("581"), "req_r4:quota_reserve", "")
	if err != ErrQuotaExceeded {
		t.Fatalf("available 不足应返回 ErrQuotaExceeded，实际 %v", err)
	}

	// 8. 归属不符 -> 40003
	_, err = svc.ReserveEntitlement(ctx, entID, 9_900_009_999, dec("1"), "req_r5:quota_reserve", "")
	if err != ErrEntitlementNotOwned {
		t.Fatalf("归属不符应返回 ErrEntitlementNotOwned，实际 %v", err)
	}
}

// TestReserveUnlimited_DB 不限量（quota_total=NULL）预占恒过，settle 照常累加 used。
func TestReserveUnlimited_DB(t *testing.T) {
	db, cleanup := setupReserveDBTest(t)
	defer cleanup()
	svc := NewAssetService(db)
	ctx := context.Background()

	entID := seedUnlimitedReserveEntitlement(t, db, dbTestUserReserveUnl)

	r, err := svc.ReserveEntitlement(ctx, entID, dbTestUserReserveUnl, dec("999999999"), "req_unl:quota_reserve", "")
	if err != nil {
		t.Fatalf("不限量预占应恒过: %v", err)
	}
	if r.Available != nil {
		t.Fatalf("不限量 available 应为 nil，实际 %v", r.Available)
	}
	// reserved 应记入（占位），used 仍 0
	assertEnt(t, db, entID, "0", "999999999")

	s, err := svc.SettleEntitlementHold(ctx, r.HoldID, "", dec("123"))
	if err != nil {
		t.Fatalf("不限量结算失败: %v", err)
	}
	if !s.SettledAmount.Equal(dec("123")) {
		t.Fatalf("不限量结算 settled 应为 123，实际 %s", s.SettledAmount)
	}
	assertEnt(t, db, entID, "123", "0")
}

// TestReserve_Concurrency_DB 并发预占不超占（available 守恒）：
// total=1000000, 100 并发各预占 10000，恰好占满，reserved=1000000 且无超占。
func TestReserve_Concurrency_DB(t *testing.T) {
	db, cleanup := setupReserveDBTest(t)
	defer cleanup()
	svc := NewAssetService(db)
	ctx := context.Background()

	entID := seedReserveEntitlement(t, db, dbTestUserReserveConc, "1000000", "0", "0")

	const n = 100
	const each = "10000"
	var wg sync.WaitGroup
	var mu sync.Mutex
	var okCount, failCount int

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("req_resv_conc_%d:quota_reserve", i)
			_, err := svc.ReserveEntitlement(ctx, entID, dbTestUserReserveConc, dec(each), key, "")
			mu.Lock()
			if err == nil {
				okCount++
			} else {
				failCount++
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if okCount != n {
		t.Fatalf("100 笔各 10000 恰好占满 1000000，应全部成功，实际 ok=%d fail=%d", okCount, failCount)
	}

	var e model.UserEntitlement
	if err := db.First(&e, entID).Error; err != nil {
		t.Fatalf("查权益失败: %v", err)
	}
	if !e.QuotaReserved.Equal(dec("1000000")) {
		t.Fatalf("并发预占后 quota_reserved=%s，期望 1000000（无超占/重占）", e.QuotaReserved)
	}
	if e.QuotaTotal != nil && e.QuotaUsed.Add(e.QuotaReserved).GreaterThan(*e.QuotaTotal) {
		t.Fatalf("出现超占：used+reserved=%s > total=%s", e.QuotaUsed.Add(e.QuotaReserved), e.QuotaTotal)
	}

	// 超占第 101 笔应失败（available=0）
	_, err := svc.ReserveEntitlement(ctx, entID, dbTestUserReserveConc, dec("1"), "req_resv_over:quota_reserve", "")
	if err != ErrQuotaExceeded {
		t.Fatalf("占满后再占应 ErrQuotaExceeded，实际 %v", err)
	}
}

// assertEnt 断言权益当前 quota_used / quota_reserved。
func assertEnt(t *testing.T, db *gorm.DB, id uint64, wantUsed, wantReserved string) {
	t.Helper()
	var e model.UserEntitlement
	if err := db.First(&e, id).Error; err != nil {
		t.Fatalf("查权益失败: %v", err)
	}
	if !e.QuotaUsed.Equal(dec(wantUsed)) {
		t.Fatalf("quota_used 期望 %s，实际 %s", wantUsed, e.QuotaUsed)
	}
	if !e.QuotaReserved.Equal(dec(wantReserved)) {
		t.Fatalf("quota_reserved 期望 %s，实际 %s", wantReserved, e.QuotaReserved)
	}
}
