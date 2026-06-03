# 云资源与应用售卖平台 MVP 设计

## 1. MVP 目标

第一版先做一个可运营、可收费、可管控权限的平台，不追求一次性做成完整云厂商系统。

核心目标：

- 用户可以注册、登录、充值、消费。
- 平台可以管理用户、角色、应用、价格、订单、余额和设备。
- 用户可以购买应用服务，也可以租用 GPU 裸金属设备。
- 不同角色看到不同应用、不同价格、不同权限。
- 所有财务动作都有流水，方便对账和追责。

暂不放进第一版的能力：

- 完整 agent 定制市场。
- skills 技能市场。
- Token 上游聚合网关。
- 复杂 GPU 自动调度。
- 多区域灾备。
- 分库分表。
- 企业级审批流。

这些可以在平台稳定后作为第二、第三阶段扩展。

## 2. 第一版功能范围

### 2.1 用户端

- 用户注册、登录、退出。
- 查看账户余额。
- 查看充值记录、消费记录。
- 查看可购买应用。
- 购买应用服务。
- 查看已购买服务。
- 查看可租用 GPU 设备。
- 提交 GPU 租赁订单。
- 查看租赁状态。

### 2.2 管理后台

- 用户管理。
- 角色管理。
- 权限管理。
- 应用管理。
- 应用价格管理。
- 应用角色可见性管理。
- 钱包流水查询。
- 订单管理。
- 设备管理。
- GPU 租赁管理。
- 操作审计日志。

### 2.3 权限范围

第一版使用 RBAC 为主，保留 ABAC 扩展字段。

权限控制点：

- 后台菜单权限。
- 后台接口权限。
- 应用可见权限。
- 应用购买权限。
- GPU 租赁权限。
- 价格策略权限。

角色示例：

- platform_admin：平台管理员。
- finance_admin：财务管理员。
- ops_admin：运维管理员。
- app_admin：应用管理员。
- normal_user：普通用户。
- vip_user：高级用户。
- reseller：渠道商。

## 3. 推荐技术栈

建议第一版采用：

- 前端：Vue3 + Vite + TypeScript。
- 管理后台 UI：Element Plus 或 Naive UI。
- 用户控制台 UI：Element Plus 或 Naive UI。
- 后端：Go。
- Go Web 框架：Gin、Fiber 或 Hertz。
- ORM / SQL：GORM 或原生 SQL。
- 数据库：MySQL。
- 缓存：Redis。
- 队列：RabbitMQ 或 Kafka。
- 对象存储：MinIO。
- 部署：Docker Compose 起步，后续迁移 Kubernetes。

第一版推荐：

```text
Vue3 + Vite + TypeScript + Go + Gin + MySQL + Redis + RabbitMQ
```

如果后端希望更高性能、更直接控制 SQL，可以采用：

```text
Vue3 + Vite + TypeScript + Go + Gin + 原生 SQL + MySQL + Redis + RabbitMQ
```

## 4. 系统模块

```text
auth-service
  登录、注册、JWT、API Key

iam-service
  用户、角色、权限、访问控制

app-service
  应用、应用版本、应用价格、应用权限

billing-service
  钱包、订单、支付、退款、流水、余额

resource-service
  GPU 设备、设备分组、租赁、释放、状态同步

admin-console
  运营后台

user-console
  用户控制台
```

第一版可以用模块化单体实现，代码里按模块拆分。等业务稳定后，再把 billing、resource 拆成独立服务。

## 5. 核心数据模型

### 5.1 用户与权限

```text
users
- id
- email
- phone
- password_hash
- status
- wallet_id
- created_at
- updated_at

roles
- id
- code
- name
- description
- created_at
- updated_at

permissions
- id
- code
- name
- resource
- action
- created_at
- updated_at

user_roles
- id
- user_id
- role_id

role_permissions
- id
- role_id
- permission_id
```

### 5.2 应用售卖

```text
applications
- id
- code
- name
- type
- description
- status
- created_at
- updated_at

application_plans
- id
- application_id
- name
- billing_type
- duration_days
- quota
- status

application_prices
- id
- application_plan_id
- role_id
- price_amount
- currency
- effective_from
- effective_to

application_role_access
- id
- application_id
- role_id
- can_view
- can_buy
- can_use
```

### 5.3 订单与钱包

```text
wallets
- id
- user_id
- balance_amount
- frozen_amount
- currency
- version
- created_at
- updated_at

wallet_transactions
- id
- wallet_id
- user_id
- type
- direction
- amount
- balance_after
- related_order_id
- remark
- created_at

orders
- id
- order_no
- user_id
- order_type
- status
- amount
- currency
- paid_at
- cancelled_at
- created_at
- updated_at

order_items
- id
- order_id
- item_type
- item_id
- item_name
- quantity
- unit_price
- total_price
```

账务要求：

- 所有充值、消费、退款都必须写入 `wallet_transactions`。
- 钱包扣费必须使用事务和乐观锁。
- `balance_amount` 只是当前余额快照，不是唯一账务依据。
- 每笔订单都要能追溯到钱包流水。

### 5.4 GPU 设备租赁

```text
gpu_devices
- id
- device_no
- region
- gpu_model
- gpu_count
- memory_gb
- cpu_model
- cpu_cores
- ram_gb
- disk_gb
- network_spec
- status
- price_per_hour
- price_per_day
- created_at
- updated_at

gpu_rentals
- id
- rental_no
- user_id
- device_id
- order_id
- status
- start_at
- end_at
- actual_release_at
- billing_mode
- total_amount
- created_at
- updated_at

gpu_device_events
- id
- device_id
- event_type
- old_status
- new_status
- operator_id
- remark
- created_at
```

设备状态建议：

```text
available
reserved
deploying
running
expired
releasing
maintenance
fault
offline
```

## 6. 核心业务流程

### 6.1 应用购买流程

```text
用户选择应用
  -> 检查角色是否可见
  -> 检查角色是否可购买
  -> 读取角色对应价格
  -> 创建订单
  -> 检查余额
  -> 扣减钱包
  -> 写入钱包流水
  -> 支付订单
  -> 开通应用权限
```

### 6.2 GPU 租赁流程

```text
用户选择设备规格
  -> 查询可用设备
  -> 锁定设备
  -> 创建租赁订单
  -> 检查余额
  -> 扣减或冻结金额
  -> 设备状态改为 reserved
  -> 开始部署
  -> 部署成功后改为 running
  -> 到期释放
  -> 写入设备事件
```

### 6.3 财务扣费流程

```text
开启数据库事务
  -> 查询钱包并锁定版本
  -> 判断余额是否足够
  -> 更新钱包余额和 version
  -> 写入 wallet_transactions
  -> 更新订单状态
提交事务
```

## 7. API 草案

### 用户端

```text
POST /api/auth/register
POST /api/auth/login
GET  /api/me

GET  /api/wallet
GET  /api/wallet/transactions
POST /api/recharge/orders

GET  /api/apps
GET  /api/apps/:id
POST /api/apps/:id/purchase
GET  /api/my/apps

GET  /api/gpu/devices
GET  /api/gpu/devices/:id
POST /api/gpu/rentals
GET  /api/gpu/rentals
GET  /api/gpu/rentals/:id
```

### 管理后台

```text
GET    /api/admin/users
PATCH  /api/admin/users/:id/status

GET    /api/admin/roles
POST   /api/admin/roles
PATCH  /api/admin/roles/:id

GET    /api/admin/apps
POST   /api/admin/apps
PATCH  /api/admin/apps/:id
PATCH  /api/admin/apps/:id/access
PATCH  /api/admin/apps/:id/prices

GET    /api/admin/orders
GET    /api/admin/wallet-transactions

GET    /api/admin/gpu/devices
POST   /api/admin/gpu/devices
PATCH  /api/admin/gpu/devices/:id
GET    /api/admin/gpu/rentals

GET    /api/admin/audit-logs
```

## 8. 第一版页面

用户控制台：

- 登录页。
- 总览页。
- 应用市场。
- 应用详情。
- 我的应用。
- GPU 租赁。
- 我的 GPU 实例。
- 账户余额。
- 账单流水。

管理后台：

- 仪表盘。
- 用户管理。
- 角色管理。
- 权限管理。
- 应用管理。
- 应用价格配置。
- 订单管理。
- 财务流水。
- GPU 设备管理。
- GPU 租赁管理。
- 审计日志。

## 9. 开发计划

### 第 1 周：基础工程

- 建立前后端工程。
- 建立数据库 migration。
- 建立登录注册。
- 建立用户、角色、权限模型。
- 建立后台基础布局。

### 第 2 周：权限与应用

- 完成 RBAC。
- 完成应用 CRUD。
- 完成应用角色可见性。
- 完成应用价格配置。
- 完成用户端应用市场。

### 第 3 周：订单与钱包

- 完成钱包。
- 完成充值订单。
- 完成消费订单。
- 完成钱包流水。
- 完成应用购买。
- 完成基础对账查询。

### 第 4 周：GPU 租赁

- 完成 GPU 设备管理。
- 完成设备状态流转。
- 完成租赁订单。
- 完成到期释放任务。
- 完成用户端租赁页面。

### 第 5 周：运营后台完善

- 完成订单后台。
- 完成财务后台。
- 完成设备后台。
- 完成审计日志。
- 完成基础数据报表。

### 第 6 周：测试与上线

- 接口测试。
- 权限测试。
- 账务测试。
- 并发扣费测试。
- 设备状态流转测试。
- Docker 部署。
- 灰度上线。

## 10. AI 开发方式

建议把 AI 当成研发团队里的初级到中级开发助手，而不是架构负责人。

适合交给 AI：

- 生成 CRUD。
- 生成数据库 migration。
- 生成接口 controller/service。
- 生成管理后台页面。
- 生成表单和表格。
- 生成权限校验中间件。
- 生成单元测试。
- 生成接口测试。
- 生成部署脚本。
- 生成文档。

必须人工把关：

- 钱包扣费事务。
- 订单状态机。
- GPU 设备状态机。
- 权限模型。
- 支付回调。
- 对账逻辑。
- 数据库索引。
- 高并发容量设计。
- 安全策略。

推荐 AI 工作流：

```text
产品负责人写业务规则
  -> AI 生成 PRD 和接口草案
  -> 技术负责人审设计
  -> AI 生成代码
  -> 开发人员审代码
  -> AI 生成测试
  -> CI 自动验证
  -> 人工验收关键流程
```

## 11. 第一版团队与时间

最低配置：

- 1 名后端。
- 1 名前端。
- 1 名产品/测试。
- 1 名运维兼职。

使用 AI 辅助，第一版可运营 MVP 预计：

```text
4 到 6 周
```

更稳妥的商业试运营版本：

```text
8 到 10 周
```

如果后续要支撑 10 万用户、5 万设备、2000 个应用，建议在 MVP 验证后再做：

- 账务服务独立。
- 设备服务独立。
- 订单服务独立。
- 读写分离。
- Redis 缓存。
- 队列削峰。
- 设备状态异步同步。
- 分布式任务调度。
- 审计和风控。

## 12. 下一步

建议下一步直接创建工程：

```text
web/admin-console
web/user-console
server/api
server/internal
server/pkg
server/migrations
infra/docker-compose.yml
```

第一轮代码优先实现：

- 登录。
- 用户管理。
- 角色管理。
- 应用管理。
- 应用价格。
- 钱包。
- 订单。

GPU 租赁在账务闭环完成后再接入。
