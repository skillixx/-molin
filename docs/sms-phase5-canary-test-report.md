# 阿里云短信验证码阶段 5 Canary 测试报告

## 1. 当前状态

状态：**技术前置、实际回滚、验收层级、本地脱敏计划和固定测试服双号码只读状态核验均已完成；target-admin 精确白名单变更及其新 ChangeId 独立关闭态只读复核均已通过，等待后续真实短信授权。**

阶段 5A 关闭态部署与代理验证没有发送短信。当前测试服 `SMS_ENABLED=false`、`SMS_TEST_MODE=true`、白名单数量 2，
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

2026-08-06 在分支提交 `a6ea0b4` 上重新复算同一成功 manifest 摘要并执行证据限定校验，仍得到
`notification_evidence_validation=passed`；随后完整聚合预检六项仍全部为 true，`canary_preflight=passed`。本次复验
远程连接、业务配置修改、服务重启和真实短信均为 0，证明无参运行得到的 `notification_drill_ready=false` 只是缺少四项
显式证据绑定参数，不能覆盖已经通过摘要和人工确认的成功演练结论。

2026-08-05 产品负责人选择“真实受理与收件 Canary”，明确不消费 OTP、不批准账号、密码、会话、换绑或 MFA 状态变化。
本地默认关闭生成器随后创建 ChangeId `20260805T132831Z` 的仓库外脱敏计划，SHA-256 为
`633f4eeb1b855d9295d0b9fae8ed3d7dc47de3b33e577726c8ed21173301034b`。候选固定五场景各提交一次、总量 5、零重试，
`register/bind_phone` 使用 `target-new:unregistered`，其余三个场景使用 `target-admin` 的注册/管理员状态；候选文件数 1、
手机号字面量 0、敏感字段 0。生成和两次独立静态校验期间网络、上传、短信均为 0，未修改开关。该证据不证明真实号码归属、
账号状态、白名单就绪或短信收件，固定测试服只读状态预检和真实执行仍须分别授权。

随后按独立授权生成绑定同一 ChangeId 与计划摘要的双号码本地隐藏输入 runner，SHA-256 为
`eb67246dffaabcfdb95a71fecaf3bec9a7da522461bfc63907e90b79483fff9e`。候选仅实现 `Read-Host -AsSecureString`、BSTR
临时解包、`ZeroFreeBSTR` 清理以及内存中的格式/互异校验；AST 语法错误、完整手机号字面量和网络命令标记均为 0。
生成时只运行默认关闭和合成值自测，未进入 `-Interactive`。随后取得与 ChangeId 和 runner 摘要绑定的独立授权，并仅执行一次
本地隐藏输入：`interactive_prompts=2`、`format_verified=true`、`distinct_targets_verified=true`；号码只短暂进入内存，未输出或
持久化。结果同时明确 `registration_state_verified=false`、`admin_identity_verified=false`、`whitelist_verified=false`，因此不能
推定号码归属、注册状态、管理员身份或白名单状态。本次网络、上传、白名单修改、短信开关修改和真实短信均为 0；远端只读预检仍须独立授权。

随后按独立授权生成固定测试服只读状态 runner，最终 SHA-256 为
`4fc5c4442a5530f8b5cad83a7d92db68722ecc5972ebacf6791ffe1e305d8e9c`。候选固定 SSH 主机、端口、用户和唯一 ED25519 指纹，
隐藏输入的两个号码仅经 stdin 进入远端内存；内嵌负载只允许 `SELECT` 用户、直接管理员角色、`user:manage` 权限、白名单和发送计数。
PowerShell/Bash 语法、默认关闭、合成值、白名单总门禁、只读 SQL 和敏感字面量检查均通过，双轴复审无 P0/P1/P2。2026-08-06
取得一次性执行授权后，操作人完成两次隐藏输入，runner 返回 `readonly_exit_code=2` 和“固定测试服只读状态预检未通过”；没有自动重试。
该版本在输出远端缓冲前抛出异常，导致实际布尔原因不可恢复；只能确认预检未通过，不能确认账号、管理员或白名单状态。生成器已在本地补充
“先输出低敏远端结果和退出码、再失败关闭”的回归修复；原 ChangeId、SHA 和执行批准均已消费，修正版必须使用新 ChangeId 并重新取得生成授权。

随后取得仅限本地生成和静态验证的独立授权，生成新 ChangeId `20260805T164138Z` 的 `receipt_only` 计划和修正版 runner。计划
SHA-256 为 `9188dce74133797bb155ed9fc969be11ec64daf58921d2747a5fe1e8ecb6e126`，runner SHA-256 为
`d00ff59ab40d23b20fb350557cd436db0cd86641553dc76ea3afc7e868687f34`。独立校验确认 PowerShell/Bash 语法、默认关闭、合成值、
固定 SSH 身份、只读 SQL、失败证据输出顺序和敏感字面量均通过；真实输入、网络、上传、业务 POST、白名单/开关修改、邮件和短信均为 0。
该结果只证明修正版候选本地可审计，不构成固定测试服执行授权。

该 runner 随后按一次性批准执行，远端 Bash 在首个 `then` 处返回语法错误，`readonly_exit_code=2`。错误回显证明多行负载经 SSH 参数重组后变成单行；脚本未进入 API 进程、数据库、白名单或发送计数查询，因此不能据此判定任何目标状态。执行摘要为 `network_connections=1`、`uploads=0`、`business_posts=0`、`real_sms_sent=0`，且没有重试。生成器已改为以 LF/无 BOM 的 SSH stdin 交给 `bash -s`，并新增禁止 `eval` 参数链的回归断言；修复后尚未生成或执行新 ChangeId。

随后按独立本地生成授权创建 ChangeId `20260805T170528Z` 的新候选。计划 SHA-256 为
`43b37bdb00ed954004324a3cc9fcfd50ce013d5b4517e6ae3715f5a0392b1a75`，runner SHA-256 为
`884ec7f681f8b1e0502c71efc31bc0aa2d97b459d10551875b6daeeb4dbac8c3`。计划校验、PowerShell 解析、默认关闭、SelfTest、5 项候选契约和 Bash `-n` 均通过；远端负载为 125 个 LF、0 个 CR、无 BOM，只读 SQL 写匹配、完整手机号字面量、旧 `eval` 与旧 `$remoteCommand` 均为 0。验证确认 stdin 底层字节写入、内存字节数组清零和 `bash -s` 传输契约存在。全过程未输入手机号、未连接测试服、未上传、未修改白名单、未调用业务 POST、未发送邮件或短信。该结果仅证明本地候选可审计，不构成执行授权。

随后取得一次性固定测试服只读执行授权，操作人完成两次隐藏输入。runner 成功通过固定 SSH stdin 读取状态并输出
`readonly_exit_code=3`：`sms_enabled=false`、`sms_test_mode=true`、target-new 未注册、target-admin 已注册且手机号已验证、直接
admin 角色、`user:manage` 权限、target-new 白名单、白名单读取和发送日志零增量均为 true；唯一失败项为
`target_admin_whitelisted=false`，因此 `whitelist_targets_ready=false`、`whitelist_verified=false`。执行期间配置修改、业务 POST、上传、短信提交请求和真实短信均为 0，远端 stderr 为空，没有自动重试。候选已移至仓库外 `consumed-exit3-884ec7f6` 隔离路径；不得重跑。该结果把下一门禁精确收敛为 target-admin 白名单受控变更与回滚，不构成任何真实发送授权。

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

## 4. target-admin 精确白名单变更候选验证

按独立本地生成授权，已生成 target-admin 精确白名单变更与自动回滚最终候选 ChangeId `20260805T180909Z`，runner SHA-256 为 `d202e6f7f9ee23b63f7c9556dd2f9e2fca7ca846ef5e0c21cbfa06d7b60079f7`。4 项候选契约测试通过；独立复验确认 runner PowerShell 解析错误 0、内嵌 Bash 语法退出码 0、负载自测退出码 0 且 stderr 为空，`candidate_add_only_test=passed`、`automatic_file_rollback_test=passed`。双轴审查发现的动态 SQL 进程参数、锁竞态/信号窗口和同名目录证据污染均已修复：SQL 改为 stdin，锁与 ChangeId 目录通过不可中断临界区完成原子创建和状态登记，退出证据只写入本次核验过的目录。

负载 CR、完整手机号字面量、外部 URL、上传命令、动态 SQL argv 和 `SMS_ENABLED=true` 均为 0。旧候选（包括继续加固前的 `20260805T174747Z`、`20260805T175544Z`、`20260805T175907Z`、`20260805T180434Z`）已以 superseded 后缀可恢复隔离；在该本地生成阶段没有输入手机号、联网、上传、修改环境、发送信号、重启服务、发送邮件或短信，实际白名单变更当时尚未批准或执行。

随后 ChangeId `20260805T180909Z`、runner SHA-256 `d202e6f7...0079f7` 获得一次性精确授权并执行成功，禁止重试。runner 输出确认白名单数量从 1 变为 2、target-new 保留、target-admin 新增，`SMS_ENABLED=false`、`SMS_TEST_MODE=true`、Alertmanager 活动告警 `0:0`；发送日志、Provider 与通知计数均零增量，业务 POST、上传、短信提交请求和真实短信均为 0，服务停止/启动各 1 次，未进入回滚路径。

执行授权消费后，仅在本地生成变更后只读复核候选 ChangeId `20260805T182328Z`：receipt-only 计划 SHA-256 为 `f84c96a61172d025909c5b3d15116f9f6cb67f7c056bf6d2e071234f6accda89`，runner SHA-256 为 `2a4225f6b7c77738226afb495c8596b9b04f80bf057d49a81532a0a90da8540f`。PowerShell/Bash 语法、只读 SQL、双目标白名单总门禁、默认关闭、完整手机号字面量和上传命令检查均通过；生成与静态验证期间输入、网络、上传、配置修改、服务操作和短信均为 0。

该 runner 随后按绑定 ChangeId、计划摘要和 runner 摘要的一次性授权执行，固定 SSH stdin 仅连接 1 次且未重试。结果为 `target_state_readonly_preflight=passed`、`readonly_exit_code=0`：`SMS_ENABLED=false`、`SMS_TEST_MODE=true`，target-new 未注册，target-admin 已注册且手机号已验证，并具有直接 admin 角色与 `user:manage` 权限；两个目标均在当前白名单，`whitelist_targets_ready=true`、`whitelist_verified=true`。发送日志零增量，业务配置修改、业务 POST、上传、短信提交请求、敏感值持久化和真实短信均为 0，远端 stderr 为空。该结果关闭白名单技术门禁，但不构成真实短信发送授权或收件证明。
