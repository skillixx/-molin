package dto

import (
	"encoding/json"
	"time"
)

// CreateMCPServerReq 创建 MCP server 请求体（管理端）。
// AuthConfig 为明文鉴权配置（如 {"header":"Authorization","value":"Bearer xxx"}），入参加密落库，响应不返回。
type CreateMCPServerReq struct {
	Code        string `json:"code"`         // 唯一编码，做工具命名空间前缀
	Name        string `json:"name"`         // 名称
	Description string `json:"description"`  // 描述
	EndpointURL string `json:"endpoint_url"` // MCP server HTTP 端点（仅 https + 白名单域名）
	AuthConfig  string `json:"auth_config"`  // 明文鉴权配置，加密落库后丢弃；空则不设
	TimeoutMs   int    `json:"timeout_ms"`   // 单次调用超时（毫秒），空则默认 15000
	IsPaid      bool   `json:"is_paid"`      // 是否产生平台成本
	DailyLimit  int    `json:"daily_limit"`  // 付费时每用户每日上限（0=不限）
	Status      string `json:"status"`       // 空则默认 inactive（发现+审核后再启用）
}

// UpdateMCPServerReq 更新 MCP server 请求体，字段均为指针，nil 表示不更新。
// AuthConfig 非 nil 则重设凭证（空字符串=清空凭证）。
type UpdateMCPServerReq struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	EndpointURL *string `json:"endpoint_url"`
	AuthConfig  *string `json:"auth_config"`
	TimeoutMs   *int    `json:"timeout_ms"`
	IsPaid      *bool   `json:"is_paid"`
	DailyLimit  *int    `json:"daily_limit"`
	Status      *string `json:"status"`
}

// MCPServerResp 管理端 MCP server 响应体。安全红线：凭证不回，仅以 has_auth 表征。
type MCPServerResp struct {
	ID               uint64     `json:"id"`
	Code             string     `json:"code"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	EndpointURL      string     `json:"endpoint_url"`
	HasAuth          bool       `json:"has_auth"`         // 是否已配置鉴权凭证（不回明文）
	ProtocolVersion  string     `json:"protocol_version"` // initialize 协商到的协议版本（discover 后回填）
	TimeoutMs        int        `json:"timeout_ms"`
	IsPaid           bool       `json:"is_paid"`
	DailyLimit       int        `json:"daily_limit"`
	Status           string     `json:"status"`
	LastDiscoveredAt *time.Time `json:"last_discovered_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// PublicMCPServerResp 用户端 MCP server 精简视图（不回 endpoint/凭证/付费配额内部信息）。
type PublicMCPServerResp struct {
	ID          uint64 `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsPaid      bool   `json:"is_paid"`
}

// MCPToolResp MCP 工具快照响应体（管理端审核视图）。
type MCPToolResp struct {
	ID              uint64          `json:"id"`
	ServerID        uint64          `json:"server_id"`
	ToolName        string          `json:"tool_name"`
	Description     string          `json:"description"`
	InputSchemaJSON json.RawMessage `json:"input_schema_json"`
	Enabled         bool            `json:"enabled"`
	SchemaHash      string          `json:"schema_hash"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// DiscoverMCPResp discover 接口响应：本次发现/更新的工具数 + 工具快照列表（含待审标记）。
type DiscoverMCPResp struct {
	ProtocolVersion string        `json:"protocol_version"`
	Discovered      int           `json:"discovered"` // 本次 tools/list 返回的工具总数
	Changed         int           `json:"changed"`    // 新增或定义变更（需重审）的工具数
	Tools           []MCPToolResp `json:"tools"`
}

// UpdateMCPToolReq 审核单个工具的启用状态。
type UpdateMCPToolReq struct {
	Enabled *bool `json:"enabled"`
}
