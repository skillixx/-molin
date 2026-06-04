package model

import "time"

// VerificationCode 存储邮箱/短信验证码，used_at 为 NULL 表示未使用。
type VerificationCode struct {
	ID          uint64     `gorm:"primaryKey;autoIncrement"`
	TargetType  string     `gorm:"size:32;not null"`  // email / phone
	TargetValue string     `gorm:"size:191;not null"` // 邮箱地址或手机号
	Code        string     `gorm:"size:16;not null"`
	Scene       string     `gorm:"size:32;not null"` // register / login / reset_password
	ExpiresAt   time.Time  `gorm:"not null;index"`
	UsedAt      *time.Time
	CreatedAt   time.Time
}
