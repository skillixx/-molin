package model

import "time"

// LoginLog 记录每次登录尝试（成功/失败均记录）。
type LoginLog struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement"`
	UserID       *uint64   `gorm:"index"`
	LoginType    string    `gorm:"size:32;not null"` // email / phone
	LoginAccount string    `gorm:"size:191;not null"`
	IP           string    `gorm:"size:64"`
	UserAgent    string    `gorm:"size:512"`
	Status       string    `gorm:"size:32;not null"` // success / failed
	CreatedAt    time.Time `gorm:"index"`
}

func (LoginLog) TableName() string { return "user_login_logs" }
