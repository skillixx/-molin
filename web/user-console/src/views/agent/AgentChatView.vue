<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowLeft, ChatDotRound, Promotion, Refresh, VideoPause } from '@element-plus/icons-vue'
import { agentChatStream, getAgent, listModels } from '@/api/token'
import type {
  AgentChatMessage,
  AgentChatToolCall,
  AgentChatToolResult,
  AgentItem,
  ChatStreamError,
  TokenModel,
} from '@/types/token'

interface ToolTimelineItem {
  type: 'call' | 'result'
  name: string
  content: string
}

const route = useRoute()
const router = useRouter()
const agentId = computed(() => Number(route.params.id))

const agent = ref<AgentItem | null>(null)
const models = ref<TokenModel[]>([])
const selectedModel = ref('')
const loading = ref(false)
const streaming = ref(false)
const input = ref('')
const messages = ref<AgentChatMessage[]>([])
const tools = ref<ToolTimelineItem[]>([])
const listRef = ref<HTMLElement | null>(null)
let controller: AbortController | null = null

const canSend = computed(() => !streaming.value && input.value.trim().length > 0 && !!agent.value)

onMounted(async () => {
  await Promise.all([fetchAgent(), fetchModels()])
})

async function fetchAgent() {
  loading.value = true
  try {
    agent.value = await getAgent(agentId.value)
    selectedModel.value = agent.value.default_model_code
  } finally {
    loading.value = false
  }
}

async function fetchModels() {
  const res = await listModels()
  models.value = res.items.filter((item) => item.modality === 'chat')
}

async function scrollToBottom() {
  await nextTick()
  if (listRef.value) listRef.value.scrollTop = listRef.value.scrollHeight
}

async function handleSend() {
  if (!canSend.value || !agent.value) return
  const text = input.value.trim()
  input.value = ''

  messages.value.push({ role: 'user', content: text })
  messages.value.push({ role: 'assistant', content: '' })
  const assistantIndex = messages.value.length - 1
  tools.value = []
  await scrollToBottom()

  streaming.value = true
  controller = new AbortController()

  const history = messages.value.slice(0, assistantIndex)
  await agentChatStream({
    agent_id: agent.value.id,
    messages: history,
    model: selectedModel.value || undefined,
    onToolCall: handleToolCall,
    onToolResult: handleToolResult,
    onMessage: (data) => {
      const suffix = data.finish_reason === 'max_rounds' ? '\n\n已达工具上限，已正常计费。' : ''
      messages.value[assistantIndex].content += `${data.content || ''}${suffix}`
      scrollToBottom()
    },
    onDone: finishStream,
    onError: (err) => {
      handleStreamError(err, assistantIndex)
      finishStream()
    },
    signal: controller.signal,
  })
}

function handleToolCall(data: AgentChatToolCall) {
  tools.value.push({
    type: 'call',
    name: data.name,
    content: data.arguments || '正在调用工具',
  })
  scrollToBottom()
}

function handleToolResult(data: AgentChatToolResult) {
  tools.value.push({
    type: 'result',
    name: data.name,
    content: data.content || '工具已返回',
  })
  scrollToBottom()
}

function handleStreamError(err: ChatStreamError, assistantIndex: number) {
  if (err.aborted) {
    if (!messages.value[assistantIndex].content) messages.value[assistantIndex].content = '（已停止）'
    return
  }

  if (err.status === 401 || err.code === 40001) {
    ElMessage.error('登录已失效，请重新登录')
    router.push('/login')
  } else if (err.code === 40300) {
    ElMessage.error('未开通 Token 服务或暂无可用模型')
    router.push('/token/packages')
  } else if (err.code === 60001) {
    ElMessage.error('钱包余额不足，请先充值')
    router.push('/wallet/recharge')
  } else if (err.code === 60005) {
    ElMessage.error('套餐额度不足，请购买 Token 套餐')
    router.push('/token/packages')
  } else if (err.code === 50301) {
    ElMessage.error('系统繁忙，请稍后重试')
  } else {
    ElMessage.error(err.message || '编排对话失败，请重试')
  }

  if (!messages.value[assistantIndex]?.content) messages.value.splice(assistantIndex, 1)
}

function finishStream() {
  streaming.value = false
  controller = null
  scrollToBottom()
}

function handleStop() {
  controller?.abort()
}

function handleClear() {
  if (streaming.value) return
  messages.value = []
  tools.value = []
}

function handleKeydown(e: Event | KeyboardEvent) {
  if (!(e instanceof KeyboardEvent)) return
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    handleSend()
  }
}
</script>

<template>
  <div class="agent-chat-page">
    <div class="page-container chat-shell">
      <section class="chat-header glass-card" v-loading="loading">
        <div class="header-left">
          <el-button :icon="ArrowLeft" text @click="router.push('/agents')">返回工作台</el-button>
          <div>
            <span class="page-kicker">编排对话</span>
            <h2>{{ agent?.name || 'Agent 对话' }}</h2>
            <p>{{ agent?.description || '站内 Agent 会自动编排已绑定的 Skill 与插件。' }}</p>
          </div>
        </div>
        <div class="header-right">
          <el-select v-model="selectedModel" class="model-select" filterable placeholder="默认模型">
            <el-option
              v-for="model in models"
              :key="model.logical_model_code"
              :label="model.display_name || model.logical_model_code"
              :value="model.logical_model_code"
            />
          </el-select>
          <el-button :icon="Refresh" @click="fetchAgent">刷新</el-button>
        </div>
      </section>

      <section class="chat-body glass-card">
        <div ref="listRef" class="message-list">
          <div v-if="messages.length === 0" class="empty-state">
            <el-icon><ChatDotRound /></el-icon>
            <p>向 Agent 发起第一轮对话</p>
          </div>

          <div v-for="(msg, idx) in messages" :key="idx" class="message-row" :class="msg.role">
            <div class="avatar">{{ msg.role === 'user' ? '我' : 'AI' }}</div>
            <div class="bubble">
              <span v-if="msg.content">{{ msg.content }}</span>
              <span v-else class="typing">正在编排工具与模型...</span>
            </div>
          </div>

          <el-collapse v-if="tools.length > 0" class="tool-panel">
            <el-collapse-item title="工具调用过程" name="tools">
              <div v-for="(item, idx) in tools" :key="idx" class="tool-item" :class="item.type">
                <strong>{{ item.type === 'call' ? '正在调用' : '工具返回' }}：{{ item.name }}</strong>
                <pre>{{ item.content }}</pre>
              </div>
            </el-collapse-item>
          </el-collapse>
        </div>

        <div class="chat-input">
          <el-input
            v-model="input"
            type="textarea"
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
            <el-button v-if="streaming" type="danger" :icon="VideoPause" @click="handleStop">
              停止
            </el-button>
            <el-button v-else type="primary" :icon="Promotion" :disabled="!canSend" @click="handleSend">
              发送
            </el-button>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.agent-chat-page { padding: 24px 0 0; height: calc(100vh - 64px); }
.chat-shell { height: 100%; display: flex; flex-direction: column; gap: 16px; padding-bottom: 24px; }
.chat-header {
  flex-shrink: 0;
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: 18px;
  padding: 18px 22px;
}
.header-left,
.header-right { display: flex; align-items: flex-end; gap: 14px; }
.page-kicker { color: var(--color-accent); font-size: 13px; font-weight: 700; }
.chat-header h2 { margin: 6px 0 4px; }
.chat-header p { color: var(--color-text-muted); }
.model-select { width: 220px; }
.chat-body { flex: 1; min-height: 0; display: flex; flex-direction: column; overflow: hidden; padding: 0; }
.message-list { flex: 1; overflow-y: auto; padding: 22px; display: flex; flex-direction: column; gap: 14px; }
.empty-state { margin: auto; text-align: center; color: var(--color-text-muted); }
.empty-state .el-icon { font-size: 44px; color: var(--color-primary); margin-bottom: 10px; }
.message-row { display: flex; gap: 12px; max-width: 88%; }
.message-row.user { align-self: flex-end; flex-direction: row-reverse; }
.avatar {
  width: 36px;
  height: 36px;
  border-radius: 50%;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  background: rgba(34, 211, 238, 0.18);
  color: var(--color-accent);
  font-weight: 800;
}
.message-row.user .avatar { background: var(--gradient-primary); color: #fff; }
.bubble {
  padding: 12px 16px;
  border-radius: 12px;
  border: 1px solid var(--color-border);
  background: rgba(15, 23, 42, 0.62);
  white-space: pre-wrap;
  line-height: 1.7;
  word-break: break-word;
}
.message-row.user .bubble {
  background: linear-gradient(135deg, rgba(99, 102, 241, 0.25), rgba(34, 211, 238, 0.12));
}
.typing { color: var(--color-text-muted); }
.tool-panel { margin-top: 4px; }
.tool-item {
  padding: 10px 12px;
  border-radius: 8px;
  border: 1px solid var(--color-border);
  background: rgba(15, 23, 42, 0.5);
  margin-bottom: 8px;
}
.tool-item.result { border-color: rgba(52, 211, 153, 0.35); }
.tool-item pre { margin-top: 8px; white-space: pre-wrap; color: var(--color-text-muted); }
.chat-input { flex-shrink: 0; padding: 14px 20px 18px; border-top: 1px solid var(--color-border); }
.input-actions { display: flex; justify-content: flex-end; align-items: center; gap: 8px; margin-top: 10px; }
@media (max-width: 900px) {
  .chat-header,
  .header-left,
  .header-right { flex-direction: column; align-items: stretch; }
  .model-select { width: 100%; }
  .message-row { max-width: 96%; }
}
</style>
