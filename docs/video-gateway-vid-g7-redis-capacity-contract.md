# VID-G7 Redis 容量准入与恢复合同

状态：`LOCAL_IMPLEMENTATION_AND_MULTI_PROCESS_VERIFIED`。Redis/MySQL接线、Task→guard锁序、恢复epoch、完整快照、queued/running租约及2/4/8独立进程混合T2V/I2V hard cap已在本机隔离环境通过；这不表示测试服已安装，也不改变真实Provider、钱包和测试服写入均为0的边界。

## 功能与现状

本模块为视频API和后台Worker协调排队、运行与Provider容量，防止多个实例或T2V/I2V混合请求突破同一个上限。客户通过原视频API调用，不新增产品页面或另一套任务、消息、财务账本。

当前已有实现：

- `service/video_queue_admission.go`：G6创建事务尾部，以原`ai_video_queue_admission_guard`串行检查原Task数量，用户2、Project10、全局100。
- `service/video_running_admission.go`：G6从queued进入submitting时，以原门闩检查用户1、Project2、模型2。
- `service/video_billing_reservation.go`：原Request/生成意图先裁决重放，再验证Quote、权利和输入，原Hold/Task/Event/Outbox同事务提交。
- `service/resource_limiter.go`：既有Chat资源治理使用并发/RPM/TPM租约，会在准入时清理到期成员。其同步请求语义不能直接作为视频未知执行的释放依据；本阶段不得改变Chat/Image的既有合同。
- 普通Worker执行权由[执行租约合同](video-gateway-vid-g7-worker-lease-contract.md)定义，Redis容量许可不能替代执行、归档或补偿证明。

特别注意：G6的`videoRunningStatuses`只包括submitting至labeling，不含pending_reconcile。不能据此宣称已有MySQL查询保护了未知Provider的hard cap；G7恢复和Provider容量校验必须单独覆盖提交未知及未取得可靠结束证据的任务。

## 关闭态策略参考

以下是闭合工程默认值，不是生产容量或商业SLA。允许范围内收紧，不得通过数据库策略、进程配置、路由别名或operation放宽。

| 维度 | queued默认上限 | running默认上限 | 依据与行为 |
|---|---:|---:|---|
| User | 2 | 1 | G0冻结；跨Project/Key合计 |
| Project | 10 | 2 | G0冻结；精确归属，不由客户端指定其他主体 |
| API Key | 2 | 1 | G7补齐维度，不高于原用户总限额 |
| Model | 100 | 2 | queued不高于全局100，running沿用G0 |
| 全局 | 100 | 不另造新政策值 | queued沿用G0；运行仍同时受各主体/模型/Provider限制 |
| Provider | 不另造新queued政策值 | 2 | 同一供应商所有operation和路由别名共用 |

API Key使用原数据库ID，绝不使用SK明文。JWT任务没有API Key时，采用带类型标识的稳定`user+project`主体，不把所有JWT并入全局key=0，也不能按Token、登录次数或请求随机生成桶。所有上层桶同时保留。Key/model queued独立有效性的测试仅通过收紧该轴证明，不能把User/Global拒绝冒称该轴生效。

策略版本在同一部署域内必须一致。收紧配置不能驱逐已占用任务；新准入停止，直到占用低于新上限。

## 内部数据与接口边界

### 已实现的策略参考

`server/internal/modules/token_gateway/video/capacity_policy.go`目前只实现纯策略校验，不连接Redis或修改Task/钱包：

- `DefaultVideoCapacityLimits() VideoCapacityLimits`返回上述queued/running十个`uint32`上限的独立值副本。
- `NewVideoCapacityPolicy(VideoCapacityLimits) (*VideoCapacityPolicy, error)`要求每个值在1至对应冻结上限之间，零值和放宽均整份拒绝，返回`ErrVideoCapacityPolicy`，不补默认、不静默截断。
- `(*VideoCapacityPolicy).Limits() (VideoCapacityLimits, error)`返回不可改写原策略的值副本；nil和零对象拒绝。
- `(*VideoCapacityPolicy).Fingerprint() (string, error)`对固定版本前缀和十轴固定顺序计算SHA-256；它是配置一致性标识，不是容量、执行或恢复许可。

默认策略的无换行规范文本为`video-capacity-v1|2|10|2|100|100|1|2|1|2|2`，指纹为`1d489742b370cb9cf8f4a82dca5051ed04f3afdb73b398ac5fbf7c92d1e29734`。queued顺序为User/Project/APIKey/Model/Global，running顺序为User/Project/APIKey/Model/Provider。

在`server`目录运行`go test ./internal/modules/token_gateway/video -run '^TestVideoG7CapacityPolicy' -count=1`，两项测试覆盖默认值、固定向量、值副本、空对象、十轴零/放宽拒绝、合法收紧及每个可收紧维度影响指纹。已先记录未实现构造器拒绝合法策略的RED，再实现并验证GREEN；session18726的默认环境Go全库/vet/mod verify通过，session99229的锁定Linux Go镜像、network=none、两项race通过，前后server哈希99d3553e不变且清理通过。该单元结果不证明Redis、多进程或跨系统恢复已实现。

### 已实现的Redis存储组件

代码位于`service/video_redis_capacity.go`，嵌入脚本位于`service/video_redis_capacity.lua`。它只提供容量快照存储，不查询MySQL，不授予调用Provider、更新Task或修改钱包的权限，目前未接到运行时。

| 接口 | 当前行为 |
|---|---|
| `NewVideoCapacityAttempt(VideoCapacityIdentity)` | 校验Task/Request、非零uint64主体、可空Key、规范化模型字符串、冻结Fake Provider与operation；立即冻结身份JSON和随机尝试nonce |
| `NewRedisVideoCapacityStore(*redis.Client, uint64, *VideoCapacityPolicy)` | 要求非零预期epoch、禁用客户端自动重试并遵守context；不创建ready、不初始化空库 |
| `ReserveQueued(context.Context, *VideoCapacityAttempt)` | 原子预留五个queued维度；同尝试只读重放、不续期，旧nonce或异身份拒绝 |
| `PrepareRunning(context.Context, *VideoCapacityAttempt)` | 一次核验全部running维度，转为promoting并保留queued；重复准备不续期、不重复计数 |
| 包内`confirmRunning(context.Context, *VideoCapacityAttempt)` | 仅把完全匹配且未过期的promoting原子转running，移除queued计数并刷新技术租期；同running重放零写 |
| 包内`releaseCapacity(context.Context, *VideoCapacityAttempt)` | 仅移除完全匹配记录；不存在记录只表示当前无占用，业务安全终态必须由上层MySQL协调器证明 |
| `Renew(context.Context, *VideoCapacityAttempt)` | 仅有效原nonce/epoch续期；不改变阶段，不释放过期业务占用 |
| `Read(context.Context, *VideoCapacityAttempt)` | 返回低敏阶段、截止/观察时间及是否过期；不是授权证明 |

当前ready发布和完整重建由独立恢复协调器完成。Redis组件已有包内confirm/release原子动作，但它们不判断Task、Provider或财务终态；不存在记录时的release结果也不证明历史调用身份。数据库提交证明和释放依据必须由后续业务协调器包装，完成前不提供公开调用面。测试直接写入已知初始快照的部分仍只证明Redis组件行为，不能单独当作业务运行时验收。

多进程不能依赖恢复进程内的临时proof生成容量nonce。当前新增`VideoCapacityNonceKey`，从独立32字节仓库外密钥按HMAC-SHA256稳定派生，输入绑定schema域、epoch及完整规范身份；同密钥的2/4/8进程可重建同一能力，不落库、不进普通输出。恢复Builder已改为必须显式持有该密钥，不再从临时恢复proof派生；配置用途与Redis密码、载荷密钥分别加载。运行时装配、真实多进程及下述轮换流程实测仍待完成。

容量密钥禁止在同一epoch热替换。轮换必须先关闸并暂停新的容量修改，保留回调、Provider查询和持久事实；所有旧Worker退出容量写入后，以新密钥开启严格递增的新恢复epoch，从完整MySQL账本重新派生全部活动nonce，依次完成Stage、MySQL ready和Activate。随后2/4/8个新进程必须从同一个受限文件加载新密钥并只接受新epoch，才可恢复Worker和Fake流量。旧密钥在回滚窗口内仅保存于受限备份，不再参与新epoch写入；删除须另行授权。任何进程仍使用旧密钥、epoch未递增或完整恢复失败时保持关闭。每个环境使用独立CSPRNG 32字节密钥，测试固定字符夹具不代表生产熵。

阶段化恢复入口由`StageRecovery`接受包内恢复器生成的不可变快照并写为`rebuilding`，`ActivateRecovery`只把完全相同的staged快照切为`ready`。MySQL ready协调已通过组合测试；快照对象仅公开低敏count/digest，值和指针JSON/格式化均脱敏。新epoch可以替换完整旧run_id数据，避免依赖先删除唯一业务键；同epoch run_id不符、TTL、形状、期限或hard cap异常继续失败关闭。

组件采用固定键`molin:{video-g7}:capacity:active-v1`上的单一STRING快照，不写Chat/Image命名空间。字段仅为schema、epoch、policy、run_id、status、count和活动records；单条record保存身份规范JSON、尝试nonce、技术阶段及毫秒截止。Request→Task必须一对一。uint64主体与epoch使用十进制字符串，避免Lua/cjson浮点舍入；不保存Prompt、媒体、财务金额或Provider凭据。

v1仅接受已冻结`fake-native-async`，因此活动记录最多为全局queued100加Provider running2，共102条；这是关闭态单Provider校验边界，不是通用多Provider实现。超限、超过128KiB、非法字段/类型/阶段、重复Request、缺少字段、错误policy/epoch或实例身份均失败关闭。超字节数在GET前以STRLEN拒绝，避免先读取巨大数据。业务占用键必须无Redis TTL；30秒技术截止保存在record中，过期仍计容量。

脚本从实际执行连接的INFO读取run_id，不能只相信调用方缓存；INFO不可用同样拒绝。Redis官方说明脚本具有原子执行语义，本实现仍将全部校验和序列化放在唯一SET之前，不假定执行错误会自动撤销先前写入。[Lua执行语义](https://redis.io/docs/latest/develop/programmability/eval-intro/)、[INFO实例身份](https://redis.io/docs/latest/commands/info/)。

### 当前组件测试边界

执行入口为`infra/scripts/verify-video-gateway-vid-g7-redis.ps1`，需显式`VIDEO_GATEWAY_G7_REDIS_ISOLATED_APPROVED=YES`；`-LinuxRace`使用锁定Go镜像，`-Focus binding`仅复验Request唯一绑定。运行器创建自己的Redis/网络/随机认证，校验真实run_id，只发布loopback，数据在本轮tmpfs，不读取共享凭据、不执行共享FLUSHDB，按本次创建返回的精确ID清理。

session80555 native及session56671 Linux race在同一server哈希218f7ee7下14/14通过，SKIP=0且清理通过。除既有100并发、坏快照、Request唯一绑定、轴收紧、大整数、30秒过期债务及恢复四项外，新增confirm/release覆盖：promoting原子转running并直接恢复同用户queued名额、确认回执丢失后查明、running重放零写、不同nonce释放拒绝、释放回执丢失后只读重放、过期promoting/running的confirm拒绝与exact清债，以及最终count/records同时归零。

丢响应测试在**真实EVAL成功后**由客户端Hook返回错误，证明不自动重试、原nonce恢复原占用、新nonce不能夺取。它不是TCP断连；100个goroutine不是2/4/8个独立Go进程。真实重启、全部running轴独立边界、MySQL COMMIT未知、关闭态装配和Provider调用计数仍需后续验收。

### 待接线的容量与授权边界

计划实现的视频专用协调层只保存可重建的容量索引，不保存Prompt、媒体、Base64、签名URL、Provider正文、钱包金额或Key。身份绑定必须来自原授权和路由结果：Task、Request、User、Project、可空API Key、规范化模型、Provider及operation。

- 同Task和同不可变身份指纹重放必须返回原占用，不新增成员、不重置业务阶段。
- 同Task异身份/异operation/异指纹失败关闭，不移动任何桶。
- operation只进入意图指纹和低基数指标，不进入容量桶名称；路由别名归一后再形成Provider/Model桶。
- 租约/许可必须包含代次或不可复用的内部能力标识；调用方不能用旧许可释放或续期新持有者。
- Redis键和容量证明只供内部协调，普通JSON和错误不公开内部键、策略细节或当前深度。

后续公共测试边界为原G5/G6生成协调器、提交协调器和受持久事实约束的恢复/释放入口；不能用上述存储组件的直接调用替代真实认证、数据库提交和在途任务恢复。

## 原子迁移与到期语义

排队到运行采用带数据库确认的两步协调，而不是把Redis迁移成功当作MySQL已经提交：

1. 单次原子操作核验所有身份、旧许可及全部running限额；全部通过后取得完整running预留，进入内部`promoting`，此时保留完整queued占用。
2. MySQL原Task的ClaimRunning、提交身份与授权事实确定提交成功后，再由当前代次原子确认running并移除queued。只有数据库提交确定成功、Redis当前代次有效且普通执行证明有效，才可能进入Provider调用。
3. Redis容量不足或数据不一致，原queued成员不变；数据库确定回滚时，证明原Task仍queued且未发出提交许可后，可以撤销本次running预留，原queued无需抢回空位。
4. 数据库提交未知、确认响应丢失或恢复依据不足时，保留双重保护的容量债务，查询原事实后幂等确认或撤销，不先释放running再尝试重建queued。

`promoting`只是协调层状态，不添加另一个业务执行状态轴。不得先释放queued，再分批尝试各running桶；短时间保守计入两类容量是避免跨系统无保护窗口的明确取舍。

30秒是持有者技术有效期，10秒心跳沿用G0；不是Provider业务占用的自动有效期。到期进入待核对，不自动删除唯一占用依据。容量扫描、指标采集和新准入不能仅凭时间执行全量到期删除。

pending_reconcile、提交ACK未知、Worker崩溃、轮询超时均不能证明上游已停止。只有原提交身份和可靠结束事实足以证明安全时，才允许按当前授权释放占用。后续恢复仍只查询原Provider任务，不重新create。心跳不能延长Quote、权利接受、输入资产或最大20分钟执行观察窗。

权衡是故障时可能暂时保守占满容量；必须依靠有界恢复和告警推进收口，不能以自动清空计数换取表面可用。

## Redis 与 MySQL 的事务衔接

Redis是预留与协调层，MySQL原Task、Request、Hold、Usage、事件和Outbox仍是持久事实。不得拆开原G5财务原子提交或更改价格/退款政策。

1. 原合法同意图重放优先读取原事实，不重新申请容量或新建Hold/MQ。
2. 首次意图必须先由原Request唯一INSERT取得本次竞争胜者身份，再经过原Quote、输入及权利校验，在原Hold写入前取得Redis排队预留。事务外首次重放查询不代替此唯一键裁决。Redis不可用或确定超限时返回失败，原事务回滚，不留下Hold/MQ。
3. 原G6事务尾部门闩和完整业务核验继续保留；在提交边界重新证明Redis许可仍属于当前身份和代次。不能为更早取得门闩而未分析就改变既有锁序。
4. Redis响应丢失或MySQL COMMIT未知时保留原身份和占用，以原意图/Task/Request查明结果，不直接释放后重做。
5. 清理预留和新任务提交必须共享可验证的串行边界。仅查到“暂时没有Task”不足以删除预留：可能仍有事务尚未提交。恢复必须在原门闩/当前代次保护下核对，并让恢复后才继续的旧创建在尾部失败关闭。
6. queued转running和Worker提交前同时保持原MySQL状态/CAS及G7执行围栏。Redis许可不等于允许调用Provider、更新终态或结算。

稳定业务身份与事务尝试代次必须分离：数据库重试复用原意图，但每次重新取得写入资格需对当前Redis尝试能力进行CAS。旧失败回调或清理只能作用于自己的代次，不能删除重试或接管后的占用。创建最外层持有原门闩完成许可复核直到COMMIT，不能只在内层函数返回前验证。

锁序约束必须在实现时统一：已存在Task的G7恢复路径先锁原Task/Request，再进入末端容量门闩；缺失Task的回收在门闩下只读取主库已提交事实，不追加锁定正在创建的Request。当前G6 ClaimRunning有guard→Task顺序，不能直接与Task→guard恢复路径拼接；G7装配须形成一致锁序，并以创建/运行接管/恢复交叉测试证明，不把重试当成未分析死锁环的替代。

具体锁序、提交未知恢复和尾部校验均需真实MySQL+Redis故障注入证明，不能只用Lua单元测试判定本节完成。

## 空库、断连与恢复屏障

恢复屏障的前置cutoff已经接入旧创建、ClaimRunning、计划/claim校验和Provider紧前统一门。Begin进入recovering后，旧创建必须整笔回滚，Claim保持queued，已claim的执行在RPC前返回治理不可用；回执入口不加此门，以便已经发生的RPC继续绑定和收口。恢复状态不是容量满，不返回429容量错误。该增量只保证快照期间不再产生未计入的新提交，不表示快照、Redis staged状态或ready已经实现。

锁序采用现有创建一致的Task→guard。恢复Begin只在短事务中更新guard并提交，随后使用独立RR一致性读取形成快照；不得在持有guard时FOR UPDATE扫描所有Task。更窄的“紧前门检查后才Begin”竞态中，Task已是submitting：有持久计划时必须计Provider债务，无计划时阻断ready，因此不能漏计后放行。

持久恢复租约的当前实现见[MySQL恢复代次](video-gateway-vid-g7-capacity-recovery-epoch.md)。它只支持uninitialized/recovering/blocked和私有恢复证明，尚未发布ready或生成完整恢复快照，不能当成下述恢复屏障已经完成。

Redis断连和数据丢失均关闭新准入，不能把空库解释为全部容量空闲。初始化/恢复必须有可验证的屏障，防止其他实例在重建期间开始新Hold或Provider提交。

重建读取原持久账本，覆盖queued、实际运行、提交未知和pending_reconcile；旧G6运行状态集合不能直接充当恢复全集。还要核对原队列/Outbox、冻结额、Provider提交身份与结束证据。部分重建、策略不一致、读取失败、代次变化或依据不足时保持关闭，不自动放行。

需要严格区分：

- 已证实没有提交的孤立预留：核对并满足串行边界后可回收。
- 已有Task或提交结果未知：保留容量债务并安排原任务恢复。
- 上游已结束且原事实满足释放条件：记录核对结果，当前授权幂等释放。

不因容量租约变化修改用户资金、释放TaskInput或提前交付。Redis重建、释放与接管的当前代次证明需要单独测试，不能只检查一个ready布尔值。

### 工程审查补充的持久事实约束

1. 首次create之前，必须把规范化的计划Provider、原提交意图和容量代次关联到原Task及追加事件，并确定提交成功。现有`ProviderCode/ProviderTaskID`表示收到回执后的绑定，不能提前填充它们破坏现有BindProviderTask语义；需要在原Task中扩展独立、不可变、普通JSON不可见的提交计划字段，具体Schema随实现审查，不新增平行账本。
2. ACK未知且没有ProviderTaskID时仍按原持久提交计划恢复到同一Provider桶。历史未知任务无法确定原Provider时关闭相关视频域；不能忽略，也不能用当前路由猜测归属。
3. 恢复代次以原MySQL容量门闩的持久版本及受控恢复状态为依据，Redis仅镜像该代次。恢复状态切换必须串行、CAS且有明确开放条件；部分重建不得发布ready。具体字段与状态迁移需Schema测试后才能判通过。
4. 任一已有业务占用的Redis身份记录或桶缺失、类型错误、成员关系不完整，都不能解释成零占用。业务占用键不设自动删除TTL；准入/提交前须复验原持久代次及相关原业务债务，异常关闭并恢复，不能只验证当前新任务刚写入的键。
5. 重建全集包括所有已经授予提交权的任务，即便旧Worker尚未实际发出create。旧技术租约到期不能证明该请求永远不会到达Provider；在有可靠依据前保留原容量债务，不把“Provider暂未查到”直接当成可释放证明。

上述四类工程审查问题分别对应提交归属、双系统迁移、事务尝试和恢复屏障。其中原Task提交计划已由112迁移和服务实现，完整快照、ready发布和跨系统恢复协调也已有本地组合证据；容量epoch尚未绑定到原提交计划，跨系统业务提交许可和多进程证据仍缺，不标为完整工程审查PASS。

### 提交计划与执行许可分离（2026-09-04工程复核）

当前`recovering`租约只授权恢复工作，不授权Provider提交。独立工程角色`g7_outbox_safety_check`核对现有Submit链后，确认按以下顺序补齐：

1. 在原Task记录只写一次的计划Provider、原Request意图、赢得的submitting业务版本和原submit Worker代次；同事务追加唯一计划事件。计划记录不写回执Provider字段，不增加Provider attempt，不改变财务或输入事实。
2. 提交容量代次尚未授予时保持NULL；不能把`Current`或`Validate`返回的恢复epoch填成执行授权。后续ready与promoting协调器首次确定绑定后才可成为不可变容量依据。
3. 记录计划只返回持久元数据或写入结果，不返回可调用Provider的proof。相同计划重放只读，新Worker接管或恢复证明有效都不产生第二次create许可。
4. 完整账本快照及ready屏障已完成局部验证；仍须完成统一Task/Request/guard锁序、promoting与MySQL确定提交、running确认和容量/Worker双重许可后，再接通G7 RPC路径。缺少这些依赖时G7装配失败关闭，不根据空字段或缺接口自动降级到G6路径。
5. 容量未ready应在ClaimRunning前保持queued并拒绝。现有`ValidateSubmissionClaim`错误分支会进入pending_reconcile，不可把普通容量未就绪误记成Provider提交未知。

提交计划记录、完整快照和ready恢复协调已经实现；容量epoch绑定与业务执行许可仍是待实现接线约束，不是运行时验收证据。历史无计划任务只能依据原接受/未知事实核对，不用当前路由猜测补填后重新create。

完整账本快照由`VideoCapacitySnapshotBuilder`实现。接口只返回脱敏快照和低敏epoch/queued/running/total摘要；内部按主键分页验证全部历史Task，不把累计终态数误作Redis活动上限。活动记录最多102且继续由十轴hard cap复核。同一恢复proof通过HMAC稳定派生每Task nonce，重建digest相同；proof丢失必须新开epoch，不能生成同epoch新nonce。当前`VideoCapacityRecoveryCoordinator`已完成快照→Redis stage→MySQL ready→Redis activate及未知回执恢复；它不授予业务Provider调用。

## 接口错误与关闭行为

确认容量不足沿用原HTTP429、`concurrency_limit_exceeded`与`Retry-After`，稳定低敏scope扩展须同步API合同。Redis连接、脚本、数据形状、初始化/恢复状态异常属于依赖或治理不可用，不能伪装成429。

模块关闭不装配此协调层；缺少Redis或配置错误不回退到进程内计数、旧G6-only运行模式或Fake。流量关闭停止新工作，但保留原在途任务必要恢复路径。

## 实现与验收任务

| 编号 | 结果 | 必须证明 | 当前状态 |
|---|---|---|---|
| REDIS-01 | 身份、策略和许可边界 | 归属、operation不分桶、JWT稳定主体、仅收紧、错误脱敏 | 部分：存储身份/策略/尝试验证通过，真实数据库授权待接 |
| REDIS-02 | 排队原子预留 | 每轴边界、同意图重放、冲突、满额零新增占用 | Redis组件已验证，业务事务接线未完成 |
| REDIS-03 | 转运行、续期与围栏 | 全轴原子转移、真实30秒到期、旧许可不能覆盖/释放新许可 | 部分：promoting/confirm/release/续期/过期债务存储通过，MySQL业务提交与终态证明尚未包装 |
| REDIS-04 | 原G5/G6事务接线 | Redis故障/容量拒绝无Hold无MQ、原Quote错误优先级、COMMIT未知 | 待实现 |
| REDIS-05 | 重建与安全回收 | Redis重启/空库、未知Provider仍占hard cap、双向核对、无并发误删 | 待实现 |
| REDIS-06 | 跨进程证明 | 2/4/8个真实Go进程、混合T2V/I2V、第三个Provider create为0 | 待实现 |
| REDIS-07 | 装配、监控与回归 | 默认关闭、策略一致、低基数指标、Chat/Image及钱包基线不变 | 待实现 |

真实Redis镜像必须锁定版本/digest，测试数据库/实例为本轮所有，禁止对共享实例FLUSHDB。Mock只允许外部网络/时钟等故障边界，不能把核心准入、幂等、事务和资金实现替换为恒成功。

## 关联与范围确认

- [G0冻结合同](video-gateway-vid-g0-gate.md)：第6节租约/未知提交与第8节容量。
- [G6排队与运行合同](video-gateway-vid-g6-queue-admission-contract.md)：保留原本地MySQL门闩及原事务。
- [G7阶段矩阵](video-gateway-vid-g7-infra-recovery.md)：G7-10、11、12及相关运行时/恢复/监控要求。

独立产品/Spec审查`g7_late_observation_policy`确认新增Key/model默认值属于已授权关闭态工程决策，并要求保留未知Provider占用和重建屏障。该结论不是运行证据或完整PM PASS；未启动G8，不改变测试服务器授权边界。
