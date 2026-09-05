# VID-G7 容量恢复的持久代次与租约

状态：`READY_COORDINATOR_LOCAL_VERIFIED_RUNTIME_STILL_PARTIAL`。本增量在原MySQL门闩上提供持久代次、私有恢复租约和受约束ready摘要；它仍不提供业务任务提交、容量释放或Provider授权。

## 功能与使用者

使用者为后续G7后台恢复协调器，不是用户或管理员HTTP客户端。Redis丢失、实例切换或恢复失败时，需要一个不会随Redis一起消失的代次，防止旧恢复者覆盖新恢复结果。当前实现复用`ai_video_queue_admission_guard`及原`audit_logs`，不新增任务、资产或财务账本。

恢复代次与普通Task执行租约、容量尝试nonce、业务version_no不同，不能互相替代。持有本租约只表示当前恢复者身份；没有完整账本快照证明，仍然不能开放新视频流量。

## Schema与状态

`000111_video_capacity_recovery_epoch.up.sql`扩展原门闩：

| 字段 | 含义 |
|---|---|
| capacity_epoch | 从0开始的单调恢复代次 |
| capacity_state | uninitialized、recovering、blocked或ready；ready必须绑定000113快照摘要与审计 |
| capacity_policy_sha256 | 本次候选策略指纹 |
| capacity_redis_run_id | 本次候选Redis实例身份；仓储只校验格式，不执行Redis连接验证 |
| capacity_recovery_owner | 内部恢复者标识，不是用户身份 |
| capacity_token_sha256 | 私有随机nonce的SHA-256，原nonce不入库 |
| capacity_heartbeat_at / capacity_lease_until | 数据库时间心跳与固定30秒截止 |

初态为epoch0、uninitialized及全部空绑定。Begin取得新的epoch并进入recovering；只有未被占用或旧租约确实到期才可认领。Renew保持同epoch、owner和token摘要，只向后延长截止。Block进入blocked并保留原绑定/时间，不回退到初态。重新恢复必须增加epoch，不能复用原nonce。

原门闩version_no在容量变化时增加一次；单独回退或跳号也禁止，旧G6的锁读和id=id迁移重入不改变版本，继续允许。最大uint64边界已通过独立MySQL专项：session69481 native与session34808 Linux race分别验证末次认领、续期、阻断、耗尽拒绝、只读重放和SQL回绕拒绝；Linux绑定server源码d9362d78，1/1、SKIP0、清理通过。

## 仓储接口与事务边界

实现位于`repository/video_capacity_recovery.go`：

- `Current`读取状态、当前审计绑定及ready摘要，不发布ready。
- `Begin`校验expectedEpoch并认领30秒租约，认领及审计同事务提交。
- `Renew`要求当前完整私有证明，按取得行锁后的数据库时间续期。
- `Block`只允许有效持有者阻断；已blocked的同证明重放只读，新epoch后旧证明一律拒绝。
- `Validate`只做当前持有者诊断，不能把返回成功当成后续事务外操作的授权。
- `PublishReady`只允许同proof的recovering转ready；相同摘要重放只读，相反摘要或旧证明拒绝。
- `ValidateReady`只读核对epoch、policy、Redis run_id、快照摘要与数量；仍须协调器独立确认Redis同快照。

所有方法使用有界根事务；sql.Tx及GORM事务/PreparedStmtTX包装被拒绝，防止savepoint成功后提前借出尚未外层COMMIT的证明。合法根连接的PreparedStmt仍可使用。COMMIT报错时返回nil证明，不用补偿SQL清除可能已经提交的恢复占用；只能保留事实、受控核对或等待到期接管。

Begin/Renew/Block在写入完成后再次检查数据库截止，尤其Block审计写入过程中跨过期限时必须整笔回滚。私有证明显式实现JSON和格式化脱敏；禁止将它放入HTTP、MQ或普通日志。

## 审计事实

认领与阻断写入原audit_logs的两类事件：`video_capacity_recovery_claimed`和`video_capacity_recovery_blocked`。摘要仅包含schema、epoch、owner、policy_sha256、redis_run_id、token_sha256和result，系统操作的operator_id为空。

新增nullable生成键只约束这两类事件的唯一性，其余模块为NULL，保留原行为。恢复审计INSERT必须绑定当前门闩，UPDATE/DELETE禁止；大小写别名也不能伪装成其他模块绕过。仓储复核当前epoch的唯一认领及必要阻断记录，recovering不允许已经存在相反阻断事实。

审计记录保护和SQL版本守卫仍不能只凭早期基础用例声明全部门禁完成。session98087实际复现缺schema但补extra保持7键、owner数字123与字符串123被错误接受。现已增加必需七字段检查，根类型/长度、schema INTEGER=1及其余六个STRING使用NULL安全判断，防止IF变成UNKNOWN。

## 当前测试与证据边界

使用`verify-video-gateway-vid-g7-outbox.ps1 -Focus capacity_epoch`，Linux增加`-LinuxRace`。`-Focus capacity_epoch_version`仅复验版本和审计SQL守卫。完整all把capacity_epoch放在独立临时数据库，避免原单行门闩状态和G6全局排队容量夹具相互干扰。

- session78929：未实现认领的RED，100个尝试没有赢家。
- session42539：基础native通过，31.81秒。
- session91061：扩展native/Linux race通过，分别62.62/61.36秒，覆盖真实100 CAS、30秒接管、I2V/资金不变、G6准入兼容、SQL负例、审计失败回滚、嵌套事务/根PreparedStmt、Block尾到期及真实COMMIT后包装层丢确认。
- 独立QA发现单独回退version_no与故障回调清理缺口。
- session87305：版本回退/跳号、恢复审计更新/删除/重复均实际未拒绝，形成新的RED。
- session43096：补强后的native/Linux race各两项通过、SKIP=0、最外层exit=0且清理通过，server哈希d8f95f36；随后独立QA确认尚未覆盖的审计JSON缺字段/类型问题，不能以已有绿测关闭。

新增17个用例在事务内合法转为blocked并确认尚无blocked审计，再直接INSERT正文后统一回滚：一个合法对照、owner数字、schema字符串、七字段各自missing加extra及JSON null。必须观察绑定触发器指定1644，不以唯一键1062或Go读取拒绝代替。session39272原生专项及同源码Linux capacity_epoch两项回归均通过，SKIP=0、exit=0且清理通过，server哈希33cc1706；主测试包含全部17例，61.88秒。该缺口已局部关闭，不是完整G7 PASS。

快速入口为`-Focus capacity_audit_types`；默认capacity_epoch主测试在COMMIT未知用例之前调用同一helper，避免重复初始化原单行状态，同时保证完整回归包含全部17例。

上界测试使用`-Focus capacity_boundary`独立新库，通过新库初态与runner绑定server_uuid确认目标，只在夹具准备时短暂移除本轮UPDATE触发器，恢复完整守卫后才调用公开Repository。它是合成数值边界，不是自然执行2^64次或真实业务恢复。完整all必须将耗尽门闩留在独立库，不清零复用。

错误注入不是TCP断连；I2V/财务不变也不等于完整G7运行时已验收。完整账本到ready的本地协调切片已通过，真实进程/Redis重启、提交计划到容量授权、确认与释放仍待完成。

## 回滚与后续接线

down只保留Expand Schema和审计事实，不DROP门闩、不重置epoch、不删除历史事件。本增量未部署测试服。

当前协调器已经把恢复租约与完整原Task/Request/Quote/资金/提交计划快照绑定，并在任何ready写入前验证实际Redis实例和完整staged快照。旧prepared配新proof、固定键TTL漂移、两侧未知回执及只读重放均有真实MySQL+Redis验证；业务运行时仍只能通过该协调器取得ready事实，不能直接用Current返回的epoch手工授权。相关边界见[Redis容量合同](video-gateway-vid-g7-redis-capacity-contract.md)及[G7阶段矩阵](video-gateway-vid-g7-infra-recovery.md)。
