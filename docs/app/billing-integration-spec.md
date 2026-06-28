# 应用接入会员/商品计费 —— 开发对接规范（字段级）

> 📚 本文属于 [业务与计费总览](../business-billing-overview.md) 文档体系；面向**应用开发者**，规定开发一个应用时如何对接会员计费、商品计费与计费统计，精确到字段与流程。
> 配套阅读：[应用×财务商品扣费集成设计](./billing-integration-design.md)（概念与运营配置）、[商品与商品计费](../product-and-billing-guide.md)、[会员管理](../membership-management-guide.md)、[Token 计费契约](../backend-token-billing-contract.md)。
> 统一响应信封：`{ "code": 0, "message": "ok", "data": ... }`。金额一律字符串 decimal。

---

## 0. 一句话先建立认知

> **你（应用开发者）几乎不写“扣费逻辑”本身**。平台已把定价、扣钱包、扣额度、订单、对账全部做好。
> 你要做的只有三件事：① 让运营把你的应用**配成商品**（不写代码）；② 在你的应用里**在正确时机调用一个上报/扣减接口**（写少量代码）；③ 按需**查会员状态**做特性门禁。

---

## 1. 角色与分工（务必先看这张表）

“计费”不是一个模块的事，而是多个功能协作。下表区分每个功能**由谁负责、作用是什么、你是否要写代码**：

| 功能 | 归属模块 / 负责人 | 作用 | 应用开发者要做什么 |
|---|---|---|---|
| 应用元数据 | `app`（后端丙） | 图标/描述/适配器登记 | ❌ 运营在管理端配 |
| 商品/套餐/价格 | `product`（后端乙） | 定“怎么卖、卖多少钱” | ❌ 运营在管理端配 |
| **会员价** | `product_prices` 会员档（后端乙） | 会员自动享专属价 | ❌ 运营配价；购买时自动命中 |
| 计费规则 | `product_billing_rules`（后端乙） | 定“用一次扣多少” | ❌ 运营配规则 |
| 订单状态机 | `order`（后端乙） | 下单→支付→开通流转 | ❌ 平台已实现 |
| 购买扣费（钱包） | `billing`（后端乙） | 下单时扣钱包 | ❌ 购买接口已实现 |
| **使用扣费·后付** | `finance_consumer`（后端乙） | 收用量事件→扣钱包→写流水 | ✅ **你上报用量事件** |
| **使用扣费·预付** | `asset`（后端丙） | 扣套餐额度（reserve/settle/consume） | ✅ **你调额度接口** |
| 资产/权益 | `asset`（后端丙） | 开通后的资产与额度 | ❌ 查询即可 |
| 开通路由 | `provision`（后端丙） | 按 product_type 开通生成资产 | ⚠️ 复用 `application` 无需写；新资源类型需实现 `ProvisionHandler`（平台后端协助） |
| 会员状态查询 | `membership`（后端丙） | 回答“是不是有效会员” | ✅ **按需查询做门禁** |
| 计费统计/对账 | `finance_consumer` / `billing` | 消费流水、钱包流水查询 | ❌ 调查询接口即可 |

> 黄色 ⚠️ 与绿色 ✅ 才是“应用开发者要落地的代码点”，其余均为配置或平台已实现。

---

## 2. 接入决策树（先定方案再动手）

```
你的应用要怎么向用户收费？
│
├─ A. 只在购买时收一次（买断/月卡/年卡）
│     → 不写任何扣费代码。配置：商品+套餐+价格(+会员价)+访问权限 即可。
│        用户购买走平台购买接口，开通生成资产。【见 §3 购买扣费】
│
├─ B. 按使用量后付（用多少扣多少，扣钱包）
│     → 配置计费规则(billing_mode=postpaid) + 你的应用在使用后上报用量事件。
│        【见 §4 使用扣费·后付】
│
└─ C. 预付额度（先买额度包，用时扣额度，不扣钱包）
      → 套餐 quota_json 声明额度 + 计费规则(billing_mode=prepaid) +
        你的应用在使用时调 asset 额度接口（reserve→settle 或 consume）。
        【见 §5 使用扣费·预付】

A / B / C 可叠加：如“买套餐额度(A+C)”“开通免费按量后付(B)”。
会员折扣对 A 自动生效（会员价）；会员特性门禁见 §6。
```

---

## 3. 购买扣费（A）：你只需配置，不写代码

平台购买入口唯一：`POST /api/products/{id}/purchase`（带 `Idempotency-Key` 头）。链路：实名校验→购买权限→会员门槛→取价（会员价>角色价>默认价）→算总价→幂等→建订单→**扣钱包+订单 paid 同事务**→开通生成资产。

**应用开发者职责**：仅确保运营完成配置（应用 active → 商品 `product_type=application` 且 `business_ref_id=应用ID` → 套餐 → 价格 → 访问权限 → 上架）。详见 [集成设计文档 §四/案例 1–6](./billing-integration-design.md)。

> 会员价**自动生效**：用户是有效会员且该套餐配了对应会员档价格，购买时自动按会员价扣费——你和运营都无需在购买时做额外判断。

---

## 4. 使用扣费·后付（B）：上报用量事件

这是**最常见**的应用计费接入点。你的应用在用户产生一次计费用量后，向计费消费端上报一条事件，平台据计费规则扣钱包并记流水。

### 4.1 接口契约（字段级）

```
POST /api/internal/product-usage-events        （内部接口，不对外）
Headers:
  Content-Type: application/json
  X-Internal-Token: <环境变量 INTERNAL_API_TOKEN 的值>   ← 主闸，缺失/不符直接 40003
  （来源 IP 必须在 INTERNAL_ALLOWED_IPS 白名单内，或本机 127.0.0.1；Nginx 注入 X-Real-IP）
```

请求体（`ProductUsageEventReq`）：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `event_id` | string | 是 | UUID，业务事件唯一标识（对账用） |
| `user_id` | uint64 | 是 | 计费归属用户 |
| `product_id` | uint64 | 是 | 商品 ID（规则匹配主键之一） |
| `product_type` | string | 否 | 冗余，便于排查 |
| `product_code` | string | 否 | 冗余 |
| `product_plan_id` | uint64 | 否 | 套餐 ID；传则优先精确匹配该套餐规则，否则用商品级通用规则（plan_id 为 NULL） |
| `instance_id` | uint64 | 否 | 业务实例 ID（如某网盘空间 ID），写入流水便于区分 |
| `usage_type` | string | 是 | 用量类型，**必须与计费规则一致**（如 `storage_used`/`calls`/`input_tokens`） |
| `usage_amount` | decimal(字符串) | 是 | 本次用量（如 `"25"`） |
| `usage_unit` | string | 否 | 计量单位（如 `GB`/`count`/`tokens`） |
| `occurred_at` | RFC3339 | 否 | 用量发生时间 |
| `idempotency_key` | string | 是 | **全局唯一**，推荐 `event_id:usage_type`，重复上报不二次扣费 |

响应 `data`（`ConsumptionResultResp`）：

| 字段 | 说明 |
|---|---|
| `consumption_record_id` | 消费记录 ID |
| `amount` | 本次实扣金额（decimal） |
| `idempotency_key` | 回显幂等键 |
| `wallet_transaction_id` | 钱包扣费流水 ID（即时返回，列表查询不持久化该字段） |

### 4.2 平台侧处理（你需要知道的行为）

1. **幂等**：按 `idempotency_key` 查 `product_consumption_records`，命中直接返回原结果。
2. **规则匹配**：按 `(product_id, product_plan_id, usage_type)` 命中 `product_billing_rules`；**无匹配规则返回 HTTP 400 + `code=40000`、`message="未找到匹配的计费规则"`**。⚠️ 注意该错误码与参数错误同为 `40000`，需**靠 message 串判别**——命中此 message 时应用应**静默跳过**（说明该商品未配该类计费），不要当错误重试。
3. **金额**：`amount = (usage_amount − free_quota 适用部分) × price_amount`；全部落在免费额度内（`amount ≤ 0`）时**不扣费、不报错**。
4. **扣费**：`WalletService.DeductTx` 扣钱包（FOR UPDATE 行锁 + 乐观锁，无负余额）；余额不足按钱包错误码（`60001`）。
5. **流水**：写 `product_consumption_records`，凭 `event_id` 可对账。

### 4.3 调用范例（你的应用服务端，伪代码）

```go
// 用户一次计费动作完成后调用（如网盘按小时巡检存储用量、AI 一次提问结束）
func reportUsage(userID, productID, planID uint64, usage decimal.Decimal) error {
    body := map[string]any{
        "event_id":        uuid,                       // 你生成的 UUID
        "user_id":         userID,
        "product_id":      productID,                  // 来自商品配置
        "product_plan_id": planID,                     // 用户所购套餐，可选
        "usage_type":      "storage_used",             // 必须与规则一致
        "usage_amount":    usage.String(),             // "25"
        "usage_unit":      "GB",
        "occurred_at":     time.Now().Format(time.RFC3339),
        "idempotency_key": uuid + ":storage_used",     // 全局唯一
    }
    // 必带 X-Internal-Token；部署在白名单内网/经 Nginx 注入 X-Real-IP
    resp := httpPost("/api/internal/product-usage-events", body,
        header("X-Internal-Token", os.Getenv("INTERNAL_API_TOKEN")))
    // code==0 成功；code==40000 且 message=="未找到匹配的计费规则" → 静默跳过；余额不足(60001) → 按业务降级
    return handle(resp)
}
```

> **何时上报由你决定**：可按动作（每次调用）、按周期（每小时巡检用量）、按阈值。每条事件一个唯一幂等键即可。

---

## 5. 使用扣费·预付（C）：调 asset 额度接口

用户先买“额度套餐”（走 §3 购买扣费，套餐 `quota_json` 声明额度，开通生成 `user_entitlements`）。使用时**不扣钱包，扣额度**。额度接口在 `asset` 模块，**不经 finance_consumer**。

> `entitlement_id` 从哪来？两条路径：
> - **用户端**：用户购买套餐后，查 `GET /api/my/entitlements`（用户 JWT）得到其权益（含 `id`、`quota_total/quota_used`、`quota_unit`、`status`、`expires_at`）。
> - **第三方应用（SSO 票据）**：应用只换得 `{user_id, app_id, product_id}`、没有 `entitlement_id`、也拿不到用户 JWT，用内部接口 `GET /api/internal/user-entitlements?user_id={uid}&product_id={pid}`（`X-Internal-Token`）解析——见 §5.0。

### 5.0 定位 entitlement_id（第三方应用 SSO 场景）

```
GET /api/internal/user-entitlements?user_id={uid}&product_id={pid}    （内部接口，不对外）
Headers: X-Internal-Token: <INTERNAL_API_TOKEN>   （+ IP 白名单）
```

响应 `data`：`{ entitlements: [ {entitlement_id, user_id, quota_total, quota_used, quota_reserved, remaining, status, expires_at, usable} ] }`

- 仅返回该用户在该商品下 **status=active** 的权益（suspended/cancelled 等非 active 不返回）；其中 `usable=false`（已过期 / 额度耗尽）的也会返回，应用应**跳过**、只取 `usable=true` 的。
- 不限量（`quota_total` 为 NULL）时 `quota_total`/`remaining` 为 `null`，`status=active` 且未过期即 `usable=true`。
- 错误：`40003` 鉴权失败；`40000` 参数错误（`user_id`/`product_id` 缺失或非正整数）。
- 拿到 `entitlement_id` 后，再调下面 §5.1/§5.2 的 balance/reserve/settle/consume 扣额度。

### 5.1 两种扣减方式，按并发需求二选一

| 方式 | 适用 | 接口 |
|---|---|---|
| **一步扣减** | 用量已确定、无需先占后结 | `POST /api/internal/entitlement-consume` |
| **预占→结算/释放** | 转发前不知道实际用量（如 LLM），先占额度防并发透支，拿到实际再多退少补 | `reserve` → `settle`（成功）/ `release`（失败） |

所有接口同样需 `X-Internal-Token` 主闸 + IP 白名单。

### 5.2 字段契约

**一步扣减** `POST /api/internal/entitlement-consume`
- 请求：`{ entitlement_id, user_id, amount(>0,decimal), idempotency_key(唯一) }`
- 响应 `data`：`{ entitlement_id, quota_total, quota_used, remaining, status }`
- 错误：`60005` 额度不足/权益不可用；`40003` 鉴权失败或权益不属于该用户；`40400` 权益不存在；`40000` 参数错误。
- 说明：**不限量额度（`quota_total` 为 NULL）时，`quota_total`/`remaining` 字段为 `null`**（omitempty 指针），接入方需容许其缺省。

**预占** `POST /api/internal/entitlement-reserve`
- 请求：`{ entitlement_id, user_id, amount, idempotency_key }`
- 响应：`{ hold_id, reserved, available, status }`（`available = total − used − reserved`）
- 错误：同上（`60005` 额度不足）。

**结算** `POST /api/internal/entitlement-settle`（多退少补，`actual ≤ 预占额`计入 `quota_used`）
- 请求：`{ hold_id 或 idempotency_key, actual_amount(≥0) }`
- 响应：`{ hold_id, status, settled_amount, quota_used, quota_reserved, available }`

**释放** `POST /api/internal/entitlement-release`（失败/异常路径，不计 `quota_used`）
- 请求：`{ hold_id 或 idempotency_key }`
- 响应：`{ hold_id, status, settled_amount(=0), quota_used, quota_reserved, available }`

**余额只读** `GET /api/internal/entitlement-balance?entitlement_id={id}&user_id={uid}`（前置闸用，不加锁不扣减）
- 响应：`{ entitlement_id, user_id, quota_total, quota_used, quota_reserved, remaining, status, expires_at, usable }`
- `quota_reserved`：当前已预占额度（恒返回）；`remaining = quota_total − quota_used − quota_reserved`（含已预占，避免误判可用）。
- 不限量（`quota_total` 为 NULL）时 `quota_total`/`remaining` 为 `null`，此时 `status=active` 且未过期即 `usable=true`。
- `usable=false`（过期/暂停/额度耗尽）时门面应直接拒绝。

### 5.3 预占模式时序（你的应用/门面侧）

```
转发前：reserve(entitlement_id, amount=预估上限, idem) → 拿 hold_id；available 不足 → 拒 60005
执行业务 → 得到实际用量 actual
成功：settle(hold_id, actual_amount=min(actual, 预占额))    // quota_used += settled
失败：release(hold_id)                                       // 仅归还预占，quota_used 不变
```

> **红线**：prepaid 与 postpaid **互斥**，同一次使用绝不能既上报 product-usage-events（扣钱包）又扣额度。由你按 `billing_mode` 选一条路。
>
> **并发红线**：防超用靠 `reserve`/`consume` 的原子扣减（平台 `FOR UPDATE` 行锁），**不要在应用里写"查 balance → if 够 → 再扣"**——并发下两个请求都查到够、都通过 if 会超扣变负。`entitlement-balance` 只用于 UX 软前置，够不够的最终判定交给扣减调用。

---

## 6. 会员计费对接：你基本不用管，按需做门禁

会员对计费的影响只有一处：**会员价**。它由 `product_prices` 的会员档承载，购买时自动命中——**应用和运营都无需在扣费时判断会员身份**。

你**唯一可能要写代码**的场景：应用要按会员等级**开放/限制功能**（如“黄金会员才能用高级导出”）。此时查会员状态：

```
GET /api/my/membership        （需登录，查本人有效会员）
  → data.membership = null               // 非会员
  → data.membership = {                  // 有效会员
      level_id, level_code, level_name,
      status:"active", started_at, expires_at(null=永久) }
```

判定有效会员的口径（平台已保证）：`status=active AND (expires_at IS NULL OR expires_at > NOW())`。

> 公开页可用 `GET /api/memberships`（等级列表）、`GET /api/memberships/{id}/benefits`（权益）做营销展示。
> ⚠️ 当前“购买会员商品自动开通”链路**尚未接线**（provision 未注册 membership 处理器），会员开通以管理端手动为准，详见 [会员文档·现状必读](../membership-management-guide.md)。

---

## 7. 计费统计与对账：调查询接口

| 维度 | 接口 | 权限 | 说明 |
|---|---|---|---|
| 本人消费明细 | `GET /api/product-consumption-records` | 登录 | 扁平分页；query：`product_id`/`usage_type`/`created_from`/`created_to` |
| 全量消费明细 | `GET /api/admin/product-consumption-records` | `wallet:view` | 上述 query + `user_id` |
| 我的资产 | `GET /api/my/assets` `GET /api/my/assets/{id}` | 登录 | 购买开通的资产 |
| 我的权益额度 | `GET /api/my/entitlements` | 登录 | prepaid 额度余量；**无 `remaining` 字段，需自己算 `quota_total − quota_used`**（服务端要现成 `remaining`/`usable` 用 `GET /api/internal/entitlement-balance`） |

消费记录项（`ConsumptionRecordItem`）字段：`id, user_id, product_id, product_plan_id, instance_id, usage_type, usage_amount, usage_unit, amount, event_id, created_at`。
> 列表不含 `wallet_transaction_id`（仅上报响应即时返回）；对账以 `event_id` 为锚点。钱包余额/充值流水走 billing 模块「我的钱包」接口。

---

## 8. 三处必须一致的关键字段（最易出错点）

“使用扣费”链路断裂的头号原因是 `usage_type` 不一致。**以下三处必须用同一个 `usage_type` 字符串**：

```
① 应用适配器声明        application_adapters.usage_event_types_json = ["storage_used"]
② 商品计费规则          product_billing_rules.usage_type           = "storage_used"
③ 你上报的事件          product-usage-events.usage_type            = "storage_used"
```

任一不一致 → 事件匹配不到规则 → “未找到匹配的计费规则” → 不扣费（静默）。
其余对齐项：`product_id`/`product_plan_id` 来自商品配置；`entitlement_id` 来自 `GET /api/my/entitlements`；`idempotency_key` 全局唯一且可复算。

---

## 9. 幂等与鉴权规范（强制）

- **幂等键**：每条计费请求必带，且全局唯一、可复算。约定格式 `业务请求ID:类型`（如 `req_abc:calls`、`evt_1:storage_used`、`req_abc:quota`）。重复请求平台保证不二次扣费。
- **内部接口鉴权**：`/api/internal/*` 全部要求 `X-Internal-Token`（= 环境变量 `INTERNAL_API_TOKEN`，常量时间比较），未配置时 fail-closed 全拒；并受 `INTERNAL_ALLOWED_IPS` IP 白名单约束（Nginx 注入 `X-Real-IP`，不信任 `X-Forwarded-For`）。**这些是会改动用户余额/额度的接口，禁止暴露公网。**
- **金额**：一律字符串 decimal，禁止 float。
- **错误码**：余额不足 `60001`（钱包）/`60005`（额度）；鉴权或归属不符 `40003`；不存在 `40400`；参数错误 `40000`。

---

## 10. 新资源类型的开通对接（仅当不复用 application 时）

若你的应用是一种**全新资源类型**（不走 `product_type=application` 的“确认即开通”），需要在 `provision` 模块实现并注册一个开通处理器（属平台后端工作，应用开发者提供业务逻辑）：

```go
// 实现 provision/service.ProvisionHandler 接口
type ProvisionHandler interface {
    Provision(ctx, req ProvisionReq) (*ProvisionResult, error) // 开通：启动实例/初始化，返回 BusinessInstanceID、ExpiresAt、AssetType(可选)
    Renew(ctx, assetID, planID uint64) error                   // 续期
    Suspend(ctx, assetID uint64) error                         // 暂停
    Resume(ctx, assetID uint64) error                          // 恢复
    Cancel(ctx, assetID uint64) error                          // 取消（退款联动）
}
// 在 bootstrap/app.go 注册： provisionService.RegisterHandler("你的product_type", yourProvisioner)
```

开通成功后由 `ProvisionService` 统一调 `asset.CreateAsset` 生成资产，并按 `plan.quota_json` 生成 prepaid 额度（entitlement）——**你无需自己写资产/额度落库**。

> 多数应用类商品直接复用 `application`（`AppProvisioner`：校验商品 active 即开通成功，无实例），无需写本节代码。

---

## 11. 落地步骤清单（端到端）

```
□ 1. 定方案：A 购买扣费 / B 后付 / C 预付（§2 决策树）
□ 2. 运营配置：应用元数据 active → 商品(product_type=application, business_ref_id=应用ID)
□ 3. 运营配置：套餐 + 价格(默认/角色/会员价) + 访问权限(can_buy/can_use) → 上架
□ 4. 若 B/C：运营配 product_billing_rules（usage_type、price_amount、billing_mode、free_quota）
□ 5. 若 C：套餐 quota_json 声明额度；用户购买后查 /api/my/entitlements 拿 entitlement_id
□ 6. 应用编码：在使用时机调用
      · B → POST /api/internal/product-usage-events（带 X-Internal-Token、幂等键）
      · C → reserve/settle/release 或 consume（带 X-Internal-Token、幂等键）
□ 7. 校验三处 usage_type 一致（§8）
□ 8. 按需：应用查 /api/my/membership 做会员特性门禁
□ 9. 对账：/api/product-consumption-records、/api/my/entitlements
□ 10. 部署：INTERNAL_API_TOKEN、INTERNAL_ALLOWED_IPS 配好，内部接口不暴露公网
```

---

## 12. 常见问题

| 问题 | 答案 |
|---|---|
| 应用建好了为什么用户买不到？ | 还没配成商品（缺 `product_type=application` 且 `business_ref_id` 的 product）或商品未 active |
| 上报了用量为什么没扣钱？ | 多半 `usage_type` 三处不一致，或全在 `free_quota` 内，或 `amount≤0` |
| 会员价要在应用里判断吗？ | 不要，购买时自动命中会员档价格 |
| 后付和预付能同时对一次使用扣吗？ | 不能，互斥，按 `billing_mode` 选一条 |
| 内部接口返回 40003？ | 缺 `X-Internal-Token` 或 IP 不在白名单 |
| 重复上报会重复扣吗？ | 不会，`idempotency_key` 全局唯一即幂等 |
| `entitlement_id` 从哪拿？ | 用户购买额度套餐后 `GET /api/my/entitlements` |
</content>
