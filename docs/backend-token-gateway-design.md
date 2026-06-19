# 第二阶段落地方案：Token 上游聚合网关（token_gateway）

> 状态：待评审 → 实现｜阶段：第二阶段（Week 5–9）
> 上游设计依据：`docs/cloud-resource-app-marketplace-mvp.md` §6.13/§7.6、`docs/full-api-design.md` §6.4
> 读者：实现后端（建议后端乙/新设负责人）、后端甲（鉴权/权限挂接）、产品经理（验收）

## 1. 背景与目标

平台要把「模型调用能力」做成可售卖资源：接入多个上游 Token 供应商，对外暴露统一的逻辑模型名（如 `gpt-4o`），用户购买 Token 额度后通过 OpenAI 兼容接口调用，平台赚取「售价 − 上游成本」的价差。

### 范围
- 上游供应商管理（api_key AES-256-GCM 加密）、模型与定价、模型路由（加权 + 优先级 + 断路器）。
- OpenAI 兼容 chat 接口（流式 SSE 透传 + 非流式），用量统计。
- 把 Token 接成统一商品（`product_type=token`）+ 按 input/output tokens 计费。

### 非目标（本期不做）
- 不做 Agent/Skills（同属第二阶段，另立方案）。
- 不自研模型，只做聚合转发。
- 不改 finance_consumer / billing / asset 既有代码（仅作为调用方对接）。

## 2. 总体架构（关键：最大化复用现有体系）

```
售卖侧（复用现有，几乎零新建）
  商品(product_type=token) → 套餐/价格 → 角色/会员访问控制(你已建的组→角色) → 下单扣钱
     → provision 开通 → user_assets(token_quota) + user_entitlements(quota_total/used, unit=tokens)

调用侧（本方案新建 = token_gateway）
  POST /api/token/chat/completions
   → 鉴权(登录/API Key) → 校验额度(token_quota) → 按 logical_model_code 选路由
   → 上游请求(流式/非流式) → 记 token_usage_logs → 上报 ProductUsageEvent → finance_consumer 扣费
   → 透传返回(SSE 不缓冲)
```

**真正新建的只有 token_gateway 模块本身**；商品/订单/资产/计费/权限全部复用。

## 3. 数据模型（§6.13，新增 4 表）

```sql
-- 000030_create_token_gateway_tables.up.sql
token_providers     -- 上游供应商
  id, code(uniq), name, base_url, auth_type(api_key/oauth),
  api_key_encrypted(AES-256-GCM，禁明文/禁返回), status, priority, created_at, updated_at

token_models        -- 上游模型 + 成本价/售价
  id, provider_id, model_code, display_name, context_window,
  input_price_per_1k, output_price_per_1k,        -- 上游成本价
  sale_input_price_per_1k, sale_output_price_per_1k, -- 用户售价
  status, created_at, updated_at

token_model_routes  -- 逻辑模型 → 上游路由
  id, logical_model_code(对外名,如 gpt-4o), provider_model_id,
  weight(加权随机), priority(同权重按此升序故障切换), status, created_at, updated_at

token_usage_logs    -- 每次调用的用量与成本
  id, request_id(uniq), user_id, provider_id, model_id, logical_model_code,
  input_tokens, output_tokens, total_tokens,
  provider_cost_amount, sale_amount, latency_ms, is_stream,
  status(success/failed/timeout), error_code, created_at
```

> `api_key_encrypted`：AES-256-GCM 加密，密钥经环境变量 `TOKEN_PROVIDER_KEY` 注入（已是仓库安全红线）。**任何接口响应绝不返回明文/密文 Key。**

## 4. 模块结构

```
server/internal/modules/token_gateway/
  model/        token_provider.go / token_model.go / token_route.go / token_usage_log.go
  repository/   provider_repo / model_repo / route_repo / usage_log_repo
  service/      gateway_service(选路由+断路器+转发+用量)、admin_service(供应商/模型/路由 CRUD)
  handler/      chat_handler(SSE)、admin_handler
  dto/          route.go
server/migrations/000030_create_token_gateway_tables.*.sql
server/migrations/000031_seed_token_manage_permission.*.sql   -- 新权限码 token:manage（红线：建码必建 seed）
```

## 5. 接口契约（§6.4）

### 用户端（需登录）
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/token/models` | 列出可用逻辑模型（对外目录） |
| POST | `/api/token/chat/completions` | OpenAI 兼容对话（流式/非流式） |
| GET | `/api/token/usage` | 查本人用量 [扁平分页] |

Chat 请求体：`{ model, messages[], stream?, temperature?, max_tokens? }`（兼容 OpenAI messages 格式）。
- `stream=true` → SSE（`Content-Type: text/event-stream`），**网关不缓冲 body，直接透传上游**（Logger/Recovery 中间件须确认不缓冲 response body）。

### 管理端（需 `token:manage` + 管理员双重认证）
```
GET/POST/PATCH  /api/admin/token/providers[/{id}]   -- 供应商（POST/PATCH 收 api_key_plaintext，响应不返回 Key）
GET/POST/PATCH  /api/admin/token/models[/{id}]       -- 模型 + 成本价/售价
GET/POST/PATCH  /api/admin/token/routes[/{id}]       -- 逻辑模型路由（weight/priority）
GET             /api/admin/token/usage               -- 全量用量 [扁平分页]
```
> 列表统一 **D-95 扁平分页** `{items,page,page_size,total}`。

## 6. 核心调用流程（§7.6，权威实现序列）

```
1. 鉴权：登录态 或 API Key（API Key 方案见 §决策2）
2. 校验调用资格：用户须持有该逻辑模型对应的 token_quota 额度（或后付费允许扣钱包）
3. 选路由：按 logical_model_code 查 token_model_routes → 按 weight 加权随机选上游
4. 断路器：选中上游熔断时，按 priority 升序切换备用路由
5. 请求上游：用 token_providers.api_key（解密）转发；流式则 SSE 透传
6. 统计：写 token_usage_logs（request_id、input/output/total tokens、provider_cost、sale_amount、latency、status）
7. 计费：上报 ProductUsageEvent 给 finance_consumer（见 §7）
8. 返回：非流式返回 OpenAI 兼容响应 + usage；流式直接透传，结束后异步落用量与计费
```

断路器建议：每上游维护「连续失败计数 + 半开探测」，失败率超阈值熔断 N 秒，期间走备用路由；指标进 token_usage_logs.status。

## 7. 计费挂接（零改 finance_consumer，仅作调用方）

`finance_consumer` 已有 `POST /api/internal/product-usage-events`（幂等 + 匹配 `product_billing_rules` + 扣费 + 写消费记录）。token_gateway 调用时按 input/output 各上报一次：

```go
ProductUsageEvent{
  EventID:        requestID,          // UUID
  UserID:         userID,
  ProductType:    "token",
  ProductCode:    "<token商品code>",
  UsageType:      "input_tokens",     // 再上报一条 output_tokens
  UsageAmount:    decimal(inputTokens),
  UsageUnit:      "tokens",
  IdempotencyKey: requestID + ":input_tokens",   // 全局唯一，天然幂等
}
```
- 计费规则（单价）在 `product_billing_rules` 配置（后端乙现有能力），token_gateway 不自己算钱包扣费。
- **预付额度**：扣减 `user_entitlements.quota_used`；**后付费**：finance_consumer 扣钱包。两种模式由商品配置决定。

## 8. 售卖挂接（复用你已建的访问/定价）

- Token 是 `product_type=token` 的商品，**谁能买、什么价**完全走现有 `product_role_access` + 角色价/会员价 —— 即你做的「组→角色→商品访问」直接治理 Token 售卖（如 VIP 组享高级模型折扣）。
- 购买 → provision 开通 → 生成 `token_quota` 资产与额度（`user_entitlements`）。

## 9. 安全约定

1. **api_key**：AES-256-GCM 加密存 `api_key_encrypted`，密钥来自 `TOKEN_PROVIDER_KEY` 环境变量；接口接收 `api_key_plaintext` 入参，**响应永不返回任何形式的 Key**。
2. **新权限码 `token:manage`** 必须同时建 seed migration（000031）——历史 P1 红线。
3. **限流**：Token 网关按用户级限流（§8.1），月度限额建议在额度账户维护。
4. SSE 透传不得把上游内容落日志明文（仅落 tokens/状态等元数据）。

## 10. 依赖与边界

| 依赖 | 用途 |
|---|---|
| finance_consumer（乙） | 上报用量事件扣费（调用方，不改其代码） |
| product / billing_rule（乙） | token 商品、计费规则配置 |
| asset / provision（丙） | token_quota 额度资产开通与扣减 |
| iam（甲） | 调用鉴权、`token:manage` 权限校验中间件 |

## 11. Migration
- `000030` 建 4 张 token 表；`000031` seed 权限码 `token:manage`。（当前最新 000029，顺序接续。）

## 12. 工作量与归属（待确认）
| 部分 | 建议归属 |
|---|---|
| token_gateway 模块（表/CRUD/路由/转发/SSE/用量） | **后端乙** 或新设专职负责人（设计建议最先拆独立服务） |
| `token:manage` 权限码 + 鉴权中间件挂接 | 后端甲 |
| token 商品/计费规则配置 | 后端乙（配置 + 既有能力） |
| token_quota 额度开通/扣减 | 后端丙（asset/provision） |
| 用户端模型市场/对话页、管理端供应商/模型/路由页 | 前端乙/前端甲 |

## 13. 待确认决策
1. ~~**模块归属**~~ ✅ **已定：新设专职负责人「后端工程师丁」（backend-d）**，负责第二阶段 AI 模块 token_gateway（优先）/ agent / skill，agent 定义见 `.claude/agents/后端工程师丁.md`。
2. **调用鉴权方式**：仅登录态 Bearer，还是同时支持「平台签发的 API Key」（供 agent/外部程序调用）？后者需加一张 api_keys 表 + 鉴权中间件。
3. **计费模式默认**：预付额度（扣 token_quota）为主，还是允许后付费（直扣钱包）？或两者按商品可选。
4. **上游对接范围**：首批接哪几家供应商 / 转发哪些模型（含成本价、售价）—— 实现前需定 SSOT；可参考各家最新模型与定价（推荐将最新 Claude 模型纳入可转发模型集）。
