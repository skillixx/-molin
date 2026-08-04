package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
)

type memoryOutboxDeadRequeuer struct {
	eventID string
	now     time.Time
}

func (r *memoryOutboxDeadRequeuer) RequeueDead(_ context.Context, eventID string, now time.Time) error {
	r.eventID, r.now = eventID, now
	return nil
}

func TestValidSafetyRulesRequiresAllCategoriesAndBoundedKeywords(t *testing.T) {
	categories := []string{"illegal", "sexual", "gambling", "drugs", "terror", "hate", "self_harm"}
	rules := make([]safetyRule, 0, len(categories))
	for _, category := range categories {
		rules = append(rules, safetyRule{Code: category + "-001", Category: category, Keywords: []string{category}})
	}
	raw, _ := json.Marshal(rules)
	if !validSafetyRules(raw) {
		t.Fatal("覆盖七类且关键词合法的安全策略应通过校验")
	}
	missing, _ := json.Marshal(rules[:len(rules)-1])
	if validSafetyRules(missing) {
		t.Fatal("缺少任一必需类别的安全策略不得创建")
	}
	rules[0].Keywords = []string{string(make([]rune, maxSafetyKeywordRunes+1))}
	tooLong, _ := json.Marshal(rules)
	if validSafetyRules(tooLong) {
		t.Fatal("超过流式审核重叠上限的关键词不得创建")
	}
}

func TestGovernanceAdminRejectsPartialNegativeBudget(t *testing.T) {
	daily := decimal.NewFromInt(10)
	negative := decimal.NewFromInt(-1)
	svc := NewGovernanceAdminService(nil)
	err := svc.PutBudgetPolicy(context.Background(), 1, model.AIBudgetPolicy{
		ScopeType: "project", ScopeID: 1, Mode: model.AIBudgetHard, DailyLimit: &daily, MonthlyLimit: &negative,
	}, 1)
	if !IsValidation(err) {
		t.Fatalf("任一非空限额不是正数时必须拒绝: %v", err)
	}
}

func TestGovernanceAdminRequeuesDeadOutbox(t *testing.T) {
	outbox := &memoryOutboxDeadRequeuer{}
	svc := NewGovernanceAdminService(nil).WithOutboxDeadRequeuer(outbox)
	fixed := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixed }
	if err := svc.RequeueDeadOutbox(context.Background(), " req-1:billing_settled ", "核对钱包与请求终态后重试"); err != nil {
		t.Fatal(err)
	}
	if outbox.eventID != "req-1:billing_settled" || !outbox.now.Equal(fixed) {
		t.Fatalf("死信重试参数错误: event=%s now=%s", outbox.eventID, outbox.now)
	}
	if err := svc.RequeueDeadOutbox(context.Background(), "req-2:billing", " "); !IsValidation(err) {
		t.Fatalf("空重试原因必须拒绝: %v", err)
	}
}
