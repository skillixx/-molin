# 后端乙接口 — APIPost 人工测试手册

> **覆盖模块**：product / order / billing / finance_consumer（共 27 个接口 P1-P17 / O1-O6 / B1-B8 / F1-F3）
> **服务地址**：`http://8.130.9.163:8080`
> **更新日期**：2026-06-16

---

## 准备工作：APIPost 环境变量配置

在 APIPost → 「环境管理」中新建 `molin-test` 环境，添加：

| 变量名 | 初始值 | 说明 |
|---|---|---|
| `base_url` | `http://8.130.9.163:8080` | 服务基地址 |
| `token` | （空，登录后填入） | 用户 access_token |
| `admin_token` | （空，管理员登录后填入） | 管理员 access_token |
| `product_id` | （空，创建后填入） | 测试商品 ID |
| `plan_id` | （空，创建后填入） | 测试套餐 ID |
| `order_id` | （空，下单后填入） | 测试订单 ID |

所有请求 URL 写 `{{base_url}}/api/...`，Header 写 `{{token}}` / `{{admin_token}}`。

**APIPost 技巧**：在「环境变量」中设置 `token` 变量，请求 Header 写 `{{token}}`，登录后手动更新变量值，后续所有请求自动带上。

---

## 第一步：登录获取 Token

### 1-A. 用户账号登录

```
POST {{base_url}}/api/auth/login/email
Content-Type: application/json
```

Body（raw JSON）：
```json
{
  "email": "testuser@example.com",
  "password": "Test123456!"
}
```

> **操作**：将返回值 `data.access_token` 复制，粘贴到环境变量 `token`。

预期返回：
```json
{
  "code": 0,
  "data": {
    "access_token": "eyJhbGci...",
    "refresh_token": "...",
    "expires_in": 7200
  }
}
```

### 1-B. 管理员账号登录

```
POST {{base_url}}/api/auth/login/email
Content-Type: application/json
```

Body（raw JSON）：
```json
{
  "email": "admin@molin.com",
  "password": "Admin123456!"
}
```

> **操作**：将返回值 `data.access_token` 粘贴到环境变量 `admin_token`。

---

## 第二步：钱包接口（B1-B3）

### B1. 查询钱包余额

```
GET {{base_url}}/api/wallet
Authorization: Bearer {{token}}
```

预期返回：
```json
{
  "code": 0,
  "data": {
    "id": 1,
    "user_id": 1,
    "balance_amount": "0.000000",
    "frozen_amount": "0.000000",
    "currency": "CNY"
  }
}
```

---

### B2. 查询钱包流水（支持过滤）

```
GET {{base_url}}/api/wallet/transactions
Authorization: Bearer {{token}}
```

**可选过滤参数（Query String）：**

| 参数 | 可选值 | 示例 |
|---|---|---|
| `type` | recharge / consume / refund / freeze / unfreeze | `?type=recharge` |
| `direction` | in / out | `?direction=in` |
| `created_from` | RFC3339 或 日期（2026-01-01） | `?created_from=2026-01-01` |
| `created_to` | RFC3339 或 日期 | `?created_to=2026-12-31` |
| `page` | 页码（默认 1） | `?page=1` |
| `page_size` | 每页条数（默认 20） | `?page_size=10` |

测试请求示例：
```
GET {{base_url}}/api/wallet/transactions?type=recharge&direction=in&page=1&page_size=10
```

预期返回：
```json
{
  "code": 0,
  "data": {
    "items": [],
    "page": 1,
    "page_size": 10,
    "total": 0
  }
}
```

---

### B3. 创建充值订单

```
POST {{base_url}}/api/recharge/orders
Authorization: Bearer {{token}}
Content-Type: application/json
Idempotency-Key: test-recharge-001
```

Body（raw JSON）：
```json
{
  "amount": "100.00",
  "payment_method": "wechat"
}
```

> `payment_method` 枚举：`wechat` / `alipay`，其他值返回 400

预期返回 HTTP 201：
```json
{
  "code": 0,
  "data": {
    "order_id": 1,
    "order_no": "ORD202606161A3B9C2F",
    "amount": "100.000000",
    "status": "pending",
    "pay_url": "https://pay.example.com/..."
  }
}
```

> **注意**：真实支付验签（PR#123）已启用，实际充值需由支付平台真实回调才能到账。

---

## 第三步：商品模块 — 用户端（P1-P4）

### P1. 商品市场列表

```
GET {{base_url}}/api/products
Authorization: Bearer {{token}}
```

可选参数：`?page=1&page_size=10`

预期返回：
```json
{
  "code": 0,
  "data": {
    "items": [
      {
        "id": 1,
        "product_type": "app",
        "product_code": "gpt-api-v1",
        "name": "GPT API 服务",
        "description": "...",
        "status": "active"
      }
    ],
    "page": 1,
    "page_size": 10,
    "total": 1
  }
}
```

> 此接口按角色 `can_view` 过滤，仅返回用户有权查看的 active 商品。

---

### P2. 商品详情（含套餐 + 用户实际价格）

```
GET {{base_url}}/api/products/{{product_id}}
Authorization: Bearer {{token}}
```

预期返回：
```json
{
  "code": 0,
  "data": {
    "product": { "id": 1, "name": "GPT API 服务" },
    "plans": [
      {
        "id": 1,
        "plan_code": "basic",
        "name": "基础版",
        "billing_type": "prepaid",
        "duration_days": 30,
        "user_price": "9.990000",
        "currency": "CNY"
      }
    ]
  }
}
```

---

### P3. 商品套餐列表（仅套餐，含用户价格）

```
GET {{base_url}}/api/products/{{product_id}}/plans
Authorization: Bearer {{token}}
```

预期返回：
```json
{
  "code": 0,
  "data": {
    "plans": [
      { "id": 1, "plan_code": "basic", "user_price": "9.990000", "currency": "CNY" }
    ]
  }
}
```

---

### P4. 购买商品（钱包扣费）

```
POST {{base_url}}/api/products/{{product_id}}/purchase
Authorization: Bearer {{token}}
Content-Type: application/json
Idempotency-Key: buy-{{product_id}}-001
```

Body（raw JSON）：
```json
{
  "plan_id": 1,
  "quantity": 1,
  "remark": "测试购买"
}
```

**常见错误响应：**

| 场景 | HTTP | code | 说明 |
|---|---|---|---|
| 未实名 | 400 | 70001 | 需先完成实名认证 |
| 无购买权限 | 403 | 40003 | 角色不在 can_buy 范围 |
| 余额不足 | 400 | 60001 | 钱包余额不够 |
| 套餐未配价格 | 400 | 40000 | 管理员未配置该套餐价格 |
| 缺少幂等键 | 400 | 40000 | 请求头 Idempotency-Key 必填 |
| 并发冲突 | 409 | 50000 | 系统繁忙，稍后重试 |

预期成功返回：
```json
{
  "code": 0,
  "data": {
    "order_id": 5,
    "order_no": "ORD20260616XXXXXXXX",
    "status": "paid"
  }
}
```

---

## 第四步：订单模块 — 用户端（O1-O4）

### O1. 查询我的订单列表（支持过滤）

```
GET {{base_url}}/api/orders
Authorization: Bearer {{token}}
```

**可选过滤参数：**

| 参数 | 可选值 | 示例 |
|---|---|---|
| `order_type` | product / recharge | `?order_type=product` |
| `status` | pending / paid / cancelled / failed | `?status=paid` |
| `created_from` | RFC3339 或 日期 | `?created_from=2026-01-01` |
| `created_to` | RFC3339 或 日期 | `?created_to=2026-12-31` |
| `page` / `page_size` | 分页 | `?page=1&page_size=10` |

```
GET {{base_url}}/api/orders?order_type=product&status=paid&page=1&page_size=10
```

预期返回：
```json
{
  "code": 0,
  "data": {
    "items": [
      {
        "id": 5,
        "order_no": "ORD20260616XXXXXXXX",
        "order_type": "product",
        "status": "paid",
        "amount": "9.990000",
        "currency": "CNY",
        "created_at": "2026-06-16T10:00:00Z"
      }
    ],
    "page": 1,
    "page_size": 10,
    "total": 1
  }
}
```

---

### O2. 查询单个订单详情

```
GET {{base_url}}/api/orders/{{order_id}}
Authorization: Bearer {{token}}
```

> 只能查询本人订单，查他人订单返回 404。

---

### O3. 钱包支付待付款订单

```
POST {{base_url}}/api/orders/{{order_id}}/pay
Authorization: Bearer {{token}}
Content-Type: application/json
Idempotency-Key: pay-order-{{order_id}}-001
```

Body（raw JSON）：
```json
{
  "pay_method": "wallet"
}
```

> `pay_method` 目前只支持 `wallet`；充值订单（order_type=recharge）不支持此接口。

**错误场景：**

| 场景 | HTTP | code |
|---|---|---|
| 订单不是 pending 状态 | 400 | 40900 |
| 充值订单不支持 wallet 支付 | 400 | 40000 |
| 余额不足 | 400 | 60001 |
| 并发冲突 | 409 | 40900 |
| 缺少幂等键 | 400 | 40000 |

预期成功返回：
```json
{
  "code": 0,
  "data": {
    "order_id": 5,
    "status": "paid",
    "wallet_transaction_id": 12,
    "asset_id": 3
  }
}
```

---

### O4. 取消待付款订单

```
POST {{base_url}}/api/orders/{{order_id}}/cancel
Authorization: Bearer {{token}}
Content-Type: application/json
```

Body（raw JSON）：
```json
{
  "reason": "测试取消"
}
```

预期返回：
```json
{ "code": 0, "data": { "cancelled": true } }
```

---

## 第五步：消费记录（F2）

### F2. 查询本人消费记录

```
GET {{base_url}}/api/product-consumption-records
Authorization: Bearer {{token}}
```

**可选过滤参数：**

| 参数 | 说明 |
|---|---|
| `product_id` | 按商品过滤 |
| `usage_type` | 如 `input_tokens` / `output_tokens` |
| `created_from` | 开始时间（RFC3339 或 日期） |
| `created_to` | 截止时间 |
| `page` / `page_size` | 分页 |

预期返回：
```json
{
  "code": 0,
  "data": {
    "items": [],
    "page": 1,
    "page_size": 20,
    "total": 0
  }
}
```

---

## 第六步：管理端接口（需 admin_token）

> 以下所有请求 Header 使用 `Authorization: Bearer {{admin_token}}`

---

### P5. 管理员商品列表（支持过滤）

```
GET {{base_url}}/api/admin/products
Authorization: Bearer {{admin_token}}
```

可选参数：`?keyword=GPU&status=active&type=app&page=1&page_size=10`

预期返回（D-95 扁平分页）：
```json
{
  "code": 0,
  "data": {
    "items": [ { "id": 1, "name": "GPT API 服务", "status": "active" } ],
    "page": 1,
    "page_size": 10,
    "total": 1
  }
}
```

---

### P6. 创建商品

```
POST {{base_url}}/api/admin/products
Authorization: Bearer {{admin_token}}
Content-Type: application/json
```

Body（raw JSON）：
```json
{
  "product_type": "app",
  "product_code": "test-product-001",
  "name": "测试商品",
  "description": "这是一个测试商品",
  "status": "draft"
}
```

> 必填：`product_type` / `product_code` / `name`
> `status` 枚举：`draft`（默认） / `active` / `inactive`
> 返回 HTTP 201，将 `data.id` 保存到 `{{product_id}}`

---

### P7. 管理员商品详情

```
GET {{base_url}}/api/admin/products/{{product_id}}
Authorization: Bearer {{admin_token}}
```

---

### P8. 更新商品信息（部分更新）

```
PATCH {{base_url}}/api/admin/products/{{product_id}}
Authorization: Bearer {{admin_token}}
Content-Type: application/json
```

Body（raw JSON）：
```json
{
  "name": "测试商品（已更新）",
  "description": "更新后的描述"
}
```

---

### P9. 上架 / 下架商品

```
PATCH {{base_url}}/api/admin/products/{{product_id}}/status
Authorization: Bearer {{admin_token}}
Content-Type: application/json
```

Body（raw JSON）：
```json
{ "status": "active" }
```

> `status` 枚举：`active`（上架） / `inactive`（下架） / `draft`（草稿）

---

### P10. 管理员查套餐列表

```
GET {{base_url}}/api/admin/products/{{product_id}}/plans
Authorization: Bearer {{admin_token}}
```

预期返回：`{ "code": 0, "data": { "plans": [...] } }`

---

### P11. 创建套餐

```
POST {{base_url}}/api/admin/products/{{product_id}}/plans
Authorization: Bearer {{admin_token}}
Content-Type: application/json
```

Body（raw JSON）：
```json
{
  "plan_code": "basic",
  "name": "基础版",
  "billing_type": "prepaid",
  "duration_days": 30,
  "status": "active"
}
```

> 必填：`plan_code` / `name` / `billing_type`
> `billing_type`：`prepaid`（预付费） / `postpaid`（后付费） / `metered`（计量）
> 返回 HTTP 201，将 `data.id` 保存到 `{{plan_id}}`

---

### P12. 更新套餐（部分更新）

```
PATCH {{base_url}}/api/admin/products/{{product_id}}/plans/{{plan_id}}
Authorization: Bearer {{admin_token}}
Content-Type: application/json
```

Body（raw JSON）：
```json
{
  "name": "基础版（更新后）",
  "duration_days": 60
}
```

---

### P13. 配置商品角色访问权限（覆盖写）

```
PATCH {{base_url}}/api/admin/products/{{product_id}}/access
Authorization: Bearer {{admin_token}}
Content-Type: application/json
```

Body（raw JSON）：
```json
{
  "items": [
    { "role_id": 1, "can_view": true, "can_buy": true, "can_use": true },
    { "role_id": 2, "can_view": true, "can_buy": false, "can_use": false }
  ]
}
```

> **覆盖写**：每次请求替换该商品全部角色配置，未在 `items` 中的角色会被删除。

---

### P14. 配置套餐价格（覆盖写）

```
PATCH {{base_url}}/api/admin/products/{{product_id}}/prices
Authorization: Bearer {{admin_token}}
Content-Type: application/json
```

Body（raw JSON）：
```json
{
  "plan_id": 1,
  "items": [
    { "price_amount": "9.99", "currency": "CNY" },
    { "role_id": 2, "price_amount": "7.99", "currency": "CNY" }
  ]
}
```

> 价格优先级：会员价 > 角色价 > 默认价
> `role_id` / `membership_level_id` 均为空 = 默认价；必填：`plan_id`

---

### P15. 计费规则列表

```
GET {{base_url}}/api/admin/product-billing-rules
Authorization: Bearer {{admin_token}}
```

可选参数：`?product_id=1&status=active&page=1&page_size=10`

---

### P16. 创建计费规则

```
POST {{base_url}}/api/admin/product-billing-rules
Authorization: Bearer {{admin_token}}
Content-Type: application/json
```

Body（raw JSON）：
```json
{
  "product_id": 1,
  "product_plan_id": 1,
  "usage_type": "input_tokens",
  "usage_unit": "1k_tokens",
  "price_amount": "0.002000",
  "currency": "CNY",
  "billing_mode": "metered",
  "free_quota": "10.000000",
  "status": "active"
}
```

> 必填：`product_id` / `usage_type` / `usage_unit` / `billing_mode`
> `free_quota`：免费额度，用量在此范围内不扣费

---

### P17. 更新计费规则（部分更新）

```
PATCH {{base_url}}/api/admin/product-billing-rules/1
Authorization: Bearer {{admin_token}}
Content-Type: application/json
```

Body（raw JSON）：
```json
{
  "price_amount": "0.001500",
  "free_quota": "20.000000",
  "status": "inactive"
}
```

---

### O5. 管理员全量订单列表（支持过滤）

```
GET {{base_url}}/api/admin/orders
Authorization: Bearer {{admin_token}}
```

**可选过滤参数：**

| 参数 | 说明 |
|---|---|
| `user_id` | 按用户 ID 过滤 |
| `order_type` | product / recharge |
| `status` | pending / paid / cancelled / failed |
| `created_from` | 开始时间（RFC3339 或 日期） |
| `created_to` | 截止时间 |
| `page` / `page_size` | 分页 |

```
GET {{base_url}}/api/admin/orders?status=paid&order_type=product&created_from=2026-01-01
```

---

### O6. 管理员查订单详情

```
GET {{base_url}}/api/admin/orders/{{order_id}}
Authorization: Bearer {{admin_token}}
```

---

### B5. 查询指定用户钱包

```
GET {{base_url}}/api/admin/users/1/wallet
Authorization: Bearer {{admin_token}}
```

> 路径中 `1` 替换为目标用户 ID

---

### B6. 全量钱包流水（支持过滤）

```
GET {{base_url}}/api/admin/wallet-transactions
Authorization: Bearer {{admin_token}}
```

**可选过滤参数：**

| 参数 | 说明 |
|---|---|
| `user_id` | 按用户过滤 |
| `type` | recharge / consume / refund / freeze / unfreeze |
| `direction` | in / out |
| `created_from` | 开始时间 |
| `created_to` | 截止时间 |
| `page` / `page_size` | 分页 |

```
GET {{base_url}}/api/admin/wallet-transactions?type=recharge&direction=in&user_id=1
```

---

### B7. 冻结 / 解冻用户余额

```
PATCH {{base_url}}/api/admin/users/1/wallet/freeze
Authorization: Bearer {{admin_token}}
Content-Type: application/json
```

Body（raw JSON）：
```json
{
  "action": "freeze",
  "amount": "50.00",
  "reason": "涉嫌异常充值，暂时冻结"
}
```

> `action` 枚举：`freeze`（冻结） / `unfreeze`（解冻）；`amount` 必须大于 0

---

### B8. 查询支付回调记录

```
GET {{base_url}}/api/admin/payment-callbacks
Authorization: Bearer {{admin_token}}
```

可选参数：`?provider=wechat&status=processed&page=1&page_size=10`

> `provider`：wechat / alipay
> `status`：received / processed / ignored
> **注意**：响应中不含 `notify_body` 字段（安全红线 B-04，禁止回传）

---

### F3. 管理员全量消费记录

```
GET {{base_url}}/api/admin/product-consumption-records
Authorization: Bearer {{admin_token}}
```

可选参数：`?user_id=1&product_id=1&usage_type=input_tokens&created_from=2026-01-01`

---

## 第七步：内部接口（F1，仅内部服务调用）

### F1. 上报消费事件（内部扣费接口）

```
POST {{base_url}}/api/internal/product-usage-events
X-Internal-Token: <INTERNAL_API_TOKEN 环境变量值>
Content-Type: application/json
```

Body（raw JSON）：
```json
{
  "event_id": "evt-20260616-001",
  "idempotency_key": "evt-20260616-001-input_tokens",
  "user_id": 1,
  "product_id": 1,
  "product_type": "app",
  "product_code": "gpt-api-v1",
  "product_plan_id": 1,
  "instance_id": 100,
  "usage_type": "input_tokens",
  "usage_amount": "5.000000",
  "usage_unit": "1k_tokens",
  "occurred_at": "2026-06-16T10:00:00Z"
}
```

> **鉴权方式**：`X-Internal-Token` 请求头（非 Bearer token），值为环境变量 `INTERNAL_API_TOKEN`
> 此接口还检查 IP 白名单，测试时需从 `127.0.0.1` 发起或联系运维配置白名单
> **未配置 `INTERNAL_API_TOKEN` 时一律返回 403（fail-closed）**

必填字段：`event_id` / `idempotency_key` / `user_id` / `product_id`

预期返回：
```json
{
  "code": 0,
  "data": {
    "consumption_record_id": 1,
    "wallet_transaction_id": 10,
    "amount": "0.010000",
    "idempotency_key": "evt-20260616-001-input_tokens"
  }
}
```

---

## 附录 A：通用错误码

| code | 含义 | 常见场景 |
|---|---|---|
| 40000 | 请求参数错误 | 缺必填字段、格式错误、不支持的枚举值 |
| 40003 | 无权限 / 鉴权失败 | 无对应权限码、IP 未授权、内部 Token 错误 |
| 40004 | 资源不存在 | 商品/订单/用户不存在 |
| 40100 | 未登录 | token 缺失或过期 |
| 40900 | 状态冲突 | 订单已付款/已取消，不可再操作 |
| 50000 | 服务内部错误 | DB 异常、并发冲突重试耗尽 |
| 60001 | 余额不足 | 钱包余额 < 扣费金额，或冻结金额超余额 |
| 70001 | 未实名 | 购买商品需先完成实名认证 |

---

## 附录 B：接口索引

| 编号 | 方法 | 路径 | 说明 | 鉴权 |
|---|---|---|---|---|
| P1 | GET | /api/products | 商品市场列表 | Bearer token |
| P2 | GET | /api/products/:id | 商品详情+套餐 | Bearer token |
| P3 | GET | /api/products/:id/plans | 套餐列表+用户价格 | Bearer token |
| P4 | POST | /api/products/:id/purchase | 购买商品 | Bearer token + Idempotency-Key |
| P5 | GET | /api/admin/products | 管理员商品列表 | Bearer admin_token |
| P6 | POST | /api/admin/products | 创建商品 | Bearer admin_token |
| P7 | GET | /api/admin/products/:id | 管理员商品详情 | Bearer admin_token |
| P8 | PATCH | /api/admin/products/:id | 更新商品 | Bearer admin_token |
| P9 | PATCH | /api/admin/products/:id/status | 上架/下架 | Bearer admin_token |
| P10 | GET | /api/admin/products/:id/plans | 套餐列表 | Bearer admin_token |
| P11 | POST | /api/admin/products/:id/plans | 创建套餐 | Bearer admin_token |
| P12 | PATCH | /api/admin/products/:id/plans/:plan_id | 更新套餐 | Bearer admin_token |
| P13 | PATCH | /api/admin/products/:id/access | 配置角色访问权限 | Bearer admin_token |
| P14 | PATCH | /api/admin/products/:id/prices | 配置套餐价格 | Bearer admin_token |
| P15 | GET | /api/admin/product-billing-rules | 计费规则列表 | Bearer admin_token |
| P16 | POST | /api/admin/product-billing-rules | 创建计费规则 | Bearer admin_token |
| P17 | PATCH | /api/admin/product-billing-rules/:id | 更新计费规则 | Bearer admin_token |
| O1 | GET | /api/orders | 我的订单列表 | Bearer token |
| O2 | GET | /api/orders/:id | 订单详情 | Bearer token |
| O3 | POST | /api/orders/:id/pay | 钱包支付 | Bearer token + Idempotency-Key |
| O4 | POST | /api/orders/:id/cancel | 取消订单 | Bearer token |
| O5 | GET | /api/admin/orders | 管理员全量订单 | Bearer admin_token |
| O6 | GET | /api/admin/orders/:id | 管理员订单详情 | Bearer admin_token |
| B1 | GET | /api/wallet | 查询钱包余额 | Bearer token |
| B2 | GET | /api/wallet/transactions | 本人钱包流水 | Bearer token |
| B3 | POST | /api/recharge/orders | 创建充值订单 | Bearer token |
| B4 | POST | /api/payments/notify/:provider | 支付回调（无需登录） | 签名校验 |
| B5 | GET | /api/admin/users/:id/wallet | 查用户钱包 | Bearer admin_token |
| B6 | GET | /api/admin/wallet-transactions | 全量钱包流水 | Bearer admin_token |
| B7 | PATCH | /api/admin/users/:id/wallet/freeze | 冻结/解冻余额 | Bearer admin_token |
| B8 | GET | /api/admin/payment-callbacks | 支付回调记录 | Bearer admin_token |
| F1 | POST | /api/internal/product-usage-events | 上报消费事件（内部） | X-Internal-Token |
| F2 | GET | /api/product-consumption-records | 本人消费记录 | Bearer token |
| F3 | GET | /api/admin/product-consumption-records | 全量消费记录 | Bearer admin_token |
