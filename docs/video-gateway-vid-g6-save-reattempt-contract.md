# VID-G6 保存清理后再次尝试（开发中）

## 功能合同

部分转存失败后，若原存储权益到期，可沿已批准的清理流程删除全部独立目标并释放原容量。原源仍可保存、用户重新具备当前有效存储权益时，可以使用新幂等键再次保存。此功能不延长源期限，不重新生成视频，不写生成钱包或计费账本。

旧幂等键永远返回原尝试。已aborted时拟返回HTTP 200、原7字段平台data，`status=aborted`、`user_asset_id=0`、`idempotent=true`；0不是可下载资产ID。新键加入同Task当前唯一有效尝试，或在全部旧终止尝试的清理证明有效后创建新尝试。已有completed时新键只返回既有长期资产。

当前真实服务反例36103已复现旧键只能返回笼统冲突。新实现正在临时MySQL验证，尚未完成HTTP和100并发验收，不能提前开放业务。

## 数据和实现

000091演进原保存协调表，不创建平行视频或资产账本：

- 原`public_id`改为尝试主键，其值和全部历史字段不变；保留Task复合外键。
- `attempt_no`从1递增，`previous_save_id`指向同Task前一个已清理aborted尝试，前驱最多一个后继。
- 生成列`live_task_id`只在非aborted时为原Task编号，唯一约束保证至多一个有效尝试，completed仍占据唯一位置。
- 原命令新增`save_public_id`并通过复合外键和NULL安全Key触发器绑定精确尝试。新列只能在迁移停写期间按旧每Task唯一事实回填；孤儿、错Key或多候选失败关闭。
- `video_asset_save_attempt.go`在原Task锁下选取旧命令对应尝试、唯一有效尝试或完整历史后的后继。
- finish、cleanup和CAS以public_id精确定位。媒体删除扫描全部历史尝试，不能只读第一条aborted而漏掉当前copying。

新目标由新尝试身份生成，绝不复用旧五目标。旧复制迟到仍应被原围栏拒绝；旧清理重放只能检查原尝试，不能释放新预占。

## 迁移与回滚边界

仅在一次性隔离数据库验证，未执行共享环境迁移。MySQL DDL不是整段事务：先加列，再在旧Task唯一约束仍存在时确定性回填命令，完成校验后才移除Task主键并建立尝试约束。重入只补缺失结构，不重置既有序号、前驱、非NULL关联或历史权益类型。

迁移期间停止保存、媒体删除和清理全部写入口。down保留结构与事实，不把多尝试强行压回单行。旧版本按Task取一行可能忽略新处理中尝试；回滚必须关闭所有依赖保存状态的删除/清理入口，或使用理解多尝试的兼容版本，不能只关闭Save。

## 验证与剩余任务

`verify-video-gateway-vid-g6.sh`的`save-reattempt`范围要求真实MySQL、schema91重复up/down/up、原旧键零复制零容量变化、新键新目标、旧清理安全重放和原生成财务不变。88532局部6项通过，包括同一应用上的100并发新键汇合为一个后继、恰好五次目标复制和一次容量结转；这不是跨进程或完整迁移验收。

## 历史迁移验证增量

专用`save-migration`范围先只安装到89版结构，执行真实G5生成、旧列与旧触发器下的保存协调/容量操作，形成copying、copy_failed、completed、aborted四种历史。每个子夹具用既有Cleanup退休自身权利政策并关闭连接，随后在父连接池重新构造应用；不修改政策唯一约束，不复用已关闭连接，也不冒称运行旧二进制。

30701验证19张表的全部原列不变，90版对旧权益类型保持NULL，91版在九次ALTER已落地后分别SIGNAL中断、从脚本开头重入，最终保留式down/up仍保留原事实。同时直接核对PK、五组唯一索引、三组复合/前驱FK、命令NOT NULL与live值；合法后继在事务回滚后无残留，错前驱、跳号、缺前驱、错Key、孤儿命令和跨Task命令均被SQL拒绝。

67960发现旧NULL未完成计划可借当前配置继续发布，已在finish增加原冻结类型校验，90823原键恢复拒绝通过。89365进一步复现新键虽被拒绝但已追加命令；现prepare在复用旧未完成计划、绑定新命令之前使用与finish相同的冻结执行政策判断。37162增强矩阵通过，copying/copy_failed使用新旧键拒绝后19表原列均不变。未知历史类型不自动回填，也不能以当前配置代替原始证据。

```powershell
$env:VIDEO_GATEWAY_G6_ISOLATED_MYSQL_APPROVED='YES'
$env:VIDEO_GATEWAY_G6_TEST_FOCUS='save-migration'
& 'C:\Program Files\Git\bin\bash.exe' infra/scripts/verify-video-gateway-vid-g6.sh
```

该专用历史测试不由普通最新schema的99项回归替代。它覆盖九个ALTER后中断，不代表已覆盖所有DROP/CREATE触发器空窗、真实连接中断或旧二进制兼容。

仍须补齐：全部迁移中断/损坏历史矩阵、旧Copy与新预占竞争、全部写点及COMMIT未知恢复、媒体DELETE全历史竞争、旧版本关闭态，以及完整G6验收。

相关：[保存合同](./video-gateway-vid-g6-asset-save-contract.md)、[独立产品边界](./evidence/video-gateway-vid-g6-save-reattempt-review.md)。
