# AI 网关 G4 运维手册

## 1. 上线前检查

> 迁移依赖：测试环境已经应用短信阶段2 `000059`。AI 网关迁移固定为 G1 `000060`、G2 `000061`、G3 `000062`、G4 `000063`；必须先合并并应用短信阶段2 PR #315，禁止在缺少 `000059` 的数据库上越号部署，也禁止复用已经被短信占用的迁移编号。

1. 确认目标环境、数据库名、Redis、RabbitMQ 和回滚负责人。
2. 备份 MySQL，记录当前 migration 版本和应用镜像摘要。
3. 确认至少有一个管理员用户；全新空库若没有用户，000063 不会自动发布默认策略，需通过后台创建并发布策略后才开放请求。
4. 检查 Redis 持久性与监控，但不得把 Redis 当作预算或钱包事实源。
5. 在隔离环境跑 G4 和 G3 验收脚本，再执行全量 race、vet 和敏感信息扫描。

## 2. 环境变量

```text
AI_GATEWAY_USER_CONCURRENCY=100
AI_GATEWAY_USER_RPM=600
AI_GATEWAY_USER_TPM=2000000
AI_GATEWAY_PROJECT_CONCURRENCY=100
AI_GATEWAY_PROJECT_RPM=600
AI_GATEWAY_PROJECT_TPM=2000000
AI_GATEWAY_KEY_CONCURRENCY=50
AI_GATEWAY_KEY_RPM=300
AI_GATEWAY_KEY_TPM=1000000
AI_GATEWAY_MODEL_CONCURRENCY=500
AI_GATEWAY_MODEL_RPM=3000
AI_GATEWAY_MODEL_TPM=10000000
```

所有值必须大于 0，服务启动时会验证。数据库中的 active 资源策略覆盖对应默认值。

## 3. 发布顺序

```text
备份和只读预检
  -> 执行 000063 up
  -> 检查 10 张 G4 表
  -> 发布应用但暂不导入真实流量
  -> 发布并复核 active 安全策略
  -> 小流量验证 403/429/503、SSE、Usage 和钱包
  -> 逐级放量
```

000063 是 expand migration。应用回滚时保留表和数据，down 文件故意不删除事实；不要手工 DROP 表。

## 4. 监控指标

- 40310/40311、42920/42921/42922、50320/50321 的数量与比例。
- Redis 延迟、错误率、租约数量、租约过期和续租失败。
- held 预算数量、最老 expires_at、dead/manual_review 补偿任务数。
- 80/90/100 预算阈值事件、Project/SK 预算使用率。
- G3 settlement_pending、billing_exception、Outbox dead 与钱包差异。
- SSE 审核失败率、客户端断连率和 Usage 完整率。

## 5. 故障处理

| 故障 | 预期行为 | 操作 |
|---|---|---|
| Redis 不可用 | 新请求 50321，禁止绕过 | 恢复 Redis，验证 PING 和原子脚本后再放量 |
| 安全策略不可读 | 新请求 50320 | 检查 MySQL和 active 策略，不允许临时关闭审核 |
| hard 预算大量 42920 | 上游未调用 | 核对周期、时区、held 和临时增额，不直接改钱包 |
| 补偿任务 dead | 自动重试停止 | 查 G3 request/钱包/usage 事实，确认后转 retry 或 manual_review |
| SSE 内容违规 | 当前违规段不外泄；保留上游 Usage/成本，用户扣费为 0 | 通过 request_id 查安全事件、钱包 hold 释放和 `billing_content_policy_waived` 事件，不重放上游 |
| RabbitMQ 不可用 | G3 Outbox 保留 | 恢复 RabbitMQ，按 G3 手册重放，不改 G4 预算金额 |

## 6. 只读排查 SQL

```sql
SELECT status, COUNT(*) FROM ai_budget_reservations GROUP BY status;
SELECT status, COUNT(*) FROM ai_compensation_tasks GROUP BY status;
SELECT category, direction, COUNT(*) FROM ai_safety_events
WHERE created_at >= UTC_TIMESTAMP() - INTERVAL 1 HOUR
GROUP BY category, direction;
SELECT scope_type, scope_id, period_type, threshold_percent, created_at
FROM ai_budget_alerts ORDER BY id DESC LIMIT 100;
```

禁止在未完成人工对账前直接更新预算预留、G3 请求、钱包流水或 Usage。输出审核阻断请求缺少 Usage 时，先从可信供应商账单核对四类 Token 数量，再通过 `POST /api/admin/token/billing/content-policy/{request_id}/resolve` 提交；不得在 SQL 控制台补写。操作人必须具有 `ai_gateway:reconcile_manage`、完成管理员二次认证并核对审计记录。成功后确认请求为 `released`、hold 为 `released`、用户 consume 数为 0、`provider_cost` 完整且 `billing_content_policy_waived` 事件唯一。

## 7. 回滚

应用回滚到 G3 版本时停止新流量，等待或标记运行中请求，保留 000063 表。恢复 G4 时先跑恢复扫描和账本对账，再开放请求。生产回滚、真实数据库变更和人工财务处置必须由负责人审批。
