package model

import "time"

// Application 应用业务详情，对应 applications 表。
//
// 重要边界（详见 app/CLAUDE.md）：本表只保存应用的非交易类业务详情字段
// （图标、描述、回调地址、适配器配置等），套餐/价格/角色权限统一通过
// products（product_type=application, business_ref_id=applications.id）/
// product_plans / product_prices / product_role_access 管理，本模块不涉及。
//
// status 取值：
//
//	draft    —— 草稿，未对外展示
//	active   —— 已上架，正常可用
//	inactive —— 已下架，暂时不可用（可恢复）
//	archived —— 已归档，永久下线
type Application struct {
	ID                uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Code              string    `gorm:"size:64;not null;uniqueIndex:uk_applications_code" json:"code"`
	Name              string    `gorm:"size:128;not null" json:"name"`
	Type              string    `gorm:"size:64;not null" json:"type"`
	Description       *string   `gorm:"size:1024" json:"description,omitempty"`
	IconURL           *string   `gorm:"size:512" json:"icon_url,omitempty"`
	CallbackURL       *string   `gorm:"size:512" json:"callback_url,omitempty"`
	AdapterConfigJSON *string   `gorm:"type:json" json:"adapter_config_json,omitempty"`
	Status            string    `gorm:"size:32;not null;default:draft;index:idx_applications_status" json:"status"` // draft/active/inactive/archived
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (Application) TableName() string { return "applications" }

// ApplicationAdapter 应用适配器注册信息，对应 application_adapters 表。
//
// adapter_type 取值：
//
//	internal —— 内部服务适配器（平台自身实现）
//	external —— 外部回调适配器（通过 callback_url 与第三方系统对接）
//
// status 取值：active / inactive
type ApplicationAdapter struct {
	ID                   uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	AppCode              string    `gorm:"size:64;not null;uniqueIndex:uk_app_adapters_app_code" json:"app_code"`
	AppName              string    `gorm:"size:128;not null" json:"app_name"`
	AppType              string    `gorm:"size:64;not null" json:"app_type"`
	AdapterType          string    `gorm:"size:32;not null;default:internal" json:"adapter_type"` // internal/external
	ServiceName          *string   `gorm:"size:128" json:"service_name,omitempty"`
	CallbackURL          *string   `gorm:"size:512" json:"callback_url,omitempty"`
	SupportedActionsJSON *string   `gorm:"type:json" json:"supported_actions_json,omitempty"` // JSON 数组：["provision","renew","suspend","resume","cancel"]
	UsageEventTypesJSON  *string   `gorm:"type:json" json:"usage_event_types_json,omitempty"` // JSON 数组
	Status               string    `gorm:"size:32;not null;default:active;index:idx_app_adapters_status" json:"status"` // active/inactive
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (ApplicationAdapter) TableName() string { return "application_adapters" }
