package dto

// OpenAI 兼容别名层 DTO。
// 用于让 Cline / Cherry Studio 等「OpenAI 兼容」客户端凭平台 sk 直接接入：
// GET /v1/models 必须返回 OpenAI 标准的 {"object":"list","data":[...]} 顶层结构，
// 而非 Molin 自有的 {code,message,data} 包络分页格式。

// OpenAIModel OpenAI /v1/models 列表中的单个模型对象。
type OpenAIModel struct {
	ID      string `json:"id"`       // 模型标识，对应 logical_model_code
	Object  string `json:"object"`   // 固定为 "model"
	Created int64  `json:"created"`  // 创建时间（Unix 秒）
	OwnedBy string `json:"owned_by"` // 归属方，固定为 "molin"
}

// OpenAIModelList OpenAI /v1/models 的顶层响应结构。
type OpenAIModelList struct {
	Object string        `json:"object"` // 固定为 "list"
	Data   []OpenAIModel `json:"data"`
}
