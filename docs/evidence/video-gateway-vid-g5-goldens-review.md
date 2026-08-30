# VID-G5 十二金额金样局部复核

本记录只覆盖金额金样与证据校验，不是完整G5验收。

- 基线：`36b6a5c5f9e60a4ef182ae434337bb05e165477c`。
- 源码检查点：`55dd5e43872be73434fd87275c9761ff6747ecb3b914a67ad46a4410cd4ad1bf`，82个源码/脚本文件、77个Go文件；本轮包含新增的独立Python校验器与测试。
- 独立任务：`vid_g5_cancel_slice_qa`，只读核对批准表、执行器、观察器及验证器，不运行数据库。
- 实际金样数据：[golden-amounts.json](./video-gateway-vid-g5-golden-amounts.json)。运行和源码清单见[检查点](./video-gateway-vid-g5-goldens-checkpoint.json)。

## 核查发现与补强

1. 初始样本只记录F12的pending，但批准预期为manual_review。现保留F12_before_review中间快照，再经两名不同合成主体ClaimManual及FinishTx到manual_review。F11也实际执行一次有界Worker到retry，不只停留在首次pending。
2. 未闭合样本本来预期Reconcile=false，不能因此忽略额外同量Usage或零金额消费。观察器已严格检查每例source/kind/序号与资金动作的白名单和精确数量，已提交的已知成本只接受provider_cost，F07例外为网关未提交证明。
3. 观察器分别校验冻结、解冻、消费总额与余额链，以及完整资产归属、角色和父子关系。中间快照不重复加入最终十二例汇总。

独立最终复核确认以上两处主要缺漏在源码中闭合，金额字面量及三组汇总与批准表一致。Python验证器使用冻结预期与Decimal复算，未发现明显关键假阳性；它验证导出内容，不替代MySQL执行或全阶段验收。

## 验证范围

- 最终golden定向MySQL/race通过：10.639秒，导出14个观察值与3组汇总。
- Python执行3项测试，含27种篡改子用例及JSON重复字段拒绝；合法实际导出通过。
- 篡改包括未知成本伪零、同量重复计量、被忽略的来源、零金额额外消费、错误成本来源、F12错误终态、错误关联/汇总、敏感额外字段、布尔冒充计数和真实操作边界非零。
- 全部样本为非商业夹具，真实Provider费用为0；已知合成成本小计1.54不包含两个未知成本请求。

主任务最终默认all隔离MySQL/race通过（320.199秒），含完整1..77迁移、重复up、保留式down/re-up；同源码Go测试、vet、依赖与格式检查通过。实际金样JSON的SHA-256为`f83b7ddbf1de06c0d936e78cec2580461fbe49d56390588d0b674f1e03ed5cf6`。

八个最终闭合样本逐项对账通过，四个未闭合样本保留差异且禁止交付。汇总资金守恒为0差异，不等于全部请求最终闭合，也不等于G5或商业验收通过。

完整G5尚需Chat/Image完整隔离兼容及最终QA/PM/Standards/Spec同源验收；不执行Git提交、推送或VID-G6。
