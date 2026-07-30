# Phase 4 测试 MySQL 备份与隔离恢复门禁

## 结论与安全边界

MySQL Shell 8.4 官方支持通过 `util.dumpSchemas()` 生成单 schema 逻辑备份，并通过
`util.loadDump(..., {schema: target})` 装载到不同 schema。该重映射能力只允许 dump 最终包含一个 schema。

官方同时明确说明：重映射不会修改数据或数据库对象中对旧 schema 名的引用，视图、存储过程等对象仍可能引用原 schema。
因此本门禁在 dump 前执行失败关闭预检：源 schema 只要存在 VIEW、TRIGGER、ROUTINE、EVENT、非 InnoDB 表，
或表定义含显式旧 schema 引用，就不生成备份并返回 `BLOCKED`；不得为通过门禁而删除对象或执行 migration。

参考：

- MySQL Shell 8.4 `util` API：<https://dev.mysql.com/doc/dev/mysqlsh-api-javascript/8.4/group__util.html>
- MySQL Shell 8.4 dump 工具：<https://dev.mysql.com/doc/mysql-shell/8.4/en/mysql-shell-utilities-dump-instance-schema.html>
- MySQL Shell 8.4 配置项：<https://dev.mysql.com/doc/mysql-shell/8.4/en/mysql-shell-configuring-options.html>

## 固定行为

- 仅从 `infra/.env.test` 内存读取 `MYSQL_HOST`、`MYSQL_PORT`、`MYSQL_USER`、`MYSQL_PASSWORD`、
  `MYSQL_DATABASE`，不输出这些值。
- 要求本机已受控安装 MySQL Shell 8.4，不自动下载或安装工具。
- 密码只通过 mysqlsh 的 `--passwords-from-stdin` 输入，不进入命令行参数。
- 第一次确认后、读取任何配置值前，必须验证凭据文件更新时间严格晚于泄露基线与显式 UTC 下限，且 SHA-256
  不以运行方显式传入的旧文件摘要前缀开头；仓库不保存完整旧摘要、旧密码或轮换后的新密码。
- 默认备份根目录为 `$env:LOCALAPPDATA\Molin\mysql-backups`，脚本拒绝仓库内路径。
- 备份目录关闭 ACL 继承，只授予当前 Windows 身份完全控制。
- 私有 BackupRoot 必须先通过 ACL 自检且不能是 reparse point。本轮随机 child 路径的父目录必须精确等于 BackupRoot，
  utility 调用前必须不存在；包装器不预创建 child，由 `util.dumpSchemas` 创建。成功后验证 child 确实存在、不是
  reparse point、仍精确位于 BackupRoot，再收紧并复核 child ACL，最后才标记备份完成。
- 目标已存在固定返回 `dump_target_conflict`，工具成功但输出目录形态或位置不合法返回 `dump_output_invalid`。
  历史空目录或失败输出不会自动删除。
- dump 启用一致性快照与 checksum；恢复启用 checksum 校验，并先执行 `dryRun`。
- 恢复 dry-run 依次固定标记源 schema 检查、隔离目标不存在检查、对象清单/重映射检查、限定引用检查和
  `loadDump` dry-run。前四段查询异常分别收敛为 `restore_source_schema_check_failed`、
  `restore_validation_target_check_failed`、`restore_object_inventory_failed`、
  `restore_qualified_reference_check_failed`，不输出查询、对象名或异常原文。
- `loadDump` dry-run 使用单 schema dump 的 `schema` 重映射、`dryRun`、固定线程和关闭进度显示；不传
  `checksum`。MySQL Shell 8.4 的 dry-run 本身不执行 checksum 校验，真实参数矩阵也证明仅
  `schema + checksum=true` dry-run 组合阻断，而相同 schema、DDL-only、data-only 和无 checksum 组合均通过。
  dry-run 显式设置 `progressFile=""`。MySQL Shell 8.4 省略该参数会在 dump 目录创建默认进度文件，只有空字符串才禁用
  进度跟踪；真实装载才使用精确进度文件。
- `loadDump` dry-run 失败只按受控词汇收敛为 `local_infile_off`、`restore_missing_privileges`、
  `restore_schema_remap_unsupported`、`restore_dump_metadata_invalid`、`restore_option_invalid` 或兜底
  `restore_dry_run_failed`；合法数字诊断码继续保留，outer raw 永不输出。
- MySQL Shell 8.4.10 在 `schema remap + checksum=true` 已完成装载且无 mismatch 后仍会从 `restore_load` 异常退出。
  因此真实 `loadDump` 固定 `checksum=false`，改由门禁执行独立补偿校验；标准 dry-run 与真实 restore 均固定 `threads=1`，
  当前测试数据量很小，优先减少并行 worker 带来的非确定性。
- 补偿校验严格解析 `@.checksums.json`，要求 config 精确为 `version=1.0.0`、`algorithm=bit_xor`、`hash=sha256`；
  checksum 元数据与 dump schema 元数据都必须是同一单 schema，表集合精确相等，每表必须且只能有一个 64 位 checksum
  和一个非负安全整数 count。任何原始 schema、表名、checksum 或文件路径都不会输出。
- 装载前先要求当前源库表集合与 dump 一致，且源库精确总行数等于 dump count 安全聚合，防止业务源已变化；装载后按
  dump 元数据逐表检查目标行数，再对源/目标相同表集合逐表执行 `CHECKSUM TABLE`。单表 checksum 只在进程内比较，成功
  摘要仅新增 `checked_table_count` 与 SHA-256 聚合 `checksum_fingerprint`。
- 补偿校验固定区分 `dump_checksum_metadata_invalid`、`coverage_mismatch`、`row_count_mismatch`、
  `source_target_checksum_mismatch`、`checksum_unavailable`。任一失败仍进入既有精确 cleanup，不允许忽略或降级通过。
- 真实恢复按源聚合、装载、目标聚合和聚合比较分别记录内部阶段；装载异常只收敛为重复键、约束、数据值、包大小、
  事务大小、存储空间、锁、worker、checksum 或兜底数据装载失败等固定枚举。异常原文、表名和数据值不会输出。
- 对 MySQL Shell 8.4 官方 loadDump 错误号执行固定映射：53020 为目标主键策略阻断，53021 为重复对象，
  53023/53024/53029/53030 为 dump 元数据异常，53025 为 `local_infile` 关闭，53026/53027 为进度状态异常，
  53006/53007/53009/53010/53011/53019 为版本或能力不兼容，53002 为 DDL 解析失败，54000-54511 为连接异常。
  若异常对象未直接提供 code，只允许从进程内 raw 的明确 `MYSQLSH`/`MySQL Error`/`Error code` 标签提取 3 至 6 位数字；
  其他数字一律忽略，原文仍不输出。
- 调用 `loadDump` 前，本地只读解析 `@.json`、`@.done.json` 和 `@.checksums.json`，确认 dump 来自
  `dumpSchemas`、只有一个 schema、与当前源 schema 精确匹配、checksum 已启用且元数据完整。失败只返回
  `retained_dump_*` 固定原因，不输出 schema、对象名或文件路径。
- Windows PowerShell 5 的 `ConvertFrom-Json` 不能可靠处理 `@.checksums.json` 的合法键结构，因此包装器仍只检查它是
  非 reparse 的非空普通文件；完整语义解析由 MySQL Shell JS 在 restore 前完成。
- 正常流程先执行独立只读 `preflight` action，只返回 `preflight_complete`、`reason=none` 和表数量；不返回对象名、
  schema、主机或账号。预检固定区分 `source_schema_unavailable`、`unsafe_objects`、`preflight_query_failed`、
  `qualified_reference_check_failed`；通过后才进入 backup。
- preflight 还会从 `information_schema.user_privileges` 只读确认当前连接账号被直接授予
  `SESSION_VARIABLES_ADMIN`。缺失时固定返回 `restore_session_variables_admin_required`，且不会把权限更大的
  `SYSTEM_VARIABLES_ADMIN` 当作可接受替代。
- preflight、恢复 dry-run、诊断和真实恢复都会在任何恢复写入前通过 `CURRENT_ROLE()` 与 `SHOW GRANTS` 只读核验隔离目标
  schema 的有效授权。表型 dump 的完整生命周期固定要求 `ALTER`、`CREATE`、`DROP`、`INDEX`、`INSERT`、`REFERENCES`、
  `SELECT`：分别覆盖建表与索引、数据装载、恢复后计数/checksum 读取及精确 cleanup。任一权限未证实或授权查询异常都失败
  关闭为 `restore_target_privileges_required`。证据解析同时应用 `SHOW GRANTS` 中由 `partial_revokes` 产生的目标 schema
  `REVOKE`，且授权 scope 按 MySQL grant table 的 `Db` 大小写敏感规则匹配。解析前只读查询
  `@@GLOBAL.partial_revokes`：关闭时未转义 `%/_` 按 SQL 通配符解释，开启时按字面字符解释，显式转义始终按字面字符解释；
  查询失败或返回值无法确定时同样失败关闭。门禁不会尝试创建 schema，也不会输出 grant、schema、账号或连接信息。
- 预检查询进一步按固定阶段细分为 `preflight_schema_query_failed`、`preflight_tables_query_failed`、
  `preflight_engine_query_failed`、`preflight_views_query_failed`、`preflight_triggers_query_failed`、
  `preflight_routines_query_failed`、`preflight_events_query_failed`。这些原因只表明失败阶段，不包含对象名、查询原文或值。
- `util.dumpSchemas` 本体失败固定为 `dump_utility_failed`，dump 阶段缺少权限优先归类 `dump_missing_privileges`。
- 真实 backup 前使用相同目标和一致性/checksum 参数执行 `backup_dry_run`；成功必须确认没有产生目录或文件，随后才允许
  实际 dump。常见失败只映射为 `dump_missing_privileges`、`dump_consistency_lock_failed`、`dump_target_exists`、
  `dump_option_invalid`、`dump_server_unsupported`、`dump_utility_failed`。
- 若 MySQL Shell 异常对象的 `error.code` 本身是 1 至 999999 的整数，失败摘要可额外包含该数字
  `diagnostic_code`；不会返回异常 message、type/name 或原始输出。非法、缺失或超范围 code 均忽略。
- 当 marker 已是 `dump_utility_failed` 且 action 为 `backup`/`backup_dry_run` 时，wrapper 可在内存对 outer mysqlsh raw
  使用同一组受控词汇二次收窄；更具体的 marker reason 绝不覆盖，raw 与密码提示仍不输出。
- mysqlsh 没有返回 marker、marker 重复或无法解析、成功 marker 却伴随非零退出码，或原生进程启动/等待异常时，统一
  收敛为 `mysqlsh_process_abnormal`。摘要只允许包含 `process_exit_class=zero|positive|negative`；实际退出码能够安全解析为
  32 位整数时才额外返回 `diagnostic_process_exit_code`。原始 stdout/stderr、异常文本、账号、主机和路径永不输出。
- 失败摘要同时只允许输出固定 `failure_source`：`no_marker`、`duplicate_marker`、`malformed_marker`、`blocked_marker`、
  `success_marker_nonzero_exit`、`unexpected_success_status` 或 `wrapper_exception`。`marker_count` 只允许 `0`、`1`、`2plus`；
  `payload_status` 只允许脚本定义的成功状态、`blocked`、`unavailable` 或 `unexpected`，未知原始状态不会透传。恢复失败后的
  成功 cleanup 不会覆盖先前 restore 的安全失败来源。
- JS 的 blocked marker 额外携带严格白名单 `failure_stage`。允许值仅覆盖初始化、配置、各只读预检、dump utility、
  restore 的源/目标检查、对象检查、限定引用检查、源聚合、dry-run/真实装载、目标聚合、聚合比较、cleanup，以及
  `unknown`。JS 与 wrapper 都会把缺失或未知内部阶段收敛为 `unknown`，绝不透传任意阶段文本；失败后的成功 cleanup
  同样不会覆盖真实 restore 的阶段证据。
- 真正装载前计算源库表数量、精确总行数与结构指纹，装载后计算隔离库同三项并逐项比较；任一不一致即阻断。
- 隔离 schema 名由本轮 32 位随机 run_id 精确派生，并固定使用 `molin_restore_verify_` 前缀。
- 清理只允许本轮精确 schema；禁止扫描、模糊匹配或批量删除。包装器不会在真实恢复调用前预先授予清理资格：只有恢复、
  行数/结构/checksum 核验和数据库内随机所有权 marker 全部成功后，才允许进入清理路径。
- 所有权 marker 使用独立于 `run_id` 的本轮 64 位随机十六进制令牌，保存在隔离 schema 的固定专用表中。真实装载前由
  当前连接执行不带 `IF NOT EXISTS` 的精确 `CREATE SCHEMA`，创建成功后立即写入并回读 marker；若缺席检查后发生同名
  schema 竞争，创建语句必须失败，脚本不会取得清理资格。装载时只容许已由本轮创建的 schema 与 marker 存在，恢复后的
  业务表覆盖率、行数、结构和 checksum 仍按备份清单逐项核验。
- 任何
  `DROP SCHEMA` 前必须同时核对目标精确名称、`molin_restore_verify_` 前缀、marker 表类型、唯一行数和令牌完全一致；
  同名外来 schema、marker 缺失、marker 不匹配或检查异常全部失败关闭且不执行 DROP。清理后继续查询
  `information_schema.schemata`，复核目标确实不存在；复核失败固定返回清理阻断。
- 若 `loadDump` 在所有权 marker 确认前失败，即使隔离 schema 已部分创建也不会自动删除，需保留现场由人工确认归属后处理，
  以避免“目标不存在检查”与实际装载之间的同名 schema 竞争导致误删。
- 完整备份不会由门禁删除，必须保留到 migration 与回滚验证完成后，再按获批留存策略人工处置。
- 脚本不调用 migration，不包含 `000055`，也不修改 `schema_migrations`。

## 保留备份续跑

当同一 `RunId` 已完成 dump、但后续 `restore_dry_run` 尚未成功时，可显式增加 `-ResumeRetainedBackup`。
该开关只接受 `BackupRoot\phase4_<RunId>` 这一精确目录；不扫描其他目录，也不允许通过另一个 RunId 复用备份。

续跑仍必须通过凭据轮换门禁、两次精确确认、MySQL Shell 8.4 检查与只读 preflight。进入恢复前，包装器只读验证：

- BackupRoot 与固定 child 都存在、父子关系精确，且都不是 reparse point；
- child ACL 已关闭继承，唯一显式规则为当前 Windows SID 的完全控制；续跑不会自动重写 ACL；
- child 顶层同时存在普通文件 `@.json`、`@.done.json`，并至少有一个标记外的实际 dump 文件；
- child 全树不存在 reparse point，也不存在任何 `*.progress` 恢复进度文件。

上述目录门禁不再把所有异常归并为一个原因：路径、root/target 缺失、root/target/child reparse、ACL、完成标记、
payload、progress 与读取失败均有独立 `retained_backup_*` 原因；主元数据、完成元数据、schema 数量/匹配、origin、
checksum 开关/文件、version 与 basenames 则分别返回独立 `retained_dump_*` 原因。所有原因都不包含名称、值或路径。

全部通过后直接继续 `restore_dry_run → restore → 聚合校验 → 精确清理隔离 schema`。续跑分支不会再次执行
`backup_dry_run` 或 `backup`，不会覆盖或删除保留备份。未显式传入该开关时，既有目标仍固定返回
`dump_target_conflict`；保留备份校验失败固定返回 `retained_backup_invalid`。安全摘要不包含备份路径、schema 或连接信息。
实现中 Resume 判断与“是否需要新备份”使用独立互斥谓词；diagnostic 判断位于该决策之后，不能拥有或绑定 backup 的
`else` 分支。离线动态回归以调用计数确认普通 Resume 的目标不存在断言与 backup action 均为零次。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/backup-verify-test-mysql.ps1 `
  -RunId "<原32位run_id>" `
  -BackupRoot "<仓库外私有备份根目录>" `
  -ResumeRetainedBackup `
  -CredentialFileModifiedAfterUtc "2026-06-26T12:18:59Z" `
  -LeakedCredentialFileSha256Prefix "<通过安全渠道取得的12至64位旧文件SHA-256前缀>"
```

## 失败恢复的显式安全重试

真实恢复已经开始且留下 `*.progress` 时，普通 `-ResumeRetainedBackup` 仍失败关闭。只有操作员明确同时传入
`-ResumeRetainedBackup -RetryFailedRestore` 才进入重试门禁。重试不会删除、截断、重命名或覆盖任何历史 progress，且要求：

- 保留备份继续通过精确路径、私有 ACL、reparse、完成标记和单 schema/checksum 元数据门禁；
- 所有历史 `*.progress` 都是非 reparse 普通文件、每行均为合法 JSON，聚合后至少存在一个未完成任务；
- 输出只包含 progress 文件数、记录数、任务完成/未完成数及固定 operation 枚举计数，不包含路径、schema、表或 chunk；
- `operation_counts` 始终序列化为嵌套 JSON 数字对象，不允许退化为 PowerShell Hashtable 类型名称字符串；
- 第二次恢复前先以只读 `validation_status` 确认本轮精确隔离 schema 不存在；若仍存在则阻断，不自动清理未知状态；
- 每次获准重试都使用新的 `restore_retry_<随机32位>.progress`，且调用前确认该文件不存在。
- 离线重试生命周期自测会对历史 progress 前后 SHA-256 做等值比较，并确认新随机 progress 路径尚不存在，防止覆盖历史证据。

重试仍需凭据轮换门禁、两次精确确认、只读 preflight 和恢复 dry-run。以下命令只是调用形式示例，不应把确认短语写入 CI：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/backup-verify-test-mysql.ps1 `
  -RunId "<原32位run_id>" -BackupRoot "<仓库外私有备份根目录>" `
  -ResumeRetainedBackup -RetryFailedRestore `
  -CredentialFileModifiedAfterUtc "2026-06-26T12:18:59Z" `
  -LeakedCredentialFileSha256Prefix "<通过安全渠道取得的12至64位旧文件SHA-256前缀>"
```

## 临时最小权限授权与回收

真实重试前必须由获授权 DBA 在维护窗口内，把 `SESSION_VARIABLES_ADMIN` 临时、直接授予测试环境所用的精确数据库账号。
不要授予 `SYSTEM_VARIABLES_ADMIN`、`SUPER` 或 `GRANT OPTION`，也不要在聊天、工单或仓库文件中填写真实账号、主机或密码。
以下占位命令由 DBA 在安全终端执行，门禁脚本自身不会执行授权或回收：

```sql
GRANT SESSION_VARIABLES_ADMIN ON *.* TO '<测试账号>'@'<精确来源主机>';
```

授权后先运行只读 `-PreflightOnly`；只有返回 `preflight_complete` 才能在另一次明确批准下重试恢复。维护窗口结束或验证完成后，
DBA 必须立即执行：

```sql
REVOKE SESSION_VARIABLES_ADMIN ON *.* FROM '<测试账号>'@'<精确来源主机>';
```

回收后再次运行只读 preflight，应固定阻断为 `restore_session_variables_admin_required`。授权、验证、回收三步需在安全审计系统
记录操作人和时间；任何一步失败都不得改授 `SYSTEM_VARIABLES_ADMIN` 继续尝试。

## 默认双确认

直接运行时，脚本会在任何数据库连接前要求第一次精确确认；完整备份成功后，再要求第二次精确确认，
随后才允许 dry-run 与隔离恢复写入：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/backup-verify-test-mysql.ps1 `
  -CredentialFileModifiedAfterUtc "2026-06-26T12:18:59Z" `
  -LeakedCredentialFileSha256Prefix "<通过安全渠道取得的12至64位旧文件SHA-256前缀>"
```

自动化执行必须先用非交互模式取得随机 `run_id` 与第一次确认短语。该准备调用会以退出码 2 阻断，且不会读取环境文件、
查找 mysqlsh 或连接数据库：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/backup-verify-test-mysql.ps1 -NonInteractive
```

之后由操作员核对变更窗口，以同一 `run_id` 传入两条精确确认短语。第二条短语格式固定为
`I_CONFIRM_ISOLATED_RESTORE_<run_id>`。不得把确认短语固化到 CI 或长期配置。

如需定位无错误编号的 `loadDump` dry-run 阻断，可在 `-ResumeRetainedBackup` 基础上增加
`-RestoreDryRunDiagnosticOnly`。该模式仍执行凭据轮换、第一次确认、只读 preflight、保留目录与 metadata 门禁，
但不请求第二次写入确认，也不执行真实 restore 或 cleanup。所有 `loadDump` 探针都固定 `dryRun=true`、
`progressFile=""`、单线程且关闭进度输出。

诊断只返回以下受控信息：源 schema 存在、隔离目标不存在两个布尔值；目标完整权限、CREATE 与 SELECT 能力为
`evidenced/not_evidenced/unknown`。目标完整权限未证实时诊断直接以 `restore_target_privileges_required` 失败关闭，不再运行
后续 `loadDump` dry-run 探针；通过后才返回无 schema、schema+无 checksum、DDL-only、data-only、
schema+checksum 五组探针的 `success`、固定 `reason` 和可选数字 `diagnostic_code`。不返回 grant 原文、schema 名、
对象名、路径、账号、主机或 mysqlsh raw，且摘要固定 `remote_write=false`。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/backup-verify-test-mysql.ps1 `
  -RunId "<原32位run_id>" -ResumeRetainedBackup -RestoreDryRunDiagnosticOnly `
  -CredentialFileModifiedAfterUtc "2026-06-26T12:18:59Z" `
  -LeakedCredentialFileSha256Prefix "<通过安全渠道取得的12至64位旧文件SHA-256前缀>"
```

如只需独立执行只读预检，可在同样的凭据轮换参数与第一次确认基础上增加 `-PreflightOnly`。该模式在创建本轮
dump 目录前退出，只输出 `status=preflight_complete`、`reason=none`、表数量和 `remote_write=false`，不执行备份或恢复。

`CredentialFileModifiedAfterUtc` 必须是精确 UTC 格式 `yyyy-MM-ddTHH:mm:ssZ`。脚本取该值与固定泄露基线
`2026-06-26T12:18:59Z` 中较晚者；文件时间不严格晚于有效下限，或当前文件 SHA-256 仍以运行时传入的旧摘要前缀开头，
都会在配置值读取和 mysqlsh 查找前返回 `credential_rotation_not_evidenced`。缺少或错误的下限/摘要参数返回
`credential_rotation_policy_required`。

旧摘要前缀必须严格为 12 至 64 位十六进制。旧版 `LeakedCredentialFileSha256` 完整哈希参数继续兼容，但不再要求为
门禁伪造或猜测未知的完整摘要；新执行统一使用 `LeakedCredentialFileSha256Prefix`。

## 安全输出与退出码

- `0`：备份完成、checksum 恢复校验通过、精确隔离 schema 已清理。
- `2`：确认缺失、配置/工具/ACL/备份/dry-run/恢复/清理任一步骤阻断。

最终通过摘要只包含状态、备份保留标志、隔离库清理标志、表数量、总行数和结构 SHA-256 指纹。
失败摘要只包含固定原因枚举，不包含表内容、表名、schema 名、备份绝对路径、主机、端口、用户、密码或 mysqlsh 原始错误。
mysqlsh 失败优先解析 JS 的唯一固定结果标记，并限制为 `connection_failed`、`insufficient_privileges`、
`unsafe_objects`、`dump_failed`、`source_schema_unavailable` 等白名单原因；连接若在 JS 启动前失败，也只在内存匹配固定
MySQL 错误码后归类，禁止回显 stdout、stderr 或原始异常。未知、缺失或非法原因一律按当前 action 收敛为固定 fallback，
最终 `BLOCKED` 摘要的 `reason` 永远是非空白名单值。
mysqlsh 的密码提示可能不换行并与结果 marker 位于同一行；解析器允许 marker 前存在提示文本，但要求完整内存输出中
固定 marker 恰好一次，只提取其后的单行 JSON，提示前缀与原始输出永不进入摘要。

## 离线自测

以下命令只检查语法和安全红线，并验证缺省运行会在连接前失败关闭；不会读取真实 `infra/.env.test`，不会连接远程服务：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File tests/email/mysql-backup-restore-gate-selftest.ps1
```

如需单独诊断 Windows ACL，可运行下列模式。它只创建并精确删除本轮随机临时目录，成功摘要固定包含
`acl_private=true` 与 `remote_access=false`：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/backup-verify-test-mysql.ps1 -AclSelfTest
```

保留备份生命周期也可单独离线验证。该模式只创建并精确删除随机本机 fake 目录，覆盖缺失目录、合法完整目录和
历史恢复进度阻断；不读取环境文件、不启动 mysqlsh、不连接远程服务，摘要不输出临时路径：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/backup-verify-test-mysql.ps1 -RetainedBackupSelfTest
```

失败重试生命周期可用 `-RetryLifecycleSelfTest` 单独离线验证。它只在随机临时目录创建假 progress，验证安全聚合、
未完成门禁、旧文件摘要不变和新随机路径尚不存在，摘要固定 `remote_access=false`。

离线通过不等于真实备份或恢复通过。远程执行必须由获授权操作员在维护窗口完成，并单独保存安全摘要。
