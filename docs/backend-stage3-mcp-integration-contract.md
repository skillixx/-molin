# MCP 接入对接契约（Model Context Protocol 工具源）

> 状态：设计契约 v1（2026-06-23）｜ 阶段：第三阶段（工具生态做深，第二阶段已封板，本功能新增）
> 目标：在现有「HTTP 插件（单工具）」之外，新增 **MCP server** 作为工具源——接一个 server 即**自动导入它暴露的一批工具**到 Agent，编排时统一调用。
> 实现方：后端丁（token_gateway / workbench 编排 + 新增 MCP client）。
> 关联：扩展 `backend-chat-workbench-contract.md` §4 编排 + §5 安全；复用 `workbench/security/ValidateOutboundURL`（SSRF）。
> 铁律延续：工具（含 MCP）对用户**免费**，唯一收费=模型 token。

---

## 1. 背景与定位（插件 vs MCP）

| | 现有「插件」(type=http) | 新增「MCP server」 |
|---|---|---|
| 粒度 | 1 条配置 = 1 个 HTTP 工具 | 1 条配置 = 1 个 server，**自动暴露 N 个工具** |
| 工具定义 | 运营**手填** `tool_schema_json` | server **自描述**，网关 `tools/list` **发现** |
| 调用 | 门面 POST `endpoint_url` | 门面经 MCP 协议 `tools/call` |
| 生态 | 私有、逐个配 | 开放标准，接入现成 MCP server 生态 |

二者**并存**：简单包一个 REST 接口用插件够了；要批量接标准化工具用 MCP。MCP 不替换插件，是第二种工具源。

---

## 2. 本期范围（v1，明确边界）

**做**：
- 传输：**Streamable HTTP** transport（远程 MCP server）。
- 能力原语：**tools**（映射为模型 function-calling 工具）。
- 生命周期：`initialize` 握手 → `tools/list` 发现 → `tools/call` 调用。
- 鉴权：静态 **Bearer / 自定义 header**（复用插件 auth_config 风格）。

**不做（预留/后续）**：
- **stdio transport**（本地子进程）——多租户 SaaS 网关不适合拉起本地进程，排除。
- **resources / prompts / sampling / elicitation / roots** 等其它原语——v1 只做 tools；resources（喂上下文）可作 v2。
- **OAuth 2.1 授权流**（MCP HTTP 标准授权）——v1 用静态 token；动态 OAuth 后续。

> 范围收窄是为安全 + 可控：只接「远程 HTTP + 只读发现 + tools 调用 + 静态鉴权」，把攻击面压到最小。

---

## 3. 数据模型 + 迁移

MCP server「一对多工具」，不塞进 `plugins`（其为单工具），**新建独立表**（结构对齐 plugins 风格）：

```sql
-- MCP server 注册
CREATE TABLE IF NOT EXISTS mcp_servers (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(64) NOT NULL COMMENT '唯一编码，做工具命名空间前缀',
  name VARCHAR(128) NOT NULL,
  description VARCHAR(512) NOT NULL DEFAULT '',
  endpoint_url VARCHAR(512) NOT NULL COMMENT 'MCP server HTTP 端点（仅 https + 白名单）',
  auth_config_encrypted VARBINARY(1024) NULL COMMENT '鉴权配置 AES-256-GCM，响应不返回',
  protocol_version VARCHAR(32) NOT NULL DEFAULT '' COMMENT 'initialize 协商到的协议版本（回填）',
  timeout_ms INT NOT NULL DEFAULT 15000 COMMENT '单次调用超时（≤30000）',
  is_paid TINYINT(1) NOT NULL DEFAULT 0 COMMENT '调用是否产生平台成本（同插件 D3）',
  daily_limit INT NOT NULL DEFAULT 0 COMMENT '付费时每用户每日调用上限（0=不限）',
  status VARCHAR(16) NOT NULL DEFAULT 'inactive' COMMENT 'active/inactive；新建默认 inactive，发现+审核后再启用',
  last_discovered_at DATETIME NULL COMMENT '最近一次 tools/list 时间',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_mcp_servers_code (code),
  KEY idx_mcp_servers_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='MCP server 注册（工具源）';

-- 已发现并经审核的工具（快照，防 rug-pull + 免每次对话联网 list）
CREATE TABLE IF NOT EXISTS mcp_server_tools (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  server_id BIGINT UNSIGNED NOT NULL,
  tool_name VARCHAR(128) NOT NULL COMMENT 'MCP 原始工具名',
  description VARCHAR(1024) NOT NULL DEFAULT '',
  input_schema_json JSON NOT NULL COMMENT 'MCP inputSchema（JSON Schema），转 OpenAI tools 用',
  enabled TINYINT(1) NOT NULL DEFAULT 0 COMMENT '运营审核后是否对编排暴露',
  schema_hash CHAR(64) NOT NULL DEFAULT '' COMMENT '工具定义指纹，变更触发重新审核',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_mcp_tool (server_id, tool_name),
  CONSTRAINT fk_mcp_tool_server FOREIGN KEY (server_id) REFERENCES mcp_servers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='MCP server 已发现/审核工具快照';

-- Agent 绑定 MCP server（绑 server 而非单工具；该 server 下 enabled 工具全进 Agent 工具集）
CREATE TABLE IF NOT EXISTS agent_mcp_bindings (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  agent_id BIGINT UNSIGNED NOT NULL,
  server_id BIGINT UNSIGNED NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_agent_mcp (agent_id, server_id),
  CONSTRAINT fk_agent_mcp_agent FOREIGN KEY (agent_id) REFERENCES agents (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Agent 绑定 MCP server';
```

> `mcp_server_tools` 快照是关键安全设计：对话时只用 **enabled 的快照工具**，不实时 `tools/list`；server 改了工具定义（`schema_hash` 变）需运营重新审核启用——挡 **tool poisoning / rug-pull**。

---

## 4. MCP client 流程（网关侧，JSON-RPC 2.0 over Streamable HTTP）

```
discover(server):                        # 运营点"刷新工具"时
  POST endpoint_url  { jsonrpc:"2.0", method:"initialize", params:{protocolVersion, capabilities, clientInfo} }
    → 拿 server 能力 + protocolVersion（回填 mcp_servers.protocol_version）
  发送 notifications/initialized
  POST { method:"tools/list" }           # 可能分页（nextCursor）→ 循环取全
    → 工具数组 [{name, description, inputSchema}]
  upsert 到 mcp_server_tools（算 schema_hash；定义变化的置 enabled=0 待重审）

callTool(server, toolName, args):        # 编排某轮命中 MCP 工具时
  （复用已建立/缓存的会话；无则先 initialize）
  POST { method:"tools/call", params:{ name:toolName, arguments:args } }
    → result.content（文本/结构化）→ 截断后回灌模型
```

要点：
- **会话/连接**：Streamable HTTP 每次调用是 HTTP 请求；如 server 要求会话（`Mcp-Session-Id`），client 维护会话头并按需重握手。带 `timeout_ms`。
- **协议版本协商**：`initialize` 带本网关支持的 protocolVersion，按 server 返回适配。
- **错误**：JSON-RPC error / HTTP 非 2xx / 超时 → 该工具返回错误结果回灌（不中断对话，同插件失败降级）。

---

## 5. 编排集成（复用 workbench ChatService）

在 `assembleTools`（见 chat-workbench 编排）里，除 skill/plugin 外**再汇总 Agent 绑定的 MCP server 的 enabled 工具**：

1. **命名空间防撞**：MCP 工具暴露给模型的名字加前缀 `mcp__{server_code}__{tool_name}`（与 skill/plugin/其它 server 工具不冲突；模型按此名发起 tool_call）。
2. **schema 转换**：MCP `inputSchema`(JSON Schema) → OpenAI tool `{type:function, function:{name(带前缀), description, parameters:inputSchema}}`。
3. **toolIndex 路由**：`toolIndex[前缀名] = {kind:mcp, serverID, originalToolName}`；编排循环命中该名 → 走 MCP client `callTool(serverID, originalToolName, args)`。
4. 工具失败 → 错误结果回灌、不中断（与现有 skill/plugin 一致）。

> 编排循环、SSE 事件（tool_call/tool_result/message/[DONE]）、计费（每轮 token / 整次 calls 计 1）**完全复用现状**，MCP 只是多一种工具来源。

---

## 6. 接口契约

### 6.1 管理端（✅ 定稿：复用 `plugin:manage` + 双重认证，不新增权限码）
- `GET/POST /api/admin/mcp-servers`、`GET/PATCH/DELETE /api/admin/mcp-servers/{id}` —— CRUD；凭证只入不出（`has_auth` 表征，同插件红线）。
- `POST /api/admin/mcp-servers/{id}/discover` —— 触发 `initialize`+`tools/list`，回写 `mcp_server_tools`，返回发现的工具列表（含变更/待审标记）。
- `GET /api/admin/mcp-servers/{id}/tools` —— 列已发现工具；`PATCH .../tools/{toolId}` 改 `enabled`（审核启用/停用单个工具）。
- Agent 绑定：`POST /api/admin/agents/{id}/mcp-servers` Body `{ "ids":[...] }`（覆盖语义，同 skills/plugins 绑定）。

### 6.2 用户端（只读，供自建 Agent 绑定——若开放）
- `GET /api/mcp-servers` —— active server 精简视图（`id,code,name,description,is_paid`，**不回 endpoint/凭证**）。
- 自建 Agent 是否可绑 MCP：✅ **定稿 v1 仅官方 Agent 可绑 MCP**（外部接入风险高，比 skill/插件更需审核）；用户自建放开后续再议。

### 6.3 错误码
| 情形 | 码 |
|---|---|
| endpoint 非 https/内网、参数校验、tool schema 非法 | 40000 |
| code 重复 | 40900 |
| server/工具不存在 | 40400 |
| discover 连接/握手失败 | 502（50200 族）+ 错误详情，不改 server 状态 |

---

## 7. 安全（重点，MCP 比插件风险更高）

- **SSRF**：`endpoint_url` 复用 `ValidateOutboundURL`（https + 拒内网/回环 + 运行时 DNS 解析 + 域名白名单）；禁跟随重定向。
- **凭证**：`auth_config` AES-256-GCM 加密，响应仅 `has_auth`，绝不回显。
- **tool poisoning / rug-pull**：工具定义经 `mcp_server_tools` 快照 + 运营审核（enabled）才暴露；`schema_hash` 变化自动置未启用待重审——server 事后篡改工具描述/参数无法静默生效。
- **prompt 注入**：MCP 工具的 **description 和返回 content 都是外部不可信文本**，会进模型上下文。需：工具结果**截断**（如 ≤6KB）、明确标注为工具输出、不把 MCP 文本当系统指令；高风险 server 仅限官方 Agent。
- **超时 / 熔断**：`timeout_ms` 强制上限（≤30s）；连续失败自动置 server `inactive` 告警（同插件熔断）。
- **配额防滥用**：`is_paid=1` 的 server 按 `daily_limit` 每用户每日限流（✅ 定稿：用**通用表 `tool_daily_call_logs`**，与插件共用同款机制）。
- **权限/越权**：MCP 配置仅管理端；用户不能自建 MCP server。

---

## 8. 计费

- MCP 工具调用对用户**免费**（红线：唯一收费=模型 token）。编排里每轮模型调用照常计 token、整次 calls 计 1。
- `is_paid` 标记**平台成本**归属（同插件 D3 语义，非向用户收费），配 `daily_limit` 防滥用。

---

## 9. 任务拆分（后端丁）

1. 迁移：`mcp_servers` + `mcp_server_tools` + `agent_mcp_bindings` + 通用 `tool_daily_call_logs`（收口替代 `plugin_daily_call_logs`）。
2. MCP client：JSON-RPC over Streamable HTTP，实现 `initialize`/`tools/list`(分页)/`tools/call` + 会话头 + 超时 + 错误归一。
3. model/repo/service：server CRUD（凭证加密、SSRF 校验）、discover（写快照 + schema_hash + 待审）、tool enable 审核、agent-mcp 绑定。
4. 编排集成：`assembleTools` 汇总 MCP enabled 工具（命名空间 + schema 转换 + toolIndex 路由）；`runTool` 增 mcp 分支调 client。
5. handler/route + bootstrap 装配（注入 cipher / 复用 SSRF / 限流计数）。
6. 安全：快照审核、熔断、截断、白名单。
7. 测试：discover 发现+审核、命名空间不撞、tools/call 回灌、失败降级不中断、schema 变更需重审、SSRF 拦截、付费日限、越权。
8. 回写 `frontend-api-reference.md`（新增 §14.11 MCP 管理）+ `backend-chat-workbench-contract.md` §4/§5。

预估 **6~9 人日**（MCP client + 审核流 + 编排集成 + 安全 + 测试；是第三阶段较大件）。

---

## 10. 边界与已知限制（v1）
- 仅 HTTP transport + tools 原语 + 静态鉴权；stdio / resources / prompts / sampling / OAuth 预留。
- 工具用**快照**（非每对话实时 list），新工具需运营 discover+审核后才可用（换取安全与性能）。
- 默认仅官方 Agent 可绑 MCP；用户自建可绑待定。
- 渠道/上游单点等既有限制不变。

## 11. 已定稿决策（2026-06-23，PM 确认）
- **权限码**：✅ **复用 `plugin:manage`**（不新增 mcp:manage）。
- **限流计数**：✅ **新建通用表 `tool_daily_call_logs`**（插件 / MCP / 未来工具源共用按用户每日计数），收口替代插件专用 `plugin_daily_call_logs`（迁移时一并迁移）。
- **用户自建 Agent 绑 MCP**：✅ **v1 仅官方 Agent 可绑**（后续视情况放开）。
- **工具快照刷新**：✅ **仅手动 discover**（不做后台定时联网外部）。
- **实现优先级**：✅ MCP 为第三阶段**最后实现**（最重最高危）；顺序 分类 → 定向可见性 → MCP，且均待第二阶段前端落地 + 上线后再进实现。
- 迁移序号：第三阶段起点，按合并顺序排（实现时定）。
