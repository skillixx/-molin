package dto

import (
	"encoding/json"
	"time"
)

// CreatePluginReq 创建 Plugin 请求体（管理端）。
// AuthConfig 为明文鉴权配置（如 {"header":"Authorization","value":"Bearer xxx"}），入参加密落库，响应不返回。
type CreatePluginReq struct {
	Code           string          `json:"code"`             // 唯一编码
	Name           string          `json:"name"`             // 名称
	Description    string          `json:"description"`      // 描述
	ToolSchemaJSON json.RawMessage `json:"tool_schema_json"` // function calling 工具定义
	EndpointURL    string          `json:"endpoint_url"`     // 外部 HTTP 端点（仅 https + 白名单域名）
	AuthConfig     string          `json:"auth_config"`      // 明文鉴权配置，加密落库后丢弃；空则不设
	TimeoutMs      int             `json:"timeout_ms"`       // 转发超时（毫秒），空则默认 10000
	IsPaid         bool            `json:"is_paid"`          // D3：是否付费插件
	DailyLimit     int             `json:"daily_limit"`      // D3：付费插件每用户每日上限（0=不限）
	Status         string          `json:"status"`           // 空则默认 active
}

// UpdatePluginReq 更新 Plugin 请求体，字段均为指针，nil 表示不更新。
// AuthConfig 非 nil 则重设凭证（空字符串=清空凭证）；ToolSchemaJSON 非 nil 则覆盖。
type UpdatePluginReq struct {
	Name           *string         `json:"name"`
	Description    *string         `json:"description"`
	ToolSchemaJSON json.RawMessage `json:"tool_schema_json"`
	EndpointURL    *string         `json:"endpoint_url"`
	AuthConfig     *string         `json:"auth_config"`
	TimeoutMs      *int            `json:"timeout_ms"`
	IsPaid         *bool           `json:"is_paid"`
	DailyLimit     *int            `json:"daily_limit"`
	Status         *string         `json:"status"`
}

// PluginResp 管理端 Plugin 响应体。安全红线：凭证不回，仅以 has_auth 表征是否已配置鉴权。
type PluginResp struct {
	ID             uint64          `json:"id"`
	Code           string          `json:"code"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	ToolSchemaJSON json.RawMessage `json:"tool_schema_json"`
	EndpointURL    string          `json:"endpoint_url"`
	HasAuth        bool            `json:"has_auth"` // 是否已配置鉴权凭证（仿渠道 key 红线，不回明文）
	TimeoutMs      int             `json:"timeout_ms"`
	IsPaid         bool            `json:"is_paid"`
	DailyLimit     int             `json:"daily_limit"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// PublicPluginResp 用户端 Plugin 精简视图（供自建 Agent 绑定，不回 endpoint/凭证/付费配额等内部信息）。
type PublicPluginResp struct {
	ID          uint64 `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsPaid      bool   `json:"is_paid"` // 仅提示是否付费，便于前端标注
}
