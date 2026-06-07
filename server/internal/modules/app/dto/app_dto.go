package dto

// CreateAppReq 创建应用请求（管理端）。
type CreateAppReq struct {
	Code              string  `json:"code"`
	Name              string  `json:"name"`
	Type              string  `json:"type"`
	Description       *string `json:"description"`
	IconURL           *string `json:"icon_url"`
	CallbackURL       *string `json:"callback_url"`
	AdapterConfigJSON *string `json:"adapter_config_json"` // JSON 字符串，应用特有的非交易配置
}

// UpdateAppReq 修改应用请求（管理端，含上下架操作）。
type UpdateAppReq struct {
	Name              *string `json:"name"`
	Type              *string `json:"type"`
	Description       *string `json:"description"`
	IconURL           *string `json:"icon_url"`
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
