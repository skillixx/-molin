# AI 网关 G3 阶段验收记录

> 验收对象：`feature/bifrost-ai-gateway-g3`
>
> 当前状态：2026-08-03 独立 QA 与产品经理双签通过，P0=0、P1=0。

## 1. 自动化证据

| 检查项 | 结果 |
|---|---|
| Go 全量测试 | `go test -count=1 ./...` 通过 |
| Go 静态检查 | `go vet ./...` 通过 |
| Linux race | `go test -race -count=1 ./...` 通过 |
| Migration 000061 | 首次 up、重复 up、保留 down、re-up 通过 |
| 钱包并发 | 100 请求竞争同一钱包，无负余额 |
| 请求幂等 | 20 并发同请求只形成一个请求和 hold |
| 终态竞争 | settle/release/重复 settle 只形成一个终态 |
| 任意末端写失败 | 强制 Outbox 写失败，钱包/Usage/请求终态全部回滚 |
| Usage 缺失 | 返回 202 分类，保持 hold 和待对账 Outbox |
| 客户端断连 | 可信 Usage 继续结算，流水连续可还原 |
| 超过 hold | exception、价格暂停、P0 Outbox、无补扣 |
| RabbitMQ | 停止留存、恢复 confirm、持久队列消费通过 |
| 旧账本 | G3 正式链路不写 `token_usage_logs` |

最终候选为 v38，归档 `molin-g3-v38.tar.gz`。本地与测试 Linux 重新计算的 SHA-256 均为：

```text
b583dce38f7e81b71256c1b6c5ee15d93635f32db3944d92fb19d6bf52ec28d1
```

远程验证使用 `/home/pc/molin/tmp/molin-g3-current-20260803-v38` 隔离目录和临时 Docker 基础设施，不连接项目数据库。归档哈希一致后依次执行 Migration、真实 MySQL/RabbitMQ 集成测试和 Linux 全仓 race。

隔离脚本摘要：

```text
G3_MYSQL=PASS mysql=8.0 isolated=true project_database=false first_up=true repeated_up=true retained_down=true reup=true go_integration=true concurrent_wallet=100 idempotency=20 terminal_once=true over_hold_exception=true
G3_RABBITMQ=PASS broker_confirm=true stopped_retained=true recovered_published=true
```

## 2. QA 清单

- [x] 独立测试工程师确认 P0=0、P1=0（v38 最终签收通过）。
- [x] Decimal 金样、极小金额、舍入和 uint64 边界通过。
- [x] 价格快照包含最低收费，不受活动价格变化污染。
- [x] 钱包、hold、流水、请求关联和 Outbox 同事务。
- [x] Outbox 使用租约 CAS，RabbitMQ 使用必达队列和 broker confirm。
- [x] 错误响应不暴露数据库、钱包、路由和上游凭据。

## 3. 产品确认清单

- [x] 独立产品经理确认 P0=0、P1=0，业务范围和状态文案通过。
- [x] 采用人民币钱包按量收费，不引入积分。
- [x] G3 不包含 UI、内容审核、限流、预算硬限制、多模态或 fallback。
- [x] 待结算和异常不会展示为免费或已退款。
- [x] 功能、开发、API、数据库和测试文档同步。

## 4. 阶段边界

本记录不批准合并 `main`、生产部署、真实支付、真实用户流量或进入 G4。独立 QA 与产品经理已满足 P0/P1 为 0 的阶段门槛，仅允许创建固定中文提交并推送 G3 功能分支；远程分支与提交哈希由本次交付在推送后核验。
