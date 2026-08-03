# AI 网关 G4 开发文档

## 1. 代码结构

| 路径 | 职责 |
|---|---|
| `server/internal/modules/token_gateway/service/safety_service.go` | 输入/输出审核、规范化、摘要和稳定拒绝 |
| `server/internal/modules/token_gateway/service/resource_limiter.go` | Redis 四层并发、RPM、TPM 与租约心跳 |
| `server/internal/modules/token_gateway/service/governance_service.go` | 安全、预算、资源准入及恢复编排 |
| `server/internal/modules/token_gateway/service/governance_admin_service.go` | 策略、申诉、预算和补偿后台业务校验 |
| `server/internal/modules/token_gateway/repository/g4_governance_repository.go` | MySQL 策略、预算预留、事件和乐观锁 |
| `server/internal/modules/token_gateway/service/request_orchestrator.go` | G2/G3/G4 唯一请求链路和 SSE 输出审核 |
| `server/internal/modules/token_gateway/handler/governance_handler.go` | 管理端和用户申诉 HTTP 契约、前置审计 |
| `server/migrations/000063_*` | G4 expand migration；down 保留事实表 |
| `infra/scripts/verify-ai-gateway-g4-governance.sh` | 远程 Linux 临时容器隔离验收 |

## 2. 数据一致性

MySQL 是策略、预算和审计事实源，Redis 只保存可过期的短期资源状态。预算事务先锁定 Project/SK 策略行，再使用 locking read 汇总 settled 与 held 金额，避免 InnoDB repeatable-read 快照导致并发超卖。相同 request_id 的预留、释放和同步均幂等。

G4 不修改 G3 的钱包流水或销售 Outbox 金额语义。预算金额来自 G3 冻结报价，最终销售金额只从 G3 终态读取；输出审核拒绝额外写入 `ai_usage_items.source=provider_cost`，保存数量、冻结成本单价和平台成本金额，不进入用户销售额汇总。Usage 暂缺时保持 `settlement_pending`，由 `ResolveContentPolicyWaiver` 在行锁事务中校验错误类型和状态、补写原始 Usage 与平台成本、释放 hold 并写唯一 Outbox。终态重复调用会逐项核对数量、成本单价和金额；不一致返回冲突，不允许静默覆盖。

## 3. Redis 原子性

单次 Lua 脚本同时检查四层并发、RPM 和 TPM；任何层失败都不保留部分准入。并发成员使用 `lease_id=request_id` 和过期分值，心跳续租时检查租约仍存在。Redis 请求失败统一映射为 503，禁止降级为本地内存计数。

## 4. SSE 安全

SSE 扫描器最大单行 2 MiB。公开事件暂存在有界段内，遇到句末、换行、512 字符或流结束时审核；审核通过才写出该段。保留前 128 字符尾部参与下一段审核，降低关键词跨片段绕过。审核递归覆盖正文、工具定义、工具调用名称与 arguments。违规时丢弃当前段，但继续解析 Usage 和 `[DONE]`；账本保存 Provider Usage 与平台成本，释放用户钱包 hold，不生成销售计费行，并写 `billing_content_policy_waived` Outbox 后再发送墨灵安全终止事件。

## 5. 并发与故障恢复

- 请求正常、失败、panic 可通过 defer 回收资源租约。
- 客户端断开不取消已经形成的上游事实和结算。
- 没有可信 Usage 时继续遵守 G3 settlement_pending，不猜测消费量。
- 预算同步失败写补偿任务，`next_retry_at` 到期即重新扫描，不等待预算预留自身过期；八次后进入 dead 并退出自动扫描，manual_review 也不会被恢复任务覆盖。管理员使用 `updated_at` 乐观锁显式转 retry 后才恢复扫描。
- migration down 只执行 no-op，不删除安全、预算或补偿审计事实。

## 6. 扩展边界

图片、音频、视频和 embedding 在 G5 通过统一 metric/异步任务接入。G4 当前只在 chat completion 链路生效，不应复制一套独立安全、预算或限流实现。后续 Adapter 必须复用 GovernanceService 并在资产持久化前执行输出审核。

## 7. 测试入口

```bash
cd server
go test -count=1 ./internal/modules/token_gateway/... ./internal/bootstrap ./internal/config
go test -race -count=1 ./...
go vet ./...
```

隔离 Linux 验收：

```bash
AI_GATEWAY_G4_ISOLATED_APPROVED=YES \
G4_DOCKER_PULL_POLICY=never \
bash infra/scripts/verify-ai-gateway-g4-governance.sh
```

脚本只允许临时 Docker 网络和随机凭证，不连接项目数据库。成功标志为 `G4_VERIFY=PASS`。
