# 第三阶段功能验收报告

> 测试日期：2026-06-23 ｜ 测试工程师：QA
> 被测：第三阶段后端（main commit a8501c7，测试服务器 DB schema version=50）
> 环境：测试服务器 8.130.9.163:8080 / MySQL 13306（与本地开发端口一致）
> 验收基准：
> - `docs/backend-stage3-agent-category-contract.md`
> - `docs/backend-stage3-agent-visibility-contract.md`（v1.1）
> - `docs/backend-stage3-mcp-integration-contract.md`
> - `docs/frontend-api-reference.md` §14.9 / §14.10 / §14.11

---

## 测试结论：**通过**

三个特性 + 第二阶段回归全部通过，未发现 P0/P1/P2/P3 缺陷。MCP discover/tools_call 的「真实外呼端到端」因运行时 SSRF + TLS 限制无法经线上 HTTP API 触发（属环境限制，非缺陷，见 §3.7），已由后端 Go 集成测试（httptest stub + 真实测试库）覆盖该路径并实测通过。

**建议：允许第三阶段验收通过、合并上线。**

---

## 测试范围与统计

| 分组 | 用例脚本 | 通过/总数 | 结论 |
|---|---|---|---|
| 特性① Agent 分类 | `tests/test_s3_workbench.py`（feat1） | 全通过 | 通过 |
| 特性② Agent 定向可见性 | `tests/test_s3_workbench.py`（feat2） | 全通过 | 通过 |
| 特性③ MCP 接入 | `tests/test_s3_workbench.py`（feat3） | 全通过 | 通过 |
| 第三阶段新端点权限/鉴权 | `tests/test_s3_workbench.py`（feat_perm） | 全通过 | 通过 |
| **S3 HTTP API 合计** | `tests/test_s3_workbench.py` | **80/80** | 通过 |
| MCP 协议/编排 Go 集成（含 e2e 回灌） | `server/.../service/*_test.go` | 全 PASS | 通过 |
| 回归 M1（Token 套餐 / sk 鉴权 / 封禁） | `tests/test_s2_m1_token_sk.py` | 44/44 | 通过 |
| 回归 M2（预付计费 / 三计费 / 并发） | `tests/test_s2_m2_prepaid_billing.py` | 75/75 | 通过 |
| 回归 M3（CRUD / 编排 tool-use / 插件日限） | `tests/test_s2_m3_workbench.py` | 84/84 | 通过 |
| 回归 M4（整合 / 越权 / 凭证安全） | `tests/test_s2_m4_integration.py` | 62/62 | 通过 |

---

## 特性① Agent 分类（全通过）

- `GET /api/agent-categories`（登录态）：仅 active、按 `sort_order` 升序返回 4 类（office/study/business/entertainment），数量与 DB active 分类数一致；插入的 inactive 分类不出现在用户端。
- `GET /api/admin/agent-categories`：含 inactive 全量。
- admin 建 Agent 带 `category_code=office` → 成功且 `AgentResp` 回显 `category_code=office` + `category_name=办公`。
- 非法 `category_code`（建 / PATCH）→ `40000`。
- 不传 `category_code` = 未分类（`category_code=null`，`category_name=""`）；`PATCH category_code=""` 清为未分类。
- 用户自建 Agent 带 `category_code=study` → 成功回显（契约允许自建选分类）；自建非法分类 → `40000`。
- `GET /api/agents?category=office` 仅返回 office 分类且含目标 Agent；`?category=study` 不混入 office。
- `GET /api/admin/agents?category=office` 过滤正确。

## 特性② Agent 定向可见性（全通过，三处过滤 + 写入校验）

准备：建 2 个分组（g1 含 member/admin 不同组内角色用户、g2 无关组），给用户配全局角色。

- **scope=groups（不限组内角色）**：组内成员/管理员列表可见、组外用户不可见；组外用户详情 → `40003`、chat → `40003`；组内用户详情 200。
- **scope=groups + group_roles=[admin]**：仅组管理员列表可见，普通组员不可见；普通组员详情/chat → `40003`；组管理员详情 200。
- **scope=roles**：命中全局角色用户可见、未命中不可见；未命中详情/chat → `40003`；命中详情 200。
- **scope=all（回归）**：对组外/无角色用户全员可见，详情 200；本人自建恒可见（特性① 已覆盖自建可见）。
- **三处访问控制红线全部验证**：列表 `GET /api/agents`、详情 `GET /api/agents/{id}`、编排 `POST /api/agents/{id}/chat`——越权用户直连不可见 Agent 的详情/chat 均返回 `40003`（列表过滤 ≠ 访问控制，三处独立校验已落实）。
- **PUT `/api/admin/agents/{id}/visibility`（覆盖语义）**：all→groups 覆盖后组外用户由可见变不可见、组内可见；再覆盖回 all 后组外用户重新可见，`target_audience` 置 `null`。
- **写入侧校验（均 `40000`）**：`visible_scope` 非法（含预留 members）/`groups` 但 group_ids 空/group_roles 含非 admin·member/`roles` 但 role_codes 空/group_ids 含不存在分组/role_codes 含不存在角色；`PUT /visibility` 同款校验生效。

## 特性③ MCP 接入（全通过）

- **server CRUD 凭证不回**：建 server 带 `auth_config`，响应仅 `has_auth=true`，不回凭证明文，不含 `auth_config`/`auth_config_encrypted` 内部字段；GET 详情同样不泄漏。新建默认 `status=inactive`。
- **endpoint SSRF 校验 → `40000`**：非 https(http://) / localhost / 127.0.0.1 / 10.x 私网 / *.internal 全部拒绝。
- **code 重复 → `40900`**。
- **用户端 `GET /api/mcp-servers`**：仅 active server 精简视图（id/code/name/description/is_paid），不回 endpoint_url/has_auth/凭证。
- **仅官方 Agent 可绑 MCP**：绑官方 Agent → `{bound:true}`，解绑（ids=[]）成功；绑用户自建 Agent → `40003`。
- **不存在的 server → `40400`**。
- **discover 失败处理**：endpoint 指向不可达公网域名 → `502`/`50200`（连接/握手失败），且**不改 server 状态**（仍 inactive）。
- **删除级联**：DELETE server 后工具快照（`mcp_server_tools`）级联清空。

### 3.7 MCP discover + tools/call 真实端到端 —— 环境卡点说明（非缺陷）

> 该路径无法经测试服务器线上 HTTP API 触发，原因为运行时安全策略，**已确认是环境/安全限制而非缺陷**，并已由代码侧集成测试覆盖。

**卡点：**
1. **运行时 SSRF（`resolveDNS=true`）**：MCP client 每次外呼前调用 `security.ValidateOutboundURL(..., resolveDNS=true)`，强制 https 且解析后的所有 IP 必须为公网，回环/私网（127.0.0.1、10.x、192.168.x、172.x）一律拒绝。测试主机本地接口均为私网地址（192.168.29.32 / 172.x），本地 stub 无法被 API 进程访问。
2. **TLS 校验**：MCP client 使用标准 `http.Client`，**无 `InsecureSkipVerify`**，自签证书的本地/公网 stub 会在 TLS 握手阶段失败。
3. 生产恒 `skipSSRF=false`，无运行时绕过开关；`PLUGIN_DOMAIN_WHITELIST` 为空（不限定域名，但仍受上述 1/2 约束）。

要在测试主机跑通真实 discover+tools_call，需要一个「公网可解析 + 有效 CA 证书 + 实现 MCP JSON-RPC 的 HTTPS server」，测试环境不具备该条件。

**代码侧覆盖（已实测通过）：** `server/internal/modules/workbench/service/` 下 Go 集成测试用 `httptest` stub + 测试客户端（绕过 SSRF）覆盖完整 MCP 协议与编排回灌路径，对测试库（8.130.9.163:13306）执行全部 PASS：

| 测试 | 覆盖点 | 结果 |
|---|---|---|
| `TestMCPClient_DiscoverFlow` | initialize → notifications/initialized → tools/list | PASS |
| `TestMCPClient_ListToolsPagination` | tools/list 分页（nextCursor 循环取全） | PASS |
| `TestMCPClient_CallTool` | tools/call 调用 + result.content 解析 | PASS |
| `TestMCPClient_CallToolError` | JSON-RPC error 归一 | PASS |
| `TestMCPClient_HTTPNon2xx` | HTTP 非 2xx 归一 | PASS |
| `TestMCPClient_SSEFraming` | Streamable HTTP SSE 帧解析 | PASS |
| `TestMCPClient_RuntimeSSRFBlocked` | 运行时 SSRF 拦内网 | PASS |
| `TestMCPDiscoverAndAudit` | discover 写快照 + schema_hash + 默认未启用待审 | PASS |
| `TestMCPServerCRUD_NoCredentialLeak` | CRUD 凭证不泄漏 | PASS |
| `TestMCPBindOnlyOfficial` | 仅官方 Agent 可绑 | PASS |
| `TestMCPPublicListNoEndpoint` | 用户端不回 endpoint | PASS |
| **`TestMCPOrchestrationIntegration`** | **discover→审核 enabled→绑官方 Agent→编排命中 `mcp__{code}__{tool}`→tools/call→结果回灌** | **PASS** |
| `TestMCPPaidDailyLimit` | 付费 MCP 走 `tool_daily_call_logs` mcp 维度日限 | PASS |

`TestMCPOrchestrationIntegration` 即契约要求的端到端：discover 写快照（默认未启用）→ 审核 enabled → 绑到官方 Agent → 编排触发 MCP 工具（命名空间 `mcp__{server_code}__{tool_name}`）→ 工具调用结果回灌模型，全部断言通过。

## 第二阶段回归（全通过，确认未破坏）

第三阶段改动（chat_service 可见性接入 + 限流收口至 `tool_daily_call_logs`）未破坏既有功能：

- M1（44/44）：Token 套餐、sk 鉴权（postpaid/prepaid）、封禁吊销。
- M2（75/75）：预付计费、三计费（input/output tokens 按量 + calls 按次）、并发扣费安全、错误码区分（60001 余额不足 vs 50301）。
- M3（84/84）：Skill/Plugin/Agent CRUD、编排 tool-use（SSE 事件齐全 + 回灌）、整次提问 calls=1、**付费插件日限**（收口改用 `tool_daily_call_logs` 后行为不变：超 daily_limit 当轮回灌「已达上限」、对话不中断）。
- M4（62/62）：整合、越权（admin 端 403/40003）、Agent 详情不泄漏插件凭证、未登录编排端点 401。

---

## 缺陷表

| 编号 | 等级 | 模块 | 复现 | 期望 vs 实际 | 状态 |
|---|---|---|---|---|---|
| — | — | — | 无缺陷 | — | — |

> 无 P0 / P1 / P2 / P3 缺陷。

---

## 备注

- 测试脚本入库：`tests/test_s3_workbench.py`（80 用例，HTTP API 黑盒）；MCP 真实外呼 e2e 由 `server/internal/modules/workbench/service/` 下 Go 集成测试覆盖（`RUN_DB_TESTS=1` + 测试库）。
- 测试数据：在测试库临时插入 1 条 inactive 分类（`s3inact_<UNIQ>`）用于「用户端不含 inactive」对照；分组/角色/agent 均带 `s3*_<UNIQ>` 前缀，不影响既有数据。
- 凭据全部走环境变量，脚本不硬编码真实密钥。
