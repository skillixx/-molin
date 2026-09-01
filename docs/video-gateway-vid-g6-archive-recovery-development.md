# VID-G6 归档恢复开发记录（未完成）

## 完整目标

`POST /api/admin/token/video-tasks/{task_id}/archive-retry`须在原Task下恢复fetching/storing/moderating/labeling，以及有可信结果证据的pending_reconcile。不能重新Submit、创建Provider UUID、覆盖原安全或财务事实，也不能把pending_reconcile回退为fetching。

本记录保留围栏和执行器的增量证据，不代表完整归档验收。当前[归档HTTP首版](./video-gateway-vid-g6-admin-archive-contract.md)及[调账首版](./video-gateway-vid-g6-admin-adjustment-contract.md)已注册，本地共46/46个局部入口；完整异常矩阵与阶段门禁未通过。

## 已实现的共享围栏

000099在原`ai_gateway_tasks`上增加archive_generation、archive_token_hash、archive_lease_until、archive_phase。技术phase只供私有恢复过程使用，不成为普通任务DTO或新业务状态。

`repository/video_archive_fence.go`提供认领、技术phase CAS及退让：

- 每次认领递增原Task版本与归档代次，追加原TaskEvent，不创建第二Task或资金账本。
- 使用仓储生成的32字节随机令牌；仅SHA256进入专用列，原始令牌只在不可公开构造、不可序列化的内存证明中。
- 证明绑定原Task、用户、Project、代次；每次写入仍校验原owner/Key和Task版本。
- 默认执行租约2分钟，内部测试允许100毫秒至2分钟的有界租约；不是媒体保留期限，不修改任何资产expires_at。
- 技术步骤只能fetching→storing→moderating→labeling→verified，不能跳级。verified不等于Task成功。
- 租约过期不放行普通Worker，只能显式接管递增代次；旧证明即使知道最新Task版本也无效。
- 只有原Task已在安全终态或pending_reconcile时可退让；退让保留代次与追加历史，不让旧执行中Worker复活。

`VideoTaskRepository.TransitionExecution`统一检查证明。普通G4 Ledger发现围栏即拒绝开始新媒体IO；已经开始的旧IO，其状态/资产写回还会被共享Task CAS拒绝。Provider Callback保留低敏接收事实，但不能在围栏期间直接推进状态。旧库SELECT *未返回归档列时为nil，原未接管任务行为不变。

数据库检查围栏字段完整性、代次与Task版本单步递增及技术phase单向变化。仓储提供的是协调原语，不代替上层管理员JWT/MFA、reason、审计或媒体来源授权。

## 完整接口尚需接入

当前新增私有恢复执行器`service/video_archive_executor.go`，成功路径现已接入HTTP管理命令，异常矩阵仍未完全验收：复用原G4 FetchAndFinalize、探测/审核/标识、共享资产持久化；正常任务沿原G3逐级推进，pending只推进私有技术phase，最终同时验证原成功成本、时长、六角色安全树及六对象Head/hash/size后才进入原安全终态。归档不结算、不发布交付、不释放预占中的输入租约。已接入持久化管理命令、前后审计、unknown收口及明确审核拒绝事实；标识/IO等完整异常矩阵仍待验证，不能据成功路径签完整接口。

`video/archive_object_fence.go`及FakeStore在对象写入提交/移动的同一锁内检查代次，Put读体后再次复验，拒绝旧Worker迟到提交；被接管的旧归档上下文不能移动或删除对象。普通用户删除仍走既有生命周期/财务门禁，不能把该保护解释为存储API的通用身份认证。代次不回退，实际固定对象仍是不可覆盖写入。原始Provider界面只给OpenContent，Submit/Query/Cancel/Delete均拒绝；受控结果ref必须与原请求一致。

执行器在数据库认领成功后同步存储代次，然后才开始新媒体操作。每次读/存储操作前后复验当前管理员、原围栏和成本事实；上下文不晚于JWT和归档租约。最终提交时，所有数据库动作后再进行管理员复验，并检查先前保存的租约/媒体最早期限，不能用清除后的围栏记录误判。元数据Head验证有界，媒体正文抓取与写入不放在长SQL事务中。

1. 管理员task_manage、双MFA、reason、幂等和前后审计，与围栏认领放在同一事务。
2. 从原Provider绑定、已确认成功计量、无冲突事实和原资产推导恢复起点，不能相信客户端phase。
3. 复用G4 FetchAndFinalize和共享资产持久化，只开放受控OpenContent/Probe/Safety/Labeler/不可变Store，不暴露Submit/Query/Cancel。
4. 外部IO不持长SQL事务；操作前后检查同一代次、Task版本、租约、管理员资格及新冲突。
5. 固定对象必须条件写/不可变；失效Worker不能覆盖、提升或删除新代次产物。确认丢失须先核验已有对象和hash，不盲目重做。
6. 最终同一事务复验原成本、实测时长、六角色父子资产、安全版本与生命周期；再走原G3安全终态。不能用phase=verified作为成功证明。
7. 媒体恢复和G5财务补偿分离；未结算或对账不为零仍不交付、释放输入租约。真实failed/cancelled/expired不复活。
8. 部分派生资产不能当成完整六资产；需逐项验证恢复或明确拒绝损坏事实，不能覆盖原审核/标识。

上述是完整范围的剩余工作，不是新增人工门禁；不得将现有方法可运行范围冒充完整archive-retry能力。

## 验证

单元红例先证明缺少ArchiveTokenHash/CheckVideoArchiveFence实现；实现后普通无证明写入拒绝、无围栏旧任务兼容通过。MySQL专项`archive-fence`包含：100认领唯一、普通旧Worker拒绝、仅知道新版本仍不能写、持证明安全进入pending、技术推进不回退主状态、禁止跳级、过期接管、旧令牌失效、退让与五份追加事件；并回归管理poll、回调与原Fake G4完整流程。

过期接管用显式夹具时钟推进3分钟，不能冒充真实等待或已经证明外部IO已停止。尚待实际运行回执、独立审查及更多并发/故障/对象围栏证据。特别是原始SQL、已有IO与资产持久化的所有交叉路径仍需全矩阵检查。

已修复的独立审查缺陷：

| 编号 | 等级 | 问题与处置 |
|---|---|---|
| G6-ARCHIVE-FENCE-001 | P2 | 通用检查为旧无围栏写入返回允许，释放方法随后解引用nil证明；推进与退让现先显式检查证明和活动围栏，保持普通旧路径兼容 |
| G6-ARCHIVE-FENCE-002 | P2 | 使用调用前Now校验租约，锁等待可能跨期；现默认实时UTC，事件时间和租约时钟分离，在锁后及最后读取后复验，测试用WithArchiveClock显式注入虚拟时钟 |

82012旧基础批5项全部RUN/PASS、schema99/Linux race通过，repository 1.076秒、service 21.935秒、video 1.109秒，围栏MySQL 3.76秒；复制树SHA256为`cb42becac5794e4aa96555846cd2e5bc01a48a8c71ba6a526abc3a153e36c5f1`。它不覆盖随后上述修复与100ms实际锁等待用例，修复后证据另列，不能复用旧绿色关闭新缺陷。

95313修复批6项全部RUN/PASS、无SKIP，schema99/Linux race通过；repository 1.048秒、service 25.187秒、video 1.112秒。100并发/接管围栏用例3.60秒，实际锁等待跨100ms租约用例3.44秒。复制树SHA256为`98707a7a02125bedc8154b1c67206f5e89d6c43060aae05cd157bbb184451b22`。独立工程已确认两项修复位置；本批不覆盖尚未接入的管理员命令、媒体IO围栏、完整归档API及G6阶段验收。

新增存储围栏测试先因缺实现而编译失败，随后通过读体期间接管、旧代次Put/Promote/Quarantine/Delete拒绝和代次不可回退；video包原生全量通过（1.003秒）。1872首次执行器批整体FAIL：三个普通/待核对恢复组合通过，第四个I2V待核对组合被夹具重复全局TaskEvent ID拦住；只修正测试事件号为每个合成主体唯一，没有放宽SQL唯一性。该批复制树SHA256为`f5eaafff69301ab0e34ad32af28f82bfcbe77259bd0de339533c29613e56265c`。

缺陷`G6-ARCHIVE-EXECUTOR-001`（P2）：最终authorize之后仍有资产读取和围栏释放，可能跨权限/MFA期限。已将最终鉴权移至所有SQL动作之后，保存原lease及最早媒体期限做末尾纯时钟检查；增加最后释放读取跨权限期限的整事务回滚测试。修复后实际运行结果另列，旧正例不替代该反例证据。

39784修复批7项全部RUN/PASS、无SKIP，schema99/Linux race通过；service 39.116秒、video 1.133秒。普通/待核对×T2V/I2V四种执行恢复全部通过（合计23.65秒），最终权限到期回滚用例7.20秒；围栏并发和实际锁等待到期兼容均通过。复制树SHA256为`877f465cecb6350ed2dc717c5ad65f7d924d617c8ed75f8ca8a346145152e5b2`。这证明私有成功执行器与本批反例，不证明HTTP、持久化管理命令/审计、安全失败和部分资产恢复已完成。

## 回滚边界

停止新归档恢复，不自动清除已有围栏、删除事件、覆盖媒体或退款。000099 down保留原Task扩展与历史；需要恢复工作时按新的有效代次认领，不使用旧证明。当前不部署共享环境，不使用真实Provider/Key/资金，不进入G7。
