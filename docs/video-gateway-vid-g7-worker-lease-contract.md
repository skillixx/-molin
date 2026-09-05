# VID-G7 Worker执行租约合同

## 功能与边界

本功能供视频提交、轮询和抓取Worker协调器使用，在原Task上记录唯一执行持有者，避免重复投递和接管后的旧Worker继续推进任务。不新增任务、财务或租约平行账本，不新增客户端接口。T2V/I2V使用同一机制。

当前为租约基础仓储和自动10秒心跳执行器切片，不代表完整G7运行时完成。业务消费者、Provider请求/媒体IO/财务的所有写入边界、实际kill恢复和Redis容量租约尚待接线验证。

## 数据与状态

新增migration `000110_video_worker_leases.up.sql` 扩展原表：

| 原表 | 字段 | 规则 |
|---|---|---|
| ai_gateway_tasks | lease_version | 从0开始，每次认领递增；不同于业务version_no |
| ai_gateway_tasks | lease_owner、worker_stage | 持有者与submit/poll/fetch技术阶段；不表示用户身份或业务执行态 |
| ai_gateway_tasks | heartbeat_at、lease_until | UTC数据库微秒时间，期限固定为心跳加30秒 |
| ai_gateway_tasks | worker_lease_active | 1仍须检查期限；0保留最后持有者、代次、心跳与截止历史 |
| ai_gateway_task_events | worker_lease_version、worker_lease_owner、worker_lease_stage | 认领/释放时的不可变技术快照；普通事件不得填写 |

新Task必须空租约初态。认领只允许无人持有或原租约已过期；相同Worker名称也不能冒用未过期租约或复用旧代次。续期不递增代次，释放不删除历史。过期持有者不能续期或释放；新持有者接管不要求旧进程先写release事件。

租约变更不修改业务version_no、执行状态、Provider尝试数、输入绑定或财务。Claim/Release与事件写入位于同一事务；MySQL Task/Request锁之后读取数据库时钟，不使用请求抵达时间授予租约。事件ID固定由Task公开ID、代次、事件类型生成SHA-256，并由SQL触发器和原唯一键约束，不能另造同代次审计副本。

## 代码与调用

- `repository/video_worker_lease.go`：Claim、Renew、Release、Validate及内部证明传递。
- `repository/video_task_repository.go`：Worker执行迁移与Provider绑定的租约校验。
- `service/video_submission_receipt.go`：Provider提交前的Claim校验和截止时间收紧。
- `repository/video_asset_event_payload_repository.go`：禁止通用事件追加入口伪造租约事件。

租约证明字段私有，不能通过HTTP/MQ反序列化制造；公开格式化只返回固定低敏标记。Worker使用`WithVideoWorkerLease`把仓储授予的证明带入下游事务。校验只是当前写入点有效，不是跨越长时间外部IO的永久授权；后续业务消费者必须续期、取消过期IO并在写入前重验。

原有零代次任务保留历史兼容路径。已经进入租约管理的Task不能只凭新的业务version_no绕过当前持有者。迟到回执、归档围栏及所有结算路径的完整联动仍列为阶段待办，不以本切片冒称已覆盖。

## 测试与证据

执行入口：`infra/scripts/verify-video-gateway-vid-g7-outbox.ps1 -Focus lease`，Linux race增加`-LinuxRace`。必须显式授权本机隔离MySQL；runner使用锁定镜像，校验完整migration、必需RUN/PASS和无SKIP，结束仅清理本轮资源。

七项Linux race专项已在同一源码状态通过：100并发唯一持有者、真实30秒到期接管、旧Worker写入围栏、SQL空初态/确定性事件ID/最大代次、真实事件INSERT失败回滚、身份拒绝/取消context，以及I2V待对账输入保护。新增临时实例绑定后，session99067的124/124组合race通过，含全部99项G5与真实RabbitMQ，SKIP=0、清理通过。具体有效源码与局限以租约验证回执为准，历史PASS不绑定后续源码。

后续自动心跳增量位于`service/video_worker_heartbeat.go`：执行前认领，10秒续期，失败取消工作；工作同步退出后停止并等待心跳，再以独立5秒context释放原技术租约。它尚未接到业务消费者；忽略context的Go代码不能被本执行器强制终止，取消后心跳停止，原租约到期仍可被接管，旧代码必须在写入/外部IO处复核围栏。该增量由新的三项专项和29项组合race验证，不继承前述124项的源码绑定。

构造入口`NewVideoWorkerLeaseRunner(db)`拒绝缺少数据库；`Execute(ctx, VideoWorkerExecution, work)`接收内部Task/Owner/Worker/Stage及同步工作函数，空context或空工作被拒绝。工作通过派生context获得私有租约证明；心跳不换代。取消、续期失败、panic和释放失败不能返回成功；panic正文不回传。该接口的context取消只是停止当前技术工作，不等同于用户业务取消订单或释放资金。

专项命令为`verify-video-gateway-vid-g7-outbox.ps1 -Focus heartbeat -LinuxRace`。正例实际观察首轮10秒心跳，跨初始30秒截止后进行真实Task CAS；财务对照分别验证七表原样保留和Request仅执行态/版本/更新时间的预期变化。负例在绑定的临时MySQL注入续期错误，并让取消后的旧代码继续存活至新代次接管，验证迟到CAS与旧清理都不能覆盖新持有者；这不是操作系统进程强杀测试。

session11741三项race通过（10.28秒、30.59秒、30.46秒），之后独立审查要求把旧CAS错误从Execute合并错误中单独取证，并增加5秒写入期限；补强后的session50802完成29/29组合race，SKIP=0、清理通过。旧CAS与旧清理分别核对，新Task业务版本/状态也须不变，不能仅用新租约仍有效推断所有旧写入均被拒绝。最新心跳三项耗时为10.21秒、30.56秒、30.43秒，源码执行摘要为`e490668c7296a196b33e5662384ed4ef5f6b5ff76f063e1bc38395d23172f3d9`。

仍须补齐归档互斥、锁等待跨期、运行时10秒心跳及真实进程kill恢复。数值极限夹具只允许在一次性隔离库准备，恢复原触发器后才调用被测接口，不能冒充正常业务生成历史。旧G5环境标记不独立证明临时实例：极限夹具还会解析DSN、限制目标库与网络地址形状，并核对当前连接的库名及runner从新建容器读取的server_uuid；无绑定、错绑定直接失败，不SKIP。此绑定由runner传入并在结束恢复环境，不从业务配置取得。

## 提交回执写入围栏

`service/video_submission_receipt.go`把原回执分为身份写入和已接受重放两类。原Owner、Request、Quote、提交版本和回执摘要仍按G5核对；不新增HTTP字段、队列字段、财务政策或第二份任务。

| 情况 | 行为 |
|---|---|
| 尚未绑定、Task已进入G7执行租约 | 首次状态/Provider身份写入前校验当前证明；无证明、旧代次或过期均拒绝 |
| 正常submitting或pending直接绑定 | 绑定与接受事件仍在原事务；事务末尾按数据库当前时间再次核对租约，过期整笔回滚 |
| 已接受的相同回执 | 原提交身份和摘要一致时只读返回，不要求重新认领，不新增事件或资金写入 |
| 已接受但回执冲突 | 保留原G5低敏拒绝审计，不覆盖绑定、执行/财务事实 |
| 历史lease_version=0且无证明 | 保持原G5合同，不把历史任务强行升级为新租约 |

`WithoutCancel`只让有限时间的回执保存不受原RPC取消影响，不能移除其内部租约证明。当前持有者可以保存同一次原提交的回执，不得重新调用Provider；pending不能因此回退submitted或提前交付。普通提交分支则仍按原两分钟提交窗口判断是否可以submitted，G7的30秒执行租约是另一条独立约束。

测试入口新增`-Focus receipt`，自动发现并要求全部13项`TestVideoG5Submission*`实际RUN/PASS。新测试覆盖T2V/I2V的无证明及旧证明、当前证明正例、释放后的只读重放、输入保护及单次Fake提交。事务尾过期测试覆盖正常与pending两分支：先在事务外等到只剩两秒，再在已写入接受事件之后真实等到截止；必须命中该写入钩子并返回LeaseLost，而非入口拒绝或五秒context超时。Task、事件、八表、输入及原本不存在/存在的补偿任务均须回滚，新代次再保存同一回执。

首轮14/14真实MySQL通过；增加实际尾过期测试后，session5470的15/15 Linux race通过，包含原13项G5提交合同，SKIP=0、清理通过。两条真实尾过期分支合计60.86秒，源码摘要为`71253b87c6f290a6303922931ebb2c17ba90fdaba3fe46072be3dbf3fa16f991`。同源码session82352的31/31 G7/Broker组合复验也通过，SKIP=0、清理通过。该切片不代替结算、退款、共享恢复写入或外部IO的完整围栏接线，后者仍属G7待办。

## 普通财务写入与独立补偿授权

普通`SettleReady`、`ReleaseUnserviceable`的首次资金写入，在Task已进入G7租约管理时必须持有当前执行证明，并在原资金事务末尾再次检查数据库租期。钱包、Usage、Request、输入释放、Outbox及相关事件均处于原事务内，不能因执行权已失效而留下部分结算或退款。

已结算/已释放的事实重放仍按原账本、金额、媒体或退款依据核对；没有新资金写入时不要求重新取得普通执行租约。新增检查不修改价格、退款金额、成本确认或交付政策，也不新增HTTP接口。

`RecoverSettlement`、`RecoverRelease`保留G5的独立财务恢复授权：必须先验证同一请求、补偿任务ID、当前运行代次、锁时间、模式与期限；不能仅凭非nil参数跳过检查。补偿Worker使用有效补偿租约恢复，不强制重新启动普通执行任务或再次调用Provider。

`markVideoFinancialPending`是原事务回滚后的另一笔事务，必须重新锁定Task并在首尾验证普通证明。原错误本身不构成创建补偿及P/C事件的授权；`WithoutCancel`也不能移除原内部证明。确定失权直接停止补记，其他错误只有在补记事务重新验证当前执行权后才可记录恢复事实。

普通结算/退款及独立补偿三项Linux race已由session84514验证通过；错误请求的非nil补偿证明被拒绝，合法补偿无需重启普通执行租约。新增四个并行真实到期子例后，session55140的四项专项全部通过。随后session48490同源133/133组合通过，含完整99项G5，SKIP=0、清理通过；四个到期子例分别31.12、31.12、31.52、31.55秒，不能把顶层0.01秒当作总等待时间。共享恢复写入及用户/管理员取消属于不同调用边界，尚未在本切片中统一接线；不得据此宣称完整财务围栏或G7验收完成。

财务专项使用`-Focus financial_fence -LinuxRace`。四个到期子用例分别在`settle_lease`、`release_checked`或失败后新事务的`execution_required_outbox`钩子跨过实际30秒截止；大部分等待位于事务外。主事务合成错误与尾部到期须分别命中，不能用context超时冒充LeaseLost。失败后Task、事件、八表、输入及“补偿不存在”保持原样，再由新代次按原金额完成首次结算/退款。它们注入的是事务内合成故障与真实时间流逝，不是数据库断连或进程强杀。

## 观察接收权与执行权的后续边界

有界产品/Spec审查确认：G0第362行限制旧Worker提交Provider、推进终态和结算；G5第258、264、266行及G5-CANCEL-005则要求保留已验证原Provider任务的迟到矛盾观察，并同事务形成限定的安全恢复记录。不能给`ensureVideoRecoveryTx`直接套用“旧proof全部拒绝”，把观察一起回滚而重现旧缺陷。

后续须通过专用内部观察接收路径核验原任务、归属、Provider绑定、操作类型、真实历史执行证明与可信Adapter来源，允许低敏冲突摘要、合法确认观察及原合同限定的P/C或review_required；不恢复旧Worker的新Provider动作、终态推进、收费/退款、交付或输入释放权。不清除context证明，不提供通用bypass。该观察接收路径尚未实现，本次产品结论不是完整PM验收。

用户与管理取消继续保留原授权；携带内部Worker证明的取消才附加执行租约检查，已取消的只读重放不应被误伤。嵌入外层事务时，还须核对最外层提交边界，不能仅凭内部函数尾检查宣称全部覆盖。

基础取消已增加`CheckVideoWorkerContextLeaseTx`：缺少ctx/tx/Task或私有证明值畸形时拒绝；没有私有key仅表示不附加Worker限制，绝不是授权。普通Worker专属结算/退款/绑定仍使用强制检查。原G5取消在原授权和neverSubmitted验证后、首次写入前检查，并在整个退款事务尾再次检查；已取消的只读重放保留早返回。

session97947在T2V/I2V两条取消尾过期路径实际复现错误成功；修复后session12746的六个并行子例通过，包括当前Worker、原授权用户、真实尾过期回滚、Key吊销拒绝及旧证明只读重放。Scope为原G5数据库准入与基础取消事务，不代表G6 HTTP/MFA或私有管理入口已验收。Repo畸形证明单元测试已通过；同源session66425的36/36组合也通过、SKIP=0、清理通过，两个取消到期子例分别30.64与30.70秒。尚未给G6外层或Provider观察接收路径添加绕过开关。

## G6 外层取消事务增量（未提交任务路径已验证）

用户 `VideoHTTPService.CancelTask` 与管理员 `VideoAdminService.CancelTask` 的事务包裹原G5退款。在内层函数返回后，外层仍需读取详情、复验权限并提交命令/审计，不能用内层最后一次租约校验替代外层提交边界。首次新命令在原身份授权后、任何新写入前检查私有Worker证明，并在最外层权限复验后再检查；原无证明控制面操作仍需完整用户权限或管理员JWT/权限/MFA，已存在命令的只读重放不附加新租约要求。

测试 `TestVideoG7WorkerCancelOuterTransactionMySQL` 通过真实G6创建任务、价格、权限和G5钱包事务，覆盖T2V/I2V与用户/管理员。仅在数据库返回外层报价详情时延迟到实际30秒租约之后，检查命令、管理原因信封、前后审计、Task、事件、八张财务表和输入释放全部回滚。无证明控制面正例及旧证明只读重放继续验证。管理员JWT验签和数据库MFA为真实实现，吊销存储为内存替身；用户Caller在服务边界注入，不把本测试称为HTTP认证全量覆盖。

session68961先复现用户T2V外层到期仍成功，另三例因唯一生效版权政策冲突而未进入业务测试。已修正夹具：任务创建完成后精确退役自身合成政策，保留数据库唯一约束和历史接受事实。session42851不再发生政策冲突，管理员T2V/I2V两条外层到期路径仍错误成功（32.46/32.64秒）。两条入口均实现首尾检查；新增真实MFA拒绝、已到期证明入口拒绝及独立第二任务的有效Worker首次成功正例。session58972最终两组135/135通过、SKIP=0、Linux race和清理通过，最外层exit=0；外层四例分别33.49、33.35、33.48、33.54秒。此结果绑定server哈希f6372389，不重新绑定后续容量策略源码。

本增量验证尚未提交Provider的取消退款路径。`cancel_requested`、新命令针对既有终态以及完整HTTP路由兼容仍需补证，不能只凭本组服务测试宣称全部G6取消链已经验收。

组合测试隔离：session83706实际135项运行、134项通过、SKIP=0，唯一失败项为上述四个G6创建夹具。原Outbox并发测试保留110个reserved任务，真实G6全局queued=100正确拒绝后续创建。运行器现将`outer_cancel`放到独立临时数据库，成功并清理后再运行其余测试；完整门禁合计全部必需项，任一分组失败均失败。生产容量、创建准入和历史任务未修改。session58972已实际得到最外层exit=0和groups=2/required=135汇总，不是取子组先输出的PASS。

## 回滚执行限制

不部署测试服。应用回退先关新流量，保留兼容Worker处理在途任务。down保留新增字段、租约代次和追加事件，不DROP任何业务表或审计事实。本地已以真实`pending_reconcile`、hard-cap下queued、两个Hold及计划/发送事实完成110—121逆序12步兼容撤回、13字段前后对比和关闭态重启；共享测试服实际演练仍需独立授权。
