package model

import (
	"encoding/json"
	"time"
)

// MCPServer MCP server 注册（第二种工具源），对应 mcp_servers 表。
// 一对多工具：经 discover 自动暴露 N 个工具。凭证 AuthConfigEncrypted 以 AES-256-GCM 加密落库，
// 安全红线：禁止序列化到任何响应，故 json:"-"。MCP server 均为官方上架，用户不能自建。
type MCPServer struct {
	ID                  uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Code                string     `gorm:"size:64;not null;uniqueIndex:uk_mcp_servers_code" json:"code"`                // 唯一编码，做工具命名空间前缀
	Name                string     `gorm:"size:128;not null" json:"name"`                                               // 名称
	Description         string     `gorm:"size:512;not null;default:''" json:"description"`                             // 描述
	EndpointURL         string     `gorm:"column:endpoint_url;size:512;not null" json:"endpoint_url"`                   // MCP server HTTP 端点（仅 https + 白名单）
	AuthConfigEncrypted *string    `gorm:"column:auth_config_encrypted" json:"-"`                                       // 鉴权配置 AES-256-GCM 密文，响应不返回
	ProtocolVersion     string     `gorm:"column:protocol_version;size:32;not null;default:''" json:"protocol_version"` // initialize 协商到的协议版本（回填）
	TimeoutMs           int        `gorm:"column:timeout_ms;not null;default:15000" json:"timeout_ms"`                  // 单次调用超时（毫秒，≤30000）
	IsPaid              bool       `gorm:"column:is_paid;not null;default:0" json:"is_paid"`                            // 调用是否产生平台成本（同插件 D3）
	DailyLimit          int        `gorm:"column:daily_limit;not null;default:0" json:"daily_limit"`                    // 付费时每用户每日调用上限（0=不限）
	Status              string     `gorm:"size:16;not null;default:inactive" json:"status"`                             // active 启用 / inactive 停用；新建默认 inactive
	LastDiscoveredAt    *time.Time `gorm:"column:last_discovered_at" json:"last_discovered_at,omitempty"`               // 最近一次 tools/list 时间
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// TableName 指定表名。
func (MCPServer) TableName() string { return "mcp_servers" }

// MCPServerTool MCP server 已发现并经审核的工具快照，对应 mcp_server_tools 表。
// 安全设计：对话时只用 enabled 的快照工具，不实时 tools/list；schema_hash 变更自动置未启用待重审。
type MCPServerTool struct {
	ID              uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ServerID        uint64          `gorm:"column:server_id;not null;uniqueIndex:uk_mcp_tool" json:"server_id"`
	ToolName        string          `gorm:"column:tool_name;size:128;not null;uniqueIndex:uk_mcp_tool" json:"tool_name"` // MCP 原始工具名
	Description     string          `gorm:"size:1024;not null;default:''" json:"description"`
	InputSchemaJSON json.RawMessage `gorm:"column:input_schema_json;type:json;not null" json:"input_schema_json"` // MCP inputSchema（JSON Schema）
	Enabled         bool            `gorm:"not null;default:0" json:"enabled"`                                    // 运营审核后是否对编排暴露
	SchemaHash      string          `gorm:"column:schema_hash;size:64;not null;default:''" json:"schema_hash"`    // 工具定义指纹，变更触发重新审核
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// TableName 指定表名。
func (MCPServerTool) TableName() string { return "mcp_server_tools" }

// AgentMCPBinding Agent ↔ MCP server 绑定关系，对应 agent_mcp_bindings 表。
type AgentMCPBinding struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	AgentID   uint64    `gorm:"column:agent_id;not null;uniqueIndex:uk_agent_mcp" json:"agent_id"`
	ServerID  uint64    `gorm:"column:server_id;not null;uniqueIndex:uk_agent_mcp" json:"server_id"`
	Enabled   bool      `gorm:"not null;default:1" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName 指定表名。
func (AgentMCPBinding) TableName() string { return "agent_mcp_bindings" }
