# 完整接口设计

## 1. 通用约定

### 1.1 响应结构

```json
{
  "code": 0,
  "message": "ok",
  "data": {},
  "request_id": "req_xxx"
}
```

### 1.2 分页结构

```json
{
  "items": [],
  "page": 1,
  "page_size": 20,
  "total": 100
}
```

### 1.3 通用请求头

```text
Authorization: Bearer <access_token>
X-Request-ID: req_xxx
Idempotency-Key: idem_xxx
```

`Idempotency-Key` 用于购买、支付、充值、按量计费、资产开通等关键写操作。

## 2. 认证和实名接口

### 2.1 发送邮箱验证码

```text
POST /api/auth/verification-codes/email
```

请求：

```json
{
  "email": "user@example.com",
  "scene": "register"
}
```

响应：

```json
{
  "sent": true
}
```

### 2.2 发送短信验证码

```text
POST /api/auth/verification-codes/phone
```

请求：

```json
{
  "phone": "13800138000",
  "scene": "register"
}
```

响应：

```json
{
  "sent": true
}
```

### 2.3 邮箱注册

```text
POST /api/auth/register/email
```

请求：

```json
{
  "email": "user@example.com",
  "password": "password",
  "verification_code": "123456"
}
```

响应：

```json
{
  "user_id": 1,
  "email": "user@example.com",
  "real_name_status": "unverified"
}
```

### 2.4 手机号注册

```text
POST /api/auth/register/phone
```

请求：

```json
{
  "phone": "13800138000",
  "password": "password",
  "verification_code": "123456"
}
```

响应：

```json
{
  "user_id": 1,
  "phone": "13800138000",
  "real_name_status": "unverified"
}
```

### 2.5 邮箱登录

```text
POST /api/auth/login/email
```

请求：

```json
{
  "email": "user@example.com",
  "password": "password"
}
```

响应：

```json
{
  "access_token": "xxx",
  "refresh_token": "yyy",
  "expires_in": 7200
}
```

### 2.6 手机号登录

```text
POST /api/auth/login/phone
```

请求：

```json
{
  "phone": "13800138000",
  "password": "password"
}
```

响应同邮箱登录。

### 2.7 刷新令牌

```text
POST /api/auth/refresh
```

请求：

```json
{
  "refresh_token": "yyy"
}
```

响应同登录接口。

### 2.8 当前用户

```text
GET /api/me
```

响应：

```json
{
  "id": 1,
  "email": "user@example.com",
  "phone": "13800138000",
  "real_name_status": "verified",
  "roles": ["normal_user"],
  "permissions": ["product:list"]
}
```

### 2.9 提交实名认证

```text
POST /api/identity/verifications
```

请求：

```json
{
  "real_name": "张三",
  "id_card_no": "110101199001011234",
  "verification_type": "id_card",
  "attachments": [
    {
      "file_key": "identity/front.png",
      "file_type": "id_card_front"
    }
  ]
}
```

响应：

```json
{
  "verification_id": 1,
  "status": "pending"
}
```

### 2.10 查询最新实名认证

```text
GET /api/identity/verifications/latest
```

响应：

```json
{
  "id": 1,
  "status": "pending",
  "reject_reason": null,
  "submitted_at": "2026-06-03T00:00:00Z"
}
```

## 3. 管理后台账号权限接口

### 3.1 用户管理

```text
GET    /api/admin/users
GET    /api/admin/users/:id
POST   /api/admin/users
PATCH  /api/admin/users/:id
PATCH  /api/admin/users/:id/status
GET    /api/admin/users/:id/roles
PATCH  /api/admin/users/:id/roles
GET    /api/admin/users/:id/permission-overrides
PATCH  /api/admin/users/:id/permission-overrides
GET    /api/admin/users/:id/login-logs
GET    /api/admin/users/:id/identity
```

用户列表筛选参数：

```text
email
phone
status
real_name_status
role_code
page
page_size
```

### 3.2 角色权限

```text
GET    /api/admin/roles
POST   /api/admin/roles
GET    /api/admin/roles/:id
PATCH  /api/admin/roles/:id
DELETE /api/admin/roles/:id
GET    /api/admin/permissions
POST   /api/admin/permissions
PATCH  /api/admin/roles/:id/permissions
```

### 3.3 实名审核

```text
GET   /api/admin/identity-verifications
GET   /api/admin/identity-verifications/:id
PATCH /api/admin/identity-verifications/:id/review
```

审核请求：

```json
{
  "action": "approve",
  "reject_reason": ""
}
```

拒绝请求：

```json
{
  "action": "reject",
  "reject_reason": "证件信息不清晰"
}
```

### 3.4 审计日志

```text
GET /api/admin/audit-logs
```

筛选参数：

```text
operator_id
module
action
created_from
created_to
page
page_size
```

## 4. 商品、订单、钱包接口

### 4.1 用户端商品

```text
GET  /api/products
GET  /api/products/:id
GET  /api/products/:id/plans
POST /api/products/:id/purchase
GET  /api/my/products
```

购买请求：

```json
{
  "plan_id": 1,
  "quantity": 1
}
```

购买响应：

```json
{
  "order_id": 1,
  "order_no": "ORD202606030001",
  "status": "paid",
  "asset_id": 1
}
```

### 4.2 管理后台商品

```text
GET    /api/admin/products
POST   /api/admin/products
GET    /api/admin/products/:id
PATCH  /api/admin/products/:id
PATCH  /api/admin/products/:id/status
GET    /api/admin/products/:id/plans
POST   /api/admin/products/:id/plans
PATCH  /api/admin/products/:id/plans/:plan_id
PATCH  /api/admin/products/:id/access
PATCH  /api/admin/products/:id/prices
GET    /api/admin/product-handlers
```

创建商品请求：

```json
{
  "product_type": "app",
  "product_code": "demo_app",
  "name": "演示应用",
  "description": "用于第一阶段售卖闭环",
  "business_ref_id": 1
}
```

### 4.3 订单

```text
GET  /api/orders
GET  /api/orders/:id
POST /api/orders/:id/pay
POST /api/orders/:id/cancel
GET  /api/admin/orders
GET  /api/admin/orders/:id
```

### 4.4 钱包

```text
GET   /api/wallet
GET   /api/wallet/transactions
POST  /api/recharge/orders
GET   /api/admin/wallet-transactions
GET   /api/admin/users/:id/wallet
PATCH /api/admin/users/:id/wallet/freeze
```

充值请求：

```json
{
  "amount": "1000.00",
  "payment_method": "manual"
}
```

### 4.5 按量计费

```text
POST  /api/internal/product-usage-events
GET   /api/product-consumption-records
GET   /api/admin/product-billing-rules
POST  /api/admin/product-billing-rules
PATCH /api/admin/product-billing-rules/:id
GET   /api/admin/product-consumption-records
```

消费事件请求：

```json
{
  "event_id": "evt_001",
  "user_id": 1,
  "product_id": 1,
  "product_plan_id": 1,
  "instance_id": "asset_1",
  "usage_type": "token_input",
  "usage_amount": "1000",
  "usage_unit": "token",
  "idempotency_key": "usage_evt_001"
}
```

## 5. 用户资产、会员、应用、内容接口

### 5.1 用户资产

```text
GET /api/my/assets
GET /api/my/assets/:id
GET /api/my/entitlements
GET /api/admin/user-assets
GET /api/admin/user-entitlements
GET /api/admin/asset-events
GET /api/admin/users/:id/assets
GET /api/admin/users/:id/entitlements
```

### 5.2 会员

```text
GET   /api/memberships
GET   /api/my/membership
POST  /api/memberships/:id/purchase
GET   /api/admin/membership-levels
POST  /api/admin/membership-levels
PATCH /api/admin/membership-levels/:id
GET   /api/admin/membership-benefits
POST  /api/admin/membership-benefits
PATCH /api/admin/membership-benefits/:id
GET   /api/admin/product-membership-rules
POST  /api/admin/product-membership-rules
PATCH /api/admin/product-membership-rules/:id
GET   /api/admin/user-memberships
```

### 5.3 应用

```text
GET   /api/apps
GET   /api/apps/:id
POST  /api/apps/:id/purchase
GET   /api/my/apps
GET   /api/admin/apps
POST  /api/admin/apps
PATCH /api/admin/apps/:id
PATCH /api/admin/apps/:id/access
PATCH /api/admin/apps/:id/prices
GET   /api/admin/application-adapters
POST  /api/admin/application-adapters
PATCH /api/admin/application-adapters/:id
```

### 5.4 公告和帮助文档

```text
GET   /api/announcements
GET   /api/help/categories
GET   /api/help/articles
GET   /api/help/articles/:id
GET   /api/admin/announcements
POST  /api/admin/announcements
PATCH /api/admin/announcements/:id
GET   /api/admin/help/categories
POST  /api/admin/help/categories
PATCH /api/admin/help/categories/:id
GET   /api/admin/help/articles
POST  /api/admin/help/articles
PATCH /api/admin/help/articles/:id
```

## 6. 后续扩展接口

### 6.1 GPU

```text
GET    /api/gpu/devices
GET    /api/gpu/devices/:id
POST   /api/gpu/rentals
GET    /api/gpu/rentals
GET    /api/gpu/rentals/:id
GET    /api/admin/gpu/devices
POST   /api/admin/gpu/devices
PATCH  /api/admin/gpu/devices/:id
GET    /api/admin/gpu/rentals
```

### 6.2 Agent

```text
GET   /api/agents/templates
GET   /api/agents/templates/:id
POST  /api/agents/customization-orders
GET   /api/my/agents
POST  /api/my/agents
PATCH /api/my/agents/:id
GET   /api/admin/agent-templates
POST  /api/admin/agent-templates
PATCH /api/admin/agent-templates/:id
GET   /api/admin/agent-customization-orders
PATCH /api/admin/agent-customization-orders/:id
```

### 6.3 Skills

```text
GET   /api/skills
GET   /api/skills/:id
POST  /api/skills/:id/purchase
POST  /api/my/agents/:id/skills
GET   /api/admin/skills
POST  /api/admin/skills
PATCH /api/admin/skills/:id
POST  /api/admin/skills/:id/versions
```

### 6.4 Token

```text
GET   /api/token/models
POST  /api/token/chat/completions
GET   /api/token/usage
GET   /api/admin/token/providers
POST  /api/admin/token/providers
PATCH /api/admin/token/providers/:id
GET   /api/admin/token/models
POST  /api/admin/token/models
PATCH /api/admin/token/models/:id
GET   /api/admin/token/usage
```

## 7. 错误码

```text
0      成功
40000  请求参数错误
40001  未登录
40003  无权限
40400  资源不存在
40900  数据冲突
50000  系统内部错误
60001  余额不足
60002  重复支付
60003  商品状态不可用
60004  资产未生效
60005  权益额度不足
70001  未完成实名制认证
70002  实名认证审核中
70003  实名认证被拒绝
```
