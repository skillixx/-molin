package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func TestG4MySQLBudgetIntegration(t *testing.T) {
	dsn := os.Getenv("G4_MYSQL_DSN")
	if dsn == "" || os.Getenv("G4_ISOLATED_TEST") != "YES" {
		t.Skip("仅在 G4 隔离 MySQL 脚本显式授权时运行")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("连接隔离 MySQL 失败: %v", err)
	}
	repo := repository.NewG4GovernanceRepository(db)
	ctx := context.Background()
	now := time.Now().UTC()
	daily := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monthly := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	cleanupG4BudgetFacts(t, db)
	policy := model.AIBudgetPolicy{
		ScopeType: "project", ScopeID: 1, Mode: model.AIBudgetHard,
		DailyLimit: decimalPointerForG4(decimal.NewFromInt(10)), VersionNo: 1, UpdatedBy: 1,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	var loadedPolicy model.AIBudgetPolicy
	if err := db.Where("scope_type = 'project' AND scope_id = 1").First(&loadedPolicy).Error; err != nil || loadedPolicy.DailyLimit == nil || loadedPolicy.Mode != model.AIBudgetHard {
		t.Fatalf("预算策略写入后必须可读: policy=%+v err=%v", loadedPolicy, err)
	}

	t.Run("多SK并发共享Project硬预算且阈值幂等", func(t *testing.T) {
		var admitted atomic.Int64
		var rejected atomic.Int64
		var unexpected atomic.Int64
		start := make(chan struct{})
		var wg sync.WaitGroup
		for index := 0; index < 100; index++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				keyID := uint64(1 + index%2)
				reservation, reserveErr := repo.ReserveBudget(ctx, repository.BudgetReservationRequest{
					RequestID: fmt.Sprintf("g4-budget-%03d", index), UserID: 1, ProjectID: 1, APIKeyID: keyID,
					Amount: decimal.NewFromInt(1), DailyPeriodStart: daily, MonthlyPeriodStart: monthly, ExpiresAt: now.Add(time.Hour),
				})
				switch {
				case reserveErr == nil && reservation != nil:
					admitted.Add(1)
				case reserveErr == repository.ErrBudgetLimitExceeded:
					rejected.Add(1)
				default:
					unexpected.Add(1)
				}
			}(index)
		}
		close(start)
		wg.Wait()
		if admitted.Load() != 10 || rejected.Load() != 90 || unexpected.Load() != 0 {
			t.Fatalf("预算并发结果异常: admitted=%d rejected=%d unexpected=%d", admitted.Load(), rejected.Load(), unexpected.Load())
		}
		var held decimal.Decimal
		if err := db.Model(&model.AIBudgetReservation{}).Select("COALESCE(SUM(reserved_amount),0)").Where("status = ?", model.AIBudgetHeld).Scan(&held).Error; err != nil {
			t.Fatal(err)
		}
		if !held.Equal(decimal.NewFromInt(10)) {
			t.Fatalf("Project 预算不得超卖，held=%s", held)
		}
		var thresholds []uint64
		if err := db.Model(&model.AIBudgetAlert{}).Order("threshold_percent").Pluck("threshold_percent", &thresholds).Error; err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(thresholds) != "[80 90 100]" {
			t.Fatalf("80/90/100 阈值必须各生成一次，实际 %v", thresholds)
		}
	})

	t.Run("释放幂等且软预算不阻断", func(t *testing.T) {
		// 并发胜出者是不确定的，先读取一条真实 held 记录，避免测试把被拒绝的固定请求误当成已预留。
		var heldRequestID string
		if err := db.Model(&model.AIBudgetReservation{}).Where("status = ?", model.AIBudgetHeld).Order("id ASC").Limit(1).Pluck("request_id", &heldRequestID).Error; err != nil || heldRequestID == "" {
			t.Fatalf("读取待释放预留失败: request_id=%s err=%v", heldRequestID, err)
		}
		if err := repo.ReleaseBudget(ctx, heldRequestID); err != nil {
			t.Fatal(err)
		}
		if err := repo.ReleaseBudget(ctx, heldRequestID); err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AIBudgetPolicy{}).Where("scope_type = 'project' AND scope_id = 1").Updates(map[string]interface{}{
			"mode": model.AIBudgetSoft, "daily_limit": decimal.NewFromInt(1), "version_no": gorm.Expr("version_no + 1"),
		}).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ReserveBudget(ctx, repository.BudgetReservationRequest{
			RequestID: "g4-budget-soft", UserID: 1, ProjectID: 1, APIKeyID: 1,
			Amount: decimal.NewFromInt(100), DailyPeriodStart: daily, MonthlyPeriodStart: monthly, ExpiresAt: now.Add(time.Hour),
		}); err != nil {
			t.Fatalf("soft 预算只能提醒不能阻断: %v", err)
		}
	})

	t.Run("过期孤立预留自动释放且补偿任务可人工处置", func(t *testing.T) {
		reservation := model.AIBudgetReservation{
			RequestID: "g4-expired-without-request", UserID: 1, ProjectID: 1, APIKeyID: 1,
			ReservedAmount: decimal.NewFromInt(1), Status: model.AIBudgetHeld,
			DailyPeriodStart: daily, MonthlyPeriodStart: monthly, ExpiresAt: now.Add(-time.Minute),
		}
		if err := db.Create(&reservation).Error; err != nil {
			t.Fatal(err)
		}
		settled, err := repo.SyncBudgetFromRequest(ctx, reservation.RequestID)
		if err != nil || !settled {
			t.Fatalf("没有 G3 请求事实的过期预留应安全释放: settled=%v err=%v", settled, err)
		}
		var status string
		if err := db.Model(&model.AIBudgetReservation{}).Where("request_id = ?", reservation.RequestID).Pluck("status", &status).Error; err != nil || status != model.AIBudgetExpired {
			t.Fatalf("过期预留状态错误: status=%s err=%v", status, err)
		}

		brokenReservation := model.AIBudgetReservation{
			RequestID: "g4-broken-request", UserID: 1, ProjectID: 1, APIKeyID: 1,
			ReservedAmount: decimal.NewFromInt(1), Status: model.AIBudgetHeld,
			DailyPeriodStart: daily, MonthlyPeriodStart: monthly, ExpiresAt: now.Add(time.Hour),
		}
		if err := db.Create(&brokenReservation).Error; err != nil {
			t.Fatal(err)
		}
		if err := repo.RecordCompensationFailure(ctx, "g4-broken-request", "budget_sync_failed"); err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AICompensationTask{}).Where("task_key = ?", "budget:g4-broken-request").Update("next_retry_at", now.Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
		dueIDs, err := repo.ListHeldBudgetRequestIDs(ctx, now, 20)
		if err != nil {
			t.Fatal(err)
		}
		foundDue := false
		for _, requestID := range dueIDs {
			foundDue = foundDue || requestID == "g4-broken-request"
		}
		if !foundDue {
			t.Fatal("补偿任务到期后必须立即重试，不能等待预算预留过期")
		}
		for index := 1; index < 8; index++ {
			if err := repo.RecordCompensationFailure(ctx, "g4-broken-request", "budget_sync_failed"); err != nil {
				t.Fatal(err)
			}
		}
		items, total, err := repo.ListCompensationTasks(ctx, 0, 20)
		if err != nil || total != 1 || len(items) != 1 || items[0].Status != "dead" || items[0].RetryCount != 8 {
			t.Fatalf("达到重试上限后必须进入 dead: items=%+v total=%d err=%v", items, total, err)
		}
		staleUpdatedAt := items[0].UpdatedAt.Add(-time.Second)
		if err := repo.ResolveCompensationTask(ctx, items[0].ID, staleUpdatedAt, "manual_review"); err != repository.ErrRequestStateConflict {
			t.Fatalf("过期乐观锁必须拒绝覆盖: %v", err)
		}
		if err := repo.ResolveCompensationTask(ctx, items[0].ID, items[0].UpdatedAt, "manual_review"); err != nil {
			t.Fatalf("dead 任务应可转人工处理: %v", err)
		}
		if err := repo.RecordCompensationFailure(ctx, "g4-broken-request", "budget_sync_failed"); err != nil {
			t.Fatal(err)
		}
		items, _, err = repo.ListCompensationTasks(ctx, 0, 20)
		if err != nil || items[0].Status != "manual_review" || items[0].RetryCount != 8 {
			t.Fatalf("人工接管后自动失败记录不得覆盖状态或重试次数: items=%+v err=%v", items, err)
		}
		heldIDs, err := repo.ListHeldBudgetRequestIDs(ctx, now, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, requestID := range heldIDs {
			if requestID == "g4-broken-request" {
				t.Fatal("dead 或 manual_review 补偿任务不得再次进入自动恢复扫描")
			}
		}
	})

	t.Run("禁用资源策略不能绕过状态过滤", func(t *testing.T) {
		if err := db.Exec("DELETE FROM ai_resource_policies").Error; err != nil {
			t.Fatal(err)
		}
		policies := []model.AIResourcePolicy{
			{ScopeType: "user", ScopeKey: "1", ConcurrencyLimit: 3, RPMLimit: 30, TPMLimit: 3000, Status: "active", VersionNo: 1, UpdatedBy: 1},
			{ScopeType: "project", ScopeKey: "1", ConcurrencyLimit: 99, RPMLimit: 99, TPMLimit: 9999, Status: "disabled", VersionNo: 1, UpdatedBy: 1},
		}
		if err := db.Create(&policies).Error; err != nil {
			t.Fatal(err)
		}
		loaded, err := repo.LoadResourcePolicies(ctx, map[string]string{"user": "1", "project": "1"})
		if err != nil {
			t.Fatal(err)
		}
		if len(loaded) != 1 || loaded["user"].ConcurrencyLimit != 3 {
			t.Fatalf("只能读取匹配的启用策略: %+v", loaded)
		}
		if _, exists := loaded["project"]; exists {
			t.Fatal("禁用策略不得参与资源限制覆盖")
		}
	})

	t.Run("用户安全事件隔离且申诉校验归属", func(t *testing.T) {
		var policy model.AISafetyPolicyVersion
		policyErr := db.Where("status = ?", model.AISafetyPolicyActive).First(&policy).Error
		if policyErr == gorm.ErrRecordNotFound {
			// 全新库执行 migration 时尚无用户，运维必须在首个管理员创建后显式发布安全策略。
			operatorID := uint64(1)
			policy = model.AISafetyPolicyVersion{
				VersionNo: 100, Status: model.AISafetyPolicyActive, RefusalMessage: DefaultSafetyRefusal,
				RulesJSON: []byte(`[{"code":"illegal-001","category":"illegal","keywords":["blocked"]}]`),
				CreatedBy: operatorID, ApprovedBy: &operatorID, EffectiveAt: &now,
			}
			policyErr = db.Create(&policy).Error
		}
		if policyErr != nil {
			t.Fatalf("创建隔离测试活动安全策略失败: %v", policyErr)
		}
		events := []model.AISafetyEvent{
			{EventID: "g4-event-owner", RequestID: "g4-safe-owner", UserID: 1, ProjectID: 1, APIKeyID: 1, Direction: "input", Category: "illegal", RuleCode: "illegal-001", PolicyVersionID: policy.ID, ContentDigest: strings.Repeat("a", 64), Action: "reject", Result: "blocked"},
			{EventID: "g4-event-other", RequestID: "g4-safe-other", UserID: 2, ProjectID: 1, APIKeyID: 2, Direction: "input", Category: "illegal", RuleCode: "illegal-001", PolicyVersionID: policy.ID, ContentDigest: strings.Repeat("b", 64), Action: "reject", Result: "blocked"},
		}
		if err := db.Create(&events).Error; err != nil {
			t.Fatal(err)
		}
		items, total, err := repo.ListUserSafetyEvents(ctx, 1, 0, 20)
		if err != nil || total != 1 || len(items) != 1 || items[0].EventID != "g4-event-owner" {
			t.Fatalf("用户事件列表必须严格隔离: items=%+v total=%d err=%v", items, total, err)
		}
		if err := repo.CreateAppeal(ctx, &model.AISafetyAppeal{EventID: "g4-event-other", UserID: 1, Reason: "非本人事件", Status: "pending", VersionNo: 1}); err != gorm.ErrRecordNotFound {
			t.Fatalf("不得申诉其他用户事件: %v", err)
		}
		if err := repo.CreateAppeal(ctx, &model.AISafetyAppeal{EventID: "g4-event-owner", UserID: 1, Reason: "请求复核", Status: "pending", VersionNo: 1}); err != nil {
			t.Fatalf("本人事件应允许申诉: %v", err)
		}
	})
}

func cleanupG4BudgetFacts(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, table := range []string{"ai_compensation_tasks", "ai_budget_alerts", "ai_budget_reservations", "ai_budget_overrides", "ai_budget_policies"} {
		if err := db.Exec("DELETE FROM " + table).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func decimalPointerForG4(value decimal.Decimal) *decimal.Decimal { return &value }
