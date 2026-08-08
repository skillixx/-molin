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
五场景状态和五次提交计划经独立校验通过，手机号字面量与敏感字段均为 0。后续双号码状态只读核验确认唯一阻断为 target-admin 不在白名单；精确变更随后按一次性授权成功执行，白名单数量从 1 变为 2，新 ChangeId 的独立关闭态只读复核也已确认双目标均在白名单。当前合规结论更新为：**验收层级、脱敏计划、双号码状态、白名单受控变更及独立复核均已完成；真实发送仍未授权，不得开启 `SMS_ENABLED`。**

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

## 10. target-admin 精确白名单变更与自动回滚候选

`scripts/prepare-sms-phase5-canary-whitelist-change.ps1` 仅在显式 `-ExportCandidate` 时向全新的本地目录导出一个默认关闭 runner。生成过程不提示输入、不读取 SSH 身份、不联网，也不修改测试服或本地 `infra/.env.test`。runner 固定测试服地址、端口、用户和 ED25519 指纹；未来只有取得绑定 ChangeId 与完整 SHA-256 的独立执行授权后，才允许隐藏输入两个自有手机号并通过 LF、无 BOM 的 SSH stdin 在内存中传递。

远端负载在任何业务配置写入前核验：API 单进程与二进制身份、`SMS_ENABLED=false`、`SMS_TEST_MODE=true`、文件和进程白名单都精确等于 target-new、target-new 未注册、target-admin 已注册且手机号已验证并具有直接 `admin` 与 `user:manage`、Alertmanager 根路由为 `discard`、活动告警为 0，以及发送日志、Provider 调用和通知计数基线。候选只把 `SMS_TEST_PHONE_WHITELIST` 从单个 target-new 改为 `target-new,target-admin`，拒绝任何已有额外条目、重复键、符号链接或权限异常。

变更前保存 `pc:600` 的环境备份和原进程 NUL 环境快照，使用 `pc:700` 原子目录排他锁，并在不可中断临界区内登记锁与 ChangeId 目录的持有状态。成功路径只允许停止/启动 API 各一次并稳定观察 10 秒；任一写入后失败或收到 INT/TERM/HUP 时自动恢复原环境文件和原进程环境。整个候选不包含上传、业务 POST、告警触发、邮件或短信发送路径；实际执行、服务信号与配置变更仍需新的独立人工批准。

本轮最终本地候选 ChangeId 为 `20260805T180909Z`，runner SHA-256 为 `d202e6f7f9ee23b63f7c9556dd2f9e2fca7ca846ef5e0c21cbfa06d7b60079f7`。PowerShell 解析、Bash `-n`、负载自测、只新增 target-admin、文件自动恢复、固定 SSH 身份、默认关闭、零外部 URL、零上传命令、零完整手机号字面量和零 `SMS_ENABLED=true` 负载均通过；SQL 只经 stdin 进入固定参数客户端，排他锁与 ChangeId 目录均使用原子创建并以不可中断临界区登记状态，同名 ChangeId 创建失败不会污染历史证据。生成与复验期间交互输入、网络连接、上传、配置修改、服务重启、邮件和短信均为 0。此前 `20260805T174747Z`、`20260805T175544Z`、`20260805T175907Z`、`20260805T180434Z` 候选因继续加固已用 superseded 后缀可恢复隔离，不得执行。该证据只证明本地候选可审计，不构成测试服执行授权。

该最终候选随后按绑定完整摘要的一次性授权执行成功，执行尝试 1、自动重试 0。低敏结果确认 target-new 保留、target-admin 新增、白名单数量 1→2，关闭态和测试模式保持，服务停止/启动各 1 次并稳定通过；发送日志、Provider 调用、Alertmanager 通知计数均零增量，活动告警 `0:0`，业务 POST、上传、短信提交请求和真实短信均为 0，未触发回滚。

变更执行授权消费后，已离线生成新 ChangeId `20260805T182328Z` 的 receipt-only 计划与固定测试服只读复核 runner。计划 SHA-256 为 `f84c96a61172d025909c5b3d15116f9f6cb67f7c056bf6d2e071234f6accda89`，runner SHA-256 为 `2a4225f6b7c77738226afb495c8596b9b04f80bf057d49a81532a0a90da8540f`。该 runner 只允许重新读取关闭态、账号/IAM、双目标白名单和发送计数。

该 runner 已按一次性授权执行并消费，固定 SSH stdin 仅连接 1 次且没有自动重试。结果为 `target_state_readonly_preflight=passed`、`readonly_exit_code=0`：关闭态和测试模式保持，target-new 未注册，target-admin 已注册且手机号已验证，并具有直接 admin 角色和 `user:manage` 权限；两个目标均在当前白名单，发送日志零增量。业务配置修改、业务 POST、上传、短信提交请求、敏感值持久化和真实短信均为 0，远端 stderr 为空。白名单技术门禁据此通过；该证据不授权开启 `SMS_ENABLED`，也不证明阿里云受理或手机收件。

## 11. 五场景真实收件默认关闭候选

`scripts/prepare-sms-phase5-canary-send-candidate.ps1` 只在显式 `-ExportCandidate` 时生成绑定新 ChangeId、receipt-only 计划摘要和固定测试服 SSH 身份的 runner。生成器默认关闭，`-SelfTest` 与导出过程均不提示或读取手机号、Bearer Token，不联网、不上传、不修改配置、不重启服务、不发送邮件或短信。

runner 同样默认关闭，未来只有同时提供 `-Interactive` 和获批的完整 runner SHA-256 才会进入交互分支。两个自有手机号和管理员 Bearer Token 均使用隐藏输入，只在内存中转换并通过 LF、无 BOM 的 SSH stdin 传递；候选不保存、不输出这些值，也不把它们放入进程参数。远端执行前再次核对 API 单进程、文件与进程关闭态、测试模式、双目标白名单和 Alertmanager `discard` 路由。

受控窗口严格固定 `register`、`login`、`reset_password`、`bind_phone`、`admin_verify` 各提交一次，总计 5 次且没有重试分支。候选在写入前保存原环境文件和原进程 NUL 环境，使用排他锁，临时启用短信后启动同一二进制；成功或任一失败路径均恢复原环境文件及原进程环境，并核验 `SMS_ENABLED=false`、`SMS_TEST_MODE=true` 和 API ready。API 提交成功只记为等待人工收件确认，不等同于供应商最终投递或手机收件。实际执行仍必须另行批准 ChangeId、计划与 runner 完整摘要、两次停止/启动上限、5 次真实短信提交及供应商费用。

## 12. 首次执行失败与最终修正版

首次真实收件候选 ChangeId `20260805T191326Z` 按一次性精确授权启动且没有重试。后续事后只读核验 ChangeId `20260805T193505Z` 确认测试服已经保持 `SMS_ENABLED=false`、`SMS_TEST_MODE=true`、health/ready 正常、双目标白名单数量为 2、恢复锁已清除，但发送日志仍为基线 `13:13:0:0`，没有基线后五场景、供应商受理或关联 OTP 记录。因此首次 runner 的实际短信提交数为 0，不能记录为阿里云受理或手机收件。

确定性根因是首次 runner 的发送前 Alertmanager 门禁引用旧路径 `/home/pc/molin/infra/alertmanager/alertmanager.yml`，而固定测试服实际关闭态配置为 `/home/pc/molin-alertmanager-phase5/20260805T084215Z/alertmanager.closed.yml`。该门禁位于远程账号/IAM 绑定、排他锁、开关切换和五次提交之前，所以失败没有打开短信开关或产生发送记录。生成器现固定实际配置路径，同时核验 `molin-alertmanager-phase5-closed` 容器运行态和 `/-/ready` HTTP 200。

最终修正版已使用 ChangeId `20260805T200244Z` 离线生成；receipt-only 计划 SHA-256 为 `3d47f96d172f3fc976b5acdc20e65b64813669bbce64799b0abc9d587fa45045`，runner SHA-256 为 `d7748b2df0056b9fcd2775b464a0dafd622142809f30d723deeff4d8de96c9ec`。runner 只把预定义低敏字段保存到 `result-20260805T200244Z.txt`，拒绝任意新增字段名，使用 `CreateNew` 防止覆盖或重复执行，不保存手机号、Token、OTP、stderr 或自由文本。计划校验、摘要绑定、PowerShell、UTF-8 无 BOM Bash、默认关闭、runner/载荷 SelfTest、全量阶段 5 契约和敏感扫描均已通过；候选尚未连接测试服、尚未授权、尚未发送。更早的中间候选 `20260805T200101Z` 因结果字段名门禁不够严格已在执行前隔离，禁止使用。

项目负责人随后批准并执行该最终候选一次。结果文件成功落盘且 SHA-256 为 `ea58e017b7a47f48efaf1ed1e670b43ce6f0eec7c65afb727eb0294c8b00524f`：`failure_gate=enabled_api_ready`、`sms_submission_requests=0`、`automatic_retries=0`、`automatic_closed_state_restore=true`，服务停止 1 次、启动 2 次。即临时启用进程未在 20 秒门禁内满足完整 ready 条件，runner 未调用任何五场景业务 POST，随后恢复关闭态；真实短信仍为 0。该 ChangeId 与摘要的授权已经消费，结果文件会阻断再次进入交互分支。由于成功恢复时临时日志按既定清理策略删除，现有结果无法进一步区分进程退出、二进制身份、环境状态或 ready HTTP 失败；后续须先执行独立获批的关闭态只读诊断，不得直接重试真实发送。

## 13. 启用态启动失败的关闭态只读诊断候选

新增 `scripts/prepare-sms-phase5-enabled-startup-readonly-diagnostic.ps1`。生成器和 runner 均默认关闭；生成与 `-SelfTest` 不连接测试服。未来只有取得绑定完整 SHA-256 的独立授权后，runner 才允许通过固定 SSH stdin 连接一次。远端负载只读取当前关闭态 API 的唯一进程、二进制身份、`pc:600` 环境文件、进程环境与文件环境中短信键的内存视图，以及本机 `/api/ready`；不要求手机号、Bearer Token 或其他交互输入。

诊断按 `config.ValidateSMS` 的实际语义输出预定义布尔值：关闭态、测试模式、Aliyun Provider、旧键不存在、必需值存在、Endpoint 形状、HMAC 字节长度、白名单非空、短信键唯一、文件/进程配置一致和当前关闭态 ready。它不输出任何配置原值、长度、摘要、手机号、Token、OTP 或 stderr；不修改配置，不发送信号或重启，不调用业务 POST，不发送邮件或短信。ChangeId `20260806T015216Z` 的 runner SHA-256 为 `65e1aed60921cea057bbe63fbaf663bb705171f31fffcbc9ec48025db356c9f6`。PowerShell 语法、Bash `-n`、默认关闭、runner/负载 SelfTest、契约测试、readiness 与敏感扫描均通过；生成及复核阶段网络连接为 0。该证据仅证明候选可审计，尚未授权或执行测试服只读诊断。

该候选随后取得一次性授权并执行，结果 SHA-256 为 `6326a849d654e8cc21dfc0285850d74b85a4d92c628ec65834c98690402528ea`。唯一阻断项为 `legacy_sms_keys_absent=false`：进程与文件短信配置完全一致，其余 Provider、必需值、Endpoint、HMAC、白名单、文件身份/权限、二进制身份和关闭态 ready 均为 true。代码中的 `ValidateSMS` 在启用态发现 `SMS_ACCESS_KEY`、`SMS_ACCESS_SECRET`、`SMS_SIGN_NAME` 任一旧键存在即失败，因此该证据足以解释临时启用进程退出；它没有输出具体旧键或原值。执行只连接固定 SSH 一次，配置修改、信号、重启、业务 POST、邮件、短信和自动重试均为 0。原诊断授权已经消费，下一步必须使用新 ChangeId 准备旧键精确清理与自动回滚候选。

## 14. 旧短信环境键精确清理与自动回滚候选

新增 `scripts/prepare-sms-phase5-legacy-config-cleanup.ps1`。生成器与 runner 默认关闭；只有未来同时提供 `-ExecuteChange` 和获批的完整 runner SHA-256 才会连接固定测试服一次。远端前置门禁要求 API 单进程、二进制摘要、`pc:600` 环境文件、`SMS_ENABLED=false`、`SMS_TEST_MODE=true`、全部 Aliyun 新配置、白名单、文件/进程一致性、关闭态 ready 和 Alertmanager `discard` 同时通过，并要求三个旧键中至少一个存在且文件与进程的旧键集合完全相同。

候选只从环境文件和原进程 NUL 环境快照中删除 `SMS_ACCESS_KEY`、`SMS_ACCESS_SECRET`、`SMS_SIGN_NAME`，拒绝重复旧键、集合不一致、其他配置漂移或任何新 Aliyun 字段变化。写入前创建 `pc:700` 排他锁和 ChangeId 目录，保存 `pc:600` 原环境文件与原进程环境；失败路径自动恢复两者并重新启动关闭态 API。TERM 超时后只有再次核验同一 PID 和二进制摘要才允许 KILL。成功路径要求新进程 ready、旧键完全不存在、Aliyun 新配置保持、文件/进程一致、Alertmanager discard，并稳定运行 10 秒；成功或恢复后删除敏感环境副本，保留受控运行日志。

最终本地候选 ChangeId 为 `20260806T021613Z`，runner SHA-256 为 `6979bf61a6d4352e9adb8d7540b335bd04fffb710e1435749f985990f4882117`。PowerShell 语法、Bash `-n`、默认关闭、生成器/runner 自测、精确键集合、自动回滚、零业务 POST、零邮件/短信和敏感字段门禁均通过；阶段 5 全量离线契约为 121 项通过、3 项跳过，敏感扫描 `findings=0`、`sms_enable_literals=0`。生成和验证期间网络连接、上传、配置修改、服务信号/重启、业务 POST、邮件和短信均为 0。早期 ChangeId `20260806T021534Z` 因尚未加入 10 秒稳定性复核已在执行前隔离，禁止使用。

该最终候选随后按一次性精确授权执行成功，低敏结果 SHA-256 为 `3564bd9259f819b4386e4867a5666118173c2c52b8754ff7833f3e28194a366d`。三个旧键在文件与新进程环境中全部不存在，Aliyun 新配置保持且文件/进程一致，`SMS_ENABLED=false`、`SMS_TEST_MODE=true`、API ready、10 秒稳定性和 Alertmanager discard 均通过。成功路径停止/启动 API 各 1 次，配置修改 1 次；未进入回滚，恢复材料和排他锁均已清除，受控运行日志保留。业务 POST、邮件、短信提交和真实短信全部为 0。远端 stderr 存在低敏布尔标记但未持久化正文；远端退出码为 0 且所有强制门禁完整通过，后续只读复核仍需独立授权。

清理后只读复核候选已使用新 ChangeId `20260806T022804Z` 离线生成，runner SHA-256 为 `1dd13c8e2caba052c46de7963d106a5515e5c28bfdac9e27ce940c869a233ffb`。该候选只重新读取关闭态配置布尔值和 ready，不修改配置或服务；尚未连接测试服或取得执行授权。

该复核候选随后按一次性授权执行通过，结果 SHA-256 为 `eb666eb3520bac38bbfeafe8778a963b448b951c946d2c825043c67648889a43`。API 单进程与二进制、环境文件身份/权限、短信键唯一、关闭态、测试模式、Aliyun Provider、旧键不存在、必需值、Endpoint、HMAC、白名单、文件/进程一致和当前 ready 全部为 true；远端 stderr 为空、退出码 0。执行只建立一次固定 SSH 连接，配置修改、服务信号/重启、业务 POST、邮件、短信提交和真实短信均为 0。旧键残留根因据此完成独立关闭态复核。

在此基础上，最终待授权五场景真实收件 Canary 已使用 ChangeId `20260806T040627Z` 离线生成：receipt-only 计划 SHA-256 `c3f47450080443754c1bc140750717a39affda61821badabbd5bc54c7ca4cc07`，runner SHA-256 `885d356587752c6ecf58cd34007b03c0fcf7fb7ef73b367059304ed96a117868`。计划仍固定 register、login、reset_password、bind_phone、admin_verify 各一次，总计 5 次、零重试、OTP 不消费、业务状态不变；runner 在任何配置变更和真实提交前固化两项数据库最大 ID、发送日志绝对汇总、Provider 总调用/非受理计数，成功恢复关闭态后记录 UTC 完成时间，供独立事后核验与 5m/15m/30m/2h/24h 观察精确建立基线。计划绑定、PowerShell/Bash 语法、默认关闭、runner/负载自测、自动关闭态恢复和契约测试均通过；本地生成与验证未连接测试服、未打开短信开关、未产生短信。旧 ChangeId `20260806T034200Z`、`20260806T035209Z` 均未执行且已作废，真实执行仍需对本 ChangeId 的新完整人工授权。

事后只读核验生成器已同步加入仓库。它只有在源计划、源 runner 和成功低敏结果三个完整 SHA-256 均匹配，且结果明确证明五场景各一次、总计五次、零重试、已恢复关闭态并包含两项数字游标时，才允许生成独立 ChangeId 候选。未来候选仅通过固定 SSH stdin 连接一次，以只读事务核验游标后的五条发送日志、五条未消费验证码及其业务请求关联，同时读取 health/ready、双白名单数量、内部 Provider 指标、Alertmanager discard、活动告警、通知失败和恢复锁；不采集手机号、Token、OTP、HMAC 或供应商请求 ID，不执行配置修改、信号、重启、业务 POST、邮件或短信。该资产当前仅完成本地实现与静态验证，尚无源成功结果，因此没有生成或执行远端事后核验候选。

五档观察证据离线组装器也已加入仓库。组装器要求源 Canary 成功结果、逐场景人工收件确认、5m/15m/30m/2h/24h 五个快照和最终状态全部位于 Git 工作区外，并对七类输入逐一核对完整 SHA-256、源 ChangeId、精确字段集合、UTF-8/LF/无 BOM 与敏感字段禁令。输出采用 `closed_after_canary`/`receipt_only` 契约，基线直接取自 Canary 发送前固化值，五条收件必须逐项为 true；写出新文件前先调用权威观察验证器验证，禁止覆盖已有证据。本轮只有离线实现、自测和攻击夹具，没有人工确认、真实快照或 24 小时观察结论。

固定测试服五窗口只读快照候选生成器现已补齐。生成器只有在源 Canary 低敏结果完整摘要匹配、五场景成功、零重试、关闭态恢复及观察基线/UTC 完成时间齐全时才输出一个冻结 runner；同一 runner 仅接受 `5m|15m|30m|2h|24h`，每个窗口结果文件只能创建一次。runner 在本地和远端都校验最小经过时间，不包含 `sleep`，每次仅通过固定 SSH stdin 连接一次，使用只读事务和本机 GET 读取 health/ready、发送汇总、Provider 总量/非受理/平均耗时、活动短信告警、Alertmanager 活动告警及通知失败；要求短信仍关闭、测试模式保持、Alertmanager discard/ready，禁止配置、信号、重启、业务 POST、邮件和短信。本轮没有成功源结果，因此仅完成默认关闭、五负载 Bash 语法、自测与攻击夹具，没有生成真实窗口 runner 或快照。

## 15. 第三场景失败后的只读诊断门禁

ChangeId `20260806T040627Z` 已按一次性授权执行并消费。低敏结果 SHA-256 `51eb9596cdbe01fa2c60959495ac464431f359772284e5dd48942bf53898ec4d` 只证明 `register`、`login` 返回提交成功标志，第三个 `reset_password` 失败，业务提交请求总数为 3、自动重试为 0；`bind_phone`、`admin_verify` 没有执行。失败路径自动恢复关闭态，服务停止/启动各 2 次，远端 stderr 为空。该结果不含供应商受理字段、发送日志增量或人工收件确认，因此不能将前两个标志提升为供应商最终受理或手机收件，实际外发及费用按 0–2 次未知处理。原 runner 禁止重试。

新增 `scripts/prepare-sms-phase5-canary-failure-readonly-diagnostic.ps1`。生成器只接受与上述部分失败完全一致的字段集合、完整结果摘要、独立新 ChangeId 和发送前已验证日志总数 13。生成器与 runner 均默认关闭；本地导出不连接测试服。未来远端执行只允许固定 SSH stdin 单连接、数据库 `START TRANSACTION READ ONLY` 和本机 GET，读取当前关闭态/测试模式、API ready、Alertmanager discard/ready、恢复锁与材料、事件窗口的发送日志 accepted/failed 分布、`reset_password` 失败和供应商频率限制安全分类、验证码总数及未消费数。

诊断只输出预定义布尔值和计数，不读取或输出手机号、Token、OTP、供应商请求标识或自由文本；禁止配置修改、服务信号/重启、业务 POST、邮件与短信。最终候选 ChangeId `20260806T051625Z` 随后按新授权执行一次，结果 SHA-256 `d8006dffbd18fe631ee5c652cd7f794472aefb4a34db619e42f9eb5ba86f638c`：发送日志 13→16，accepted 2、failed 1；`register` 与 `login` 各 accepted 1，`reset_password` failed 1 且精确命中供应商频率限制；3 条验证码均未消费。关闭态、测试模式、ready、Alertmanager discard、恢复锁/材料清除全部通过，诊断自身副作用为 0。根因据此确认。

## 16. 供应商频控修正版五场景候选

计划契约新增两个强制低敏字段：`same_target_min_interval_seconds=65`、`scheduled_waits=2`。runner 保持既定顺序，在 `login` 后等待 65 秒再提交 `reset_password`，在 `bind_phone` 后再次等待 65 秒再提交 `admin_verify`；这样 target-admin 的三次提交以及 target-new 的两次提交都至少间隔一分钟。每个等待逐秒核验启用进程存活，结束后重新执行进程身份、开关、测试模式和 ready 门禁。固定等待不是失败重试，不增加请求预算；场景仍各一次、总计 5 次、自动重试 0。

最终本地候选 ChangeId `20260806T053735Z`，计划 SHA-256 `2511648d46c6ef3395d6a8fab0e9400e400c2fe66bf0dde184f799a61efca625`，runner SHA-256 `39b4a84b8e4b9b009cfb05045fd859d5d889405ea44e18dada3139057dd5b7aa`。计划公共校验、PowerShell 解析、默认关闭、runner/载荷 SelfTest、Git Bash `bash -n`、五次精确调用、两个 65 秒等待、零自动重试和结果不存在均通过；生成与验证未联网、未修改测试服、未发送邮件或短信。真实执行必须以新 ChangeId、两项完整摘要、约 130 秒受控等待和最多 5 次供应商费用重新取得独立授权。

该候选随后按新授权执行成功。低敏结果 SHA-256 `0ae03e57b796993f7b5418891720ea62601b9f6bd2a47605b7e06c2388cc29d9`：五个场景提交标志均为 true，完成 5 次提交、2 次固定等待、自动重试 0；结束恢复关闭态和测试模式，服务停止/启动各 2 次，远端 stderr 为空。发送前日志/验证码游标为 16/1751，完成 UTC 时间为 `2026-08-06T05:57:50Z`。结果状态固定为等待人工收件确认，不能直接提升为供应商五次 accepted 或手机收件。

结果中三个节奏字段因成功主体与退出汇总各输出一次而出现一次同值重复。证据原文件和摘要不修改；读取器仅允许这三个字段最多一次同值重复，其他字段重复、同字段第三次出现或值不一致均阻断。生成器已移除成功主体的重复输出，未来结果只保留退出汇总一次。

独立事后只读候选 ChangeId `20260806T060018Z` 已绑定源计划、runner 和结果完整摘要生成；runner SHA-256 `d547cd54e2d8d3ee917fa4c4716dc13cbb03c9d9047909184ed662706be5581d`。它将以源游标精确核验五场景发送日志、供应商受理字段、五条未消费 OTP、Provider 指标、Alertmanager discard、活动告警、通知失败和恢复锁；只读且不发送。候选已通过本地静态验证，远端执行需要独立批准。

项目负责人已人工确认五场景全部实际收件；结构化低敏确认 SHA-256 为 `6c9bda7862084567b78521921234e2c36afa0e143aa4b4dbc6ece2d19af0d61c`，不包含手机号或 OTP。

原事后候选执行一次后在 `provider_metrics_shape` 阻断，结果 SHA-256 `c9c56de2a0d0c368bd24244d14ea48f1146f7a5906487bc0d444da8c1a9b4d75`。由于五场景数据库 SQL 门禁位于该指标门禁之前，控制流已经证明五条日志、五条 accepted、供应商受理字段、五条未消费 OTP 与日志关联全部通过。当前进程指标在关闭态恢复重启后为 0，不能要求大于等于 5；它只适合证明指标端点可读。修正版 ChangeId `20260806T062059Z`、runner SHA-256 `1c0291b2d3eb07b872a0aeae24bebf42e04c1f3a342fa844fc3b4eb67b3ca383` 保留持久数据库强断言，只把当前进程指标门禁改为非负整数，并输出当前值。

该修正版随后按一次性授权执行通过，低敏结果 SHA-256 `5fd533d891772e57675721463f4c94f8f9952bce81ec05410908d81dc7ee421e`。结果确认五场景日志、供应商受理字段和五条未消费 OTP 全部完整，系统保持 `SMS_ENABLED=false`、`SMS_TEST_MODE=true`，health/ready、双白名单、Alertmanager discard、零活动告警、零通知失败及恢复锁/材料清除均通过；当前恢复后进程 Provider 计数可读且为 0。执行仅建立一次固定 SSH 连接，没有配置修改、服务操作、业务 POST、邮件、短信或自动重试。真实收件 Canary 的发送与事后门禁至此闭环，后续仍需按独立授权采集五档只读观察快照。

固定观察 runner 随后取得 5m、15m、30m、2h 四个窗口的限定授权并按顺序各执行一次。由于窗口语义是最小经过时间而不是精确时点，四次实际采集时距 Canary 完成分别为 1812、1821、1830、13965 秒；每个窗口均只建立一次固定 SSH stdin 连接。四份快照一致显示 health/ready 200、累计发送 `21/20/1`、当前进程 Provider `0/0`、活动短信和 Alertmanager 告警 0、通知失败增量 0，所有副作用计数均为 0。完整快照 SHA-256 为 5m `80a84638ba2412acf7eda15ee5ba9d2f5263a578353afe96da001ada0a27bf78`、15m `13ccd424716feaf8703e557522078711397552db3b4ff95a0549f50a68be9320`、30m `7833cc5252c3f560993f0b4f667357fbe8c080ebe4f2b1c62e6a8d8886ab4e8a`、2h `1c14fed55aba8fdbb5b125baf30c69826c988a180d475de37b30a75c6feb3cc6`。同一 runner 仅余 24h 快照仍需在最小时间到达后取得授权执行，完成前不得组装最终五档观察证据。

为闭合最终状态来源，新增 `assemble-sms-phase5-final-state.py` 纯离线组装器。它不从人为常量单独生成结论，而是同时绑定源 Canary 结果、修正版事后核验结果与 24h 快照的完整摘要，要求五场景成功且零重试、持久日志五次受理、OTP 全部未消费、关闭态和 discard 通过，并要求 24h 发送总数/accepted 精确等于基线加 5、failed 不增长、告警和通知失败为 0。输入还必须符合 Canary/事后核验字段白名单，禁止夹带手机号等额外字段。只有全部成立时才创建工作区外 `final_state`，供最终五档组装器再次使用权威验证器校验；自测、成功夹具、关闭态发送增长、混合换行和未批准敏感字段攻击用例共 5 项通过。

权威观察验证器按运行模式解释 Provider 指标。测试服 `closed_after_canary` 在恢复关闭态时重启 API，因此 Provider 指标只代表恢复后的当前进程，首窗口允许为 0，但后续窗口必须保持不增长；五次历史供应商受理继续由持久发送日志和独立事后核验证明。生产 `production_enabled` 不使用该重置例外，Provider 增量仍必须与发送日志增量一致。该区分防止把进程重启误判为计数回退，也防止关闭态新 Provider 调用被忽略。

新增 `verify-sms-phase5-observation-progress.py` 为观察尚未满五档时提供纯离线连续前缀验证。它不填充或推断未来窗口，只接受已有快照的完整摘要并复用最终组装格式；当前源 Canary 与 5m、15m、30m、2h 四份真实快照已验证通过。24h 仍必须在时间到达后执行真实只读快照，进度验证不能替代它。

五窗口观察 runner 也已使用独立 ChangeId `20260806T060345Z` 生成，SHA-256 `9b89eab7bde8461d3002422f58672ce4adf2b318e0c4c961423d8f6139faa636`。它绑定同一源结果和 `2026-08-06T05:57:50Z` 完成时间，各窗口在本地与远端双重校验最小经过时间，不包含 sleep 或自动重试；5m、15m、30m、2h 已各执行一次并通过，仅余不得早于北京时间 `2026-08-07 13:57:50` 的 24h 窗口。
