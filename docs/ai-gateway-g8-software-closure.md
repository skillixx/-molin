# AI 网关 G8 软件闭环执行清单

> 当前状态：`G8_STAGE_ACCEPTANCE=PASS`、`G8_SOFTWARE_CLOSED_LOOP=COMPLETED`、`G8_TEST_ENV_USABLE=YES`、`G8_REAL_PROVIDER_SETTLEMENT=PASS`、`ACCEPTED_EXCEPTIONS=YES`。021～024 的失败、消费与归档记录均作为历史事实保留；最终状态来自后续真实 Provider 与结算证据及项目负责人的阶段验收裁决，不得反向改写历史尝试。

## 0. 最终闭环裁决

| 类别 | 归档结论 |
|---|---|
| 已有技术证据 | 真实 Provider 调用、执行、Usage、计费结算、钱包流水、Outbox 与低敏证据持久化主链路满足阶段验收要求 |
| 接受的非阻断项 | `RESPONSE_MATCH=NO` 保留；未配置临时 SK 的手工脚本不要求补跑；账单/争议查询追加核对接受关闭，但均不得写成技术验证通过 |
| 后续运维专项 | 测试服对账、失败补偿、双闸门、回滚演练、Prometheus/Grafana、告警规则、备份周期、RabbitMQ ready 消息 |
| 风险边界 | 测试服真实流量闸门保持开启，调用可能产生真实费用；生产开放与商业验收仍未授权 |

本次归档不删除任何历史失败，不补写历史请求、Usage、钱包流水或 Outbox，也不执行新的远端动作。`G8_SOFTWARE_CLOSED_LOOP=COMPLETED` 仅表示 G8 软件阶段按负责人验收决定结项。

### 0.1 历史消费失败索引

- `CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-021` 历史状态为 `CONSUMED_LOCAL_RECEIPT_UNAVAILABLE_SSH_NOT_STARTED`；021 已消费，SSH 与远端命令均为 0，当时 `G8_SOFTWARE_CLOSED_LOOP` 尚未完成。
- `CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-022` 历史状态为 `CONSUMED_LOCAL_IDENTITY_PAIR_FAILED_SSH_NOT_STARTED`；022 已消费，SSH 与远端命令均为 0，当时 `G8_SOFTWARE_CLOSED_LOOP` 尚未完成。
- `CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-023` 历史状态为 `CONSUMED_SSH_SESSION_FAILED_REMOTE_AUDIT_NOT_PROVEN`，固定错误分类为 `ssh_session_failed`；该记录形成时 `G8_SOFTWARE_CLOSED_LOOP` 尚未完成。

以上均为历史时点事实；本次负责人验收决定不会恢复 021—023 的执行能力，也不会把历史失败改写为成功。

## 1. 闭环目标

在受控测试环境完成以下真实软件链路：

```text
管理员发布文字模型、价格、路由和安全策略
  -> 用户发现模型并创建 Project/SK
  -> 发起非流式与 SSE 文字调用
  -> 生成请求账本、Usage 和价格快照
  -> 钱包预占并一次终态结算或释放
  -> 用户查询用量、账单并提交申诉
  -> 管理员查询异常、审计和监控
  -> 只读对账严格为零差异
  -> 候选回滚并恢复后再次通过
```

## 2. 原计划实施顺序（历史基线）

1. 不安装 host 受控入口、不写 sudoers、不使用 sudo；使用新的独立 ChangeId，由 `pc` 在最多一次非交互 SSH 中通过既有 Docker 权限执行冻结的无参数只读审计，核实测试服 API、依赖、Schema、Bifrost、监控、备份和账务当前状态。Docker 权限接近宿主 root，候选必须反向禁止容器变更、宿主写入、migration、业务请求、真实上游和费用动作，并在任何失败后零重试停止。
2. 轮换历史可能暴露或复用的测试凭据，保留仓库外低敏回执；不得把 Secret 写入 Git、日志或聊天。
3. 生成并验证 `test_candidate` 清单，以应用总闸和边缘总闸同时关闭的方式部署精确候选制品。
4. 完成健康检查、Migration、配置键集、Fake Bifrost 双节点路由、监控告警和备份/恢复验证。软件闭环禁止配置真实付费上游密钥。
5. 使用独立 ChangeId 打开仅测试网络可达的边缘入口和非生产应用总闸，固定 Fake/零费用上游、测试钱包与测试用户；请求上限 20、上游费用上限 0 CNY，不允许公网客户访问。
6. 执行管理端与用户端真实后端浏览器旅程，以及并发、幂等、断连、依赖故障和异常结算回归。
7. 执行只读对账，分别确认“账本↔Usage”“账本↔钱包预占”“账本↔钱包消费流水”为 `0.00000000 CNY`；七类异常、未释放 hold、Outbox 和补偿活跃积压均为 0。
8. 立即重新关闭应用总闸和边缘入口，验证所有文字调用入口返回 HTTP 503/业务码 50330，且关闭态不创建账本、不调用上游、不扣费。
9. 执行候选→基线→候选实际回滚，验证账本、钱包流水、Usage、Outbox、审计和申诉事实保持完整；恢复候选后再次完成受控开闸、调用、对账和关闸。
10. 对最终精确 HEAD 和测试候选完成 CI、独立代码安全复评、测试工程师验收和产品经理确认。

### 2.1 下一只读审计候选的工程前置门禁

020 的冻结生成器本身通过工程门禁，但唯一授权尝试被仓库外临时 PowerShell 包装的语法错误阻断。新的独立 ChangeId 必须先关闭启动链路缺口：

- “复核工程 merge 原始 blob、生成冻结命令、校验大小与 SHA-256、解析并启动”必须收敛为仓库内唯一固定入口，禁止人工拼接或临时外层包装。
- 固定入口必须兼容 Windows PowerShell 5.1，不依赖可选模块；使用纯 .NET 完成摘要与语法解析，并在任何材料读取、子进程或网络动作前失败关闭。
- 原生 Windows 动态测试必须以假子 PowerShell、假 `ssh.exe` 和临时低敏回执证明：语法错误时子进程为 0；成功路径只启动一个子 PowerShell、一个假 SSH；父窗口保活且退出码、阶段标志和 ActionPreference 均可确定恢复。
- Linux `--network none` 与 Windows CI 必须同时覆盖历史 015 至 023 墓碑，并覆盖 024 候选的未授权、低敏输出、单假 SSH 和零重试；测试不得读取真实 SSH 身份或建立网络连接。
- 023 不得再次授权、重试或重放。若继续诊断 SSH 会话失败或获取运行态证据，必须使用新的独立 ChangeId，并重新完成工程、CI、独立评审、main 合并与冻结摘要复核。
- 024 只允许固定目标上的一次 `printf` 回执探针，不包含 Docker、HTTP、数据库或业务能力；工程合并后仍须新的独立精确授权才可执行。

## 3. 原计划完成门禁（历史基线）

下表保留最初的严格计划口径，便于追溯；其中未追加执行的对账、补偿、双闸门和回滚项已由项目负责人转入后续运维专项，不能再把表内旧 `PENDING` 理解为当前 G8 阶段阻断。

| 门禁 | 完成标准 |
|---|---|
| 运行态 | API `health/ready` 正常；MySQL、Redis、RabbitMQ、Bifrost、监控和备份无阻断性 `UNKNOWN` |
| 数据库 | Schema 为批准版本且 `dirty=0`，Migration 与回滚证据完整 |
| 用户旅程 | 发布、发现、Project/SK、调用、Usage、账单、申诉全链路真实后端通过 |
| 财务安全 | 三项正常账务差额 `0.00000000`；无重复扣费、负余额、未释放 hold 或不可追踪结算 |
| 可靠性 | 幂等、并发、断连、上游与依赖故障、恢复和补偿通过 |
| 回滚 | 候选→基线→候选实际执行成功，历史财务和审计事实完整 |
| 前端 | 1440/768/375 无横向溢出，所有按钮有明确交互反馈 |
| 质量 | 全量自动化与 CI 通过，独立复评、QA、产品均通过，P0=0、P1=0 |

## 4. 原计划验收证据表（历史基线）

以下字段是原计划要求，继续保留以防历史语义丢失；表内 `PENDING` 不代表最终归档伪造为 PASS，而是表示该原计划条目未按原路径补录，并已按第 0 节的负责人裁决接受或延期。

| 证据项 | 必填证据 | 当前状态 |
|---|---|---|
| 源码与制品 | 最终 commit、API/双端/Bifrost digest、配置键集摘要 | `PENDING` |
| 数据库 | MySQL 版本、Schema 版本、`dirty=0`、Migration 前后与回滚证据 | `PENDING` |
| 依赖运行态 | API health/ready、Redis、RabbitMQ、Fake Bifrost 2/2、Prometheus targets、Grafana、Alertmanager、备份摘要 | `PENDING` |
| 受控开闸 | ChangeId、测试网络范围、Fake 上游证明、测试用户/钱包匿名标识、请求上限 20、费用 0 CNY | `PENDING` |
| 真实后端旅程 | 管理员发布、用户发现、Project/SK、JSON/SSE 调用、Usage、账单、申诉及 request_id 集合摘要 | `PENDING` |
| 三项差额 | 账本↔Usage、账本↔钱包预占、账本↔钱包消费流水分别为 `0.00000000 CNY` | `PENDING` |
| 异常与积压 | 七类异常、未释放 hold、Outbox、补偿活跃积压均为 0 | `PENDING` |
| 关闸复位 | 应用/边缘总闸关闭回执，全部文字入口 503/50330，账本/上游/扣费增量为 0 | `PENDING` |
| 回滚 | 候选、基线、恢复候选三阶段制品摘要、健康、旅程、对账、事实保留，以及恢复候选后的第二次关闸与账本/上游/扣费零增量回执 | `PENDING` |
| 前端适配 | 1440/768/375 截图或 Playwright 证据，按钮加载/成功/失败/禁用反馈 | `PENDING` |
| 质量签署 | 全量命令及结果、精确 CI run、代码安全、QA、产品签署，P0/P1=0 | `PENDING` |

## 5. 最终状态边界

- 当前阶段按负责人验收决定报告 `G8_SOFTWARE_CLOSED_LOOP=COMPLETED`，软件实现任务结项；历史未执行项仍按第 0 节标记为接受例外或后续运维专项。
- 未取得生产授权：不得部署生产、运行真实付费请求、配置真实通知或开放客户。
- `G8_COMMERCIAL_ACCEPTED` 属于后续独立任务；观察周期与商业指标届时确认，不阻塞本清单。
