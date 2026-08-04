package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
)

type governanceAdminRepository interface {
	ListSafetyPolicies(context.Context, int, int) ([]model.AISafetyPolicyVersion, int64, error)
	GetSafetyPolicy(context.Context, uint64) (*model.AISafetyPolicyVersion, error)
	CreateSafetyPolicy(context.Context, *model.AISafetyPolicyVersion) error
	PublishSafetyPolicy(context.Context, uint64, uint64, uint64) error
	RollbackSafetyPolicy(context.Context, uint64, uint64) (*model.AISafetyPolicyVersion, error)
	ListSafetyEvents(context.Context, int, int) ([]model.AISafetyEvent, int64, error)
	ListUserSafetyEvents(context.Context, uint64, int, int) ([]model.AISafetyEvent, int64, error)
	CreateSubjectAction(context.Context, *model.AISafetySubjectAction) error
	ListSubjectActions(context.Context, int, int) ([]model.AISafetySubjectAction, int64, error)
	RevokeSubjectAction(context.Context, uint64, uint64) error
	CreateAppeal(context.Context, *model.AISafetyAppeal) error
	ListAppeals(context.Context, int, int) ([]model.AISafetyAppeal, int64, error)
	ResolveAppeal(context.Context, uint64, uint64, uint64, string, string) error
	UpsertResourcePolicy(context.Context, *model.AIResourcePolicy, uint64) error
	ListResourcePolicies(context.Context, int, int) ([]model.AIResourcePolicy, int64, error)
	UpsertBudgetPolicy(context.Context, *model.AIBudgetPolicy, uint64) error
	ListBudgetPolicies(context.Context, int, int) ([]model.AIBudgetPolicy, int64, error)
	CreateBudgetOverride(context.Context, *model.AIBudgetOverride) error
	ListBudgetOverrides(context.Context, int, int) ([]model.AIBudgetOverride, int64, error)
	ListBudgetAlerts(context.Context, int, int) ([]model.AIBudgetAlert, int64, error)
	ListCompensationTasks(context.Context, int, int) ([]model.AICompensationTask, int64, error)
	ResolveCompensationTask(context.Context, uint64, time.Time, string) error
}

type GovernanceAdminService struct {
	repo   governanceAdminRepository
	outbox outboxDeadRequeuer
	now    func() time.Time
}

type outboxDeadRequeuer interface {
	RequeueDead(context.Context, string, time.Time) error
}

func NewGovernanceAdminService(repo governanceAdminRepository) *GovernanceAdminService {
	return &GovernanceAdminService{repo: repo, now: time.Now}
}

// WithOutboxDeadRequeuer 注入仅允许受控管理员调用的 Outbox 死信重试能力。
func (s *GovernanceAdminService) WithOutboxDeadRequeuer(outbox outboxDeadRequeuer) *GovernanceAdminService {
	s.outbox = outbox
	return s
}

func (s *GovernanceAdminService) ListPolicies(ctx context.Context, page, pageSize int) ([]model.AISafetyPolicyVersion, int64, error) {
	page, pageSize = normalizePage(page, pageSize)
	return s.repo.ListSafetyPolicies(ctx, (page-1)*pageSize, pageSize)
}

func (s *GovernanceAdminService) CreatePolicy(ctx context.Context, operatorID uint64, rules json.RawMessage) (*model.AISafetyPolicyVersion, error) {
	if !validSafetyRules(rules) {
		return nil, newValidation("安全策略规则不合法")
	}
	items, _, err := s.repo.ListSafetyPolicies(ctx, 0, 1)
	if err != nil {
		return nil, err
	}
	version := uint64(1)
	if len(items) > 0 {
		version = items[0].VersionNo + 1
	}
	policy := &model.AISafetyPolicyVersion{
		VersionNo: version, Status: model.AISafetyPolicyDraft, RefusalMessage: DefaultSafetyRefusal,
		RulesJSON: append([]byte(nil), rules...), CreatedBy: operatorID,
	}
	if err := s.repo.CreateSafetyPolicy(ctx, policy); err != nil {
		return nil, err
	}
	return policy, nil
}

func validSafetyRules(raw json.RawMessage) bool {
	var rules []safetyRule
	if json.Unmarshal(raw, &rules) != nil || len(rules) == 0 {
		return false
	}
	allowed := map[string]bool{"illegal": true, "sexual": true, "gambling": true, "drugs": true, "terror": true, "hate": true, "self_harm": true}
	seen := map[string]bool{}
	categories := map[string]bool{}
	for _, rule := range rules {
		if strings.TrimSpace(rule.Code) == "" || seen[rule.Code] || !allowed[rule.Category] || len(rule.Keywords) == 0 {
			return false
		}
		seen[rule.Code] = true
		categories[rule.Category] = true
		for _, keyword := range rule.Keywords {
			normalized := normalizeModerationText(keyword)
			if normalized == "" || len([]rune(normalized)) > maxSafetyKeywordRunes {
				return false
			}
		}
	}
	// 发布策略必须覆盖七类平台底线，避免遗漏某一类别后仍被误认为完整策略。
	return len(categories) == len(allowed)
}

func (s *GovernanceAdminService) PublishPolicy(ctx context.Context, id, expectedVersion, operatorID uint64) error {
	if id == 0 || expectedVersion == 0 {
		return newValidation("策略 ID 和版本号不能为空")
	}
	policy, err := s.repo.GetSafetyPolicy(ctx, id)
	if err != nil {
		return err
	}
	if !validSafetyRules(policy.RulesJSON) {
		return newValidation("安全策略必须完整覆盖七类底线且关键词合法")
	}
	return s.repo.PublishSafetyPolicy(ctx, id, expectedVersion, operatorID)
}

func (s *GovernanceAdminService) RollbackPolicy(ctx context.Context, id, operatorID uint64) (*model.AISafetyPolicyVersion, error) {
	policy, err := s.repo.GetSafetyPolicy(ctx, id)
	if err != nil {
		return nil, err
	}
	if !validSafetyRules(policy.RulesJSON) {
		return nil, newValidation("历史安全策略不满足当前七类底线，不能直接回滚")
	}
	return s.repo.RollbackSafetyPolicy(ctx, id, operatorID)
}

func (s *GovernanceAdminService) ListEvents(ctx context.Context, page, pageSize int) ([]model.AISafetyEvent, int64, error) {
	page, pageSize = normalizePage(page, pageSize)
	return s.repo.ListSafetyEvents(ctx, (page-1)*pageSize, pageSize)
}

func (s *GovernanceAdminService) ListUserEvents(ctx context.Context, userID uint64, page, pageSize int) ([]model.AISafetyEvent, int64, error) {
	if userID == 0 {
		return nil, 0, newValidation("用户标识不合法")
	}
	page, pageSize = normalizePage(page, pageSize)
	return s.repo.ListUserSafetyEvents(ctx, userID, (page-1)*pageSize, pageSize)
}

func (s *GovernanceAdminService) SuspendSubject(ctx context.Context, operatorID uint64, subjectType, subjectID, reason string, expiresAt *time.Time) (*model.AISafetySubjectAction, error) {
	subjectType, subjectID, reason = strings.TrimSpace(subjectType), strings.TrimSpace(subjectID), strings.TrimSpace(reason)
	if (subjectType != "user" && subjectType != "api_key") || subjectID == "" || reason == "" || len(reason) > 255 || (expiresAt != nil && !expiresAt.After(s.now())) {
		return nil, newValidation("安全处置参数不合法")
	}
	action := &model.AISafetySubjectAction{
		SubjectType: subjectType, SubjectID: subjectID, Action: "suspend", Status: "active",
		Reason: reason, OperatorID: operatorID, VersionNo: 1, ExpiresAt: expiresAt,
	}
	if err := s.repo.CreateSubjectAction(ctx, action); err != nil {
		return nil, err
	}
	return action, nil
}

func (s *GovernanceAdminService) ListSubjectActions(ctx context.Context, page, pageSize int) ([]model.AISafetySubjectAction, int64, error) {
	page, pageSize = normalizePage(page, pageSize)
	return s.repo.ListSubjectActions(ctx, (page-1)*pageSize, pageSize)
}

func (s *GovernanceAdminService) RevokeSubjectAction(ctx context.Context, id, expectedVersion uint64) error {
	if id == 0 || expectedVersion == 0 {
		return newValidation("处置 ID 和版本号不能为空")
	}
	return s.repo.RevokeSubjectAction(ctx, id, expectedVersion)
}

func (s *GovernanceAdminService) CreateAppeal(ctx context.Context, userID uint64, eventID, reason string) (*model.AISafetyAppeal, error) {
	eventID, reason = strings.TrimSpace(eventID), strings.TrimSpace(reason)
	if userID == 0 || eventID == "" || reason == "" || len(reason) > 1000 {
		return nil, newValidation("申诉参数不合法")
	}
	appeal := &model.AISafetyAppeal{EventID: eventID, UserID: userID, Reason: reason, Status: "pending", VersionNo: 1}
	if err := s.repo.CreateAppeal(ctx, appeal); err != nil {
		return nil, err
	}
	return appeal, nil
}

func (s *GovernanceAdminService) ListAppeals(ctx context.Context, page, pageSize int) ([]model.AISafetyAppeal, int64, error) {
	page, pageSize = normalizePage(page, pageSize)
	return s.repo.ListAppeals(ctx, (page-1)*pageSize, pageSize)
}

func (s *GovernanceAdminService) ResolveAppeal(ctx context.Context, id, expectedVersion, operatorID uint64, status, resolution string) error {
	status, resolution = strings.TrimSpace(status), strings.TrimSpace(resolution)
	if id == 0 || expectedVersion == 0 || (status != "approved" && status != "rejected") || resolution == "" || len(resolution) > 1000 {
		return newValidation("申诉处理参数不合法")
	}
	return s.repo.ResolveAppeal(ctx, id, expectedVersion, operatorID, status, resolution)
}

func (s *GovernanceAdminService) PutResourcePolicy(ctx context.Context, operatorID uint64, policy model.AIResourcePolicy, expectedVersion uint64) error {
	if !map[string]bool{"user": true, "project": true, "api_key": true, "model": true}[policy.ScopeType] || strings.TrimSpace(policy.ScopeKey) == "" ||
		policy.ConcurrencyLimit == 0 || policy.RPMLimit == 0 || policy.TPMLimit == 0 || !map[string]bool{"active": true, "disabled": true}[policy.Status] {
		return newValidation("资源策略参数不合法")
	}
	policy.UpdatedBy = operatorID
	return s.repo.UpsertResourcePolicy(ctx, &policy, expectedVersion)
}

func (s *GovernanceAdminService) ListResourcePolicies(ctx context.Context, page, pageSize int) ([]model.AIResourcePolicy, int64, error) {
	page, pageSize = normalizePage(page, pageSize)
	return s.repo.ListResourcePolicies(ctx, (page-1)*pageSize, pageSize)
}

func (s *GovernanceAdminService) PutBudgetPolicy(ctx context.Context, operatorID uint64, policy model.AIBudgetPolicy, expectedVersion uint64) error {
	if !map[string]bool{"project": true, "api_key": true}[policy.ScopeType] || policy.ScopeID == 0 ||
		!map[string]bool{model.AIBudgetDisabled: true, model.AIBudgetSoft: true, model.AIBudgetHard: true}[policy.Mode] {
		return newValidation("预算策略参数不合法")
	}
	if policy.Mode == model.AIBudgetDisabled {
		policy.DailyLimit, policy.MonthlyLimit = nil, nil
	} else if (policy.DailyLimit == nil && policy.MonthlyLimit == nil) ||
		(policy.DailyLimit != nil && policy.DailyLimit.LessThanOrEqual(decimal.Zero)) ||
		(policy.MonthlyLimit != nil && policy.MonthlyLimit.LessThanOrEqual(decimal.Zero)) {
		return newValidation("启用预算时至少配置一个限额，且每个限额必须为正数")
	}
	policy.UpdatedBy = operatorID
	return s.repo.UpsertBudgetPolicy(ctx, &policy, expectedVersion)
}

func (s *GovernanceAdminService) ListBudgetPolicies(ctx context.Context, page, pageSize int) ([]model.AIBudgetPolicy, int64, error) {
	page, pageSize = normalizePage(page, pageSize)
	return s.repo.ListBudgetPolicies(ctx, (page-1)*pageSize, pageSize)
}

func (s *GovernanceAdminService) CreateBudgetOverride(ctx context.Context, operatorID uint64, override model.AIBudgetOverride) (*model.AIBudgetOverride, error) {
	if !map[string]bool{"project": true, "api_key": true}[override.ScopeType] || override.ScopeID == 0 || override.ExtraAmount.LessThanOrEqual(decimal.Zero) ||
		strings.TrimSpace(override.Reason) == "" || len(override.Reason) > 255 || !override.ExpiresAt.After(s.now()) {
		return nil, newValidation("临时预算增额参数不合法")
	}
	override.OperatorID = operatorID
	if err := s.repo.CreateBudgetOverride(ctx, &override); err != nil {
		return nil, err
	}
	return &override, nil
}

func (s *GovernanceAdminService) ListBudgetOverrides(ctx context.Context, page, pageSize int) ([]model.AIBudgetOverride, int64, error) {
	page, pageSize = normalizePage(page, pageSize)
	return s.repo.ListBudgetOverrides(ctx, (page-1)*pageSize, pageSize)
}

func (s *GovernanceAdminService) ListBudgetAlerts(ctx context.Context, page, pageSize int) ([]model.AIBudgetAlert, int64, error) {
	page, pageSize = normalizePage(page, pageSize)
	return s.repo.ListBudgetAlerts(ctx, (page-1)*pageSize, pageSize)
}

func (s *GovernanceAdminService) ListCompensationTasks(ctx context.Context, page, pageSize int) ([]model.AICompensationTask, int64, error) {
	page, pageSize = normalizePage(page, pageSize)
	return s.repo.ListCompensationTasks(ctx, (page-1)*pageSize, pageSize)
}

func (s *GovernanceAdminService) ResolveCompensationTask(ctx context.Context, id uint64, expectedUpdatedAt time.Time, status string) error {
	status = strings.TrimSpace(status)
	if id == 0 || expectedUpdatedAt.IsZero() || (status != "retry" && status != "manual_review") {
		return newValidation("补偿任务处置参数不合法")
	}
	return s.repo.ResolveCompensationTask(ctx, id, expectedUpdatedAt, status)
}

// RequeueDeadOutbox 将单个 dead 事件按原 event_id 重新排队，消费者继续依赖 event_id 保证幂等。
func (s *GovernanceAdminService) RequeueDeadOutbox(ctx context.Context, eventID, reason string) error {
	eventID = strings.TrimSpace(eventID)
	reason = strings.TrimSpace(reason)
	if eventID == "" || len(eventID) > 191 {
		return newValidation("Outbox 事件标识不合法")
	}
	if reason == "" || len(reason) > 255 {
		return newValidation("Outbox 重试原因不合法")
	}
	if s == nil || s.outbox == nil {
		return errors.New("Outbox 重试服务不可用")
	}
	return s.outbox.RequeueDead(ctx, eventID, s.now())
}

func normalizePage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return page, pageSize
}
