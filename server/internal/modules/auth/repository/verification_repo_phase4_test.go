package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/auth/model"
)

func TestPhase4VerificationConsumptionRequiresAcceptedStatus(t *testing.T) {
	dialector := mysql.New(mysql.Config{
		DSN:                       "phase4:phase4@tcp(127.0.0.1:1)/phase4?charset=utf8mb4&parseTime=True&loc=UTC",
		SkipInitializeWithVersion: true,
	})
	db, err := gorm.Open(dialector, &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("初始化只读 SQL 验收器失败: %v", err)
	}

	sql := db.ToSQL(func(tx *gorm.DB) *gorm.DB {
		repo := NewVerificationRepository(tx)
		return repo.verificationConsumptionQuery(context.Background(), "email", strings.Repeat("a", 64), "register", strings.Repeat("b", 64), time.Now()).
			Update("used_at", time.Now())
	})
	normalized := strings.ToLower(strings.ReplaceAll(sql, "`", ""))
	if !strings.Contains(normalized, "send_status") || !strings.Contains(normalized, "accepted") {
		t.Fatal("验证码消费 SQL 必须显式限定 send_status=accepted，pending/failed 不得认证")
	}
	if !strings.Contains(normalized, "used_at is null") || !strings.Contains(normalized, "expires_at >") {
		t.Fatal("验证码消费 SQL 必须同时限定未使用且未过期")
	}
}

func TestVerificationRepositoryUsesUTCUnderShanghaiProcessTimezone(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	originalLocal := time.Local
	time.Local = shanghai
	defer func() { time.Local = originalLocal }()
	dialector := mysql.New(mysql.Config{
		DSN:                       "phase4:phase4@tcp(127.0.0.1:1)/phase4?charset=utf8mb4&parseTime=True&loc=Local",
		SkipInitializeWithVersion: true,
	})
	db, err := gorm.Open(dialector, &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("初始化只读 SQL 验收器失败: %v", err)
	}
	fixedLocal := time.Date(2026, 7, 27, 20, 0, 0, 0, shanghai)
	repo := NewVerificationRepository(db)
	repo.now = func() time.Time { return fixedLocal }

	nowUTC := repo.nowUTC()
	if nowUTC.Location() != time.UTC || nowUTC.Hour() != 12 {
		t.Fatalf("数据库比较时间必须换算为 UTC: %s", nowUTC)
	}
	sql := db.ToSQL(func(tx *gorm.DB) *gorm.DB {
		dryRepo := NewVerificationRepository(tx)
		return dryRepo.verificationConsumptionQuery(context.Background(), "email", strings.Repeat("a", 64), "register", strings.Repeat("b", 64), nowUTC).
			Update("used_at", databaseWriteDatetimeUTC(nowUTC))
	})
	if !strings.Contains(sql, "2026-07-27 12:00:00") || strings.Contains(sql, "2026-07-27 20:00:00") {
		t.Fatalf("消费 SQL 必须使用 UTC 边界而不是上海墙上时间: %s", sql)
	}

	scannedExpires := time.Date(2026, 7, 27, 12, 10, 0, 0, shanghai)
	scannedAccepted := time.Date(2026, 7, 27, 12, 0, 1, 0, shanghai)
	v := model.VerificationCode{ExpiresAt: scannedExpires, AcceptedAt: &scannedAccepted}
	normalizeVerificationCodeDatabaseUTC(&v)
	if v.ExpiresAt.Location() != time.UTC || v.ExpiresAt.Hour() != 12 || v.AcceptedAt == nil || v.AcceptedAt.Location() != time.UTC || v.AcceptedAt.Hour() != 12 {
		t.Fatalf("扫描出的验证码时间必须保留 UTC 墙上时间: %#v", v)
	}
}

func TestVerificationExpiryBoundaryIsStrict(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 10, 0, 0, time.UTC)
	atBoundary := model.VerificationCode{ExpiresAt: now}
	justAfter := model.VerificationCode{ExpiresAt: now.Add(time.Second)}
	if atBoundary.ExpiresAt.After(now) {
		t.Fatal("恰到过期边界的验证码不得继续有效")
	}
	if !justAfter.ExpiresAt.After(now) {
		t.Fatal("数据库秒级边界后一秒的验证码应仍在有效窗口")
	}
}
