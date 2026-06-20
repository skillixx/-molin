package model

import "time"

// TokenModel 对外模型目录，对应 token_models 表。
// 决定上架哪些逻辑模型 + 关联 token 商品/计费；并通过 ChannelID/UpstreamModel 路由到上游渠道。
type TokenModel struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	LogicalModelCode string    `gorm:"size:128;not null;uniqueIndex:uk_token_models_code" json:"logical_model_code"` // 对外逻辑模型名，如 gpt-4o / claude-*
	DisplayName      string    `gorm:"size:191;not null" json:"display_name"`                                        // 展示名称
	Modality         string    `gorm:"size:32;not null;default:chat" json:"modality"`                                // 模态：chat/image/audio/video
	ProductID        *uint64   `json:"product_id,omitempty"`                                                         // 关联的 token 商品 ID，可空
	ChannelID        *uint64   `gorm:"column:channel_id" json:"channel_id,omitempty"`                                // 路由到的渠道 ID（token_channels.id），可空
	UpstreamModel    *string   `gorm:"column:upstream_model;size:128" json:"upstream_model,omitempty"`               // 上游真实模型名，如 deepseek-chat / gpt-4o
	Status           string    `gorm:"size:32;not null;default:active" json:"status"`                                // active 上架 / inactive 下架
	SortOrder        int       `gorm:"not null;default:0" json:"sort_order"`                                         // 排序权重，越小越靠前
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (TokenModel) TableName() string { return "token_models" }
