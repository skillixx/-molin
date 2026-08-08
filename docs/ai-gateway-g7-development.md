# AI 网关 G7 可靠性开发说明

> 状态：当前工作树范围内实现完成并通过本地定向、Linux 竞态、隔离 MySQL/Redis 和只读 CLI 验证；最终门禁见 `docs/ai-gateway-g7-acceptance.md`。

## 1. 设计总览

G7 不创建第二套请求或财务事实表。进程事件由 `AIGatewayMetrics` 记录，持久状态由 `AIGatewayDBGaugeCollector` 使用只读 SQL 聚合，统一追加到现有 `/api/internal/metrics`：

```text
RequestOrchestrator / Governance / ResourceLimiter / AIBilling
  -> 进程内低基数 Counter、Histogram
  -> /api/internal/metrics（内部 Token + 来源网段）
  -> Prometheus 告警
  -> Grafana SLO 看板

MySQL 请求/Usage/钱包/Outbox/补偿事实
  -> 只读聚合 Collector
  -> 指标端点 + ai-gateway-reconcile

Redis 四层共享租约索引
  -> 抓取时按未过期成员读取权威 Gauge
  -> 指标端点
```

## 2. 代码结构

| 文件 | 职责 |
|---|---|
| `server/internal/modules/token_gateway/service/observability.go` | 指标注册、封闭标签、直方图和 Prometheus 文本输出 |
| `server/internal/modules/token_gateway/service/observability_collector.go` | MySQL 状态、积压、差额和异常的只读聚合 |
| `server/internal/modules/token_gateway/service/request_orchestrator.go` | 请求结果、端到端耗时、TTFT、上游结果/重试、Usage 缺失、流式断连和输出审核失败埋点 |
| `server/internal/modules/token_gateway/service/resource_limiter.go` | 四层共享租约索引与 Gauge 采集、拒绝、心跳失败和幽灵租约埋点 |
| `server/internal/modules/token_gateway/service/governance_service.go` | 内容、预算、并发、RPM、TPM 拒绝原因埋点 |
| `server/internal/modules/token_gateway/service/ai_billing_service.go` | 事务提交后的账务状态转换埋点 |
| `server/internal/modules/auth/handler/metrics_handler.go` | 复用内部指标端点，鉴权并在事实读取失败时返回脱敏 503 |
| `server/cmd/ai-gateway-reconcile` | 显式非生产安全门、READ ONLY 事务、中文摘要和 JSON 报告 |
| `infra/prometheus/ai-gateway-alerts*.yml` | AI 网关告警规则及 promtool 阈值测试 |
| `infra/prometheus/blackbox.yml`、`targets/bifrost-nodes.json` | Bifrost 双节点无凭据 HTTP Blackbox 探测 |
| `infra/grafana` | Prometheus 数据源和 `molin-ai-gateway-g7` SLO 仪表盘 |
| `infra/scripts/verify-ai-gateway-g7-reliability.sh` | 隔离全迁移、负载、幂等、断连、混沌和零差额验收 |

## 3. 指标契约

主要指标族：

- `molin_ai_gateway_requests_total`、`request_duration_seconds`、`ttft_seconds`；
- `request_duration_seconds` 从 `Prepare` 入口开始计时，正常请求把起点移交给 `Execute`，输入审核、报价、预算、预占及执行阶段合并为一次端到端观测；业务拒绝记为 `rejected`，依赖故障与内部错误记为 `failure`，幂等回放不重复计数；
- `stream_interruptions_total`、`upstream_requests_total`、`upstream_retries_total`、`usage_missing_total`；
- `billing_requests`、`billing_oldest_age_seconds`、`billing_transitions_total`；
- `unreleased_holds`、`unreleased_holds_amount_cny`、`unreleased_holds_oldest_age_seconds`；
- `outbox_backlog`、`outbox_oldest_age_seconds`、`compensation_backlog`、`compensation_oldest_age_seconds`；
- `billing_difference_cny`、`billing_anomalies`、`billing_amount_cny`、`security_findings`；
- `concurrency_leases`、`concurrency_rejections_total`、`heartbeat_failures_total`、`ghost_leases_total`。

请求类型、结果、驱动、拒绝原因、状态和 scope 均为封闭枚举。逻辑模型只接受最长 128 字符的字母、数字、点、下划线、斜杠和连字符，并限制为 32 个注册值。

## 4. 对账口径

一次对账在同一个只读可重复读事务中计算：

1. `request_usage`：已结算请求 `settled_amount` 与权威销售 Usage 金额合计的绝对差。
2. `request_hold`：请求 `held_amount`、关联表 `held_amount`、钱包 hold `hold_amount`、`unfreeze/in` 释放流水之间三段绝对差之和。
3. `request_wallet`：已结算请求 `settled_amount` 与唯一 `consume/out` 流水金额的绝对差。
4. 价格事实：快照必须逐项匹配不可变价格版本的 `version_no/model/currency/exchange_rate/rounding_mode/failure_charge_policy/max_tokens`，四个 SKU 必须按 `variant_hash` 精确匹配成本价、销售价、scale 和币种；`minimum_charge` 固定为 `0.00000100`。结构合法但版本、成本价或销售价不同同样失败。
5. Usage 事实：正常 Provider 路径要求 sequence 0 的 `input/output/total` 三项原始事实，可附带 `cached/reasoning` 和合法 `provider_cost` 成本行；`total=input+output`，cached/reasoning 不得超过各自父项。已结算请求的 sequence 1 必须恰好包含四个互斥销售计量项，满足 `input+cached=raw input`、`output+reasoning=raw output`，单价等于快照，逐项金额按 `ceil_8(quantity×unit_price/scale)` 重算，成功且正用量时再应用最低收费。经审计的 reconciled sequence 1 可替代不存在的 Provider 原始事实。
6. 钱包事实：request/link/hold/freeze/unfreeze/consume 的金额、用户、钱包实体 owner、方向、类型与关联 ID 必须一致；报价和预占为正，`0 ≤ settled ≤ held`，零金额结算不得伪造消费流水，在途或 exception 状态不得提前挂接终态流水。
7. 异常：重复结算、超过五分钟仍成功未结算、缺失完整价格事实、缺失或不一致的钱包财务链、缺失或不一致 Usage、完成未收敛和账务 exception。
8. 门禁：任何未释放 hold、Outbox 活跃积压或补偿任务未收敛同样返回 FAIL；JSON 同时输出最多 500 条 request_id/issue_code 明细，超限显式标记截断。

在线 `usage_missing_total` 与只读对账 `billing_anomalies{kind="missing_usage"}` 任一路径非零都会触发 P1；未释放预占只要最老年龄超过 300 秒或总额超过 10 元也会触发 P1。真正的内容策略拒绝记为请求结果 `rejected` 并从可用性 SLO 排除，分类器超时或审核状态持久化失败记为 `failure` 并进入可用性分母。

SQL 表名只来自代码内固定集合；全部动态值使用参数绑定。金额从 MySQL `CAST(... AS CHAR)` 解析为 Decimal。

## 5. 事务与并发

- 账务埋点只在事务成功提交后累加，事务回滚和幂等重放不制造虚假转换次数。
- Redis Lua 在实体级准入 ZSET 之外同步维护四个 scope 的共享租约索引；Gauge 抓取按当前时间只统计未过期成员，因此 B 实例清理 A 实例的租约不会导致任一进程本地计数漂移，重复释放也不会产生负值。
- MySQL 负载执行 10 波、每波 100 并发，共 1000 个请求；100 个独立测试钱包循环使用，服务内部处理死锁，验收客户端对明确可重试冲突执行最多 10 次有界退避。
- 性能门使用可达的本机 Fake HTTP 服务和独立内存仓储：JSON 直接累加 `Prepare`、`Execute→生产驱动调用`、`上游响应体首字节→客户端首次写出` 三个本地阶段，避免用两个并发墙钟区间相减引入调度误差；SSE 只以上游首字节和首个公开数据帧成功写入两个直接时间戳计算首包附加开销。财务完整性由同脚本的 MySQL 千请求门禁独立验证，二者不混报。
- 既有 G3 单钱包 100 并发继续验证不超扣；G4 8 节点 100 并发继续验证最多 20 个租约准入。
- 隔离 MySQL 反向测试在同一事务内覆盖 18 项损坏：hold 结算金额、三段预占、freeze/release 关联、伪结构快照、错误版本号、错误 SKU 成本/销售价、销售 Usage 空值、原始来源冒充、额外计量项、raw↔sale 数量不守恒、错误单价、错误重算金额、跨钱包消费、link 指向他人钱包、结算超过预占、在途提前终结和 exception 缺链。聚合与 request_id 明细必须同时识别，事务回滚后再验证最终零差额。

## 6. 验证命令

```bash
cd server
go test -count=1 ./internal/modules/token_gateway/service ./internal/modules/auth/handler ./cmd/ai-gateway-reconcile
go test -race -count=1 ./...

cd ..
AI_GATEWAY_G7_ISOLATED_APPROVED=YES G7_DOCKER_PULL_POLICY=missing \
  bash infra/scripts/verify-ai-gateway-g7-reliability.sh
```

Prometheus 使用固定镜像 `prom/prometheus:v3.12.0-distroless` 运行 `promtool check rules` 和 `promtool test rules`；Blackbox Exporter 固定 `prom/blackbox-exporter:v0.28.0`。Grafana 使用官方 Docker 文档推荐的 `grafana/grafana` 镜像系列；当前固定 `13.1.3`，避免 `latest` 漂移，版本升级必须重新跑 Compose 与仪表盘验收。

## 7. 安全说明

- 指标端点保持内部 Token 常量时间比较、来源 IP allowlist 和可信代理边界，不信任开放互联网传入的 XFF。
- 指标错误响应不包含 SQL、DSN、请求 ID、提示词或上游错误原文。
- 用户账单申诉先校验 `request_id/user_id` 归属；通用疑似密钥文本只拒绝入库，只有候选平台 SK 的 HMAC 精确匹配该请求所属有效 `api_keys.key_hash` 时才写 `secret_leak_detected`。审计目标只保存 `api_key` ID，摘要只含来源、确认和拦截布尔值；五分钟指标按唯一 Key 计数，审计失败时请求失败关闭。
- 测试脚本随机生成 MySQL 密码和隔离库名，不映射容器端口，使用 tmpfs；Go 测试还会解析 DSN，拒绝非随机 G7 库名和非本机/隔离容器主机，退出时只删除自己创建的精确容器和网络。
- CLI 不接受生产环境，也不会根据报告自动执行退款、补扣、释放或重排任务。
