# 多模型聊天工作台对接契约（Agent / Skill / 插件 + tool-use 编排）

> 状态：对接契约 v1（2026-06-21）
> 阶段：第二阶段 M3（Week 7–8）
> 实现方：后端丁（agent/skill/plugin 模块 + 门面编排）、后端甲（权限码 seed）
> 关联：`docs/backend-stage2-architecture-roadmap.md` §5/§6、`docs/frontend-api-reference.md` §14
> 铁律：Agent / Skill / 插件 / 角色**全部免费**（运营配置或用户自建均不收费）；唯一收费点 = 模型 token 调用（计费见 `docs/backend-token-billing-contract.md`）。

---

## 1. 范围与概念

「Gemini 式」站内聊天工作台，本期模型类型**仅 chat**。三层能力（均免费、脱离 product/order）：

| 层 | 含义 | 执行方式 |
|---|---|---|
| Agent（=角色/人设） | system_prompt + 默认模型 + 绑定的 skill/插件 | 选定后作为对话上下文 |
| Skill（平台内置能力） | 联网搜索 / 读文档…（D4：本期不含代码执行） | 门面内置函数（`handler_key` 路由） |
| 插件（外部第三方） | 外部 HTTP 工具 | 门面按 schema 转发 `endpoint_url` |

> 「角色」不单独建表，**Agent 即角色**。用户「切角色」= 选不同 Agent。

调用二分（见 §4）：开发者直连 `/api/token/chat/completions`（纯透传）；产品内聊天 `/api/agents/{id}/chat`（门面 tool-use 编排）。

---

## 2. 数据模型 + 迁移

迁移序号紧随 sk(000034 api_keys)/wallet_holds(000035)/计费(000036 calls seed) 之后，以实际合并顺序为准（下用 000037–000040 示意；golang-migrate 不留空号、严格按合并顺序递增）。

```sql
-- 000037 agent
CREATE TABLE agents (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  code VARCHAR(64) NULL,                       -- 官方预设唯一编码；用户自建为 NULL
  name VARCHAR(128) NOT NULL,
  description VARCHAR(512) NOT NULL DEFAULT '',
  avatar VARCHAR(512) NOT NULL DEFAULT '',
  owner_type VARCHAR(16) NOT NULL DEFAULT 'official',   -- official / user
  owner_user_id BIGINT UNSIGNED NULL,                   -- user 自建时非空
  system_prompt TEXT NOT NULL,
  default_model_code VARCHAR(128) NOT NULL,             -- 指向 token_models.logical_model_code
  status VARCHAR(16) NOT NULL DEFAULT 'active',         -- active / inactive
  sort_order INT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_agents_code (code),
  KEY idx_agents_owner (owner_type, owner_user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE agent_skill_bindings (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  agent_id BIGINT UNSIGNED NOT NULL,
  skill_id BIGINT UNSIGNED NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_agent_skill (agent_id, skill_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE agent_plugin_bindings (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  agent_id BIGINT UNSIGNED NOT NULL,
  plugin_id BIGINT UNSIGNED NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_agent_plugin (agent_id, plugin_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 000038 skill（平台内置能力）
CREATE TABLE skills (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  code VARCHAR(64) NOT NULL,                   -- 唯一
  name VARCHAR(128) NOT NULL,
  description VARCHAR(512) NOT NULL DEFAULT '',
  category VARCHAR(64) NOT NULL DEFAULT '',
  tool_schema_json JSON NOT NULL,              -- function calling 工具定义（OpenAI tools 格式）
  handler_key VARCHAR(64) NOT NULL,            -- 内置实现路由键（门面 dispatch）
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_skills_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 000039 plugin（外部第三方）
CREATE TABLE plugins (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  code VARCHAR(64) NOT NULL,                   -- 唯一
  name VARCHAR(128) NOT NULL,
  description VARCHAR(512) NOT NULL DEFAULT '',
  tool_schema_json JSON NOT NULL,              -- function calling 工具定义
  endpoint_url VARCHAR(512) NOT NULL,          -- 外部 HTTP 端点
  auth_config_encrypted VARBINARY(1024) NULL,  -- 如需鉴权，AES-256-GCM 加密；响应不返回
  timeout_ms INT NOT NULL DEFAULT 10000,
  is_paid TINYINT(1) NOT NULL DEFAULT 0,        -- D3：是否付费第三方插件（成本平台担）
  daily_limit INT NOT NULL DEFAULT 0,           -- D3：付费插件每用户每日调用上限（0=不限；默认值由运营配，建议保守如 50）
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_plugins_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 000040 权限码 seed（红线：建码必建 seed）
-- agent:manage / skill:manage / plugin:manage
```

---

## 3. 接口契约

### 3.1 管理端（运营配置官方资源）

| 方法 | 路径 | 权限 |
|---|---|---|
| `GET/POST` | `/api/admin/agents`、`PATCH/DELETE /api/admin/agents/{id}` | `agent:manage` + 双重认证 |
| `POST` | `/api/admin/agents/{id}/skills`、`/api/admin/agents/{id}/plugins`（绑定/解绑） | `agent:manage` |
| `GET/POST` | `/api/admin/skills`、`PATCH/DELETE /api/admin/skills/{id}` | `skill:manage` + 双重认证 |
| `GET/POST` | `/api/admin/plugins`、`PATCH/DELETE /api/admin/plugins/{id}` | `plugin:manage` + 双重认证 |

- 插件 `auth_config` 仅入参加密落库，响应用 `has_auth` 表征（仿渠道 key 红线）。

### 3.2 用户端（选用 + 自建）

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| `GET` | `/api/agents` | 登录态 | 列可用 Agent：官方（official, active）+ 本人自建 |
| `GET` | `/api/agents/{id}` | 登录态 | Agent 详情（含绑定的 skill/插件名称，不含插件凭证） |
| `POST` | `/api/agents` | 登录态 | 用户自建 Agent（`owner_type=user`），可选基模型 + 绑定已开放 skill/插件 |
| `PATCH/DELETE` | `/api/agents/{id}` | 登录态 | 仅能改/删本人自建（官方只读，越权 40003） |
| `GET` | `/api/skills`、`/api/plugins` | 登录态 | 列 active 能力（供自建 Agent 绑定，只读精简视图，插件不回 endpoint/凭证） |

> 用户自建 Agent **可绑定 status=active 的官方 skill + 官方插件**（PM 决策 2026-06-21）；但**不能自建/上传 skill 或插件**（外部接入涉及 SSRF/凭证，必须经运营审核上架）。

### 3.3 聊天对话（核心，tool-use 编排）

- **POST** `/api/agents/{id}/chat` *(**仅登录态**，D2 已拍板：sk 不可调编排端点)*
  > **D2 边界（2026-06-21）**：编排端点会触发平台代付的付费插件调用且多轮放大，**只允许站内登录态**（真人会话天然限速）。外部程序/sk 只能用透传端点 `POST /api/token/chat/completions`（工具循环自理、不触发平台代付）。产品边界：外部=纯透传自付，站内=平台编排。
- 请求：
  ```json
  { "messages": [{ "role": "user", "content": "帮我查一下今天的新闻并总结" }], "stream": true }
  ```
  - `model` 可选（缺省用 Agent 的 `default_model_code`；传则覆盖，须为该用户可用模型）。
  - `stream=true` → 最终答案走 SSE。
- 行为：门面取 Agent → 注入 system_prompt + 绑定 skill/插件的 `tool_schema_json` 为 tools → tool-use 编排循环（见 §4）→ 返回最终答案。
- 计费：按 token 累加所有轮 / 按次计 1（见 billing 契约）。Agent/skill/插件本身免费。
- 错误码复用 §14：40300（未开通 token 服务/无可用模型）、50200/50300（上游/渠道）、新增 `42201`（工具执行失败，可选）。

> HTTP 细化（请求/SSE 事件结构）补入 `docs/frontend-api-reference.md` §14.8，供前端对接。

---

## 4. tool-use 编排实现规范（后端丁）

```
ChatWithAgent(ctx, agentID, userID, billingCtx, messages, stream):
  agent  = load(agentID); 校验可见性（official 或 owner==userID）
  model  = req.model or agent.default_model_code
  tools  = 汇总 agent 绑定且 enabled 的 skill/plugin 的 tool_schema_json
           + 绑定 active MCP server 的 enabled 工具（第二种工具源，第三阶段接入；命名空间 mcp__{server_code}__{tool_name}）
  msgs   = [system: agent.system_prompt] + messages
  for round in 1..MAX_ROUNDS:                            # MAX_ROUNDS 可配置，默认 5（见下）
      resp = forward.ChatOnce(model, msgs, tools)        # 复用转发器（选渠道/转发/读usage/计费）
      记 usage（每轮都计 token）
      if resp 无 tool_calls:
          return resp（最终答案，stream 则 SSE 透传本轮）
      for call in resp.tool_calls:
          if call 属 skill:  result = skillDispatch(handler_key, args)      # 内置函数
          if call 属 plugin: result = pluginForward(endpoint_url, args)     # 外部 HTTP（超时/SSRF 防护）
          if call 属 mcp:    result = mcpClient.callTool(server, name, args) # MCP tools/call（超时/SSRF/禁重定向，失败同插件降级）
          msgs += [assistant tool_call, tool result(result)]
  到达 MAX_ROUNDS 仍未收敛 → 终止，返回已有内容 + 提示「工具调用已达上限，本次已正常计费」
  整次提问结束：按次计 1（见 billing 契约 §3.2）
```

要点：
- **MAX_ROUNDS 可配置（PM 决策 2026-06-21）**：默认 5，经配置项/环境变量注入，**不硬编码**，便于后续按需调整；超限终止时给用户友好提示并**明确告知本次已正常计费**（已消耗 token 不退）。
- **复用现有转发器**：每轮上游调用走 `ForwardService` 的选渠道/转发/读 usage/计费，不另写转发逻辑。
- **skill dispatch**：`handler_key` → 门面内置函数注册表（如 `web_search` / `doc_read`）；本期先上 1–2 个示例。**D4 已拍板：本期不做 `code_exec`**（服务端执行模型生成代码 = RCE，需独立沙箱，超出本期范围，留待第三阶段 + 安全评审）。
- **plugin forward**：按 `tool_schema_json` 取参 → POST `endpoint_url`（带解密后的 auth、`timeout_ms` 超时）→ 取 JSON 结果回灌。
- **流式**：中间工具轮可向前端发进度事件（`event: tool_call` / `tool_result`，前端契约定义）；最终答案轮发 `event: message` + `[DONE]`。
- **失败降级**：单个工具失败 → 作为 tool 错误结果回灌，让模型自行决定，不直接中断整次对话（除非连续失败）。
- **会话历史（PM 决策 2026-06-21）**：本期**对话历史由前端/客户端自持**（每次请求传完整 `messages`），**后端不落库存储对话内容**——与隐私红线「对话内容不落明文日志」一致。`token_usage_logs` 仅记 tokens/状态等元数据，不含对话正文。多端同步/历史会话查看为后续阶段需求。

---

## 5. 安全（重点：插件外部接入）

> 第二种工具源 **MCP server**（第三阶段接入，见 `docs/backend-stage3-mcp-integration-contract.md`）共用本节安全机制：SSRF（配置时校验 + 运行时 DNS 解析 + 禁重定向）、凭证 AES-256-GCM、超时熔断、付费 server 用通用 `tool_daily_call_logs` 限流（`tool_type='mcp'`）。额外加强：工具定义经 `mcp_server_tools` 快照 + 运营审核（`enabled`）才暴露，`schema_hash` 变更自动置未启用待重审（挡 tool poisoning / rug-pull）；MCP 工具结果截断 ≤6KB；v1 仅官方 Agent 可绑 MCP。

- **SSRF 防护**：插件 / MCP `endpoint_url` 仅允许 https + **可配置域名白名单**（配置项/白名单表，新增插件不改代码）/ 禁内网网段（10./172.16./192.168./169.254./localhost），运营配置时校验。
- **超时与熔断**：`timeout_ms` 强制上限（如 ≤30s），连续失败的插件自动置 `inactive` 告警。
- **凭证加密**：`auth_config_encrypted` AES-256-GCM（复用 `TOKEN_PROVIDER_KEY` 或新增 `PLUGIN_SECRET_KEY`），任何响应不返回。
- **成本与配额（D3 已拍板）**：付费插件外部 API 成本**由平台承担**（既定红线，用户侧不计费）；为防滥用，`is_paid=1` 的插件按 `daily_limit` 做**每用户每日调用上限**（超限当轮工具返回「已达上限」错误结果回灌，不中断对话），且插件调用**计入 sk/user 限流维度**（sk 契约 §9）。仅登录态可调编排端点（D2），进一步收窄滥用面。
- **越权**：用户只能改/删本人自建 Agent；不能创建/修改 skill/plugin。
- **权限码必建 seed**：`agent:manage`/`skill:manage`/`plugin:manage`（红线）。
- **工具参数注入**：skill/plugin 入参来自模型输出，执行前按 `tool_schema_json` 校验类型，内置函数做参数白名单，防命令注入。

---

## 6. 任务拆分

**后端甲**：`agent:manage`/`skill:manage`/`plugin:manage` 权限码 seed。

**后端丁**
1. 迁移 000037–000039（agent/skill/plugin + 绑定表）
2. 三模块 model/repo/service/handler/route + bootstrap 装配
3. 管理端 CRUD + 绑定接口（3.1）
4. 用户端列表/自建/绑定接口（3.2）
5. `POST /api/agents/{id}/chat` tool-use 编排（§4），复用 ForwardService
6. skill 内置函数注册表（1–2 示例）+ plugin HTTP 转发器（SSRF/超时/凭证解密）
7. 计费接入（每轮 token + 一次提问计 1 次，见 billing 契约）

---

## 7. 验收（测试/PM）

- 运营建官方 Agent（挂 1 skill + 1 插件）→ 用户选用对话 → 触发工具调用 → 拿到含工具结果的答案
- 用户自建 Agent（选模型 + 绑定 skill）→ 可用；改/删他人 Agent → 40003
- token 正常计费（按量累加/按次计 1）；Agent/skill/插件零计费
- 工具循环超 MAX_ROUNDS → 安全终止
- 插件指向内网/超时 → 被拦截/熔断
- 插件凭证不在任何响应出现

---

## 8. PM 决策（2026-06-21，已确认）

- 按次循环口径：**按提问计 1 次**；前置失败（未发起上游）不计次（见 billing 契约 §3.2）。
- tool-use 最大轮数：**默认 5，可配置**；超限终止提示已计费。
- 用户自建 Agent：**可绑定官方已上架 skill + 插件**，不能自建/上传 skill/插件。
- 会话历史：本期**前端自持，后端不存对话内容**（见 §4 要点）。
