# VID-G5 跨门面生成幂等局部核查

本文件不是完整G5验收，只记录生成查询、自动报价事务、别名隔离与一致三轴返回。

- 基线：`36b6a5c5f9e60a4ef182ae434337bb05e165477c`。
- 源码检查点：`5677e594100b0430580992629a8bfe9dc550715b2dced8332f24fc0f57828ad9`，76个源码/脚本文件，73个Go文件。
- 独立核查任务：`vid_g5_cancel_slice_qa`，只读合同与源码，不运行数据库、Docker或Provider。
- 主任务运行结果见[跨门面检查点](./video-gateway-vid-g5-facade-replay-checkpoint.json)。

## 反例与修复

| 反例 | 实际先红证据 | 修复 |
|---|---|---|
| 原请求跨门面重放先多写Quote；撤销Key的新请求也先写Quote | 默认all 294.488秒，两项均从1张Quote变2张 | G5自动Quote和预占同事务，先查询共享生成意图和当前权限 |
| 显式重放传入另一用户同SHA输入ID，返回成功 | facade定向0.904秒，预期404语义却得到nil | 显式/自动/竞争重放共用绑定与别名元数据校验 |
| RC外层的嵌套RR无法形成新快照，返回混合三轴 | facade定向7.008秒，出现pending_reconcile/held | 最终三轴从同一条Task/Request JOIN取得 |
| 仅转发Reserve的G5包装器静默降级 | 同次7.008秒，撤销Key后仍多写Quote | 自动协调能力缺失时写入前拒绝；旧G2明确选择legacy合同 |

独立核查提醒，简单Lookup不够：必须保留后续写入权限与生成唯一键裁决，并避免两个别名在Quote指纹层先冲突。实现使用归属Project围栏、同事务Quote仓储及最终Reserve重验；不创建新的幂等账本。

## 已建立用例

- 显式创建、取消后自动重放返回原Quote/Task/三轴，不新增Quote。
- 原输入进入pending_delete、ready resolver不可用时，仍可纯元数据重放。
- 同归属同SHA/version的别名可返回原请求，但不替换TaskInput、不重建租约；伪造InternalID不起作用。
- 跨User/Project/Key、缺失别名及伪造SHA拒绝。
- 双别名和显式/自动混用分别100并发，只产生一个Request、Task、Hold和冻结流水，只消费一个Quote。
- 自动创建遇到Key过期、余额不足及Hold写点故障，自动Quote与其他事实整体回滚。
- Request读后由另一事务完整写入pending/HPC的交错读取，不得返回混合三轴。
- 缺少自动协调能力的包装器，不得先写Quote。
- 原G2协调器和测试夹具显式声明其旧自动合同，原门面单元回归保留。

独立最终源码复核确认三轴混读与包装器降级修复已闭合，未发现这两项修复新增的确定问题；运行由主任务完成。不将这项局部结论扩展为完整G5的P0/P1/P2计数或产品验收。

主任务最终默认all隔离MySQL/race通过（306.021秒），含完整1..77迁移、重复up、保留式down/re-up；同源码全量Go测试、vet、依赖和格式检查通过。已建立的本轮反例均纳入该运行；完整阶段仍有下方待办。

## 保留边界

没有正式HTTP接口、真实Provider、真实钱包、真实资金或部署。自动协调器不持有Provider，也不执行原任务；恢复原请求不重建Queue、租约或钱包事实。Git仍为LOCAL_ONLY。

完整G5仍需调账剩余边界、12金额金样、Chat/Image含77兼容迁移的隔离回归、证据校验脚本及最终QA/PM/Standards/Spec同源验收。VID-G6未开始。
