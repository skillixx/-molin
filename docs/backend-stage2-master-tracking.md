# 第二阶段总控：设计流程 + 需求接口设计 + 任务完成度

> 状态：总控追踪 v1（2026-06-21）
> 阶段：第二阶段（Week 5–9）= Token 转发售卖 + 多模型聊天工作台
> 定位：把第二阶段全部规划文档串成一张「流程 + 接口 + 完成度」总控视图，作为开发/验收的单一追踪入口。
> 文档族：
> - 纲领：`backend-stage2-architecture-roadmap.md`
> - 契约：`backend-sk-auth-contract.md`、`backend-token-billing-contract.md`、`backend-chat-workbench-contract.md`
> - 前端：`frontend-api-reference.md` §14
> - 排期：`backend-stage2-task-schedule.md`
> 图例：✅ 已实现并合并 main ｜ 🚧 进行中 ｜ 🔜 待实现（规划就绪，未开发）

---

## 一、设计流程

### 1.1 总流程（需求 → 上线）

```
① 需求澄清        产品蓝图 + 4 项范围决策（概念分层/多模态/计费/创建权）        ✅ 完成
        │
② 架构规划        roadmap v2：两条能力 + 三层免费 + 唯一 token 收费 + 里程碑      ✅ 完成
        │
③ 接口契约        sk / 计费 / 聊天工作台 三契约 + 前端 §14；数据模型 + 错误码      ✅ 完成
        │
④ 评审定稿        PM 两轮 review：错误码冲突修正 + 16 项决策回填 + 排期减压       ✅ 完成（PR #202/#203/#204 合并）
        │
④.5 风险审查      架构师风险复盘 12 项 + PM 拍板 D1–D6                          ✅（PR #205）
                 （并发透支/sk编排边界/插件配额/code_exec/entitlement幂等/渠道单点）
        │
④.6 契约回写      D1–D6 决议吸收进各契约（billing/sk/chat-workbench/roadmap）+ 调排期   ✅（PR #206）
        │
⑤ 实现           各后端按契约开发（迁移 → model/repo/service → handler → 装配）   🚧 进行中：M1✅验收通过，M2✅验收通过，M3✅后端全完成（待前端+测试验收）
        │
⑥ 联调           前端按 §14 对接 + 后端接口就位；字段变更回写契约                🔜
        │
⑦ 测试验收        逐 M 用例 + 并发无负余额(硬) + tool-use 多轮 + 端到端           🔜
        │
⑧ 上线           上线检查单（环境变量/迁移序/配置项/回滚）→ PM 验收 → 发布        🔜
```

**当前位置**：①–⑦ 全部完成；⑧ 上线审批中——**M1 验收通过（真实上游 44/44）、M2 验收通过（75/75，方案 B 根治白嫖）、M3 验收通过（85/85）、M4 整合验收通过（266/266，无 P0–P3 缺陷）**。**S2-PM1 第二阶段业务验收：有条件通过（go，2026-06-22，见 §3.6）**，6 条业务红线全部达成。上线前置条件：运维注入 5 个密钥/配置（含 INTERNAL_API_TOKEN，prepaid 硬依赖）+ 运营配置渠道/模型目录数据。**后端 20/24 接口就绪，余 4 项为前端页面（Codex 范围，非后端缺口）；前端页面 + 运维 .env 注入为合并后待办，已登记。**

### 1.2 单个接口的开发流程（每位工程师统一遵循）

```
读契约(§对应章节) → 建迁移(必要时,建权限码必带 seed) → model/repo/service
 → handler/route → bootstrap 装配 + 冒烟 → 自测 → 同步 frontend-api-reference §14
 → 提 PR(feature 分支,中文 commit) → CI + 评审 → PM 确认 → merge
```

### 1.3 计费决策流程（运行时）

```
请求(sk/登录态) → 鉴权(甲) → 门禁:持有 token 权益?(丙)
 → 计费模式? postpaid→钱包余额闸 + 预扣保证金(D1,冻结 max_tokens×单价,乙) / prepaid→套餐额度闸(丙)
 → 转发上游(丁,选渠道/换 key/模型名;SSE 不缓冲)
 → 读 usage(SSE 断开仍读完上游再结算,R5) → 写 token_usage_logs
 → 结算: 按量(input/output tokens) + 按次(calls,一次提问计1)
     postpaid → 解冻保证金 + 实扣钱包(乙,多退少补) ｜ prepaid → entitlement-consume(丙,幂等表去重 D5)
```
> 计费模式由 sk 上下文决定；编排端点 `/api/agents/{id}/chat` 仅登录态（D2，sk 不可调）。渠道本期单点（D6，故障转移延后第三阶段）。

---

## 二、需求接口设计（总表）

> 鉴权列：`登录态`=JWT；`sk`=平台 API Key；`管理`=对应权限码 + 管理员双重认证。计费仅发生在模型调用接口。

### 2.1 Token 网关 — 管理端（渠道/模型目录）

| # | 方法 路径 | 鉴权 | 状态 | 契约 |
|---|---|---|---|---|
| 1 | `GET/POST /api/admin/token/channels` | 管理(token:manage) | ✅ | §14.5 |
| 2 | `GET/PATCH/DELETE /api/admin/token/channels/{id}` | 管理 | ✅ | §14.5 |
| 3 | `GET/POST /api/admin/token/models` | 管理 | ✅ | §14.6 |
| 4 | `GET/PATCH/DELETE /api/admin/token/models/{id}` | 管理 | ✅ | §14.6 |
| 5 | `GET /api/admin/token/usage` | 管理 | ✅ S2-丁2（PR #212） | §14.7 |

### 2.2 Token 网关 — 用户端 / 开发者

| # | 方法 路径 | 鉴权 | 状态 | 契约 |
|---|---|---|---|---|
| 6 | `GET /api/token/models` | 登录态/sk | ✅（定向可见性 PR #273：按 `visible_scope` all/groups/roles 过滤列表，migration 000052） | §14.1 |
| 7 | `POST /api/token/chat/completions`（纯透传，SSE） | 登录态/sk | ✅（sk 路径已接入 PR #214/#215；不可见模型转发前置闸 PR #273，按「模型不可用」拒绝不泄漏存在性） | §14.2 |
| 8 | `GET /api/token/usage` | 登录态/sk | ✅ S2-丁1（PR #212） | §14.3 |

### 2.3 平台 sk 鉴权（后端甲）

| # | 方法 路径 | 鉴权 | 状态 | 契约 |
|---|---|---|---|---|
| 9 | `POST /api/keys`（创建，明文只回一次） | 登录态 | ✅ S2-甲4（PR #214） | §14.4 / sk 契约 |
| 10 | `GET /api/keys`（列本人，只回 prefix） | 登录态 | ✅ S2-甲4（PR #214） | §14.4 |
| 11 | `DELETE /api/keys/{id}`（吊销，越权 40003） | 登录态 | ✅ S2-甲4（PR #214） | §14.4 |
| — | `RequireUserAuth` 双模式中间件 + `ResolveKey` 内部 | — | ✅ S2-甲3（PR #214） | sk 契约 §5 |
| — | IssueKey 支持 prepaid + source_id（绑套餐 entitlement） | — | ✅ S2-甲6（PR #225，ResolveKey 带出 billing_mode/source_id） | sk 契约 §5 |
| — | 按 sk 限流 | — | 🔜 S2-甲8（M2） | roadmap §9 #2 |

### 2.4 计费（后端乙/丙/丁）

| # | 能力 | 归属 | 状态 | 契约 |
|---|---|---|---|---|
| 12 | 按量计费（input/output tokens → 钱包） | 乙+丁 | ✅（含 seed 000033 + `POST /api/internal/product-usage-events`） | billing §2 |
| 13 | 按次计费规则 seed（calls/count）+ 门面次数事件 | 乙+丁 | ✅ S2-乙1/丁4（PR #210/#215） | billing §3 |
| 14 | 套餐商品 + plan（quota_json + valid_days） | 乙 | ✅ S2-乙3（PR #223，000037 token-pkg-1m） | billing §4.1 |
| 15 | `POST /api/internal/entitlement-consume`（锁行+有效期；幂等用 **D5 新表 `entitlement_consume_logs`**） | 丙 | ✅ S2-丙2（PR #226，60005/40003，100并发无超扣） | billing §4.2 |
| 16 | token_quota entitlement 生成（TokenProvisioner 套餐分支） | 丙 | ✅ S2-丙1（PR #226，含 valid_days→expires_at） | billing §4.2 |
| 17 | 门面计费路由（postpaid/prepaid 互斥 + 余额闸 + **D1 预扣保证金**：postpaid 冻结 max_tokens×单价，结算多退少补，复用钱包 freeze） | 丁 | ✅ S2-丁5（PR #227，三红线通过；prepaid 休眠待甲6b 激活） | billing §4.3 |
| — | 钱包 `freeze/unfreeze` + `WalletHoldService`（**D1 前置**，预扣保证金能力） | 乙 | ✅ S2-乙0（PR #208，门面编排待 S2-丁5/M2） | billing §4.3 |

### 2.5 聊天工作台 — Agent/Skill/插件（后端丁，全免费）

| # | 方法 路径 | 鉴权 | 状态 | 契约 |
|---|---|---|---|---|
| 18 | `GET/POST /api/admin/agents` (+ `/{id}/skills`、`/{id}/plugins` 绑定) | 管理(agent:manage) | ✅ S2-丁8（PR 待提） | §14.10 |
| 19 | `GET/POST/PATCH/DELETE /api/admin/skills` | 管理(skill:manage) | ✅ S2-丁8 | §14.10 |
| 20 | `GET/POST/PATCH/DELETE /api/admin/plugins`（凭证不回；**D3 加 `is_paid`/`daily_limit` 字段**） | 管理(plugin:manage) | ✅ S2-丁8（has_auth + SSRF 前置校验） | §14.10 |
| 21 | `GET /api/agents`、`GET /api/agents/{id}` | 登录态 | ✅ S2-丁9 | §14.9 |
| 22 | `POST/PATCH/DELETE /api/agents`（自建，越权 40003） | 登录态 | ✅ S2-丁9 | §14.9 |
| 23 | `GET /api/skills`、`GET /api/plugins`（供自建绑定） | 登录态 | ✅ S2-丁9 | §14.9 |
| 24 | `POST /api/agents/{id}/chat`（tool-use 编排，SSE） | **仅登录态**（D2，sk 不可调） | ✅ S2-丁10（commit 52c4d25） | §14.8 / 工作台契约 §4 |

### 2.6 数据库迁移（序号以实际合并顺序为准）

| 迁移 | 内容 | 状态 |
|---|---|---|
| 000030–000033 | token_models/usage_logs、channels+路由、token:manage seed、token 商品+按量规则 | ✅ |
| 000034 | api_keys | ✅ S2-甲1（PR #207） |
| 000035 | wallet_holds（预扣保证金 hold 表，D1） | ✅ S2-乙0（PR #208） |
| 000036 | 按次计费规则 seed（calls） | ✅ S2-乙1（PR #210） |
| 000037 | token 套餐 plan seed（token-pkg-1m） | ✅ S2-乙3（PR #223） |
| 000038–000040 | agents+绑定表 / skills / plugins（plugins 含 D3 `is_paid`/`daily_limit`） | ✅ S2-丁6（PR #224） |
| 000041 | entitlement_consume_logs（D5 幂等表，idempotency_key 唯一） | ✅ S2-丙2（PR #226） |
| 000043 | 权限码 seed（agent/skill/plugin:manage，绑定 admin 角色） | ✅ S2-甲7（M3，commit 097cbbd） |
| 000044 | plugin_daily_call_logs（付费插件每用户每日调用计数，D3 限流） | ✅ S2-甲9（commit 52c4d25） |
| 0000XX | **`entitlement_consume_logs`（D5 幂等表，idempotency_key 唯一）** | 🔜 S2-丙2（M2，序号紧随套餐相关迁移） |

> **迁移序号铁律**：golang-migrate 不支持 out-of-order——序号严格按**合并顺序**递增、**不留空号**（曾因 wallet_holds 预留空号导致 gap，已修正为连续）。上表 000036 起为预估，实际以合并顺序为准。

> **渠道路由（D6）**：本期 `token_models` 仍绑**单一渠道**（`forward_service` 单点，`token_channels.priority` 建而未用）；多渠道故障转移+熔断**延后第三阶段**，已在 roadmap §9 显式登记，验收不当 bug。

### 2.7 错误码（第二阶段相关）

| code | 含义 | 备注 |
|---|---|---|
| 40300 | 未开通 token 服务 / 模型越界 | chat 专用 |
| 50200 / 50300 | 上游失败 / 渠道不可用 | chat 专用 |
| 60001 | 钱包余额不足 | 既有 |
| 60005 | 权益额度不足（含套餐额度耗尽） | **复用，禁用 60002（=重复支付）** |
| 40003 | 无权限（含 sk 越权吊销） | **不用 40004** |

### 2.8 环境变量 / 配置项（运维）

| 项 | 用途 | 状态 |
|---|---|---|
| `TOKEN_PROVIDER_KEY` | 渠道 api_key AES-256-GCM | ✅ 已用 |
| `API_KEY_HMAC_SECRET` | sk 的 HMAC 存储密钥 | ✅ S2-甲5/运1（.env.example PR #218；测试环境已设） |
| `PLUGIN_SECRET_KEY`（或复用 `TOKEN_PROVIDER_KEY`） | 插件凭证 AES-256-GCM 加密 | ✅ 代码就绪 S2-丁7（config 已读取，未配置时回退复用 `TOKEN_PROVIDER_KEY`；32 字节，未配则工作台不装配）；运维注入待 S2-运2 |
| `MAX_ROUNDS`（默认 5） | tool-use 编排最大轮数 | ✅ 代码就绪 S2-丁10（config 已读取，默认 5）；运维注入待 S2-运2 |
| `PLUGIN_DOMAIN_WHITELIST`（逗号分隔，可空） | 插件/skill 外呼域名白名单（SSRF） | ✅ 代码就绪 S2-甲9（空=仅按网段拦内网）；运维注入待 S2-运2 |

---

## 三、任务完成度列表

### 3.1 规划阶段 — ✅ 全部完成（已合并 main）

- [x] 产品蓝图澄清 + 4 项范围决策
- [x] roadmap v2（含 16 项 PM 决策回填）
- [x] sk 鉴权对接契约（v1.1，支持 prepaid）
- [x] 计费对接契约（按量+按次+套餐）
- [x] 聊天工作台对接契约（Agent/Skill/插件 + tool-use 编排）
- [x] 前端契约 `frontend-api-reference.md` §14
- [x] 任务排期（Week 5–9，PM 两轮 review 优化，PR #203 合并）
- [x] 本总控追踪文档

### 3.2 已实现能力（第一砖，PR #188–#201）— ✅

- [x] token_models / token_usage_logs / token_channels + 路由（000030–000031）
- [x] 渠道 CRUD + 模型目录 CRUD（管理端，token:manage）
- [x] `GET /api/token/models`、`POST /api/token/chat/completions`（纯透传 + SSE）
- [x] 按量计费（000033 seed + product-usage-events 上报 → 扣钱包）
- [x] TokenProvisioner（按量分支）+ 资产门禁
- [x] 前端（Codex）：用户端对话页、管理端 Token 配置页

### 3.2.5 契约回写（④.6，D1–D6 决议吸收）— ✅ 完成（PR #206）

> 架构师已把 D1–D6 写入各契约（见 risk-review §5/§6）。

- [x] D1 → billing §4.3 + sk §9：预扣保证金方案 + 钱包 freeze 依赖
- [x] D2 → chat-workbench §3.3 + §14.8 + roadmap §6：编排端点改「仅登录态」
- [x] D3 → chat-workbench §2/§5：付费插件每日上限 + 限流 + `plugins` 表 `is_paid`/`daily_limit`
- [x] D4 → chat-workbench §4：移除 code_exec 示例（改 web_search/doc_read）
- [x] D5 → billing §4.2：`entitlement_consume_logs` 建表 SQL + 迁移（丙）
- [x] D6 → roadmap §9 #17 + §11：渠道单点风险登记 + 第三阶段任务清单
- [x] 据 D1–D6 调整 `backend-stage2-task-schedule.md`（W5 加 S2-乙0 等）

### 3.3 实现阶段 — 🚧 进行中（M1✅完成；M2/M3 待开。按周/工程师，勾选追踪）

**W5 · M1 Token 售卖闭环 — ✅ 全部完成并验收通过（2026-06-21）**
- [x] S2-乙0 确认钱包 `freeze/unfreeze` 内部接口可供门面调用（补 WalletHoldService，PR #208）
- [x] S2-甲1 迁移 000034 api_keys + model（PR #207）
- [x] S2-甲2 APIKeyService（Issue/Resolve/Revoke/List，PR #211）
- [x] S2-甲3 RequireUserAuth 双模式中间件 + APIKeyIDFromContext（PR #214）
- [x] S2-甲4 `/api/keys` 管理路由 + 封禁联动（PR #214）
- [x] S2-甲5 config 注入 API_KEY_HMAC_SECRET（PR #214）
- [x] S2-乙1 迁移 000036 按次计费规则 seed（PR #210）
- [x] S2-乙2 管理端按量/按次互斥强校验（PR #213）
- [x] S2-丁1 `GET /api/token/usage`（用户端，PR #212）
- [x] S2-丁2 `GET /api/admin/token/usage`（管理端，PR #212）
- [x] S2-丁3 chat 三接口换 RequireUserAuth + 写 api_key_id（PR #215）
- [x] S2-丁4 门面按次事件上报（前置失败不计次，PR #215）
- [x] S2-丁4b model_scope 越界校验（→40300，PR #217）
- [x] S2-运1 .env.example 补 API_KEY_HMAC_SECRET（PR #218）+ 测试库迁移到 000036 + 部署最新二进制
- [x] S2-测1 M1 端到端验收（PR #219，真实上游 44/44 全过；P3 sale_amount 见下）

**W6 · M2 套餐预付**
- [x] S2-乙3 token 套餐 plan（quota_json + valid_days）+ 售价（PR #223，000037）
- [x] S2-丙1 套餐分支生成 token_quota entitlement（PR #226，确认 Provisioner 放行 + 修 valid_days→expires_at）
- [x] S2-丙2 `POST /api/internal/entitlement-consume`（锁行+有效期，不足 60005；幂等用 D5 新表 `entitlement_consume_logs` + 迁移 000041）（PR #226）
- [ ] S2-丙3 内部余额查询（门面前置闸）— 契约 §4.2 C 列为可选；门面前置闸可暂复用「我的权益额度」，确需再补
- [x] S2-甲6 IssueKey 支持 prepaid + source_id（PR #225）
- [ ] S2-甲8 按 sk 限流（D3：插件调用计入限流维度）
- [x] S2-丁5 门面计费路由（postpaid/prepaid 互斥 + 余额闸 + D1 预扣保证金/解冻 + R5 读完上游再结算 + 回填 sale_amount 修 P3）（PR #227）
- [x] S2-甲6b auth 补 `BillingByID(apiKeyID)→(mode,sourceID,ok)` + bootstrap 注入 billingResolverAdapter，**prepaid 代码已点亮**（PR #228，postpaid/登录态零回归）。⚠️ 实扣生效还需运维部署新二进制 + 注入 4 个环境变量
- [x] S2-丁6（前置）迁移 000038–000040 agent/skill/plugin 建表（PR #224，plugins 含 D3 is_paid/daily_limit）
- [ ] S2-前乙1 sk 管理页 + 用量页（§14.4/14.3）
- [ ] S2-前乙2 token 套餐购买页
- [ ] S2-前甲1 管理端全量用量页（§14.7）
- [x] S2-测2 M1 回归 + M2 用例（并发额度无超扣）✅ 75/75 全绿（方案 B 根治 D-M2-01）

**W7 · M3 工作台（上）**
- [x] S2-甲7 权限码 seed **000043**（agent/skill/plugin:manage）✅ 本地 up/down/重 up 幂等验证（commit 097cbbd）
- [x] S2-丁7 三模块 model/repo/service + bootstrap 装配 ✅（新建 `workbench` 模块，commit 66a2df7）
- [x] S2-丁8 管理端 CRUD（agents/skills/plugins + 绑定，凭证不回）✅（绑定覆盖语义 `{ids}`；插件凭证 AES-GCM 加密、响应仅 `has_auth`；endpoint SSRF 前置校验）
- [x] S2-丁9 用户端 Agent 列表/详情/自建/绑定 + skills/plugins 列表 ✅（自建仅可绑 active 官方资源；越权 40003；§14.9/14.10 已回写）
- [ ] S2-前甲2 管理端 Agent/Skill/插件配置页（§14.10）— 待 Codex
- [ ] S2-前乙3 工作台 Agent 选择 + 自建页（§14.9）— 待 Codex
- [ ] S2-测3 三模块 CRUD + 自建越权用例（后端已附 3 项 DB 集成测试 `workbench_db_test.go`，待测试工程师端到端回归）

**W8 · M3 工作台（下）tool-use 编排**
- [x] S2-丁10（关键路径）`POST /api/agents/{id}/chat` 编排循环（D2 仅登录态）✅（commit 52c4d25；ForwardService.ChatOnce 复用转发器，SSE tool_call/tool_result/message/[DONE]）
- [x] S2-丁11 skill 内置函数注册表 ✅（doc_read 真实抓取+SSRF / web_search 占位降级；**D4 不含 code_exec**）
- [x] S2-甲9（协丁）plugin HTTP 转发器 ✅（运行时 SSRF 解析 DNS+白名单 / 超时 / 凭证解密注入 / 连续失败熔断 / D3 付费日上限原子计数，迁移 000044）
- [x] S2-丁13 编排计费接入 ✅（每轮实计 token；calls 仅首轮，整次提问计 1，skipCallBilling 零回归既有 Forward）
- [x] S2-丁14 bootstrap 总装配收口 + 冒烟 ✅（编排端点装配；token 网关未启用则不注册；路由 401 鉴权闸验证）
- [ ] S2-前乙4 聊天对话页（SSE + tool_call/tool_result 事件，§14.8）— 待 Codex
- [~] S2-运2 配置项注入（MAX_ROUNDS / 白名单 / PLUGIN_SECRET_KEY）— **代码就绪**（config 已读取 MAX_ROUNDS/PLUGIN_DOMAIN_WHITELIST/PLUGIN_SECRET_KEY），运维注入 .env 待办
- [ ] S2-测4 编排用例（多轮 / 超限 / SSRF / 计费正确）— 后端已附 DB 集成测试（编排首/次轮、MAX_ROUNDS、付费日上限）+ SSRF 单测，待测试工程师端到端回归

**W9 · M4 整合验收 — ✅ 全部完成（2026-06-22）**
- [x] S2-测5（前半）端到端 + 三计费 + tool-use + 并发无负余额(硬) ✅ M4 整合 62/62 + 三子里程碑回归 204/204 = 266/266 全绿（`docs/backend-stage2-m4-test-report.md`）
- [x] S2-测6（前半）缺陷跟踪表 P0–P3 ✅ 无 P0/P1/P2/P3 缺陷，无需回 S2-各1 修复
- [x] S2-各1（后半）按缺陷修复回归（两轮 QA 闭环）✅ 无缺陷，跳过（M4 报告 §S2-测6）
- [x] S2-运3 backend-stage2-go-live-checklist.md ✅（PR #240，main `3d9251f`）
- [x] **S2-PM1 第二阶段业务验收 ✅ 有条件通过（2026-06-22，见 §3.6）**

### 3.4 完成度统计

| 阶段 | 完成 / 总数 |
|---|---|
| 规划阶段 | 8 / 8 ✅ |
| 已实现能力（第一砖） | 6 / 6 ✅ |
| 风险审查 + D1–D6 决议 | ✅（PR #205） |
| 契约回写（④.6） | 7 / 7 ✅（PR #206） |
| **M1 Token 售卖闭环（W5）** | **15 / 15 ✅ 验收通过（真实上游 44/44，PR #207–219）** |
| 实现阶段任务（S2-xx 总） | 25 / 47（M1 全完成；M2 验收通过；M3 后端全完成：甲7/9 + 丁7~14；余前端页面 + 运维注入 + 测试验收） |
| **接口总数** | 已实现 20 项 / 待实现 4 项（共 24；全部后端接口就绪，余前端页面 + 测试） |

### 3.4.5 S2-测2 验收（2026-06-22）— M2 套餐 + 并发，发现 3 缺陷（2 P1）

> 45 项：42 过 / 3 缺陷。M1 回归 12/12、postpaid 单发 5/5、M2 套餐核心 10/10、幂等通过；并发无负余额/无超扣（硬验收）通过。测试脚本 `tests/test_s2_m2_prepaid_billing.py`（待回归通过后入库）。**3 缺陷未全修前 M2 不上线。**

| 缺陷 | 等级 | 模块 | 状态 |
|---|---|---|---|
| D-M2-03 钱包并发漏扣 + 保证金 hold 泄漏（根因：GORM v2 下 FOR UPDATE 行锁失效 + 乐观锁无重试） | P1 | billing | ✅ 已修（PR #229，改 clause.Locking + RetryOnVersionConflict） |
| D-M2-02 freeze 撞版本号被误当余额不足返 60001 | P2 | billing+门面 | ✅ 已修并回归通过（#229+#231）：余额充足并发无 60001/50301 误报；余额不足组全 60001 不混 50301 |
| D-M2-03 钱包并发漏扣 + 保证金 hold 泄漏 | P1 | billing | ✅ 已修并回归通过（#229）：并发10次 净扣=consume流水=sale_amount 三方账实一致；frozen 归零、holding 残留=0、无漏扣无负余额 |
| D-M2-01 prepaid 额度耗尽仍 200+免费答案 | P1 | token_gateway | ✅ **方案 B 根治并回归通过（#232+#233）**：丙4 entitlement 预占能力（reserve/settle/release + 迁移 000042 + quota_reserved 列）+ 丁门面 prepaid 改 reserve→settle/release。串行低余额(quota=20)后5次全 60005、quota_used 不增；并发(quota=48,N=10)精确 3 次成功，7 次 60005，无超扣；quota_reserved=0 无泄漏。 |

> **三缺陷全部闭环 ✅ S2-测2 75/75 全量通过（方案 B 根治 D-M2-01）。** 测试环境：main `c8dee4e`（含 #232/#233），DB 000042，dirty=0。
> M1 回归 12/12、postpaid 预扣 5/5、M2 套餐核心全通过、并发硬验收（prepaid 无超扣、postpaid 账实相符）全通过。无遗留 P0/P1。
> **M2 验收建议通过，可进入上线审批。**
> 回归脚本 `tests/test_s2_m2_prepaid_billing.py` + 报告 `docs/backend-stage2-m2-test-report.md` 已入库（commit 7a4e334，feature/test-s2-m2-regression）。
> 新增业务码 **50301**（系统繁忙/可重试，HTTP 503）已登记 full-api-design §1.4 + frontend §14。

### 3.5 已知跟进项（M2 顺手修 / 后续）

- ~~**[P3] `token_usage_logs.sale_amount` 恒为 0**~~ **✅ 已修（S2-丁5 / PR #227）**：结算后回填，postpaid=实扣金额(CNY)、prepaid=实扣额度(token 数)。**注意双量纲**：展示侧需按 billing_mode 标注单位（前端用量页对接说明待补）。
- **§14.1 `GET /api/token/models` 契约措辞**：已据实测校准为扁平分页（本 PR 修），前端按 `{items,...}` 处理。

---

## 3.6 S2-PM1 第二阶段业务验收结论（2026-06-22）

> 主持：产品经理 ｜ 基线：main `3d9251f`（验收依据 6219fdc + 上线检查单 #240）
> 技术依据：M2 75/75、M3 85/85、M4 整合 266/266 全绿，无 P0/P1/P2/P3 缺陷。

### 验收结论：**有条件通过（GO）**

第二阶段（Token 售卖 + 套餐预付 + 多模型聊天工作台）业务验收**通过**，准予进入上线审批（检查单第 7 节放行签字）。**条件**为「合并后待办 + 上线前置」必须在生产放行前满足（见下「上线前置条件」），其中 `INTERNAL_API_TOKEN`、渠道/模型目录数据为**硬阻断**项（不满足则套餐用户 chat 全 503 / 全平台 chat 不可用）。

### 6 条业务红线逐条核对

| # | 红线 | 结论 | 核对依据 |
|---|---|---|---|
| 1 | 产品定位：Token 售卖 + 多模型工作台；Agent/Skill/插件/角色全免费，唯一收费=模型 token | ✅ 达成 | roadmap §0 铁律；M4 报告 C 段「Agent/skill/插件零计费，唯一扣费载体=token，净扣==sale_sum 无额外扣费」 |
| 2 | 三计费模式互斥不串扣（postpaid 按量/按次 + prepaid 套餐） | ✅ 达成 | M4 报告 B 段 B1/B2/B3：postpaid 不扣 entitlement、prepaid 不扣钱包/不动 freeze、编排走钱包不碰 entitlement；三方账实一致 |
| 3 | 范围决策落地 D1–D6 | ✅ 达成 | D1 预扣保证金（B/D 段 hold 归零）、D2 编排仅登录态（E1–E3 sk 调编排=401）、D3 付费插件日上限（M3 脚本覆盖）、D4 不做 code_exec（roadmap §9 #20）、D5 幂等表 entitlement_consume_logs（000041）、D6 渠道单点延后第三阶段（检查单 §3 已登记，验收不当 bug） |
| 4 | 资金安全硬红线：并发无负余额、prepaid 无超扣、额度耗尽不返免费答案、保证金/预占无泄漏 | ✅ 达成 | M4 报告 D 段：postpaid 10 并发余额≥0（最终 0.00097）、frozen=0/holding=0；prepaid 精确放行 3=K、超出全 60005、quota_used≤total、reserved=0；D-M2-01 方案 B 根治白嫖 |
| 5 | 隐私红线：对话内容前端自持、后端不落明文、token_usage_logs 仅记元数据 | ✅ 达成 | chat-workbench 契约 §4「后端不落库存储对话内容，token_usage_logs 仅记 tokens/状态元数据」；检查单 §6 已登记前端自持限制 |
| 6 | 安全红线：sk/插件凭证不外泄、SSRF 防护、权限码必建 seed、越权 40003 | ✅ 达成 | M4 报告 E 段：E5 Agent 详情无凭证字段、E4/E6 越权=40003；SSRF 配置时+运行时拦截（M3 脚本）；权限码 seed 000043（agent/skill/plugin:manage 绑 admin） |

### 交付完整度（红线 7）

- 后端接口：总控 §3.4 统计 **已实现 20 项 / 共 24 项**；余 4 项（前甲2 管理端工作台配置页、前乙3 工作台选择/自建页、前乙4 聊天对话页、前甲/前乙用量与套餐购买页）均为**前端页面（Codex 范围）**，非后端缺口，验收不计后端欠交。
- 全部后端接口已就绪并经端到端实测；编排/计费/鉴权/工作台 CRUD 均有真实上游 + DB 集成测试覆盖。

### 上线前置条件（生产放行前必须满足，登记到检查单第 2/3 节）

**硬阻断（不满足则核心功能不可用）：**
1. **运维 S2-运2/运3 注入环境变量**：
   - `TOKEN_PROVIDER_KEY`（32B，必填，否则 token 网关 + 工作台整体不装配）
   - `API_KEY_HMAC_SECRET`（必填，否则 sk 不装配、计费降级为一律 postpaid，prepaid 套餐用户无法走 sk）
   - **`INTERNAL_API_TOKEN`（M2 prepaid 硬依赖，否则套餐用户 chat 全部 503 fail-closed）**
   - `PLUGIN_SECRET_KEY`（或确认回退复用 TOKEN_PROVIDER_KEY 符合预期）
2. **运营配置渠道/模型目录数据**（迁移仅 seed 商品/规则/权限码，不含渠道凭证）：至少一个 active 渠道 + active 模型，否则 chat/编排不可用（检查单 §3）。

**建议（生产强烈建议，非阻断）：**
3. `PLUGIN_DOMAIN_WHITELIST` 按真实外呼依赖显式配置，收紧 SSRF 外呼面（空仅按网段拦内网）。
4. `MAX_ROUNDS` / `TOKEN_HOLD_UNIT_PRICE` / `TOKEN_HOLD_DEFAULT_MAX_TOKENS` 确认采用默认或显式覆盖。

**合并后待办（不阻断后端上线，前端联调后随前端发布）：**
5. 前端页面交付（Codex）：S2-前甲2 / 前乙3 / 前乙4 / 前甲1 / 前乙1 / 前乙2；按 §14 契约对接，字段变更回写契约。

### 已知限制（验收知悉，非缺陷）

- D6 渠道单点：单渠道故障即对应模型不可用，需运营手动切换；多渠道故障转移延后第三阶段。
- 对话历史前端自持、后端不落库：切端/换设备不同步，前端丢失即历史丢失。
- 付费插件成本平台担：按 daily_limit 限量兜底，需运营监控每日成本。
- 低余额/额度耗尽主动提醒后置第三阶段（现为「耗尽即拒」）。

### 放行动作

- 准予执行 `docs/backend-stage2-go-live-checklist.md` 第 7 节放行流程。
- **数据库变更提醒**：本阶段迁移 000030–000044（连续 15 条），上线时通知运维执行 `./scripts/migrate.sh up`、确认 `schema_migrations` version=44 dirty=0 后重启 api 服务。


## 四、维护说明

- 本文是第二阶段单一追踪入口；实现推进时，**勾选 §3.3 复选框**并更新 §2 状态列与 §3.4 统计。
- 接口字段或错误码变更，先改对应契约 + `frontend-api-reference.md` §14，再回写本表（项目反复出现根因）。
- 状态语义：🔜→🚧（PR 提出）→✅（合并 main）。
