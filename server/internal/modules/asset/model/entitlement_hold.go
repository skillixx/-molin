package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// EntitlementHold 权益额度预占记录，对应 entitlement_holds 表。
//
// S2-丙4（方案 B，根治 D-M2-01）：prepaid 模式下门面转发前按预估消耗预占额度，与 postpaid
// 钱包保证金（wallet_holds）完全对称。
//   - Reserve：available = quota_total - quota_used - quota_reserved >= amount 才成功，
//     quota_reserved += amount 并建 holding 记录；available 不足返回额度不足（对外 60005）。
//   - Settle：quota_reserved -= amount（预占额），quota_used += actual（actual 封顶到预占额，多退少补），
//     置 settled、记 settled_amount。
//   - Release：quota_reserved -= amount，置 released（不计 used，用于失败/异常路径）。
//
// idempotency_key 唯一索引保证「同一请求重复预占」幂等（门面约定 request_id:quota_reserve）。
type EntitlementHold struct {
	ID             uint64           `gorm:"primaryKey;autoIncrement" json:"id"`
	EntitlementID  uint64           `gorm:"not null;index:idx_entitlement_holds_ent" json:"entitlement_id"`
	UserID         uint64           `gorm:"not null;index:idx_entitlement_holds_user" json:"user_id"`
	Amount         decimal.Decimal  `gorm:"type:decimal(18,6);not null" json:"amount"`
	SettledAmount  *decimal.Decimal `gorm:"type:decimal(18,6)" json:"settled_amount,omitempty"`
	Status         string           `gorm:"size:16;not null;default:holding;index:idx_entitlement_holds_status" json:"status"` // holding/settled/released
	IdempotencyKey string           `gorm:"size:191;not null;uniqueIndex:uk_entitlement_holds_idem" json:"idempotency_key"`
	Remark         string           `gorm:"size:512;not null;default:''" json:"remark"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
	SettledAt      *time.Time       `json:"settled_at,omitempty"`
}

// TableName 指定表名。
func (EntitlementHold) TableName() string { return "entitlement_holds" }

// 预占状态常量。
const (
	HoldStatusHolding  = "holding"  // 预占中（已占额度，未结算）
	HoldStatusSettled  = "settled"  // 已结算（按实际计入 quota_used）
	HoldStatusReleased = "released" // 已释放（释放预占，不计 used）
)
