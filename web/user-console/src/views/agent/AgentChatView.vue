<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  ArrowLeft,
  ChatDotRound,
  Delete,
  Edit,
  Menu,
  Plus,
  Promotion,
  Refresh,
  VideoPause,
} from '@element-plus/icons-vue'
import {
  conversationChatStream,
  createConversation,
  deleteConversation,
  getConversation,
  listConversations,
  listMessages,
  renameConversation,
} from '@/api/conversation'
import { getAgent, listModels } from '@/api/token'
import type {
  AgentChatMessage,
  AgentChatToolCall,
  AgentChatToolResult,
  AgentItem,
  ChatStreamError,
  TokenModel,
} from '@/types/token'
import type { Conversation } from '@/types/conversation'

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
const conversationsLoading = ref(false)
const messagesLoading = ref(false)
const streaming = ref(false)
const input = ref('')
const messages = ref<AgentChatMessage[]>([])
const tools = ref<ToolTimelineItem[]>([])
const conversations = ref<Conversation[]>([])
const activeConversationId = ref<number | null>(null)
const drawerVisible = ref(false)
const listRef = ref<HTMLElement | null>(null)
let controller: AbortController | null = null

const currentAgentConversations = computed(() => (
  conversations.value.filter((item) => item.agent_id === agentId.value)
))
const activeConversation = computed(() => (
  currentAgentConversations.value.find((item) => item.id === activeConversationId.value) || null
))
const canSend = computed(() => !streaming.value && input.value.trim().length > 0 && !!agent.value)

onMounted(async () => {
  await Promise.all([fetchAgent(), fetchModels(), fetchConversations()])
  await syncConversationFromRoute()
})

watch(
  () => route.query.conversation_id,
  () => {
    syncConversationFromRoute()
  },
)

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

async function fetchConversations() {
  conversationsLoading.value = true
  try {
    const res = await listConversations({ type: 'agent', page: 1, page_size: 100 })
    conversations.value = res.items
  } finally {
    conversationsLoading.value = false
  }
}

async function syncConversationFromRoute() {
  const queryId = Number(route.query.conversation_id || 0)
  if (!queryId) return
  if (queryId === activeConversationId.value && messages.value.length > 0) return
  await openConversation(queryId, false)
}

async function openConversation(id: number, syncRoute = true) {
  if (streaming.value) return
  messagesLoading.value = true
  try {
    const conversation = await getConversation(id)
    if (conversation.agent_id !== agentId.value) {
      ElMessage.error('该会话不属于当前 Agent')
      return
    }
    activeConversationId.value = conversation.id
    selectedModel.value = conversation.model_code || selectedModel.value
    tools.value = []
    const res = await listMessages(conversation.id, { page: 1, page_size: 200 })
    messages.value = res.items
      .filter((item) => item.role === 'user' || item.role === 'assistant')
      .map((item) => ({ role: item.role as 'user' | 'assistant', content: item.content }))
    drawerVisible.value = false
    if (syncRoute) {
      await router.replace({
        path: `/agents/${agentId.value}/chat`,
        query: { conversation_id: String(conversation.id) },
      })
    }
    await scrollToBottom()
  } catch (e) {
    handleConversationLoadError(e)
  } finally {
    messagesLoading.value = false
  }
}

function handleConversationLoadError(e: unknown) {
  const code = (e as { response?: { data?: { code?: number } } })?.response?.data?.code
  if (code === 40400) {
    ElMessage.error('会话不存在或已被删除')
    startNewConversation()
  }
}

async function scrollToBottom() {
  await nextTick()
  if (listRef.value) listRef.value.scrollTop = listRef.value.scrollHeight
}

async function ensureConversation() {
  if (activeConversationId.value) return activeConversationId.value
  const conversation = await createConversation({
    agent_id: agentId.value,
    model_code: selectedModel.value || undefined,
  })
  activeConversationId.value = conversation.id
  await router.replace({
    path: `/agents/${agentId.value}/chat`,
    query: { conversation_id: String(conversation.id) },
  })
  await fetchConversations()
  return conversation.id
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

  try {
    const conversationId = await ensureConversation()
    await conversationChatStream(conversationId, {
      content: text,
      onToolCall: handleToolCall,
      onToolResult: handleToolResult,
      onMessage: (data) => {
        const suffix = data.finish_reason === 'max_rounds' ? '\n\n已达工具上限，已正常计费。' : ''
        messages.value[assistantIndex].content = `${data.content || ''}${suffix}`
        scrollToBottom()
      },
      onDone: async () => {
        finishStream()
        await fetchConversations()
      },
      onError: (err) => {
        handleStreamError(err, assistantIndex)
        finishStream()
      },
      signal: controller.signal,
    })
  } catch (e) {
    handleStreamError(toChatStreamError(e), assistantIndex)
    finishStream()
  }
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

function toChatStreamError(e: unknown): ChatStreamError {
  const response = (e as { response?: { status?: number; data?: { code?: number; message?: string } } })?.response
  return {
    status: response?.status,
    code: response?.data?.code,
    message: response?.data?.message || (e instanceof Error ? e.message : '请求失败，请重试'),
  }
}

function handleStreamError(err: ChatStreamError, assistantIndex: number) {
  if (err.aborted) {
    if (!messages.value[assistantIndex].content) messages.value[assistantIndex].content = '（已停止）'
    return
  }

  if (err.status === 401 || err.code === 40001) {
    ElMessage.error('登录已失效，请重新登录')
    router.push('/login')
  } else if (err.code === 40400) {
    ElMessage.error('会话不存在，请新建会话后重试')
    startNewConversation()
  } else if (err.code === 40300 || err.status === 403) {
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

function startNewConversation() {
  if (streaming.value) return
  activeConversationId.value = null
  messages.value = []
  tools.value = []
  drawerVisible.value = false
  router.replace({ path: `/agents/${agentId.value}/chat` })
}

async function handleRename(conversation: Conversation) {
  let title = ''
  try {
    const { value } = await ElMessageBox.prompt('请输入新的会话标题', '重命名会话', {
      inputValue: conversation.title,
      inputPattern: /\S+/,
      inputErrorMessage: '标题不能为空',
      confirmButtonText: '保存',
      cancelButtonText: '取消',
    })
    title = value.trim()
  } catch {
    // 用户取消重命名时不提示错误。
    return
  }
  await renameConversation(conversation.id, { title })
  ElMessage.success('会话已重命名')
  await fetchConversations()
}

async function handleDelete(conversation: Conversation) {
  try {
    await ElMessageBox.confirm(`确认删除会话「${conversation.title || '未命名会话'}」？`, '删除会话', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    // 用户取消删除时不提示错误。
    return
  }
  await deleteConversation(conversation.id)
  ElMessage.success('会话已删除')
  if (activeConversationId.value === conversation.id) {
    startNewConversation()
  }
  await fetchConversations()
}

function handleKeydown(e: Event | KeyboardEvent) {
  if (!(e instanceof KeyboardEvent)) return
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    handleSend()
  }
}

function formatConversationTime(value: string | null) {
  if (!value) return '暂无消息'
  return new Date(value).toLocaleString()
}
</script>

<template>
  <div class="agent-chat-page">
    <div class="page-container chat-layout">
      <aside class="conversation-panel glass-card">
        <div class="panel-header">
          <strong>会话</strong>
          <el-button type="primary" :icon="Plus" size="small" @click="startNewConversation">新建</el-button>
        </div>
        <div v-loading="conversationsLoading" class="conversation-list">
          <button
            v-for="conversation in currentAgentConversations"
            :key="conversation.id"
            class="conversation-item"
            :class="{ active: conversation.id === activeConversationId }"
            @click="openConversation(conversation.id)"
          >
            <span class="conversation-title">{{ conversation.title || '未命名会话' }}</span>
            <small>{{ formatConversationTime(conversation.last_message_at) }}</small>
            <span class="conversation-actions" @click.stop>
              <el-button :icon="Edit" text size="small" @click="handleRename(conversation)" />
              <el-button :icon="Delete" text size="small" type="danger" @click="handleDelete(conversation)" />
            </span>
          </button>
          <div v-if="currentAgentConversations.length === 0 && !conversationsLoading" class="panel-empty">暂无历史会话</div>
        </div>
      </aside>

      <div class="chat-main">
        <section class="chat-header glass-card" v-loading="loading">
          <div class="header-left">
            <el-button :icon="ArrowLeft" text @click="router.push('/agents')">返回工作台</el-button>
            <div>
              <span class="page-kicker">编排对话</span>
              <h2>{{ activeConversation?.title || agent?.name || 'Agent 对话' }}</h2>
              <p>{{ agent?.description || '站内 Agent 会自动编排已绑定的 Skill 与插件。' }}</p>
            </div>
          </div>
          <div class="header-right">
            <el-button class="mobile-history" :icon="Menu" @click="drawerVisible = true">历史</el-button>
            <el-select v-model="selectedModel" class="model-select" filterable placeholder="默认模型" :disabled="streaming || !!activeConversationId">
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

        <section class="chat-body glass-card" v-loading="messagesLoading">
          <div ref="listRef" class="message-list">
            <div v-if="messages.length === 0" class="empty-state">
              <el-icon><ChatDotRound /></el-icon>
              <p>向 Agent 发起第一轮对话</p>
            </div>

            <div v-for="(msg, idx) in messages" :key="idx" class="message-row" :class="msg.role">
              <div class="avatar">{{ msg.role === 'user' ? '我' : 'AI' }}</div>
              <div class="bubble">
                <span v-if="msg.content">{{ msg.content }}</span>
                <span v-else-if="msg.role === 'assistant' && streaming && idx === messages.length - 1" class="typing">正在编排工具与模型...</span>
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
              <el-button text :disabled="streaming" @click="startNewConversation">新建会话</el-button>
              <el-button v-if="streaming" type="danger" :icon="VideoPause" @click="handleStop">停止</el-button>
              <el-button v-else type="primary" :icon="Promotion" :disabled="!canSend" @click="handleSend">发送</el-button>
            </div>
          </div>
        </section>
      </div>
    </div>

    <el-drawer v-model="drawerVisible" title="会话历史" size="82%">
      <div class="panel-header drawer-header">
        <strong>会话</strong>
        <el-button type="primary" :icon="Plus" size="small" @click="startNewConversation">新建</el-button>
      </div>
      <div v-loading="conversationsLoading" class="conversation-list drawer-list">
        <button
          v-for="conversation in currentAgentConversations"
          :key="conversation.id"
          class="conversation-item"
          :class="{ active: conversation.id === activeConversationId }"
          @click="openConversation(conversation.id)"
        >
          <span class="conversation-title">{{ conversation.title || '未命名会话' }}</span>
          <small>{{ formatConversationTime(conversation.last_message_at) }}</small>
          <span class="conversation-actions" @click.stop>
            <el-button :icon="Edit" text size="small" @click="handleRename(conversation)" />
            <el-button :icon="Delete" text size="small" type="danger" @click="handleDelete(conversation)" />
          </span>
        </button>
      </div>
    </el-drawer>
  </div>
</template>

<style scoped>
.agent-chat-page { padding: 24px 0 0; height: calc(100vh - 64px); }
.chat-layout { display: grid; grid-template-columns: 280px minmax(0, 1fr); height: 100%; gap: 16px; padding-bottom: 24px; }
.conversation-panel { min-height: 0; overflow: hidden; display: flex; flex-direction: column; padding: 14px; }
.panel-header { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-bottom: 12px; }
.conversation-list { flex: 1; min-height: 0; overflow-y: auto; display: flex; flex-direction: column; gap: 8px; }
.conversation-item {
  position: relative;
  width: 100%;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: rgba(15, 23, 42, 0.5);
  color: var(--color-text);
  text-align: left;
  padding: 10px 72px 10px 12px;
  cursor: pointer;
}
.conversation-item.active { border-color: rgba(34, 211, 238, 0.75); background: rgba(34, 211, 238, 0.12); }
.conversation-title { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 700; }
.conversation-item small { display: block; margin-top: 4px; color: var(--color-text-muted); font-size: 12px; }
.conversation-actions { position: absolute; right: 4px; top: 5px; display: flex; gap: 0; }
.panel-empty { margin: auto; color: var(--color-text-muted); font-size: 13px; }
.chat-main { min-width: 0; height: 100%; display: flex; flex-direction: column; gap: 16px; }
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
.mobile-history { display: none; }
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
.message-row.user .bubble { background: linear-gradient(135deg, rgba(99, 102, 241, 0.25), rgba(34, 211, 238, 0.12)); }
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
.drawer-list { min-height: 420px; }
@media (max-width: 900px) {
  .agent-chat-page { padding: 14px 0 0; }
  .chat-layout { display: flex; padding-bottom: 16px; }
  .conversation-panel { display: none; }
  .chat-header,
  .header-left,
  .header-right { flex-direction: column; align-items: stretch; }
  .mobile-history { display: inline-flex; }
  .model-select { width: 100%; }
  .message-row { max-width: 96%; }
}
</style>
