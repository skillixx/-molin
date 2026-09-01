package repository

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	authmodel "molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/token_gateway/model"
)

func TestG6UserRepositoryMySQLIsolation(t *testing.T) {
	dsn := os.Getenv("AI_GATEWAY_G6_MYSQL_DSN")
	if dsn == "" {
		t.Skip("仅在 G6 隔离 MySQL 验收中执行")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewG6UserRepository(db)
	ctx := context.Background()
	budget, err := repo.SumMonthlyBudget(ctx, 965, time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC))
	if err != nil || budget == nil || budget.StringFixed(2) != "105.00" {
		t.Fatalf("预算总览必须计入有效增额并排除过期增额和归档 Project: budget=%v err=%v", budget, err)
	}
	withoutBudget, err := repo.SumMonthlyBudget(ctx, 999999, time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC))
	if err != nil || withoutBudget != nil {
		t.Fatalf("未配置月预算时必须返回空预算而不是查询失败: budget=%v err=%v", withoutBudget, err)
	}
	budgetUsage, err := repo.SumCurrentProjectBudgetUsage(ctx, 965, time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC))
	if err != nil || !budgetUsage.Equal(decimal.NewFromInt(21)) {
		t.Fatalf("预算进度必须按 Project 固化月周期聚合: usage=%s err=%v", budgetUsage, err)
	}
	projectIDFilter := uint64(965)
	rows, total, err := repo.ListRequests(ctx, G6RequestFilter{UserID: 965, ProjectID: &projectIDFilter}, 0, 20)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].RequestID != "req_g6_isolated_965" {
		t.Fatalf("本人请求账本读取错误: total=%d rows=%+v err=%v", total, rows, err)
	}
	if !rows[0].InputTokens.Equal(decimal.NewFromInt(20)) || !rows[0].OutputTokens.Equal(decimal.NewFromInt(5)) {
		t.Fatalf("人工核定后账本必须展示权威用量: input=%s output=%s", rows[0].InputTokens, rows[0].OutputTokens)
	}
	billed, err := repo.ListBilledUsage(ctx, "req_g6_isolated_965")
	if err != nil || len(billed) != 2 || billed[0].Source != "reconciled" || billed[1].Source != "reconciled" {
		t.Fatalf("人工核定后计价明细必须只展示 reconciled 事实: items=%+v err=%v", billed, err)
	}
	aggregate, err := repo.AggregateUsage(ctx, 965, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || !aggregate.InputTokens.Equal(decimal.NewFromInt(20)) || !aggregate.OutputTokens.Equal(decimal.NewFromInt(5)) {
		t.Fatalf("人工核定后总览必须聚合权威用量: aggregate=%+v err=%v", aggregate, err)
	}
	if _, err := repo.FindRequestRow(ctx, 966, "req_g6_isolated_965"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("跨用户读取必须按不存在处理: %v", err)
	}
	if rows, total, err := repo.ListRequests(ctx, G6RequestFilter{UserID: 966}, 0, 20); err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("跨用户列表不得泄漏事实: total=%d rows=%+v err=%v", total, rows, err)
	}
	duplicate := &model.AIBillingDispute{DisputeNo: "DSP-G6-SECOND", RequestID: "req_g6_isolated_965", UserID: 965, Reason: "重复申诉应映射稳定错误", Status: "submitted"}
	if err := repo.CreateDispute(ctx, duplicate); !errors.Is(err, ErrBillingDisputeExists) {
		t.Fatalf("重复申诉必须映射稳定冲突错误: %v", err)
	}

	// Project SK 高风险动作与审计共用事务，审计失败时不得留下密钥或吊销事实。
	g2 := NewG2Repository(db)
	auditFailure := func(*gorm.DB, uint64, []string, *ProjectKeyIdempotency) (uint64, error) {
		return 0, errors.New("模拟审计写入失败")
	}
	projectID := uint64(965)
	failedKey := &authmodel.APIKey{UserID: 965, ProjectID: &projectID, KeyPrefix: "sk-g6-fail", KeyHash: "g6-failed-audit-hash", Name: "审计失败密钥", BillingMode: "postpaid", ScopeMode: "allowlist", Status: "active"}
	if err := g2.CreateProjectKey(ctx, failedKey, nil, auditFailure); err == nil {
		t.Fatal("审计失败时签发事务必须失败")
	}
	var keyCount int64
	if err := db.Model(&authmodel.APIKey{}).Where("key_hash = ?", failedKey.KeyHash).Count(&keyCount).Error; err != nil || keyCount != 0 {
		t.Fatalf("审计失败后不得留下 SK: count=%d err=%v", keyCount, err)
	}
	if err := g2.RevokeProjectKey(ctx, 965, 965, 965, auditFailure); err == nil {
		t.Fatal("审计失败时吊销事务必须失败")
	}
	var keyStatus string
	if err := db.Model(&authmodel.APIKey{}).Where("id = 965").Pluck("status", &keyStatus).Error; err != nil || keyStatus != "active" {
		t.Fatalf("审计失败后原 SK 必须保持 active: status=%s err=%v", keyStatus, err)
	}
}
