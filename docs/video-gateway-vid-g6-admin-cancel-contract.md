# VID-G6 管理员取消任务（开发与验证中）

## 功能与使用角色

管理员使用`POST /api/admin/token/video-tasks/{task_id}/cancel`处置原视频任务。需要当前管理员JWT、`ai_gateway:task_manage`权限及有效手机/邮箱双MFA，不接受Project SK，不冒用目标用户身份。目标用户或Key停用不妨碍有权管理员进行安全取消。

本入口只在本地显式装配；默认关闭，尚未接bootstrap或产品页面。必须显式提供专用原因加密器，否则管理写返回503，既有管理只读接口不受影响。当前开发不代表完整G6、生产或商业验收。

## 接口参考

请求要求单值16—128字节`Idempotency-Key`、UTF-8 `application/json`，不接受query、Content-Encoding或未知内容类型参数。正文上限4KiB，严格只允许两个字段：

```json
{"reason":"按用户申请取消本任务","version_no":1}
```

- `task_id`为原任务公开ID。
- `reason`去除首尾空白后为1—256个Unicode字符，UTF-8最多1024字节；拒绝控制字符。原因仅在内存使用及专用AES-GCM密文中保存，不回显。
- `version_no`为原Task正整数版本；HTTP上限为uint64最大值减8。版本冲突409。不得提供用户归属、金额、checker、Provider结果或替代存储参数。
- 同管理员同key绑定任务、原版本和规范化原因；同键异意图409。管理取消与用户取消命令域分离。

成功data为原管理员任务详情28字段，加`cancel_requested_at`、`cancellation_result`、`idempotent`共31字段。保留目标user_id/project_id/api_key_id和原业务`X-Molin-Request-ID`，不返回原因、密文或Provider原文。

| 结果 | HTTP | 行为 |
|---|---|---|
| cancelled | 200 | 已证明未提交的reserved/queued任务经原G5安全取消，释放原Hold和输入租约 |
| cancel_requested | 202 | submitting/submitted/执行中或pending_reconcile仅记录意图，不代表Provider接受，不释放Hold或输入租约 |
| already_terminal | 200 | 原成功/失败/取消/过期终态保持，不重新退款、不删除媒体 |

幂等重放先验证当前管理员权限/MFA及原命令、密文、审计绑定，不因取消自身增加版本而拒绝原版本。已经完成的取消仍验证原G5释放事实；提交过的取消最终转为cancelled，必须有原Provider释放证明，不能仅根据终态猜测退款。

错误：认证401/40001；权限403/40003；MFA403/40031；无效参数400/40000；媒体类型415/40000；版本或幂等冲突409/40900；未知任务404/40400；缺依赖、账本/审计/密文异常503/50300。失败不返回半份结果。

## 开发结构与事务边界

- `handler/video_admin_cancel_handler.go`：严格解析与默认关闭，映射200/202和平台错误。
- `service/video_admin_cancel.go`：管理员授权、原归属解析、CAS、独立命令回执、前后审计、重放核验。
- `service/video_admin_reason.go`：显式专用32字节密钥，派生独立加密及HMAC子密钥；AES-GCM随机12字节nonce。
- `service/video_billing_cancel.go`：原用户入口仍走原`authorizeVideo`；管理员只能用私有、同事务的权限能力进入相同取消内核，绑定操作者、Task、归属和原版本。没有修改原退款、Usage、租约及Outbox算法。
- `000094_video_admin_cancellation`：管理命令回执表，不是平行任务、事件或财务账本。

同一外层事务按顺序执行：当前管理员认证→锁原Task/Request→命令重放或CAS校验→前置审计→原G5取消／仅记录意图／终态无操作→事后审计→命令INSERT→核对审计及结果→末尾管理员复验→提交。只有最外层重试事务，不在失效保存点内重试。任何审计或命令写失败必须回滚整个取消，不允许业务成功却缺事后审计。

原因信封的AAD绑定密钥版本、管理员ID、Task公开ID、命令摘要和初始版本。回执保存nonce、ciphertext、AAD SHA-256、ciphertext SHA-256、原因HMAC和字符数。普通audit_logs只保存命令引用、HMAC、长度、版本与低敏结果；不保存自由文本。没有公开解密接口。密钥版本不匹配或密文损坏失败关闭，不能换密钥后把原key当新命令；旧原因的密钥留存和受控审阅仍需在部署前落实，不自动使用真实凭据。

回执主键为管理员及命令摘要，引用原Task/Request/owner/Key与原前后audit_logs；INSERT校验归属及审计，UPDATE/DELETE禁止。重放再次核对审计身份和七字段摘要，审计异常不得被忽略。

## 测试与证据边界

已新增加密公用组件测试：原身份解密、随机nonce、Actor/Task/命令/版本错绑、密钥/nonce/AAD/密文/摘要/长度篡改及普通JSON隐藏。本地通过不代表MySQL通过。

真实HTTP/MySQL测试覆盖目标停用、基础权限与MFA拒绝、严格JSON、CAS、同键重放与异意图、原因密文可审阅、回执不可修改、已提交仅记意图、事后审计失败回滚、Provider调用不增加。81315首轮取消应200实际503，审查发现触发器错误引用Task的计费/交付列；改为原Request字段后98876全部17项专项通过，取消HTTP测试4.07秒、service56.214秒、schema94及Linux race通过。此结果不代替下列剩余矩阵或完整G6验收。

独立Standards发现的三个P2均经17623真实HTTP/MySQL反例复现：原始0xff被接受、真实换密钥被误报409、审计读取错误链丢失。修复后23589对应三项通过，分别验证400且无写入、三类密钥变化503且原事实不增加、1213/1205错误边界注入后重试完整外层事务且不重复退款。错误注入不等于数据库自行发生锁竞争。另补的平台错误码和`*sql.Tx`类型断言尚待扩大MySQL回归，不能用23589覆盖。

后续批次已补100并发（1首次/99重放）、前后审计与命令写入回滚、I2V租约、真实COMMIT成功后确认丢失及幂等恢复。42546强化了命令/审计全文不变与原Request/幂等结果断言；I2V金额断言误用0.50，经查原G5 I2V夹具0.15元/秒×5秒应0.75，修正后65318单项通过，未修改任何计费规则。T2V夹具仍为0.50，不混用两种金额。

尚需完成：多管理员/异键及提交竞争、数据库自行产生的锁竞争、私有权限能力错绑、末尾权限/JWT/MFA跨期及完整G6回归。不得把上述未完成项写成PASS。

本地执行使用`infra/scripts/verify-video-gateway-vid-g6.sh`的当前管理员范围；必须同时核对整体退出码、所有强制项RUN/PASS、无SKIP。禁止用共享测试数据库替代隔离库。

## 回滚边界

关闭管理写路由或移除写依赖即可停止新操作。000094 down只保留事实，不删除表、回执、原因密文或审计，不撤销已完成退款，不重新提交任务，也不自动释放在途任务。回滚版本仍必须保留既有输入租约、G5幂等及财务事实。

相关：[G6完整合同](./video-gateway-vid-g6-http-project-sk-contract.md)、[管理只读查询](./video-gateway-vid-g6-admin-read-contract.md)、[API总表](./full-api-design.md)、[数据库设计](./database-schema-design.md)、[测试计划](./test-plan.md)。
