# 应用 × 财务商品 结合实现用户扣费 —— 集成设计文档

> 📚 本文属于 [业务与计费总览](../business-billing-overview.md) 文档体系（商品·会员·应用·扣费），建议先读总览建立全局认知。
> 🛠️ 要动手写应用对接代码？看 [应用接入会员/商品计费 开发对接规范（字段级）](./billing-integration-spec.md)。
> 读者：后端、运营、产品、测试
> 关联模块：`app`（应用元数据）、`product`（商品/套餐/价格/计费规则）、`order`（订单）、`billing`（钱包扣费）、`provision`（开通）、`asset`（资产/权益）、`finance_consumer`（用量计费）
> 关联文档：`../app-management-guide.md`、`../product-and-billing-guide.md`、`../membership-management-guide.md`、`../backend-token-billing-contract.md`
> 统一响应信封：`{ "code": 0, "message": "ok", "data": ... }`

---

## 一、为什么要“结合”：职责分离的设计

平台刻意把“应用是什么”和“应用怎么收钱”拆成两个模块，互不越界：

| 模块 | 负责 | 不负责 |
|---|---|---|
| **应用管理 `app`** | 应用的业务元数据（图标、描述、回调、适配器配置） | 套餐、价格、权限、扣费 |
| **财务商品 `product`** | 套餐、价格、角色权限、计费规则 | 应用图标/描述等业务详情 |

**桥梁**：`products.product_type = "application"` 且 `products.business_ref_id = applications.id`。

```
applications（应用元数据）            products（财务商品）
  id = 7  ◄───────────────────────  business_ref_id = 7
  code = netdisk-basic               product_type = "application"
  name/icon/description              product_code = netdisk-basic-product
  adapter_config_json                  └─ product_plans   套餐（月付/年付/一次性）
                                        └─ product_prices  价格（默认/角色/会员三层）
                                        └─ product_role_access 谁能买/用
                                        └─ product_billing_rules 用量计费规则
```

> 一句话：**应用提供“身份和对接方式”，商品提供“怎么卖、怎么扣钱”。把同一个应用 ID 用 `business_ref_id` 挂到一个商品上，应用就具备了被购买和被扣费的能力。**

---

## 二、两种扣费模式（核心）

一个应用对用户的扣费，可能同时存在两层，按业务需要任选或叠加：

| 扣费模式 | 何时扣 | 配置载体 | 结算路径 | 适用 |
|---|---|---|---|---|
| **① 购买扣费（一次性）** | 用户下单购买时 | `product_prices`（套餐价格） | 钱包扣订单总价 | 买月卡/年卡/一次性开通/额度包 |
| **② 使用扣费（按用量）** | 用户使用应用过程中持续扣 | `product_billing_rules`（计费规则） | 应用上报用量 → 钱包扣 / 扣套餐额度 | 按调用次数、按 token、按存储量等 |

- **只配①**：买断式应用（如“买一个月网盘，期间随便用”）。
- **只配②**：纯用量应用（如“开通免费，按调用次数后付”）。
- **①+②**：先买套餐（预付额度），用时扣额度（prepaid），额度耗尽拒绝。

计费规则的 `billing_mode` 决定使用扣费走哪条路：
- `postpaid`（后付）→ 用多少扣多少，**扣钱包**。
- `prepaid`（预付）→ 购买时已发额度（entitlement），用时**扣额度，不扣钱包**。

---

## 三、端到端扣费链路

### 链路 A：购买扣费（一次性，下单即扣）

用户购买应用商品时，`PurchaseService.Purchase` 一条龙完成（见 `product/service/purchase_service.go`）：

```
POST /api/products/{商品ID}/purchase   (Header: Idempotency-Key)
  │
  ├─ 1. 实名校验          real_name_status=verified，否则 70001
  ├─ 2. 购买权限校验      product_role_access.can_buy，否则 40003
  ├─ 3. 会员门槛校验      配了会员专属价则需命中会员等级，否则 40003
  ├─ 4. 取价 + 算总价     会员价>角色价>默认价；total = 单价 × quantity
  ├─ 5. 幂等检查          同 Idempotency-Key 直接返回已有订单
  ├─ 6. 建订单(pending)   order + order_items
  ├─ 7. 扣费(同一事务)    billing.DeductTx 扣钱包（乐观锁，最多重试3次）
  │                       └─ 余额不足 → 60001；瞬时锁冲突 → 409 重试
  ├─ 8. 订单 pending→paid （与第7步同事务，杜绝“钱扣了订单还pending”）
  └─ 9. 触发开通          provision.Provision(orderID, productID, planID, userID)
                          └─ 按 product_type="application" 路由到 AppProvisioner
                          └─ AppProvisioner 校验 product.status=active → 成功
                          └─ ProvisionService 创建 user_asset（资产/权益）
```

**关键设计**：
- 第 7、8 步在**同一数据库事务**内（`purchasePayTx`），进程崩溃同时回滚，消除“扣了钱订单还 pending（用户丢钱）”。
- 第 9 步开通失败**不回滚**已 paid 订单（钱已扣、订单已付），仅记 warn 供运维补偿；前端通过「我的资产」轮询到账。
- 幂等键防重复扣费：用户重复点击/网络重试只扣一次。

### 链路 B：使用扣费（按用量，使用时持续扣）

应用在用户使用时把“用量事件”上报给计费消费端（见 `finance_consumer/service/consumer_service.go`）：

> ⚠️ **两条扣费路径走不同接口，不要混淆**：
> - **postpaid（后付，扣钱包）** 走 `POST /api/internal/product-usage-events` → `finance_consumer`。该端点**只扣钱包**，不读 `billing_mode`、不碰额度。
> - **prepaid（预付，扣额度）** **不经过** product-usage-events，而是由门面（token_gateway 等）直接调 `asset` 模块的额度内部接口（预占 reserve / 结算 settle / 释放 release），扣 `user_entitlements` 额度。

postpaid 路径（`POST /api/internal/product-usage-events` → `finance_consumer/service/consumer_service.go`）：

```
应用/门面 ──上报用量事件──► POST /api/internal/product-usage-events   (内部接口，需 X-Internal-Token)
  body: { event_id, user_id, product_id, product_plan_id, usage_type, usage_amount, idempotency_key, ... }
  │
  ├─ 1. 幂等检查          按 idempotency_key 查 product_consumption_records，重复直接返回原结果
  ├─ 2. 匹配计费规则       按 (product_id, plan_id, usage_type) 命中 product_billing_rules
  │                       └─ 无规则 → ErrNoBillingRule（门面静默跳过，不报错）
  ├─ 3. 计算金额          amount = usage_amount × price_amount（扣除 free_quota 后）
  ├─ 4. 钱包扣费          WalletService.DeductTx 扣钱包（amount<=0 即全在免费额度内时跳过扣费）
  └─ 5. 写消费记录        product_consumption_records（带 event_id 可对账）
```

prepaid 路径（门面 → `asset` 模块额度接口，**不走上面的 product-usage-events**）：

```
门面（如 token_gateway）转发前 reserve 预占额度 → 结算 settle 多退少补 / 失败 release 释放
  └─ asset 模块对 user_entitlements 操作；额度不足 → 60005「权益额度不足」
```

**关键设计**：
- 每条用量事件必须带**全局唯一幂等键**（`request_id:usage_type`），杜绝重复扣费。
- 按量（`input_tokens`/`output_tokens`）与按次（`calls`）**二选一**配置在商品上，管理端强校验，避免重复收费。
- prepaid 走 entitlement 额度（`entitlement_holds` 预占 + settle 多退少补 + FOR UPDATE 行锁防透支），与钱包路径结构对称，**绝不同一次调用既扣钱包又扣额度**。详见 `../backend-token-billing-contract.md`。

---

## 四、落地操作流程（从“一个应用”到“能扣费”）

```
① 建应用元数据        POST /api/admin/apps                （得到 applications.id = 7）
② 注册适配器          POST /api/admin/app-adapters        （声明开通/续期等对接方式）
③ 上架应用            PATCH /api/admin/apps/7 status=active
④ 建财务商品          POST /api/admin/products
                      product_type="application", business_ref_id=7
⑤ 配套餐              POST /api/admin/products/{商品ID}/plans
⑥ 配价格（购买扣费）   PATCH /api/admin/products/{商品ID}/prices
⑦ 配访问权限          PATCH /api/admin/products/{商品ID}/access  can_buy/can_use
⑧ 配计费规则（使用扣费）POST /api/admin/product-billing-rules    （可选，按需）
⑨ 上架商品            PATCH /api/admin/products/{商品ID}/status active
─────────────────────────────────────────────────────────────
用户购买 → 链路A 扣购买费 → 开通生成资产 → 使用时 → 链路B 按用量扣费
```

> ①②③ 属应用模块；④~⑨ 属财务商品模块；缺了 ④ 应用永远无法被购买；缺了 ⑥ 用户取价失败无法下单。

---

## 五、操作教程案例

> 以“基础网盘”为例，演示把一个应用做成“**买一个月（购买扣费）+ 按存储量后付（使用扣费）**”的完整可扣费商品。
> `{{TOKEN}}` 为管理员 JWT（需 `app:manage` / `product:*` 权限）；`{{USER_TOKEN}}` 为用户 JWT。

---

### 案例 1：建应用并上架（应用模块）

**作用**：先把应用元数据登记好并上架。这是后续一切的前提，`applications.id` 将作为商品的 `business_ref_id`。

```bash
# 建应用（得到 id=7，draft）
curl -X POST https://api.example.com/api/admin/apps \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "code": "netdisk-basic", "name": "基础网盘", "type": "netdisk",
        "description": "个人云存储", "icon_url": "https://cdn.example.com/netdisk.png" }'

# 注册适配器（声明开通方式）
curl -X POST https://api.example.com/api/admin/app-adapters \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "app_code": "netdisk-basic", "app_name": "基础网盘", "app_type": "netdisk",
        "adapter_type": "internal", "service_name": "netdisk-provisioner",
        "supported_actions_json": "[\"provision\",\"renew\",\"cancel\"]",
        "usage_event_types_json": "[\"storage_used\"]" }'

# 上架应用
curl -X PATCH https://api.example.com/api/admin/apps/7 \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "status": "active" }'
```

**要点**：`usage_event_types_json` 声明该应用会上报哪些用量类型（如 `storage_used`），这是“使用扣费”的事件来源约定。应用此时只是登记好，**还不能买**。

---

### 案例 2：把应用挂成财务商品（关键桥梁）

**作用**：建立 `product → application` 的关联。`product_type="application"` 让开通时路由到 `AppProvisioner`；`business_ref_id=7` 指明这是哪个应用。**没有这一步，应用无法进入购买/扣费链路。**

```bash
curl -X POST https://api.example.com/api/admin/products \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{
    "product_type": "application",
    "product_code": "netdisk-basic-product",
    "name": "基础网盘",
    "business_ref_id": 7,
    "status": "draft"
  }'
```

**响应**：返回 `data.id`（设为商品 ID = 100），后续配套餐/价格/规则都挂在它下面。

**要点**：
- `product_type` 必须是已注册处理器的类型——`application` 已在 bootstrap 注册（`RegisterHandler("application", appProvisioner)`）。
- `business_ref_id` 指向 `applications.id`，开通时 `AppProvisioner` 据商品状态校验放行。

---

### 案例 3：配套餐 + 价格（实现“购买扣费”）

**作用**：定义“买什么规格、付多少钱”。价格写入 `product_prices`，用户下单时按此扣钱包——这就是**购买扣费**。

```bash
# 套餐：月付，30天
curl -X POST https://api.example.com/api/admin/products/100/plans \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "plan_code": "netdisk-monthly", "name": "网盘月付", "billing_type": "monthly", "duration_days": 30 }'
# → 得到 plan_id = 50

# 价格：默认价 19.9 元/月，会员等级2 优惠价 14.9 元/月
curl -X PATCH https://api.example.com/api/admin/products/100/prices \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "items": [
    { "product_plan_id": 50, "role_id": null, "membership_level_id": null, "price_amount": "19.90", "currency": "CNY" },
    { "product_plan_id": 50, "role_id": null, "membership_level_id": 2,    "price_amount": "14.90", "currency": "CNY" }
  ] }'
```

**要点**：`duration_days=30` 让开通的资产 `expires_at = 开通时间 + 30 天`；会员价让会员用户自动享 14.9（取价优先级见商品文档）。

---

### 案例 4：配访问权限 + 上架商品

**作用**：`can_buy` 决定谁能下单（购买扣费的前置闸），`can_use` 决定开通后谁能用。上架后商品才进入用户市场。

```bash
# 角色10 可买可用
curl -X PATCH https://api.example.com/api/admin/products/100/access \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "items": [ { "role_id": 10, "can_view": true, "can_buy": true, "can_use": true } ] }'

# 上架商品
curl -X PATCH https://api.example.com/api/admin/products/100/status \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "status": "active" }'
```

**要点**：到这里应用已可被购买，走的是**链路 A 购买扣费**。若该应用只做买断式，配到这步即可结束。

---

### 案例 5：配计费规则（实现“使用扣费”）

**作用**：定义“使用时按什么扣”。规则写入 `product_billing_rules`，应用上报用量后由 `finance_consumer` 据此扣费——这就是**使用扣费**。本例按存储量后付（postpaid，扣钱包）。

```bash
curl -X POST https://api.example.com/api/admin/product-billing-rules \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{
    "product_id": 100,
    "product_plan_id": null,
    "usage_type": "storage_used",
    "usage_unit": "GB",
    "price_amount": "0.010000",
    "currency": "CNY",
    "billing_mode": "postpaid",
    "free_quota": "10",
    "status": "active"
  }'
```

**要点**：
- `product_plan_id: null` = 商品级通用规则，对所有套餐生效。
- `usage_type=storage_used` 必须与应用适配器声明的 `usage_event_types_json` 一致，否则上报的事件匹配不到规则。
- `free_quota: 10` = 前 10GB 免费，超出部分每 GB 0.01 元。
- `billing_mode: postpaid` = 后付扣钱包。

---

### 案例 6：用户购买（触发购买扣费 + 开通）

**作用**：用户下单，触发**链路 A**：扣钱包、订单 paid、开通生成资产。

```bash
curl -X POST https://api.example.com/api/products/100/purchase \
  -H "Authorization: Bearer {{USER_TOKEN}}" -H "Content-Type: application/json" \
  -H "Idempotency-Key: 7c2a...-uuid" \
  -d '{ "plan_id": 50, "quantity": 1 }'
```

**响应**：

```json
{ "code": 0, "message": "ok", "data": {
  "order_id": 9001, "order_no": "ORD2026...", "status": "paid",
  "amount": "19.900000", "asset_id": null, "idempotent": false
}}
```

**要点**：
- 扣 19.9 元（或会员 14.9），订单 paid，开通生成网盘资产（30 天有效）。
- `asset_id: null` 正常，前端通过「我的资产」查到账。
- 失败码：未实名 70001 / 无权限 40003 / 余额不足 60001。

---

### 案例 7：使用时上报用量（触发使用扣费）

**作用**：用户用了 25GB 存储，应用把用量事件上报，触发**链路 B（postpaid 扣钱包）**：匹配规则、扣钱包、写消费记录。此接口为**内部接口**，由应用后端/门面调用，不对终端用户暴露；**fail-closed 鉴权**：必须带共享密钥头 `X-Internal-Token`（值为环境变量 `INTERNAL_API_TOKEN`），并受 IP 白名单限制，缺失或不符直接拒绝。

```bash
curl -X POST https://api.example.com/api/internal/product-usage-events \
  -H "Content-Type: application/json" \
  -H "X-Internal-Token: {{INTERNAL_API_TOKEN}}" \
  -d '{
    "event_id": "evt-uuid-1",
    "user_id": 1001,
    "product_id": 100,
    "product_plan_id": 50,
    "usage_type": "storage_used",
    "usage_amount": "25",
    "usage_unit": "GB",
    "occurred_at": "2026-06-27T12:00:00Z",
    "idempotency_key": "evt-uuid-1:storage_used"
  }'
```

**响应**：

```json
{ "code": 0, "message": "ok", "data": {
  "consumption_record_id": 5001, "amount": "0.150000",
  "idempotency_key": "evt-uuid-1:storage_used", "wallet_transaction_id": 88001
}}
```

**扣费计算**：用量 25GB − 免费 10GB = 15GB × 0.01 元 = **0.15 元**，从用户钱包扣除。

**要点**：
- `idempotency_key` 全局唯一，重复上报直接返回原结果，不二次扣费。
- 命中不到规则（如 usage_type 拼错）返回“未找到匹配的计费规则”，门面侧应静默跳过、不报错。
- 用户可在 `GET /api/product-consumption-records` 查自己的消费明细（凭 event_id 对账）。

---

### 案例 8（进阶）：预付额度模式（prepaid，不扣钱包）

**作用**：换一种组合——用户**先买额度包**（购买扣费一次性付钱），**用时扣额度**（使用扣费扣 entitlement，不再扣钱包）。适合“充值额度、用完为止”的应用。

**配置差异**：
1. 套餐 `quota_json` 声明额度（`quota_total/quota_unit/valid_days`），见会员/Token 套餐写法。
2. 购买时（链路 A）钱包付套餐价，开通生成 `user_entitlements`（`quota_total` 额度）。
3. 计费规则 `billing_mode: prepaid`；使用时门面调内部额度接口扣减，**不走钱包**。

**关键边界**（见 `../backend-token-billing-contract.md`）：
- 额度单位与计量同维度，额度耗尽即拒（错误码 `60005 权益额度不足`，区别于钱包 `60001`）。
- 预占 + 结算（多退少补）+ FOR UPDATE 行锁，防并发透支。
- prepaid 与 postpaid **互斥**：同一次调用绝不既扣钱包又扣额度。

---

## 六、设计要点与红线

| 主题 | 设计 | 原因 |
|---|---|---|
| 应用↔商品解耦 | `business_ref_id` 关联，应用只存元数据 | 单一数据源，避免两处维护价格/权限 |
| 购买原子性 | 扣费 + 订单 paid 同事务 | 杜绝“扣了钱订单还 pending” |
| 购买幂等 | `Idempotency-Key` 唯一索引 | 防重复点击/重试多扣 |
| 开通失败处理 | 不回滚已 paid 订单，记 warn 补偿 | 钱已扣，资产可异步补发 |
| 用量幂等 | 事件 `idempotency_key` 全局唯一 | 防重复上报多扣 |
| 计费方式互斥 | 按量/按次二选一，管理端强校验 | 防同一商品重复收费 |
| 扣费路径互斥 | postpaid 扣钱包 / prepaid 扣额度 | 绝不一次调用双扣 |
| 并发防透支 | 钱包乐观锁、额度 FOR UPDATE 行锁 | 高并发不超扣/不负余额 |

**红线**：
- 计费事件必须带幂等键（`request_id:类型`），杜绝重复扣费。
- 应用上报的 `usage_type` 必须与 `product_billing_rules` 配置、与适配器 `usage_event_types_json` 三者一致，否则扣费链路断裂（事件匹配不到规则）。
- 应用下架时，要同步下架其关联商品（`products.status`），否则用户端可见性与可购买性会不一致。
- prepaid 与 postpaid 严禁同一次调用既扣钱包又扣额度。

---

## 七、给前端 / 测试的对接提醒

- **购买扣费**走 `POST /api/products/{id}/purchase`（带 `Idempotency-Key`）；**使用扣费**走内部上报接口，前端通常不直接调。
- 用户消费明细查 `GET /api/product-consumption-records`（本人），管理端 `GET /api/admin/product-consumption-records`。
- 一个应用要“能扣费”至少需：应用 active + 商品(application, business_ref_id) active + 套餐 + 价格 + can_buy；要“按用量扣”再加计费规则。
- 测试使用扣费时，注意 `usage_type` 三处一致（适配器声明 / 计费规则 / 上报事件）。
- 金额一律按字符串 decimal 解析；免费额度 `free_quota` 会先抵扣再计费。
</content>
