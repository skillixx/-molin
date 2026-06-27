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
          <div>
            <strong>历史会话</strong>
            <small>{{ conversations.length }} 条</small>
          </div>
          <el-button class="panel-new-button" type="primary" :icon="Plus" size="small" @click="startNewConversation">新建</el-button>
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
              <el-button class="conversation-action-button" :icon="Edit" text size="small" @click="handleRename(conversation)" />
              <el-button class="conversation-action-button danger" :icon="Delete" text size="small" type="danger" @click="handleDelete(conversation)" />
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
            <el-button class="chat-toolbar-button mobile-history" :icon="Menu" @click="drawerVisible = true">历史</el-button>
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
            <el-button class="chat-toolbar-button" :icon="Refresh" :loading="modelsLoading" @click="fetchModels">刷新</el-button>
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
              <el-button class="composer-secondary-button" text :disabled="streaming" @click="startNewConversation">新建会话</el-button>
              <el-button v-if="streaming" class="composer-stop-button" type="danger" :icon="VideoPause" @click="handleStop">停止</el-button>
              <el-button v-else class="composer-send-button" type="primary" :icon="Promotion" :disabled="!canSend" @click="handleSend">发送</el-button>
            </div>
          </div>
        </section>
      </div>
    </div>

    <el-drawer v-model="drawerVisible" title="会话历史" size="82%" class="conversation-drawer">
      <div class="panel-header drawer-header">
        <div>
          <strong>历史会话</strong>
          <small>{{ conversations.length }} 条</small>
        </div>
        <el-button class="panel-new-button" type="primary" :icon="Plus" size="small" @click="startNewConversation">新建</el-button>
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
            <el-button class="conversation-action-button" :icon="Edit" text size="small" @click="handleRename(conversation)" />
            <el-button class="conversation-action-button danger" :icon="Delete" text size="small" type="danger" @click="handleDelete(conversation)" />
          </span>
        </button>
      </div>
    </el-drawer>
  </div>
</template>

<style scoped>
.chat-page {
  height: calc(100vh - 64px);
  overflow: hidden;
  padding: 18px 0 0;
}

.chat-layout {
  display: grid;
  grid-template-columns: 304px minmax(0, 1fr);
  gap: 14px;
  height: 100%;
  padding-bottom: 18px;
}

.conversation-panel {
  display: flex;
  min-height: 0;
  flex-direction: column;
  overflow: hidden;
  padding: 12px;
  background:
    linear-gradient(180deg, rgba(15, 23, 42, 0.92), rgba(8, 13, 22, 0.86));
}

.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 12px;
  padding: 2px 2px 4px;
}

.panel-header strong {
  display: block;
  color: var(--color-text);
  font-size: 15px;
}

.panel-header small {
  display: block;
  margin-top: 2px;
  color: var(--color-text-disabled);
  font-size: 12px;
}

.panel-new-button,
.chat-toolbar-button,
.composer-secondary-button,
.composer-stop-button,
.composer-send-button {
  border-radius: 8px;
  font-weight: 700;
}

.panel-new-button {
  height: 32px;
  border: 0;
  background: linear-gradient(135deg, #22d3ee, #34d399);
  color: #04111d;
  box-shadow: 0 10px 20px rgba(34, 211, 238, 0.18);
}

.panel-new-button:hover,
.composer-send-button:hover {
  filter: brightness(1.05);
  transform: translateY(-1px);
}

.chat-toolbar-button {
  height: 36px;
  border-color: rgba(148, 163, 184, 0.18);
  background: rgba(15, 23, 42, 0.66);
  color: var(--color-text);
}

.chat-toolbar-button:hover {
  border-color: rgba(34, 211, 238, 0.44);
  background: rgba(34, 211, 238, 0.12);
  color: var(--color-primary);
}

.conversation-list {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  gap: 8px;
  overflow-y: auto;
  padding-right: 2px;
}

.conversation-item {
  position: relative;
  width: 100%;
  min-height: 68px;
  border: 1px solid rgba(148, 163, 184, 0.14);
  border-radius: 8px;
  background: rgba(15, 23, 42, 0.48);
  color: var(--color-text);
  cursor: pointer;
  padding: 12px 74px 12px 12px;
  text-align: left;
  transition: background 0.18s ease, border-color 0.18s ease, transform 0.18s ease;
}

.conversation-item:hover {
  border-color: rgba(34, 211, 238, 0.38);
  background: rgba(20, 31, 47, 0.72);
  transform: translateY(-1px);
}

.conversation-item.active {
  border-color: rgba(34, 211, 238, 0.72);
  background: rgba(34, 211, 238, 0.13);
}

.conversation-item.active::before {
  position: absolute;
  top: 12px;
  bottom: 12px;
  left: 0;
  width: 3px;
  border-radius: 0 4px 4px 0;
  background: var(--color-primary);
  content: "";
}

.conversation-title {
  display: block;
  overflow: hidden;
  color: var(--color-text);
  font-size: 14px;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.conversation-item small {
  display: block;
  margin-top: 7px;
  color: var(--color-text-muted);
  font-size: 12px;
}

.conversation-actions {
  position: absolute;
  top: 8px;
  right: 5px;
  display: flex;
  gap: 0;
  opacity: 0.72;
  transition: opacity 0.18s ease;
}

.conversation-item:hover .conversation-actions,
.conversation-item.active .conversation-actions {
  opacity: 1;
}

.conversation-action-button {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  color: var(--color-text-muted);
}

.conversation-action-button:hover {
  background: rgba(34, 211, 238, 0.12);
  color: var(--color-primary);
}

.conversation-action-button.danger:hover {
  background: rgba(239, 68, 68, 0.12);
  color: var(--color-danger);
}

.panel-empty {
  margin: auto;
  color: var(--color-text-muted);
  font-size: 13px;
}

.chat-main {
  display: flex;
  min-width: 0;
  height: 100%;
  flex-direction: column;
  gap: 12px;
}

.chat-header {
  display: flex;
  flex-shrink: 0;
  align-items: flex-end;
  justify-content: space-between;
  gap: 16px;
  min-height: 104px;
  padding: 18px 22px;
  background:
    linear-gradient(135deg, rgba(15, 23, 42, 0.94), rgba(10, 16, 27, 0.84));
}

.page-kicker {
  color: var(--color-accent);
  font-size: 12px;
  font-weight: 800;
}

.page-title {
  margin: 7px 0 5px;
  color: var(--color-text);
  font-size: 24px;
  line-height: 1.25;
}

.page-subtitle {
  max-width: 560px;
  color: var(--color-text-muted);
  line-height: 1.6;
}

.header-right {
  display: flex;
  align-items: center;
  gap: 10px;
}

.model-select {
  width: 220px;
}

.mobile-history {
  display: none;
}

.chat-body {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  overflow: hidden;
  padding: 0;
  background: linear-gradient(180deg, rgba(10, 15, 25, 0.82), rgba(7, 11, 18, 0.94));
}

.message-list {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  gap: 18px;
  overflow-y: auto;
  padding: 24px 28px 18px;
}

.empty-state {
  margin: auto;
  color: var(--color-text-muted);
  text-align: center;
}

.empty-icon {
  margin-bottom: 10px;
  color: var(--color-primary);
  font-size: 44px;
}

.empty-state p {
  color: var(--color-text);
  font-weight: 700;
}

.empty-state small {
  display: block;
  margin-top: 6px;
  font-size: 12px;
}

.message-row {
  display: flex;
  max-width: min(780px, 82%);
  gap: 12px;
}

.message-row.user {
  align-self: flex-end;
  flex-direction: row-reverse;
}

.avatar {
  display: grid;
  width: 34px;
  height: 34px;
  flex-shrink: 0;
  place-items: center;
  border: 1px solid rgba(148, 163, 184, 0.18);
  border-radius: 50%;
  color: #fff;
  font-size: 12px;
  font-weight: 800;
}

.avatar.user {
  background: var(--gradient-primary);
}

.avatar.assistant {
  background: rgba(34, 211, 238, 0.14);
  color: var(--color-accent);
}

.bubble {
  border: 1px solid rgba(148, 163, 184, 0.16);
  border-radius: 8px;
  box-shadow: 0 14px 30px rgba(0, 0, 0, 0.16);
  color: var(--color-text);
  line-height: 1.75;
  padding: 13px 16px;
  white-space: pre-wrap;
  word-break: break-word;
}

.message-row.user .bubble {
  border-color: rgba(34, 211, 238, 0.34);
  background: linear-gradient(135deg, rgba(14, 116, 144, 0.42), rgba(20, 184, 166, 0.18));
}

.message-row.assistant .bubble {
  background: rgba(15, 23, 42, 0.72);
}

.typing {
  color: var(--color-text-muted);
}

.typing .dot {
  animation: blink 1.2s infinite;
}

.typing .dot:nth-child(2) {
  animation-delay: 0.2s;
}

.typing .dot:nth-child(3) {
  animation-delay: 0.4s;
}

@keyframes blink {
  0%, 100% { opacity: 0.2; }
  50% { opacity: 1; }
}

.chat-input {
  flex-shrink: 0;
  border-top: 1px solid rgba(148, 163, 184, 0.16);
  background: rgba(5, 9, 15, 0.72);
  padding: 14px 20px 18px;
}

.chat-input :deep(.el-textarea__inner) {
  border-radius: 8px;
  line-height: 1.7;
}

.input-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 10px;
}

.composer-secondary-button {
  height: 38px;
  color: var(--color-text-muted);
}

.composer-secondary-button:hover {
  background: rgba(148, 163, 184, 0.1);
  color: var(--color-text);
}

.composer-send-button,
.composer-stop-button {
  min-width: 94px;
  height: 38px;
}

.composer-send-button {
  border: 0;
  background: linear-gradient(135deg, #22d3ee, #34d399);
  color: #04111d;
  box-shadow: 0 12px 24px rgba(34, 211, 238, 0.2);
}

.composer-send-button.is-disabled {
  background: rgba(148, 163, 184, 0.18);
  box-shadow: none;
  color: var(--color-text-disabled);
}

.composer-stop-button {
  border-color: rgba(239, 68, 68, 0.38);
  background: rgba(239, 68, 68, 0.14);
  color: #fecaca;
}

.drawer-list {
  min-height: 420px;
}

:global(.conversation-drawer) {
  background: #0b1220 !important;
  color: var(--color-text);
}

:global(.conversation-drawer .el-drawer__header) {
  margin-bottom: 0;
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text);
  padding: 18px 20px;
}

:global(.conversation-drawer .el-drawer__title),
:global(.conversation-drawer .el-drawer__close-btn) {
  color: var(--color-text);
}

:global(.conversation-drawer .el-drawer__body) {
  padding: 16px;
}

@media (max-width: 900px) {
  .chat-page {
    padding: 12px 0 0;
  }

  .chat-layout {
    display: flex;
    padding-bottom: 12px;
  }

  .conversation-panel {
    display: none;
  }

  .chat-main {
    width: 100%;
  }

  .chat-header {
    align-items: stretch;
    min-height: auto;
    flex-direction: column;
    padding: 16px;
  }

  .header-right {
    flex-wrap: wrap;
  }

  .mobile-history {
    display: inline-flex;
  }

  .model-select {
    width: 100%;
    flex: 1;
  }

  .message-list {
    padding: 18px 14px;
  }

  .message-row {
    max-width: 96%;
  }

  .chat-input {
    padding: 12px;
  }
}
</style>
