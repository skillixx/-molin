package dto

import (
	"time"

	"molin/server/internal/modules/app/model"
)

// MarketplaceAppResponse 用户端应用详情响应 DTO（白名单字段）。
//
// 仅暴露展示所需的非敏感字段，刻意剔除以下字段以避免过度暴露：
//   - callback_url        —— 内部回调地址
//   - adapter_config_json —— 应用非交易配置（freeform JSON，可能含集成参数/内网地址/密钥）
//   - updated_at          —— 内部维护时间，用户端无需感知
//
// 该剔除仅作用于用户端 AP1（GET /api/marketplace/apps/:id）；
// 管理端 AP2/AP3（AdminListApps/AdminGetApp）仍返回完整的 model.Application。
type MarketplaceAppResponse struct {
	ID          uint64    `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Description *string   `json:"description,omitempty"`
	IconURL     *string   `json:"icon_url,omitempty"`
	AccessURL   *string   `json:"access_url,omitempty"` // 用户「进入应用」跳转目标；面向用户，故进白名单
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// MapMarketplaceApp 将 *model.Application 映射为用户向白名单响应 DTO。
//
// 纯函数，便于单测；调用方需保证传入非 nil。
func MapMarketplaceApp(a *model.Application) MarketplaceAppResponse {
	return MarketplaceAppResponse{
		ID:          a.ID,
		Code:        a.Code,
		Name:        a.Name,
		Type:        a.Type,
		Description: a.Description,
		IconURL:     a.IconURL,
		AccessURL:   a.AccessURL,
		Status:      a.Status,
		CreatedAt:   a.CreatedAt,
	}
}

// CreateAppReq 创建应用请求（管理端）。
type CreateAppReq struct {
	Code              string  `json:"code"`
	Name              string  `json:"name"`
	Type              string  `json:"type"`
	Description       *string `json:"description"`
	IconURL           *string `json:"icon_url"`
	AccessURL         *string `json:"access_url"` // 用户访问入口地址（可选；须 https，禁危险 scheme）
	CallbackURL       *string `json:"callback_url"`
	AdapterConfigJSON *string `json:"adapter_config_json"` // JSON 字符串，应用特有的非交易配置
}

// UpdateAppReq 修改应用请求（管理端，含上下架操作）。
type UpdateAppReq struct {
	Name              *string `json:"name"`
	Type              *string `json:"type"`
	Description       *string `json:"description"`
	IconURL           *string `json:"icon_url"`
	AccessURL         *string `json:"access_url"` // 用户访问入口地址（可选；须 https，禁危险 scheme）
	CallbackURL       *string `json:"callback_url"`
	AdapterConfigJSON *string `json:"adapter_config_json"`
	Status            *string `json:"status"` // draft/active/inactive/archived
}

// CreateAdapterReq 注册适配器请求（管理端）。
type CreateAdapterReq struct {
	AppCode              string  `json:"app_code"`
	AppName              string  `json:"app_name"`
	AppType              string  `json:"app_type"`
	AdapterType          string  `json:"adapter_type"` // internal/external
	ServiceName          *string `json:"service_name"`
	CallbackURL          *string `json:"callback_url"`
	SupportedActionsJSON *string `json:"supported_actions_json"` // JSON 数组字符串，例如 ["provision","renew","suspend","resume","cancel"]
	UsageEventTypesJSON  *string `json:"usage_event_types_json"` // JSON 数组字符串
}

// UpdateAdapterReq 修改/启停适配器请求（管理端）。
type UpdateAdapterReq struct {
	AppName              *string `json:"app_name"`
	AppType              *string `json:"app_type"`
	AdapterType          *string `json:"adapter_type"`
	ServiceName          *string `json:"service_name"`
	CallbackURL          *string `json:"callback_url"`
	SupportedActionsJSON *string `json:"supported_actions_json"`
	UsageEventTypesJSON  *string `json:"usage_event_types_json"`
	Status               *string `json:"status"` // active/inactive
}
