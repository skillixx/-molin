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

// LaunchTicketResult 用户「进入应用」签发一次性票据的响应 data（阶段二 SSO）。
//
// 前端拿到后跳转 {access_url}?ticket={launch_ticket}；应用后端再用该票据
// 调内部接口 POST /api/internal/app-launch/verify 换取用户身份。
type LaunchTicketResult struct {
	AccessURL    string `json:"access_url"`    // 应用访问入口（已确保非空、https）
	LaunchTicket string `json:"launch_ticket"` // 一次性票据，形如 lt_xxx
	ExpiresIn    int    `json:"expires_in"`    // 票据有效期（秒）
}

// VerifyLaunchReq 应用后端校验/消费 launch 票据的请求体（内部接口）。
type VerifyLaunchReq struct {
	LaunchTicket string `json:"launch_ticket"`
}

// LaunchClaims launch 票据所绑定的身份信息，校验+消费后返回给应用后端。
//
// 只返回最小必要字段：应用据 user_id 建立自有会话、据 product_id 区分套餐来源；
// 不返回用户敏感资料。
type LaunchClaims struct {
	UserID    uint64 `json:"user_id"`
	AppID     uint64 `json:"app_id"`
	ProductID uint64 `json:"product_id"`
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
