package model

import "time"

// Template 保存阿里云模板同步快照；模板编码、正文和审核状态均以数据库记录为准。
type Template struct {
	ID                  uint64    `gorm:"primaryKey;autoIncrement"`
	Provider            string    `gorm:"size:32;not null;uniqueIndex:uk_sms_templates_provider_code"`
	TemplateCode        string    `gorm:"size:64;not null;uniqueIndex:uk_sms_templates_provider_code"`
	TemplateName        string    `gorm:"size:128;not null"`
	ProviderAuditStatus string    `gorm:"size:32;not null;index"`
	Content             string    `gorm:"type:text;not null"`
	LocalEnabled        bool      `gorm:"not null;default:false;index"`
	Version             uint64    `gorm:"not null;default:1"`
	LastSyncedAt        time.Time `gorm:"not null"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
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
	UpdatedBy  *uint64
	Template   Template `gorm:"foreignKey:TemplateID"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (SceneBinding) TableName() string { return "sms_scene_bindings" }

// SendLog 只保存脱敏排障字段，禁止加入验证码、完整手机号或供应商原始响应。
type SendLog struct {
	ID                uint64  `gorm:"primaryKey;autoIncrement"`
	Scene             string  `gorm:"size:32;not null;index"`
	PhoneMasked       string  `gorm:"size:32;not null"`
	PhoneHMAC         string  `gorm:"size:64;not null;index"`
	TemplateID        *uint64 `gorm:"index"`
	TemplateCode      string  `gorm:"size:64;not null;index"`
	SignName          string  `gorm:"size:128;not null"`
	Provider          string  `gorm:"size:32;not null;index"`
	BusinessRequestID string  `gorm:"size:64;not null;uniqueIndex"`
	ProviderRequestID *string `gorm:"size:128"`
	ProviderCode      *string `gorm:"size:64"`
	SubmitStatus      string  `gorm:"size:32;not null;index"`
	FailureSummary    *string `gorm:"size:255"`
	CreatedAt         time.Time
}

func (SendLog) TableName() string { return "sms_send_logs" }
