package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// Wallet 钱包主表，对应 wallets 表。
// Version 字段用于乐观锁并发控制，每次扣费/充值后 version+1。
type Wallet struct {
	ID            uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID        uint64          `gorm:"not null;uniqueIndex" json:"user_id"`
	BalanceAmount decimal.Decimal `gorm:"type:decimal(18,6);not null;default:0" json:"balance_amount"`
	FrozenAmount  decimal.Decimal `gorm:"type:decimal(18,6);not null;default:0" json:"frozen_amount"`
	Currency      string          `gorm:"size:16;not null;default:CNY" json:"currency"`
	Version       int64           `gorm:"not null;default:0" json:"version"` // 乐观锁版本号
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// WalletTransaction 钱包流水表，对应 wallet_transactions 表。
// 只追加写入，禁止 UPDATE/DELETE。
type WalletTransaction struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	WalletID       uint64          `gorm:"not null;index" json:"wallet_id"`
	UserID         uint64          `gorm:"not null;index" json:"user_id"`
	Type           string          `gorm:"size:32;not null" json:"type"`      // recharge/consume/refund/freeze/unfreeze
	Direction      string          `gorm:"size:8;not null" json:"direction"` // in/out
	Amount         decimal.Decimal `gorm:"type:decimal(18,6);not null" json:"amount"`
	BalanceAfter   decimal.Decimal `gorm:"type:decimal(18,6);not null" json:"balance_after"` // 交易后余额快照
	RelatedOrderID *uint64         `json:"related_order_id,omitempty"`
	Remark         string          `gorm:"size:512" json:"remark"`
	CreatedAt      time.Time       `json:"created_at"`
}
