package service

import (
	"context"
	"os"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/repository"
)

func TestG6UserServiceMySQLReconciledDetail(t *testing.T) {
	dsn := os.Getenv("AI_GATEWAY_G6_MYSQL_DSN")
	if dsn == "" {
		t.Skip("仅在 G6 隔离 MySQL 验收中执行")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewG6UserService(repository.NewG6UserRepository(db), nil)
	svc.now = func() time.Time { return time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC) }
	detail, err := svc.RequestDetail(context.Background(), 965, "req_g6_isolated_965")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.PriceLines) != 2 {
		t.Fatalf("人工核定详情必须返回两条互斥计价行，实际 %d", len(detail.PriceLines))
	}
	for i := range detail.PriceLines {
		if detail.PriceLines[i].MeterSource != "reconciled" {
			t.Fatalf("人工核定详情必须标识 reconciled 来源，实际 %+v", detail.PriceLines[i])
		}
	}
	overview, err := svc.Overview(context.Background(), 965, "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if overview.MonthlyBudget == nil || overview.MonthlyBudget.StringFixed(2) != "105.00" {
		t.Fatalf("预算总额必须包含有效增额，实际 %+v", overview.MonthlyBudget)
	}
	if overview.MonthlyBudgetUsage == nil || overview.MonthlyBudgetUsage.StringFixed(2) != "20.00" {
		t.Fatalf("无预算 Project 的 500 元消费不得进入预算比例，实际 %+v", overview.MonthlyBudgetUsage)
	}
}
