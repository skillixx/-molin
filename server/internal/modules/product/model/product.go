package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// Product 商品主表，对应 products 表。
type Product struct {
	ID            uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	ProductType   string     `gorm:"size:64;not null" json:"product_type"`
	ProductCode   string     `gorm:"size:128;not null;uniqueIndex:uk_products_code" json:"product_code"`
	Name          string     `gorm:"size:191;not null" json:"name"`
	Description   *string    `gorm:"type:text" json:"description,omitempty"`
	Status        string     `gorm:"size:32;not null;default:draft" json:"status"` // draft/active/inactive
	BusinessRefID *uint64    `json:"business_ref_id,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ProductPlan 套餐表，对应 product_plans 表。
type ProductPlan struct {
	ID          uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ProductID   uint64          `gorm:"not null;index" json:"product_id"`
	PlanCode    string          `gorm:"size:128;not null" json:"plan_code"`
	Name        string          `gorm:"size:191;not null" json:"name"`
	BillingType string          `gorm:"size:64;not null" json:"billing_type"` // one_time/monthly/yearly/usage
	DurationDays *int           `json:"duration_days,omitempty"`
	QuotaJSON   *string         `gorm:"type:json" json:"quota_json,omitempty"`
	Status      string          `gorm:"size:32;not null;default:active" json:"status"` // active/inactive
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`

	// 预加载字段
	Prices []ProductPrice `gorm:"foreignKey:ProductPlanID" json:"prices,omitempty"`
}

// ProductPrice 价格表，对应 product_prices 表。
// 价格优先级：会员价（membership_level_id IS NOT NULL）> 角色价（role_id IS NOT NULL）> 默认价（两者均 NULL）。
type ProductPrice struct {
	ID                uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ProductPlanID     uint64          `gorm:"not null;index" json:"product_plan_id"`
	RoleID            *uint64         `gorm:"index" json:"role_id,omitempty"`
	MembershipLevelID *uint64         `gorm:"index" json:"membership_level_id,omitempty"`
	PriceAmount       decimal.Decimal `gorm:"type:decimal(18,6);not null" json:"price_amount"`
	Currency          string          `gorm:"size:16;not null;default:CNY" json:"currency"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// ProductRoleAccess 商品角色访问规则表，对应 product_role_access 表。
type ProductRoleAccess struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ProductID uint64    `gorm:"not null" json:"product_id"`
	RoleID    uint64    `gorm:"not null" json:"role_id"`
	CanView   bool      `gorm:"not null;default:false" json:"can_view"`
	CanBuy    bool      `gorm:"not null;default:false" json:"can_buy"`
	CanUse    bool      `gorm:"not null;default:false" json:"can_use"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名，防止 GORM 自动复数化为 product_role_accesses。
func (ProductRoleAccess) TableName() string { return "product_role_access" }

// ProductBillingRule 商品计费规则表，对应 product_billing_rules 表。
type ProductBillingRule struct {
	ID            uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ProductID     uint64          `gorm:"not null;index" json:"product_id"`
	ProductPlanID *uint64         `json:"product_plan_id,omitempty"`
	UsageType     string          `gorm:"size:64;not null" json:"usage_type"`
	UsageUnit     string          `gorm:"size:32;not null" json:"usage_unit"`
	PriceAmount   decimal.Decimal `gorm:"type:decimal(18,6);not null" json:"price_amount"`
	Currency      string          `gorm:"size:16;not null;default:CNY" json:"currency"`
	BillingMode   string          `gorm:"size:64;not null" json:"billing_mode"`
	FreeQuota     *decimal.Decimal `gorm:"type:decimal(18,6)" json:"free_quota,omitempty"`
	Status        string          `gorm:"size:32;not null;default:active" json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}
