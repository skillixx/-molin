# AI 网关 G7 验收记录

> 当前状态：**开发工作树与本地隔离门禁通过；测试环境 E2E、PR CI、独立代码评审、独立 QA、产品验收和合并尚未完成，因此 G7 尚未最终验收。**

## 1. 验收对象

| 项目 | 当前值 |
|---|---|
| 分支 | `feature/backend-d-ai-gateway-g7-reliability-validation` |
| 基线 | `origin/main@37196ec57f3748bafc7763abacc665b9d3ed1872` |
| Migration | 无新增；目标测试 schema 保持 `66:0` |
| 上游 | Fake only，付费上游费用 0 |
| 部署范围 | 仅测试环境，禁止生产 |
| 模型范围 | 已发布文字模型；不含图片和视频 |

## 2. 当前证据

| 门禁 | 状态 | 证据 |
|---|---|---|
| 指标、Collector、Handler 定向测试 | PASS | Go 单元测试覆盖封闭标签、32 模型上限、并发抓取、只读 SELECT、鉴权、可信代理和采集失败脱敏 503 |
| Linux 竞态测试 | PASS | Token 指标/编排/资源/治理、内部指标 Handler、只读 CLI 定向 `go test -race -count=1` 通过 |
| 真实 Redis 100 并发 | PASS | 8 节点、100 并发、20 准入/80 拒绝；释放后四层租约 Gauge 为 0，过期租约进入幽灵租约指标 |
| G7 隔离 MySQL | PASS | 当前全部 up migration；100 个独立测试钱包并发完整结算，20 路幂等竞争，SSE 断连，Fake 上游停止/恢复均通过 |
| 正式只读 CLI | PASS | `status=PASS`、`has_mismatch=false`；三项差额均 `0.00000000 CNY`，四类异常、未释放预占、活跃积压均为 0 |
| Prometheus | PASS | `promtool check rules` 与阈值单测通过 |
| Grafana | PASS | `grafana/grafana:13.1.3` 本地容器 health 正常，UID `molin-ai-gateway-g7` 已自动加载；Compose 与 JSON 校验通过 |
| 安全门 | PASS | CLI 仅允许精确批准和明确非生产；指标无敏感/高基数标签；测试容器无项目库连接和付费上游 |
| 全量回归 | 待完成 | 等待提交前最终全量 Go、vet、mod verify 和既有 G3/G4 回归 |
| 测试环境 E2E | 待完成 | 等待目标、进程、环境、schema、ChangeId 和回滚预检后执行 |
| GitHub PR / CI | 待完成 | 尚未创建 PR |
| 独立代码评审 | 待完成 | 尚未对精确 PR HEAD 签署 |
| 独立 QA | 待完成 | 尚未对精确 PR HEAD 签署 |
| 产品验收 | 待完成 | 尚未对精确 PR HEAD 签署 |

## 3. 本地财务一致性结果

| 检查 | 结果 |
|---|---|
| 账本↔销售 Usage | `0.00000000 CNY` |
| 账本↔钱包预占 | `0.00000000 CNY` |
| 账本↔钱包消费流水 | `0.00000000 CNY` |
| 重复结算 | 0 |
| 执行成功但未结算 | 0 |
| 缺失价格快照 | 0 |
| 缺失钱包流水 | 0 |
| 未释放预占 | 0 笔 / `0.00000000 CNY` |
| Outbox / 补偿活跃积压 | 0 / 0 |

该证据来自本地临时 MySQL 8/Redis 7 和 Fake 上游，不代表远程测试环境或生产环境状态。

## 4. 缺陷门禁

当前开发自测未发现 P0/P1。最终缺陷数量必须由独立 QA 和产品经理针对精确 PR HEAD 复核；任何 P0/P1 非零、三项差额非零、异常事实非零、CI 失败或测试环境未恢复，均禁止转 Ready 和合并。

## 5. 最终签署条件

只有以下项目全部完成后，才能把本文件状态改为“G7 最终验收通过”：

1. PR CI 全绿，G7 隔离脚本在 Linux 完整输出 `G7_VERIFY=PASS`。
2. 测试环境部署、Fake 上游/Redis 混沌恢复、指标抓取、Grafana SLO 和只读对账全部通过。
3. 独立代码评审、独立 QA、产品经理均对同一精确 HEAD 签署，P0=0、P1=0。
4. 测试凭据和临时容器回收，账本事实保留，生产未部署。
5. PR 合并到 `main`，合并后 CI 与主分支提交可追溯。

当前不得声称 G7 已完成、已生产上线或允许真实客户灰度。
