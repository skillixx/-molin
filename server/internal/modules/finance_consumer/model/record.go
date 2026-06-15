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
	// WalletTransactionID 本次扣费产生的钱包流水 ID（B-03）。
	// 免费额度内未产生扣费（amount=0）时为 NULL；持久化后幂等重发可返回相同的真实 txid。
	WalletTransactionID *uint64   `json:"wallet_transaction_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

// ConsumptionResult 消费处理结果（用于幂等返回）。
// C-5：RecordID 改为 ConsumptionRecordID，新增 WalletTransactionID（钱包流水 ID）。
type ConsumptionResult struct {
	ConsumptionRecordID uint64          `json:"consumption_record_id"`
	WalletTransactionID uint64          `json:"wallet_transaction_id"`
	Amount              decimal.Decimal `json:"amount"`
	IdempotencyKey      string          `json:"idempotency_key"`
}

// ToResult 将消费记录转换为结果对象。
// B-03：WalletTransactionID 已持久化到记录，幂等重发时可直接带出原扣费流水 ID；
// 免费额度内（amount=0）未产生流水时该列为 NULL，结果中保持 0。
func (r *ProductConsumptionRecord) ToResult() *ConsumptionResult {
	res := &ConsumptionResult{
		ConsumptionRecordID: r.ID,
		Amount:              r.Amount,
		IdempotencyKey:      r.IdempotencyKey,
	}
	if r.WalletTransactionID != nil {
		res.WalletTransactionID = *r.WalletTransactionID
	}
	return res
}
