# DirectMail 可执行验收资产（Phase 2 历史命名，当前用于 Phase 4）

> 状态收口（2026-08-02）：Redis unknown fresh cycle、000055/000056 独立 MySQL 8 全矩阵、000057 技术可逆周期、真实 Redis lease、真实四角色三宽度及运行时六表面扫描均已关闭，标记 `must_not_repeat=true` 的项目不得重跑。RAM 有效权限、五场景真实重放/过期、模板测试发送真实故障矩阵和五业务流真实外发 E2E 均由项目负责人豁免但未技术验证；QA/PM 已附负责人豁免通过，Phase 4 已关闭。Phase 5 与生产上线仍未批准。

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

历史静态审计（已被后续生产 Go 测试证据取代）：当时生产 Go 测试虽覆盖五场景发送成功，但供应商拒绝/超时只逐业务验证 register，业务层成功消费、并发单次消费和重放拒绝只有 login；模板测试发送也缺 accepted 同 key 重放、并发单外呼、白名单三态及统一失败零副作用。后续生产 Go Phase 4 测试已补齐上述本地缺口并通过，但这些测试不连接真实数据库或外部 Redis，仍不能替代五场景真实重放/过期、真实模板测试发送、供应商故障注入或最终送达证据。

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

## 000055/000056 migration 离线契约检查

新增或调整 migration 后先运行不连接数据库的静态检查。000055 必须在普通模式与
`PYTHONOPTIMIZE=1` 模式分别执行，确保安全断言不依赖会被 `-O` 移除的 Python `assert`：

```powershell
python -B tests/email/migration_000055_contract.py
$env:PYTHONOPTIMIZE = '1'
try {
  python -B tests/email/migration_000055_contract.py
} finally {
  Remove-Item Env:PYTHONOPTIMIZE -ErrorAction SilentlyContinue
}

python -B tests/email/migration_000056_contract.py
```

000055 检查冻结当前 Up/Down 原始字节 SHA-256，覆盖验证码 expand-first/历史失效、五张业务表、核心字段、26 个显式索引、35 个业务安全 CHECK、7 个外键、五场景固定映射、四权限、ownership 捕获与精确 down 顺序；同时拒绝 schema 删除、基础表删除、授权修改、动态 SQL、文件导入导出及对 000001-000054 的引用。MySQL 可执行注释 `/*!...*/` 和优化器提示 `/*+...*/` 均禁止，检查器会在移除普通块注释前先失败关闭，避免隐藏危险语句或改变执行计划。内置故障注入会绕过摘要门禁后分别破坏表、字段、索引、CHECK、外键、场景、变量、权限、ownership、down 归属、删除顺序、危险语句和可执行注释，证明语义校验自身能够失败关闭。当前冻结摘要为 Up `7238522CEC2CDFB2AD042C4B668380AA691E396CD536152F3ED25049ECD1FA3D`、Down `217B8FDAB63962284DA9D6EE1C436716687E351FE313E76F88E08C421D7C26EE`。

000056 脚本会移除 SQL 行注释后检查关键 DDL/DML、严格 receipt 门禁、ownership 精确删除顺序和禁止的模糊写法，并输出 up/down SHA256。两项输出均固定包含 `mode=static`、`remote_access=false` 和 `db_scenarios=not_run`，只证明离线契约结构通过；不能替代 MySQL 8 的真实 up/down、admin 预存组合、元数据冲突、partial 故障注入、未知引用、成功 receipt 阻断和 `schema_migrations dirty=0` 验收。

### 000055 当前 SHA 隔离矩阵控制器

`run-000055-container-isolation-matrix.sh` 是独立于 000056/000057 的容器内控制器。默认调用与
`--self-test` 都会在任何 MySQL 客户端调用前返回，只有同时提供固定 `--execute` 确认短语和
`MOLIN_000055_ISOLATION_EXECUTE` 环境门禁才进入执行路径。离线契约运行方式：

```powershell
python -B tests/email/run_000055_container_isolation_matrix_contract.py
```

离线契约会在普通模式和 Python `-O` 模式中执行 Bash 语法检查、默认关闭、自检、单门禁拒绝和
17 类攻击模型；不会连接数据库，也不会执行 migration。新增的两类攻击会分别注入硬编码旧隔离库
字面量，以及通过 `mysql_admin` 查询旧库，证明两道拒绝规则互不替代。控制器冻结当前 Up/Down SHA，
禁止读取 MySQL option files，不选择测试主库，不删除任何 schema，也不清理或访问旧隔离库及本轮证据。

本轮 runner SHA-256 为 `A656E9EFF407249925781E8DEC81EA8D6ED89BAEC73BC2D6F73281DAF28A468D`；normal/`-O`、默认关闭和 17 个攻击模型通过，QA 复核 P1/P2 均为 0。本轮没有执行真实 MySQL；控制器的 partial 固定为 `not_implemented`，不得把本轮离线通过扩展为真实隔离验收。

真实执行前必须在容器内准备 root 所有、目录权限 `700`、文件权限 `400` 的固定资产目录
`/root/molin-000055-isolation-assets`，其中只允许以下文件：

- 当前 `000055` Up/Down SQL；
- `schema54-empty.sql`：由当前 `000001` 至 `000054` 建立且验证码表为空的 schema54/dirty0 基线；
- `schema54-legacy.sql`：schema54/dirty0，至少含一条邮箱、一条手机及一条 16 位历史验证码；
- `schema55.sql`：完整 schema55/dirty0，未创建 000056 对象且满足安全 down 门禁；
- `baseline-manifest.tsv`：无表头、恰三行，列为 `文件名<TAB>大写SHA256<TAB>版本<TAB>类型`，
  三行类型分别固定为 `schema54-empty.sql/54/empty`、`schema54-legacy.sql/54/legacy`、
  `schema55.sql/55/complete`。

控制器会拒绝包含库级 `USE/CREATE DATABASE/DROP DATABASE`、账号授权和 `SET GLOBAL` 的普通或
MySQL 可执行注释命令。每个用例都从系统 UUID 派生新的隔离库和独立证据目录，恢复后再次通过
`schema_migrations`、InnoDB、对象结构和数据断言证明基线，而不是只信任清单标签。当前执行矩阵覆盖：

- 空 schema54 基线 Up→Down；
- 代表性历史 schema54 基线 Up→Down，以及历史验证码统一失效、邮箱不可关联清理和手机兼容；
- 完整 schema55 基线 Down；
- 四种 ownership 组合：全新、权限预存但 admin 绑定缺失、权限和绑定均预存、混合预存；
- schema55 的 35 个 CHECK、7 个外键、35 个索引、五场景、四权限和 ownership 四行断言。

真实执行成功会保留 7 个运行时唯一隔离库及其 `600` 证据，不自动清理，并明确输出
`partial_fault_injection=not_implemented`。当前控制器**尚未覆盖** 000055 的 16 个 partial-up、15 个
partial-down、权限元数据冲突和三类未知引用阻断；这些必须由后续独立断点执行器在真实 MySQL 8
完成，不能把本控制器的离线 PASS 或基础矩阵 PASS 记为完整 000055 隔离验收。

### 000055 partial 故障注入隔离矩阵

`run-000055-container-partial-matrix.sh` 是独立于 000055 基础隔离 runner 的断点执行器。基础 runner 继续如实输出 `partial_fault_injection=not_implemented`；只有本独立资产在真实 MySQL 8 完整执行后，才能把两者作为组合证据使用。复制资产或连接数据库前，必须先运行纯本地契约：

```powershell
python -B tests/email/run_000055_container_partial_matrix_contract.py
```

离线契约在普通与 Python `-O` 模式执行相同显式异常检查，覆盖 Bash 语法、SelfTest、默认关闭、单门禁关闭和 27 个攻击模型。本轮冻结 Up SHA-256 `7238522CEC2CDFB2AD042C4B668380AA691E396CD536152F3ED25049ECD1FA3D`、Down SHA-256 `217B8FDAB63962284DA9D6EE1C436716687E351FE313E76F88E08C421D7C26EE`，边界清单 `000055-partial-boundaries.tsv` SHA-256 为 `4B5E02DC0C72490B168A47637E1DD8E6298DFEBE18AC22CD9DCAF663B8E18585`。

正式资产目录固定为 `/root/molin-000055-partial-assets`，包含当前 Up/Down、`schema54-legacy.sql`、`schema55.sql`、两行基线清单和 31 行断点清单。目录必须为 root:700，全部叶文件必须为 root:400 普通非符号链接文件。执行前还必须由独立准备步骤把 schema54、schema55 和基线清单的大写 SHA-256 分别注入 `MOLIN_000055_SCHEMA54_SHA`、`MOLIN_000055_SCHEMA55_SHA` 和 `MOLIN_000055_BASELINE_MANIFEST_SHA`；控制器将外部冻结值与清单及文件三方比对，防止基线和清单被成对替换。基线先拒绝 MySQL 可执行注释和优化器提示，再通过 SQL 感知扫描移除字符串字面量与普通注释后检查跨 schema 限定名，因此历史邮箱不会被误判；库级 DDL、账号授权和全局配置同样被拒绝。每个用例从系统 UUID 派生独立 `molin_55pt_` 新隔离库与 `600` 证据目录；共创建并保留 33 个目标，不执行清理，不选择测试主库，不修改账号或授权，不输出完整随机库名。

Up 16 个冻结断点为：五张业务表逐表创建 5 点、ownership 技术表创建 1 点、按确定性权限 code 顺序写入 ownership 第 1/2/3/4 行 4 点、权限补缺/权限 ID 回填/admin 绑定补缺/绑定 ID 回填 4 点、权限元数据强断言 1 点、admin 绑定与 ownership 强断言组 1 点。ownership 四个行级断点不是任意截断 SQL：执行器只接受当前完整 Up SHA，抽取冻结的第 27 条语句，在其唯一终止分号前注入 `ORDER BY spec.code LIMIT 1..4`，并逐点断言精确 permission code 集合；任何语句数量、顺序、集合或匹配漂移都会失败关闭。

Down 15 个冻结断点为：验证码失效与其强断言 2 点；删除 admin 绑定、删除权限、写后强断言、删除 ownership 技术表 4 点；发送日志、白名单、同步、绑定、模板五张业务表逆序删除 5 点；verification 删除 CHECK、索引、新增列并确认旧 `code VARCHAR(64) NULL` 4 点。临时断言表随同后续边界执行和连接关闭处理，不作为持久恢复对象单独计点。

每个 partial 用例先把 `schema_migrations` 标成对应源版本 dirty，再执行到冻结边界并触发固定 `SIGNAL`，随后必须用 `information_schema` 与持久表证明：业务表数量、ownership 表/行数及逐行创建标志、确定性 permission code 前缀、permission/binding 数量、两类回填 ID、verification 新增列/索引/CHECK 与断点一致。不能只依赖 SQL 退出码。另有 Up 和 Down 各一次无注入基线，必须分别收敛到 55/dirty0 与 54/dirty0。当前本轮只完成离线资产和攻击模型，没有连接 MySQL 或执行 migration，因此 16/15 真实门禁仍未关闭。

最新离线证据固定为 Up 16 点、Down 15 点、基线 2 条，共 33 个目标；partial runner SHA 为 `E9EC4C1F...EBA9C9`，boundary SHA 为 `4B5E02DC...18585`，32 个攻击模型通过。环境预检不再依赖镜像未承诺的 `/usr/bin/wc`，由既有 awk 精确核验两行 manifest。该独立资产补充了基础 runner 中 `partial_fault_injection=not_implemented` 所缺的可执行编排，但尚未完成修复后的真实 MySQL 复验。

### 000056 当前 SHA 隔离矩阵控制器

`run-000056-container-isolation-matrix.sh` 是独立于 000055/000057 的容器内控制器。正式运行默认关闭，必须同时提供参数确认短语和环境变量门禁；默认、`--self-test` 与单门禁路径都必须在首次 MySQL 客户端调用之前结束。复制任何资产或连接数据库前，先执行纯本地契约：

```powershell
python -B tests/email/run_000056_container_isolation_matrix_contract.py
```

该契约在普通与 Python `-O` 两种模式使用显式异常执行相同检查，运行 `bash -n`、SelfTest、默认关闭和单门禁关闭，并覆盖 20 个攻击模型。当前冻结的 Up SHA-256 为 `BC900F4B8420D402A5E377CAF8C83344A973995297AC577C40BE86510678C735`，Down SHA-256 为 `F42A30D70A95AD7BFD876F1515267C5FEE3DDCFD7AAC066453BDC020D201A5C2`。攻击模型会分别证明历史隔离库字面量、`mysql_admin` 访问非本轮库和账号授权注入被独立拒绝，并拒绝固定目标、测试主库选择、完整随机库名输出、读取 MySQL option file、删除 schema、绕过基线清单、移除 schema56 共用基线以及虚报矩阵覆盖。

本轮 runner SHA-256 为 `D86CA32EF77A312BDDF545A03E71BADF7177C5FB63B35EDA37AA6B8439285308`，契约 SHA-256 为 `BB0A4E9DEF62868A808077EBFC4A4F3269FE79B36C8EE0614C86C39007C52ADA`；normal/`-O`、默认关闭和 20 个攻击模型通过，QA 复核 P1/P2 均为 0。本轮没有执行真实 MySQL，partial 固定为 `not_implemented`。

正式资产目录固定为 `/root/molin-000056-isolation-assets`，由 root 持有且目录权限为 `700`；Up、Down、`schema55.sql`、`schema56.sql` 和 `baseline-manifest.tsv` 均为 root:400 普通非符号链接文件。basic 与 partial 共用同一份恰好两行的清单，分别冻结 `schema55.sql/55/complete` 与 `schema56.sql/56/complete`，并逐项与实际基线 SHA 一致；修复前 basic 要求一行而 partial 要求同名清单两行，导致六项外部输入无法同时满足，现已关闭该阻断。两份基线都不得包含库级 `USE/CREATE DATABASE/DROP DATABASE`、账号授权或全局配置命令。basic 控制器仍只从 schema55 恢复并创建 11 个从系统 UUID 派生的全新 `molin_56mx_` 隔离库，不删除目标，不修改账号或授权，不选择测试主库；输出只包含目标 SHA 摘要，完整目标仅写入权限 `600` 的保留证据。

当前真实矩阵覆盖：

- 权限与 admin 绑定的三种 ownership 组合：全新、权限预存、权限与绑定均预存；
- admin 数量为 0、admin 数量为 2、同 code 权限元数据冲突三种 Up 失败关闭；
- 空 receipt 的安全 Down、已有 receipt 的 Down 阻断；
- 未知角色、用户权限覆盖、分组权限三类引用的 Down 阻断；
- 两个事务竞争同一 `admin_verify` scope 时，只允许一条 receipt 成功的唯一约束验证。

成功摘要明确输出 `partial_fault_injection=not_implemented`。当前控制器**尚未覆盖** 000056 Up/Down 各语句断点的 partial 状态恢复或拒绝矩阵；该缺口必须由后续独立故障注入资产在真实 MySQL 8 中完成，不能把本控制器的离线 PASS 或上述基础矩阵 PASS 描述为完整 000056 隔离验收。

### 000056 partial 故障注入隔离矩阵

`run-000056-container-partial-matrix.sh` 是与基础 000056 runner 分离的全语句边界资产。当前 Up SHA `9133212C61EB4AA89B72C77D0C353F4B0F8B483080CBFB1E85A0281379861D9B` 实际包含 27 条 DDL/DML/断言语句，Down SHA `F42A30D70A95AD7BFD876F1515267C5FEE3DDCFD7AAC066453BDC020D201A5C2` 实际包含 14 条，因此矩阵固定为 Up 27 点、Down 14 点及两条无注入基线，共 43 个运行时 UUID 新隔离库。权威 partial runner SHA 为 `1BDAF1453073F48098EE131FEB7B5711C8749987025A7C37C9C0AE544A43BFB1`，边界清单 SHA 为 `7B9E3132B2A09D939FD81E908C889EE6EE41A69B5D680B52A081D5A0A9BA4A62`，离线 contract SHA 为 `407456C5C6F34DAC11EE275FE58A72340CDA55BF163D3107402A3B385608EBFA`；环境预检使用 awk 精确计数，不依赖 `/usr/bin/wc`。

执行前先运行离线契约：

```powershell
python -B tests/email/run_000056_container_partial_matrix_contract.py
```

契约在 normal 与 Python `-O` 模式执行相同检查，包含 Bash 语法、SelfTest、默认关闭、单门禁关闭、SQL 感知扫描 fixture 和 32 个攻击模型；边界名称、顺序、语句编号和状态共同冻结，避免状态数字正确但语义标签错位。正式执行还需参数与环境双门禁；schema55、schema56 和两行 manifest 分别由 `MOLIN_000056_SCHEMA55_SHA`、`MOLIN_000056_SCHEMA56_SHA`、`MOLIN_000056_BASELINE_MANIFEST_SHA` 三个独立外部摘要与 root:400 文件、清单三方绑定。基线先拒绝 MySQL 可执行注释与优化器提示，再移除普通注释和单/双引号字符串，处理反斜线、重复引号与未闭合输入后检查跨 schema 限定名；同时拒绝库级 DDL、账号授权、全局配置、主库、旧库、固定目标、option file 和完整随机目标输出。

Up 27 点依次覆盖：持久断言表创建；14 条 schema55、verification、000055 ownership、权限、admin 绑定、场景和预存 bootstrap 元数据断言；receipt 表；000056 ownership 表及状态捕获；bootstrap 权限补缺、权限 ID 回填、admin 绑定补缺、绑定 ID 回填；4 条写后权限、绑定、ownership、空 receipt 断言；最终断言表删除。Down 14 点依次覆盖：断言表创建；完整对象、空 receipt、admin、权限元数据、ownership、未知引用六条删除前断言；精确删除 admin 绑定和权限；两条删除后断言；按依赖顺序删除 receipt、ownership 和断言表。

每点先把对应基线的 `schema_migrations` 标成 55/dirty1 或 56/dirty1，执行到冻结边界并触发固定 `SIGNAL`，再直接核验断言表存在性/通过行数、receipt 的 9 列/5 索引/2 外键/5 CHECK 与零业务行、ownership 行及 `permission_created/admin_binding_created` 标志、两类回填 ID、bootstrap 权限和唯一 admin 绑定。无注入基线分别收敛为 56/dirty0 与 55/dirty0。基础 runner 继续诚实输出 `partial_fault_injection=not_implemented`；只有本独立资产真实执行后才能形成组合证据。当前仅完成本地资产与离线验证，未连接 MySQL 或执行 migration。

最新离线契约固定为 `attack_cases=32`，终审 QA P1/P2=0，文档证据漂移已关闭。独立 partial 资产只补充了可执行编排；由于本轮没有真实 MySQL 运行证据，000056 partial 仍不得记为已验收，Phase 4 仍未通过。

### 本地隔离矩阵打包资产

打包资产由 `scripts/email-migration-isolation-bundle.manifest.tsv`、`scripts/build-email-migration-isolation-bundle.ps1` 和 `scripts/email_migration_isolation_bundle_contract.py` 组成，对应 SHA 分别为 `D17B5895...45E2`、`10EAACA3...BCD4`、`CA2E2E6C...AB60`。manifest 固定 20 项，其中 14 项为仓库内部资产、6 项为执行前由受控环境提供并校验的外部基线占位；最终 tar 内容固定为 15 项。

离线验证已覆盖 SelfTest、PowerShell AST、默认关闭、normal 与 Python `-O`，结果为 `attack_cases=13`、`output_preservation_cases=4`、`symlink_checks=true`，QA P1/P2=0。此前 P1“预存输出被删除”已通过 `CreateNew` 与 owned flags 修复：输出只能以创建新文件方式产生，失败处理只能作用于本轮创建且归属明确的输出，预存路径不得删除或覆盖。

本轮没有生成持久包，没有上传或外连，没有访问数据库，也没有执行 migration。打包契约通过只证明本地资产集合、输出保留和符号链接门禁可执行，不代表 000055/000056 已在真实 MySQL 运行；真实 MySQL 仍未执行，Phase 4 仍未通过。

最新执行四套 basic/partial runner 的 normal/`-O` 契约和打包器 SelfTest、normal/`-O` 契约，攻击模型分别为 17、27、20、32 和 13，全部通过，且固定 `database_access=false`、`migration_executed=false`。测试服务器此前只读检查确认唯一 MySQL 容器内四个正式资产目录全部不存在；六项外部输入 `schema54-empty.sql`、`schema54-legacy.sql`、`schema55.sql`、`schema56.sql`、`000055-baseline-manifest.tsv`、`000056-baseline-manifest.tsv` 尚未交付。000056 的同名 manifest 行数冲突已在本地资产中关闭，但当前仍缺 migration 专项授权及通过外部 SHA 门禁的受控基线，不能生成正式执行包，更不能把 runner 可执行性扩展为真实 MySQL 通过。

### 受控 migration 基线生成器

`scripts/generate-email-migration-baselines.sh` 用于在后续专项授权窗口生成上述六项输入。它默认关闭，`--self-test` 只冻结 000001→000056 恰好 56 个 Up migration 的集合 SHA，不发现 Docker；真实执行必须同时提供参数确认词、环境确认词、本地已存在的 `mysql@sha256:<digest>` 镜像引用、独立冻结的镜像 ID，以及预先存在、为空、当前用户所有且权限 700 的输出目录。生成器禁止拉取镜像，临时容器固定 `--network none`、不发布端口、只读根文件系统，数据库仅为容器内 `molin_baseline`；除 digest/ID 外还在容器内核验 `mysql --version` 主版本为 8，退出时只按本轮 64 位容器 ID 删除该临时容器。当前生成器/契约 SHA-256 为 `FBAD0D661E7EB0DFCDA02DCD1A5C57142F6906D409F1CAF4589F46AC8C9EB6F0`、`E967BE5941A2B125C02106F4B7BCBDD348FE4B80C9F604B9A6EF41A2B7D6B011`，normal/`-O` 20 个攻击模型通过。

`scripts/email-migration-matrix-remote.payload.sh` 与 `scripts/run-email-migration-full-isolation-matrix.ps1` 把基线生成和四套矩阵绑定为单次失败关闭操作。控制器本地冻结并打包 66 项源文件，成功路径固定两次 SSH、一次 `scp -O`；远端只读获取唯一测试 `molin-mysql` 的镜像 digest/ID，不对其执行 `docker exec` 或任何数据库连接。随后顺序创建两个不同时存在的无网络临时 MySQL 8 容器：第一个生成六项基线并删除，第二个运行 7+33+11+43 共 94 个 UUID 隔离目标，最终核对四类目标数量后删除。失败时精确删除临时容器、保留唯一 Stage 且零重试；成功才删除 Stage。payload/controller/contract SHA-256 为 `4FC51DB9AB2172CD5E574E28F18B3FE9E3D0C8AFDC14EE8BEFA7DAC87F5FA401`、`1E002BAA1E8DC279D0734DB61754ACF9475BC3CFAC950514EEF9644115076A91`、`7769FD36AC9DDBA31DF22003283C0BFDAD16B8621F74C040B2216E38CACCE658`；normal/`-O` 32 个攻击模型、PowerShell 5.1 SelfTest 和默认关闭通过。当前未取得执行授权，不能据此关闭 migration 门禁。

```powershell
python tests/email/email_migration_baseline_generator_contract.py
python -O tests/email/email_migration_baseline_generator_contract.py
powershell.exe -NoProfile -Command "& 'C:\Program Files\Git\bin\bash.exe' scripts/generate-email-migration-baselines.sh --self-test"
```

真实路径依次应用 000001→000054，生成空 schema54；写入固定隔离 email、phone 和 16 位历史验证码夹具后生成 legacy schema54；再依次应用当前 SHA 的 000055、000056 并生成 schema55/schema56。dump 拒绝可执行注释、优化器提示、库级 DDL、账号授权和全局配置。六项输出先落在唯一 `/tmp/molin-email-baseline-<32hex>` stage，全部验证后才以 `noclobber` 发布；失败只删除本轮记录的六个固定输出。当前 migration 集合 SHA 为 `8EB0770187264AFE2E1DA0FC529760B633B6DB507D74F6C06738B4A199B31A82`，生成器 SHA 为 `FBAD0D661E7EB0DFCDA02DCD1A5C57142F6906D409F1CAF4589F46AC8C9EB6F0`，契约 SHA 为 `E967BE5941A2B125C02106F4B7BCBDD348FE4B80C9F604B9A6EF41A2B7D6B011`；normal/`-O` 20 个攻击模型通过。本轮没有启动容器或生成基线，真实执行仍需新的 migration 专项授权。

## 000057 容器隔离周期离线检查

在复制脚本到受控容器或连接数据库前，先运行纯本地契约检查：

```powershell
python -B tests/email/run_000057_container_cycle_contract.py
```

该检查分别在普通 Python 与 `PYTHONOPTIMIZE=1` 优化模式执行同一套显式异常校验，并执行 `bash -n`，冻结 Up、Down 与执行脚本 SHA-256。契约会枚举 helper、直接 MySQL 调用和管理查询中的源库 SQL，要求源库门禁精确为 schema57/dirty0 且所有源库语句均为只读 `SELECT`；全部 `mysql`、`mysqldump` 调用必须把 `--no-defaults` 作为客户端首参数，禁止读取 option files。
脚本只读取一次 `/proc/sys/kernel/random/uuid`，使用同一后缀派生全新
`molin_restore_57_reverify_<32位小写十六进制>` 隔离库和独立运行目录；任何完整旧隔离库名都不得写入正式脚本，
因此不能被查询、修改或清理。脚本输出也不得包含本轮随机库名或目录名。

当前执行脚本 SHA-256 为 `D3A4B8A318D101640BFC130A482ECE423D61B63F63DC36DF6E89D497A7AF83A6`；
冻结 Up SHA-256 为 `50DCD97A45D8ADCF2F7CAC316B44D942DDB880D4F922B8872CAA34BA01CFC67C`，
Down SHA-256 为 `EE05D166EB874D34A14A0D12FC17EE083CAC28DAFEEAC3772A8C14A4945495BB`。
固定只读资产目录为 `/root/molin-000057-schema57-cycle-assets`，运行目录仅在启动时生成且必须预先不存在。

离线故障注入覆盖 12 类破坏：源版本降级、周期阶段缺失、固定旧库、增加 schema 删除、复用运行目录、移除毫秒恢复、移除非时间指纹、移除 `--no-defaults`、经 helper 写源库、经管理查询写源库及泄露原始错误。旧隔离库扫描覆盖全部额外恢复库前缀和旧库语义引用，不依赖单一历史字面量。MySQL 失败时只允许返回固定安全分类、退出码和 stderr 字节长度，禁止输出凭据、原始错误或业务内容。该离线检查本身不连接数据库、不执行 migration。

### 000057 真实周期状态与清理边界

2026-07-30 真实执行前只读门禁确认：测试主库为 schema57/dirty0、69 张 InnoDB 基础表、无 DDL 或锁活动，既有隔离库数量为 2；冻结摘要为脚本 `D3A4B8...83A6`、Up `50DCD...CFC67C`、Down `EE05D...495BB`。真实 Down→Up→Down→Up 周期实际完成两次，间隔约 55 秒；两个新隔离库最终均为 schema57/dirty0、69 表、backup 表计数 1、receipt 时间精度 0、完成 marker 权限 600，稳定表摘要均为 `D41910...B237A`。第一次 dump 摘要为 `D6696C...B479E`，第二次为 `9E1242...A6DBC`，第二次摘要与对应操作员输出一致。后验显示隔离库数量从 2 增至 4、测试主库仍为 57/0、周期进程为 0、health/ready 均为 200。

技术可逆门禁已经通过，但“授权一次、实际执行两次”已作为正式操作偏差登记。用户确认两个新隔离库及其证据冻结保留至 Phase 4 验收结束，当前不清理、复用或第三次执行；这些资产必须从 Redis unknown 墓碑测试的准备、恢复点和 cleanup 全部目标中排除，不是其只读准备的前置条件。Phase 4 结束后如需清理，必须另行取得明确的破坏性操作授权；先只读核对两个精确目标与各自 marker、dump 摘要、57/0 和 69 表证据，再分别确认数据库和运行目录的精确清理目标。禁止使用完整随机库名写入本文档，禁止按前缀、通配符或模糊匹配清理；完成后还要只读确认两个目标不存在、测试主库仍为 57/0 且 API health/ready 为 200。

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

`TestEmailUnknownTombstoneSurvivesRedisRestart` 是上述第 5 项的可执行集成门禁。它只允许 `APP_ENV=test` 语义下的进程内 `MockEmailAdapter`，不会创建真实 DirectMail Adapter。测试使用真实 `EmailRepository`、真实测试 MySQL 和真实测试 Redis；运行前强制要求 `schema_migrations=57/dirty=0`、显式操作员 ID和双重开关。phase1 启动前，状态文件目标必须不存在；只有 phase1 成功原子创建状态文件后，phase2 与 cleanup 才要求它是当前用户独占的普通 600 文件。测试 MySQL DSN 必须与生产全局连接保持 `loc=Local`，并在任何夹具写入前把 `UTC_TIMESTAMP()` 的墙钟字段重新解释为 UTC，确认与应用 UTC 当前时间偏差不超过 5 秒；禁止使用 `loc=UTC`，否则仓储的 UTC 墙钟转换会再次偏移。状态文件不保存完整邮箱、幂等键或业务号，只记录本轮随机 nonce、Redis `run_id`、原日志精确主键，以及可选的意外新-key日志精确主键；新增可选字段不改变 version 1，现存 phase1 状态文件可继续读取。000057 冻结保留的隔离库和运行证据不属于本测试状态文件描述的资产，也不得进入本测试的任何目标集合。

2026-07-30 的远程准备只启动一次 SSH 只读对账，未重试，也没有写入、重启或清理。分项检查确认 API 单实例且 health/ready 为 200、MySQL/Redis 容器唯一、主库 57/0、UTC 偏差不超过 5 秒、既有状态文件为安全普通 600 文件、两条 owned unknown 日志同 scope、模板和白名单各 1 条、Redis 只读命令成功，以及两个 000057 证据目录元数据通过。但最终 `cycle_exclusion` 聚合失败且最终摘要未输出；因此两个隔离 schema 是否都存在并已排除、状态文件精确 phase、`run_id` 变化、锁 key 的 `EXISTS` 值及孤儿目录数量均未确认。分项通过不能拼接成总门禁通过。

历史失败证据（已被后续只读门禁 PASS 取代）：该 runner 存在 PowerShell stdin UTF-8 BOM 缺陷，远端首行 `set -u` 未生效，当时总门禁未通过，禁止进入新的 phase1、Redis 重启或 phase2。后续替代运行器已消除 BOM 并完成正式只读对账；本段只保留事故与修复缘由，当前状态以下方“最新正式远程只读门禁”为准。

上述缺陷的正式替代资产现已在本地建立，但尚未连接测试服务器：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File .\scripts\run-email-unknown-remote-readonly-gate.ps1 `
  -SelfTest

python -B .\tests\email\email_unknown_remote_readonly_gate_contract.py
```

`run-email-unknown-remote-readonly-gate.ps1` 不再使用 PowerShell 管道或 `Process.StandardInput` 提交远端脚本。Windows PowerShell 5.1/.NET 在访问默认 StandardInput StreamWriter 时可能先写 UTF-8 BOM，即使随后访问 BaseStream 也无法证明首字节。正式运行器改为在本轮唯一、关闭 ACL 继承且仅当前 Windows 身份可访问的临时目录写入无 BOM `stdin.bin`，通过模块限定的 `Start-Process -RedirectStandardInput` 把文件句柄交给固定 `ssh.exe` 和冻结参数；stdout/stderr 分别进入同目录固定文件。进程有 120 秒整体超时，超时只终止精确子进程；finally 仅删除四个冻结叶文件并在空目录条件下删除本轮目录。SelfTest 使用真实 Windows 子进程重定向读取长 stdin，并以 20 个用例动态验证退出码 0 与 7 均原样穿透；离线契约同时冻结 Handle 必须在 WaitForExit 前取得、ExitCode 为 null 必须失败关闭，并拒绝直接 `[int]$process.ExitCode`。只有实际首字节为 115、无 BOM、早期失败 stderr 为空、退出码契约和摘要均固定时才通过。载荷首字符、UTF-8 BOM、UTF-16 BOM、NUL 和首行 `set -Eeuo pipefail` 都有显式门禁；SSH 不自动重试，任意 stderr、非零退出、额外 stdout、字段缺失或重排均失败关闭。远端 payload 只允许固定 GET health/ready、MySQL `SELECT`、Redis `PING`/`INFO server`/单一精确 `EXISTS`，以及受限的 `stat`/`find` 元数据读取。

修复后的 `cycle_exclusion` 不再使用嵌套 `sh -c`。外层 Bash 从恰好两个完成 marker 路径分别派生 32 位十六进制后缀，确认两个目标互异、不是主库且不出现在状态文件中，再逐个只读查询 `information_schema.schemata`；只有两个 schema 都存在时才累计 `cycle_schema_count=2` 和 `cycle_excluded_count=2`。成功和失败正则均使用 `\r?\n?\z` 绝对结尾；双 LF、双 CRLF、额外行均拒绝。失败 stdout 只有完整匹配单行白名单且 stderr 为空时，包装器才在 `remote_gate_failed` 安全 JSON 中保留命名组 stage；未知 stage、额外行或任意 stderr 均不得带 stage。本地包装器不会输出远端原始错误、状态文件值、目标名、锁 key、凭据或业务数据。远程固定证据已确认旧聚合 stage 为 `cycle_not_isolated`，但该 stage 同时覆盖六类断言，不能确定具体根因；现拆为 `cycle_target_source`、`cycle_dir_metadata`、`cycle_marker_metadata`、`cycle_dump_symlink`、`cycle_dump_metadata`、`cycle_targets_duplicate`。类型使用 `find -type d/f/l` 证明，`stat` 只比较数值 UID、mode、size，不再依赖 `%F` 的中文或其他 locale 文本。

离线契约覆盖 37 个攻击模型，包括 UTF-8/UTF-16 BOM、CRLF、NUL、Shell 选项缺失、嵌套变量提前展开、引号/分号/空格/换行/通配符及长度异常后缀、缺库、重复目标、额外目标、最终摘要缺失、字段重排、额外输出、stderr、SSH 非零，以及 MySQL/Redis/HTTP 写命令注入。新增模型冻结历史状态文件 `/home/pc/molin-email-unknown-<32位小写十六进制>.state` 的精确匹配、孤儿目录近似名排除、health/ready 仅接受 HTTP 200、000057 dump 在 schema 查询前显式拒绝符号链接，以及“线上测试 API 必须非 Mock、独立 phase1/phase2 测试进程必须显式 Mock、持久化模板与发送日志 provider 必须为 `aliyun_directmail`”的三重边界。模板归属同时核验 TemplateId、审核通过、`Code`/`ExpireMinutes` 两个变量、变量完整、本地启用和夹具版本；发送日志归属同时核验 provider TemplateId。两个 root 创建且未向应用账号授权的 000057 隔离库，改由容器内 root 严格只读通道查询；外层只传入已验证、互异且非主库的 schema 名，容器内只生成一条固定 `information_schema.schemata` COUNT SELECT，root 凭据不离开容器。第 35 项模型覆盖“应用账号不可见但 root 可见 2/2”以及写 SQL 失败关闭。一次正式只读对账返回 `stdout_length=34` 与 `stderr_length=43`；34 字符同时可能对应 `shell_options` 和 `primary_query` 等长摘要，不能单凭长度定位 stage。QA 后续确认正式运行器的默认 StandardInput StreamWriter 会注入 BOM；第 36 项现由真实 Windows 文件重定向子进程验证首字节 115、无 BOM、固定早期失败摘要和 stderr 为空。第 37 项拒绝中文、英文及其他 locale 类型文本进入文件元数据判断。Shell 选项自检仍使用不依赖 locale/文本格式的 `shopt -qo`，失败函数会先耗尽剩余 stdin。成功摘要仅输出固定 `live_adapter_mock=false`，不会暴露真实 Adapter 配置值。`SelfTest` 与离线契约通过只证明本地资产结构，不代表远程只读总门禁已通过；真实运行仍需新的单次只读授权。

最新正式远程只读门禁已在严格一次、未重试的窗口中完整 PASS。固定安全摘要确认：API 单实例，health/ready 均为 true，线上 Adapter 已退出 Mock；MySQL/Redis 容器各 1，主库 schema 57/dirty 0，时钟门禁通过；历史状态文件安全且处于 `phase1_created`，原 unknown 日志 1 条、意外新 key unknown 日志 1 条、同 scope 共 2 条，模板与白名单归属各 1；Redis PING 通过、`run_id` 已变化、精确锁 key 不存在；孤儿目录为 0；两个 000057 周期证据的 evidence/valid/schema/excluded 计数均为 2；本次写入、重启和清理均为 false。摘要不包含邮箱、业务号、锁 key、数据库名、凭据或其他敏感值。至此 Redis unknown 只读准备门禁记为通过；下一步只能在新的人工授权下对历史 Redis unknown 夹具执行精确 cleanup，且不得清理、修改或纳入任何 000057 隔离库及其证据资产。

先在仓库根目录分别以普通模式和 Python 优化模式执行完全离线的静态门禁；它只读取 Go 测试源码，不连接 MySQL、Redis、服务器或网络，也不会执行 cleanup：

```powershell
python -B tests/email/email_unknown_restart_integration_contract.py
$env:PYTHONOPTIMIZE='1'
python -B tests/email/email_unknown_restart_integration_contract.py
$env:PYTHONOPTIMIZE=$null
```

该契约要求 cleanup 在任何 Redis 或数据库访问前完整验证状态：固定为 `version=1`、`phase=phase1_created`，nonce/run_id 格式合法，操作员、模板、白名单、原日志、意外日志五个主键均为正数，且两个日志主键不同。Redis 只允许在数据库事务前后各执行一次精确 `EXISTS` 并要求结果为 0，禁止 `DEL`。数据库必须在同一事务内以 `FOR UPDATE` 锁定同 scope 恰好两条日志，并用完整冻结谓词锁定两条日志、一条白名单和一条模板；模板变量归属必须由 `JSON_LENGTH=2` 和两个 `JSON_CONTAINS` 精确限定为 `Code`、`ExpireMinutes`，禁止回退为 JSON 字面相等或允许额外变量。删除阶段复用相同谓词，逐项要求 `RowsAffected=1`，任何后续失败都必须回滚。仅全部数据库清理和 Redis 后验成功后，才能唯一一次删除状态文件。

离线契约当前覆盖 23 个单点攻击模型：阶段降级、遗漏意外日志主键、双日志主键重复、移除连接前完整状态校验、接受状态符号链接或错误属主、接受重复键/未知字段/尾随内容 JSON、移除 Redis 前置或后置检查、注入 Redis `DEL`、移除数据库事务或 `FOR UPDATE`、scope 只要求一条日志、削弱完整归属谓词、删除不复用锁定谓词、忽略删除行数、提前删除状态、重复删除状态，以及模板变量回退旧字面相等、缺失必需变量、允许额外变量。任一变异未被拒绝都必须返回 `contract_mismatch`。

真实集成必须在受控测试环境分两次启动 Go 测试。MySQL/Redis 凭据只通过当前测试进程环境注入，禁止写入命令记录、状态文件或报告。以下只列非敏感控制变量，连接变量沿用测试环境既有注入：

```powershell
$env:RUN_EMAIL_UNKNOWN_RESTART_INTEGRATION='1'
$env:EMAIL_UNKNOWN_RESTART_ACK='I_UNDERSTAND_ISOLATED_EMAIL_UNKNOWN_RESTART_TEST'
$env:APP_ENV='test'
$env:EMAIL_ADAPTER='mock'
$env:EMAIL_UNKNOWN_RESTART_STATE_FILE='<当前用户独占的绝对状态文件>'
$env:EMAIL_UNKNOWN_RESTART_OPERATOR_ID='<已存在的隔离测试操作员ID>'
$env:EMAIL_UNKNOWN_RESTART_NONCE='<与唯一stage同源的32位小写十六进制nonce>'
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

任一阶段失败都只输出固定分类并保留状态文件，不输出完整邮箱、幂等键、业务号、Redis key、凭据或原始异常。取证结束或失败恢复时，cleanup 必须作为新的独立命令运行，同时满足原集成开关、原确认短语以及以下清理专用开关和确认短语；缺少任一项只返回 `cleanup_gate_denied`，不执行任何删除。当前历史夹具的 cleanup 只接受完整的 `phase1_created` 状态；意外新-key日志主键不再是可选恢复字段，而是本次精确清理的必需正主键：

```powershell
$env:EMAIL_UNKNOWN_RESTART_PHASE='cleanup'
$env:RUN_EMAIL_UNKNOWN_RESTART_CLEANUP='1'
$env:EMAIL_UNKNOWN_RESTART_CLEANUP_ACK='I_UNDERSTAND_EXACT_EMAIL_UNKNOWN_RESTART_CLEANUP'
go test ./internal/modules/auth/service -run '^TestEmailUnknownTombstoneSurvivesRedisRestart$' -count=1 -v
```

cleanup 先从完整状态派生唯一锁键，只在数据库事务前后各执行一次 `EXISTS` 并要求结果严格为 0；不得执行 Redis `DEL`，避免数据库失败时形成跨系统部分清理。数据库事务先锁定同 scope 恰好两条且主键分别等于状态中的原日志与意外日志，再按完整冻结谓词分别 `FOR UPDATE` 两条日志、一条白名单和一条模板。日志谓词同时匹配主键、模板、供应商模板号、供应商、验证码关联为空、场景、用途、收件人 HMAC、scope、对应 key hash、请求指纹、unknown 状态和失败原因；白名单、模板也必须匹配全部冻结属性。四项删除复用锁定时的同一谓词，严格删除 `2+1+1` 行；任一归属漂移、缺行、多行或错误都回滚整个事务。

数据库事务提交且 Redis 后验仍为 0 后，状态文件才允许唯一一次 `Remove`。若提交后 Redis 后验失败，数据库结果记为未知并保留状态文件供人工对账，禁止自动重试。cleanup 不扫描数据库，不使用 Redis `DEL`、`FLUSHDB`、`FLUSHALL`、`KEYS`、`SCAN` 或模式删除，也不触碰 000057 隔离资产。状态文件缺失或不安全、schema 非 57/dirty0、归属不符或清理数量不精确时均为 `FAIL/BLOCKED`。Redis `run_id` 前后变化只属于 phase2；cleanup 不比较 `run_id`，也不以当前 Redis 实例身份变化作为清理前置。

本轮最新授权 cleanup 在 metadata 阶段以 `metadata_exit_nonzero` 安全失败，`cleanup_started=false`、`postcheck_started=false`，且未重试。该结果不构成真实 cleanup 或 postcheck 通过，修复后二进制的新 Redis `phase1→重启→phase2` 周期仍不得开始。

随后最新一次只读 recovery preflight diagnostic 的本地 SelfTest 为 `cases=34`，远程严格单次 SSH 成功。脱敏结果确认 schema57/dirty0、`migration_rows=1`，两条日志、一条白名单和一条模板均存在，全部字段归属与摘要匹配；副作用固定为 `writes=false`、`backup=false`、`cleanup=false`、`restarts=false`、`retries=0`。因此前次 `metadata_exit_nonzero` 未证明历史夹具状态失效，当前只读门禁已重新通过；但原 cleanup 授权已失败且不得重试，cleanup/postcheck 仍未完成，Phase 4 判定不变。

最新事件复核进一步确认：修复前正式流程只执行一次，并在 `metadata_exit_nonzero` 停止，cleanup 与 postcheck 均未启动。随后一次远端只读诊断 PASS，结果为 `mysql_identity_count=1`、`mysql_compose_label_count=0`，state、recovery、binary、cycle、snapshot 门禁全部通过。根因是正式 metadata 错误依赖 Compose label；实际唯一 MySQL 目标可由容器 `ID|Image|Name` 三元组确定，但没有 Compose label。运维修复后的脚本 SHA 为 `A9DC...E073`，本地 PowerShell AST 错误数 0、SelfTest 33 项、LocalPreflight 9 项、两个 payload 的 `bash -n` 均通过，QA P1/P2=0。

该修复只使 metadata 识别逻辑具备再次执行条件，没有执行真实 cleanup，也没有执行 postcheck。原专项授权已失效且不得复用；必须取得新的明确授权后，才可按 `metadata→cleanup→postcheck` 顺序重新开始。Phase 4 仍未通过。

后续新的单次专项流程中，metadata 与 cleanup 均通过严格成功摘要；精确 cleanup 已删除两条历史夹具日志、一条白名单和一条模板，状态文件已移除，恢复点与两套 000057 周期资产按约束保留，Redis 键未被删除。随后启动的独立只读 postcheck 失败，外层仅记录 `stage=postcheck classification=postcheck_failed`，未重试 cleanup 或 postcheck。纯本地诊断确认分类丢失来自外层 postcheck `catch` 无条件折叠子 runner 失败；修复后仅在退出码、stderr、固定 JSON 形状、分类和远端 stage 全部命中白名单时传播安全诊断，未知输出仍折叠为通用失败。本地 PowerShell AST 错误数 0、授权流程 SelfTest 36/36、独立 postcheck SelfTest 56/56、LocalPreflight 9/9、payload `bash -n` 与 `git diff --check` 均通过，且未联网、未访问数据库或 Redis。由于失败执行只保留了通用分类且临时 stdout/stderr 已按设计清理，上次 postcheck 的真实失败根因仍未知；该结果不得记为 postcheck 或新 Redis 周期通过，下一步只能在新授权下单次执行独立只读 postcheck。

2026-07-31 用户新授权后，postcheck-only 正式入口严格执行一次，但 Windows PowerShell 5.1 空数组折叠导致 SSH 前 `local_gate_failed`；固定计数为 `metadata_ssh=0`、`postcheck=0`、`retries=0`，未访问远端，未执行 metadata、cleanup 或真实 postcheck。该次授权已消费且不得复用。根因已通过共享 `Initialize-RunFiles` 修复，新 runner SHA 为 `9F524238...BE29`；本地自检 24/24、预检 3/3，QA 复核 P1/P2=0。真实 postcheck 仍未执行，下一次必须重新取得单次只读授权；历史 cleanup 保持通过并严禁再次执行。

随后取得新授权并第二次执行 postcheck-only：`metadata_ssh=1`、`postcheck_child=1`、`retries=0`，最终为 `postcheck_failed`，cleanup 未调用。纯本地复核确认 PowerShell `-File` 把两个数组参数按 `hash1 hash2` 位置展开，触发 `PositionalParameterNotFound`；修复后改为两个命名 scalar 参数。三个 SelfTest 分别为 56/56、25/25、37/37，preflight 分别为 3/3、9/9，QA 复核 P1/P2=0；postcheck-only 新 runner SHA 为 `5E69D2E6...5C85`。本次授权已消费且不得复用，真实 postcheck 仍未完成，下一次必须重新取得单次只读授权；历史 cleanup 保持通过并严禁再次调用。

第三次 postcheck-only 在新授权下执行：`metadata_ssh=1`、`postcheck=1`、`retries=0`、`cleanup=0`，精确失败为 `recovery_gate`。静态差异确认 metadata 漏检 `/home/pc` 与 `/home/pc/molin` 父链 owner、符号链接及 group/other writable；修复已把相同门禁前移对齐，未放宽任何安全条件。SelfTest 29/29、Preflight 3/3，QA 复核 P1/P2=0；新 runner SHA 为 `BE0217D5...17C9`。当前不知道远端具体哪一级父链不满足，下一步必须取得新授权先单次执行只读父链诊断，不能直接重试 postcheck；任何 `chmod`/`chown` 均须另行明确授权。历史 cleanup 保持通过并严禁再次调用。

修复 recovery trailer parser 后，identity diagnostic 严格执行一次并返回 `parser_pass=true`、`classification=pass`、`candidate_unique=true`、`file_identity=true`；随后独立 postcheck-only 严格执行一次并返回 `status=pass`、`stage=complete`、`metadata_ssh_attempts=1`、`postcheck_calls=1`、`retries=0`。正式 parser 增加 variable-width、2..8 个空格及数值范围严格白名单，没有放宽身份或父链门禁；本地测试 postcheck 58/58、postcheck-only 29/29、identity 70/70，QA P1=0/P2=0。历史 cleanup 的 2 条日志、1 条白名单、1 条模板精确删除已核验，继续保持通过且不得重跑。该 PASS 不代表新 Redis 周期、RAM 有效权限或最终 QA/PM 已完成；`accepted` 仅表示供应商明确受理，不等于人工收件或最终送达。

## 5. DirectMail RAM 否定矩阵

运维通过安全渠道切换专用最小权限测试 RAM 身份；测试记录只写身份别名和策略版本，不写 AccessKey。每次先快照模板镜像版本/missing、验证码/发送日志状态与 Adapter 指标，再执行单个 Deny，最后恢复最小 Allow 并复核。

| 策略 | 流程 | 期望 |
|---|---|---|
| 最小 Allow：仅 QueryTemplateByParam、DescTemplate、SingleSendMail | 基线 | 同步/发送进入业务流程；Create/Modify/Delete 调用计数为 0 |
| 显式 Deny `dm:QueryTemplateByParam` | 同步 | 502/51002；run failed；镜像、版本和 missing 不变 |
| 显式 Deny `dm:DescTemplate` | 同步详情 | 502/51002；整批回滚，无半新半旧镜像 |
| 显式 Deny `dm:SingleSendMail` | 正式 OTP、test-send | 502/51002；OTP 不可用；测试不返回 200/accepted |
| 直接探测 CreateTemplate/ModifyTemplate/DeleteTemplate | 越权否定 | 三者均被 RAM 拒绝，应用运行轨迹从不调用 |

RAM 门禁现按[最小权限验收说明](./directmail-ram-minimum-permission-probe.md)执行：官方已确认 `SingleSendMail` 没有 `DryRun`，缺少必填字段的 `SingleSendMail` 或模板写 API 请求不能证明 Allow/Deny，旧缺参真实探针及其 `request`/`permission` 结论已废止。后端甲已修复 `directmail_ram_probe_test.go`：真实探针只调用 QueryTemplateByParam、DescTemplate 两个 read action；SingleSendMail、CreateTemplate、ModifyTemplate、DeleteTemplate 等副作用 action 无法安全构造，旧 Deny 用例在进入 Adapter 前失败关闭。四个官方权限码仅按完整字符串精确匹配，未知码不猜测、不扩展白名单；专项 `go test`、`go vet` 均 PASS，QA 复核 P1/P2=0。

2026-07-31 已用当前 `directmail_ram_probe_test.go` 源码 SHA `987D8859...F953` 在测试服务器的冻结 Go 镜像中构建一次性二进制 SHA `AAC7C92F...F4A0`。离线安全测试先通过，随后真实探针固定输出 `RAM_PROBE PASS mode=minimum_allow reads=true send_allow=existing_authorized_evidence_required deny=external_diagnosis_required`，证明同一运行身份的 QueryTemplateByParam、DescTemplate 两个 read action 均成功；本次没有调用 SingleSendMail 或任何模板写接口，也没有访问数据库或 Redis。源码包、二进制、远端构建目录和本地归档均已精确回收。

最小 Allow 仍须把上述两个读 action 成功证据、既有真实 `accepted` 证据与有效策略快照和 RAM 权限审计关联。Create/Modify/Delete 及显式 Deny 必须由权限审计或既有 `RequestId` 的 OpenAPI Troubleshoot 诊断证明；Chrome 权限审计因本机原生通信缺失暂未完成，当前尚未取得权限审计/RequestId 证据，因此 RAM 最终门禁为 `PARTIAL / BLOCKED_BY_AUTH`。补证无需新增真实邮件，也不得构造有副作用的真实请求；如需其他真实 API 拒绝测试，必须另行授权并先证明请求无副作用，否则不得执行。

平台 RBAC 拒绝必须为 403/40003，不能与 RAM 的 502/51002 混淆。响应、日志、审计和 telemetry 还需执行：

```powershell
python tests/email/sensitive_scan.py <文件或目录> --repo-root . --allow-domain example.invalid
```

扫描器递归读取文本源码、日志和前端构建产物，只输出固定级别、分类、仓库相对路径和行号，不回显命中内容。AccessKey/Secret、JWT、Refresh Token、私钥、生产源码中的六位 OTP，以及运行时日志/JSONL 内未脱敏的完整邮箱、手机号或供应商正文判为 `FAIL`；源码中的完整真实域名邮箱/手机号和 debug code、供应商 raw/message/正文输出面判为 `REVIEW`，需要结合上下文确认是否为合成数据或受环境门禁的安全分支；`example.invalid` 等保留占位域、约定测试手机号、测试/文档中的合成 OTP 和文档术语只判为 `INFO`。`.env`、`.env.local`、`.env.test`、`.env.production` 固定拒绝读取并报告 `protected_env`，仅允许扫描无秘密的 `.env.example`。可重复传入 `--show-level FAIL --show-level REVIEW` 抑制 INFO 明细，但汇总仍统计全部级别。先运行 `python -B tests/email/sensitive_scan_selftest.py` 验证分类与输出脱敏。

2026-07-29 同一时间窗本地扫描固定证据：扫描 `server`、管理端、用户端、`tests`、`docs`、`infra`、`scripts` 共 1003 个文本文件，包含当时存在的两个前端 `dist`。扫描器自测 4/4 通过；结果为 `PASS`，`FAIL=0`、`read_errors=0`、`REVIEW=3`、`INFO=246`。三项 REVIEW 已按最新验收记录完成人工只读复核：分别属于非验证码业务字段、合成脱敏示例，以及受安全非生产环境判定与显式调试开关共同约束的验证码响应载体；未发现已知真实秘密值。该结论只关闭该时间点工作树的静态字面量复核，不替代运行时日志、真实响应、数据库、审计、telemetry 和部署产物的发布前同一时间窗复扫。

本轮最新离线总回归中，`go test ./...` 与 `go vet ./...` 均通过；全工作树敏感扫描为 `FAIL=0`、`protected_env=0`、`read_errors=0`。运行时同一时间窗扫描已由正式六表面 PASS 独立关闭；后续 Redis unknown fresh cycle 与 000055/000056 真实隔离 MySQL 矩阵也已关闭并禁止重复。四项负责人豁免均保持“未技术验证”，当前最终 QA/PM 书面签署仍未关闭。

2026-07-31 最新安全续跑中，Python 进程内矩阵 normal/`-O` 均为 9/9；当前 Go 源码通过一次性容器构建执行 `TestPhase4*`，8 个顶层测试全部 PASS。构建过程没有注入集成开关，未连接数据库、Redis 或 DirectMail；源码包、测试二进制、远端构建目录和本地归档均已回收。该结果继续只证明离线业务规则，不关闭真实重放、过期、并发、unknown、冷却或五业务流 E2E。

同一续跑末尾再次执行扫描器 SelfTest 4/4 和全工作树静态扫描：覆盖 1083 个文本文件，`FAIL=0`、`REVIEW=3`、`INFO=265`、`skipped_protected_env=0`、`read_errors=0`。三项 REVIEW 为一处既有完整手机号示例和两类受环境/显式开关约束的调试回码响应表面，没有新增秘密值。运行时六表面已有独立正式 PASS，本次静态扫描不重跑也不取代该证据。

### Phase 4 剩余门禁机器清单

`phase4_remaining_gates.json` 是 2026-08-02 当前 QA/PM 状态快照：固定 19 个已关闭门禁、0 个未关闭门禁。13 项具有技术 PASS 证据；RAM 有效权限、五场景真实重放/过期、模板测试发送真实故障矩阵和五业务流真实外发 E2E 共 4 项为 `waived_by_project_owner_not_verified`；QA 与 PM 均为 `passed_with_project_owner_waivers`。Redis unknown fresh cycle 与 000055/000056 隔离矩阵均标记 `must_not_repeat=true`。

最新单次 migration 恢复已真实使用 SSH 2、`scp.exe -O` 1、零重试，结果仍为 `partial55_execution`、退出码 2；因此 `/usr/bin/wc` 已被排除为根因，不能把此前静态差分当作通过证据。新 Stage 保留、临时容器已精确移除。partial55 runner 现把预检细分为七个有序白名单阶段，远端入口只解析固定四行失败摘要并输出脱敏 `partial55_*` 分类；其他形状统一失败关闭。当前机器状态为 `partial55_precheck_instrumented_recovery_authorization_required`，000055/000056 真实矩阵仍未通过。

精确观测恢复的下一次正式执行严格使用 SSH 2、`scp.exe -O` 1、零重试，返回 `partial55_boundary_manifest_shape`、退出码 2；当前 Stage 保留，临时容器已精确删除。前六项预检已真实通过，目标库尚未创建。根因是 000055/000056 partial runner 的 boundary awk 相邻 action 缺少独立 `{...}`，Bash `-n` 无法发现、真实 awk 会报语法错误。两处现均改为 `{seen[$2]++}`，契约会实际执行正式 awk；本地 normal/`python -O` 攻击模型为 34/39。机器状态更新为 `partial55_boundary_awk_fix_recovery_authorization_required`。

修复后的精确恢复在第一次 SSH 被 `partial55_stderr_pair` 门禁阻止，SCP 0、零重试，Stage 原样保留且未触发 Docker/数据库。外层 SSH stderr 为空不代表 Stage 内 `partial55.stderr` 为空。只读诊断资产现支持 `awk_syntax` 固定分类，payload SHA 为 `A9E03452DE555B9E02F61B37399277C2E22CB8ACFEB0A25960FB15B0295B4B4D`；normal/`python -O` 32 个攻击模型及 SelfTest 通过。下一步仅允许一次纯只读分类。

该纯只读分类已严格执行一次并确认 `partial55_failure=boundary_manifest_shape`、`case=none`、`target_created=false`、`stderr_class=awk_syntax`，资产与 matrix55 成功证据完整；SSH 1、零重试、无写入、无 Docker/数据库访问。恢复清理器现只接受大小受限且全部行符合 awk/mawk/gawk 固定语法错误外壳的 stderr。机器状态为 `partial55_boundary_awk_stderr_fix_recovery_authorization_required`。

最终精确恢复及隔离矩阵随后一次通过：SSH 2、`scp.exe -O` 1、零重试；`baseline_generation`、`full55`、`partial55`、`full56`、`partial56` 全为 true，目标总数 94，临时容器全部移除、Stage 不保留、主库未修改。机器清单已把 000055/000056 移入已关闭门禁并标记 `must_not_repeat=true`。

`phase4_remaining_gates_contract.py` 使用结构化 JSON 解析，不依赖文案搜索；精确冻结门禁集合、状态、proof、授权和未知字段边界。normal/`-O` 攻击模型覆盖伪造整体通过、删除或重复门禁、替换 proof、允许执行、伪造授权、把任一负责人豁免篡改为技术通过、签署 QA/PM、重跑已冻结门禁、改变 `accepted` 语义和添加旁路签署字段。该资产只防止误签；QA/PM 仍须分别书面签署，不能用修改 JSON 代替角色结论。

### 5.1 Phase 4 运行时同一时间窗敏感扫描

`phase4_runtime_sensitive_scan.py` 是发布验收阶段的只读聚合扫描器。它要求受控采集器先生成一个 UTF-8 JSON manifest，并完整提供以下六个证据面：

1. 公开 GET 与管理 GET 响应安全投影，两个角色缺一不可；
2. 应用日志；扫描器把文件内容读入内存判定，不复制或回显日志；
3. 审计安全投影；禁止原始 `details` 或可还原敏感字段；
4. 数据库安全投影；只接收冻结白名单中的计数、状态和布尔检查字段；`user`、`admin`、`business`、`provider`、`template`、`recipient`、`client_ip`、`provider_request_id` 等可还原或高基数字段即使声称已哈希也不得进入投影；
5. Prometheus/telemetry 文本；只允许 `email_adapter_calls_total` 的 21 个固定 `operation/scene/result` 组合，拒绝额外标签、未知值和高基数序列；
6. 已部署前端完整目录；以全部文件数量、文本文件数量和确定性 tree SHA-256 闭合，避免只抽取部分资源。

manifest 的 `schema` 固定为 `molin.phase4.runtime-sensitive-scan/v1`。`window.start_utc`、`window.end_utc` 及每份证据的 `captured_at_utc` 必须是秒级 `Z` 格式 UTC，窗口最长 30 分钟；全部证据必须绑定同一个 40 位 Git SHA 或 64 位部署 SHA。普通证据文件必须给出 SHA-256，路径必须相对 manifest 所在目录且不能穿越目录或使用符号链接。结构化响应、审计和数据库文件必须是严格字段白名单的安全投影 JSON，不允许用原始响应或审计 `details` 冒充投影。

证据包契约限定为 `trusted_local_closed_v1` 可信本地 collector 创建的封闭普通目录。collector 完成采集后必须把目录链和普通文件设为只读、确认无 symlink/reparse，并独立冻结 manifest SHA-256、部署 SHA 和 bundle 文件身份摘要；三个值必须通过 CLI 单独传入，不能只信任 manifest 自报。正式扫描器仅支持具备 `dir_fd/openat` 的 POSIX/Linux：它在任何证据路径 `lstat/resolve/open` 前先执行平台门禁，并同时按 Windows 和 POSIX 词法拒绝盘符、UNC、rooted、ADS、反斜杠、设备路径和保留设备名；通过后只使用逐级目录描述符与 `openat/O_NOFOLLOW`。前端目录树也只能从已打开并持续持有的 bundle/tree 目录描述符开始，以 `os.scandir(fd)` 枚举，并通过父目录描述符打开子目录和文件；枚举和身份复核完成前不得释放对应描述符。非 POSIX 固定返回 `platform_not_supported`，不得尝试用 Python 证明 Windows ACL，也不得形成真实扫描通过证据；Windows 仅可运行受控 fixture 验证解析和判定逻辑，POSIX 父目录及前端中间目录并发交换用例明确标记 `skipped_nonposix`。单文件上限 16 MiB、单证据面 32 MiB、前端总量 64 MiB、证据包扫描总量 96 MiB；前端 tree SHA 流式计算，不缓存全部文件正文。

POSIX/Linux 本地离线执行：

```powershell
python -B tests/email/phase4_runtime_sensitive_scan.py `
  --manifest <封闭证据目录>/manifest.json `
  --manifest-sha256 <collector独立冻结的manifest-sha256> `
  --deployment-sha <collector独立冻结的部署sha> `
  --bundle-id <collector独立冻结的bundle身份摘要> `
  --collector-mode trusted_local_closed_v1
python -B tests/email/phase4_runtime_sensitive_scan_contract.py
python -O -B tests/email/phase4_runtime_sensitive_scan_contract.py
```

扫描器 stdout 固定只有一行聚合摘要，stderr 必须为空；任何额外 stdout/stderr、未知字段、缺面、时间越界、部署 SHA 不一致、路径逃逸、文件身份或哈希不一致、完整邮箱/手机号、OTP、Token、Secret、RequestId 原文、私钥或供应商 raw/message/body 命中均失败关闭。无论成功或失败，摘要都固定包含 `writes=false restart=false deploy=false mail_sent=false`，且不输出原始日志、审计正文、文件路径或命中值。

结构化响应、审计和数据库投影使用独立字段白名单；应用日志、telemetry 和前端产物只按非结构化值形态扫描。前端兼容契约直接只读当前 `web/admin-console/dist` 与 `web/user-console/dist`，允许普通 `user/template/provider/admin` 业务键和 `example.com` 等保留示例域，同时要求植入的 JWT、AccessKey、私钥、完整非示例邮箱、完整手机号、裸六位 OTP 和硬编码 secret 赋值全部命中；不会把整个 dist 复制进 fixture。

当前仅完成扫描器与离线攻击契约，不代表测试环境六面证据已经采集或 Phase 4 已通过。远端执行时仍须在同一个已冻结部署时间窗内生成证据包，并由独立 QA 复核固定摘要；扫描器本身不联网、不连接数据库或 Redis、不部署、不重启、不发送邮件。

### 5.2 Phase 4 受控证据采集器

`phase4_runtime_sensitive_collector.py` 是扫描器前置的最小 POSIX/Linux assembler。它不会调用 HTTP、MySQL、Redis、系统服务或部署工具，也不会读取 `.env`。调用者必须显式提供八个已生成且只读的本地来源：公开 GET 安全投影、管理 GET 安全投影、应用日志、审计安全投影、数据库安全投影、完整 telemetry 文本、管理端部署目录和用户端部署目录；另行提供部署 SHA 与一个预先不存在的绝对输出目录。

公开/管理 GET、审计和数据库来源不是供应商或业务接口原始响应。调用者必须先按扫描器冻结字段生成安全投影；collector 会再次执行严格字段白名单、敏感值和高基数检查，再以稳定 JSON 格式写入证据包。任何 `details`、`data`、`raw_response`、供应商正文、可还原标识、完整邮箱/手机号、验证码、Token、Secret 或高基数字段都会失败关闭。telemetry 必须仍是唯一 `email_adapter_calls_total` 指标族的 21 个固定序列。当前工作树只读盘点中的管理端和用户端 `dist` 分别为 65 与 85 个文件，这只是本地构建现状；正式采集必须传入同一部署时间窗内实际部署的两个完整目录，不能用该数量代替部署证据。

所有来源路径、输出路径都必须是绝对 POSIX 路径。六个文件来源、两个前端目录和输出目录之间必须互不相同，且输出不得是任一输入的祖先或后代。每条显式路径的全部组成段以及前端遍历到的每个名称都会在打开前拒绝 `.env`、`.env.*` 等受保护环境文件；因此固定摘要中的 `env_read=false` 表示采集器在本轮没有打开任何这类节点，不是仅靠扫描结果推断。来源叶节点及前端完整树必须为普通只读文件/目录，不得含 symlink 或写权限。

collector 逐级使用目录描述符、`openat` 与 `O_NOFOLLOW`，读取前后复核身份。前端遍历先逐项计入目录和文件共用的全局节点上限，再进入单目录有界排序；同时冻结最大目录项数、最大深度、文件数与字节数，禁止先把未知规模目录全部装载后再计数。输出只会在调用者指定且预先不存在的目录创建，成功时文件封闭为 `0444`、目录封闭为 `0555`。

每创建一个输出文件或目录，collector 都立即登记其父目录描述符、名称、设备号、inode 和节点类型，并持续持有已创建目录的描述符。输出根目录一旦创建，任何后续失败都保留原始失败分类并返回 `partial_retained=true`；collector 自身不调用 `unlink` 或 `rmdir`，也不尝试自动清理部分输出。原因是 POSIX 按名称删除无法与先前核验的 inode 原子绑定，同 UID 并发交换可能让 `stat → unlink` 删除替换节点。隔离 fixture 只能在 collector 返回后按自身生命周期和独立授权回收临时目录，不能把该责任交给 collector。

POSIX/Linux 组装示例：

```bash
python3 -B tests/email/phase4_runtime_sensitive_collector.py \
  --public-get /absolute/evidence/public-get.safe.json \
  --admin-get /absolute/evidence/admin-get.safe.json \
  --application-log /absolute/evidence/application.safe.log \
  --audit-projection /absolute/evidence/audit.safe.json \
  --database-projection /absolute/evidence/database.safe.json \
  --telemetry /absolute/evidence/email-metrics.prom \
  --admin-frontend /absolute/deploy/admin-console \
  --user-frontend /absolute/deploy/user-console \
  --output /absolute/output/phase4-runtime-bundle \
  --deployment-sha <40位GitSHA或64位部署SHA>
```

成功 stdout 只有一行安全摘要，其中 `manifest_sha256` 与 `bundle_id` 是后续扫描器必须独立传入的冻结参数；stderr 必须为空。不要从 manifest 自报字段反向替代这两个参数。离线契约：

```powershell
python -B tests\email\phase4_runtime_sensitive_collector_contract.py
python -O -B tests\email\phase4_runtime_sensitive_collector_contract.py
```

离线契约自身的失败摘要额外返回固定的 `failure_stage` 与 `failure_reason`，两者仅允许预定义小写标签，
不得包含路径、输入值或系统异常正文。任何业务模块导入前，离线契约先核对人工审查后
collector 与 scanner 两个文件的原始字节 SHA-256；任一不一致都必须直接终止，不能执行
候选模块顶层代码，也不能向 stderr 输出 traceback 或绝对路径；stdout 只能返回一行固定
`preimport_gate` 失败摘要。任何追加、替换、别名传播或反射等价实现都会因字节漂移失败；AST
能力检查仅作为辅助审查，不单独声称完备。离线 `retention_contract` 同时证明 collector 源码
不存在 `os.unlink`/`os.rmdir` 调用，并用多类删除能力变体验证统一字节门禁能够按预期变红；
POSIX 动态契约还会在部分写入失败时劫持删除入口，证明 collector 不会调用自动删除。

契约在真实 POSIX 上覆盖完整组装、扫描器复验、缺面、原始响应/`details`、敏感值、高基数、路径逃逸、`.env`、symlink、来源只读、来源交换、预存目标、partial 故障与清理归属；非 POSIX 只执行语法、静态边界和词法路径契约，并明确标记 `posix_io=skipped_nonposix`，不能作为真实采集通过证据。

### 5.3 Phase 4 同窗口源投影准备器

`phase4_runtime_source_projection.py` 只负责为 collector 准备六个安全文件和两个已部署前端目录，
不会自动启动 collector 或 scanner。无参数固定返回 `disabled`；`--self-test` 只运行内存夹具，
两种模式均不读取配置、`.env`、网络、数据库或远端文件，也不形成持久输出。

真实执行只允许 POSIX 测试环境，并要求固定确认短语、绝对 JSON 配置路径和全部冻结字段。
配置显式提供回环 API、两个权限 `0600` 的 Token 文件、MySQL 客户端、权限 `0600` 的严格 JSON
连接文件、数据库名、捕获器原样收集的 Go 标准应用日志、两个实际部署目录、两个独立权限 `0600` 的前端 manifest、
预先不存在的输出目录，以及不超过 30 分钟的 UTC 起止时间。连接 JSON 只允许 `host/port/user/password/socket`
五个固定键，重复键、未知键、非回环 host、参数前缀、`init-command`、`local-infile` 或 option-file 注入均失败。
任何路径段以 `.env` 开头都会在读取前拒绝；凭据不写入输出、命令行或摘要。

准备器只执行公开 `health/ready/version` GET、管理端邮件概览 GET、内部 metrics GET、冻结 SHA-256
的七条 SELECT、Go 标准应用日志逐行只读和完整前端目录遍历。查询入口同时拒绝多语句、注释、
写入/DDL/权限关键字、`INTO OUTFILE/DUMPFILE`、`FOR UPDATE` 和 `GET_LOCK`。MySQL 固定使用
`--no-defaults` 与显式连接 argv，清空继承环境后只向子进程注入 `MYSQL_PWD`；每个查询会话先设置并读取
`@@session.transaction_read_only=1`，再进入显式只读事务。额外 `SHOW GRANTS` 门禁只接受 `USAGE` 与当前
schema 的 `SELECT/SHOW VIEW`，写权限、其他 schema 或 `GRANT OPTION` 均失败。完整命令模板与七条查询分别冻结 SHA-256。
`MYSQL_PWD` 仍可能在 mysql 子进程短暂存续期间被 root 或同 UID 进程读取，因此只能在隔离测试账号下短时运行，
不能据此声称达到生产凭据隔离要求。
数据库检查不受 30 分钟窗口缩小，
而是聚合检查当前全部邮箱验证码及五张邮件业务表；任一必需类别为零、结构不兼容或脱敏/哈希
断言失败都会关闭。审计动作允许点号，但只映射到 `template_status`、`scene_binding`、
`template_sync`、`allowlist`、`test_send`、`bootstrap` 六类；未知动作失败且绝不输出原始 action。

应用日志只接受非空普通只读文件。普通行必须严格符合 Go 默认 logger 的
`YYYY/MM/DD HH:MM:SS message` 格式；此外只允许唯一一条 `app.Run()` 输出的固定无时间启动行，
地址只接受本机监听白名单且端口必须为 `8080`。`bootstrap.NewApp()` 可能先输出带时间的 security、
token gateway 或 workbench 启动告警，所以固定启动行不要求是首行，但必须早于任何 HTTP 请求日志；
重复启动行、伪地址、错端口、请求后才出现启动行，或启动行之后的其他无时间行都失败关闭。
GORM 1.31 默认彩色 Warn logger 的慢查询是唯一允许的多行例外：必须连续出现固定 `CRLF` 前缀、
位于 Go 模块根 `molin/server/` 或等价部署根 `/home/pc/molin/server/` 下的 `.go:行号` 首行，以及带 ANSI 颜色序列的 SQL 续行；首行时间仍须
落入同一捕获窗口。SQL 只允许无分号、无注释、无文件读取、无锁和无延时函数的单条 `SELECT`。
写 SQL、伪造源码路径、残缺块、无前缀块、乱序块及窗口外块全部失败关闭；块内每条原始行仍在
结构解析前先执行敏感扫描，且慢查询正文不会进入安全投影输出。
这是捕获器原样追加 stdout/stderr 后的真实格式，不再接受 journald JSON。Go 时间戳不携带时区，准备器必须读取执行主机在 UTC 窗口起止点的本地偏移，
要求两端偏移一致后再换算为 UTC；因此 UTC+08:00 主机上的本地时间不会被误当成 UTC，跨夏令时偏移窗口也会失败关闭。
准备器对每一条完整原始行先调用 SHA-256 冻结的
`phase4_runtime_sensitive_scan.py::contains_sensitive`，再检查 64 KiB 单行上限、UTF-8、唯一 LF 结尾、
时间戳和消息格式，再要求每条带时间记录都位于 UTC 窗口，最后才按业务相关性分类；因此窗口外、
无时间、畸形、超长和不相关记录都不能绕过敏感扫描，窗口外记录即使不敏感也失败关闭，
同时窗口内相关业务记录仍必须大于零。任一敏感命中或相关记录
为零均失败。`/dev/null` 不是普通文件，因此明确失败，不能用空日志伪造通过。契约在导入准备器前
还会先核对准备器原始字节 SHA；恶意 projection 顶层 sentinel、scanner SHA 漂移和 scanner 读取异常
在普通模式与 `python -O` 下都必须保持固定 stdout、空 stderr 和固定退出码，且不得执行候选顶层代码。

原始响应、数据库明细和原始日志只在内存解析；落盘仅包含固定字段、低基数计数及 21 条固定
metrics，禁止完整邮箱、手机号、OTP、Token、RequestId、provider raw 或响应正文进入投影。

六个固定文件为 `public-get.safe.json`、`admin-get.safe.json`、`application.safe.log`、
`audit.safe.json`、`database.safe.json`、`email-metrics.prom`；两个前端输入仍使用配置中的部署目录。
目录遍历仅在 POSIX/Linux 运行，从绝对路径根逐段使用目录描述符、`openat/O_NOFOLLOW`；每层逐项
收集目录项，读取到 `MAX_DIRECTORY_ENTRIES+1` 时立即失败，通过上限门禁后才排序。遍历同时执行
前后 inode/模式/大小/mtime 身份复核；symlink、节点交换、空目录、两文件子集、缺少 `index.html`、
`assets/*.js|mjs` 或 `assets/*.css` 都会失败。每个独立 manifest 固定 `role`、`tree_sha256`、`file_count`、
`byte_count` 与 `container_or_image_digest`，必须由独立的只读容器根盘点流程产生；准备器不会生成 manifest，真实运行不接受
用待验证目录临时自报的 manifest，且逐项不匹配即失败。

首次 Linux 全新 stage 的 normal 契约真实失败：`exchange_tree` 攻击夹具把 `os.scandir` 替换为不支持
上下文管理和 `close()` 的普通 `list_iterator`，导致 `_bounded_directory_entries` 抛出 `TypeError`，未进入预期的
`frontend_identity_changed` 安全分类；同轮 `-O` 因启动命令 CRLF 问题未执行。该问题已在本地把替身修正为具备真实
scandir 关闭语义，并增加关闭动作断言；在新的 Linux normal/`-O` 实机复验完成前，不得把本地验证描述为 Linux 通过。
受控捕获器会短时把重启后的同一 API 二进制 stdout/stderr 原样追加到 `application.safe-source.log`。
正式顺序必须是 `capture → 在捕获进程存活期间发起固定 GET → restore 封存 → source projection`：
GET 所需 Token 只能由受控请求进程从权限 `0600` 文件在内存读取，不得进入 argv、环境变量或应用日志；
当前 capture payload 本身不会主动发起 health、admin 或 metrics 请求，不能把仅有启动行的文件冒充已 priming。
restore 恢复原进程后才把日志封闭为 `0400`；正式投影窗口必须覆盖上述完整捕获时段并包含相关 API 记录。前端容器无
mount，正式运行前还需只读完整导出 `/usr/share/nginx/html`，空目录或缺少 `index.html/assets` 会失败。

```powershell
python -B tests\email\phase4_runtime_source_projection.py
python -B tests\email\phase4_runtime_source_projection.py --self-test
python -B tests\email\phase4_runtime_source_projection_contract.py
python -O -B tests\email\phase4_runtime_source_projection_contract.py
```

配置与日志门禁补齐后，真实测试环境只读准备命令为：

```bash
python3 -B tests/email/phase4_runtime_source_projection.py \
  --execute --confirm I_CONFIRM_PHASE4_RUNTIME_SOURCE_PROJECTION_READONLY \
  --config /absolute/readonly/phase4-source-projection.json
```

成功摘要中的 `deployment_sha` 由 API `version` 的 UTF-8 字节、实际监听 8080 的唯一 Linux PID/starttime、
`/proc/<pid>/exe` 设备号/inode/二进制 SHA-256，以及两个完整前端目录和独立 manifest 一起派生，
不接受配置任意文本。监听归属从 `/proc/net/tcp{,6}` socket inode 与 `/proc/*/fd` 唯一反查；落盘前会
再次复核 PID/starttime/exe/二进制、版本、连接文件、manifest 并重新遍历两个完整目录，身份漂移立即失败。30 分钟
窗口只约束捕获时刻与应用日志范围，不用于制造数据库或审计零记录通过。人工把六个固定文件、
配置中的两个部署目录及该摘要值分别传给 collector；
准备器不会自动拼接或执行下一条命令，防止门禁失败后继续采集。

以上通过只证明本地编排边界，不代表远端六表面已采集或 Phase 4 已通过。

### 5.4 Phase 4 捕获期日志 priming

`phase4_runtime_log_prime.py` 只用于受控 API 捕获进程存活期间依次触发五个只读请求：
`health`、`ready`、`version`、`admin/email/summary`、`internal/metrics`。无参数固定关闭；
`--self-test` 只验证内存解析夹具，不联网、不读配置、不写文件。真实模式只接受固定确认短语、
权限 `0600` 的严格 JSON 配置文件、回环 `127.0.0.1/localhost:8080`、两个权限 `0600` 的 Token 文件，
以及捕获期属主正确、权限 `0600` 的普通非符号链接 `application.safe-source.log`。

priming 原始字节绑定 source projection SHA-256
`2BC04F38C2E5073B5FE390C83394F16ACC46B0C6B353834A848EEC5487F606AB`，只有门禁通过后才复用其
唯一 8080 监听进程身份、版本解析和 21 条 telemetry 序列契约。配置拒绝重复键、未知键、非 UTF-8、
非绝对 POSIX 路径、`.env` 路径、路径复用、非回环地址和超过 30 分钟的窗口。Token 只从已打开的
`O_NOFOLLOW` 文件读入内存并作为 HTTP header 使用，不进入 argv、环境变量、stdout 或 stderr。

HTTP 固定使用 GET、显式禁用环境代理、禁止重定向和自动重试，连接与完整正文共享 10 秒硬期限，单响应上限 1 MiB。
五个响应必须均为 200；三个公开响应、邮件概览必须符合固定安全 JSON 字段，metrics 必须严格通过
既有 21 序列契约。执行前后分别复核唯一 8080 API 的 PID/starttime/可执行文件身份，以及日志的
device/inode/owner/mode；日志大小必须严格增长，UTC 窗口在请求前后都必须有效。成功 stdout 只有：

```text
status=pass mode=runtime_log_prime public=pass admin=pass internal=pass requests=5 writes=application_log_only env_read=false
```

失败不输出响应正文、Token、路径或原始异常；由于失败前可能已经完成部分 GET 并写入应用日志，
失败摘要保守声明 `requests=not_completed writes=application_log_possible`，不能误报零副作用。

正式操作顺序仍为：先由捕获器进入 capture 状态，再运行 priming；priming 成功后才运行 restore 把日志
封闭为 `0400`，最后运行 source projection。priming 不负责 capture、restore、数据库、Redis、部署或发信。

```powershell
python -B tests/email/phase4_runtime_log_prime.py
python -B tests/email/phase4_runtime_log_prime.py --self-test
python -B tests/email/phase4_runtime_log_prime_contract.py
python -O -B tests/email/phase4_runtime_log_prime_contract.py
```

最新修复后的冻结 SHA-256 为 projection `2BC04F38...606AB`、projection contract
`17A5BEB2...8CC17`、scanner `BDF32624...E2A3`。最终版本的 Windows normal/`-O` projection
contract 均为 126 项通过，且最终版本已在 Linux 完成真实 source projection；Linux normal/`-O`
的 126 项契约结果来自最终 MySQL 十六进制比较修复前的版本。runtime log prime contract 在 Windows
为 48 项、Linux 为 52 项，两个平台的 normal/`-O` 均通过。运维资产契约 normal/`-O` 为 43 项，
API 日志捕获启动器 SelfTest 8 项、前端导出启动器 SelfTest 9 项均通过。这些结果证明修复资产和平台
文件门禁；最终远端六表面是否完成，以紧随其后的正式采集和扫描记录为准，也不单独关闭 RAM、Redis、
000055/000056 或 QA/PM 总门禁。

2026-07-31 最终运行时执行中，source projection 六面通过并冻结 deployment SHA；collector 组装
157 个文件、4036542 字节，scanner 实际扫描 156 个文件、4033941 字节，固定结果为六面 6/6、
findings=0、window/deployment 均绑定，且 writes/restart/deploy/mail_sent 均为 false。临时 MySQL
账号、Token、连接文件和捕获环境快照均已精确回收；无凭据 bundle、六面安全投影和部署前端导出保留。

### 5.5 全新 Redis unknown 周期执行器

`scripts/email-unknown-fresh-cycle.payload.sh` 是下一次单次授权使用的固定远端 payload，动作顺序为 `preflight`、`phase1`、`restart`、`phase2`、`cleanup_verified`、`finalize`。它只允许 `docker restart "$redis_id"` 重启经唯一容器名和 API Redis `run_id` 双重绑定的 `molin-redis`，主库只允许 schema、操作员和精确清理后验查询；不会执行 migration、down、结构修改或整库备份。

`scripts/run-email-unknown-fresh-cycle.ps1` 默认关闭。`-SelfTest` 完全离线；真实模式必须显式提供 `-Execute`、SFTP 专用单次确认词和冻结的 Linux ELF 测试二进制，操作员 ID 已固定在控制器内。控制器先创建唯一 nonce stage，再用 OpenSSH 9 默认 SFTP 分别上传二进制和 payload；第二次 SSH 复核普通文件、非链接、属主、无特殊位权限、冻结 SHA 和大小后，归一为 `0500` 并按固定顺序执行全部动作。退出码非零、stderr 非空或完整摘要不匹配都会立即停止、重试数保持 0，并保留唯一 stage 供重新授权后诊断。只有 `finalize` 通过后才精确删除 stage 内唯一剩余的 payload 和空目录。

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/run-email-unknown-fresh-cycle.ps1 -SelfTest
python -B tests/email/email_unknown_fresh_cycle_payload_contract.py
python -O -B tests/email/email_unknown_fresh_cycle_payload_contract.py
```

本地契约固定拒绝真实 Adapter、固定容器名绕过、schema 漂移、controller nonce 脱绑定、Redis `FLUSHDB/FLUSHALL/KEYS/SCAN`、Redis `DEL/UNLINK`、通配文件删除、关闭 SSH 主机密钥校验、忽略 stderr、自动重试、递归 stage 清理、缺少 LF 规范化、编码回退、篡改冻结 SHA/大小/传输次数以及在摘要输出 operation identity。当前 35 个攻击变异、Bash 语法和 PowerShell 5.1 实际 stdin 传输 SelfTest 均通过。2026-08-01 的最新单次授权执行在 `upload_binary` 阶段停止：`exit_code=1`、`stdout_length=0`、`stderr_length=56`、`retained=true`、`ssh_attempts=1`、`scp_attempts=1`、`retries=0`。未进入 phase1、Redis 重启、phase2 或 cleanup；现场按门禁保留，再次诊断或执行必须取得新的专项授权。

针对该上传失败现场，新增默认关闭的纯只读诊断资产 `email-unknown-upload-failure-readonly.payload.sh`、`run-email-unknown-upload-failure-readonly.ps1` 和 `email_unknown_upload_failure_readonly_contract.py`。正式路径固定一次 SSH、零 SCP、零重试，不访问 MySQL/Redis，不写入、不清理、不重启；只核验唯一 Stage 的属主/权限/inode 稳定性、空目录或固定名称二进制的大小分类与冻结 SHA、远端 SCP 工具身份以及文件系统容量分类，输出不含路径、nonce、文件原文或 stderr 原文。首次授权启动暴露控制器为无 BOM UTF-8，PowerShell 5.1 将正式分支唯一赋值语句并入中文注释，因 `$result` 未定义在 SSH 前失败；AST 证明 `ssh_attempts=0`，远端未读取、未改变。控制器现已改为 UTF-8 BOM，并在 SelfTest 中用 PowerShell 5.1 AST 强制确认正式分支恰有一条 `$result = Invoke-OneSSH` 语句。normal/`-O` 21 个攻击模型、Bash 语法、SelfTest 和默认关闭均通过；三项 SHA-256 为 `8405A10E...388F3`、`897EEAC3...ACF43`、`E0D704B3...3E9D6`。修复后按新单次授权正式执行通过：`classification=upload_failure_stage_empty`、`ssh_attempts=1`、`scp_attempts=0`、stderr0，所有副作用字段为 false，确认唯一 Stage 身份正确、内容为空且现场保持不变。

为该明确空目录新增独立精确清理资产 `email-unknown-empty-stage-cleanup.payload.sh`、`run-email-unknown-empty-stage-cleanup.ps1` 和 `email_unknown_empty_stage_cleanup_contract.py`。载荷只在唯一严格命名目录、`pc:700`、同一 inode、连续两次为空全部成立时执行一次 `/usr/bin/rmdir -- "$stage"`，不删除文件、不访问 MySQL/Redis、不重启、不上传、不重试。normal/`-O` 20 个攻击模型、Bash 语法、PowerShell 5.1 AST SelfTest、默认关闭和敏感扫描通过；三项 SHA-256 为 `6B628FF7...70DC9`、`BED05748...36025`、`8DF7D6F6...212D3`。该资产已按单次授权执行成功：`classification=empty_stage_removed`、`stage_removed=true`、SSH1、SCP0、stderr0、零重试，数据库/Redis/重启均未触碰；授权已消耗。

最新一次新授权执行已通过 stage、上传哈希和只读 preflight，但在 `phase1` 调用处以 `remote_gate_failed` 停止，`retained=true`、`retries=0`。没有 phase1 成功摘要，因此未重启 Redis，也未执行 phase2 或 cleanup。控制器后续版本把固定动作名和随机 operation ID 加入失败摘要，便于下一次授权精确定位；该修复不回溯构造本次已经缺失的 operation ID。本次保留 stage 不得在无新授权时扫描、诊断或删除。

失败现场恢复入口新增 `preflight/cleanup` 两动作和 `cleanup_phase1` 单日志精确恢复。Go、Bash、PowerShell SelfTest 及 recovery normal/`-O` 18 个攻击模型通过；但首次恢复授权只读 preflight 被外层折叠为通用失败，未上传恢复二进制或执行清理。runner 后续只对白名单内的固定 payload stage 分类传播 `remote_stage`，未知 stdout、非空 stderr或其他退出码仍统一失败关闭。该修复不构造本次真实分类，必须在新授权下重新执行只读 preflight。

2026-08-01 更新后的 recovery preflight 在新的单次授权下再次严格执行一次，结果仍为 `remote_stage=unknown`、`retained=true`、`retries=0`。因为没有得到 `state_class=complete` 和 `phase=phase1_created` 的固定 PASS 摘要，控制器没有上传恢复二进制，也没有执行 `cleanup_phase1`、Redis 操作或 stage 删除。该授权已消耗，禁止自动重试。

本地源码复核确认首次 fresh-cycle phase1 存在 nonce 脱绑定，现已把 `EMAIL_UNKNOWN_RESTART_NONCE` 设为 phase1 必填输入，并在任何状态或夹具写入前校验。第二轮加固又修正 allowlist 单数表名，加入 MySQL `--no-defaults`、state `O_NOFOLLOW`/`fstat`/单硬链接门禁，并冻结控制器输入。当前 payload SHA-256 为 `29EAA0B18959D9ABCCDCF10D3793AA6A0C8574B85028714AB7D6EB4E429DEF54`；Linux amd64 ELF SHA-256 为 `1179E29D9F43EFEA79F185E8D2319D015A627F69A48EF9ED7CE22E72BA6AD900`、大小 `25573597`。旧远端 Stage 已精确清理，这些本地结果仍不等于新 fresh cycle 已完成。

后续“保留 stage 只读诊断”授权只执行了一次 SSH 且未重试。独立载荷固定只读检查唯一 stage、固定文件 SHA、严格状态、单数/复数表身份、三条精确夹具归属以及 Redis `PING`、`INFO server` 和一个派生精确 key 的 `EXISTS`；不包含远端落地文件、cleanup、数据库写入、Redis 删除/扫描/重启或真实发信。正式命令返回后，本地控制器因 `$result` 未赋值在脱敏汇总前失败，`finally` 已删除捕获文件，因此没有可恢复的远端 PASS/FAIL 摘要，不能把本轮记为 `retained_stage_reconciled`。原 stage 和夹具继续视为保留，下一次访问必须重新授权。

静态复核确认旧 recovery preflight 把真实单数表 `email_test_recipient_allowlist` 写成了复数 `email_test_recipient_allowlists`，可解释 MySQL 非零退出为何绕过固定 stage 摘要，但这仍是本地缺陷候选，不是本轮远端结论。新只读诊断载荷 SHA-256 为 `8A99E4B52C1B32413B2BAC59C4F3DAC169E37E482145538FB3AC62307644DDF5`；修复后的诊断控制器先固化捕获对象再清理，显式拒绝 null 结果，并加入脱敏失败摘要回归。正式 recovery payload 同步改为单数表名与 `mysql --no-defaults`，schema/operator/fixture/postcheck 查询失败均返回固定白名单分类；控制器先解析固定失败摘要，再对其余 stderr 失败关闭。正式 recovery payload、controller、contract SHA-256 分别为 `B22B4EFF856ACDADA44C4D1D1AF751A0295E6F3B00DA245CE1376A4D54AFB916`、`2777E041E968F51CB8670621D2ADBDB274F3EA51CA763150EB672633072EA0DC`、`783F6A4CE0CA4974E2577FC0F2336E5E284410F616F90DBBF3D7CEEAA81D3D03`；PowerShell AST/SelfTest、Bash 语法及 Python normal/`-O` 23 个攻击模型通过，均未再次联网。最终仓库静态扫描覆盖 1096 个文本文件，汇总为 `FAIL=0`、`REVIEW=3`、`INFO=267`、`protected_env=0`、`read_errors=0`；三项 REVIEW 与既有人工分类一致。

第二次同范围只读授权再次只执行一次 SSH。远端返回后，本地控制器在 `New-Object PSObject` 的捕获对象绑定处得到 null，未能进入脱敏摘要解析；`finally` 已精确删除捕获文件，授权随本次执行消耗且没有重试。正式路径仍没有 SCP、远端临时文件、cleanup、数据库写入、Redis 删除/扫描/重启、migration、RAM 或真实邮件，原 stage 与夹具继续保留。由于原始 stdout/stderr 已删除，不能推断远端 PASS/FAIL 或登记 `retained_stage_reconciled`。

本地动态复现已把捕获结果改为初始化后构造的非空四字段 `PSCustomObject`，SelfTest 固定核验类型、退出码、stdout/stderr 长度、摘要正文和失败分类。修复后的 controller SHA-256 为 `45711793B6CE0E05EB9192B6B299AA778D207EE398095BD7B38C05AF3258E13B`，契约 SHA-256 为 `6FCC290DA662A7F0AA178568DE9B84489AC26EB2E7A38548897F24E778165F3E`；PowerShell AST/SelfTest、Bash 语法和 Python normal/`-O` 22 个攻击模型通过，均未再次联网。

完整本地进程链路随后暴露并关闭两个更深层缺陷。第一，Windows PowerShell 5.1 对 UTF-8 无 BOM 中文脚本按系统代码页解析，部分中文字节会使换行被并入注释，造成等待、退出码读取或赋值语句从 AST 消失；诊断控制器因此固定为 UTF-8 BOM，契约同时强制 BOM 和 PS5 AST。第二，原 SelfTest 夹具首行 `cat >/dev/null` 会把同一 stdin 中后续的 `printf` 与 `exit 2` 一并消费；夹具现直接输出固定失败摘要并退出 2。正式捕获路径使用逐项无空白参数数组和无 BOM stdin 文件重定向，仍仅创建本轮唯一受限临时目录并在 finally 精确删除固定叶文件。最终 payload/controller/contract SHA-256 分别为 `8A99E4B52C1B32413B2BAC59C4F3DAC169E37E482145538FB3AC62307644DDF5`、`778F364910FD493C58E5BC5B7AEE3CC66CF5FD95C9FC525A5CD4F9E7F815A495`、`3852942071DEF7CAD26AB5611B3726DDEC736A78DE46C4AB0E1AF1154AA6637C`。Windows PowerShell 5.1 AST=0，SelfTest 的真实 Git Bash 子进程链路通过，Bash 语法和 Python normal/`-O` 26 个攻击模型均通过；未再次联网，远端摘要仍不可恢复，原 stage 与夹具继续保留。

其余开放门禁的执行资产也完成一次不联网复核。`directmail_ram_effective_evidence_contract.py` normal/`-O` 各通过 15 个攻击模型；更新后的基线生成器为 20 个攻击模型，原隔离包、000055 完整/partial、000056 完整/partial 依次为 13、17、27、20、32 个。新增全隔离矩阵控制器再通过 32 个攻击模型和 PS5 SelfTest。所有默认关闭检查均确认 `docker_access=false`、`database_access=false`、`migration_executed=false`。本机没有 Docker CLI，未生成六项外部基线、未运行任何 migration；RAM 也没有新增云侧证据，所以这些结果仅证明获批前资产就绪，不关闭对应真实门禁。

第三次 Redis unknown 保留 stage 纯只读诊断使用最终冻结资产并严格执行一次 SSH。脱敏结果为 `diagnostic_complete`、`phase1_created`、`state_class=complete`、stage 唯一、固定文件数 3、文件身份和 SHA 通过、schema57/dirty0、migration/operator/单数表为 1、复数表为 0、template/allowlist/send_log/scope 各 1、Redis PING 与身份通过、派生精确 key `EXISTS=0`；传输计数为 `ssh_attempts=1`、`retries=0`、stderr0，副作用字段全部为 false。唯一不满足归属不变量的是 `stage_nonce_match=false`。由于数据库业务字段均由 state nonce 推导并精确匹配，只能证明 state 与三条夹具相互一致，不能证明它们属于当前 stage 目录；原 stage 和夹具继续保留，本次授权已消费，严禁直接 cleanup、新 phase1 或重启。

为防止误操作，旧 `email-unknown-fresh-recovery` 资产已立即失败关闭。payload 把 stage operation ID 传入严格 JSON 解析并要求 `state.nonce` 完全相等；controller 改为 UTF-8 BOM，并在正式确认、SSH、SCP、上传和 cleanup 之前固定抛出 `recovery_asset_disabled_stage_nonce_mismatch`。payload/controller/contract SHA-256 分别为 `36853BAE63A78C3F4B9A869BCB6BFF9B184A6258A06F3DC7B5EDE62E8BAC6172`、`C83BA172655B232FB3B703BFABF39BFB84084F51E8046A0A77FB4410B89B243F`、`520643CAFF855FEF53C47FAFC81F0C8A96758BB87FCC9A124CC24BA315C6C774`；PS5 SelfTest、默认禁用、Bash 语法和 Python normal/`-O` 25 个攻击模型通过。当时机器清单 SHA 为 `C50BBF8A69F1BE84F82CC17E96167E731B1AD2557F30C85F02D78400E85B254C`，契约 SHA 为 `019378AFC234BD610E68B7F974551CD7EAE359B5D1D3692B7084DE6915F65D49`。

替代旧资产的 nonce mismatch 精确恢复入口现已完成离线冻结。payload 只接受唯一合规 stage、三个旧固定文件、严格完整 `phase1_created` state、state nonce 与目录 nonce 不等、由 state nonce 派生且全字段精确匹配的 template/allowlist/send-log、唯一 scope、原 Redis run ID 和派生精确 key `EXISTS=0`；state 通过 `O_NOFOLLOW + fstat` 读取。控制器严格执行一次 preflight SSH、一次 legacy SCP 和一次 cleanup SSH，第二轮 fixture/Redis 门禁全部通过后才允许 `chmod` 和 Go `cleanup_phase1`，不包含 Redis 删除、扫描或重启。成功/失败摘要不转发 operation ID 或远端原始内容。冻结 ELF SHA-256 为 `1179E29D9F43EFEA79F185E8D2319D015A627F69A48EF9ED7CE22E72BA6AD900`、大小 `25573597` 字节；payload/controller/contract SHA-256 为 `12B57C09DDD14333ECA4B159D09DEA2E7BD9974170B9188BC437ACE3F2ACEC63`、`8461938874417E2D9D143D08C187C2AFFA388ACFB09D90F0BD399558CD5B02F6`、`3BA1C76D0D678DC0970343192621EE05F0F2D8F4FCAB3AAE1C636CDC05DC534B`。PS5 AST/SelfTest、Bash 语法、默认关闭、normal/`-O` 31 个篡改用例、目标 Go 测试和 ELF 身份均通过；最新静态扫描覆盖 1096 个文本文件，`FAIL=0`、`REVIEW=3`、`INFO=268`、读取错误 0。本轮未联网、未上传、未执行 cleanup；必须取得新的单次专项授权后才能运行。

该专项授权随后只执行一次正式控制器。第一次 preflight SSH 返回退出码 0，但 stderr 非空，旧控制器按固定门禁立即失败；没有进入 SCP、`chmod`、Go cleanup、数据库删除、Redis 操作或 stage 删除。临时 stdout/stderr 文件在 `finally` 中精确删除，未读取或输出原文；授权随失败消耗且禁止重试。仅在本地增强失败分类后，控制器会输出实际 SSH/SCP 次数、退出码和 stdout/stderr 长度，stderr 非空固定分类为 `stderr_nonempty`，仍不输出任何标识符或正文。新 controller/contract SHA-256 为 `A2121034FBB5A104AF9CE82A5353911AAAAD1DF47C608CFF1A06C7DCC5F3CCB7`、`FF5D605A8CEAEEDCAC6F3475C63E81E829CE2F16E9FDABDEF7BB387C1D5A8D20`，normal/`-O` 32 个攻击模型和 PS5 SelfTest 均通过。Redis 门禁现为 `stage_nonce_mismatch_recovery_preflight_stderr_reauthorization_required`。

当前控制器进一步提供独立 `-PreflightOnly` 诊断模式，使用不同确认词且不接受恢复二进制路径；该路径只有一次 SSH，无论 preflight 成功还是失败都不会到达 SCP/cleanup。脱敏失败摘要仅增加实际传输次数、退出码和 stdout/stderr 长度。最终 payload/controller/contract SHA-256 为 `12B57C09DDD14333ECA4B159D09DEA2E7BD9974170B9188BC437ACE3F2ACEC63`、`59F927FD39E7923CDA352340020347053A922B739D4C9CD7AE65A1343FDA96EA`、`939122A14B0D12362132563148069A25F1DAB0F2744B667FD7D2CFD2D8D1251F`；PS5 SelfTest、默认关闭和 normal/`-O` 34 个攻击模型通过。本轮没有再次联网。

源码级对比进一步确认，成功的只读诊断载荷在入口全局关闭 stderr，而失败 recovery payload 缺少该边界，其精确 Redis `EXISTS` 也是 preflight 唯一未单独重定向 stderr 的容器命令。该差异尚未由新远端执行确认，不能登记为真实根因。修复后 payload 通过 `exec 2>/dev/null` 禁止远端错误正文进入 SSH 捕获，并将 Redis run-id/EXISTS 非零退出显式映射到固定分类。最终 payload/controller/contract SHA-256 为 `76430E008CEB5AF3E5457ECD6CF863FA2BADCC24D9908642C1B5C2C6CEA41D60`、`C0EEE96D3E4AB5C8D8D76BF38C3FAA89298AC1B57D33B0575F719C5F54169CEA`、`6BD2DAA67C50C6D4739714B37F9A9E47D93839EA8E65996D95FFDFEF27DB305F`；PS5 SelfTest、Bash 语法和 normal/`-O` 36 个攻击模型通过，未再次联网。

随后严格执行一次 `-PreflightOnly`。固定摘要确认 `preflight=true`、完整 `phase1_created` state、stage nonce mismatch、三条夹具全字段归属、Redis 身份和派生精确 key `EXISTS=0` 均通过；`exit_code=0`、`stderr_length=0`、`ssh_attempts=1`、`scp_attempts=0`、`retained=true`、`writes=false`、`retries=0`。本次没有上传、`chmod`、cleanup、数据库写入、Redis 删除/扫描/重启或真实发信，只读授权已消费。机器状态现为 `stage_nonce_mismatch_recovery_cleanup_authorization_required`。

精确 cleanup 随后获得单次授权并执行。只读 preflight 再次通过，一次 SCP 完成；cleanup SSH 在 `recovery_binary_identity` 门禁以退出码 2 失败，stdout 长度 84、stderr0，固定计数为两次 SSH、一次 SCP、零重试。失败点先于 `chmod` 和 Go cleanup，因此数据库夹具、state、Redis 和 stage 未被删除，上传 ELF 作为第四个固定文件保留，授权已消费。

为只读识别该文件而不猜测远端模式，恢复载荷新增 `uploaded_preflight`，控制器新增不同确认词的 `-UploadedBinaryPreflightOnly`。它只允许一次 SSH、零 SCP，核验四文件 Stage、完整 state、夹具和 Redis 后，仅输出 ELF 是否 regular/symlink/owner 匹配、模式分类 `500/600/644/700/755/other` 及 SHA 是否匹配；不能进入 cleanup。payload/controller/contract SHA-256 为 `B2DF03AEFFACE2343E2478295F7328C250C9F16E925103EDBA24DA094CB41F1D`、`9F50446CBAB9B2DC983F0516F14D989C3CDC7519C958C62B3322734C1DE7C65A`、`18890BF17789FC4A3A799DE282916ED8603A26453C82C6CEC9117A345D9E6D39`；PS5 SelfTest、默认关闭和 normal/`-O` 38 个攻击模型通过，尚未联网。

该只读授权随后严格执行一次并通过：上传 ELF 为普通文件、非符号链接、属主正确且冻结 SHA 匹配，权限模式仅报告为白名单外 `other`；完整 state、夹具、Redis 身份和精确 key 不存在均继续成立。固定计数为一次 SSH、零 SCP、零重试、stderr0、writes=false，四文件 Stage 原样保留。

`-ResumeUploadedCleanup` 已按单次授权执行成功。第一次 SSH 的 `uploaded_preflight` 通过，第二次 SSH 将 ELF 权限归一为 `0500` 并复核，随后运行一次 Go `cleanup_phase1`；三个精确夹具、state、固定测试资产和空 Stage 已删除，API health/ready 通过。回执为 `preflight=true binary_hash_match=true cleanup=true retained=false ssh_attempts=2 scp_attempts=0 retries=0`，该授权已消耗。

`email-unknown-fresh-cycle.payload.sh`、`run-email-unknown-fresh-cycle.ps1` 和对应契约用于新周期。legacy `scp -O` 两次、默认 SFTP 一次均在首个上传阶段返回相同 56 字节 stderr 摘要，未进入业务阶段；纯本地同一 `scp.exe`、同一 ELF、同一 `Start-Process` 复制探针 SHA 匹配且 stderr0。修复后的可写性诊断 payload/controller/contract SHA 为 `F7D5319E...4388F`、`7F9146C1...6CEA9`、`04625A33...4FD3F`，normal/`-O` 25 个攻击模型、Bash 和 SelfTest 通过；正式执行确认唯一空 Stage 的 parent 与 Stage 均实际可写。负责人要求后续仅使用 `scp.exe -O`，因此下一步不直接重复完整周期，而是先对当前空 Stage 执行固定小文件 legacy SCP 写入、哈希校验和精确删除探针。

固定 44 字节 legacy SCP 探针最终通过上传、SHA 校验和精确删除。后续确认早期 159 字节并非 SCP stdout，而是空字节数组在 PowerShell 参数绑定阶段失败后沿用的 preflight 长度；修复版增加 `AllowEmptyCollection` 与真实空输入进程回归。完整控制器的大 ELF 上传仍因 `Start-Process -ArgumentList` 路径失败，手工原生 `scp.exe -O --` 则以 24 MB、约 4.5 MB/s、exit0 成功，并在 Linux 端通过大小和 SHA 复核。两个历史空 Stage 经聚合只读诊断确认为 `empty_count=2` 后，按 inode 和四次空目录门禁精确移除；最终手工周期复用冻结 ELF/payload 完成全部动作并清理现场。

## 6. 发布判定

历史 Phase 2 判定口径继续作为测试资产完整性基线：以下任何一项为 `SKIP/BLOCKED/FAIL`，不得宣称对应阶段通过，包括13接口授权态、D-95、四权限 MFA/RBAC、同步和测试发送幂等、模板/绑定/白名单乐观锁、真实 Redis Go 集成、五场景真实发送及消费、unknown 墓碑、RAM 否定矩阵、敏感扫描。供应商 accepted 与用户确认收件必须作为两条独立证据，且 accepted 不等于最终送达。

当前 Phase 4 判定：Redis fresh cycle、000055/000056、000057、真实 Redis lease、真实四角色三宽度、历史 Redis cleanup、identity diagnostic、独立 postcheck-only 与运行时六表面均已关闭且相关高风险门禁不得重跑；RAM 有效权限、五场景真实重放/过期、真实发送故障矩阵和五业务流 E2E 由项目负责人豁免但未独立验证。QA/PM 已附负责人豁免通过，Phase 4 状态为 `passed_with_project_owner_waivers`；`accepted` 不等于人工收件或最终送达，Phase 5 与生产上线仍未批准。

本地 main 对账仅证明：邮件分支 HEAD `87161414c553eddf3f900057488c3b0b7702838c` 与本地 `main` `608172e1aa4532e12087afd90daa611ffccd4a73` 的 merge-base 为 `288599f054eacbe334ea0e3a5734a75db7331a9f`；本地 main 独有提交只涉及用户端布局、Agent 聊天和普通聊天三个非邮件路径，与邮件分支相对 merge-base 的修改无路径交集。本轮未 `fetch`，不能据此声称已与最新远端 main 对账。

最新前端本地复验位于 `feature/aliyun-email-template-management`、HEAD `87161414...`。管理端 `type-check`、`lint`、`build` 均 PASS，契约测试为 email 11、admin MFA 7、outbound 4，合计 22/22；用户端 `type-check`、`lint`、`build` 均 PASS，email OTP 15/15。构建只有既有 Vite chunk、dynamic-import 和模块类型 warning，没有错误。

本次仅与本地 `origin/main` `288599f0...` merge-base 对账，没有执行 `fetch`，所以最新远端 main Gate 0 仍未通过。复验未部署、未运行真实浏览器、未访问外部服务，不能扩展为真实环境 E2E 或 Phase 4 通过。
