/**
 * Token 网关（AI 对话）相关类型定义
 * 对应后端用户端接口 /api/token/*
 */

// 可用模型（GET /api/token/models 的 items 元素）
export interface TokenModel {
  logical_model_code: string
  display_name: string
  // 模态：本期只使用 chat，生图/语音/视频为后续预留
  modality: 'chat' | string
}

export type ApiKeyStatus = 'active' | 'revoked'
export type ApiKeyBillingMode = 'postpaid' | 'prepaid'

// 用户端 API Key 列表项；列表只返回 key_prefix，不返回完整密钥。
export interface ApiKeyItem {
  id: number
  name: string
  key_prefix: string
  billing_mode: ApiKeyBillingMode
  model_scope: string[]
  status: ApiKeyStatus
  last_used_at: string | null
  created_at: string
}

// 创建 API Key 的请求体；model_scope 为空或不传表示不限模型。
export interface CreateApiKeyReq {
  name: string
  model_scope?: string[]
}

// 创建 API Key 的响应会额外返回完整明文 secret_key，且只返回一次。
export interface CreatedApiKey extends ApiKeyItem {
  secret_key: string
}

export type TokenUsageStatus = 'success' | 'failed' | 'timeout'

// Token 用量流水，用户端不包含 user_id / api_key_id。
export interface TokenUsageRecord {
  request_id: string
  logical_model_code: string
  modality: string
  input_tokens: number
  output_tokens: number
  total_tokens: number
  sale_amount: string
  is_stream: boolean
  status: TokenUsageStatus
  error_code: string | null
  created_at: string
}

// OpenAI 兼容消息角色
export type ChatRole = 'system' | 'user' | 'assistant'

// 一条对话消息
export interface ChatMessage {
  role: ChatRole
  content: string
}

// 流式增量 chunk（OpenAI 兼容，choices[0].delta.content）
export interface ChatCompletionChunk {
  choices?: Array<{
    delta?: { role?: ChatRole; content?: string }
    finish_reason?: string | null
  }>
  usage?: ChatUsage
}

// 本轮 token 消耗
export interface ChatUsage {
  prompt_tokens?: number
  completion_tokens?: number
  total_tokens?: number
}

// 流式对话回调参数
export interface ChatStreamOptions {
  model: string
  messages: ChatMessage[]
  // 每收到一段增量文本时回调
  onDelta: (text: string) => void
  // 流正常结束（收到 data: [DONE]）时回调，附带末尾 usage（若有）
  onDone: (usage?: ChatUsage) => void
  // 出错时回调（网络异常 / 非 2xx 起始响应 / 流内错误）
  onError: (err: ChatStreamError) => void
  // 用于「停止」按钮中断流式
  signal?: AbortSignal
}

// 流式错误类型，便于页面按 HTTP 状态码做差异化兜底
export interface ChatStreamError {
  // HTTP 状态码（非 2xx 起始响应时存在）
  status?: number
  // 后端业务码 / 提示
  code?: number
  message: string
  // 是否为用户主动 abort（停止按钮），页面无需提示错误
  aborted?: boolean
}
