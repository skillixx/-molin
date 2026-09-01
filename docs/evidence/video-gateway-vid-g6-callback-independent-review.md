# VID-G6 内部回调增量独立审查（未全阶段验收）

## 产品工程合同

产品角色`vid_g6_g5_gate`只读核对原G6与G0，确认首版真实Runware不依赖Webhook；本次只冻结Fake工程协议。专用三个签名头、32字节secret、64字符小写hex nonce、Unix整数时间窗及三bool ACK不改变商业、财务或交付规则。用户JWT/SK不能替代签名，未知/错绑拒绝，无效签名不占用nonce或Callback事件键。详见[回调合同](../video-gateway-vid-g6-callback-contract.md)。

## 独立QA发现与已执行反例

QA角色`vid_g6_contract_audit`指出：原SQL账本在Gateway保护之前就会应用回调，故迟到终态可覆盖fetching及待对账；内部TaskEvent裸外部ID会跨任务碰撞或超长；历史Applied=true重放又会调用G5对账；直接成功补齐前的普通COUNT会受旧RR快照影响。主代理依次通过55332、63201、3542与84216真实隔离测试复现。

修复均在原账本边界完成：持Task锁限制回调来源；完整三元组长度前缀摘要与新ID命名域；只对新应用事件执行G5协调；首个成功在同事务按原矩阵补齐processing；旧事件存在性使用事件实体当前读，不重解释历史ignored。

16000三项专项通过；20299增强五项通过，service18.764秒，schema93与Linux race。后者包含真实nonce写入后故障的全事务回滚、真实sql.Tx.Commit成功再返回合成确认丢失、同请求重试后的整行不变及唯一记录，另有旧RR历史ignored重放保护。具体复制树、范围及历史源码状态见[回执](./video-gateway-vid-g6-callback-wip.json)。

QA静态核对事务辅助器确实先执行真实COMMIT，且UseApplicationDB仅存在于测试构建；G4/G5仓储仍复用外层连接和保存点，未替换业务为恒成功。QA未另跑数据库。测试夹具全局nonce复用造成的50838失败已按协议改为每夹具独立随机nonce，不把409改成成功。

## 尚未签收

旧G4 Gateway组合、完整HTTP负向、nonce跨Task竞争、更多事务故障与I2V租约仍须独立复验。已通过的局部结果不代表真实Provider、生产部署、商业可用或完整G6；回调尚无最终独立验收，不能进入G7或执行未满足门禁的Git合并。

后续动态补充：85483九项已通过，service21.721秒，复制树`eaf1e0063e2ca15868785edc2902b8399c3e7395d1e9b4b9a92eadb2594c1371`。旧Gateway与真实SQL账本组合返回正确processing/fetching、受控Content及幂等结果；既有advance遇版本冲突会重读当前状态，因此未新增Gateway修复。该结果只消除本轮假设风险，不冒充尚未执行的所有G4/G5兼容。RR历史ignored、nonce事务及真实COMMIT恢复也随本轮通过，等待最后的独立源码/证据复核。

最新独立复核：QA逐文件校验231文件清单`2f44f94e100fd42ae150433ab44af649ab2ebe9d6fd4985c598b81fb23819739`一致，无漂移。结合独立源码审查及20299/85483动态记录，确认上述五类已复现回调缺陷可局部关闭；QA未另跑数据库。25685的119项回归仍在运行，不能预先计为通过；nonce跨任务竞争、全部HTTP负向、I2V与完整G6验收继续保留。

## 下一项管理端合同

产品角色已确认剩余12条管理路由：任务列表/详情/cancel/poll/archive-retry、输入资产列表/quarantine、输出资产列表/quarantine/release、对账summary及adjustments。GET使用管理员JWT、权限与手机/邮箱MFA，不额外要求reason/CAS。写入另需reason、CAS、幂等及前后审计；release与adjustments必须独立maker/checker审批事实，system不能自批。poll/archive-retry只恢复原Task，禁止重新Submit；summary只读，不隐式运行对账写方法。该清单仅定位后续工作，不表示已实现。
