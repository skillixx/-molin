package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// ProductConsumptionRecord 消费计费记录，对应 product_consumption_records 表。
// 只追加写入，禁止 UPDATE/DELETE。
type ProductConsumptionRecord struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	EventID        string          `gorm:"size:128;not null" json:"event_id"`
	UserID         uint64          `gorm:"not null;index" json:"user_id"`
	ProductID      uint64          `gorm:"not null;index" json:"product_id"`
	ProductPlanID  *uint64         `json:"product_plan_id,omitempty"`
	InstanceID     *uint64         `json:"instance_id,omitempty"`
	UsageType      string          `gorm:"size:64;not null" json:"usage_type"`
	UsageAmount    decimal.Decimal `gorm:"type:decimal(18,6);not null" json:"usage_amount"`
	UsageUnit      string          `gorm:"size:32;not null" json:"usage_unit"`
	Amount         decimal.Decimal `gorm:"type:decimal(18,6);not null" json:"amount"` // 实际扣费金额
	IdempotencyKey string          `gorm:"size:128;not null;uniqueIndex" json:"idempotency_key"`
	CreatedAt      time.Time       `json:"created_at"`
}

// ConsumptionResult 消费处理结果（用于幂等返回）。
type ConsumptionResult struct {
	RecordID       uint64          `json:"record_id"`
	Amount         decimal.Decimal `json:"amount"`
	IdempotencyKey string          `json:"idempotency_key"`
}

// ToResult 将消费记录转换为结果对象。
func (r *ProductConsumptionRecord) ToResult() *ConsumptionResult {
	return &ConsumptionResult{
		RecordID:       r.ID,
		Amount:         r.Amount,
		IdempotencyKey: r.IdempotencyKey,
	}
}
