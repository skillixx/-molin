package service

import (
	"context"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var (
	ErrBudgetUnavailable = errors.New("预算服务不可用")
	ErrBudgetExceeded    = errors.New("预算达到硬限制")
)

type budgetRepository interface {
	ReserveBudget(ctx context.Context, req repository.BudgetReservationRequest) (*model.AIBudgetReservation, error)
	ReleaseBudget(ctx context.Context, requestID string) error
	SyncBudgetFromRequest(ctx context.Context, requestID string) (bool, error)
	ListHeldBudgetRequestIDs(ctx context.Context, before time.Time, limit int) ([]string, error)
	RecordCompensationFailure(ctx context.Context, requestID, class string) error
}

type gatewayRejectionRecorder interface {
	RecordGatewayRejection(context.Context, *model.AIGatewayRejectionEvent) error
}

type resourceLimiter interface {
	Acquire(context.Context, string, uint64, uint64, uint64, string, uint64) (*ResourceTicket, error)
	Renew(context.Context, *ResourceTicket) error
	Release(context.Context, *ResourceTicket) error
	ReconcileTokens(context.Context, *ResourceTicket, uint64) error
	StartHeartbeat(context.Context, *ResourceTicket) <-chan error
}

type GovernanceTicket struct {
	Subject           SafetySubject
	Resource          *ResourceTicket
	BudgetReserved    bool
	SafetyPolicyID    uint64
	SafetyPolicyNo    uint64
	SafetyRefusalText string
}

// GovernanceService 按“内容安全 -> 预算 -> Redis 资源租约”顺序准入，任何依赖不确定都失败关闭。
type GovernanceService struct {
	safety  *SafetyService
	budget  budgetRepository
	limiter resourceLimiter
	metrics *AIGatewayMetrics
	now     func() time.Time
}

// WithMetrics 注入安全、预算和资源治理拒绝指标。
func (s *GovernanceService) WithMetrics(metrics *AIGatewayMetrics) *GovernanceService {
	s.metrics = metrics
	return s
}

func NewGovernanceService(safety *SafetyService, budget budgetRepository, limiter resourceLimiter) *GovernanceService {
	return &GovernanceService{safety: safety, budget: budget, limiter: limiter, now: time.Now}
}

func (s *GovernanceService) Admit(ctx context.Context, subject SafetySubject, timezone string, body map[string]interface{}, quote *PriceQuote) (*GovernanceTicket, error) {
	if s == nil || s.safety == nil || s.budget == nil || s.limiter == nil || quote == nil {
		return nil, ErrResourceUnavailable
	}
	decision, err := s.CheckInput(ctx, subject, body)
	if err != nil {
		return nil, err
	}
	return s.AdmitAfterSafety(ctx, subject, timezone, body, quote, decision)
}

func (s *GovernanceService) CheckInput(ctx context.Context, subject SafetySubject, body map[string]interface{}) (*SafetyDecision, error) {
	if s == nil || s.safety == nil {
		return nil, ErrModerationUnavailable
	}
	decision, err := s.safety.ModerateInput(ctx, subject, body)
	if errors.Is(err, ErrContentPolicyViolation) {
		s.recordRejection(ctx, subject, "content_policy_violation", "user", strconv.FormatUint(subject.UserID, 10))
	} else if err != nil {
		// 分类器超时、依赖错误和无法判定都属于失败关闭，不暴露内部错误原文。
		s.recordRejection(ctx, subject, "fail_closed", "user", strconv.FormatUint(subject.UserID, 10))
	}
	return decision, err
}

func (s *GovernanceService) AdmitAfterSafety(ctx context.Context, subject SafetySubject, timezone string, body map[string]interface{}, quote *PriceQuote, decision *SafetyDecision) (*GovernanceTicket, error) {
	if s == nil || s.budget == nil || s.limiter == nil || quote == nil || decision == nil || !decision.Allowed {
		return nil, ErrResourceUnavailable
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, ErrBudgetUnavailable
	}
	now := s.now().In(location)
	daily := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location).UTC()
	monthly := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location).UTC()
	reservation, err := s.budget.ReserveBudget(ctx, repository.BudgetReservationRequest{
		RequestID: subject.RequestID, UserID: subject.UserID, ProjectID: subject.ProjectID, APIKeyID: subject.APIKeyID,
		Amount: quote.HeldAmount, DailyPeriodStart: daily, MonthlyPeriodStart: monthly,
		ExpiresAt: s.now().Add(24 * time.Hour),
	})
	if errors.Is(err, repository.ErrBudgetLimitExceeded) {
		s.recordRejection(ctx, subject, "budget_limit_exceeded", "project", strconv.FormatUint(subject.ProjectID, 10))
		return nil, ErrBudgetExceeded
	}
	if err != nil {
		return nil, ErrBudgetUnavailable
	}
	reservedTokens, err := conservativeTokenReservation(body, quote.MaxTokens)
	if err != nil {
		if reservation != nil {
			s.releaseBudgetOrCompensate(ctx, subject.RequestID)
		}
		return nil, ErrResourceUnavailable
	}
	resource, err := s.limiter.Acquire(ctx, subject.RequestID, subject.UserID, subject.ProjectID, subject.APIKeyID, quote.Snapshot.LogicalModelCode, reservedTokens)
	if err != nil {
		if reservation != nil {
			s.releaseBudgetOrCompensate(ctx, subject.RequestID)
		}
		if limitErr, ok := err.(*ResourceLimitError); ok {
			reason := "concurrency_limit_exceeded"
			if limitErr.LimitType == "rpm" {
				reason = "rpm_limit_exceeded"
			}
			if limitErr.LimitType == "tpm" {
				reason = "tpm_limit_exceeded"
			}
			s.recordRejection(ctx, subject, reason, limitErr.LimitScope, rejectionScopeID(subject, limitErr.LimitScope))
		}
		return nil, err
	}
	return &GovernanceTicket{
		Subject: subject, Resource: resource, BudgetReserved: reservation != nil,
		SafetyPolicyID: decision.PolicyID, SafetyPolicyNo: decision.PolicyVersion, SafetyRefusalText: decision.RefusalMessage,
	}, nil
}

func (s *GovernanceService) recordRejection(ctx context.Context, subject SafetySubject, reason, scopeType, scopeID string) {
	s.metrics.RecordRejection(metricRejectionReason(reason))
	recorder, ok := s.budget.(gatewayRejectionRecorder)
	if !ok || strings.TrimSpace(subject.RequestID) == "" {
		return
	}
	_ = recorder.RecordGatewayRejection(context.WithoutCancel(ctx), &model.AIGatewayRejectionEvent{RequestID: subject.RequestID, LogicalModelCode: subject.LogicalModelCode, ReasonCode: reason, ScopeType: scopeType, ScopeID: scopeID})
}

func metricRejectionReason(reason string) string {
	switch reason {
	case "content_policy_violation":
		return "content_policy"
	case "budget_limit_exceeded":
		return "budget_limit"
	case "concurrency_limit_exceeded":
		return "concurrency_limit"
	case "rpm_limit_exceeded":
		return "rpm_limit"
	case "tpm_limit_exceeded":
		return "tpm_limit"
	case "fail_closed":
		return "fail_closed"
	default:
		return "other"
	}
}

func rejectionScopeID(subject SafetySubject, scope string) string {
	switch scope {
	case "user":
		return strconv.FormatUint(subject.UserID, 10)
	case "project":
		return strconv.FormatUint(subject.ProjectID, 10)
	case "api_key":
		return strconv.FormatUint(subject.APIKeyID, 10)
	default:
		return "global"
	}
}

// AbortBeforeUpstream 回收钱包 hold 前失败路径的预算和并发租约；RPM/TPM 窗口事实按真实准入请求保留。
func (s *GovernanceService) AbortBeforeUpstream(ctx context.Context, ticket *GovernanceTicket) {
	if s == nil || ticket == nil {
		return
	}
	_ = s.limiter.Release(context.WithoutCancel(ctx), ticket.Resource)
	if ticket.BudgetReserved {
		s.releaseBudgetOrCompensate(ctx, ticket.Subject.RequestID)
	}
}

// FinishExecution 先核销 TPM，再释放并发租约，并依据 G3 财务终态同步预算预留。
func (s *GovernanceService) FinishExecution(ctx context.Context, ticket *GovernanceTicket, usage ExecutionUsage) {
	if s == nil || ticket == nil {
		return
	}
	if usage.Present && usage.TotalTokens >= 0 {
		_ = s.limiter.ReconcileTokens(context.WithoutCancel(ctx), ticket.Resource, uint64(usage.TotalTokens))
	}
	_ = s.limiter.Release(context.WithoutCancel(ctx), ticket.Resource)
	if ticket.BudgetReserved {
		if synced, err := s.budget.SyncBudgetFromRequest(context.WithoutCancel(ctx), ticket.Subject.RequestID); err != nil || !synced {
			s.recordBudgetCompensation(context.WithoutCancel(ctx), ticket.Subject.RequestID, "budget_sync_failed")
		}
	}
}

func (s *GovernanceService) releaseBudgetOrCompensate(ctx context.Context, requestID string) {
	if err := s.budget.ReleaseBudget(context.WithoutCancel(ctx), requestID); err != nil {
		s.recordBudgetCompensation(context.WithoutCancel(ctx), requestID, "budget_release_failed")
	}
}

func (s *GovernanceService) recordBudgetCompensation(ctx context.Context, requestID, class string) {
	if err := s.budget.RecordCompensationFailure(ctx, requestID, class); err != nil {
		// 数据库完全不可用时无法伪造“已持久化”事实；保留 held 到自然过期并记录错误，禁止按时间猜测提前释放。
		log.Printf("[token_gateway] G4 预算补偿登记失败 request_id=%s class=%s err=%v", requestID, class, err)
	}
}

func (s *GovernanceService) StartHeartbeat(ctx context.Context, ticket *GovernanceTicket) <-chan error {
	if s == nil || ticket == nil {
		result := make(chan error)
		close(result)
		return result
	}
	return s.limiter.StartHeartbeat(ctx, ticket.Resource)
}

// ReconcileExpiredBudgets 逐条收敛过期预留；单条损坏只进入补偿任务，不阻断整个批次。
func (s *GovernanceService) ReconcileExpiredBudgets(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	ids, err := s.budget.ListHeldBudgetRequestIDs(ctx, s.now(), limit)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, requestID := range ids {
		settled, syncErr := s.budget.SyncBudgetFromRequest(ctx, requestID)
		if syncErr != nil {
			s.recordBudgetCompensation(ctx, requestID, "budget_sync_failed")
			continue
		}
		if settled {
			completed++
		}
	}
	return completed, nil
}

func (s *GovernanceService) StartRecoveryLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	run := func() {
		if _, err := s.ReconcileExpiredBudgets(ctx, 100); err != nil {
			log.Printf("[token_gateway] G4 预算补偿扫描失败: %v", err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
