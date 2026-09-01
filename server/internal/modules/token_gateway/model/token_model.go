package model

import (
	"encoding/json"
	"time"
)

// TokenModel 对外模型目录，对应 token_models 表。
// 决定上架哪些逻辑模型 + 关联 token 商品/计费；并通过 ChannelID/UpstreamModel 路由到上游渠道。
type TokenModel struct {
	ID               uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	LogicalModelCode string          `gorm:"size:128;not null;uniqueIndex:uk_token_models_code" json:"logical_model_code"` // 对外逻辑模型名，如 gpt-4o / claude-*
	DisplayName      string          `gorm:"size:191;not null" json:"display_name"`                                        // 展示名称
	ProviderName     string          `gorm:"size:191;not null;default:''" json:"provider_name"`
	Description      *string         `gorm:"type:text" json:"description,omitempty"`
	CapabilitiesJSON json.RawMessage `gorm:"column:capabilities_json;type:json" json:"capabilities,omitempty"`
	// 工作副本合同只有经发布事务校验并写入快照后才能影响视频准入。
	VideoContractJSON         json.RawMessage `gorm:"column:video_contract_json;type:json" json:"video_contract,omitempty"`
	ContextWindow             uint64          `gorm:"not null;default:0" json:"context_window"`
	IntroURL                  *string         `gorm:"size:1024" json:"intro_url,omitempty"`
	IntroURLHealthStatus      string          `gorm:"size:16;not null;default:unpublished" json:"intro_url_health_status"`
	DocsURL                   *string         `gorm:"size:1024" json:"docs_url,omitempty"`
	DocsURLHealthStatus       string          `gorm:"size:16;not null;default:unpublished" json:"docs_url_health_status"`
	QuickStartURL             *string         `gorm:"size:1024" json:"quick_start_url,omitempty"`
	QuickStartURLHealthStatus string          `gorm:"size:16;not null;default:unpublished" json:"quick_start_url_health_status"`
	Modality                  string          `gorm:"size:32;not null;default:chat" json:"modality"`                  // 模态：chat/image/audio/video
	ProductID                 *uint64         `json:"product_id,omitempty"`                                           // 关联的 token 商品 ID，可空
	ChannelID                 *uint64         `gorm:"column:channel_id" json:"channel_id,omitempty"`                  // 路由到的渠道 ID（token_channels.id），可空
	UpstreamModel             *string         `gorm:"column:upstream_model;size:128" json:"upstream_model,omitempty"` // 上游真实模型名，如 deepseek-chat / gpt-4o
	Status                    string          `gorm:"size:32;not null;default:active" json:"status"`                  // active 上架 / inactive 下架
	// 定向可见性（对齐 Agent visible_scope）：all 所有登录用户可见 / groups 按分组 / roles 按全局角色。
	VisibleScope string `gorm:"size:32;not null;default:all" json:"visible_scope"`
	// 定向目标 JSON 原文：scope=groups 时 {group_ids,group_roles}；scope=roles 时 {role_codes}；scope=all 为 nil。
	TargetAudience   *string    `gorm:"column:target_audience_json;type:json" json:"target_audience_json,omitempty"`
	SortOrder        int        `gorm:"not null;default:0" json:"sort_order"` // 排序权重，越小越靠前
	ReleaseVersionNo uint64     `gorm:"not null;default:0" json:"release_version_no"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	UpdatedBy        *uint64    `json:"updated_by,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// TableName 指定表名。
func (TokenModel) TableName() string { return "token_models" }

// TokenModelReleaseSnapshot 描述模型发布时冻结的对外配置；回滚会基于旧快照创建新版本，而不会修改历史记录。
type TokenModelReleaseSnapshot struct {
	VideoContract             json.RawMessage `json:"video_contract,omitempty"`
	LogicalModelCode          string          `json:"logical_model_code"`
	DisplayName               string          `json:"display_name"`
	ProviderName              string          `json:"provider_name"`
	Description               *string         `json:"description,omitempty"`
	Capabilities              json.RawMessage `json:"capabilities,omitempty"`
	ContextWindow             uint64          `json:"context_window"`
	IntroURL                  *string         `json:"intro_url,omitempty"`
	IntroURLHealthStatus      string          `json:"intro_url_health_status"`
	DocsURL                   *string         `json:"docs_url,omitempty"`
	DocsURLHealthStatus       string          `json:"docs_url_health_status"`
	QuickStartURL             *string         `json:"quick_start_url,omitempty"`
	QuickStartURLHealthStatus string          `json:"quick_start_url_health_status"`
	Modality                  string          `json:"modality"`
	ProductID                 *uint64         `json:"product_id,omitempty"`
	ChannelID                 *uint64         `json:"channel_id,omitempty"`
	UpstreamModel             *string         `json:"upstream_model,omitempty"`
	VisibleScope              string          `json:"visible_scope"`
	TargetAudience            *string         `json:"target_audience_json,omitempty"`
}

// MarshalReleaseSnapshot 生成不含数据库审计时间的发布快照，供回溯当时对外配置使用。
func (m TokenModel) MarshalReleaseSnapshot() (json.RawMessage, error) {
	// 发布不允许缺项默认授权，也不允许把视频合同附着到其他模态后绕过解析。
	if m.Modality == "video" {
		if _, err := ParseVideoModelContract(m.VideoContractJSON, m.ProductID); err != nil {
			return nil, err
		}
	} else if len(m.VideoContractJSON) != 0 {
		return nil, ErrVideoModelContractInvalid
	}
	return m.MarshalDraftSnapshot()
}

// MarshalDraftSnapshot仅用于管理员查看/接管未配置工作副本时绑定原始状态；不得作为发布校验替代。
func (m TokenModel) MarshalDraftSnapshot() (json.RawMessage, error) {
	return json.Marshal(TokenModelReleaseSnapshot{
		VideoContract:    m.VideoContractJSON,
		LogicalModelCode: m.LogicalModelCode, DisplayName: m.DisplayName, ProviderName: m.ProviderName,
		Description: m.Description, Capabilities: m.CapabilitiesJSON, ContextWindow: m.ContextWindow,
		IntroURL: m.IntroURL, IntroURLHealthStatus: m.IntroURLHealthStatus,
		DocsURL: m.DocsURL, DocsURLHealthStatus: m.DocsURLHealthStatus,
		QuickStartURL: m.QuickStartURL, QuickStartURLHealthStatus: m.QuickStartURLHealthStatus, Modality: m.Modality,
		ProductID: m.ProductID, ChannelID: m.ChannelID, UpstreamModel: m.UpstreamModel,
		VisibleScope: m.VisibleScope, TargetAudience: m.TargetAudience,
	})
}
