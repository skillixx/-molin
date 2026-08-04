package model

import (
	"encoding/json"
	"time"
)

// AIModelReleaseVersion 保存模型每次发布或回滚形成的不可变配置快照。
type AIModelReleaseVersion struct {
	ID           uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ModelID      uint64          `gorm:"not null;uniqueIndex:uk_ai_model_release_version,priority:1" json:"model_id"`
	VersionNo    uint64          `gorm:"not null;uniqueIndex:uk_ai_model_release_version,priority:2" json:"version_no"`
	Status       string          `gorm:"size:16;not null" json:"status"`
	SnapshotJSON json.RawMessage `gorm:"column:snapshot_json;type:json;not null" json:"snapshot"`
	Reason       string          `gorm:"size:255;not null" json:"reason"`
	CreatedBy    uint64          `gorm:"not null" json:"created_by"`
	PublishedAt  time.Time       `json:"published_at"`
	RetiredAt    *time.Time      `json:"retired_at,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

func (AIModelReleaseVersion) TableName() string { return "ai_model_release_versions" }

// AIModelRoute 保存逻辑模型到具体渠道及 Bifrost provider/model 的可审计路由策略。
type AIModelRoute struct {
	ID                      uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	LogicalModelCode        string    `gorm:"size:128;not null" json:"logical_model_code"`
	ChannelID               uint64    `gorm:"not null" json:"channel_id"`
	ProviderModel           string    `gorm:"size:191;not null" json:"provider_model"`
	Priority                int       `gorm:"not null" json:"priority"`
	Weight                  uint64    `gorm:"not null" json:"weight"`
	TimeoutMS               uint64    `gorm:"column:timeout_ms;not null" json:"timeout_ms"`
	MaxRetries              uint64    `gorm:"not null" json:"max_retries"`
	CircuitBreakerThreshold uint64    `gorm:"not null" json:"circuit_breaker_threshold"`
	FallbackOrder           uint64    `gorm:"not null" json:"fallback_order"`
	Status                  string    `gorm:"size:16;not null" json:"status"`
	VersionNo               uint64    `gorm:"not null" json:"version_no"`
	UpdatedBy               uint64    `gorm:"not null" json:"updated_by"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

func (AIModelRoute) TableName() string { return "ai_model_routes" }

// AIModelRouteRuntimeState 保存跨节点共享的短期熔断状态，不改变不可变路由配置。
type AIModelRouteRuntimeState struct {
	RouteID             uint64     `gorm:"primaryKey" json:"route_id"`
	ConsecutiveFailures uint64     `gorm:"not null" json:"consecutive_failures"`
	CircuitOpenUntil    *time.Time `json:"circuit_open_until,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

func (AIModelRouteRuntimeState) TableName() string { return "ai_model_route_runtime_states" }
