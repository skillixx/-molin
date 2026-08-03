package model

import "time"

// VerificationCode 存储邮箱/短信验证码，used_at 为 NULL 表示未使用。
type VerificationCode struct {
	ID                uint64 `gorm:"primaryKey;autoIncrement"`
	TargetType        string `gorm:"size:32;not null"`  // email / phone
	TargetValue       string `gorm:"size:191;not null"` // 邮箱地址或手机号
	Code              string `gorm:"type:char(64);not null"`
	Scene             string `gorm:"size:32;not null"` // register / login / reset_password
	SendStatus        string `gorm:"size:32;not null;default:not_applicable;index"`
	SentAt            *time.Time
	Provider          *string   `gorm:"size:32"`
	ProviderRequestID *string   `gorm:"size:128"`
	BusinessRequestID *string   `gorm:"size:64;uniqueIndex"`
	ExpiresAt         time.Time `gorm:"not null;index"`
	UsedAt            *time.Time
	CreatedAt         time.Time
}
