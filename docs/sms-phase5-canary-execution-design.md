# 短信阶段 5 测试服 Canary 执行设计

## 1. 当前结论

截至 2026-08-05，技术聚合预检和测试服实际回滚已经通过，产品负责人已选择“真实受理与收件 Canary”，但真实发送仍不能直接执行。
当前测试服白名单数量为 1，验收矩阵却同时要求：

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
五场景状态和五次提交计划经独立校验通过，手机号字面量与敏感字段均为 0。当前唯一合规结论更新为：**验收层级和脱敏计划已通过，
但双号码归属/状态、白名单准备、固定测试服只读状态预检和真实发送授权仍未完成；不得开启 `SMS_ENABLED`。**

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
6. 本地生成器已经补充失败输出顺序回归：未来 runner 必须先输出远端低敏结果和精确退出码，再失败关闭。修正版必须使用新 ChangeId，经独立授权后才能在仓库外生成和静态验证；生成授权仍不等于执行授权。
7. 只有新候选经独立批准执行且账号状态、管理员直接角色/权限、双目标白名单和关闭态均通过，才可另行批准五场景真实执行；任何本地生成、只读预检或白名单变更授权均不得继承为短信发送授权。

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

ChangeId `20260805T132831Z`、runner SHA-256 `4fc5c444...d8e9c` 的一次性执行门禁已经消费。其批准口令和 runner 均已撤销执行资格；禁止再次运行、禁止把失败视为白名单结论，也禁止据此修改测试服。

本次结果判定：

- `target_state_readonly_preflight=passed` 仅表示关闭态、未注册目标、合格直接管理员身份与权限、双目标白名单及发送日志零增量同时通过。
- `target_state_readonly_preflight=blocked` 或非零退出码表示本次只读核验停止；即使前面的布尔字段已经输出，也禁止自动重试。
- `target_new_whitelisted=false` 或 `target_admin_whitelisted=false` 只允许据此准备新的精确白名单变更候选，不能直接修改测试服。
- 任何输出都不能替代号码归属和持有人同意确认；不得粘贴完整终端输出中可能出现的意外敏感内容。

## 9. 修正版候选的新门禁

修正版只修复失败证据输出顺序，不改变固定 SSH 身份、隐藏输入、stdin 内存传递、只读 SQL、零上传、零业务 POST、零配置修改和零短信边界。必须先取得“生成新 ChangeId 修正版候选，仅本地生成与静态验证”的独立授权；生成后再按新 runner 完整 SHA-256 请求一次新的执行授权。任何旧 ChangeId、旧 SHA 或旧批准口令都不得复用。
