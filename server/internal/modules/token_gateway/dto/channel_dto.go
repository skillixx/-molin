package dto

import "time"

// CreateChannelReq 创建渠道请求体。
// APIKeyPlaintext 为上游真实 api_key 明文，服务层会用 AES-256-GCM 加密后落库；
// 安全红线：明文/密文 api_key 绝不出现在任何响应中。
type CreateChannelReq struct {
	Code            string `json:"code"`              // 渠道编码，唯一，如 openai/deepseek/kimi
	Name            string `json:"name"`              // 展示名称
	Type            string `json:"type"`              // 渠道类型，空则默认 openai_compatible
	BaseURL         string `json:"base_url"`          // 上游 API 基础地址
	APIKeyPlaintext string `json:"api_key_plaintext"` // 上游 api_key 明文（仅入参，加密落库）
	Status          string `json:"status"`            // 空则默认 active
	Priority        int    `json:"priority"`          // 优先级
}

// UpdateChannelReq 更新渠道请求体，字段均为指针，nil 表示不更新。
// APIKeyPlaintext 非 nil 且非空时才重新加密覆盖；为保护已存 key，nil 不动。
type UpdateChannelReq struct {
	Name            *string `json:"name"`
	Type            *string `json:"type"`
	BaseURL         *string `json:"base_url"`
	APIKeyPlaintext *string `json:"api_key_plaintext"`
	Status          *string `json:"status"`
	Priority        *int    `json:"priority"`
}

// ChannelResp 渠道响应体。
// 安全红线：不含 api_key_plaintext / api_key_encrypted 任一字段；改用 HasAPIKey 表征是否已配置。
type ChannelResp struct {
	ID                   uint64     `json:"id"`
	Code                 string     `json:"code"`
	Name                 string     `json:"name"`
	Type                 string     `json:"type"`
	BaseURL              string     `json:"base_url"`
	HasAPIKey            bool       `json:"has_api_key"` // 是否已配置上游 api_key（不暴露 key 本身）
	Status               string     `json:"status"`
	Priority             int        `json:"priority"`
	HealthStatus         string     `json:"health_status"`
	LastHealthCheckAt    *time.Time `json:"last_health_check_at,omitempty"`
	LastHealthErrorClass *string    `json:"last_health_error_class,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}
