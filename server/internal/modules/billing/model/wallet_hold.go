package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// 预扣保证金（hold）状态常量。
const (
	// HoldStatusHolding 冻结中：保证金已从可用余额转入冻结金额，等待结算。
	HoldStatusHolding = "holding"
	// HoldStatusSettled 已结算：解冻原保证金后按实际 usage 实扣（多退少补）。
	HoldStatusSettled = "settled"
	// HoldStatusReleased 已释放：全额解冻退回可用余额（actual=0 或调用 ReleaseHold）。
	HoldStatusReleased = "released"
)

// WalletHold 钱包预扣保证金记录，对应 wallet_holds 表。
//
// S2-乙0：为 D1「token 网关 postpaid 预扣保证金」提供追踪能力。
//   - FreezeHold：按 max_tokens×单价冻结保证金，新增一条 holding 记录。
//   - SettleHold：解冻该 hold 的全额保证金，再按实际 usage 实扣（多退少补），置 settled。
//
// IdempotencyKey 唯一索引保证「同一请求重复冻结」幂等。
type WalletHold struct {
	ID             uint64           `gorm:"primaryKey;autoIncrement" json:"id"`
	WalletID       uint64           `gorm:"not null" json:"wallet_id"`
	UserID         uint64           `gorm:"not null;index" json:"user_id"`
	HoldAmount     decimal.Decimal  `gorm:"type:decimal(20,8);not null" json:"hold_amount"`                            // 冻结的保证金金额
	SettledAmount  *decimal.Decimal `gorm:"type:decimal(20,8)" json:"settled_amount,omitempty"`                        // 结算实扣金额，未结算为 nil
	Status         string           `gorm:"size:16;not null;default:holding" json:"status"`                            // holding/settled/released
	IdempotencyKey string           `gorm:"size:191;not null;uniqueIndex:uk_wallet_holds_idem" json:"idempotency_key"` // 冻结幂等键
	FreezeTxnID    *uint64          `json:"freeze_txn_id,omitempty"`                                                   // 冻结流水 ID
	SettleTxnID    *uint64          `json:"settle_txn_id,omitempty"`                                                   // 结算实扣流水 ID
	Remark         string           `gorm:"size:512;not null;default:''" json:"remark"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
	SettledAt      *time.Time       `json:"settled_at,omitempty"`
}

// TableName 显式指定表名。
func (WalletHold) TableName() string { return "wallet_holds" }
