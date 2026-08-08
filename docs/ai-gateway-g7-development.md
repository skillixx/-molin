# AI 网关 G7 可靠性开发说明

> 状态：当前工作树范围内实现完成并通过本地定向、Linux 竞态、隔离 MySQL/Redis 和只读 CLI 验证；最终门禁见 `docs/ai-gateway-g7-acceptance.md`。

## 1. 设计总览

G7 不创建第二套请求或财务事实表。进程事件由 `AIGatewayMetrics` 记录，持久状态由 `AIGatewayDBGaugeCollector` 使用只读 SQL 聚合，统一追加到现有 `/api/internal/metrics`：

```text
RequestOrchestrator / Governance / ResourceLimiter / AIBilling
  -> 进程内低基数 Counter、Histogram、Gauge
  -> /api/internal/metrics（内部 Token + 来源网段）
  -> Prometheus 告警
  -> Grafana SLO 看板

MySQL 请求/Usage/钱包/Outbox/补偿事实
  -> 只读聚合 Collector
  -> 指标端点 + ai-gateway-reconcile
```

## 2. 代码结构

| 文件 | 职责 |
|---|---|
| `server/internal/modules/token_gateway/service/observability.go` | 指标注册、封闭标签、直方图和 Prometheus 文本输出 |
| `server/internal/modules/token_gateway/service/observability_collector.go` | MySQL 状态、积压、差额和异常的只读聚合 |
| `server/internal/modules/token_gateway/service/request_orchestrator.go` | 请求结果、端到端耗时、TTFT、上游结果/重试、Usage 缺失和流式断连埋点 |
| `server/internal/modules/token_gateway/service/resource_limiter.go` | 四层租约、拒绝、心跳失败和幽灵租约埋点 |
| `server/internal/modules/token_gateway/service/governance_service.go` | 内容、预算、并发、RPM、TPM 拒绝原因埋点 |
| `server/internal/modules/token_gateway/service/ai_billing_service.go` | 事务提交后的账务状态转换埋点 |
| `server/internal/modules/auth/handler/metrics_handler.go` | 复用内部指标端点，鉴权并在事实读取失败时返回脱敏 503 |
| `server/cmd/ai-gateway-reconcile` | 显式非生产安全门、READ ONLY 事务、中文摘要和 JSON 报告 |
| `infra/prometheus/ai-gateway-alerts*.yml` | AI 网关告警规则及 promtool 阈值测试 |
| `infra/grafana` | Prometheus 数据源和 `molin-ai-gateway-g7` SLO 仪表盘 |
| `infra/scripts/verify-ai-gateway-g7-reliability.sh` | 隔离全迁移、负载、幂等、断连、混沌和零差额验收 |

## 3. 指标契约

主要指标族：

- `molin_ai_gateway_requests_total`、`request_duration_seconds`、`ttft_seconds`；
- `stream_interruptions_total`、`upstream_requests_total`、`upstream_retries_total`、`usage_missing_total`；
- `billing_requests`、`billing_oldest_age_seconds`、`billing_transitions_total`；
- `unreleased_holds`、`unreleased_holds_amount_cny`；
- `outbox_backlog`、`outbox_oldest_age_seconds`、`compensation_backlog`、`compensation_oldest_age_seconds`；
- `billing_difference_cny`、`billing_anomalies`；
- `concurrency_leases`、`concurrency_rejections_total`、`heartbeat_failures_total`、`ghost_leases_total`。

请求类型、结果、驱动、拒绝原因、状态和 scope 均为封闭枚举。逻辑模型只接受最长 128 字符的字母、数字、点、下划线、斜杠和连字符，并限制为 32 个注册值。

## 4. 对账口径

一次对账在同一个只读可重复读事务中计算：

1. `request_usage`：已结算请求 `settled_amount` 与权威销售 Usage 金额合计的绝对差。
2. `request_hold`：请求 `held_amount`、关联表 `held_amount`、钱包 hold `hold_amount` 两段绝对差之和。
3. `request_wallet`：已结算请求 `settled_amount` 与唯一 `consume/out` 流水金额的绝对差。
4. 异常：重复结算、超过五分钟仍成功未结算、缺失合法价格快照、缺失或不一致的钱包结算流水。

SQL 表名只来自代码内固定集合；全部动态值使用参数绑定。金额从 MySQL `CAST(... AS CHAR)` 解析为 Decimal。

## 5. 事务与并发

- 账务埋点只在事务成功提交后累加，事务回滚和幂等重放不制造虚假转换次数。
- Redis Lua 返回实际 ZADD/ZREM 数量，重复释放不会让进程 Gauge 变为负数。
- 100 并发负载使用 100 个独立测试钱包；服务内部处理死锁，验收客户端对明确可重试冲突执行最多 10 次有界退避。
- 既有 G3 单钱包 100 并发继续验证不超扣；G4 8 节点 100 并发继续验证最多 20 个租约准入。

## 6. 验证命令

```bash
cd server
go test -count=1 ./internal/modules/token_gateway/service ./internal/modules/auth/handler ./cmd/ai-gateway-reconcile
go test -race -count=1 ./...

cd ..
AI_GATEWAY_G7_ISOLATED_APPROVED=YES G7_DOCKER_PULL_POLICY=missing \
  bash infra/scripts/verify-ai-gateway-g7-reliability.sh
```

Prometheus 使用固定镜像 `prom/prometheus:v3.12.0-distroless` 运行 `promtool check rules` 和 `promtool test rules`。Grafana 使用官方 Docker 文档推荐的 `grafana/grafana` 镜像系列；当前固定 `13.1.3`，避免 `latest` 漂移，版本升级必须重新跑 Compose 与仪表盘验收。

## 7. 安全说明

- 指标端点保持内部 Token 常量时间比较、来源 IP allowlist 和可信代理边界，不信任开放互联网传入的 XFF。
- 指标错误响应不包含 SQL、DSN、请求 ID、提示词或上游错误原文。
- 测试脚本随机生成 MySQL 密码，不映射容器端口，使用 tmpfs，退出时只删除自己创建的精确容器和网络。
- CLI 不接受生产环境，也不会根据报告自动执行退款、补扣、释放或重排任务。
