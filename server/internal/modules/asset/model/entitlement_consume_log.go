package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// EntitlementConsumeLog 权益额度扣减幂等日志，对应 entitlement_consume_logs 表。
//
// M2 套餐预付（prepaid）：门面每次扣减 user_entitlements 额度前，在同一事务内先写一条本记录。
// 唯一键 idempotency_key（约定 request_id:quota）保证「同一次调用重复上报不二次扣减」。
// 与 finance_consumer 的钱包消费流水（postpaid）域不同，二者各自独立幂等、互不复用。
type EntitlementConsumeLog struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	EntitlementID  uint64          `gorm:"not null;index:idx_entitlement_consume_ent" json:"entitlement_id"`
	UserID         uint64          `gorm:"not null;index:idx_entitlement_consume_user" json:"user_id"`
	Amount         decimal.Decimal `gorm:"type:decimal(18,6);not null" json:"amount"`
	IdempotencyKey string          `gorm:"size:128;not null;uniqueIndex:uk_entitlement_consume_idem" json:"idempotency_key"`
	CreatedAt      time.Time       `json:"created_at"`
}

// TableName 指定表名。
func (EntitlementConsumeLog) TableName() string { return "entitlement_consume_logs" }
