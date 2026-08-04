package dto

import (
	"encoding/json"
	"time"

	"molin/server/internal/modules/token_gateway/model"
)

type G5DashboardResp struct {
	From                time.Time `json:"from"`
	To                  time.Time `json:"to"`
	TotalRequests       int64     `json:"total_requests"`
	SuccessfulRequests  int64     `json:"successful_requests"`
	SuccessRate         string    `json:"success_rate"`
	TotalTokens         string    `json:"total_tokens"`
	SaleAmount          string    `json:"sale_amount"`
	UpstreamCost        string    `json:"upstream_cost"`
	GrossProfit         string    `json:"gross_profit"`
	SafetyRejections    int64     `json:"safety_rejections"`
	RateLimitRejections int64     `json:"rate_limit_rejections"`
	BudgetRejections    int64     `json:"budget_rejections"`
	ActiveModels        int64     `json:"active_models"`
	ActiveChannels      int64     `json:"active_channels"`
	UnhealthyChannels   int64     `json:"unhealthy_channels"`
	ActivePrices        int64     `json:"active_prices"`
	ActiveRoutes        int64     `json:"active_routes"`
	PendingExceptions   int64     `json:"pending_exceptions"`
	OpenBudgetAlerts    int64     `json:"open_budget_alerts"`
	OpenCompensations   int64     `json:"open_compensations"`
}

type G5DashboardQuery struct {
	From      time.Time
	To        time.Time
	Model     string
	ChannelID uint64
	Status    string
}

type PublishModelReq struct {
	Reason string `json:"reason"`
}

type RollbackModelReq struct {
	TargetVersionNo uint64 `json:"target_version_no"`
	Reason          string `json:"reason"`
}

type RouteWriteReq struct {
	LogicalModelCode        string `json:"logical_model_code"`
	ChannelID               uint64 `json:"channel_id"`
	ProviderModel           string `json:"provider_model"`
	Priority                int    `json:"priority"`
	Weight                  uint64 `json:"weight"`
	TimeoutMS               uint64 `json:"timeout_ms"`
	MaxRetries              uint64 `json:"max_retries"`
	CircuitBreakerThreshold uint64 `json:"circuit_breaker_threshold"`
	FallbackOrder           uint64 `json:"fallback_order"`
	Status                  string `json:"status"`
	VersionNo               uint64 `json:"version_no"`
}

type PriceSKUReq struct {
	MeterType     string          `json:"meter_type"`
	Variant       json.RawMessage `json:"variant,omitempty"`
	CostUnitPrice string          `json:"cost_unit_price"`
	SaleUnitPrice string          `json:"sale_unit_price"`
	Scale         string          `json:"scale"`
}

type CreatePriceReq struct {
	LogicalModelCode string        `json:"logical_model_code"`
	MinMarginRate    string        `json:"min_margin_rate"`
	MaxInputTokens   uint64        `json:"max_input_tokens"`
	MaxOutputTokens  uint64        `json:"max_output_tokens"`
	CostUpdatedAt    time.Time     `json:"cost_updated_at"`
	CostExpiresAt    time.Time     `json:"cost_expires_at"`
	EffectiveAt      time.Time     `json:"effective_at"`
	ExpiresAt        *time.Time    `json:"expires_at"`
	SKUs             []PriceSKUReq `json:"skus"`
}

type PriceDetailResp struct {
	Version *model.AIPriceVersion `json:"version"`
	SKUs    []model.AIPriceSKU    `json:"skus"`
}

type PriceStatusReq struct {
	Reason string `json:"reason"`
}

type RollbackPriceReq struct {
	Reason        string    `json:"reason"`
	EffectiveAt   time.Time `json:"effective_at"`
	CostExpiresAt time.Time `json:"cost_expires_at"`
}
