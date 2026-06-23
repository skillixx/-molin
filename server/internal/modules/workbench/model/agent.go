package model

import "time"

// Agent 聊天工作台 Agent（=角色/人设），对应 agents 表。
// 免费资源：区分官方上架（owner_type=official）与用户自建（owner_type=user）。
// 用户「切角色」= 选不同 Agent；选定后 system_prompt + 默认模型 + 绑定 skill/插件作为对话上下文。
type Agent struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Code             *string   `gorm:"size:64;uniqueIndex:uk_agents_code" json:"code,omitempty"`               // 官方预设唯一编码；用户自建为 NULL
	Name             string    `gorm:"size:128;not null" json:"name"`                                          // Agent 名称
	Description      string    `gorm:"size:512;not null;default:''" json:"description"`                        // 描述
	Avatar           string    `gorm:"size:512;not null;default:''" json:"avatar"`                             // 头像 URL
	OwnerType        string    `gorm:"size:16;not null;default:official" json:"owner_type"`                    // official 官方 / user 用户自建
	OwnerUserID      *uint64   `gorm:"column:owner_user_id" json:"owner_user_id,omitempty"`                    // user 自建时非空
	SystemPrompt     string    `gorm:"type:text;not null" json:"system_prompt"`                                // 系统提示词（人设）
	DefaultModelCode string    `gorm:"size:128;not null" json:"default_model_code"`                            // 指向 token_models.logical_model_code
	Status           string    `gorm:"size:16;not null;default:active" json:"status"`                          // active 启用 / inactive 停用
	VisibleScope     string    `gorm:"column:visible_scope;size:16;not null;default:all" json:"visible_scope"` // all 全体 / groups 指定分组 / roles 指定全局角色
	TargetAudience   *string   `gorm:"column:target_audience_json;type:json" json:"-"`                         // 定向目标 JSON 原文，按 visible_scope 解释；不直接序列化，响应用 target_audience 结构
	CategoryCode     *string   `gorm:"column:category_code;size:64" json:"category_code,omitempty"`            // 所属分类，指向 agent_categories.code；NULL=未分类
	SortOrder        int       `gorm:"not null;default:0" json:"sort_order"`                                   // 排序权重，越小越靠前
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (Agent) TableName() string { return "agents" }

// AgentSkillBinding Agent ↔ Skill 绑定关系，对应 agent_skill_bindings 表。
type AgentSkillBinding struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	AgentID   uint64    `gorm:"column:agent_id;not null;uniqueIndex:uk_agent_skill" json:"agent_id"`
	SkillID   uint64    `gorm:"column:skill_id;not null;uniqueIndex:uk_agent_skill" json:"skill_id"`
	Enabled   bool      `gorm:"not null;default:1" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName 指定表名。
func (AgentSkillBinding) TableName() string { return "agent_skill_bindings" }

// AgentPluginBinding Agent ↔ Plugin 绑定关系，对应 agent_plugin_bindings 表。
type AgentPluginBinding struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	AgentID   uint64    `gorm:"column:agent_id;not null;uniqueIndex:uk_agent_plugin" json:"agent_id"`
	PluginID  uint64    `gorm:"column:plugin_id;not null;uniqueIndex:uk_agent_plugin" json:"plugin_id"`
	Enabled   bool      `gorm:"not null;default:1" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName 指定表名。
func (AgentPluginBinding) TableName() string { return "agent_plugin_bindings" }

// AgentCategory Agent 分类字典，对应 agent_categories 表。
// 展示维度（前端分类导航/筛选），与可见性/计费正交。本期 seed 固定 4 类，不做 CRUD。
type AgentCategory struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Code      string    `gorm:"size:64;uniqueIndex:uk_agent_categories_code;not null" json:"code"` // 分类编码，唯一
	Name      string    `gorm:"size:64;not null" json:"name"`                                      // 展示名称
	Icon      string    `gorm:"size:128;not null;default:''" json:"icon"`                          // 图标标识/URL
	SortOrder int       `gorm:"column:sort_order;not null;default:0" json:"sort_order"`            // 排序，越小越靠前
	Status    string    `gorm:"size:16;not null;default:active" json:"status"`                     // active 启用 / inactive 停用
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (AgentCategory) TableName() string { return "agent_categories" }
