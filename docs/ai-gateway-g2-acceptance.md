# AI 网关 G2 阶段验收记录

> 验收对象：`feature/bifrost-ai-gateway-g2`
>
> 阶段：G2 RequestOrchestrator、Project、Project SK、显式模型权限和无收费文字请求正式链路。

## 1. 自动化证据

| 检查项 | 当前结果 |
|---|---|
| Go 全量测试 | 通过：`go test ./...` |
| G2 服务与 Handler 专项测试 | 通过 |
| 20 并发相同幂等键 | 仅形成一条请求 |
| JSON/SSE/断连/结果未知 | 通过 |
| Usage 缺失不伪造 | 通过 |
| Finalize 重试 | 不重复 Usage |
| 空消息/多值幂等 Header | 在 Prepare 前拒绝 |
| 模型定向可见性 | 与 Project SK allowlist 双重校验 |
| 中断恢复扫描 | 超过安全窗口后事务锁内重查，再收敛 unknown |
| Project SK 审计 | 创建/轮换/吊销脱敏记录，失败输出脱敏告警 |
| 租户、Project、SK 归属 | 应用测试与 MySQL 复合外键通过 |
| Migration 000061 | 隔离 MySQL 8 首次 up、保留式 down、re-up 通过 |
| G3 事实扫描 | 未发现钱包、hold、settled、released 或旧用量双写 |
| 敏感信息扫描 | 未发现真实 SK、HMAC Secret 或 Bifrost 内部 Token |
| Linux race | 通过：测试 Linux 临时 `golang:1.25` 容器执行 `go test -race -count=1 ./...` |

隔离 MySQL 输出：

```text
G2_MYSQL_MIGRATION=PASS mysql=8.0 isolated=true project_database=false first_up=true down_retained=true reup=true tenant_constraints=true allowlist_constraints=true billing_unquoted=true
```

## 2. QA 验收清单

- [x] Project 归属查询不只使用 project_id。
- [x] 新 Project SK 默认空 allowlist，拒绝全部模型。
- [x] Key 明文只返回一次，数据库只保存 HMAC。
- [x] 轮换原子地产生新 Key 并吊销旧 Key。
- [x] 停用 Project、吊销/过期 Key、停用/未实名用户在上游前拒绝。
- [x] 未实名使用 70001，渠道不可用使用 50300，空消息和异常幂等 Header 在 Prepare 前拒绝。
- [x] 用户分组/角色可见性与 Project SK allowlist 同时生效。
- [x] 周期恢复扫描在事务锁内重查状态和截止时间，只收敛仍然过期的遗留 pending/running 请求。
- [x] Project SK 创建、轮换和吊销写脱敏审计，不记录完整 SK；写入失败输出脱敏告警。
- [x] JSON 和 SSE 共用 RequestOrchestrator。
- [x] 请求、attempt、Usage 和错误进入正式账本。
- [x] 幂等冲突和并发重复请求不重复调用上游。
- [x] 断连后继续形成可确定的 Usage；未知结果不 fallback。
- [x] `billing_status` 始终为 `unquoted`，价格和金额字段为空。
- [x] 测试 Linux 全量 `go test -race -count=1 ./...` 通过，临时源码目录已清理。

## 3. 产品经理确认清单

- [x] G2 只提供 Project SK 和无收费文字正式链路。
- [x] 未进入价格、钱包、预算硬限制、限流、审核、多模态和 UI。
- [x] 模型列表与 Project SK 授权一致。
- [x] 重放返回已有请求状态，不制造第二次上游成本。
- [x] 错误文案不泄露原请求、上游 Key 或内部路由。
- [x] 功能、开发、API、数据库和测试文档同步。
- [x] 独立 QA 复验通过：P0=0、P1=0；路由旧计费语义注释已在提交前修正。
- [x] 产品经理有条件签收通过：P0=0、P1=0；当前分支规则已统一。

## 4. 当前阶段状态

G2 代码、本地测试、隔离 MySQL 8、测试 Linux 全量 race、独立 QA 和产品经理验收均已完成。产品验收保留一项非阻断残余风险：恢复竞态目前由内存仓储交错测试和 MySQL 事务锁代码审查覆盖，尚未增加真实 MySQL 双连接锁竞争测试；该测试应在后续 PR/CI 能力完善时补充。

本次签收只允许提交并推送 `feature/bifrost-ai-gateway-g2`，不代表允许合并 `main`、进入 G3、部署生产或切换真实流量。
