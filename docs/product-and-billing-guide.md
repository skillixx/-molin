# 商品与商品计费 功能说明（操作流程 + 操作教程案例）

> 📚 本文属于 [业务与计费总览](./business-billing-overview.md) 文档体系（商品·会员·应用·扣费），建议先读总览建立全局认知。
> 适用模块：`product` / `order` / `billing` / `finance_consumer`
> 读者：运营、产品、测试、前端对接
> 权威契约：`docs/full-api-design.md`、`docs/backend-token-billing-contract.md`、`server/internal/modules/product/CLAUDE.md`
> 所有接口统一响应信封：`{ "code": 0, "message": "ok", "data": <业务数据> }`；出错时 `code != 0` 且 `data = null`。
> 列表统一“扁平分页”：`data = { items, page, page_size, total }`。

---

## 一、整体概念

平台把“可售卖的东西”抽象成一棵四层结构，再叠加“怎么收钱”的计费层：

```
商品 Product            一个可售卖单元（如「Token API」「某 Agent 服务」）
  └─ 套餐 Plan          同一商品的不同购买规格（月付 / 年付 / 一次性 / 按量）
       └─ 价格 Price    同一套餐对不同人群的售价（默认价 / 角色价 / 会员价）
  └─ 访问规则 Access     哪些角色能 看见 / 购买 / 使用 这个商品
  └─ 计费规则 BillingRule 使用过程中“按用量”怎么扣费（按量 token / 按次 calls / 套餐额度）
```

两类“收钱时机”一定要分清，这是理解整篇文档的关键：

| 收钱时机 | 由谁决定 | 对应配置 | 典型场景 |
|---|---|---|---|
| **购买时一次性收费** | 套餐价格 `product_prices` | 价格三层（默认/角色/会员） | 买月卡、买一次性套餐、预付额度包 |
| **使用时按量收费** | 计费规则 `product_billing_rules` | 按量 / 按次 / 套餐额度 | 调用模型按 token 扣钱、按次扣钱、扣套餐额度 |

> 一句话：**价格 = 买的时候付多少；计费规则 = 用的时候怎么扣。**

---

## 二、核心数据模型

| 表 | 作用 | 关键字段 |
|---|---|---|
| `products` | 商品主表 | `product_code`(唯一)、`product_type`、`status`(draft/active/inactive) |
| `product_plans` | 套餐 | `plan_code`、`billing_type`(one_time/monthly/yearly/usage)、`duration_days`、`quota_json` |
| `product_prices` | 套餐价格 | `role_id`、`membership_level_id`、`price_amount`、`currency` |
| `product_role_access` | 角色访问规则 | `role_id`、`can_view`、`can_buy`、`can_use` |
| `product_billing_rules` | 计费规则 | `usage_type`、`usage_unit`、`price_amount`、`billing_mode`(postpaid/prepaid)、`free_quota` |

### 价格优先级（核心规则）

同一套餐可以给不同人群配不同价。系统取价严格按以下优先级（命中即停）：

```
会员专属价（membership_level_id 命中用户当前会员等级）
  ▼ 没命中
角色价（role_id 命中用户任一角色；多角色取最低价）
  ▼ 没命中
默认价（role_id 与 membership_level_id 均为 NULL）
  ▼ 没命中
未配置价格 → 拒绝购买（ErrNoPriceConfigured）
```

> 用户端套餐列表里 `user_price = -1` 表示“该套餐尚未给你配置任何价格”，前端应隐藏购买按钮或提示“暂未开放”。

### 三种使用计费方式（计费规则）

| 方式 | usage_type / unit | billing_mode | 结算路径 |
|---|---|---|---|
| 按量（token） | `input_tokens` / `output_tokens`，单位 `tokens` | `postpaid` | 用多少扣多少，扣钱包 |
| 按次（调用） | `calls`，单位 `count` | `postpaid` | 每次提问扣 1 次，扣钱包 |
| 套餐预付额度 | token 额度 | `prepaid` | 扣 entitlement 额度，不走钱包 |

**铁律**：同一商品的「按量」与「按次」**二选一**，不能并存（避免重复收费）。管理端保存时强校验：已有按量规则再加按次（或反之）会被拦截。

---

## 三、接口清单

### 管理端（需登录 + 权限码）

| 方法 | 路径 | 权限码 | 作用 |
|---|---|---|---|
| GET | `/api/admin/products` | product:view | 商品列表（分页） |
| POST | `/api/admin/products` | product:create | 创建商品 |
| GET | `/api/admin/products/{id}` | product:view | 商品详情 |
| PATCH | `/api/admin/products/{id}` | product:edit | 改商品基本信息 |
| PATCH | `/api/admin/products/{id}/status` | product:edit | 上架 / 下架 |
| GET | `/api/admin/products/{id}/plans` | product:view | 套餐列表 |
| POST | `/api/admin/products/{id}/plans` | product:create | 新增套餐 |
| PATCH | `/api/admin/products/{id}/plans/{plan_id}` | product:edit | 改套餐 |
| GET | `/api/admin/products/{id}/access` | product:view | 回显访问规则 |
| PATCH | `/api/admin/products/{id}/access` | product:edit | 覆盖写访问规则 |
| GET | `/api/admin/products/{id}/prices` | product:view | 回显价格配置 |
| PATCH | `/api/admin/products/{id}/prices` | product:edit | 覆盖写价格 |
| GET | `/api/admin/product-billing-rules` | product:view | 计费规则列表 |
| POST | `/api/admin/product-billing-rules` | product:create | 新增计费规则 |
| PATCH | `/api/admin/product-billing-rules/{id}` | product:edit | 改计费规则 |

### 用户端（需登录）

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/api/products` | 商品市场（按角色 can_view 过滤，仅 active） |
| GET | `/api/products/{id}` | 商品详情 + 套餐 + 本人实际价格 |
| GET | `/api/products/{id}/plans` | 套餐列表（含 user_price） |
| POST | `/api/products/{id}/purchase` | 购买（必须带 `Idempotency-Key` 请求头） |

---

## 四、操作流程总览

### 流程 A：运营上架一个商品（管理端）

```
① 创建商品（draft 草稿态）
② 配置套餐（至少 1 个 plan）
③ 配置访问规则（哪些角色 can_view / can_buy / can_use）
④ 配置价格（默认价必配；可选叠加角色价 / 会员价）
⑤ 【可选】配置使用计费规则（按量 / 按次 / 套餐额度）
⑥ 上架（status: draft → active）
```

> 顺序建议先配齐 ②③④ 再 ⑥ 上架，避免用户看到“能买但没价格”的半成品商品。

### 流程 B：用户购买并使用（用户端）

```
① 浏览市场 GET /api/products（只看到 can_view 的 active 商品）
② 看详情/套餐 → 看到本人实际价格 user_price
③ 购买 POST /api/products/{id}/purchase（带 Idempotency-Key）
   后端：实名校验 → 购买权限校验 → 会员门槛校验 → 取价 → 算总价
        → 幂等检查 → 建订单(pending) → 钱包扣费 → 订单 paid → 触发开通
④ 开通完成后在「我的资产 / 我的权益」查看
⑤ 使用商品时，按计费规则在线扣费（按量/按次扣钱包，套餐扣额度）
```

---

## 五、操作教程案例

> 以下案例用 `curl` 演示，`{{TOKEN}}` 为登录后拿到的 JWT；管理端账号需具备对应权限码。
> 金额字段为字符串型 decimal（如 `"9.900000"`），避免浮点误差。

---

### 案例 1：创建一个商品

**作用**：先建出商品壳子（草稿态），后续才能往里挂套餐、价格、规则。商品默认 `draft`，不会被用户看到，方便慢慢配。

**操作**：

```bash
curl -X POST https://api.example.com/api/admin/products \
  -H "Authorization: Bearer {{TOKEN}}" \
  -H "Content-Type: application/json" \
  -d '{
    "product_type": "token_api",
    "product_code": "token-api",
    "name": "Token API 调用服务",
    "description": "多模型统一接口，按 token 计费",
    "status": "draft"
  }'
```

**响应**（`data` 即新建商品）：

```json
{ "code": 0, "message": "ok",
  "data": { "id": 1, "product_code": "token-api", "name": "Token API 调用服务", "status": "draft", "created_at": "2026-06-27T10:00:00Z" } }
```

**要点**：
- `product_code` 全局唯一，重复会返回 `40000「商品编码已存在」`。
- `product_type / product_code / name` 必填，缺一返回 `40000`。
- 即便传 `status: active`，也建议先 draft，配齐再上架。

---

### 案例 2：给商品加套餐

**作用**：套餐是“购买规格”。同一商品可以有「月付」「年付」「一次性」「按量」等多种规格，用户购买时选其一。`billing_type` 决定计费形态，`quota_json` 可声明套餐含的额度。

**操作**（给商品 1 加一个一次性的“100 万 Token 套餐”）：

```bash
curl -X POST https://api.example.com/api/admin/products/1/plans \
  -H "Authorization: Bearer {{TOKEN}}" \
  -H "Content-Type: application/json" \
  -d '{
    "plan_code": "token-pkg-1m",
    "name": "100万 Token 套餐",
    "billing_type": "usage",
    "quota_json": "{\"entitlement_type\":\"token_quota\",\"quota_total\":1000000,\"quota_unit\":\"tokens\",\"valid_days\":365}",
    "status": "active"
  }'
```

**响应**：返回新建 plan，含 `id`（后续配价格要用）。

**要点**：
- `billing_type` 取值：`one_time` 一次性 / `monthly` 月付 / `yearly` 年付 / `usage` 用量套餐。
- `plan_code` 在同一商品内唯一，重复返回 `40000「套餐编码已存在」`。
- `quota_json` 是给开通环节（provision）读的，声明买完后发多少额度、有效期多久。到期未用完额度清零。

---

### 案例 3：配置访问规则（谁能看 / 能买 / 能用）

**作用**：控制商品对不同角色的可见性与购买权。`can_view` 决定是否出现在市场，`can_buy` 决定能否下单，`can_use` 决定开通后能否使用。**覆盖写**：本次提交的 `items` 会整体替换该商品已有的访问规则。

**操作**（角色 10=普通用户 可看可买可用；角色 11=游客 仅可看）：

```bash
curl -X PATCH https://api.example.com/api/admin/products/1/access \
  -H "Authorization: Bearer {{TOKEN}}" \
  -H "Content-Type: application/json" \
  -d '{
    "items": [
      { "role_id": 10, "can_view": true,  "can_buy": true,  "can_use": true  },
      { "role_id": 11, "can_view": true,  "can_buy": false, "can_use": false }
    ]
  }'
```

**响应**：`{ "code": 0, "message": "ok", "data": { "message": "访问权限配置成功" } }`

**要点**：
- 这是**全量覆盖**，不是增量。要保留旧规则必须连旧带新一起提交。
- 缺少 `items` 字段（如误用旧的 `accesses` 键名）会被拒绝 `40000`，防止“静默清空所有访问规则”。
- 回显用 `GET /api/admin/products/1/access`，返回 `{ "items": [...] }`。

---

### 案例 4：配置价格（默认价 / 角色价 / 会员价 三层）

**作用**：决定“购买时一次性付多少”。一次请求可同时给多个套餐、多种人群配价，**按 `product_plan_id` 分组覆盖写**。三层价格命中规则见前文“价格优先级”。

**操作**（给套餐 5：默认价 99 元；角色 20=VIP 角色价 79 元；会员等级 2 会员价 59 元）：

```bash
curl -X PATCH https://api.example.com/api/admin/products/1/prices \
  -H "Authorization: Bearer {{TOKEN}}" \
  -H "Content-Type: application/json" \
  -d '{
    "items": [
      { "product_plan_id": 5, "role_id": null, "membership_level_id": null, "price_amount": "99.00", "currency": "CNY" },
      { "product_plan_id": 5, "role_id": 20,   "membership_level_id": null, "price_amount": "79.00", "currency": "CNY" },
      { "product_plan_id": 5, "role_id": null, "membership_level_id": 2,    "price_amount": "59.00", "currency": "CNY" }
    ]
  }'
```

**响应**：`{ ... "data": { "message": "价格配置成功" } }`

**作用演示（取价结果）**：
- 普通用户（无 VIP 角色、非会员）→ 付 **99**（默认价）。
- VIP 角色用户（非会员）→ 付 **79**（角色价）。
- 会员等级 2 的用户 → 付 **59**（会员价优先级最高）。

**要点**：
- **默认价务必配**：用户没命中角色/会员价时全靠默认价兜底，否则取价失败、无法购买。
- 每个 `item` 必须带 `product_plan_id`，否则 `40000`。
- 覆盖写以 `product_plan_id` 为单位，提交某套餐的价格会替换该套餐的全部已配价格。
- `price_amount: "0"` 是合法的“免费”，与“未配置”（`user_price = -1`）语义不同。

---

### 案例 5：上架 / 下架商品

**作用**：控制商品是否对用户可售。只有 `active` 的商品才会出现在用户市场。配置期保持 `draft`，配齐后上架 `active`；需要临时停售时改 `inactive`。

**操作**：

```bash
# 上架
curl -X PATCH https://api.example.com/api/admin/products/1/status \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "status": "active" }'

# 下架
curl -X PATCH https://api.example.com/api/admin/products/1/status \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "status": "inactive" }'
```

**要点**：商品不存在返回 `40400`。下架不影响已购用户的既有资产，只是阻止新购买。

---

### 案例 6：用户浏览市场并查看本人价格

**作用**：用户视角的商品市场。只返回当前用户**有权查看（can_view）且 active** 的商品；详情里直接给出“本人实际价格”，前端无需自己算优先级。

**操作**：

```bash
# 市场列表（支持 keyword / product_type / 分页）
curl "https://api.example.com/api/products?page=1&page_size=20&keyword=token" \
  -H "Authorization: Bearer {{USER_TOKEN}}"

# 商品详情 + 套餐 + 本人价格
curl "https://api.example.com/api/products/1" \
  -H "Authorization: Bearer {{USER_TOKEN}}"
```

**详情响应**（节选）：

```json
{ "code": 0, "message": "ok", "data": {
  "product": { "id": 1, "name": "Token API 调用服务", "status": "active" },
  "plans": [
    { "id": 5, "name": "100万 Token 套餐", "billing_type": "usage", "user_price": "59.000000", "currency": "CNY" }
  ]
}}
```

**要点**：
- `user_price` 已是按优先级算好的本人价；`-1` 表示该套餐对本人未配价，应禁用购买。
- 列表为扁平分页 `{ items, page, page_size, total }`。

---

### 案例 7：用户购买商品（幂等下单）

**作用**：核心交易入口。后端一条龙完成：实名校验 → 购买权限校验 → 会员门槛校验 → 取价 → 按数量算总价 → 幂等检查 → 建订单 → 钱包扣费 → 订单置 paid → 触发开通。

**操作**（购买套餐 5，数量 2）：

```bash
curl -X POST https://api.example.com/api/products/1/purchase \
  -H "Authorization: Bearer {{USER_TOKEN}}" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: 1f8c9e7a-0b2d-4c11-9a3e-7d6f5b4a2c10" \
  -d '{ "plan_id": 5, "quantity": 2, "remark": "618 活动购买" }'
```

**成功响应**：

```json
{ "code": 0, "message": "ok", "data": {
  "order_id": 10086, "order_no": "PO20260627...", "status": "paid",
  "amount": "118.000000", "asset_id": null, "idempotent": false
}}
```

**要点 / 作用说明**：
- **`Idempotency-Key` 请求头必填**：缺失直接 `40000`。同一个 key 重复请求只会下一单，重复返回时 `idempotent: true`——用于防止用户重复点击 / 网络重试导致多扣钱。
- `quantity` 缺省或 ≤0 视为 1；总价 = 单价 × 数量（示例 59 × 2 = 118）。
- `amount` 返回的是**总价**。
- `asset_id` 为 `null` 属正常：开通是后续动作，前端通过「我的资产 / 我的权益」轮询到账状态。
- 扣费与订单状态在**同一事务**内完成，杜绝“钱扣了订单还 pending”。

**常见失败（作用：前端据此提示用户）**：

| 场景 | HTTP | code | 提示 |
|---|---|---|---|
| 未实名 | 400 | 70001 | 需要先完成实名认证 |
| 无购买权限 / 非会员买会员专属 | 403 | 40003 | 无购买权限 |
| 钱包余额不足 | 400 | 60001 | 余额不足 |
| 套餐未配价格 | 400 | 40000 | 该套餐未配置价格 |
| 高并发瞬时锁冲突 | 409 | 50000 | 系统繁忙，请稍后重试 |

---

### 案例 8：配置“按量计费”规则（按 token 扣费）

**作用**：决定“使用时按用量怎么扣”。按量规则把每次模型调用的 input/output token 数 × 单价，后付扣钱包。这是 Token 服务的基线计费方式。

**操作**（给商品 1 配输入、输出 token 各一条规则）：

```bash
# 输入 token：每 token 0.000002 元
curl -X POST https://api.example.com/api/admin/product-billing-rules \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{
    "product_id": 1, "product_plan_id": null,
    "usage_type": "input_tokens", "usage_unit": "tokens",
    "price_amount": "0.000002", "currency": "CNY",
    "billing_mode": "postpaid", "free_quota": "0", "status": "active"
  }'

# 输出 token：每 token 0.000006 元
curl -X POST https://api.example.com/api/admin/product-billing-rules \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{
    "product_id": 1, "product_plan_id": null,
    "usage_type": "output_tokens", "usage_unit": "tokens",
    "price_amount": "0.000006", "billing_mode": "postpaid", "status": "active"
  }'
```

**要点**：
- `product_plan_id: null` = 商品级通用规则，对该商品所有套餐生效。
- `billing_mode: postpaid` = 后付，走钱包。
- `free_quota` 可选，声明免费额度（超出部分才计费）。
- `price_amount` 必须 > 0，否则 `price_amount 必须大于 0`。
- 计费口径：一次提问触发多轮上游调用时，**按量累加所有轮的 token**。

---

### 案例 9：配置“按次计费”规则（与按量互斥）

**作用**：换一种计费口径——不按 token，而是**每次提问扣固定金额**。适合想给用户简单透明定价的场景。关键演示：**按量与按次二选一**，系统强校验。

**操作**：

```bash
curl -X POST https://api.example.com/api/admin/product-billing-rules \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{
    "product_id": 1, "product_plan_id": null,
    "usage_type": "calls", "usage_unit": "count",
    "price_amount": "0.010000", "billing_mode": "postpaid", "status": "active"
  }'
```

**互斥校验演示（作用：防止重复收费）**：
- 若商品 1 已有 active 的 `input_tokens`/`output_tokens` 规则，再提交上面的 `calls` 规则 →
  被拦截：`该商品已配置按量计费规则，不能再加按次规则（二选一）`。
- 反之，已有 `calls` 再加按量 → `该商品已配置按次计费规则，不能再加按量规则（二选一）`。
- 想从按量切到按次：先把按量规则 `PATCH` 改为 `status: inactive`，再加按次规则。

**计费口径**：一次用户提问只计 **1 次**（即便 tool-use 触发多轮上游调用也只算 1 次）；纯前置失败（鉴权失败、未开通、余额闸拒绝等，尚未发起上游调用）**不计次**。

---

### 案例 10：套餐预付额度（prepaid，不走钱包）

**作用**：让用户**先买额度包、用时扣额度**，与按量后付互补。购买走案例 7 的下单流程（一次性付套餐价），开通时按 `quota_json` 生成权益额度（entitlement）；使用时扣额度而非钱包。

**配置链路**：
1. 套餐声明额度——见案例 2，`billing_type: usage` + `quota_json` 含 `quota_total/quota_unit/valid_days`。
2. 配套餐售价——见案例 4，给该套餐配一次性预付价。
3. 用户购买——见案例 7，钱包付套餐价、开通生成 `token_quota` entitlement。
4. 使用扣额度——门面按 `billing_mode: prepaid` 调用内部接口扣额度（预占 → 结算多退少补）。

**作用与边界**：
- 额度单位 = **token 数**（与计费同维度，额度耗尽即拒绝，不折算金额）。
- 有效期：`expires_at = 开通时间 + valid_days`；**到期未用完额度清零**（entitlement 置 expired，前置闸拒绝）。
- 额度不足错误码 `60005「权益额度不足」`（区别于钱包 `60001`）。
- prepaid 一次调用**只扣额度、不扣钱包**；postpaid 只扣钱包、不扣额度，二者互斥。

---

## 六、错误码速查（商品 & 计费相关）

| code | HTTP | 含义 | 出现场景 |
|---|---|---|---|
| 40000 | 400 | 参数错误 | 必填缺失、编码重复、缺幂等键、未配价格 |
| 40003 | 403 | 无购买权限 | `can_buy=false` 或非会员买会员专属 |
| 40400 | 404 | 商品/套餐不存在 | 改商品、改套餐、上下架时目标不存在 |
| 70001 | 400 | 需要先完成实名认证 | 购买前未实名 |
| 60001 | 400 | 钱包余额不足 | 购买扣费、按量/按次后付扣费 |
| 60005 | 400 | 权益额度不足 | 套餐 prepaid 额度耗尽 |
| 50000 | 409 | 系统繁忙请重试 | 高并发乐观锁瞬时冲突 |

---

## 七、给前端 / 测试的对接提醒

- **下单必带 `Idempotency-Key`**（建议 UUID），同 key 重试返回 `idempotent: true`，不会重复扣钱。
- **金额一律按字符串解析**（decimal），不要转 float。
- **列表一律扁平分页** `{ items, page, page_size, total }`，不要按 `{ list, pagination }` 解析。
- **`user_price = -1` = 未配价**，禁用购买入口。
- **访问规则 / 价格是覆盖写**：编辑页要先 `GET` 回显，连旧带新整体提交，避免误删。
- **计费方式按量/按次二选一**：管理端切换前先停用对立规则。
</content>
</invoke>
