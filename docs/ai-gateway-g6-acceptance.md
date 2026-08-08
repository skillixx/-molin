# AI 网关 G6 验收记录

> 当前状态：**进行中，尚未通过阶段门禁。**
>
> 已有证据仅覆盖当前工作树的本地代码、单元测试、类型检查和 Mock 浏览器旅程。测试环境部署、Migration 000065、真实 Bifrost 请求、金额/钱包对账、越权验证、凭据回收、CI、独立 QA 与产品双签尚未完成前，不得声明 G6 完成或允许生产开放。

## 1. 验收对象

| 项目 | 当前值 |
|---|---|
| 分支 | `codex/ai-gateway-g6-customer-journey` |
| 基线 | `origin/main@60b569f` |
| Migration | `000065_create_ai_gateway_g6_customer_journey` |
| 部署范围 | 仅测试环境，禁止生产 |
| 多模态范围 | 仅已发布文字模型 |

## 2. 当前证据

| 门禁 | 状态 | 证据 |
|---|---|---|
| Token 网关定向 Go 测试 | PASS | `go test -count=1 ./internal/modules/token_gateway/... ./migrations` |
| 用户端类型检查 | PASS | `npm.cmd run type-check` |
| 管理端类型检查 | PASS | `npm.cmd run type-check` |
| 用户端 Playwright | PASS | 生产构建预览下 8 条通过，并重复执行为 16/16：客户链路、Project/SK、文档状态、空/错误、账本/导出/申诉及三宽度 |
| 全量 Go/vet/mod verify | PASS | `go test -count=1 ./...`、`go vet ./...`、`go mod verify`；Windows 因 `CGO_ENABLED=0` 不执行竞态检测，Linux `-race` 仍待测试环境执行 |
| 两端 lint/build | PASS | 用户端与管理端均通过 `type-check`、`lint`、`build`；用户端生产依赖审计为 0 个漏洞 |
| Migration 隔离 MySQL 8 验证 | PENDING | 尚未执行 |
| 测试环境部署与真实浏览器 | PENDING | 尚未执行 |
| 真实 Bifrost 与人民币对账 | PENDING | 尚未执行 |
| 越权、吊销、不调用上游 | PENDING | 尚未执行 |
| CI、独立评审、QA、产品 | PENDING | 尚未执行 |

## 3. 真实 E2E 待填证据

部署时必须记录但不得输出敏感值：部署 commit、备份目录、Migration 前后版本、Bifrost 镜像/节点健康、测试用户内部 ID、临时 Project ID、脱敏 SK 前缀、模型发布版本、价格版本、唯一幂等键、request_id、Usage 各计量行、销售金额、钱包流水 ID、差异值、越权结果、吊销后结果及凭据回收时间。

金额验收标准：请求 `settled_amount`、销售计量行合计、钱包 consume 流水和用户页面展示完全一致，差异为 `0.00000000 CNY`。

## 4. 阶段结论

当前只能说明 G6 本地实现正在收口，不能说明测试环境可用、PR 可合并或阶段完成。只有 CI 全绿、真实 E2E 与对账通过、临时凭据回收、独立 QA/产品均 PASS 且 P0=0、P1=0 后，才更新本节为最终通过。
