/**
 * Token 网关（AI 对话）API
 *
 * - listModels：列出可用模型，走现有 axios http 实例（自动带 Bearer）。
 * - chatCompletionsStream：流式对话，**必须用原生 fetch** 而非 axios
 *   （axios 不适合读 SSE 流），手动加 Bearer 头、手动解析 `data:` chunk。
 */
import http from './http'
import type { PageResult } from '@/types/api'
import type {
  ApiKeyItem,
  ChatCompletionChunk,
  CreatedApiKey,
  CreateApiKeyReq,
  ChatStreamError,
  ChatStreamOptions,
  ChatUsage,
  TokenModel,
  TokenUsageRecord,
} from '@/types/token'

/**
 * 列出可用模型（D-95 扁平分页）
 * 走 axios http 实例，响应拦截器已解包 data，并自动带 Bearer。
 */
export function listModels() {
  return http.get<unknown, PageResult<TokenModel>>('/token/models', {
    params: { page: 1, page_size: 50 },
  })
}

export function listApiKeys(params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResult<ApiKeyItem>>('/keys', { params })
}

export function createApiKey(data: CreateApiKeyReq) {
  return http.post<unknown, CreatedApiKey>('/keys', data)
}

export function revokeApiKey(id: number) {
  return http.delete<unknown, null>(`/keys/${id}`)
}

export function listMyTokenUsage(params: {
  model?: string
  start?: string
  end?: string
  page?: number
  page_size?: number
} = {}) {
  return http.get<unknown, PageResult<TokenUsageRecord>>('/token/usage', { params })
}

/**
 * 流式对话（OpenAI 兼容，SSE）
 *
 * 技术约束（对接文档第三节）：
 * 1. 用原生 fetch + response.body.getReader() 逐块读取，axios 不适合读流。
 * 2. fetch 不走 http 拦截器，需手动加 Authorization 头；URL 前缀 /api。
 * 3. 逐行解析 `data:` chunk，取 choices[0].delta.content 回调 onDelta；
 *    收到 `data: [DONE]` 触发 onDone。
 */
export async function chatCompletionsStream(options: ChatStreamOptions): Promise<void> {
  const { model, messages, onDelta, onDone, onError, signal } = options

  let response: Response
  try {
    response = await fetch('/api/token/chat/completions', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        // fetch 不经过 axios 拦截器，鉴权需手动加（与 http.ts 约定一致）
        Authorization: `Bearer ${localStorage.getItem('access_token') ?? ''}`,
      },
      body: JSON.stringify({ model, messages, stream: true }),
      signal,
    })
  } catch (e) {
    // 网络层异常 / 主动 abort 都在这里捕获
    onError(toStreamError(e))
    return
  }

  // 非 2xx 起始响应：流尚未开始，尝试读取 JSON 错误体兜底
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
  let lastUsage: ChatUsage | undefined

  try {
    // 逐块读取，按行拆分 SSE 帧
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })

      // SSE 以换行分隔事件行；保留最后一段不完整行到下次循环
      const lines = buffer.split('\n')
      buffer = lines.pop() ?? ''

      for (const rawLine of lines) {
        const line = rawLine.trim()
        if (!line || !line.startsWith('data:')) continue

        const payload = line.slice(5).trim()
        if (payload === '[DONE]') {
          onDone(lastUsage)
          return
        }

        let chunk: ChatCompletionChunk
        try {
          chunk = JSON.parse(payload) as ChatCompletionChunk
        } catch {
          // 非 JSON 帧（心跳/注释等）忽略，不打断主流程
          continue
        }

        // 流内错误事件兜底：部分上游会在帧里塞 error 字段
        const maybeErr = (chunk as unknown as { error?: { message?: string; code?: number } }).error
        if (maybeErr) {
          onError({ message: maybeErr.message || '模型服务异常，请重试', code: maybeErr.code })
          return
        }

        const delta = chunk.choices?.[0]?.delta?.content
        if (delta) onDelta(delta)
        if (chunk.usage) lastUsage = chunk.usage
      }
    }

    // 未显式收到 [DONE] 但流自然结束，也视为完成
    onDone(lastUsage)
  } catch (e) {
    onError(toStreamError(e))
  } finally {
    reader.releaseLock()
  }
}

// 将 fetch 抛出的异常归一化为 ChatStreamError
function toStreamError(e: unknown): ChatStreamError {
  if (e instanceof DOMException && e.name === 'AbortError') {
    return { message: '已停止', aborted: true }
  }
  const message = e instanceof Error ? e.message : '网络异常，请重试'
  return { message }
}

// 解析非 2xx 起始响应的错误体（尽量读出后端 code/message）
async function parseHttpError(response: Response): Promise<ChatStreamError> {
  let code: number | undefined
  let message = ''
  try {
    const data = await response.json()
    code = typeof data?.code === 'number' ? data.code : undefined
    message = data?.message || ''
  } catch {
    // 错误体可能不是 JSON（如上游直接透传），忽略
  }
  return { status: response.status, code, message: message || `请求失败（${response.status}）` }
}
