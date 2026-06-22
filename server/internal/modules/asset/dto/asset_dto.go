package dto

import (
	"time"

	"github.com/shopspring/decimal"
)

// CreateAssetReq 创建资产请求（由 provision 模块调用）。
type CreateAssetReq struct {
	UserID             uint64
	AssetType          string
	ProductID          uint64
	PlanID             uint64
	OrderID            uint64
	BusinessInstanceID string
	ExpiresAt          *time.Time
	QuotaConfig        interface{} // product_plans.quota_json 原始内容
}

// CreateAssetResult 创建资产结果。
type CreateAssetResult struct {
	AssetID uint64 `json:"asset_id"`
}

// AssetResponse 资产列表/详情响应。
type AssetResponse struct {
	ID                 uint64     `json:"id"`
	UserID             uint64     `json:"user_id"`
	AssetType          string     `json:"asset_type"`
	ProductID          uint64     `json:"product_id"`
	ProductPlanID      *uint64    `json:"product_plan_id,omitempty"`
	SourceOrderID      *uint64    `json:"source_order_id,omitempty"`
	BusinessInstanceID *string    `json:"business_instance_id,omitempty"`
	Status             string     `json:"status"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

// EntitlementResponse 权益响应。
type EntitlementResponse struct {
	ID              uint64           `json:"id"`
	UserID          uint64           `json:"user_id"`
	AssetID         uint64           `json:"asset_id"`
	EntitlementType string           `json:"entitlement_type"`
	ProductID       uint64           `json:"product_id"`
	QuotaTotal      *decimal.Decimal `json:"quota_total,omitempty"`
	QuotaUsed       decimal.Decimal  `json:"quota_used"`
	QuotaUnit       *string          `json:"quota_unit,omitempty"`
	Status          string           `json:"status"`
	ExpiresAt       *time.Time       `json:"expires_at,omitempty"`
}

// AssetDetailResponse 资产详情响应（含关联权益），用于 GET /api/my/assets/{id}。
type AssetDetailResponse struct {
	AssetResponse
	Entitlements []EntitlementResponse `json:"entitlements"`
}

// AdminAssetActionReq 管理员操作资产请求（冻结/解冻/取消）。
type AdminAssetActionReq struct {
	Action string `json:"action"` // freeze / unfreeze / cancel
	Remark string `json:"remark"` // 取消原因或冻结备注
}

// AssetSummary 用户资产摘要（D-86：供管理端用户详情接口注入 asset_summary 字段）。
type AssetSummary struct {
	Total     int64 `json:"total"`
	Active    int64 `json:"active"`
	Suspended int64 `json:"suspended"`
	Expired   int64 `json:"expired"`
	Cancelled int64 `json:"cancelled"`
}

// ConsumeEntitlementReq 权益额度扣减内部接口请求（M2 套餐预付，S2-丙2）。
// 由门面 prepaid 模式结算时调用 POST /api/internal/entitlement-consume。
type ConsumeEntitlementReq struct {
	EntitlementID  uint64          `json:"entitlement_id"`  // 目标权益 ID（= sk 解析出的 source_id）
	Amount         decimal.Decimal `json:"amount"`          // 本次消耗额度（token 数，必须为正）
	IdempotencyKey string          `json:"idempotency_key"` // 幂等键，约定 request_id:quota
	UserID         uint64          `json:"user_id"`         // 归属校验：必须等于权益所属用户
}

// ConsumeEntitlementResult 权益额度扣减响应（扣减后的权益快照）。
// remaining = quota_total - quota_used；quota_total 为 NULL（不限量）时 remaining 亦为 NULL。
type ConsumeEntitlementResult struct {
	EntitlementID uint64           `json:"entitlement_id"`
	QuotaTotal    *decimal.Decimal `json:"quota_total,omitempty"`
	QuotaUsed     decimal.Decimal  `json:"quota_used"`
	Remaining     *decimal.Decimal `json:"remaining,omitempty"`
	Status        string           `json:"status"`
}

// EntitlementBalanceResult 权益余额查询响应（S2-丙3，供门面 prepaid 前置闸）。
//
// 纯只读快照，不参与扣减、不改状态。门面在 prepaid 转发前查询本接口，据 usable 做前置拒绝
// （usable=false → 直接拒 60005），据 remaining 可做阈值判断；真正的防透支由结算阶段的
// ConsumeEntitlementIdempotent（FOR UPDATE 行锁 + 幂等）兜底。
//
// usable 计算口径：status=active 且未过期（expires_at 为空或晚于当前时间）且 remaining>0 → true；
// 否则 false。不限量（quota_total 为 NULL）时 remaining 为 NULL，此时只要 status=active 且未过期即 usable=true。
type EntitlementBalanceResult struct {
	EntitlementID uint64           `json:"entitlement_id"`
	UserID        uint64           `json:"user_id"`
	QuotaTotal    *decimal.Decimal `json:"quota_total,omitempty"`
	QuotaUsed     decimal.Decimal  `json:"quota_used"`
	Remaining     *decimal.Decimal `json:"remaining,omitempty"`
	Status        string           `json:"status"`
	ExpiresAt     *time.Time       `json:"expires_at,omitempty"`
	Usable        bool             `json:"usable"`
}
