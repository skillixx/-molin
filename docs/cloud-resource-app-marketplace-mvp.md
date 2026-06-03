# 云资源与应用售卖平台 MVP 设计

## 1. MVP 目标

第一版先做一个可运营、可收费、可管控权限的平台，不追求一次性做成完整云厂商系统。

核心目标：

- 用户可以注册、登录、充值、消费。
- 平台可以管理用户、角色、应用、价格、订单、余额和设备。
- 用户可以购买应用服务，也可以租用 GPU 裸金属设备。
- 用户可以购买、定制和使用 agent。
- 用户可以购买、安装和管理 skills 技能。
- 平台可以接入多个 Token 上游供应商，并统一售卖模型调用能力。
- 不同角色看到不同应用、不同价格、不同权限。
- 所有财务动作都有流水，方便对账和追责。

暂不放进第一版的能力：

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
- 查看 agent 市场。
- 购买 agent 模板。
- 创建和定制自己的 agent。
- 查看 skills 技能市场。
- 购买和安装 skills。
- 查看 Token 模型服务。
- 购买 Token 套餐或按量调用。
- 查看 agent、skills、Token 的使用量和消费记录。

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
- Agent 模板管理。
- Agent 定制订单管理。
- Skills 技能管理。
- Token 上游供应商管理。
- Token 模型、价格、路由和用量管理。
- 操作审计日志。

### 2.3 权限范围

第一版使用 RBAC 为主，保留 ABAC 扩展字段。

权限控制点：

- 后台菜单权限。
- 后台接口权限。
- 应用可见权限。
- 应用购买权限。
- GPU 租赁权限。
- Agent 查看、购买、创建、发布权限。
- Skills 查看、购买、安装、发布权限。
- Token 模型调用权限。
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

product-service
  统一商品、套餐、价格、角色可见性、购买入口

business-router
  按业务类型路由到应用、GPU、Agent、Skills、Token、网盘等处理器

application-adapter
  新应用接入适配器、应用能力声明、开通/停用/续费回调

billing-service
  钱包、订单、支付、退款、流水、余额

finance-consumer-router
  产品消费事件接收、计费规则匹配、扣费、流水、对账

resource-service
  GPU 设备、设备分组、租赁、释放、状态同步

agent-service
  Agent 模板、用户 agent、定制订单、版本、发布

skill-service
  Skills 技能、版本、安装、授权、上下架

token-gateway
  Token 上游供应商、模型路由、调用鉴权、用量统计、成本核算

membership-service
  会员等级、会员权益、会员价格、会员订阅、到期续费

content-service
  系统公告、帮助文档、分类、发布、置顶、可见范围

admin-console
  运营后台

user-console
  用户控制台
```

第一版可以用模块化单体实现，代码里按模块拆分。由于第一版已经包含 Token 网关和 agent / skills 市场，建议把 `billing`、`token-gateway`、`resource` 三个模块的边界先设计清楚，后续最先拆成独立服务。

### 4.1 分层路由架构

第一版架构建议采用分层路由形式，把通用交易能力和具体业务开通能力拆开。

```text
HTTP API 层
  -> Auth / IAM 鉴权层
  -> Product 商品路由层
  -> Order / Billing 交易层
  -> Business Provision 业务开通层
  -> Finance Consumer Router 消费计费层
  -> Resource / App / Agent / Skill / Token / NetDisk 等业务模块
```

分层职责：

- `HTTP API 层`：只处理请求参数、响应格式、版本号。
- `Auth / IAM 鉴权层`：统一处理登录态、API Key、角色、权限。
- `Product 商品路由层`：根据 `product_type` 和 `product_code` 找到商品、套餐、价格和处理器。
- `Order / Billing 交易层`：统一创建订单、扣余额、写流水、退款、对账。
- `Business Provision 业务开通层`：把已支付订单交给对应业务处理器完成开通。
- `Finance Consumer Router 消费计费层`：接收各业务模块产生的消费事件，匹配计费规则并生成扣费流水。
- `业务模块`：只负责自己的业务规则，不直接操作钱包扣费。

业务类型建议统一枚举：

```text
app
gpu
agent
skill
token
netdisk
membership
```

新增网盘售卖应用时，只需要：

1. 在 `products` 注册 `product_type = netdisk` 的商品。
2. 在 `product_plans` 配置容量、时长、流量等套餐。
3. 在 `product_prices` 配置不同角色价格。
4. 在 `product_role_access` 配置可见、可购买、可使用权限。
5. 新增 `netdisk-service` 或 `netdisk` 模块。
6. 注册 `netdisk` 的开通处理器。
7. 实现 `Provision(order)`、`Renew(order)`、`Suspend(instance)`、`Cancel(instance)`。

订单、钱包、角色权限、财务流水和后台查询不需要重做。

### 4.2 应用扩展式接入

应用不要和订单、钱包、角色权限强绑定。每个新应用只需要按标准适配器接入。

应用适配器需要声明：

```text
AppDescriptor
- app_code
- app_name
- app_type
- supported_plan_fields
- supported_actions
- provision_mode
- callback_url
- usage_event_types
```

应用适配器需要实现：

```text
Provision(order, product, plan)
Renew(instance, order)
Suspend(instance, reason)
Resume(instance)
Cancel(instance)
QueryUsage(instance, period)
```

这样后面新增网盘、对象存储、数据库、API 套餐、SaaS 工具时，只需要：

1. 新增应用模块或外部应用对接配置。
2. 注册应用适配器。
3. 配置商品、套餐、价格、角色权限。
4. 配置财务消费规则。
5. 前端使用统一商品页面或增加少量业务详情页。

### 4.3 财务按产品消费快速对接

财务系统不要只支持“购买时扣费”，还要支持“使用中按量扣费”。所有产品消费都转换成统一消费事件。

统一消费事件：

```text
ProductUsageEvent
- event_id
- user_id
- product_type
- product_code
- product_plan_id
- instance_id
- usage_type
- usage_amount
- usage_unit
- occurred_at
- idempotency_key
```

财务消费路由处理：

```text
业务模块上报 ProductUsageEvent
  -> Finance Consumer Router 校验幂等键
  -> 匹配 product_billing_rules
  -> 计算消费金额
  -> 检查余额或额度
  -> 扣费或冻结
  -> 写入 wallet_transactions
  -> 写入 product_consumption_records
  -> 返回计费结果
```

这种结构下，Token、GPU、网盘、agent 调用、skills 调用都可以走统一消费对接：

```text
token     -> 按 input_tokens / output_tokens 扣费
gpu       -> 按小时、天、月扣费
netdisk   -> 按容量、流量、用户数、时长扣费
agent     -> 按调用次数、Token、定制服务扣费
skill     -> 按授权周期、调用次数、增值功能扣费
```

### 4.4 会员制售卖预留

会员制不要单独游离在商品体系外，应该和商品、权限、价格、财务统一打通。

会员制支持四种模式：

```text
member_only
  仅会员可购买或使用某应用

member_discount
  会员购买某应用享受折扣

member_price
  会员使用独立价格

member_included
  会员权益内包含某应用、额度或功能
```

会员本身也作为一种商品：

```text
products.product_type = membership
```

这样会员购买、续费、退款、流水、发票都走统一订单和钱包链路。某个应用是否采用会员制，只需要配置应用商品和会员权益规则，不需要重写应用购买流程。

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

### 5.2 统一商品与应用售卖

```text
products
- id
- product_type
- product_code
- name
- description
- status
- business_ref_id
- created_at
- updated_at

product_plans
- id
- product_id
- plan_code
- name
- billing_type
- duration_days
- quota_json
- status
- created_at
- updated_at

product_prices
- id
- product_plan_id
- role_id
- membership_level_id
- price_amount
- currency
- discount_rate
- effective_from
- effective_to

product_role_access
- id
- product_id
- role_id
- can_view
- can_buy
- can_use

product_provision_handlers
- id
- product_type
- handler_code
- service_name
- status
- created_at
- updated_at

application_adapters
- id
- app_code
- app_name
- app_type
- adapter_type
- service_name
- callback_url
- supported_actions_json
- usage_event_types_json
- status
- created_at
- updated_at

product_billing_rules
- id
- product_id
- product_plan_id
- usage_type
- usage_unit
- price_amount
- currency
- billing_mode
- free_quota
- status
- created_at
- updated_at

product_consumption_records
- id
- event_id
- user_id
- product_id
- product_plan_id
- instance_id
- usage_type
- usage_amount
- usage_unit
- amount
- wallet_transaction_id
- idempotency_key
- created_at

membership_levels
- id
- code
- name
- level_order
- status
- created_at
- updated_at

membership_benefits
- id
- membership_level_id
- benefit_type
- target_product_id
- target_product_type
- benefit_config_json
- status
- created_at
- updated_at

user_memberships
- id
- user_id
- membership_level_id
- source_order_id
- status
- started_at
- expires_at
- auto_renew
- created_at
- updated_at

product_membership_rules
- id
- product_id
- membership_level_id
- rule_type
- discount_rate
- included_quota_json
- status
- created_at
- updated_at

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

说明：

- `products` 是统一售卖入口，应用、GPU、Agent、Skills、Token、网盘都先抽象成商品。
- `business_ref_id` 指向具体业务表，例如 `applications.id`、`gpu_devices.id`、`agent_templates.id`。
- `quota_json` 存储不同业务的套餐参数，例如网盘容量、GPU 时长、Token 额度、skill 授权时长。
- 原有 `applications` 作为应用业务详情表保留，不直接承担所有商品交易逻辑。
- `application_adapters` 负责让新应用扩展式接入，业务系统不直接改订单和钱包代码。
- `product_billing_rules` 负责把产品用量转换成金额。
- `product_consumption_records` 负责记录每一次产品消费，便于财务对账、用户账单和运营分析。
- `membership_levels`、`membership_benefits`、`user_memberships` 负责会员等级、权益和用户会员状态。
- `product_membership_rules` 负责某个商品是否会员专属、会员折扣、会员价或会员内含。

### 5.2.1 系统公告与帮助文档

```text
announcements
- id
- title
- content
- type
- priority
- status
- visible_scope
- target_roles_json
- start_at
- end_at
- created_by
- created_at
- updated_at

help_categories
- id
- parent_id
- name
- sort_order
- status
- created_at
- updated_at

help_articles
- id
- category_id
- title
- content
- summary
- tags_json
- status
- sort_order
- view_count
- created_by
- published_at
- created_at
- updated_at
```

公告可见范围建议：

```text
all
roles
members
admins
```

帮助文档要支持分类、草稿、发布、下线、排序和搜索。

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

### 5.5 Agent 定制市场

```text
agent_templates
- id
- code
- name
- description
- category
- base_prompt
- default_model_id
- status
- created_by
- created_at
- updated_at

agent_template_prices
- id
- agent_template_id
- role_id
- price_amount
- currency
- billing_type
- effective_from
- effective_to

user_agents
- id
- user_id
- template_id
- name
- description
- system_prompt
- model_id
- status
- version
- created_at
- updated_at

agent_customization_orders
- id
- order_no
- user_id
- agent_template_id
- requirement
- status
- quoted_amount
- order_id
- assigned_operator_id
- delivered_at
- created_at
- updated_at

agent_usage_logs
- id
- user_id
- agent_id
- model_id
- input_tokens
- output_tokens
- total_tokens
- cost_amount
- created_at
```

### 5.6 Skills 技能市场

```text
skills
- id
- code
- name
- description
- category
- status
- publisher_id
- created_at
- updated_at

skill_versions
- id
- skill_id
- version
- manifest_json
- package_url
- changelog
- status
- created_at

skill_prices
- id
- skill_id
- role_id
- price_amount
- currency
- billing_type
- effective_from
- effective_to

user_skill_installs
- id
- user_id
- skill_id
- skill_version_id
- status
- installed_at
- expires_at

agent_skill_bindings
- id
- agent_id
- skill_id
- skill_version_id
- enabled
- created_at
```

### 5.7 Token 上游聚合网关

```text
token_providers
- id
- code
- name
- base_url
- auth_type
- encrypted_api_key
- status
- priority
- created_at
- updated_at

token_models
- id
- provider_id
- model_code
- display_name
- context_window
- input_price_per_1k
- output_price_per_1k
- sale_input_price_per_1k
- sale_output_price_per_1k
- status
- created_at
- updated_at

token_model_routes
- id
- logical_model_code
- provider_model_id
- weight
- priority
- status
- created_at
- updated_at

token_usage_logs
- id
- request_id
- user_id
- provider_id
- model_id
- logical_model_code
- input_tokens
- output_tokens
- total_tokens
- provider_cost_amount
- sale_amount
- latency_ms
- status
- error_code
- created_at

token_quota_accounts
- id
- user_id
- logical_model_code
- remaining_tokens
- monthly_limit_tokens
- status
- created_at
- updated_at
```

### 5.8 网盘售卖应用示例

网盘作为新增售卖应用时，不需要重做交易链路，只需要挂到统一商品和开通路由上。

```text
netdisk_plans
- id
- code
- name
- storage_gb
- traffic_gb
- max_files
- duration_days
- status
- created_at
- updated_at

netdisk_instances
- id
- user_id
- product_id
- product_plan_id
- order_id
- storage_gb
- used_storage_gb
- traffic_gb
- used_traffic_gb
- status
- started_at
- expires_at
- created_at
- updated_at

netdisk_events
- id
- instance_id
- event_type
- operator_id
- remark
- created_at
```

接入关系：

```text
products.product_type = netdisk
products.business_ref_id = netdisk_plans.id
product_provision_handlers.product_type = netdisk
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

### 6.1.1 统一商品购买与开通流程

```text
用户选择商品
  -> Product Router 根据 product_type 查询商品和套餐
  -> IAM 检查角色可见、可购买、可使用权限
  -> Pricing 读取角色对应价格
  -> Order 创建统一订单
  -> Billing 检查余额并扣费
  -> 写入钱包流水
  -> Provision Router 根据 product_type 调用业务处理器
  -> 业务模块开通实例或授权
  -> 返回购买结果
```

不同业务处理器示例：

```text
app       -> 开通应用访问权限
gpu       -> 锁定设备并创建租赁实例
agent     -> 创建用户 agent 或定制订单
skill     -> 创建 skill 安装授权
token     -> 增加 Token 额度或开启按量调用
netdisk   -> 创建网盘空间、容量、有效期
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

### 6.3.1 产品消费计费流程

```text
业务模块产生消费事件
  -> 上报 ProductUsageEvent
  -> 校验 idempotency_key 防重复扣费
  -> 根据 product_id、plan_id、usage_type 匹配计费规则
  -> 计算消费金额
  -> 开启数据库事务
  -> 扣减钱包余额或扣减产品额度
  -> 写入 wallet_transactions
  -> 写入 product_consumption_records
  -> 提交事务
  -> 返回计费结果
```

接入新产品时，财务侧只需要新增：

- 产品计费规则。
- 消费事件类型。
- 账单展示字段。
- 对账维度。

不需要为每个应用重新开发一套扣费逻辑。

### 6.4 Agent 定制流程

```text
用户选择 agent 模板
  -> 检查角色可见和购买权限
  -> 提交定制需求
  -> 创建定制订单
  -> 平台报价或读取标准价格
  -> 用户支付
  -> 运营人员交付 agent
  -> 用户验收
  -> agent 状态改为 available
```

### 6.5 Skills 购买安装流程

```text
用户选择 skill
  -> 检查角色可见和购买权限
  -> 读取角色对应价格
  -> 创建订单
  -> 扣减钱包
  -> 写入钱包流水
  -> 创建 user_skill_installs
  -> 用户绑定到指定 agent
```

### 6.6 Token 网关调用流程

```text
用户或 agent 发起模型调用
  -> 校验 API Key / 登录态
  -> 校验角色和模型调用权限
  -> 校验余额或 Token 额度
  -> 选择上游供应商和模型路由
  -> 请求上游模型
  -> 记录 Token 用量、延迟、状态
  -> 计算成本和销售金额
  -> 扣减钱包或 Token 额度
  -> 返回模型结果
```

### 6.7 会员购买与应用售卖流程

```text
用户购买会员商品
  -> Product Router 识别 product_type = membership
  -> 创建订单
  -> Billing 扣费并写入钱包流水
  -> Provision Router 开通 user_memberships
  -> 根据 membership_benefits 生效权益
```

用户购买会员制应用时：

```text
用户选择应用商品
  -> IAM 检查角色权限
  -> Membership 检查用户会员状态
  -> Product Pricing 匹配会员专属价、会员折扣或会员内含规则
  -> 如果 member_included 且额度充足，直接开通或扣权益额度
  -> 如果需要支付，走统一订单和钱包扣费
  -> Provision Router 开通应用权限
```

### 6.8 公告和帮助文档发布流程

```text
运营创建公告或帮助文档
  -> 保存草稿
  -> 设置可见范围、角色、会员范围和发布时间
  -> 发布
  -> 用户端按权限拉取
  -> 记录查看量和操作日志
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
GET  /api/product-consumption-records

GET  /api/apps
GET  /api/apps/:id
POST /api/apps/:id/purchase
GET  /api/my/apps

GET  /api/products
GET  /api/products/:id
GET  /api/products/:id/plans
POST /api/products/:id/purchase
GET  /api/my/products
GET  /api/memberships
GET  /api/my/membership
POST /api/memberships/:id/purchase

GET  /api/announcements
GET  /api/help/categories
GET  /api/help/articles
GET  /api/help/articles/:id

GET  /api/gpu/devices
GET  /api/gpu/devices/:id
POST /api/gpu/rentals
GET  /api/gpu/rentals
GET  /api/gpu/rentals/:id

GET  /api/agents/templates
GET  /api/agents/templates/:id
POST /api/agents/customization-orders
GET  /api/my/agents
POST /api/my/agents
PATCH /api/my/agents/:id

GET  /api/skills
GET  /api/skills/:id
POST /api/skills/:id/purchase
POST /api/my/agents/:id/skills

GET  /api/token/models
POST /api/token/chat/completions
GET  /api/token/usage
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

GET    /api/admin/products
POST   /api/admin/products
PATCH  /api/admin/products/:id
POST   /api/admin/products/:id/plans
PATCH  /api/admin/products/:id/access
PATCH  /api/admin/products/:id/prices
GET    /api/admin/product-handlers
GET    /api/admin/application-adapters
POST   /api/admin/application-adapters
PATCH  /api/admin/application-adapters/:id
GET    /api/admin/product-billing-rules
POST   /api/admin/product-billing-rules
PATCH  /api/admin/product-billing-rules/:id
GET    /api/admin/product-consumption-records
GET    /api/admin/membership-levels
POST   /api/admin/membership-levels
PATCH  /api/admin/membership-levels/:id
GET    /api/admin/membership-benefits
POST   /api/admin/membership-benefits
PATCH  /api/admin/membership-benefits/:id
GET    /api/admin/product-membership-rules
POST   /api/admin/product-membership-rules
PATCH  /api/admin/product-membership-rules/:id
GET    /api/admin/user-memberships

GET    /api/admin/orders
GET    /api/admin/wallet-transactions

GET    /api/admin/gpu/devices
POST   /api/admin/gpu/devices
PATCH  /api/admin/gpu/devices/:id
GET    /api/admin/gpu/rentals

GET    /api/admin/agent-templates
POST   /api/admin/agent-templates
PATCH  /api/admin/agent-templates/:id
GET    /api/admin/agent-customization-orders
PATCH  /api/admin/agent-customization-orders/:id

GET    /api/admin/skills
POST   /api/admin/skills
PATCH  /api/admin/skills/:id
POST   /api/admin/skills/:id/versions

GET    /api/admin/token/providers
POST   /api/admin/token/providers
PATCH  /api/admin/token/providers/:id
GET    /api/admin/token/models
POST   /api/admin/token/models
PATCH  /api/admin/token/models/:id
GET    /api/admin/token/usage

GET    /api/admin/announcements
POST   /api/admin/announcements
PATCH  /api/admin/announcements/:id
GET    /api/admin/help/categories
POST   /api/admin/help/categories
PATCH  /api/admin/help/categories/:id
GET    /api/admin/help/articles
POST   /api/admin/help/articles
PATCH  /api/admin/help/articles/:id

GET    /api/admin/audit-logs
```

## 8. 第一版页面

用户控制台：

- 登录页。
- 总览页。
- 统一商品市场。
- 商品详情。
- 我的商品和服务。
- 会员中心。
- 系统公告。
- 帮助中心。
- 应用市场。
- 应用详情。
- 我的应用。
- GPU 租赁。
- 我的 GPU 实例。
- Agent 市场。
- Agent 定制。
- 我的 Agent。
- Skills 市场。
- 我的 Skills。
- Token 模型服务。
- Token 用量统计。
- 账户余额。
- 账单流水。

管理后台：

- 仪表盘。
- 用户管理。
- 角色管理。
- 权限管理。
- 统一商品管理。
- 商品套餐管理。
- 商品价格配置。
- 商品角色权限配置。
- 商品开通处理器管理。
- 会员等级管理。
- 会员权益管理。
- 商品会员规则配置。
- 应用接入适配器管理。
- 产品计费规则管理。
- 产品消费记录。
- 系统公告管理。
- 帮助文档分类管理。
- 帮助文档内容管理。
- 应用管理。
- 应用价格配置。
- 订单管理。
- 财务流水。
- GPU 设备管理。
- GPU 租赁管理。
- Agent 模板管理。
- Agent 定制订单。
- Skills 技能管理。
- Skills 版本管理。
- Token 供应商管理。
- Token 模型管理。
- Token 路由管理。
- Token 用量与成本分析。
- 审计日志。

## 9. 开发计划

### 第 1 周：基础工程

- 建立前后端工程。
- 建立数据库 migration。
- 建立登录注册。
- 建立用户、角色、权限模型。
- 建立后台基础布局。

### 第 2 周：权限、商品路由与应用

- 完成 RBAC。
- 完成统一商品模型。
- 完成商品套餐、价格、角色权限配置。
- 完成商品购买路由。
- 完成业务开通处理器接口。
- 完成会员等级、会员权益和商品会员规则模型。
- 完成应用适配器注册接口。
- 完成应用 CRUD。
- 完成应用角色可见性。
- 完成应用价格配置。
- 完成用户端应用市场。

### 第 3 周：订单与钱包

- 完成钱包。
- 完成充值订单。
- 完成消费订单。
- 完成钱包流水。
- 完成统一商品购买。
- 完成产品消费事件接入。
- 完成产品计费规则。
- 完成产品消费记录。
- 完成会员商品购买和会员权益校验。
- 完成应用购买。
- 完成基础对账查询。

### 第 4 周：内容管理

- 完成系统公告管理。
- 完成帮助文档分类管理。
- 完成帮助文档内容管理。
- 完成用户端公告展示。
- 完成用户端帮助中心。

### 第 5 周：GPU 租赁

- 完成 GPU 设备管理。
- 完成设备状态流转。
- 完成租赁订单。
- 完成到期释放任务。
- 完成用户端租赁页面。

### 第 6 周：Agent 定制市场

- 完成 agent 模板管理。
- 完成 agent 角色价格配置。
- 完成用户 agent 创建和编辑。
- 完成 agent 定制订单。
- 完成 agent 调用记录。

### 第 7 周：Skills 技能市场

- 完成 skills 管理。
- 完成 skill 版本管理。
- 完成 skill 角色价格配置。
- 完成用户购买和安装 skill。
- 完成 agent 绑定 skill。

### 第 8 周：Token 上游聚合网关

- 完成 Token 上游供应商管理。
- 完成模型管理。
- 完成模型路由。
- 完成调用鉴权。
- 完成 Token 用量统计。
- 完成成本和售价核算。
- 完成余额或额度扣费。

### 第 9 周：运营后台完善

- 完成订单后台。
- 完成财务后台。
- 完成设备后台。
- 完成 agent 后台。
- 完成 skills 后台。
- 完成 Token 网关后台。
- 完成审计日志。
- 完成基础数据报表。

### 第 10 到 11 周：测试与上线

- 接口测试。
- 权限测试。
- 账务测试。
- 并发扣费测试。
- 会员购买、续费、权益校验测试。
- 系统公告和帮助文档发布测试。
- 设备状态流转测试。
- Agent 定制订单测试。
- Skills 安装和绑定测试。
- Token 网关路由、限流、扣费测试。
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
- 生成 agent、skills、Token 网关的 CRUD 和后台页面。
- 生成统一商品中心、商品路由和开通处理器接口。
- 生成会员等级、会员权益、公告、帮助文档管理页面。
- 生成 OpenAI 兼容接口适配层的基础代码。
- 生成单元测试。
- 生成接口测试。
- 生成部署脚本。
- 生成文档。

必须人工把关：

- 钱包扣费事务。
- 订单状态机。
- 商品路由抽象。
- 业务开通处理器幂等性。
- 会员权益和商品价格优先级。
- 公告可见范围和帮助文档发布权限。
- GPU 设备状态机。
- Agent 交付和版本管理。
- Skills 包安全审核。
- Token 上游密钥加密、路由和限流。
- Token 成本核算和扣费一致性。
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
9 到 11 周
```

更稳妥的商业试运营版本：

```text
13 到 17 周
```

如果后续要支撑 10 万用户、5 万设备、2000 个应用，建议在 MVP 验证后再做：

- 账务服务独立。
- 设备服务独立。
- 订单服务独立。
- Token 网关服务独立。
- Agent 服务独立。
- Skills 服务独立。
- 会员服务独立。
- 内容管理服务独立。
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
server/internal/modules/auth
server/internal/modules/iam
server/internal/modules/product
server/internal/modules/order
server/internal/modules/billing
server/internal/modules/finance_consumer
server/internal/modules/provision
server/internal/modules/application_adapter
server/internal/modules/app
server/internal/modules/gpu
server/internal/modules/agent
server/internal/modules/skill
server/internal/modules/token
server/internal/modules/netdisk
server/internal/modules/membership
server/internal/modules/content
server/pkg
server/migrations
infra/docker-compose.yml
```

第一轮代码优先实现：

- 登录。
- 用户管理。
- 角色管理。
- 统一商品中心。
- 商品路由。
- 开通处理器接口。
- 应用适配器。
- 财务消费路由。
- 会员等级和权益。
- 系统公告和帮助文档。
- 应用管理。
- 应用价格。
- 钱包。
- 订单。
- GPU 租赁。
- Agent 模板和定制订单。
- Skills 技能市场。
- Token 上游聚合网关。
