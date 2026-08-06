# 阿里云短信验证码阶段 5 Canary 测试报告

## 1. 当前状态

状态：**技术前置、实际回滚、日志留存、Alertmanager 邮件演练、双目标账号/IAM 与白名单均已通过。供应商频控修正版真实 Canary ChangeId `20260806T053735Z` 已完成五场景各一次提交、两个 65 秒窗口与零重试，两个自有号码的五场景均已人工确认收件；修正版事后只读核验 ChangeId `20260806T062059Z` 已证明五场景全部受理、OTP 均未消费且系统恢复关闭态。五档观察和最终验收尚未完成，阶段 5 尚未最终通过。**

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

## 3. 五场景验收证据

| 场景 | 模板后四位 | 阿里云受理 | 手机收件 | OTP 单次消费 | 最终业务状态 |
|---|---|---|---|---|---|
| register | 不持久化（数据库独立绑定已预检） | accepted | 已人工确认 | 未消费（`receipt_only`） | 无业务状态变更 |
| login | 不持久化（数据库独立绑定已预检） | accepted | 已人工确认 | 未消费（`receipt_only`） | 无业务状态变更 |
| reset_password | 不持久化（数据库独立绑定已预检） | accepted | 已人工确认 | 未消费（`receipt_only`） | 无业务状态变更 |
| bind_phone | 不持久化（数据库独立绑定已预检） | accepted | 已人工确认 | 未消费（`receipt_only`） | 无业务状态变更 |
| admin_verify | 不持久化（数据库独立绑定已预检） | accepted | 已人工确认 | 未消费（`receipt_only`） | 无业务状态变更 |

报告只允许保存脱敏手机号、模板后四位、时间、业务请求标识摘要和供应商请求标识摘要，不保存 OTP。

## 4. target-admin 精确白名单变更候选验证

按独立本地生成授权，已生成 target-admin 精确白名单变更与自动回滚最终候选 ChangeId `20260805T180909Z`，runner SHA-256 为 `d202e6f7f9ee23b63f7c9556dd2f9e2fca7ca846ef5e0c21cbfa06d7b60079f7`。4 项候选契约测试通过；独立复验确认 runner PowerShell 解析错误 0、内嵌 Bash 语法退出码 0、负载自测退出码 0 且 stderr 为空，`candidate_add_only_test=passed`、`automatic_file_rollback_test=passed`。双轴审查发现的动态 SQL 进程参数、锁竞态/信号窗口和同名目录证据污染均已修复：SQL 改为 stdin，锁与 ChangeId 目录通过不可中断临界区完成原子创建和状态登记，退出证据只写入本次核验过的目录。

负载 CR、完整手机号字面量、外部 URL、上传命令、动态 SQL argv 和 `SMS_ENABLED=true` 均为 0。旧候选（包括继续加固前的 `20260805T174747Z`、`20260805T175544Z`、`20260805T175907Z`、`20260805T180434Z`）已以 superseded 后缀可恢复隔离；在该本地生成阶段没有输入手机号、联网、上传、修改环境、发送信号、重启服务、发送邮件或短信，实际白名单变更当时尚未批准或执行。

随后 ChangeId `20260805T180909Z`、runner SHA-256 `d202e6f7...0079f7` 获得一次性精确授权并执行成功，禁止重试。runner 输出确认白名单数量从 1 变为 2、target-new 保留、target-admin 新增，`SMS_ENABLED=false`、`SMS_TEST_MODE=true`、Alertmanager 活动告警 `0:0`；发送日志、Provider 与通知计数均零增量，业务 POST、上传、短信提交请求和真实短信均为 0，服务停止/启动各 1 次，未进入回滚路径。

执行授权消费后，仅在本地生成变更后只读复核候选 ChangeId `20260805T182328Z`：receipt-only 计划 SHA-256 为 `f84c96a61172d025909c5b3d15116f9f6cb67f7c056bf6d2e071234f6accda89`，runner SHA-256 为 `2a4225f6b7c77738226afb495c8596b9b04f80bf057d49a81532a0a90da8540f`。PowerShell/Bash 语法、只读 SQL、双目标白名单总门禁、默认关闭、完整手机号字面量和上传命令检查均通过；生成与静态验证期间输入、网络、上传、配置修改、服务操作和短信均为 0。

该 runner 随后按绑定 ChangeId、计划摘要和 runner 摘要的一次性授权执行，固定 SSH stdin 仅连接 1 次且未重试。结果为 `target_state_readonly_preflight=passed`、`readonly_exit_code=0`：`SMS_ENABLED=false`、`SMS_TEST_MODE=true`，target-new 未注册，target-admin 已注册且手机号已验证，并具有直接 admin 角色与 `user:manage` 权限；两个目标均在当前白名单，`whitelist_targets_ready=true`、`whitelist_verified=true`。发送日志零增量，业务配置修改、业务 POST、上传、短信提交请求、敏感值持久化和真实短信均为 0，远端 stderr 为空。该结果关闭白名单技术门禁，但不构成真实短信发送授权或收件证明。

## 5. 五场景真实收件默认关闭候选验证

按本地生成授权创建最终候选 ChangeId `20260805T191326Z`：receipt-only 计划 SHA-256 为 `772b5bcdfe49e24bc508c8dd8224994c079369895a4b4f2f19ac15c24a280a3b`，runner SHA-256 为 `062f45cd500caf21a3cafb0b2c941529df12121f89a14dadbf131271440e543c`。独立验证确认 PowerShell 与 Bash 语法、默认关闭、runner/payload 自测、固定 SSH 辅助脚本摘要、管理员 Token 与 target-admin/IAM 的发送前只读绑定、锁所有权、恢复失败材料保留、请求体 stdin 和双敏感输入无 argv 均通过；完整手机号和 JWT 字面量为 0。

候选生成与静态验证阶段的阶段 5 全量契约为 115 项通过、3 项环境跳过；静态总门禁和历史敏感扫描通过，`findings=0`、`sms_enable_literals=0`。该生成轮次未实际输入手机号或 Token，网络、上传、测试服配置修改、服务重启和真实短信均为 0。

2026-08-06，项目负责人以 ChangeId、计划完整 SHA-256、runner 完整 SHA-256、五场景各一次、总计 5 次、零重试、自动恢复关闭态及失败保留恢复材料的精确边界，批准并启动该 runner。启动前再次核验计划、runner 与固定 SSH 辅助脚本摘要，默认关闭和 SelfTest 均通过；随后仅启动 1 个可见交互进程，`execution_attempts=1`、`automatic_retry=false`。该进程已经结束，项目负责人仅确认管理员 Token、手机号及身份绑定前置验证成功；尚未提供 runner 的五场景提交计数、供应商受理、恢复关闭态、Provider/通知/告警增量等低敏摘要，也未提供两个目标逐场景人工收件结果。本地候选目录没有结果文件，因此当前证据只能记为“单次授权已消耗、发送前身份门禁通过、执行结果待回收”，不得重跑、不得声称五次提交成功、供应商受理、手机收件或关闭态恢复已经证明。

执行结束后，项目负责人提供管理员安全认证页面截图：第 1 步手机发码停留在原步骤，按钮恢复可操作，并显示“短信功能当前不可用”。本分支前端只在后端返回 HTTP `503` / 业务码 `50300` 时映射该提示；服务层在短信开关关闭、Sender/配置不可用、白名单或模板门禁失败、Redis 门禁异常等失败关闭路径均可能返回该业务错误。因此该截图与 `SMS_ENABLED=false` 恢复预期一致，且没有显示发码成功、倒计时或进入下一步，但它不能单独区分具体失败原因，也不能证明 Provider 零调用或服务端恢复完成。该页面操作不得计入获批五场景，后续须用 runner 摘要或另行批准的关闭态只读核验完成服务端证据闭环。

在上述执行结束后，本地重新运行阶段 5 准备度、历史敏感扫描和全量契约：115 项通过、3 项按 Windows 环境设计跳过；`findings=0`、`sms_enable_literals=0`。该复跑不联网、不读取测试服状态，也不能替代缺失的运行摘要和人工收件证据。

## 6. 五场景执行后只读核验候选

由于原 runner 未持久化低敏摘要且交互窗口已经结束，已在仓库外生成默认关闭的事后只读候选 ChangeId `20260805T193505Z`，绑定源执行 ChangeId `20260805T191326Z`，runner SHA-256 为 `57f3233d8d3f08b302173935c0cbb21c3bbf33e7677be09da0df991e11b0dae3`。候选计划仅通过固定 ED25519 身份建立 1 次 SSH stdin 连接，读取 API 进程与文件关闭态、health/ready、白名单数量、短信日志聚合、基线 13 条之后的五场景分布与供应商受理字段、关联 OTP 未消费状态、当前恢复后进程的 Provider 指标、Alertmanager `discard`、活动告警、通知失败计数以及精确恢复锁/材料状态。内部 metrics 与 Alertmanager metrics 均使用显式可达性门禁，请求失败或指标族缺失时输出 `unavailable` 并阻断，禁止把不可用误记为零。

PowerShell 语法、Windows PowerShell 5.1 UTF-8 BOM 兼容、默认关闭、runner SelfTest、Git Bash `bash -n` 和载荷 SelfTest 均已通过；完整手机号与 JWT 字面量为 0。生成与静态验证期间网络连接、上传、业务 POST、配置修改、服务信号、自动重试和真实短信均为 0。实际执行必须重新取得该 ChangeId 与完整 runner SHA-256 的独立只读授权；它不会补发短信、修复环境、清理恢复材料或替代人工逐场景收件确认。

首次收到的事后核验批准仍引用修正前旧 SHA-256 `23112fea...36b1e`，与当前 runner 摘要不匹配，已在连接前失败关闭。该错误批准没有启动 runner、没有建立 SSH、没有读取测试服、没有上传、业务 POST、配置修改、服务信号、邮件或短信；不得把它登记为执行尝试或复用其授权范围。

随后项目负责人以当前完整 SHA-256 `57f3233d8d3f08b302173935c0cbb21c3bbf33e7677be09da0df991e11b0dae3` 批准一次性只读执行。runner 仅建立 1 次固定 SSH stdin 连接且未重试，输出 `canary_postcheck=blocked`、退出码 3。关闭态、测试模式、API health/ready、白名单数量 2 和恢复锁已确认：`SMS_ENABLED=false`、`SMS_TEST_MODE=true`、`recovery_lock_clear=true`，没有遗留恢复材料。短信日志仍为基线 `13:13:0:0`，基线后五场景形状、供应商受理字段和 OTP 未消费证据均不存在，证明源执行 ChangeId `20260805T191326Z` 没有形成任何五场景发送记录，而不是“发送结果待回收”。本次事后核验自身的业务 POST、配置修改、上传、服务信号、自动重试、邮件和短信均为 0。

根因已定位为源 Canary 生成器的发送前 Alertmanager 门禁使用了不存在的旧路径 `/home/pc/molin/infra/alertmanager/alertmanager.yml`；固定测试服实际关闭态配置位于 `/home/pc/molin-alertmanager-phase5/20260805T084215Z/alertmanager.closed.yml`，因此 runner 在账号/IAM 远程绑定、加锁、打开短信开关和发送之前即失败退出。事后核验中的 `current_process_provider_metric_total=unavailable` 另由一次性候选误读 `INTERNAL_METRICS_TOKEN` 引起，实际环境键为 `INTERNAL_API_TOKEN`；该字段不可作为 Provider 零调用证据。`alertmanager_route_discard=false` 同样来自旧路径检查，不能推翻此前已完成的 Alertmanager 关闭态证据。仓库内生成器现已改为核验实际部署配置、容器运行态和 ready，并新增严格低敏结果文件的单次创建与摘要输出；本地阶段 5 契约 115 项通过、3 项环境跳过，静态准备度和历史敏感扫描通过，网络连接与真实发送均为 0。任何修正版真实 Canary 必须生成新 ChangeId、重新冻结计划及 runner 摘要并取得新的精确发送授权。

## 7. 修正版五场景候选

首次修正后生成的 ChangeId `20260805T200101Z` 在进一步静态审查中发现结果持久化仅限制了键值字符形状，尚未限制允许的字段名；虽然既定远端载荷不会输出敏感字段，为防止异常输出借任意键进入结果文件，该候选已在执行前隔离为 rejected，未输入敏感值、未联网、未连接测试服且真实短信为 0，不得执行。

最终重新生成 ChangeId `20260805T200244Z`：receipt-only 计划 SHA-256 为 `3d47f96d172f3fc976b5acdc20e65b64813669bbce64799b0abc9d587fa45045`，runner SHA-256 为 `d7748b2df0056b9fcd2775b464a0dafd622142809f30d723deeff4d8de96c9ec`。runner 固定实际 Alertmanager 关闭态配置、容器运行态和 ready 门禁，仍在账号/IAM 绑定、加锁、开关切换及发送前完成核验；低敏结果只允许预定义字段名和受限值字符，使用 `CreateNew` 单次写入并输出文件摘要，不保存手机号、Token、OTP、远端 stderr 或自由文本，既有结果文件会阻断重复执行。

独立验证确认计划契约、计划与 runner 摘要、PowerShell 解析、默认关闭、runner SelfTest、UTF-8 无 BOM、Git Bash `bash -n`、载荷 SelfTest、实际 Alertmanager 路径、旧路径不存在、低敏字段白名单和候选目录单文件集合均通过；完整手机号与 Bearer Token 字面量为 0，结果文件尚未生成。阶段 5 全量契约 115 项通过、3 项按环境设计跳过，静态准备度与历史敏感扫描通过，`findings=0`、`sms_enable_literals=0`。本轮网络连接、上传、配置修改、服务重启和真实短信均为 0。该候选尚未取得真实发送授权；执行前必须由项目负责人以完整 ChangeId、计划 SHA-256、runner SHA-256、五场景各一次、零重试、自动恢复关闭态及失败保留恢复材料的边界重新批准。

项目负责人随后以完整 ChangeId、计划摘要、runner 摘要和五场景边界批准一次性执行。启动前计划、runner、固定 SSH 辅助脚本摘要、默认关闭、SelfTest 与结果文件不存在均重新通过；可见交互进程只启动 1 次，没有自动重试。runner 生成的低敏结果 SHA-256 为 `ea58e017b7a47f48efaf1ed1e670b43ce6f0eec7c65afb727eb0294c8b00524f`，结果为 `canary_send=blocked`、`failure_gate=enabled_api_ready`、`canary_send_exit_code=2`。临时启用后的 API 未在门禁窗口内同时满足进程身份、`SMS_ENABLED=true`、`SMS_TEST_MODE=true` 和 `/api/ready=200`，因此 runner 在任何业务 POST 前停止；`sms_submission_requests=0`，五个场景均未提交，真实短信和供应商费用为 0。

### 启用态启动只读诊断候选静态验证

针对 `enabled_api_ready` 阻断新增默认关闭的只读诊断生成器与 3 项契约测试。ChangeId `20260806T015216Z`、runner SHA-256 `65e1aed60921cea057bbe63fbaf663bb705171f31fffcbc9ec48025db356c9f6` 已在本地生成。默认关闭、生成器 SelfTest、runner SelfTest、PowerShell 解析、内嵌 Bash `-n`、低敏字段白名单、固定 SSH 辅助脚本摘要、禁止写操作/服务信号/业务 POST/邮件/短信断言均通过。随后完整 readiness 与敏感扫描通过：`findings=0`、`sms_enable_literals=0`。本轮网络连接、配置修改、服务信号、服务重启、业务 POST、邮件和短信均为 0；测试服诊断尚未授权或执行。

一次性执行授权随后被消费，固定 SSH 连接 1 次且没有重试。结果文件 SHA-256 `6326a849d654e8cc21dfc0285850d74b85a4d92c628ec65834c98690402528ea` 显示：API 单进程和二进制身份、环境文件身份/权限、文件/进程短信配置一致性、关闭态 ready、Aliyun Provider、必需值、Endpoint、HMAC 和白名单均通过；`legacy_sms_keys_absent=false` 导致 `environment_file_sms_config_valid=false`、`enabled_startup_config_ready=false` 和退出码 3。执行期间配置修改、服务信号、服务重启、业务 POST、邮件、短信提交和真实短信全部为 0。依据 `server/internal/config/config.go`，任何旧键存在都会在短信启用时失败关闭，这与前次临时启用 API 退出一致；尚未核验具体旧键，且不得通过再次执行已消费诊断来获取。

### 旧短信环境键精确清理候选静态验证

ChangeId `20260806T021613Z`、runner SHA-256 `6979bf61a6d4352e9adb8d7540b335bd04fffb710e1435749f985990f4882117` 已离线生成。契约测试确认删除集合精确等于三个旧键，文件与进程候选同步变换，全部 Aliyun 新键保留，关闭态和测试模式不变；固定 SSH、摘要绑定、结果 `CreateNew`、排他锁、TERM/KILL 同 PID 复核、原环境双备份、失败自动恢复、Alertmanager discard 和 10 秒稳定性复核均存在。负载不包含 `SMS_ENABLED=true`、业务 SQL 写入、业务 POST、发送场景、完整手机号或 Bearer 输入。

全量阶段 5 离线契约执行 121 项通过、3 项跳过；PowerShell、Bash `-n`、默认关闭、runner 自测、readiness 与敏感扫描均通过，`findings=0`、`sms_enable_literals=0`。本轮网络连接、上传、配置修改、服务信号、服务重启、业务 POST、邮件和短信全部为 0。该证据不代表测试服旧键已经删除；实际连接、环境文件替换、API 停止/启动和自动回滚仍需绑定 ChangeId 与完整摘要的独立授权。

一次性执行随后成功完成，结果 SHA-256 `3564bd9259f819b4386e4867a5666118173c2c52b8754ff7833f3e28194a366d`、退出码 0。输出确认 `exact_legacy_keys_absent=true`、`aliyun_keys_preserved=true`、`file_process_sms_config_parity=true`、`current_closed_api_ready=true`、`closed_state_stability_verified=true` 和 `alertmanager_discard=true`；服务停止/启动各 1 次、配置修改 1 次。自动回滚保护已布防但未触发，恢复失败为 false，敏感恢复材料与排他锁均未保留。业务 POST、邮件、短信提交和真实短信均为 0。`remote_stderr_present=true` 仅作为布尔异常残余风险记录，正文按低敏契约未保存；不影响远端退出码 0 和全部强制成功字段，但不得据此跳过新的独立只读复核。

清理后复核候选 ChangeId `20260806T022804Z`、runner SHA-256 `1dd13c8e2caba052c46de7963d106a5515e5c28bfdac9e27ce940c869a233ffb` 已离线生成，默认关闭且结果文件不存在；生成阶段网络、配置修改、服务操作、业务 POST、邮件和短信均为 0。

一次性只读复核随后通过，结果 SHA-256 `eb666eb3520bac38bbfeafe8778a963b448b951c946d2c825043c67648889a43`、退出码 0、远端 stderr 为空。所有启用配置布尔门禁均为 true，包括 `legacy_sms_keys_absent=true`、`environment_file_sms_config_valid=true`、`file_process_sms_config_parity=true`、`current_closed_api_ready=true` 和 `enabled_startup_config_ready=true`。固定 SSH 连接 1 次，配置修改、服务操作、业务 POST、邮件和短信为 0。

真实 Canary ChangeId `20260806T040627Z` 随后按计划 SHA-256 `c3f47450080443754c1bc140750717a39affda61821badabbd5bc54c7ca4cc07`、runner SHA-256 `885d356587752c6ecf58cd34007b03c0fcf7fb7ef73b367059304ed96a117868` 的一次性授权执行。低敏结果 SHA-256 `51eb9596cdbe01fa2c60959495ac464431f359772284e5dd48942bf53898ec4d` 显示：`register`、`login` 的提交标志为 true；第三个 `reset_password` 触发失败门禁；`bind_phone`、`admin_verify` 未执行；业务提交请求 3、自动重试 0、退出码 2、远端 stderr 为空。失败路径完成自动关闭态恢复，服务停止/启动各 2 次。结果文件存在意味着本 ChangeId 已消费并禁止重跑。该摘要没有携带供应商受理字段、发送日志增量或人工收件结果，因此实际外发/费用只能记为 0–2 次待核验，不能把两个提交成功标志直接写成供应商受理或真实收件。

本地源码审计先排除了应用 IP 桶、跨场景 Redis 门禁和账号状态前置拒绝。随后获批执行 ChangeId `20260806T051625Z` 的单次固定 SSH 只读诊断，结果 SHA-256 `d8006dffbd18fe631ee5c652cd7f794472aefb4a34db619e42f9eb5ba86f638c`、退出码 0、远端 stderr 为空：发送日志从 13 增至 16，事件窗口 accepted 2、failed 1；`register` accepted 1、`login` accepted 1、`reset_password` failed 1，且失败安全分类精确命中供应商频率限制。事件验证码共 3 条且未消费 3 条。关闭态、测试模式、API ready、Alertmanager discard、恢复锁和材料清除全部通过。诊断自身配置修改、信号、重启、业务 POST、邮件、短信提交和真实短信均为 0。根因据此从推断提升为已确认；两条 accepted 仍只证明供应商受理，不证明手机收件。

基于已确认的同号码分钟级频控，计划契约新增 `same_target_min_interval_seconds=65` 与 `scheduled_waits=2` 两个强制字段；真实发送负载在 `login→reset_password`、`bind_phone→admin_verify` 之间分别等待 65 秒，并在等待期间持续核验启用进程存活，结束后重新通过 ready 门禁。等待不增加提交次数，也不是失败重试；五场景仍各一次、总计 5 次、自动重试 0。最终本地候选 ChangeId `20260806T053735Z`，计划 SHA-256 `2511648d46c6ef3395d6a8fab0e9400e400c2fe66bf0dde184f799a61efca625`，runner SHA-256 `39b4a84b8e4b9b009cfb05045fd859d5d889405ea44e18dada3139057dd5b7aa`。计划校验、PowerShell 解析、默认关闭、runner/载荷 SelfTest、Git Bash `bash -n`、五次精确调用、两个固定等待和结果文件不存在均通过；生成及验证没有联网、配置修改、业务 POST、邮件或短信。真实执行必须取得新的独立授权。

该候选随后按完整计划/runner 摘要的一次性授权执行，结果 SHA-256 `0ae03e57b796993f7b5418891720ea62601b9f6bd2a47605b7e06c2388cc29d9`、退出码 0、远端 stderr 为空。五个 `scene_*_submitted` 均为 true，`requested_sends=5`、`completed_scenes=5`、`sms_submission_requests=5`、`automatic_retries=0`；两个 65 秒窗口均完成。发送前游标为 send log ID 16、verification code ID 1751，绝对日志基线 16（accepted 15、failed 1），完成时间 `2026-08-06T05:57:50Z`。结束后 `SMS_ENABLED=false`、`SMS_TEST_MODE=true`，服务停止/启动各 2 次，敏感值持久化 0。该结果仅证明 API 五次提交成功与关闭态恢复，不直接证明五条供应商 accepted、OTP 未消费、监控无异常或手机收件。

原 runner 的成功主体与退出汇总重复输出三个完全相同的节奏字段。原始结果和摘要保持不可变；仓库解析器只对这三个已知字段允许一次同值重复，其他重复、第三次重复或不一致值仍失败关闭，生成器未来只由退出汇总输出一次。相关 postcheck、观察快照和离线组装契约测试通过。

事后只读候选 ChangeId `20260806T060018Z` 已绑定源计划、runner、结果三项完整摘要生成，runner SHA-256 `d547cd54e2d8d3ee917fa4c4716dc13cbb03c9d9047909184ed662706be5581d`。PowerShell 解析、默认关闭、SelfTest、Git Bash `bash -n`、只读事务、零业务 POST/服务信号和结果不存在均通过；本地生成没有连接测试服。实际只读核验仍需独立授权。

项目负责人同时人工确认两个自有号码的五场景均已收件，未提供或持久化手机号及 OTP。低敏确认文件绑定源 ChangeId 和确认时间，SHA-256 `6c9bda7862084567b78521921234e2c36afa0e143aa4b4dbc6ece2d19af0d61c`，UTF-8/LF/无 BOM，仅包含五个场景布尔值。

ChangeId `20260806T060018Z` 随后按一次性授权执行并消费，结果 SHA-256 `c9c56de2a0d0c368bd24244d14ea48f1146f7a5906487bc0d444da8c1a9b4d75`，在 `provider_metrics_shape` 阻断。控制流已先通过精确的五条日志、五条 accepted、五个独立场景、五条供应商受理字段完整、五条验证码及未消费/关联一致性数据库门禁，随后才读取当前进程 Provider 指标。阻断原因是指标为进程内计数，Canary 恢复关闭态重启 API 后归零，而旧候选错误要求当前值至少 5。执行自身配置、信号、重启、业务 POST、邮件和短信为 0，远端 stderr 为空；旧候选禁止重跑。

修正版事后候选 ChangeId `20260806T062059Z` 已按 runner SHA-256 `1c0291b2d3eb07b872a0aeae24bebf42e04c1f3a342fa844fc3b4eb67b3ca383` 的一次性只读授权执行通过，低敏结果 SHA-256 为 `5fd533d891772e57675721463f4c94f8f9952bce81ec05410908d81dc7ee421e`。核验确认：基线后短信日志 5 条、accepted 5 条、独立场景 5 个、供应商受理字段完整，验证码 5 条且全部未消费、日志与验证码关联完整；`SMS_ENABLED=false`、`SMS_TEST_MODE=true`、health/ready、双号码白名单、Alertmanager discard 均正常，活动 Alertmanager/SMS 告警均为 0、通知失败 0、恢复锁与材料已清除。恢复后当前进程 Provider 指标读取成功且计数为 0，这只说明重启后的进程内计数当前为零，五次历史受理由持久数据库证据证明。此次核验仅建立一次固定 SSH 连接，配置修改、服务操作、业务 POST、邮件、短信提交与真实短信均为 0，退出码 0、远端 stderr 为空。

观察快照 ChangeId `20260806T060345Z` 随后按 runner SHA-256 `9b89eab7bde8461d3002422f58672ce4adf2b318e0c4c961423d8f6139faa636` 的限定授权执行 5m、15m、30m 三个窗口，每个窗口各执行一次、各建立一次固定 SSH stdin 连接且没有重试。三个名称表示“Canary 完成后至少经过该时长”的门禁；实际分别在 1812、1821、1830 秒采集。三次均返回 health/ready 200、累计发送 `21/20/1`、当前进程 Provider 调用/非受理为 `0/0`、活动 SMS/Alertmanager 告警 0、通知失败增量 0，并且配置修改、服务信号/重启、业务 POST、邮件和短信均为 0。快照 SHA-256 依次为：5m `80a84638ba2412acf7eda15ee5ba9d2f5263a578353afe96da001ada0a27bf78`、15m `13ccd424716feaf8703e557522078711397552db3b4ff95a0549f50a68be9320`、30m `7833cc5252c3f560993f0b4f667357fbe8c080ebe4f2b1c62e6a8d8886ab4e8a`。2h 与 24h 窗口尚未到达或执行，禁止把前三个快照解释为完整观察通过。

本地完成前三个快照后审计发现，最终证据组装器还要求独立 `final_state` 文件，而原工具链没有受控生成路径。现已新增纯离线最终状态组装器：它必须同时绑定源 Canary 成功结果、修正版事后只读核验结果和 24h 快照的完整 SHA-256，并按两类结果字段白名单重新核验五场景成功/零重试、五次受理、OTP 未消费、关闭态、discard、零告警/通知失败，以及 24h 累计发送严格等于基线加 5；任何关闭态新增发送、计数回退、摘要篡改、未批准敏感字段、异常混合换行或工作区内输出都会失败关闭。输出仅包含组装器要求的七个最终低敏字段，不联网、不发送。新增 5 项契约通过，并已接入 readiness 与 PR CI；真实 `final_state` 仍必须等待 24h 快照后离线生成。

真实输入兼容复核确认：Canary 结果为 UTF-8/LF/无 BOM，修正版事后核验结果为 PowerShell 生成的 UTF-8/规范 CRLF/无 BOM，前三份快照为 UTF-8/LF/无 BOM。最终状态组装器已仅对低敏 `key=value` 结果入口兼容纯 LF 或规范 CRLF，并继续拒绝 BOM、NUL、裸 CR 和异常混合换行；JSON 入口仍严格限定 LF。契约夹具改为使用真实同类 CRLF 事后结果后通过，避免 24h 后出现格式误阻断。

同一审计还发现权威观察验证器曾错误要求关闭态快照的 Provider 进程内计数等于 Canary 前基线加 5；但关闭态恢复会重启 API，真实快照的当前进程计数从 0 重新开始。验证器现按模式区分：发送日志始终以持久数据库严格要求基线加 5；`closed_after_canary` 的 Provider 计数只要求非负、非受理不超过调用数且五个恢复后窗口不增长，支持首个快照为 0；`production_enabled` 仍要求相对基线增量与发送日志一致并保持单调。自测已覆盖进程指标归零可接受、关闭态 Provider 增长拒绝、生产延迟停止线与计数回退拒绝；前三份真实快照因此可被最终组装器按正确口径接收。

为让未满五档的观察也能重复验证，新增纯离线观察进度验证器。它复用最终组装器的摘要、敏感字段和快照结构门禁，只接受从 5m 开始的连续窗口前缀，核验时间严格递增、持久发送始终为 `21/20/1`、恢复后当前进程 Provider `0/0` 不增长、health/ready 200、零活动告警和零通知失败。3 项契约通过；使用源 Canary 结果及 5m/15m/30m 三份真实完整摘要执行得到 `phase5_observation_progress=passed`、`snapshots_verified=3`，网络连接和真实短信均为 0。

五窗口观察快照候选 ChangeId `20260806T060345Z` 也已绑定同一源结果生成，runner SHA-256 `9b89eab7bde8461d3002422f58672ce4adf2b318e0c4c961423d8f6139faa636`。同一 runner 覆盖 5m/15m/30m/2h/24h，每个窗口只允许创建一个结果，拒绝提前执行且内部不 sleep。PowerShell、默认关闭和 SelfTest 通过，候选目录仅含 runner；尚未连接测试服或生成任何窗口快照。

新增事后只读核验候选生成器及 3 项攻击/契约用例：默认关闭与 SelfTest 不联网；生成时强制绑定源计划、源 runner、源成功结果三项摘要并提取两个数字游标；远端负载只允许 `START TRANSACTION READ ONLY` 查询和本机 GET，精确要求五场景日志、供应商受理字段、五条未消费 OTP 与业务请求关联均完整，并核验关闭态、监控 discard/零活动告警/零通知失败及恢复锁清除。篡改源结果摘要会在本地生成阶段失败。该生成器后续已产出并执行修正版候选 `20260806T062059Z`，结果通过且完整证据见本报告前文；不得再把此段最初的离线实现时点解释为当前状态。

新增五档观察证据离线组装器及 3 项契约/篡改用例。组装器强制绑定源 Canary 成功结果、五场景人工收件确认、五个窗口快照及最终状态的完整摘要，只允许工作区外 UTF-8/LF/无 BOM 低敏文件；基线来自真实发送前固化的绝对计数，输出前复用权威观察验证器。正常夹具可生成并复核五档证据，篡改人工确认后摘要不匹配且不会创建输出。本轮没有联网、没有等待观察窗口、没有生成真实观察证据。

新增五窗口只读快照候选生成器及 3 项契约/篡改用例。单一摘要冻结 runner 覆盖 5m、15m、30m、2h、24h，各窗口只能产生一次结果；本地与远端双重拒绝提前执行，负载无内部 sleep，仅执行固定 SSH 单连接、只读事务和本机 GET。五个 Bash 负载语法通过，篡改源结果摘要被生成门禁拒绝。本轮没有源成功结果、测试服连接或真实快照。

失败路径已自动恢复关闭态：`automatic_closed_state_restore=true`、服务停止 1 次、启动 2 次、远端 stderr 为空。结果文件的存在同时撤销该 runner 的再次执行资格；ChangeId `20260805T200244Z` 和 runner SHA-256 `d7748b2d...6c9ec` 已消费，禁止重跑。当前低敏结果不能区分启用进程提前退出、二进制身份、开关/测试模式环境或 ready HTTP 哪一项失败；成功恢复路径按安全设计清除了临时启动日志。后续只能先生成并独立批准新的关闭态只读诊断，读取该执行时间窗的脱敏服务日志或 journald 分类；诊断完成前不得生成或执行新的真实发送候选。
