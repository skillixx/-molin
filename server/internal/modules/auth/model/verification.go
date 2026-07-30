package model

import "time"

// VerificationCode 存储邮箱/短信验证码，used_at 为 NULL 表示未使用。
type VerificationCode struct {
	ID                 uint64  `gorm:"primaryKey;autoIncrement"`
	TargetType         string  `gorm:"size:32;not null"` // email / phone
	TargetValue        *string `gorm:"size:191"`         // 仅手机号兼容链路使用；邮箱必须为 NULL
	TargetHash         *string `gorm:"size:64;index"`    // 邮箱目标使用带密钥 HMAC，禁止保存完整邮箱
	TargetMasked       *string `gorm:"size:191"`         // 邮箱仅保留脱敏展示值
	CodeHash           string  `gorm:"column:code_hash;size:64;not null"`
	Scene              string  `gorm:"size:32;not null"`
	SendStatus         string  `gorm:"size:16;not null"` // pending / accepted / failed
	BusinessRequestNo  *string `gorm:"size:64;uniqueIndex"`
	IdempotencyScope   *string `gorm:"size:191;index"`
	RequestFingerprint *string `gorm:"size:64"`
	AcceptedAt         *time.Time
	ExpiresAt          time.Time `gorm:"not null;index"`
	UsedAt             *time.Time
	CreatedAt          time.Time
}
