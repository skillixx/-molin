# VID-G7 基础设施、关闭态与恢复开发合同

## 当前状态与范围

- 状态：`LOCAL_RUNTIME_IMPLEMENTED_PENDING_FINAL_GATES`，不是完整阶段验收通过。
- 基线：`4d80d1cf0966d876c6c2171dce1a337afd2aa05b`。
- 分支：`codex/video-gateway-vid-g7-infra-recovery`。
- G6 进入证据：[最终合并事实](evidence/video-gateway-vid-g6-final-merge.json)。
- 权威需求：[阶段规划 VID-G7](video-gateway-goal-stage-execution-prompt.md#vid-g7rabbitmqminioredis与测试服关闭态)与本次项目负责人 Goal。
- 以下矩阵区分本地实现、测试服和最终Git闭环；`LOCAL_*_PASS`只证明本机隔离环境，不代表完整阶段验收通过。

本阶段让 G6 的视频 HTTP/业务能力具备真实中间件支撑、默认关闭装配、故障恢复和可安全回滚能力。使用者为平台运维和后端开发；不新增产品前端页面，不进入 VID-G8。

## 授权与业务红线

允许本机临时隔离 MySQL、RabbitMQ、Redis、MinIO、监控组件及多个 Go 进程，使用合成账户/资金事实与 Fake 原生异步 Provider。不得接触共享容器、真实钱包、真实 Provider/Key 或生产。

`TEST_SERVER_AUTHORIZATION=NOT_GRANTED`。测试服部署、迁移、重启、备份写入和实际回滚必须等待精确授权包批准。该缺口只限制远端动作，不阻止完成本地实现；本地通过不能替代完整 G7 完成。Git 普通提交/推送/唯一阶段 PR 在本 Goal 范围内已授权，完整阶段未通过不得合并。

## 业务与技术不变量

1. T2V/I2V 复用同一任务、资产、事件、回调、财务账本、队列、Worker 和 Provider hard cap。
2. 模块开关、流量开关、真实 Provider 开关独立且默认关闭。模块关闭不注册视频路由、不读视频凭据、不启动视频或输入清理 Worker，也不签发上传能力。
3. 关闸停止新报价/提交，但保留必要回调、轮询、归档、安全处理、结算和补偿；不得靠删除消息、对象或财务事实实现收口。
4. 复用 G5 Hold/Outbox/补偿和 G6 授权链；Redis 是准入与租约协调层，不能取代 MySQL 持久化事实。
5. Redis 排队容量和可用性在新 Hold/MQ 之前检查；跨系统提交未知保留证据，以账本恢复，不无条件释放或重建任务。
6. Provider Submit 结果未知只查询或待对账；必须以 Fake 任务计数证明不重复创建。Bifrost 视频数据面保持关闭。
7. MinIO 中已绑定输入只读封存版本或新不可变对象；旧上传 URL 覆写不能改变任务输入。清理必须尊重执行租约、争议、隔离、legal hold、保存竞争和父子关系。
8. RabbitMQ 消息只包含冻结低敏标识 `task_id`、`request_id`、`input_asset_id`、`attempt`；不携带 Prompt、媒体、Base64、Key、Provider正文或签名 URL。

## 需求、实现、测试与证据矩阵

`PENDING` 表示尚未形成实现与有效测试证据。子项不能因为所在行的一条路径通过而整体改为 PASS。

| 编号 | 必须交付的结果 | 公共测试边界及必需证据 | 状态 |
|---|---|---|---|
| G7-01 | G6精确源码、Ready CI、五轴验收、merge/main追溯 | Git、GitHub和413项manifest复算；最终合并JSON | PASS_ENTRY |
| G7-02 | 三层默认关闭开关及严格配置校验 | 配置加载、非法值、开关组合与关闭态运行时依赖已接入；模块关闭404、流量关闭503，真实Provider仍不可构造 | LOCAL_PASS |
| G7-03 | Secret绝对普通文件、非链接、0600、用途独立 | 十类配置引用与受限加载已连接，独立32字节容量HMAC密钥、关闭态零读取、Linux 0600/链接/硬链接/FIFO/替换竞争和整包用途隔离均已复验 | LOCAL_SECRET_PASS |
| G7-04 | 视频bootstrap与模块关闭/流量关闭行为 | Linux隔离四依赖运行时已接线：模块关闭路由404，装配但流量关闭稳定503，三个阶段各2个Worker、Outbox、容量恢复与MinIO均已启动；实际OS进程kill恢复通过，测试服仍待授权 | LOCAL_CLOSED_RUNTIME_PASS |
| G7-05 | native_async Fake原生HTTP合同 | 回环Fake Runware形状已覆盖单元素videoInference、getResponse、Range content、delete、429/404/5xx/损坏响应和非回环拒绝；T2V/I2V MySQL+Redis native/Linux通过，外部列表仍读Molin账本 | LOCAL_NATIVE_HTTP_PASS |
| G7-06 | Submit至多一次与结果未知恢复 | Provider taskUUID在RPC前持久化；100并发入口/任务均1；HTTP ACK断连和实际OS进程kill后新Worker只查询/待对账，不重发 | LOCAL_CRASH_PASS |
| G7-07 | RabbitMQ提交/轮询/抓取/延迟/DLQ拓扑 | 消息白名单和18条持久队列、真实隔离Broker路由/死信/延迟、原Task/Request归属重读、受控DLQ恢复、submit/poll/fetch业务处理器及bootstrap均通过 | LOCAL_RABBIT_PASS |
| G7-08 | confirm/mandatory、prefetch与有界消费 | confirm/mandatory、manual ACK、prefetch、有界重试、DLQ、毒消息持久熔断/审计处置和持久化后ACK均已覆盖；重复submit的Provider入口/任务均为1 | LOCAL_RABBIT_DELIVERY_PASS |
| G7-09 | G5 Outbox到异步执行的可靠衔接 | 投影、确认发布、不可路由、ACK丢失、确认写入故障、100认领、真实MySQL→held Outbox→Rabbit submit→poll/fetch→结算→交付→容量释放、迟到消息重放、进程kill及启动恢复均通过 | LOCAL_OUTBOX_RUNTIME_PASS |
| G7-10 | Redis多轴queued/running原子租约 | queued准入、promoting、计划/epoch/发送权、running确认、安全终态release、显式/自动Quote、容量满零Hold/MQ和bootstrap装配均通过 | LOCAL_CAPACITY_PASS |
| G7-11 | Redis故障准入与跨系统恢复 | 完整快照、ready协调、旧epoch进入新epoch、COMMIT/Confirm未知、幽灵租约、permit崩溃、运行时启动与关闭恢复均通过 | LOCAL_RECOVERY_PASS |
| G7-12 | 多实例与Provider共享hard cap | 2/4/8真实Go子进程native/Linux race分别得到2/0、2/2、2/6 running/queued；各进程独立连接/Worker lease且混合T2V/I2V。真实HTTP Provider仍待 | LOCAL_MULTI_PROCESS_PASS |
| G7-13 | MinIO原始/规范化/结果用途隔离 | 真实MinIO输入/输出/保存Store、服务端目标、私有Bucket策略、匿名拒绝、Range、晋级/隔离、保存副本、16并发单赢家及双向孤儿补偿均通过 | LOCAL_MINIO_PASS |
| G7-14 | 有界上传与不可变封存 | MIME/hash/size/session/方法/TTL/If-None-Match、Seal复核、不可变规范化副本、旧URL覆写拒绝、cancel墓碑、漂移拒绝、并发complete及bootstrap均通过 | LOCAL_SEALING_PASS |
| G7-15 | Provider规范化参考图读取 | Adapter只接收冻结ControlledInputRef，由数据库精确版本/hash读取ai-result规范化PNG；HTTP测试逐字节匹配后仅在Provider请求内编码，不进入任务/MQ/日志。provider-scoped URL路径不启用 | LOCAL_BOUNDED_BYTES_PASS |
| G7-16 | 策略化输入/会话/输出留存 | 7天输入留存复用原删除账本；24小时未完成会话先墓碑再追加事实；六项输出父子全部到期后复用原媒体删除账本并保留审核副本 | LOCAL_RETENTION_PASS |
| G7-17 | 应用清理与父子资产保护 | 持久游标避免受保护首页饿死尾页；TaskInput、pending_reconcile、legal hold、争议、保存引用继续失败关闭；输出清理财务快照不变 | LOCAL_CLEANUP_PASS |
| G7-18 | 双向孤儿扫描与补偿 | 000117观察+000119持久分页；支持`vid_`及历史`video_`，保存目标纳入DB→MinIO；缺失Worker有界恢复，孤儿删除有界退避，重启续页与DB回写失败收敛通过 | LOCAL_CLOSED_LOOP_PASS |
| G7-19 | Worker崩溃与租约/队列恢复 | permit消费前崩溃、Provider入口前崩溃及2分钟pending_reconcile已有故障窗；新增真实Go子进程在Provider create后/ACK前被终止，30秒租约后新Worker仍在Provider前失败，native/Linux任务计数均1。Rabbit业务消费者已接线 | LOCAL_CRASH_RECOVERY_PASS |
| G7-20 | 监控、面板与告警 | 组件up/失败/最近成功、队列、容量、Hold、对象容量、清理和补偿指标均为低基数；单依赖失败不抹掉其余指标；10条规则与8面板通过 | LOCAL_MONITORING_PASS |
| G7-21 | 三类回滚与在途任务收口 | 优雅停机等待9类组件；保留pending_reconcile、queued、两个Hold、计划/发送及Rabbit熔断事实；110–122逆序13个Expand-only down后14字段一致并关闭态重启；109列manifest基线、122 expanded基线和down后单RR低敏事实摘要逐字节一致 | LOCAL_ROLLBACK_PASS |
| G7-22 | Chat/Image/G6 SDK/财务兼容 | 锁定Python OpenAI 2.45.0和TS OpenAI 6.39.0真实HTTP/MySQL、独立image schema三项闭环、全库Go回归及Finance 99/99均通过 | LOCAL_COMPAT_PASS |
| G7-23 | 工具链与实际执行门禁 | 本地四依赖runtime、122个migration、119—121双schema重入、13个down、10条规则、8面板、Finance组合22组166/166及绑定式敏感扫描须以最终同源回执为准 | LOCAL_FINAL_GATES_PENDING_REVERIFY |
| G7-24 | 中文功能/开发/配置/安装/轮换/恢复文档 | 功能、开发、API、数据库、测试、监控、回滚及README已同步；最终证据编号冻结后再做一致性复核 | LOCAL_DOCS_READY |
| G7-25 | 源码、测试与独立验收绑定 | SOURCE_STATE、缺陷台账、QA/PM/Standards/Spec/DEV，P0/P1/P2=0 | PENDING |
| G7-26 | 精确测试服授权包 | 已生成主机/服务/端口/卷/镜像/migration/备份/回滚/在途边界；主机指纹、目标镜像digest和备份落点须经获批只读预检回填 | READY_WAIT_IDENTITY |
| G7-27 | 获批测试服关闭态安装和实际回滚 | 独立写入授权、实际安装/回滚证据及Chat/Image/钱包基线 | WAIT_AUTH |
| G7-28 | 唯一阶段PR、精确HEAD Ready CI及合并 | 全阶段门禁通过、无阻塞意见、锁HEAD普通merge和fresh main包含性 | PENDING |

隔离和拒绝输出不进入普通到期删除：`quarantined`、legal hold、open dispute和审核副本承担安全、申诉或法务证据职责，本阶段没有获批自动销毁期限。只有独立审核/法务流程解除保护并形成追加事实后，资产才能进入可删除生命周期；不得为追求清理数量绕过保护。普通`available`输出由持久公平游标清理，长期受保护前缀不会饿死后续可删除任务。

## 实施顺序与代码复用

1. 完整读取Goal要求的基础合同；完成G6补录和需求映射。
2. 配置、Secret和关闭态装配的纵向测试切片。
3. Redis租约准入、RabbitMQ消息/拓扑和现有G5/G6协调器衔接。
4. MinIO存储适配、封存、清理与双向对账。
5. 真实隔离中间件运行、多进程故障注入及监控回滚。
6. 全量兼容、源码冻结、独立终审、测试服授权包。
7. 单独获批后完成远端关闭态验收与最终Git闭环。

优先检查复用位置：`server/internal/config`、`server/internal/bootstrap`、`server/internal/modules/token_gateway/video`、同模块 `service`/`repository`、图片MinIO/RabbitMQ适配及共享Outbox。既有业务实现不因本阶段重新命名或复制为另一账本。新migration编号在实际编写时按最新main核对，不预设或抢占。

## 当前执行记录

2026-09-04提交计划增量：`service/video_submission_plan.go`新增`RecordSubmissionPlan`，供后续G7提交协调器使用，不是HTTP入口或Provider许可。112迁移在原Task增加只写一次的Provider/Request/原claim版本/原Worker代次；首次写入增加一个业务版本并原子追加`video_submission_planned`，同计划只读重放。回执字段和attempt保持不变，容量epoch暂强制NULL。session34808的计划专项Linux race已通过，覆盖65/128字符ID、计划故障回滚、原claim回执及I2V待对账不回退，server哈希d9362d78。当前未接RPC，必须完成ready/promoting及原提交确认后才能接通；100并发、SQL负例和完整运行时仍待验收，G7-06、10、11不因此改为PASS。

回滚边界：112 down保留计划和事件，仅兼容撤回装配；不能抹除可能对应Provider请求的追溯事实。代码不改变G5/G6旧装配，新G7装配不得根据计划为空降级执行。测试入口新增`-Focus submission_plan`；上界恢复专项使用`-Focus capacity_boundary`独立新库，避免耗尽全局门闩污染其他测试。

提交计划后续加固：session17002发现计划后public_id仍能直接SQL改变。112现于首次计划和已有计划两种状态冻结原Task身份及input_json规范规格，字符串逐字节比较，可空Key使用NULL安全比较；不冻结状态、心跳、归档和回执。session82439四项native通过，包括每operation的100并发、SQL负例、两阶段各14种身份/规格篡改以及心跳/回执正例。session16923七项扩展Linux race全部通过，新增归属、根事务、真实COMMIT后丢确认与真实30秒租约尾到期；尾到期用例60.51秒，7/7、SKIP0、清理通过。session22256又将七项与13项原G5提交及两项既有G7围栏合并，22/22 Linux race、SKIP0、清理通过。两轮均绑定server哈希8285b73a，但尚无ready/容量绑定或Provider RPC，不改完整阶段门禁。

持久恢复cutoff增量：旧G6队列门、ClaimRunning、RecordSubmissionPlan、ValidateSubmissionClaim和Gateway的Provider紧前统一门均读取原MySQL门闩。旧schema空状态及111的uninitialized保留兼容；recovering/blocked/未来ready禁止旧路径。锁序统一为Task→guard，恢复Begin只锁guard并先提交形成cutoff；此后新创建在原事务尾回滚、queued不推进、已claim任务在Provider紧前停止。session65089的Linux cutoff专项通过并验证Provider调用0；完整容量恢复回归仍在执行，未发布ready。

Redis阶段化恢复增量：`StageRecovery`只写`rebuilding`，普通Read/Reserve/Prepare/Renew全部失败关闭；`ActivateRecovery`必须匹配同一epoch、policy及逐条identity/nonce/phase。更高epoch可替换完整的旧run_id状态，但带TTL、超前期限、错误hard cap或不完整形状均拒绝且零写。session78608完整Linux十二项通过；修复QA指出的固定键顺序依赖后，session93633恢复Linux四项通过，server哈希97bf78c8。该历史回执当时尚未发布MySQL ready；当前ready协调结果见下段，业务运行时仍未接线。

MySQL完整快照增量：`VideoCapacitySnapshotBuilder.BuildSnapshot`在同一RR只读事务内按Task主键每页50条核对Request、Quote、Hold/Link、钱包流水、Usage、TaskInput、Provider成本、提交事件、Outbox及调账。reserved/queued进入queued；planned pending_reconcile和可信submitted进入running；安全闭合终态逐条验证后排除；未知failed/expired阻断。独立容量密钥按epoch与完整身份为每个Task稳定派生nonce，恢复proof只负责持久恢复授权并逐页续期，固定租期仍为30秒。新密钥源码下session51344 native 116.39秒、session46988 Linux race 44.83秒通过，server哈希53c692bb；测试包含103条完整取消历史、终态调账和Outbox损坏反例，并已由下一段协调器接到Redis/MySQL ready。

MySQL ready与跨系统协调增量：000113在原门闩增加快照SHA-256、数量和ready时间，并在原审计表追加严格绑定且不可修改的ready事件；down保留全部事实。`VideoCapacityRecoveryCoordinator`将完整Build、Redis Stage、MySQL PublishReady及Redis Activate封装为一个深接口。发布前同时核对私有proof、prepared、门闩、实际Redis run_id和完整staged快照；ready重放只读，若前次在Activate执行前退出，则同prepared从DB ready与Redis rebuilding继续收敛。独立容量密钥源码下session54386 native与session98536 Linux race均1/1通过，server哈希53c692bb，覆盖旧prepared配新proof、Prepare后Redis TTL漂移、MySQL COMMIT成功但回执丢失、Redis Activate成功但回执丢失及Activate执行前失败后的幂等恢复。session1266/28590的ready原生/Linux专项还实际模拟生成列已存在而CHECK、索引及三个Trigger缺失，再运行000113恢复为CHECK1、索引1、Trigger3；NULL租期由SQL层拒绝。该切片仍未接入业务准入、提交确认、释放或多进程运行时，不代表G7完成。

容量执行业务协调增量：000114把原计划绑定到首次ready容量epoch，000115增加一次性发送token摘要、Worker代次和开始时间；三项明文permit只驻赢得CAS的进程内存并在消费时清零。`VideoCapacityExecutionCoordinator`按Redis Prepare→MySQL submitting/plan/epoch/send事件→Redis Confirm顺序执行，明确失败才abort回queued，COMMIT未知先用WithoutCancel查明。G7专用Ledger在原claim窗口、当前Task版本、Worker、held财务、三事件、ready和Redis running全部通过后才消费permit。session70631先复现100并发进入Submit 88次但只生成1任务；修复后14f0d284的native/Linux主专项分别session70982/99976，入口1、任务1。历史计划、下一epoch和崩溃收口分别由session65100/26367、65133/32515、1085/49954同源通过。终态释放、新创建queued预留和2/4/8进程仍待实现。

容量运行时组件增量：`VideoCapacityReservationCoordinator`在原财务事务尾部、全部授权与安全复核后预留Redis queued；容量拒绝使Request/Task/Hold/Input/Payload/Outbox整笔回滚。Redis执行后丢回复或后续MySQL失败时先用WithoutCancel查询原Task，只有确定未提交才按同attempt清理。显式Quote、OpenAI自动Quote、COMMIT未知、global=1零财务残留和100并发同意图由session33793/27836 native/Linux通过。`ReleaseTerminal`复用完整终态快照验证，session45067/95205证明安全取消释放与重放，session96157/28025证明pending_reconcile不释放。2/4/8真实进程在session54889/32588/29730及Linux 79068/41420/54140通过，源码1e3ef61a。上述仍未装配bootstrap/Rabbit业务Worker或Fake HTTP。

2026-09-03：已fresh fetch和创建独立分支；G6源码/合并/CI进入门禁通过。当前尚未完成所有跨模块SSOT通读；已实现视频开关配置切片并接入启动前校验，新增Linux受限凭据加载器。完整运行时与中间件尚未装配，不预填通过。

## 配置切片功能与开发说明

使用者为启动API的运维人员。环境模板位于 `infra/.env.example`；配置加载位于 `server/internal/config/video_gateway.go`，主配置通过 `VideoGateway` 字段持有独立视频配置。`bootstrap.NewApp` 在连接基础设施前调用 `ValidateVideoGatewayConfig`。

| 环境变量 | 默认值 | 当前行为 |
|---|---|---|
| VIDEO_GATEWAY_ENABLED | false | 视频模块许可，与Chat/Image独立 |
| VIDEO_GATEWAY_TRAFFIC_ENABLED | false | 新视频流量许可，模块关闭时不能为true |
| REAL_PROVIDER | false | 本阶段视频真实Provider第三层开关，G7无论其他开关如何均拒绝true |
| VIDEO_GATEWAY_LOCAL_FAKE_TEST | false | Fake流量必须显式许可且APP_ENV为test；不能自动回退 |
| VIDEO_EXECUTION_DRIVER | native_async | 启用模块时仅接受G0冻结驱动，不允许Bifrost |

布尔变量未设置时使用关闭默认值；显式设置只接受小写 `true`/`false`（可带首尾空白）。显式空值、1、yes、TRUE及其他字符串返回低敏配置错误，错误不回显原值。视频专用加载函数只查询上述白名单，不读取Bifrost变量或真实Provider Key；全局配置仍可为既有Chat读取自己的Bifrost配置。

模块关闭允许不配置视频驱动和外部依赖。启用模块时，即使流量关闭也必须声明仓库根和十类独立凭据路径；启动前校验缺少、相对、仓库内和复用路径，尚不读取正文。`Config.LoadVideoSecrets`执行同一配置校验后连接Linux加载器，供后续运行时装配消费；模块关闭返回空包且不查询凭据路径环境变量、不读文件。该方法尚未被视频运行时消费，视频路由、Worker、Redis/RabbitMQ/MinIO装配仍须后续实现，不能据此宣称服务可用。

测试边界为公开配置加载/校验和 `NewApp` 启动：默认关闭、8种三层组合、显式Fake/环境/驱动矩阵、非法布尔值、错误脱敏、主配置接线和依赖连接前阻断。对应 `server/internal/config/video_gateway_test.go` 与 `server/internal/bootstrap/video_config_test.go`。Windows执行 `go test ./internal/config ./internal/bootstrap -count=1`；Linux配置race使用锁定Go镜像与本轮临时可执行目录，源码只读且网络关闭。

配置切片回退仅撤回新配置加载/校验接线；不迁移数据库、不触碰钱包、任务、消息或对象事实，不改变Chat/Image已有配置。

## 凭据加载切片

代码位于 `server/internal/config/videosecrets`。公开边界是 `Load(repositoryRoot, files)` 和只返回副本的 `Bundle.Bytes(purpose)`。仓库根路径必须由可信部署配置提供，不是HTTP/MQ输入；运行时接线时仍须在模块开关之后调用。本切片没有读取或配置真实Provider Key，也没有修改图片现有凭据加载器。

Linux加载流程：先验证全部用途和仓库外绝对规范路径，再从根目录逐层通过 `openat` 与 `O_NOFOLLOW` 打开。每个目录均锚定已打开描述符，不在 `Lstat` 检查后重新解析整条路径。最终文件必须为普通文件、单一硬链接、权限不宽于0600、无特殊权限且大小1—8192字节；FIFO采用非阻塞打开后拒绝。对同一个描述符读取前后复核inode、大小、权限、属主、链接数、mtime和ctime，发现变化整包失败关闭。

用途限定为Quote、载荷、回调、管理员原因、下载签名、MinIO访问标识/密钥、RabbitMQ密码及Redis密码。G6管理与下载接口要求独立用途，不能从Prompt/Quote密钥派生复用。重复用途、同一路径、不同文件的相同值均拒绝。载荷/管理员原因AES密钥均为32字节文本；Quote/回调/下载签名至少32字节，MinIO访问标识至少8字节，其余至少16字节。只移除文件末尾CR/LF，不猜测Base64编码；拒绝无效UTF-8、空白和控制字符。服务端应配置独立随机值，不由HTTP请求选择用途。

读取失败不回显原路径、文件正文或操作系统原始错误，不返回部分凭据。凭据包指针和解引用副本的JSON、普通格式化及Go语法格式化均固定脱敏；调用方显式取出的字节只供密码学或连接组件使用，不能写入日志。失败时尽力清除暂存字节，不宣称GC可提供绝对内存擦除。

Windows尚无等价ACL实现，加载返回明确的不支持错误，不能像旧辅助函数一样跳过权限校验；当前本机通过隔离Linux容器验证运行边界。

测试包括0400/0600允许、宽权限/目录/符号链接/父目录链接/仓库根链接/硬链接/FIFO拒绝、大小/文本边界、用途隔离、半包失败、不可变副本、JSON/格式化脱敏，以及100轮文件/符号链接替换与200次读取竞争。发现并修复了值类型副本未进入脱敏方法的缺口，保留红绿回归测试。该结果不能替代后续启动装配、用途轮换和真实中间件集成证据。

十类路径环境变量均在 `infra/.env.example` 中以空引用列出，启用模块后必填：`VIDEO_GATEWAY_QUOTE_SECRET_FILE`、`VIDEO_GATEWAY_PAYLOAD_SECRET_FILE`、`VIDEO_GATEWAY_CALLBACK_SECRET_FILE`、`VIDEO_GATEWAY_ADMIN_REASON_SECRET_FILE`、`VIDEO_GATEWAY_DOWNLOAD_SECRET_FILE`、`VIDEO_GATEWAY_MINIO_ACCESS_KEY_FILE`、`VIDEO_GATEWAY_MINIO_SECRET_KEY_FILE`、`VIDEO_GATEWAY_RABBIT_PASSWORD_FILE`、`VIDEO_GATEWAY_REDIS_PASSWORD_FILE`、`VIDEO_GATEWAY_CAPACITY_SECRET_FILE`。容量密钥必须为独立32字节文本，不得复用Redis密码、恢复token或载荷密钥。仓库/制品根边界使用 `VIDEO_GATEWAY_REPOSITORY_ROOT`，必须来自可信部署而非客户输入。

配置测试将按十类引用重新验证遗漏/相对/仓库内路径、关闭态不读取引用/文件、缺少凭据在基础设施连接前拒绝启动；Linux使用真实0600文件验证用途对应、容量密钥精确32字节、复用/缺失整包失败及模块关闭跳过读取。基础合同通读已补齐G0全文和OpenAPI快照；其余未完成部分保留待办，不以局部阅读代替完整对账。

历史切片状态曾为`LOCAL_INTEGRATION=PARTIAL_RABBITMQ_TRANSPORT`；该状态已由后续完整本地运行时证据取代。测试服关闭态安装与实际回滚仍为`NOT_RUN`，`G7_ACCEPTANCE=INCOMPLETE`，`VID_G8_STARTED=NO`。

## RabbitMQ业务处理与MinIO增量

`VideoRabbitTaskHandler`只接受四字段低敏消息，并从原Task、Request和TaskInput重新解析用户、Project、Key及operation；消息声明不能改变归属。处理器在MySQL Worker租约内复用同一`VideoGateway`，持久状态成功后才发布下一阶段消息并ACK。真实隔离MySQL、Redis、RabbitMQ的native与Linux race专项均为1/1 RUN/PASS；重复submit时Fake Provider入口和实际任务计数都保持1。该结果尚未包含bootstrap进程、真实kill或完整恢复启动演练。

MinIO新增两个生产边界：`MinIOVideoObjectStore`处理视频结果、隔离、Range、保存副本和删除；`MinIOVideoUploadStore`处理预签PUT、原图封存、规范化副本与清理墓碑。对象位置始终来自服务端冻结目标，输出写入先在本机0600临时文件有界流式计算SHA-256，再以`If-None-Match`提交；输入URL签名固定MIME、hash、size、session及不可覆盖条件。取消时先在原键写墓碑，旧URL即使仍在TTL内也不能复活原图。当前native/Linux race两项均RUN/PASS，冻结哈希覆盖整个server；最终SOURCE_STATE仍会随后续bootstrap、孤儿补偿和监控实现重新计算。

该切片当时的分层状态为`LOCAL_INTEGRATION=PARTIAL_RABBIT_REDIS_MINIO`，已由后续14/14运行时和22组166/166组合门禁取代；测试服关闭态安装与实际回滚仍为`NOT_RUN`，`G7_ACCEPTANCE=INCOMPLETE`，`VID_G8_STARTED=NO`。

## RabbitMQ拓扑切片

Redis实现遵循[容量准入与恢复合同](video-gateway-vid-g7-redis-capacity-contract.md)。当前session80555/56671的十四项native/Linux race绑定218f7ee7，包含真实100并发、身份/重放、坏快照、promoting、confirm、release、过期债务及完整恢复；MySQL ready协调另由组合专项通过。这些仍是存储与恢复组件，不是HTTP/Provider运行时闭环。提交计划/终态的MySQL业务证明包装和多进程尚未实现，不能将G7-10至12改为PASS。

[消息与拓扑合同](video-gateway-vid-g7-rabbitmq-contract.md)记录四字段消息、三阶段共享队列、四级延迟、至少一次死信和发布/消费传输协调。真实本机隔离Broker已验证路由恢复、确认异常与ACK边界；仍未接入视频运行时、G3/G5持久化处理器、Outbox或Redis hard cap。

## Outbox领取切片

MySQL持久恢复租约的当前实现和未完成项见[容量恢复代次](video-gateway-vid-g7-capacity-recovery-epoch.md)。111迁移只复用原门闩与审计表，不提供ready，不能用持有恢复证明替代完整账本快照或Provider执行授权。

普通Worker租约切片见[执行租约合同](video-gateway-vid-g7-worker-lease-contract.md)。租约基础、现有归档围栏与Redis容量租约不是同一个许可，不能用其中一个替代其余边界；当前完整业务消费者仍未接线。

[共享Outbox合同](video-gateway-vid-g7-outbox-contract.md)定义显式视频范围、旧Chat/Image领取保护、聚合顺序及秒精度租约防重用。原G5预占/取消事务生成的真实隔离事实用于验证；不新增表或改变价格。运行时发布接线尚未完成，不能仅凭Repository通过宣称MQ业务闭环。

完整合同读取已补齐API、数据库及测试计划，见读取记录。Outbox七项专项的Linux race实际通过且无SKIP，运行前后server源码哈希一致；原默认Go全库、vet、依赖校验和变更敏感扫描通过。独立测试工程师完成本切片只读审查并复核两项缺测关闭，不代替完整阶段QA/PM/工程终审。

新接线发现原G5四条财务校验仍限定Outbox必须pending且无锁，真实MySQL已复现首次结算/退款和T2V/I2V终态重放失败。现按[运输与财务兼容合同](video-gateway-vid-g7-outbox-financial-contract.md)接受有限四态，保留财务全集、金额和归属；大小写身份缺口经反例确认并补精确比较。99项G5与11项G7组合已返回110/110通过；其后严格dead和投影增量由17项Linux race专项覆盖，不把历史99项结果重新绑定新源码。

投影切片历史结果：复用原Task→Request映射并核对原资金/补偿依据及输入历史归属；粗细状态直比、缺业务依据、旧函数名和亚秒夹具问题修复后17/17通过，server源码前后不变。当时尚未接入实际Broker发布，现由下一段增量取代。历史图片导入来源与媒体清理后的独立投影证据仍待补，Task处理器、Redis/MinIO、运行时、独立终审和测试服门禁仍未完成。

后续发布桥接已由`VideoOutboxPublisher`接到原共享Worker与生产确认发布器。19/19同源Linux race通过，其中联合测试包含七个真实MySQL/RabbitMQ场景；恢复保留原Task/事件且七张财务表不变。确认写入故障为GORM边界注入、接管为受控时钟前移，不冒称真实DB断连/kill；测试队列观察者不是业务消费者。Task处理器、Redis/MinIO、完整运行时及测试服仍未完成。具体证据见`video-gateway-vid-g7-outbox-relay-verification.json`。
