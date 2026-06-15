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

// ConsumptionRecordFilter 消费记录列表查询过滤条件（F2/F3 共用）。
// F2（用户端）：UserID 由 handler 从 JWT 强制写入，禁止由 query 覆盖（防越权）。
// F3（管理端）：UserID 可由 query 传入用于过滤指定用户，为 0 表示查全量。
type ConsumptionRecordFilter struct {
	UserID      uint64     // 用户 ID 过滤（F2 强制本人；F3 可选）
	ProductID   uint64     // 商品 ID 过滤（可选，0 表示不过滤）
	UsageType   string     // 用量类型过滤（可选，空表示不过滤）
	CreatedFrom *time.Time // 创建时间起（可选，含）
	CreatedTo   *time.Time // 创建时间止（可选，含）
}

// ConsumptionRecordItem 消费记录列表项（F2/F3 响应）。
// 字段：记录 ID、商品、用量、金额、关联事件、时间。
// 说明：product_consumption_records 表未持久化 wallet_transaction_id（扣费流水 ID 仅在
// 上报响应即时返回，见 ConsumptionResultResp），故列表查询不含该字段，避免返回恒为 null 的伪字段；
// 关联流水/事件以 event_id 体现（每条消费事件唯一），如需对账可凭 event_id 追溯。
type ConsumptionRecordItem struct {
	ID            uint64          `json:"id"`
	UserID        uint64          `json:"user_id"`
	ProductID     uint64          `json:"product_id"`
	ProductPlanID *uint64         `json:"product_plan_id"`
	InstanceID    *uint64         `json:"instance_id"`
	UsageType     string          `json:"usage_type"`
	UsageAmount   decimal.Decimal `json:"usage_amount"`
	UsageUnit     string          `json:"usage_unit"`
	Amount        decimal.Decimal `json:"amount"`
	EventID       string          `json:"event_id"`
	CreatedAt     time.Time       `json:"created_at"`
}
