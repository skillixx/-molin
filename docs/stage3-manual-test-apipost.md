# 第三阶段接口手动测试文档（apiPost 用）

> 范围：第三阶段工作台增强 —— ① Agent 分类 ② Agent 定向可见性 ③ MCP 接入。后端已实现+验收通过（80/80 零缺陷）并部署测试环境（DB version=50）。
> 用途：apiPost / Postman 手动测试。JSON 请求统一返回 `{code,message,data}`，`code=0` 成功。
> 关系：第二阶段手测见 `m1~m4-manual-test-apipost.md`；本文聚焦第三阶段新增能力。
> 契约：`frontend-api-reference.md` §14.9/14.10/14.11、`backend-stage3-*-contract.md`。
> 日期：2026-06-23 ｜ 测试服 main `698ff97`（DB schema 000050）

---

## 0. 第三阶段在测什么

| 特性 | 一句话 | 红线 |
|---|---|---|
| **① 分类** | 给 Agent 打分类（办公/学习/商务/娱乐），前端分类导航 | 展示维度，与可见性/计费正交 |
| **② 定向可见性** | 官方 Agent 定向给「分组（可按组内角色）/ 全局角色」看 | **三处都过滤**（列表/详情/编排 chat），越权直连 40003 |
| **③ MCP 接入** | MCP server 作第二种工具源，自动暴露一批工具 | 凭证只入不出、SSRF、工具先审核才暴露、仅官方可绑 |

---

## 1. 环境与前置

### apiPost 环境变量

| 变量 | 说明 |
|---|---|
| `base_url` | `http://localhost:8080`（或测试服，连接见 m1 文档 §0） |
| `token` | 用户 A 登录态 JWT |
| `token_b` | 用户 B 登录态 JWT（验越权/分组定向用） |
| `admin_token` | 管理员 JWT，**且已完成二次验证**（管理端用） |
| `official_agent_id` | 官方 Agent ID（建后填） |
| `mcp_server_id` | MCP server ID（建后填） |

### 前置数据（验②可见性需要）
- 用管理后台已有功能：建一个**用户分组**，把**用户 A 设为组管理员(admin)、用户 B 设为普通组员(member)**（或按需安排谁在组内/组外）；记分组 ID。
- 给某用户配一个**全局角色**（角色管理），记角色 code（如 `vip`）。
- 管理端需 `agent:manage`（分类/可见性）和 `plugin:manage`（MCP）权限 + 二次验证。

---

## 2. ① Agent 分类

> 🛡️ admin_token + 二次验证 ｜ 🔑 登录态

### 2.1 分类列表 🔑
- **GET** `{{base_url}}/api/agent-categories`
- 预期：`data.items` 为 4 类（office 办公 / study 学习 / business 商务 / entertainment 娱乐），仅 active、按 sort_order；元素 `{code,name,icon,sort_order}`。
- 管理端全量：**GET** `{{base_url}}/api/admin/agent-categories` 🛡️（含 inactive）。

### 2.2 建官方 Agent 带分类 🛡️
- **POST** `{{base_url}}/api/admin/agents`
  ```json
  { "name":"办公助手","system_prompt":"你是办公助手","default_model_code":"DeepSeek","category_code":"office" }
  ```
- 预期：`data` 回显 `category_code:"office"` + `category_name:"办公"` → 记 id 填 `official_agent_id`。
- 负向：`category_code:"nope"`（不存在）→ `40000`。

### 2.3 改分类 / 清分类 🛡️
- **PATCH** `{{base_url}}/api/admin/agents/{{official_agent_id}}` body `{ "category_code":"study" }` → 改为学习。
- 清空：body `{ "category_code":"" }` → 变未分类（`category_code:null`）。

### 2.4 按分类过滤 🔑/🛡️
- **GET** `{{base_url}}/api/agents?category=office`（用户端）/ `{{base_url}}/api/admin/agents?category=office`（管理端）
- 断言：只返回该分类的 Agent；不带 `category` 返回全部。

---

## 3. ② Agent 定向可见性（核心）

> 目标：设定官方 Agent 的可见范围，然后**用不同用户**验证列表/详情/编排三处都按范围过滤。

### 3.1 设可见范围（两种方式）🛡️
- 内联（建/改 Agent 时带）：
  ```json
  { "...":"...", "visible_scope":"groups", "group_ids":[<分组ID>], "group_roles":["admin"] }
  ```
- 独立端点（覆盖语义）：**PUT** `{{base_url}}/api/admin/agents/{{official_agent_id}}/visibility`
  ```json
  { "visible_scope":"groups", "group_ids":[<分组ID>], "group_roles":["admin"] }
  ```
- 三种 scope：
  - `{"visible_scope":"all"}` 全员可见（默认）
  - `{"visible_scope":"groups","group_ids":[10],"group_roles":["admin"]}` 仅组10的组管理员（`group_roles` 留空=组内任意成员）
  - `{"visible_scope":"roles","role_codes":["vip"]}` 仅 vip 角色
- 回显：AgentResp 含 `visible_scope` + `target_audience`（`{group_ids,group_roles}` 或 `{role_codes}`；all 时为 null）。

### 3.2 三处过滤验证（关键）
设该 Agent 为 `groups + group_ids=[组X] + group_roles=["admin"]`，用户 A 是组 X 的 admin、用户 B 在组外（或组内 member）：

| 验证点 | 用户 A（命中）| 用户 B（不命中）|
|---|---|---|
| **列表** `GET /api/agents` | 列表**含**该 Agent | 列表**不含** |
| **详情** `GET /api/agents/{{official_agent_id}}` | 200 正常 | **40003** |
| **编排** `POST /api/agents/{{official_agent_id}}/chat` | 正常对话 | **40003** |

> 红线：列表过滤 ≠ 访问控制——用户 B 即使知道 id 直连详情/chat 也必须 40003。

### 3.3 回归：all 全员可见 + 本人自建恒可见 🔑
- 把 Agent 设回 `{"visible_scope":"all"}` → 用户 A/B 都能看到。
- 用户自建 Agent（`POST /api/agents`）对本人恒可见，不受 scope 影响。

### 3.4 写入校验（负向，均 40000）🛡️
- `visible_scope` 非 all/groups/roles；
- `groups` 但 `group_ids` 空；`group_roles` 含非 admin/member；
- `roles` 但 `role_codes` 空；
- `group_ids`/`role_codes` 含不存在的分组/角色。

---

## 4. ③ MCP 接入

> 🛡️ `plugin:manage`（复用，非新权限码）+ 二次验证。错误码：40000 校验/SSRF、40900 code 冲突、40400 不存在、40003 越权、502 discover 失败。

### 4.1 建 MCP server 🛡️
- **POST** `{{base_url}}/api/admin/mcp-servers`
  ```json
  {
    "code":"demo-mcp","name":"演示MCP","description":"",
    "endpoint_url":"https://mcp.example.com/rpc",
    "auth_config":"{\"header\":\"Authorization\",\"value\":\"Bearer demo-xxx\"}",
    "timeout_ms":15000,"is_paid":false,"daily_limit":0,"status":"inactive"
  }
  ```
- 预期：`data` 含 `id`、**`has_auth:true`**、`status:"inactive"`（新建默认未启用）→ 记 `mcp_server_id`。
- **断言（安全红线）**：响应**无** `auth_config`/凭证明文，仅 `has_auth`。
- 负向：`endpoint_url:"http://..."`（非 https）或内网（`https://127.0.0.1/...`/`https://10.0.0.1/...`）→ `40000`；`code` 重复 → `40900`。

### 4.2 改/清凭证 🛡️
- **PATCH** `{{base_url}}/api/admin/mcp-servers/{{mcp_server_id}}` body `{ "auth_config":"" }` → 清空凭证，再查 `has_auth:false`。

### 4.3 发现工具 + 审核 🛡️
- **POST** `{{base_url}}/api/admin/mcp-servers/{{mcp_server_id}}/discover`
  - 预期（连得上时）：`data` 含 `protocol_version`、`discovered`(数量)、`changed`、`tools[]`；server 回填 `protocol_version`/`last_discovered_at`。
  - 连不上/握手失败 → **502**，**不改 server 状态**。
- **GET** `{{base_url}}/api/admin/mcp-servers/{{mcp_server_id}}/tools` → 工具快照列表（含 `enabled`，新发现默认 `false`）。
- **PATCH** `{{base_url}}/api/admin/mcp-servers/{{mcp_server_id}}/tools/{toolId}` body `{ "enabled":true }` → 审核启用。**仅 enabled=true 的工具会进编排。**

> ⚠️ **discover/编排需要一个真实可达的 MCP server**（公网 https + 有效证书 + 讲 MCP JSON-RPC）。测试环境通常没有——此时 4.3/4.6 的"真实外呼"无法手测，属环境限制（运行时 SSRF 拒内网 + 无自签证书支持），后端已由 Go 集成测试（httptest stub）覆盖完整 e2e。手测可只验 4.1/4.2/4.4/4.5 与 discover 的失败路径(502)。

### 4.4 绑定到官方 Agent（仅官方）🛡️
- **POST** `{{base_url}}/api/admin/agents/{{official_agent_id}}/mcp-servers` body `{ "ids":[{{mcp_server_id}}] }`（覆盖语义，`[]`=全解绑）→ `{ "bound":true }`。
- 负向：绑到**用户自建** Agent → `40003`。

### 4.5 用户端只读 🔑
- **GET** `{{base_url}}/api/mcp-servers` → 仅 active server 精简 `{id,code,name,description,is_paid}`，**不回 endpoint/凭证/配额**。

### 4.6 编排触发 MCP 工具（需真实 MCP server）🔑
- 前置：server active + 工具 enabled + 绑到官方 Agent。
- **POST** `{{base_url}}/api/agents/{{official_agent_id}}/chat`（登录态，SSE）→ 模型调用工具时以 `mcp__demo-mcp__<tool>` 命名出现在 `tool_call` 事件，结果回灌。
- （无真实 MCP server 时跳过，见 4.3 说明。）

---

## 5. 错误码对照（第三阶段）

| code | HTTP | 含义 |
|---|---|---|
| 40000 | 400 | 参数校验 / 分类不存在 / 可见性写入校验 / MCP endpoint 非https或内网 |
| 40003 | 403 | 越权（越权访问不可见 Agent 详情/chat、绑 MCP 到用户自建 Agent） |
| 40400 | 404 | agent / mcp server / 工具不存在 |
| 40900 | 409 | code 已存在（mcp server） |
| 401 | 401 | 未登录 |
| 403 | 403 | 无权限码 / 管理员未二次验证 |
| 502 | 502 | MCP discover 连接/握手失败（50200，不改 server 状态） |

---

## 6. 推荐测试顺序

```
前置：admin 登录+二次验证→admin_token；用户 A/B 登录→token/token_b；建分组(A=admin,B=member)+配全局角色
① 2.1 分类列表 → 2.2 建官方 Agent 带分类 → 2.3 改/清 → 2.4 过滤
② 3.1 设可见范围(groups+group_roles=admin) → 3.2 三处过滤(A 可见/B 40003) → 3.3 改回 all 回归 → 3.4 写入校验 40000
③ 4.1 建 MCP(验 has_auth 不回凭证 + SSRF 负向) → 4.2 清凭证 → 4.3 discover(无真实 server 验 502) → 4.4 绑官方(绑自建 40003) → 4.5 用户端只读不回 endpoint →（有真实 server 才）4.6 编排触发
```

---

## 7. 红线速查（必过 checklist）

- [ ] 分类列表仅 active 按序；Agent 回显 category_code+category_name；非法分类 40000
- [ ] `?category=` 过滤正确
- [ ] 可见性 groups（含组内角色 admin/member）/roles/all 三语义正确
- [ ] **三处过滤**：越权用户 列表不含 + 详情 40003 + chat 40003
- [ ] 可见性写入校验全 40000
- [ ] MCP 凭证只入不出（仅 has_auth，无明文）
- [ ] MCP endpoint 非 https/内网 → 40000
- [ ] MCP 工具 discover 后默认未启用，审核 enabled 才进编排
- [ ] MCP 仅官方 Agent 可绑（绑自建 40003）
- [ ] 用户端 MCP 列表不回 endpoint/凭证

> 字段/错误码若与实现不符，以代码/§14 为准并回写本文 + `frontend-api-reference.md` §14。
