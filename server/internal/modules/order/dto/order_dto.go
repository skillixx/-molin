package dto

import (
	"time"

	"github.com/shopspring/decimal"
)

// OrderListItem 订单列表中的单个订单摘要。
type OrderListItem struct {
	ID            uint64          `json:"id"`
	OrderNo       string          `json:"order_no"`
	OrderType     string          `json:"order_type"`
	ProductID     *uint64         `json:"product_id,omitempty"`
	ProductPlanID *uint64         `json:"product_plan_id,omitempty"`
	Status        string          `json:"status"`
	Amount        decimal.Decimal `json:"amount"`
	Currency      string          `json:"currency"`
	PaidAt        *time.Time      `json:"paid_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

// AdminOrderFilter 管理员查询订单的过滤参数。
type AdminOrderFilter struct {
	UserID    uint64
	Status    string
	OrderType string
}
