# Token 网关 ↔ 甲/乙/丙 集成对接清单

> 配套：`docs/backend-token-gateway-design.md`（v3 架构：Molin 门面 + 自写薄转发器，首批 OpenAI/DeepSeek/Kimi 全 OpenAI 兼容，不外接 one-api）
> 用途：把 token 网关**深度复用**平台已建的用户/财务/应用模块（单账号、单钱包、统一商品），列清每个模块**要新增/暴露的接口**、归属、顺序与整合调用时序。
> 读者：后端甲/乙/丙/丁、运维、产品经理。

## 0. 集成原则

- **复用，不另起炉灶**：token 网关用平台**同一套用户(甲)、同一个钱包/商品(乙)、同一套资产/开通(丙)**，不建独立用户/账本。
- **门面(丁)只编排**：丁的 token_gateway 门面负责「鉴权→门禁→扣费→自写转发器转发上游→记账」，扣费/开通/鉴权的**底层能力由甲/乙/丙各暴露一个接口**，丁通过 service 接口调用，不跨改他人模块。转发引擎为自写（首批三家全 OpenAI 兼容，纯透传），不外接 one-api。
- **必要的新增**：甲/乙/丙均为「已完成」模块，但集成需要各补一小块接口（见下）。这是集成工作，不是重写。

## 1. 现成可接 vs 必须补（总览）

| 接入项 | 模块 | 状态 |
|---|---|---|
| 用户账号 / 登录中间件 | 甲 | ✅ 现成（复用 RequireAuth） |
| 钱包按量扣费（finance_consumer 上报） | 乙 | ✅ 现成 |
| 商品 / 订单 / 定价规则 | 乙 | ✅ 现成（加一个 token 商品即可） |
| 资产 / 权益模型 | 丙 | ✅ 现成 |
| 平台 sk 系统 + 双模式鉴权中间件 | **甲** | ✗ 新增 |
| `token:manage` 权限码 + seed | 甲 | ✗ 新增 |
| `product_type=token` 开通处理器 TokenProvisioner | **丙** | ✗ 新增 |
| 套餐额度扣减接口（预付才需要） | 丙 | ✗ 新增（重新立项） |
| 「用户能否使用某商品」门禁 | 乙/丙 | ✗ 二选一补（CanUse 或「持有 active 资产」查询） |

## 2. 接入 甲（用户管理 / auth / iam）

### 2.1 复用（现成）
- 门面**管理端**接口（建/列/吊销 sk、查用量）直接包 `middleware.RequireAuth`，用现成 JWT 认登录用户。

### 2.2 新增（甲负责）
**A. 平台 sk 系统**（沿用 Refresh Token「只存 HMAC、明文只回一次」模式）
```go
// 表 api_keys：id, user_id, key_hash, key_prefix, name, status,
//             billing_mode(postpaid/prepaid), source_id(套餐 entitlement_id 或 NULL=钱包),
//             model_scope, rate_limit, expires_at, last_used_at, created_at
// 接口（甲）
IssueKey(ctx, userID, name, billingMode, sourceID, modelScope) (plaintextSK string, err error) // 明文仅此一次返回
RevokeKey(ctx, userID, keyID) error
ListKeys(ctx, userID) ([]ApiKeyView, error)          // 不含明文/hash
ResolveKey(ctx, plaintextSK) (*KeyPrincipal, error)  // 供门面 /v1 鉴权：hash→查→返回 user_id+billing_mode+source_id+scope；校验 status/expires
```
**B. 双模式鉴权中间件**：`/v1/*` 端点用 sk 认人（调 `ResolveKey`），管理端用 JWT。两条路最终都注入 `user_id` 到 context，门面后续逻辑一致。
**C. 封禁联动**：用户被封 → 其名下所有 sk 失效（`ResolveKey` 校验用户 status，或封禁时批量置 key status=revoked）。
**D. 权限码**：`token:manage`（管理端配模型目录/看全量用量），**必须同时建 seed migration**（红线）。

## 3. 接入 乙（财务管理 / billing / product / order）

### 3.1 复用（现成）
- **按量付费扣钱包**：门面读到 usage → 上报 `POST /api/internal/product-usage-events`（`ProductUsageEvent{ProductType:"token", UsageType:"input_tokens"/"output_tokens", UsageAmount, IdempotencyKey:requestID:usageType}`）→ finance_consumer 匹配 `product_billing_rules` 算钱 → 扣钱包。**零改**。
- **套餐购买**：token 套餐 = `product_type=token` 商品，走现有 `POST /orders` 下单 + 钱包支付；售价单价配在 `product_billing_rules`。
- 角色/会员定价复用现有 `product_prices`。

### 3.2 新增/配置（乙负责）
- 建若干 token 商品：「按量服务」+ 各档「套餐」（金额套餐或 token 套餐）；
- 配套餐与按量的 `product_billing_rules`（input/output 单价 = 售价）；
- （门禁二选一之一）在 product/service 暴露 `CanUse(ctx, userID, productID) bool`（封装 roleIDs + `product_role_access.can_use`）。

## 4. 接入 丙（应用管理 / app / asset / provision）

### 4.1 复用（现成）
- 资产/权益模型 `user_assets` / `user_entitlements` 直接用。

### 4.2 新增（丙负责）
**A. TokenProvisioner**（仿现有 AppProvisioner）
```go
// 注册：provisionService.RegisterHandler("token", NewTokenProvisioner(...))
// 开通：订单 paid → 触发 → 按计费模式产出
//   后付费(按量)：建一条「token 服务」资产(asset_type=token_service, status=active) —— 仅标记"有资格用"
//   预付(套餐)：建一条 entitlement(entitlement_type=token_package, quota_total=套餐金额或token数, quota_unit=CNY/tokens)
//   并（预付）触发甲 IssueKey 绑定该 entitlement_id，把 sk 返回给用户
```
**B. 套餐额度扣减接口**（预付才需要，重新立项；按业务维度定位）
```go
// 内部接口（X-Internal-Token + IP 白名单 + 幂等键）
POST /api/internal/entitlement-consume
// body: {user_id, entitlement_id 或 (product_id+entitlement_type), amount, unit, idempotency_key}
// 行为：余额(quota_total-quota_used) >= amount 则 quota_used += amount，否则拒绝（余额不足）
```
**C. （门禁二选一之另一）** 暴露「用户是否持有该 token 商品的 active 资产/权益」查询接口（比 CanUse 更贴「买了才能用」语义，推荐）。
**D. （可选）** 把「AI 对话/生图」作为 `application` 上架到现有应用市场，用户从应用市场进入 → 调门面。

## 5. 门面（丁）编排：整合后的调用时序

### 5.1 买套餐 → 开通
```
用户 → POST /orders 买 token 套餐(乙) → 钱包支付(乙) → 订单 paid
   → provision 触发 TokenProvisioner(丙) → 建 entitlement(套餐余额)
   → 触发甲 IssueKey(billing_mode=prepaid, source_id=entitlement_id) → 返回 sk 给用户
```
（按量：用户开通按量服务 → 建 token_service 资产(丙) → 甲 IssueKey(postpaid, source=钱包) → 返回 sk）

### 5.2 调用扣费（门面核心）
```
请求(Authorization: Bearer sk) 
 1. 甲 ResolveKey(sk) → user_id + billing_mode + source_id + model_scope（/v1 用 sk 鉴权）
 2. 模型在 token_models 目录且 active；在 model_scope 内
 3. 门禁：持有 active token 资产/权益(丙 查询 或 乙 CanUse)
 4. 余额闸：postpaid→钱包余额>0(乙)；prepaid→套餐余额>0(丙)
 5. 自写转发器：查 token_models→渠道+上游模型名→换 base_url/key/模型名→转发上游（流式 SSE 不缓冲）→ 读响应 usage
 6. 算金额(product_billing_rules 售价) 并扣费：
       postpaid → 上报 finance_consumer 扣钱包(乙)
       prepaid  → 调 /api/internal/entitlement-consume 扣套餐(丙)
 7. 写 token_usage_logs（按 api_key_id 可做单 sk 计费统计）
```

### 5.3 流式扣费策略（必须实现）
- 转发时对上游开 `stream_options.include_usage`，usage 在末尾 chunk；
- 「先校验余额>0、结束后结算扣费」；防透支：余额低于阈值拒绝新请求，或并发按 sk 串行/预扣估算。

## 6. 归属与交付顺序

| 步骤 | 谁 | 是否阻塞门面 |
|---|---|---|
| 决策：计费模式（按量/套餐/两者） | 你/PM | 决定丙是否要建额度扣减 |
| 甲：sk 系统 + 双模式鉴权 + `token:manage` seed | 后端甲 | 门面 chat 鉴权依赖 |
| 乙：token 商品 + 计费规则 +（门禁）CanUse | 后端乙 | 套餐购买/计费依赖 |
| 丙：TokenProvisioner +（预付）额度扣减 +（门禁）持有查询 | 后端丙 | 开通/预付扣费依赖 |
| 丁：门面（渠道/目录/转发器/编排/记账） | 后端丁 | — |
| 运营：在 Molin 管理端配渠道（上游 api_key）+ 模型目录 | 运营 | 门面 chat 依赖 |
| 丁：`token-facade-tables` / `token-channels` | 后端丁 | **不依赖上游，可立即起步** |

**并行**：甲(sk) ∥ 乙(商品/规则) ∥ 丙(provisioner/扣减) ∥ 丁(渠道/目录/转发器)；门面的 chat/计费(丁)等甲/乙/丙就位后收口。**无需运维部署 one-api**（自写转发器，上游直连）。

## 7. 与「自包含」方案的取舍（已选集成）
- 本文档 = **深度集成**：单账号、单钱包、体验统一；代价是甲/乙/丙各补一接口。
- 备选「自包含」（token 网关自带 sk/额度/统计，零改甲乙丙）最快但有两套余额——已不采用，仅作为应急/验证备选记录在案。

## 8. 待确认
1. 计费模式（按量 / 套餐 / 两者）—— 决定丙额度扣减是否本期立项。
2. 门禁用「乙 CanUse」还是「丙 持有资产查询」（推荐后者）。
3. 套餐余额单位：金额(CNY) 还是 token 数。
4. 是否把 AI 对话/生图作为 application 上架应用市场（复用应用管理入口）。
