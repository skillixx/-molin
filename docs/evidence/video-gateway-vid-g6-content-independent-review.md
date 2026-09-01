# VID-G6 私有内容子项独立复核

本回执只覆盖content增量，不是完整QA、PM、Standards或Spec验收。

主代理动态回填：42007最终exit0，43项必选测试全部RUN/PASS，包含`TestVideoG6ContentHTTPMySQL`的真实存储失败单JSON反例，源码copy hash与冻结清单一致；详见`video-gateway-vid-g6-content-checkpoint.json`。独立角色未自行运行该进程，不能混淆动态测试执行者。

## 需求确认

产品经理角色`vid_g6_g5_gate`只读核对原G0/G6合同，确认只允许当前Project SK读取；必须复用原G5全部财务、安全、六资产及对账条件，不能调用DeliverReady作为读前补救。下载用户/Project并发2/4、20MiB/s、30秒写超时与256MiB上限仍属于本阶段范围。不得用本子项缩减43路由完整合同。

## 工程与QA增量复核

测试工程师角色`vid_g6_contract_audit`指出P2 G6-CONTENT-001：应用内容不可用分支直接输出503后又输出默认500，导致两段JSON。主代理用handler单测47d667复现；修复为只设置错误三元组，在公共出口单次写入，9bb60f通过。实际HTTP测试新增Head故障、低敏503、第二次JSON解码必须EOF。

独立角色再次只读检查确认修复，并复算116个文件的SOURCE_STATE_ID为`af47d1eb351e15f57f01d465cd69ee2d6c8cecc1f5f796eb4d90ecd19fc3e27a`，与冻结清单一致。本P2可关闭；实际HTTP更新的动态证据由主代理42007运行回填，不冒称独立角色亲自重跑。

顺带发现的错误分类缺口已修正：`loadVideoSettlementMediaTx`只有业务事实不满足映射404，其余数据库错误映射低敏内容不可用503。定向数据库故障矩阵尚需补齐，不将代码检查当运行证据。

已确认代码沿用原G5事务连接与锁序，每片固定资产ID/version/hash/大小/位置、缓冲最多1MiB、读取前后复验当前权限及对账，未持钱包锁等待客户端，也未调用Provider或写结算。

## 保留的未完成项

- 大于2MiB的真实可播放媒体，多片中途失败/撤权/删除竞争与无JSON断流。
- 用户/Project并发限制及所有退出路径释放占用；默认同用户Project场景中用户上限先触发，Project上限需独立且明确标注配置测试。
- 20MiB/s速率、取消限速等待且不持钱包锁。
- 当前业务HTTP的完整财务字段不变、单连接、期限跨越、浏览器拖动与双SDK。
- 全阶段独立QA/PM/Standards/Spec、Ready CI、PR及合并。
