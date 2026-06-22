# M3 接口手动测试文档（apiPost 用）

> 范围：第二阶段 M3（多模型聊天工作台：Agent / Skill / 插件 CRUD + tool-use 编排）已完成接口。
> 用途：在 apiPost / Postman 里手动测试。所有 JSON 请求统一返回 `{code,message,data}`，`code=0` 为成功；编排端点 `stream=true` 时走 SSE（非 `{code,message,data}` 包裹，见 §4）。
> 关系：M1（按量/按次 postpaid + sk）→ `m1-manual-test-apipost.md`；M2（套餐预付 prepaid）→ `m2-manual-test-apipost.md`；本文聚焦 **M3 新增能力**。
> 契约：`backend-chat-workbench-contract.md`、`frontend-api-reference.md` §14.8/14.9/14.10。
> 日期：2026-06-22 ｜ 对应测试服 main `426be7f`（DB schema 000044）

---

## 0. M3 在测什么（先读这段）

M3 = **站内多模型聊天工作台**，三层能力**全部免费**，唯一收费点仍是模型 token 调用（复用 M1/M2 计费）：

| 层 | 含义 | 谁能配 | 计费 |
|---|---|---|---|
| **Agent**（=角色/人设） | system_prompt + 默认模型 + 绑定 skill/插件 | 运营建官方；用户可自建 | 免费 |
| **Skill**（平台内置能力） | 联网搜索 / 读文档（门面内置函数，D4 不含 code_exec） | **仅运营** | 免费 |
| **插件**（外部第三方） | 外部 HTTP 工具 | **仅运营** | 免费（付费插件成本平台担，按 daily_limit 限量） |

**本文要验的 5 条主线**：
1. 运营建 Skill / 插件 / 官方 Agent（含绑定），**插件凭证不回响应**（只回 `has_auth`）
2. 用户选用官方 Agent / 自建 Agent（绑 active 官方 skill/插件）；**改删他人 Agent → 40003**
3. 编排对话 `POST /api/agents/{id}/chat`：注入人设+工具→工具循环→SSE 返回；**仅登录态（sk 不可调，D2）**
4. 编排计费：**每轮计 token、整次提问按次计 1**；Agent/skill/插件零计费
5. 安全：插件凭证不外泄、SSRF 防护（https + 拒内网 + DNS 解析 + 白名单）、超 `MAX_ROUNDS` 安全终止、付费插件日上限

---

## 1. 环境与连接

### apiPost 环境变量

| 变量 | 示例值 | 说明 |
|---|---|---|
| `base_url` | `http://localhost:8080` | API 地址（连接方式同 M1 文档 §0） |
| `token` | （登录后填） | 普通用户登录态 JWT（用户端 §3 + 编排 §4） |
| `token_b` | （登录后填） | 第二个用户的 JWT（验越权 §3.6 用） |
| `admin_token` | （登录后填） | 管理员 JWT，**且已完成二次验证**（管理端 §2 用） |
| `sk_postpaid` | （M1 签发） | 平台 sk 明文（验编排端点拒 sk §4.3 用） |
| `skill_id` | （建后填） | 测试 Skill ID |
| `plugin_id` | （建后填） | 测试插件 ID |
| `agent_id` | （建后填） | 官方 Agent ID |
| `my_agent_id` | （自建后填） | 用户自建 Agent ID |

### 连接 / 请求头约定

- 连接方式（本地起后端 / SSH 隧道连测试服）见 `m1-manual-test-apipost.md` §0。
- 用户端/编排端：`Authorization: Bearer {{token}}`。
- **管理端（§2）双重认证**：除 `Bearer {{admin_token}}` 外，管理员须先完成**二次验证**（管理员双重认证流程见 `api-test-guide-backend-a.md` / admin-console 登录流程）；未通过二次验证调管理端 → 被 `RequireAdminVerified` 拒。账号还须具备对应权限码（`agent:manage` / `skill:manage` / `plugin:manage`，迁移 000043 已 seed 给 admin 角色）。
- **编排前置（§4）**：登录态调编排端点走 **postpaid（钱包）**，故测试用户须**持有 active token 服务资产**且**钱包有余额**（同 M1 §2 前置）；测试服 DeepSeek 渠道/模型已配置可用。

---

## 2. 管理端：运营配置官方资源

> 图例：🛡️ 需 `Bearer {{admin_token}}` + 二次验证 + 对应权限码 ｜ 🔑 需 `Bearer {{token}}`（登录态）

> 列表统一**扁平分页** `data.{items,page,page_size,total}`（D-95）。错误码：`40000` 校验 / `40900` code 已存在 / `40400` 不存在 / `40003` 越权 / `403` 无权限 / `401` 未登录。

### 2.1 建 Skill 🛡️（`skill:manage`）
- **POST** `{{base_url}}/api/admin/skills`
- Body（`tool_schema_json` 为 OpenAI function 工具定义对象；`handler_key` 决定门面派发到哪个内置函数，本期支持 `doc_read` / `web_search`）：
  ```json
  {
    "code": "doc_read",
    "name": "读取文档",
    "description": "抓取一个 https 网页并返回文本",
    "category": "web",
    "handler_key": "doc_read",
    "tool_schema_json": {
      "type": "function",
      "function": {
        "name": "doc_read",
        "description": "读取指定 https 网址的文档内容",
        "parameters": {
          "type": "object",
          "properties": {
            "url": { "type": "string", "description": "https 文档地址" },
            "max_chars": { "type": "integer", "description": "最多返回字符数" }
          },
          "required": ["url"]
        }
      }
    }
  }
  ```
- 预期：`code=0`，`data` 回完整 Skill（含 `id`、`status=active`、`tool_schema_json`、`handler_key`）→ 记 `id` 填 `skill_id`。
- 负向：`code` 重复 → `40900`；`tool_schema_json` 非合法 JSON → `40000`；缺 `handler_key` → `40000`。
- **断言**：`tool_schema_json.function.name`（这里 `doc_read`）即编排时模型可调用的工具名；要能被门面派发，`handler_key` 须为内置支持值（`doc_read` 真实抓取；`web_search` 为占位，调用会优雅降级返回"未配置"）。

### 2.2 建插件 🛡️（`plugin:manage`）
- **POST** `{{base_url}}/api/admin/plugins`
- Body（`auth_config` 为**明文**鉴权配置，形如 `{"header":"...","value":"..."}`，入参加密落库后**永不回显**；`endpoint_url` 必须 https 且非内网）：
  ```json
  {
    "code": "weather",
    "name": "天气查询",
    "description": "查询城市天气",
    "endpoint_url": "https://api.example.com/weather",
    "auth_config": "{\"header\":\"Authorization\",\"value\":\"Bearer demo-secret-123\"}",
    "timeout_ms": 8000,
    "is_paid": false,
    "daily_limit": 0,
    "tool_schema_json": {
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "查询某城市天气",
        "parameters": {
          "type": "object",
          "properties": { "city": { "type": "string" } },
          "required": ["city"]
        }
      }
    }
  }
  ```
- 预期：`code=0`，`data` 含 `id`、`endpoint_url`、`timeout_ms`、`is_paid`、`daily_limit`、**`has_auth: true`** → 记 `id` 填 `plugin_id`。
- **断言（安全红线）**：响应**不含** `auth_config` / `auth_config_encrypted` 字段，不含任何凭证明文，仅以 `has_auth` 表征是否已配置鉴权。
- 负向（SSRF 配置时拦截，均 `40000`）：
  - `endpoint_url` 非 https：`"http://api.example.com"` → `40000`
  - 指向内网/回环：`"https://127.0.0.1/x"`、`"https://10.0.0.1/x"`、`"https://192.168.1.1/x"`、`"https://localhost/x"`、`"https://169.254.169.254/meta"` → `40000`
  - `timeout_ms` > 30000 → `40000`
  - `code` 重复 → `40900`

### 2.3 建官方 Agent + 绑定 skill/插件 🛡️（`agent:manage`）
- **POST** `{{base_url}}/api/admin/agents`
- Body（`skill_ids` / `plugin_ids` 一次建好绑定；`default_model_code` 须为已上架逻辑模型，如测试服 `DeepSeek`）：
  ```json
  {
    "code": "news-helper",
    "name": "资讯助手",
    "description": "会查文档的助手",
    "avatar": "",
    "system_prompt": "你是资讯助手，必要时调用工具查资料后再回答。",
    "default_model_code": "DeepSeek",
    "status": "active",
    "sort_order": 0,
    "skill_ids": [ {{skill_id}} ],
    "plugin_ids": []
  }
  ```
- 预期：`code=0`，`data` 含 `id`、`owner_type=official`、`skills:[{id,code,name}]`、`plugins:[]` → 记 `id` 填 `agent_id`。
- 负向：绑定不存在的 skill/plugin id → `40000`「skill/plugin 含不存在的 ID」；缺 `name`/`system_prompt`/`default_model_code` → `40000`；`code` 重复 → `40900`。

### 2.4 绑定/解绑（覆盖语义）🛡️（`agent:manage`）
- **POST** `{{base_url}}/api/admin/agents/{{agent_id}}/skills` Body：`{ "ids": [ {{skill_id}} ] }`
- **POST** `{{base_url}}/api/admin/agents/{{agent_id}}/plugins` Body：`{ "ids": [ {{plugin_id}} ] }`
- 预期：`code=0`，返回更新后的 Agent 详情。**覆盖语义**：传的 `ids` 即最终全量绑定；传 `{ "ids": [] }` = **全部解绑**该类。
- 断言：`data.skills` / `data.plugins` 与传入 `ids` 一致。

### 2.5 列表 / 查 / 改 / 删 🛡️
- **GET** `{{base_url}}/api/admin/skills?status=active&category=web&page=1&page_size=20` → 扁平分页
- **GET** `{{base_url}}/api/admin/plugins?status=active` → 扁平分页（含 `has_auth`，无凭证）
- **GET** `{{base_url}}/api/admin/agents?owner_type=official&status=active` → 扁平分页
- **GET** `{{base_url}}/api/admin/agents/{{agent_id}}` → 详情
- **PATCH** `{{base_url}}/api/admin/skills/{{skill_id}}` Body：`{ "description": "改一下" }`（标量缺省不改；`tool_schema_json` 传则覆盖）
- **PATCH** `{{base_url}}/api/admin/plugins/{{plugin_id}}` Body：`{ "auth_config": "" }`（传空串 = **清空凭证**，再查 `has_auth=false`）
- **PATCH** `{{base_url}}/api/admin/agents/{{agent_id}}` Body：`{ "status": "inactive" }`
- **DELETE** `{{base_url}}/api/admin/skills/{{skill_id}}` → `{ "deleted": true }`（删 Agent 时绑定关系 FK 级联清理）

### 2.6 权限/鉴权负向 🔑
- 用**无对应权限码**的账号调任一 `/api/admin/{agents,skills,plugins}` → `403`。
- 不带 token → `401`。
- 管理员**未完成二次验证** → 被 `RequireAdminVerified` 拒。

---

## 3. 用户端：选用 + 自建 Agent

> 全部 🔑 `Bearer {{token}}`（仅登录态）。

### 3.1 列可用 Agent 🔑
- **GET** `{{base_url}}/api/agents?page=1&page_size=20`
- 预期：`data.items` 含**官方 active**（§2.3 建的，需先把 §2.5 改回 `status=active`）+ **本人自建**；元素结构同详情（`id,code,name,owner_type,system_prompt,default_model_code,status,skills[],plugins[]`）。

### 3.2 查 Agent 详情 🔑
- **GET** `{{base_url}}/api/agents/{{agent_id}}`
- 预期：详情含绑定 `skills:[{id,code,name}]` / `plugins:[{id,code,name}]`，**不含插件凭证/endpoint**。
- 可见性：仅官方 active 或本人自建可查，否则 `40003`。

### 3.3 自建 Agent 🔑
- **POST** `{{base_url}}/api/agents`（`owner_type` 强制 `user`，**不可传 code**）
- Body：
  ```json
  {
    "name": "我的小助手",
    "description": "自建测试",
    "avatar": "",
    "system_prompt": "你是我的私人助手。",
    "default_model_code": "DeepSeek",
    "skill_ids": [ {{skill_id}} ],
    "plugin_ids": []
  }
  ```
- 预期：HTTP 201，`data` 含 `owner_type=user`、`owner_user_id`=当前用户 → 记 `id` 填 `my_agent_id`。
- 负向（自建只能绑 **active 官方** skill/插件）：`skill_ids` 含 **inactive** 或不存在的 id → `40000`「含未上架（非 active）项 / 含不存在的 ID」。
- 红线：用户端**没有**建/改 skill/插件的接口（外部接入须运营审核上架）。

### 3.4 改/删本人自建 🔑
- **PATCH** `{{base_url}}/api/agents/{{my_agent_id}}` Body：`{ "name": "改个名", "skill_ids": [] }`（`skill_ids`/`plugin_ids` 传则覆盖，传 `[]` 清空，不传保留）
- **DELETE** `{{base_url}}/api/agents/{{my_agent_id}}` → `{ "deleted": true }`

### 3.5 可绑定能力只读列表 🔑
- **GET** `{{base_url}}/api/skills` → active skill 精简视图 `{id,code,name,description,category}`（**不回 handler_key**）
- **GET** `{{base_url}}/api/plugins` → active 插件精简视图 `{id,code,name,description,is_paid}`（**不回 endpoint/凭证/配额**）

### 3.6 越权（负向，核心）🔑
- 用 **用户 B 的 `{{token_b}}`** 操作 **用户 A 自建的 `{{my_agent_id}}`**：
  - **GET** `/api/agents/{{my_agent_id}}` → `40003`
  - **PATCH** `/api/agents/{{my_agent_id}}` → `40003`
  - **DELETE** `/api/agents/{{my_agent_id}}` → `40003`
- 用普通用户改/删**官方 Agent** → `40003`（官方对用户只读）。

---

## 4. 编排对话 `POST /api/agents/{id}/chat`（核心）

> 🔑 **仅登录态**（D2 已拍板：sk 不可调本端点）。`stream=true` 走 SSE；`stream=false` 返回单条 JSON。
> 前置：用户持有 active token 资产 + 钱包有余额（登录态走 postpaid）；Agent 需可见（官方 active 或本人自建）。

### 4.1 流式对话（SSE）🔑
- **POST** `{{base_url}}/api/agents/{{agent_id}}/chat`
- Headers：`Authorization: Bearer {{token}}`，`Content-Type: application/json`（apiPost 里建议关闭"格式化"以看原始 SSE 流）
- Body（`messages` 客户端自持完整历史，后端不落对话内容；`model` 可选，缺省用 Agent 默认模型）：
  ```json
  {
    "messages": [ { "role": "user", "content": "读取 https://example.com 并用一句话总结" } ],
    "stream": true
  }
  ```
- 预期：HTTP 200，`Content-Type: text/event-stream`，事件序列：
  ```
  event: tool_call
  data: {"name":"doc_read","arguments":"{\"url\":\"https://example.com\"}"}

  event: tool_result
  data: {"name":"doc_read","content":"<抓取到的文本或失败说明>"}

  event: message
  data: {"content":"<最终答案>","finish_reason":"stop"}

  data: [DONE]
  ```
- **断言**：
  1. 模型若决定用工具 → 先出 `tool_call` + `tool_result`，再出 `message`；不用工具则直接 `message`。
  2. 末尾固定 `data: [DONE]`。
  3. 工具失败（如 `web_search` 占位、或 doc_read 抓取失败）→ `tool_result.content` 为「工具执行失败: …」，但**对话不中断**，模型据此继续答。

### 4.2 非流式对话 🔑
- 同上，Body 改 `"stream": false`。
- 预期：单条 JSON：
  ```json
  { "choices": [ { "message": { "role": "assistant", "content": "..." }, "finish_reason": "stop" } ] }
  ```
  （中间工具事件不下发。）

### 4.3 D2 边界：sk 不可调编排端点（负向，核心）🆔
- 用 **`Bearer {{sk_postpaid}}`**（平台 sk）调 `POST /api/agents/{{agent_id}}/chat`：
- 预期：**`401`**（编排端点只挂登录态 JWT 校验，不认 sk）。外部程序/sk 只能用透传端点 `POST /api/token/chat/completions`（见 M1）。

### 4.4 MAX_ROUNDS 安全终止 🔑
- 构造一个让模型反复要求调工具、始终不收敛的场景（或临时把 `MAX_ROUNDS` 调小由运维配合）：
- 预期：到达上限（默认 5 轮）后发 `event: message`，`finish_reason=max_rounds`，文案含「工具调用已达上限…已正常计费」，随后 `[DONE]`。
- 断言：不会无限循环；已消耗 token 已计费（不退）。

### 4.5 编排计费正确性（核心红线）🔑
- 对话前记录基线：`GET /api/wallet` 余额、`GET /api/token/usage`。
- 跑一次**触发工具的多轮**对话（如 §4.1，产生 ≥2 轮上游调用）。
- 断言：
  1. `GET /api/token/usage`：本次提问对应 **多条** token 用量记录（每轮一条，`request_id` 形如 `<reqid>:r1`、`:r2`），各轮均计 input/output token。
  2. **按次计费 calls 只 1 次**（整次提问计 1，非每轮各计）——可由运维查 `product_consumption_records`（`usage_type=calls` 仅 1 条）确认。
  3. `GET /api/wallet`：钱包净扣 = 各轮实扣之和；Agent/Skill/插件**零计费**（免费）。

### 4.6 付费插件每日上限（可选，需 is_paid 插件）🔑
- 运营建一个 `is_paid=true, daily_limit=1` 的插件并绑到 Agent（§2.2/§2.4）。
- 同一用户当天触发该插件 **2 次**：
- 预期：第 2 次该插件的 `tool_result.content` 为「该付费插件今日调用次数已达上限」，**对话不中断**（模型据此降级回答）。

### 4.7 编排前置错误（负向）🔑
- 未持有 token 资产 / 无可用模型 → `40300`。
- 钱包余额不足（postpaid 预扣失败）→ `60001`（HTTP 402）。
- Agent 不存在 → `40400`；不可见（他人私有）→ `40003`。
- 上游失败 → `50200`；渠道不可用 → `50300`；系统繁忙可重试 → `50301`。
  （注：这些 HTTP 错误码仅在 SSE **尚未开始**时返回；一旦已开始流式，中途错误改走 `event: error` + `[DONE]`。）

---

## 5. 安全断言速查（必过）

| 红线 | 验法 | 期望 |
|---|---|---|
| 插件凭证不外泄 | §2.2 建插件响应 / §2.5 查插件 / §3.5 用户列插件 | 任何响应均无 `auth_config`/凭证明文，仅 `has_auth` |
| SSRF（配置时） | §2.2 endpoint 非 https / 内网 / 回环 | 一律 `40000` |
| SSRF（运行时） | §4.1 让 doc_read 传内网 URL（`http://127.0.0.1` 等） | `tool_result` 为安全拒绝，不外呼内网 |
| sk 不可调编排 | §4.3 | `401` |
| 越权改删 Agent | §3.6 | `40003` |
| 工具循环不失控 | §4.4 | 达 MAX_ROUNDS 终止 + 已计费 |
| 用户不能建 skill/插件 | §3 无对应接口 | N/A（接口不存在） |

---

## 6. 推荐测试顺序（端到端串一遍）

```
准备：admin 登录+二次验证 → admin_token；普通用户 A/B 登录 → token/token_b；用户 A 备好 token 资产+钱包余额
2.1 建 Skill(doc_read) → skill_id
2.2 建插件(has_auth=true，验凭证不回 + SSRF 负向) → plugin_id
2.3 建官方 Agent 绑 skill → agent_id
2.4 绑定/解绑覆盖语义
2.5 列/查/改/删（改 auth_config="" 验 has_auth=false）
2.6 权限/鉴权负向（403/401/未二次验证）
3.1-3.2 用户列/查可用 Agent
3.3 自建 Agent（绑 active 官方 skill；inactive→40000）→ my_agent_id
3.4 改/删本人自建
3.5 列可绑定 skills/plugins（不回敏感字段）
3.6 越权：B 操作 A 的 Agent → 40003
4.1 流式编排对话（SSE 事件齐全）
4.2 非流式编排
4.3 sk 调编排 → 401（D2）
4.4 MAX_ROUNDS 终止
4.5 计费正确性（每轮计 token、calls 仅 1）
4.6 付费插件日上限（可选）
4.7 编排前置错误
```

---

## 7. 错误码对照（M3 相关）

| code | HTTP | 含义 |
|---|---|---|
| 0 | 200/201 | 成功 |
| 40000 | 400 | 参数校验（非法 tool_schema_json / 缺必填 / SSRF 配置拒绝 / 绑定不存在或非 active 资源 / messages 为空） |
| 40003 | 403 | 越权（改删他人/官方 Agent、查他人私有 Agent） |
| 40300 | 403 | 未开通 token 服务 / 无可用模型（编排前置） |
| 40400 | 404 | 资源不存在（agent/skill/plugin 按 id 查无） |
| 40900 | 409 | code 已存在（skill/plugin/agent 唯一冲突） |
| 401 | 401 | 未登录 / **sk 调编排端点**（D2） |
| 403 | 403 | 无权限码 / 管理员未二次验证 |
| 60001 | 402 | 钱包余额不足（编排登录态 postpaid 预扣失败） |
| 60005 | 402 | 套餐额度不足（prepaid，编排登录态默认 postpaid，一般不触发） |
| 50200 | 502 | 上游模型失败 |
| 50300 | 503 | 上游渠道不可用 |
| 50301 | 503 | 系统繁忙、可重试（勿与 60001 混淆） |

---

## 8. M3 接口清单速查

| 接口 | 方法 | 鉴权 | M3 关注点 |
|---|---|---|---|
| `/api/admin/skills`(+`/{id}`) | GET/POST/PATCH/DELETE | 🛡️ `skill:manage` | tool_schema_json 合法性、handler_key、唯一 40900 |
| `/api/admin/plugins`(+`/{id}`) | GET/POST/PATCH/DELETE | 🛡️ `plugin:manage` | 凭证不回(has_auth)、SSRF 配置拦截、is_paid/daily_limit |
| `/api/admin/agents`(+`/{id}`) | GET/POST/PATCH/DELETE | 🛡️ `agent:manage` | 绑定回填名称、唯一 40900 |
| `/api/admin/agents/{id}/skills`、`/plugins` | POST | 🛡️ `agent:manage` | 绑定/解绑覆盖语义 `{ids:[...]}` |
| `/api/agents`(+`/{id}`) | GET/POST/PATCH/DELETE | 🔑 登录态 | 选用/自建、越权 40003、自建仅绑 active 官方 |
| `/api/skills`、`/api/plugins` | GET | 🔑 登录态 | 只读精简（不回 handler_key/endpoint/凭证） |
| `/api/agents/{id}/chat` | POST | 🔑 **仅登录态** | tool-use 编排 SSE；sk→401；每轮计 token、calls 计 1 |

> 字段/错误码若与实现不符，以代码为准并回写本文 + `frontend-api-reference.md` §14（接口字段变更未同步为本项目反复出现根因）。
