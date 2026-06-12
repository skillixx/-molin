package model

import "time"

// AuditLog 操作审计日志，记录管理员对各模块资源的操作。
// 表名：audit_logs（migration 000002 已建表，本次不新增 migration）。
type AuditLog struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement"`
	OperatorID     *uint64   `gorm:"index"`
	Module         string    `gorm:"size:64;not null"`
	Action         string    `gorm:"size:64;not null"`
	TargetType     *string   `gorm:"size:64"`
	TargetID       *string   `gorm:"size:128"`
	IP             *string   `gorm:"size:64"`
	RequestSummary *string   `gorm:"type:json"` // JSON 格式请求摘要
	CreatedAt      time.Time `gorm:"index"`
}
