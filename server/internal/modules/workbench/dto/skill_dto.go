package dto

import (
	"encoding/json"
	"time"
)

// CreateSkillReq 创建 Skill 请求体（管理端）。
type CreateSkillReq struct {
	Code           string          `json:"code"`             // 唯一编码
	Name           string          `json:"name"`             // 名称
	Description    string          `json:"description"`      // 描述
	Category       string          `json:"category"`         // 分类
	ToolSchemaJSON json.RawMessage `json:"tool_schema_json"` // function calling 工具定义（OpenAI tools 格式）
	HandlerKey     string          `json:"handler_key"`      // 内置实现路由键
	Status         string          `json:"status"`           // 空则默认 active
}

// UpdateSkillReq 更新 Skill 请求体，字段均为指针，nil 表示不更新。
type UpdateSkillReq struct {
	Name           *string         `json:"name"`
	Description    *string         `json:"description"`
	Category       *string         `json:"category"`
	ToolSchemaJSON json.RawMessage `json:"tool_schema_json"` // 非 nil 则覆盖
	HandlerKey     *string         `json:"handler_key"`
	Status         *string         `json:"status"`
}

// SkillResp 管理端 Skill 响应体（完整字段）。
type SkillResp struct {
	ID             uint64          `json:"id"`
	Code           string          `json:"code"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Category       string          `json:"category"`
	ToolSchemaJSON json.RawMessage `json:"tool_schema_json"`
	HandlerKey     string          `json:"handler_key"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// PublicSkillResp 用户端 Skill 精简视图（供自建 Agent 绑定，不含 handler_key 内部路由键）。
type PublicSkillResp struct {
	ID          uint64 `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}
