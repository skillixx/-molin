package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
)

type videoBudgetCommitPool struct {
	gorm.ConnPool
	lost atomic.Bool
}

func (p *videoBudgetCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &videoBudgetCommitTx{ConnPool: tx, tx: tx, pool: p}, nil
}

type videoBudgetCommitTx struct {
	gorm.ConnPool
	tx          *sql.Tx
	pool        *videoBudgetCommitPool
	budgetWrite bool
}

func (t *videoBudgetCommitTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if strings.Contains(strings.ToLower(query), "ai_budget_reservations") && strings.Contains(strings.ToLower(query), "insert") {
		t.budgetWrite = true
	}
	return t.tx.ExecContext(ctx, query, args...)
}
func (t *videoBudgetCommitTx) Commit() error {
	if err := t.tx.Commit(); err != nil {
		return err
	}
	if t.budgetWrite && t.pool.lost.CompareAndSwap(false, true) {
		return errors.New("合成预算生成COMMIT确认丢失")
	}
	return nil
}
func (t *videoBudgetCommitTx) Rollback() error { return t.tx.Rollback() }

func TestVideoG6BudgetAdmissionProjectHardLimitMySQL(t *testing.T) {
	f := newVideoG6I2VFixture(t)
	id := f.command.Caller.UserID
	fixedUTC := time.Now().UTC().Truncate(time.Second)
	f.app.billing.now = func() time.Time { return fixedUTC.In(time.FixedZone("fixture-utc8", 8*60*60)) }
	limit := decimal.RequireFromString("0.49000000")
	policy := model.AIBudgetPolicy{ScopeType: "project", ScopeID: id, Mode: model.AIBudgetHard, DailyLimit: &limit, MonthlyLimit: &limit, VersionNo: 1, UpdatedBy: id}
	if err := f.legacy.db.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	command := VideoCommand{Caller: f.command.Caller, IdempotencyKey: "g6-budget-project-hard-0001", Model: f.command.Model, Prompt: "合成预算硬限制", Operation: model.AIVideoOperationTextToVideo, Facade: "openai"}
	counts := func() (int64, int64, int64, int64) {
		var requests, tasks, holds, budgets int64
		_ = f.legacy.db.Table("ai_requests").Where("user_id=?", id).Count(&requests).Error
		_ = f.legacy.db.Table("ai_gateway_tasks").Where("user_id=?", id).Count(&tasks).Error
		_ = f.legacy.db.Table("wallet_holds").Where("user_id=?", id).Count(&holds).Error
		_ = f.legacy.db.Table("ai_budget_reservations").Where("user_id=?", id).Count(&budgets).Error
		return requests, tasks, holds, budgets
	}
	r0, t0, h0, b0 := counts()
	if _, err := f.app.Create(t.Context(), command); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("Project hard预算低于0.50必须拒绝: %v", err)
	}
	r1, t1, h1, b1 := counts()
	if r1 != r0 || t1 != t0 || h1 != h0 || b1 != b0 {
		t.Fatalf("预算拒绝留下部分事实: request=%d/%d task=%d/%d hold=%d/%d budget=%d/%d", r0, r1, t0, t1, h0, h1, b0, b1)
	}
	limit = decimal.RequireFromString("0.50000000")
	if err := f.legacy.db.Model(&model.AIBudgetPolicy{}).Where("id=? AND version_no=1", policy.ID).Updates(map[string]any{"daily_limit": limit, "monthly_limit": limit, "version_no": 2}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := f.app.Create(t.Context(), command)
	if err != nil || result == nil || result.HeldAmount != "0.50000000" {
		t.Fatalf("等于预算上限必须成功: result=%+v err=%v", result, err)
	}
	var reservation model.AIBudgetReservation
	if err := f.legacy.db.Where("request_id=?", result.RequestID).Take(&reservation).Error; err != nil || reservation.Status != model.AIBudgetHeld || !reservation.ReservedAmount.Equal(limit) || !reservation.ExpiresAt.Equal(fixedUTC.Add(24*time.Hour)) {
		t.Fatalf("预算预留未与请求同事务形成: %+v err=%v", reservation, err)
	}
	replay, err := f.app.Create(t.Context(), command)
	if err != nil || replay.RequestID != result.RequestID || !replay.Existing {
		t.Fatal("预算请求重放未返回原事实")
	}
	var total int64
	if err := f.legacy.db.Table("ai_budget_reservations").Where("request_id=?", result.RequestID).Count(&total).Error; err != nil || total != 1 {
		t.Fatalf("重放重复预留预算: total=%d err=%v", total, err)
	}
	cancelled, err := f.app.CancelTask(t.Context(), command.Caller, result.Job.ID, "g6-budget-cancel-0001")
	if err != nil || cancelled.BillingStatus != model.AIBillingReleased {
		t.Fatalf("预算请求取消失败: result=%+v err=%v", cancelled, err)
	}
	if err := f.legacy.db.Where("request_id=?", result.RequestID).Take(&reservation).Error; err != nil || reservation.Status != model.AIBudgetReleased || reservation.ReleasedAt == nil || !reservation.ReleasedAt.Equal(fixedUTC) {
		t.Fatalf("钱包释放未同步预算终态: %+v err=%v", reservation, err)
	}
	if err := f.legacy.db.Model(&model.AIBudgetPolicy{}).Where("id=? AND version_no=2", policy.ID).Updates(map[string]any{"mode": model.AIBudgetDisabled, "daily_limit": nil, "monthly_limit": nil, "version_no": 3}).Error; err != nil {
		t.Fatal(err)
	}
	disabled := command
	disabled.IdempotencyKey, disabled.Prompt = "g6-budget-disabled-0001", "合成预算关闭"
	disabledResult, err := f.app.Create(t.Context(), disabled)
	if err != nil {
		t.Fatalf("disabled预算不得阻断: %v", err)
	}
	if err := f.legacy.db.Table("ai_budget_reservations").Where("request_id=?", disabledResult.RequestID).Count(&total).Error; err != nil || total != 0 {
		t.Fatalf("disabled预算不得创建预留: total=%d err=%v", total, err)
	}
	if _, err := f.app.CancelTask(t.Context(), disabled.Caller, disabledResult.Job.ID, "g6-budget-disabled-cancel"); err != nil {
		t.Fatal(err)
	}
	keyLimit := decimal.RequireFromString("0.49000000")
	keyPolicy := model.AIBudgetPolicy{ScopeType: "api_key", ScopeID: id, Mode: model.AIBudgetHard, DailyLimit: &keyLimit, MonthlyLimit: &keyLimit, VersionNo: 1, UpdatedBy: id}
	if err := f.legacy.db.Create(&keyPolicy).Error; err != nil {
		t.Fatal(err)
	}
	keyCommand := command
	keyCommand.IdempotencyKey, keyCommand.Prompt = "g6-budget-key-hard-0001", "合成Key预算硬限制"
	if _, err := f.app.Create(t.Context(), keyCommand); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("API Key hard预算必须独立拒绝: %v", err)
	}
	if err := f.legacy.db.Model(&model.AIBudgetPolicy{}).Where("id=? AND version_no=1", keyPolicy.ID).Updates(map[string]any{"mode": model.AIBudgetSoft, "version_no": 2}).Error; err != nil {
		t.Fatal(err)
	}
	soft, err := f.app.Create(t.Context(), keyCommand)
	if err != nil || soft == nil {
		t.Fatalf("API Key soft超限只告警不得拒绝: result=%+v err=%v", soft, err)
	}
}

func TestVideoG6BudgetAdmissionConcurrentMySQL(t *testing.T) {
	f := newVideoG6I2VFixture(t)
	id := f.command.Caller.UserID
	limit := decimal.RequireFromString("0.50000000")
	if err := f.legacy.db.Create(&model.AIBudgetPolicy{ScopeType: "project", ScopeID: id, Mode: model.AIBudgetHard, DailyLimit: &limit, MonthlyLimit: &limit, VersionNo: 1, UpdatedBy: id}).Error; err != nil {
		t.Fatal(err)
	}
	var winners, rejected atomic.Int32
	var group sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < 100; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			_, err := f.app.Create(t.Context(), VideoCommand{Caller: f.command.Caller, IdempotencyKey: fmt.Sprintf("g6-budget-race-%04d", index), Model: f.command.Model, Prompt: fmt.Sprintf("合成预算竞争%d", index), Operation: model.AIVideoOperationTextToVideo, Facade: "openai"})
			switch {
			case err == nil:
				winners.Add(1)
			case errors.Is(err, ErrBudgetExceeded):
				rejected.Add(1)
			default:
				t.Errorf("预算并发返回异常: %v", err)
			}
		}(index)
	}
	close(start)
	group.Wait()
	if winners.Load() != 1 || rejected.Load() != 99 {
		t.Fatalf("唯一0.50预算槽位竞争错误: winners=%d rejected=%d", winners.Load(), rejected.Load())
	}
	for _, table := range []string{"ai_requests", "ai_gateway_tasks", "wallet_holds", "ai_budget_reservations"} {
		var count int64
		if err := f.legacy.db.Table(table).Where("user_id=?", id).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("%s必须只有一个预算赢家事实: count=%d err=%v", table, count, err)
		}
	}
}

func TestVideoG6BudgetTimezoneDayMonthBoundaryMySQL(t *testing.T) {
	f := newVideoG6I2VFixture(t)
	id := f.command.Caller.UserID
	if err := f.legacy.db.Model(&model.AIProject{}).Where("id=? AND user_id=?", id, id).Update("timezone", "Asia/Shanghai").Error; err != nil {
		t.Fatal(err)
	}
	limit := decimal.RequireFromString("0.50000000")
	if err := f.legacy.db.Create(&model.AIBudgetPolicy{ScopeType: "project", ScopeID: id, Mode: model.AIBudgetHard, DailyLimit: &limit, MonthlyLimit: &limit, VersionNo: 1, UpdatedBy: id}).Error; err != nil {
		t.Fatal(err)
	}
	owner := f.legacy.owner
	clock := time.Date(2026, time.January, 31, 15, 59, 59, 0, time.UTC)
	reserve := func(requestID string) error {
		return f.legacy.db.Transaction(func(tx *gorm.DB) error {
			return f.app.billing.budget.ReserveTx(t.Context(), tx, requestID, owner, limit, func() time.Time { return clock })
		})
	}
	if err := reserve("vid_budget_before_month_boundary"); err != nil {
		t.Fatal(err)
	}
	clock = time.Date(2026, time.January, 31, 16, 0, 1, 0, time.UTC)
	if err := reserve("vid_budget_after_month_boundary"); err != nil {
		t.Fatal(err)
	}
	if err := reserve("vid_budget_same_new_period"); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("同一新日/月周期第三笔必须被hard预算拒绝：%v", err)
	}
	var before, after model.AIBudgetReservation
	if err := f.legacy.db.Where("request_id=?", "vid_budget_before_month_boundary").Take(&before).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.legacy.db.Where("request_id=?", "vid_budget_after_month_boundary").Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	wantBeforeDaily := time.Date(2026, time.January, 30, 16, 0, 0, 0, time.UTC)
	wantBeforeMonthly := time.Date(2025, time.December, 31, 16, 0, 0, 0, time.UTC)
	wantAfterStart := time.Date(2026, time.January, 31, 16, 0, 0, 0, time.UTC)
	if !before.DailyPeriodStart.Equal(wantBeforeDaily) || !before.MonthlyPeriodStart.Equal(wantBeforeMonthly) || !after.DailyPeriodStart.Equal(wantAfterStart) || !after.MonthlyPeriodStart.Equal(wantAfterStart) {
		t.Fatalf("Asia/Shanghai日/月周期边界错误：before=%+v after=%+v", before, after)
	}
	if !before.ExpiresAt.Equal(time.Date(2026, time.February, 1, 15, 59, 59, 0, time.UTC)) || !after.ExpiresAt.Equal(time.Date(2026, time.February, 1, 16, 0, 1, 0, time.UTC)) {
		t.Fatalf("预算恢复期限必须绑定原请求时钟：before=%s after=%s", before.ExpiresAt, after.ExpiresAt)
	}
}

func TestVideoG6BudgetGenerationCommitUnknownMySQL(t *testing.T) {
	f := NewVideoImportHTTPFixture(t)
	id := f.ProjectID
	limit := decimal.RequireFromString("1.00000000")
	if err := f.DB.Create(&model.AIBudgetPolicy{ScopeType: "project", ScopeID: id, Mode: model.AIBudgetHard, DailyLimit: &limit, MonthlyLimit: &limit, VersionNo: 1, UpdatedBy: id}).Error; err != nil {
		t.Fatal(err)
	}
	pool := &videoBudgetCommitPool{ConnPool: f.DB.ConnPool}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: f.DB.Logger})
	if err != nil {
		t.Fatal(err)
	}
	app := f.WithDB(db)
	command := VideoCommand{Caller: VideoCaller{UserID: id, APIKeyID: id}, IdempotencyKey: "g6-budget-commit-create-0001", Model: f.Model, Prompt: "合成预算提交确认未知", Operation: model.AIVideoOperationTextToVideo, Facade: "openai"}
	var holdsBefore int64
	if err := f.DB.Table("wallet_holds").Where("user_id=?", id).Count(&holdsBefore).Error; err != nil {
		t.Fatal(err)
	}
	result, commitErr := app.Create(t.Context(), command)
	if result != nil || commitErr == nil || !pool.lost.Load() {
		t.Fatalf("预算事务必须真实提交后丢失确认：result=%+v err=%v lost=%t", result, commitErr, pool.lost.Load())
	}
	read := func() (int64, int64, int64, int64) {
		var requests, tasks, holds, budgets int64
		_ = f.DB.Table("ai_requests").Where("user_id=? AND modality='video'", id).Count(&requests).Error
		_ = f.DB.Table("ai_gateway_tasks").Where("user_id=? AND capability='video.generate'", id).Count(&tasks).Error
		_ = f.DB.Table("wallet_holds").Where("user_id=?", id).Count(&holds).Error
		_ = f.DB.Table("ai_budget_reservations").Where("user_id=?", id).Count(&budgets).Error
		return requests, tasks, holds, budgets
	}
	r1, t1, h1, b1 := read()
	if r1 != 1 || t1 != 1 || h1 != holdsBefore+1 || b1 != 1 {
		t.Fatalf("提交未知必须保留唯一生成与预算事实：request=%d task=%d hold=%d/%d budget=%d", r1, t1, h1, holdsBefore, b1)
	}
	replayed, err := app.Create(t.Context(), command)
	if err != nil || replayed == nil || !replayed.Existing || replayed.Job.ID == "" {
		t.Fatalf("预算提交未知重放必须恢复原Job：result=%+v err=%v", replayed, err)
	}
	r2, t2, h2, b2 := read()
	if r2 != r1 || t2 != t1 || h2 != h1 || b2 != b1 {
		t.Fatalf("预算提交未知重放不得重复事实：before=%d/%d/%d/%d after=%d/%d/%d/%d", r1, t1, h1, b1, r2, t2, h2, b2)
	}
}
