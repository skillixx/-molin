package model

import (
	"encoding/json"
	"time"
)

// Plugin 聊天工作台 Plugin（外部第三方工具），对应 plugins 表。
// 门面按 tool_schema_json 取参后转发 endpoint_url（带超时/SSRF 防护）。
// 凭证 AuthConfigEncrypted 以 AES-256-GCM 加密落库（base64 文本存 VARBINARY），安全红线：禁止序列化到任何响应，故 json:"-"。
// plugin 均为官方上架，用户不能自建/上传。
type Plugin struct {
	ID                  uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Code                string          `gorm:"size:64;not null;uniqueIndex:uk_plugins_code" json:"code"`           // 唯一编码
	Name                string          `gorm:"size:128;not null" json:"name"`                                      // 名称
	Description         string          `gorm:"size:512;not null;default:''" json:"description"`                    // 描述
	ToolSchemaJSON      json.RawMessage `gorm:"column:tool_schema_json;type:json;not null" json:"tool_schema_json"` // function calling 工具定义
	EndpointURL         string          `gorm:"column:endpoint_url;size:512;not null" json:"endpoint_url"`          // 外部 HTTP 端点（仅 https + 白名单域名）
	AuthConfigEncrypted *string         `gorm:"column:auth_config_encrypted" json:"-"`                              // 鉴权配置 AES-256-GCM 密文，响应不返回
	TimeoutMs           int             `gorm:"column:timeout_ms;not null;default:10000" json:"timeout_ms"`         // 转发超时（毫秒，强制上限如 30000）
	IsPaid              bool            `gorm:"column:is_paid;not null;default:0" json:"is_paid"`                   // D3：是否付费第三方插件（成本由平台承担）
	DailyLimit          int             `gorm:"column:daily_limit;not null;default:0" json:"daily_limit"`           // D3：付费插件每用户每日调用上限（0=不限）
	Status              string          `gorm:"size:16;not null;default:active" json:"status"`                      // active 启用 / inactive 停用
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// TableName 指定表名。
func (Plugin) TableName() string { return "plugins" }
