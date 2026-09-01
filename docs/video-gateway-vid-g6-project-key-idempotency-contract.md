# VID-G6 视频Project Key生命周期幂等合同（开发中）

## 适用范围

携带`video_generate_allowed=true`的Project Key在签发、轮换和吊销时强制Idempotency-Key。旧Chat/Image及video=false的既有路径保持原兼容行为，不从本增量获得视频能力。

## 响应与Secret

- 签发首次201：返回新Key元数据、`secret_key`、`secret_available=true`、`idempotent=false`。
- 签发重放200：返回原Key元数据，`secret_key=null`、`secret_available=false`、`idempotent=true`。
- 轮换首次201、重放200，Secret规则相同；重放不创建第二把Key。
- 吊销首次及重放均204；不会追加第二次审计或状态变更。
- 同键异用户、Project、源Key或配置指纹返回409。

Secret仅用于首次响应的进程内值，数据库继续只保存HMAC；命令表不保存Secret、KeyHash或可恢复密文。响应丢失后重放可确认结果Key，但必须再次轮换才能取得新的Secret。

## 事务与完整性

107迁移新增不可变`ai_project_key_commands`，冻结User/Project、issue/rotate/revoke动作、命令键HMAC、意图指纹、源/结果Key、严格结果、原审计ID及审计摘要SHA-256。结果JSON只允许`key_id`与`status=completed`两个字段；触发器禁止UPDATE/DELETE，down保留事实。

仓储先锁User行以串行同主体命令；审计摘要冻结动作、命令键HMAC、意图指纹及规范化低敏配置，不记录原Idempotency-Key、Secret或KeyHash。命中旧命令时验证指纹、完整命令行、结果/审计双SHA-256、原审计module/action/target/完整摘要以及结果Key归属和scope。issue只接受active且无轮换来源的结果；rotate要求来源revoked、结果active且`rotated_from_id`精确关联；revoke要求结果就是来源且已revoked。首次执行调用原Key/scope/审计事务后写命令并立即锁读同一套关系，任一损坏整体回滚。

轮换重放在旧Key已revoked后仍可由命令返回原结果；首次轮换仍完全从数据库锁定的旧Key和scope重建。签发预检和事务内模型/grant校验保持不变。

## 测试与边界

`project-key-idempotency`专项在临时MySQL schema107验证签发/轮换首次与重放、Secret只一次、同键异意图、吊销重放、Project grant回归和原Chat审计无明文；并验证首写审计缺绑定整笔回滚、已吊销issue结果、错绑rotate结果和未吊销revoke结果均失败关闭。最近一次副本SHA-256为`1ac9e4d883f89db99a1968ccb0d7553e1a28c5af31a4782295745dc9eda67fd7`。外部Provider、真实Key、钱包和费用为0。

仍需完整响应丢失/COMMIT确认未知故障注入和Key命令各写点故障矩阵；本专项只关闭已报告的命令—审计—结果语义绑定缺陷，不能据本片签VID-G6通过。

回滚只关闭视频Key幂等入口，保留Key、命令、scope和审计；不能删除命令后重新暴露Secret。
