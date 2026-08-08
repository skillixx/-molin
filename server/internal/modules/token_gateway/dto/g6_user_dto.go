package dto

import (
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
)

// PublicPriceSKU 是面向客户的人民币销售价，不包含成本价、毛利或上游信息。
type PublicPriceSKU struct {
	MeterType     string          `json:"meter_type"`
	SaleUnitPrice decimal.Decimal `json:"sale_unit_price"`
	Scale         decimal.Decimal `json:"scale"`
	Currency      string          `json:"currency"`
}

// PublicModelCatalogItem 是 G6 模型市场和详情共用的已发布模型视图。
type PublicModelCatalogItem struct {
	LogicalModelCode          string           `json:"logical_model_code"`
	DisplayName               string           `json:"display_name"`
	ProviderName              string           `json:"provider_name"`
	Description               *string          `json:"description,omitempty"`
	Capabilities              json.RawMessage  `json:"capabilities,omitempty"`
	ContextWindow             uint64           `json:"context_window"`
	Modality                  string           `json:"modality"`
	IntroURL                  *string          `json:"intro_url,omitempty"`
	IntroURLHealthStatus      string           `json:"intro_url_health_status"`
	DocsURL                   *string          `json:"docs_url,omitempty"`
	DocsURLHealthStatus       string           `json:"docs_url_health_status"`
	QuickStartURL             *string          `json:"quick_start_url,omitempty"`
	QuickStartURLHealthStatus string           `json:"quick_start_url_health_status"`
	ReleaseVersionNo          uint64           `json:"release_version_no"`
	PublishedAt               time.Time        `json:"published_at"`
	PriceVersionNo            uint64           `json:"price_version_no"`
	PriceEffectiveAt          time.Time        `json:"price_effective_at"`
	FailureChargePolicy       string           `json:"failure_charge_policy"`
	RoundingMode              string           `json:"rounding_mode"`
	MinimumCharge             string           `json:"minimum_charge"`
	ServiceStatus             string           `json:"service_status"`
	Prices                    []PublicPriceSKU `json:"prices"`
}

// UsageOverview 汇总本人今日和本月的请求、Token 与人民币结算金额。
type UsageOverview struct {
	TodayRequests      int64            `json:"today_requests"`
	TodayInputTokens   decimal.Decimal  `json:"today_input_tokens"`
	TodayOutputTokens  decimal.Decimal  `json:"today_output_tokens"`
	TodayAmount        decimal.Decimal  `json:"today_amount"`
	MonthRequests      int64            `json:"month_requests"`
	MonthInputTokens   decimal.Decimal  `json:"month_input_tokens"`
	MonthOutputTokens  decimal.Decimal  `json:"month_output_tokens"`
	MonthAmount        decimal.Decimal  `json:"month_amount"`
	MonthlyBudget      *decimal.Decimal `json:"monthly_budget,omitempty"`
	MonthlyBudgetUsage *decimal.Decimal `json:"monthly_budget_usage_percent,omitempty"`
	Currency           string           `json:"currency"`
}

// EffectiveResourceLimit 是某一层最终生效的并发、RPM 和 TPM 限制。
type EffectiveResourceLimit struct {
	ScopeType      string           `json:"scope_type"`
	ScopeID        uint64           `json:"scope_id"`
	Name           string           `json:"name"`
	Concurrency    uint64           `json:"concurrency"`
	RPM            uint64           `json:"rpm"`
	TPM            uint64           `json:"tpm"`
	Source         string           `json:"source"`
	BudgetMode     string           `json:"budget_mode,omitempty"`
	DailyBudget    *decimal.Decimal `json:"daily_budget,omitempty"`
	MonthlyBudget  *decimal.Decimal `json:"monthly_budget,omitempty"`
	BudgetOverride *decimal.Decimal `json:"budget_override,omitempty"`
}

// UserResourceLimits 汇总本人、Project 和 Project SK 的当前有效资源限制。
type UserResourceLimits struct {
	User     EffectiveResourceLimit   `json:"user"`
	Projects []EffectiveResourceLimit `json:"projects"`
	APIKeys  []EffectiveResourceLimit `json:"api_keys"`
}

// UserRequestLedgerItem 是用户端请求账本摘要，不包含提示词、响应正文和内部执行路由。
type UserRequestLedgerItem struct {
	RequestID        string           `json:"request_id"`
	ProjectID        uint64           `json:"project_id"`
	ProjectName      string           `json:"project_name"`
	APIKeyID         uint64           `json:"api_key_id"`
	APIKeyName       string           `json:"api_key_name"`
	APIKeyPrefix     string           `json:"api_key_prefix"`
	LogicalModelCode string           `json:"logical_model_code"`
	ModerationStatus string           `json:"moderation_status"`
	ExecutionStatus  string           `json:"execution_status"`
	BillingStatus    string           `json:"billing_status"`
	InputTokens      decimal.Decimal  `json:"input_tokens"`
	OutputTokens     decimal.Decimal  `json:"output_tokens"`
	ReasoningTokens  decimal.Decimal  `json:"reasoning_tokens"`
	CachedTokens     decimal.Decimal  `json:"cached_tokens"`
	QuotedAmount     *decimal.Decimal `json:"quoted_amount,omitempty"`
	SettledAmount    *decimal.Decimal `json:"settled_amount,omitempty"`
	ErrorCode        *string          `json:"error_code,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	CompletedAt      *time.Time       `json:"completed_at,omitempty"`
}

// UserRequestPriceLine 是从请求冻结快照中脱敏得到的销售计价行。
type UserRequestPriceLine struct {
	MeterType     string          `json:"meter_type"`
	MeterSource   string          `json:"meter_source"`
	Quantity      decimal.Decimal `json:"quantity"`
	SaleUnitPrice decimal.Decimal `json:"sale_unit_price"`
	Scale         decimal.Decimal `json:"scale"`
	Amount        decimal.Decimal `json:"amount"`
	Currency      string          `json:"currency"`
}

// UserRequestDetail 在账本摘要上增加价格版本、销售计价行和钱包流水关联。
type UserRequestDetail struct {
	UserRequestLedgerItem
	PriceVersionID       uint64                 `json:"price_version_id"`
	PriceVersionNo       uint64                 `json:"price_version_no"`
	FailureChargePolicy  string                 `json:"failure_charge_policy"`
	RoundingMode         string                 `json:"rounding_mode"`
	MinimumCharge        string                 `json:"minimum_charge"`
	PriceLines           []UserRequestPriceLine `json:"price_lines"`
	WalletHoldID         *uint64                `json:"wallet_hold_id,omitempty"`
	SettleTransactionID  *uint64                `json:"settle_transaction_id,omitempty"`
	ReleaseTransactionID *uint64                `json:"release_transaction_id,omitempty"`
	Dispute              *BillingDisputeResp    `json:"dispute,omitempty"`
}

// BillingDisputeResp 是用户可见的账单申诉状态。
type BillingDisputeResp struct {
	DisputeNo  string     `json:"dispute_no"`
	RequestID  string     `json:"request_id"`
	Reason     string     `json:"reason"`
	Status     string     `json:"status"`
	Resolution *string    `json:"resolution,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}
