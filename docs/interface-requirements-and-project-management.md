# 接口需求、任务边界与项目管理方案

## 1. 目标

这份文档用于指导开发团队按角色开发接口、页面、测试和部署，并用于后续项目管理、演示和验收。

团队角色：

- 后端 1：账号、认证、实名制认证、角色、权限、审计。
- 后端 2：商品、订单、钱包、财务流水、按量计费。
- 后端 3：用户资产、会员、应用接入、公告、帮助文档。
- 前端 1：管理后台。
- 前端 2：用户控制台。
- 产品 / 测试：规则、用例、验收。
- 运维：Docker、MySQL、Redis、RabbitMQ、MinIO、CI。

第一阶段总目标：

```text
邮箱/手机号注册登录
  -> 实名制认证
  -> 用户角色权限
  -> 商品配置
  -> 用户充值
  -> 用户购买商品
  -> 钱包扣费
  -> 生成订单和流水
  -> 生成用户资产
  -> 用户访问已购买应用
  -> 后台可查询和管理
```

## 2. 通用接口规范

### 2.1 URL 规范

```text
用户端接口：/api/...
管理后台接口：/api/admin/...
```

### 2.2 响应格式

```json
{
  "code": 0,
  "message": "ok",
  "data": {},
  "request_id": "req_xxx"
}
```

错误响应：

```json
{
  "code": 40001,
  "message": "permission denied",
  "data": null,
  "request_id": "req_xxx"
}
```

### 2.3 分页格式

```json
{
  "items": [],
  "page": 1,
  "page_size": 20,
  "total": 100
}
```

### 2.4 通用要求

- 所有接口必须返回 `request_id`。
- 管理后台接口必须校验管理员权限。
- 用户端接口必须校验登录态。
- 资金、订单、资产相关接口必须支持幂等。
- 所有写操作必须记录审计日志。
- 列表接口必须支持分页。
- 重要列表需要支持筛选和时间范围查询。

## 3. 后端 1：账号、认证、实名制认证、角色、权限、审计

### 3.1 负责模块

```text
server/internal/modules/auth
server/internal/modules/identity
server/internal/modules/iam
server/internal/modules/audit
server/internal/middleware/auth
server/internal/middleware/permission
```

### 3.2 用户端认证接口

```text
POST /api/auth/verification-codes/email
POST /api/auth/verification-codes/phone
POST /api/auth/register/email
POST /api/auth/register/phone
POST /api/auth/login/email
POST /api/auth/login/phone
POST /api/auth/logout
POST /api/auth/refresh
POST /api/identity/verifications
GET  /api/identity/verifications/latest
GET  /api/me
PATCH /api/me/profile
PATCH /api/me/password
```

功能要求：

- 邮箱注册必须校验邮箱验证码。
- 手机号注册必须校验短信验证码。
- 邮箱和手机号都必须唯一。
- 登录成功返回 `access_token` 和 `refresh_token`。
- `access_token` 用于接口鉴权。
- `refresh_token` 用于续期。
- 支持用户后续绑定邮箱或手机号。
- 登录日志必须记录 IP、User-Agent、登录方式和结果。
- 用户注册后默认未实名。
- 未实名用户不能购买商品、租赁 GPU、调用 Token 或开通资产。
- 实名认证信息必须脱敏和加密存储。

### 3.3 实名制认证接口

```text
POST /api/identity/verifications
GET  /api/identity/verifications/latest

GET   /api/admin/identity-verifications
GET   /api/admin/identity-verifications/:id
PATCH /api/admin/identity-verifications/:id/review
```

功能要求：

- 用户注册后可以提交实名信息。
- 实名信息包括真实姓名、证件类型、证件号、必要的认证材料。
- 第一版可以先做人工审核模式，后续接第三方实名服务。
- 审核通过后更新 `users.real_name_status = verified`。
- 审核拒绝必须填写原因。
- 身份证号不能明文存储，只保存 hash 和 masked。
- 实名审核必须记录操作日志。

### 3.4 管理后台用户接口

```text
GET    /api/admin/users
GET    /api/admin/users/:id
PATCH  /api/admin/users/:id/status
GET    /api/admin/users/:id/roles
PATCH  /api/admin/users/:id/roles
GET    /api/admin/users/:id/permission-overrides
PATCH  /api/admin/users/:id/permission-overrides
GET    /api/admin/users/:id/login-logs
GET    /api/admin/users/:id/identity
```

功能要求：

- 支持按邮箱、手机号、状态、角色筛选用户。
- 可以启用、禁用用户。
- 可以动态调整用户角色。
- 可以给用户单独增加或禁用某个权限。
- 用户角色或权限变化后必须清理权限缓存。

### 3.5 角色权限接口

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

功能要求：

- 角色 code 不可重复。
- 权限 code 不可重复。
- 权限格式建议为 `resource:action`，例如 `product:create`。
- 删除角色前必须检查是否已有用户绑定。

### 3.6 审计日志接口

```text
GET /api/admin/audit-logs
```

功能要求：

- 记录操作人、操作对象、操作类型、请求 IP、请求参数摘要。
- 支持按用户、模块、操作类型、时间范围筛选。

### 3.7 交付验收

- 邮箱注册可用。
- 手机号注册可用。
- 实名认证提交可用。
- 实名认证审核可用。
- 未实名用户无法购买商品。
- 邮箱登录可用。
- 手机号登录可用。
- 管理员可以创建角色和权限。
- 管理员可以给用户分配角色。
- 权限中间件可以拦截无权限接口。
- 审计日志可查询。

## 4. 后端 2：商品、订单、钱包、财务流水、按量计费

### 4.1 负责模块

```text
server/internal/modules/product
server/internal/modules/order
server/internal/modules/billing
server/internal/modules/finance_consumer
```

### 4.2 商品接口

```text
GET    /api/products
GET    /api/products/:id
GET    /api/products/:id/plans
POST   /api/products/:id/purchase

GET    /api/admin/products
POST   /api/admin/products
GET    /api/admin/products/:id
PATCH  /api/admin/products/:id
POST   /api/admin/products/:id/plans
PATCH  /api/admin/products/:id/plans/:plan_id
PATCH  /api/admin/products/:id/access
PATCH  /api/admin/products/:id/prices
GET    /api/admin/product-handlers
```

功能要求：

- 商品支持 `app`、`gpu`、`agent`、`skill`、`token`、`netdisk`、`membership`。
- 商品购买前必须校验用户实名状态。
- 商品套餐支持 `billing_type`，例如一次性、包月、按量。
- 商品价格支持角色价格和会员价格。
- 商品访问控制支持可见、可购买、可使用。

### 4.3 订单接口

```text
GET  /api/orders
GET  /api/orders/:id
POST /api/orders/:id/pay
POST /api/orders/:id/cancel

GET  /api/admin/orders
GET  /api/admin/orders/:id
```

功能要求：

- 下单必须生成唯一 `order_no`。
- 支付前必须检查余额。
- 支付成功必须写钱包流水。
- 支付成功必须通知资产模块生成资产。
- 支持订单取消。
- 后续预留退款。

### 4.4 钱包接口

```text
GET  /api/wallet
GET  /api/wallet/transactions
POST /api/recharge/orders

GET  /api/admin/wallet-transactions
GET  /api/admin/users/:id/wallet
PATCH /api/admin/users/:id/wallet/freeze
```

功能要求：

- 钱包余额使用事务和乐观锁。
- 每次余额变化必须写流水。
- 余额不能直接无流水修改。
- 支持充值、消费、退款、冻结。

### 4.5 按量计费接口

```text
POST /api/internal/product-usage-events
GET  /api/product-consumption-records
GET  /api/admin/product-billing-rules
POST /api/admin/product-billing-rules
PATCH /api/admin/product-billing-rules/:id
GET  /api/admin/product-consumption-records
```

功能要求：

- 消费事件必须带 `idempotency_key`。
- 重复事件不能重复扣费。
- 计费规则按 `product_id + plan_id + usage_type` 匹配。
- 计费后必须写钱包流水和消费记录。

### 4.6 交付验收

- 管理员可以创建商品、套餐、价格。
- 用户可以购买商品。
- 购买会生成订单。
- 支付会扣钱包。
- 钱包流水可查。
- 重复支付不会重复扣款。
- 按量消费事件可以生成扣费记录。

## 5. 后端 3：用户资产、会员、应用接入、公告、帮助文档

### 5.1 负责模块

```text
server/internal/modules/asset
server/internal/modules/membership
server/internal/modules/application_adapter
server/internal/modules/app
server/internal/modules/content
```

### 5.2 用户资产接口

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

功能要求：

- 购买成功后生成 `user_assets`。
- 带额度的商品生成 `user_entitlements`。
- 资产要有状态、开始时间、到期时间。
- 资产变化必须记录 `asset_events`。

### 5.3 会员接口

```text
GET  /api/memberships
GET  /api/my/membership
POST /api/memberships/:id/purchase

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

功能要求：

- 会员本身作为商品售卖。
- 支持会员专属、会员折扣、会员价、会员内含。
- 用户购买会员后生成会员资产。
- 会员权益影响商品购买和访问判断。

### 5.4 应用接入接口

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

功能要求：

- 第一版先接一个普通应用做售卖闭环。
- 应用通过 `application_adapter` 接入。
- 应用开通后必须生成用户资产。
- 应用访问时必须校验角色、会员、资产和权益额度。

### 5.5 公告和帮助文档接口

```text
GET /api/announcements
GET /api/help/categories
GET /api/help/articles
GET /api/help/articles/:id

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

功能要求：

- 公告支持草稿、发布、下线。
- 公告支持按角色、会员、全员可见。
- 帮助文档支持分类、发布、下线、搜索。

### 5.6 交付验收

- 用户购买商品后可以看到资产。
- 会员购买后可以看到会员状态。
- 会员规则能影响商品购买。
- 管理员可以发布公告。
- 用户可以查看公告。
- 管理员可以维护帮助文档。
- 用户可以查看帮助文档。

## 6. 前端 1：管理后台

### 6.1 页面清单

```text
登录页
仪表盘
用户管理
角色管理
权限管理
用户动态授权
商品管理
商品套餐管理
商品价格配置
商品会员规则配置
订单管理
钱包流水
用户资产管理
会员等级管理
会员权益管理
应用管理
应用接入适配器管理
公告管理
帮助文档管理
审计日志
```

### 6.2 第一阶段优先页面

先做：

```text
登录页
用户管理
角色管理
权限管理
商品管理
套餐管理
价格配置
订单管理
钱包流水
用户资产管理
```

后做：

```text
会员管理
应用管理
公告管理
帮助文档
审计日志
```

### 6.3 验收标准

- 管理员可以登录。
- 管理员可以配置角色和权限。
- 管理员可以配置商品、套餐、价格。
- 管理员可以查看订单、流水和资产。

## 7. 前端 2：用户控制台

### 7.1 页面清单

```text
邮箱注册
手机号注册
邮箱登录
手机号登录
总览页
商品市场
商品详情
购买页
我的资产
我的权益额度
会员中心
账户余额
账单流水
系统公告
帮助中心
```

### 7.2 第一阶段优先页面

先做：

```text
注册页
登录页
总览页
商品市场
商品详情
购买页
我的资产
账户余额
账单流水
```

后做：

```text
会员中心
系统公告
帮助中心
```

### 7.3 验收标准

- 用户可以注册登录。
- 用户可以查看商品。
- 用户可以购买商品。
- 用户可以查看资产。
- 用户可以查看余额和流水。

## 8. 产品 / 测试

### 8.1 产品需要输出

- 角色清单。
- 权限清单。
- 商品类型清单。
- 订单状态说明。
- 钱包流水类型。
- 用户资产状态。
- 会员等级和权益。
- 第一版演示商品。
- 验收用例。

### 8.2 核心测试用例

```text
邮箱注册
手机号注册
邮箱登录
手机号登录
管理员创建角色
管理员分配权限
管理员创建商品
管理员配置套餐
管理员配置价格
用户充值
用户购买商品
钱包扣费
生成订单
生成流水
生成资产
用户查看资产
管理员查看订单和流水
会员购买
会员价购买商品
按量消费扣费
```

### 8.3 每周验收

每周至少验收一次，不要等全部开发完。

验收方式：

```text
开发人员演示
  -> 产品按用例验收
  -> 测试记录问题
  -> 问题进入下周修复
```

## 9. 运维

### 9.1 环境要求

```text
Docker
Docker Compose
MySQL
Redis
RabbitMQ
MinIO
Go API
Vue3 管理后台
Vue3 用户控制台
```

### 9.2 运维接口和工具

```text
GET /api/health
GET /api/ready
GET /api/version
```

要求：

- 开发环境一键启动。
- 测试环境可部署。
- migration 可执行。
- 日志可查看。
- 接口健康检查可用。
- 前端可以配置 API 地址。

## 10. 你怎么管理整个项目

### 10.1 项目节奏

建议采用一周一个迭代。

每周固定节奏：

```text
周一：确认本周任务
周三：中间检查
周五：演示和验收
```

### 10.2 任务管理

每个任务都要有：

- 负责人。
- 模块。
- 接口或页面。
- 截止时间。
- 验收标准。
- 当前状态。

任务状态：

```text
todo
doing
testing
done
blocked
```

### 10.3 分支管理

建议：

```text
main
develop
feature/auth
feature/product
feature/billing
feature/asset
feature/admin-console
feature/user-console
```

规则：

- 开发人员不要直接提交到 `main`。
- 功能开发进 feature 分支。
- 自测后合并到 `develop`。
- 每周验收通过后再合并到 `main`。

### 10.4 你每周要看什么

你重点看：

- 售卖闭环是否能跑通。
- 订单和钱包是否一致。
- 用户资产是否正确生成。
- 权限是否能正确拦截。
- 前端页面是否能完成真实操作。
- 测试环境是否可演示。

不要只看页面好不好看，也不要只听开发说接口写完了。

## 11. 项目演示方案

### 11.1 第一次演示

演示目标：

```text
管理员创建商品
  -> 配置套餐
  -> 配置价格
  -> 配置角色
  -> 用户注册
  -> 用户登录
  -> 用户购买商品
  -> 系统扣费
  -> 生成资产
  -> 后台查询订单和流水
```

### 11.2 第二次演示

演示目标：

```text
会员商品创建
  -> 用户购买会员
  -> 会员价购买应用
  -> 用户获得应用资产
```

### 11.3 第三次演示

演示目标：

```text
按量计费规则配置
  -> 模拟产品消费事件
  -> 自动扣费
  -> 生成消费记录和钱包流水
```

### 11.4 演示要求

- 必须用测试环境演示。
- 必须使用真实接口。
- 不允许只演示静态页面。
- 演示前准备测试账号。
- 演示后记录问题清单。

## 12. 测试方案

### 12.1 接口测试

每个后端负责人必须提供：

- Postman / Apifox 接口集合。
- 成功用例。
- 失败用例。
- 权限失败用例。

### 12.2 业务测试

重点测试：

- 注册登录。
- 实名认证提交和审核。
- 权限拦截。
- 商品购买。
- 钱包扣费。
- 订单状态。
- 资产生成。
- 会员权益。
- 按量扣费。

### 12.3 财务测试

财务相关必须重点测：

- 余额不足。
- 重复支付。
- 重复消费事件。
- 扣费失败回滚。
- 订单成功但资产失败。
- 资产成功但流水缺失。

### 12.4 上线前检查

上线前必须确认：

- 所有核心接口有权限控制。
- 购买、租赁、Token 调用等接口有实名状态校验。
- 钱包扣费有事务。
- 支付和扣费接口有幂等。
- 关键操作有审计日志。
- 数据库有索引。
- 测试环境演示通过。
- 主要流程测试通过。

## 13. 第一阶段完成标准

第一阶段只有满足下面条件才算完成：

- 可以注册登录。
- 可以提交和审核实名制认证。
- 可以管理用户角色权限。
- 可以创建商品和套餐。
- 可以配置价格和角色权限。
- 用户可以购买商品。
- 钱包可以扣费。
- 订单可以查询。
- 财务流水可以查询。
- 用户资产可以生成。
- 用户可以查看资产。
- 后台可以演示完整闭环。

达不到这些，不要进入 GPU、Agent、Skills、Token 的大规模开发。

## 14. 后端接口设计细化

### 14.1 接口分层

Go 后端接口建议按下面结构实现：

```text
HTTP Router
  -> Request Middleware
  -> Auth Middleware
  -> Permission Middleware
  -> Handler
  -> Service
  -> Repository
  -> MySQL / Redis / RabbitMQ / MinIO
```

每个模块都按照同一套目录规范：

```text
server/internal/modules/{module}
  handler.go
  service.go
  repository.go
  model.go
  dto.go
  routes.go
  errors.go
```

### 14.2 后端 1 接口明细

账号认证接口：

```text
POST /api/auth/verification-codes/email
POST /api/auth/verification-codes/phone
POST /api/auth/register/email
POST /api/auth/register/phone
POST /api/auth/login/email
POST /api/auth/login/phone
POST /api/auth/logout
POST /api/auth/refresh
POST /api/identity/verifications
GET  /api/identity/verifications/latest
GET  /api/me
PATCH /api/me/profile
PATCH /api/me/password
```

后台用户接口：

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
GET    /api/admin/identity-verifications
GET    /api/admin/identity-verifications/:id
PATCH  /api/admin/identity-verifications/:id/review
```

角色权限接口：

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

审计接口：

```text
GET /api/admin/audit-logs
```

### 14.3 后端 2 接口明细

商品接口：

```text
GET    /api/products
GET    /api/products/:id
GET    /api/products/:id/plans
POST   /api/products/:id/purchase

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

订单接口：

```text
GET  /api/orders
GET  /api/orders/:id
POST /api/orders/:id/pay
POST /api/orders/:id/cancel

GET  /api/admin/orders
GET  /api/admin/orders/:id
```

钱包和流水接口：

```text
GET   /api/wallet
GET   /api/wallet/transactions
POST  /api/recharge/orders

GET   /api/admin/wallet-transactions
GET   /api/admin/users/:id/wallet
PATCH /api/admin/users/:id/wallet/freeze
```

按量计费接口：

```text
POST  /api/internal/product-usage-events
GET   /api/product-consumption-records
GET   /api/admin/product-billing-rules
POST  /api/admin/product-billing-rules
PATCH /api/admin/product-billing-rules/:id
GET   /api/admin/product-consumption-records
```

### 14.4 后端 3 接口明细

用户资产接口：

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

会员接口：

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

应用和内容接口：

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

## 15. 数据库设计和基础软件

### 15.1 需要的软件

第一阶段需要：

```text
MySQL 8
Redis 7
RabbitMQ
MinIO
Docker
Docker Compose
```

用途：

- `MySQL`：业务主库，存用户、商品、订单、钱包、资产。
- `Redis`：登录态缓存、权限缓存、验证码缓存、限流。
- `RabbitMQ`：订单支付成功、资产开通、消费计费等异步事件。
- `MinIO`：帮助文档图片、应用图标、附件、导入导出文件。

### 15.2 数据库命名规范

```text
表名：小写复数，例如 users、orders
主键：id
时间字段：created_at、updated_at
状态字段：status
金额字段：decimal(18, 6)
JSON 字段：xxx_json
```

### 15.3 核心表分组

账号权限：

```text
users
identity_verifications
identity_verification_logs
verification_codes
user_login_logs
roles
permissions
user_roles
role_permissions
user_permission_overrides
role_change_logs
audit_logs
```

商品交易：

```text
products
product_plans
product_prices
product_role_access
product_provision_handlers
product_billing_rules
product_consumption_records
orders
order_items
wallets
wallet_transactions
```

资产会员：

```text
user_assets
user_entitlements
asset_events
membership_levels
membership_benefits
user_memberships
product_membership_rules
```

应用内容：

```text
applications
application_adapters
announcements
help_categories
help_articles
```

### 15.4 关键索引

必须有唯一索引：

```text
users.email
users.phone
identity_verifications.user_id
roles.code
permissions.code
products.product_code
orders.order_no
wallets.user_id
product_consumption_records.idempotency_key
```

必须有普通索引：

```text
orders.user_id
orders.status
orders.created_at
wallet_transactions.user_id
wallet_transactions.created_at
user_assets.user_id
user_assets.status
user_entitlements.user_id
product_consumption_records.user_id
product_consumption_records.created_at
audit_logs.operator_id
audit_logs.created_at
```

### 15.5 Migration 要求

目录：

```text
server/migrations
```

命名：

```text
000001_create_users.up.sql
000001_create_users.down.sql
000002_create_iam.up.sql
000002_create_iam.down.sql
```

要求：

- 每个模块必须提供 migration。
- migration 必须可重复在空库执行。
- 禁止手动直接改测试库结构。

## 16. 后端公共代码和路由网关

### 16.1 公共代码目录

```text
server/internal/bootstrap
server/internal/config
server/internal/http
server/internal/middleware
server/internal/router
server/internal/events
server/internal/jobs
server/internal/database
server/internal/cache
server/internal/storage
server/pkg/response
server/pkg/errors
server/pkg/logger
server/pkg/jwt
server/pkg/password
server/pkg/validator
server/pkg/pagination
server/pkg/idgen
server/pkg/money
server/pkg/timeutil
```

### 16.2 路由网关

第一版不用独立 API Gateway 服务，先在 Go API 内做路由网关。

路由分组：

```text
/api/auth
/api/identity
/api/me
/api/products
/api/orders
/api/wallet
/api/my
/api/admin
/api/internal
```

网关中间件顺序：

```text
RequestID
Logger
Recovery
CORS
RateLimit
Auth
Permission
Audit
```

### 16.3 权限中间件

示例规则：

```text
GET /api/admin/products       -> product:list
POST /api/admin/products      -> product:create
PATCH /api/admin/products/:id -> product:update
GET /api/admin/orders         -> order:list
GET /api/admin/users          -> user:list
PATCH /api/admin/users/:id    -> user:update
GET /api/admin/identity-verifications -> identity:list
PATCH /api/admin/identity-verifications/:id/review -> identity:review
```

### 16.4 事件总线

RabbitMQ 事件：

```text
order.paid
order.cancelled
asset.created
asset.expired
wallet.transaction.created
product.usage.reported
membership.activated
```

第一阶段必须实现：

```text
order.paid -> asset-service 创建资产
product.usage.reported -> finance-consumer 扣费
```

### 16.5 公共响应和错误码

错误码建议：

```text
0      success
40000  bad request
40001  unauthorized
40003  forbidden
40400  not found
40900  conflict
50000  internal error
60001  insufficient balance
60002  duplicate payment
60003  invalid product status
60004  asset not active
60005  quota not enough
```

## 17. 前端展示功能、组件和公共代码仓库

### 17.1 前端工程

```text
web/admin-console
web/user-console
web/shared
```

如果使用 monorepo，建议：

```text
web/packages/api-client
web/packages/components
web/packages/constants
web/packages/utils
web/packages/types
```

### 17.2 前端公共组件

必须沉淀公共组件：

```text
AppLayout
PageHeader
DataTable
SearchForm
FilterBar
StatusTag
MoneyText
DateTimeText
ConfirmButton
PermissionButton
RoleSelector
UserSelector
ProductSelector
PlanSelector
PriceEditor
JsonEditor
AuditDrawer
DetailDrawer
FormModal
UploadField
```

### 17.3 前端公共代码

```text
apiClient
authStore
permissionStore
routeGuard
formatMoney
formatDateTime
enumOptions
paginationHelper
errorHandler
requestInterceptor
responseInterceptor
```

### 17.4 管理后台功能页面

账号权限：

```text
登录页
用户列表
用户详情
实名认证审核
用户角色分配
用户动态授权
角色列表
角色权限配置
权限列表
审计日志
```

商品交易：

```text
商品列表
商品编辑
套餐配置
价格配置
角色可见配置
会员规则配置
订单列表
订单详情
钱包流水
消费记录
```

资产会员：

```text
用户资产列表
资产详情
用户权益额度
会员等级
会员权益
用户会员
```

应用内容：

```text
应用列表
应用适配器
公告列表
公告编辑
帮助分类
帮助文档编辑
```

### 17.5 用户控制台功能页面

```text
邮箱注册
手机号注册
邮箱登录
手机号登录
总览页
商品市场
商品详情
购买确认
我的资产
我的权益额度
会员中心
实名认证
钱包余额
账单流水
系统公告
帮助中心
```

### 17.6 前端路由权限

管理后台路由要支持权限控制：

```text
/users              -> user:list
/roles              -> role:list
/products           -> product:list
/orders             -> order:list
/wallet-transactions -> wallet:list
/assets             -> asset:list
/memberships        -> membership:list
/announcements      -> content:announcement:list
/help               -> content:help:list
```

按钮也要支持权限控制：

```text
新增
编辑
删除
启用
禁用
分配角色
配置权限
配置价格
```

## 18. 前后端联调要求

### 18.1 API 文档

每个后端负责人必须提供：

- 接口路径。
- 请求参数。
- 响应示例。
- 错误码。
- 权限码。
- 测试账号。

推荐使用：

```text
Apifox
Postman
OpenAPI JSON
```

### 18.2 Mock 规则

前端可以先用 mock，但必须和后端响应结构一致。

Mock 文件建议：

```text
web/shared/mocks
```

### 18.3 联调顺序

```text
auth
  -> iam
  -> product
  -> order
  -> wallet
  -> asset
  -> membership
  -> content
```

不要先联调复杂业务模块。

## 19. 第一阶段代码交付物

后端必须交付：

- migration。
- model。
- repository。
- service。
- handler。
- route。
- middleware。
- 单元测试。
- 接口文档。

前端必须交付：

- 页面。
- 表单。
- 表格。
- 权限按钮。
- API client。
- 状态管理。
- 错误处理。
- 路由守卫。

运维必须交付：

- Docker Compose。
- MySQL 初始化。
- Redis 配置。
- RabbitMQ 配置。
- MinIO 配置。
- 环境变量模板。
- 启动文档。

产品 / 测试必须交付：

- 业务规则。
- 测试用例。
- 演示脚本。
- 验收报告。
