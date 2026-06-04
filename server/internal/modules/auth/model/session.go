package model

import "time"

// UserSession 存储登录会话，DB 只存 Refresh Token 的 HMAC hash。
// revoked_at 为 NULL 表示会话有效；退出/封禁时写入该字段。
type UserSession struct {
	ID               uint64     `gorm:"primaryKey;autoIncrement"`
	UserID           uint64     `gorm:"not null;index"`
	RefreshTokenHash string     `gorm:"size:128;not null;uniqueIndex"`
	UserAgent        string     `gorm:"size:512"`
	IP               string     `gorm:"size:64"`
	ExpiresAt        time.Time  `gorm:"not null;index"`
	RevokedAt        *time.Time
	CreatedAt        time.Time
}
