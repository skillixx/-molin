# 阿里云短信验证码阶段 5 Canary 测试报告

## 1. 当前状态

状态：**未执行，等待独立测试服真实短信授权。**

阶段 5A 关闭态部署与代理验证没有发送短信。当前测试服 `SMS_ENABLED=false`、`SMS_TEST_MODE=true`、白名单数量 1，
固定代理和 4 条短信告警已经部署通过。
发送日志只读基线为 accepted 13、failed 0，与阶段 2 历史 7+6 条一致；阶段 5 Canary 必须只计算基线后的增量。

2026-08-05 首次执行只读聚合预检：关闭态、回滚候选、回滚材料和监控均为 true；通知演练与日志留存策略均为
false，因此固定输出 `canary_preflight=blocked`、`canary_preflight_ready=false` 并以退出码 2 失败关闭。执行过程
业务配置修改 0、服务重启 0、真实短信 0；SSH 只读访问可能增加系统访问审计日志。

2026-08-05 后续获批完成 journald `8G/50G/14day/1day` 留存策略和 Alertmanager 邮件候选关闭态部署。最新实服聚合预检中
关闭态、回滚候选、回滚材料、监控和日志留存均为 true，仅 `notification_drill_ready=false`，因此仍严格输出
`canary_preflight=blocked`。Alertmanager 根路由为 `discard`，本次部署与复核未触发告警，邮件和短信发送均为 0。

2026-08-05 经独立授权执行邮件通知演练 `20260805T094720Z`：Alertmanager 仅形成 1 次 firing SMTP 通知请求，通知失败
指标为 0，业务短信 Provider 增量为 0；负责人随后明确确认 firing 邮件**未收到**。失败清理尝试提交 resolved 时，
Alertmanager 于 `2026-08-05T09:55:54.116Z` 以 `start time must be before end time` 拒绝无效载荷，因此 resolved
通知计数为 0。失败路径已恢复 `discard` 和关闭态配置，活动告警为 0，`SMS_ENABLED=false`、`SMS_TEST_MODE=true`。
本次演练正式记为**失败**，不得据此解除 Canary 门禁，也不得在原授权下重试 firing 或 resolved。仓库外受控失败证据位于
`D:\molingproject\molin-phase5-alertmanager-drill-failure-evidence-20260805T094720Z`，manifest SHA-256 为
`22acc123e477cc3084cde19e56e6e123864ef3db42c7295776bbbe245419d606`，不含邮箱地址或 SMTP Secret。复盘已新增
离线载荷转换校验和中断恢复规则；下一次通知演练必须取得新的独立授权与 ChangeId，并先完成 126 发件侧记录、QQ
垃圾箱/拦截规则和收件地址哈希的双人核对。

## 2. 执行门禁

只读聚合入口：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-canary-preflight.ps1
```

该入口依次复用关闭态、回滚候选、回滚材料/通知链和 journald 四个既有只读验证器，只输出布尔与低敏摘要。
只有关闭态、固定代理、零发送增量、监控规则、回滚材料、当前环境派生候选、Alertmanager 通知演练和日志留存策略
全部通过时，才输出 `canary_preflight_ready=true`。它没有开启开关或发送短信的代码路径；输出通过也只代表允许进入
后续人工批准窗口，不代表已取得真实短信授权或完成 Canary。

通知演练不能由配置存在性自动推定。完成独立获批的合成演练后，负责人必须同时提供精确确认短语、演练 ChangeId、
仓库外证据 JSON 的绝对路径和其 SHA-256；聚合器会从同一文件句柄读取并核对摘要、24 小时有效期，并逐份读取 JSON
引用的五层仓库外原始证据文件、复算五个独立摘要，再校验五层结果、
一次 firing/resolved、短信关闭、Provider 零增量、通知队列清空及无敏感字段，再确认运行态为
`transport_present_receiver_unverified`。任一条件缺失即阻断。证据确认不会部署 Alertmanager、触发告警或构成真实短信授权。

- 测试服固定代理、关闭态来源链与监控加载通过；原始异常头和限流矩阵在真实 Canary 前复核。
- 部署版本、备份和回滚点已记录。
- 五模板审核通过、启用且五场景一一绑定。
- 只读脚本确认白名单非空且数量等于批准值，不输出号码；号码归属、用户同意以及窗口结束恢复原白名单仍由两人复核并留存脱敏变更记录。
- 单窗口总发送上限 10 条；预算仍受项目 500 条总上限约束。
- 执行结束无条件恢复原白名单和 `SMS_ENABLED=false`。

## 3. 待填证据

| 场景 | 模板后四位 | 阿里云受理 | 手机收件 | OTP 单次消费 | 最终业务状态 |
|---|---|---|---|---|---|
| register | 待执行 | 待执行 | 待确认 | 待执行 | 待执行 |
| login | 待执行 | 待执行 | 待确认 | 待执行 | 待执行 |
| reset_password | 待执行 | 待执行 | 待确认 | 待执行 | 待执行 |
| bind_phone | 待执行 | 待执行 | 待确认 | 待执行 | 待执行 |
| admin_verify | 待执行 | 待执行 | 待确认 | 待执行 | 待执行 |

报告只允许保存脱敏手机号、模板后四位、时间、业务请求标识摘要和供应商请求标识摘要，不保存 OTP。
