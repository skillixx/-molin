package model

import "time"

// AdminSummary 是短信管理概览的一次数据库聚合结果。
type AdminSummary struct {
	TemplateTotal     int64      `json:"template_total"`
	ApprovedTotal     int64      `json:"approved_total"`
	EnabledTotal      int64      `json:"enabled_total"`
	BoundSceneTotal   int64      `json:"bound_scene_total"`
	UnboundSceneTotal int64      `json:"unbound_scene_total"`
	LastSyncedAt      *time.Time `json:"last_synced_at"`
}

// TemplateSnapshot 是一次完整供应商查询完成后的本地落库输入，避免在数据库事务中执行外部调用。
type TemplateSnapshot struct {
	Provider            string
	TemplateCode        string
	TemplateName        string
	TemplateType        string
	Content             string
	Variables           []string
	ProviderAuditStatus string
	RejectionReason     *string
	ProviderUpdatedAt   *time.Time
}

// TemplateSyncResult 返回本次同步的可核对统计，不包含供应商密钥或原始响应。
type TemplateSyncResult struct {
	CreatedCount   int64     `json:"created_count"`
	UpdatedCount   int64     `json:"updated_count"`
	UnchangedCount int64     `json:"unchanged_count"`
	IgnoredCount   int64     `json:"ignored_count"`
	TotalCount     int64     `json:"total_count"`
	LastSyncedAt   time.Time `json:"last_synced_at"`
}

type TemplateListFilter struct {
	Keyword     string
	AuditStatus string
	Enabled     *bool
	Scene       string
	Offset      int
	Limit       int
}

type SendLogListFilter struct {
	Scene             string
	Status            string
	TemplateID        uint64
	BusinessRequestID string
	StartTime         *time.Time
	EndTime           *time.Time
	Offset            int
	Limit             int
}

// Template 保存阿里云模板同步快照；模板编码、正文和审核状态均以数据库记录为准。
type Template struct {
	ID                  uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Provider            string     `gorm:"size:32;not null;uniqueIndex:uk_sms_templates_provider_code" json:"provider"`
	TemplateCode        string     `gorm:"size:64;not null;uniqueIndex:uk_sms_templates_provider_code" json:"template_code"`
	TemplateName        string     `gorm:"size:128;not null" json:"template_name"`
	TemplateType        string     `gorm:"size:32;not null;default:verification" json:"template_type"`
	ProviderAuditStatus string     `gorm:"size:32;not null;index" json:"provider_audit_status"`
	RejectionReason     *string    `gorm:"size:255" json:"rejection_reason"`
	ProviderUpdatedAt   *time.Time `json:"provider_updated_at"`
	Content             string     `gorm:"type:text;not null" json:"content"`
	Variables           []string   `gorm:"column:variables_json;serializer:json" json:"variables"`
	LocalEnabled        bool       `gorm:"not null;default:false;index" json:"local_enabled"`
	Version             uint64     `gorm:"not null;default:1" json:"version"`
	LastSyncedAt        time.Time  `gorm:"not null" json:"last_synced_at"`
	BoundScenes         []string   `gorm:"-" json:"bound_scenes"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

func (Template) TableName() string { return "sms_templates" }

// SceneBinding 将固定业务场景绑定到一份已审核的模板快照。
type SceneBinding struct {
	ID         uint64 `gorm:"primaryKey;autoIncrement"`
	Scene      string `gorm:"size:32;not null;uniqueIndex"`
	TemplateID uint64 `gorm:"not null;index"`
	SignName   string `gorm:"size:128;not null"`
	Enabled    bool   `gorm:"not null;default:false;index"`
	Version    uint64 `gorm:"not null;default:1"`
	CreatedBy  *uint64
	UpdatedBy  *uint64
	Template   Template `gorm:"foreignKey:TemplateID"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (SceneBinding) TableName() string { return "sms_scene_bindings" }

// AdminScene 是固定五场景的管理端视图，未绑定场景保留空值而不是从列表消失。
type AdminScene struct {
	Scene               string     `json:"scene"`
	TemplateID          *uint64    `json:"template_id"`
	TemplateCode        *string    `json:"template_code"`
	TemplateName        *string    `json:"template_name"`
	ProviderAuditStatus *string    `json:"provider_audit_status"`
	SignName            *string    `json:"sign_name"`
	Enabled             bool       `json:"enabled"`
	Version             uint64     `json:"version"`
	UpdatedBy           *uint64    `json:"updated_by"`
	UpdatedAt           *time.Time `json:"updated_at"`
}

// SendLog 只保存脱敏排障字段，禁止加入验证码、完整手机号或供应商原始响应。
type SendLog struct {
	ID                      uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Purpose                 string     `gorm:"size:16;not null;default:otp" json:"purpose"`
	Scene                   string     `gorm:"size:32;not null;index" json:"scene"`
	PhoneMasked             string     `gorm:"size:32;not null" json:"phone_masked"`
	PhoneHMAC               string     `gorm:"size:64;not null;index" json:"-"`
	TemplateID              *uint64    `gorm:"index" json:"template_id"`
	TemplateCode            string     `gorm:"size:64;not null;index" json:"template_code"`
	SignName                string     `gorm:"size:128;not null" json:"sign_name"`
	Provider                string     `gorm:"size:32;not null;index" json:"provider"`
	BusinessRequestID       string     `gorm:"size:64;not null;uniqueIndex" json:"business_request_id"`
	IdempotencyScope        *string    `gorm:"size:191;uniqueIndex:uk_sms_send_logs_idempotency" json:"-"`
	IdempotencyKeyHash      *string    `gorm:"size:64;uniqueIndex:uk_sms_send_logs_idempotency" json:"-"`
	IdempotencyOwnerKeyHash *string    `gorm:"size:64;uniqueIndex:uk_sms_send_logs_owner_key" json:"-"`
	RequestFingerprint      *string    `gorm:"size:64" json:"-"`
	ProviderRequestID       *string    `gorm:"size:128" json:"provider_request_id"`
	ProviderCode            *string    `gorm:"size:64" json:"provider_code"`
	SubmitStatus            string     `gorm:"size:32;not null;index" json:"submit_status"`
	FailureSummary          *string    `gorm:"size:255" json:"failure_summary"`
	SubmittedAt             time.Time  `json:"submitted_at"`
	CompletedAt             *time.Time `json:"completed_at"`
	CreatedAt               time.Time  `json:"-"`
}

func (SendLog) TableName() string { return "sms_send_logs" }
