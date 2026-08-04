package dto

import (
	"encoding/json"
	"time"
)

// ModelAudience 定向可见目标（按 visible_scope 解释）：
// groups 时用 group_ids（可选 group_roles 细分组内角色）；roles 时用 role_codes。
type ModelAudience struct {
	GroupIDs   []uint64 `json:"group_ids,omitempty"`
	GroupRoles []string `json:"group_roles,omitempty"`
	RoleCodes  []string `json:"role_codes,omitempty"`
}

// CreateModelReq 创建对外模型目录请求体。
type CreateModelReq struct {
	LogicalModelCode string          `json:"logical_model_code"` // 对外逻辑模型名，唯一
	DisplayName      string          `json:"display_name"`       // 展示名称
	ProviderName     string          `json:"provider_name"`
	Description      *string         `json:"description"`
	Capabilities     json.RawMessage `json:"capabilities"`
	ContextWindow    uint64          `json:"context_window"`
	IntroURL         *string         `json:"intro_url"`
	DocsURL          *string         `json:"docs_url"`
	QuickStartURL    *string         `json:"quick_start_url"`
	Modality         string          `json:"modality"`       // 模态，空则默认 chat
	ProductID        *uint64         `json:"product_id"`     // 关联 token 商品 ID，可空
	ChannelID        *uint64         `json:"channel_id"`     // 路由到的渠道 ID，可空
	UpstreamModel    *string         `json:"upstream_model"` // 上游真实模型名，可空
	Status           string          `json:"status"`         // 空则默认 active
	SortOrder        int             `json:"sort_order"`     // 排序权重
	// 定向可见性：visible_scope 空则默认 all。groups 时用 group_ids/group_roles，roles 时用 role_codes。
	VisibleScope string   `json:"visible_scope"`
	GroupIDs     []uint64 `json:"group_ids"`
	GroupRoles   []string `json:"group_roles"`
	RoleCodes    []string `json:"role_codes"`
}

// UpdateModelReq 更新模型目录请求体，字段均为指针/切片，nil 表示不更新。
type UpdateModelReq struct {
	DisplayName   *string         `json:"display_name"`
	ProviderName  *string         `json:"provider_name"`
	Description   *string         `json:"description"`
	Capabilities  json.RawMessage `json:"capabilities"`
	ContextWindow *uint64         `json:"context_window"`
	IntroURL      *string         `json:"intro_url"`
	DocsURL       *string         `json:"docs_url"`
	QuickStartURL *string         `json:"quick_start_url"`
	Modality      *string         `json:"modality"`
	ProductID     *uint64         `json:"product_id"`
	ChannelID     *uint64         `json:"channel_id"`
	UpstreamModel *string         `json:"upstream_model"`
	Status        *string         `json:"status"`
	SortOrder     *int            `json:"sort_order"`
	// 定向可见性：visible_scope 非 nil 时整体覆盖（连同 group_ids/group_roles/role_codes）。
	VisibleScope *string  `json:"visible_scope"`
	GroupIDs     []uint64 `json:"group_ids"`
	GroupRoles   []string `json:"group_roles"`
	RoleCodes    []string `json:"role_codes"`
}

// ModelResp 对外模型目录响应体。
type ModelResp struct {
	ID               uint64          `json:"id"`
	LogicalModelCode string          `json:"logical_model_code"`
	DisplayName      string          `json:"display_name"`
	ProviderName     string          `json:"provider_name"`
	Description      *string         `json:"description,omitempty"`
	Capabilities     json.RawMessage `json:"capabilities,omitempty"`
	ContextWindow    uint64          `json:"context_window"`
	IntroURL         *string         `json:"intro_url,omitempty"`
	DocsURL          *string         `json:"docs_url,omitempty"`
	QuickStartURL    *string         `json:"quick_start_url,omitempty"`
	Modality         string          `json:"modality"`
	ProductID        *uint64         `json:"product_id,omitempty"`
	ChannelID        *uint64         `json:"channel_id,omitempty"`
	UpstreamModel    *string         `json:"upstream_model,omitempty"`
	Status           string          `json:"status"`
	VisibleScope     string          `json:"visible_scope"`
	// scope=all 时为 nil；groups/roles 时回显已配置的定向目标，供前端表单回填。
	TargetAudience   *ModelAudience `json:"target_audience,omitempty"`
	SortOrder        int            `json:"sort_order"`
	ReleaseVersionNo uint64         `json:"release_version_no"`
	PublishedAt      *time.Time     `json:"published_at,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// PublicModelResp 用户端可见的模型目录精简视图（不含渠道/上游/商品等内部路由字段）。
type PublicModelResp struct {
	LogicalModelCode string  `json:"logical_model_code"`
	DisplayName      string  `json:"display_name"`
	Modality         string  `json:"modality"`
	ProviderName     string  `json:"provider_name"`
	Description      *string `json:"description,omitempty"`
	ContextWindow    uint64  `json:"context_window"`
	IntroURL         *string `json:"intro_url,omitempty"`
	DocsURL          *string `json:"docs_url,omitempty"`
	QuickStartURL    *string `json:"quick_start_url,omitempty"`
}
