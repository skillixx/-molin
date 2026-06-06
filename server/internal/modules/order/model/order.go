package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// Order 订单主表，对应 orders 表。
// 状态机约束（只允许以下跳转，其他跳转为 Bug）：
//
//	pending → paid       （扣费成功）
//	pending → cancelled  （超时或用户取消）
//	pending → failed     （系统错误）
//	paid    → refunded   （退款，第一阶段暂不实现）
type Order struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	OrderNo        string          `gorm:"size:64;not null;uniqueIndex" json:"order_no"`
	UserID         uint64          `gorm:"not null;index" json:"user_id"`
	OrderType      string          `gorm:"size:32;not null" json:"order_type"` // product/recharge
	ProductID      *uint64         `json:"product_id,omitempty"`
	ProductPlanID  *uint64         `json:"product_plan_id,omitempty"`
	Status         string          `gorm:"size:32;not null;default:pending" json:"status"` // pending/paid/cancelled/failed/refunded
	Amount         decimal.Decimal `gorm:"type:decimal(18,6);not null" json:"amount"`
	Currency       string          `gorm:"size:16;not null;default:CNY" json:"currency"`
	IdempotencyKey string          `gorm:"size:128;not null;uniqueIndex" json:"idempotency_key"`
	PaidAt         *time.Time      `json:"paid_at,omitempty"`
	CancelledAt    *time.Time      `json:"cancelled_at,omitempty"`
	FailedAt       *time.Time      `json:"failed_at,omitempty"`
	Remark         *string         `gorm:"size:512" json:"remark,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// OrderItem 订单商品明细，对应 order_items 表。
type OrderItem struct {
	ID            uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	OrderID       uint64          `gorm:"not null;index" json:"order_id"`
	ProductID     uint64          `gorm:"not null" json:"product_id"`
	ProductPlanID uint64          `gorm:"not null" json:"product_plan_id"`
	Quantity      int             `gorm:"not null;default:1" json:"quantity"`
	UnitPrice     decimal.Decimal `gorm:"type:decimal(18,6);not null" json:"unit_price"`
	TotalPrice    decimal.Decimal `gorm:"type:decimal(18,6);not null" json:"total_price"`
	CreatedAt     time.Time       `json:"created_at"`
}
