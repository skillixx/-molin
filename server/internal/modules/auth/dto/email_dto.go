package dto

import "time"

type EmailTemplateItem struct {
	ID                 uint64     `json:"id"`
	Provider           string     `json:"provider"`
	ProviderTemplateID string     `json:"provider_template_id"`
	Name               string     `json:"name"`
	Subject            string     `json:"subject"`
	ProviderStatus     string     `json:"provider_status"`
	ReviewComment      *string    `json:"review_comment"`
	VariablesComplete  bool       `json:"variables_complete"`
	LocalEnabled       bool       `json:"local_enabled"`
	BoundScenes        []string   `json:"bound_scenes"`
	Missing            bool       `json:"missing"`
	MissingSince       *time.Time `json:"missing_since"`
	LastSyncedAt       time.Time  `json:"last_synced_at"`
	Version            uint64     `json:"version"`
}
type EmailTemplateDetail struct {
	EmailTemplateItem
	SenderNickname *string  `json:"sender_nickname"`
	TemplateText   string   `json:"template_text"`
	Variables      []string `json:"variables"`
	ContentSHA256  string   `json:"content_sha256"`
}
type EmailTemplateStatusReq struct {
	LocalEnabled *bool  `json:"local_enabled"`
	Version      uint64 `json:"version"`
}
type EmailSceneBindingItem struct {
	Scene              string            `json:"scene"`
	DisplayName        string            `json:"display_name"`
	TemplateID         *uint64           `json:"template_id"`
	ProviderTemplateID *string           `json:"provider_template_id"`
	ProviderStatus     *string           `json:"provider_status"`
	LocalEnabled       bool              `json:"local_enabled"`
	VariablesComplete  bool              `json:"variables_complete"`
	Missing            bool              `json:"missing"`
	Enabled            bool              `json:"enabled"`
	VariableMapping    map[string]string `json:"variable_mapping"`
	Version            uint64            `json:"version"`
	UpdatedAt          time.Time         `json:"updated_at"`
}
type EmailSceneBindingReq struct {
	TemplateID uint64 `json:"template_id"`
	Enabled    *bool  `json:"enabled"`
	Version    uint64 `json:"version"`
}
type EmailSyncReq struct {
	Provider string `json:"provider"`
}
type EmailSyncRunItem struct {
	RunID          uint64     `json:"run_id"`
	Provider       string     `json:"provider"`
	Status         string     `json:"status"`
	CreatedCount   uint       `json:"created_count"`
	UpdatedCount   uint       `json:"updated_count"`
	MissingCount   uint       `json:"missing_count"`
	UnchangedCount uint       `json:"unchanged_count"`
	ErrorCode      *string    `json:"error_code"`
	ErrorMessage   *string    `json:"error_message"`
	CreatedBy      uint64     `json:"created_by"`
	StartedAt      time.Time  `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	Idempotent     bool       `json:"idempotent"`
}
type EmailAllowlistAddReq struct {
	Email string `json:"email"`
}
type EmailVersionReq struct {
	Version uint64 `json:"version"`
}
type EmailAllowlistItem struct {
	ID          uint64     `json:"id"`
	EmailMasked string     `json:"email_masked"`
	Status      string     `json:"status"`
	Version     uint64     `json:"version"`
	CreatedBy   uint64     `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

type EmailAllowlistMutationResp struct {
	ID          uint64     `json:"id"`
	EmailMasked string     `json:"email_masked"`
	Status      string     `json:"status"`
	Version     uint64     `json:"version"`
	CreatedAt   time.Time  `json:"created_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}
type EmailTestSendReq struct {
	Scene string `json:"scene"`
	Email string `json:"email"`
}
type EmailSendResult struct {
	SendLogID         uint64    `json:"send_log_id"`
	BusinessRequestNo string    `json:"business_request_no"`
	TemplateID        uint64    `json:"template_id"`
	Scene             string    `json:"scene"`
	RecipientMasked   string    `json:"recipient_masked"`
	Status            string    `json:"status"`
	FailureReason     *string   `json:"failure_reason"`
	Idempotent        bool      `json:"idempotent"`
	SubmittedAt       time.Time `json:"submitted_at"`
}
type EmailSendLogItem struct {
	ID                 uint64    `json:"id"`
	Scene              string    `json:"scene"`
	Purpose            string    `json:"purpose"`
	RecipientMasked    string    `json:"recipient_masked"`
	TemplateID         uint64    `json:"template_id"`
	ProviderTemplateID string    `json:"provider_template_id"`
	BusinessRequestNo  string    `json:"business_request_no"`
	ProviderRequestID  *string   `json:"provider_request_id"`
	Status             string    `json:"status"`
	FailureReason      *string   `json:"failure_reason"`
	SubmittedAt        time.Time `json:"submitted_at"`
}
type EmailSummaryResp struct {
	TemplateTotal       int64      `json:"template_total"`
	ApprovedCount       int64      `json:"approved_count"`
	LocalEnabledCount   int64      `json:"local_enabled_count"`
	UnboundSceneCount   int64      `json:"unbound_scene_count"`
	SubmittedTodayCount int64      `json:"submitted_today_count"`
	FailedTodayCount    int64      `json:"failed_today_count"`
	LastSyncedAt        *time.Time `json:"last_synced_at"`
}
type VerificationSendResp struct {
	Sent      bool   `json:"sent"`
	ExpiresIn int    `json:"expires_in"`
	Code      string `json:"code,omitempty"`
}
