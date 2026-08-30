# VID-G5 明确失败与安全拒绝释放：独立局部核查

## 核查范围与版本

- 分支：`feature/video-gateway-vid-g5-billing-outbox-reconcile`；基线/HEAD：`36b6a5c5f9e60a4ef182ae434337bb05e165477c`，全部G5修改仍未提交。
- 本轮源码状态：`904e93225cc4f81f7617bbb255ac79f2ecf0171f0ce828778e48c39080c5a453`；42个源码/脚本文件、39个Go文件，复算清单见[检查点](./video-gateway-vid-g5-release-checkpoint.json)。
- 独立任务：测试工程师 `vid_g5_cancel_slice_qa`。只读源码和测试设计，不编辑、不执行数据库或Provider；MySQL与Go运行由主任务完成。
- 本回执不是完整G5 QA、PM、Standards或Spec验收，不替代财务人工批准和最终阶段缺陷归零。

## 两项P1及关闭证据

| 编号 | 独立发现/复现 | 修复与验证 |
|---|---|---|
| G5-RELEASE-001 | 通用TaskEvent Append可补造确定性释放marker；原label_unknown/derived_failed均可能表现为隔离＋标签失败 | 移除独立marker，把封闭failure_origin写入原失败CAS事件；证明需唯一真实终止迁移、来源、前状态、原因和完成时间。通用保留类型拒绝，直接SQL补记旧marker也不能改变结果，原始原因禁止改写，NULL/错误阶段拒绝。 |
| G5-RELEASE-002 | 四个显隐标签失败用例停在labeling；诊断回归准确返回MySQL3819：chk_ai_gateway_assets_quarantine violated | 77增量仅为video增加审核passed且任一标签failed可隔离分支；原图片条件保留，不把passed改成error。T2V/I2V显隐标签失败各100释放通过。 |

独立复核结论：两项源码修复已闭合，未发现新的直接可达阻断问题；运行关闭由主任务后续同版本绿测确认。两次修改前的隔离红测分别记录126.722s、126.774s，第二次同时复现通用追加旁路和3819约束冲突。最终隔离回归178.263s通过，覆盖全部已实现G5筛选及Linux race、迁移1..77、重复up和保留down/re-up。

## 本轮实际验收内容

- 明确Provider失败、输出审核拒绝、主视频显式/隐式标识失败，共8个操作/结果组合，每个100并发，1新释放＋99重放。
- 用户零计量/销售与Provider确认计量/成本分别保留；T2V/I2V安全成本0.20/0.30均为非商业合成值。每个组合最终17项检查、零差异、不可下载。
- label_unknown、derived_failed、archive_failed均不能退款；Hold、原Provider事实、I2V输入租约保留。其完整待对账编排不在本次通过范围。
- 10个释放写入点故障回滚；唯一release_failed补偿恢复后released/rejected/completed同事务闭合。已完成Worker重放不再解冻，后续settle不能覆盖released。
- 原正常结算、交付、补偿、未提交取消、预占和旧G4事实共存回归保持通过。
- 全量Go、vet、依赖校验、39个Go格式和差异检查通过；敏感扫描生成证据前231文件/0发现，补齐本回执后再扫233文件/0发现；42文件源码摘要复算一致。

## 仍未验收

释放补偿release_completed/release_checked之后的租约过期反例、完整相反终态竞争、主动Provider取消确认、拒绝取消、迟到成功、未知/归档/Usage冲突编排、调账、完整跨门面/身份矩阵、Chat/Image全基础设施及最终独立验收仍待完成。

财务合同已由用户批准，仅适用于本地非商业夹具。未提交、未推送、未建PR；真实Provider/Key/钱包/资金/调账/费用/测试服/生产均为0。VID-G6未开始。
