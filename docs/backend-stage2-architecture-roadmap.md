# 第二阶段架构路线图（v2）：Token 转发售卖 + 多模型聊天工作台

> 状态：架构规划 v2（2026-06-20，按产品蓝图重写；v1 为「Agent/Skill 收费市场」方向，已废弃）
> 阶段：第二阶段（Week 5–9）
> 范围：Token 网关收口 + Agent/Skill/插件三层能力（免费）+ 多模型聊天工作台（本期仅 chat）
> 读者：后端甲/乙/丙/丁、前端甲/乙、测试、产品经理
> 关联：`docs/backend-token-gateway-design.md`、`docs/backend-token-gateway-integration.md`、`docs/backend-sk-auth-contract.md`、`docs/frontend-api-reference.md` §14、`docs/cloud-resource-app-marketplace-mvp.md` §6.11/§6.12/§6.13
> 分工红线：本文为后端架构与对接契约规划，前端页面由 Codex/前端团队实现，本文只给接口契约。

---

## 0. 产品定位（一句话）

第二阶段交付两个相互独立又共享底座的能力：

1. **Token 转发售卖网关**：平台聚合上游模型，用户购买后获得平台 sk，用 sk 以 OpenAI 兼容方式直连我们转发的 AI 接口（开发者/外部程序场景，纯透传）。
2. **多模型聊天工作台**（Gemini 式）：站内聊天产品，用户可切换不同**模型类型/模型**，选择不同 **Agent（角色/人设）**，Agent 可挂载 **Skill（内置能力）/ 插件（外部接入）**。本期模型类型**仅 chat**，图片/视频后置第三阶段。

**核心商业规则（本阶段铁律）**：**Agent / Skill / 插件 / 角色全部免费**（运营配置或用户自建均不收费）；**唯一收费点 = 底层模型 token 调用**，计费方式三选一/可组合：**按量（token 数）/ 按次（调用次数）/ 套餐（预付 token 额度）**。

---

## 1. 相对 v1 的关键修订（产品蓝图澄清，2026-06-20）

| # | v1 旧规划 | v2 新蓝图 | 影响 |
|---|---|---|---|
| 1 | Agent 定制市场（报价→支付→交付订单） | Agent **免费**，运营配置 + 用户自建 | **删除** `agent_customization_orders` 整套订单/状态机 |
| 2 | Skill 收费市场（授权周期/买断） | Skill **免费**，运营配置挂载 | **删除** skill 购买/授权计费、`user_skill_installs.expires_at` 收费 |
| 3 | Agent/Skill 进商品中心 | 三层能力**脱离** product/order 体系 | 不建 `product_type=agent/skill` 商品 |
| 4 | 仅 chat 透传 | 新增 **Agent tool-use 编排**（产品内聊天） | 门面新增有状态工具循环端点（见 §6） |
| 5 | 概念两层 | **三层**：Agent / Skill / 插件 分开 | 三套表（见 §5） |
| 6 | 计费「按量先行，套餐不立项」 | **按量 + 按次 + 套餐 全上** | 套餐重新进场：丙建额度扣减、sk 支持 `prepaid`（见 §4、§7） |
| 7 | 多模态全做 | **本期仅 chat**，图片/视频→第三阶段 | 转发器不做多模态适配，大幅减负 |

> 「角色」= 用户视角下的 Agent（人设），不单独建表，**Agent 即角色**。用户「切换角色」= 选择不同 Agent。

---

## 2. 现状盘点（截至 2026-06-20）

### 2.1 已完成（合并 main，PR #188–#201）
- 迁移 `000030–000033`：`token_models` / `token_usage_logs` / `token_channels`+路由 / `token:manage` seed / token 商品+按量计费规则
- 模块 `token_gateway`：渠道·模型目录 CRUD（管理端）、`GET /api/token/models`、`POST /api/token/chat/completions`（OpenAI 兼容 chat，SSE，**纯透传**）
- 计费：按量扣钱包（`finance_consumer` 零改）
- `provision.TokenProvisioner` + 资产门禁
- 前端（Codex）：用户端对话页、管理端 Token 配置页

### 2.2 本阶段缺口
| 缺口 | 归属 | 所属能力 |
|---|---|---|
| 平台 sk 系统（postpaid+**prepaid**）+ 双模式鉴权 | 甲 | Token 售卖 |
| 用量查询接口（用户端/管理端） | 丁 | Token 售卖 |
| **按次**计费规则 + 门面上报次数事件 | 乙+丁 | 计费 |
| **套餐**：token 额度 entitlement + 额度扣减接口 | 丙 | 计费 |
| `agent` 模块（运营预设 + 用户自建 + tool-use 编排） | 丁 | 聊天工作台 |
| `skill` 模块（内置能力，免费挂载） | 丁 | 聊天工作台 |
| `plugin` 模块（外部接入，免费挂载） | 丁 | 聊天工作台 |
| 端到端验收 | 测试/PM | 全部 |

---

## 3. 全景架构

```
  外部程序/Agent(sk)            站内聊天工作台(用户登录态)
        │ 纯透传                       │ 选模型类型(chat)→选模型→选 Agent(角色)
        ▼                             ▼
  POST /api/token/chat/completions   POST /api/agents/{id}/chat
        │                             │  ┌─ 注入 Agent 绑定的 Skill/插件为 tools
        │                             │  ▼  tool-use 编排循环（门面有状态）
        └──────────┬──────────────────┘  ① 请求上游 ② 收 tool_calls
                   │                       ③ 执行 Skill(内置)/插件(外部HTTP)
                   ▼                       ④ 回灌结果 ⑤ 重复直到最终答案
        ┌──────────────────────────────────────┐
        │   token_gateway 转发器 + 计费编排（丁）  │
        │   选渠道→换 base_url/key/上游模型名→转发  │
        │   读 usage→写 token_usage_logs→计费     │
        └──────────────────────────────────────┘
                   │ 计费（唯一收费点）
   ┌───────────────┼────────────────┐
 按量(token)     按次(calls)      套餐(预付额度)
 扣钱包(乙)      扣钱包(乙)        扣 entitlement(丙)
                   │ 鉴权(甲) / 门禁(丙) / 余额(乙/丙)
        ┌──────────────────────────────────────┐
        │           第一阶段平台底座（复用）        │
        └──────────────────────────────────────┘
                   │ 直连 HTTPS（OpenAI 兼容）
            OpenAI / DeepSeek / Kimi
```

---

## 4. 收费模型（唯一收费点 = 模型 token 调用）

复用底座通用计费表 `product_billing_rules`（`usage_type` + `usage_unit` + `price_amount`），三种方式：

| 方式 | usage_type / unit | 付费时点 | 落地 | 状态 |
|---|---|---|---|---|
| 按量（token 数） | `input_tokens`/`output_tokens` `tokens` | 后付，调用后扣钱包 | 已做（000033 seed） | ✅ |
| 按次（调用次数） | `calls` `count` | 后付，每次提问扣 1 次 | 加一条计费规则 + 门面上报次数事件 | 🔜 乙+丁 |
| 套餐（预付额度） | token 额度 entitlement | 预付，调用扣额度 | 买 token 套餐→entitlement→调用扣减 | 🔜 丙+甲 |

**计费口径（铁律，避免歧义）**
- 一次用户提问若触发 tool-use 多轮上游调用：**按量** = 累加所有轮 token；**按次** = 仍算 **1 次**（按用户发起的提问计，不按上游轮数）。
- 计费模式由 **sk / 调用上下文** 决定：`postpaid`→钱包；`prepaid`→套餐额度。
- Agent/Skill/插件本身**零计费**，不出现在 `product_billing_rules`。

---

## 5. 三层能力模型（全部免费｜运营配置 + 用户自建）

> 三套表，均不进 product/order。运营配置「官方预设」，用户可在官方基础上自建个性化 Agent。

### 5.1 Agent（角色/人设）— 模块 `agent`
```text
agents                      -- 官方预设 + 用户自建统一表（owner_type 区分）
  id, code(官方唯一,用户自建可空), name, description, avatar,
  owner_type(official/user), owner_user_id(user 自建时非空),
  system_prompt, default_model_code(指向 token_models.logical_model_code),
  status(active/inactive), sort_order, created_at, updated_at
agent_skill_bindings        -- Agent 绑定的内置 Skill
  id, agent_id, skill_id, enabled, created_at
agent_plugin_bindings       -- Agent 绑定的外部插件
  id, agent_id, plugin_id, enabled, created_at
```
- 官方 Agent：`token:manage`（或新增 `agent:manage`）后台配置。
- 用户自建 Agent：登录态创建，`owner_type=user`，可选基模型 + 绑定已开放的 skill/插件。

### 5.2 Skill（平台内置能力）— 模块 `skill`
```text
skills                      -- 平台实现的内置工具（联网搜索/代码执行/读文档…）
  id, code(唯一), name, description, category,
  tool_schema_json(function calling 工具定义), handler_key(内置实现路由键),
  status(active/inactive), created_at, updated_at
```
- 纯运营配置；执行 = 门面内置函数（按 `handler_key` 路由）。本期可先上 1–2 个示例 skill（如联网搜索）。

### 5.3 插件（外部第三方接入）— 模块 `plugin`
```text
plugins                     -- 外部 HTTP 工具，门面转发调用
  id, code(唯一), name, description,
  tool_schema_json(工具定义), endpoint_url, auth_config_encrypted(如需,AES-256-GCM),
  timeout_ms, status(active/inactive), created_at, updated_at
```
- 与 skill 的唯一区别：执行 = 门面按 schema 转发到 `endpoint_url`（外部 HTTP），结果回灌。
- 安全：插件凭证 AES-256-GCM 加密（复用 `TOKEN_PROVIDER_KEY` 或新增密钥），响应不回凭证。

---

## 6. 关键架构决策：薄透传 vs Agent tool-use 编排

门面提供**两条调用路径**，互不干扰：

| 路径 | 端点 | 鉴权 | 行为 | 工具循环 |
|---|---|---|---|---|
| 开发者直连 | `POST /api/token/chat/completions` | sk / 登录态 | **纯透传** OpenAI 兼容请求 | 调用方自理 |
| 产品内聊天 | `POST /api/agents/{id}/chat` | **仅登录态**（D2，sk 不可调） | 门面**有状态 tool-use 编排** | 门面自动执行 |

**tool-use 编排循环（产品内聊天）**
```
1. 取 Agent → system_prompt + default_model + 绑定的 skill/插件
2. 组装请求：messages + tools(由 skill.tool_schema_json / plugin.tool_schema_json 汇总)
3. 请求上游 → 若返回 tool_calls：
     skill  → 门面内置函数(handler_key) 执行
     plugin → 转发 endpoint_url 执行
   将工具结果作为 tool message 回灌 → 回到步骤 3
4. 无 tool_calls → 返回最终答案（支持 SSE）
5. 每轮上游调用按 §4 计费；整次提问按次只计 1
```
- 防失控：单次提问工具循环 **最大轮数上限**（如 5），超限终止并提示。
- 流式：最终答案轮走 SSE；中间工具轮对前端可发进度事件（前端契约另定）。

---

## 7. sk 鉴权（双计费模式，套餐进场后更新）

详见 `docs/backend-sk-auth-contract.md`（v1.1 已按本决策更新）。要点：
- sk 沿用「只存 HMAC、明文只回一次、支持吊销、封禁联动」。
- `billing_mode`：`postpaid`（钱包）/ **`prepaid`（套餐额度，本期启用）**；`source_id` 在 prepaid 时绑 `entitlement_id`。
- 双模式鉴权中间件 `RequireUserAuth`：`Bearer sk-…` 走 sk，否则走 JWT；统一注入 `user_id`(+`api_key_id`)。

---

## 8. 里程碑（Week 5–9）

**M1（Week 5）｜Token 售卖闭环**
- 甲：sk 系统（postpaid+prepaid）+ 双模式鉴权 + 封禁联动
- 丁：用量查询接口（用户端 14.3 / 管理端 14.7）
- 乙：**按次**计费规则 + 门面上报次数事件（与丁）
- 验收：sk 直连 chat → 转发 → 按量/按次扣钱包 → 用量可查

**M2（Week 6）｜套餐预付**
- 乙：token 套餐商品（预付）+ 购买
- 丙：token 额度 entitlement（`TokenProvisioner` 扩展）+ `entitlement-consume` 额度扣减接口
- 甲：`IssueKey(prepaid, source_id=entitlement_id)`
- 验收：买套餐 → 得 sk → 调用扣套餐额度 → 余额耗尽拒绝

**M3（Week 7–8）｜聊天工作台 + 三层能力**
- 丁：`agent`/`skill`/`plugin` 三模块（运营配置 + 用户自建 Agent）
- 丁：`POST /api/agents/{id}/chat` tool-use 编排循环（内置 skill 执行 + 插件转发）
- 甲：`agent:manage`/`skill:manage`/`plugin:manage` seed（建码必建 seed，红线）
- 验收：选模型→选 Agent→挂 skill/插件→对话触发工具→token 正常计费；用户自建 Agent 可用

**M4（Week 9）｜整合验收**
- 测试：两路径端到端 + 三计费 + tool-use 多轮回归 + **并发扣费无负余额（硬指标）** + 并发安全（worktree 隔离）
- PM：第二阶段业务验收
- 运维+丁：`docs/backend-stage2-go-live-checklist.md`

---

## 9. 风险与待确认

| # | 项 | 结论（含 PM 2026-06-21 终确认） |
|---|---|---|
| 1 | 计费方式 | ✅ 按量 + 按次 + 套餐 全上 |
| 2 | sk 开放 | ✅ 本期开放，加按 sk 限流 + 余额阈值防透支 |
| 3 | 概念分层 | ✅ Agent/Skill/插件三层，角色=Agent |
| 4 | 多模态范围 | ✅ 本期仅 chat，图片/视频→第三阶段 |
| 5 | 创建权 | ✅ 运营配置 + 用户自建 Agent |
| 6 | 按次计费循环口径 | ✅ 按用户提问计 1 次；前置失败（未发起上游）不计次 |
| 7 | tool-use 最大轮数 | ✅ 默认 5，**可配置**（不硬编码）；超限终止提示已计费 |
| 8 | 插件安全 | ✅ 可配置域名白名单 + 禁内网 + 超时熔断 + 凭证 AES-256-GCM；列 M3 安全评审项 |
| 9 | 套餐额度单位 | ✅ **token 数**；prepaid 折算 = input+output（1:1 不加权） |
| 10 | 自建 Agent 绑插件 | ✅ 可绑定官方已上架 skill + 插件；不能自建/上传 |
| 11 | 套餐有效期 | ✅ `quota_json.valid_days`（默认 365）；**到期未用完额度清零**（entitlement 置 expired） |
| 12 | 会话历史留存 | ✅ 本期**前端自持，后端不存对话内容**（与隐私红线一致）；多端历史为后续阶段 |
| 13 | 并发防透支 | ✅ **D1：预扣保证金**（postpaid 转发前冻结 `max_tokens×单价`，结算解冻多退少补，复用钱包 freeze）；M1/M4 硬验收「并发扣费无负余额」。prepaid 靠 entitlement 锁行。详见 billing §4.3 / sk §9 |
| 14 | 低余额/额度提醒 | ⬜ 后置：现仅"耗尽即拒"，缺主动提醒（钱包低额/套餐将耗尽）；后续阶段补用户端阈值提醒 |
| 15 | 插件外部成本归属 | ✅ 官方插件外部 API 成本由**平台承担**，用户侧不额外计费；**D3：付费插件 `daily_limit` 每用户每日上限 + 计入限流**（防滥用） |
| 16 | 错误码 | ✅ 套餐额度不足复用 **`60005`**（禁用已占用的 60002=重复支付）；sk 越权吊销用 `40003`（非 40004） |
| 17 | **渠道故障转移（D6）** | ⬜ **本期不做**：`token_models` 绑单一渠道（`forward_service` 单点，`token_channels.priority` 建而未用），上游抖动→对应模型不可用为**已知行为，验收不当 bug**。多渠道路由(一对多 + weight/priority)+ 熔断切换**延后第三阶段**（见 §11 第三阶段任务） |
| 18 | **sk 编排端点边界（D2）** | ✅ `POST /api/agents/{id}/chat` **仅登录态**，sk 不可调（防平台代付插件成本被脚本化滥用）；sk 仅用透传端点 |
| 19 | **entitlement 幂等（D5）** | ✅ 新建 `entitlement_consume_logs(idempotency_key UNIQUE)`，prepaid 扣额度幂等，不复用钱包流水（见 billing §4.2） |
| 20 | **code_exec（D4）** | ✅ 本期不做（RCE 风险需独立沙箱）；skill 示例用 web_search/doc_read，沙箱方案延后第三阶段 |

**红线复用提醒**：权限码必建 seed；上游/插件凭证 AES-256-GCM、响应不返回；sk/refresh 只存 HMAC、明文只回一次、支持吊销；后端字段变更同步前端契约；错误码不复用已占用码值；并发多 actor 用 worktree 隔离。

---

## 10. 下一步

§9 待确认项已由 PM 于 2026-06-21 全部拍板（见上表），细化对接契约已就位：
- sk 鉴权：`backend-sk-auth-contract.md`（v1.1，支持 prepaid）
- 三计费：`backend-token-billing-contract.md`（按次 + 套餐预付）
- 聊天工作台：`backend-chat-workbench-contract.md`（Agent/Skill/插件 + tool-use 编排）
- 前端对接：`frontend-api-reference.md` §14

D1–D6 风险决议已吸收进上述契约（见 `backend-stage2-risk-review.md` + 本节 §9 #13/#15/#17–#20）。进入实现阶段：各后端按契约开发（甲 sk + 三权限码 seed + 两层限流 / 乙 按次规则 + 套餐商品 + 钱包 freeze 确认 / 丙 entitlement-consume + 幂等表 / 丁 用量查询 + 预扣计费 + 三模块 + 编排）。

---

## 11. 延后第三阶段的项（本期显式登记，验收不当 bug）

| 项 | 来源 | 说明 |
|---|---|---|
| 渠道多路由 + 故障转移 + 熔断 | D6 / §9 #17 | `token_models` 一对多渠道（weight/priority）+ 失败重试下一渠道 + 连续失败熔断；本期单渠道单点 |
| skill `code_exec` 沙箱执行 | D4 / §9 #20 | 容器/gVisor/WASM 隔离 + 独立安全评审 |
| 用户端低余额/额度耗尽主动提醒 | §9 #14 | 钱包低额、套餐将耗尽阈值提醒 |
| 多模态：图片/视频模型 | §9 #4 | 图片 images 适配 + 视频异步任务（提交→轮询/回调） |
| 会话历史多端同步/留存 | §9 #12 | 本期前端自持，后续做后端会话存储（含隐私加密设计） |
