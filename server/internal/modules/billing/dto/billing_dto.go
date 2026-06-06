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
type CreateRechargeOrderReq struct {
	Amount   decimal.Decimal `json:"amount"`
	Provider string          `json:"provider"` // wechat / alipay
	Remark   string          `json:"remark"`
}

// CreateRechargeOrderResp 创建充值订单响应（返回模拟支付 URL）。
type CreateRechargeOrderResp struct {
	OrderID uint64 `json:"order_id"`
	PayURL  string `json:"pay_url"`
}

// FreezeReq 冻结/解冻请求体。
type FreezeReq struct {
	Amount decimal.Decimal `json:"amount"`
	Action string          `json:"action"` // freeze / unfreeze
	Remark string          `json:"remark"`
}
