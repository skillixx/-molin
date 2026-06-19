# 数据库表设计

## 1. 数据库基础约定

数据库使用 MySQL 8。

基础约定：

- 字符集：`utf8mb4`
- 排序规则：`utf8mb4_0900_ai_ci`
- 金额字段：`DECIMAL(18,6)`
- 时间字段：`DATETIME`
- 主键：`BIGINT UNSIGNED AUTO_INCREMENT`
- 状态字段统一使用 `status`
- JSON 数据使用 `JSON` 类型

## 2. 安全约定

在实现任何表结构之前，必须确认以下安全约定：

**身份证号**

- 严禁明文存储。
- 严禁使用 SHA-256 或 MD5 直接 hash（身份证号格式已知，可被穷举）。
- 必须使用 `HMAC-SHA256(id_card_no, server_secret)`，字段名为 `id_card_no_hmac`。
- `server_secret` 通过环境变量 `ID_CARD_HMAC_SECRET` 注入，不入库、不入配置文件、不入代码仓库。
- 同时保存 `id_card_no_masked`（保留前6后4，中间替换为 `*`），用于管理后台展示。

**Refresh Token**

- 必须持久化到 `user_sessions` 表，否则退出登录和封禁用户时无法吊销。
- 数据库只存储 `HMAC-SHA256(refresh_token, server_secret)`，不存明文。
- 密钥通过环境变量 `REFRESH_TOKEN_SECRET` 注入。

**Token 供应商 API Key**

- 使用 `AES-256-GCM` 加密存储，字段名为 `api_key_encrypted`。
- 加密密钥通过环境变量 `TOKEN_PROVIDER_KEY` 注入。
- 建议在表中增加 `key_version` 字段，便于密钥轮换迁移。

**支付回调报文**

- `payment_callbacks.notify_body` 存储原始回调报文，建议加密存储（同上 AES-256-GCM）。
- 用于审计和幂等重放，不能随意清理。

## 3. 表分组

### 3.1 账号、会话、实名、权限（第一阶段）

- `users`
- `user_sessions`
- `verification_codes`
- `user_login_logs`
- `identity_verifications`
- `identity_verification_logs`
- `roles`
- `permissions`
- `user_roles`
- `role_permissions`
- `user_permission_overrides`
- `role_change_logs`
- `audit_logs`

### 3.2 商品、订单、钱包、计费（第一阶段）

- `products`
- `product_plans`
- `product_prices`
- `product_role_access`
- `product_provision_handlers`
- `product_billing_rules`
- `product_consumption_records`
- `orders`
- `order_items`
- `payment_callbacks`
- `wallets`
- `wallet_transactions`

### 3.3 用户资产、会员、应用、内容（第一阶段）

- `user_assets`
- `user_entitlements`
- `asset_events`
- `membership_levels`
- `membership_benefits`
- `user_memberships`
- `product_membership_rules`
- `applications`
- `application_adapters`
- `announcements`
- `help_categories`
- `help_articles`

> **注意：不单独维护 `application_plans`、`application_prices`、`application_role_access`**。  
> 应用的套餐、价格和角色权限统一走 `product_plans`、`product_prices`、`product_role_access`，通过 `products.business_ref_id = applications.id` 关联。

### 3.4 GPU（第三阶段）

- `gpu_devices`
- `gpu_rentals`
- `gpu_device_events`

### 3.5 Token 网关、Agent、Skills（第二阶段）

- `agent_templates`
- `user_agents`
- `agent_customization_orders`
- `agent_usage_logs`
- `skills`
- `skill_versions`
- `user_skill_installs`
- `agent_skill_bindings`
- `token_providers`
- `token_models`
- `token_model_routes`
- `token_usage_logs`
- `token_quota_accounts`

## 4. 关键状态

用户状态：

```text
active
disabled
```

实名状态：

```text
unverified
pending
verified
rejected
```

订单状态：

```text
pending
paid
cancelled
refunded
failed
```

资产状态：

```text
active
expired
frozen
cancelled
```

商品状态：

```text
draft
active
inactive
archived
```

GPU 设备状态：

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

GPU 租赁状态：

```text
pending
active
releasing
released
cancelled
```

## 5. 关键索引约定

以下字段必须建索引：

| 表 | 字段 | 索引类型 | 原因 |
|---|---|---|---|
| users | email | UNIQUE | 登录唯一标识 |
| users | phone | UNIQUE | 登录唯一标识 |
| user_sessions | user_id | INDEX | 按用户查会话 |
| user_sessions | refresh_token_hash | UNIQUE | 刷新令牌校验 |
| verification_codes | target_type, target_value, scene | INDEX | 验证码查询 |
| identity_verifications | user_id | INDEX | 按用户查实名 |
| identity_verifications | id_card_no_hmac | INDEX | 查重校验 |
| user_roles | user_id | INDEX | 权限查询 |
| role_permissions | role_id | INDEX | 权限查询 |
| products | product_type, status | INDEX | 商品列表 |
| product_plans | product_id | INDEX | 套餐查询 |
| product_prices | product_plan_id, role_id | INDEX | 价格查询 |
| product_consumption_records | idempotency_key | UNIQUE | 防重复扣费 |
| product_consumption_records | user_id, product_id, created_at | INDEX | 消费记录查询 |
| orders | order_no | UNIQUE | 订单号唯一 |
| orders | user_id, status, created_at | INDEX | 用户订单查询 |
| wallet_transactions | wallet_id, created_at | INDEX | 流水查询 |
| wallet_transactions | user_id, created_at | INDEX | 流水查询 |
| payment_callbacks | provider, provider_trade_no | UNIQUE | 支付回调幂等 |
| user_assets | user_id, asset_type, status | INDEX | 资产查询 |
| user_entitlements | user_id, product_id, status | INDEX | 权益查询 |
| audit_logs | operator_id, module, created_at | INDEX | 审计查询 |
| gpu_device_events | device_id, created_at | INDEX | 设备事件查询 |
| token_usage_logs | user_id, created_at | INDEX | 用量查询 |
| token_usage_logs | request_id | UNIQUE | 请求去重 |

## 6. 大表增长预警与处理策略

第一阶段不做分库分表，但需预留字段和索引，便于后期扩展。

增长最快的表（按风险排序）：

```text
1. product_consumption_records    -- Token 调用、GPU 按量、agent 调用量会很大
2. token_usage_logs               -- 每次模型调用写一条
3. wallet_transactions            -- 每次充值/消费/退款写一条
4. audit_logs                     -- 后台所有敏感写操作
5. user_login_logs                -- 每次登录写一条
6. gpu_device_events              -- 5 万设备频繁上报状态
7. asset_events                   -- 资产状态变更
8. orders                         -- 随用户增长
```

处理策略见 [数据量和分库分表规划](data-scale-sharding-plan.md)。

## 7. 建表脚本

建表脚本保存为 migration 文件：

```text
server/migrations/000001_create_core_tables.up.sql
```

使用方式：

```bash
chmod +x scripts/create_mysql_tables.sh
./scripts/create_mysql_tables.sh
```

默认读取环境变量：

```text
MYSQL_HOST
MYSQL_PORT
MYSQL_DATABASE
MYSQL_USER
MYSQL_PASSWORD
```

核心表建表顺序（注意外键依赖）：

```text
1. users
2. wallets（依赖 users）
3. user_sessions（依赖 users）
4. verification_codes
5. user_login_logs（依赖 users）
6. identity_verifications（依赖 users）
7. identity_verification_logs（依赖 identity_verifications）
8. roles
9. permissions
10. user_roles（依赖 users、roles）
11. role_permissions（依赖 roles、permissions）
12. user_permission_overrides（依赖 users、permissions）
13. role_change_logs（依赖 users、roles）
14. audit_logs
15. applications
16. products（依赖 applications 可选）
17. product_plans（依赖 products）
18. product_prices（依赖 product_plans）
19. product_role_access（依赖 products、roles）
20. product_provision_handlers
21. application_adapters（依赖 applications）
22. product_billing_rules（依赖 products）
23. orders（依赖 users）
24. order_items（依赖 orders）
25. payment_callbacks（依赖 orders）
26. wallet_transactions（依赖 wallets、orders）
27. product_consumption_records（依赖 wallet_transactions）
28. membership_levels
29. membership_benefits（依赖 membership_levels）
30. user_memberships（依赖 users、membership_levels）
31. product_membership_rules（依赖 products、membership_levels）
32. user_assets（依赖 users、products）
33. user_entitlements（依赖 users、user_assets）
34. asset_events（依赖 user_assets）
35. announcements
36. help_categories
37. help_articles（依赖 help_categories）
```
