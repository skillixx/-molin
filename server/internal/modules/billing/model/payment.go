package model

import "time"

// PaymentCallback 支付回调记录表，对应 payment_callbacks 表。
// provider_trade_no 加 provider 联合唯一索引，保证幂等。
// NotifyBody 存储原始回调报文（Week 2 先明文存，后续优化为 AES-256-GCM 加密）。
type PaymentCallback struct {
	ID              uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	OrderID         uint64     `gorm:"not null;index" json:"order_id"`
	Provider        string     `gorm:"size:32;not null" json:"provider"` // wechat/alipay
	ProviderTradeNo string     `gorm:"size:128;not null" json:"provider_trade_no"`
	NotifyBody      string     `gorm:"type:text" json:"notify_body"` // 原始回调报文
	Status          string     `gorm:"size:32;not null;default:received" json:"status"` // received/processed/ignored
	ProcessedAt     *time.Time `json:"processed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}
