# VID-G4：Fake异步执行、媒体处理、安全审核与AI标识

> 阶段：VID-G4
>
> 基线：origin/main@e4e8d34fa7ab016d7dcd89f8a63b6a73c4301e74
>
> 分支：feature/video-gateway-vid-g4-fake-async-media-safety
>
> Git：LOCAL_ONLY
>
> 外部副作用：真实Provider请求0、真实Provider Key 0、真实钱包写入0、费用CNY 0、外部HTTP请求0、测试服写入0、生产操作0

VID-G4让文生视频和图生视频在本地Fake环境中走完整异步执行、媒体探测、审核、双标识和对象存储闭环。它复用VID-G3的Task、InputAsset、TaskInput、OutputAsset、TaskEvent、ProviderCallbackEvent和TaskPayload Repository，不创建平行视频账本。

2026-08-30 CI复核更正：PR #420 的 `77d0be6` 在旧图片隔离门禁暴露了缺少 `000076` 兼容迁移的问题。下方原验收回执属于历史源码快照，不能据此宣称该 PR 的 CI 已通过；本地修复范围、复现和后续验证见[CI兼容修复记录](./video-gateway-vid-g4-ci-compat-fix.md)。

## 1. 当前能做什么

- 后端开发可以通过VideoGateway、Fake Adapter和三个Worker验证原生异步视频协议。
- 测试工程师可以运行输入、媒体、回调、竞争、故障和共享Repository矩阵。
- 安全审查人员可以验证本地无出口、流式有界处理、SSRF阻断、审核和敏感JSON边界。
- 产品经理可以确认Fake成功只表示软件合同成立，不表示商业可用。

页面入口：无。

正式HTTP接口：无。当前没有 /v1/videos、/api/token/videos/* 或前端页面。

真实Provider：无。Runware、Runway和Bifrost视频数据面均未连接。TARGET_PROVIDER_CONTRACT=RUNWARE_RUNWAY_GEN4_5_TASKUUID_5S只用于冻结异步字段语义。

## 2. 运行结构

~~~text
VID-G2 Quote/Reserve
        │
        ▼
VID-G3共享Task + TaskPayload(AES-GCM) + 可选TaskInput
        │
        ▼
SubmitWorker ── FakeAsyncVideoAdapter.Submit ── taskUUID
        │                         │
        │                         ├── ACK已知：只Query恢复
        │                         └── 结果不明：pending_reconcile
        ▼
PollWorker / Provider Callback / Cancel（CAS竞争）
        │
        ▼
AssetFetchWorker
        ├── LocalOnlyMediaFetchPolicy
        ├── ISO-BMFF流式Probe
        ├── Prompt/参考图/输出帧/音轨Fake审核
        ├── 显式+隐式Fake AI标识
        └── FakeVideoObjectStore
                ├── content
                └── cover/preview/thumbnail/moderation_copy/derived
~~~

AssetFetchWorker不是单次直线脚本。它可从`fetching`、`storing`、`moderating`、`labeling`继续，并可在`succeeded`已提交但输入租约尚未释放时补做一次幂等释放。每个阶段只依赖共享Repository事实、确定性对象键和受控Provider任务ID恢复；进程在状态提交后退出不会造成状态永久悬挂。

所有状态推进通过VideoTaskLedger。单元测试使用InMemoryVideoTaskLedger，隔离MySQL验收使用VideoRepositoryTaskLedger桥接VID-G3 Repository。

## 3. 核心文件

| 文件 | 作用 |
|---|---|
| server/internal/modules/token_gateway/video/provider.go | Adapter和Submit/Query/Cancel/Content/Delete合同 |
| server/internal/modules/token_gateway/video/fake_provider.go | taskUUID式Fake异步任务和故障模式 |
| server/internal/modules/token_gateway/video/gateway.go | 执行编排、回调、取消、内容读取和删除 |
| server/internal/modules/token_gateway/video/workers.go | Submit、Poll、Asset Fetch Worker |
| server/internal/modules/token_gateway/video/queue.go | 可恢复的确定性进程内Fake队列 |
| server/internal/modules/token_gateway/video/input_normalizer.go | 参考图格式、元数据、EXIF方向和资源限制 |
| server/internal/modules/token_gateway/video/media_probe.go | ISO-BMFF流式探测、Codec和资源限制 |
| server/internal/modules/token_gateway/video/fetch_policy.go | URL、重定向、SSRF和DNS重绑定阻断 |
| server/internal/modules/token_gateway/video/safety.go | Prompt、参考图、输出帧和音轨Fake审核 |
| server/internal/modules/token_gateway/video/labeler.go | 显式/隐式AI标识与版本 |
| server/internal/modules/token_gateway/video/object_store.go | 临时区、结果区、隔离区及流式对象合同 |
| server/internal/modules/token_gateway/service/video_g4_repository_ledger.go | Worker到VID-G3共享Repository的桥接 |
| server/migrations/000076_video_fake_async_media_safety.up.sql | 审核和双标识版本列及数据库触发器 |
| infra/scripts/verify-video-gateway-migration-000076.sh | 无出口MySQL 1→76、重复迁移和Linux race |

## 4. 接口参考

### 4.1 VideoProviderAdapter

| 方法 | 约束 |
|---|---|
| Submit | 接收request_id、operation、内存Prompt、受控输入引用和规格；返回taskUUID |
| Query | 返回queued/processing/succeeded/failed/cancelled/unknown |
| Cancel | 幂等取消，相反终态不覆盖 |
| OpenContent | 返回ReaderAt和大小，不返回URL或Base64 |
| Delete | 删除不存在内容幂等 |

T2V的Input必须为空。I2V的Input必须且只能是一个vin_*内部资产快照，包含SHA-256和version，不接受URL、bucket或object_key。

### 4.2 VideoGateway

| 方法 | 行为 |
|---|---|
| Submit | 输入复核，推进reserved/queued/submitting，调用Fake Submit并绑定Provider任务 |
| Poll | 只对submitted/processing执行Query并向前推进 |
| HandleCallback | 验签、三元去重、body哈希冲突检查和CAS应用 |
| Cancel | 与Poll/Callback竞争，安全终态只选一个 |
| FetchAndFinalize | 内容读取、Probe、存储、审核、标识、派生资产和终态收敛 |
| Query | 读取低敏任务事实 |
| ReadContent | 仅对succeeded、available且未删除资产开放受控Range |
| DeleteContent | 删除六类正文，保留hash、规格、父子关系和审计事实 |

### 4.3 Worker和队列

- Submit、Poll和Asset Fetch Worker均可重复投递。
- DeterministicTaskQueue以task_id幂等入队。
- 同一时刻只允许一个Worker持有租约。
- Worker崩溃后RecoverExpired恢复过期租约，attempt递增。
- Asset Fetch在`storing/moderating/labeling/succeeded`各提交后崩溃均有故障注入测试，重投最终收敛到一个成功终态且租约只释放一次。
- ACK后任务不再领取。
- 本阶段不接RabbitMQ、Redis或Outbox。

## 5. ACK丢失与结果未知

| 情况 | 网关行为 |
|---|---|
| ACK丢失但已知provider_task_id | 进入submitted，后续只能Query，禁止再次Submit |
| ACK丢失且无法证明provider_task_id | 进入pending_reconcile |
| Submit或Query结果未知 | 进入pending_reconcile |
| pending_reconcile | 不交付、不释放输入租约、不自动重提 |
| 明确失败或取消 | 进入安全终态，真实租约满足计费条件时只释放一次 |

Fake Adapter提供success、explicit_failure、provider_cancelled、submit_timeout、query_timeout、fetch_timeout、result_unknown、corrupt_result、ack_lost_known_task和ack_lost_unknown_task模式。

## 6. 参考图安全处理

ReferenceImageNormalizer仅接受PNG/JPEG，并同时核对扩展名、声明MIME和真实魔数。

阶段边界：VID-G4执行主链从VID-G3已经形成的`ready`规范化私有InputAsset和冻结TaskInput开始，不实现上传HTTP入口。为验证两阶段能够衔接，本阶段另有一条本地集成测试执行“原始JPEG→Normalizer无元数据PNG→冻结SHA/version→Gateway Provider前复核→Provider只接收ControlledInputRef”。

拒绝：

- SVG、HTML、GIF/APNG、polyglot、尾随正文、截断和损坏文件。
- 超大文件、宽高越界、像素炸弹、整数溢出和宽高比越界。
- 超大EXIF、GPS、定位文本、异常ICC和超限文本元数据。
- MIME、扩展名、魔数不一致。
- 上下文取消或超过MaxDecodeDuration。
- JPEG真实EOI后的任何尾随正文，即使攻击者再附加伪EOI；多个APP1/APP2段按EXIF/ICC总量累计。

规范化：

- 解析JPEG EXIF orientation 1-8并旋转像素。
- 完整解码后重编码为PNG，删除EXIF、GPS、XMP、文本块和ICC。
- 分别记录原图和规范化副本SHA-256。
- 不使用临时磁盘，TempDiskBytes=0；MaxTempDiskBytes仍是显式配置边界。
- Provider只看到ControlledInputRef，看不到正文、对象位置或签名URL。
- 阻塞读取使用最多4个受控工作槽；合作来源在超时时被关闭并等待退出，不合作来源继续占用固定槽位，第5个请求立即失败关闭，因此资源不会随恶意请求无限增长。图片解码、方向旋转和PNG编码在受控子进程执行，`CommandContext`到期会强制终止并回收整个CPU工作实体；测试以2秒故意延迟证明150ms deadline生效且后续正常解码不受影响。

## 7. 视频流式探测

VideoMediaProbe要求：

- MIME必须为video/mp4，来源必须是ControlledContentRef。
- 顶层必须包含合法ftyp、有界moov和mdat。
- 从mvhd、tkhd、hdlr、stsd和stts读取时长、宽高、Codec、音轨和帧率。
- 只缓冲有界box和64KiB哈希缓冲区，不把完整视频加载进内存。
- 流式计算完整SHA-256。
- 检查文件、box、时长、宽高、帧率、Codec、Range、执行时间和box数量。
- Range模式必须严格等于`supported`，单次对象Range最大1MiB。探测读取同样最多占用4个受控工作槽，不合作Cancel不能造成无界goroutine增长。

LocalOnlyMediaFetchPolicy在读取前拒绝外部URL、重定向、私网地址和DNS解析漂移，因此EXTERNAL_HTTP_REQUESTS=0。

## 8. 审核与AI标识

T2V执行Prompt、首帧、尾帧、固定间隔帧、场景切换帧和音轨审核。

I2V增加参考图OCR、视觉分类、二维码、图片文字和元数据审核。

所有审核器都是确定性Fake。拒绝或错误均不能进入available。

VideoAILabeler同时写入显式和隐式标识状态及版本。任一标识失败，任务失败且资产进入隔离。

## 9. 对象与资产树

| 区域 | bucket |
|---|---|
| 临时区 | video-temp |
| 结果区 | video-result |
| 隔离区 | video-quarantine |

对象键由task_id/asset_id/role.bin生成。调用方不能传bucket、object_key、URL或签名参数。同键同内容幂等，同键不同内容冲突。

~~~text
content（MP4，根）
├── cover
├── preview
├── thumbnail
├── moderation_copy
└── derived
~~~

五类子资产必须引用content父资产，并各自完成Probe（视频角色）、审核和显式/隐式双标识。派生资产先全部完成安全判断，再写入临时区；全部成功后才逐个晋升结果区。任一中途失败会清理确定性临时/结果键并隔离根资产，不留下无账本结果对象。

删除采用两阶段合同：Repository先原子把六类资产推进到`deleting`，法律保全或争议会在触碰对象正文前失败；对象删除完成后再原子记录`deleted`，故障则记录`delete_failed`供安全重试。删除正文后hash、规格、父子关系、请求、Quote和审计事实继续保留。

## 10. 数据库增量

Migration 000076只向共享ai_gateway_assets增加moderation_policy_version、explicit_label_version和implicit_label_version。

触发器要求新形成的视频审核或标识结果必须带版本；从非available进入available时必须同时满足审核通过和双标识完成。旧阶段事实不伪造回填。

TaskEvent白名单增加provider_bound、input_validated、media_probed、moderation_passed、label_applied和lease_released。自由文本、message/data和未知键仍被拒绝。

## 11. 本地验收 How-to

前置条件：

- 本机已有mysql:8.0和golang:1.25-bookworm镜像。
- 禁止容器拉取，脚本使用--pull=never。
- Docker网络为--internal，MySQL无宿主端口，数据目录为tmpfs。

Windows全量回归：

~~~powershell
cd D:\molingproject\molin-gateway-worktree\server
go test ./... -count=1
go vet ./...
go mod verify
~~~

隔离MySQL和Linux race：

~~~powershell
cd D:\molingproject\molin-gateway-worktree
$env:VIDEO_GATEWAY_G4_ISOLATED_MYSQL_APPROVED='YES'
& 'C:\Program Files\Git\bin\bash.exe' ./infra/scripts/verify-video-gateway-migration-000076.sh
Remove-Item Env:VIDEO_GATEWAY_G4_ISOLATED_MYSQL_APPROVED
~~~

预期末行包含：

~~~text
VIDEO_G4_MYSQL=PASS ... full_chain_1_to_76=true ... external_http_requests=0 provider_calls=0 provider_keys=0 real_wallet_writes=0 cost_cny=0
~~~

故障处理：

- APPROVAL_REQUIRED表示没有选择一次性MySQL合同测试，不表示代码失败。
- docker_missing时启动Docker Desktop后重试。
- --pull=never失败时不得自动拉取，需先完成供应链审查。
- pending_reconcile不能通过重新Submit修复。

## 12. 回滚边界

- 应用层回滚：停止本地Fake Worker和G4装配。
- Migration down为保留式SELECT 1，不删除审核、标识、回调、任务或资产事实。
- 不允许删除或覆盖TaskEvent、Callback、TaskPayload、TaskInput或媒体审计字段。
- 本阶段没有测试服或生产部署，因此不存在远端运行时回滚。

## 13. 缺陷台账

全部条目均由独立QA、产品或Spec审查提出，状态只在原故障测试和相关回归通过后改为`CLOSED_VERIFIED`。最终复核绑定[`video-gateway-vid-g4-source-state.json`](./evidence/video-gateway-vid-g4-source-state.json)。

| ID | 级别 | 状态 | 根因与修复 | 回归证据 |
|---|---|---|---|---|
| VID-G4-001 | P1 | CLOSED_VERIFIED | 输入审核位于Submit之后；拆分Preflight并在Provider前验证冻结规范化快照 | unsafe input、hash漂移均证明SubmitCalls=0 |
| VID-G4-002 | P1 | CLOSED_VERIFIED | submitting恢复可能重提；无法证明结果时只进入pending_reconcile | ACK丢失与提交后恢复测试 |
| VID-G4-003 | P1 | CLOSED_VERIFIED | 成功Callback缺少Content；按provider_task_id重建受控句柄 | Callback→Fetch完整闭环 |
| VID-G4-004 | P1 | CLOSED_VERIFIED | 错绑Callback可能先应用其他任务；记录命令增加ExpectedTask/Owner并先比对 | MySQL错绑状态不变测试 |
| VID-G4-005 | P1 | CLOSED_VERIFIED | 状态与资产写入分属事务；Advance改为共享GORM事务 | 资产注入失败时状态/事件/资产全回滚 |
| VID-G4-006 | P1 | CLOSED_VERIFIED | 审核/标识失败未持久化隔离位置和状态；补审核、标识、对象位置CAS | 拒绝/失败隔离测试 |
| VID-G4-007 | P1 | CLOSED_VERIFIED | 对象Put和Probe存在完整正文读取；改为64KiB分块与有界Range | PeakBuffer、1MiB Range上限测试 |
| VID-G4-008 | P1 | CLOSED_VERIFIED | JPEG遇SOS即接受且元数据逐段限额；解析真实EOI并累计APP1/APP2 | 尾随正文+伪EOI、聚合ICC测试 |
| VID-G4-009 | P1 | CLOSED_VERIFIED | RangeMode只拒绝invalid；改为只允许supported | Range严格白名单矩阵 |
| VID-G4-010 | P1 | CLOSED_VERIFIED | 派生角色复用根安全结论；六类资产分别Probe/审核/双标识 | Labeler调用6次和派生视频Probe测试 |
| VID-G4-011 | P1 | CLOSED_VERIFIED | 队列只验证领取恢复；补过期租约后实际运行Gateway Worker | crash→recover→Run→Ack测试 |
| VID-G4-012 | P1 | CLOSED_VERIFIED | 删除先删对象再写DB，legal hold可能失效；改为Prepare/Complete两阶段 | legal hold正文保留、delete_failed重试、六事实保留 |
| VID-G4-013 | P1 | CLOSED_VERIFIED | Fetch只接受fetching导致执行中崩溃永久卡住；按五阶段幂等恢复 | storing/moderating/labeling/succeeded提交后崩溃矩阵 |
| VID-G4-014 | P1 | CLOSED_VERIFIED | 派生逐项直写结果区形成孤儿；先全量安全判断、临时写、统一晋升/清理 | 第N次Put失败无temp/result孤儿 |
| VID-G4-015 | P1 | CLOSED_VERIFIED | 超时放弃等待可无界泄漏goroutine；引入4槽上限并让超时任务继续占槽 | 不合作Reader/ReaderAt第5次立即失败且槽可回收 |
| VID-G4-016 | P2 | CLOSED_VERIFIED | Normalizer与执行链证据分离；补原图到冻结引用的本地衔接测试并明确G3边界 | Normalizer→snapshot→Submit Preflight测试 |
| VID-G4-017 | P2 | CLOSED_VERIFIED | T2V零TaskInput被释放门禁误拦；零绑定释放幂等成功 | T2V Repository闭环 |
| VID-G4-018 | P1 | CLOSED_VERIFIED | 终态I2V读取误用执行前租约校验；活跃态强复核、终态只读冻结绑定 | I2V成功与租约一次释放 |
| VID-G4-019 | P1 | CLOSED_VERIFIED | 进程内同步图片解码无法在CPU deadline处中断；迁移到当前可执行文件的受控子进程并由CommandContext强制终止 | 故意延迟2秒的解码进程在150ms超时并被回收，随后正常任务成功 |
| VID-G4-020 | P1 | CLOSED_VERIFIED | 显式标识或派生失败留下pending事实，使Repository隔离事务回滚；所有失败分支写入带版本的完整失败事实并优先返回持久化错误 | 显式/隐式标识失败与派生第N次Put失败均收敛failed+quarantined |
| VID-G4-021 | P1 | CLOSED_VERIFIED | failed/cancelled后的租约释放错误被忽略且终态重投不补偿；释放错误改为优先暴露，所有Worker终态重投补做幂等释放 | I2V首次释放注入失败、重投后恰好释放一次 |
| VID-G4-022 | P1 | CLOSED_VERIFIED | 000076只校验首次形成安全事实，允许后续回退或清空版本；UPDATE触发器冻结三类非pending状态及版本 | 六种直接SQL篡改全部失败且原值不变 |
| VID-G4-023 | P2 | CLOSED_VERIFIED | 三个Go文件机械格式未统一 | 全量gofmt后`gofmt -d`与git diff检查为空 |
| VID-G4-024 | P2 | CLOSED_VERIFIED | 000076保留式down脚本末尾存在多余空行，导致提交前`git diff --check`告警；删除空行且不改变SQL语义 | `git diff --check`重新执行无输出，Migration静态测试与全量Go回归通过 |
| VID-G4-025 | P2 | CLOSED_LOCAL_VERIFIED | 当前共享资产模型包含三类安全版本字段，但五份旧图片隔离脚本漏装000076；补齐兼容层并将MySQL非1062错误独立报告 | 原MySQL1054红灯与修复后100并发绿灯、六个图片门禁及VID-G4四包race均已验证；未推送，远程CI待复验 |

VID-G4-001至024的原验收开放缺陷为`P0=0、P1=0、P2=0`；新增VID-G4-025的当前状态以上表及CI兼容修复记录为准，不能用原快照覆盖新发现。

## 14. 明确未完成

- 没有正式视频HTTP接口或前端页面。
- 没有真实Runware、Runway或其他Provider。
- 没有真实Bifrost视频数据面。
- 没有真实RabbitMQ、Redis或MinIO运行证明。
- 没有真实钱包结算、补偿、Outbox或零差异对账。
- 没有测试服或生产部署。
- Fake成功不能解释为商业可用。
- VID-G5未开始。

## 15. 相关文档

- [VID-G3任务、资产与事件](./video-gateway-vid-g3-task-asset-events.md)
- [完整API设计](./full-api-design.md)
- [数据库设计](./database-schema-design.md)
- [测试计划](./test-plan.md)

## 16. 最终门禁

- 可复算源码快照：[`video-gateway-vid-g4-source-state.json`](./evidence/video-gateway-vid-g4-source-state.json)
- 隔离集成与零副作用：[`video-gateway-vid-g4-isolated-integration.json`](./evidence/video-gateway-vid-g4-isolated-integration.json)
- MySQL与Linux race：[`video-gateway-vid-g4-mysql-contract.json`](./evidence/video-gateway-vid-g4-mysql-contract.json)
- 媒体安全矩阵：[`video-gateway-vid-g4-media-safety-matrix.json`](./evidence/video-gateway-vid-g4-media-safety-matrix.json)
- 独立QA、产品、Standards与Spec：[`video-gateway-vid-g4-independent-reviews.md`](./evidence/video-gateway-vid-g4-independent-reviews.md)
- 阶段验收汇总：[`video-gateway-vid-g4-acceptance.json`](./evidence/video-gateway-vid-g4-acceptance.json)

`SOURCE_STATE_ID`以源码快照文件为唯一值；测试和独立复核必须引用同一值。验收与独立复核文件属于生成型回执，为避免自引用递归，不参与该源码hash，其余G4代码、测试、migration、脚本、主文档和技术证据均参与计算。
