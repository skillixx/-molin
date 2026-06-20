package dto

// ChatCompletionReq 仅用于校验/取关键字段（model、stream）。
// 完整 OpenAI 请求体由 handler 以 map[string]interface{} 原样透传给上游，
// 避免在门面层定义全部 OpenAI 字段（薄转发器，近似纯透传）。
type ChatCompletionReq struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}
