package dto

import (
	"time"

	"github.com/shopspring/decimal"
)

// ProductUsageEventReq 消费事件上报请求体（内部接口，不对外暴露）。
type ProductUsageEventReq struct {
	EventID        string          `json:"event_id"`        // UUID，用于幂等
	UserID         uint64          `json:"user_id"`
	ProductID      uint64          `json:"product_id"`
	ProductType    string          `json:"product_type"`
	ProductCode    string          `json:"product_code"`
	ProductPlanID  uint64          `json:"product_plan_id"`
	InstanceID     uint64          `json:"instance_id"`
	UsageType      string          `json:"usage_type"`
	UsageAmount    decimal.Decimal `json:"usage_amount"`
	UsageUnit      string          `json:"usage_unit"`
	OccurredAt     time.Time       `json:"occurred_at"`
	IdempotencyKey string          `json:"idempotency_key"` // 必须全局唯一
}

// ConsumptionResultResp 消费处理结果响应。
// C-5：record_id 改为 consumption_record_id，新增 wallet_transaction_id（钱包流水 ID）。
type ConsumptionResultResp struct {
	ConsumptionRecordID uint64          `json:"consumption_record_id"`
	Amount              decimal.Decimal `json:"amount"`
	IdempotencyKey      string          `json:"idempotency_key"`
	WalletTransactionID uint64          `json:"wallet_transaction_id"`
}
