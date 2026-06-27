<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
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
import { listModels } from '@/api/token'
import type { ChatMessage, ChatStreamError, TokenModel } from '@/types/token'
import type { Conversation } from '@/types/conversation'

const route = useRoute()
const router = useRouter()

const models = ref<TokenModel[]>([])
const modelsLoading = ref(false)
const selectedModel = ref('')

const conversations = ref<Conversation[]>([])
const conversationsLoading = ref(false)
const messagesLoading = ref(false)
const activeConversationId = ref<number | null>(null)
const drawerVisible = ref(false)

const messages = ref<ChatMessage[]>([])
const input = ref('')
const streaming = ref(false)
let controller: AbortController | null = null

const listRef = ref<HTMLElement | null>(null)

const activeConversation = computed(() => (
  conversations.value.find((item) => item.id === activeConversationId.value) || null
))
const canSend = computed(
  () => !streaming.value && !!selectedModel.value && input.value.trim().length > 0,
)

onMounted(async () => {
  await Promise.all([fetchModels(), fetchConversations()])
  await syncConversationFromRoute()
})

watch(
  () => route.query.conversation_id,
  () => {
    syncConversationFromRoute()
  },
)

async function fetchModels() {
  modelsLoading.value = true
  try {
    const res = await listModels()
    models.value = res.items.filter((m) => m.modality === 'chat')
    if (models.value.length > 0 && !selectedModel.value) {
      selectedModel.value = models.value[0].logical_model_code
    }
  } finally {
    modelsLoading.value = false
  }
}

async function fetchConversations() {
  conversationsLoading.value = true
  try {
    const res = await listConversations({ type: 'plain', page: 1, page_size: 50 })
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
    activeConversationId.value = conversation.id
    selectedModel.value = conversation.model_code || selectedModel.value
    const res = await listMessages(conversation.id, { page: 1, page_size: 200 })
    messages.value = res.items
      .filter((item) => item.role === 'user' || item.role === 'assistant')
      .map((item) => ({ role: item.role as 'user' | 'assistant', content: item.content }))
    drawerVisible.value = false
    if (syncRoute) {
      await router.replace({ path: '/chat', query: { conversation_id: String(conversation.id) } })
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
  if (listRef.value) {
    listRef.value.scrollTop = listRef.value.scrollHeight
  }
}

async function ensureConversation() {
  if (activeConversationId.value) return activeConversationId.value
  const conversation = await createConversation({ model_code: selectedModel.value })
  activeConversationId.value = conversation.id
  await router.replace({ path: '/chat', query: { conversation_id: String(conversation.id) } })
  await fetchConversations()
  return conversation.id
}

async function handleSend() {
  if (!canSend.value) return

  const text = input.value.trim()
  input.value = ''

  messages.value.push({ role: 'user', content: text })
  messages.value.push({ role: 'assistant', content: '' })
  const assistantIndex = messages.value.length - 1
  await scrollToBottom()

  streaming.value = true
  controller = new AbortController()

  try {
    const conversationId = await ensureConversation()
    await conversationChatStream(conversationId, {
      content: text,
      onMessage: (data) => {
        messages.value[assistantIndex].content = data.content || ''
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
    if (messages.value[assistantIndex].content === '') {
      messages.value[assistantIndex].content = '（已停止）'
    }
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
  } else if (err.code === 60001 || err.status === 402) {
    ElMessage.error('钱包余额不足，请先充值')
    router.push('/wallet/recharge')
  } else if (err.code === 60005) {
    ElMessage.error('套餐额度不足，请购买 Token 套餐')
    router.push('/token/packages')
  } else if (err.code === 50301 || (err.status && err.status >= 500)) {
    ElMessage.error('系统繁忙，请稍后重试')
  } else {
    ElMessage.error(err.message || '请求失败，请重试')
  }

  if (messages.value[assistantIndex]?.content === '') {
    messages.value.splice(assistantIndex, 1)
  }
}

function finishStream() {
  streaming.value = false
  controller = null
  scrollToBottom()
}

function handleStop() {
  controller?.abort()
}

function handleKeydown(e: Event | KeyboardEvent) {
  if (!(e instanceof KeyboardEvent)) return
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    handleSend()
  }
}

function startNewConversation() {
  if (streaming.value) return
  activeConversationId.value = null
  messages.value = []
  drawerVisible.value = false
  router.replace({ path: '/chat' })
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

function formatConversationTime(value: string | null) {
  if (!value) return '暂无消息'
  return new Date(value).toLocaleString()
}
</script>

<template>
  <div class="chat-page">
    <div class="page-container chat-layout">
      <aside class="conversation-panel glass-card">
        <div class="panel-header">
          <strong>会话</strong>
          <el-button type="primary" :icon="Plus" size="small" @click="startNewConversation">新建</el-button>
        </div>
        <div v-loading="conversationsLoading" class="conversation-list">
          <button
            v-for="conversation in conversations"
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
          <div v-if="conversations.length === 0 && !conversationsLoading" class="panel-empty">暂无历史会话</div>
        </div>
      </aside>

      <div class="chat-main">
        <section class="chat-header glass-card">
          <div class="header-left">
            <span class="page-kicker">AI 对话</span>
            <h2 class="page-title">{{ activeConversation?.title || '智能助手' }}</h2>
            <p class="page-subtitle">会话由后端持久化保存，刷新页面后可继续上下文。</p>
          </div>
          <div class="header-right">
            <el-button class="mobile-history" :icon="Menu" @click="drawerVisible = true">历史</el-button>
            <el-select
              v-model="selectedModel"
              class="model-select"
              placeholder="选择模型"
              :loading="modelsLoading"
              :disabled="streaming || !!activeConversationId"
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

        <section class="chat-body glass-card" v-loading="messagesLoading">
          <div ref="listRef" class="message-list">
            <div v-if="messages.length === 0" class="empty-state">
              <el-icon class="empty-icon"><ChatDotRound /></el-icon>
              <p>开始一段新的对话吧</p>
              <small v-if="models.length === 0 && !modelsLoading">暂无可用模型，请稍后刷新或确认已开通 AI 服务</small>
            </div>

            <div v-for="(msg, idx) in messages" :key="idx" class="message-row" :class="msg.role">
              <div class="avatar" :class="msg.role">{{ msg.role === 'user' ? '我' : 'AI' }}</div>
              <div class="bubble">
                <span v-if="msg.content">{{ msg.content }}</span>
                <span v-else-if="msg.role === 'assistant' && streaming && idx === messages.length - 1" class="typing">
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
              <el-button text :disabled="streaming" @click="startNewConversation">新建会话</el-button>
              <el-button v-if="streaming" type="danger" :icon="VideoPause" @click="handleStop">停止</el-button>
              <el-button v-else type="primary" :icon="Promotion" :disabled="!canSend" @click="handleSend">发送</el-button>
            </div>
          </div>
        </section>
      </div>
    </div>

    <el-drawer v-model="drawerVisible" title="会话历史" size="82%" class="conversation-drawer">
      <div class="panel-header drawer-header">
        <strong>会话</strong>
        <el-button type="primary" :icon="Plus" size="small" @click="startNewConversation">新建</el-button>
      </div>
      <div v-loading="conversationsLoading" class="conversation-list drawer-list">
        <button
          v-for="conversation in conversations"
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
.chat-page { padding: 24px 0 0; height: calc(100vh - 64px); }
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
.chat-header { display: flex; justify-content: space-between; align-items: flex-end; gap: 16px; padding: 20px 24px; flex-shrink: 0; }
.page-kicker { color: var(--color-accent); font-size: 13px; font-weight: 700; }
.page-title { margin: 6px 0 4px; }
.page-subtitle { color: var(--color-text-muted); }
.header-right { display: flex; align-items: center; gap: 10px; }
.model-select { width: 210px; }
.mobile-history { display: none; }
.chat-body { flex: 1; min-height: 0; display: flex; flex-direction: column; padding: 0; overflow: hidden; }
.message-list { flex: 1; min-height: 0; overflow-y: auto; padding: 20px 24px; display: flex; flex-direction: column; gap: 16px; }
.empty-state { margin: auto; text-align: center; color: var(--color-text-muted); }
.empty-icon { font-size: 44px; color: var(--color-primary); margin-bottom: 10px; }
.empty-state small { display: block; margin-top: 6px; font-size: 12px; }
.message-row { display: flex; gap: 12px; max-width: 86%; }
.message-row.user { flex-direction: row-reverse; align-self: flex-end; }
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
.message-row.user .bubble { background: linear-gradient(135deg, rgba(99, 102, 241, 0.26), rgba(139, 92, 246, 0.18)); border-color: rgba(99, 102, 241, 0.4); }
.message-row.assistant .bubble { background: rgba(15, 23, 42, 0.6); }
.typing { color: var(--color-text-muted); }
.typing .dot { animation: blink 1.2s infinite; }
.typing .dot:nth-child(2) { animation-delay: 0.2s; }
.typing .dot:nth-child(3) { animation-delay: 0.4s; }
@keyframes blink { 0%, 100% { opacity: 0.2; } 50% { opacity: 1; } }
.chat-input { flex-shrink: 0; padding: 14px 20px 18px; border-top: 1px solid var(--color-border); background: rgba(7, 11, 18, 0.4); }
.input-actions { display: flex; justify-content: flex-end; align-items: center; gap: 8px; margin-top: 10px; }
.drawer-list { min-height: 420px; }
@media (max-width: 900px) {
  .chat-page { padding: 14px 0 0; }
  .chat-layout { display: flex; padding-bottom: 16px; }
  .conversation-panel { display: none; }
  .chat-header { flex-direction: column; align-items: stretch; }
  .header-right { flex-wrap: wrap; }
  .mobile-history { display: inline-flex; }
  .model-select { width: 100%; flex: 1; }
  .message-row { max-width: 96%; }
}
</style>
