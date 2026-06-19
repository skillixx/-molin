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

## 2. 分阶段交付范围

> **重要原则：** GPU 租赁、Agent 定制市场、Skills 技能市场、Token 网关各自都有足够的复杂度，不应全部放进第一轮 MVP。先把平台底座做稳，再接入各业务模块。

### 第一阶段：平台底座 + 应用售卖（Week 1–4）

目标：跑通完整的商品购买闭环，支撑基本运营。

**用户端**

- 用户注册（邮箱 / 手机号）、登录、退出、刷新令牌。
- 查看和修改个人资料、修改密码。
- 提交实名认证、查看认证状态。
- 查看账户余额。
- 查看充值记录、消费记录。
- 充值（余额充值，支持微信支付 / 支付宝）。
- 查看可购买应用。
- 购买应用服务（一次性 / 按周期）。
- 查看已购买服务和资产。
- 系统公告展示。
- 帮助中心。

**管理后台**

- 用户管理（查询、状态变更、角色分配）。
- 实名认证审核。
- 角色管理、权限管理、用户动态授权。
- 应用管理、应用价格配置、应用角色可见性管理。
- 统一商品管理、套餐配置、价格配置、角色权限配置。
- 会员等级、会员权益、商品会员规则管理。
- 钱包流水查询、订单管理。
- 用户资产管理、权益额度管理。
- 应用适配器管理。
- 系统公告管理、帮助文档管理。
- 审计日志。

**暂不放进第一阶段的能力**

- GPU 租赁。
- Agent 定制市场。
- Skills 技能市场。
- Token 网关。
- 复杂 GPU 自动调度。
- 多区域灾备。
- 分库分表。
- 企业级审批流。

### 第三阶段：GPU 服务器出售（Week 10–12）

底座稳定后接入：

- GPU 设备管理（上架、下架、维护、故障、状态流转）。
- GPU 租赁订单（按小时 / 按天 / 按月）。
- 设备状态同步（状态机：available → reserved → deploying → running → releasing → released）。
- 租赁到期自动释放任务。
- 按量计费接入统一财务消费路由。
- 用户端：GPU 市场、租赁详情、我的实例。
- 管理后台：设备管理、租赁管理、设备事件。

### 第二阶段：Token 网关 + Agent / Skills（Week 5–9）

- Agent 模板管理、用户 Agent 创建与定制、定制订单。
- Skills 技能市场、版本管理、用户安装与授权、Agent 绑定 Skill。
- Token 上游供应商管理、模型路由、调用鉴权、用量统计、成本核算。
- 三个模块都挂在统一商品和财务消费路由上，不重写交易链路。

### 不进任何阶段（长期规划）

- 复杂 GPU 自动调度。
- 多区域灾备。
- 分库分表（有专项规划文档）。
- 企业级审批流。

## 3. 权限范围

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

- `platform_admin`：平台管理员。
- `finance_admin`：财务管理员。
- `ops_admin`：运维管理员。
- `app_admin`：应用管理员。
- `normal_user`：普通用户。
- `vip_user`：高级用户。
- `reseller`：渠道商。

权限优先级（由高到低）：

```text
用户显式禁用权限 (deny)
  > 用户显式授权 (allow override)
  > 角色权限
  > 商品 / 会员规则
```

## 4. 推荐技术栈

第一版采用：

```text
Vue3 + Vite + TypeScript + Element Plus
Go + Gin + GORM
MySQL 8
Redis 7
RabbitMQ
MinIO
```

部署：Docker Compose 起步，后续迁移 Kubernetes。

## 5. 系统模块

```text
auth
  邮箱注册、手机号注册、邮箱登录、手机号登录、实名制认证、JWT、Refresh Token、API Key

iam
  用户、角色、权限、动态授权、访问控制、权限缓存

app
  应用业务详情（icon、callback_url、描述等非交易字段）

product
  统一商品、套餐、价格、角色可见性、会员规则、购买入口、开通路由

provision
  业务开通处理器接口、各 product_type 对应处理器

billing
  钱包、订单、支付、退款、流水、余额、乐观锁扣费

finance_consumer
  产品消费事件接收、计费规则匹配、扣费、流水、幂等、对账

asset
  用户资产、产品实例、权益额度、到期时间、资产事件

membership
  会员等级、会员权益、会员价格、会员订阅、到期续费

content
  系统公告、帮助文档、分类、发布、置顶、可见范围

audit
  审计日志、操作记录

identity
  实名制认证、附件、审核流程

-- 第三阶段 --

resource
  GPU 设备、设备分组、租赁、释放、状态同步

-- 第二阶段 --

agent
  Agent 模板、用户 agent、定制订单、版本、发布

skill
  Skills 技能、版本、安装、授权、上下架

token_gateway
  Token 上游供应商、模型路由、调用鉴权、用量统计、成本核算
```

第一阶段用模块化单体实现，代码按模块拆分。`billing`、`token_gateway`、`resource` 三个模块的边界先设计清楚，后续最先拆成独立服务。

### 5.1 分层路由架构

```text
HTTP API 层
  -> Auth / IAM 鉴权层
  -> Product 商品路由层
  -> Order / Billing 交易层
  -> Business Provision 业务开通层
  -> Finance Consumer Router 消费计费层
  -> Resource / App / Agent / Skill / Token 等业务模块
```

分层职责：

- `HTTP API 层`：只处理请求参数、响应格式、版本号、限流。
- `Auth / IAM 鉴权层`：统一处理登录态、API Key、角色、权限。
- `Product 商品路由层`：根据 `product_type` 和 `product_code` 找到商品、套餐、价格和处理器。
- `Order / Billing 交易层`：统一创建订单、扣余额、写流水、退款、对账。
- `Business Provision 业务开通层`：把已支付订单交给对应业务处理器完成开通。
- `Finance Consumer Router 消费计费层`：接收各业务模块产生的消费事件，匹配计费规则并生成扣费流水。
- `业务模块`：只负责自己的业务规则，不直接操作钱包扣费。

### 5.2 应用与商品的边界

**重要约定：`applications` 表只存储应用的业务字段，所有商品交易逻辑统一走 `products` 体系。**

```text
applications（业务详情表）
  只保存：icon、description、callback_url、adapter_config 等非交易字段
  通过 products.business_ref_id 关联

products（统一商品表）
  保存：product_type、price、plan、role_access、billing_rules
  product_type = app 时，business_ref_id 指向 applications.id
```

不单独维护 `application_plans`、`application_prices`、`application_role_access`，这些全部走 `product_plans`、`product_prices`、`product_role_access`，避免双套配置混乱。

### 5.3 应用扩展式接入

每个新应用只需要按标准适配器接入：

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

新增网盘时，只需要：

1. 在 `products` 注册 `product_type = netdisk` 的商品。
2. 在 `product_plans` 配置容量、时长、流量等套餐。
3. 在 `product_prices` 配置不同角色价格。
4. 在 `product_role_access` 配置可见、可购买、可使用权限。
5. 新增 `netdisk` 模块。
6. 注册 `netdisk` 的开通处理器。
7. 实现 `Provision`、`Renew`、`Suspend`、`Cancel`。

订单、钱包、角色权限、财务流水和后台查询不需要重做。

### 5.4 财务按产品消费快速对接

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

不同业务消费对接：

```text
token     -> 按 input_tokens / output_tokens 扣费
gpu       -> 按小时、天、月扣费
netdisk   -> 按容量、流量、用户数、时长扣费
agent     -> 按调用次数、Token、定制服务扣费
skill     -> 按授权周期、调用次数、增值功能扣费
```

### 5.5 会员制售卖预留

会员本身也作为一种商品：

```text
products.product_type = membership
```

会员制支持四种模式：

```text
member_only       仅会员可购买或使用某应用
member_discount   会员购买享受折扣
member_price      会员使用独立价格
member_included   会员权益内包含某应用、额度或功能
```

这样会员购买、续费、退款、流水都走统一订单和钱包链路。某个应用是否采用会员制，只需要配置商品会员规则，不需要重写应用购买流程。

## 6. 核心数据模型

### 6.1 用户与会话

```text
users
- id
- email
- email_verified
- phone
- phone_verified
- nickname
- avatar_url
- real_name_status       -- unverified / pending / verified / rejected
- password_hash
- status                 -- active / disabled
- wallet_id
- created_at
- updated_at

user_sessions
- id
- user_id
- refresh_token_hash     -- HMAC-SHA256(refresh_token, server_secret)，不存明文
- user_agent
- ip
- expires_at
- revoked_at             -- 主动退出或封禁用户时写入
- created_at
```

说明：

- Refresh Token 必须持久化，否则退出登录和封禁用户时无法吊销。
- `refresh_token_hash` 使用 `HMAC-SHA256(token, server_secret)`，`server_secret` 通过环境变量注入，不入库。
- 登录时创建会话记录，退出时写入 `revoked_at`，刷新时校验 `revoked_at` 是否为空。
- 管理员封禁用户时，批量写入该用户所有活跃会话的 `revoked_at`。

### 6.2 验证码与登录日志

```text
verification_codes
- id
- target_type            -- email / phone
- target_value
- code
- scene                  -- register / login / bind / reset_password
- expires_at
- used_at
- created_at

user_login_logs
- id
- user_id
- login_type             -- email / phone
- login_account
- ip
- user_agent
- status                 -- success / failed
- fail_reason
- created_at
```

### 6.3 实名认证

```text
identity_verifications
- id
- user_id
- real_name
- id_card_no_hmac        -- HMAC-SHA256(id_card_no, server_secret)，用于查重
- id_card_no_masked      -- 保留前6后4，中间用 * 替换，用于展示
- verification_type      -- id_card
- provider               -- manual / third_party
- status                 -- pending / verified / rejected
- reject_reason
- submitted_at
- verified_at
- created_at
- updated_at

identity_verification_logs
- id
- verification_id
- user_id
- action
- operator_id
- remark
- created_at
```

说明：

- 身份证号严禁明文存储。中国身份证号格式已知，直接 SHA-256 可被穷举攻击，**必须使用 HMAC-SHA256 加服务端密钥**，密钥通过环境变量管理，不入库、不入配置文件。
- `id_card_no_hmac` 用于查重（同一身份证号是否已实名过其他账号）。
- `id_card_no_masked` 用于管理后台审核展示，保留前 6 位（地区）和后 4 位（出生年 + 序列最后两位），中间全部替换为 `*`。

### 6.4 权限

```text
roles
- id
- code
- name
- description
- created_at
- updated_at

permissions
- id
- code                   -- 格式：resource:action，例如 product:create
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

user_permission_overrides
- id
- user_id
- permission_id
- effect                 -- allow / deny
- reason
- expires_at
- created_by
- created_at
- updated_at

role_change_logs
- id
- user_id
- role_id
- action                 -- grant / revoke
- operator_id
- reason
- created_at

audit_logs
- id
- operator_id
- operator_type          -- user / admin
- module
- action
- target_type
- target_id
- before_json
- after_json
- ip
- user_agent
- created_at
```

说明：

- 每次角色和权限变化都必须写审计日志，并清理用户权限 Redis 缓存。
- 权限缓存 key 建议：`perm:user:{user_id}`，TTL 5 分钟，角色变更时主动删除。
- `user_permission_overrides.effect` 支持 `allow` 和 `deny`，`deny` 优先级高于 `allow`。

### 6.5 统一商品与应用

```text
products
- id
- product_type           -- app / gpu / agent / skill / token / netdisk / membership
- product_code
- name
- description
- status                 -- draft / active / inactive / archived
- business_ref_id        -- 指向 applications.id / gpu_devices.id 等业务表
- created_at
- updated_at

product_plans
- id
- product_id
- plan_code
- name
- billing_type           -- one_time / subscription / pay_as_you_go
- duration_days          -- 订阅型有效天数，按量付费为 0
- quota_json             -- 不同业务套餐参数，例如 {"storage_gb": 100}
- status
- created_at
- updated_at

product_prices
- id
- product_plan_id
- role_id                -- NULL 表示默认价格
- membership_level_id    -- NULL 表示非会员价
- price_amount           -- DECIMAL(18,6)
- currency               -- CNY
- discount_rate          -- 折扣率，1.0 表示无折扣
- effective_from
- effective_to           -- NULL 表示永久有效

product_role_access
- id
- product_id
- role_id
- can_view               -- 是否可见
- can_buy                -- 是否可购买
- can_use                -- 是否可使用

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
- adapter_type           -- internal / external
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
- billing_mode           -- per_unit / tiered
- free_quota             -- 免费额度
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
- idempotency_key        -- 唯一索引，防重复扣费
- created_at

applications
- id
- code
- name
- type
- description
- icon_url
- callback_url
- adapter_config_json    -- 应用特有配置，非交易字段
- status
- created_at
- updated_at
```

说明：

- `applications` 只保存应用业务详情（icon、callback_url、adapter_config），**不单独维护 application_plans、application_prices、application_role_access**，所有价格和权限配置统一走 `product_plans`、`product_prices`、`product_role_access`。
- `product_prices` 同时有 `role_id` 和 `membership_level_id`：两者同时为 NULL 是默认价；只有 `role_id` 是角色价；只有 `membership_level_id` 是会员价；两者都有是特定角色 + 会员价。优先级：会员专属价 > 角色价 > 默认价。
- `product_consumption_records.idempotency_key` 必须建唯一索引。

### 6.6 会员

```text
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
- status                 -- active / expired / cancelled
- started_at
- expires_at
- auto_renew
- created_at
- updated_at

product_membership_rules
- id
- product_id
- membership_level_id
- rule_type              -- member_only / member_discount / member_price / member_included
- discount_rate
- included_quota_json
- status
- created_at
- updated_at
```

### 6.7 订单与钱包

```text
wallets
- id
- user_id
- balance_amount         -- DECIMAL(18,6)
- frozen_amount          -- DECIMAL(18,6)
- currency               -- CNY
- version                -- 乐观锁版本号
- created_at
- updated_at

wallet_transactions
- id
- wallet_id
- user_id
- type                   -- recharge / consume / refund / freeze / unfreeze
- direction              -- in / out
- amount                 -- DECIMAL(18,6)
- balance_after          -- DECIMAL(18,6)，记录交易后余额快照
- related_order_id
- remark
- created_at

orders
- id
- order_no               -- 唯一，格式例如 ORD20260604XXXXXXXX
- user_id
- order_type             -- purchase / recharge / refund
- status                 -- pending / paid / cancelled / refunded / failed
- amount                 -- DECIMAL(18,6)
- currency
- paid_at
- cancelled_at
- refund_amount          -- DECIMAL(18,6)
- refunded_at
- created_at
- updated_at

order_items
- id
- order_id
- item_type
- item_id
- item_name
- quantity
- unit_price             -- DECIMAL(18,6)
- total_price            -- DECIMAL(18,6)

payment_callbacks
- id
- order_id
- provider               -- wechat / alipay
- provider_trade_no      -- 第三方支付流水号
- notify_body            -- 原始回调报文（加密存储）
- status                 -- received / processed / ignored
- processed_at
- created_at
```

说明：

- 所有充值、消费、退款都必须写入 `wallet_transactions`。
- 钱包扣费必须使用事务和乐观锁（`SELECT ... WHERE version = ? FOR UPDATE` 后 `UPDATE ... SET version = version + 1`）。
- `balance_amount` 只是当前余额快照，不是唯一账务依据；真实余额可从 `wallet_transactions` 累计重建。
- 充值订单依赖第三方支付回调完成，`payment_callbacks` 记录每次回调原始报文，支持幂等重放。
- 第三方支付 notify URL 必须独立存在，不依赖前端跳转，详见 API 设计。

### 6.8 用户资产与权益

```text
user_assets
- id
- user_id
- asset_type             -- app_access / gpu_instance / agent_instance / skill_license / token_quota / netdisk_instance / membership
- product_id
- product_plan_id
- source_order_id
- business_instance_id
- status                 -- active / expired / frozen / cancelled
- started_at
- expires_at
- created_at
- updated_at

user_entitlements
- id
- user_id
- asset_id
- entitlement_type
- product_id
- quota_total            -- DECIMAL(18,6)
- quota_used             -- DECIMAL(18,6)
- quota_unit             -- tokens / gb / requests / ...
- status
- started_at
- expires_at
- created_at
- updated_at

asset_events
- id
- asset_id
- user_id
- event_type             -- created / activated / suspended / expired / cancelled / renewed
- before_status
- after_status
- operator_id
- remark
- created_at
```

说明：

- 会员制应用、按量付费应用和普通购买应用都应该生成 `user_assets`。
- 如果产品带额度（Token、网盘容量、Agent 调用次数），则同时生成 `user_entitlements`。

### 6.9 内容管理

```text
announcements
- id
- title
- content
- type                   -- notice / maintenance / promotion
- priority
- status                 -- draft / published / offline
- visible_scope          -- all / roles / members / admins
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
- status                 -- draft / published / offline
- sort_order
- view_count
- created_by
- published_at
- created_at
- updated_at
```

### 6.10 GPU 设备租赁（第三阶段）

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
- status                 -- available / reserved / deploying / running / expired / releasing / maintenance / fault / offline
- price_per_hour         -- DECIMAL(18,6)
- price_per_day          -- DECIMAL(18,6)
- created_at
- updated_at

gpu_rentals
- id
- rental_no
- user_id
- device_id
- order_id
- status                 -- pending / active / releasing / released / cancelled
- start_at
- end_at
- actual_release_at
- billing_mode           -- hourly / daily / monthly
- total_amount           -- DECIMAL(18,6)
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

### 6.11 Agent 定制市场（第二阶段）

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

user_agents
- id
- user_id
- template_id
- name
- description
- system_prompt
- model_id
- status                 -- draft / active / suspended
- version
- created_at
- updated_at

agent_customization_orders
- id
- order_no
- user_id
- agent_template_id
- requirement
- status                 -- pending / quoted / paid / in_progress / delivered / accepted / cancelled
- quoted_amount          -- DECIMAL(18,6)
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
- cost_amount            -- DECIMAL(18,6)
- created_at
```

### 6.12 Skills 技能市场（第二阶段）

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

### 6.13 Token 上游聚合网关（第二阶段）

```text
token_providers
- id
- code
- name
- base_url
- auth_type              -- api_key / oauth
- api_key_encrypted      -- AES-256-GCM 加密，密钥通过环境变量管理
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
- input_price_per_1k     -- DECIMAL(18,6)，上游成本价
- output_price_per_1k    -- DECIMAL(18,6)，上游成本价
- sale_input_price_per_1k  -- DECIMAL(18,6)，用户售价
- sale_output_price_per_1k -- DECIMAL(18,6)，用户售价
- status
- created_at
- updated_at

token_model_routes
- id
- logical_model_code     -- 对外暴露的逻辑模型名，例如 gpt-4o
- provider_model_id
- weight                 -- 负载均衡权重
- priority               -- 同等 weight 时按 priority 排序
- status
- created_at
- updated_at

token_usage_logs
- id
- request_id             -- 全局唯一请求 ID
- user_id
- provider_id
- model_id
- logical_model_code
- input_tokens
- output_tokens
- total_tokens
- provider_cost_amount   -- DECIMAL(18,6)，实际上游成本
- sale_amount            -- DECIMAL(18,6)，向用户收取的金额
- latency_ms
- is_stream              -- 是否流式请求
- status                 -- success / failed / timeout
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

说明：

- `api_key_encrypted` 使用 **AES-256-GCM** 加密存储，加密密钥通过环境变量 `TOKEN_PROVIDER_KEY` 注入，不入库、不入配置文件、不入代码仓库。
- 密钥轮换时需要重新加密所有 `api_key_encrypted` 字段，建议记录密钥版本号（`key_version`）字段便于轮换迁移。
- Token 网关调用上游时需实现断路器（失败率超阈值时切换路由），`token_model_routes` 的 `priority` 用于 fallback 顺序。
- 流式请求（`is_stream = true`）需特别确认中间件不缓冲 response body，否则 SSE 流式响应会失效。

## 7. 核心业务流程

### 7.1 统一商品购买与开通流程

```text
用户选择商品
  -> Product Router 根据 product_type 查询商品和套餐
  -> IAM 检查角色可见、可购买权限
  -> Pricing 读取角色 / 会员对应价格
  -> Order 创建统一订单（状态：pending）
  -> Billing 检查余额并扣费（乐观锁事务）
  -> 写入 wallet_transactions
  -> 更新 Order 状态为 paid
  -> Provision Router 根据 product_type 调用业务处理器
  -> 业务模块开通实例或授权
  -> 生成 user_assets / user_entitlements
  -> 返回购买结果
```

### 7.2 充值与支付回调流程

```text
用户发起充值
  -> 创建充值订单（status = pending）
  -> 生成第三方支付跳转链接或二维码
  -> 用户完成支付
  -> 第三方支付平台异步回调 POST /api/payments/notify/:provider
  -> 校验签名
  -> 校验幂等（provider_trade_no 是否已处理）
  -> 写入 payment_callbacks
  -> 开启数据库事务
    -> 更新 Order 状态为 paid
    -> 钱包余额加款
    -> 写入 wallet_transactions（type = recharge, direction = in）
  -> 提交事务
  -> 返回第三方支付平台成功响应
```

说明：

- 前端跳转回调（return_url）仅用于展示，不作为充值完成的依据。
- 支付回调必须幂等，同一 `provider_trade_no` 收到多次回调只处理一次。
- 回调接口无需登录态，但必须校验签名。

### 7.3 财务扣费流程

```text
开启数据库事务
  -> SELECT wallet WHERE user_id = ? FOR UPDATE
  -> 校验余额是否 >= 扣款金额
  -> UPDATE wallet SET balance_amount = balance_amount - ?, version = version + 1 WHERE id = ? AND version = ?
  -> 如果 UPDATE 影响行数 = 0，说明并发冲突，重试或返回错误
  -> 写入 wallet_transactions
  -> 更新订单状态
提交事务
```

### 7.4 产品消费计费流程

```text
业务模块产生消费事件
  -> 上报 ProductUsageEvent（含 idempotency_key）
  -> Finance Consumer Router 校验 idempotency_key（查 product_consumption_records）
  -> 如果已存在，直接返回原结果（幂等）
  -> 根据 product_id、plan_id、usage_type 匹配 product_billing_rules
  -> 计算消费金额
  -> 开启数据库事务
    -> 扣减钱包余额或扣减 user_entitlements.quota_used
    -> 写入 wallet_transactions
    -> 写入 product_consumption_records
  -> 提交事务
  -> 返回计费结果
```

### 7.5 会员购买与应用售卖流程

```text
用户购买会员商品
  -> Product Router 识别 product_type = membership
  -> 创建订单，Billing 扣费，写入钱包流水
  -> Provision Router 开通 user_memberships
  -> 根据 membership_benefits 生效权益

用户选择会员制应用
  -> IAM 检查角色权限
  -> Membership 检查用户会员状态
  -> Product Pricing 匹配会员专属价、折扣或内含规则
  -> 如果 member_included 且额度充足，扣减权益额度，直接开通
  -> 如果需要支付，走统一订单和钱包扣费
  -> Provision Router 开通应用权限
```

### 7.6 Token 网关调用流程（第二阶段）

```text
用户或 agent 发起模型调用
  -> 校验 API Key / 登录态
  -> 校验角色和模型调用权限
  -> 校验余额或 Token 额度
  -> 根据 logical_model_code 和路由权重选择上游供应商
  -> 如果上游不可用（断路器熔断），按 priority 切换到备用路由
  -> 请求上游模型（流式或非流式）
  -> 记录 token_usage_logs（request_id、tokens、延迟、状态）
  -> 计算成本和销售金额
  -> 上报 ProductUsageEvent → Finance Consumer Router 扣费
  -> 返回模型结果（流式接口直接透传，不缓冲 response body）
```

## 8. API 设计摘要

### 8.1 中间件

所有请求经过：

```text
RequestID -> Logger -> Recovery -> RateLimit -> Auth（非公开接口）-> Permission（需权限接口）
```

限流策略建议：

```text
全局：1000 req/s per IP
认证接口（注册、登录、验证码）：10 req/min per IP
支付回调接口：不限流（但需签名校验）
Token 网关：按用户级别限流，在 token_quota_accounts 中维护月度限额
```

### 8.2 用户端接口

```text
-- 认证 --
POST /api/auth/verification-codes/email
POST /api/auth/verification-codes/phone
POST /api/auth/register/email
POST /api/auth/register/phone
POST /api/auth/login/email
POST /api/auth/login/phone
POST /api/auth/logout
POST /api/auth/refresh
GET  /api/me
PATCH /api/me/profile
PATCH /api/me/password

-- 实名 --
POST /api/identity/verifications
GET  /api/identity/verifications/latest

-- 钱包与充值 --
GET  /api/wallet
GET  /api/wallet/transactions
POST /api/recharge/orders
GET  /api/product-consumption-records

-- 商品与资产 --
GET  /api/products
GET  /api/products/:id
GET  /api/products/:id/plans
POST /api/products/:id/purchase
GET  /api/my/products
GET  /api/my/assets
GET  /api/my/assets/:id
GET  /api/my/entitlements

-- 会员 --
GET  /api/memberships
GET  /api/my/membership
POST /api/memberships/:id/purchase

-- 内容 --
GET  /api/announcements
GET  /api/help/categories
GET  /api/help/articles
GET  /api/help/articles/:id

-- 支付回调（无需登录，签名校验） --
POST /api/payments/notify/:provider

-- GPU（第三阶段） --
GET  /api/gpu/devices
GET  /api/gpu/devices/:id
POST /api/gpu/rentals
GET  /api/gpu/rentals
GET  /api/gpu/rentals/:id

-- Agent（第二阶段） --
GET  /api/agents/templates
GET  /api/agents/templates/:id
POST /api/agents/customization-orders
GET  /api/my/agents
POST /api/my/agents
PATCH /api/my/agents/:id

-- Skills（第二阶段） --
GET  /api/skills
GET  /api/skills/:id
POST /api/skills/:id/purchase
POST /api/my/agents/:id/skills

-- Token 网关（第二阶段） --
GET  /api/token/models
POST /api/token/chat/completions
GET  /api/token/usage
```

### 8.3 管理后台接口

```text
-- 用户与权限 --
GET    /api/admin/users
POST   /api/admin/users
GET    /api/admin/users/:id
PATCH  /api/admin/users/:id
PATCH  /api/admin/users/:id/status
GET    /api/admin/users/:id/roles
PATCH  /api/admin/users/:id/roles
GET    /api/admin/users/:id/permission-overrides
PATCH  /api/admin/users/:id/permission-overrides
GET    /api/admin/users/:id/assets
GET    /api/admin/users/:id/entitlements
GET    /api/admin/users/:id/login-logs
GET    /api/admin/identity-verifications
GET    /api/admin/identity-verifications/:id
PATCH  /api/admin/identity-verifications/:id/review
GET    /api/admin/roles
POST   /api/admin/roles
PATCH  /api/admin/roles/:id
DELETE /api/admin/roles/:id
PATCH  /api/admin/roles/:id/permissions
GET    /api/admin/permissions
POST   /api/admin/permissions
GET    /api/admin/audit-logs

-- 商品与计费 --
GET    /api/admin/products
POST   /api/admin/products
GET    /api/admin/products/:id
PATCH  /api/admin/products/:id
POST   /api/admin/products/:id/plans
PATCH  /api/admin/products/:id/plans/:plan_id
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

-- 会员 --
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
GET    /api/admin/user-assets
GET    /api/admin/user-entitlements
GET    /api/admin/asset-events

-- 订单与财务 --
GET    /api/admin/orders
GET    /api/admin/wallet-transactions
GET    /api/admin/payment-callbacks

-- 应用 --
GET    /api/admin/apps
POST   /api/admin/apps
PATCH  /api/admin/apps/:id

-- 内容 --
GET    /api/admin/announcements
POST   /api/admin/announcements
PATCH  /api/admin/announcements/:id
GET    /api/admin/help/categories
POST   /api/admin/help/categories
PATCH  /api/admin/help/categories/:id
GET    /api/admin/help/articles
POST   /api/admin/help/articles
PATCH  /api/admin/help/articles/:id

-- GPU（第三阶段） --
GET    /api/admin/gpu/devices
POST   /api/admin/gpu/devices
PATCH  /api/admin/gpu/devices/:id
GET    /api/admin/gpu/rentals

-- Agent（第二阶段） --
GET    /api/admin/agent-templates
POST   /api/admin/agent-templates
PATCH  /api/admin/agent-templates/:id
GET    /api/admin/agent-customization-orders
PATCH  /api/admin/agent-customization-orders/:id

-- Skills（第二阶段） --
GET    /api/admin/skills
POST   /api/admin/skills
PATCH  /api/admin/skills/:id
POST   /api/admin/skills/:id/versions

-- Token 网关（第二阶段） --
GET    /api/admin/token/providers
POST   /api/admin/token/providers
PATCH  /api/admin/token/providers/:id
GET    /api/admin/token/models
POST   /api/admin/token/models
PATCH  /api/admin/token/models/:id
GET    /api/admin/token/routes
POST   /api/admin/token/routes
PATCH  /api/admin/token/routes/:id
GET    /api/admin/token/usage
```

## 9. 开发计划

### 第一阶段：平台底座 + 应用售卖（Week 1–4）

#### Week 1：基础工程 + 认证

- 建立前后端工程。
- 建立数据库 migration（核心表：users、user_sessions、verification_codes、roles、permissions、user_roles、role_permissions、wallets）。
- 邮箱注册、手机号注册、邮箱登录、手机号登录。
- 邮箱验证码、短信验证码。
- JWT Access Token + Refresh Token（user_sessions 持久化）。
- 退出登录（吊销 refresh token）、刷新令牌。
- 实名制认证提交和审核流程。
- 用户动态授权和权限缓存失效机制。
- 后台基础布局（登录页、菜单骨架）。

#### Week 2：商品、权限与应用

- 完成 RBAC（角色、权限、配置）。
- 完成统一商品模型（products、product_plans、product_prices、product_role_access）。
- 完成商品购买路由和业务开通处理器接口。
- 完成会员等级、会员权益和商品会员规则模型。
- 完成应用适配器注册接口。
- 完成应用 CRUD（applications 表，仅业务详情字段）。
- 用户端应用市场骨架。

#### Week 3：订单、钱包与资产

- 完成钱包（乐观锁扣费、充值、流水）。
- 完成充值订单和支付回调接口（`POST /api/payments/notify/:provider`）。
- 完成消费订单、钱包流水。
- 完成统一商品购买。
- 完成产品消费事件接入和计费规则。
- 完成会员商品购买和会员权益校验。
- 完成用户资产和权益生成。
- 完成应用购买。
- 完成基础对账查询。

#### Week 4：内容、会员与完善

- 完成系统公告和帮助文档。
- 完成用户端会员中心页面。
- 完善管理后台（订单管理、财务流水、用户资产管理）。
- 完成第一个完整应用售卖闭环测试。

### 第三阶段：GPU 服务器出售（Week 10–12）

- GPU 设备管理、状态机。
- 租赁订单（按量计费接入统一财务消费路由）。
- 到期释放定时任务。
- 用户端租赁页面和我的实例。
- 管理后台设备和租赁管理。

### 第二阶段：Token 网关 + Agent / Skills（Week 5–9）

- Agent 模板管理、用户 Agent 创建和定制订单。
- Skills 管理、版本、购买、安装、Agent 绑定。
- Token 供应商管理、模型路由、断路器、流式调用、用量统计。

### 测试与上线（Week 13–14）

- 接口测试、权限测试。
- 账务测试、并发扣费测试（重点：乐观锁冲突、幂等重试）。
- 会员购买、续费、权益校验测试。
- 支付回调幂等测试。
- 设备状态流转测试。
- Token 网关路由、限流、扣费测试。
- Docker 部署。
- 灰度上线。

## 10. AI 开发方式

适合交给 AI：

- 生成 CRUD（migration、model、repository、service、handler、route）。
- 生成管理后台表格和表单页面。
- 生成权限校验中间件。
- 生成 OpenAI 兼容接口适配层。
- 生成单元测试、接口测试。
- 生成部署脚本。

必须人工把关：

- 钱包扣费事务和乐观锁。
- 订单状态机。
- 商品路由抽象。
- 业务开通处理器幂等性。
- 会员权益和商品价格优先级。
- 动态权限生效顺序和缓存失效。
- 用户资产、权益额度和财务流水一致性。
- 支付回调签名校验和幂等处理。
- GPU 设备状态机。
- Agent 交付和版本管理。
- Skills 包安全审核。
- Token 上游密钥加密、路由和断路器。
- Token 成本核算和扣费一致性。
- 数据库索引设计。
- 高并发容量设计。
- 安全策略（HMAC、AES-GCM、限流）。

## 11. 团队与时间

最低配置：

- 1 名后端。
- 1 名前端。
- 1 名产品/测试。
- 1 名运维兼职。

使用 AI 辅助，第一阶段平台底座 + 应用售卖预计：

```text
4 周
```

完整三阶段（含 GPU + Agent/Skills/Token 网关）：

```text
13 到 15 周
```

商业试运营稳定版本（含完整测试、安全加固）：

```text
16 到 20 周
```

## 12. 下一步

建议下一步直接开始：

```text
web/admin-console
web/user-console
server/internal/modules/auth
server/internal/modules/iam
server/internal/modules/identity
server/internal/modules/product
server/internal/modules/provision
server/internal/modules/billing
server/internal/modules/finance_consumer
server/internal/modules/asset
server/internal/modules/membership
server/internal/modules/content
server/internal/modules/audit
server/migrations
infra/docker-compose.yml
```

第一批优先实现（按顺序）：

1. 登录和用户管理（auth + iam）。
2. 角色和权限管理（iam）。
3. 统一商品中心和商品路由（product）。
4. 钱包和订单（billing）。
5. 充值和支付回调（billing）。
6. 产品消费事件和计费规则（finance_consumer）。
7. 用户资产和权益额度（asset）。
8. 应用管理和应用售卖闭环（app + provision）。
