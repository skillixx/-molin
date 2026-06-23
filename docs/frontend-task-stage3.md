# 前端任务总单：第三阶段（给 Codex）

> ## 📌 给 Codex 的一句话指令（可直接粘贴）
>
> **任务：实现 Molin 第三阶段前端（工作台增强）**
>
> 按本文（`docs/frontend-task-stage3.md`）实现第三阶段前端。开工前先通读 **§0（角色边界与全局约定）** 和 `docs/frontend-api-reference.md` **§14.9 / §14.10 / §14.11**。
>
> **边界**：只写前端页面（`web/admin-console` / `web/user-console`），后端接口已全部就绪并部署测试环境（8.130.9.163:8080），**只按 §14 契约调用，不碰后端**；缺接口只列出来。
>
> **要做 4 项**（详见下文）：
> 1. 用户端工作台：Agent **分类导航 + 筛选**（`GET /api/agent-categories` 渲染 Tab + `?category=` 过滤）
> 2. 管理端 Agent 配置页：加**分类选择**（下拉 `category_code`）
> 3. 管理端 Agent 配置页：加**定向可见性配置**（`visible_scope` all/groups/roles + 分组/组内角色/全局角色多选；或 `PUT /api/admin/agents/{id}/visibility`）
> 4. 管理端**新建 MCP server 管理页**（CRUD + discover + 工具逐个审核启用 + 绑定官方 Agent）
>
> **务必注意的坑**（§5 有全列）：
> - **凭证只入不出**：MCP `auth_config` 只在创建/编辑时填，响应只回 `has_auth`，**绝不回显**，用"重设凭证"入口。
> - **覆盖语义**：绑定（MCP server / 可见性目标）都提交完整集合，不做增量。
> - **MCP 工具要先 discover 再逐个审核启用**才会生效；未启用不暴露。
> - 可见性 groups 可再按组内角色（admin/member），留空=组内任意。
> - 列表都是扁平分页 `{items,page,page_size,total}`；管理端需双重认证。
> - 用户端**对话页不用改**（MCP 工具走同一套 SSE 事件）。
>
> 联调对照 `docs/backend-stage3-test-report.md` 的预期行为；可用 `docs/stage3-manual-test-apipost.md` 自测接口。
>
> ---

> 阶段：第三阶段 = 工作台增强（Agent 分类 / 定向可见性 / MCP 工具源）。**后端已全部实现 + 验收通过**（80/80 + 全回归零缺陷）并部署测试环境（DB version=50）。
> 定位：第三阶段前端的单一入口。续 `frontend-task-stage2.md`（第二阶段页面）。
> 唯一事实来源（开工前通读）：
> - `docs/frontend-api-reference.md` **§14.9 / §14.10 / §14.11**（字段/分页/错误码/鉴权，以此为准）
> - 设计契约：`docs/backend-stage3-agent-category-contract.md`、`backend-stage3-agent-visibility-contract.md`、`backend-stage3-mcp-integration-contract.md`
> - 接口总表/错误码速查：`docs/backend-stage2-master-tracking.md` §2.7

---

## 0. 角色边界与全局约定（必读）

**边界**：Codex 只写前端页面（`web/admin-console` / `web/user-console`）。后端接口已就绪，**只按 §14 契约调用，不设计后端逻辑**；发现接口缺失只列出来。

**全局约定**（同第二阶段）：
- 响应 `{ code, message, data }`，`code=0` 成功。
- 列表扁平分页 `data.{ items, page, page_size, total }`。
- 管理后台 `/api/admin/*` 需管理员登录 + 双重认证 + 对应权限码。
- 计费仅在模型 token 调用；Agent/Skill/插件/MCP 工具对用户**免费**，UI 无收费文案。

---

## 1. 第三阶段前端页面总览

| # | 页面/改动 | 控制台 / 负责人 | 契约 | 性质 |
|---|---|---|---|---|
| 1 | 工作台 **Agent 分类导航 + 筛选** | user-console / 前端乙 | §14.9 | 在第二阶段工作台 Agent 选用页上增强 |
| 2 | 管理端 Agent 配置加 **分类选择** | admin-console / 前端甲 | §14.10 | 在 Agent 配置页加字段 |
| 3 | 管理端 Agent **定向可见性配置** | admin-console / 前端甲 | §14.10 | 在 Agent 配置页加"可见范围"区 |
| 4 | 管理端 **MCP server 管理页** | admin-console / 前端甲 | §14.11 | 新页面（CRUD + discover + 工具审核 + 绑定） |

> 用户端无 MCP 配置入口（MCP 仅官方 Agent 可绑、仅运营管理）；用户端只是"用"绑了 MCP 的官方 Agent 对话，对话页（第二阶段 §14.8）无需改动——MCP 工具调用走同一套 SSE `tool_call`/`tool_result` 事件。

---

## 2. Agent 分类导航 + 筛选（user-console / 前端乙）

**目标**：工作台 Agent 选用页按分类（办公/学习/商务/娱乐）导航筛选。

**接口**：
- `GET /api/agent-categories`（登录态）→ `{ items: [{ code, name, icon, sort_order }] }`（仅 active、按 sort_order）
- `GET /api/agents?category=office`（在既有 Agent 列表基础上加 `category` 查询参数过滤）
- Agent 列表/详情响应新增 `category_code` + `category_name`（可在卡片显示分类标签）

**页面要素**：
- 顶部分类 Tab：用 `GET /api/agent-categories` 渲染（含"全部"项）；选中某类 → `GET /api/agents?category=<code>` 拉该类。
- Agent 卡片显示 `category_name` 标签。

**坑**：分类是展示维度，与"谁能看到"（可见性）无关；未分类 Agent `category_code=null`。

---

## 3. 管理端 Agent 配置：分类 + 定向可见性（admin-console / 前端甲）

在**已有的官方 Agent 创建/编辑页**（§14.10）上增加两块。

### 3.1 分类选择（§14.10）
- 表单加"分类"下拉：选项来自 `GET /api/admin/agent-categories`（管理端含 inactive 全量）。
- 提交时带 `category_code`（创建/编辑 Agent body）；空=未分类；非法 → 后端 40000。

### 3.2 定向可见性配置（§14.10，核心）
让运营设置某官方 Agent **给谁看**。可在 Agent 编辑页内联，也可用独立保存按钮。

- **字段**：`visible_scope`（`all` 全体 / `groups` 指定分组 / `roles` 指定全局角色）+ 对应目标：
  - `groups`：选 `group_ids`（多选，来自分组管理列表接口）+ 可选 `group_roles`（`admin` 组管理员 / `member` 普通组员，**留空=组内任意成员**）
  - `roles`：选 `role_codes`（多选，来自角色管理列表接口）
- **两种提交方式**（任选其一接入）：
  - 内联：随 Agent 创建/更新 body 带 `visible_scope` + `group_ids`/`group_roles`/`role_codes`
  - 独立端点：`PUT /api/admin/agents/{id}/visibility`，body `{ visible_scope, group_ids, group_roles, role_codes }`（**覆盖语义**）
- **回显**：AgentResp 含 `visible_scope` + `target_audience`（`{ group_ids, group_roles }` 或 `{ role_codes }`；scope=all 时为 null）。
- **数据源复用**：分组下拉用管理后台已有的"用户分组管理"列表接口；角色下拉用"角色权限管理"列表接口（前端甲已有这两个页面）。

**校验/错误（后端会拒，UI 做前置提示）**：
- scope=groups 但未选分组 → 40000；group_roles 含非 admin/member → 40000；scope=roles 但未选角色 → 40000；选了不存在的分组/角色 → 40000。

**交互建议**：`visible_scope=all` 时隐藏目标选择；切到 groups/roles 才显示对应多选；UI 上明确"全体可见 / 指定分组 / 指定角色"三态。

---

## 4. 管理端 MCP server 管理页（admin-console / 前端甲，新页面，§14.11）

**目标**：运营接入 MCP server（一个 server = 一批工具），发现并审核其工具，绑定到官方 Agent。需 `plugin:manage` + 双重认证。

**接口**：
- `GET /api/admin/mcp-servers`（列表，扁平分页）/ `GET /api/admin/mcp-servers/{id}`（详情）
- `POST /api/admin/mcp-servers`、`PATCH/DELETE /api/admin/mcp-servers/{id}`（CRUD）
- `POST /api/admin/mcp-servers/{id}/discover`（连接 server 拉取工具，写入快照，返回发现的工具）
- `GET /api/admin/mcp-servers/{id}/tools`（已发现工具列表）/ `PATCH /api/admin/mcp-servers/{id}/tools/{toolId}`（审核启用/停用单个工具）
- 绑定到 Agent：`POST /api/admin/agents/{id}/mcp-servers`，body `{ "ids": [..] }`（**覆盖语义**，仅官方 Agent）

**页面要素 / 流程**：
1. MCP server 列表 + 新建/编辑表单：`code / name / description / endpoint_url(https) / auth_config(凭证,只入不出) / timeout_ms / is_paid / daily_limit / status`。
2. 详情页"刷新工具"按钮 → 调 discover → 展示发现的工具（name/description/inputSchema），每个工具有**启用开关**（PATCH tools/{id}）。**新发现/定义变更的工具默认未启用，需运营审核启用后才会进 Agent 工具集**。
3. 在官方 Agent 配置页（§3）加"绑定 MCP server"多选（覆盖语义）。

**关键坑（重要）**：
- **凭证只入不出**：`auth_config` 创建/编辑时填，响应**永远不回**；UI 用 `has_auth`（bool）显示"已配置鉴权 ✓" + "重设凭证"入口（填空串清空）。**绝不回显凭证**。
- `endpoint_url` 必须 **https** 且非内网（后端 SSRF 校验，违反 40000），表单前置提示。
- **工具审核机制**：discover 只是"发现"，工具默认 `enabled=false`；必须运营在工具列表逐个启用才生效。server 改了工具定义会被标记需重新审核——UI 要能体现"待审核/已启用"状态。
- **仅官方 Agent 可绑 MCP**（用户自建不可绑，绑了后端 40003）。
- discover 失败（连不上/握手失败）→ 502，不改 server 状态；UI 给"发现失败"提示。
- 用户端 `GET /api/mcp-servers` 只回精简（无 endpoint/凭证）——本期用户端无需 MCP 页面（用户不配 MCP），可不接。

**错误码**：40000 校验/SSRF / 40900 code 重复 / 40400 不存在 / 40003 越权（绑用户自建）/ 502 discover 失败。

---

## 5. 关键坑总汇

1. 列表全是扁平分页 `{items,page,page_size,total}`。
2. **凭证只入不出**（MCP `auth_config` 同第二阶段插件）：UI 用 `has_auth` + "重设凭证"，绝不回显。
3. 绑定（MCP server / 可见性目标）都是**覆盖语义**：提交完整集合，不做增量 diff。
4. MCP 工具要**先 discover 再逐个审核启用**才会进 Agent；未启用不暴露给模型。
5. 可见性 `groups` 可再按组内角色（admin/member）细分；留空=组内任意。
6. 分类/可见性是两个正交维度；都不影响计费。
7. MCP/插件/工具对用户免费，UI 无收费文案。
8. 用户端对话页（§14.8）无需改动——MCP 工具走同一套 SSE 事件。

---

## 6. 自测对照

- 后端验收报告 `docs/backend-stage3-test-report.md`（三特性预期行为可对照）。
- 字段/错误码以 `frontend-api-reference.md` §14.9/14.10/14.11 与实现为准；不符反馈后端回写 §14。
