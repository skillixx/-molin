# VID-G5 最小人工财务审查包

## 1. 需要批准什么

审批人为项目负责人兼财务负责人。本包只请求批准本地、非商业、合成钱包夹具的开发合同，不授权真实钱包、真实资金、真实调账、Provider、HTTP、基础设施部署或远程Git操作。

当前状态：五项本地非商业财务合同已获用户明确批准，见[批准记录](./evidence/video-gateway-vid-g5-finance-approval.json)。下面保留审批时的规则与预期金样，完整G5财务闭环仍在开发，不能将合同批准标为测试通过。审批前文档保存在[历史快照包](./evidence/video-gateway-vid-g5-approved-documents.json)，完整范围见[开发合同](./video-gateway-vid-g5-billing-outbox-reconcile.md)。

## 2. 五项审批合同

### F1：Quote、预占、请求与事件同事务

是否批准：Quote单次消费、Wallet Hold、冻结流水、请求钱包关联、Task、I2V冻结输入/租约及held Outbox在同一MySQL事务中提交？任一写入点失败全部回滚，输入拒绝及余额不足不能留下可执行任务。

建议采用既有 `ConsumeTx/CreateHoldTx` 等事务内边界。Provider与Queue只能发生在事务提交之后；数据库重试候选最多3次且重试整个事务，不能把Provider放进重试闭包。

生成命令采用用户＋Project＋create_video＋幂等键，两个未来HTTP门面共享该作用域，但本阶段不注册路由。重放必须重新鉴权；同键同意图返回原事实，同键不同意图冲突，不能借另一Project SK/JWT改变归属。

### F2：销售、成本、拒绝与释放规则

是否批准：销售依据冻结Quote与实际可交付规格；Provider成本只记已确认事实，不能等于或反推用户销售额？T2V/I2V分开定价，不叠加其他视频计量。

明确失败且无产物、输出审核拒绝、明确显式/隐式标识失败时，候选规则为用户销售0、全量释放Hold；Provider已确认成本作为平台安全成本保留。无法证明安全结果或Provider成本时，未知事实保持未知，不能伪记为0。

设Hold=H、消费=S、净释放=R，财务终结时R=H-S且0≤S≤H；未终结时必须另计仍冻结的F，满足H=S+R+F。现有钱包会先生成解冻H的流水，再生成消费S的流水，因此“解冻流水金额”不等于“净释放R”。金额、币种和每个balance_after必须按完整流水链核对。超出H的结算不封顶、不透支，转待对账。

### F3：未知结果保持冻结，补偿不重调Provider

是否批准：超时、断连、ACK不明、Usage冲突或成功后归档失败时保持Hold，不猜测收费、不自动释放、不重复Submit？

补偿只能读已持久化Request/Quote/Hold/Task/Asset/Usage等事实，修复未完成的settle、release、delivery或Outbox。它不持有Provider Adapter，也不重新抓取Provider内容。证据不充分保持pending_reconcile/manual_review；最多8次后dead，不能自动第9次或重置计数继续。人工核对不得抢占活跃Worker租约。

### F4：结算提交后才交付，而且只交付一次

是否批准：财务事务先提交，后续独立交付事务再把满足全部门禁的六类资产变为available？包括完整且不可变的审核/双标识版本、无争议或保全阻断、未删除、无活动补偿、request_id零差异。

交付事务失败不撤销已存在财务事实，而是幂等补偿交付；不能再次扣费或重调Provider。补偿完成标记与交付在同一交付事务内完成并再次全量对账，失败一并回滚，避免“补偿仍活跃但资产已可见”。

### F5：调账只追加，双人复核，钱包动作不可缺

是否批准：调整必须追加方向、原因、maker、不同的checker和对应钱包动作引用，不改原Quote、Usage或钱包流水？

maker=checker一律拒绝。项目负责人兼财务身份不能替代两名不同主体。仅有adjustment、没有对应钱包动作时，对账继续失败；不能靠手工写一条“调整”强行把差额归零。本阶段仅使用合成主体与合成余额，不执行真实调账。

## 3. 金额金样候选

全部标记 `non_commercial_test_fixture`，不是实际报价或账单。每个用例独立使用初始可用余额10.00000000、冻结余额0.00000000，名义请求5秒；金额单位CNY，计算使用十进制字符串与Decimal。

- T2V：销售单价0.10000000/秒，确认成本夹具0.04000000/秒，Quote=Hold=0.50000000。
- I2V：销售单价0.15000000/秒，确认成本夹具0.06000000/秒，Quote=Hold=0.75000000。
- 表中的成本是合成账本预期值；真实Provider支出恒为0。未知成本使用未确认状态，不把NULL当0。
- 数量列仅表示用户可交付计量；拒绝或未交付时Provider已观测数量另行保留，不重写成用户数量。

| 用例 | 操作/结果 | Quote/Hold | 用户数量（秒） | 销售 | 确认成本 | 已结算 | 净释放 | 可用余额后 | 冻结后 |
|---|---|---|---|---|---|---|---|---|---|
| F01 | T2V成功 | 0.50/0.50 | 5 | 0.50 | 0.20 | 0.50 | 0 | 9.50 | 0 |
| F02 | I2V成功 | 0.75/0.75 | 5 | 0.75 | 0.30 | 0.75 | 0 | 9.25 | 0 |
| F03 | T2V审核拒绝 | 0.50/0.50 | 0 | 0 | 0.20 | 0 | 0.50 | 10.00 | 0 |
| F04 | T2V明确失败、确认无成本 | 0.50/0.50 | 0 | 0 | 0 | 0 | 0.50 | 10.00 | 0 |
| F05 | T2V成功但归档失败 | 0.50/0.50 | 0 | 0 | 0.20 | 0 | 0 | 9.50 | 0.50 |
| F06 | T2V结算失败后补偿成功 | 0.50/0.50 | 5 | 0.50 | 0.20 | 0.50 | 0 | 9.50 | 0 |
| F07 | T2V queued取消 | 0.50/0.50 | 0 | 0 | 0 | 0 | 0.50 | 10.00 | 0 |
| F08 | T2V取消被接受、确认无成本 | 0.50/0.50 | 0 | 0 | 0 | 0 | 0.50 | 10.00 | 0 |
| F09 | T2V取消被拒绝、继续执行 | 0.50/0.50 | 0 | 0 | 未确认 | 0 | 0 | 9.50 | 0.50 |
| F10 | T2V cancel_requested后迟到成功 | 0.50/0.50 | 5 | 0.50 | 0.20 | 0.50 | 0 | 9.50 | 0 |
| F11 | T2V结果未知 | 0.50/0.50 | 0 | 0 | 未确认 | 0 | 0 | 9.50 | 0.50 |
| F12 | T2V Provider报6秒、媒体5秒 | 0.50/0.50 | 0 | 0 | 0.24（夹具已确认） | 0 | 0 | 9.50 | 0.50 |

Outbox缩写：H=video_billing_held，S=video_billing_settled，R=video_billing_released，P=video_settlement_pending，A=video_delivery_available，J=video_delivery_rejected，C=video_compensation_required。每种独立事实恰好一条；下表是预期，不是执行结果。

| 用例 | Outbox | 资产/交付 | 补偿 | 最终对账预期 |
|---|---|---|---|---|
| F01/F02 | H,S,A | 六类资产available | 无 | 所有差异0 |
| F03 | H,R,J | quarantined/rejected | 无 | 销售0与平台安全成本都可追溯，所有差异0 |
| F04 | H,R,J | 无可交付产物/rejected | 无 | 所有差异0 |
| F05 | H,P,C | pending，不交付 | pending/manual_review | 余额守恒但补偿未闭合，整体对账不能PASS |
| F06 | H,P,C,S,A | 补偿完成后available | completed | 原结算失败未留半条财务事实；最终差异0 |
| F07 | H,R,J | cancelled/rejected | 无 | Submit=0，所有差异0 |
| F08 | H,R,J | cancelled/rejected | 无 | 所有差异0 |
| F09 | H | cancel_requested/pending | 无，原任务继续 | 未形成终态，不得报告最终闭合或交付 |
| F10 | H,S,A | succeeded/available | 无 | 未覆盖相反财务终态，所有差异0 |
| F11 | H,P,C | pending_reconcile/pending | manual_review或有界retry | 未知事实与活动补偿阻断对账通过 |
| F12 | H,P,C | pending_reconcile/pending | manual_review | 两份Usage/媒体事实冲突保留，不取高值向用户收费 |

F06在补偿前的中间状态仍为可用9.50、冻结0.50、不可交付；不能只验证表中补偿后的终态。F10只适用于cancel_requested尚未released的任务，若先形成released则迟到成功必须失败关闭，不能重新收费。

## 4. 审批后仍要证明

批准合同不等于批准实现或验收通过。之后必须逐项完成原Goal全部代码、故障注入、100并发、隔离MySQL、保留式migration、Chat/Image兼容、敏感扫描及独立QA/PM/Standards/Spec审查；任一差异不为0或缺少足够证据都不能交付。

补偿不重提Provider、settle/release互斥、未结算不交付、完整请求对账及敏感正文不进入普通字段，是不可放宽的停止条件。

## 5. 审批文本

若同意上述F1至F5本地测试合同，可在任务中回复：

```text
FINANCE=APPROVE_VID_G5_QUOTE_HOLD_SETTLE_RELEASE_OUTBOX_COMPENSATION_RECONCILIATION
```

该文本不授权Git提交、推送、PR、合并、真实钱包、真实资金、真实调账、Provider/Key、HTTP、基础设施、测试服务器或生产操作。VID-G6仍禁止启动。
