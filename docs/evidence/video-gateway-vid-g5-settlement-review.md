# VID-G5 正常结算切片：独立核查回执

范围是G5 Fake媒体就绪、确认成本和正常结算，不是完整G5验收。测试工程师子任务`vid_g5_cancel_slice_qa`只读核查源码和测试设计，没有修改文件或执行数据库/Provider/钱包操作；运行证据由主任务在一次性隔离MySQL完成。

## 发现、复现与修复

| 编号 | 局部级别 | 复现结果 | 修复与回归 |
|---|---|---|---|
| G5-SETTLE-001 | P1 | 只用状态Repository即可把仍holding的请求推进settled，具备绕过实际消费的条件 | G5初态限制；终态需真实Hold/link/冻结/解冻/消费；交付需独立Outbox且无未完成补偿。伪推进及直接INSERT终态均拒绝 |
| G5-SETTLE-002 | P1 | 改held金额、settled版本或追加相反released事件后仍可重放成功 | 校验完整事件集合及六字段/version；三个反例均拒绝 |
| G5-SETTLE-003 | P1 | 在settle_pending/settle_outbox时推进时钟越过媒体到期，仍然扣费 | 提交前新时钟复核六资产，两个反例均整体回滚 |
| G5-SETTLE-004 | P2 | 仅有同任务事件ID和摘要长度，错误成本摘要/分母可进入仓储 | Go/SQL重算摘要并固定每秒分母1。错误摘要、正确摘要但分母100分别拒绝；正确摘要/分母1正向对照通过 |

独立复核确认四类源码修复与反例对应，未将结论扩大到完整G5。按核查建议，最后一轮单独隔离了“摘要正确但分母错误”的因素，并分别经仓储与直接SQL验证。

主任务最终结果：`ok molin/server/internal/modules/token_gateway/service 40.710s`；完整1→77迁移、重复up、保留down/re-up、Linux race通过。T2V/I2V各100并发结算、8处结算写入点故障也通过；原Provider确认成本保留，财务失败整体回滚且不重新Submit。

## 未完成边界

正常结算后仍为六资产temporary、delivery pending；F01/F02完整交付金样尚未通过。其余释放/未知结果矩阵、唯一补偿、调账、完整17类对账、Chat/Image全部基础设施回归及最终QA/PM/Standards/Spec仍未完成，不能报告完整G5的P0/P1/P2=0或AUTO_PASS。

当前LOCAL_ONLY。真实Provider、Key、钱包、资金、调账、费用、外部业务HTTP、测试服务器与生产操作均为0；Fake成本只是非商业测试事实。
