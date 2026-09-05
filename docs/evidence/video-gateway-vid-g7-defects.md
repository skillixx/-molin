# VID-G7 开发期缺陷台账

本台账仅记录已发现问题，不表示阶段缺陷已经由独立验收清零。

恢复租约补强（未全部验收）：

- `G7-CAPACITY-EPOCH-001 / P2`：111初版仅在capacity字段变化时校验version_no，单独回退/跳号未拒绝。session87305真实SQL返回nil；已补不变或+1约束，43096和39272相关回归通过，状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-AUDIT-001 / P2`：恢复证明依赖的原audit_logs记录可改删或重复。session87305实际接受；已新增仅限本模块事件的唯一键、INSERT绑定和只追加约束，43096和39272相关回归通过，状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-TEST-001 / P2`：故障测试意外t.Fatal可能在Remove前退出并残留GORM回调；独立QA发现，已在注册后立即安排Cleanup，43096和39272相关回归通过，状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-AUDIT-002 / P2`：session98087已实际复现缺schema加extra保持7字段及数字owner被错误接受；反例先合法转blocked且确认无重复审计。已补七字段和NULL安全类型校验，session39272原生17例及同源码Linux回归通过，SKIP=0且清理通过；状态`FIXED_PENDING_VERIFY`，不以唯一键或Go解码遮蔽触发器结果。

Redis存储增量：

- `G7-REDIS-REQUEST-001 / P2`：新组件原本只按Task判重，同Request可以取得第二个Task预留。session15789实际返回nil；修复为快照内Request唯一校验及本次Request别名拒绝，session70915八项native/Linux race通过，状态`FIXED_PENDING_VERIFY`。
- `G7-REDIS-TEST-001 / P2`：queued用例假定临时实例仍为空，合并其他用例后读取到上例占用，session68665空库断言错误得到Conflict。改为在已核验身份的本轮临时Redis显式模拟整键丢失，不清理共享实例；session70915通过，状态`FIXED_PENDING_VERIFY`。

Request唯一修复只关闭该存储组件缺口，不代表数据库提交授权、多进程或恢复屏障已完成。

| 编号 | 级别 | 原因 | 修复及证据 | 状态 |
|---|---|---|---|---|
| G7-SEC-001 | P1 | 凭据包脱敏方法仅定义于指针，解引用值副本可能绕过JSON/格式化保护 | 改为值接收者；TestVideoG7SecretBundleValueRedaction实际先红后绿，Linux安全读取测试覆盖指针和值以及三类格式化固定输出 | FIXED_PENDING_VERIFY |
| G7-MQ-TEST-001 | P2 | 隔离runner的internal网络无宿主端口，root诊断造成cookie权限竞争，单纯TCP探测又可能只看到Docker代理 | 改为独立bridge且只发布loopback，并以rabbitmq服务身份check_running；真实Broker测试最终通过并回收本轮资源 | FIXED_PENDING_VERIFY |
| G7-MQ-TEST-002 | P2 | 故障恢复测试只等待45秒，短于RabbitMQ至少一次死信默认3分钟重试间隔 | 依据官方合同将该故障观察窗改210秒，正常2秒断言不变；原消息实际184.76秒整轮恢复通过 | FIXED_PENDING_VERIFY |
| G7-MQ-TEST-003 | P2 | 发布NACK测试假定quorum长度上限严格且第二条必然拒绝，与允许少量在途超额的Broker合同不符 | 必须在有界次数内实际观察NACK，并核对全部已确认消息；组合回归发布器3.62秒通过，不以队列长度替代业务hard cap | FIXED_PENDING_VERIFY |
| G7-OUTBOX-001 | P1 | 秒精度locked_at在同秒失败/重排/重领后复用，旧Worker的MarkPublished错误成功 | 真实MySQL反例先FAIL；视频保留最后令牌并逐行单调递增，旧管理重排同步保留；原反例和增强七项Linux race通过 | FIXED_PENDING_VERIFY |
| G7-OUTBOX-TEST-001 | P2 | PowerShell把未加引号的-h127.0.0.1拆为-h127和.0.0.1，临时MySQL已就绪但探针连接错误主机 | 实际参数回显与错误127:3306复现；改完整带引号--host参数，109迁移及真实测试通过 | FIXED_PENDING_VERIFY |
| G7-OUTBOX-QA-001 | P2 | 独立审查发现普通重试、旧回写及连续同秒高水位缺测 | 增加六轮交替旧/新入口普通重试、旧MarkRetry/Publish拒绝及未来令牌精确接管边界；独立静态复核和Linux race通过 | FIXED_PENDING_VERIFY |
| G7-OUTBOX-QA-002 | P2 | 独立审查发现不同历史高水位同批返回与数据库一致性缺测 | 两个真实聚合分别使用未来/空高水位，逐项检查独立令牌并用数据库CAS确认；独立静态复核和Linux race通过 | FIXED_PENDING_VERIFY |
| G7-OUTBOX-FINANCE-001 | P1 | G5四处校验把pending且无锁当唯一合法Outbox，G7合法领取阻断首次财务终结及重放 | session4538真实8场景先红；四态修复后session75089的99项G5+11项G7全部通过 | FIXED_PENDING_VERIFY |
| G7-OUTBOX-FINANCE-002 | P2 | 大小写不敏感SQL查询后未精确复核聚合ID、调整事件类型或恢复事件ID | session32401身份反例先红；四处补精确比较且不隐藏异常全集，session75089组合通过 | FIXED_PENDING_VERIFY |
| G7-OUTBOX-FINANCE-003 | P2 | dead缺少最后租约仍能通过运输形状校验，与当前G7可达写入路径不一致 | session16142的dead_without_lease真实反例先红；收紧非空锁并保持其他dead反例独立，session50944的17项Linux race通过 | FIXED_PENDING_VERIFY |
| G7-OUTBOX-PROJECTION-001 | P1 | 新投影器直接比较Task细粒度与Request粗粒度执行态，拒绝合法预占与执行中事件 | 复用原Repository映射并单列reserved/pending初态；held、执行中、unknown和终态正例由17项专项通过 | FIXED_PENDING_VERIFY |
| G7-OUTBOX-PROJECTION-002 | P2 | 格式、摘要ID和金额正确的伪造S/R/A/J/P/C缺少原业务依据校验 | session4885伪造依据反例先红；新增原冻结、解冻、消费和补偿检查，17项专项通过，不新增财务写入 | FIXED_PENDING_VERIFY |
| G7-OUTBOX-PROJECTION-003 | P2 | Repository测试仍调用已重命名的内部状态映射函数，service专项不会编译该测试包 | 全库Go编译实际失败后修正唯一旧引用；session94734全库Go、vet和依赖验证通过 | FIXED_PENDING_VERIFY |
| G7-OUTBOX-PROJECTION-004 | P2 | 新夹具使用亚秒next_retry_at而领取截秒，DATETIME(0)舍入导致刚写入的事件偶发尚未到期 | 唯一G7临时MySQL只读CAST确认.9秒舍入进位；五处夹具统一UTC秒值，领取规则不变，17项专项通过 | FIXED_PENDING_VERIFY |

| G7-LEASE-001 | P1 | 普通Worker知道业务version后仍可绕过已持有执行租约推进Task | session76344真实反例先红；Task迁移/Provider绑定接入内部证明与事务结束复核，session5567七项Linux race通过；不代表其他媒体/财务边界已全部接线 | FIXED_PENDING_VERIFY |
| G7-LEASE-002 | P2 | SQL允许新Task直接INSERT非零租约，跳过零初态 | session31833真实反例；新增INSERT守卫，session99367及5567通过 | FIXED_PENDING_VERIFY |
| G7-LEASE-003 | P2 | 同一租约代次可另造任意ID的认领/释放快照 | session31833真实反例；SQL限定确定性ID并复用唯一键，Repository精确比较，session5567通过 | FIXED_PENDING_VERIFY |
| G7-LEASE-004 | P2 | UPDATE触发器在最大无符号代次先做加一，阻断合法续期/释放 | session23908最大代次续期真实失败；扩宽DECIMAL后加一，session5567验证最后代次认领/续期/释放及耗尽拒绝 | FIXED_PENDING_VERIFY |
| G7-LEASE-TEST-001 | P2 | 旧G5 helper仅检查DSN库名与标记，不能独立证明全局DDL夹具目标是本轮临时实例 | runner增加新容器server_uuid绑定，极限夹具解析DSN并核对实际库名/UUID；session99067的124项组合race通过，独立只读复核关闭 | FIXED_PENDING_VERIFY |

当前源码绑定和具体测试结果见 `video-gateway-vid-g7-source-state.json` 及各专项验证回执。QA、PM、Standards、Spec和DEV终审尚未执行，不预填最终P0/P1/P2结论。

回执围栏增量：`G7-LEASE-005 / P1`。已进入G7租约的pending任务可绕过普通绑定入口，由无证明或已失效Worker直接写Provider ID。session15302在T2V/I2V的四个反例均错误返回nil；增加首次写入前及事务尾部检查后，session92280的14项真实MySQL测试及session5470的15项Linux race均通过。旧G5只读重放与低敏冲突审计保留；真实尾过期验证正常/pending两条分支全事务回滚，新代次可保存同一原回执。状态：`FIXED_PENDING_VERIFY`，不等同于完整G7验收。

普通财务围栏增量：

- `G7-FINANCE-FENCE-001 / P1`：普通结算接受无证明或旧Worker，session99131的四个反例错误返回nil。增加普通首次资金写入及事务尾部证明检查、阻止失权后补记，session83687、55472及84514对应专项通过。
- `G7-FINANCE-FENCE-002 / P1`：普通退款同样绕过执行租约，session86764的四个反例错误返回nil。增加首尾检查并保留已退款只读重放和独立补偿授权，session55472及84514通过。

上述缺陷状态为`FIXED_PENDING_VERIFY`；session55140四项Linux race及session48490的133项组合（完整99项G5）均通过。四个真实到期子例均运行31秒以上；这不表示完整G7财务调用链或最终验收完成。

基础取消增量：`G7-CANCEL-FENCE-001 / P1`。Worker取消在事务尾失去执行租约仍可提交退款。session97947两条T2V/I2V真实30秒反例错误返回nil；增加条件式首尾检查后，session12746六个子例通过，原用户准入与已取消只读重放保持。状态`FIXED_PENDING_VERIFY`；G6外层事务边界未覆盖，不冒称全部取消链完成。

自动心跳测试记录：

外层取消增量（验证中）：

- `G7-CANCEL-FENCE-002 / P1`：G6用户/管理员取消内层退款返回后，在外层详情读取期间普通Worker租约到期仍可提交。session68961真实MySQL中T2V/user `hits=1, err=nil`，32.56秒；session42851管理员T2V/I2V同样失败，32.46/32.64秒。两条入口首尾检查已添加，session58972独立子组四例Linux race通过、SKIP=0且清理通过，状态`FIXED_PENDING_VERIFY`；其他取消状态及完整阶段未据此判通过。
- `G7-CANCEL-TEST-002 / P2`：四个并行用例准备时上一例active政策尚未Cleanup，后三例违反唯一生效政策约束。独立QA发现，session68961实际1062复现。现精确退役已完成任务创建的本例政策后再并行等待，不删除历史、不放松约束；session42851四例均进入业务路径，不再出现1062，状态`FIXED_PENDING_VERIFY`。

以上局部缺陷关闭不代表完整阶段缺陷已清零。

## 2026-09-04 RabbitMQ业务链与MinIO增量

- `G7-RABBIT-RUNNER-001 / P2`：`rabbit_business`实际依赖Redis，但脚本最初未把该Focus纳入Redis授权前置，可能在缺少显式隔离许可时启动Redis。已将Focus加入失败关闭名单；真实native/Linux专项均1/1通过且资源清理完成。状态`FIXED_PENDING_VERIFY`。
- `G7-MINIO-RUNNER-001 / P2`：Windows Docker的internal网络不会发布宿主回环端口，首轮在测试前失败；改为本轮专属网络并只绑定`127.0.0.1`，空端口输出显式失败。后续native通过且无遗留。状态`FIXED_PENDING_VERIFY`。
- `G7-MINIO-IDEMPOTENCY-001 / P2`：首次写返回本机时间、重放返回MinIO时间，导致相同对象出现两个元数据视图。首次写后统一Head并复核hash/size，native/Linux重放返回同一权威事实。状态`FIXED_PENDING_VERIFY`。
- `G7-MINIO-INLINE-001 / P1`：inline对象已存在时只核已存元数据，未先验证本次正文；声明旧hash但传漂移正文会错误返回幂等成功。真实MinIO反例先红；现有界读取本次正文并复算SHA-256后才能进入条件写，native/Linux正例、重放和漂移反例通过。状态`FIXED_PENDING_VERIFY`。
- `G7-MINIO-ENDPOINT-001 / P2`：Linux测试把容器服务名的明文HTTP误作浏览器公开端点，正确安全校验拒绝。测试改为进程内回环代理连接内部MinIO，不放宽生产HTTPS/回环规则；Linux race通过。状态`FIXED_PENDING_VERIFY`。
- `G7-MINIO-EVIDENCE-001 / P2`：首版MinIO源码哈希仅覆盖video包，遗漏service上传封存实现。runner现冻结整个server及自身脚本；旧哈希作废，新native/Linux结果绑定完整范围。状态`FIXED_PENDING_VERIFY`。

以上仅为本地MinIO和Rabbit业务链增量；bootstrap、双向孤儿扫描、监控、实际回滚和独立终审未完成，不能据此汇总阶段P0/P1/P2为0。

## 2026-09-04 关闭态运行时增量

- `G7-RUNTIME-RUNNER-001 / P2`：首次四依赖就绪循环调用未设自身超时的容器CLI，使120轮上限被单轮阻塞放大；中断后PowerShell未进入清理。改用有限容器日志判定启动阶段，并把清理改为缺失目标不阻断的逐项删除和零遗留复核。状态`FIXED_PENDING_VERIFY`。
- `G7-RUNTIME-RUNNER-002 / P2`：migration的`-h127.0.0.1`在Windows参数传递中被解析为主机`127`，首个migration失败；改为`--host=127.0.0.1`，115个migration全部通过。状态`FIXED_PENDING_VERIFY`。
- `G7-RUNTIME-RUNNER-003 / P2`：Docker环境参数以字符串加法进入数组，密钥值被拆成镜像位置参数；改为单个插值参数，错误输出继续脱敏，后续Linux测试执行。状态`FIXED_PENDING_VERIFY`。
- `G7-RUNTIME-REDIS-001 / P1`：bootstrap专用Redis客户端沿用go-redis默认自动重试和未启用context超时，容量Store正确拒绝装配。改为`MaxRetries=-1`、ContextTimeoutEnabled及1秒拨号/读写超时，保持Lua结果未知不自动重放；四依赖Linux race通过。状态`FIXED_PENDING_VERIFY`。

关闭态运行时证据只属于本地一次性隔离环境；后续已完成实际OS进程kill、双向孤儿补偿、监控及本地12步Expand-only回滚。测试服仍未授权，远端关闭态安装和实际回滚尚未执行。

- `G7-MINIO-SCHEMA-001 / P1`：G7输出Store改为共享`ai-*` bucket后，G4数据库触发器仍只允许旧`video-temp→video-result/video-quarantine`，真实Rabbit业务链停在labeling。新增Expand-only migration 116，同时仅允许同key的历史旧迁移对和共享新迁移对，其他归属/位置变化继续拒绝；native/Linux Rabbit业务链恢复通过，116个migration及完整runtime再次通过。状态`FIXED_PENDING_VERIFY`。
- `G7-ORPHAN-INVENTORY-001 / P1`：当前MinIO List API未稳定返回自定义墓碑metadata，首轮扫描把合法墓碑误判为缺少SHA的损坏对象并整体失败。列表现只提供候选键，每项再用Stat读取权威元数据；任一Stat未知仍失败关闭。四依赖Linux race复验通过。状态`FIXED_PENDING_VERIFY`。
- `G7-ORPHAN-RACE-001 / P1`：若确认孤儿后仍允许迟到数据库绑定，Worker可能删除刚被资产引用的对象。migration 117在confirmed期间阻止资产、InputAsset、UploadSession和保存计划绑定；Worker删除紧前仍重读全部引用，删除确认后才resolved。真实迟到UploadSession反例被SQL拒绝。状态`FIXED_PENDING_VERIFY`。
- `G7-ORPHAN-RECOVERY-001 / P1`：物理删除成功后数据库完成事务失败会留下running补偿。实际故障注入先证明对象已不存在且DB仍未完成；两分钟租约后重入，按对象不存在幂等完成并保留观察/补偿事实。状态`FIXED_PENDING_VERIFY`。
- `G7-METRICS-CAPACITY-001 / P1`：容量指标若从进程内计数或非原子GET派生，可能与Redis实际epoch/phase漂移。新增Lua `metrics`动作，在同次执行中完整验证ready结构、run_id、epoch、policy、TTL和每条租约后返回queued/promoting/running。Linux runtime指标抓取通过。状态`FIXED_PENDING_VERIFY`。
- `G7-NATIVE-TASKUUID-001 / P1`：提交计划原把Molin request_id保存为`submission_intent_id`，不满足冻结的Runware预生成UUIDv4恢复合同。计划事务现生成并持久化`taskUUID-<UUIDv4>`，Gateway只传该值；migration 112/114校验格式，115和回执事务要求Provider ID字节一致。native/Linux计划各7/7和HTTP链各1/1通过。状态`FIXED_PENDING_VERIFY`。
- `G7-NATIVE-TEST-001 / P2`：首个MySQL+Redis+HTTP测试用不同协调器生成和消费进程内send permit，正确被治理门拒绝。测试改为同一Capacity Ledger完成promote/consume，并由Fake HTTP接收时反查MySQL证明taskUUID已先提交；未放宽生产许可。状态`FIXED_PENDING_VERIFY`。
- `G7-PROCESS-KILL-001 / P1`：此前只有函数故障注入，不能证明操作系统终止Worker后不会重提。新增真实Go子进程，在回环Provider已记录create但ACK阻塞时强制Kill；30秒真实租约到期后新Worker取得新DB租约但无法生成/消费第二份permit，Provider计数保持1。native/Linux race均通过。状态`FIXED_PENDING_VERIFY`。
- `G7-RETENTION-001 / P1`：G6删除请求只允许输入到期前创建，服务停机跨过expires_at后无法形成清理凭据。migration 118增加仅后台可生成的确定性`retention`请求，已到期才允许；仍复用原pending_delete、TaskInput、legal hold和7天执行后留存门禁。真实MySQL+MinIO清理通过。状态`FIXED_PENDING_VERIFY`。
- `G7-RETENTION-QUERY-001 / P1`：Retention Worker复用同一个GORM查询对象，第二阶段叠加第一次JOIN/WHERE，形成重复别名并同时要求ready/pending_delete。每阶段现从`NewDB`干净Session构造查询；runtime测试包改为`-p=1`避免共享隔离库跨包并发污染。Linux race通过。状态`FIXED_PENDING_VERIFY`。
- `G7-RETENTION-IMPORT-001 / P1`：Retention Worker构造器错误强制要求uploads，只有imports的合法运行时无法清理并遗留输入。改为上传或导入至少一个即可构造，并按来源分别查询API Key归属；G6导入ready留存测试验证独立副本删除、来源图片保留和retention请求。状态`FIXED_PENDING_VERIFY`。

`G7-CANCEL-TEST-003 / P2`：同库组合运行99项G5及G7 Outbox后，保留的合成reserved任务已达110，G6真实创建夹具被冻结global queued=100拒绝。session83706四例实际报“视频生成排队容量已满”，未进入取消围栏。已把G6创建型用例分到独立临时库并纳入总门禁，未提高生产阈值或删除原任务。session58972最外层exit=0、groups=2、135/135通过、SKIP=0且清理通过；状态`FIXED_PENDING_VERIFY`。

| 编号 | 级别 | 原因 | 修复及证据 | 状态 |
|---|---|---|---|---|
| G7-HEARTBEAT-TEST-001 | P2 | 正例执行合法Task CAS后，错误要求Request执行轴也保持不变 | session69723实测失败；独立验证执行态/版本/更新时间三项变化，其他Request字段与七表完整比较；session83930真实MySQL正例通过 | FIXED_PENDING_VERIFY |
| G7-HEARTBEAT-TEST-002 | P2 | 原八表快照省略空表JSON键，新断言以键数8误判空Usage为缺失 | session45016实测失败；改逐一核对固定八表并拒绝未知表，空表仍参与比较；session83930正例、session11741三项race及session50802的29项组合race通过 | FIXED_PENDING_VERIFY |

## 2026-09-04 提交计划与数值边界增量

- `G7-SUBMISSION-PLAN-001 / P2`：新增计划意图列64字符，缩短原RequestID的128字符合同。独立工程审查发现；session75438的65字符T2V和128字符I2V均实际报SQL1406。已改为128；session2989通过长ID写入后停在回执夹具前缀错误。修正夹具后session34808的计划Linux race专项1/1通过，两种operation均完成，清理通过，源码d9362d78。状态`FIXED_PENDING_VERIFY`，不代替完整计划与G7验收。
- `G7-CAPACITY-RUNNER-001 / P2`：capacity_epoch通配分组可能把独立AuditTypes从父required移除并计PASS，但子Focus不执行该顶层测试。改为精确测试集合，Version-only选择对应子Focus；独立QA只读确认修复，PowerShell真实AST提取的分组集合验证AuditTypes不被吞入。状态`FIXED_PENDING_VERIFY`；完整FinanceRegression组合尚未复跑。
- `G7-CAPACITY-BOUNDARY-FIXTURE-001 / P2`：新数值极限夹具误写审计JSON键policy而非policy_sha256，session89349被既有SQL1644拒绝，未进入被测业务。修正夹具后session69481 native上界测试通过、清理完成。状态`FIXED_PENDING_VERIFY`；不归为生产恢复缺陷。
- `G7-SUBMISSION-PLAN-002 / P2`：计划四字段只写，但计划后仍可通过不改计划列的SQL替换原Task身份和输入规格。session17002两种operation的public_id负例均实际返回nil；112增加首次/后续计划身份及input_json逐字节冻结。session82439 native四项通过，session16923 Linux race七项通过、SKIP0、清理通过；两阶段各14项均精确命中1644计划守卫，合法心跳与回执不被误挡。状态`FIXED_PENDING_VERIFY`。
- `G7-SUBMISSION-PLAN-TEST-001 / P2`：首次Linux回执兼容夹具使用`plan_`而非既有`taskUUID-`，session2989在T2V进入原Repository校验后失败，第二上界组未启动。只修夹具、不放宽生产校验；session34808计划及独立上界Linux两组均1/1通过，SKIP0、清理通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-CUTOFF-001 / P1`：Begin进入recovering后旧ClaimRunning仍可成功，且Gateway非defer路径不调用持久紧前门。session57910实测Claim err=nil；视频单测两种operation实测gate=0、Provider=1。已接入旧创建、Claim、计划、claim校验和Provider紧前统一门；session65089 Linux专项通过，Provider=0。状态`FIXED_PENDING_VERIFY`，不表示ready已实现。
- `G7-CAPACITY-CUTOFF-TEST-001 / P2`：G5底层夹具未装生产HTTP使用的队列门，session92095在其他门通过后误见新创建成功。显式装配真实MySQL门后验证Request/Task/Hold/财务零残留；状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-CUTOFF-TEST-002 / P2`：两个顶层测试复用不可回退的单行恢复门闩，前例blocked使后例未完成Claim。session79344实测status=queued、proof=false；合并为单一恢复epoch且不清零事实，最终Linux专项通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-CUTOFF-ERR-001 / P2`：recovering被误返回运行容量已满，可能让上层伪装成429。session98750实测该错误；现区分治理不可用和真实容量上限，session65089复验通过。状态`FIXED_PENDING_VERIFY`。
- `G7-REDIS-RECOVERY-001 / P1`：完整旧快照保留但Redis run_id变化时，即使MySQL准备更高epoch也无法原位Stage，迫使运维先删唯一业务键。session91943精确复现治理不可用；现仅允许更高epoch覆盖通过完整形状/hard cap复算的旧状态，同epoch及activate仍要求当前run_id。session86078 native与session93633 Linux恢复四项通过。状态`FIXED_PENDING_VERIFY`。
- `G7-REDIS-RECOVERY-002 / P2`：恢复Lua未检查固定业务键TTL，会接受即将自动消失的staged/ready状态。新增统一PTTL=-1检查，并覆盖两种状态零写拒绝；session86078与93633通过。状态`FIXED_PENDING_VERIFY`。
- `G7-REDIS-RECOVERY-TEST-001 / P2`：Stage测试依赖新容器和注册顺序，没有自行确认固定键为空。独立QA发现后，在已绑定本轮Redis上精确DEL并确认Exists=0；session93633 Linux恢复四项通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-SNAPSHOT-001 / P1`：快照只查非终态，未证明终态Provider确已结束，可能漏债后ready。改为分页扫描全部Task；安全未提交cancelled、完整settled succeeded及released failed/cancelled逐项验证后排除，expired或证据不足阻断。session23276/63450通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-SNAPSHOT-002 / P1`：同proof每次构建随机nonce，同epoch Stage未知后重建必冲突。改为proof HMAC稳定派生Task/Request nonce，不额外调用随机源；同proof双构建digest相同。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-SNAPSHOT-003 / P1`：排除终态前先限制全部Task<=102，使历史累计永久阻断。session72893以110条历史/活动实际复现；改为同RR按主键50条分页，只有最终活动records>102拒绝。103个真实取消闭环后仍返回4活动，session23276/63450通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-SNAPSHOT-004 / P1`：终态调账和资产证明复用FOR UPDATE，在READ ONLY RR中不满足纯读合同；同时仅验证现存Outbox会漏缺失事件。抽取钱包、调整、结算资产、release proof和完整终态财务的只读适配；普通Outbox按类型单例，调账按event_id/sequence全集核对。最终session49686以成功两笔调账、失败一笔调账和坏交付Outbox反例通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-SNAPSHOT-005 / P1`：103条历史的多次全链验证超过固定30秒proof，末尾Validate失败。保持30秒不变，Builder开始及每页完成后用同proof续期；session23276总时长112.37秒仍通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-SNAPSHOT-TEST-001 / P2`：Provider失败夹具只Poll一次仍为processing，提前Release被正确拒绝；补第二次Poll和Fetch后形成真实明确失败闭环。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-SNAPSHOT-TEST-002 / P2`：Outbox损坏/恢复用普通Update改变updated_at，零写快照正确发现差异。改用UpdateColumns固定原时间，仅变目标payload并字节级恢复。状态`FIXED_PENDING_VERIFY`。

## 2026-09-04 ready与跨系统协调增量

- `G7-CAPACITY-COORDINATOR-001 / P1`：Complete未在写MySQL前绑定prepared与proof/store/current的epoch、policy、run_id和快照，session61324以旧prepared配新proof实际观察到MySQL被改写。增加发布前四方核验和双侧零写反例后，session87620 native及session67154 Linux race通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-COORDINATOR-002 / P1`：Prepare后Redis键被加TTL、删除或替换时，旧流程仍先发布MySQL ready。Complete现先Inspect完整staged快照；TTL漂移反例确认MySQL和Redis无协调器附加写入。session87620/67154通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-COORDINATOR-003 / P1`：MySQL ready提交后，Redis Activate若在执行前确定失败会保留rebuilding；旧重试要求Redis已经ready，无法收敛。新增EVAL调用前故障，首调确认DB ready与Redis rebuilding，第二次同prepared只读确认DB并补做Activate；session87620/67154通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-READY-001 / P2`：000113重建CHECK时漏掉`capacity_lease_until IS NOT NULL`，NULL可能以UNKNOWN通过MySQL CHECK。CHECK及claim/renew Trigger均补显式非空，真实SQL反例确认零写拒绝。session1266 native及session28590 Linux race通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-READY-002 / P2`：ready审计列、唯一索引、Trigger及恢复CHECK的DDL部分成功后不可安全重跑。现分别探测列/索引/CHECK并在三个Trigger前DROP IF EXISTS；runner实际保留列、删除CHECK/索引/Trigger后重放000113，恢复为CHECK1、索引1、Trigger3。session1266/28590通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-RUNNER-002 / P1`：all/Finance原把snapshot、ready、coordinator放入同一不可回退门闩库，且未显式Redis也要求coordinator。三者现分别进入新MySQL，coordinator只在`-Redis`时纳入并取得独立Redis；子组成功后才计入父总数。独立工程静态复核未发现假PASS；完整all/Finance动态复验仍待后续总门禁。状态`FIXED_PENDING_VERIFY`。

以上结果只关闭ready与协调器局部缺陷；业务运行时、多进程、MinIO、监控、回滚及完整阶段终审仍未完成。

Redis确认与释放增量：

- `G7-REDIS-RELEASE-003 / P2`：记录不存在时Redis无法区分原释放重放、从未预留或其他nonce；若业务可直接调用将把持久终态证明留空。confirm/release现保持包内私有，只允许后续MySQL业务协调器调用；文档明确`released`只表示当前无容量记录，不证明调用身份。状态`FIXED_PENDING_VERIFY`，上层协调器仍待实现。
- `G7-REDIS-CONFIRM-TEST-001 / P2`：首轮只检查单记录phase/count，未直接证明promoting转running后queued名额恢复，也未覆盖过期债务清理。新增同用户第三任务在confirm前拒绝、confirm后成功，过期promoting/running的confirm拒绝、不同nonce零写拒绝及exact release清债。session80555 native与session56671 Linux race同源14/14通过，server哈希218f7ee7。状态`FIXED_PENDING_VERIFY`。

仍待业务协调器证明：不存在记录的release不会在缺少MySQL安全终态时被调用；pending_reconcile和未知Provider不会因技术租期过期自动释放；两路promoting确认及confirm/release并发不会突破Provider hard cap。

多进程容量能力增量：

- `G7-CAPACITY-NONCE-001 / P1`：恢复快照nonce原由单一恢复进程内的临时proof派生，ready后其他2/4/8进程无法重建同一能力，不能安全Renew/Confirm/Release。新增独立32字节`capacity_nonce`仓库外密钥及HMAC域分离，Builder强制注入该密钥并移除proof派生入口；session224880先以接口不存在形成编译RED，随后固定向量、epoch/完整身份/不同密钥分离、值/指针脱敏测试通过。新源码下完整快照session51344/46988及协调器session54386/98536的native/Linux真实MySQL+Redis均通过，server哈希53c692bb。状态`FIXED_PENDING_VERIFY`，多进程运行时仍待验收。
- `G7-CAPACITY-NONCE-002 / P2`：容量密钥用途、轮换和跨进程证据原不完整。配置新增第十个独立用途，精确32字节，显式验证与Redis密码同值整包拒绝；冻结“关闸→停止旧容量写→新epoch完整恢复→全实例装载新密钥→恢复流量”的轮换顺序。后续2/4/8独立进程均使用同一受限Bundle副本重建能力，临时副本按退出路径清理，完整epoch恢复门禁通过。状态`FIXED_PENDING_VERIFY`；测试服轮换仍随远端授权包执行，不计本地开放缺陷。

容量执行与发送权增量：

- `G7-CAPACITY-EXEC-001 / P1`：计划更新后Task当前版本被误当原claim版本，Provider门必然拒绝。GatewayTask新增隐藏冻结claim版本，校验/回执使用原claim、CAS继续使用当前版本；端到端Gateway回执和100并发通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-EXEC-002 / P1`：DB committed恢复未核计划/容量事件，坏财务事实可获running，历史NULL epoch计划无法收敛。现统一核三事件、held/pending/holding及无Usage/释放事实；历史计划只补epoch/发送权，缺事件双侧零写拒绝。session70982/99976及65100/26367通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-EXEC-003 / P1`：新协调器写入非NULL容量epoch后，旧快照校验仍要求NULL，导致下一轮恢复全局失败。快照现接受不高于新恢复epoch的首次授权，核原epoch事件并用新HMAC nonce重建；session65133/32515通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-SEND-001 / P1`：Fake任务计数位于Provider幂等去重后，session70631真实观察100并发进入Submit 88次但任务仅1，掩盖高成本重复RPC。000115增加持久发送权CAS和唯一事件，只有匹配明文permit的赢家能在所有门禁后消费；session70982/99976入口1、任务1。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-SEND-002 / P1`：permit消费后、Provider入口前崩溃可能无Provider ID可查。冻结为失败关闭：不得重提；原两分钟观察窗后进入pending_reconcile，Hold与running容量继续保留。session1085/49954验证Provider请求0、重启无permit、任务收口且不退款。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-EXEC-004 / P2`：原只有单进程goroutine，不能证明跨进程Provider hard cap。新增2/4/8真实Go子进程，各自独立连接MySQL/Redis并领取Worker lease；native/Linux六轮严格得到running=2，余量queued。状态`FIXED_PENDING_VERIFY`；真实HTTP/Rabbit进程仍属后续缺口。
- `G7-CAPACITY-RESERVE-001 / P1`：Redis ReserveQueued执行成功但回复丢失时，原admission在成功返回后才保存attempt，MySQL回滚会留下幽灵queued。现EVAL前登记确定性attempt，外层确认Task不存在后才release；显式故障Hook及零Task/零容量通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-RESERVE-002 / P1`：共享`seen[task_id]`可能让一个回滚调用清理另一个未提交调用的同attempt。每次Reserve改用独立财务服务浅拷贝与独立admission；100并发同意图仅一个Request/Task/Hold/queued。session33793/27836通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-RELEASE-001 / P1`：终态若只按Task状态调用Redis release，可能提前释放pending、未结算或输入仍租用的Provider债务。`ReleaseTerminal`复用完整财务/Outbox/Input/Provider终态验证；安全cancelled释放，pending_reconcile、跨归属和坏事实拒绝。session45067/95205及96157/28025通过。状态`FIXED_PENDING_VERIFY`。

## 2026-09-04 基础设施终审缺陷关闭

- `G7-OBJECT-PAGINATION-001 / P1`：双向对象扫描每轮固定读取首页，超过limit的尾部对象永久饥饿；000119增加MySQL持久游标、每前缀独立续页和尾部回卷，真实MinIO跨页及新Scanner实例续扫通过。状态`FIXED_PENDING_VERIFY`。
- `G7-OBJECT-PREFIX-001 / P1`：输出Store实际键以`vid_`开头，扫描白名单只含`video_`，会漏掉当前视频对象。现以`vid_`为当前前缀并保留历史`video_`兼容，真实MinIO清单通过。状态`FIXED_PENDING_VERIFY`。
- `G7-OBJECT-MISSING-001 / P1`：`video_object_missing_reconcile`只有任务没有Worker。新增有界Missing Worker；confirmed观察直接阻断输入绑定/Provider提交和输出交付，同摘要对象重现后原子resolved。状态`FIXED_PENDING_VERIFY`。
- `G7-OBJECT-RETRY-001 / P1`：孤儿补偿固定一分钟无限重试；现9次上限、指数退避封顶、dead/manual_review不再自动领取。状态`FIXED_PENDING_VERIFY`。
- `G7-OBJECT-RESOLVE-001 / P1`：观察resolved后原补偿仍pending，队首永久阻塞后续任务；未领取pending/retry现与观察原子completed，running由原Worker收口。状态`FIXED_PENDING_VERIFY`。
- `G7-MINIO-POLICY-001 / P1`：普通启动只检查Bucket存在，匿名策略漂移仍会启动。Verify现只读确认四Bucket无策略，无法判断或非空即失败关闭；公开读策略负例通过。状态`FIXED_PENDING_VERIFY`。
- `G7-NATIVE-CACHE-001 / P1`：Native Adapter按request_id永久保存提交map导致无界内存增长；移除非权威缓存，完全依赖MySQL发送许可。构造器同时强制禁止重定向并拒绝尾随/重复键JSON。状态`FIXED_PENDING_VERIFY`。
- `G7-RETENTION-FAIRNESS-001 / P1`：pending_delete首页保护项会饿死尾部且upload可饿死import。000119持久ID游标与轮次配额已覆盖失败关闭、跨页和Worker重启。状态`FIXED_PENDING_VERIFY`。
- `G7-RETENTION-SESSION-001 / P1`：24小时未完成UploadSession没有后台收口。000120要求墓碑确认后写expired/control cleaned_at并追加不可变事实。状态`FIXED_PENDING_VERIFY`。
- `G7-RETENTION-OUTPUT-001 / P1`：输出父子资产无到期Worker。000121复用原媒体删除账本和财务对账删除五项交付对象，保留审核副本及全部账务事实。状态`FIXED_PENDING_VERIFY`。
- `G7-RUNTIME-SHUTDOWN-001 / P1`：runtime取消后立即关闭依赖且后台错误被吞掉。现WaitGroup等待九类组件退出，依赖后关；健康状态记录up、失败计数和最近成功。状态`FIXED_PENDING_VERIFY`。
- `G7-MONITORING-001 / P1`：单依赖失败会使全部视频指标消失，且缺组件、对象容量和清理失败指标。现MySQL/Redis/RabbitMQ独立降级，10条规则和8面板通过。状态`FIXED_PENDING_VERIFY`。
- `G7-ROLLBACK-INFLIGHT-001 / P1`：旧回滚只验证空运行。现保留真实pending_reconcile、hard-cap下queued、两个holding Hold及提交计划/发送事实，110–121十二个down前后13字段一致并关闭态重启。状态`FIXED_PENDING_VERIFY`。
- `G7-FINANCE-RUNNER-001 / P1`：Finance主组合原未隔离`TestVideoG7CapacityCutoffMySQL`，该测试按合同把不可回退门闩留在blocked，随后60项G5/G7财务用例统一失败为“视频生成治理暂不可用”。生产门闩保持失败关闭，仅把cutoff放入独立临时数据库；修复后的完整Linux race得到22组、166/166、SKIP0、Finance 99/99且清理通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CAPACITY-AUDIT-RUNNER-001 / P1`：`TestVideoG7CapacityRecoveryAuditTypesMySQL`虽有Focus入口，却未加入all/Finance必跑集合，存在完整门禁漏跑风险。现显式加入required并使用独立数据库，完整组合中1/1通过且总计数包含该项。状态`FIXED_PENDING_VERIFY`。

## 2026-09-05 最终门禁复审修复

- `G7-OUTPUT-RETENTION-FAIRNESS-002 / P1`：输出留存固定读取最小Task ID，长期受保护前缀会饿死尾部。新增`retention|output|available`持久数值游标、CAS推进和周期回卷；真实limit=1反例先红后绿，受保护资产不删、尾部第二轮删除、解除保护后下一周期收口。状态`FIXED_PENDING_VERIFY`。
- `G7-NATIVE-FETCH-RESTART-001 / P1`：Native Adapter只在Poll进程内缓存内容URL，跨进程Fetch误报Provider任务不存在。移除内容map；OpenContent/Delete只凭持久taskUUID重新getResponse并校验同源URL、MIME和大小，跨Adapter与短效URL漂移测试通过且create=1。状态`FIXED_PENDING_VERIFY`。
- `G7-API-SHUTDOWN-001 / P1`：API入口未捕获SIGINT/SIGTERM，RegisterOnShutdown回调也不保证等待视频Runtime完成。新增`RunContext`同步执行HTTP排空、九组件收口和依赖后关；收口失败向进程入口传播并保留依赖。状态`FIXED_PENDING_VERIFY`。
- `G7-RABBIT-TERMINAL-FINANCE-001 / P1`：Rabbit Fetch成功只推进Task，不在Worker租约内结算、交付和释放容量；原容量Ledger还把Worker租约释放误当容量释放。两类租约已分离，新增必需终态协调器在Fetch租约内执行原G5三步闭环，任何失败不ACK；真实Broker专项先红后1/1绿。状态`FIXED_PENDING_VERIFY`。
- `G7-ROLLBACK-MESSAGE-001 / P1`：原回滚证据只比较在途字段，没有让真实Rabbit消息经兼容Worker收口。业务专项现先入队再创建兼容Worker，完成submit/poll/fetch、结算、交付和容量释放，并重放迟到消息验证财务快照不变、Provider create=1。状态`FIXED_PENDING_VERIFY`。
- `G7-SOURCE-STATE-001 / P1`：旧生成器只按文件manifest计算ID并硬编码runtime/finance/scan PASS。生成器现绑定HEAD、BASE、origin/main提交、脱敏URL、fresh/cached来源、观察时间、原始tracked patch、untracked manifest和采集时间；只输出WORKTREE身份，不输出门禁PASS。状态`FIXED_PENDING_VERIFY`。
- `G7-RUNNER-CLEANUP-002 / P2`：Runtime与MinIO验证器忽略网络/卷删除退出码且只检查容器。现逐项检查删除退出码，并分别验证容器、网络、卷零遗留。状态`FIXED_PENDING_VERIFY`。
- `G7-CURSOR-GUARD-001 / P2`：migration119允许插入非零初始游标，且DB/存储方向更新没有字典序单调约束。新增INSERT初态Trigger和三方向严格推进/周期回卷约束；运行时加入非法初态及非单调更新反例。状态`FIXED_PENDING_VERIFY`。
- `G7-RETENTION-POLICY-001 / P2`：输入7天、上传/导入24小时和审计版本散落在多个服务文件。新增统一`videoRetentionPolicy`并让所有生产入口读取同一版本化策略。状态`FIXED_PENDING_VERIFY`。
- `G7-MIGRATION-REENTRY-001 / P2`：119—121只有首次up证据。运行时验证器现对两套schema删除各一个Trigger后重跑原up，要求三组Trigger均恢复为3。状态`FIXED_PENDING_VERIFY`。
- `G7-RUNBOOK-LINK-001 / P2`：10条告警链接指向不存在的文档锚点。全部改为实际存在的`video-gateway-monitoring.md#告警与处置`。状态`FIXED_PENDING_VERIFY`。
- `G7-DOC-STATE-001 / P2`：API、数据库和测试计划仍保留当前“尚未装配/验证中”断言。已按本地实现、测试服未授权的证据边界收敛，不篡改历史执行记录。状态`FIXED_PENDING_VERIFY`。
- `G7-SENSITIVE-EVIDENCE-001 / P2`：敏感扫描文件数被硬编码且未绑定SOURCE_STATE。新增manifest逐文件哈希复核、UTF-8/二进制分类、规则命中和SOURCE_STATE绑定回执。状态`FIXED_PENDING_VERIFY`。
- `G7-DEFECT-LEDGER-001 / P1`：历史台账使用非规范关闭状态，无法机械计算开放缺陷。所有历史项先统一降为`FIXED_PENDING_VERIFY`；新增结构化生成器，只有当前SOURCE_STATE测试和独立QA复核后才能转`CLOSED_VERIFIED`。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-RUNBOOK-001 / P1`：测试服实际回滚模板在执行down后才引用未声明的`BACKUP_DIR`，安装模板也未把SOURCE_STATE约束到镜像。现将备份路径、主机指纹、镜像和五项关闭态检查全部置于任何写之前；API镜像增加commit/SOURCE_STATE OCI标签，安装前同时核标签、digest和Compose引用。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-CHECKOUT-002 / P1`：授权模板仍可能使用获批镜像配另一套checkout migration，且备份只有dump/hash而没有恢复验证。写前现核`git HEAD`、SOURCE_STATE ID及207+项manifest逐文件哈希；固定关闭态Compose覆盖、正式`migrate up/down 12`及109↔121版本；备份恢复到精确临时schema并比较Chat/Image/Project SK/钱包聚合基线后才允许迁移。状态`FIXED_PENDING_VERIFY`。
- `G7-SENSITIVE-SCOPE-002 / P1`：绑定式扫描只覆盖SOURCE_STATE manifest并永久排除证据目录。扫描器现合并manifest、基线差异和未跟踪文件，源码逐文件验hash，证据虽不参与自引用ID但全部进入泄漏扫描并记录candidate_count。状态`FIXED_PENDING_VERIFY`。
- `G7-FINANCE-RUNNER-BINDING-001 / P2`：Finance回执只绑定server哈希，运行器改变必跑集合后旧回执仍可能通过。最终证据校验现要求回执`runner_sha256`匹配当前脚本。状态`FIXED_PENDING_VERIFY`。
- `G7-RUNTIME-CLEANUP-PARTIAL-001 / P2`：Runtime容器创建半失败时可能未加入created集合。finally现始终按预声明的MySQL、Redis、RabbitMQ、MinIO、builder和rollback builder精确名称回收，再核容器/网络/卷零遗留。状态`FIXED_PENDING_VERIFY`。
- `G7-DEFECT-LEDGER-PARSER-002 / P2`：结构化台账把24条Markdown表格解析成整行或仅ID摘要。解析器现显式读取原因与修复证据列，并对空摘要、仅ID或含竖线结果失败关闭。状态`FIXED_PENDING_VERIFY`。
- `G7-MATRIX-STATE-002 / P2`：阶段矩阵在绑定式扫描通过后仍写待执行。已更新为`LOCAL_FINAL_GATES_PASS`，同时保留独立复审和测试服未授权边界。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-DB-003 / P1`：授权模板未锁定MySQL host/port/user/凭据和`@@server_uuid`，正式migrate可能回落默认实例。安装与回滚现要求同一受限0400/0600密码文件、显式客户端参数、导出给migrate的同一连接字段及写前server_uuid等值校验。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-RESTORE-COLLISION-004 / P1`：确定性恢复库名可能与既有schema碰撞并被trap误删。恢复库现绑定授权CHANGE_ID，CREATE前确认不存在；只有CREATE明确成功后才置清理资格，写入commit/ChangeId marker，DROP前再次精确核marker，不匹配则拒绝删除。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-VERSION-005 / P1`：安装后错误要求固定`/api/version`包含commit，会在数据库和API变更后必然失败。源码身份现仅以写前manifest、镜像OCI标签和启动容器实际digest三方确认；版本端点只验证可用性。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-API-DB-006 / P1`：migration使用显式Shell MySQL身份，而API可能从独立ENV_FILE连接另一实例。Compose现强制覆盖获批database/user/password及容器内host/port；启动后核容器实际环境低敏字段和密码摘要，并在API内部网络用锁定MySQL客户端验证`@@server_uuid:DATABASE()`与迁移目标完全一致。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-EXACT-VARS-007 / P1`：授权正文未列出脚本全部MySQL、环境文件和回滚镜像变量，授权后仍可换目标。精确授权文本现列出宿主/API连接、库、用户、受限密码文件及摘要、环境文件及摘要、前后镜像、MySQL客户端、备份和ChangeId；脚本在任何写前逐项验格式、权限、hash和server_uuid。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-VAR-NAME-008 / P2`：授权正文的主机指纹和备份目录变量名与脚本不一致，原样执行会停在HUMAN_REQUIRED。正文已统一为`EXPECTED_HOST_FINGERPRINT`与`BACKUP_DIR`，不允许临场映射。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-INSTALL-PREFLIGHT-009 / P1`：安装块未重复核主机指纹，且`set -u`下直接读取未初始化关闭开关。安装现把`EXPECTED_HOST_FINGERPRINT`列为必需值并在写前等值核验；四开关使用失败关闭默认false，Compose仍显式强制false。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-PASSWORD-ARGV-010 / P1`：API网络身份探针把MySQL密码展开进Docker CLI参数，可能被共享主机进程观察或审计采集。两处均改为`-e MYSQL_PWD`只从父进程继承已设置环境值，argv不再含密码正文。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-ROLLBACK-SOURCE-011 / P1`：回滚down前未重新核源码身份，checkout漂移时可能执行未授权migration。回滚现写前重验HEAD、SOURCE_STATE ID和manifest逐文件哈希。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-BACKUP-NOCLOBBER-012 / P1`：BACKUP_DIR可复用并覆盖旧恢复点。安装现要求`vid-g7-$CHANGE_ID`绝对新目录、父目录非链接、目标不存在，启用noclobber并写0600 owner marker；回滚精确复核marker。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-VIDEO-FACTS-013 / P1`：回滚基线未比较视频Request、Task、Callback、Usage、Asset、Event、Outbox、Hold和审计事实。九类视频事实已加入安装前、安装后和回滚后同一摘要逐字节比较。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-FACT-CONTENT-014 / P1`：旧基线只比较行数，无法发现状态、金额、归属、版本或摘要原位漂移。新增单一受审快照脚本，在一个RR一致性事务中按固定列和主键顺序计算行SHA-256，再输出低敏`table|count|aggregate_digest`；覆盖Chat/Image/Key/钱包及完整视频事实，四时点逐字节比较。状态`FIXED_PENDING_VERIFY`。
- `G7-AUTH-FACT-RR-015 / P1`：旧逐表mysqldump使用多个时间点且109直接读取117—121新表，既可能形成半事务快照也无法跨Expand Schema比较。109现生成列manifest并复用于restore/post-install；121再生成expanded manifest供down后复用；两层均由单连接RR事务生成，空表有marker和empty digest。实际隔离演练两组均通过且清理0。状态`FIXED_PENDING_VERIFY`。
- `G7-FACT-EVIDENCE-016 / P2`：事实快照回执未分别记录两轮实际演练，最终证据校验也未读取。回执现包含两个session、各自范围、比较项、exit和cleanup；最终校验器重算脚本hash并强制run_count=2及三类比较全部通过。状态`FIXED_PENDING_VERIFY`。
- `G7-FACT-MANIFEST-INJECTION-017 / P1`：事实快照输入manifest的WHERE原样拼接到多语句SQL，恶意文件可先COMMIT再执行DDL。脚本现按base/expanded内建合同逐项核表集合、顺序和WHERE，拒绝缺失、重复、额外或变形项；session55142实际注入`COMMIT; DROP TABLE wallets`被拒绝、原表仍存在，正常restore/expand_down 2/2通过。状态`FIXED_PENDING_VERIFY`。
- `G7-RABBIT-POISON-018 / P1`：非法正文、Content-Type或非持久消息令Worker组退出后，Runtime原每2秒无差别重启，毒消息会热循环并饿死合法消息。现从非法正文只提取SHA-256，在MySQL追加持久熔断审计；当前进程立即停止该stage，新进程启动也先读熔断状态。管理员精确绑定熔断审计ID、stage、摘要、权限、双MFA、原因HMAC和前后审计后才能ACK非法队头；合法消息受保护。真实Rabbit消费专项和runtime15/15通过。状态`FIXED_PENDING_VERIFY`。
- `G7-RABBIT-DLQ-RECOVERY-019 / P1`：原实现只有PublishDead和DLQ检查，没有按原Task/Request、状态、attempt、权限及审计恢复的闭环。新增受控恢复入口，保持原attempt，重新核Task/Request/operation/InputAsset/version/Provider绑定和阶段状态；恢复请求、发布结果、管理员权限/MFA及前后审计持久化，publisher confirm和完成事实成功后才ACK。已发布重放只ACK，未知结果保留DLQ，不重置attempt或盲目Submit。真实Rabbit和MySQL专项通过。状态`FIXED_PENDING_VERIFY`。
- `G7-APP-LISTEN-SHUTDOWN-020 / P2`：API监听失败后的Runtime收口分支没有直接测试。新增占用loopback端口反例，要求Listen失败仍同步调用视频Runtime shutdown并返回原错误；核心bootstrap测试通过。状态`FIXED_PENDING_VERIFY`。
- `G7-RUNTIME-FAILURE-COVERAGE-021 / P2`：Consumer瞬态故障健康降级/重连、毒消息不重启、跨进程持久熔断和Shutdown超时生命周期保留缺少直接注入验证。新增四项确定性测试并纳入完整runtime动态必跑集合；session76501最终15/15 Linux race、回滚和清理通过。状态`FIXED_PENDING_VERIFY`。
- `G7-ADMIN-RECOVERY-IDEMPOTENCY-022 / P1`：DLQ与毒消息入口原只记录Key哈希，同一管理员可用同Key改绑另一Task、stage、fuse或正文摘要。两入口现使用同一Key哈希域，以管理员users行`FOR UPDATE`串行后查询既有前审计，逐项冻结kind、Task、Request、stage、attempt、Task版本、fuse审计ID和正文摘要；同Key异意图409。MySQL专项包含跨Task和跨入口反例。状态`FIXED_PENDING_VERIFY`。
- `G7-DLQ-COMPLETE-RACE-023 / P1`：工作消息publisher confirm后可能先被Worker推进Task，完成审计原仍要求旧Task版本/阶段，导致工作已执行但DLQ永久未ACK。完成阶段现只依赖发布前已冻结的request事件、原消息身份和当前权限，不要求Task停在旧版本；Prepare重放先核requested+published事件再检查新恢复状态。MySQL测试在Prepare后推进版本仍完成，联合ACK未知重放只补ACK。状态`FIXED_PENDING_VERIFY`。
- `G7-POISON-REORDER-024 / P1`：一个Worker发现毒消息并取消组时，其他最多7条未ACK合法消息可能重排到毒消息前方；单条管理Consume会反复遇到合法消息。处置连接现以prefetch=8有界暂存合法消息，找到同摘要毒消息并完成审计后ACK，再逐条requeue合法消息；找不到则全部保留。真实Rabbit用例以合法在前、毒消息在后验证。状态`FIXED_PENDING_VERIFY`。
- `G7-POISON-E2E-025 / P2`：原跨进程熔断测试仅替换`poisonState/poisonBlock`函数，未贯通MySQL。新增真实MySQL测试：Runtime A调用生产`blockRabbitPoison`写熔断，Runtime B从同库调用生产`rabbitPoisonBlocked`拒绝启动，追加受控恢复事实后Runtime C放行。状态`FIXED_PENDING_VERIFY`。
- `G7-DLQ-UNKNOWN-E2E-026 / P2`：原Rabbit测试使用内存Handler、MySQL测试绕过Broker，未联合覆盖confirm丢失、发布后完成审计失败和完成后ACK未知。新增同一隔离MySQL+真实Rabbit三场景：均核工作/DLQ消息与TaskEvent；ACK未知后同恢复命令只补ACK且不产生第二条工作消息。状态`FIXED_PENDING_VERIFY`。
- `G7-RUNTIME-RUNNER-PACKAGE-027 / P2`：runtime正则列出四个父包测试，但发现和执行包清单漏掉`./internal/modules/token_gateway`，15/15未实际包含。两处包清单已补父包，并新增真实MySQL熔断和联合Rabbit故障测试；session49633动态发现22项、22/22 Linux race、SKIP0、回滚和清理通过。状态`FIXED_PENDING_VERIFY`。
- `G7-DLQ-OUTBOX-028 / P1`：管理员恢复原直接向Rabbit发布，publisher confirm未知时只能永久保留DLQ，后续命令又会与已存在requested事实冲突。恢复请求现原子写TaskEvent与专用Outbox，统一Relay承担确认未知与重试；只有Outbox published和完成事实齐备后才ACK原DLQ。最新runtime 22/22及Finance 166/166通过。状态`FIXED_PENDING_VERIFY`。
- `G7-RABBIT-FUSE-CONSTRAINT-029 / P1`：仅靠审计推断熔断状态可被同名普通审计伪造恢复。migration122新增固定三行持久熔断表、外键、CHECK及插入/更新/删除Trigger；阻断与恢复必须绑定系统/管理员审计、原摘要和CAS版本。真实MySQL跨Runtime与负向SQL通过。状态`FIXED_PENDING_VERIFY`。
- `G7-POISON-MULTIPROCESS-030 / P1`：8进程各自可能把一条合法消息回队，管理扫描prefetch=8仍可能看不到第9条毒消息。受控扫描上界改为9，真实Rabbit以8条不同合法消息在前验证毒消息可达、合法消息各返回一次且身份不变。状态`FIXED_PENDING_VERIFY`。
- `G7-FUSE-MIGRATION-REENTRY-031 / P2`：migration122重放时既有固定集合INSERT Trigger会在`INSERT IGNORE`前失败。up迁移现先移除三条旧Trigger、只补缺失固定行、再完整重建三条约束；双库重放、非法插入、无审计更新和删除反例均通过。状态`FIXED_PENDING_VERIFY`。
- `G7-FUSE-RECOVERY-CHAIN-032 / P1`：migration122原只要求恢复审计带operator，任意主体可直接插入最小审计并更新fuse，旁路受控管理入口。更新Trigger强制恢复事实按ID顺序绑定同一操作者、同一command key摘要、reason HMAC、fuse、stage和body的before/after/recovered三段链；新增“有operator但缺链”数据库反例。修复后runtime 22/22、Finance 166/166、Fact 2/2通过。状态`FIXED_PENDING_VERIFY`。
- `G7-SOURCE-DESCENDANT-033 / P1`：证据后代规则的diff-filter漏掉删除状态D，后续证据提交可能删除未进入阶段manifest的基础文件。校验器现同时纳入D并用排除`docs/evidence/**`后的内容diff作第二道拒绝；临时后代真实删除`server/go.mod`时稳定失败，临时工作树清理通过。状态`FIXED_PENDING_VERIFY`。
- `G7-CI-DIFF-FORMAT-034 / P2`：本地仅对未提交工作树执行`git diff --check`，未按CI的`origin/main...HEAD`范围检查；PowerShell生成JSON使用CRLF并有四个Expand-only migration多余尾空行。生成器现固定UTF-8无BOM和LF，全部46个证据文件机械规范化，四个SQL仅删除尾空行；本地分支全量diff检查通过。状态`FIXED_PENDING_VERIFY`。
