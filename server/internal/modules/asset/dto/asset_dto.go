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
