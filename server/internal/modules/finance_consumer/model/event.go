package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// ProductUsageEvent 业务模块上报的消费事件结构体。
// IdempotencyKey 必须全局唯一，推荐使用 event_id + usage_type 组合。
// ProductID 字段为修复：原 CLAUDE.md 中遗漏此字段，但 Handle 函数引用了 event.ProductID。
type ProductUsageEvent struct {
	EventID        string          `json:"event_id"`        // UUID，用于幂等
	UserID         uint64          `json:"user_id"`
	ProductID      uint64          `json:"product_id"`      // 修复：补充缺失的 ProductID 字段
	ProductType    string          `json:"product_type"`    // app/gpu/agent/skill/token
	ProductCode    string          `json:"product_code"`
	ProductPlanID  uint64          `json:"product_plan_id"`
	InstanceID     uint64          `json:"instance_id"`
	UsageType      string          `json:"usage_type"`      // 例如 input_tokens/output_tokens/storage_gb/hours
	UsageAmount    decimal.Decimal `json:"usage_amount"`
	UsageUnit      string          `json:"usage_unit"`
	OccurredAt     time.Time       `json:"occurred_at"`
	IdempotencyKey string          `json:"idempotency_key"` // 必须全局唯一，推荐 event_id + usage_type
}
