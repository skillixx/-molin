package model

import "time"

// User 是平台用户主表。
// real_name_status: unverified / pending / verified / rejected
// status: active / disabled
type User struct {
	ID             uint64     `gorm:"primaryKey;autoIncrement"`
	Email          *string    `gorm:"uniqueIndex;size:191"`
	EmailVerified  bool       `gorm:"default:false"`
	Phone          *string    `gorm:"uniqueIndex;size:32"`
	PhoneVerified  bool       `gorm:"default:false"`
	PasswordHash   string     `gorm:"size:255;not null"`
	RealNameStatus string     `gorm:"size:32;default:unverified"`
	RealName       *string    `gorm:"size:128"`
	Status         string     `gorm:"size:32;default:active"`
	WalletID       *uint64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
