package dto

import "time"

// CreateModelReq 创建对外模型目录请求体。
type CreateModelReq struct {
	LogicalModelCode string  `json:"logical_model_code"` // 对外逻辑模型名，唯一
	DisplayName      string  `json:"display_name"`       // 展示名称
	Modality         string  `json:"modality"`           // 模态，空则默认 chat
	ProductID        *uint64 `json:"product_id"`         // 关联 token 商品 ID，可空
	ChannelID        *uint64 `json:"channel_id"`         // 路由到的渠道 ID，可空
	UpstreamModel    *string `json:"upstream_model"`     // 上游真实模型名，可空
	Status           string  `json:"status"`             // 空则默认 active
	SortOrder        int     `json:"sort_order"`         // 排序权重
}

// UpdateModelReq 更新模型目录请求体，字段均为指针，nil 表示不更新。
type UpdateModelReq struct {
	DisplayName   *string `json:"display_name"`
	Modality      *string `json:"modality"`
	ProductID     *uint64 `json:"product_id"`
	ChannelID     *uint64 `json:"channel_id"`
	UpstreamModel *string `json:"upstream_model"`
	Status        *string `json:"status"`
	SortOrder     *int    `json:"sort_order"`
}

// ModelResp 对外模型目录响应体。
type ModelResp struct {
	ID               uint64    `json:"id"`
	LogicalModelCode string    `json:"logical_model_code"`
	DisplayName      string    `json:"display_name"`
	Modality         string    `json:"modality"`
	ProductID        *uint64   `json:"product_id,omitempty"`
	ChannelID        *uint64   `json:"channel_id,omitempty"`
	UpstreamModel    *string   `json:"upstream_model,omitempty"`
	Status           string    `json:"status"`
	SortOrder        int       `json:"sort_order"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// PublicModelResp 用户端可见的模型目录精简视图（不含渠道/上游/商品等内部路由字段）。
type PublicModelResp struct {
	LogicalModelCode string `json:"logical_model_code"`
	DisplayName      string `json:"display_name"`
	Modality         string `json:"modality"`
}
