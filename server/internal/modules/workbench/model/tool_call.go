package model

import "time"

// ToolDailyCallLog 通用工具每用户每日调用计数（限流），对应 tool_daily_call_logs 表。
// 收口替代插件专用 plugin_daily_call_logs：插件 / MCP / 未来工具源共用同款机制。
// tool_type 取 "plugin"（tool_id=plugin_id）或 "mcp"（tool_id=mcp_server_id）。
type ToolDailyCallLog struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ToolType  string    `gorm:"column:tool_type;size:16;not null;uniqueIndex:uk_tool_user_date" json:"tool_type"`
	ToolID    uint64    `gorm:"column:tool_id;not null;uniqueIndex:uk_tool_user_date" json:"tool_id"`
	UserID    uint64    `gorm:"column:user_id;not null;uniqueIndex:uk_tool_user_date" json:"user_id"`
	CallDate  string    `gorm:"column:call_date;type:date;not null;uniqueIndex:uk_tool_user_date" json:"call_date"`
	Count     int       `gorm:"not null;default:0" json:"count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (ToolDailyCallLog) TableName() string { return "tool_daily_call_logs" }
