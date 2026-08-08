package repository

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

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
	rows, total, err := repo.ListRequests(ctx, G6RequestFilter{UserID: 965}, 0, 20)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].RequestID != "req_g6_isolated_965" {
		t.Fatalf("本人请求账本读取错误: total=%d rows=%+v err=%v", total, rows, err)
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
	auditFailure := func(*gorm.DB, uint64) error { return errors.New("模拟审计写入失败") }
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
