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

## 2. 表分组

### 2.1 账号、实名、权限

- `users`
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

### 2.2 商品、订单、钱包、计费

- `products`
- `product_plans`
- `product_prices`
- `product_role_access`
- `product_provision_handlers`
- `product_billing_rules`
- `product_consumption_records`
- `orders`
- `order_items`
- `wallets`
- `wallet_transactions`

### 2.3 用户资产、会员、应用、内容

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

### 2.4 后续扩展

- `gpu_devices`
- `gpu_rentals`
- `gpu_device_events`
- `agent_templates`
- `user_agents`
- `agent_customization_orders`
- `skills`
- `skill_versions`
- `user_skill_installs`
- `token_providers`
- `token_models`
- `token_usage_logs`

## 3. 关键状态

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

## 4. 建表脚本

建表脚本保存为：

```text
scripts/create_mysql_tables.sh
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

如果没有设置环境变量，会使用本地开发默认值。
