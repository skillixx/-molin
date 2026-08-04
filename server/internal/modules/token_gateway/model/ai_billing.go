package model

import (
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
)

const (
	AIPriceDraft     = "draft"
	AIPriceApproved  = "approved"
	AIPriceActive    = "active"
	AIPriceRetired   = "retired"
	AIPriceSuspended = "suspended"

	AIOutboxPending    = "pending"
	AIOutboxPublishing = "publishing"
	AIOutboxPublished  = "published"
	AIOutboxDead       = "dead"
)

// AIPriceVersion 保存一个逻辑模型的不可变价格事实；已生效版本只能退役或暂停，不能原地改价。
type AIPriceVersion struct {
	ID                  uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	LogicalModelCode    string          `gorm:"size:128;not null;uniqueIndex:uk_ai_price_model_version,priority:1;index:idx_ai_price_active_window,priority:1" json:"logical_model_code"`
	VersionNo           uint64          `gorm:"not null;uniqueIndex:uk_ai_price_model_version,priority:2" json:"version_no"`
	Currency            string          `gorm:"size:8;not null;default:CNY" json:"currency"`
	ExchangeRate        decimal.Decimal `gorm:"type:decimal(20,8);not null;default:1" json:"exchange_rate"`
	Status              string          `gorm:"size:16;not null;default:draft;index:idx_ai_price_active_window,priority:2" json:"status"`
	MinMarginRate       decimal.Decimal `gorm:"type:decimal(12,8);not null" json:"min_margin_rate"`
	MaxInputTokens      uint64          `gorm:"not null" json:"max_input_tokens"`
	MaxOutputTokens     uint64          `gorm:"not null" json:"max_output_tokens"`
	FailureChargePolicy string          `gorm:"size:32;not null;default:confirmed_usage" json:"failure_charge_policy"`
	RoundingMode        string          `gorm:"size:16;not null;default:ceil_8" json:"rounding_mode"`
	CostUpdatedAt       time.Time       `json:"cost_updated_at"`
	CostExpiresAt       time.Time       `json:"cost_expires_at"`
	EffectiveAt         time.Time       `gorm:"index:idx_ai_price_active_window,priority:3" json:"effective_at"`
	ExpiresAt           *time.Time      `gorm:"index:idx_ai_price_active_window,priority:4" json:"expires_at,omitempty"`
	CreatedBy           uint64          `gorm:"not null" json:"created_by"`
	ApprovedBy          *uint64         `json:"approved_by,omitempty"`
	ApprovedAt          *time.Time      `json:"approved_at,omitempty"`
	PublishedAt         *time.Time      `json:"published_at,omitempty"`
	SuspendedReason     *string         `gorm:"size:191" json:"suspended_reason,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

func (AIPriceVersion) TableName() string { return "ai_price_versions" }

// AIPriceSKU 保存价格版本内的一条成本价和销售价，scale 表示单价对应的计量单位数量。
type AIPriceSKU struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	PriceVersionID uint64          `gorm:"not null;uniqueIndex:uk_ai_price_sku_variant,priority:1;index:idx_ai_price_skus_version" json:"price_version_id"`
	MeterType      string          `gorm:"size:64;not null;uniqueIndex:uk_ai_price_sku_variant,priority:2" json:"meter_type"`
	VariantJSON    json.RawMessage `gorm:"type:json" json:"variant_json,omitempty"`
	VariantHash    string          `gorm:"size:64;not null;uniqueIndex:uk_ai_price_sku_variant,priority:3" json:"variant_hash"`
	CostUnitPrice  decimal.Decimal `gorm:"type:decimal(20,8);not null" json:"cost_unit_price"`
	SaleUnitPrice  decimal.Decimal `gorm:"type:decimal(20,8);not null" json:"sale_unit_price"`
	Scale          decimal.Decimal `gorm:"type:decimal(30,10);not null" json:"scale"`
	Currency       string          `gorm:"size:8;not null;default:CNY" json:"currency"`
	CreatedAt      time.Time       `json:"created_at"`
}

func (AIPriceSKU) TableName() string { return "ai_price_skus" }

// AIRequestWalletLink 把一次 AI 请求与唯一钱包预占、结算和释放流水关联起来。
type AIRequestWalletLink struct {
	ID                   uint64           `gorm:"primaryKey;autoIncrement" json:"id"`
	RequestID            string           `gorm:"size:128;not null;uniqueIndex:uk_ai_request_wallet_request" json:"request_id"`
	WalletID             uint64           `gorm:"not null;index:idx_ai_request_wallet_wallet" json:"wallet_id"`
	WalletHoldID         uint64           `gorm:"not null;uniqueIndex:uk_ai_request_wallet_hold" json:"wallet_hold_id"`
	HoldTransactionID    uint64           `gorm:"not null;uniqueIndex:uk_ai_request_wallet_hold_tx" json:"hold_transaction_id"`
	SettleTransactionID  *uint64          `gorm:"uniqueIndex:uk_ai_request_wallet_settle_tx" json:"settle_transaction_id,omitempty"`
	ReleaseTransactionID *uint64          `gorm:"uniqueIndex:uk_ai_request_wallet_release_tx" json:"release_transaction_id,omitempty"`
	QuotedAmount         decimal.Decimal  `gorm:"type:decimal(20,8);not null" json:"quoted_amount"`
	HeldAmount           decimal.Decimal  `gorm:"type:decimal(20,8);not null" json:"held_amount"`
	SettledAmount        *decimal.Decimal `gorm:"type:decimal(20,8)" json:"settled_amount,omitempty"`
	CreatedAt            time.Time        `json:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at"`
}

func (AIRequestWalletLink) TableName() string { return "ai_request_wallet_links" }

// AIOutboxEvent 是财务事务内可靠写入的事件；payload 只能保存脱敏标识和金额状态。
type AIOutboxEvent struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	EventID        string          `gorm:"size:128;not null;uniqueIndex:uk_ai_outbox_event" json:"event_id"`
	AggregateType  string          `gorm:"size:64;not null" json:"aggregate_type"`
	AggregateID    string          `gorm:"size:128;not null;index:idx_ai_outbox_aggregate" json:"aggregate_id"`
	EventType      string          `gorm:"size:64;not null" json:"event_type"`
	PayloadJSON    json.RawMessage `gorm:"type:json;not null" json:"payload_json"`
	Status         string          `gorm:"size:16;not null;default:pending;index:idx_ai_outbox_status_retry,priority:1" json:"status"`
	RetryCount     uint32          `gorm:"not null;default:0" json:"retry_count"`
	NextRetryAt    time.Time       `gorm:"index:idx_ai_outbox_status_retry,priority:2" json:"next_retry_at"`
	LockedAt       *time.Time      `json:"locked_at,omitempty"`
	ProcessedAt    *time.Time      `json:"processed_at,omitempty"`
	LastErrorClass *string         `gorm:"size:64" json:"last_error_class,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func (AIOutboxEvent) TableName() string { return "ai_outbox_events" }
