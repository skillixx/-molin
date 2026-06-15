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

// PayOrderReq O3 支付订单请求体。
type PayOrderReq struct {
	PayMethod string `json:"pay_method"` // 目前仅支持 wallet
}

// PayOrderResp O3 支付订单响应。
type PayOrderResp struct {
	OrderID             uint64 `json:"order_id"`
	Status              string `json:"status"`
	WalletTransactionID uint64 `json:"wallet_transaction_id"`
	AssetID             uint64 `json:"asset_id"`
}

// CancelOrderReq O4 取消订单请求体。
type CancelOrderReq struct {
	Reason string `json:"reason"` // 取消原因（可选）
}

// CancelOrderResp O4 取消订单响应。
type CancelOrderResp struct {
	Cancelled bool `json:"cancelled"`
}
