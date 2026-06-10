# 前端接口参考文档

> **版本**：Week 1 + Week 2 已验收（2026-06-06）；2026-06-10 补丁更新（发码拦截 + 管理员双重认证强制）
> **测试服务器**：`http://8.130.9.163:8080`
> **鉴权方式**：所有需要登录的接口在 Header 中携带 `Authorization: Bearer <access_token>`

---

## 通用规范

### 响应结构

所有接口统一返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": { ... }
}
```

失败时：

```json
{
  "code": 40000,
  "message": "请求参数错误",
  "data": null
}
```

### 错误码速查

| code  | HTTP | 含义 |
|-------|------|------|
| 40000 | 400  | 请求参数错误 / 验证码错误或已过期 |
| 40001 | 401  | 未登录 / Token 无效或过期 |
| 40003 | 403  | 无权限 |
| 40004 | 404  | 资源不存在 |
| 40031 | 403  | 管理员未完成双重认证（手机+邮箱），需先调用 verify-phone 和 verify-email |
| 40101 | 401  | 账号已被封禁 |
| 40404 | 404  | 账号未注册，请先注册（登录发码时账号不存在） |
| 40900 | 409  | 账号已注册（注册发码时账号已存在） |
| 42900 | 429  | 请求频率超限 |
| 50000 | 500  | 服务器内部错误 |
| 60001 | 400  | 余额不足 |
| 70001 | 400  | 需要先完成实名认证 |

### 分页参数（列表接口通用）

请求：`?page=1&page_size=10`

响应 `data` 结构：
```json
{
  "list": [...],
  "pagination": {
    "page": 1,
    "page_size": 10,
    "total": 100
  }
}
```

---

## 一、认证模块（后端甲）

### 1.1 发送验证码

**POST** `/api/auth/verification-codes/email` — 发送邮箱验证码

**POST** `/api/auth/verification-codes/phone` — 发送手机验证码

请求体：
```json
{
  "target": "user@example.com",
  "scene": "register"
}
```

`scene` 可选值及前置校验规则：

| scene | 说明 | 前置校验 |
|---|---|---|
| `register` | 注册验证码 | 账号已注册 → 返回 409/40900，拒绝发码 |
| `login` | 登录验证码 | 账号未注册 → 返回 404/40404，提示先注册 |
| `reset_password` | 重置密码 | 无前置校验 |
| `bind_phone` | 换绑手机号 | 无前置校验 |
| `bind_email` | 换绑邮箱 | 无前置校验 |
| `admin_verify` | 管理员双重认证 | 需要 Bearer Token + user:manage 权限 |

响应：`data: null`（成功即可）；测试环境响应体包含明文 `code` 字段

---

### 1.2 注册

> ⚠️ 旧的单独邮箱注册（`/api/auth/register/email`）和单独手机号注册（`/api/auth/register/phone`）已下线，唯一入口为统一注册。

**POST** `/api/auth/register` — 统一注册（手机 + 邮箱 + 用户名，需双验证码）

```json
{
  "username": "张三",
  "phone": "13812345678",
  "email": "user@example.com",
  "password": "Test1234!",
  "phone_code": "123456",
  "email_code": "654321"
}
```

响应（HTTP 201）：
```json
{
  "access_token": "eyJhbGci...",
  "refresh_token": "eyJhbGci...",
  "expires_in": 7200
}
```

---

### 1.3 登录

**POST** `/api/auth/login/email` — 邮箱 + 密码登录

```json
{
  "email": "user@example.com",
  "password": "Test1234!"
}
```

**POST** `/api/auth/login/phone` — 手机号 + 验证码登录（需先用 `scene=login` 发码）

```json
{
  "phone": "13812345678",
  "code": "123456"
}
```

> 注意：`scene=login` 发码时若手机号未注册，返回 404/40404"手机号未注册，请先注册"

响应：同注册，返回 `access_token` / `refresh_token` / `expires_in`

---

### 1.4 刷新 Token

**POST** `/api/auth/refresh`

```json
{
  "refresh_token": "eyJhbGci..."
}
```

响应：同登录，返回新的 token 对

---

### 1.5 退出登录

**POST** `/api/auth/logout` *(需登录)*

```json
{
  "refresh_token": "eyJhbGci..."
}
```

响应：`data: null`

---

### 1.6 重置密码（忘记密码，无需旧密码）

**POST** `/api/auth/password/reset`

```json
{
  "target": "user@example.com",
  "target_type": "email",
  "code": "123456",
  "new_password": "NewPass1234!"
}
```

`target_type`：`phone` 或 `email`

---

### 1.7 个人信息

**GET** `/api/me` *(需登录)*

响应 `data`：
```json
{
  "id": 1,
  "username": "张三",
  "email": "us***@example.com",
  "email_verified": true,
  "phone": "138****5678",
  "phone_verified": true,
  "real_name_status": "unverified",
  "status": "active",
  "admin_phone_verified": false,
  "admin_email_verified": false,
  "created_at": "2026-06-01T10:00:00Z",
  "last_login_at": "2026-06-06T08:00:00Z"
}
```

`real_name_status`：`unverified` / `pending` / `verified` / `rejected`

---

### 1.8 修改个人信息

**PATCH** `/api/me/password` *(需登录)*
```json
{ "old_password": "OldPass!", "new_password": "NewPass!" }
```

**PATCH** `/api/me/username` *(需登录)*
```json
{ "username": "新用户名" }
```

**PATCH** `/api/me/phone` *(需登录)*
```json
{ "phone": "13912345678", "code": "123456" }
```

**PATCH** `/api/me/email` *(需登录)*
```json
{ "email": "new@example.com", "code": "123456" }
```

响应：`data: null`

---

## 二、实名认证模块（后端甲）

### 2.1 提交实名认证

**POST** `/api/identity/verifications` *(需登录)*

```json
{
  "real_name": "张三",
  "id_card_no": "110101199001011234",
  "attachments": ["https://oss.example.com/front.jpg", "https://oss.example.com/back.jpg"]
}
```

> 注意：身份证号不存明文，后端仅用于 HMAC 校验后丢弃，响应中返回脱敏值

---

### 2.2 查询我的认证状态

**GET** `/api/identity/verifications/me` *(需登录)*

响应 `data`：
```json
{
  "id": 1,
  "real_name": "张三",
  "id_card_no_masked": "110101******1234",
  "status": "pending",
  "reject_reason": null
}
```

`status`：`pending`（待审核）/ `verified`（已认证）/ `rejected`（已拒绝）

---

### 1.9 管理员双重认证（仅管理员账号）

> 管理员登录后，调用 IAM / 实名审核 / 封禁用户等管理端接口前必须先完成双重认证。
> 未完成时返回 403/40031"请先完成管理员双重认证（手机+邮箱）"。
> 认证有效期由服务端 `ADMIN_VERIFY_EXPIRE_HOURS` 配置（默认 24 小时），超时需重新认证。

**流程：**
```
1. 发手机验证码：POST /api/auth/verification-codes/phone  scene=admin_verify
2. 完成手机认证：POST /api/admin/auth/verify-phone  {"code": "..."}
3. 发邮箱验证码：POST /api/auth/verification-codes/email  scene=admin_verify
4. 完成邮箱认证：POST /api/admin/auth/verify-email  {"code": "..."}
5. 此后可调用管理端接口
```

**POST** `/api/admin/auth/verify-phone` *(需登录 + user:manage 权限)*
```json
{ "code": "123456" }
```

**POST** `/api/admin/auth/verify-email` *(需登录 + user:manage 权限，需手机已认证)*
```json
{ "code": "123456" }
```

---

## 三、角色权限模块（后端甲，需 `role:manage` 权限 + 管理员双重认证）

### 3.1 角色管理

**GET** `/api/admin/roles` — 角色列表

**POST** `/api/admin/roles`
```json
{ "code": "vip", "name": "VIP用户", "description": "可见高级商品" }
```

**PUT** `/api/admin/roles/{id}`
```json
{ "code": "vip", "name": "VIP会员", "description": "更新描述" }
```

**DELETE** `/api/admin/roles/{id}`

### 3.2 权限列表

**GET** `/api/admin/permissions` — 查看所有权限定义

### 3.3 用户角色分配

**GET** `/api/admin/users/{id}/roles` — 查询用户角色

**POST** `/api/admin/users/{id}/roles`
```json
{ "role_id": 2, "reason": "升级为 VIP" }
```

**DELETE** `/api/admin/users/{id}/roles/{role_id}`

### 3.4 用户权限覆盖

**GET** `/api/admin/users/{id}/permission-overrides`

**POST** `/api/admin/users/{id}/permission-overrides`
```json
{ "permission_id": 5, "effect": "allow", "reason": "临时授权" }
```

`effect`：`allow` / `deny`

**DELETE** `/api/admin/users/{id}/permission-overrides/{override_id}`

---

## 四、实名审核（后端甲，需 `identity:review` 权限）

**GET** `/api/admin/identity-verifications?page=1&page_size=10` — 待审核列表

**GET** `/api/admin/identity-verifications/{id}` — 审核详情

**PATCH** `/api/admin/identity-verifications/{id}/review`
```json
{ "approve": true, "reason": "" }
```
拒绝时：`{ "approve": false, "reason": "证件模糊" }`

---

## 五、商品模块（后端乙）

### 5.1 用户端

**GET** `/api/products?page=1&page_size=10` *(需登录)*

响应 `data`：
```json
{
  "list": [
    {
      "id": 1,
      "product_type": "service",
      "product_code": "cloud-001",
      "name": "云服务基础版",
      "description": "...",
      "status": "active"
    }
  ],
  "pagination": { "page": 1, "page_size": 10, "total": 5 }
}
```

**GET** `/api/products/{id}` *(需登录)*

响应 `data`：
```json
{
  "product": { "id": 1, "name": "...", "status": "active" },
  "plans": [
    {
      "id": 1,
      "plan_code": "basic",
      "name": "基础版一年",
      "billing_type": "one_time",
      "duration_days": 365,
      "user_price": "10.000000",
      "currency": "CNY",
      "status": "active"
    }
  ]
}
```

**GET** `/api/products/{id}/plans` *(需登录)*

响应 `data`：`{ "plans": [...] }`（含用户实际价格）

---

### 5.2 购买商品

**POST** `/api/products/{id}/purchase` *(需登录)*

> **必须携带请求头** `Idempotency-Key: <唯一字符串>`（防重复提交）

```json
{
  "plan_id": 1,
  "quantity": 1,
  "remark": "购买备注（可选）"
}
```

响应 `data`：
```json
{
  "order_id": 101,
  "order_no": "ORD2026060600001",
  "status": "paid",
  "amount": "10.000000",
  "idempotent": false
}
```

`idempotent: true` 表示该 Idempotency-Key 已存在，返回原订单，不重复扣费。

**常见错误**：
- `70001` — 需要先完成实名认证
- `40003` — 无购买权限（角色未配置 can_buy）
- `60001` — 余额不足

---

### 5.3 管理端商品

**GET** `/api/admin/products?page=1&page_size=10` *(需 `product:view` 权限)*

**POST** `/api/admin/products` *(需 `product:create` 权限)*
```json
{
  "product_type": "service",
  "product_code": "cloud-001",
  "name": "云服务基础版",
  "description": "描述文字",
  "status": "draft"
}
```

**GET** `/api/admin/products/{id}` *(需 `product:view` 权限)*

**PATCH** `/api/admin/products/{id}` *(需 `product:edit` 权限)*
```json
{ "name": "新名称", "description": "新描述" }
```

**PATCH** `/api/admin/products/{id}/status` *(需 `product:edit` 权限)*
```json
{ "status": "active" }
```
`status`：`draft` / `active` / `inactive`

**GET** `/api/admin/products/{id}/plans` *(需 `product:create` 权限)*

**POST** `/api/admin/products/{id}/plans` *(需 `product:create` 权限)*
```json
{
  "plan_code": "basic",
  "name": "基础版",
  "billing_type": "one_time",
  "duration_days": 365,
  "status": "active"
}
```
`billing_type`：`one_time` / `monthly` / `yearly` / `usage`

**PATCH** `/api/admin/products/{id}/plans/{plan_id}` *(需 `product:edit` 权限)*
```json
{ "name": "新套餐名", "status": "inactive" }
```

**PATCH** `/api/admin/products/{id}/prices` *(需 `product:edit` 权限)*

覆盖写入（全量替换该套餐的价格）：
```json
{
  "prices": [
    { "price_amount": "10.00", "currency": "CNY" },
    { "role_id": 2, "price_amount": "8.00", "currency": "CNY" },
    { "membership_level_id": 1, "price_amount": "6.00", "currency": "CNY" }
  ]
}
```

价格优先级：**会员价 > 角色价 > 默认价**（三者均可配置，取用户匹配的最高优先级）

**PATCH** `/api/admin/products/{id}/access` *(需 `product:edit` 权限)*

覆盖写入角色访问规则：
```json
{
  "accesses": [
    { "role_id": 1, "can_view": true, "can_buy": true, "can_use": true },
    { "role_id": 2, "can_view": true, "can_buy": false, "can_use": false }
  ]
}
```

---

## 六、订单模块（后端乙）

### 6.1 用户端

**GET** `/api/orders?page=1&page_size=10` *(需登录)*

支持过滤：`?status=paid&order_type=purchase`

响应 `data`：
```json
{
  "list": [
    {
      "id": 101,
      "order_no": "ORD2026060600001",
      "order_type": "purchase",
      "product_id": 1,
      "product_plan_id": 1,
      "status": "paid",
      "amount": "10.000000",
      "currency": "CNY",
      "paid_at": "2026-06-06T10:00:00Z",
      "created_at": "2026-06-06T09:59:00Z"
    }
  ],
  "pagination": { "page": 1, "page_size": 10, "total": 3 }
}
```

`status`：`pending` / `paid` / `cancelled` / `failed`

`order_type`：`purchase`（购买订单）/ `recharge`（充值订单）

**GET** `/api/orders/{id}` *(需登录)*

---

### 6.2 管理端

**GET** `/api/admin/orders?page=1&page_size=10` *(需 `order:list` 权限)*

支持过滤：`?user_id=1&status=paid&order_type=purchase`

**GET** `/api/admin/orders/{id}` *(需 `order:list` 权限)*

---

## 七、钱包 & 支付模块（后端乙）

### 7.1 用户端

**GET** `/api/wallet` *(需登录)*

响应 `data`：
```json
{
  "id": 1,
  "user_id": 1,
  "balance_amount": "90.000000",
  "frozen_amount": "0.000000",
  "currency": "CNY"
}
```

**GET** `/api/wallet/transactions?page=1&page_size=10` *(需登录)*

响应 `data.list` 单条结构：
```json
{
  "id": 1,
  "type": "recharge",
  "direction": "in",
  "amount": "100.000000",
  "balance_after": "100.000000",
  "remark": "微信支付充值",
  "created_at": "2026-06-06T10:00:00Z"
}
```

`type`：`recharge`（充值）/ `consume`（消费）/ `refund`（退款）/ `freeze`（冻结）/ `unfreeze`（解冻）

`direction`：`in`（入账）/ `out`（出账）

**POST** `/api/recharge/orders` *(需登录)*

```json
{
  "amount": "100.00",
  "provider": "wechat",
  "remark": "充值"
}
```

`provider`：`wechat` / `alipay`

响应 `data`：
```json
{
  "order_id": 201,
  "pay_url": "https://pay.example.com/..."
}
```

---

### 7.2 支付回调（无需登录）

**POST** `/api/payments/notify/{provider}`

`provider`：`wechat` 或 `alipay`

微信必须携带请求头：
```
Wechatpay-Signature: <签名>
Wechatpay-Timestamp: <时间戳>
Wechatpay-Nonce: <随机串>
```

支付宝 body 中必须含 `sign` 字段。

缺少签名字段返回 HTTP 400 / code=40000。

---

### 7.3 管理端钱包

**GET** `/api/admin/users/{id}/wallet` *(需 `wallet:view` 权限)*

**GET** `/api/admin/wallet-transactions?page=1&page_size=10` *(需 `wallet:view` 权限)*

支持过滤：`?user_id=1`

**PATCH** `/api/admin/users/{id}/wallet/freeze` *(需 `wallet:view` 权限)*
```json
{
  "amount": "50.00",
  "action": "freeze",
  "remark": "风控冻结"
}
```
`action`：`freeze` / `unfreeze`

**GET** `/api/admin/payment-callbacks?page=1&page_size=10` *(需 `wallet:view` 权限)*

支持过滤：`?provider=wechat&status=processed`

---

## 八、管理员双重认证（后端甲，需 `user:manage` 权限）

**POST** `/api/admin/auth/verify-phone`
```json
{ "code": "123456" }
```

**POST** `/api/admin/auth/verify-email`
```json
{ "code": "123456" }
```

---

## 附录

### 权限码清单

| 权限码 | 说明 |
|--------|------|
| `role:manage` | 角色与权限管理 |
| `identity:review` | 实名认证审核 |
| `user:manage` | 用户管理（管理员双重认证） |
| `product:view` | 查看商品（只读） |
| `product:create` | 创建商品/套餐 |
| `product:edit` | 编辑商品/价格/权限 |
| `order:list` | 查看订单 |
| `wallet:view` | 查看钱包/流水/回调 |

### 枚举值汇总

| 字段 | 可选值 |
|------|--------|
| `real_name_status` | `unverified` / `pending` / `verified` / `rejected` |
| `order.status` | `pending` / `paid` / `cancelled` / `failed` |
| `order.order_type` | `purchase` / `recharge` |
| `product.status` | `draft` / `active` / `inactive` |
| `wallet_transaction.type` | `recharge` / `consume` / `refund` / `freeze` / `unfreeze` |
| `wallet_transaction.direction` | `in` / `out` |
| `payment_callback.status` | `received` / `processed` / `ignored` |
| `billing_type` | `one_time` / `monthly` / `yearly` / `usage` |
| `provider` | `wechat` / `alipay` |
