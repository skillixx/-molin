# DirectMail Phase 2 可执行验收资产

## Phase 4 RBAC 替代账号浏览器验收

当原四账号登录输入已安全销毁时，只能在获得专项授权后使用 replacement 状态机。安全准备器仅交互读取当前有效且已完成双 MFA 的管理员短期 Access Token；替代账号专项不要求页面内存中的 Refresh Token，清理时按 D-92 仅吊销该 Access Token。四组唯一合成身份和随机强密码由程序在内存生成，并复用既有上传器写入固定 600 输入文件，终端不显示任何敏感值：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File tests/email/prepare-rbac-replacement-input-secure.ps1 -SelfTest

powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File tests/email/prepare-rbac-replacement-input-secure.ps1
```

`rbac_replacement_executor.py` 只读列出现有 `qa_email_rbac_` 角色，并按 `user:manage` 与对应邮件权限的精确集合确认 `view`、`view+manage`、`view+sync`、`view+test` 各自唯一匹配。它不会创建、修改或删除角色。替代账号通过管理 API 创建并只绑定冻结角色；短时调试回码必须与 Mock 邮件适配器同时启用，手机和邮箱验证码仅在内存消费。

`rbac_replacement_browser_qa.py` 从 stdin 接收四个短期 Access Token，在 1440、768、390 三种宽度检查邮件管理页面权限条、模板开关、测试发送、立即同步、新增邮箱等操作的权限降级、页面可读性和横向溢出，并生成 12 张固定名称截图。它只点击只读页签，不点击写按钮或开关、不提交表单，并监听全部网络请求，出现 GET/HEAD/OPTIONS 以外的方法立即失败。真实运行要求测试服务器已离线准备 Python Playwright 与 Chromium；缺失时固定失败，禁止联网安装。

若完整四角色验收仅剩 `view+test` 未形成证据，获得单独授权后可使用 `rbac_replacement_executor.py --view-test-only`。该模式仍读取相同的 600 安全输入 schema，但只匹配 `view+test` 唯一角色、只创建一个替代账号、只向浏览器传递一个短期会话，并只生成 `view_test-1440.png`、`view_test-768.png`、`view_test-390.png` 三张证据；finally 顺序固定为替代账号 logout、禁用账号、操作管理员 logout。

```powershell
python -B tests/email/rbac_replacement_browser_qa.py --self-test
python -B tests/email/rbac_replacement_executor.py --self-test
```

真实执行无论成功或失败都会先恢复原环境并重启，仅通过磁盘与进程环境、health、ready 只读门禁确认调试回码和 bootstrap 已关闭；恢复验证禁止调用任何发码接口。随后通过管理 API 禁用本轮创建的四个替代账号并吊销四账号及操作管理员会话。只有恢复、禁用和吊销全部成功才删除回滚点。脚本不删除替代账号，不修改旧账号，不访问数据库，不执行 migration、Bootstrap、Git，也不发送真实邮件或短信。

## Phase 4 RBAC 安全输入准备

先执行完全离线自测。自测使用进程内假传输验证固定 SSH 参数、stdin JSON schema、重复目标拒绝、远端失败分类和敏感输出拒绝；不会读取真实凭据、联网或写远端：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File tests/email/prepare-rbac-phase4-input-secure.ps1 `
  -SelfTest
```

自测通过后，由获批操作员在受控 Windows 终端运行真实模式：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File tests/email/prepare-rbac-phase4-input-secure.ps1
```

脚本只通过 `Read-Host -AsSecureString` 交互读取操作管理员短期 Access/Refresh Token，以及 `view`、`view_manage`、`view_sync`、`view_test` 四组隔离账号的邮箱、手机号和初始密码。敏感值不进入参数、环境变量或日志；Access Token 只检查 JWT 三段格式，不解码或输出；四组邮箱、手机号和密码必须分别唯一，密码必须为 12–72 位并同时包含大小写字母、数字和特殊字符。固定 JSON schema 只包含 `admin_session` 和 `accounts`，禁止 OTP/验证码字段。

真实模式只允许使用 Windows 系统目录中的 `OpenSSH\ssh.exe`，固定连接测试服务器 SSH key 会话，启用 `StrictHostKeyChecking=yes` 和 `BatchMode=yes`。JSON 通过 stdin 字节流写入固定运行时文件；远端使用 `umask 077`、父目录 700、noclobber 禁止覆盖，并复核目标为当前用户所有的普通非符号链接文件、mode 600 和合理大小。若创建后任一检查失败，只精确删除本次刚创建的不完整目标文件，不删除目录或其他文件；目标已存在时直接失败，输入文件成功后保留等待 RBAC 执行和人工确认。终端只输出固定布尔摘要，不输出 Token、邮箱、手机号、密码、路径随机段或远端错误原文。脚本退出前会清零 BSTR、JSON 字节数组并释放明文变量引用。

## Phase 4 RBAC 一次性状态机执行器

`rbac_phase4_executor.py` 只能在测试服务器本机运行，真实模式固定读取 `/home/pc/molin-runtime/email-rbac-phase4-input.json`，固定访问 `http://127.0.0.1:8080`。先运行完全离线的状态机自测：

```powershell
python -B tests/email/rbac_phase4_executor.py --self-test
```

自测通过只证明进程内成功路径和阶段失败注入均会进入环境恢复及会话吊销，不代表真实后端通过。真实模式执行前强制检查：输入文件为当前用户所有的普通 600 文件；`APP_ENV=test`；API 进程恰好一个且健康；`EMAIL_DEBUG_RETURN_CODE=false`；`EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED=false`；固定回滚文件不存在。任一门禁失败都不会进入管理 API 写入。

执行器先以 600 权限保存原 `infra/.env.test` 回滚点，再短时设置 `EMAIL_DEBUG_RETURN_CODE=true` 和 `EMAIL_ADAPTER=mock` 并重启唯一 API。Mock 是禁止真实邮件外呼的强制安全条件，不能作为真实投递证据。四个隔离角色都包含管理员 MFA 入口所需的 `user:manage`，并分别包含 `view`、`view+manage`、`view+sync`、`view+test` 对应邮件权限；随后通过管理 API 创建四个账号、仅在内存消费手机和邮箱调试验证码，并复用冻结的 4×12 无副作用权限矩阵。

无论中间阶段成功或失败，`finally` 都会先恢复原环境并重启，再通过手机发码响应确认不再包含明文 `code`，最后退出四个隔离账号及操作管理员会话。角色和账号作为隔离测试资产保留，不自动删除；部分失败也不会模糊删除已创建对象。只有环境恢复、明文关闭验证和全部会话吊销都成功后，才删除临时回滚点。终端只输出固定阶段、分类和计数，不输出 Token、验证码、邮箱、手机号、密码、角色随机后缀或原始异常。

真实执行必须先由授权人员在受控 Windows 终端运行组合生成模式；该模式只提示短期管理员 Access/Refresh，不显示生成的四组身份或密码：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File tests/email/prepare-rbac-phase4-input-secure.ps1 `
  -GenerateTestIdentities -GeneratePasswords
```

确认安全上传器成功创建固定 600 输入文件后，再在测试服务器项目目录运行：

```bash
python3 -B tests/email/rbac_phase4_executor.py
```

2026-07-29 测试环境 RBAC 专项固定证据：一次性执行器通过管理 API 创建并保留四个隔离角色和四个隔离账号，权限查询返回 29 项，四账号完成手机与邮箱双 MFA；固定无副作用矩阵 48/48 通过，四类角色分别覆盖 `view`、`view+manage`、`view+sync`、`view+test`。该 MFA 使用短时调试回码和显式非生产 Mock 邮件适配器，只证明验证码消费、管理员 MFA、平台 RBAC 和接口门禁路径，不证明 DirectMail 供应商受理、真实收件或生产可用。

执行器 `finally` 已恢复原环境并重启唯一 API；恢复后磁盘及运行进程的调试回码、bootstrap 均为关闭状态，health、ready 均为 200，响应不再包含明文验证码。四个隔离账号会话及操作管理员会话共 5/5 退出成功；对操作管理员原 Access/Refresh 的独立复核均返回 401。角色和账号按验收要求保留；远端固定 600 输入材料已在获得单独授权后通过普通文件、非符号链接、当前用户属主和 600 权限门禁精确删除，并只读确认不存在。全过程未执行真实邮件或短信、数据库直写、migration、Bootstrap、Git 或生产操作，证据中不记录用户 ID、角色 ID、邮箱、手机号、Token、OTP、文件路径或随机后缀。

## 本地 OTP 与模板测试发送 Mock 矩阵

`in_memory_email_matrix.py` 是完全离线、进程内的冻结状态机验收模型，覆盖五个场景的 accepted、供应商拒绝、超时 unknown、不可消费、成功后单次消费、重放拒绝、严格过期边界、冷却和供应商调用次数；同时覆盖模板测试发送的同 key accepted 重放、八并发单外呼、unknown 墓碑、新 key 冷却、冷却到期、白名单 active/revoked/missing 以及前置失败零副作用。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File tests/email/run-local-email-matrix.ps1
```

该 runner 先运行 Python Mock 矩阵，再运行 auth 模块相关 Go 测试。若本机没有 Go 工具链，会固定输出 `go_tests=blocked` 和 `reason=go_toolchain_unavailable` 并退出 2；禁止联网安装、静默跳过或把 Mock 通过冒充为生产 Go 通过。Mock 矩阵不访问网络、测试服务器、数据库、外部 Redis、真实邮件或文件系统业务状态，也不证明生产实现、真实并发、供应商受理或最终送达。

截至本轮静态审计，生产 Go 测试已覆盖五场景发送成功，但供应商拒绝/超时只逐业务验证了 register；accepted 消费 SQL 有通用门禁，业务层成功消费、并发单次消费和重放拒绝只有 login。register、reset_password、bind_email、admin_verify 仍缺少各自成功业务效果、单次消费、重放和过期的生产测试证据。模板测试发送已有 unknown 墓碑、冷却和外呼前丢锁覆盖，但仍缺 accepted 同 key 重放、真实并发同 key 单外呼、白名单三态及统一失败零副作用的生产 Go 测试。上述缺口不得因本地模型通过而关闭。

## 管理端浏览器短期会话安全注入器

`inject-admin-browser-session-secure.ps1` 仅面向已经完成人工双 MFA 的短期管理员 Access Token。脚本固定使用 `C:\Users\skillixx\.codex\skills\gstack\browse\dist\browse.exe` 和固定 `BROWSE_SERVER_SCRIPT`，先通过不含机密的 `browse chain` 查询当前 URL；只有 origin 精确为 `http://8.130.9.163:3001` 才调用 `Read-Host -AsSecureString`。Token 只校验 JWT 三段 Base64URL 形态，不解码、不写参数、环境变量、文件或日志；注入 JSON 只经 stdin 发送，固定执行 `storage set access_token`、`reload`、`url`。子进程 stdout/stderr 全部在内存捕获，若包含原 Token 立即固定失败且绝不回显原文；BSTR 与字符数组在 `finally` 中尽力清零。

先运行完全离线自测；自测仅启动当前 PowerShell 脚本的 FakeChild，不启动 browse、不启动浏览器、不访问网络或 API：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File tests/email/inject-admin-browser-session-secure.ps1 -SelfTest
```

自测必须固定输出 `selftest=true`、`argv_exposed=false`、`output_exposed=false`、`file_exposed=false`、`network=false`。真实模式成功后只输出 `injected=true` 和 `token_exposed=false`；失败只输出固定分类，不输出浏览器原始响应或 Token。本仓库测试资产不保存真实 Token，且不得把真实模式命令包装为携带 Token 的参数或管道文本。

## admin_verify 模板安全单次初始化

该运行器只用于已经审批的一次性维护窗口：调用固定的 `POST /api/internal/email/bootstrap/admin-verify` 一次，不重试、不跟随重定向，也不会配置环境变量、SSH、重启服务、查询数据库或发送邮件。脚本不会开启邮件写入开关，bootstrap 服务端开关与一次性 token 必须由获批的独立流程准备并在完成后回收。

先执行完全离线的 AST 与假传输自测：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File tests/email/bootstrap_secure_runner_selftest.ps1
```

若从其他工作目录调用，可显式传入不含任何机密的 runner 本地路径；JWT、bootstrap token 与幂等键仍禁止进入命令行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass `
  -File tests/email/bootstrap_secure_runner_selftest.ps1 `
  -RunnerPath "<仓库目录>\scripts\run-email-admin-verify-bootstrap-secure.ps1"
```

只有自测通过且维护审批明确后，才可由操作员执行真实模式。远端 API 基址只允许 HTTPS；HTTP 只允许 `localhost`、`127.0.0.0/8` 或 `::1` 回环地址。API 基址和模板平台编号必须显式传入；管理员 JWT 与 bootstrap token 只能在提示后安全输入，禁止写入命令行、环境变量、脚本、日志或证据文件：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass `
  -File scripts/run-email-admin-verify-bootstrap-secure.ps1 `
  -ApiBase "https://<受控API地址>" `
  -TemplateId "<1至64位非全零ASCII数字>"
```

若测试服务器只暴露 HTTP 8080，必须先由操作员另开终端建立 SSH 本地端口转发，再让运行器访问本机回环地址：

```powershell
ssh -N -L 18080:127.0.0.1:8080 -p <SSH端口> <SSH用户>@<测试服务器>

powershell -NoProfile -ExecutionPolicy Bypass `
  -File scripts/run-email-admin-verify-bootstrap-secure.ps1 `
  -ApiBase "http://127.0.0.1:18080" `
  -TemplateId "<1至64位非全零ASCII数字>"
```

SSH 命令只建立本地传输通道，不得携带 JWT、bootstrap token 或幂等键。运行器会在本机终端通过安全提示读取机密，禁止将机密通过明文远端 HTTP、SSH 命令参数或远端环境变量传输。

运行器会先要求逐字确认“本次最多一次 HTTP 调用且无自动重试”，随后使用 `Read-Host -AsSecureString` 分别读取两项机密。幂等键由 CSPRNG 在内存生成且不输出。发送请求前会启动唯一的 15 秒总期限；等待响应头、取得响应流以及每次异步读取正文都共享同一个取消令牌，正文分块变慢不会重新获得 15 秒。响应流最多读取 4096 字节；读取到第 4097 字节立即失败关闭。成功只接受 HTTP 200 以及 Go JSON encoder 的冻结字节契约 `{"code":0,"message":"ok","data":{"scene":"admin_verify","configured":true,"idempotent":false}}`，末尾可有且仅可有一个 LF。BOM、CR、其他空白、字段重排、重复键和额外字段全部拒绝，避免 Windows PowerShell 5.1 的 JSON 重复键覆盖行为。终端仅输出固定的 `status/http_status/code`，不会输出原始响应、JWT、token、幂等键、完整邮箱或供应商原文。

本目录只提供测试与证据模板，不包含真实 Token、邮箱、OTP、Redis/MySQL/DirectMail 凭据，也不修改业务数据结构。自动化结果只有 `PASS`、`FAIL`、`SKIP`；API 不可达时为 `BLOCKED`（退出码 2）。`SKIP/BLOCKED` 不能计为通过。

## 000056 migration 离线契约检查

新增 migration 后先运行不连接数据库的静态检查：

```powershell
python -B tests/email/migration_000056_contract.py
```

该脚本会移除 SQL 行注释后检查关键 DDL/DML、严格 receipt 门禁、ownership 精确删除顺序和禁止的模糊写法，并输出 up/down SHA256。输出固定包含 `mode=static`、`remote_access=false` 和 `db_scenarios=not_run`，只证明离线契约结构通过；不能替代 MySQL 8 的真实 up/down、admin 预存组合、元数据冲突、partial 故障注入、未知引用、成功 receipt 阻断和 `schema_migrations dirty=0` 验收。

## 000057 容器隔离周期离线检查

在复制脚本到受控容器或连接数据库前，先运行纯本地契约检查：

```powershell
python -B tests/email/run_000057_container_cycle_contract.py
```

该检查执行 `bash -n`、核对当前 Up/Down 文件 SHA-256、确认目标 schema 只由一次 `/proc/sys/kernel/random/uuid`
生成并按 `molin_restore_57_reverify_<32位小写十六进制>` 校验后立即冻结，同时扫描旧 dirty1 目标名、源库写入、
schema 删除、目录清理、强制修复及其他危险命令。旧目标
`molin_restore_57_reverify_8fb6f25611b25d07a563f15105d0906a` 只允许作为禁止复用常量存在，不得查询、修改或清理；
脚本输出不得包含本轮随机目标名。当前执行脚本 SHA-256 为
`6C43A8E82440A7A939FDD66EA375EB56BF4BD8DEFE1A6F0ADD1F1FF0EA9290F8`，冻结 Down SHA-256 为
`EE05D166EB874D34A14A0D12FC17EE083CAC28DAFEEAC3772A8C14A4945495BB`。
容器工作目录精确冻结为只读预检确认不存在的
`/root/molin-000057-container-cycle-3263e5469732436c910dd22f894d647b`；已存在的旧目录
`/root/molin-000057-container-cycle` 不得作为 `work_dir`，新候选只允许在脚本的 readonly 定义处出现一次，不能被参数或环境覆盖。

该离线检查不连接数据库、不执行 migration，也不证明真实容器中的源库、目标不存在、备份恢复、数据快照或
Up→Down→Up 已通过；这些门禁只能在后续单独授权的隔离执行中验证。

## Phase 4 数据库备份与隔离恢复门禁

发布前使用 MySQL Shell 8.4 对测试 MySQL 做单 schema 完整 checksum 逻辑备份，并在本轮随机、固定前缀的隔离 schema
执行 dry-run、恢复 checksum、聚合行数与结构指纹校验。完整说明见 `infra/mysql-backup-restore-gate.md`。

本地只能先运行不访问远程服务的离线自测：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File tests/email/mysql-backup-restore-gate-selftest.ps1
```

真实运行默认两次精确确认，备份固定在仓库外且收紧 ACL；脚本只输出安全聚合，不执行 migration 或 `000055`。
同一 RunId 的完整保留备份可显式使用 `-ResumeRetainedBackup` 继续恢复验证。该模式只接受精确
`BackupRoot\phase4_<RunId>`，要求私有 ACL、两个完成标记、实际 dump 文件且没有恢复进度；不会重新 dump、覆盖或删除备份。
离线自测同时运行 fake 保留备份生命周期，验证缺失目录与历史 `*.progress` 均失败关闭，且不会访问远程服务。
同一自测还校验恢复 dry-run 的五段固定失败分类、`local_infile`/权限/重映射/元数据/参数受控归类、数字诊断码保留，
官方 53000 段错误码映射、单 schema/checksum 元数据门禁，并确认 dry-run 以 `progressFile=""` 禁用默认进度文件；
这些均为纯 mock、只读本地元数据与静态检查，不代表真实 MySQL 恢复已经通过。
Windows PowerShell 5 不直接反序列化 MySQL Shell 的 checksum JSON；测试要求以非空普通文件门禁替代，并验证
路径、ACL、reparse、标记、payload、progress 和各元数据子条件均返回独立固定原因。
无编号 `loadDump` 错误可用 `-RestoreDryRunDiagnosticOnly` 做受控参数矩阵；测试静态保证所有探针均为 dry-run、
禁用进度状态，只输出 CREATE 能力枚举、布尔结果、固定 reason 和可选数字码。
已确认正常恢复 dry-run 不再传 `checksum`，真实 restore 仍必须传 `checksum=true`；离线静态断言分别锁定这两个参数边界。
另有 Resume 控制流动态自测，要求保留备份验证调用一次、目标不存在断言和新 backup action 均为零次，防止 diagnostic
条件错误接管 backup 的 `else` 分支。
真实恢复失败并留下 progress 后，普通 Resume 仍拒绝继续；显式增加 `-RetryFailedRestore` 才会验证全部历史 JSONL、
聚合至少一个未完成任务、只读确认精确验证 schema 不存在，并为下一次恢复生成不覆盖历史证据的随机新 progress 文件名。
离线 retry 生命周期自测会验证历史文件内容摘要保持不变，输出仅含固定状态且 `remote_access=false`。
只读 preflight 还要求当前精确账号具备 `SESSION_VARIABLES_ADMIN`，缺失固定返回
`restore_session_variables_admin_required`；测试锁定不得用权限更大的 `SYSTEM_VARIABLES_ADMIN` 替代。该权限只能由 DBA
在获批维护窗口临时直接授予，验证完成后立即回收，门禁脚本不会执行 GRANT/REVOKE。
只读 `schema_migrations` 门禁仍是独立检查，不能替代备份恢复门禁。

## 0. Phase 4 schema_migrations 安全只读门禁

该门禁固定从 `D:\molingproject\molin\infra\.env.test` 读取 `MYSQL_HOST`、`MYSQL_PORT`、`MYSQL_USER`、
`MYSQL_PASSWORD`、`MYSQL_DATABASE`，只传入包装脚本的 Python 子进程，不打印配置值。脚本唯一 SQL 为：

```sql
SELECT version, dirty FROM schema_migrations
```

必须逐字输入确认短语，脚本才会连接数据库：

```powershell
powershell -ExecutionPolicy Bypass -File tests/email/run-schema-readonly-gate.ps1 `
  -ConfirmReadOnly "I_CONFIRM_SCHEMA_MIGRATIONS_SELECT_ONLY_NO_MIGRATION"
```

门禁复用仓库现有 Go MySQL 驱动，只精确运行 `TestEmailSchemaReadonlyGate54`，不再维护第二套 Python 数据库实现。默认从 `PATH` 查找 `go.exe`；若 Go 未加入 `PATH`，可显式传入已受控安装的可执行文件和可选临时 modfile：

```powershell
powershell -ExecutionPolicy Bypass -File tests/email/run-schema-readonly-gate.ps1 `
  -ConfirmReadOnly "I_CONFIRM_SCHEMA_MIGRATIONS_SELECT_ONLY_NO_MIGRATION" `
  -GoExecutable "<受控Go目录>\go.exe" `
  -GoModFile "<受控临时modfile>.mod"
```

wrapper 会验证 Go 文件确实存在且文件名严格为 `go.exe`，不会输出工具路径。它不会静默联网安装 Go、驱动或其他依赖。

安全输出只包含 `reachable`、`version`、`dirty`、`is_54_0` 和固定原因枚举，不包含 DSN、主机、端口、账号、密码或异常原文。
退出码 `0` 表示精确命中 `version=54, dirty=false`；退出码 `1` 表示数据库可达但不是 54/0；退出码 `2` 表示确认、配置、驱动、连接或查询门禁阻断。
该脚本不会执行 migration、DDL、DML、事务控制、锁表或数据清理；禁止为了让门禁通过而手工修改 `schema_migrations`。

## 1. 黑盒 API 契约

安全只读运行：

```powershell
$env:API_BASE='http://localhost:8080'
$env:EMAIL_ADMIN_MFA_TOKEN='<临时双MFA管理员Token>'
$env:EMAIL_ADMIN_NO_MFA_TOKEN='<仅完成单项MFA的临时管理员Token>'
$env:EMAIL_ADMIN_NO_PERMISSION_TOKEN='<双MFA但无邮件权限的临时管理员Token>'
$env:EMAIL_ADMIN_VIEW_ONLY_TOKEN='<双MFA且仅有view权限的临时管理员Token，可选>'
$env:EMAIL_ADMIN_VIEW_MANAGE_TOKEN='<双MFA且仅有view+manage权限的临时管理员Token，可选>'
$env:EMAIL_ADMIN_VIEW_SYNC_TOKEN='<双MFA且仅有view+sync权限的临时管理员Token，可选>'
$env:EMAIL_ADMIN_VIEW_TEST_TOKEN='<双MFA且仅有view+test权限的临时管理员Token，可选>'
python tests/email/phase2_email_api.py
```

脚本覆盖 13 个 `/api/admin/email/*` 端点的无 Token 门禁，保留全权限、无双 MFA 和无邮件权限三类兼容输入，并按可用 Token 覆盖四权限隔离矩阵。四类可选 Token 都必须具备 `email:template:view`，脚本会验证概览和五个 D-95 列表均可读。`view+manage`、`view+sync`、`view+test` 分别只允许通过对应写端点的权限层；写探针固定提交空 JSON 且不带 `Idempotency-Key`，因此授权态应在必填参数校验返回 `400/40000`，不具权限者应返回 `403/40003「无权限」`。这些探针不会进入数据库写入、阿里云同步或邮件发送。

真实后端验收的前置条件是管理员或运维已在测试环境通过独立审批流程准备四个隔离账号/角色，分别只绑定 `view`、`view+manage`、`view+sync`、`view+test`，并且四个会话都已完成手机与邮箱双 MFA。脚本只消费临时 Token，不创建账号、不绑定权限、不查询身份数据，也不会为凑齐矩阵而修改测试环境。

推荐使用安全包装脚本读取 Token。四类最小权限 Token 为可选输入，直接回车会明确记为 `SKIP`；默认不启用任何 mutation：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File scripts/run-email-phase4-api-test-secure.ps1 `
  -ApiBase "http://127.0.0.1:18080"
```

四权限测试资产可先执行完全离线自测。该模式只使用进程内假传输，不读取 Token、不打开端口、不访问 API、数据库或供应商：

```powershell
python -B tests/email/phase2_email_api.py --self-test-permission-matrix

powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File scripts/run-email-phase4-api-test-secure.ps1 `
  -SelfTest
```

离线自测固定覆盖 4 类 Token ×（6 个只读端点 + 6 个安全写权限探针）共 48 次内存请求，并输出 `external_access=false mutations=false provider_calls=false`。该结果只证明测试资产的权限期望与无副作用约束正确，不代表真实后端权限配置已经通过。

API 不可达退出码的离线自测不访问任何真实端口：

```powershell
python tests/email/exit_code_selftest.py
```

期望自测进程退出 0，并确认被测脚本输出 `BLOCKED` 且固定退出 2。PowerShell 中读取被测退出码应在命令执行后立即保存 `$LASTEXITCODE`；不要在 `cmd /c` 同一行用未启用延迟展开的 `%ERRORLEVEL%` 判断。

写接口会触发真实同步或发送，默认不执行。仅在隔离测试数据、已审核模板和白名单邮箱准备完毕后显式开启：

```powershell
$env:EMAIL_ALLOW_MUTATIONS='1'
$env:EMAIL_TEMPLATE_ID='<隔离测试模板平台ID>'
$env:EMAIL_SCENE_VERSION='<当前隔离场景绑定版本>'
$env:EMAIL_TEST_RECIPIENT='<受控白名单邮箱>'
python tests/email/phase2_email_api.py
```

写模式使用每轮唯一幂等前缀，精确撤销本轮创建的白名单记录；不会扫描或批量删除。模板停用是有意的安全动作，不自动恢复，避免在并发环境覆盖其他管理员的新版本。运行前必须确认该模板专用于本轮测试。

## 2. 真实 Redis 锁原语

只能通过包装脚本从 `D:\molingproject\molin\infra\.env.test` 加载 Redis 变量到当前进程：

```powershell
powershell -ExecutionPolicy Bypass -File tests/email/run-redis-lock-contract.ps1
```

脚本验证同步 30 秒、OTP/测试发送 15 秒的 `SET NX PX` 互斥，以及 compare-token 后 `PEXPIRE`/`DEL`。每次使用 `qa:email:phase2:<timestamp>:<uuid>` 唯一前缀，只精确 `DEL` 自己记录的 key；代码中没有 `FLUSHDB`、`KEYS *` 或模式删除。该脚本只证明 Redis 原语，不证明 Go 服务已接入续租、外呼前复核和 fencing。

Phase 4 使用独立隔离运行器补充更严格的创建前与清理后证据。先执行完全离线的假 Redis 自测；该模式覆盖成功、创建前冲突、协议异常、清理失败、未预期异常和致命异常，不读取配置、不打开网络连接，也不创建 key：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass `
  -File tests/email/run-redis-lock-phase4-isolated.ps1 `
  -SelfTest
```

真实模式只能由包装脚本从受控 env 文件加载 `REDIS_ADDR`、`REDIS_PASSWORD`、`REDIS_DB`、`REDIS_TLS` 四项到当前子进程。包装层在内存生成并冻结本轮 UUID 前缀；终端只允许输出固定状态、安全计数和前缀摘要，不输出完整前缀、完整 key、连接信息或异常原文：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass `
  -File tests/email/run-redis-lock-phase4-isolated.ps1
```

运行器固定使用 `sync`、`otp`、`test` 三条精确 key。创建前逐条确认 `EXISTS=0`，然后验证 `SET NX PX`、错误所有者续租返回 0、正确所有者续租返回 1、错误所有者释放返回 0、正确所有者释放返回 1；任何路径都在 `finally` 中只清理由本进程成功创建并记录的精确 key，最后使用新连接逐条确认 `EXISTS=0`。若子进程输出为空、包含 stderr 或不符合固定格式，包装器只返回 `runner_output_invalid`，不得回显原始内容。

Go lease 集成测试还必须在正常结束前精确 `DEL` 本轮随机 key 并断言 `EXISTS=0`，且通过 `t.Cleanup` 覆盖所有 `Fatal` 失败路径；成功时只记录固定的 `lease_verified/cleanup_exists_zero=true`。本地不连接 Redis 的静态契约验证命令：

```powershell
python -B tests/email/email_lock_integration_contract.py
```

静态脚本只读取 Go 测试源码，禁止 `KEYS`、`SCAN`、`FLUSHDB/FLUSHALL` 等宽范围命令，固定输出 `remote_access=false redis_commands=false`；它不能替代显式启用后的真实 Redis lease 集成结果。

2026-07-29 的受控远程 Redis 原语验收结果为 `PASS`：固定三条 key，创建前不存在 3/3，原语检查全部通过，清理后不存在 3/3，进程退出码 0。随后使用已校验的 Go 1.25.12 显式启用 `TestEmailRedisLeaseIntegration`，真实连接同一测试 Redis；测试未 SKIP，并通过锁竞争、TTL 续租、ownership fencing、非所有者释放保护和结束前精确清理，固定结果为 `cleanup_exists_zero=true`。该证据仍不替代 Redis 重启和数据库 unknown 墓碑阻断；两者涉及服务重启与数据库写入，必须作为后续独立授权门禁执行。

## 3. 五场景 OTP 的 Phase 2 证据矩阵

真实收件和验证码消费不能伪造。每个场景必须使用独立脱敏账号，先记录 `email_adapter_calls_total{operation="send_mail",scene,result}` 前值，首次发码后人工从受控邮箱取得 OTP（不得写入报告），完成消费，再记录后值。

| scene | 发码入口 | 消费入口 | 必须证据 |
|---|---|---|---|
| register | `POST /api/auth/verification-codes/email` | 统一注册 | pending→accepted；调用增 1；账号仅创建一次；重放/过期均 400/40000 |
| login | 同上，scene=login | 邮箱验证码登录 | accepted 可消费一次；重放失败；过期不签发 Token |
| reset_password | 同上，scene=reset_password | 密码重置 | accepted 可消费一次；旧会话吊销；过期不改密 |
| bind_email | `POST /api/me/verification-codes/email` | `PATCH /api/me/email` | 只绑定当前流程目标；重放/过期不改邮箱 |
| admin_verify | `POST /api/admin/auth/verification-codes/email`（Body 不得含 email） | `POST /api/admin/auth/verify-email` | 手机 MFA 有效；消费后 email MFA=true；过期时邮件管理仍 403/40003 |

每行还必须确认：首次成功响应仅 `{sent:true,expires_in:600}`；生产响应无 `code`；accepted 文案只表示“供应商已受理发送请求”；pending/failed OTP 永远不可认证；冷却窗口重放不重置过期时间、不再次调用 Adapter。

## 4. unknown 墓碑旧/新 key 故障注入

此矩阵要求受控 Adapter 故障开关或网络代理；无注入能力时必须记为 `BLOCKED`，不能用普通 502 代替。

1. 让外呼已开始后超时/结果未知，确认原 pending 行在同一事务收敛为 failed，`failure_reason=provider_outcome_unknown`。
2. OTP 同事务置 failed 且保留 `expires_at`；test 日志的 `expires_at` 仍为 NULL。
3. 原请求与旧 Idempotency-Key 重放均为 `502/51002「供应商响应未知，请在验证码过期后重试」`，旧 key 永久重放原失败且不再次外呼。
4. 冷却期内用新 key 请求同 scope，期望 `409/40900「邮件发送结果确认中，请在验证码过期后重试」`，Adapter 增量 0。
5. 重启 Redis 或精确删除本轮锁 key 后重复第 4 步，仍由数据库墓碑阻断；禁止 `FLUSHDB` 或 `KEYS *`。
6. `cooldown_until` 到期后，新 key 可发送且 Adapter 恰增 1；旧 key 仍返回原 502。

OTP 的 `cooldown_until=expires_at`；test 的 `cooldown_until=submitted_at+10分钟`，不得新增数据库列。所有测试对象使用本轮 UUID 前缀，数据库清理由已记录主键精确执行并保留必要审计，禁止模糊条件删除。

### Redis 重启后的数据库墓碑两阶段门禁

`TestEmailUnknownTombstoneSurvivesRedisRestart` 是上述第 5 项的可执行集成门禁。它只允许 `APP_ENV=test` 语义下的进程内 `MockEmailAdapter`，不会创建真实 DirectMail Adapter。测试使用真实 `EmailRepository`、真实测试 MySQL 和真实测试 Redis；运行前强制要求 `schema_migrations=57/dirty=0`、显式操作员 ID、双重开关和当前用户独占的 600 状态文件。测试 MySQL DSN 必须与生产全局连接保持 `loc=Local`，并在任何夹具写入前把 `UTC_TIMESTAMP()` 的墙钟字段重新解释为 UTC，确认与应用 UTC 当前时间偏差不超过 5 秒；禁止使用 `loc=UTC`，否则仓储的 UTC 墙钟转换会再次偏移。状态文件不保存完整邮箱、幂等键或业务号，只记录本轮随机 nonce、Redis `run_id`、原日志精确主键，以及可选的意外新-key日志精确主键；新增可选字段不改变 version 1，现存 phase1 状态文件可继续读取。

先在仓库根目录执行完全离线的静态门禁；它不连接 MySQL、Redis、服务器或网络：

```powershell
python -B tests/email/email_unknown_restart_integration_contract.py
```

真实集成必须在受控测试环境分两次启动 Go 测试。MySQL/Redis 凭据只通过当前测试进程环境注入，禁止写入命令记录、状态文件或报告。以下只列非敏感控制变量，连接变量沿用测试环境既有注入：

```powershell
$env:RUN_EMAIL_UNKNOWN_RESTART_INTEGRATION='1'
$env:EMAIL_UNKNOWN_RESTART_ACK='I_UNDERSTAND_ISOLATED_EMAIL_UNKNOWN_RESTART_TEST'
$env:APP_ENV='test'
$env:EMAIL_ADAPTER='mock'
$env:EMAIL_UNKNOWN_RESTART_STATE_FILE='<当前用户独占的绝对状态文件>'
$env:EMAIL_UNKNOWN_RESTART_OPERATOR_ID='<已存在的隔离测试操作员ID>'
$env:EMAIL_UNKNOWN_RESTART_PHASE='phase1'
go test ./internal/modules/auth/service -run '^TestEmailUnknownTombstoneSurvivesRedisRestart$' -count=1 -v
```

phase1 必须固定得到 `classification=tombstone_created`、`adapter_calls=1` 和 `redis_restart_required=true`。随后停止测试操作；外部维护人员在独立授权窗口中只能重启本轮测试 Redis，不得执行 `FLUSHDB`、`FLUSHALL`、`KEYS`、`SCAN`、精确删除锁键以外的删除，也不得改动 MySQL、API、环境文件或业务数据。重启完成后只把 phase 改为 `phase2`，使用同一状态文件和同一连接配置再次运行同一 Go 测试：

```powershell
$env:EMAIL_UNKNOWN_RESTART_PHASE='phase2'
go test ./internal/modules/auth/service -run '^TestEmailUnknownTombstoneSurvivesRedisRestart$' -count=1 -v
```

phase2 先比较前后 Redis `run_id`，无法证明发生过重启即失败；在任何 `TestSend` 前，再以状态中的原日志 ID、派生 scope、旧 key hash、请求指纹和收件人 HMAC 精确核验数据库 unknown 墓碑。若已存在同 scope+新 key hash 的意外日志，测试会先严格核验归属并把其唯一主键写回状态，然后固定 `BLOCKED` 且 Adapter 调用数为 0。原墓碑距离 `submitted_at+10分钟` 不足 120 秒时同样固定 `BLOCKED`，不得进入旧 key 或新 key 调用。

预检通过后，旧 key 与新 key 分步执行：旧 key 必须返回 unknown 且该步 Adapter 累计调用数仍为 0；新 key 必须返回确认中且累计调用数仍为 0。失败摘要输出实际的 `old_key_unknown`、`new_key_pending`、`adapter_calls` 和 `unexpected_log_recorded`，不再用固定文案假称 Adapter 已调用。若新 key 意外落库，测试在失败返回前按 scope+新 key hash 找到唯一日志，严格核对模板、场景、用途、收件人、指纹、状态与失败原因后保存其主键。phase2 成功只输出 `cleanup_performed=false`、`test_data=retained` 和 `cleanup_authorization_required=true`，把状态更新为 `phase2_verified`；它不得删除数据库行、Redis key 或状态文件。保留数据是本轮可复核证据，不代表可以长期滞留，必须在验收取证结束后进入独立 cleanup 阶段。

任一阶段失败都只输出固定分类并保留状态文件，不输出完整邮箱、幂等键、业务号、Redis key、凭据或原始异常。取证结束或失败恢复时，cleanup 必须作为新的独立命令运行，同时满足原集成开关、原确认短语以及以下清理专用开关和确认短语；缺少任一项只返回 `cleanup_gate_denied`，不执行任何删除：

```powershell
$env:EMAIL_UNKNOWN_RESTART_PHASE='cleanup'
$env:RUN_EMAIL_UNKNOWN_RESTART_CLEANUP='1'
$env:EMAIL_UNKNOWN_RESTART_CLEANUP_ACK='I_UNDERSTAND_EXACT_EMAIL_UNKNOWN_RESTART_CLEANUP'
go test ./internal/modules/auth/service -run '^TestEmailUnknownTombstoneSurvivesRedisRestart$' -count=1 -v
```

cleanup 只按状态文件中的原发送日志主键、可选意外新-key日志主键、白名单、模板主键以及推导出的唯一锁键精确删除。删除两类发送日志时还必须同时匹配模板、场景、用途、收件人 HMAC、scope、对应 key hash、请求指纹、unknown 状态与失败原因；任一归属不符即回滚数据库事务。测试使用 `EXISTS=0` 复核 Redis key 后才删除状态文件。它不扫描数据库、不使用 Redis `FLUSHDB`、`FLUSHALL`、`KEYS`、`SCAN` 或模式删除，也不计入 Phase 4 阻断验证通过证据。状态文件缺失、不安全、schema 非 57/dirty0、Redis `run_id` 未变化、墓碑冷却余量不足或清理数量不精确时均为 `FAIL/BLOCKED`，不得记为通过。

## 5. DirectMail RAM 否定矩阵

运维通过安全渠道切换专用最小权限测试 RAM 身份；测试记录只写身份别名和策略版本，不写 AccessKey。每次先快照模板镜像版本/missing、验证码/发送日志状态与 Adapter 指标，再执行单个 Deny，最后恢复最小 Allow 并复核。

| 策略 | 流程 | 期望 |
|---|---|---|
| 最小 Allow：仅 QueryTemplateByParam、DescTemplate、SingleSendMail | 基线 | 同步/发送进入业务流程；Create/Modify/Delete 调用计数为 0 |
| 显式 Deny `dm:QueryTemplateByParam` | 同步 | 502/51002；run failed；镜像、版本和 missing 不变 |
| 显式 Deny `dm:DescTemplate` | 同步详情 | 502/51002；整批回滚，无半新半旧镜像 |
| 显式 Deny `dm:SingleSendMail` | 正式 OTP、test-send | 502/51002；OTP 不可用；测试不返回 200/accepted |
| 直接探测 CreateTemplate/ModifyTemplate/DeleteTemplate | 越权否定 | 三者均被 RAM 拒绝，应用运行轨迹从不调用 |

平台 RBAC 拒绝必须为 403/40003，不能与 RAM 的 502/51002 混淆。响应、日志、审计和 telemetry 还需执行：

```powershell
python tests/email/sensitive_scan.py <文件或目录> --repo-root . --allow-domain example.invalid
```

扫描器递归读取文本源码、日志和前端构建产物，只输出固定级别、分类、仓库相对路径和行号，不回显命中内容。AccessKey/Secret、JWT、Refresh Token、私钥、生产源码中的六位 OTP，以及运行时日志/JSONL 内未脱敏的完整邮箱、手机号或供应商正文判为 `FAIL`；源码中的完整真实域名邮箱/手机号和 debug code、供应商 raw/message/正文输出面判为 `REVIEW`，需要结合上下文确认是否为合成数据或受环境门禁的安全分支；`example.invalid` 等保留占位域、约定测试手机号、测试/文档中的合成 OTP 和文档术语只判为 `INFO`。`.env`、`.env.local`、`.env.test`、`.env.production` 固定拒绝读取并报告 `protected_env`，仅允许扫描无秘密的 `.env.example`。可重复传入 `--show-level FAIL --show-level REVIEW` 抑制 INFO 明细，但汇总仍统计全部级别。先运行 `python -B tests/email/sensitive_scan_selftest.py` 验证分类与输出脱敏。

2026-07-29 同一时间窗本地扫描固定证据：扫描 `server`、管理端、用户端、`tests`、`docs`、`infra`、`scripts` 共 1003 个文本文件，包含当时存在的两个前端 `dist`。扫描器自测 4/4 通过；结果为 `PASS`，`FAIL=0`、`read_errors=0`、`REVIEW=3`、`INFO=246`。三项 REVIEW 已按最新验收记录完成人工只读复核：分别属于非验证码业务字段、合成脱敏示例，以及受安全非生产环境判定与显式调试开关共同约束的验证码响应载体；未发现已知真实秘密值。该结论只关闭该时间点工作树的静态字面量复核，不替代运行时日志、真实响应、数据库、审计、telemetry 和部署产物的发布前同一时间窗复扫。

## 6. 发布判定

以下任何一项为 `SKIP/BLOCKED/FAIL`，Phase 2 都不得宣称通过：13 接口授权态、D-95、四权限 MFA/RBAC、同步和测试发送幂等、模板/绑定/白名单乐观锁、真实 Redis Go 集成、五场景真实发送及消费、unknown 墓碑、RAM 否定矩阵、敏感扫描。供应商 accepted 与用户确认收件必须作为两条独立证据，且 accepted 不等于最终送达。
