package model

import (
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
)

const (
	AISafetyPolicyDraft   = "draft"
	AISafetyPolicyActive  = "active"
	AISafetyPolicyRetired = "retired"

	AIBudgetDisabled = "disabled"
	AIBudgetSoft     = "soft"
	AIBudgetHard     = "hard"

	AIBudgetHeld     = "held"
	AIBudgetSettled  = "settled"
	AIBudgetReleased = "released"
	AIBudgetExpired  = "expired"
)

// AISafetyPolicyVersion 保存不可原地修改的内容安全规则版本。
type AISafetyPolicyVersion struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	VersionNo      uint64          `gorm:"not null;uniqueIndex" json:"version_no"`
	Status         string          `gorm:"size:16;not null" json:"status"`
	RefusalMessage string          `gorm:"size:255;not null" json:"refusal_message"`
	RulesJSON      json.RawMessage `gorm:"column:rules_json;type:json;not null" json:"rules"`
	CreatedBy      uint64          `gorm:"not null" json:"created_by"`
	ApprovedBy     *uint64         `json:"approved_by,omitempty"`
	EffectiveAt    *time.Time      `json:"effective_at,omitempty"`
	RetiredAt      *time.Time      `json:"retired_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func (AISafetyPolicyVersion) TableName() string { return "ai_safety_policy_versions" }

// AISafetyEvent 只保存摘要、分类和处置结果，不保存完整提示词或模型响应。
type AISafetyEvent struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	EventID         string    `gorm:"size:128;not null;uniqueIndex" json:"event_id"`
	RequestID       string    `gorm:"size:128;not null;index" json:"request_id"`
	UserID          uint64    `gorm:"not null;index:idx_ai_safety_events_subject_created,priority:1" json:"user_id"`
	ProjectID       uint64    `gorm:"not null" json:"project_id"`
	APIKeyID        uint64    `gorm:"not null;index:idx_ai_safety_events_subject_created,priority:2" json:"api_key_id"`
	Direction       string    `gorm:"size:16;not null" json:"direction"`
	Category        string    `gorm:"size:32;not null" json:"category"`
	RuleCode        string    `gorm:"size:64;not null" json:"rule_code"`
	PolicyVersionID uint64    `gorm:"not null" json:"policy_version_id"`
	ContentDigest   string    `gorm:"size:64;not null" json:"-"`
	Action          string    `gorm:"size:32;not null" json:"action"`
	Result          string    `gorm:"size:32;not null" json:"result"`
	CreatedAt       time.Time `json:"created_at"`
}

func (AISafetyEvent) TableName() string { return "ai_safety_events" }

// AISafetySubjectAction 保存用户或 Project SK 的暂停、解除和过期事实。
type AISafetySubjectAction struct {
	ID          uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	SubjectType string     `gorm:"size:16;not null" json:"subject_type"`
	SubjectID   string     `gorm:"size:128;not null" json:"subject_id"`
	Action      string     `gorm:"size:16;not null" json:"action"`
	Status      string     `gorm:"size:16;not null" json:"status"`
	Reason      string     `gorm:"size:255;not null" json:"reason"`
	OperatorID  uint64     `gorm:"not null" json:"operator_id"`
	VersionNo   uint64     `gorm:"not null" json:"version_no"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (AISafetySubjectAction) TableName() string { return "ai_safety_subject_actions" }

// AISafetyAppeal 保存用户对单个违规事件的申诉及管理员处理结论。
type AISafetyAppeal struct {
	ID         uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	EventID    string     `gorm:"size:128;not null;uniqueIndex:uk_ai_safety_appeal_event_user,priority:1" json:"event_id"`
	UserID     uint64     `gorm:"not null;uniqueIndex:uk_ai_safety_appeal_event_user,priority:2" json:"user_id"`
	Reason     string     `gorm:"size:1000;not null" json:"reason"`
	Status     string     `gorm:"size:24;not null" json:"status"`
	Resolution *string    `gorm:"size:1000" json:"resolution,omitempty"`
	ResolvedBy *uint64    `json:"resolved_by,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	VersionNo  uint64     `gorm:"not null" json:"version_no"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (AISafetyAppeal) TableName() string { return "ai_safety_appeals" }

// AIResourcePolicy 保存四层并发、RPM 和 TPM 覆盖值，scope_key 对模型可使用逻辑模型编码。
type AIResourcePolicy struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ScopeType        string    `gorm:"size:16;not null;uniqueIndex:uk_ai_resource_policy_scope,priority:1" json:"scope_type"`
	ScopeKey         string    `gorm:"size:191;not null;uniqueIndex:uk_ai_resource_policy_scope,priority:2" json:"scope_key"`
	ConcurrencyLimit uint64    `gorm:"not null" json:"concurrency_limit"`
	RPMLimit         uint64    `gorm:"column:rpm_limit;not null" json:"rpm_limit"`
	TPMLimit         uint64    `gorm:"column:tpm_limit;not null" json:"tpm_limit"`
	Status           string    `gorm:"size:16;not null" json:"status"`
	VersionNo        uint64    `gorm:"not null" json:"version_no"`
	UpdatedBy        uint64    `gorm:"not null" json:"updated_by"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (AIResourcePolicy) TableName() string { return "ai_resource_policies" }

// AIBudgetPolicy 保存 Project 或 SK 的日/月预算模式和上限。
type AIBudgetPolicy struct {
	ID           uint64           `gorm:"primaryKey;autoIncrement" json:"id"`
	ScopeType    string           `gorm:"size:16;not null;uniqueIndex:uk_ai_budget_policy_scope,priority:1" json:"scope_type"`
	ScopeID      uint64           `gorm:"not null;uniqueIndex:uk_ai_budget_policy_scope,priority:2" json:"scope_id"`
	Mode         string           `gorm:"size:16;not null" json:"mode"`
	DailyLimit   *decimal.Decimal `gorm:"type:decimal(20,8)" json:"daily_limit,omitempty"`
	MonthlyLimit *decimal.Decimal `gorm:"type:decimal(20,8)" json:"monthly_limit,omitempty"`
	VersionNo    uint64           `gorm:"not null" json:"version_no"`
	UpdatedBy    uint64           `gorm:"not null" json:"updated_by"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

func (AIBudgetPolicy) TableName() string { return "ai_budget_policies" }

// AIBudgetOverride 保存有原因、操作者和失效时间的临时预算增额。
type AIBudgetOverride struct {
	ID          uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ScopeType   string          `gorm:"size:16;not null" json:"scope_type"`
	ScopeID     uint64          `gorm:"not null" json:"scope_id"`
	ExtraAmount decimal.Decimal `gorm:"type:decimal(20,8);not null" json:"extra_amount"`
	Reason      string          `gorm:"size:255;not null" json:"reason"`
	OperatorID  uint64          `gorm:"not null" json:"operator_id"`
	ExpiresAt   time.Time       `json:"expires_at"`
	RevokedAt   *time.Time      `json:"revoked_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

func (AIBudgetOverride) TableName() string { return "ai_budget_overrides" }

// AIBudgetReservation 在钱包 hold 前原子占用预算，之后只按 G3 请求终态收敛。
type AIBudgetReservation struct {
	ID                 uint64           `gorm:"primaryKey;autoIncrement" json:"id"`
	RequestID          string           `gorm:"size:128;not null;uniqueIndex" json:"request_id"`
	UserID             uint64           `gorm:"not null" json:"user_id"`
	ProjectID          uint64           `gorm:"not null;index:idx_ai_budget_reservation_project_status,priority:1" json:"project_id"`
	APIKeyID           uint64           `gorm:"not null;index:idx_ai_budget_reservation_key_status,priority:1" json:"api_key_id"`
	ReservedAmount     decimal.Decimal  `gorm:"type:decimal(20,8);not null" json:"reserved_amount"`
	SettledAmount      *decimal.Decimal `gorm:"type:decimal(20,8)" json:"settled_amount,omitempty"`
	Status             string           `gorm:"size:24;not null" json:"status"`
	DailyPeriodStart   time.Time        `json:"daily_period_start"`
	MonthlyPeriodStart time.Time        `json:"monthly_period_start"`
	ExpiresAt          time.Time        `json:"expires_at"`
	ReleasedAt         *time.Time       `json:"released_at,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
}

func (AIBudgetReservation) TableName() string { return "ai_budget_reservations" }

// AIBudgetAlert 是相同主体、周期和阈值只生成一次的通知事实。
type AIBudgetAlert struct {
	ID               uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	EventID          string          `gorm:"size:128;not null;uniqueIndex" json:"event_id"`
	ScopeType        string          `gorm:"size:16;not null" json:"scope_type"`
	ScopeID          uint64          `gorm:"not null" json:"scope_id"`
	PeriodType       string          `gorm:"size:16;not null" json:"period_type"`
	PeriodStart      time.Time       `json:"period_start"`
	ThresholdPercent uint64          `gorm:"not null" json:"threshold_percent"`
	ChannelsJSON     json.RawMessage `gorm:"column:channels_json;type:json;not null" json:"channels"`
	CreatedAt        time.Time       `json:"created_at"`
}

func (AIBudgetAlert) TableName() string { return "ai_budget_alerts" }

// AICompensationTask 记录可重试、死亡和人工处理状态，单条坏数据不会阻塞批次。
type AICompensationTask struct {
	ID             uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	TaskKey        string     `gorm:"size:191;not null;uniqueIndex" json:"task_key"`
	TaskType       string     `gorm:"size:64;not null" json:"task_type"`
	AggregateID    string     `gorm:"size:128;not null" json:"aggregate_id"`
	Status         string     `gorm:"size:24;not null" json:"status"`
	RetryCount     uint64     `gorm:"not null" json:"retry_count"`
	NextRetryAt    time.Time  `json:"next_retry_at"`
	LockedAt       *time.Time `json:"locked_at,omitempty"`
	LastErrorClass *string    `gorm:"size:64" json:"last_error_class,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (AICompensationTask) TableName() string { return "ai_compensation_tasks" }

// AIGatewayRejectionEvent 保存前置治理拒绝的脱敏事实，不记录提示词、响应正文或密钥。
type AIGatewayRejectionEvent struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	RequestID        string    `gorm:"size:128;not null;uniqueIndex:uk_ai_gateway_rejection_request_reason,priority:1" json:"request_id"`
	LogicalModelCode string    `gorm:"size:128;not null" json:"logical_model_code"`
	ReasonCode       string    `gorm:"size:64;not null;uniqueIndex:uk_ai_gateway_rejection_request_reason,priority:2" json:"reason_code"`
	ScopeType        string    `gorm:"size:32;not null" json:"scope_type"`
	ScopeID          string    `gorm:"size:191;not null" json:"scope_id"`
	CreatedAt        time.Time `json:"created_at"`
}

func (AIGatewayRejectionEvent) TableName() string { return "ai_gateway_rejection_events" }
