# VID-G5 Usage/未提交取消：独立切片核查

本回执只覆盖本地Usage和未提交取消切片，不是完整G5的QA、产品、Standards或Spec验收。Git仍为LOCAL_ONLY，金额为合成测试值。

独立核查角色：测试工程师子任务`vid_g5_cancel_slice_qa`。核查者只读源码与测试设计，没有修改文件或运行数据库、Provider、网络、钱包；以下运行证据由主任务在一次性隔离MySQL执行。

## 发现及处理

| 编号 | 级别 | 独立发现 | 修复与运行证据 |
|---|---|---|---|
| G5-CANCEL-001 | P1（切片） | 取消幂等只数三条零Usage和三条Outbox，可能掩盖额外计量或错误payload | 先复现额外Usage仍返回成功；改为全量读取、逐类型/归属/价格/金额/payload校验，首次提交也复用；迟到Usage、错误Outbox金额和取消前额外Usage回滚测试通过 |
| G5-USAGE-001 | P2（切片） | 网关零成本只看cancelled、attempt_count和Provider字段，遗漏共享Attempt与历史submitting | 已复现两种路径在仓储和直接SQL均放行；服务/仓储共用VerifyVideoNeverSubmittedTx，SQL同样检查Provider/Bifrost、Attempt、产物、回调及执行事件；四个反例修复后通过 |

独立复核结论：这两处发现的源码修复已闭合，未发现修复引入的新阻断问题；运行结论依赖随后完成的隔离测试。主任务最终运行结果：`ok molin/server/internal/modules/token_gateway/service 17.355s`，`VIDEO_G5_MYSQL_SLICE=PASS`，范围明确为reservation/usage/cancel，`full_stage=false`。

## 保留边界

- 取消与submitting竞争测试已加入；后续仍应扩展各胜者的钱包/Outbox完整分支断言，以及100个不同任务共享钱包释放。
- 正常结算、Provider确认成本、结果未知、补偿、调账、完整17类事实对账、全部交付门禁仍未完成。
- 完整G5的QA/PM/Standards/Spec、P0/P1/P2总数尚未评定；不能把局部两条发现关闭写成全阶段PASS。
- 原始钱包流水与Usage不被修改；失败测试只使用一次性合成数据库。真实Provider/Key/钱包/资金/费用/部署操作均为0。
