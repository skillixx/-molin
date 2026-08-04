package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

type memoryBudgetRepository struct {
	steps       *[]string
	reserveErr  error
	releaseErr  error
	reserved    bool
	released    int
	synced      int
	compensated int
}

func (r *memoryBudgetRepository) ReserveBudget(_ context.Context, _ repository.BudgetReservationRequest) (*model.AIBudgetReservation, error) {
	if r.steps != nil {
		*r.steps = append(*r.steps, "budget")
	}
	if r.reserveErr != nil {
		return nil, r.reserveErr
	}
	if !r.reserved {
		return nil, nil
	}
	return &model.AIBudgetReservation{ID: 1, Status: model.AIBudgetHeld}, nil
}
func (r *memoryBudgetRepository) ReleaseBudget(context.Context, string) error {
	r.released++
	return r.releaseErr
}

func TestGovernanceRecordsCompensationWhenBudgetReleaseFails(t *testing.T) {
	budget := &memoryBudgetRepository{reserved: true, releaseErr: errors.New("db down")}
	governance := NewGovernanceService(nil, budget, &memoryResourceLimiter{})
	governance.AbortBeforeUpstream(context.Background(), &GovernanceTicket{
		Subject: SafetySubject{RequestID: "req-budget-release-failed"}, Resource: &ResourceTicket{}, BudgetReserved: true,
	})
	if budget.released != 1 || budget.compensated != 1 {
		t.Fatalf("预算释放失败必须进入补偿: released=%d compensated=%d", budget.released, budget.compensated)
	}
}
func (r *memoryBudgetRepository) SyncBudgetFromRequest(context.Context, string) (bool, error) {
	r.synced++
	return true, nil
}
func (r *memoryBudgetRepository) ListHeldBudgetRequestIDs(context.Context, time.Time, int) ([]string, error) {
	return nil, nil
}
func (r *memoryBudgetRepository) RecordCompensationFailure(context.Context, string, string) error {
	r.compensated++
	return nil
}

type memoryResourceLimiter struct {
	steps      *[]string
	acquireErr error
	released   int
	reconciled uint64
}

func (l *memoryResourceLimiter) Acquire(_ context.Context, requestID string, _, _, _ uint64, _ string, tokens uint64) (*ResourceTicket, error) {
	if l.steps != nil {
		*l.steps = append(*l.steps, "resource")
	}
	if l.acquireErr != nil {
		return nil, l.acquireErr
	}
	return &ResourceTicket{LeaseID: requestID, ReservedTPM: tokens}, nil
}
func (l *memoryResourceLimiter) Renew(context.Context, *ResourceTicket) error { return nil }
func (l *memoryResourceLimiter) Release(context.Context, *ResourceTicket) error {
	l.released++
	return nil
}
func (l *memoryResourceLimiter) ReconcileTokens(_ context.Context, _ *ResourceTicket, actual uint64) error {
	l.reconciled = actual
	return nil
}
func (l *memoryResourceLimiter) StartHeartbeat(context.Context, *ResourceTicket) <-chan error {
	result := make(chan error)
	close(result)
	return result
}

func TestGovernanceAdmitOrderAndResourceFailureCompensation(t *testing.T) {
	steps := []string{}
	safetyRepo := &memorySafetyRepository{policy: testSafetyPolicy(t)}
	safety := NewSafetyService(safetyRepo, "0123456789abcdef0123456789abcdef")
	budget := &memoryBudgetRepository{steps: &steps, reserved: true}
	limiter := &memoryResourceLimiter{steps: &steps, acquireErr: ErrConcurrencyExceeded}
	governance := NewGovernanceService(safety, budget, limiter)
	subject := SafetySubject{RequestID: "req-governance-1", UserID: 1, ProjectID: 2, APIKeyID: 3}
	body := map[string]interface{}{"messages": []interface{}{map[string]interface{}{"role": "user", "content": "正常问题"}}}
	decision, err := governance.CheckInput(context.Background(), subject, body)
	if err != nil {
		t.Fatal(err)
	}
	steps = append(steps, "safety")
	_, err = governance.AdmitAfterSafety(context.Background(), subject, "Asia/Shanghai", body, &PriceQuote{HeldAmount: decimal.NewFromInt(1), MaxTokens: 8, Snapshot: PriceSnapshot{LogicalModelCode: "molin/test"}}, decision)
	if !errors.Is(err, ErrConcurrencyExceeded) {
		t.Fatalf("资源拒绝错误不正确: %v", err)
	}
	want := []string{"safety", "budget", "resource"}
	if len(steps) != len(want) {
		t.Fatalf("治理顺序不正确: %v", steps)
	}
	for index := range want {
		if steps[index] != want[index] {
			t.Fatalf("治理顺序不正确: %v", steps)
		}
	}
	if budget.released != 1 {
		t.Fatalf("资源拒绝后必须释放预算预留，实际 %d", budget.released)
	}
}

func TestGovernanceHardBudgetStopsBeforeResource(t *testing.T) {
	safetyRepo := &memorySafetyRepository{policy: testSafetyPolicy(t)}
	budget := &memoryBudgetRepository{reserveErr: repository.ErrBudgetLimitExceeded}
	limiter := &memoryResourceLimiter{}
	governance := NewGovernanceService(NewSafetyService(safetyRepo, "0123456789abcdef0123456789abcdef"), budget, limiter)
	subject := SafetySubject{RequestID: "req-governance-2", UserID: 1, ProjectID: 2, APIKeyID: 3}
	body := map[string]interface{}{"messages": []interface{}{map[string]interface{}{"role": "user", "content": "正常问题"}}}
	decision, _ := governance.CheckInput(context.Background(), subject, body)
	_, err := governance.AdmitAfterSafety(context.Background(), subject, "Asia/Shanghai", body, &PriceQuote{HeldAmount: decimal.NewFromInt(1), MaxTokens: 8, Snapshot: PriceSnapshot{LogicalModelCode: "molin/test"}}, decision)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("硬预算应稳定拒绝，实际 %v", err)
	}
	if limiter.released != 0 {
		t.Fatal("预算拒绝前不得创建资源租约")
	}
}

func TestGovernanceFinishReleasesLeaseAndSyncsBudget(t *testing.T) {
	budget := &memoryBudgetRepository{reserved: true}
	limiter := &memoryResourceLimiter{}
	governance := NewGovernanceService(nil, budget, limiter)
	ticket := &GovernanceTicket{Subject: SafetySubject{RequestID: "req-governance-3"}, Resource: &ResourceTicket{}, BudgetReserved: true}
	governance.FinishExecution(context.Background(), ticket, ExecutionUsage{Present: true, TotalTokens: 17})
	if limiter.released != 1 || limiter.reconciled != 17 || budget.synced != 1 {
		t.Fatalf("终态回收不完整: release=%d tokens=%d sync=%d", limiter.released, limiter.reconciled, budget.synced)
	}
}
