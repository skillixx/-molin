package dto

import "github.com/shopspring/decimal"

// WalletResp 钱包余额响应。
type WalletResp struct {
	ID            uint64          `json:"id"`
	UserID        uint64          `json:"user_id"`
	BalanceAmount decimal.Decimal `json:"balance_amount"`
	FrozenAmount  decimal.Decimal `json:"frozen_amount"`
	Currency      string          `json:"currency"`
}

// CreateRechargeOrderReq 创建充值订单请求体。
// 字段名与接口文档（docs/full-api-design.md 4.18）保持一致：
// payment_method 取值仅支持 wechat / alipay。
type CreateRechargeOrderReq struct {
	Amount        decimal.Decimal `json:"amount"`
	PaymentMethod string          `json:"payment_method"` // wechat / alipay
	Remark        string          `json:"remark"`
	ReturnURL     string          `json:"return_url"` // 支付完成后跳转地址（可选，仅展示用）
}

// CreateRechargeOrderResp 创建充值订单响应。
// C-3：补充 order_no / amount / status，与 docs/full-api-design.md §4.18 对齐。
type CreateRechargeOrderResp struct {
	OrderID uint64          `json:"order_id"`
	OrderNo string          `json:"order_no"`
	Amount  decimal.Decimal `json:"amount"`
	Status  string          `json:"status"`
	PayURL  string          `json:"pay_url"`
}

// FreezeReq 冻结/解冻请求体。
// C-4：字段顺序与命名对齐为 {action, amount, reason}（原 remark 改为 reason）。
type FreezeReq struct {
	Action string          `json:"action"` // freeze / unfreeze
	Amount decimal.Decimal `json:"amount"`
	Reason string          `json:"reason"`
}
