import http from './http'
import type { PageResult } from '@/types/api'
import type { ChatStreamError } from '@/types/token'
import type {
  Conversation,
  ConversationChatStreamOptions,
  ConversationMessage,
  ConversationType,
  CreateConversationReq,
} from '@/types/conversation'

export function createConversation(data: CreateConversationReq) {
  return http.post<unknown, Conversation>('/conversations', data)
}

export function listConversations(params: {
  type?: ConversationType
  page?: number
  page_size?: number
} = {}) {
  return http.get<unknown, PageResult<Conversation>>('/conversations', { params })
}

export function getConversation(id: number) {
  return http.get<unknown, Conversation>(`/conversations/${id}`)
}

export function listMessages(id: number, params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResult<ConversationMessage>>(`/conversations/${id}/messages`, { params })
}

export function renameConversation(id: number, data: { title: string }) {
  return http.patch<unknown, { id: number; title: string }>(`/conversations/${id}`, data)
}

export function deleteConversation(id: number) {
  return http.delete<unknown, { id: number }>(`/conversations/${id}`)
}

export async function conversationChatStream(
  conversationId: number,
  options: ConversationChatStreamOptions,
): Promise<void> {
  const { content, onToolCall, onToolResult, onMessage, onDone, onError, signal } = options

  let response: Response
  try {
    response = await fetch(`/api/conversations/${conversationId}/chat`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        // fetch 不经过 axios 拦截器，流式接口需要手动携带登录态。
        Authorization: `Bearer ${localStorage.getItem('access_token') ?? ''}`,
      },
      body: JSON.stringify({ content, stream: true }),
      signal,
    })
  } catch (e) {
    onError(toStreamError(e))
    return
  }

  if (!response.ok) {
    onError(await parseHttpError(response))
    return
  }

  const reader = response.body?.getReader()
  if (!reader) {
    onError({ message: '响应不支持流式读取' })
    return
  }

  const decoder = new TextDecoder('utf-8')
  let buffer = ''
  let currentEvent = 'message'

  try {
    // 会话聊天使用 workbench 事件流：event/data 组成一帧，空行表示帧结束。
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })

      const frames = buffer.split('\n\n')
      buffer = frames.pop() ?? ''

      for (const frame of frames) {
        const lines = frame.split('\n').map((line) => line.trim()).filter(Boolean)
        let dataPayload = ''

        for (const line of lines) {
          if (line.startsWith('event:')) currentEvent = line.slice(6).trim()
          if (line.startsWith('data:')) dataPayload += line.slice(5).trim()
        }

        if (!dataPayload) continue
        if (dataPayload === '[DONE]') {
          onDone()
          return
        }

        try {
          const data = JSON.parse(dataPayload)
          if (currentEvent === 'tool_call') onToolCall?.(data)
          else if (currentEvent === 'tool_result') onToolResult?.(data)
          else if (currentEvent === 'error') {
            onError({ message: data?.message || '对话异常' })
            return
          } else onMessage(data)
        } catch {
          // 非 JSON 帧可能是心跳或异常噪声，忽略后继续读取后续事件。
        } finally {
          currentEvent = 'message'
        }
      }
    }

    onDone()
  } catch (e) {
    onError(toStreamError(e))
  } finally {
    reader.releaseLock()
  }
}

function toStreamError(e: unknown): ChatStreamError {
  if (e instanceof DOMException && e.name === 'AbortError') {
    return { message: '已停止', aborted: true }
  }
  const message = e instanceof Error ? e.message : '网络异常，请重试'
  return { message }
}

async function parseHttpError(response: Response): Promise<ChatStreamError> {
  let code: number | undefined
  let message = ''
  try {
    const data = await response.json()
    code = typeof data?.code === 'number' ? data.code : undefined
    message = data?.message || ''
  } catch {
    // 错误体可能不是 JSON，使用 HTTP 状态码兜底。
  }
  return { status: response.status, code, message: message || `请求失败（${response.status}）` }
}
