package model

import (
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
)

const (
	AIModerationPending  = "pending"
	AIModerationPassed   = "passed"
	AIModerationRejected = "rejected"
	AIModerationError    = "error"

	AIExecutionPending   = "pending"
	AIExecutionRunning   = "running"
	AIExecutionSucceeded = "succeeded"
	AIExecutionFailed    = "failed"
	AIExecutionCancelled = "cancelled"
	AIExecutionUnknown   = "unknown"

	AIBillingUnquoted          = "unquoted"
	AIBillingHeld              = "held"
	AIBillingSettlementPending = "settlement_pending"
	AIBillingSettled           = "settled"
	AIBillingReleased          = "released"
	AIBillingException         = "exception"
)

// AIProject 是平台 SK、预算和消费归集的边界；G1 只冻结身份关系，不实现 G2 的 Project 管理接口。
type AIProject struct {
	ID            uint64           `gorm:"primaryKey;autoIncrement;uniqueIndex:uk_ai_projects_id_user,priority:1" json:"id"`
	UserID        uint64           `gorm:"not null;uniqueIndex:uk_ai_projects_user_name,priority:1;uniqueIndex:uk_ai_projects_id_user,priority:2;index:idx_ai_projects_user_status,priority:1" json:"user_id"`
	Name          string           `gorm:"size:191;not null;uniqueIndex:uk_ai_projects_user_name,priority:2" json:"name"`
	Status        string           `gorm:"size:32;not null;default:active;index:idx_ai_projects_user_status,priority:2" json:"status"`
	MonthlyBudget *decimal.Decimal `gorm:"type:decimal(20,8)" json:"monthly_budget,omitempty"`
	BudgetMode    string           `gorm:"size:16;not null;default:disabled" json:"budget_mode"`
	Timezone      string           `gorm:"size:64;not null;default:Asia/Shanghai" json:"timezone"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

// TableName 指定 AI Project 表名。
func (AIProject) TableName() string { return "ai_projects" }

// AIRequest 是一次公开 AI 请求的事实账本，三个状态维度彼此正交，禁止合并成单一综合状态。
// G1 只建立持久化契约；后续必须由 RequestOrchestrator 独占状态流转与结算写入。
type AIRequest struct {
	ID                 uint64           `gorm:"primaryKey;autoIncrement" json:"id"`
	RequestID          string           `gorm:"size:128;not null;uniqueIndex:uk_ai_requests_request_id" json:"request_id"`
	IdempotencyKey     *string          `gorm:"size:191;uniqueIndex:uk_ai_requests_user_idempotency,priority:2" json:"idempotency_key,omitempty"`
	RequestFingerprint *string          `gorm:"size:64" json:"-"`
	UserID             uint64           `gorm:"not null;uniqueIndex:uk_ai_requests_user_idempotency,priority:1;index:idx_ai_requests_user_created,priority:1" json:"user_id"`
	ProjectID          *uint64          `gorm:"index:idx_ai_requests_project_created,priority:1" json:"project_id,omitempty"`
	APIKeyID           *uint64          `gorm:"index:idx_ai_requests_apikey_created,priority:1" json:"api_key_id,omitempty"`
	LogicalModelCode   string           `gorm:"size:128;not null;index:idx_ai_requests_model_created,priority:1" json:"logical_model_code"`
	ExecutionModelCode *string          `gorm:"size:191" json:"-"`
	Modality           string           `gorm:"size:32;not null;default:chat" json:"modality"`
	IsStream           bool             `gorm:"not null;default:0" json:"is_stream"`
	ModerationStatus   string           `gorm:"size:32;not null;default:pending" json:"moderation_status"`
	ExecutionStatus    string           `gorm:"size:32;not null;default:pending;index:idx_ai_requests_states_updated,priority:1" json:"execution_status"`
	BillingStatus      string           `gorm:"size:32;not null;default:unquoted;index:idx_ai_requests_states_updated,priority:2" json:"billing_status"`
	ClientDisconnected bool             `gorm:"not null;default:0" json:"client_disconnected"`
	PriceSnapshotJSON  json.RawMessage  `gorm:"column:price_snapshot_json;type:json" json:"-"`
	QuotedAmount       *decimal.Decimal `gorm:"type:decimal(20,8)" json:"quoted_amount,omitempty"`
	HeldAmount         *decimal.Decimal `gorm:"type:decimal(20,8)" json:"held_amount,omitempty"`
	SettledAmount      *decimal.Decimal `gorm:"type:decimal(20,8)" json:"settled_amount,omitempty"`
	ErrorClass         *string          `gorm:"size:64" json:"error_class,omitempty"`
	ErrorCode          *string          `gorm:"size:64" json:"error_code,omitempty"`
	VersionNo          uint64           `gorm:"not null;default:1" json:"version_no"`
	StartedAt          *time.Time       `json:"started_at,omitempty"`
	CompletedAt        *time.Time       `json:"completed_at,omitempty"`
	CreatedAt          time.Time        `gorm:"index:idx_ai_requests_user_created,priority:2;index:idx_ai_requests_project_created,priority:2;index:idx_ai_requests_apikey_created,priority:2;index:idx_ai_requests_model_created,priority:2" json:"created_at"`
	UpdatedAt          time.Time        `gorm:"index:idx_ai_requests_states_updated,priority:3" json:"updated_at"`
}

// TableName 指定 AI 请求事实账本表名。
func (AIRequest) TableName() string { return "ai_requests" }

// AIUsageItem 保存标准化计量项。数量和金额均使用 Decimal，禁止用 float64 参与财务计算。
type AIUsageItem struct {
	ID         uint64           `gorm:"primaryKey;autoIncrement" json:"id"`
	RequestID  string           `gorm:"size:128;not null;uniqueIndex:uk_ai_usage_request_meter_source_seq,priority:1;index:idx_ai_usage_request" json:"request_id"`
	MeterType  string           `gorm:"size:64;not null;uniqueIndex:uk_ai_usage_request_meter_source_seq,priority:2;index:idx_ai_usage_meter_created,priority:1" json:"meter_type"`
	Source     string           `gorm:"size:32;not null;uniqueIndex:uk_ai_usage_request_meter_source_seq,priority:3" json:"source"`
	SequenceNo uint32           `gorm:"not null;default:0;uniqueIndex:uk_ai_usage_request_meter_source_seq,priority:4" json:"sequence_no"`
	Quantity   decimal.Decimal  `gorm:"type:decimal(30,10);not null" json:"quantity"`
	UnitPrice  *decimal.Decimal `gorm:"type:decimal(20,8)" json:"unit_price,omitempty"`
	Amount     *decimal.Decimal `gorm:"type:decimal(20,8)" json:"amount,omitempty"`
	CreatedAt  time.Time        `gorm:"index:idx_ai_usage_meter_created,priority:2" json:"created_at"`
}

// TableName 指定标准化 Usage 表名。
func (AIUsageItem) TableName() string { return "ai_usage_items" }

// AIExecutionAttempt 保存一次确定的驱动和端点执行，不允许在结果未知时覆盖为另一个供应商的成功结果。
type AIExecutionAttempt struct {
	ID                 uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	RequestID          string     `gorm:"size:128;not null;uniqueIndex:uk_ai_attempts_request_no,priority:1" json:"request_id"`
	AttemptNo          uint32     `gorm:"not null;uniqueIndex:uk_ai_attempts_request_no,priority:2" json:"attempt_no"`
	ExecutionDriver    string     `gorm:"size:32;not null" json:"execution_driver"`
	ProviderCode       string     `gorm:"size:64;not null;index:idx_ai_attempts_provider_upstream,priority:1" json:"-"`
	EndpointCode       *string    `gorm:"size:128" json:"-"`
	ExecutionModelCode string     `gorm:"size:191;not null" json:"-"`
	UpstreamRequestID  *string    `gorm:"size:191;index:idx_ai_attempts_provider_upstream,priority:2" json:"-"`
	Status             string     `gorm:"size:32;not null;index:idx_ai_attempts_status_created,priority:1" json:"status"`
	ResultUnknown      bool       `gorm:"not null;default:0" json:"result_unknown"`
	LatencyMS          *uint64    `json:"latency_ms,omitempty"`
	PromptTokens       *uint64    `json:"prompt_tokens,omitempty"`
	CompletionTokens   *uint64    `json:"completion_tokens,omitempty"`
	ReasoningTokens    *uint64    `json:"reasoning_tokens,omitempty"`
	CachedTokens       *uint64    `json:"cached_tokens,omitempty"`
	ErrorClass         *string    `gorm:"size:64" json:"error_class,omitempty"`
	StartedAt          time.Time  `json:"started_at"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
	CreatedAt          time.Time  `gorm:"index:idx_ai_attempts_status_created,priority:2" json:"created_at"`
}

// TableName 指定上游执行尝试表名。
func (AIExecutionAttempt) TableName() string { return "ai_execution_attempts" }
