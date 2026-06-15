package dto

import "github.com/shopspring/decimal"

// CreateProductReq 创建商品请求体。
type CreateProductReq struct {
	ProductType   string  `json:"product_type"`
	ProductCode   string  `json:"product_code"`
	Name          string  `json:"name"`
	Description   *string `json:"description"`
	Status        string  `json:"status"`
	BusinessRefID *uint64 `json:"business_ref_id"`
}

// UpdateProductReq 更新商品请求体（所有字段均为可选）。
type UpdateProductReq struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	BusinessRefID *uint64 `json:"business_ref_id"`
}

// UpdateStatusReq 上架/下架请求体。
type UpdateStatusReq struct {
	Status string `json:"status"` // active / inactive
}

// CreatePlanReq 创建套餐请求体。
type CreatePlanReq struct {
	PlanCode     string  `json:"plan_code"`
	Name         string  `json:"name"`
	BillingType  string  `json:"billing_type"`
	DurationDays *int    `json:"duration_days"`
	QuotaJSON    *string `json:"quota_json"`
	Status       string  `json:"status"`
}

// UpdatePlanReq 更新套餐请求体（所有字段均为可选）。
type UpdatePlanReq struct {
	Name         *string `json:"name"`
	BillingType  *string `json:"billing_type"`
	DurationDays *int    `json:"duration_days"`
	QuotaJSON    *string `json:"quota_json"`
	Status       *string `json:"status"`
}

// PriceItem 单条价格配置（用于批量配置请求）。
type PriceItem struct {
	RoleID            *uint64         `json:"role_id"`
	MembershipLevelID *uint64         `json:"membership_level_id"`
	PriceAmount       decimal.Decimal `json:"price_amount"`
	Currency          string          `json:"currency"`
}

// ReplacePricesReq 批量覆盖写入价格请求体。
// C-2：批量写入 body 键名统一为 items（原 prices）。
type ReplacePricesReq struct {
	Items []PriceItem `json:"items"`
}

// AccessItem 单条角色访问规则（用于批量配置请求）。
type AccessItem struct {
	RoleID  uint64 `json:"role_id"`
	CanView bool   `json:"can_view"`
	CanBuy  bool   `json:"can_buy"`
	CanUse  bool   `json:"can_use"`
}

// ReplaceAccessReq 批量覆盖写入访问权限请求体。
// C-2：批量写入 body 键名统一为 items（原 accesses）。
type ReplaceAccessReq struct {
	Items []AccessItem `json:"items"`
}

// PurchaseReq 购买请求体（Idempotency-Key 在请求头中，此结构体用于 Body）。
type PurchaseReq struct {
	PlanID   uint64 `json:"plan_id"`
	Quantity int    `json:"quantity"` // 购买数量，默认为 1；传 0 或不传均视为 1
	Remark   string `json:"remark,omitempty"` // 购买备注（可选）
}

// PurchaseResult 购买结果。
type PurchaseResult struct {
	OrderID    uint64          `json:"order_id"`
	OrderNo    string          `json:"order_no"`
	Status     string          `json:"status"`
	Amount     decimal.Decimal `json:"amount"`
	Idempotent bool            `json:"idempotent"` // true 表示重复请求，返回已有订单
}

// CreateBillingRuleReq 新增计费规则请求体（P16）。
type CreateBillingRuleReq struct {
	ProductID     uint64           `json:"product_id"`               // 必填，指向存在的商品
	ProductPlanID *uint64          `json:"product_plan_id"`          // 可选，空=商品级通用规则
	UsageType     string           `json:"usage_type"`               // 必填，计费用量类型
	UsageUnit     string           `json:"usage_unit"`               // 必填，计量单位
	PriceAmount   decimal.Decimal  `json:"price_amount"`             // 必填，单价，必须 > 0
	Currency      string           `json:"currency"`                 // 可选，默认 CNY
	BillingMode   string           `json:"billing_mode"`             // 必填，计费模式
	FreeQuota     *decimal.Decimal `json:"free_quota"`               // 可选，免费额度
	Status        string           `json:"status"`                   // 可选，默认 active
}

// UpdateBillingRuleReq 修改计费规则请求体（P17，全部字段可选）。
type UpdateBillingRuleReq struct {
	UsageType   *string          `json:"usage_type,omitempty"`
	UsageUnit   *string          `json:"usage_unit,omitempty"`
	PriceAmount *decimal.Decimal `json:"price_amount,omitempty"`
	Currency    *string          `json:"currency,omitempty"`
	BillingMode *string          `json:"billing_mode,omitempty"`
	FreeQuota   *decimal.Decimal `json:"free_quota,omitempty"`
	Status      *string          `json:"status,omitempty"`
}

// BillingRuleItem 计费规则响应项（P15 列表 / 详情）。
type BillingRuleItem struct {
	ID            uint64           `json:"id"`
	ProductID     uint64           `json:"product_id"`
	ProductPlanID *uint64          `json:"product_plan_id,omitempty"`
	UsageType     string           `json:"usage_type"`
	UsageUnit     string           `json:"usage_unit"`
	PriceAmount   decimal.Decimal  `json:"price_amount"`
	Currency      string           `json:"currency"`
	BillingMode   string           `json:"billing_mode"`
	FreeQuota     *decimal.Decimal `json:"free_quota,omitempty"`
	Status        string           `json:"status"`
	CreatedAt     string           `json:"created_at"`
	UpdatedAt     string           `json:"updated_at"`
}

// PlanWithPrice 套餐信息（含用户实际价格）。
type PlanWithPrice struct {
	ID           uint64          `json:"id"`
	PlanCode     string          `json:"plan_code"`
	Name         string          `json:"name"`
	BillingType  string          `json:"billing_type"`
	DurationDays *int            `json:"duration_days,omitempty"`
	QuotaJSON    *string         `json:"quota_json,omitempty"`
	Status       string          `json:"status"`
	UserPrice    decimal.Decimal `json:"user_price"`    // 用户实际价格（-1 表示未配置）
	Currency     string          `json:"currency"`
}
