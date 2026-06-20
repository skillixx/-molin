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
