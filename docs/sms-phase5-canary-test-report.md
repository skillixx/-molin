# 阿里云短信验证码阶段 5 Canary 测试报告

## 1. 当前状态

状态：**技术前置门禁已通过，等待独立测试服真实短信 Canary 授权。**

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
指标为 0，业务短信 Provider 增量为 0。负责人初次检查反馈未收到，后续以主题 `[test] MolinSMSDrill` 全邮箱搜索确认
邮件实际位于 QQ 收件箱；预期收件地址 SHA-256 也与测试服配置一致，因此 firing 的接收渠道到达已得到人工确认。
失败清理尝试提交 resolved 时，Alertmanager 于 `2026-08-05T09:55:54.116Z` 以
`start time must be before end time` 拒绝无效载荷，因此 resolved 通知计数为 0。失败路径已恢复 `discard` 和关闭态配置，
活动告警为 0，`SMS_ENABLED=false`、`SMS_TEST_MODE=true`。本次演练仍正式记为**失败**，唯一失败阶段修正为
`resolved_validation`；不得据此解除 Canary 门禁，也不得在原授权下重试 firing 或 resolved。仓库外受控失败证据位于
`D:\molingproject\molin-phase5-alertmanager-drill-failure-evidence-20260805T094720Z`，最终 manifest SHA-256 为
`96ea4637945d80ee8c27035a5b6bdbb2030514e2a72906b23bb6efb466f0f2c7`，保留初次未找到与后续收件箱确认两层时序证据，
且不含邮箱地址或 SMTP Secret。复盘已新增离线载荷转换校验和中断恢复规则；下一次通知演练必须取得新的独立授权与
ChangeId，并使用 `resolved.endsAt > resolved.startsAt` 的候选载荷。

同日将该失败 manifest 输入 `-ValidateNotificationEvidenceOnly` 成功证据入口，验证器以退出码 1 和“字段集合不符合契约”
失败关闭，证明失败材料不能误置 `notification_drill_ready=true`，该本地验证没有连接测试服或产生通知。

针对上述缺陷生成新 ChangeId `20260805T105517Z` 修正版候选：resolved 仅在 firing 实际收件确认后动态生成，并强制
`resolved.endsAt > resolved.startsAt`；授权输入 300 秒、收件确认 600 秒、firing 自然到期 1800 秒，确保确认超时后
先恢复 `discard`。runner SHA-256 为 `b076494ac2e07fa75f3f155869348580f41b9e808e4d6dd42e1a75f0959b578c`。
经独立上传/只读预检授权，runner 与专用摘要清单已放入固定测试服暂存目录；远端摘要、`bash -n`、`--self-test` 和
关闭态预检均通过，确认路由关闭、活动告警 0、累计通知/请求基线 1/1、失败计数 0、收件哈希一致、Provider 0、
`SMS_ENABLED=false`、`SMS_TEST_MODE=true`。本窗口配置修改 0、服务重载 0、通知 POST 0、真实短信 0；候选执行尚未授权，
不得以无参数方式运行。仓库外候选 manifest SHA-256 为
`774b740474ecc1b7e55966d84125d9fa44eb0df8b4fcdb2f1e388c10fa27e98c`。

随后经独立执行授权完成修正版单次邮件演练 `20260805T105517Z`：仅发送 1 次 firing 和 1 次 resolved，负责人均在 QQ
收件箱确认收到主题为 `[test] MolinSMSDrill` 的当前 ChangeId 邮件；resolved 载荷转换校验通过，且没有重试或其他告警。
演练结束后根路由恢复为 `discard`，配置 SHA-256 精确恢复为
`2e906ed20a48d2585f7b7648892de1ee809afdf34c6e45b9a110722fab48239d`。独立复核确认累计通知/请求为 3/3、失败计数为
0、活动告警为 0、Provider 增量为 0、`SMS_ENABLED=false`、`SMS_TEST_MODE=true`、Alertmanager health/ready 为
200/200、Prometheus 活跃 Alertmanager 为 1。全过程真实短信 0。

成功证据位于仓库外受控目录
`D:\molingproject\molin-phase5-alertmanager-drill-evidence-20260805T105517Z`；成功 manifest SHA-256 为
`67c9b95adc648c6689e904bae255131a619fc7a93f5fd8a1a15f9ce5062bf7a0`，证据契约校验输出
`notification_evidence_validation=passed`。在此基础上再次执行完整实服聚合预检，关闭态、回滚候选、回滚材料、监控、
通知演练和日志留存全部为 true，输出 `canary_preflight=passed`、`canary_preflight_ready=true`；该只读预检业务配置修改
0、服务重启 0、真实短信 0。预检通过仅表示技术前置条件满足，不构成真实短信 Canary 授权，也不代表阶段 5 已完成。

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
