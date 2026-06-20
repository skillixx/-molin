<script setup lang="ts">
/**
 * AI 对话页（Token 网关，本期只做文本 chat）
 * - 顶部模型下拉（来自 listModels，只取 modality==='chat'）
 * - 消息气泡列表（assistant 随流式增量实时追加）
 * - 输入框 + 发送 + 「停止」（AbortController 中断流式）
 * - 加载/错误态
 */
import { computed, nextTick, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ChatDotRound, Promotion, Refresh, VideoPause } from '@element-plus/icons-vue'
import { chatCompletionsStream, listModels } from '@/api/token'
import type { ChatMessage, ChatStreamError, TokenModel } from '@/types/token'

const router = useRouter()

// 模型列表与当前选择
const models = ref<TokenModel[]>([])
const modelsLoading = ref(false)
const selectedModel = ref<string>('')

// 对话消息（含本地展示的 user / assistant 气泡）
const messages = ref<ChatMessage[]>([])
// 输入框内容
const input = ref('')
// 是否正在流式接收
const streaming = ref(false)
// 中断控制器
let controller: AbortController | null = null

// 消息列表容器，用于自动滚动到底部
const listRef = ref<HTMLElement | null>(null)

const canSend = computed(
  () => !streaming.value && !!selectedModel.value && input.value.trim().length > 0,
)

onMounted(fetchModels)

async function fetchModels() {
  modelsLoading.value = true
  try {
    const res = await listModels()
    // 本期只展示/使用 chat 模态的模型
    models.value = res.items.filter((m) => m.modality === 'chat')
    if (models.value.length > 0 && !selectedModel.value) {
      selectedModel.value = models.value[0].logical_model_code
    }
  } catch {
    // 列模型失败的业务提示已由 http 拦截器统一弹出，这里只兜底空列表
  } finally {
    modelsLoading.value = false
  }
}

async function scrollToBottom() {
  await nextTick()
  if (listRef.value) {
    listRef.value.scrollTop = listRef.value.scrollHeight
  }
}

async function handleSend() {
  if (!canSend.value) return

  const text = input.value.trim()
  input.value = ''

  // 追加用户气泡
  messages.value.push({ role: 'user', content: text })
  // 预置一个空的 assistant 气泡，随流式增量实时填充
  messages.value.push({ role: 'assistant', content: '' })
  const assistantIndex = messages.value.length - 1
  await scrollToBottom()

  streaming.value = true
  controller = new AbortController()

  // 仅发送对话历史（不含刚预置的空 assistant 气泡）
  const payload: ChatMessage[] = messages.value
    .slice(0, assistantIndex)
    .map((m) => ({ role: m.role, content: m.content }))

  await chatCompletionsStream({
    model: selectedModel.value,
    messages: payload,
    onDelta: (delta) => {
      messages.value[assistantIndex].content += delta
      scrollToBottom()
    },
    onDone: () => {
      finishStream()
    },
    onError: (err) => {
      handleStreamError(err, assistantIndex)
      finishStream()
    },
    signal: controller.signal,
  })
}

function handleStreamError(err: ChatStreamError, assistantIndex: number) {
  // 用户主动停止：保留已生成内容，不提示错误
  if (err.aborted) {
    if (messages.value[assistantIndex].content === '') {
      messages.value[assistantIndex].content = '（已停止）'
    }
    return
  }

  // 401 由 fetch 路径无法走 axios 拦截器，这里显式引导重新登录
  if (err.status === 401) {
    ElMessage.error('登录已失效，请重新登录')
    router.push('/login')
  } else if (err.status === 403) {
    ElMessage.error('请先开通/购买 AI 服务')
  } else if (err.status === 402) {
    ElMessage.error('余额不足，请充值')
  } else if (err.status && err.status >= 500) {
    ElMessage.error('模型服务异常，请重试')
  } else {
    ElMessage.error(err.message || '请求失败，请重试')
  }

  // 错误时若该 assistant 气泡还是空的，移除占位避免留空气泡
  if (messages.value[assistantIndex]?.content === '') {
    messages.value.splice(assistantIndex, 1)
  }
}

function finishStream() {
  streaming.value = false
  controller = null
  scrollToBottom()
}

// 「停止」：中断当前流式请求
function handleStop() {
  controller?.abort()
}

// 回车发送（Shift+Enter 换行）
function handleKeydown(e: Event | KeyboardEvent) {
  if (!(e instanceof KeyboardEvent)) return
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    handleSend()
  }
}

// 清空当前对话
function handleClear() {
  if (streaming.value) return
  messages.value = []
}
</script>

<template>
  <div class="chat-page">
    <div class="page-container chat-container">
      <section class="chat-header glass-card">
        <div class="header-left">
          <span class="page-kicker">AI 对话</span>
          <h2 class="page-title">智能助手</h2>
          <p class="page-subtitle">选择模型，输入问题，助手将逐字流式回复。</p>
        </div>
        <div class="header-right">
          <el-select
            v-model="selectedModel"
            class="model-select"
            placeholder="选择模型"
            :loading="modelsLoading"
            :disabled="streaming"
          >
            <el-option
              v-for="m in models"
              :key="m.logical_model_code"
              :label="m.display_name || m.logical_model_code"
              :value="m.logical_model_code"
            />
          </el-select>
          <el-button :icon="Refresh" :loading="modelsLoading" @click="fetchModels">刷新</el-button>
        </div>
      </section>

      <section class="chat-body glass-card">
        <div ref="listRef" class="message-list">
          <div v-if="messages.length === 0" class="empty-state">
            <el-icon class="empty-icon"><ChatDotRound /></el-icon>
            <p>开始一段新的对话吧</p>
            <small v-if="models.length === 0 && !modelsLoading">暂无可用模型，请稍后刷新或确认已开通 AI 服务</small>
          </div>

          <div
            v-for="(msg, idx) in messages"
            :key="idx"
            class="message-row"
            :class="msg.role"
          >
            <div class="avatar" :class="msg.role">
              {{ msg.role === 'user' ? '我' : 'AI' }}
            </div>
            <div class="bubble">
              <span v-if="msg.content">{{ msg.content }}</span>
              <span
                v-else-if="msg.role === 'assistant' && streaming && idx === messages.length - 1"
                class="typing"
              >
                正在思考<span class="dot">.</span><span class="dot">.</span><span class="dot">.</span>
              </span>
            </div>
          </div>
        </div>

        <div class="chat-input">
          <el-input
            v-model="input"
            type="textarea"
            :rows="2"
            :autosize="{ minRows: 2, maxRows: 6 }"
            resize="none"
            placeholder="输入消息，Enter 发送，Shift+Enter 换行"
            :disabled="streaming"
            @keydown="handleKeydown"
          />
          <div class="input-actions">
            <el-button text :disabled="streaming || messages.length === 0" @click="handleClear">
              清空对话
            </el-button>
            <el-button
              v-if="streaming"
              type="danger"
              :icon="VideoPause"
              @click="handleStop"
            >
              停止
            </el-button>
            <el-button
              v-else
              type="primary"
              :icon="Promotion"
              :disabled="!canSend"
              @click="handleSend"
            >
              发送
            </el-button>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.chat-page {
  padding: 34px 0 0;
  height: calc(100vh - 64px);
}
.chat-container {
  display: flex;
  flex-direction: column;
  height: 100%;
  gap: 18px;
  padding-bottom: 24px;
}
.chat-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: 16px;
  padding: 20px 24px;
  flex-shrink: 0;
}
.page-kicker { color: var(--color-accent); font-size: 13px; font-weight: 700; }
.page-title { margin: 6px 0 4px; }
.page-subtitle { color: var(--color-text-muted); }
.header-right {
  display: flex;
  align-items: center;
  gap: 10px;
}
.model-select { width: 200px; }

.chat-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 0;
  overflow: hidden;
}
.message-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 20px 24px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.empty-state {
  margin: auto;
  text-align: center;
  color: var(--color-text-muted);
}
.empty-icon { font-size: 44px; color: var(--color-primary); margin-bottom: 10px; }
.empty-state small { display: block; margin-top: 6px; font-size: 12px; }

.message-row {
  display: flex;
  gap: 12px;
  max-width: 86%;
}
.message-row.user {
  flex-direction: row-reverse;
  align-self: flex-end;
}
.avatar {
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  font-size: 13px;
  font-weight: 700;
  color: #fff;
}
.avatar.user { background: var(--gradient-primary); }
.avatar.assistant { background: rgba(34, 211, 238, 0.22); color: var(--color-accent); }
.bubble {
  padding: 12px 16px;
  border-radius: 12px;
  line-height: 1.7;
  color: var(--color-text);
  white-space: pre-wrap;
  word-break: break-word;
  border: 1px solid var(--color-border);
}
.message-row.user .bubble {
  background: linear-gradient(135deg, rgba(99, 102, 241, 0.26), rgba(139, 92, 246, 0.18));
  border-color: rgba(99, 102, 241, 0.4);
}
.message-row.assistant .bubble {
  background: rgba(15, 23, 42, 0.6);
}
.typing { color: var(--color-text-muted); }
.typing .dot { animation: blink 1.2s infinite; }
.typing .dot:nth-child(2) { animation-delay: 0.2s; }
.typing .dot:nth-child(3) { animation-delay: 0.4s; }
@keyframes blink { 0%, 100% { opacity: 0.2; } 50% { opacity: 1; } }

.chat-input {
  flex-shrink: 0;
  padding: 14px 20px 18px;
  border-top: 1px solid var(--color-border);
  background: rgba(7, 11, 18, 0.4);
}
.input-actions {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 8px;
  margin-top: 10px;
}

@media (max-width: 900px) {
  .chat-page { padding: 18px 0 0; height: calc(100vh - 64px); }
  .chat-header { flex-direction: column; align-items: stretch; }
  .header-right { flex-wrap: wrap; }
  .model-select { width: 100%; flex: 1; }
  .message-row { max-width: 96%; }
}
</style>
