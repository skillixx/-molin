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

**短信验证码与手机号**

- 验证码明文只允许在生成后到供应商调用前短暂存在，数据库只保存验证码哈希，日志和审计均不得记录明文。
- 短信发送记录不得保存完整手机号，只保存 `phone_masked` 和 `HMAC-SHA256(normalized_phone, SMS_PHONE_HMAC_SECRET)`。
- `SMS_PHONE_HMAC_SECRET` 只能通过环境变量或密钥管理服务注入，不得与 AccessKey Secret 共用，也不得进入数据库和代码仓库。
- 阿里云 AccessKey、请求签名原文和完整供应商响应不得写入任何业务表。

## 3. 表分组

### 3.1 账号、会话、实名、权限（第一阶段）

- `users`
- `user_sessions`
- `verification_codes`
- `sms_templates`
- `sms_scene_bindings`
- `sms_send_logs`
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

#### 3.1.1 阿里云短信与模板管理表设计（阶段 1，migration `000058`）

> 本节记录阶段 1 已实现的最小结构。模板同步、管理端测试发送幂等字段和管理查询复合索引属于阶段 2，届时通过后续 migration 增量扩展。首期只支持 `provider=aliyun`。

`verification_codes` 需要在现有字段基础上补充发送状态，使短信供应商提交失败的验证码不可被校验：

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| code | CHAR(64) | NOT NULL | SHA-256 十六进制验证码哈希；替换现有不足 64 位的字段定义 |
| send_status | VARCHAR(32) | NOT NULL，默认 `not_applicable` | `not_applicable` / `pending` / `sent` / `failed` |
| sent_at | DATETIME | NULL | 手机短信被阿里云受理的时间；邮箱保持 NULL |
| provider | VARCHAR(32) | NULL | 手机短信供应商，邮箱保持 NULL |
| provider_request_id | VARCHAR(128) | NULL | 清洗后的供应商请求标识 |
| business_request_id | VARCHAR(64) | NULL, UNIQUE | 平台生成的短信业务请求标识 |

验证码校验必须按 `target_type` 区分：手机号要求 `send_status='sent'`，邮箱保持现有校验链路并使用 `send_status='not_applicable'`；两者都必须满足 `used_at IS NULL`、`expires_at > NOW()` 和验证码哈希匹配。验证码仍保持 10 分钟过期、单次原子消费。

迁移目标明确如下：先停止新手机号发码并等待现有 10 分钟 OTP 窗口耗尽；把 `code` 扩为 `CHAR(64)`；新增 `send_status NOT NULL DEFAULT 'not_applicable'`。历史邮箱和手机号记录均保持 `not_applicable`，但历史手机号记录不得通过新校验；新手机号记录显式从 `pending` 流转为 `sent/failed`，新邮箱记录保持 `not_applicable`。禁止把历史手机号记录笼统回填为 `sent`。

`sms_templates`：阿里云模板的本地只读快照及本地启停状态。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| id | BIGINT UNSIGNED | PK, AUTO_INCREMENT | 本地主键 |
| provider | VARCHAR(32) | NOT NULL | 首期固定 `aliyun` |
| template_code | VARCHAR(64) | NOT NULL | 阿里云模板编码 |
| template_name | VARCHAR(128) | NOT NULL | 模板名称 |
| provider_audit_status | VARCHAR(32) | NOT NULL | `pending` / `approved` / `rejected` |
| content | TEXT | NOT NULL | 阿里云返回的模板正文，只能由同步更新；发送前必须包含 `${code}` |
| local_enabled | TINYINT(1) | NOT NULL DEFAULT 0 | 本地启用状态；未审核通过时必须为 0 |
| version | BIGINT UNSIGNED | NOT NULL DEFAULT 1 | 本地启停乐观锁版本 |
| last_synced_at | DATETIME | NOT NULL | 最近同步时间 |
| created_at | DATETIME | NOT NULL | 创建时间 |
| updated_at | DATETIME | NOT NULL | 更新时间 |

唯一约束：`UNIQUE(provider, template_code)`。阿里云正文、模板编码和审核状态只允许后续同步服务写入，阶段 1 不提供管理写接口。

`sms_scene_bindings`：五个固定业务场景的当前模板绑定。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| id | BIGINT UNSIGNED | PK, AUTO_INCREMENT | 本地主键 |
| scene | VARCHAR(32) | NOT NULL, UNIQUE | `register` / `login` / `reset_password` / `bind_phone` / `admin_verify` |
| template_id | BIGINT UNSIGNED | NOT NULL, FK | 关联 `sms_templates.id`，删除受限 |
| sign_name | VARCHAR(128) | NOT NULL | 已审核通过的短信签名名称，不是密钥 |
| enabled | TINYINT(1) | NOT NULL DEFAULT 0 | 是否允许该场景提交短信 |
| version | BIGINT UNSIGNED | NOT NULL DEFAULT 1 | 更新绑定的乐观锁版本 |
| updated_by | BIGINT UNSIGNED | NULL | 最后更新管理员 ID；阶段 1 fixture 可为空 |
| created_at | DATETIME | NOT NULL | 创建时间 |
| updated_at | DATETIME | NOT NULL | 更新时间 |

同一场景只能存在一条当前绑定。`sign_name` 只能由后端固定配置 `SMS_ALIYUN_SIGN_NAME` 写入，管理请求不得自由提交。启用绑定前必须在事务内确认模板 `provider_audit_status='approved'`、`local_enabled=1` 且包含 `code` 变量；绑定修改必须写审计日志。

`sms_send_logs`：短信提交日志，仅表达平台向供应商的提交结果，不表达运营商送达状态。

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| id | BIGINT UNSIGNED | PK, AUTO_INCREMENT | 本地主键 |
| scene | VARCHAR(32) | NOT NULL | 业务场景 |
| phone_masked | VARCHAR(32) | NOT NULL | 仅保存前 3 后 4 的脱敏结果 |
| phone_hmac | CHAR(64) | NOT NULL | 使用独立密钥计算的手机号 HMAC，不通过 API 返回 |
| template_id | BIGINT UNSIGNED | NULL, FK | 本地模板 ID；模板删除时置 NULL |
| template_code | VARCHAR(64) | NOT NULL | 提交时模板编码快照 |
| sign_name | VARCHAR(128) | NOT NULL | 提交时签名快照 |
| provider | VARCHAR(32) | NOT NULL | 首期固定 `aliyun` |
| business_request_id | VARCHAR(64) | NOT NULL, UNIQUE | 平台生成的请求标识 |
| provider_request_id | VARCHAR(128) | NULL | 阿里云 RequestId |
| provider_code | VARCHAR(64) | NULL | 阿里云清洗后的返回码 |
| submit_status | VARCHAR(32) | NOT NULL | `pending` / `accepted` / `failed` |
| failure_summary | VARCHAR(255) | NULL | 清洗后的失败摘要，不保存原始响应 |
| created_at | DATETIME | NOT NULL | 提交创建时间 |

`accepted` 仅表示阿里云受理，不代表运营商送达。发送日志保留周期和归档清理阈值由运维与安全评审确认，清理任务不得删除审计日志。

阶段 1 使用 `UNIQUE(business_request_id)` 防止同一业务请求重复落日志。管理端测试发送所需的 `operator_id`、`idempotency_key`、请求摘要、延迟和筛选复合索引在阶段 2 增量加入；`provider_request_id/provider_code/failure_summary` 均可为空，`last_synced_at` 在模板快照创建后不可为空。

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

短信模板审核状态：

```text
pending
approved
rejected
disabled
```

短信提交状态：

```text
pending
accepted
failed
```

验证码发送状态：

```text
not_applicable
pending
sent
failed
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
| verification_codes | target_type, send_status, expires_at | INDEX | 手机验证码发送状态与过期筛选 |
| verification_codes | business_request_id | UNIQUE | 短信业务请求追踪 |
| sms_templates | provider, template_code | UNIQUE | 阿里云模板同步幂等 |
| sms_templates | provider_audit_status / local_enabled | INDEX | 阶段 1 可用快照校验 |
| sms_scene_bindings | scene | UNIQUE | 每个场景最多一个当前绑定 |
| sms_scene_bindings | template_id / enabled | INDEX | 模板引用和有效绑定查询 |
| sms_send_logs | business_request_id | UNIQUE | 发送请求追踪与防重 |
| sms_send_logs | phone_hmac / scene / submit_status | INDEX | 阶段 1 脱敏排障与状态查询 |
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
6. sms_send_logs                  -- 每次短信提交至少写一条，需设置保留和归档策略
7. gpu_device_events              -- 5 万设备频繁上报状态
8. asset_events                   -- 资产状态变更
9. orders                         -- 随用户增长
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
5. sms_templates
6. sms_scene_bindings（依赖 sms_templates、users）
7. sms_send_logs（依赖 sms_templates 可选）
8. user_login_logs（依赖 users）
9. identity_verifications（依赖 users）
10. identity_verification_logs（依赖 identity_verifications）
11. roles
12. permissions
13. user_roles（依赖 users、roles）
14. role_permissions（依赖 roles、permissions）
15. user_permission_overrides（依赖 users、permissions）
16. role_change_logs（依赖 users、roles）
17. audit_logs
18. applications
19. products（依赖 applications 可选）
20. product_plans（依赖 products）
21. product_prices（依赖 product_plans）
22. product_role_access（依赖 products、roles）
23. product_provision_handlers
24. application_adapters（依赖 applications）
25. product_billing_rules（依赖 products）
26. orders（依赖 users）
27. order_items（依赖 orders）
28. payment_callbacks（依赖 orders）
29. wallet_transactions（依赖 wallets、orders）
30. product_consumption_records（依赖 wallet_transactions）
31. membership_levels
32. membership_benefits（依赖 membership_levels）
33. user_memberships（依赖 users、membership_levels）
34. product_membership_rules（依赖 products、membership_levels）
35. user_assets（依赖 users、products）
36. user_entitlements（依赖 users、user_assets）
37. asset_events（依赖 user_assets）
38. announcements
39. help_categories
40. help_articles（依赖 help_categories）
```
