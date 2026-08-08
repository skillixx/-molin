package model

import "time"

// AIBillingDispute 保存用户对本人 AI 请求账单发起的可追踪申诉。
// 该表只保存请求标识和必要说明，禁止写入提示词、模型响应或完整密钥。
type AIBillingDispute struct {
	ID         uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	DisputeNo  string     `gorm:"size:64;not null;uniqueIndex" json:"dispute_no"`
	RequestID  string     `gorm:"size:128;not null;uniqueIndex:uk_ai_billing_disputes_request_user,priority:1" json:"request_id"`
	UserID     uint64     `gorm:"not null;uniqueIndex:uk_ai_billing_disputes_request_user,priority:2" json:"-"`
	Reason     string     `gorm:"size:1000;not null" json:"reason"`
	Status     string     `gorm:"size:24;not null;default:submitted" json:"status"`
	Resolution *string    `gorm:"size:1000" json:"resolution,omitempty"`
	ResolvedBy *uint64    `json:"-"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (AIBillingDispute) TableName() string { return "ai_billing_disputes" }
