package model

import (
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
)

const (
	AIImageCapability = "image.generate"

	AIImageTaskCreated          = "created"
	AIImageTaskReserved         = "reserved"
	AIImageTaskSubmitted        = "submitted"
	AIImageTaskProcessing       = "processing"
	AIImageTaskStoring          = "storing"
	AIImageTaskModerating       = "moderating"
	AIImageTaskSucceeded        = "succeeded"
	AIImageTaskFailed           = "failed"
	AIImageTaskCancelled        = "cancelled"
	AIImageTaskExpired          = "expired"
	AIImageTaskPendingReconcile = "pending_reconcile"

	AIImageAssetPrimaryOutput  = "primary_output"
	AIImageAssetThumbnail      = "thumbnail"
	AIImageAssetModerationCopy = "moderation_copy"
	AIImageAssetDerived        = "derived"

	AIImageAssetTemporary    = "temporary"
	AIImageAssetAvailable    = "available"
	AIImageAssetQuarantined  = "quarantined"
	AIImageAssetExpiring     = "expiring"
	AIImageAssetDeleting     = "deleting"
	AIImageAssetDeleted      = "deleted"
	AIImageAssetDeleteFailed = "delete_failed"

	AIImageLabelPending = "pending"
	AIImageLabelApplied = "applied"
	AIImageLabelFailed  = "failed"

	AIImageDisputeNone     = "none"
	AIImageDisputeOpen     = "open"
	AIImageDisputeResolved = "resolved"
)

// AIGatewayQuote 保存一次只能消费一次的图片报价事实；请求指纹和价格快照均不向普通接口直接暴露。
type AIGatewayQuote struct {
	ID                 uint64           `gorm:"primaryKey;autoIncrement;uniqueIndex:uk_ai_gateway_quotes_owner,priority:1" json:"id"`
	PublicID           string           `gorm:"size:128;not null;uniqueIndex:uk_ai_gateway_quotes_public_id" json:"quote_id"`
	UserID             uint64           `gorm:"not null;uniqueIndex:uk_ai_gateway_quotes_owner,priority:2;index:idx_ai_gateway_quotes_owner_expiry,priority:1" json:"user_id"`
	ProjectID          uint64           `gorm:"not null;uniqueIndex:uk_ai_gateway_quotes_owner,priority:3;index:idx_ai_gateway_quotes_owner_expiry,priority:2" json:"project_id"`
	APIKeyID           *uint64          `json:"api_key_id,omitempty"`
	LogicalModelCode   string           `gorm:"size:128;not null" json:"logical_model_code"`
	Capability         string           `gorm:"size:64;not null;default:image.generate" json:"capability"`
	RequestFingerprint string           `gorm:"size:64;not null" json:"-"`
	RequestVariantHash string           `gorm:"size:64;not null" json:"variant_hash"`
	PriceVersionID     uint64           `gorm:"not null;index:idx_ai_gateway_quotes_price_version" json:"price_version_id"`
	PriceSnapshotJSON  json.RawMessage  `gorm:"column:price_snapshot_json;type:json;not null" json:"-"`
	QuotedAmount       decimal.Decimal  `gorm:"type:decimal(20,8);not null" json:"quoted_amount"`
	HeldAmount         *decimal.Decimal `gorm:"type:decimal(20,8)" json:"held_amount,omitempty"`
	Currency           string           `gorm:"size:8;not null;default:CNY" json:"currency"`
	ExpiresAt          time.Time        `gorm:"index:idx_ai_gateway_quotes_owner_expiry,priority:3" json:"expires_at"`
	ConsumedRequestID  *string          `gorm:"size:128;uniqueIndex:uk_ai_gateway_quotes_consumed_request" json:"consumed_request_id,omitempty"`
	ConsumedAt         *time.Time       `json:"consumed_at,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
}

// TableName 指定图片报价事实表名。
func (AIGatewayQuote) TableName() string { return "ai_gateway_quotes" }

// AIImageTask 保存图片执行、存储、审核和结算编排状态；输入与结果 JSON 只能包含低敏规格和资产引用。
type AIImageTask struct {
	ID                uint64          `gorm:"primaryKey;autoIncrement;uniqueIndex:uk_ai_gateway_tasks_owner,priority:1" json:"id"`
	PublicID          string          `gorm:"size:128;not null;uniqueIndex:uk_ai_gateway_tasks_public_id" json:"task_id"`
	RequestID         string          `gorm:"size:128;not null;uniqueIndex:uk_ai_gateway_tasks_request;uniqueIndex:uk_ai_gateway_tasks_owner,priority:2" json:"request_id"`
	QuoteID           uint64          `gorm:"not null;uniqueIndex:uk_ai_gateway_tasks_quote" json:"quote_id"`
	UserID            uint64          `gorm:"not null;uniqueIndex:uk_ai_gateway_tasks_owner,priority:3;index:idx_ai_gateway_tasks_owner_status,priority:1" json:"user_id"`
	ProjectID         uint64          `gorm:"not null;uniqueIndex:uk_ai_gateway_tasks_owner,priority:4;index:idx_ai_gateway_tasks_owner_status,priority:2" json:"project_id"`
	APIKeyID          *uint64         `json:"api_key_id,omitempty"`
	LogicalModelCode  string          `gorm:"size:128;not null" json:"logical_model_code"`
	Capability        string          `gorm:"size:64;not null;default:image.generate" json:"capability"`
	Status            string          `gorm:"size:32;not null;default:created;index:idx_ai_gateway_tasks_owner_status,priority:3;index:idx_ai_gateway_tasks_status_poll,priority:1" json:"status"`
	Progress          uint8           `gorm:"not null;default:0" json:"progress"`
	ProviderCode      *string         `gorm:"size:64;uniqueIndex:uk_ai_gateway_tasks_provider_ref,priority:1" json:"-"`
	ProviderTaskID    *string         `gorm:"size:191;uniqueIndex:uk_ai_gateway_tasks_provider_ref,priority:2" json:"-"`
	AttemptCount      uint32          `gorm:"not null;default:0" json:"attempt_count"`
	NextPollAt        *time.Time      `gorm:"index:idx_ai_gateway_tasks_status_poll,priority:2" json:"next_poll_at,omitempty"`
	InputJSON         json.RawMessage `gorm:"column:input_json;type:json;not null" json:"-"`
	ResultJSON        json.RawMessage `gorm:"column:result_json;type:json" json:"-"`
	ErrorCode         *string         `gorm:"size:64" json:"error_code,omitempty"`
	ErrorMessageSafe  *string         `gorm:"size:512" json:"error_message,omitempty"`
	VersionNo         uint64          `gorm:"not null;default:1" json:"version_no"`
	CancelRequestedAt *time.Time      `json:"cancel_requested_at,omitempty"`
	CompletedAt       *time.Time      `json:"completed_at,omitempty"`
	CreatedAt         time.Time       `gorm:"index:idx_ai_gateway_tasks_owner_status,priority:4" json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// TableName 指定图片任务事实表名。
func (AIImageTask) TableName() string { return "ai_gateway_tasks" }

// AIImageAsset 只保存对象元数据和归属，不保存图片正文、Base64、Prompt或长期签名地址。
type AIImageAsset struct {
	ID                  uint64     `gorm:"primaryKey;autoIncrement;uniqueIndex:uk_ai_gateway_assets_parent_owner,priority:1" json:"id"`
	PublicID            string     `gorm:"size:128;not null;uniqueIndex:uk_ai_gateway_assets_public_id" json:"asset_id"`
	UserID              uint64     `gorm:"not null;index:idx_ai_gateway_assets_owner_state,priority:1" json:"user_id"`
	ProjectID           uint64     `gorm:"not null;index:idx_ai_gateway_assets_owner_state,priority:2" json:"project_id"`
	RequestID           string     `gorm:"size:128;not null;uniqueIndex:uk_ai_gateway_assets_request_result_role,priority:1;uniqueIndex:uk_ai_gateway_assets_parent_owner,priority:2" json:"request_id"`
	TaskID              uint64     `gorm:"not null;index:idx_ai_gateway_assets_task" json:"task_id"`
	ResultIndex         uint32     `gorm:"not null;uniqueIndex:uk_ai_gateway_assets_request_result_role,priority:2" json:"result_index"`
	AssetRole           string     `gorm:"size:32;not null;uniqueIndex:uk_ai_gateway_assets_request_result_role,priority:3" json:"asset_role"`
	ParentAssetID       *uint64    `json:"parent_asset_id,omitempty"`
	IsBillableOutput    bool       `gorm:"not null;default:0" json:"is_billable_output"`
	Bucket              *string    `gorm:"size:128" json:"-"`
	ObjectKey           *string    `gorm:"size:512" json:"-"`
	MIMEType            *string    `gorm:"size:64" json:"mime_type,omitempty"`
	SizeBytes           *uint64    `json:"size_bytes,omitempty"`
	SHA256              *string    `gorm:"size:64" json:"sha256,omitempty"`
	Width               *uint32    `json:"width,omitempty"`
	Height              *uint32    `json:"height,omitempty"`
	Source              string     `gorm:"size:32;not null" json:"source"`
	ModerationStatus    string     `gorm:"size:32;not null;default:pending" json:"moderation_status"`
	ExplicitLabelStatus string     `gorm:"size:16;not null;default:pending" json:"explicit_label_status"`
	ImplicitLabelStatus string     `gorm:"size:16;not null;default:pending" json:"implicit_label_status"`
	LifecycleState      string     `gorm:"size:32;not null;default:temporary;index:idx_ai_gateway_assets_owner_state,priority:3;index:idx_ai_gateway_assets_cleanup,priority:1" json:"lifecycle_state"`
	RetentionPolicyID   string     `gorm:"size:64;not null" json:"retention_policy_id"`
	ExpiresAt           time.Time  `gorm:"index:idx_ai_gateway_assets_cleanup,priority:3" json:"expires_at"`
	LegalHold           bool       `gorm:"not null;default:0;index:idx_ai_gateway_assets_cleanup,priority:2" json:"legal_hold"`
	VersionNo           uint64     `gorm:"not null;default:1" json:"version_no"`
	DisputeStatus       string     `gorm:"size:16;not null;default:none;index:idx_ai_gateway_assets_dispute,priority:1" json:"dispute_status"`
	DisputeOpenedAt     *time.Time `json:"dispute_opened_at,omitempty"`
	DisputeResolvedAt   *time.Time `json:"dispute_resolved_at,omitempty"`
	DeletedAt           *time.Time `json:"deleted_at,omitempty"`
	CreatedAt           time.Time  `gorm:"index:idx_ai_gateway_assets_owner_state,priority:4" json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// TableName 指定图片资产元数据表名。
func (AIImageAsset) TableName() string { return "ai_gateway_assets" }
