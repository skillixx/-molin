# 前端对接任务清单 —— 聊天接入有状态会话（记忆/历史/新建会话）

> 负责：**前端工程师乙（Codex）**，目标项目 `web/user-console`。
> 后端已完成并合并：会话持久化（MySQL）+ Redis 热缓存（PR #283、#284，已在 main）。
> 接口契约 SSOT：`docs/frontend-conversation-persistence.md`（本清单只列「改哪、怎么改」，字段以契约为准）。
> 制定日期：2026-06-27。**本文件不含页面实现代码**，由前端按清单落地。

---

## 0. 现状与目标

**现状（问题）**：聊天历史只在前端内存 `ref([])`，刷新即丢，模型记忆不连贯。
- `src/views/chat/ChatView.vue:24` `messages = ref<ChatMessage[]>([])`，发送时把**全量历史**自行拼进请求（`ChatView.vue:81-87`），走无状态端点 `/api/token/chat/completions`。
- `src/views/agent/AgentChatView.vue:32` 同样内存态，发送时拼 `history`（`AgentChatView.vue:77-80`），走无状态端点 `/api/agents/{id}/chat`。

**目标**：聊天改为「**新建会话 → 按会话发消息（只发新消息）→ 进会话拉历史**」，由后端落库 + 记忆。前端不再拼全量历史、不再依赖内存/ localStorage。

**关键认知**：新端点 `POST /api/conversations/{id}/chat` 的 SSE 用的是**与现有 Agent 对话一致的事件格式**（`tool_call` / `tool_result` / `message`），**不是 OpenAI delta 逐字流**。所以普通聊天页的「逐字追加」要改为「整条 `message` 落地」（见 §4）。

---

## 1. 新增 API 封装（前端实现，src/api/conversation.ts）

按下表封装（请求/响应字段见契约文档）。**SSE 流式函数可直接复用 `src/api/token.ts:190 agentChatStream` 的事件解析逻辑**（事件格式完全一致），仅替换 URL 与请求体。

| 函数（建议名） | 方法/路径 | 入参 | 备注 |
|---|---|---|---|
| `createConversation` | `POST /api/conversations` | `{ agent_id?, model_code?, title? }` | 普通聊天必传 `model_code`；Agent 会话传 `agent_id` |
| `listConversations` | `GET /api/conversations?type=&page=&page_size=` | `type: 'plain'\|'agent'\|''` | 扁平分页 `{items,page,page_size,total}`，按 `last_message_at` 倒序 |
| `getConversation` | `GET /api/conversations/{id}` | — | 会话元信息 |
| `listMessages` | `GET /api/conversations/{id}/messages?page=&page_size=` | — | 消息按 id 升序（最早→最新） |
| `renameConversation` | `PATCH /api/conversations/{id}` | `{ title }` | |
| `deleteConversation` | `DELETE /api/conversations/{id}` | — | 级联删消息 |
| `conversationChatStream` | `POST /api/conversations/{id}/chat` | `{ content, stream:true }` + 回调 | 复用 agentChatStream 的 SSE 解析；回调 `onToolCall/onToolResult/onMessage/onDone/onError` |

类型补充（src/types/conversation.ts）：`Conversation`、`ConversationMessage`（字段见契约 §3）。

---

## 2. 通用：会话生命周期约定

- **何时建会话**：用户点「新建会话」或在空会话首次发送时调用 `createConversation`，拿到 `id` 后再调 `conversationChatStream`。
  - 普通聊天：`createConversation({ model_code: 选中的模型 })`。
  - Agent 会话：`createConversation({ agent_id, model_code: 选中模型或留空用 Agent 默认 })`。
- **进入已有会话**：`getConversation(id)` 取元信息 + `listMessages(id)` 拉历史渲染气泡。
- **发送**：只发 `{ content }`，**删除前端拼全量 messages 的逻辑**，历史由后端重建。
- **会话标题**：首条消息后后端自动生成，前端刷新列表即可显示；支持 `renameConversation` 手动改名。

---

## 3. 改造 `views/chat/ChatView.vue`（普通聊天）

| 当前代码 | 改为 |
|---|---|
| `ChatView.vue:13` import `chatCompletionsStream` | 改 import `conversationChatStream` 等会话 API |
| `ChatView.vue:24` `messages` 纯内存 | 进入会话后由 `listMessages` 填充；保留响应式渲染 |
| `ChatView.vue:81-87` 拼全量 `payload` 后发送 | 删除拼接；改为：无活动会话则先 `createConversation({model_code: selectedModel})`，再 `conversationChatStream(convId, { content: text })` |
| `ChatView.vue:88-91` `onDelta` 逐字追加 | **改为 `onMessage`**：一次性把 `data.content` 写入当前 assistant 气泡（见 §4，逐字流取消） |
| `ChatView.vue:153-156` `handleClear` 清空内存 | 改为「新建会话」语义：清空当前视图 + 置空 activeConversationId（下次发送会新建） |
| 错误处理 `ChatView.vue:103-130` | 复用现有 status/code 分支即可；新增会话不存在(40400) 提示 |

新增：左侧/抽屉式**会话列表**（`listConversations('plain')`），支持切换、改名、删除、新建。

---

## 4. 普通聊天的 SSE 变化（重点，别踩坑）

- 旧：`/api/token/chat/completions` 是 OpenAI delta，逐 token 走 `onDelta`（`ChatView.vue:88`）。
- 新：`/api/conversations/{id}/chat` 是 workbench 事件流，最终答案一次性来一个 `event: message`，`data = { content, finish_reason }`，随后 `data: [DONE]`。
- 因此：assistant 气泡从「逐字增量」变为「**收到 message 事件后整条填充**」。「正在思考…」占位保留到 message 到达即可。
- 「停止」`AbortController` 仍可用（`ChatView.vue:139`），中断后该轮 assistant 可能无内容，按现有 `aborted` 分支处理。
- 逐 token 打字机效果当前后端不提供（编排需完整结果）；如产品强需要，记为后端后续增强项，不在本期。

---

## 5. 改造 `views/agent/AgentChatView.vue`（Agent 聊天）

改动比普通聊天小，因为 SSE 格式不变（本来就是 tool_call/tool_result/message）。

| 当前代码 | 改为 |
|---|---|
| `AgentChatView.vue:6` import `agentChatStream` | 改用 `conversationChatStream`（会话维度） |
| `AgentChatView.vue:77-95` 拼 `history` + `agentChatStream({agent_id, messages})` | 删除拼 history；首次发送前若无会话则 `createConversation({ agent_id, model_code: selectedModel })`，再 `conversationChatStream(convId, { content })` |
| `AgentChatView.vue:84-88` `onMessage` | 逻辑不变（含 `max_rounds` 提示），仍用 message 事件 |
| `AgentChatView.vue:153-157` `handleClear` | 改为「新建会话」语义 |
| 进入页面 `AgentChatView.vue:39-41` | 除 `getAgent`/`listModels` 外，进入指定会话时加载 `listMessages` 渲染历史 |
| 工具时间线 `tools`（`AgentChatView.vue:98-114`） | 保留，事件回调不变 |

新增：Agent 会话也接入**会话列表**（`listConversations('agent')`），或按 agent 维度展示该 Agent 下的历史会话。

---

## 6. 路由（src/router/index.ts）

现有：`/chat`（普通）、`/agents`（工作台）、`/agents/:id/chat`（Agent 对话，`router/index.ts:47-74`）。建议：

- `/chat` 与 `/agents/:id/chat` 内部用 `?conversation_id=` 或 path 段标识当前会话，便于刷新后定位会话并拉历史。
- 可选新增「会话不存在」兜底：进入带 id 的会话若 404，回落到新建。

> 路由具体形态由前端定，不强约束；只要满足「刷新能回到同一会话并恢复历史」。

---

## 7. 验收自查（报「完成」前必过）

按 `docs/frontend-definition-of-done.md` 五关卡，并特别核对本特性：

- [ ] 关卡0 契约对账：以最新 main 核对 `docs/frontend-conversation-persistence.md` 接口与字段，无遗漏（防「已合并后端 delta 未对接」）。
- [ ] 发消息后**刷新页面**，历史仍在（来自 `listMessages`），不依赖内存/localStorage。
- [ ] 多轮对话模型能记住上文（如先告知名字、后追问，能答对）——即 `{content}` 单发、后端重建上下文生效。
- [ ] 新建会话、切换会话、改名、删除均可用；删除后会话与消息均消失。
- [ ] 普通聊天 SSE 已从 delta 适配为 message 事件，无空气泡/重复内容。
- [ ] 错误码映射齐全：40400 会话不存在、40300 未开通/无模型、60001 余额不足、60005 套餐不足、50301 繁忙。
- [ ] 移动端（xs）会话列表用抽屉，气泡与输入区自适应（遵守 user-console 响应式规范）。

---

## 8. 边界与注意

- **旧端点保留**：`/api/token/chat/completions`、`/api/agents/{id}/chat`、`/v1/*` 不下线（外部工具用）；用户控制台聊天页改用会话端点即可，不要再用旧端点发用户对话。
- **本期未做**：会话列表分页的服务端缓存（后端直查 MySQL，前端正常分页即可）；逐 token 打字机流式。
- **用户隔离**由后端保证（按登录态 user_id），前端无需额外处理；越权访问他人会话后端返回 40400。
- 接口字段若与契约不符或缺失，**列出来反馈后端补**，不要前端自行兜逻辑（遵守前后端分工）。
