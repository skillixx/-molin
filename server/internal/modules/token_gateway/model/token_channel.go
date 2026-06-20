package model

import "time"

// TokenChannel 上游供应商渠道，对应 token_channels 表。
// 自写薄转发器（v3）：首批 OpenAI / DeepSeek / Kimi，全 OpenAI 兼容。
// 上游真实 api_key 以 AES-256-GCM 加密存 APIKeyEncrypted，禁止明文落库、禁止返回响应。
type TokenChannel struct {
	ID   uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	Code string `gorm:"size:64;not null;uniqueIndex:uk_token_channels_code" json:"code"` // 渠道编码，唯一，如 openai/deepseek/kimi
	Name string `gorm:"size:191;not null" json:"name"`                                   // 展示名称
	Type string `gorm:"size:32;not null;default:openai_compatible" json:"type"`          // 渠道类型，默认 openai_compatible
	// BaseURL 上游 API 基础地址，如 https://api.deepseek.com
	BaseURL string `gorm:"size:512;not null" json:"base_url"`
	// APIKeyEncrypted 上游 api_key 的 AES-256-GCM 密文；安全红线：禁止序列化到任何响应，故标 json:"-"
	APIKeyEncrypted string    `gorm:"size:1024;not null" json:"-"`
	Status          string    `gorm:"size:32;not null;default:active" json:"status"` // active 启用 / inactive 停用
	Priority        int       `gorm:"not null;default:0" json:"priority"`            // 优先级，越大越优先
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (TokenChannel) TableName() string { return "token_channels" }
