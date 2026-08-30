# VID-G5 补偿/财务恢复切片独立核查

测试工程师子任务`vid_g5_cancel_slice_qa`只读检查源码与测试设计，没有修改文件、执行数据库/Provider/钱包操作。主任务在一次性隔离MySQL完成运行。此回执不是完整G5验收。

| 发现 | 局部级别 | 复现与修复 |
|---|---|---|
| G5-COMP-001 | P1 | 第8次崩溃过期回收被SQL期限挡住，已真实复现；只允许过期且达8次的running回收dead，不产生第9次 |
| G5-COMP-002 | P2 | SQL可创建无审核历史的manual租约，已真实复现；要求有效双主体及同版本不可变审核事件 |
| G5-COMP-003 | P1 | 有效B租约可能用于A请求；恢复入口绑定request/job，增加跨请求及过期围栏回滚反例 |
| G5-COMP-004 | P1 | 仅预占时也可completed，已真实复现；仓储/SQL要求财务、交付、输入租约闭合，completed不得发起新结算 |

独立复核确认对应源码修复闭合，首次结算及重放均有尾部围栏校验。补充测试覆盖人工正向审核历史、同worker旧围栏、8次失败/崩溃、P/C两个故障点、租约中途过期与后继恢复，以及已安全取消并释放后的真实completed正向对照。

最终运行：`ok molin/server/internal/modules/token_gateway/service 47.635s`。完整1→77、重复up、保留down/re-up、Linux race及当前G5全部已实现切片通过；本机全包Go、vet、依赖验证及敏感扫描通过。

正常财务恢复仍为retry/delivery_pending，资产不会available。统一交付/complete原子协议、其他结果矩阵、调账、完整17类对账及全阶段验收仍未完成。完整P0/P1/P2总数未评定，不写AUTO_PASS；当前LOCAL_ONLY，所有真实Provider、资金、费用与部署操作为0。
