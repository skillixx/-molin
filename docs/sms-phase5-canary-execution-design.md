# 短信阶段 5 测试服 Canary 执行设计

## 1. 当前结论

截至 2026-08-05，技术聚合预检和测试服实际回滚已经通过，产品负责人已选择“真实受理与收件 Canary”，但真实发送仍不能直接执行。
最初测试服白名单数量为 1，验收矩阵却同时要求：

- `register` 使用尚未注册的手机号；
- `login`、`reset_password` 使用已经注册的手机号；
- `admin_verify` 使用已绑定手机号且拥有 `user:manage` 权限的管理员；
- `bind_phone` 使用登录用户的新手机号；
- 五场景都完成 OTP 单次消费。

同一个手机号不可能在同一窗口内同时满足“未注册”和“已注册管理员”两个前置状态。若强行消费全部 OTP，还会创建用户、
修改密码并吊销会话、修改手机号绑定和刷新管理员 MFA 时间戳。这些业务状态变更不在“最多 10 条真实短信”的发送授权中，
也不能由执行脚本自行推定为已批准。

产品负责人已经确认采用 `receipt_only`，不消费 OTP、不批准任何业务状态变化。新的脱敏计划候选 ChangeId
`20260805T132831Z` 已在仓库外本地生成，SHA-256 为
`633f4eeb1b855d9295d0b9fae8ed3d7dc47de3b33e577726c8ed21173301034b`；候选仅含 `target-new`、`target-admin` 两个别名，
五场景状态和五次提交计划经独立校验通过，手机号字面量与敏感字段均为 0。后续双号码状态只读核验确认唯一阻断为 target-admin 不在白名单；精确变更随后按一次性授权成功执行，白名单数量从 1 变为 2。当前合规结论更新为：**验收层级、脱敏计划、双号码状态和白名单受控变更已完成；仍须用新 ChangeId 独立只读复核双目标白名单，真实发送仍未授权，不得开启 `SMS_ENABLED`。**

## 2. 两层验收边界

### 2.1 推荐：真实受理与收件 Canary

本层只验证五个真实发码入口、五个模板、阿里云受理、手机人工收件、计数和自动恢复。它不消费 OTP，不创建或修改账号。
阶段 4 已用生产 Go 代码、隔离 MySQL/Redis 和并发测试覆盖五场景 OTP 单次消费、重放拒绝及业务状态规则；本层引用该证据，
但不得把阶段 4 自动化测试描述为测试服真实 OTP 消费。

至少需要两个经负责人确认归属和同意的白名单号码：一个保持未注册，用于 `register` 和 `bind_phone`；一个绑定到合格管理员测试账号，
用于 `login`、`reset_password`、`admin_verify`。`bind_phone` 服务会拒绝已注册手机号，因此不得把管理员已绑定号码用作换绑目标。
正式执行前仍需只读验证每个目标状态，
验证结果只输出布尔值和低敏别名，不输出完整手机号。

### 2.2 可选：五场景完整业务消费验收

本层会产生真实业务状态变化，必须另行批准一次性账号、邮箱验证码配合、密码变更与会话吊销、手机号换绑、管理员权限与 MFA、
数据恢复方式和恢复后的独立核验。注册号码与换绑新号码必须分离，不能借用生产用户或普通测试管理员。没有这组独立批准时，
执行器必须失败关闭。

## 3. 共同硬门禁

- 仅固定测试服；`SMS_TEST_MODE=true`；窗口结束无条件恢复 `SMS_ENABLED=false`。
- 固定五个场景各一次，计划提交数为 5；窗口硬上限不超过 10；任何请求都不重试。
- 任一 HTTP 非预期、供应商未受理、数据库增量不等于已完成场景数、Provider 指标异常、活动告警出现或恢复失败，立即停止。
- `accepted` 只记录阿里云受理；每一条真实收件都由负责人按场景逐项确认。
- 计划、日志、证据和提交中不保存完整手机号、OTP、Token、密码、AccessKey、供应商原始响应或可逆目标摘要。
- Alertmanager 在执行前后均保持既定安全路由；短信告警出现即停止，不用短信作为告警通知渠道。

## 4. 本地计划校验

以下入口仅检查脱敏计划文件，不连接网络、不修改配置、不发送短信：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File `
  scripts/verify-sms-phase5-canary-execution-plan.ps1 -SelfTest
```

负责人确认 `receipt_only` 后，可使用以下默认关闭的本地生成器创建一个全新脱敏候选目录：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File `
  scripts/prepare-sms-phase5-canary-execution-plan.ps1 `
  -Generate `
  -ChangeId <UTC_CHANGE_ID> `
  -AcceptanceScope receipt_only `
  -OutputDirectory <NEW_LOCAL_DIRECTORY>
```

无参数运行只输出未授权状态；`-SelfTest` 仅在系统临时目录创建并精确清理自测文件。`-Generate` 要求全新目录并禁止覆盖，
固定生成五场景、五次提交、两个 `target-*` 低敏别名，随后调用独立计划校验器。生成器不采集手机号、不联网、不上传、
不修改短信开关、不发送短信；候选生成授权不继承为上传、部署或真实发送授权。

实际计划文件只能保存 `target-*` 低敏别名和账号状态类别。手机号、Token、OTP 和密码只能在以后获得独立授权的交互执行窗口内
短暂进入内存，并由隐藏输入读取；不得通过命令行参数、环境文件、暂存目录或证据包持久化。

## 5. 下一步授权顺序

1. 已完成冻结 ChangeId `20260805T115540Z` 的测试服实际回滚与恢复演练；仅执行一次且独立验收通过，禁止重复执行。
2. 产品负责人已选择“真实收件 Canary”，明确不消费 OTP、不产生业务状态变化。
3. ChangeId `20260805T132831Z` 的脱敏候选已经本地生成并通过独立静态验证；生成授权已消费，候选不得覆盖或重建。
4. 双号码本地隐藏输入的格式与互异性预检已经通过；号码未输出、未持久化、未联网。
5. 原 runner SHA-256 `4fc5c444...d8e9c` 已按独立批准执行一次并返回退出码 2；禁止重试。该版本在检查退出码后才输出远端缓冲，导致实际布尔阻断原因丢失，只能记录为“固定测试服只读状态预检未通过、原因未确认”。
6. 本地生成器已经补充失败输出顺序回归：未来 runner 必须先输出远端低敏结果和精确退出码，再失败关闭。新 ChangeId `20260805T164138Z` 修正版计划和 runner 已按独立授权在仓库外生成并通过静态验证；其一次性执行随后也以退出码 2 失败关闭。低敏错误证明 SSH 参数重组把多行 Bash 负载压成一行，远端在首个 `then` 前发生语法错误，未进入 API、数据库、白名单或发送计数查询；这不是目标业务状态失败。
7. `20260805T164138Z` 与 runner SHA-256 `d00ff59a...7f34` 的执行授权已经消费，禁止重试；原候选目录已可恢复地移至带 `consumed-exit2-d00ff59a` 后缀的仓库外隔离路径。生成器现改为用进程标准输入传送 LF/无 BOM 完整脚本并由远端 `bash -s` 执行，号码 Base64 只保留在内存和 SSH stdin，不进入命令行或文件；必须使用全新 ChangeId 重新生成、静态验证并独立批准后才可再次执行。
8. 只有新候选按完整 SHA-256 取得独立批准并执行，且账号状态、管理员直接角色/权限、双目标白名单和关闭态均通过，才可另行批准五场景真实执行；任何本地生成、只读预检或白名单变更授权均不得继承为短信发送授权。
9. 2026-08-06 按新的本地生成授权创建 ChangeId `20260805T170528Z`：计划 SHA-256 为 `43b37bdb00ed954004324a3cc9fcfd50ce013d5b4517e6ae3715f5a0392b1a75`，runner SHA-256 为 `884ec7f681f8b1e0502c71efc31bc0aa2d97b459d10551875b6daeeb4dbac8c3`。随后按一次性授权执行并以退出码 3 失败关闭；唯一未通过项为 `target_admin_whitelisted=false`，证明传输、账号、手机号验证、直接管理员角色、`user:manage` 权限、target-new 白名单和发送日志零增量均已实际读取并通过。该候选已消费且移入带 `consumed-exit3-884ec7f6` 后缀的仓库外隔离路径，禁止重试。

## 6. 双号码本地交互只读预检候选

`scripts/prepare-sms-phase5-canary-target-preflight.ps1` 用于生成绑定脱敏计划 ChangeId 与 SHA-256 的本地 runner。生成器默认关闭，只有显式 `-ExportCandidate` 才会在全新的本地目录写入一个 runner；生成过程只执行 PowerShell 语法检查、默认关闭检查和合成号码自测，不进入真实交互分支。

runner 的 `-Interactive` 分支通过 `Read-Host -AsSecureString` 分别读取 `target-new` 与 `target-admin`，使用 BSTR 临时解包，并在 `finally` 中调用 `ZeroFreeBSTR`。号码只用于内存中的格式与互异性校验，不输出、不写盘、不传输。由于托管字符串无法保证物理清零，脚本仅缩短引用生命周期；号码归属、注册状态、管理员身份与白名单状态仍须后续获得独立授权后，在固定测试服执行只读预检确认。

本地静态生成示例：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File `
  scripts/prepare-sms-phase5-canary-target-preflight.ps1 `
  -ExportCandidate `
  -ChangeId <UTC_CHANGE_ID> `
  -PlanFile <LOCAL_PLAN_FILE> `
  -ExpectedPlanSHA256 <LOWERCASE_SHA256> `
  -OutputDirectory <NEW_LOCAL_DIRECTORY>
```

本入口不修改白名单、不连接测试服、不上传、不修改 `SMS_ENABLED`，也不发送短信。实际执行 `-Interactive` 必须另行批准；候选生成授权不继承为交互输入或任何远程操作授权。

## 7. 双号码固定测试服只读状态预检候选

`scripts/prepare-sms-phase5-canary-target-state-readonly.ps1` 生成绑定同一 ChangeId 与计划摘要的默认关闭 runner。候选冻结测试服地址 `8.130.9.163:10003`、SSH 用户 `pc` 和唯一 ED25519 指纹；执行前必须从本机普通 `known_hosts` 文件重新计算并核对指纹，禁止接受新主机密钥或回退其他算法。

runner 的默认入口与 `-SelfTest` 均不提示输入、不读取 `known_hosts`、不建立网络连接。只有后续取得独立执行授权并显式传入 `-ExecuteReadOnly` 与绑定 ChangeId 的批准口令后，才会隐藏输入两个号码并通过单次 SSH stdin 传入远端内存。远端负载只执行 `SELECT`：核验 `target-new` 未注册、`target-admin` 为启用且手机号已验证的直接 `admin` 角色账号、管理员角色拥有 `user:manage` 权限，并分别判断两个号码是否位于当前测试白名单；输出仅包含布尔值和计数，不包含原值、掩码或可关联哈希。

候选不包含上传、白名单修改、业务 POST、短信开关变更或短信发送路径；每个 runner 的生成与执行必须分别绑定 ChangeId 和 SHA-256 独立批准。

ChangeId `20260805T132831Z` 的最终本地候选位于仓库外目录，runner SHA-256 为
`4fc5c4442a5530f8b5cad83a7d92db68722ecc5972ebacf6791ffe1e305d8e9c`。PowerShell 语法、内嵌 Bash 语法、只读 SQL、默认关闭、合成值、自身摘要和敏感字面量检查均通过。2026-08-06 该 runner 获得一次性批准并执行，返回 `readonly_exit_code=2`；没有自动重试，执行后没有活动 SSH 子进程。旧 runner 在非零退出时吞掉远端缓冲，因此不能从当前证据区分 API 进程数量异常与远端格式校验失败；本地格式与互异性在建立 SSH 前已经通过，所以 API 进程数量异常只是较强推断，不是确认事实。该 SHA 的执行授权已消费，禁止再次运行。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File `
  scripts/prepare-sms-phase5-canary-target-state-readonly.ps1 `
  -ExportCandidate `
  -ChangeId <UTC_CHANGE_ID> `
  -PlanFile <LOCAL_PLAN_FILE> `
  -ExpectedPlanSHA256 <LOWERCASE_SHA256> `
  -OutputDirectory <NEW_LOCAL_DIRECTORY>
```

## 8. 已消费的固定测试服只读执行人工门禁

ChangeId `20260805T132831Z`、runner SHA-256 `4fc5c444...d8e9c` 的一次性执行门禁已经消费。其批准口令和 runner 均已撤销执行资格；原候选目录已可恢复地移至带 `consumed-exit2-4fc5c444` 后缀的仓库外隔离路径。禁止再次运行、禁止把失败视为白名单结论，也禁止据此修改测试服。

本次结果判定：

- `target_state_readonly_preflight=passed` 仅表示关闭态、未注册目标、合格直接管理员身份与权限、双目标白名单及发送日志零增量同时通过。
- `target_state_readonly_preflight=blocked` 或非零退出码表示本次只读核验停止；即使前面的布尔字段已经输出，也禁止自动重试。
- `target_new_whitelisted=false` 或 `target_admin_whitelisted=false` 只允许据此准备新的精确白名单变更候选，不能直接修改测试服。
- 任何输出都不能替代号码归属和持有人同意确认；不得粘贴完整终端输出中可能出现的意外敏感内容。

## 9. 修正版候选的新门禁

修正版只修复失败证据输出顺序，不改变固定 SSH 身份、隐藏输入、stdin 内存传递、只读 SQL、零上传、零业务 POST、零配置修改和零短信边界。必须先取得“生成新 ChangeId 修正版候选，仅本地生成与静态验证”的独立授权；生成后再按新 runner 完整 SHA-256 请求一次新的执行授权。任何旧 ChangeId、旧 SHA 或旧批准口令都不得复用。

2026-08-06 已生成新 ChangeId `20260805T164138Z`：计划 SHA-256 为
`9188dce74133797bb155ed9fc969be11ec64daf58921d2747a5fe1e8ecb6e126`，runner SHA-256 为
`d00ff59ab40d23b20fb350557cd436db0cd86641553dc76ea3afc7e868687f34`。独立验证确认计划仍为 `receipt_only`、五场景各一次、总量 5、零重试；PowerShell/Bash 语法、默认关闭、合成值、只读 SQL、固定身份契约、失败证据输出顺序和敏感字面量均通过。该候选随后按一次性批准执行，但因 SSH 参数传输丢失 Bash 换行而在首个 `then` 处语法失败，未进入状态查询，`real_sms_sent=0`。候选及批准均已消费；传输修复仅存在于生成器，尚未生成新的可执行候选。

传输修复后已使用全新 ChangeId `20260805T170528Z` 生成仓库外候选。计划仍固定为 `receipt_only`、五场景各一次、总量 5、零重试；计划 SHA-256 为
`43b37bdb00ed954004324a3cc9fcfd50ce013d5b4517e6ae3715f5a0392b1a75`，runner SHA-256 为
`884ec7f681f8b1e0502c71efc31bc0aa2d97b459d10551875b6daeeb4dbac8c3`。独立静态验证确认 PowerShell 解析错误 0、Bash `-n` 退出码 0、负载 125 个 LF/0 个 CR/无 BOM、只读 SQL 写操作 0、完整手机号字面量 0、旧 `eval`/`remoteCommand` 传输链不存在，并确认 stdin 底层字节写入、字节数组清零和 `bash -s` 均存在。生成和复验期间隐藏输入提示、网络连接、上传、业务 POST、配置修改、邮件和短信均为 0；执行门禁仍关闭。

该 runner 随后按完整 SHA-256 的一次性批准执行。固定 SSH stdin 连接成功，返回 `target_state_readonly_preflight=blocked`、`readonly_exit_code=3`；关闭态、测试模式、target-new 未注册、target-admin 已注册且手机号已验证、直接 admin 角色、`user:manage` 权限、target-new 白名单、白名单环境读取和发送日志零增量均为 true，仅 target-admin 未在当前白名单，导致 `whitelist_targets_ready=false` 与 `whitelist_verified=false`。执行期间业务配置修改、业务 POST、上传、短信提交请求和真实短信均为 0，远端 stderr 为空且没有重试。下一步只能先生成精确白名单变更与回滚候选并取得独立配置变更授权；不得复用本 runner 或把其他通过项解释为 Canary 发送授权。

## 9. target-admin 精确白名单变更与自动回滚候选

`scripts/prepare-sms-phase5-canary-whitelist-change.ps1` 仅在显式 `-ExportCandidate` 时向全新的本地目录导出一个默认关闭 runner。生成过程不提示输入、不读取 SSH 身份、不联网，也不修改测试服或本地 `infra/.env.test`。runner 固定测试服地址、端口、用户和 ED25519 指纹；未来只有取得绑定 ChangeId 与完整 SHA-256 的独立执行授权后，才允许隐藏输入两个自有手机号并通过 LF、无 BOM 的 SSH stdin 在内存中传递。

远端负载在任何业务配置写入前核验：API 单进程与二进制身份、`SMS_ENABLED=false`、`SMS_TEST_MODE=true`、文件和进程白名单都精确等于 target-new、target-new 未注册、target-admin 已注册且手机号已验证并具有直接 `admin` 与 `user:manage`、Alertmanager 根路由为 `discard`、活动告警为 0，以及发送日志、Provider 调用和通知计数基线。候选只把 `SMS_TEST_PHONE_WHITELIST` 从单个 target-new 改为 `target-new,target-admin`，拒绝任何已有额外条目、重复键、符号链接或权限异常。

变更前保存 `pc:600` 的环境备份和原进程 NUL 环境快照，使用 `pc:700` 原子目录排他锁，并在不可中断临界区内登记锁与 ChangeId 目录的持有状态。成功路径只允许停止/启动 API 各一次并稳定观察 10 秒；任一写入后失败或收到 INT/TERM/HUP 时自动恢复原环境文件和原进程环境。整个候选不包含上传、业务 POST、告警触发、邮件或短信发送路径；实际执行、服务信号与配置变更仍需新的独立人工批准。

本轮最终本地候选 ChangeId 为 `20260805T180909Z`，runner SHA-256 为 `d202e6f7f9ee23b63f7c9556dd2f9e2fca7ca846ef5e0c21cbfa06d7b60079f7`。PowerShell 解析、Bash `-n`、负载自测、只新增 target-admin、文件自动恢复、固定 SSH 身份、默认关闭、零外部 URL、零上传命令、零完整手机号字面量和零 `SMS_ENABLED=true` 负载均通过；SQL 只经 stdin 进入固定参数客户端，排他锁与 ChangeId 目录均使用原子创建并以不可中断临界区登记状态，同名 ChangeId 创建失败不会污染历史证据。生成与复验期间交互输入、网络连接、上传、配置修改、服务重启、邮件和短信均为 0。此前 `20260805T174747Z`、`20260805T175544Z`、`20260805T175907Z`、`20260805T180434Z` 候选因继续加固已用 superseded 后缀可恢复隔离，不得执行。该证据只证明本地候选可审计，不构成测试服执行授权。

该最终候选随后按绑定完整摘要的一次性授权执行成功，执行尝试 1、自动重试 0。低敏结果确认 target-new 保留、target-admin 新增、白名单数量 1→2，关闭态和测试模式保持，服务停止/启动各 1 次并稳定通过；发送日志、Provider 调用、Alertmanager 通知计数均零增量，活动告警 `0:0`，业务 POST、上传、短信提交请求和真实短信均为 0，未触发回滚。

变更执行授权消费后，已离线生成新 ChangeId `20260805T182328Z` 的 receipt-only 计划与固定测试服只读复核 runner。计划 SHA-256 为 `f84c96a61172d025909c5b3d15116f9f6cb67f7c056bf6d2e071234f6accda89`，runner SHA-256 为 `2a4225f6b7c77738226afb495c8596b9b04f80bf057d49a81532a0a90da8540f`。该 runner 只允许重新读取关闭态、账号/IAM、双目标白名单和发送计数，默认关闭且尚未执行；再次 SSH 连接必须取得新的独立授权。
