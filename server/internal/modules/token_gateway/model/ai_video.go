package model

import (
	"encoding/json"
	"time"
)

const (
	// AIVideoCapability 是共享请求、报价和任务表的视频能力标识。
	AIVideoCapability = "video.generate"
	// AIVideoOperationTextToVideo 表示不绑定参考图的文生视频任务。
	AIVideoOperationTextToVideo = "text_to_video"
	// AIVideoOperationImageToVideo 表示恰好绑定一张规范化参考图的图生视频任务。
	AIVideoOperationImageToVideo = "image_to_video"

	AIUploadSourcePlatformPresigned     = "platform_presigned"
	AIUploadSourceOpenAIInlineMultipart = "openai_inline_multipart"
	AIInputSourceGatewayAssetSnapshot   = "gateway_asset_snapshot"
	AIUploadPurposeVideoReferenceImage  = "video_reference_image"
	AIInputMIMEPNG                      = "image/png"
	AIInputMIMEJPEG                     = "image/jpeg"

	AIUploadSessionCreated   = "created"
	AIUploadSessionUploading = "uploading"
	AIUploadSessionVerifying = "verifying"
	AIUploadSessionCompleted = "completed"
	AIUploadSessionRejected  = "rejected"
	AIUploadSessionCancelled = "cancelled"
	AIUploadSessionExpired   = "expired"

	AIInputAssetPending       = "pending"
	AIInputAssetNormalizing   = "normalizing"
	AIInputAssetModerating    = "moderating"
	AIInputAssetReady         = "ready"
	AIInputAssetRejected      = "rejected"
	AIInputAssetQuarantined   = "quarantined"
	AIInputAssetPendingDelete = "pending_delete"
	AIInputAssetExpiring      = "expiring"
	AIInputAssetDeleting      = "deleting"
	AIInputAssetDeleted       = "deleted"
	AIInputAssetDeleteFailed  = "delete_failed"

	AITaskInputReferenceImage = "reference_image"
)

// AIUploadSession 保存上传入口的归属和完成事实；对象位置及上游版本标识只供内部校验使用。
type AIUploadSession struct {
	ID                uint64     `gorm:"primaryKey;autoIncrement;uniqueIndex:uk_ai_upload_sessions_owner,priority:1" json:"-"`
	PublicID          string     `gorm:"size:128;not null;uniqueIndex:uk_ai_upload_sessions_public_id" json:"upload_id"`
	UserID            uint64     `gorm:"not null;uniqueIndex:uk_ai_upload_sessions_owner,priority:2;uniqueIndex:uk_ai_upload_sessions_object_owner,priority:1;index:idx_ai_upload_sessions_owner_status,priority:1" json:"user_id"`
	ProjectID         uint64     `gorm:"not null;uniqueIndex:uk_ai_upload_sessions_owner,priority:3;uniqueIndex:uk_ai_upload_sessions_object_owner,priority:2;index:idx_ai_upload_sessions_owner_status,priority:2" json:"project_id"`
	APIKeyID          *uint64    `json:"api_key_id,omitempty"`
	Purpose           string     `gorm:"size:32;not null;default:video_reference_image" json:"purpose"`
	SourceType        string     `gorm:"size:32;not null" json:"source_type"`
	Status            string     `gorm:"size:16;not null;default:created;index:idx_ai_upload_sessions_owner_status,priority:3" json:"status"`
	MIMEType          string     `gorm:"size:64;not null" json:"mime_type"`
	SizeBytes         uint64     `gorm:"not null" json:"size_bytes"`
	Bucket            string     `gorm:"size:128;not null;uniqueIndex:uk_ai_upload_sessions_object_owner,priority:3" json:"-"`
	ObjectKey         string     `gorm:"size:512;not null;uniqueIndex:uk_ai_upload_sessions_object_owner,priority:4" json:"-"`
	SourceETag        *string    `gorm:"column:source_etag;size:191" json:"-"`
	SourceVersionID   *string    `gorm:"size:191" json:"-"`
	FinalInputAssetID *uint64    `json:"input_asset_id,omitempty"`
	ExpiresAt         time.Time  `gorm:"index:idx_ai_upload_sessions_expiry" json:"expires_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	RejectedAt        *time.Time `json:"rejected_at,omitempty"`
	CancelledAt       *time.Time `json:"cancelled_at,omitempty"`
	ExpiredAt         *time.Time `json:"expired_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// TableName 指定视频上传会话表名。
func (AIUploadSession) TableName() string { return "ai_upload_sessions" }

// AIGatewayInputAsset 是一次不可变的规范化输入快照；不复用来源对象键或临时签名地址。
type AIGatewayInputAsset struct {
	ID                      uint64     `gorm:"primaryKey;autoIncrement;uniqueIndex:uk_ai_gateway_input_assets_owner,priority:1" json:"-"`
	PublicID                string     `gorm:"size:128;not null;uniqueIndex:uk_ai_gateway_input_assets_public_id" json:"input_asset_id"`
	UserID                  uint64     `gorm:"not null;uniqueIndex:uk_ai_gateway_input_assets_owner,priority:2;index:idx_ai_gateway_input_assets_owner_state,priority:1" json:"user_id"`
	ProjectID               uint64     `gorm:"not null;uniqueIndex:uk_ai_gateway_input_assets_owner,priority:3;index:idx_ai_gateway_input_assets_owner_state,priority:2" json:"project_id"`
	SourceType              string     `gorm:"size:32;not null" json:"source_type"`
	UploadSessionID         *uint64    `gorm:"uniqueIndex:uk_ai_gateway_input_assets_upload" json:"upload_session_id,omitempty"`
	SourceGatewayAssetID    *uint64    `gorm:"index:idx_ai_gateway_input_assets_source_asset" json:"source_gateway_asset_id,omitempty"`
	Bucket                  *string    `gorm:"size:128" json:"-"`
	ObjectKey               *string    `gorm:"size:512" json:"-"`
	OriginalSHA256          string     `gorm:"size:64;not null" json:"-"`
	NormalizedSHA256        *string    `gorm:"size:64" json:"-"`
	MIMEType                *string    `gorm:"size:64" json:"mime_type,omitempty"`
	SizeBytes               *uint64    `json:"size_bytes,omitempty"`
	Width                   *uint32    `json:"width,omitempty"`
	Height                  *uint32    `json:"height,omitempty"`
	ModerationPolicyVersion *string    `gorm:"size:64" json:"moderation_policy_version,omitempty"`
	ModerationStatus        string     `gorm:"size:32;not null;default:pending" json:"moderation_status"`
	VersionNo               uint64     `gorm:"not null;default:1" json:"version_no"`
	LifecycleState          string     `gorm:"size:32;not null;default:normalizing;index:idx_ai_gateway_input_assets_owner_state,priority:3;index:idx_ai_gateway_input_assets_cleanup,priority:1" json:"lifecycle_state"`
	ExpiresAt               time.Time  `gorm:"index:idx_ai_gateway_input_assets_cleanup,priority:3" json:"expires_at"`
	LegalHold               bool       `gorm:"not null;default:0;index:idx_ai_gateway_input_assets_cleanup,priority:2" json:"legal_hold"`
	DeleteRequestedAt       *time.Time `json:"delete_requested_at,omitempty"`
	PendingDeleteAt         *time.Time `gorm:"index:idx_ai_gateway_input_assets_cleanup,priority:4" json:"pending_delete_at,omitempty"`
	DeletedAt               *time.Time `json:"deleted_at,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

// TableName 指定规范化输入资产表名。
func (AIGatewayInputAsset) TableName() string { return "ai_gateway_input_assets" }

// AIGatewayTaskInput 只绑定任务与已冻结输入快照，不再记录上传或已有资产的门面来源。
type AIGatewayTaskInput struct {
	ID               uint64     `gorm:"primaryKey;autoIncrement" json:"-"`
	TaskID           uint64     `gorm:"not null;uniqueIndex:uk_ai_gateway_task_inputs_task_role_ordinal,priority:1;uniqueIndex:uk_ai_gateway_task_inputs_task_asset,priority:1;index:idx_ai_gateway_task_inputs_asset" json:"task_id"`
	InputAssetID     uint64     `gorm:"not null;uniqueIndex:uk_ai_gateway_task_inputs_task_asset,priority:2;index:idx_ai_gateway_task_inputs_asset" json:"input_asset_id"`
	UserID           uint64     `gorm:"not null" json:"user_id"`
	ProjectID        uint64     `gorm:"not null" json:"project_id"`
	Role             string     `gorm:"size:32;not null;uniqueIndex:uk_ai_gateway_task_inputs_task_role_ordinal,priority:2" json:"role"`
	Ordinal          uint32     `gorm:"not null;default:0;uniqueIndex:uk_ai_gateway_task_inputs_task_role_ordinal,priority:3" json:"ordinal"`
	NormalizedSHA256 string     `gorm:"size:64;not null" json:"-"`
	InputVersion     uint64     `gorm:"not null" json:"input_version"`
	LeaseReleasedAt  *time.Time `json:"lease_released_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// TableName 指定任务输入绑定表名。
func (AIGatewayTaskInput) TableName() string { return "ai_gateway_task_inputs" }

// AIGatewayTaskEvent 保存任务状态的追加式审计事件，事件数据不进入普通JSON响应。
type AIGatewayTaskEvent struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement" json:"-"`
	EventID        string          `gorm:"size:128;not null;uniqueIndex:uk_ai_gateway_task_events_event_id" json:"event_id"`
	TaskID         uint64          `gorm:"not null;index:idx_ai_gateway_task_events_task_created,priority:1" json:"task_id"`
	UserID         uint64          `gorm:"not null" json:"user_id"`
	ProjectID      uint64          `gorm:"not null" json:"project_id"`
	EventType      string          `gorm:"size:64;not null" json:"event_type"`
	FromStatus     *string         `gorm:"size:32" json:"from_status,omitempty"`
	ToStatus       *string         `gorm:"size:32" json:"to_status,omitempty"`
	Source         string          `gorm:"size:32;not null" json:"source"`
	SafeDetailJSON json.RawMessage `gorm:"type:json" json:"-"`
	CreatedAt      time.Time       `gorm:"index:idx_ai_gateway_task_events_task_created,priority:2" json:"created_at"`
}

// TableName 指定追加式任务事件表名。
func (AIGatewayTaskEvent) TableName() string { return "ai_gateway_task_events" }

// AIGatewayProviderCallbackEvent 保存回调去重、验签和应用结果；严禁保存原始回调正文。
type AIGatewayProviderCallbackEvent struct {
	ID                    uint64          `gorm:"primaryKey;autoIncrement" json:"-"`
	TaskID                *uint64         `gorm:"index:idx_ai_gateway_callback_events_task_received,priority:1" json:"task_id,omitempty"`
	UserID                *uint64         `json:"user_id,omitempty"`
	ProjectID             *uint64         `json:"project_id,omitempty"`
	ProviderCode          string          `gorm:"size:64;not null;uniqueIndex:uk_ai_gateway_provider_callbacks_replay,priority:1" json:"-"`
	ProviderTaskID        string          `gorm:"size:191;not null;uniqueIndex:uk_ai_gateway_provider_callbacks_replay,priority:2" json:"-"`
	ExternalEventID       string          `gorm:"size:191;not null;uniqueIndex:uk_ai_gateway_provider_callbacks_replay,priority:3" json:"-"`
	BodySHA256            string          `gorm:"size:64;not null" json:"-"`
	SignatureStatus       string          `gorm:"size:16;not null" json:"-"`
	ApplicationResultJSON json.RawMessage `gorm:"type:json" json:"-"`
	ProcessStatus         string          `gorm:"size:16;not null;default:received" json:"process_status"`
	ReceivedAt            time.Time       `gorm:"index:idx_ai_gateway_callback_events_task_received,priority:2" json:"received_at"`
	ProcessedAt           *time.Time      `json:"processed_at,omitempty"`
}

// TableName 指定Provider回调事件表名。
func (AIGatewayProviderCallbackEvent) TableName() string {
	return "ai_gateway_provider_callback_events"
}

// AIGatewayTaskPayload 保存任务敏感载荷的密文信封，任何密钥材料和明文都不得进入该表。
type AIGatewayTaskPayload struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement" json:"-"`
	TaskID           uint64    `gorm:"not null;uniqueIndex:uk_ai_gateway_task_payloads_kind,priority:1" json:"task_id"`
	UserID           uint64    `gorm:"not null" json:"user_id"`
	ProjectID        uint64    `gorm:"not null" json:"project_id"`
	PayloadKind      string    `gorm:"size:32;not null;uniqueIndex:uk_ai_gateway_task_payloads_kind,priority:2" json:"payload_kind"`
	Ciphertext       []byte    `gorm:"type:longblob;not null" json:"-"`
	Nonce            []byte    `gorm:"type:varbinary(32);not null" json:"-"`
	KeyVersion       string    `gorm:"size:64;not null" json:"-"`
	AADSHA256        string    `gorm:"column:aad_sha256;size:64;not null" json:"-"`
	CiphertextSHA256 string    `gorm:"size:64;not null" json:"-"`
	CreatedAt        time.Time `json:"created_at"`
}

// TableName 指定加密任务载荷表名。
func (AIGatewayTaskPayload) TableName() string { return "ai_gateway_task_payloads" }
