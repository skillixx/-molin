package model

import "time"

// User 是平台用户主表。
// real_name_status: unverified / pending / verified / rejected
// status: active / disabled
type User struct {
	ID                   uint64     `gorm:"primaryKey;autoIncrement"`
	Username             *string    `gorm:"uniqueIndex;size:64"`             // 用户名，可选，唯一
	Nickname             *string    `gorm:"size:64"`                         // 昵称，可选
	AvatarURL            *string    `gorm:"column:avatar_url;size:512"`      // 头像地址
	Email                *string    `gorm:"uniqueIndex;size:191"`
	EmailVerified        bool       `gorm:"default:false"`
	Phone                *string    `gorm:"uniqueIndex;size:32"`
	PhoneVerified        bool       `gorm:"default:false"`
	PasswordHash         string     `gorm:"size:255;not null"`
	RealNameStatus       string     `gorm:"size:32;default:unverified"`
	RealName             *string    `gorm:"size:128"`
	Status               string     `gorm:"size:32;default:active"`
	WalletID             *uint64
	AdminPhoneVerifiedAt *time.Time // 管理员手机号认证时间
	AdminEmailVerifiedAt *time.Time // 管理员邮箱认证时间
	CreatedAt            time.Time
	UpdatedAt            time.Time
}
