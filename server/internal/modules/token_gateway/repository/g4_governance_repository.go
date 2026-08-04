package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/pkg/idgen"
)

var (
	ErrSafetyPolicyUnavailable = errors.New("内容安全策略不可用")
	ErrBudgetLimitExceeded     = errors.New("预算已达到硬限制")
)

// G4GovernanceRepository 统一保存安全、预算和资源策略事实，不保存提示词或模型响应正文。
type G4GovernanceRepository struct {
	db  *gorm.DB
	now func() time.Time
}

func NewG4GovernanceRepository(db *gorm.DB) *G4GovernanceRepository {
	return &G4GovernanceRepository{db: db, now: time.Now}
}

// RecordGatewayRejection 幂等记录前置治理拒绝，只保存统计所需的脱敏元数据。
func (r *G4GovernanceRepository) RecordGatewayRejection(ctx context.Context, event *model.AIGatewayRejectionEvent) error {
	if event == nil {
		return errors.New("网关拒绝事件不能为空")
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}, {Name: "reason_code"}}, DoNothing: true}).Create(event).Error
}

func (r *G4GovernanceRepository) ActiveSafetyPolicy(ctx context.Context) (*model.AISafetyPolicyVersion, error) {
	var policy model.AISafetyPolicyVersion
	err := r.db.WithContext(ctx).
		Where("status = ? AND effective_at IS NOT NULL AND effective_at <= ?", model.AISafetyPolicyActive, r.now()).
		Order("version_no DESC").First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSafetyPolicyUnavailable
	}
	return &policy, err
}

func (r *G4GovernanceRepository) RecordSafetyEvent(ctx context.Context, event *model.AISafetyEvent) error {
	return r.db.WithContext(ctx).Create(event).Error
}

func (r *G4GovernanceRepository) IsSubjectSuspended(ctx context.Context, userID, apiKeyID uint64) (bool, error) {
	now := r.now()
	var count int64
	err := r.db.WithContext(ctx).Table("ai_safety_subject_actions").
		Where("status = 'active' AND action = 'suspend' AND (expires_at IS NULL OR expires_at > ?) AND ((subject_type = 'user' AND subject_id = ?) OR (subject_type = 'api_key' AND subject_id = ?))",
			now, fmt.Sprint(userID), fmt.Sprint(apiKeyID)).Count(&count).Error
	return count > 0, err
}

func (r *G4GovernanceRepository) MarkModeration(ctx context.Context, requestID, status string) error {
	return r.db.WithContext(ctx).Model(&model.AIRequest{}).
		Where("request_id = ?", requestID).Update("moderation_status", status).Error
}

// LoadResourcePolicies 一次读取四层覆盖，缺失层由服务端使用环境配置的默认值。
func (r *G4GovernanceRepository) LoadResourcePolicies(ctx context.Context, scopeKeys map[string]string) (map[string]model.AIResourcePolicy, error) {
	if len(scopeKeys) == 0 {
		return map[string]model.AIResourcePolicy{}, nil
	}
	var policies []model.AIResourcePolicy
	// 固定顺序并把所有 scope 条件放入同一括号，确保 disabled 策略不能经顶层 OR 绕过状态过滤。
	scopes := []string{"user", "project", "api_key", "model"}
	conditions := make([]string, 0, len(scopes))
	args := make([]interface{}, 0, len(scopes)*2)
	for _, scopeType := range scopes {
		scopeKey, ok := scopeKeys[scopeType]
		if !ok {
			continue
		}
		conditions = append(conditions, "(scope_type = ? AND scope_key = ?)")
		args = append(args, scopeType, scopeKey)
	}
	query := r.db.WithContext(ctx).Where("status = 'active'")
	if len(conditions) > 0 {
		query = query.Where("("+strings.Join(conditions, " OR ")+")", args...)
	}
	if err := query.Find(&policies).Error; err != nil {
		return nil, err
	}
	result := make(map[string]model.AIResourcePolicy, len(policies))
	for _, policy := range policies {
		result[policy.ScopeType] = policy
	}
	return result, nil
}

type BudgetReservationRequest struct {
	RequestID          string
	UserID             uint64
	ProjectID          uint64
	APIKeyID           uint64
	Amount             decimal.Decimal
	DailyPeriodStart   time.Time
	MonthlyPeriodStart time.Time
	ExpiresAt          time.Time
}

// ReserveBudget 在同一事务中锁定 Project 与 SK 策略，累计已结算金额和 held 预留，防止多 SK 并发超卖。
func (r *G4GovernanceRepository) ReserveBudget(ctx context.Context, req BudgetReservationRequest) (*model.AIBudgetReservation, error) {
	var reservation model.AIBudgetReservation
	var hardExceeded bool
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("request_id = ?", req.RequestID).First(&reservation).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var policies []model.AIBudgetPolicy
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("(scope_type = 'project' AND scope_id = ?) OR (scope_type = 'api_key' AND scope_id = ?)", req.ProjectID, req.APIKeyID).
			Order("scope_type DESC").Find(&policies).Error; err != nil {
			return err
		}
		if len(policies) == 0 {
			return nil
		}
		activePolicy := false
		for _, policy := range policies {
			if policy.Mode == model.AIBudgetDisabled {
				continue
			}
			activePolicy = true
			periods := []struct {
				kind  string
				start time.Time
				limit *decimal.Decimal
			}{
				{kind: "daily", start: req.DailyPeriodStart, limit: policy.DailyLimit},
				{kind: "monthly", start: req.MonthlyPeriodStart, limit: policy.MonthlyLimit},
			}
			for _, period := range periods {
				if period.limit == nil {
					continue
				}
				limit := *period.limit
				override, err := r.activeOverrideAmount(tx, policy.ScopeType, policy.ScopeID, r.now())
				if err != nil {
					return err
				}
				limit = limit.Add(override)
				settled, held, err := r.budgetUsage(tx, policy.ScopeType, policy.ScopeID, period.kind, period.start)
				if err != nil {
					return err
				}
				projected := settled.Add(held).Add(req.Amount)
				if err := r.createThresholdAlerts(tx, policy, period.kind, period.start, projected, limit); err != nil {
					return err
				}
				if policy.Mode == model.AIBudgetHard && projected.GreaterThan(limit) {
					hardExceeded = true
				}
			}
		}
		if hardExceeded {
			// 阈值事件需要提交，因此不在事务内部返回错误。
			return nil
		}
		if !activePolicy {
			return nil
		}
		reservation = model.AIBudgetReservation{
			RequestID: req.RequestID, UserID: req.UserID, ProjectID: req.ProjectID, APIKeyID: req.APIKeyID,
			ReservedAmount: req.Amount, Status: model.AIBudgetHeld, DailyPeriodStart: req.DailyPeriodStart,
			MonthlyPeriodStart: req.MonthlyPeriodStart, ExpiresAt: req.ExpiresAt,
		}
		return tx.Create(&reservation).Error
	})
	if err != nil {
		return nil, err
	}
	if hardExceeded {
		return nil, ErrBudgetLimitExceeded
	}
	if reservation.ID == 0 {
		return nil, nil
	}
	return &reservation, nil
}

func (r *G4GovernanceRepository) activeOverrideAmount(tx *gorm.DB, scopeType string, scopeID uint64, now time.Time) (decimal.Decimal, error) {
	var amount decimal.Decimal
	err := tx.Table("ai_budget_overrides").Select("COALESCE(SUM(extra_amount), 0)").
		Where("scope_type = ? AND scope_id = ? AND revoked_at IS NULL AND expires_at > ?", scopeType, scopeID, now).
		Scan(&amount).Error
	return amount, err
}

func (r *G4GovernanceRepository) budgetUsage(tx *gorm.DB, scopeType string, scopeID uint64, periodType string, periodStart time.Time) (decimal.Decimal, decimal.Decimal, error) {
	column := "project_id"
	if scopeType == "api_key" {
		column = "api_key_id"
	}
	periodColumn := "daily_period_start"
	if periodType == "monthly" {
		periodColumn = "monthly_period_start"
	}
	var rows []struct {
		Status         string           `gorm:"column:status"`
		ReservedAmount decimal.Decimal  `gorm:"column:reserved_amount"`
		SettledAmount  *decimal.Decimal `gorm:"column:settled_amount"`
	}
	// 预算归属在准入时固化，跨午夜完成的请求仍计入原周期，不能按 completed_at 漂移到新周期。
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Model(&model.AIBudgetReservation{}).
		Select("status, reserved_amount, settled_amount").
		Where(column+" = ? AND "+periodColumn+" = ? AND status IN ?", scopeID, periodStart, []string{model.AIBudgetHeld, model.AIBudgetSettled}).
		Find(&rows).Error; err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	settled := decimal.Zero
	held := decimal.Zero
	for _, row := range rows {
		if row.Status == model.AIBudgetHeld {
			held = held.Add(row.ReservedAmount)
		} else if row.SettledAmount != nil {
			settled = settled.Add(*row.SettledAmount)
		}
	}
	return settled, held, nil
}

func (r *G4GovernanceRepository) createThresholdAlerts(tx *gorm.DB, policy model.AIBudgetPolicy, periodType string, periodStart time.Time, usage, limit decimal.Decimal) error {
	if limit.LessThanOrEqual(decimal.Zero) {
		return nil
	}
	percent := usage.Mul(decimal.NewFromInt(100)).Div(limit)
	for _, threshold := range []uint64{80, 90, 100} {
		if percent.LessThan(decimal.NewFromInt(int64(threshold))) {
			continue
		}
		channels := []string{"site"}
		if threshold >= 90 {
			channels = append(channels, "configured")
		}
		raw, _ := json.Marshal(channels)
		alert := model.AIBudgetAlert{
			EventID: idgen.NewRequestID(), ScopeType: policy.ScopeType, ScopeID: policy.ScopeID,
			PeriodType: periodType, PeriodStart: periodStart, ThresholdPercent: threshold, ChannelsJSON: raw,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&alert).Error; err != nil {
			return err
		}
	}
	return nil
}

// ReleaseBudget 用于安全、限流或钱包 hold 失败路径，重复释放保持幂等。
func (r *G4GovernanceRepository) ReleaseBudget(ctx context.Context, requestID string) error {
	now := r.now()
	return r.db.WithContext(ctx).Model(&model.AIBudgetReservation{}).
		Where("request_id = ? AND status = ?", requestID, model.AIBudgetHeld).
		Updates(map[string]interface{}{"status": model.AIBudgetReleased, "released_at": now}).Error
}

// SyncBudgetFromRequest 只读取 G3 终态和既有补偿事实，不创建金额事实，也不会重放上游调用。
// 明确的释放失败可在补偿任务到期后立即重试；没有持久化补偿事实时必须等待预留自然过期，避免慢请求被提前释放。
func (r *G4GovernanceRepository) SyncBudgetFromRequest(ctx context.Context, requestID string) (bool, error) {
	returnStatus := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var reservation model.AIBudgetReservation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ?", requestID).First(&reservation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		var task model.AICompensationTask
		taskErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_key = ?", "budget:"+requestID).First(&task).Error
		if taskErr != nil && !errors.Is(taskErr, gorm.ErrRecordNotFound) {
			return taskErr
		}
		hasTask := taskErr == nil
		if reservation.Status != model.AIBudgetHeld {
			returnStatus = true
			return completeBudgetCompensationTx(tx, hasTask, task.ID, r.now())
		}
		var request model.AIRequest
		if err := tx.Where("request_id = ?", requestID).First(&request).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				now := r.now()
				explicitRelease := hasTask && task.LastErrorClass != nil && *task.LastErrorClass == "budget_release_failed"
				// 预算预留发生在钱包预占之前；只有明确的释放补偿事实或预留自然过期，才能证明原准入链不再继续。
				if explicitRelease || !reservation.ExpiresAt.After(now) {
					returnStatus = true
					status := model.AIBudgetReleased
					if !explicitRelease {
						status = model.AIBudgetExpired
					}
					if err := tx.Model(&reservation).Updates(map[string]interface{}{"status": status, "released_at": now}).Error; err != nil {
						return err
					}
					return completeBudgetCompensationTx(tx, hasTask, task.ID, now)
				}
				return nil
			}
			return err
		}
		now := r.now()
		switch request.BillingStatus {
		case model.AIBillingSettled:
			amount := decimal.Zero
			if request.SettledAmount != nil {
				amount = *request.SettledAmount
			}
			returnStatus = true
			if err := tx.Model(&reservation).Updates(map[string]interface{}{
				"status": model.AIBudgetSettled, "settled_amount": amount, "released_at": now,
			}).Error; err != nil {
				return err
			}
			return completeBudgetCompensationTx(tx, hasTask, task.ID, now)
		case model.AIBillingReleased:
			returnStatus = true
			if err := tx.Model(&reservation).Updates(map[string]interface{}{
				"status": model.AIBudgetReleased, "released_at": now,
			}).Error; err != nil {
				return err
			}
			return completeBudgetCompensationTx(tx, hasTask, task.ID, now)
		default:
			return nil
		}
	})
	return returnStatus, err
}

func completeBudgetCompensationTx(tx *gorm.DB, exists bool, taskID uint64, now time.Time) error {
	if !exists {
		return nil
	}
	return tx.Model(&model.AICompensationTask{}).Where("id = ?", taskID).Updates(map[string]interface{}{
		"status": "completed", "locked_at": nil, "next_retry_at": now,
	}).Error
}

func (r *G4GovernanceRepository) ListHeldBudgetRequestIDs(ctx context.Context, before time.Time, limit int) ([]string, error) {
	var ids []string
	err := r.db.WithContext(ctx).Model(&model.AIBudgetReservation{}).
		Where("status = ?", model.AIBudgetHeld).
		Where(`(
			(NOT EXISTS (SELECT 1 FROM ai_compensation_tasks t WHERE t.task_key = CONCAT('budget:', ai_budget_reservations.request_id)) AND expires_at <= ?)
			OR EXISTS (SELECT 1 FROM ai_compensation_tasks t WHERE t.task_key = CONCAT('budget:', ai_budget_reservations.request_id) AND t.status IN ('pending','retry') AND t.next_retry_at <= ?)
		)`, before, before).
		Order("id ASC").Limit(limit).Pluck("request_id", &ids).Error
	return ids, err
}

func (r *G4GovernanceRepository) RecordCompensationFailure(ctx context.Context, requestID, class string) error {
	now := r.now()
	task := model.AICompensationTask{
		TaskKey: "budget:" + requestID, TaskType: "budget_reconcile", AggregateID: requestID,
		Status: "retry", RetryCount: 1, NextRetryAt: now.Add(time.Minute), LastErrorClass: &class,
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "task_key"}},
		// MySQL 按赋值顺序求值，必须先递增次数，再让状态表达式读取新次数。
		DoUpdates: clause.Set{
			{Column: clause.Column{Name: "retry_count"}, Value: gorm.Expr("IF(status IN ('completed','dead','manual_review'), retry_count, retry_count + 1)")},
			{Column: clause.Column{Name: "status"}, Value: gorm.Expr("IF(status IN ('completed','dead','manual_review'), status, IF(retry_count >= 8, 'dead', 'retry'))")},
			{Column: clause.Column{Name: "next_retry_at"}, Value: gorm.Expr("IF(status IN ('completed','dead','manual_review'), next_retry_at, ?)", now.Add(time.Minute))},
			{Column: clause.Column{Name: "last_error_class"}, Value: gorm.Expr("IF(status IN ('completed','dead','manual_review'), last_error_class, ?)", class)},
		},
	}).Create(&task).Error
}

func (r *G4GovernanceRepository) ListCompensationTasks(ctx context.Context, offset, limit int) ([]model.AICompensationTask, int64, error) {
	var items []model.AICompensationTask
	var total int64
	query := r.db.WithContext(ctx).Model(&model.AICompensationTask{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

// ResolveCompensationTask 使用更新时间作为乐观锁，防止两名管理员覆盖彼此的处置结果。
func (r *G4GovernanceRepository) ResolveCompensationTask(ctx context.Context, id uint64, expectedUpdatedAt time.Time, status string) error {
	updates := map[string]interface{}{"status": status, "locked_at": nil}
	if status == "retry" {
		updates["next_retry_at"] = r.now()
		updates["retry_count"] = 0
	}
	result := r.db.WithContext(ctx).Model(&model.AICompensationTask{}).
		Where("id = ? AND updated_at = ? AND status IN ('dead','manual_review','retry')", id, expectedUpdatedAt).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrRequestStateConflict
	}
	return nil
}

func (r *G4GovernanceRepository) ListSafetyPolicies(ctx context.Context, offset, limit int) ([]model.AISafetyPolicyVersion, int64, error) {
	var items []model.AISafetyPolicyVersion
	var total int64
	query := r.db.WithContext(ctx).Model(&model.AISafetyPolicyVersion{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("version_no DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (r *G4GovernanceRepository) GetSafetyPolicy(ctx context.Context, id uint64) (*model.AISafetyPolicyVersion, error) {
	var policy model.AISafetyPolicyVersion
	if err := r.db.WithContext(ctx).First(&policy, id).Error; err != nil {
		return nil, err
	}
	return &policy, nil
}

func (r *G4GovernanceRepository) CreateSafetyPolicy(ctx context.Context, policy *model.AISafetyPolicyVersion) error {
	return r.db.WithContext(ctx).Create(policy).Error
}

// PublishSafetyPolicy 串行发布策略并退休旧活动版本，禁止原地修改已发布规则。
func (r *G4GovernanceRepository) PublishSafetyPolicy(ctx context.Context, id, expectedVersion, operatorID uint64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var target model.AISafetyPolicyVersion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, id).Error; err != nil {
			return err
		}
		if target.Status != model.AISafetyPolicyDraft || target.VersionNo != expectedVersion {
			return ErrRequestStateConflict
		}
		now := r.now()
		if err := tx.Model(&model.AISafetyPolicyVersion{}).Where("status = ?", model.AISafetyPolicyActive).
			Updates(map[string]interface{}{"status": model.AISafetyPolicyRetired, "retired_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&target).Updates(map[string]interface{}{
			"status": model.AISafetyPolicyActive, "approved_by": operatorID, "effective_at": now,
		}).Error
	})
}

// RollbackSafetyPolicy 复制历史规则形成新版本并发布，历史版本仍保持不可变。
func (r *G4GovernanceRepository) RollbackSafetyPolicy(ctx context.Context, sourceID, operatorID uint64) (*model.AISafetyPolicyVersion, error) {
	var created model.AISafetyPolicyVersion
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source model.AISafetyPolicyVersion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&source, sourceID).Error; err != nil {
			return err
		}
		var maxVersion uint64
		if err := tx.Model(&model.AISafetyPolicyVersion{}).Select("COALESCE(MAX(version_no), 0)").Scan(&maxVersion).Error; err != nil {
			return err
		}
		now := r.now()
		if err := tx.Model(&model.AISafetyPolicyVersion{}).Where("status = ?", model.AISafetyPolicyActive).
			Updates(map[string]interface{}{"status": model.AISafetyPolicyRetired, "retired_at": now}).Error; err != nil {
			return err
		}
		created = model.AISafetyPolicyVersion{
			VersionNo: maxVersion + 1, Status: model.AISafetyPolicyActive, RefusalMessage: source.RefusalMessage,
			RulesJSON: append([]byte(nil), source.RulesJSON...), CreatedBy: operatorID, ApprovedBy: &operatorID, EffectiveAt: &now,
		}
		return tx.Create(&created).Error
	})
	return &created, err
}

func (r *G4GovernanceRepository) ListSafetyEvents(ctx context.Context, offset, limit int) ([]model.AISafetyEvent, int64, error) {
	var items []model.AISafetyEvent
	var total int64
	query := r.db.WithContext(ctx).Model(&model.AISafetyEvent{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (r *G4GovernanceRepository) ListUserSafetyEvents(ctx context.Context, userID uint64, offset, limit int) ([]model.AISafetyEvent, int64, error) {
	var items []model.AISafetyEvent
	var total int64
	query := r.db.WithContext(ctx).Model(&model.AISafetyEvent{}).Where("user_id = ?", userID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (r *G4GovernanceRepository) CreateSubjectAction(ctx context.Context, action *model.AISafetySubjectAction) error {
	return r.db.WithContext(ctx).Create(action).Error
}

func (r *G4GovernanceRepository) ListSubjectActions(ctx context.Context, offset, limit int) ([]model.AISafetySubjectAction, int64, error) {
	var items []model.AISafetySubjectAction
	var total int64
	query := r.db.WithContext(ctx).Model(&model.AISafetySubjectAction{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (r *G4GovernanceRepository) RevokeSubjectAction(ctx context.Context, id, expectedVersion uint64) error {
	now := r.now()
	result := r.db.WithContext(ctx).Model(&model.AISafetySubjectAction{}).
		Where("id = ? AND version_no = ? AND status = 'active'", id, expectedVersion).
		Updates(map[string]interface{}{"status": "revoked", "revoked_at": now, "version_no": gorm.Expr("version_no + 1")})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrRequestStateConflict
	}
	return nil
}

func (r *G4GovernanceRepository) CreateAppeal(ctx context.Context, appeal *model.AISafetyAppeal) error {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.AISafetyEvent{}).
		Where("event_id = ? AND user_id = ?", appeal.EventID, appeal.UserID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return gorm.ErrRecordNotFound
	}
	return r.db.WithContext(ctx).Create(appeal).Error
}

func (r *G4GovernanceRepository) ListAppeals(ctx context.Context, offset, limit int) ([]model.AISafetyAppeal, int64, error) {
	var items []model.AISafetyAppeal
	var total int64
	query := r.db.WithContext(ctx).Model(&model.AISafetyAppeal{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (r *G4GovernanceRepository) ResolveAppeal(ctx context.Context, id, expectedVersion, operatorID uint64, status, resolution string) error {
	now := r.now()
	result := r.db.WithContext(ctx).Model(&model.AISafetyAppeal{}).
		Where("id = ? AND version_no = ? AND status = 'pending'", id, expectedVersion).
		Updates(map[string]interface{}{
			"status": status, "resolution": resolution, "resolved_by": operatorID,
			"resolved_at": now, "version_no": gorm.Expr("version_no + 1"),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrRequestStateConflict
	}
	return nil
}

func (r *G4GovernanceRepository) UpsertResourcePolicy(ctx context.Context, policy *model.AIResourcePolicy, expectedVersion uint64) error {
	if expectedVersion == 0 {
		policy.VersionNo = 1
		return r.db.WithContext(ctx).Create(policy).Error
	}
	result := r.db.WithContext(ctx).Model(&model.AIResourcePolicy{}).
		Where("scope_type = ? AND scope_key = ? AND version_no = ?", policy.ScopeType, policy.ScopeKey, expectedVersion).
		Updates(map[string]interface{}{
			"concurrency_limit": policy.ConcurrencyLimit, "rpm_limit": policy.RPMLimit, "tpm_limit": policy.TPMLimit,
			"status": policy.Status, "updated_by": policy.UpdatedBy, "version_no": gorm.Expr("version_no + 1"),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrRequestStateConflict
	}
	return nil
}

func (r *G4GovernanceRepository) ListResourcePolicies(ctx context.Context, offset, limit int) ([]model.AIResourcePolicy, int64, error) {
	var items []model.AIResourcePolicy
	var total int64
	query := r.db.WithContext(ctx).Model(&model.AIResourcePolicy{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (r *G4GovernanceRepository) UpsertBudgetPolicy(ctx context.Context, policy *model.AIBudgetPolicy, expectedVersion uint64) error {
	if expectedVersion == 0 {
		policy.VersionNo = 1
		return r.db.WithContext(ctx).Create(policy).Error
	}
	result := r.db.WithContext(ctx).Model(&model.AIBudgetPolicy{}).
		Where("scope_type = ? AND scope_id = ? AND version_no = ?", policy.ScopeType, policy.ScopeID, expectedVersion).
		Updates(map[string]interface{}{
			"mode": policy.Mode, "daily_limit": policy.DailyLimit, "monthly_limit": policy.MonthlyLimit,
			"updated_by": policy.UpdatedBy, "version_no": gorm.Expr("version_no + 1"),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrRequestStateConflict
	}
	return nil
}

func (r *G4GovernanceRepository) ListBudgetPolicies(ctx context.Context, offset, limit int) ([]model.AIBudgetPolicy, int64, error) {
	var items []model.AIBudgetPolicy
	var total int64
	query := r.db.WithContext(ctx).Model(&model.AIBudgetPolicy{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (r *G4GovernanceRepository) CreateBudgetOverride(ctx context.Context, override *model.AIBudgetOverride) error {
	return r.db.WithContext(ctx).Create(override).Error
}

func (r *G4GovernanceRepository) ListBudgetOverrides(ctx context.Context, offset, limit int) ([]model.AIBudgetOverride, int64, error) {
	var items []model.AIBudgetOverride
	var total int64
	query := r.db.WithContext(ctx).Model(&model.AIBudgetOverride{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (r *G4GovernanceRepository) ListBudgetAlerts(ctx context.Context, offset, limit int) ([]model.AIBudgetAlert, int64, error) {
	var items []model.AIBudgetAlert
	var total int64
	query := r.db.WithContext(ctx).Model(&model.AIBudgetAlert{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}
