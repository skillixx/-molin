package model

import (
	"encoding/json"
	"time"
)

// Skill 聊天工作台 Skill（平台内置能力，免费），对应 skills 表。
// 如联网搜索/读文档；门面通过 handler_key 路由到内置实现。本期不含代码执行（D4）。
// skill 均为官方上架，用户不能自建/上传。
type Skill struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Code           string          `gorm:"size:64;not null;uniqueIndex:uk_skills_code" json:"code"`            // 唯一编码
	Name           string          `gorm:"size:128;not null" json:"name"`                                      // 名称
	Description    string          `gorm:"size:512;not null;default:''" json:"description"`                    // 描述
	Category       string          `gorm:"size:64;not null;default:''" json:"category"`                        // 分类
	ToolSchemaJSON json.RawMessage `gorm:"column:tool_schema_json;type:json;not null" json:"tool_schema_json"` // function calling 工具定义（OpenAI tools 格式）
	HandlerKey     string          `gorm:"size:64;not null" json:"handler_key"`                                // 内置实现路由键（门面 dispatch）
	Status         string          `gorm:"size:16;not null;default:active" json:"status"`                      // active 启用 / inactive 停用
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// TableName 指定表名。
func (Skill) TableName() string { return "skills" }
