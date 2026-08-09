# AI 网关 G7 验收记录

> 当前状态：**G7 已在测试环境完成最终验收并合并。PR #326 精确 HEAD、CI、测试环境 E2E、独立评审、QA、产品验收和 merge commit 均已闭环；生产部署、真实付费上游和客户灰度仍属于 G8。**

## 1. 验收对象

| 项目 | 当前值 |
|---|---|
| 分支 | `feature/backend-d-ai-gateway-g7-reliability-validation`（远端已删除） |
| 基线 | `origin/main@37196ec57f3748bafc7763abacc665b9d3ed1872` |
| 功能提交 | `cec7cdcb887b22d5aee93a7cf09c5d7709badd9e` |
| PR / 合并 | PR #326 / merge commit `6e1f67ad4c1a10bb1ad79b3aeac6b16211ccfac1` |
| Migration | 无新增；目标测试 schema 保持 `66:0` |
| 上游 | Fake only，付费上游费用 0 |
| 部署范围 | 仅测试环境，禁止生产 |
| 模型范围 | 已发布文字模型；不含图片和视频 |

## 2. 当前证据

| 门禁 | 状态 | 证据 |
|---|---|---|
| 指标、Collector、Handler 定向测试 | PASS | Go 单元测试覆盖封闭标签、32 模型上限、输入/输出审核拒绝原因、并发抓取、只读 SELECT、鉴权、可信代理和采集失败脱敏 503 |
| Linux 竞态测试 | PASS | `golang:1.25` Linux 容器执行全仓 `go test -race -count=1 ./...` 通过 |
| 真实 Redis 100 并发 | PASS | 8 节点、100 并发、20 准入/80 拒绝；跨实例从 Redis 共享索引读取权威 Gauge，B 节点清理 A 节点过期租约后两端均为 1，最终释放均为 0；Redis 停止时失败关闭、恢复后复测通过 |
| G7 隔离 MySQL | PASS | 当前全部 up migration；10 波共 1000 请求按 100 并发完整结算，100 路同键幂等竞争只创建/执行/结算一次，SSE 断连继续读取可信 Usage，Fake Bifrost 停止/恢复均通过；同一事务内注入价格版本/SKU、raw↔sale 数量、销售单价/重算金额、钱包 owner/金额域及 hold/freeze/release 等 18 项真实 MySQL 损坏，聚合与 request_id 明细均失败关闭，回滚后恢复零差额 |
| 性能门禁 | PASS | Fake HTTP 成功率 100%；最新连续三轮 1000 请求@100 并发中，JSON 直接累加三个本地阶段后的 P95 最大值 `7.425ms ≤ 20ms`；SSE 以上游响应体首字节和首个公开数据帧成功写入的直接时间戳计算首包附加开销，P95 最大值 `0.5401ms ≤ 30ms` |
| 正式只读 CLI | PASS | `status=PASS`、`has_mismatch=false`、`issues=[]`；三项差额均 `0.00000000 CNY`，七类异常、未释放预占、Outbox/补偿活跃积压均为 0 |
| Prometheus | PASS | `promtool check rules` 发现并通过 22 条规则；22/22 告警均有正向阈值用例，对账 `missing_usage` 可触发 P1，小额 hold 超过 300 秒可触发 P1，P0/P1 分级负向用例通过，每条规则均有逐告警 Runbook URL |
| Grafana | PASS | `grafana/grafana:13.1.3` 临时容器 health 正常，UID `molin-ai-gateway-g7` 自动加载；Compose 与 JSON 校验通过 |
| 安全门 | PASS | CLI 仅允许精确批准和明确非生产；账单申诉先校验请求归属，通用疑似密钥一律拒绝入库但不升级 P0，只有 Base64URL 完整候选值 HMAC 精确匹配该请求所属、未过期且有效的平台 SK 时才按唯一 API Key 写脱敏 `secret_leak_detected` 审计，审计失败则请求失败关闭；指标无敏感/高基数标签；测试容器无项目库连接和付费上游；分支敏感信息扫描 `findings=0` |
| 全量回归 | PASS | 全仓 `go test ./...`、`go vet ./...`、`go mod verify` 与 Linux 全仓 race 通过；G7 MySQL、G4 Redis 和恢复复测均通过 |
| 测试环境 E2E | PASS | ChangeId `g7-cec7cdc-20260808T165255Z`；API 制品 SHA-256 `97c4d948cd46ca6816ba59feb5466db2e9c09d8bfda136097aabccc2516a6c72`；health/ready=200、schema `66:0`、Bifrost 2/2 healthy、对账零差额 |
| GitHub PR / CI | PASS | PR #326；run `31267782855`，精确 HEAD `cec7cdcb`，7/7 Jobs PASS；merge commit `6e1f67ad` |
| 独立代码评审 | PASS | 精确 HEAD 规格与规范评审 P0=0、P1=0、P2=0 |
| 独立 QA | PASS | 精确 HEAD 验收 P0=0、P1=0、P2=0、P3=0 |
| 产品验收 | PASS | 同一精确 HEAD 产品确认通过，批准转 Ready 并按 merge commit 合并 |

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
| 缺失可信 Usage | 0 |
| 已完成但仍待结算 | 0 |
| 账务异常状态 | 0 |
| 未释放预占 | 0 笔 / `0.00000000 CNY` |
| Outbox / 补偿活跃积压 | 0 / 0 |

本地隔离证据已由测试环境正式只读对账复验：三项差额均为 `0.00000000 CNY`，七类异常、未释放预占、Outbox/补偿活跃积压均为 0。大规模负载仍使用 Fake 上游，且生产环境未部署。

## 4. 缺陷门禁

独立代码评审、QA 和产品经理已针对精确 HEAD `cec7cdcb` 完成复核：P0=0、P1=0、P2=0、P3=0。PR 合并后 tree 与已验收提交 tree 均为 `e59693d277539028c8ebb6041ccd5828fc3713e9`。

## 5. 最终签署结论

1. PR CI 全绿，G7 隔离脚本、测试环境 E2E、Fake 上游/Redis 混沌恢复、指标抓取、Grafana SLO 和只读对账全部通过。
2. 测试环境已完成新版本→旧 `4b84b89`→新版本实际回滚，三次 health/ready 均为 200，最终恢复精确 HEAD 制品。
3. 独立代码评审、独立 QA、产品经理均对同一精确 HEAD 签署，P0/P1=0。
4. PR #326 已按 merge commit 合并到 `main`，远端功能分支已删除。
5. 合并后测试服最终只读复验保持 health/ready=200、schema `66:0`、对账零差额、Bifrost 2/2 healthy、活动告警 0。

G7 最终验收通过。生产部署、生产 Migration、真实付费上游容量验证、真实客户灰度和生产告警联系人配置均未执行，不得表述为生产上线。
