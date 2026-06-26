# 前端对接文档 —— 有状态会话（聊天记忆 / 上下文连贯 / 新建会话 / 用户隔离）

> 适用范围：`web/user-console`（前端工程师乙）。后端模块：`server/internal/modules/conversation`。
> 制定日期：2026-06-26。本文件为接口契约 + 对接说明，**不含页面实现**。

## 1. 背景与问题根因

旧版聊天（`ChatView.vue` 普通聊天、`AgentChatView.vue` Agent 聊天）的历史**只存在前端内存** `ref([])`，
后端编排端点 `POST /api/agents/{id}/chat` 与 `/v1` 兼容端点都是**无状态透传、不落库**。
因此刷新 / 切路由 / 重新登录后内存清空 → **聊天记录消失**；历史不完整回传 → **模型记忆不连贯**。

## 2. 新方案（ChatGPT 式有状态会话）

后端新增 `conversation` 模块，成为**会话与上下文的唯一真相源**：

- **聊天记忆 / 上下文连贯**：发消息只需带「会话 id + 新消息」，后端自动从库里重建上下文（摘要 + 近期消息）喂给模型，用户消息与模型回复**两端都落库**。
- **压缩上下文存储**：每个会话维护滚动摘要 `summary` + 水位线 `summarized_until_id`；累计 token 超阈值时后端**异步**调用模型把较早消息压成摘要，仅保留最近若干条原文 → token 可控、长期记忆不丢。**该过程对前端完全透明**，无需前端参与。
- **新建会话 / 用户隔离**：会话 CRUD，所有读写按登录用户强隔离（无法访问他人会话）。

> 旧端点 `POST /api/agents/{id}/chat`、`/v1/chat/completions` **保留不变**（供 Cline/Cherry Studio 等外部工具与无状态场景）。用户控制台聊天页请改用本文下方的会话端点。

## 3. 数据模型（响应字段参考）

会话对象 `Conversation`：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | number | 会话 id |
| `agent_id` | number \| null | null=普通聊天；非空=Agent 会话 |
| `title` | string | 会话标题（首条用户消息自动生成，可改） |
| `model_code` | string | 会话使用的逻辑模型 |
| `message_count` | number | 消息总数 |
| `last_message_at` | string \| null | 最后消息时间（列表按此倒序） |
| `created_at` / `updated_at` | string | 时间戳 |

> `summary` / `summarized_until_id` 为后端内部压缩字段，前端**无需关心、不要展示**。

消息对象 `Message`：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | number | 消息 id |
| `role` | string | `user` / `assistant`（`tool`/`system` 为内部，列表一般只渲染前两者） |
| `content` | string | 文本内容 |
| `created_at` | string | 时间戳 |

## 4. 接口清单（均需登录态 JWT；统一响应包 `{code,message,data}`）

### 4.1 新建会话 `POST /api/conversations`

请求体：
```json
{ "agent_id": 12, "model_code": "gpt-4o", "title": "" }
```
- Agent 会话：传 `agent_id`（会校验对当前用户可见）；`model_code` 可省略（用 Agent 默认模型）。
- 普通聊天：`agent_id` 省略 / 传 null，**必须**传 `model_code`（取自 `GET /api/token/models` 里 `modality==='chat'` 的项）。
- `title` 可空，首次发消息后自动以首句生成。

响应 `data`：完整 `Conversation` 对象（含 `id`）。

错误：`40000` 普通聊天未传 model_code；`40003` Agent 不可见；`40400` Agent 不存在。

### 4.2 会话列表 `GET /api/conversations?type=&page=&page_size=`

- `type`：`plain`（仅普通聊天）/ `agent`（仅 Agent 会话）/ 省略（全部）。
- 扁平分页（D-95）：`data = { items: Conversation[], page, page_size, total }`，按 `last_message_at` 倒序。

### 4.3 会话详情 `GET /api/conversations/{id}`

响应 `data`：`Conversation` 对象。用于进入会话时拿元信息。

### 4.4 会话消息（历史回看）`GET /api/conversations/{id}/messages?page=&page_size=`

- 扁平分页，`items` 按 **id 升序（最早→最新）**。进入会话时拉取以渲染历史气泡。
- 越权访问他人会话 → `40400`。

### 4.5 重命名 `PATCH /api/conversations/{id}`
```json
{ "title": "新标题" }
```
响应 `data`：`{ id, title }`。

### 4.6 删除会话 `DELETE /api/conversations/{id}`

级联删除该会话所有消息。响应 `data`：`{ id }`。

### 4.7 发消息（有记忆对话）`POST /api/conversations/{id}/chat`

请求体：
```json
{ "content": "你好，记住我叫张三", "stream": true }
```
- 只需发**本轮新消息** `content`，**不要**再拼历史——历史由后端重建。
- `stream: true` → SSE；`false` → 一次性 JSON。

**SSE 事件格式**（与现有 `AgentChatView` 一致，非 OpenAI delta 格式）：

```
event: tool_call      data: {"name":"...","arguments":"..."}   // Agent 调工具时（可选，多次）
event: tool_result    data: {"name":"...","content":"..."}     // 工具结果（可选，多次）
event: message        data: {"content":"完整回复文本","finish_reason":"stop"}
data: [DONE]
event: error          data: {"message":"错误说明"}              // 上游/计费失败时
```

> 注意：`message` 事件一次性给出**完整回复**（编排循环需要完整结果），非逐 token 流式。
> 逐 token 打字机效果为后续增强项，当前不提供。

**非流式 JSON**（`stream:false`）：
```json
{ "code":0, "message":"ok", "data": { "choices":[ { "message":{"role":"assistant","content":"..."}, "finish_reason":"stop" } ] } }
```

错误码（对话前置失败，未开始写出时走标准 JSON 错误）：`40400` 会话不存在；`40003` Agent 不可见 / 额度归属不符；`40300` 无可用模型 / 未开通 token；`60001` 钱包余额不足；`60005` 套餐额度不足；`50300/50301/50200` 上游/系统异常。

## 5. 前端改造建议（落到 user-console）

1. **聊天页加「会话侧栏」**：调 `4.2` 列出会话，支持新建（`4.1`）、切换、重命名（`4.5`）、删除（`4.6`）。
2. **进入会话**：调 `4.4` 拉历史消息渲染气泡（替代旧的内存 `ref([])`）。
3. **发送**：改调 `4.7`，请求体只带 `content`（+`stream`），**移除前端拼接全量 `messages` 的逻辑**。
4. **普通聊天页**（`ChatView.vue`）：新建会话时 `agent_id` 省略、带选中的 `model_code`；SSE 解析从 OpenAI delta 改为上面的 `message` 事件格式。
5. **Agent 聊天页**（`AgentChatView.vue`）：新建会话时带 `agent_id`；SSE 格式与现状一致，主要改为「先建会话→按会话发消息→进会话拉历史」。
6. 刷新/重登后聊天记录与记忆均从后端恢复，前端不再需要 localStorage。

## 6. 数据库迁移

新增迁移 `000053_create_chat_conversations`（`chat_conversations` + `chat_messages` 两表）。
测试环境部署需执行 `./scripts/migrate.sh up` 后重启 API。

## 7. 安全说明

- 所有会话/消息读写带 `user_id` 强隔离，杜绝越权读取他人聊天。
- 聊天正文以明文存于库中（这是「记忆」功能的固有要求；安全约定中的「不落明文日志」针对日志，与本存储无关）。如需静态加密可作为后续增强。
