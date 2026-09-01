# VID-G6 调账双人审批HTTP（开发与验证中）

## 范围与开关

`POST /api/admin/token/video-adjustments`只在显式启用的合成测试环境运行，需`AdjustmentsEnabled=true`及专用原因加密器，默认关闭，未接生产bootstrap。该接口不调用Provider、不抓媒体，不授权真实资金或真实账户调账。

原G5规则不变：credit/debit，原因码billing_correction/service_credit，正CNY金额、最多8位小数、小于1e12。实际修正仍通过原`VideoBillingService.ApplyAdjustment`追加钱包动作、adjustment Usage和Outbox，不改原Sale、成本、Quote、Hold及settled/released状态。调账不是原订单退款，也不能用来掩盖未闭合业务账。

## 两步请求

两步都需独立`Idempotency-Key`、真实管理员JWT、手机/邮箱双MFA及`ai_gateway:reconcile_manage`。序号由服务器在原Task锁内分配，同时考虑已有Usage和所有待审批/已过期审批，不能复用被旧计划占用的序号。

发起申请：

```json
{"action":"request","task_id":"原公开任务ID","version_no":12,"amount":"0.25","direction":"credit","adjustment_reason":"billing_correction","reason":"申请说明"}
```

amount必须是规范十进制字符串，拒绝JSON数字、科学计数法、前导零、多于8位小数或非正金额，不静默四舍五入。version_no是原Task当前版本。原请求先通过完整G5对账；申请只冻结计划、原因密文和审计，不产生资金动作。HTTP202返回approval_id、审批version_no=1及15分钟操作有效期，不延长任何媒体或权益期限。

另一管理员复核：

```json
{"action":"approve","approval_id":"vadj_公开审批号","version_no":1,"reason":"独立复核说明"}
```

approve不能携带新金额、方向、任务或checker_id；只能批准原不可变计划。当前checker来自本次JWT，maker来自已验证的申请；两人不同且执行时重新核验当前权限/MFA。过期、版本冲突、同人及伪造字段拒绝。

## 响应与重放

固定14字段：approval_id、task_id、request_id、status、amount、direction、adjustment_reason、sequence_no、version_no、task_version_no、expires_at、idempotent、usage_id、wallet_transaction_id。

- pending：202，version_no=1，两个资金引用为null。
- executed：200，version_no=2，返回原追加Usage/钱包动作ID。
- expired申请历史：200，保留原计划和原期限，不重新申请或续期。

审批版本与task_version_no分开；重放使用原请求body及原version_no，而不是把执行回执的版本2作为新的批准请求。金额规范返回8位小数字符串。`X-Molin-Request-ID`保持原业务request_id，不回显自由说明或密文。

同键申请绑定规范金额、方向、原因码、原Task版本和加密说明；先验证原密文，再比较原因HMAC，密钥不可用503不能误报409。已执行重放验证原审批、完整前后审计及G5调账等式，不再写钱包。当前操作者无权时不能借旧回执读取结果。

错误沿用平台语义：401/40001认证、403/40003权限/同人、403/40031 MFA、400/40000参数、409/40900版本或意图冲突、402/60001余额不足、503/50300依赖或账本不可证明。

## 事务与数据

先用短事务完成入口鉴权并释放其锁；主事务按Task→双主体ID顺序取得UPDATE锁，避免等待Task时持用户共享锁，再与原G5的用户锁升级互相阻塞。

复核前审计、prepared执行记录、原G5资金/Usage/Outbox、事后审计、executed审批消费在同一外层事务。内部通过私有上下文标记只执行一次保存点，不在失效保存点内重试；只有最外层重新执行完整事务。最后重新核验两人资格、MFA及审批期限，任何失败回滚全部新增动作。

000101新增：

- `ai_video_adjustment_approvals`：不可变授权计划、原归属/版本、金额/方向/原因码、服务端序号、计划SHA256、原因AES-GCM信封及前后审计。
- `ai_video_adjustment_approval_executions`：独立checker、prepared/version1→executed/version2、原因信封、审计及原Usage/钱包动作引用；一个审批最多执行一次，完成记录不可改写/删除。

数据库校验计划摘要、双主体差异、审批期限及实际G5资金行匹配。两张表是授权/执行回执，不是第二资金账本；自由说明使用调账专用AAD领域，不能进入普通财务字段或Outbox。

## 验证边界

首版真实HTTP及100并发approve通过，但48966整批FAIL：两个G5兼容测试因未配置专用DSN而SKIP，不计通过。G5原库名守卫保留，运行器改为在admin-adjustments通过后强制启动legacy-adjustments独立临时库子阶段，两部分都通过才返回整批PASS。

当前测试覆盖双人真实认证、不可修改金额、待审批序号不冲突、100并发一次资金动作、原账保留及调账后零差异，以及三种原因密钥变化重放503且不写审批/资金。终审整改进一步覆盖credit/debit、maker末尾撤权、余额不足402全回滚、合法过期审批复核409、审批UPDATE/DELETE不可变，以及复核资金事务真实COMMIT确认丢失后原键恢复；三次成功调整的Usage、钱包流水、Outbox与审计均唯一。

最新整改批G6调账HTTP、输出解除兼容和旧G5调账子阶段全部RUN/PASS、无SKIP，复制树SHA256为`638bf104a38e61328c998db98f2e5081cb597ff90f13fb72c43408fa97cb2ced`，schema109/Linux race通过。该切片仍须纳入最终SOURCE_STATE全量和四轴复核，不能单独签完整G6。

独立审查发现的P2“申请重放在密钥轮换后先比较HMAC而误报409”已修为先校验原密文，再判定原因意图；本批三种密钥变化实际返回503且未重复写审批或资金。

## 回滚

关闭独立开关和接口；down保留审批、钱包动作、Usage、Outbox和审计。不能撤销已形成的资金历史、覆盖原结算或把过期申请转为新计划。不部署共享测试服或生产，不进入G7。
