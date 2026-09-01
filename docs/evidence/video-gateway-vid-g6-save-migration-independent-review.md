# VID-G6 保存历史迁移局部独立复核

独立测试角色`vid_g6_contract_audit`只读审查历史夹具、迁移测试和恢复政策检查，未修改文件或另行运行数据库。此回执不签完整G6，也不签所有迁移故障场景。

## 历史夹具与覆盖边界

夹具从真实89版结构开始，使用真实G5生成与旧列/触发器约束，形成copying、copy_failed、completed、aborted四种保存历史，实际执行容量预占、复制、结转或清理。它是旧schema数据模拟，不是旧二进制运行验收。

独立审查指出多夹具共用父t会使active权利政策唯一约束冲突；实现改为顺序子测试，使用既有Cleanup退休各自条款并关闭连接，再在父DB上重新构造应用，保留外部Fake存储及合成密钥材料。没有修改唯一约束或批量退休其他政策。

九个ALTER后的SIGNAL发生在真实结构变更落地之后，下次从脚本开头重入。期间与最终down/up均比较19表全部原列，命令原字段仍受保护；直接核对主键、五组唯一索引、三组FK、命令NOT NULL及live值。合法后继在回滚事务中无残留，错前驱/跳号/缺前驱/错Key/孤儿命令/跨Task命令必须被1644拒绝。

本测试不覆盖全部DROP/CREATE触发器空窗、真实网络断连、进程终止或全部损坏历史数据矩阵。

## 两项局部关闭

1. 67960复现旧NULL权益类型的未完成计划仍可使用当前配置继续发布。finish现在在复制、资产创建和容量结转前拒绝缺失/不匹配的冻结执行政策；90823转绿。
2. 独立审查进一步提出新键可能在prepare追加命令后才失败。89365真实复现19表快照变化。prepare现在在复用copying/copy_failed及写入新command之前调用与finish共用的`matchesVideoSaveExecutionPolicy`，保留后置复验。aborted终止回执分支、completed读取分支和合法新attempt初始化未被该新增prepare检查改变。

主代理37162最新矩阵exit0、Linux race，service20.758秒，copy=`ce9642c8e4edf73f7db113919887cfc19afb2de58bfe84f69600916777248f46`。旧NULL copying/copy_failed分别使用原键、新键，每次恢复拒绝后19表原列均不变。独立角色结合此前源码审查及上述红绿工具证据，确认两项问题可局部关闭；动态测试不是由独立角色另跑。

## 冻结及未完成范围

正式候选202文件清单为`video-gateway-vid-g6-save-migration-source.json`，SOURCE_STATE_ID=`344f0eb746dc547bb910ec8639d75c21173eba13754b33b313c0da61f9da566b`。冻结后历史复验54179和99项回归45476已启动，尚未结束，不记为PASS。

完整保存竞争/恢复、全部管理/回调/SDK/浏览器/兼容矩阵、全阶段独立验收和Git闭环仍需继续。真实Provider、Key、资金、共享服务器、生产和G7均未操作。
