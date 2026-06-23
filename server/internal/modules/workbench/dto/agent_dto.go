package dto

import "time"

// AdminCreateAgentReq 管理端创建官方 Agent 请求体。
type AdminCreateAgentReq struct {
	Code             string   `json:"code"`               // 官方编码，唯一；可空
	Name             string   `json:"name"`               // 名称
	Description      string   `json:"description"`        // 描述
	Avatar           string   `json:"avatar"`             // 头像 URL
	SystemPrompt     string   `json:"system_prompt"`      // 系统提示词
	DefaultModelCode string   `json:"default_model_code"` // 默认逻辑模型
	CategoryCode     string   `json:"category_code"`      // 所属分类编码，空=未分类；非空须存在于字典
	Status           string   `json:"status"`             // 空则默认 active
	SortOrder        int      `json:"sort_order"`         // 排序权重
	SkillIDs         []uint64 `json:"skill_ids"`          // 绑定 skill（可空）
	PluginIDs        []uint64 `json:"plugin_ids"`         // 绑定 plugin（可空）
}

// AdminUpdateAgentReq 管理端更新官方 Agent 请求体，标量字段指针 nil 不更新。
// SkillIDs/PluginIDs 非 nil 则覆盖对应绑定（空数组=清空该类绑定，nil=保留原绑定）。
type AdminUpdateAgentReq struct {
	Name             *string  `json:"name"`
	Description      *string  `json:"description"`
	Avatar           *string  `json:"avatar"`
	SystemPrompt     *string  `json:"system_prompt"`
	DefaultModelCode *string  `json:"default_model_code"`
	CategoryCode     *string  `json:"category_code"` // nil=不更新；""=清空(未分类)；非空须存在于字典
	Status           *string  `json:"status"`
	SortOrder        *int     `json:"sort_order"`
	SkillIDs         []uint64 `json:"skill_ids"`
	PluginIDs        []uint64 `json:"plugin_ids"`
}

// UserCreateAgentReq 用户端自建 Agent 请求体（owner_type 强制 user，不可传 code）。
type UserCreateAgentReq struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Avatar           string   `json:"avatar"`
	SystemPrompt     string   `json:"system_prompt"`
	DefaultModelCode string   `json:"default_model_code"` // 可空则后端不强制；须为该用户可用模型
	CategoryCode     string   `json:"category_code"`      // 所属分类编码，空=未分类；非空须存在于字典（契约 §5 允许自建选分类）
	SkillIDs         []uint64 `json:"skill_ids"`          // 仅可绑 active 官方 skill
	PluginIDs        []uint64 `json:"plugin_ids"`         // 仅可绑 active 官方 plugin
}

// UserUpdateAgentReq 用户端更新自建 Agent 请求体。
type UserUpdateAgentReq struct {
	Name             *string  `json:"name"`
	Description      *string  `json:"description"`
	Avatar           *string  `json:"avatar"`
	SystemPrompt     *string  `json:"system_prompt"`
	DefaultModelCode *string  `json:"default_model_code"`
	CategoryCode     *string  `json:"category_code"` // nil=不更新；""=清空(未分类)；非空须存在于字典
	Status           *string  `json:"status"`
	SkillIDs         []uint64 `json:"skill_ids"`
	PluginIDs        []uint64 `json:"plugin_ids"`
}

// BindIDsReq 管理端绑定/解绑请求体（覆盖语义，空数组=全部解绑）。
type BindIDsReq struct {
	IDs []uint64 `json:"ids"`
}

// BoundResource Agent 详情里绑定的 skill/plugin 精简信息（名称 + 编码，不含内部敏感字段）。
type BoundResource struct {
	ID   uint64 `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

// AgentResp Agent 响应体（含绑定的 skill/plugin 名称，不含插件凭证）。
type AgentResp struct {
	ID               uint64          `json:"id"`
	Code             *string         `json:"code,omitempty"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Avatar           string          `json:"avatar"`
	OwnerType        string          `json:"owner_type"`
	OwnerUserID      *uint64         `json:"owner_user_id,omitempty"`
	SystemPrompt     string          `json:"system_prompt"`
	DefaultModelCode string          `json:"default_model_code"`
	CategoryCode     *string         `json:"category_code"` // 所属分类编码，null=未分类
	CategoryName     string          `json:"category_name"` // 联字典带出的分类名称，未分类/字典缺失为空串
	Status           string          `json:"status"`
	SortOrder        int             `json:"sort_order"`
	Skills           []BoundResource `json:"skills"`
	Plugins          []BoundResource `json:"plugins"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// AgentCategoryResp 分类列表项（前端分类导航元数据）。
type AgentCategoryResp struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Icon      string `json:"icon"`
	SortOrder int    `json:"sort_order"`
	Status    string `json:"status"` // 管理端全量列表区分 active/inactive；用户端仅 active
}
