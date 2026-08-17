# AI 网关 G3 价格与钱包可靠结算开发文档

## 1. 架构

`RequestOrchestrator` 仍是唯一请求编排器。G3 注入 `AIBillingService` 后，Prepare 负责报价、建请求和预占；Finalize 负责写执行事实、Usage、钱包终态、请求终态、查询兼容用量汇总和 Outbox。同步请求、Settlement Worker 使用同一结算入口。

```text
Project SK 鉴权 -> 价格快照 -> 请求 + wallet hold + Outbox（同事务）
-> Native/Bifrost 执行 -> Usage
-> settle/release/pending/exception + 流水 + settled 查询汇总 + Outbox（同事务）
-> Outbox Worker -> RabbitMQ broker confirm -> 持久队列
```

## 2. 关键代码

| 文件 | 职责 |
|---|---|
| `service/pricing_service.go` | Decimal 报价、快照、四 SKU 汇总、最低收费和实际金额 |
| `repository/g3_pricing_repository.go` | 一致性读取活动价格、受控发布、重叠区间拒绝和异常暂停 |
| `service/ai_billing_service.go` | G3 单一财务入口、状态机、事务重试和异常对账 |
| `billing/service/wallet_hold_service.go` | `CreateHoldTx`、`SettleHoldTx`、`ReleaseHoldTx` |
| `repository/g3_outbox_repository.go` | 批量认领、锁超时、租约 CAS、重试和 dead 状态 |
| `service/outbox_worker.go` | Outbox 与 Settlement 周期 Worker |
| `service/rabbitmq_publisher.go` | 持久 Exchange/Queue、mandatory 路由和 broker confirm |
| `migration 000062` | 价格、模型级发布锁、请求钱包关联、Outbox 与钱包 Decimal/非负约束 |

## 3. 价格与快照

价格版本只通过受控发布方法从 `approved` 进入 `active`。发布事务先锁定 `ai_price_model_locks` 中的逻辑模型行，再校验审批、成本有效期、四个唯一 meter、时间区间和活动版本重叠；同模型并发发布只能有一个成功。G3 不提供通用 Update/Delete API；已发布内容不原地改价，暂停仅改变生命周期状态。测试环境初始化和发布步骤见 `docs/ai-gateway-g3-price-runbook.md`。

报价时价格版本和 SKU 在同一个一致性读事务中读取。快照包含版本、成本/销售单价、scale、汇率、取整、失败收费和最低收费。结算只读取 `price_snapshot_json`，不读取后来发布的活动价格；最低收费只用于成功且存在正用量的请求。

## 4. 财务事务

固定锁顺序为 `ai_requests -> wallet_holds -> wallets`。预占和结算发生死锁或锁等待超时时重跑完整事务，最多 10 次；业务拒绝、唯一键冲突和金额异常不盲目重试。

结算先完整记录 unfreeze 后余额，再记录 consume 后余额，流水可按 ID 顺序还原。`actual > hold` 不调用旧钱包封顶逻辑，而是保留 hold、写异常状态和 P0 Outbox。

仅当请求在同一事务内收敛为 `settled` 时，`AIBillingService` 才按权威 `ai_usage_items` 汇总幂等写入一条 `token_usage_logs`。写入后会逐字段核对用户、密钥、模型、流式标志、Token 汇总、状态和 `sale_amount`；任何不一致都会使结算事务整体回滚。`sale_amount` 通过 migration 000067 与权威财务账本统一为 `DECIMAL(20,8)`。人工核定未知或取消执行时，汇总状态保留为 `pending_reconcile` 并标记 `manual_reconciled`，禁止伪装为执行成功。该表只服务既有查询兼容与证据核对，不是价格计算、钱包扣费或补账依据，也不会回填历史请求。

Bifrost 非流式与流式驱动只接受响应 JSON 顶层 `id` 作为低敏 `upstream_request_id`，最大 191 字符且仅允许固定安全字符。流式分片出现不同顶层 `id` 时按结果未知失败关闭，禁止把两个上游请求拼成一条证据。引用经 `ExecutionAttempt -> ToLedgerModel -> finalizeAttemptTx` 写入 `ai_execution_attempts`，不进入公开接口；`infra/bifrost/config.json` 的 `client.enable_logging=false` 保持不变，禁止保存请求正文、响应正文、Header 或凭据。

## 5. 对账与 Outbox

- 可信 Usage 已存在：无论 attempt 成功或失败，Settlement Worker 都调用 `FinalizeRequest` 按快照结算。
- 明确失败且未产生成本：调用同一入口释放。
- 结果未知、SSE 未正常结束或 Usage 缺失/不一致：即使已取得中间 Usage 也保持 `settlement_pending`；超过 24 小时转人工对账异常并保留 hold。
- HTTP trace 已确认请求尚未写出时按明确零成本失败释放；同一幂等键在事务内从旧失败事实切换到新请求。请求已写出后的网络错误按结果未知处理。
- 对账扫描按请求隔离错误，一条损坏记录只进入错误汇总，不阻塞后续请求。人工核定后可通过受 `token:manage`、管理员二次认证和前置审计保护的后台接口释放或按可信 Usage 结算；管理 UI 留给后续阶段。
- Outbox 使用秒精度 `locked_at` 作为租约令牌；旧 Worker 不能覆盖重新认领者的结果。
- Outbox 仓储只认领同一聚合最早的未发布事件，前序 pending/dead 会阻塞后序事件；每次发布最多等待 30 秒，单 Worker 最多保留一个尚未返回的发布调用，默认第 18 次失败才进入 dead，累计自动退避窗口超过 2.5 小时。dead 事件只能通过受控 `RequeueDead` 重新入队。
- 人工结算将核定 Usage 写为 `source=reconciled`，不覆盖原始 `source=provider` 记录；钱包实扣金额可由核定行完整解释。
- RabbitMQ 发布声明持久 topic exchange 和 `exchange.events` 持久队列，绑定 `#`，使用 mandatory 和 broker confirm。

## 6. Migration 与配置

新增环境变量：

```text
RABBITMQ_URL=amqp://...
AI_OUTBOX_EXCHANGE=molin.ai.billing
```

`RABBITMQ_URL` 为空时不启动 Outbox Worker，事件保持 `pending`，避免空地址重试耗尽为 `dead`；配置恢复并重启服务后继续发布。已启动后 Broker 临时不可用则按退避策略重试。

`000062` 可重复 up。down 只执行保留式空操作，不删除价格、请求、钱包、流水或 Outbox 事实。隔离验证：

```bash
AI_GATEWAY_G3_MYSQL_APPROVED=YES bash infra/scripts/verify-ai-gateway-migration-000062.sh
```

脚本只连接无宿主端口的临时 MySQL/RabbitMQ 网络；测试机默认 `--pull=never`，CI 显式使用 `G3_DOCKER_PULL_POLICY=missing`。

## 7. 验证范围

- 本地：`go test -count=1 ./...`、`go vet ./...`、`git diff --check`。
- Linux：`go test -race -count=1 ./...`。
- 隔离 MySQL：首次/重复 up、保留 down/re-up、同模型并发发布、100 并发钱包竞争、20 幂等竞争、终态互斥、同幂等键安全重试、写失败回滚、断连、Usage 缺失、坏记录隔离、人工释放/结算、settled 查询汇总幂等与回滚、Outbox 顺序/dead 重入/真实租约 CAS 和超额异常。
- 执行证据：Bifrost 非流式与流式顶层引用提取、嵌套/超长/非法字符拒绝、跨分片引用冲突失败关闭、账本映射和请求日志关闭契约。
- 隔离 RabbitMQ：Broker 停止留存、恢复发布、broker confirm 和持久队列实际消费。

这些证据不代表生产部署、真实收费或真实上游调用已经批准。
