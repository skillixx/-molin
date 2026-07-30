package model

import "time"

// EmailProviderTemplate 是 DirectMail 模板的只读本地镜像。
type EmailProviderTemplate struct {
	ID                 uint64  `gorm:"primaryKey;autoIncrement"`
	Provider           string  `gorm:"size:32;not null;uniqueIndex:uk_email_templates_provider_id"`
	ProviderTemplateID string  `gorm:"size:64;not null;uniqueIndex:uk_email_templates_provider_id"`
	Name               string  `gorm:"size:64;not null"`
	Subject            string  `gorm:"size:256;not null"`
	SenderNickname     *string `gorm:"size:64"`
	TemplateText       string  `gorm:"type:mediumtext;not null"`
	VariablesJSON      string  `gorm:"type:json;not null"`
	ContentSHA256      string  `gorm:"size:64;not null"`
	ProviderStatus     string  `gorm:"size:16;not null;index"`
	ReviewComment      *string `gorm:"size:512"`
	VariablesComplete  bool    `gorm:"not null;default:false"`
	LocalEnabled       bool    `gorm:"not null;default:false"`
	Missing            bool    `gorm:"not null;default:false"`
	MissingSince       *time.Time
	ProviderCreatedAt  *time.Time
	LastSyncedAt       time.Time `gorm:"not null"`
	Version            uint64    `gorm:"not null;default:1"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// EmailSceneBinding 保存五个固定认证场景与模板镜像的绑定关系。
type EmailSceneBinding struct {
	ID                  uint64  `gorm:"primaryKey;autoIncrement"`
	Scene               string  `gorm:"size:32;not null;uniqueIndex"`
	Provider            string  `gorm:"size:32;not null"`
	TemplateID          *uint64 `gorm:"index"`
	Enabled             bool    `gorm:"not null;default:false"`
	VariableMappingJSON string  `gorm:"type:json;not null"`
	Version             uint64  `gorm:"not null;default:1"`
	UpdatedBy           *uint64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// EmailTemplateSyncRun 记录一次全局幂等的模板同步结果。
type EmailTemplateSyncRun struct {
	ID                 uint64    `gorm:"primaryKey;autoIncrement"`
	Provider           string    `gorm:"size:32;not null"`
	IdempotencyScope   string    `gorm:"size:128;not null;uniqueIndex:uk_email_sync_idem"`
	IdempotencyKeyHash string    `gorm:"size:64;not null;uniqueIndex:uk_email_sync_idem"`
	RequestFingerprint string    `gorm:"size:64;not null"`
	Status             string    `gorm:"size:16;not null;index"`
	CreatedCount       uint      `gorm:"not null;default:0"`
	UpdatedCount       uint      `gorm:"not null;default:0"`
	MissingCount       uint      `gorm:"not null;default:0"`
	UnchangedCount     uint      `gorm:"not null;default:0"`
	ErrorCode          *string   `gorm:"size:64"`
	ErrorMessage       *string   `gorm:"size:255"`
	CreatedBy          uint64    `gorm:"not null"`
	StartedAt          time.Time `gorm:"not null"`
	CompletedAt        *time.Time
	CreatedAt          time.Time
}

// EmailTestRecipientAllowlist 只保存规范化邮箱的 HMAC 与脱敏展示值。
type EmailTestRecipientAllowlist struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement"`
	EmailHMAC   string `gorm:"size:64;not null;uniqueIndex"`
	EmailMasked string `gorm:"size:191;not null"`
	Status      string `gorm:"size:16;not null;index"`
	Version     uint64 `gorm:"not null;default:1"`
	CreatedBy   uint64 `gorm:"not null"`
	UpdatedBy   uint64 `gorm:"not null"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	RevokedAt   *time.Time
}

// TableName 显式绑定 migration 创建的单数表名，避免 GORM 默认复数化后访问不存在的表。
func (EmailTestRecipientAllowlist) TableName() string {
	return "email_test_recipient_allowlist"
}

// EmailSendLog 只记录供应商同步受理结果，不表达最终送达状态。
type EmailSendLog struct {
	ID                 uint64  `gorm:"primaryKey;autoIncrement"`
	BusinessRequestNo  string  `gorm:"size:64;not null;uniqueIndex"`
	VerificationCodeID *uint64 `gorm:"uniqueIndex"`
	TemplateID         uint64  `gorm:"not null;index"`
	ProviderTemplateID string  `gorm:"size:64;not null"`
	Scene              string  `gorm:"size:32;not null;index"`
	Purpose            string  `gorm:"size:16;not null"`
	RecipientHMAC      string  `gorm:"size:64;not null"`
	RecipientMasked    string  `gorm:"size:191;not null"`
	IdempotencyScope   string  `gorm:"size:191;not null;uniqueIndex:uk_email_send_logs_idem"`
	IdempotencyKeyHash string  `gorm:"size:64;not null;uniqueIndex:uk_email_send_logs_idem"`
	RequestFingerprint string  `gorm:"size:64;not null"`
	Provider           string  `gorm:"size:32;not null"`
	ProviderRequestID  *string `gorm:"size:128"`
	Status             string  `gorm:"size:16;not null;index"`
	FailureReason      *string `gorm:"size:64"`
	ExpiresAt          *time.Time
	SubmittedAt        time.Time `gorm:"not null;index"`
	CreatedAt          time.Time
}

// EmailAdminVerifyBootstrapReceipt 保存一次性首次配置的成功凭据。
// 表中只允许成功结果，不保存原始幂等键、模板正文或任何认证敏感值。
type EmailAdminVerifyBootstrapReceipt struct {
	ID                 uint64    `gorm:"primaryKey;autoIncrement"`
	Scope              string    `gorm:"size:32;not null;uniqueIndex"`
	Provider           string    `gorm:"size:32;not null"`
	ProviderTemplateID string    `gorm:"size:64;not null"`
	TemplateID         uint64    `gorm:"not null"`
	IdempotencyKeyHash string    `gorm:"size:64;not null;uniqueIndex"`
	RequestFingerprint string    `gorm:"size:64;not null"`
	CompletedBy        uint64    `gorm:"not null"`
	CreatedAt          time.Time `gorm:"not null"`
}
