# VID-G5：钱包预占、结算释放、Outbox、补偿与零差异对账

## 1. 状态与范围

本文件说明完整VID-G5内部实现、开发合同、验证与回滚边界。五项本地财务合同已获项目负责人批准；生成意图、共享请求幂等、原子预占、结算/释放、补偿、交付对账、追加调账和十二金额金样已有实现及隔离验证。最终阶段结论以`docs/evidence/video-gateway-vid-g5-acceptance.json`及其同源独立回执为准；后文历史切片说明不能单独替代最终验收。

- 基线：`36b6a5c5f9e60a4ef182ae434337bb05e165477c`，来自fresh fetch后的origin/main。
- 分支：`feature/video-gateway-vid-g5-billing-outbox-reconcile`，独立从上述基线创建。
- 前置阶段：[VID-G4最终合并证据](./evidence/video-gateway-vid-g4-final-merge.json)，PR #420已合并，源码树与验收版本一致。
- 财务合同：已批准，见[人工审批记录](./evidence/video-gateway-vid-g5-finance-approval.json)；这不表示代码或最终验收通过。原审查文档可由[审批快照包](./evidence/video-gateway-vid-g5-approved-documents.json)复算。
- 当前Git权限：`LOCAL_ONLY`；不提交、推送、创建PR或合并。

使用者为后端开发、测试、产品和项目负责人兼财务审批人；没有页面入口。目标是让文生视频、图生视频在同一请求与财务事实体系中完成Quote→Hold→Fake执行→媒体安全→settle/release→交付→Outbox→补偿→对账。

严格不包含正式HTTP路由、OpenAI Videos HTTP门面、前端、真实Provider/Key、真实Bifrost视频数据面、RabbitMQ dispatcher、Redis/MinIO视频运行时、真实钱包或真实资金、测试服务器及生产操作。已有Go方法名含HTTP路径含义不代表路由已开放。Fake结算不能解释为商业可用；不进入VID-G6。

任务约束如下；这些是阶段范围标签，不表示已新增对应环境变量或运行时装配：

```text
GIT=LOCAL_ONLY
COMMERCIAL=NON_COMMERCIAL_TEST_FIXTURE_ONLY
PROVIDER=FAKE_MOCK_ONLY
EXECUTION_DRIVER=FAKE_NATIVE_ASYNC_ONLY
TARGET_PROVIDER_CONTRACT=RUNWARE_RUNWAY_GEN4_5_TASKUUID_5S
WALLET=ISOLATED_SYNTHETIC_FIXTURE_ONLY
FINANCE_APPROVER=PROJECT_OWNER_ACTING_AS_FINANCE
FINANCE_CONTRACT_APPROVAL=APPROVED_LOCAL_FIXTURE_ONLY
OUTBOX=MYSQL_FACT_ONLY
OUTBOX_DISPATCHER=OFF
RABBITMQ_RUNTIME=OFF
REDIS_RUNTIME=OFF
MINIO_RUNTIME=OFF
BIFROST_VIDEO_DATA_PLANE=OFF
TEST_DATABASE=ISOLATED_TEMPORARY_ONLY
CONTAINER_PULL=NOT_AUTHORIZED
FORMAL_HTTP_ROUTES=0
FRONTEND_CHANGES=0
MAX_COST_CNY=0
```

真实Provider请求/Key、真实钱包/用户资金/调账、外部业务HTTP、公网媒体URL、测试服写入和生产操作均必须为0；按Goal要求的GitHub合并/CI元数据核验属于只读门禁检查，不是业务Provider调用。

## 2. 已核验的复用边界

以下路径和能力已在基线源码核验；右列是G5拟做的增量，不代表已经实现。

| 既有模块/文件 | 已有能力 | G5拟议接入与缺口 |
|---|---|---|
| `service/video_quote_facade.go` | `VideoReservationCoordinator.ReserveAndCreate`接口及两类Go门面 | 实现唯一原子预占协调器，不注册HTTP |
| `repository/video_quote_repository.go` | `ConsumeTx`在调用方事务内消费Quote，校验归属、操作、指纹、过期及重放 | 与Hold、请求、Task/Input/租约、held Outbox同事务 |
| `service/video_pricing_service.go` | `video_seconds`、非商业夹具、Decimal、冻结价格快照 | 复用销售计算；确认成本与销售分开，不猜测未知成本 |
| `billing/service/wallet_hold_service.go` | `CreateHoldTx`、`SettleHoldTx`、`ReleaseHoldTx` | 只使用事务内严格方法；不得用会自行提交或静默封顶的旧接口 |
| `model/ai_billing.go` | `ai_request_wallet_links`、`ai_outbox_events` | 复用请求关联和Outbox表；视频Outbox仅持久化 |
| `model/ai_ledger.go` | `ai_requests`、`ai_usage_items`，已有四类图片财务事实 | 增补视频精确幂等作用域及每条事实的归属字段，保持Chat/Image原语义 |
| `model/ai_governance.go` | `ai_compensation_tasks`六态、retry_count、locked_at | 增补version_no、locked_by、attempt_count、completed_at等缺失合同 |
| `repository/image_compensation_repository.go` | 既有共享表的图片类型领取、幂等、人工核对与最多8次重试 | 视频使用独立task_type但同一表，不复用硬编码image_reconcile的方法 |
| `repository/video_*`、`service/video_g4_repository_ledger.go` | Task/Asset/Event/Callback、CAS、执行租约和事务桥接 | 金融状态与执行、交付保持三轴独立；打通调用方事务 |
| `video/gateway.go`与Fake Adapter/Worker | 异步执行、媒体安全、六类资产和恢复 | 拆开“媒体就绪”与“交付可见”，接入结算门禁；不再让执行成功自动交付 |

禁止新建video_wallets、video_usage或平行视频请求/财务账本。已从最新000076之后创建000077增量，目前实现共享请求幂等、Usage归属/追加、原流水与Hold终态基础约束；确认成本、调账、补偿、交付等后续部分仍须逐项实现验证，不能将部分迁移通过视为整个阶段通过。

## 3. 已批准的本地财务合同与事务边界

### 3.1 创建与预占

拟议顺序：可信用户/Project/API Key及模型准入→参数与输入审核→构造生成意图指纹→同一MySQL事务内争取幂等请求→消费Quote→严格Hold与冻结流水→请求钱包关联→创建Task及T2V零输入/I2V唯一输入绑定和租约→held Outbox→提交后才允许Fake Queue/Submit。

事务内必须再次锁定并复核I2V冻结输入、hash/version与状态，不能只相信事务外检查。任何写入点失败都整体回滚；余额不足不得留下Task、Queue、Provider任务或资产。只对死锁、锁等待和CAS冲突有界重试整个事务，候选最多3次，不能重试单个已部分提交步骤，不能把Provider调用放进重试闭包。

固定锁顺序须与既有钱包服务一致：先锁请求/幂等事实，再按统一顺序锁Quote与输入、Hold、钱包，最后处理任务与资产；所有补偿、取消、清理分支须遵守同一规则并进行竞争测试。迁移和原子接口实现必须验证不存在嵌套独立提交。

### 3.2 两种指纹与归属

G2的Quote指纹当前包含owner、API Key和input_asset_id，必须保留原语义，不改写已冻结Quote。G5新增版本化canonical intent fingerprint，不能直接复用该旧指纹。

生成意图必须包含capability、operation、公开逻辑模型、规范化Prompt HMAC、seconds、size/resolution、frame_rate、audio、rights_policy_version，I2V再含规范化输入SHA-256及冻结input version。排除quote_id、upload_session_id、input_asset_id、门面专属字段、Provider任务ID、bucket、object_key、URL和签名参数。

幂等作用域固定为 `(user_id, project_id, command_kind=create_video, idempotency_key)`。同键同意图返回原请求与原终态，不消费新Quote；同键异意图稳定冲突。每次重放先重新鉴权，存储归属不可被JWT或另一SK改写；无权读原结果时返回不泄露存在性的404语义。

现有请求唯一索引只有 `(user_id,idempotency_key)`，不能直接扩大索引而改变Chat/Image。候选是在同一ai_requests表增加可空的命令/视频幂等键摘要字段与视频专用组合唯一约束，视频不占用旧幂等列；旧数据与旧写入路径保持原约束。原始幂等键不应成为Prompt或任意文本的持久化通道。

### 3.3 结算、释放和流水口径

设预占H、实际用户结算S、净释放R，财务终结时 `0 ≤ S ≤ H`、`R=H-S`；尚未终结的剩余冻结额F满足 `H=S+R+F`，不能提前释放F。金额全程Decimal，JSON金额为字符串，MySQL使用DECIMAL；超过Hold不得静默封顶或额外扣款，应进入待对账。

既有严格钱包服务采用“全额解冻H，再消费S”的两条流水，`unfreeze.amount=H`不等于净释放R。若预占前可用余额B、冻结F，则Hold后为 `B-H,F+H`；终结后为 `B-S,F`。解冻和消费的balance_after必须分别重建为B和B-S。对账必须验证这些方向与时点，不能错误断言单条解冻流水金额等于R。

settle/release互斥：若Hold已进入相反终态，协调器必须核验原状态和金额，不能把钱包方法返回旧终态解释为本次请求成功。任何Usage、钱包关联、请求状态、Outbox写入失败，整个财务事务回滚。

### 3.4 追加事实与成本

复用ai_usage_items的 `usage_fact/sale_line/cost_line/adjustment`。每条视频事实记录request_id、task_id、quote_id、user_id、project_id、api_key_id（JWT可为NULL但须与请求一致）、logical_model_code、capability、operation、meter_type、quantity、unit_price、amount、currency、price_version_id、source、sequence_no、created_at。历史Chat/Image缺失字段只做兼容可空增量，不伪造回填。

用户可交付用量与Provider观测量必须分开：用户usage_fact仅对应可交付计量，provider_cost仅来自已确认成本；Provider Usage观测与媒体探测值分别保留在受限的追加事实/资产字段，不能重写其中一个去掩盖冲突。必要TaskEvent扩展只允许版本化低敏字段，不允许原正文。

输出审核拒绝、明确标识失败时用户销售额为0，确定的Provider成本作为平台安全成本保留。成本未知不是成本0；不得自动令成本等于销售额。T2V/I2V分别计量、计价、对账，并验证合计仍为0差异。

## 4. 完整结果矩阵（合同与验收口径）

| 场景 | 钱包/计费 | 执行与交付 | 恢复边界 |
|---|---|---|---|
| 全部成功且可交付 | 冻结Quote与实际规格确认后settled | succeeded；结算提交并通过交付门禁后available | 只能结算/交付一次 |
| 明确失败且无产物 | 全量released，销售0 | failed，不交付 | 保留明确失败事实 |
| 输出审核拒绝 | 全量released，销售0，保留已确认成本 | quarantined、delivery rejected | 不提供下载 |
| 显式或隐式标识失败 | 候选策略为明确失败全量释放；事实不充分则待对账 | quarantined，不交付 | 不伪造标识完成 |
| Provider成功但Fetch/Store/归档失败 | settlement_pending，Hold保持 | pending_reconcile或安全失败，不交付 | 唯一补偿；不重新调用Provider |
| 结算事务失败 | Hold仍可恢复，无重复Usage | 不得available | 仅持久化事实恢复settle |
| 超时、断连、结果未知 | 不自动结算或释放 | pending_reconcile、不交付，输入租约继续保护 | 有界补偿或人工核对，禁止重Submit |
| queued且尚未提交时取消 | 全量释放 | cancelled，不交付 | Submit=0，对账0差异 |
| Provider确认取消且无产物 | 用户销售0、全量释放 | 不交付 | 按确认成本事实记平台成本 |
| Provider拒绝/不支持取消 | 继续Hold | 保留cancel_requested并跟踪原任务 | 不假定免费、不创建第二个Provider任务 |
| 取消后迟到成功 | 满足冻结规则才settle，否则待对账 | 仍经过全部媒体安全与交付门禁 | 不覆盖已释放等相反终态 |
| Provider Usage与媒体规格冲突 | 保持Hold，待对账 | 不交付 | 两份事实都保留，不自动取高值收费 |

## 5. Outbox、补偿与交付

### 5.1 Outbox

只写既有ai_outbox_events，至少包含video_billing_held、video_billing_settled、video_billing_released、video_settlement_pending、video_delivery_available、video_delivery_rejected、video_compensation_required、video_adjustment_recorded。事件唯一键按请求与事件事实确定；必须与对应财务或交付事务一起提交。

payload只允许request_id、状态、Decimal金额字符串、币种、operation和必要版本。不含Prompt、Provider正文、Key、Token、Base64、媒体正文、对象位置或签名URL。`G3OutboxRepository.ClaimBatch`已同时排除video_request聚合与字面量video_事件前缀，包括过期publishing重领；视频dispatcher仍不装配，不靠“没有配置RabbitMQ”猜测安全。

### 5.2 补偿状态矩阵

| 原状态 | 允许动作 | 结果 |
|---|---|---|
| pending/retry且到期 | version_no CAS领取，写locked_by/locked_at并累计尝试 | running |
| running且租约有效 | 仅当前owner/version执行或完成 | completed或有界retry |
| running租约过期 | Claim以新版本重新领取并累计尝试 | running新租约；已达8次则dead，不执行第9次 |
| running且证据不足 | 保持Hold与安全隔离，记录低敏原因 | manual_review或有界retry |
| 第8次尝试失败 | 持久化终止事实 | dead，不自动第9次 |
| completed | 重放只返回原事实 | 不重复写账或交付 |
| dead/manual_review | 独立maker/checker核对 | 不抢占活跃租约，不以重置计数无限重试 |

复用ai_compensation_tasks和唯一task_key，不创建第二套补偿账本。补偿Worker构造边界不得持有Provider提交/查询/内容读取能力，只修复未完成settle、release、delivery或Outbox。无法仅凭已持久化事实修复的归档缺失保持待人工核对，不能偷换成重新Fetch/Submit。

### 5.3 交付门禁

实现分为财务事务和后续交付事务：先成功提交settle及财务事实；之后锁定请求、补偿与六类资产，检查完整安全版本、归属、争议/保全/删除状态和零差异，再一次性使交付可见。交付失败不回滚已提交财务事实，进入唯一补偿。

补偿交付先核验有效租约与准备态对账，再写绑定当前请求版本的发布标记；同事务内追加交付Outbox、推进请求交付、CAS发布六资产、将补偿置completed，随后做全量终态对账并复核最终时钟。任一失败整体回滚本次交付及completed标记。发布标记不是对外放行；最终读取仍须无活动补偿与零差异。

外部可见available必须同时满足：执行succeeded、计费settled、交付available、审核passed、显式与隐式标识applied，三类版本完整不可变，content及五派生资产全部可追溯，无活动争议、无阻断legal hold、media_deleted_at为空，无活动补偿，并且request_id对账0差异。

held、settlement_pending、unquoted、quoted、released、pending_reconcile、审核拒绝/错误、标识失败、隔离/删除各态、Usage冲突、非零差异、补偿未完成、未闭合调账全部拒绝交付。不生成正式下载URL。

## 6. 对账与调账合同

对账逐项覆盖Request、Quote、Hold、冻结/消费/解冻流水、四类Usage事实、Task、TaskInput、OutputAsset、TaskEvent、ProviderCallbackEvent、Compensation与Outbox，不能只比最终余额。

必须证明：请求原结算金额=原sale_line合计=Hold消费金额=消费流水金额；净释放=H-S；流水时点及方向守恒；用户usage_fact=可交付计量；cost_line=Provider确认成本；根及五子资产角色关系正确；held及所选财务终态Outbox各一条且payload一致；没有未闭合adjustment、活动/dead补偿；Submit为1或安全取消/拒绝场景的0。T2V/I2V分别与合计都为0差异，任一不一致均失败关闭。

adjustment仅追加，必须含方向、原因、操作人和不同的复核人。原Sale、Usage、Quote、Hold、钱包流水不重写；原始结算等式继续成立，调账差额单独与新追加的钱包动作建立一一对应等式。仅新增adjustment但缺少匹配钱包动作时，对账不能通过。所有调账仅用合成测试主体，不执行真实调账；同一人兼项目负责人和财务不构成maker/checker分离。

## 7. 开发任务与验收清单

### 功能与开发入口（当前实现）

以下全部是内部Go方法，供协调器、Fake执行器和隔离测试使用，没有用户页面、管理页面、正式HTTP或下载URL。入口均依赖可信归属，不接受客户端声明金额、对象位置或无产物结论。

| 功能 | 核心文件（默认相对token_gateway，迁移另列） | 调用方可观察的合同 |
|---|---|---|
| 创建与预占 | `service/video_billing_reservation.go`、`video_generation_lookup.go`、`video_automatic_reservation.go` | ReserveAndCreate/自动Quote复用同一生成意图；一次原子Hold，重放返回原Task与三轴 |
| 未提交取消 | `service/video_billing_cancel.go` | CancelBeforeSubmit只接受可证明未提交的任务；取消、释放与Outbox共同提交 |
| 成本与Usage | `service/video_provider_cost.go`、`repository/video_usage_repository.go` | 已确认成本独立于销售，事实追加；未知不是0，冲突不能重写原值 |
| 结算/释放 | `service/video_billing_settle.go`、`video_billing_release.go` | SettleReady/ReleaseUnserviceable依据持久化证明形成唯一合法财务终态 |
| 交付/读取 | `service/video_delivery.go`、`video_reconciliation.go`、`video_g4_repository_ledger.go` | 财务先提交，六资产后发布；发布和读取都必须零差异，当前无下载接口 |
| 执行恢复 | `service/video_execution_reconcile.go`、`video_submission_recovery.go`、`video_submission_receipt.go` | 未知与过期只补记原任务及核对事实，不重新Submit、不猜测收费 |
| 财务补偿 | `service/video_compensation_worker.go`、`repository/video_compensation_repository.go` | RunOne只访问数据库，校验版本/租约；最多8次，人工核对不抢活跃租约 |
| 调账 | `service/video_adjustment.go`、`video_adjustment_reconciliation.go` | ApplyAdjustment追加双主体审核、独立资金动作、Usage及Outbox，不修改原账 |
| 数据库约束 | `server/migrations/000077_video_billing_outbox_reconcile.up.sql` | 共享表最小增量，身份/外键/唯一键/状态/追加约束；down保留事实 |

功能使用者为开发、测试和审查人员；生产运行装配不在本阶段。T2V与I2V使用相同财务协调器，差异只在输入冻结、operation与冻结价格/确认成本，不存在平行钱包或Usage表。

测试使用`infra/scripts/verify-video-gateway-migration-000077.sh`，必须显式设置隔离授权；默认all是当前视频实现集合，`compatibility_chat_g4/g5/g6/g7`分别只跑独立旧Chat夹具。运行器禁止外网与拉镜像，指定旧Chat顶层测试必须实际RUN/PASS且不能SKIP。最终阶段验收需结合完整需求清单与同源独立回执，不能仅按脚本退出码判断。

### 当前已验证切片（不等于阶段验收）

以下按开发增量保留历史记录；最新代码切片见[旧Chat兼容与Outbox完整性检查点](./evidence/video-gateway-vid-g5-legacy-chat-outbox-checkpoint.json)。四组旧Chat MySQL、G5-OUT-003的16组反例及独立默认all均已通过；阶段结论见最终同源回执，不将后文历史小切片状态作为当前能力总表。

预占阶段的历史代码清单见[预占阶段检查点](./evidence/video-gateway-vid-g5-reservation-checkpoint.json)。后续源码已经扩展，历史哈希不再代表当前工作树；最新切片清单见[Usage/取消检查点](./evidence/video-gateway-vid-g5-usage-cancel-checkpoint.json)，两者都不是G5最终SOURCE_STATE_ID或完整验收。

- `VideoBillingService.ReserveAndCreate`复用共享Quote、钱包和任务仓储；生成幂等采用独立命名空间，Prompt仅以AES-GCM密文持久化。
- 临时MySQL完整1→77迁移、重复up、保留式down/re-up与Linux race通过。T2V合成Quote/Hold为0.50，I2V为0.75；均不是商业价格或真实钱包资金。
- 相同生成键100并发形成1次创建和99次重放；同钱包100个不同请求全部成功，可用100变50、冻结0变50。
- I2V只有一张冻结参考图；过期、隔离、pending_delete、版本漂移、审核拒绝不能留下任务或Hold，活跃租约阻止清理。
- 用可控时钟模拟Quote锁/钱包锁等待期间过期，先复现4个放行反例，再验证取锁后和提交前复核会整体回滚。请求状态CAS要求恰好更新一行。
- 预占事务单独使用READ COMMITTED，避免旧Hold仓储缺失唯一键的间隙锁与钱包写锁形成死锁；显式锁、唯一约束及CAS仍生效，不改变Chat/Image的事务隔离级别。
- 通用`G3OutboxRepository.ClaimBatch`排除`video_request`；隔离对照测试确认视频事件保持pending且未取租约，旧Chat/Image类型事件仍可领取。未实现视频发布器、未连接RabbitMQ。
- 当前本机`go test ./... -count=1`、`go vet ./...`和`go mod verify`通过；未设置DSN的集成测试会跳过，不能把这些结果当作完整Chat/Image基础设施回归或G5闭环验收。

上述验证后仍须完成下表所有阶段性门禁；特别是结算/释放、确认成本、补偿、交付与逐项对账，不能从预占通过推断已完成。

### 本轮新增：共享Usage与尚未提交时取消

功能用于内部财务协调与非商业测试，无页面或正式HTTP入口。`VideoBillingService.CancelBeforeSubmit`只接受任务ID和既有归属，不接受客户端金额；对reserved/queued且从未提交的任务，在一个事务内完成取消、Hold全额释放、解冻流水、请求关联、零数量/零销售/网关零成本、输入租约释放及released/rejected Outbox。出现submitting、执行尝试、产物、回调或Provider/Bifrost标识时必须拒绝即时释放。

开发文件为`service/video_billing_cancel.go`、`repository/video_usage_repository.go`、`repository/video_submission_proof.go`及`model/ai_video_billing.go`。Usage写入视图复用`ai_usage_items`，由锁定的Task/Request/Quote补全七个归属字段；同事实重放读取原记录、异值冲突，禁止UPDATE/DELETE。取消和网关零成本使用同一未提交证明，数据库触发器同步检查执行历史，不能用“当前Provider ID为空”替代证明。

本切片已在一次性MySQL通过T2V/I2V各100次并发取消、11处财务写入故障整体回滚、Cancel与submitting竞争、原流水不可变、Hold终态不可回退、迟到/额外Usage、错误Outbox金额和未提交零成本的仓储/SQL反例。正常取消只有一次释放；合成初始余额10、冻结0，T2V预占0.50或I2V预占0.75，取消后均恢复10/0，Provider真实调用始终0。

首次取消提交前与幂等重放都逐项核验Usage类型/归属/金额/价格、三条Outbox内容、冻结/解冻流水及输入租约。这个取消专用核验不等同完整17类request_id对账服务，也不证明正常成功结算、结果未知、Provider取消、调账或补偿已经完成。[独立切片核查记录](./evidence/video-gateway-vid-g5-usage-cancel-review.md)中的两处发现已修复并通过反例回归；完整QA/PM/Standards/Spec仍未验收。

### 本轮新增：正常媒体就绪、确认成本与结算

`NewVideoBillingTaskLedger`仍使用G3/G4同一仓储，只启用财务交付模式：Fake媒体完成审核、双标识及五类派生资产后，Task可记录执行成功，但六个资产保持temporary、交付保持pending；held期间不会提前释放I2V输入租约。数据库同样禁止绕过财务门禁将资产改为available。

Fake成功查询提供独立的非商业确认成本。`RecordProviderConfirmation`校验已绑定Provider/任务/operation，将确认事件摘要、Provider计量和cost_line写入同一事务；`ai_gateway_task_events.fact_sha256`及`ai_usage_items.evidence_event_id`建立可复算关联。普通JSON不输出这份Adapter确认；不保存Provider原始正文。确认成本的每秒分母固定为1，Go仓储与MySQL均验证摘要一致性，不使用销售价或Quote成本推测上游账单。

`VideoBillingService.SettleReady`只读已保存的Quote、确认成本及六类安全资产，按实际媒体时长计算用户销售，使用既有严格`SettleHoldTx`一次性写消费/解冻流水、请求关联、用户Usage和销售、settled Outbox及输入租约释放。结算提交前再次按新时钟检查媒体有效期；它不把资产改为available，独立交付仍未实现。内部财务返回不包含Prompt、对象位置或Provider正文。

已验证正常T2V/I2V各100并发只结算一次；8处结算故障保持Hold、保留先前确认成本，财务写入回滚，重试不再Submit。还覆盖状态仓储伪推进、直接INSERT终态、held/settled Outbox异常、事务中媒体过期及确认摘要/分母反例。当前失败只返回错误并保留可恢复事实，唯一补偿任务和pending_reconcile编排尚未接入；不可把局部回滚等同补偿完成。

本轮记录见[正常结算检查点](./evidence/video-gateway-vid-g5-settlement-checkpoint.json)及[独立核查](./evidence/video-gateway-vid-g5-settlement-review.md)。F01/F02仅财务部分得到验证，完整交付与17类事实零差异对账仍未完成，不能标为整条金样PASS。

### 本轮新增：持久化补偿与租约化财务恢复

`VideoCompensationRepository`复用`ai_compensation_tasks`，采用唯一`video:<request_id>`、六态、version_no围栏、2分钟租约和最多8次Worker认领。普通失败指数退避；第8次失败或第8次崩溃后的过期回收均进入dead，不自动第9次。人工认领需要不同且有效的maker/checker，不抢活跃租约；每次认领另写不可变TaskEvent，数据库要求同版本审核事件，不能只填两个用户ID。

正常媒体执行成功后，`SettleReady`失败先回滚财务事务，再以受限数据库事务写settlement_pending、唯一补偿和pending/required Outbox；补记任一步失败一并回滚并返回错误，不伪报已持久化。存在补偿时，`RecoverSettlement`必须携带属于本请求/job的有效租约，并在事务前后复核；跨请求、旧版本或中途过期的租约都不能消费。

`VideoCompensationWorker.RunOne`只依赖数据库财务服务和仓储，没有Provider/抓取/消息客户端。事实足够时恢复结算，不足时有界重试，不猜测收费或释放。正常恢复后保持retry/delivery_pending：统一交付/complete原子协议仍未实现，不能分步绕过门禁。completed要求财务、交付和输入租约闭合，正向测试使用已安全取消并全额释放的请求，而不是仅预占的伪完成。

已验证100并发认领、同worker过期重领、8次失败/崩溃、人工审核历史及SQL旁路、跨请求租约、财务中途租约过期、P/C Outbox补记故障，以及无Provider财务恢复。见[补偿检查点](./evidence/video-gateway-vid-g5-compensation-checkpoint.json)和[独立核查](./evidence/video-gateway-vid-g5-compensation-review.md)。F06交付及完整对账仍未完成，不是整条金样PASS；前面的“尚未接入唯一补偿”历史说明仅适用于上一检查点。

### 本轮新增：统一交付、补偿完成与读取对账

`VideoReconciliationService.Reconcile`对正常成功请求检查17类事实：请求、Quote、Hold、冻结/消费/解冻流水、用户Usage、销售、确认成本、调账、Task、TaskInput、六类产物、三轴事件、Provider回调、补偿及Outbox。钱包按完整流水顺序与当前余额核对，冻结额与未终结Hold合计一致；已有额外Attempt、矛盾事件、错绑回调、未闭合调整、缺失安全版本或生命周期阻断均不能得到通过结论。当前有效调账与其他失败释放矩阵仍待实现，不能由正常成功检查覆盖推断全部场景已完成。

`DeliverReady`只发布，不重复扣费。无补偿时，交付Outbox、请求available与六资产available同事务提交并前后对账。补偿路径`RecoverDelivery`先在有效本请求租约内记录请求目标版本的临时发布标记并升围栏；仅该事务可跨过内部中间态，随后六资产、completed和最终对账全部在同一外层事务完成。任一步失败撤销标记/新围栏/available/completed，保留先前已经提交的结算；重领租约会清除旧发布标记。

首次补偿原因`origin_error_code`冻结不改。纯交付失败只创建required事件，不回退settled，也不伪写settlement_pending；Worker从已存事实恢复后发布并completed，已完成重放再次对账且不产生钱包动作。上述实现替代前面历史检查点中“正常恢复仍retry/delivery_pending”的临时状态。

G5 Ledger对available资产每次读取都运行最终对账，返回前以新时钟检查期限/删除/保全/争议。旧Ledger拒绝任何非NULL新协议身份，不能换构造器绕过G5门禁；旧G4事实保持兼容。没有新增正式HTTP、下载URL或真实对象存储运行。

本轮覆盖正常T2V/I2V各100次交付、财务补偿后发布、纯交付失败恢复、completed重放、11个发布写入点回滚、末尾媒体/租约过期、子资产保全、三类矛盾事实及旧构造器旁路。见[交付/对账检查点](./evidence/video-gateway-vid-g5-delivery-reconciliation-checkpoint.json)与[独立核查](./evidence/video-gateway-vid-g5-delivery-reconciliation-review.md)。完整G5的其他结果矩阵、调账、全部兼容回归及最终验收仍未结束。

### 本轮新增：明确失败与安全拒绝释放

`VideoBillingService.ReleaseUnserviceable(ctx, taskID, owner)`供内部财务编排使用，不接受客户端金额、失败原因或对象位置。Provider明确失败且确认无产物、输出审核拒绝、主视频显式/隐式标识明确失败时，使用原Wallet Hold释放事务追加用户零计量、零销售与R/J Outbox，同时释放输入租约。原Provider计量和确认成本保持不变；审核或标识拒绝的T2V/I2V夹具安全成本分别为0.20/0.30，不是用户费用，也不是真实支出。无正式页面、HTTP或下载入口。

服务锁定Task/Request/Quote/Hold及资产后核对确认成本和原始失败原因。`ai_gateway_task_events.failure_origin`由原执行CAS同事务写入，保持追加式不可修改；审核拒绝只能来自moderating，明确标识失败只能来自labeling。归档失败、派生失败或label_unknown不能通过后补“释放标记”变成可退款事件，通用Append也不能创建保留释放标记。原因不绑定可变资产version，避免后续合法保全改变原始失败证据。

仅有failed或quarantined状态不够。缺少成本确认、未知标识、归档或派生失败仍保留Hold；其完整pending/补偿编排继续在后续结果矩阵补齐。migration 000077仅为video增加“审核passed但任一标识failed可quarantined”条件，图片原隔离约束不放宽。原审核passed不会被改写成error来绕过约束。

释放事务失败后另行原子补记settlement_pending、唯一release_failed补偿及P/C Outbox。`RecoverRelease(ctx, taskID, owner, lease)`只读持久化事实，要求本请求有效围栏；释放、拒绝交付、输入租约、completed及17项最终对账同事务完成，任一失败整体回滚。Worker依据Task终态选择结算或释放，绝不依赖错误码猜测金额，也不持有Provider。主动Provider取消接入、其他结果矩阵和调账仍未完成。

本轮源码、并发/故障结果和边界见[释放检查点](./evidence/video-gateway-vid-g5-release-checkpoint.json)及[独立核查](./evidence/video-gateway-vid-g5-release-review.md)；属于局部开发检查点，不是完整G5验收。

### 取消与提交权扩展（本地内部合同）

`VideoGateway.Cancel`先经`VideoTaskRepository.RequestCancellation`把原Task的`cancel_requested_at`与唯一`cancel_requested`事件同事务写入，只升Task版本，不改变三轴；100个同任务请求只留下一个意图。拒绝/不支持取消保留原Provider任务继续轮询；明确接受的零成本无产物取消复用`ReleaseUnserviceable`，迟到成功仍通过审核、双标识、结算及独立交付。尚未提交的财务取消仍使用`CancelBeforeSubmit`，Gateway不能单独制造已取消但未闭合的财务终态。

提交权以“本次调用亲自赢得submitting CAS”为准，不把别人推进的状态当作本调用获权。取消意图先落库时阻止后续排队/提交；原提交已在途时只记录意图，保留原RPC返回ID的绑定机会。再次调用Submit仅看到submitting时只读返回，不据此臆断Worker已停止；明确超时/ACK未知的已有分支不放宽。过期或崩溃Worker的完整恢复仍待未知结果编排补齐。

Poll与Cancel共用`recordProviderResult`校验绑定、operation、终态及确认。`RecordNoProductOutcome`在同一事务保存成本确认和`provider_no_product_confirmed`摘要事件；零成本、库中暂无资产或后补cancelled状态本身均不等于无产物证据。明确失败无产物也复用该证明。通用事件Append不能伪造这些保留类型，SQL检查同任务零成本确认、摘要及冲突状态。

已观察到产物、非零取消成本、相反确认或在途回执矛盾时，追加低敏`provider_result_conflict`事实，保留原Usage/钱包记录，不覆盖原确认。后到的无产物回执不能抹去冲突；冲突统一阻断新结算、释放、最终对账和继续读取。已形成的相反财务/执行终态不倒写，不能擅自再次收费。完整异常补偿、调账和全阶段验收仍未完成。

本轮测试包括8个T2V/I2V取消结果组合、两种在途取消回复顺序、相反Poll成功回执、14种Cancel/Poll无效或矛盾响应、先退款阻止完整Gateway提交、100并发取消意图及归属隔离、在途Submit重试保留绑定、释放完成点租约过期。结果和源码清单见[取消检查点](./evidence/video-gateway-vid-g5-cancellation-checkpoint.json)及[独立核查](./evidence/video-gateway-vid-g5-cancellation-review.md)，不替代完整G5验收。

### 执行异常、未知与恢复安排

`VideoBillingService.ReconcileExecution`依据持久化Task、资产、确认成本和冲突事实安排恢复，不接收客户端金额或失败原因。未知执行与不可安全释放的失败进入计费settlement_pending，保持Hold及输入租约，并在同一事务中写唯一补偿、P/C Outbox。Ledger的原状态/资产写入、Callback应用及Provider冲突观察均接入这一事务边界。明确可释放失败仍沿用原释放路径；已原子完成的未提交取消经完整财务核验返回not_required，不误建Provider核对。

共享补偿新增冻结的initial_billing_status，在首次状态推进前记录；held/pending来源必须有P/C，settled/released来源只新增C，不回退原财务终态。已有completed/dead/manual_review不重开、不重置次数，只追加人工核对请求并返回review_required；该请求不是实际审核，不伪造maker/checker或宣称新恢复已执行。

断连后的待核对走专用纯数据库入口，整个重读、最新版本CAS、状态、补偿和Outbox共用最多5秒存活上下文，不解密Prompt、不读取参考图、不抓取媒体、不调用Provider。Provider确认6秒但实际媒体5秒时，在进入succeeded前转pending_reconcile，同时保留原确认和完整未交付资产，禁止选择较高值收费。未知补偿8次仍缺证据则dead，资金与输入继续保护。

执行结果与源码清单见[执行待核对检查点](./evidence/video-gateway-vid-g5-execution-reconcile-checkpoint.json)及[独立核查](./evidence/video-gateway-vid-g5-execution-reconcile-review.md)。完整过期提交恢复、调账、对称人工核对场景、全量金额金样及最终阶段验收仍未结束。

### 提交租期与迟到身份补记（本地内部合同）

提交恢复复用唯一不可变的queued→submitting事件，内部租期2分钟不是商业Provider SLA，不使用updated_at或取消时间续租。`RecoverExpiredSubmission`仅操作数据库：未过期返回inflight，过期仍submitting才原子转pending并安排H/P/C，不重新Submit、不解冻。Gateway发起原RPC前核验原claimVersion与deadline，Provider上下文继承原请求取消。

`RecordSubmissionReceipt`用最多5秒纯数据库上下文保存原Request的Provider ID。正常期限内绑定submitted；过期、unknown或已pending只追加provider_task_bound_pending身份事件，保留pending，不视为成本确认或恢复完成。原取消意图增加Task版本不改变原claim；同回执幂等，错claim/请求/Provider及异ID重放拒绝。公开入口拒绝0/1版本；成功但空ID进入未知，不落旧G4分支。

事务取锁后和正常绑定提交前均重新读取时钟；尾部跨期撤销整份绑定、事件与计数，最多三次仅重试原回执数据库事务，不重调Provider。已提交的原回执重放不会因事后到期而倒写submitted或财务终态。

首次回执在同一事务追加`submission_receipt_accepted`，只保存原claimVersion与规范化回执元数据的SHA-256；同ID必须同时匹配原摘要才能幂等返回。已验证原归属与claim的异ID/异状态回执追加`submission_receipt_rejected`后返回冲突，保持原绑定、三轴与钱包事实。拒绝按候选摘要去重，不保存原始正文；无法验证归属、claim或协议形状的输入直接拒绝，不向另一任务写审计。此接口不是轮询状态更新入口。

000077同步约束两类事件的来源、归属、原提交证据、摘要格式、固定唯一键、空前后状态及固定低敏原因。通用事件Append不能伪造接受、拒绝或pending身份记录。回滚只关闭G5装配，不能删掉这些审计事实；历史G5开发检查点不是已部署数据，缺少接受摘要的异常绑定失败关闭，不编造原摘要补平。

基础证据仍见[提交恢复基础检查点](./evidence/video-gateway-vid-g5-submission-checkpoint.json)。本轮增加尾部跨期、真实MySQL锁等待、T2V/I2V真实Gateway配合Fake在途RPC的六种返回顺序、同ID异状态、SQL伪造及财务终态审计测试。默认all隔离MySQL/race通过（248.789秒），全量Go/vet/依赖校验与敏感扫描通过；[补强检查点](./evidence/video-gateway-vid-g5-submission-hardening-checkpoint.json)及[独立局部核查](./evidence/video-gateway-vid-g5-submission-hardening-review.md)绑定同一源码摘要，不能解释为完整G5验收。

### 跨门面生成幂等与自动报价事务

G5显式生成和自动报价生成复用`prepareVideoReservationIntent`与`lookupVideoReservation`：同一user/project/create_video/key先检查当前权限及原规范化意图，返回原Quote、Task和三轴，而不是先生成另一门面的Quote。独立读取使用RR，自动创建的外层RC不能被嵌套savepoint升级，因此最终三轴统一取自单条Task/Request JOIN；返回前按新时钟再次检查权限。新建和重复INSERT竞争后的重放也使用同一边界。

自动门面由`CreateWithAutomaticQuote`协调：锁定归属Project行，事务内重查权限和原生成结果；只有未命中时才使用同事务SQL Quote仓储报价，再进入原子预占。显式创建的Project权限共享锁参与并发裁决。自动Quote、Hold、Task及相关事实共同提交/回滚，Key过期、余额不足或预占故障不能留下自动Quote。最终生成唯一键仍是事实裁决，不用Quote指纹替代生成意图指纹。自动协调能力缺失直接拒绝，不允许包装器静默降级；G2旧协调器显式选择旧合同，不作为G5入口或默认fallback。

原输入重放只读TaskInput冻结hash/version及保留的输入归属元数据，不要求重新读取正文或ready resolver，安全终态后原输入删除不阻断原任务查询。替代输入别名则按公开ID验证当前user/project/key来源归属、ready状态、SHA和版本；不能相信调用方InternalID或相同SHA来跳过归属，也不替换原TaskInput或重建租约。没有正式HTTP路由。

本轮默认all隔离MySQL/race通过（306.021秒），全量Go/vet/依赖、格式与敏感扫描通过，见[跨门面检查点](./evidence/video-gateway-vid-g5-facade-replay-checkpoint.json)与[独立局部核查](./evidence/video-gateway-vid-g5-facade-replay-review.md)。这是局部开发证据，不替代完整G5验收。

### 追加式调账与独立资金动作（实现中）

内部`ApplyAdjustment`使用请求归属、调整序号、方向、低敏原因枚举、Decimal金额和两名不同有效主体。原结算/释放及交付必须已闭合，不能用调账绕过未知结果或未完成补偿。调整不重算Quote、不覆盖销售/成本，不变更原Hold或请求的settled/released终态；新增金额单列核对，不冒充原业务消费。

同一MySQL事务中以原钱包版本CAS修改可用余额，追加refund/in或consume/out流水、共享Usage adjustment及`video_adjustment_recorded` Outbox；任一写点失败整体回滚。冻结额不变，扣款不足拒绝，入账溢出拒绝。同请求同序号同字段返回原调整，异字段冲突。多次调整各有独立序号和Outbox，不覆盖旧事件。

共享Usage增加`adjustment_wallet_transaction_id`唯一外键，只在视频视图使用，旧Chat/Image模型不增加写入列。资金引用必须对应同用户、同钱包、同金额、正确方向类型的新流水，禁止引用原冻结/消费/解冻或别的请求流水；引用后不可改删。仅有调整事实而无钱包动作的异常可保留，但对账必须失败，不提供“手工写一条调整即账平”的捷径。

原业务财务对账与调账检查分开：前者保持原销售、成本、Hold、原流水和事件的逐项核验；后者逐条检查Adjustment、对应资金动作、专属Outbox以及完整钱包余额链。普通生成/结算不持有调账权限，补偿Worker不发起调账。该实现当前仍待完整MySQL反例与独立核查，不代表G5完成。

基础路径已通过默认all隔离MySQL/race（296.912秒）、完整迁移/重复up/保留式down-reup、全量Go/vet/依赖检查和敏感扫描，见[调账检查点](./evidence/video-gateway-vid-g5-adjustment-checkpoint.json)及[独立局部核查](./evidence/video-gateway-vid-g5-adjustment-review.md)。包括同序号100次只修正一次、四写点回滚、T2V/I2V已结算调整，以及100个不同序号扣0.2元竞争10元仅50笔成功。该历史检查点尚未覆盖的完整性边界由下面的后续检查点补充，不将基础回归当作完整G5验收。

后续完整性补强已建立上述剩余反例：覆盖DECIMAL最大合法余额与溢出、精度和范围拒绝；未被任何调整引用的外部钱包新流水仍不能绑定本请求；三方各一条且余额链正确的NULL资金关联仍失败；Outbox各字段及同类型错误数值均阻断对账和重放。对账先读取同request_id的全部调整事件，再检查aggregate_type，不能过滤掉错误类型的额外财务事实。

视频发布器关闭边界同时识别video_request聚合与字面量video_事件前缀，保护pending首次领取及过期publishing重领；被排除记录保持原状态和租约。旧Chat/Image事件正向对照仍可领取，不改变前序顺序规则，不启动RabbitMQ。完整运行结果在本轮检查点单独记录。

完整性补强的默认all隔离MySQL/race通过（310.805秒），含完整1..77迁移、重复up及保留式down/re-up；同源码Go/vet/依赖、格式和敏感扫描通过。见[完整性检查点](./evidence/video-gateway-vid-g5-adjustment-integrity-checkpoint.json)及[独立局部核查](./evidence/video-gateway-vid-g5-adjustment-integrity-review.md)。完整G5的金样汇总、业务兼容及最终验收仍未结束。

### 十二金额金样与独立证据校验

十二种金额金样及F06补偿前、F12人工核对前两个中间快照已落实，见[金额金样文档](./video-gateway-vid-g5-golden-amounts.md)与[原始观察值](./evidence/video-gateway-vid-g5-golden-amounts.json)。未入账字段保持null；F11进入有界retry，F12通过不同合成主体进入manual_review，不把初始pending当成批准的最终样本。

Observer精确检查Usage来源/kind/序号数量、钱包动作数量及总额、资产角色父子关系，防止未闭合案例用“本来对账失败”掩盖额外事实。独立Python校验器验证14个观察值和3组汇总，27种篡改及重复JSON字段测试拒绝异常。当前资金守恒6.25=2.25+2.00+2.00，但两个成本未知请求和四个未闭合请求仍明确保留，不能当作商业闭合或完整G5验收。

最终默认all隔离MySQL/race通过（320.199秒），同源码Go/vet/依赖、格式、Python校验和敏感扫描通过，见[金样检查点](./evidence/video-gateway-vid-g5-goldens-checkpoint.json)及[独立局部复核](./evidence/video-gateway-vid-g5-goldens-review.md)。完整阶段兼容与最终验收仍待完成。

### Chat/Image兼容首轮与完整覆盖审计

五个旧Image隔离脚本已纳入77。69/70/71及既有Image G6 HTTP用例实际通过，G7只执行清理Repository；真实基础设施段保持NOT_RUN。旧Chat31项账务用例已在完整1—77迁移下复用，并补充与G5钱包共存、旧Chat超时扫描不得领取视频、八个跨模态预占/重试入口拒绝测试。

兼容回归发现并修复旧Chat扫描/终结越界及预占入口遗漏；仅省略modality/capability的旧构造器保持默认Chat。旧成本测试改为明确识别两套原有固定夹具，而非根据实际输出反推金额。最新Chat专用隔离/race通过（4.312秒），当前实现集合的G5 all隔离/race通过（322.487秒），同源码Go/vet/依赖、Python、格式和敏感扫描通过。见[兼容检查点](./evidence/video-gateway-vid-g5-compatibility-checkpoint.json)及[局部复核](./evidence/video-gateway-vid-g5-compatibility-review.md)。

[完整覆盖审计](./evidence/video-gateway-vid-g5-spec-coverage-audit.md)仍确认一个P2：缺少直接settle/release同步竞争的明确测试；另需补齐旧Chat G4—G7允许范围内的独立MySQL兼容，不能以Go测试Skip代替。因此尚不满足完整G5验收，继续本地补测，不进入G6。

后续已补齐并验证上述直接财务竞争P2：六组T2V/I2V×成功/失败/queued、每请求100个同步相反入口，严格1写入/49重放/50状态冲突；完整迁移/race通过（26.725秒），逐项资金与17组对账通过，独立源码复核无新增问题。旧Chat G7本机Fake性能门禁也通过。见[终态竞争检查点](./evidence/video-gateway-vid-g5-terminal-race-checkpoint.json)。旧Chat G4—G7的MySQL补测和最终同源验收仍未结束。

以下保留完整阶段的验收范围；实现与局部验证不能缩小原Goal，最终是否通过以同源统一回执为准：

| 编号 | 必须核验的范围 | 证据入口 |
|---|---|---|
| G5-01 | 原子ReserveAndCreate、同事务Quote/Hold/流水/链接/Task/Input租约/held Outbox及写点回滚 | 预占检查点、独立默认all |
| G5-02 | 生成意图、三维身份准入、跨Project幂等、冲突/终态重放、撤权404 | 门面重放检查点、独立默认all |
| G5-03 | 四类追加Usage、销售/确认成本分离、归属字段与T2V/I2V错价防护 | Usage/取消、结算、调账及金样证据 |
| G5-04 | 十二结果矩阵、取消/迟到成功、相反终态、先结算后交付 | 金样、终态竞争、独立默认all |
| G5-05 | 唯一补偿、CAS、租约、八次上限、dead/manual_review及人工竞争 | 补偿、提交恢复及执行核对证据 |
| G5-06 | 逐请求17组对账、全部Outbox、未闭合调账与禁止交付态 | 交付/对账、Outbox完整性及默认all |
| G5-07 | 最小migration/约束、历史事实保留、允许范围内Chat/Image兼容与down/re-up | 隔离MySQL与兼容证据；OFF运行时明确NOT_RUN |
| G5-08 | 源码/金样/对账证据、独立QA/PM/Standards/Spec及零开放P0/P1/P2 | 最终source-state、acceptance及独立回执 |

并发测试至少覆盖：同请求100预占、100结算、100释放、settle/release竞争、同钱包100不同请求；Quote重复/过期/越权、输入hash/version漂移、输入审核拒绝的Hold/Queue/Provider全0、余额不足；每个财务/Outbox写入点故障整体回滚。

故障测试至少覆盖：Store/归档失败、审核拒绝、显式/隐式标识失败、结算/释放失败、available更新失败、补偿首败后成功/8次dead/租约竞争/人工竞争、结果未知、ACK丢失、Provider Usage冲突、queued取消、接受/拒绝取消、迟到成功、maker=checker、调账缺钱包动作及非零对账。

最终运行Billing/Hold/Usage/Outbox/Compensation/Reconciliation单元与Repository测试，隔离MySQL完整迁移、重复up、保留down/re-up、100并发、故障注入、金额金样、Linux race、全量Go/vet/mod verify/gofmt、Python证据脚本、敏感扫描、Chat/Image全量兼容回归。没有执行的检查必须标记NOT_RUN，不得填PASS。

## 8. 回滚、缺陷与交付边界

当前代码仅在本机一次性隔离库验证，未装配到运行服务。migration down必须保留财务、Usage、补偿、Outbox和审计事实；运行态回滚只能关闭视频装配并使用兼容读取器，不DROP账本或删除审计列。

| 项目 | 状态 | 处理 |
|---|---|---|
| FINANCE-CONTRACT-REVIEW | APPROVED，仅本地非商业合同 | 用户已明确批准F1至F5，其他授权不扩展 |
| G5实现与测试 | 以最终同源验收回执为准 | 各财务切片、金额金样、允许范围内兼容与独立默认all已有验证 |
| G5缺陷台账 | 见下表及最终独立回执 | 历史问题保留，不用局部核查替代最终P0/P1/P2评定 |

当前开发回归记录（不替代最终独立缺陷评定）：

| 编号 | 已复现问题 | 本地处理与验证 |
|---|---|---|
| G5-RES-001 | 同键竞争的重复INSERT事务内锁升级/旧快照导致死锁或关联漏读 | 重复INSERT先整体回滚，再用新连接快照读取原事实；100并发通过 |
| G5-RES-002 | 同钱包不同请求的Hold缺失键间隙锁与钱包锁成环 | 仅G5预占使用READ COMMITTED；100个独立请求全部成功、金额守恒 |
| G5-RES-003 | Quote在事务等待期间过期仍放行 | 先复现4个反例；取锁后/提交前复核，所有事实整体回滚，race通过 |
| G5-OUT-001 | 通用发布器会领取只允许保留MySQL事实的视频事件 | 先复现误领取；过滤video_request且保留旧事件领取能力，对照测试通过 |
| G5-CANCEL-001（局部P1） | 取消重放只数零值行，漏掉额外Usage或错误Outbox payload | 独立发现；已改逐项校验且首次提交前复用，迟到Usage/错误金额/首次取消前冲突反例通过 |
| G5-USAGE-001（局部P2） | 网关零成本仓储与SQL不检查已有Attempt或submitting历史 | 独立发现；统一未提交证明并同步SQL，两种路径的仓储与直接SQL四个反例通过 |
| G5-SETTLE-001（局部P1） | 状态仓储可伪推进settled，从而绕过真实扣费 | 真实反例后，数据库要求Hold/link/冻结/解冻/消费动作，且限制G5请求初态 |
| G5-SETTLE-002（局部P1） | 结算重放未核对held、相反事件和payload版本 | 改为完整Outbox集合和六字段逐项核验，异常反例回归 |
| G5-SETTLE-003（局部P1） | 媒体在结算事务中途过期仍被扣款 | 提交前重新校验完整六资产和新时钟，过期整体回滚 |
| G5-SETTLE-004（局部P2） | 确认成本的分母及摘要约束不足 | Go/SQL重算确认摘要并强制每秒分母1，单独验证正确摘要下的错误分母 |
| G5-COMP-001（局部P1） | 第8次崩溃过期回收被普通Finish期限挡住 | 仅放行过期且已达8次的running回收dead；不增加执行权，反例通过 |
| G5-COMP-002（局部P2） | SQL人工租约可省略有效双主体及审核事件 | 强制同版本事件、有效主体及追加历史；反例通过 |
| G5-COMP-003（局部P1） | 可借另一请求的有效租约恢复当前请求 | 绑定request/job并前后核验；跨请求与过期回滚测试通过 |
| G5-COMP-004（局部P1） | 未闭合也可标completed并绕过后续租约 | 仓储/SQL检查闭合事实，completed不能发起新的无租约结算；反例与真实闭合对照通过 |
| G5-RECON-001（局部P1） | 只验证执行事件，漏掉相反财务/交付历史 | 复用三轴矩阵逐条复核并与当前状态对齐 |
| G5-RECON-002（局部P1） | 漏检额外旧驱动Attempt | 提交前及最终对账均拒绝额外执行事实 |
| G5-RECON-003（局部P2） | 回调只核Task归属，不核Provider绑定 | 关联回调必须匹配同一Provider二元标识 |
| G5-READ-001（局部P1） | 换旧Ledger可绕过G5最终门禁 | 按持久化身份拒绝旧构造器降级，不依赖大小写匹配 |
| G5-READ-002（局部P1） | 读取等待期间过期仍沿用入口时钟 | 读取返回前刷新时钟核验已锁定资产 |
| G5-RELEASE-001（局部P1） | 通用追加事件可伪造释放依据 | 移除独立marker，绑定原失败CAS的不可变failure_origin；保留类型、未知/派生失败与伪造marker反例见本轮证据 |
| G5-RELEASE-002（局部P1） | 旧图片quarantine CHECK阻断审核通过但标识失败的视频 | MySQL3819已复现；仅在77为video补充隔离条件，保留图片规则及真实审核结论 |
| G5-SUBMIT-001（局部P1） | CAS输家把别人的submitting/cancelled当作自己获得提交权 | 直接竞争一次提交CAS；完整Gateway与退款竞争、Fake去重前入口计数反例验证 |
| G5-SUBMIT-002（局部P1） | 后到Submit把在途RPC改pending，丢失原返回任务ID | submitting重试只读；在途重试与取消意图后原ID仍可绑定，明确未知响应分支保留 |
| G5-CANCEL-002（局部P1） | Cancel/Poll缺少一致确认门禁，零成本被误当无产物 | 共用确认门禁，成本与无产物摘要同事务；14类反例及后补终态不能退款 |
| G5-CANCEL-003（局部P1） | 两个同成本摘要的在途回复对产物存在性矛盾，后者可掩盖前者 | 原子追加冲突观察；pending/冲突不能补无产物，两种到达顺序均阻断退款 |
| G5-CANCEL-004（局部P1） | 已有冲突仍可新扣费或继续读取已交付媒体 | 确认成本读取统一检查冲突，原财务终态保留但新动作与对账失败关闭 |
| G5-CANCEL-005（局部P1） | 相反成功确认写入失败整体回滚，丢失冲突观察 | 公开确认入口持Task锁，成本子事务回滚后外层提交观察再返回错误；真实在途Poll四个反例覆盖 |
| G5-POLL-001（局部P2） | Provider仍queued被误判failed | 保留本地执行状态，不失败、不回退；定向先红后绿 |
| G5-UNKNOWN-001（局部P1） | 只改执行待核对，漏掉计费、补偿和P/C | 同事务统一恢复编排；四处故障回滚与100重放验证 |
| G5-UNKNOWN-002（局部P1） | 断连补记使用旧ctx或依赖参考图，版本竞争/读取故障会丢事实 | 专用有界纯数据库入口覆盖整个重读/CAS链 |
| G5-UNKNOWN-003（局部P1） | 回调状态与恢复事实不原子，缺证明cancelled未安排核对 | Callback外层同事务接入，缺成本/无产物证明保持冻结 |
| G5-UNKNOWN-004（局部P1） | Provider与媒体计量冲突先进入succeeded | 成功前转pending并保留6秒确认、5秒媒体两份事实 |
| G5-UNKNOWN-005（局部P2） | 已闭合未提交取消被误建Provider补偿 | 核验原网关财务后not_required，不新增job/C |
| G5-SUBMIT-003 | 回执事务尾部跨期仍提交submitted | 先红后补尾部时钟围栏，整体回滚后仅重试迟到回执事务 |
| G5-SUBMIT-004（局部P2） | 异ID拒绝缺审计；新增审计被77摘要白名单阻断 | 追加式低敏拒绝记录，同步专用SQL约束及合法/非法对照 |
| G5-SUBMIT-005（局部P2） | 同ID不同状态静默作为原回执成功重放 | 首次摘要同事务冻结，异摘要拒绝并审计，原三轴与财务终态不变 |
| G5-IDEMPOTENCY-001 | 生成重放或撤销Key请求先写自动Quote | 前置共用生成查询，G5自动Quote与预占同事务 |
| G5-IDEMPOTENCY-002 | 显式重放可用另一用户相同SHA的输入ID绕过归属 | 所有重放共用冻结绑定/替代别名元数据核验 |
| G5-IDEMPOTENCY-003（局部P2） | RC外层嵌套RR无效，返回pending_reconcile/held混合三轴 | 同一条Task/Request JOIN取最终三轴，交错事务反例已复现 |
| G5-IDEMPOTENCY-004（装配风险） | 仅转发Reserve的包装器退回旧报价路径 | 缺少自动协调能力时写入前拒绝，G2显式opt-in |
| G5-ADJUST-001（局部P2） | 额外错误聚合类型的调整Outbox被过滤，对账误报通过 | 先读取请求调整事件全集，再显式核验聚合类型与逐条关联 |
| G5-OUT-002（局部P2） | 错误聚合类型的视频事件可被共享领取器领取 | 聚合类型与字面量video_前缀同时排除；首次/过期领取及Chat/Image对照验证 |
| G5-COMPAT-004/005（局部P2） | 旧Chat扫描、终结或预占重试入口可误读非Chat合同 | 读写入口统一模态隔离，保留旧默认值；31个Chat用例及8个非Chat入口反例通过 |
| G5-SPEC-001（局部P2） | 缺少直接settle/release同步竞争证据 | 六组100并发相反入口、互斥终态、资金守恒与17组对账通过；已关闭 |
| G5-OUT-003（局部P1） | 普通结算/释放对账提前过滤聚合类型，漏掉同请求额外坏事件 | 两条校验路径改为全集读取再核类型；16组反例及独立all（含读取门禁）通过，已关闭 |
| G5-DOC-001（局部P2） | API SSOT仍称取消、未知恢复和调账待实现，与后文内部入口相矛盾 | API与测试计划均已同步；产品经理独立重读确认关闭 |

输出完整功能/开发文档、财务与补偿矩阵、最小人审包、源码与MySQL/金样/对账/独立验收证据后仍只到本地就绪。提交、推送、PR、合并分别等待VID-G5授权；完成后立即停止，不进入VID-G6。
