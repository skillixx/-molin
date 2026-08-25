package dto

import "time"

type ImageGenerationReq struct {
	Model        string `json:"model"`
	Prompt       string `json:"prompt"`
	N            uint64 `json:"n,omitempty"`
	Size         string `json:"size,omitempty"`
	Quality      string `json:"quality,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
	User         string `json:"user,omitempty"`
	ProjectID    uint64 `json:"project_id,omitempty"`
	QuoteID      string `json:"quote_id,omitempty"`
}

type ImageQuoteReq struct {
	Model        string `json:"model"`
	Prompt       string `json:"prompt"`
	N            uint64 `json:"n,omitempty"`
	Size         string `json:"size,omitempty"`
	Quality      string `json:"quality,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
	ProjectID    uint64 `json:"project_id,omitempty"`
}

type ImageQuoteLineResp struct {
	MetricCode    string            `json:"metric_code"`
	Variant       map[string]string `json:"variant"`
	UsageAmount   string            `json:"usage_amount"`
	UnitSize      string            `json:"unit_size"`
	SaleUnitPrice string            `json:"sale_unit_price"`
	Subtotal      string            `json:"subtotal"`
}

type ImageQuoteResp struct {
	QuoteID          string               `json:"quote_id"`
	LogicalModelCode string               `json:"logical_model_code"`
	PriceVersionNo   uint64               `json:"price_version_no"`
	Currency         string               `json:"currency"`
	EstimatedAmount  string               `json:"estimated_amount"`
	ExpiresAt        time.Time            `json:"expires_at"`
	Lines            []ImageQuoteLineResp `json:"lines"`
}

type ImageTaskResp struct {
	TaskID           string           `json:"task_id"`
	RequestID        string           `json:"request_id"`
	LogicalModelCode string           `json:"logical_model_code"`
	Status           string           `json:"status"`
	Progress         uint8            `json:"progress"`
	ExecutionStatus  string           `json:"execution_status"`
	BillingStatus    string           `json:"billing_status"`
	DeliveryStatus   string           `json:"delivery_status"`
	QuotedAmount     *string          `json:"quoted_amount"`
	SettledAmount    *string          `json:"settled_amount"`
	ErrorCode        *string          `json:"error_code"`
	CreatedAt        time.Time        `json:"created_at"`
	CompletedAt      *time.Time       `json:"completed_at"`
	Assets           []ImageAssetResp `json:"assets"`
	Existing         bool             `json:"existing"`
}

type ImageAssetResp struct {
	AssetID          string    `json:"asset_id"`
	RequestID        string    `json:"request_id"`
	Role             string    `json:"role"`
	ResultIndex      uint32    `json:"result_index"`
	MIMEType         *string   `json:"mime_type"`
	Width            *uint32   `json:"width"`
	Height           *uint32   `json:"height"`
	SizeBytes        *uint64   `json:"size_bytes"`
	LifecycleState   string    `json:"lifecycle_state"`
	ModerationStatus string    `json:"moderation_status"`
	DisputeStatus    string    `json:"dispute_status"`
	CreatedAt        time.Time `json:"created_at"`
}

type ImageDownloadResp struct {
	AssetID   string    `json:"asset_id"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type OpenAIImageDataResp struct {
	URL          string    `json:"url"`
	MolinAssetID string    `json:"molin_asset_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type OpenAIImageGenerationResp struct {
	Created        int64                 `json:"created"`
	Data           []OpenAIImageDataResp `json:"data"`
	MolinRequestID string                `json:"molin_request_id"`
}

type ImageAdminTaskResp struct {
	ImageTaskResp
	UserID    uint64  `json:"user_id"`
	ProjectID uint64  `json:"project_id"`
	APIKeyID  *uint64 `json:"api_key_id"`
}

type ImageAdminAssetResp struct {
	ImageAssetResp
	UserID    uint64 `json:"user_id"`
	ProjectID uint64 `json:"project_id"`
	TaskID    uint64 `json:"task_id"`
	LegalHold bool   `json:"legal_hold"`
	VersionNo uint64 `json:"version_no"`
}

type ImageQuarantineReq struct {
	Reason    string `json:"reason"`
	VersionNo uint64 `json:"version_no"`
}

type ImageReconcileReq struct {
	Reason string `json:"reason"`
}

type ImageReconciliationSummaryResp struct {
	SettlementPending    int64  `json:"settlement_pending"`
	ActiveCompensations  int64  `json:"active_compensations"`
	DeadCompensations    int64  `json:"dead_compensations"`
	OutboxPending        int64  `json:"outbox_pending"`
	OutboxDead           int64  `json:"outbox_dead"`
	UnreleasedHoldAmount string `json:"unreleased_hold_amount"`
}
