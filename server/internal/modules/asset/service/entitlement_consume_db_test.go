package service

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/asset/model"
)

// 测试专用、不与真实业务冲突的高位 user_id（仅用于本集成测试的行级清理）。
const (
	dbTestUserA    uint64 = 9_900_000_001
	dbTestUserConc uint64 = 9_900_000_002
)

// setupDBTest 直接连本地 molin 开发库（迁移已建好 user_entitlements / entitlement_consume_logs）。
// 仅在显式设置 RUN_DB_TESTS=1 时运行（默认 SKIP，保持 CI 无需 MySQL）。
// 不建/删库（molin 用户无 CREATE DATABASE 权限），仅按测试专用高位 user_id 清理自己写入的行，
// 不触碰真实业务数据。连接参数可用 TEST_MYSQL_* 覆盖。
func setupDBTest(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	if os.Getenv("RUN_DB_TESTS") != "1" {
		t.Skip("跳过 DB 集成测试（设置 RUN_DB_TESTS=1 且本地 MySQL 13306 可用时运行）")
	}

	user := envOr("TEST_MYSQL_USER", "molin")
	pass := envOr("TEST_MYSQL_PASSWORD", "molin_password")
	host := envOr("TEST_MYSQL_HOST", "127.0.0.1")
	port := envOr("TEST_MYSQL_PORT", "13306")
	dbName := envOr("TEST_MYSQL_DATABASE", "molin")

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local", user, pass, host, port, dbName)
	gdb, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("GORM 连接 molin 库失败: %v", err)
	}

	clean := func() {
		gdb.Where("user_id IN ?", []uint64{dbTestUserA, dbTestUserConc}).Delete(&model.EntitlementConsumeLog{})
		gdb.Where("user_id IN ?", []uint64{dbTestUserA, dbTestUserConc}).Delete(&model.UserEntitlement{})
	}
	clean() // 前置清理，避免上次残留
	return gdb, clean
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// seedEntitlement 写入一条 active 的有限额权益，返回其 ID。
func seedEntitlement(t *testing.T, db *gorm.DB, userID uint64, total, used string) uint64 {
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

func strPtr(s string) *string { return &s }

// TestConsumeEntitlementIdempotent_DB 真实事务下覆盖：正常扣减 / 幂等重复不二次扣 / 额度不足(60005) / 归属(40003)。
func TestConsumeEntitlementIdempotent_DB(t *testing.T) {
	db, cleanup := setupDBTest(t)
	defer cleanup()
	svc := NewAssetService(db)
	ctx := context.Background()

	entID := seedEntitlement(t, db, dbTestUserA, "1000000", "5000")

	// 1. 正常扣减 232
	res, err := svc.ConsumeEntitlementIdempotent(ctx, entID, dbTestUserA, dec("232"), "req_a:quota")
	if err != nil {
		t.Fatalf("正常扣减失败: %v", err)
	}
	if !res.QuotaUsed.Equal(dec("5232")) || res.Remaining == nil || !res.Remaining.Equal(dec("994768")) {
		t.Fatalf("扣减后快照错误: used=%s remaining=%v", res.QuotaUsed, res.Remaining)
	}

	// 2. 幂等：同 key 重复扣减不二次扣（quota_used 仍为 5232）
	res2, err := svc.ConsumeEntitlementIdempotent(ctx, entID, dbTestUserA, dec("232"), "req_a:quota")
	if err != nil {
		t.Fatalf("幂等重复扣减应成功返回首次结果: %v", err)
	}
	if !res2.QuotaUsed.Equal(dec("5232")) {
		t.Fatalf("幂等重复不应二次扣减，quota_used=%s 期望 5232", res2.QuotaUsed)
	}
	// 校验 DB 中只有一条日志且 quota_used 未被二次扣
	var logCount int64
	db.Model(&model.EntitlementConsumeLog{}).Where("idempotency_key = ?", "req_a:quota").Count(&logCount)
	if logCount != 1 {
		t.Fatalf("幂等日志应只有 1 条，实际 %d", logCount)
	}

	// 3. 额度不足 → ErrQuotaExceeded（对外 60005）
	_, err = svc.ConsumeEntitlementIdempotent(ctx, entID, dbTestUserA, dec("2000000"), "req_b:quota")
	if err != ErrQuotaExceeded {
		t.Fatalf("额度不足应返回 ErrQuotaExceeded，实际 %v", err)
	}

	// 4. 归属不符 → ErrEntitlementNotOwned（对外 40003）
	_, err = svc.ConsumeEntitlementIdempotent(ctx, entID, 9_900_000_999, dec("1"), "req_c:quota")
	if err != ErrEntitlementNotOwned {
		t.Fatalf("归属不符应返回 ErrEntitlementNotOwned，实际 %v", err)
	}
}

// TestConsumeEntitlement_Concurrency_DB 并发同一权益扣减，验证 SELECT FOR UPDATE 防超扣：
// 100 并发各扣 10000，初始 used=0、total=1000000，恰好用尽且无负余额/超扣。
func TestConsumeEntitlement_Concurrency_DB(t *testing.T) {
	db, cleanup := setupDBTest(t)
	defer cleanup()
	svc := NewAssetService(db)
	ctx := context.Background()

	entID := seedEntitlement(t, db, dbTestUserConc, "1000000", "0")

	const n = 100
	const each = "10000"
	var wg sync.WaitGroup
	var mu sync.Mutex
	var okCount, failCount int

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("req_conc_%d:quota", i)
			_, err := svc.ConsumeEntitlementIdempotent(ctx, entID, dbTestUserConc, dec(each), key)
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
		t.Fatalf("100 笔各 10000 恰好用尽 1000000，应全部成功，实际 ok=%d fail=%d", okCount, failCount)
	}

	var e model.UserEntitlement
	if err := db.First(&e, entID).Error; err != nil {
		t.Fatalf("查权益失败: %v", err)
	}
	if !e.QuotaUsed.Equal(dec("1000000")) {
		t.Fatalf("并发扣减后 quota_used=%s，期望 1000000（无超扣/重扣）", e.QuotaUsed)
	}
	if e.QuotaTotal != nil && e.QuotaUsed.GreaterThan(*e.QuotaTotal) {
		t.Fatalf("出现超扣：used=%s > total=%s", e.QuotaUsed, e.QuotaTotal)
	}
}
