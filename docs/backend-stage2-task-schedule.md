# 第二阶段任务排期（Week 5–9，精确到工程师）

> 状态：排期 v1（2026-06-21）
> 阶段：第二阶段（Week 5–9）= Token 转发售卖 + 多模型聊天工作台
> 依据：`docs/backend-stage2-architecture-roadmap.md`（里程碑 M1–M4）+ sk/计费/聊天工作台三份对接契约 + `frontend-api-reference.md` §14
> 说明：本文为后端排期规划。前端任务仅标注「按 §14.x 契约对接」，页面由 Codex/前端团队实现；本文不含前端代码。
> 任务编号：`S2-<角色><序号>`，角色 = 甲/乙/丙/丁/前甲/前乙/测/运。

---

## 0. 角色与模块

| 角色 | 模块 | 第二阶段职责 |
|---|---|---|
| 后端甲 | auth/iam/middleware | 平台 sk 系统、双模式鉴权、各业务权限码 seed |
| 后端乙 | product/order/billing/finance_consumer | 按次计费规则、token 套餐商品、计费校验 |
| 后端丙 | asset/provision | 套餐额度 entitlement 生成 + 额度扣减接口 |
| 后端丁 | token_gateway/agent/skill/plugin | 用量查询、计费编排、聊天工作台三模块 + tool-use 编排 |
| 前端甲 | admin-console | 管理端 Token/用量/Agent·Skill·插件 配置页（按契约对接） |
| 前端乙 | user-console | 用户端 sk 管理/用量/套餐购买/聊天工作台（按契约对接） |
| 测试 | — | 各 M 用例、并发安全、回归、验收报告 |
| 运维 | infra | 环境变量、迁移执行、配置项注入 |

迁移序号（紧随第一阶段 000033，以实际合并顺序最终定序）：
`000034 api_keys`(已合并) / `000035 wallet_holds`(已合并) / `000036 按次计费规则 seed` / `000037 agents+绑定表` / `000038 skills` / `000039 plugins` / `000040 权限码 seed`。（golang-migrate 不支持 out-of-order，序号严格按合并顺序递增、不留空号）

---

## 1. 周 × 角色 总览矩阵

| 周 | 里程碑 | 甲 | 乙 | 丙 | 丁 | 前甲 | 前乙 | 测 | 运 |
|---|---|---|---|---|---|---|---|---|---|
| W5 | M1 Token 售卖闭环 | sk 系统+中间件 | 按次规则+互斥校验 | — | 用量查询接口 | — | — | M1 用例 | 环境变量/迁移 |
| W6 | M2 套餐预付 | sk prepaid + sk 限流 | 套餐商品+plan | 额度扣减接口 | 计费路由收口 + 三模块建表(前置) | 用量页 | sk/用量/套餐页 | M1回归+M2用例 | — |
| W7 | M3 工作台(上) | 3 权限码 seed | — | — | agent/skill/plugin 三模块 | 管理端配置页 | Agent 选择/自建页 | 模块CRUD用例 | — |
| W8 | M3 工作台(下) | plugin 转发器(协丁) | — | — | tool-use 编排主循环 | — | 聊天对话页(SSE) | 编排+插件用例 | 配置项注入 |
| W9 | M4 整合验收 | 缺陷修复 | 缺陷修复 | 缺陷修复 | 缺陷修复+检查单 | 缺陷修复 | 缺陷修复 | 端到端验收 | 上线检查单 |

---

## 2. Week 5 — M1 Token 售卖闭环

> 目标：sk 直连 chat → 转发 → 按量/按次扣钱包 → 用量可查。三条线可并行起步，周末收口 chat 鉴权切换。

| ID | 负责人 | 任务 | 依赖 | 产出物 | 验收 |
|---|---|---|---|---|---|
| S2-甲1 | 甲 | 迁移 `000034 api_keys`（key_hash 唯一、json:"-"）+ GORM model | — | migration + model | 表结构对齐 sk 契约 §3 |
| S2-甲2 | 甲 | `APIKeyService`：IssueKey/ResolveKey/RevokeKey/ListKeys（HMAC 复用 crypto.HMAC256） | S2-甲1 | service | 明文只回一次、只存 HMAC |
| S2-甲3 | 甲 | `middleware.APIKeyResolver` 接口 + `RequireUserAuth`（sk/JWT 双模式）+ `apiKeyIDKey`/`APIKeyIDFromContext` | S2-甲2 | middleware | sk 与 JWT 都注入 user_id |
| S2-甲4 | 甲 | 用户端 sk 管理路由 `POST/GET/DELETE /api/keys` + 封禁联动（ResolveKey 查 IsUserBlocked） | S2-甲2 | 路由 | 列表只回 prefix；封禁即失效 |
| S2-甲5 | 甲 | config 注入 `API_KEY_HMAC_SECRET` | — | config | 缺失启动报错 |
| S2-乙0 | 乙 | **（D1 前置）** 确认钱包 `freeze/unfreeze` 对门面暴露可调内部接口，无则补一个 | — | 内部接口确认/补全 | 门面可冻结/解冻保证金 |
| S2-乙1 | 乙 | 迁移 `000036` 按次计费规则 seed（`calls/count`，挂 token-api 商品） | — | migration | finance_consumer 能匹配 calls |
| S2-乙2 | 乙 | 管理端「按量/按次互斥」强校验（存按次时若有生效按量则拦截，反之亦然） | S2-乙1 | 校验逻辑 | 同商品不能同时生效两种 |
| S2-丁1 | 丁 | 用量查询 `GET /api/token/usage`（本人，扁平分页，时间/模型筛选） | — | 接口 | 对齐 §14.3 |
| S2-丁2 | 丁 | 用量查询 `GET /api/admin/token/usage`（全量，token:manage） | — | 接口 | 对齐 §14.7 |
| S2-丁3 | 丁 | chat 用户端三接口换 `RequireUserAuth`，bootstrap 注入 apiKeyResolver；chat handler 取 api_key_id 写日志 | S2-甲3 | 装配改动 | sk 调用落 api_key_id |
| S2-丁4 | 丁 | 门面上报 `calls` 次数事件（一次提问 1 条；前置失败不计次） | S2-乙1,S2-丁3 | 计费上报 | 按次扣 1；无规则静默跳过 |
| S2-丁4b | 丁 | **model_scope 越界校验**（sk 契约 §8.4）：sk 带 `model_scope` 且请求 model 不在范围内 → 拒绝 `40300`；门面鉴权后复用 `ResolveKey` 结果校验 | S2-丁3 | 门面校验 | 越界拒 40300；不限 scope 放行 |
| S2-运1 | 运 | `infra/.env.example` 补 `API_KEY_HMAC_SECRET`；执行 000034 api_keys + 000035 wallet_holds 迁移 | S2-甲5 | env + 迁移流程 | 测试环境可起 |
| S2-测1 | 测 | M1 用例：sk 签发/明文一次性/调用转发/按量+按次扣费/用量查询/**预扣保证金并发扣费无负余额** | 上列就位 | 测试脚本 | 全过，含并发硬指标 |

**W5 验收门槛**：sk 调 `/api/token/chat/completions` → 转发成功 → 钱包按量或按次扣费 → `/api/token/usage` 可见；吊销/封禁即时失效；并发无负余额。

---

## 3. Week 6 — M2 套餐预付

> 目标：买 token 套餐 → 得 prepaid sk → 调用扣套餐额度（不走钱包）→ 耗尽/到期拒绝。前端启动 M1 页面对接。

| ID | 负责人 | 任务 | 依赖 | 产出物 | 验收 |
|---|---|---|---|---|---|
| S2-乙3 | 乙 | token 套餐 plan（`quota_json`：quota_total/quota_unit=tokens/valid_days）+ 套餐售价 product_prices | — | seed/接口 | 套餐可下单 |
| S2-丙1 | 丙 | 确认 `ProvisionService`/`TokenProvisioner` 套餐分支按 quota_json 生成 `token_quota` entitlement（含 expires_at） | S2-乙3 | provision 改动 | 购买后生成额度 |
| S2-丙2 | 丙 | `POST /api/internal/entitlement-consume`（FindByIDForUpdate 锁行 + status/expires_at/余额校验 + 归属；不足回 60005；**D5 新建幂等表 `entitlement_consume_logs` 及迁移**） | — | 内部接口+迁移 | 并发无超扣；幂等 |
| S2-丙3 | 丙 | 内部余额查询（门面前置闸用，返回 remaining）或复用 §10.3 | — | 接口 | 门面可查额度 |
| S2-甲6 | 甲 | `IssueKey` 支持 `billing_mode=prepaid` + `source_id=entitlement_id`；购买套餐后签发 prepaid sk | S2-甲2,S2-丙1 | service 扩展 | prepaid sk 绑额度 |
| S2-甲8 | 甲 | **两层限流（D1/R9）**：鉴权前 IP 粗粒度 + 鉴权后 sk/user 维度（复用 `middleware/ratelimit.go`，插件调用计入） | S2-甲3 | 限流中间件 | 单 sk/IP 超频被限 |
| S2-丁5 | 丁 | 门面计费路由：postpaid→**预扣保证金(freeze)+结算解冻实扣(D1)**；prepaid→调丙 entitlement-consume；前置余额闸；**SSE 断开读完上游再结算(R5)** | S2-丙2,S2-甲6,S2-乙0 | 编排 | 互斥结算不双扣；并发无负余额 |
| S2-丁6 | 丁 | **（前置）** 迁移 `000037-000039`：agents+绑定表 / skills / plugins（无前序依赖，提前到 W6 与计费并行，为 W7 减压） | — | migration | 对齐工作台契约 §2 |
| S2-前乙1 | 前乙 | sk 管理页 + 我的用量页（对接 §14.4/§14.3） | S2-甲4,S2-丁1 | 页面 | 创建/列表/吊销/用量 |
| S2-前乙2 | 前乙 | token 套餐购买页（对接商品/订单/钱包既有接口 + 套餐商品） | S2-乙3 | 页面 | 购买闭环 |
| S2-前甲1 | 前甲 | 管理端全量用量页（对接 §14.7） | S2-丁2 | 页面 | 筛选/分页 |
| S2-测2 | 测 | M1 回归 + M2 用例：买套餐→prepaid 调用扣额度→耗尽拒绝(60005)→到期清零→**并发额度无超扣** | 上列就位 | 测试脚本+报告 | 全过 |

**W6 验收门槛**：postpaid/prepaid 两路计费正确且互斥；套餐额度耗尽与到期均拒绝；并发额度无超扣。

---

## 4. Week 7 — M3 聊天工作台（上）：Agent/Skill/插件 基础

> 目标：运营配置官方 Agent/Skill/插件；用户可选用 + 自建 Agent 并绑定。本周不含对话编排（W8）。

| ID | 负责人 | 任务 | 依赖 | 产出物 | 验收 |
|---|---|---|---|---|---|
| S2-甲7 | 甲 | 权限码 seed `000040`：`agent:manage`/`skill:manage`/`plugin:manage`（建码必建 seed 红线） | — | migration | 三码可分配 |
| S2-丁7 | 丁 | agent/skill/plugin 三模块 model/repo/service + bootstrap 装配（建表 S2-丁6 已于 W6 前置完成） | S2-丁6(W6) | 模块骨架 | 编译通过 |
| S2-丁8 | 丁 | 管理端 CRUD：`/api/admin/agents`(+绑定)、`/api/admin/skills`、`/api/admin/plugins`（插件凭证加密、has_auth） | S2-丁7,S2-甲7 | 接口 | 对齐 §14.10；凭证不回 |
| S2-丁9 | 丁 | 用户端：`GET /api/agents`(官方+自建)、`/api/agents/{id}`、`POST/PATCH/DELETE /api/agents`(自建+越权防护)、`GET /api/skills`、`/api/plugins` | S2-丁7 | 接口 | 对齐 §14.9；自建可绑官方 skill+插件 |
| S2-前甲2 | 前甲 | 管理端 Agent/Skill/插件 配置页（对接 §14.10） | S2-丁8 | 页面 | CRUD+绑定 |
| S2-前乙3 | 前乙 | 工作台 Agent 选择 + 自建 Agent 页（对接 §14.9） | S2-丁9 | 页面 | 选用/自建/绑定 |
| S2-测3 | 测 | 三模块 CRUD 用例 + 自建 Agent 越权用例（改/删他人→40003） | S2-丁8,S2-丁9 | 测试脚本 | 全过 |

**W7 验收门槛**：运营建官方 Agent 并挂 skill/插件；用户自建 Agent 可绑定官方已上架 skill+插件；越权被拦。

---

## 5. Week 8 — M3 聊天工作台（下）：tool-use 编排

> 目标：`POST /api/agents/{id}/chat` 完整工具循环；skill 内置执行 + 插件外部转发；token 正常计费。

| ID | 负责人 | 任务 | 依赖 | 产出物 | 验收 |
|---|---|---|---|---|---|
| S2-丁10 | 丁 | **（关键路径，不可让渡）** `POST /api/agents/{id}/chat` tool-use 编排循环（**D2：仅登录态，sk 不可调**；注入 tools→收 tool_calls→执行→回灌→直到最终答案；MAX_ROUNDS 可配置默认 5；超限提示已计费） | S2-丁9,S2-丁5 | 核心接口 | 对齐工作台契约 §4 |
| S2-丁11 | 丁 | skill 内置函数注册表（handler_key 路由，1–2 示例 **web_search/doc_read；D4：本期不含 code_exec**） | S2-丁10 | 内置实现 | skill 可被调用 |
| S2-甲9 | 甲(协丁) | plugin HTTP 转发器（按 schema 取参→转发 endpoint_url→回灌；SSRF 白名单/禁内网/timeout/凭证解密/连续失败熔断；**D3：付费插件 daily_limit 每用户每日上限计数**）——独立安全模块，由 middleware 负责人甲主理，给丁减压 | S2-丁7 | 转发器 | 安全约束 + 配额生效 |
| S2-丁13 | 丁 | 编排计费接入：每轮 token 累加；整次提问按次计 1；prepaid/postpaid 路由复用 S2-丁5；接入 S2-甲9 转发器 | S2-丁10,S2-甲9 | 计费 | 多轮只计 1 次 |
| S2-丁14 | 丁 | **bootstrap 总装配收口**：sk 中间件、用量、三模块、编排路由统一在 bootstrap 注册，跑装配冒烟（防编译期漏装，参 #199 教训） | S2-丁13 | 装配+冒烟 | main 编译/启动通过 |
| S2-前乙4 | 前乙 | 聊天对话页：SSE 渲染 + `tool_call`/`tool_result`/`message` 事件（对接 §14.8） | S2-丁10 | 页面 | 流式对话+工具进度 |
| S2-运2 | 运 | 配置项注入：`MAX_ROUNDS`、插件域名白名单、`PLUGIN_SECRET_KEY`（或复用 TOKEN_PROVIDER_KEY）；补 .env.example | S2-甲9 | 配置 | 可配置生效 |
| S2-测4 | 测 | 编排用例：多轮工具调用、MAX_ROUNDS 安全终止、插件 SSRF/超时拦截、计费正确（token 累加+按次计1） | 上列就位 | 测试脚本 | 全过 |

**W8 验收门槛**：选 Agent→对话触发 skill/插件→拿到含工具结果的答案；token 计费正确；插件 SSRF/超时被拦；循环超限安全终止且已计费；bootstrap 总装配冒烟通过。

> **负载说明（PM review #203）**：W7–W8 是全期最重段，后端丁满载。本期已将建表（S2-丁6）前置 W6、plugin 转发器（S2-甲9）拆给后端甲协作；`S2-丁10` 编排主循环为关键路径，不可让渡。若仍紧张，skill 注册表（S2-丁11）可由后端乙协助。

---

## 6. Week 9 — M4 整合验收 + 上线

> **周内切分（PM review #203）**：W9 前半（约 D1–D3）= 端到端验收 + 缺陷暴露；W9 后半（D4–D5）= 缺陷修复 + 回归 + 上线检查单。这是全期唯一缓冲，不再叠加新功能。

| ID | 负责人 | 任务 | 依赖 | 产出物 | 验收 |
|---|---|---|---|---|---|
| S2-测5 | 测 | （W9 前半）端到端全链路：两路径(透传/编排) + 三计费(按量/按次/套餐) + tool-use 多轮 + **并发扣费无负余额(硬)** + 并发安全 | W5–W8 全部 | 验收报告 | 全链路通过 |
| S2-测6 | 测 | （W9 前半）缺陷跟踪表，按 P0–P3 分级回报各工程师 | S2-测5 | 缺陷表 | 闭环 |
| S2-{各}1 | 甲/乙/丙/丁/前甲/前乙 | （W9 后半）按缺陷表修复并回归 | S2-测6 | 修复 PR | 两轮 QA 回归全闭环 |
| S2-运3 | 运 | `docs/backend-stage2-go-live-checklist.md`（环境变量/迁移顺序/配置项/回滚） | S2-丁14 | 检查单 | 可照单上线 |
| S2-PM1 | PM | 第二阶段业务验收 + 确认进入上线 | S2-测5 | 验收结论 | 通过 |

**W9 验收门槛**：测试出具验收通过报告 + PM 业务验收通过 + 上线检查单就绪。

> **M3 延期降级预案**：若 W8 tool-use 编排（S2-丁10）延期，**优先保 M1+M2（Token 售卖 + 套餐）功能闭环上线**，M3 聊天工作台顺延为「第二阶段补充发布」；不得为赶工跳过并发无负余额硬验收与插件安全约束。

---

## 7. 关键依赖链（防阻塞）

```
W5: 甲 sk 系统(S2-甲1→甲4) ──► 丁 chat 换中间件(S2-丁3) ──► 丁 按次上报(S2-丁4)
    乙 按次规则(S2-乙1) ─────────────────────────────────┘
W6【关键路径·串行长链，须周中前交付前置项】:
    乙 套餐 plan(S2-乙3) ─► 丙 entitlement 生成(S2-丙1) ─► 甲 prepaid sk(S2-甲6) ─► 丁 计费路由(S2-丁5)
    丙 额度扣减(S2-丙2) ───────────────────────────────────────────────────────┘
    （丁 同周并行前置建表 S2-丁6，为 W7 减压）
W7-W8: 丁 三模块(S2-丁7→丁9) ─► 丁 tool-use 编排(S2-丁10) ─► skill(S2-丁11)/plugin(S2-甲9 协作) ─► 编排计费(S2-丁13) ─► bootstrap 收口(S2-丁14)
       甲 权限码 seed(S2-甲7) ─► 丁 管理端 CRUD(S2-丁8)
后端接口就位 ─► 前端对接(前甲/前乙) ─► 测试介入
```

**可立即并行起步（W5 第一天，互不阻塞）**：S2-甲1、S2-乙1、S2-丁1/丁2、S2-运1、S2-测1。
**W6 关键路径提醒**：`乙3→丙1→甲6→丁5` 为同周串行长链，丙1/甲6 务必周中前交付，否则丁5 周末收口受阻。
**负载再平衡（PM review #203）**：建表 S2-丁6 前置 W6；plugin 转发器 S2-甲9 由后端甲主理；如紧张 skill 注册表 S2-丁11 可由乙协助。

---

## 8. 红线（每位工程师交付前自查）

- 权限码 `RequirePerm` 必建 seed migration（项目反复出现的 P1 根因）。
- sk / 插件凭证：sk 只存 HMAC、明文只回一次；插件凭证 AES-256-GCM；响应绝不返回。
- 计费：幂等键 `request_id:类型`；postpaid/prepaid 互斥结算；并发锁行防透支；套餐额度不足用 `60005`（禁 60002）。
- 后端接口字段变更必须同步 `frontend-api-reference.md`（项目反复出现根因）：**每张后端接口任务的产出物默认含「+ 同步 §14.x 契约」**，再让前端对接。
- 每个模块/路由完成后即跑 **bootstrap 装配冒烟**（防编译期漏装，参 #199 教训），不留到验收周才发现。
- 并发多 actor 开发用 git worktree 隔离；feature 分支开发、PR 审查、merge 前 PM 与用户确认。
