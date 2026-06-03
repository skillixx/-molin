# 数据量、分库分表和技术选型规划

## 1. 结论

第一版不要一开始就分库分表。

当前规模预估：

- 用户：10 万级。
- GPU 设备：5 万台。
- 应用：2000 个。
- 交易、钱包、Token、日志类数据会快速增长。

建议策略：

```text
第一阶段：单库 MySQL + 合理索引 + 按时间归档
第二阶段：大表按月分区或按月分表
第三阶段：订单、流水、消费记录按 user_id 分库分表
第四阶段：日志和统计类数据进入 ClickHouse / OpenSearch
```

最需要关注的不是用户表，而是：

- 钱包流水
- 产品消费记录
- Token 使用日志
- 审计日志
- 登录日志
- GPU 事件
- 订单明细

## 2. 不建议分库分表的表

这些表数据量相对可控，第一版不需要拆：

```text
users
roles
permissions
user_roles
role_permissions
user_permission_overrides
products
product_plans
product_prices
product_role_access
product_provision_handlers
membership_levels
membership_benefits
product_membership_rules
applications
application_adapters
help_categories
help_articles
announcements
```

原因：

- 用户 10 万级不算大。
- 商品 2000 个不算大。
- 角色权限表数据量很小。
- 配置类表主要读多写少。

处理方式：

- 保持单表。
- 建好唯一索引和查询索引。
- 高频配置可以缓存到 Redis。

## 3. 需要重点关注的大表

### 3.1 钱包流水表

表名：

```text
wallet_transactions
```

增长原因：

- 每次充值、消费、退款、冻结、解冻都会写流水。
- 钱包流水不能随意删除。
- 财务对账需要长期保留。

第一版处理：

- 单表。
- 按 `user_id`、`wallet_id`、`created_at` 建索引。

后期处理：

```text
按月分表：wallet_transactions_2026_06
或按 user_id 分片：wallet_transactions_00 ~ wallet_transactions_15
```

推荐优先：

```text
先按月分表，再考虑 user_id 分库
```

原因：

- 财务流水天然按时间查询。
- 对账和导出通常按时间范围。

### 3.2 产品消费记录表

表名：

```text
product_consumption_records
```

增长原因：

- Token 调用、GPU 按量、网盘流量、Agent 调用都会产生消费记录。
- 如果 Token 网关上线，这张表可能是增长最快的表之一。

第一版处理：

- 单表。
- 必须有 `idempotency_key` 唯一索引。
- 必须有 `user_id`、`product_id`、`created_at` 索引。

后期处理：

```text
product_consumption_records_2026_06
product_consumption_records_2026_07
```

如果 Token 调用量非常高，建议拆出：

```text
token_usage_logs
```

并将统计数据进入 ClickHouse。

### 3.3 Token 使用日志

表名：

```text
token_usage_logs
```

增长原因：

- 每次模型调用都写一条。
- 10 万用户下，如果日调用量达到百万级，MySQL 单表会很快变大。

第一版处理：

- 如果 Token 网关还没上线，可以只保留表结构。

上线后处理：

```text
MySQL：保存近期关键计费记录
ClickHouse：保存大规模统计分析数据
OpenSearch：保存可检索日志
```

推荐技术：

- ClickHouse：用量统计、成本分析、报表。
- OpenSearch：错误日志、请求检索。
- Kafka / RabbitMQ：异步写入。

### 3.4 审计日志

表名：

```text
audit_logs
```

增长原因：

- 后台所有敏感写操作都要记录。
- 权限、财务、实名审核、商品配置都会写审计。

第一版处理：

- 单表。
- 按 `operator_id`、`module`、`action`、`created_at` 建索引。

后期处理：

```text
按月分表
定期归档到对象存储
重要审计同步到 OpenSearch
```

### 3.5 登录日志

表名：

```text
user_login_logs
```

增长原因：

- 每次登录都写记录。
- 风控和安全分析需要保留。

后期处理：

- 按月分表。
- 历史数据归档。
- 异常登录分析可以进 OpenSearch。

### 3.6 GPU 设备事件

表名：

```text
gpu_device_events
```

增长原因：

- 5 万设备如果频繁上报状态，事件量会很大。

建议：

- 设备当前状态存在 `gpu_devices`。
- 事件流水进入 `gpu_device_events`。
- 高频状态采集不要全部进入 MySQL。

推荐技术：

```text
设备当前状态：MySQL + Redis
设备事件：ClickHouse / OpenSearch
设备实时上报：MQ
```

## 4. 可以先不拆但要保留扩展的表

```text
orders
order_items
user_assets
user_entitlements
asset_events
identity_verifications
identity_verification_logs
user_memberships
gpu_rentals
```

说明：

- `orders` 后期会变大，但第一版可以单表。
- `user_assets` 是用户当前拥有资产，数据量可控。
- `asset_events` 可能变大，后期按月分表。
- 实名认证数据敏感，重点是加密和权限，不是分表。

## 5. 分库分表策略

### 5.1 按时间分表

适合：

```text
wallet_transactions
product_consumption_records
token_usage_logs
audit_logs
user_login_logs
asset_events
gpu_device_events
```

示例：

```text
wallet_transactions_2026_06
wallet_transactions_2026_07
```

优点：

- 对账方便。
- 归档方便。
- 查询时间范围明确。

缺点：

- 跨月查询需要聚合多张表。
- 开发要封装路由逻辑。

### 5.2 按用户分片

适合：

```text
orders
wallet_transactions
product_consumption_records
user_assets
user_entitlements
```

示例：

```text
orders_00
orders_01
...
orders_15
```

分片键：

```text
user_id % 16
```

优点：

- 用户维度查询快。
- 单表数据量可控。

缺点：

- 运营后台跨用户查询复杂。
- 需要分片中间件或自研路由。

### 5.3 按业务类型分表

适合：

```text
product_consumption_records
```

示例：

```text
token_consumption_records
gpu_consumption_records
netdisk_consumption_records
agent_consumption_records
```

优点：

- 不同业务增长速度不同。
- 计费规则更清晰。

缺点：

- 统一财务报表需要合并查询。

## 6. 推荐技术选型

### 6.1 第一版

```text
MySQL 8
Redis 7
RabbitMQ
MinIO
```

不做分库分表。

需要做好：

- 索引。
- 慢查询监控。
- 归档预留。
- 表结构预留分片字段。

### 6.2 中期

```text
MySQL 分区表
按月分表
Redis 缓存
RabbitMQ 异步削峰
```

适合：

- 钱包流水按月。
- 审计日志按月。
- 登录日志按月。
- 消费记录按月。

### 6.3 后期

可选技术：

```text
Apache ShardingSphere
Vitess
TiDB
ClickHouse
OpenSearch
Kafka
```

建议：

- MySQL 分库分表：ShardingSphere 或 Vitess。
- 大规模统计报表：ClickHouse。
- 日志检索：OpenSearch。
- 超高吞吐事件：Kafka。
- 云原生 MySQL 扩展：TiDB。

## 7. 推荐落地路线

### 第一阶段

```text
单库 MySQL
核心表建索引
日志表按 created_at 建索引
消费事件做幂等
```

### 第二阶段

```text
wallet_transactions 按月分表
product_consumption_records 按月分表
audit_logs 按月分表
user_login_logs 按月分表
```

### 第三阶段

```text
Token 用量进入 ClickHouse
审计和错误日志进入 OpenSearch
订单和钱包按 user_id 分片
```

### 第四阶段

```text
引入 ShardingSphere / Vitess
拆分 billing-service
拆分 token-gateway
拆分 resource-service
```

## 8. 当前需要在代码中预留的内容

数据库表需要保留：

```text
user_id
created_at
product_id
product_type
idempotency_key
```

原因：

- `user_id` 用于按用户分片。
- `created_at` 用于按时间分表。
- `product_id` 和 `product_type` 用于业务拆分。
- `idempotency_key` 用于避免重复扣费。

后端代码需要预留：

```text
repository 层不要直接散落 SQL
表名生成逻辑集中封装
查询条件必须带时间范围
财务接口必须幂等
统计查询不要直接扫大表
```

## 9. 总结

当前最可能变大的表：

```text
product_consumption_records
token_usage_logs
wallet_transactions
audit_logs
user_login_logs
gpu_device_events
asset_events
orders
```

第一版不需要分库分表，但必须预留分片字段和索引。

真正上线 Token 网关、GPU 状态事件和大量按量计费后，再逐步引入：

```text
按月分表
ClickHouse
OpenSearch
ShardingSphere / Vitess
```
