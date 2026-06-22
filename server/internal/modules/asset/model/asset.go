package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// UserAsset 用户资产，对应 user_assets 表。
// 状态流转：active → suspended（冻结）→ active（解冻），active → expired（到期），active → cancelled（取消）
type UserAsset struct {
	ID                 uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID             uint64     `gorm:"not null;index:idx_user_assets_user_id" json:"user_id"`
	AssetType          string     `gorm:"size:64;not null" json:"asset_type"`
	ProductID          uint64     `gorm:"not null;index:idx_user_assets_product_id" json:"product_id"`
	ProductPlanID      *uint64    `json:"product_plan_id,omitempty"`
	SourceOrderID      *uint64    `json:"source_order_id,omitempty"`
	BusinessInstanceID *string    `gorm:"size:128" json:"business_instance_id,omitempty"`
	Status             string     `gorm:"size:32;not null;default:active;index:idx_user_assets_status" json:"status"` // active/suspended/expired/cancelled
	StartedAt          *time.Time `json:"started_at,omitempty"`
	ExpiresAt          *time.Time `gorm:"index:idx_user_assets_expires_at" json:"expires_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// TableName 指定表名。
func (UserAsset) TableName() string { return "user_assets" }

// UserEntitlement 用户权益/配额，对应 user_entitlements 表。
type UserEntitlement struct {
	ID               uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID           uint64          `gorm:"not null;index:idx_entitlements_user_id" json:"user_id"`
	AssetID          uint64          `gorm:"not null;index:idx_entitlements_asset_id" json:"asset_id"`
	EntitlementType  string          `gorm:"size:64;not null" json:"entitlement_type"`
	ProductID        uint64          `gorm:"not null" json:"product_id"`
	QuotaTotal       *decimal.Decimal `gorm:"type:decimal(18,6)" json:"quota_total,omitempty"`
	QuotaUsed        decimal.Decimal  `gorm:"type:decimal(18,6);not null;default:0" json:"quota_used"`
	// QuotaReserved 已预占未结算额度（对齐钱包 frozen，S2-丙4）。
	// available = quota_total - quota_used - quota_reserved；reserve 占用、settle/release 释放。
	QuotaReserved    decimal.Decimal  `gorm:"type:decimal(18,6);not null;default:0" json:"quota_reserved"`
	QuotaUnit        *string          `gorm:"size:32" json:"quota_unit,omitempty"`
	Status           string           `gorm:"size:32;not null;default:active" json:"status"` // active/suspended/expired
	StartedAt        *time.Time       `json:"started_at,omitempty"`
	ExpiresAt        *time.Time       `json:"expires_at,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

// TableName 指定表名。
func (UserEntitlement) TableName() string { return "user_entitlements" }

// AssetEvent 资产事件日志，对应 asset_events 表。
// 每次状态变更必须写入此表，用于审计追踪。
type AssetEvent struct {
	ID           uint64  `gorm:"primaryKey;autoIncrement" json:"id"`
	AssetID      uint64  `gorm:"not null;index:idx_asset_events_asset_id" json:"asset_id"`
	UserID       uint64  `gorm:"not null;index:idx_asset_events_user_id" json:"user_id"`
	EventType    string  `gorm:"size:64;not null" json:"event_type"` // created/expired/frozen/unfrozen/cancelled
	BeforeStatus *string `gorm:"size:32" json:"before_status,omitempty"`
	AfterStatus  *string `gorm:"size:32" json:"after_status,omitempty"`
	OperatorID   *uint64 `json:"operator_id,omitempty"`
	Remark       *string `gorm:"size:512" json:"remark,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// TableName 指定表名。
func (AssetEvent) TableName() string { return "asset_events" }
