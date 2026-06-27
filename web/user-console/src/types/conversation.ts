import type {
  AgentChatAnswer,
  AgentChatToolCall,
  AgentChatToolResult,
  ChatStreamError,
} from './token'

export type ConversationType = 'plain' | 'agent'

export interface Conversation {
  id: number
  agent_id: number | null
  title: string
  model_code: string
  message_count: number
  last_message_at: string | null
  created_at: string
  updated_at: string
}

export interface ConversationMessage {
  id: number
  role: 'user' | 'assistant' | 'tool' | 'system' | string
  content: string
  created_at: string
}

export interface CreateConversationReq {
  agent_id?: number | null
  model_code?: string
  title?: string
}

export interface ConversationChatStreamOptions {
  content: string
  onToolCall?: (data: AgentChatToolCall) => void
  onToolResult?: (data: AgentChatToolResult) => void
  onMessage: (data: AgentChatAnswer) => void
  onDone: () => void
  onError: (err: ChatStreamError) => void
  signal?: AbortSignal
}
