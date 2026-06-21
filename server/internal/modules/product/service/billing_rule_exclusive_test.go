package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/product/model"
	"molin/server/internal/modules/product/repository"
)

// fakeBillingRuleRepo 计费规则仓库的内存假实现，用于单测按量/按次互斥逻辑，不依赖数据库。
type fakeBillingRuleRepo struct {
	rules  map[uint64]*model.ProductBillingRule
	nextID uint64
}

func newFakeBillingRuleRepo() *fakeBillingRuleRepo {
	return &fakeBillingRuleRepo{rules: make(map[uint64]*model.ProductBillingRule), nextID: 1}
}

func (f *fakeBillingRuleRepo) ListPaged(_ context.Context, _ repository.BillingRuleFilter, _, _ int) ([]model.ProductBillingRule, int64, error) {
	return nil, 0, nil
}

func (f *fakeBillingRuleRepo) FindByID(_ context.Context, id uint64) (*model.ProductBillingRule, error) {
	r, ok := f.rules[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *r
	return &cp, nil
}

func (f *fakeBillingRuleRepo) Create(_ context.Context, rule *model.ProductBillingRule) error {
	rule.ID = f.nextID
	f.nextID++
	cp := *rule
	f.rules[rule.ID] = &cp
	return nil
}

func (f *fakeBillingRuleRepo) Update(_ context.Context, id uint64, updates map[string]interface{}) error {
	r, ok := f.rules[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	if v, ok := updates["usage_type"].(string); ok {
		r.UsageType = v
	}
	if v, ok := updates["status"].(string); ok {
		r.Status = v
	}
	return nil
}

// CountActiveByUsageTypes 复刻仓库语义：统计同一商品下 usage_type 属于集合、status=active、排除 excludeID 的规则数量。
func (f *fakeBillingRuleRepo) CountActiveByUsageTypes(_ context.Context, productID uint64, usageTypes []string, excludeID uint64) (int64, error) {
	set := make(map[string]struct{}, len(usageTypes))
	for _, t := range usageTypes {
		set[t] = struct{}{}
	}
	var cnt int64
	for id, r := range f.rules {
		if id == excludeID {
			continue
		}
		if r.ProductID != productID || r.Status != "active" {
			continue
		}
		if _, ok := set[r.UsageType]; ok {
			cnt++
		}
	}
	return cnt, nil
}

// fakeProductRepo 商品仓库假实现：所有给定商品 ID 均视为存在。
type fakeProductRepo struct{}

func (f *fakeProductRepo) FindByID(_ context.Context, id uint64) (*model.Product, error) {
	return &model.Product{ID: id}, nil
}

// newTestSvc 构造注入了 fake 仓库的服务实例。
func newTestSvc(repo billingRuleRepo) *BillingRuleService {
	return &BillingRuleService{ruleRepo: repo, productRepo: &fakeProductRepo{}}
}

// newActiveRule 构造一条有效计费规则（金额合法、状态 active）。
func newActiveRule(productID uint64, usageType string) *model.ProductBillingRule {
	unit := "tokens"
	if usageType == "calls" {
		unit = "count"
	}
	return &model.ProductBillingRule{
		ProductID:   productID,
		UsageType:   usageType,
		UsageUnit:   unit,
		BillingMode: "postpaid",
		PriceAmount: decimal.RequireFromString("0.010000"),
		Status:      "active",
	}
}

// TestCreate_MeteredExistsRejectCalls 已存在生效按量规则时，再加按次规则应被拒绝（40000）。
func TestCreate_MeteredExistsRejectCalls(t *testing.T) {
	repo := newFakeBillingRuleRepo()
	svc := newTestSvc(repo)
	ctx := context.Background()

	if err := svc.Create(ctx, newActiveRule(100, "input_tokens")); err != nil {
		t.Fatalf("先建按量规则不应失败：%v", err)
	}
	err := svc.Create(ctx, newActiveRule(100, "calls"))
	if !errors.Is(err, ErrMeteredRuleExists) {
		t.Fatalf("已有按量再加按次应返回 ErrMeteredRuleExists，实际：%v", err)
	}
}

// TestCreate_CallsExistsRejectMetered 反向：已存在生效按次规则时，再加按量规则应被拒绝。
func TestCreate_CallsExistsRejectMetered(t *testing.T) {
	repo := newFakeBillingRuleRepo()
	svc := newTestSvc(repo)
	ctx := context.Background()

	if err := svc.Create(ctx, newActiveRule(100, "calls")); err != nil {
		t.Fatalf("先建按次规则不应失败：%v", err)
	}
	// output_tokens 同属按量类
	err := svc.Create(ctx, newActiveRule(100, "output_tokens"))
	if !errors.Is(err, ErrCallRuleExists) {
		t.Fatalf("已有按次再加按量应返回 ErrCallRuleExists，实际：%v", err)
	}
}

// TestCreate_DifferentProductNotExclusive 不同商品互不影响：商品A按量、商品B按次均可创建。
func TestCreate_DifferentProductNotExclusive(t *testing.T) {
	repo := newFakeBillingRuleRepo()
	svc := newTestSvc(repo)
	ctx := context.Background()

	if err := svc.Create(ctx, newActiveRule(100, "input_tokens")); err != nil {
		t.Fatalf("商品100按量不应失败：%v", err)
	}
	if err := svc.Create(ctx, newActiveRule(200, "calls")); err != nil {
		t.Fatalf("不同商品200按次不应受商品100影响：%v", err)
	}
}

// TestCreate_SameMeteredTypesCoexist 同为按量类（input + output）可共存，不触发互斥。
func TestCreate_SameMeteredTypesCoexist(t *testing.T) {
	repo := newFakeBillingRuleRepo()
	svc := newTestSvc(repo)
	ctx := context.Background()

	if err := svc.Create(ctx, newActiveRule(100, "input_tokens")); err != nil {
		t.Fatalf("input_tokens 不应失败：%v", err)
	}
	if err := svc.Create(ctx, newActiveRule(100, "output_tokens")); err != nil {
		t.Fatalf("同属按量类的 output_tokens 应可共存，实际：%v", err)
	}
}

// TestCreate_InactiveMeteredAllowsCalls 已存在的按量规则若为 inactive，则不阻止新建按次。
func TestCreate_InactiveMeteredAllowsCalls(t *testing.T) {
	repo := newFakeBillingRuleRepo()
	svc := newTestSvc(repo)
	ctx := context.Background()

	inactive := newActiveRule(100, "input_tokens")
	inactive.Status = "inactive"
	if err := svc.Create(ctx, inactive); err != nil {
		t.Fatalf("建 inactive 按量规则不应失败：%v", err)
	}
	if err := svc.Create(ctx, newActiveRule(100, "calls")); err != nil {
		t.Fatalf("仅存在 inactive 按量规则时应允许新建按次，实际：%v", err)
	}
}

// TestCreate_InactiveCallsNotChecked 新建一条 inactive 的按次规则（即便已有生效按量）不触发互斥。
func TestCreate_InactiveCallsNotChecked(t *testing.T) {
	repo := newFakeBillingRuleRepo()
	svc := newTestSvc(repo)
	ctx := context.Background()

	if err := svc.Create(ctx, newActiveRule(100, "input_tokens")); err != nil {
		t.Fatalf("建按量规则不应失败：%v", err)
	}
	inactiveCalls := newActiveRule(100, "calls")
	inactiveCalls.Status = "inactive"
	if err := svc.Create(ctx, inactiveCalls); err != nil {
		t.Fatalf("新建 inactive 按次规则不应触发互斥，实际：%v", err)
	}
}

// TestUpdate_EnableCallsConflictWithMetered 编辑：把一条 inactive 按次规则启用，
// 若商品已有生效按量规则则应被拒绝。
func TestUpdate_EnableCallsConflictWithMetered(t *testing.T) {
	repo := newFakeBillingRuleRepo()
	svc := newTestSvc(repo)
	ctx := context.Background()

	if err := svc.Create(ctx, newActiveRule(100, "input_tokens")); err != nil {
		t.Fatalf("建按量规则不应失败：%v", err)
	}
	inactiveCalls := newActiveRule(100, "calls")
	inactiveCalls.Status = "inactive"
	if err := svc.Create(ctx, inactiveCalls); err != nil {
		t.Fatalf("建 inactive 按次规则不应失败：%v", err)
	}
	// 启用该按次规则 → 与生效按量冲突
	err := svc.Update(ctx, inactiveCalls.ID, map[string]interface{}{"status": "active"})
	if !errors.Is(err, ErrMeteredRuleExists) {
		t.Fatalf("启用按次规则与生效按量冲突应返回 ErrMeteredRuleExists，实际：%v", err)
	}
}

// TestUpdate_SelfActiveNotConflict 编辑：对一条已生效按次规则改价等更新，
// 不应把自身计入冲突（excludeID 生效）。
func TestUpdate_SelfActiveNotConflict(t *testing.T) {
	repo := newFakeBillingRuleRepo()
	svc := newTestSvc(repo)
	ctx := context.Background()

	rule := newActiveRule(100, "calls")
	if err := svc.Create(ctx, rule); err != nil {
		t.Fatalf("建按次规则不应失败：%v", err)
	}
	// 仅更新状态为 active（保持），不应因自身被判冲突
	if err := svc.Update(ctx, rule.ID, map[string]interface{}{"status": "active"}); err != nil {
		t.Fatalf("更新自身不应触发互斥，实际：%v", err)
	}
}

// TestExclusiveDecision 纯函数判定表：覆盖对立类计数为 0 / >0 的各组合。
func TestExclusiveDecision(t *testing.T) {
	cases := []struct {
		name          string
		usageType     string
		oppositeCount int64
		wantErr       error
	}{
		{"按次-无对立按量-放行", "calls", 0, nil},
		{"按次-有对立按量-拒绝", "calls", 2, ErrMeteredRuleExists},
		{"按量input-无对立按次-放行", "input_tokens", 0, nil},
		{"按量input-有对立按次-拒绝", "input_tokens", 1, ErrCallRuleExists},
		{"按量output-有对立按次-拒绝", "output_tokens", 1, ErrCallRuleExists},
		{"未知类型-不参与互斥", "storage", 5, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := exclusiveDecision(c.usageType, c.oppositeCount)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("usageType=%s oppositeCount=%d 期望 %v，实际 %v", c.usageType, c.oppositeCount, c.wantErr, err)
			}
		})
	}
}
