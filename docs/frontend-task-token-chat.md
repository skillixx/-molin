# 前端对接任务：用户端 AI 对话页（Token 网关）

> 负责人：前端工程师乙（web/user-console）
> 对应后端：Token 网关 v3（已上线 main）——用户端 `/api/token/*`
> 接口契约：`docs/backend-token-gateway-design.md` §5

## 一、背景（了解即可）

平台的「AI 对话」能力：用户登录后在对话页选模型、发消息，后端把请求转发到上游（OpenAI/DeepSeek/Kimi），**流式**返回。后端已就绪，本任务做用户控制台的对话页。

> 本期只做**文本对话（chat）**。生图/语音/视频是后续（模型 `modality` 字段已预留，先只展示/使用 `modality=chat` 的模型）。

## 二、接口契约（用户端，登录态）

### 1. 列出可用模型
`GET /api/token/models`（自动带 Bearer，走现有 http 实例即可）
返回（D-95 扁平分页）：
```json
{ "code":0, "data":{ "items":[
  { "logical_model_code":"gpt-4o", "display_name":"GPT-4o", "modality":"chat" },
  { "logical_model_code":"deepseek-chat", "display_name":"DeepSeek", "modality":"chat" }
], "page":1, "page_size":20, "total":2 } }
```
> 用 `items` 填模型下拉；本期可只取 `modality==='chat'` 的。

### 2. 对话（OpenAI 兼容，**流式 SSE**）
`POST /api/token/chat/completions`
请求体（兼容 OpenAI）：
```json
{
  "model": "gpt-4o",
  "messages": [
    { "role": "system", "content": "你是助手" },
    { "role": "user", "content": "你好" }
  ],
  "stream": true
}
```
**流式响应**：`Content-Type: text/event-stream`，逐行 `data: {...}`，每个 chunk 形如 OpenAI 增量：
```
data: {"choices":[{"delta":{"content":"你"}}]}
data: {"choices":[{"delta":{"content":"好"}}]}
data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}
data: [DONE]
```
- 逐 chunk 取 `choices[0].delta.content` 追加到当前回复气泡；
- 收到 `data: [DONE]` 结束；usage 在末尾 chunk（如需展示本轮消耗可读）。
- `stream:false` 时为一次性 JSON（`choices[0].message.content` + `usage`），本页**用流式**。

## 三、实现要点（关键技术约束）

1. **流式必须用 `fetch`，不要用 axios**（axios 不适合读 SSE 流）。`fetch('/api/token/chat/completions', {...})` + `response.body.getReader()` 逐块读、按行解析 `data:`。
2. **鉴权**：fetch 不走 http 拦截器，需**手动加头** `Authorization: 'Bearer ' + localStorage.getItem('access_token')`（与现有 http.ts 约定一致）；baseURL 前缀 `/api`。
3. **列模型**用现有 axios http 实例即可（GET，自动带 token）。

## 四、页面（web/user-console）

1. 新增 `src/api/token.ts`：`listModels()`（axios）+ `chatCompletionsStream({model,messages,onDelta,onDone,onError,signal})`（fetch+SSE）。
2. 新增对话页 `src/views/chat/ChatView.vue`（或 `token/`）：
   - 顶部模型下拉（来自 listModels）；
   - 消息列表（user/assistant 气泡），assistant 气泡随流式增量实时追加；
   - 底部输入框 + 发送；发送中可「停止」（abort fetch 的 AbortController）；
   - 加载/错误态。
3. 路由 + 顶部导航加入口（参考现有 views 的路由注册方式）。
4. 类型放 `src/types/`（或 token.ts 内）。

## 五、错误处理

| 情况 | 后端 | 前端提示 |
|---|---|---|
| 未登录/token 失效 | 401 | 走现有刷新/跳登录逻辑 |
| 未开通/无资格 | 403（门禁未通过：未购买 token 服务） | 「请先开通/购买 AI 服务」 |
| 余额不足 | 402/业务码 | 「余额不足，请充值」 |
| 上游错误 | 上游状态码原样透传 | 提示「模型服务异常，请重试」 |

> 具体错误体以实际返回为准；流式途中出错可能以 SSE 内的错误事件或非 2xx 起始响应出现，两种都要兜底。

## 六、不要做 / 边界
- 不做生图/语音/视频（本期只 chat）。
- 不做平台 API Key 管理页（本期网页对话登录态即可，sk 是后续）。
- 不碰后端、不碰 admin-console。
- 对话内容只在前端展示，不需本地持久化（除非你想加会话历史，可选，但别阻塞主流程）。

## 七、验收
- 选模型 → 发消息 → 助手回复**逐字流式**出现 → 结束；
- 未开通用户发消息 → 提示开通；
- 「停止」能中断流式。
