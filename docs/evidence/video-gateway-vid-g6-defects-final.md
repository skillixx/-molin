# VID-G6 最终本地缺陷台账

绑定SOURCE_STATE：`f7831697a58aab29d1084330de44a3eb65ce9c243d0dd08236ab1803d9722a54`

本台账保留首轮独立审查和最终全量门禁发现的问题。`CLOSED_VERIFIED`只表示当前本地源码已通过对应复验；最终P0/P1/P2计数仍须由新SOURCE_STATE下四轴独立终审确认。

| 编号 | 级别 | 根因 | 修复与当前证据 | 状态 |
|---|---|---|---|---|
| G6-FINAL-001 | P1 | HTTP账本缺少user/project/model运行态准入 | 000109门闩、原Task账本CAS、用户1/Project2/模型2及100并发赢家矩阵 | CLOSED_VERIFIED |
| G6-FINAL-002 | P1 | SDK证据未绑定当时最终源码 | 锁定Python 2.45.0、TypeScript 6.39.0真实loopback HTTP与临时MySQL重跑 | CLOSED_VERIFIED |
| G6-FINAL-003 | P1 | inline I2V缺真实TCP读限额、断连、归属和COMMIT未知矩阵 | 六个近10MiB连接、429前拒读、断连零事实、跨主体与提交未知恢复 | CLOSED_VERIFIED |
| G6-FINAL-004 | P1 | 下载缺Project第五路、真实慢连接、撤权/删除断流及COMMIT未知 | 真实TCP慢写、分片授权、租约恢复、财务不变和Project独立边界 | CLOSED_VERIFIED |
| G6-FINAL-005 | P1 | queue/budget失败关闭与日月边界证据不足 | 完整零事实快照、100并发、Asia/Shanghai边界、提交未知及生命周期矩阵 | CLOSED_VERIFIED |
| G6-FINAL-006 | P1 | 模型发布缺默认模型并发、MFA/权限期限、SQL/COMMIT恢复 | 两个实际候选一胜一冲突、全局默认隔离恢复、故障及旧Chat兼容 | CLOSED_VERIFIED |
| G6-FINAL-007 | P1 | 管理调账、轮询、归档、隔离与解除隔离缺完整并发/期限/提交未知 | 各接口100并发、双主体、CAS、前后审计、MFA/权限期限及COMMIT未知 | CLOSED_VERIFIED |
| G6-FINAL-008 | P1 | OpenAPI DELETE遗漏Idempotency-Key | 快照补充参数并由47路由矩阵与合同测试锁定 | CLOSED_VERIFIED |
| G6-FINAL-009 | P2 | requirements/README/合同在46与47路由之间漂移 | 33用户+13管理+1内部=47，Project grant列入清单，默认关闭矩阵精确断言 | CLOSED_VERIFIED |
| G6-FINAL-010 | P2 | 过程文档仍把已实现能力写为待补 | 主合同、API、前端参考、数据库、测试计划增加最终同源覆盖声明 | CLOSED_VERIFIED |
| G6-FINAL-011 | P2 | 唯一阶段PR超过仓库常规小PR建议 | 用户Goal明确要求唯一VID-G6 PR；合同记录显式范围例外，不扩展至G7/部署 | CLOSED_VERIFIED |
| G6-FINAL-012 | P2 | callback测试一次创建五个submitted任务，与新增user=1容量冲突 | 首任务走生产准入；额外回调夹具仅测试构建复用真实G5账本，回调/绑定/财务不Mock | CLOSED_VERIFIED |
| G6-FINAL-013 | P2 | 默认模型并发测试依赖全包数据库无既有默认模型 | 可恢复停用既有合成默认，候选结束后先停用赢家再恢复原默认 | CLOSED_VERIFIED |
| G6-FINAL-014 | P2 | service五百余测试超过20分钟包预算；跨进程分片会重置合成ID并主键碰撞 | 保持单进程/单库唯一序列，仅把包级验收预算调至40分钟；业务超时不变 | CLOSED_VERIFIED |
| G6-FINAL-015 | P2 | 取消HTTP测试在submitted占用名额后创建终态夹具 | 先真实执行终态并释放名额，再创建submitted取消场景；生产容量规则不变 | CLOSED_VERIFIED |
| G6-FINAL-016 | P1 | Project grant缺逐接口MFA、reason及前后审计故障回滚矩阵 | 同一真实HTTP/MySQL用例新增MFA失效、空/控制字符reason、前/后审计写失败与grant/command/audit/finance零事实快照；专项及最终all通过 | CLOSED_VERIFIED |
| G6-FINAL-017 | P2 | 五份主文档硬编码上一轮副本哈希和耗时，随源码变化漂移 | 主文档只引用最终evidence SSOT，不再重复易漂移值；精确副本和耗时只写不可变回执 | CLOSED_VERIFIED |
| G6-FINAL-018 | P2 | queue合同误写门闩在Hold前，实际为事务末尾提交前 | 文档同步为先形成事务内不可见暂态事实、末尾门闩拒绝后整笔回滚；不改已验证锁序 | CLOSED_VERIFIED |
| G6-FINAL-019 | P1 | PR #422 Ready CI分类器将Git大变更复制检测warning视为异常，后续门禁全部SKIP | 固定`git diff -l2000 --find-copies-harder`完成当前复制/重命名检测并保留有限复杂度保护，不忽略stderr；分类43项含warning/非零退出失败关闭，另29项合同及原PR精确base/head分类PASS | CLOSED_VERIFIED |
| G6-FINAL-020 | P1 | PR #422新HEAD四处敏感门禁把SHA-256/SOURCE_STATE中的11位数字片段误判为完整手机号 | 手机号正则改为ASCII字母数字边界，独立手机号仍检测且不回显；hex摘要形态不报。扫描器6项自测及最终595历史blob/596事件完整扫描findings=0 | CLOSED_VERIFIED |
| G6-FINAL-021 | P1 | G6客户专项脚本只跳过65/66却继续提前执行67—109，且请求夹具默认当前时间会在跨月后漂出固定汇总窗口 | 基线迁移严格停在000064后显式验证65/66；两条请求固定在2026-08窗口；增加4项脚本合同测试和失败输出口令遮蔽；真实临时MySQL 8专项及最终G6 all通过 | CLOSED_VERIFIED |

当前待独立终审缺陷计数候选：`P0=0 / P1=0 / P2=0`。若任一独立角色发现新问题，本台账立即恢复开放状态并重新绑定修复后的SOURCE_STATE。
