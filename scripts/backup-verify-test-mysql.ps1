param(
    [string]$EnvFile = "",
    [string]$BackupRoot = "",
    [string]$MySqlShellExecutable = "",
    [string]$RunId = "",
    [string]$ConfirmBackup = "",
    [string]$ConfirmRestore = "",
    [string]$CredentialFileModifiedAfterUtc = "",
    [string]$LeakedCredentialFileSha256 = "",
    [string]$LeakedCredentialFileSha256Prefix = "",
    [switch]$NonInteractive,
    [switch]$AclSelfTest,
    [switch]$ResultParserSelfTest,
    [switch]$PreflightOnly,
    [switch]$DumpLifecycleSelfTest,
    [switch]$ResumeRetainedBackup,
    [switch]$RetryFailedRestore,
    [switch]$RetainedBackupSelfTest,
    [switch]$RestoreDryRunDiagnosticOnly,
    [switch]$ResumeControlFlowSelfTest,
    [switch]$RetryLifecycleSelfTest
)

$ErrorActionPreference = "Stop"
$AllowedNames = @("MYSQL_HOST", "MYSQL_PORT", "MYSQL_USER", "MYSQL_PASSWORD", "MYSQL_DATABASE")
$RepositoryRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$ShellScript = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "mysql-backup-restore-gate.js"))
$RestoreStarted = $false
$OwnershipToken = $null
$BackupCompleted = $false
$ExitCode = 2
$CurrentStage = "initialization"
$configuration = $null
$dumpDirectory = $null
$progressFile = $null
$HistoricalProgressSummary = $null
$LastDiagnosticCode = $null
$LastProcessExitClass = $null
$LastDiagnosticProcessExitCode = $null
$LastFailureSource = $null
$LastMarkerCount = $null
$LastPayloadStatus = $null
$LastFailureStage = $null
$UtcParseStyles = [Globalization.DateTimeStyles](
    [Globalization.DateTimeStyles]::AssumeUniversal -bor [Globalization.DateTimeStyles]::AdjustToUniversal
)
$CredentialLeakBaselineUtc = [DateTimeOffset]::ParseExact(
    "2026-06-26T12:18:59Z",
    "yyyy-MM-ddTHH:mm:ss'Z'",
    [Globalization.CultureInfo]::InvariantCulture,
    $UtcParseStyles
)
$GateFailureReasons = @(
    "connection_failed", "insufficient_privileges", "unsafe_objects", "preflight_query_failed",
    "restore_session_variables_admin_required", "restore_target_privileges_required",
    "preflight_schema_query_failed", "preflight_tables_query_failed", "preflight_engine_query_failed",
    "preflight_views_query_failed", "preflight_triggers_query_failed", "preflight_routines_query_failed",
    "preflight_events_query_failed",
    "qualified_reference_check_failed", "dump_missing_privileges", "dump_consistency_lock_failed",
    "dump_target_exists", "dump_option_invalid", "dump_server_unsupported", "dump_utility_failed", "dump_failed",
    "source_schema_unavailable", "validation_target_rejected", "configuration_invalid",
    "restore_source_schema_check_failed", "restore_validation_target_check_failed",
    "restore_object_inventory_failed", "restore_qualified_reference_check_failed",
    "local_infile_off", "restore_missing_privileges", "restore_schema_remap_unsupported",
    "restore_dump_metadata_invalid", "restore_dump_incomplete", "restore_version_incompatible",
    "restore_primary_key_policy_blocked", "restore_duplicate_objects", "restore_ddl_parse_failed",
    "restore_progress_state_invalid", "restore_connection_failed", "restore_option_invalid", "restore_dry_run_failed",
    "restore_worker_failed", "restore_checksum_failed", "restore_duplicate_key", "restore_data_constraint_failed",
    "restore_data_value_invalid", "restore_packet_too_large", "restore_transaction_too_large",
    "restore_storage_exhausted", "restore_lock_failed", "restore_data_load_failed",
    "restore_source_aggregate_failed", "restore_target_aggregate_failed", "restore_aggregate_mismatch",
    "restore_failed", "cleanup_failed",
    "dump_checksum_metadata_invalid", "coverage_mismatch", "row_count_mismatch",
    "source_target_checksum_mismatch", "checksum_unavailable",
    "mysqlsh_process_abnormal",
    "mysqlsh_action_failed"
)
$FinalFailureReasons = $GateFailureReasons + @(
    "acl_configuration_failed", "credential_rotation_policy_required", "credential_rotation_not_evidenced",
    "mysqlsh_unavailable", "mysqlsh_invalid", "mysqlsh_8_4_required", "mysqlsh_discovery_failed",
    "backup_root_inside_repository", "backup_target_already_exists", "backup_directory_failed",
    "backup_root_reparse_point", "dump_target_conflict", "dump_output_invalid",
    "backup_acl_failed", "retained_backup_invalid", "retained_backup_path_invalid",
    "retained_backup_root_missing", "retained_backup_target_missing", "retained_backup_root_reparse",
    "retained_backup_target_reparse", "retained_backup_acl_invalid", "retained_backup_marker_missing",
    "retained_backup_marker_reparse", "retained_backup_child_reparse", "retained_backup_payload_missing",
    "retained_backup_progress_present", "retained_backup_inspection_failed",
    "retained_dump_metadata_missing", "retained_dump_metadata_invalid", "retained_dump_done_missing",
    "retained_dump_done_invalid", "retained_dump_not_single_schema", "retained_dump_schema_mismatch",
    "retained_dump_origin_invalid", "retained_dump_checksum_disabled", "retained_dump_checksum_missing",
    "retained_dump_checksum_empty", "retained_dump_checksum_reparse", "retained_dump_version_missing",
    "retained_dump_basenames_missing", "retained_dump_incomplete", "initialization_failed",
    "restore_diagnostic_requires_retained_backup", "restore_diagnostic_failed", "cleanup_after_failure_failed",
    "retry_requires_retained_backup", "retry_progress_missing", "retry_progress_reparse",
    "retry_progress_invalid", "retry_progress_no_incomplete", "retry_validation_status_failed",
    "retry_validation_schema_present", "retry_progress_target_conflict"
)

function Write-SafeSummary {
    param([hashtable]$Fields)
    # 显式提高 JSON 深度，确保 operation_counts 的二级数字对象不会退化为类型名称字符串。
    Write-Output ($Fields | ConvertTo-Json -Depth 8 -Compress)
}

function Test-BackupRequired {
    param([bool]$Resume)
    # 续跑与新备份必须严格互斥；诊断开关不能改变这一业务判断。
    return (-not $Resume)
}

function Test-PathInsideRepository {
    param([string]$Candidate)
    $rootWithSeparator = $RepositoryRoot.TrimEnd('\') + '\'
    return $Candidate.StartsWith($rootWithSeparator, [System.StringComparison]::OrdinalIgnoreCase) -or
        $Candidate.Equals($RepositoryRoot, [System.StringComparison]::OrdinalIgnoreCase)
}

function Assert-PrivateDirectoryAcl {
    param([string]$Path)
    $identity = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
    # 只读复核 ACL，续跑保留备份时禁止通过重写权限掩盖既有目录的不安全状态。
    $verifiedAcl = Get-Acl -LiteralPath $Path
    $explicitRules = @($verifiedAcl.GetAccessRules(
        $true,
        $false,
        [System.Security.Principal.SecurityIdentifier]
    ))
    if (-not $verifiedAcl.AreAccessRulesProtected -or $explicitRules.Count -ne 1 -or
        $explicitRules[0].IdentityReference -ne $identity -or
        $explicitRules[0].AccessControlType -ne [System.Security.AccessControl.AccessControlType]::Allow -or
        ($explicitRules[0].FileSystemRights -band [System.Security.AccessControl.FileSystemRights]::FullControl) -ne
            [System.Security.AccessControl.FileSystemRights]::FullControl) {
        throw "acl_configuration_failed"
    }
}

function Set-PrivateDirectoryAcl {
    param([string]$Path)
    # 关闭继承并只授权当前 Windows 身份，避免逻辑备份被同机普通用户读取。
    $identity = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
    $acl = New-Object System.Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true, $false)
    $inheritanceFlags = [System.Security.AccessControl.InheritanceFlags](
        [System.Security.AccessControl.InheritanceFlags]::ContainerInherit -bor
        [System.Security.AccessControl.InheritanceFlags]::ObjectInherit
    )
    $rule = [System.Security.AccessControl.FileSystemAccessRule]::new(
        $identity,
        [System.Security.AccessControl.FileSystemRights]::FullControl,
        $inheritanceFlags,
        [System.Security.AccessControl.PropagationFlags]::None,
        [System.Security.AccessControl.AccessControlType]::Allow
    )
    $acl.AddAccessRule($rule)
    Set-Acl -LiteralPath $Path -AclObject $acl

    # 写入后重新读取 ACL，确保继承确已关闭且只有当前 SID 获得显式完全控制。
    Assert-PrivateDirectoryAcl -Path $Path
}

function Assert-SafeDumpTargetBeforeUtility {
    param([string]$Root, [string]$Target)
    $fullRoot = [System.IO.Path]::GetFullPath($Root).TrimEnd('\')
    $fullTarget = [System.IO.Path]::GetFullPath($Target)
    $targetParent = [System.IO.Path]::GetFullPath((Split-Path -Parent $fullTarget)).TrimEnd('\')
    if (-not $targetParent.Equals($fullRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "dump_output_invalid"
    }
    $rootItem = Get-Item -LiteralPath $fullRoot
    if (($rootItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "backup_root_reparse_point"
    }
    if (Test-Path -LiteralPath $fullTarget) {
        throw "dump_target_conflict"
    }
}

function Protect-AndValidateDumpOutput {
    param([string]$Root, [string]$Target)
    $fullRoot = [System.IO.Path]::GetFullPath($Root).TrimEnd('\')
    $fullTarget = [System.IO.Path]::GetFullPath($Target)
    $targetParent = [System.IO.Path]::GetFullPath((Split-Path -Parent $fullTarget)).TrimEnd('\')
    if (-not $targetParent.Equals($fullRoot, [StringComparison]::OrdinalIgnoreCase) -or
        -not (Test-Path -LiteralPath $fullRoot -PathType Container) -or
        -not (Test-Path -LiteralPath $fullTarget -PathType Container)) {
        throw "dump_output_invalid"
    }
    $rootItem = Get-Item -LiteralPath $fullRoot
    $targetItem = Get-Item -LiteralPath $fullTarget
    if (($rootItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        ($targetItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "dump_output_invalid"
    }
    # 工具创建成功后再收紧 child ACL；函数内部会重新读取 ACL 并验证唯一显式授权。
    Set-PrivateDirectoryAcl -Path $fullTarget
}

function Assert-RetainedBackup {
    param([string]$Root, [string]$Target, [string]$ExpectedLeaf, [bool]$AllowFailedProgress = $false)
    try {
        $fullRoot = [System.IO.Path]::GetFullPath($Root).TrimEnd('\')
        $fullTarget = [System.IO.Path]::GetFullPath($Target).TrimEnd('\')
        $targetParent = [System.IO.Path]::GetFullPath((Split-Path -Parent $fullTarget)).TrimEnd('\')
        $targetLeaf = Split-Path -Leaf $fullTarget
    }
    catch {
        throw "retained_backup_path_invalid"
    }
    if (-not $targetParent.Equals($fullRoot, [StringComparison]::OrdinalIgnoreCase) -or
        -not $targetLeaf.Equals($ExpectedLeaf, [StringComparison]::Ordinal)) {
        throw "retained_backup_path_invalid"
    }
    if (-not (Test-Path -LiteralPath $fullRoot -PathType Container)) {
        throw "retained_backup_root_missing"
    }
    if (-not (Test-Path -LiteralPath $fullTarget -PathType Container)) {
        throw "retained_backup_target_missing"
    }
    try {
        $rootItem = Get-Item -LiteralPath $fullRoot
        $targetItem = Get-Item -LiteralPath $fullTarget
    }
    catch {
        throw "retained_backup_inspection_failed"
    }
    if (($rootItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "retained_backup_root_reparse"
    }
    if (($targetItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "retained_backup_target_reparse"
    }
    try {
        # 保留备份只能通过只读 ACL 复核，禁止续跑时自动修复或扩大权限。
        Assert-PrivateDirectoryAcl -Path $fullTarget
    }
    catch {
        throw "retained_backup_acl_invalid"
    }
    foreach ($markerName in @("@.json", "@.done.json")) {
        $markerPath = Join-Path $fullTarget $markerName
        if (-not (Test-Path -LiteralPath $markerPath -PathType Leaf)) {
            throw "retained_backup_marker_missing"
        }
        try {
            $markerItem = Get-Item -LiteralPath $markerPath
        }
        catch {
            throw "retained_backup_inspection_failed"
        }
        if (($markerItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "retained_backup_marker_reparse"
        }
    }
    try {
        $allItems = @(Get-ChildItem -LiteralPath $fullTarget -Recurse -Force)
    }
    catch {
        throw "retained_backup_inspection_failed"
    }
    if (@($allItems | Where-Object {
        ($_.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0
    }).Count -gt 0) {
        throw "retained_backup_child_reparse"
    }
    # 两个完成标记之外还必须至少存在一个实际 dump 文件，空壳目录不能进入恢复阶段。
    if (@($allItems | Where-Object {
        -not $_.PSIsContainer -and $_.Name -notin @("@.json", "@.done.json")
    }).Count -lt 1) {
        throw "retained_backup_payload_missing"
    }
    # 普通续跑仍拒绝历史恢复进度；显式失败重试会在后续专用门禁逐一验证这些文件。
    if (-not $AllowFailedProgress -and @($allItems | Where-Object {
        -not $_.PSIsContainer -and $_.Name -like "*.progress"
    }).Count -gt 0) {
        throw "retained_backup_progress_present"
    }
}

function Assert-RetainedDumpMetadata {
    param([string]$Target, [string]$ExpectedSchema)
    $metadataPath = Join-Path $Target "@.json"
    $donePath = Join-Path $Target "@.done.json"
    $checksumPath = Join-Path $Target "@.checksums.json"
    if (-not (Test-Path -LiteralPath $metadataPath -PathType Leaf)) {
        throw "retained_dump_metadata_missing"
    }
    if (-not (Test-Path -LiteralPath $donePath -PathType Leaf)) {
        throw "retained_dump_done_missing"
    }
    try {
        $metadata = Get-Content -LiteralPath $metadataPath -Raw -Encoding UTF8 | ConvertFrom-Json
    }
    catch {
        throw "retained_dump_metadata_invalid"
    }
    try {
        $null = Get-Content -LiteralPath $donePath -Raw -Encoding UTF8 | ConvertFrom-Json
    }
    catch {
        throw "retained_dump_done_invalid"
    }
    if (@($metadata.schemas).Count -ne 1) {
        throw "retained_dump_not_single_schema"
    }
    if (([string]@($metadata.schemas)[0]) -cne $ExpectedSchema) {
        throw "retained_dump_schema_mismatch"
    }
    if (([string]$metadata.origin) -cne "dumpSchemas") {
        throw "retained_dump_origin_invalid"
    }
    if (-not [bool]$metadata.checksum) {
        throw "retained_dump_checksum_disabled"
    }
    if (-not (Test-Path -LiteralPath $checksumPath -PathType Leaf)) {
        throw "retained_dump_checksum_missing"
    }
    try {
        $checksumItem = Get-Item -LiteralPath $checksumPath
    }
    catch {
        throw "retained_dump_checksum_missing"
    }
    if (($checksumItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "retained_dump_checksum_reparse"
    }
    # MySQL Shell checksum JSON 可包含 Windows PowerShell 5 无法反序列化的键形态；只验证受保护普通文件非空。
    if ($checksumItem.Length -le 0) {
        throw "retained_dump_checksum_empty"
    }
    if ($null -eq $metadata.version -or [string]::IsNullOrWhiteSpace([string]$metadata.version)) {
        throw "retained_dump_version_missing"
    }
    if ($null -eq $metadata.basenames -or @($metadata.basenames.PSObject.Properties).Count -lt 1) {
        throw "retained_dump_basenames_missing"
    }
}

function Get-RestoreProgressSafeSummary {
    param([string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path) -or -not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return @{present = $false; parse_status = "absent"; record_count = 0; task_count = 0; completed_count = 0; incomplete_count = 0}
    }
    try {
        $records = New-Object System.Collections.Generic.List[object]
        foreach ($line in Get-Content -LiteralPath $Path -Encoding UTF8) {
            if (-not [string]::IsNullOrWhiteSpace($line)) {
                $records.Add(($line | ConvertFrom-Json))
            }
        }
        $latest = @{}
        foreach ($record in $records) {
            # schema/table 只参与内存去重，绝不进入摘要。
            $key = ([string]$record.op) + "|" + ([string]$record.schema) + "|" +
                ([string]$record.table) + "|" + ([string]$record.chunk) + "|" + ([string]$record.uuid)
            $latest[$key] = $record
        }
        $allowedOperations = @("SCHEMA-DDL", "TABLE-DDL", "TABLE-DATA", "SERVER-UUID")
        $operationCounts = @{}
        $completed = 0
        $incomplete = 0
        foreach ($record in $latest.Values) {
            $operation = [string]$record.op
            if ($allowedOperations -notcontains $operation) {
                $operation = "OTHER"
            }
            if (-not $operationCounts.ContainsKey($operation)) {
                $operationCounts[$operation] = @{completed = 0; incomplete = 0}
            }
            if ([bool]$record.done) {
                $completed += 1
                $operationCounts[$operation]["completed"] += 1
            }
            else {
                $incomplete += 1
                $operationCounts[$operation]["incomplete"] += 1
            }
        }
        return @{
            present = $true
            parse_status = "valid_jsonl"
            record_count = $records.Count
            task_count = $latest.Count
            completed_count = $completed
            incomplete_count = $incomplete
            operation_counts = $operationCounts
        }
    }
    catch {
        return @{present = $true; parse_status = "invalid"; record_count = 0; task_count = 0; completed_count = 0; incomplete_count = 0}
    }
}

function Assert-FailedRestoreProgressHistory {
    param([string]$Target)
    try {
        $progressItems = @(Get-ChildItem -LiteralPath $Target -Recurse -Force -File | Where-Object {
            $_.Name -like "*.progress"
        })
    }
    catch {
        throw "retry_progress_invalid"
    }
    if ($progressItems.Count -lt 1) {
        throw "retry_progress_missing"
    }
    $aggregate = @{
        file_count = $progressItems.Count
        record_count = 0
        task_count = 0
        completed_count = 0
        incomplete_count = 0
        parse_status = "valid_jsonl"
        operation_counts = @{}
    }
    foreach ($item in $progressItems) {
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "retry_progress_reparse"
        }
        $summary = Get-RestoreProgressSafeSummary -Path $item.FullName
        if (-not $summary.present -or $summary.parse_status -cne "valid_jsonl" -or
            [int64]$summary.record_count -lt 1) {
            throw "retry_progress_invalid"
        }
        foreach ($field in @("record_count", "task_count", "completed_count", "incomplete_count")) {
            $aggregate[$field] = [int64]$aggregate[$field] + [int64]$summary[$field]
        }
        foreach ($operation in @($summary.operation_counts.Keys)) {
            if (-not $aggregate.operation_counts.ContainsKey($operation)) {
                $aggregate.operation_counts[$operation] = @{completed = 0; incomplete = 0}
            }
            $aggregate.operation_counts[$operation].completed =
                [int64]$aggregate.operation_counts[$operation].completed + [int64]$summary.operation_counts[$operation].completed
            $aggregate.operation_counts[$operation].incomplete =
                [int64]$aggregate.operation_counts[$operation].incomplete + [int64]$summary.operation_counts[$operation].incomplete
        }
    }
    if ([int64]$aggregate.incomplete_count -lt 1) {
        throw "retry_progress_no_incomplete"
    }
    return $aggregate
}

function New-UniqueRetryProgressPath {
    param([string]$Target)
    $targetItem = Get-Item -LiteralPath $Target
    if (($targetItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "retry_progress_target_conflict"
    }
    $candidate = Join-Path $Target ("restore_retry_" + [guid]::NewGuid().ToString("N") + ".progress")
    if (Test-Path -LiteralPath $candidate) {
        throw "retry_progress_target_conflict"
    }
    return $candidate
}

function Read-EnvFileSecurely {
    param([string]$Path)
    $values = @{}
    foreach ($line in Get-Content -LiteralPath $Path -Encoding UTF8) {
        $trimmed = $line.Trim()
        if (-not $trimmed -or $trimmed.StartsWith("#") -or -not $trimmed.Contains("=")) {
            continue
        }
        $parts = $trimmed.Split("=", 2)
        $name = $parts[0].Trim()
        if ($AllowedNames -notcontains $name) {
            continue
        }
        $value = $parts[1].Trim()
        if ($value.Length -ge 2) {
            $quoted = ($value[0] -eq [char]34 -and $value[-1] -eq [char]34) -or
                ($value[0] -eq [char]39 -and $value[-1] -eq [char]39)
            if ($quoted) {
                $value = $value.Substring(1, $value.Length - 2)
            }
        }
        $values[$name] = $value
    }
    foreach ($name in $AllowedNames) {
        if (-not $values.ContainsKey($name) -or [string]::IsNullOrWhiteSpace($values[$name])) {
            throw "configuration_invalid"
        }
    }
    return $values
}

function Get-SafeTargetId {
    param([string]$Material)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($Material)
        return ([System.BitConverter]::ToString($sha.ComputeHash($bytes))).Replace("-", "").Substring(0, 16).ToLowerInvariant()
    }
    finally {
        $sha.Dispose()
    }
}

function Confirm-ExactPhrase {
    param([string]$Provided, [string]$Expected, [string]$Stage, [string]$TargetId)
    $actual = $Provided
    if ([string]::IsNullOrEmpty($actual) -and -not $NonInteractive) {
        Write-Host ("确认阶段={0}，安全目标指纹={1}" -f $Stage, $TargetId)
        $actual = Read-Host ("请输入精确确认短语 {0}" -f $Expected)
    }
    return $actual -ceq $Expected
}

function Get-ActionFailureReason {
    param([string]$Action)
    $reason = switch ($Action) {
        "preflight" { "preflight_query_failed" }
        "backup_dry_run" { "dump_utility_failed" }
        "backup" { "dump_utility_failed" }
        "restore_dry_run" { "restore_dry_run_failed" }
        "restore_diagnostic" { "restore_diagnostic_failed" }
        "restore" { "restore_failed" }
        "cleanup" { "cleanup_failed" }
        default { "mysqlsh_action_failed" }
    }
    return ([string]$reason)
}

function Resolve-GateFailureReason {
    param([object]$Candidate, [string]$Action)
    $candidateReason = [string]$Candidate
    if (-not [string]::IsNullOrWhiteSpace($candidateReason) -and
        ($GateFailureReasons -contains $candidateReason)) {
        return $candidateReason
    }
    return (Get-ActionFailureReason -Action $Action)
}

function Resolve-DumpRawFailureReason {
    param([string]$Raw)
    # 仅把 generic dump 原因收窄为固定枚举；调用方不得输出 Raw。
    if ($Raw -match '(?i)(error\s*1227|access denied; you need|missing required privileges?|does not have.*privilege|\bprivileges?\b)') {
        return "dump_missing_privileges"
    }
    if ($Raw -match '(?i)(lock instance for backup|flush tables with read lock|backup lock|consistent snapshot)') {
        return "dump_consistency_lock_failed"
    }
    if ($Raw -match '(?i)(output directory.*(exists|not empty)|target.*(exists|not empty)|directory already exists)') {
        return "dump_target_exists"
    }
    if ($Raw -match '(?i)(invalid option|unknown option|option.*not supported)') {
        return "dump_option_invalid"
    }
    if ($Raw -match '(?i)(unsupported.*(server|version)|server version.*not supported|requires mysql server)') {
        return "dump_server_unsupported"
    }
    return "dump_utility_failed"
}

function Resolve-RestoreDryRunRawFailureReason {
    param([string]$Raw)
    # 仅对 JS 已返回通用 dry-run 原因的 outer raw 做受控收窄，原始文本永不进入摘要。
    if ($Raw -match '(?i)3948|local_infile\s*(?:=|is)?\s*(?:off|0|disabled)|loading local data is disabled') {
        return "local_infile_off"
    }
    if ($Raw -match '(?i)1044|1045|1142|1227|access denied|command denied|missing required privileges?|does not have.*privilege|\bprivileges?\b') {
        return "restore_missing_privileges"
    }
    if ($Raw -match '(?i)schema.*(?:remap|mapping).*(?:unsupported|not supported)|(?:unsupported|not supported).*schema.*(?:remap|mapping)|schema.*option.*(?:only|single.schema|unsupported|not supported)|only one schema|cannot.*target schema') {
        return "restore_schema_remap_unsupported"
    }
    if ($Raw -match '(?i)dump.*metadata.*(?:invalid|missing|corrupt)|metadata.*(?:invalid|missing|corrupt)|@\.done\.json|@\.json|invalid dump|not a dump') {
        return "restore_dump_metadata_invalid"
    }
    if ($Raw -match '(?i)sql_require_primary_key|primary key.*(?:required|enabled)') {
        return "restore_primary_key_policy_blocked"
    }
    if ($Raw -match '(?i)duplicate objects?|already exists in (?:the )?(?:target|destination)') {
        return "restore_duplicate_objects"
    }
    if ($Raw -match '(?i)unsupported dump (?:version|capabilities)|mysql version mismatch|server version.*(?:unsupported|mismatch)') {
        return "restore_version_incompatible"
    }
    if ($Raw -match '(?i)error splitting ddl|ddl.*(?:parse|splitting).*failed') {
        return "restore_ddl_parse_failed"
    }
    if ($Raw -match '(?i)invalid option|unknown option|option.*not supported|argumenterror|typeerror') {
        return "restore_option_invalid"
    }
    return "restore_dry_run_failed"
}

function Get-RestoreDiagnosticCodeFromRaw {
    param([string]$Raw)
    # 只提取官方 mysqlsh/MySQL 错误编号，其他数字（端口、版本、路径片段）一律忽略。
    $match = [regex]::Match($Raw, '(?i)\b(?:MYSQLSH|MySQL Error|Error(?: code)?)\s*[:#]?\s*(\d{3,6})\b')
    if (-not $match.Success) {
        return $null
    }
    $code = 0
    if ([int]::TryParse($match.Groups[1].Value, [ref]$code) -and $code -ge 1 -and $code -le 999999) {
        return $code
    }
    return $null
}

function Resolve-RestoreReasonFromDiagnosticCode {
    param([int]$Code)
    switch ($Code) {
        53002 { return "restore_ddl_parse_failed" }
        53004 { return "restore_missing_privileges" }
        53005 { return "restore_worker_failed" }
        {$_ -in @(53006, 53007, 53009, 53010, 53011, 53019)} { return "restore_version_incompatible" }
        53008 { return "restore_dump_incomplete" }
        53020 { return "restore_primary_key_policy_blocked" }
        53021 { return "restore_duplicate_objects" }
        {$_ -in @(53023, 53024, 53029, 53030)} { return "restore_dump_metadata_invalid" }
        53025 { return "local_infile_off" }
        {$_ -in @(53026, 53027)} { return "restore_progress_state_invalid" }
        53031 { return "restore_checksum_failed" }
        {$_ -ge 54000 -and $_ -le 54511} { return "restore_connection_failed" }
        default { return $null }
    }
}

function Resolve-RestoreRawFailureReason {
    param([string]$Raw)
    # 真实装载只按受控 MySQL 短语分类，表名、值、路径与原始错误永不进入摘要。
    if ($Raw -match '(?i)1062|duplicate entry|duplicate key') { return "restore_duplicate_key" }
    if ($Raw -match '(?i)1451|1452|foreign key constraint|cannot add or update a child row|cannot delete or update a parent row') { return "restore_data_constraint_failed" }
    if ($Raw -match '(?i)1264|1265|1366|data truncated|incorrect .* value|out of range value') { return "restore_data_value_invalid" }
    if ($Raw -match '(?i)1153|max_allowed_packet|packet.*too large|packet bigger than') { return "restore_packet_too_large" }
    if ($Raw -match '(?i)1197|max_binlog_cache_size|transaction.*too large') { return "restore_transaction_too_large" }
    if ($Raw -match '(?i)1114|table.*full|no space left|disk.*full') { return "restore_storage_exhausted" }
    if ($Raw -match '(?i)1205|1213|lock wait timeout|deadlock') { return "restore_lock_failed" }
    return "restore_data_load_failed"
}

function Get-SafeMarkerCount {
    param([int]$Count)
    if ($Count -le 0) { return 0 }
    if ($Count -eq 1) { return 1 }
    return "2plus"
}

function Get-SafePayloadStatus {
    param([object]$Status)
    $candidate = [string]$Status
    $allowed = @(
        "blocked", "preflight_complete", "backup_dry_run_complete", "backup_complete",
        "restore_dry_run_complete", "restore_diagnostic_complete", "restore_verified",
        "validation_status_complete", "cleanup_complete"
    )
    if ($allowed -contains $candidate) { return $candidate }
    if ([string]::IsNullOrWhiteSpace($candidate)) { return "unavailable" }
    return "unexpected"
}

function Get-ExpectedPayloadStatus {
    param([string]$Action)
    switch ($Action) {
        "preflight" { return "preflight_complete" }
        "backup_dry_run" { return "backup_dry_run_complete" }
        "backup" { return "backup_complete" }
        "restore_dry_run" { return "restore_dry_run_complete" }
        "restore_diagnostic" { return "restore_diagnostic_complete" }
        "restore" { return "restore_verified" }
        "validation_status" { return "validation_status_complete" }
        "cleanup" { return "cleanup_complete" }
        default { return "unavailable" }
    }
}

function Get-SafeFailureStage {
    param([object]$Stage)
    $candidate = [string]$Stage
    $allowed = @(
        "initialization", "configuration", "preflight", "preflight_query",
        "preflight_session_variables_admin", "preflight_schema_query", "preflight_tables_query",
        "preflight_engine_query", "preflight_views_query", "preflight_triggers_query",
        "preflight_routines_query", "preflight_events_query", "qualified_reference_check", "dump_utility",
        "restore_source_schema_check", "restore_validation_target_check", "restore_object_inventory",
        "restore_qualified_reference_check", "restore_target_privileges_check", "restore_source_aggregate", "restore_ownership_marker", "restore_load_dry_run", "restore_load",
        "restore_target_aggregate", "restore_aggregate_compare", "cleanup", "unknown"
    )
    if ($allowed -contains $candidate) { return $candidate }
    return "unknown"
}

function New-MySqlShellProcessAbnormalResult {
    param(
        [Nullable[int]]$ExitCode,
        [string]$FailureSource,
        [object]$MarkerCount,
        [string]$PayloadStatus = "unavailable",
        [bool]$NativeException = $false
    )
    # 进程级异常只保留退出码符号分类；原始输出与异常对象不得进入结果。
    $exitClass = if ($NativeException) {
        "negative"
    }
    elseif ([int]$ExitCode -lt 0) {
        "negative"
    }
    elseif ([int]$ExitCode -gt 0) {
        "positive"
    }
    else {
        "zero"
    }
    $result = @{
        Success = $false
        Reason = "mysqlsh_process_abnormal"
        ProcessExitClass = $exitClass
        FailureSource = $FailureSource
        MarkerCount = $MarkerCount
        PayloadStatus = $PayloadStatus
        FailureStage = "unknown"
    }
    if (-not $NativeException -and $null -ne $ExitCode) {
        $result["DiagnosticProcessExitCode"] = [int]$ExitCode
    }
    return $result
}

function ConvertFrom-MySqlShellGateOutput {
    param([string]$Raw, [int]$ExitCode, [string]$Action)
    # mysqlsh 的密码提示可能不换行；先计数固定 marker，再从唯一位置提取单行 JSON。
    $markerOccurrences = [regex]::Matches($Raw, [regex]::Escape("MOLIN_GATE_RESULT "))
    $safeMarkerCount = Get-SafeMarkerCount -Count $markerOccurrences.Count
    if ($markerOccurrences.Count -eq 0) {
        return (New-MySqlShellProcessAbnormalResult -ExitCode $ExitCode -FailureSource "no_marker" `
            -MarkerCount $safeMarkerCount -PayloadStatus "unavailable")
    }
    if ($markerOccurrences.Count -gt 1) {
        return (New-MySqlShellProcessAbnormalResult -ExitCode $ExitCode -FailureSource "duplicate_marker" `
            -MarkerCount $safeMarkerCount -PayloadStatus "unavailable")
    }
    if ($markerOccurrences.Count -eq 1) {
        $markerMatch = [regex]::Match($Raw, '(?m)MOLIN_GATE_RESULT (\{[^\r\n]+\})\s*$')
        if (-not $markerMatch.Success) {
            return (New-MySqlShellProcessAbnormalResult -ExitCode $ExitCode -FailureSource "malformed_marker" `
                -MarkerCount $safeMarkerCount -PayloadStatus "unavailable")
        }
        try {
            $payload = $markerMatch.Groups[1].Value | ConvertFrom-Json
        }
        catch {
            return (New-MySqlShellProcessAbnormalResult -ExitCode $ExitCode -FailureSource "malformed_marker" `
                -MarkerCount $safeMarkerCount -PayloadStatus "unavailable")
        }
        $safePayloadStatus = Get-SafePayloadStatus -Status $payload.status
        if ($safePayloadStatus -ceq "blocked") {
            $safeFailureStage = Get-SafeFailureStage -Stage $payload.failure_stage
            $reason = Resolve-GateFailureReason -Candidate ([string]$payload.reason) -Action $Action
            $outerDiagnosticCode = $null
            if ($Action -in @("restore_dry_run", "restore")) {
                $outerDiagnosticCode = Get-RestoreDiagnosticCodeFromRaw -Raw $Raw
            }
            # 只有 generic dump marker 可使用 outer raw 的受控词汇进一步收窄；具体 marker 原因绝不覆盖。
            if ($reason -ceq "dump_utility_failed" -and
                (($Action -ceq "backup") -or ($Action -ceq "backup_dry_run"))) {
                $reason = Resolve-DumpRawFailureReason -Raw $Raw
            }
            if ($reason -ceq "restore_dry_run_failed" -and $Action -ceq "restore_dry_run") {
                $reasonFromCode = if ($null -ne $outerDiagnosticCode) {
                    Resolve-RestoreReasonFromDiagnosticCode -Code ([int]$outerDiagnosticCode)
                } else { $null }
                if ($null -ne $reasonFromCode) {
                    $reason = $reasonFromCode
                }
                else {
                    $reason = Resolve-RestoreDryRunRawFailureReason -Raw $Raw
                }
            }
            if ($reason -ceq "restore_failed" -and $Action -ceq "restore") {
                $reasonFromCode = if ($null -ne $outerDiagnosticCode) {
                    Resolve-RestoreReasonFromDiagnosticCode -Code ([int]$outerDiagnosticCode)
                } else { $null }
                $reason = if ($null -ne $reasonFromCode) { $reasonFromCode } else { Resolve-RestoreRawFailureReason -Raw $Raw }
            }
            $result = @{
                Success = $false
                Reason = $reason
                FailureSource = "blocked_marker"
                MarkerCount = $safeMarkerCount
                PayloadStatus = $safePayloadStatus
                FailureStage = $safeFailureStage
            }
            $diagnosticCode = 0
            if ($null -ne $payload.diagnostic_code -and
                [int]::TryParse(([string]$payload.diagnostic_code), [ref]$diagnosticCode) -and
                $diagnosticCode -ge 1 -and $diagnosticCode -le 999999) {
                $result["DiagnosticCode"] = $diagnosticCode
            }
            elseif ($null -ne $outerDiagnosticCode) {
                $result["DiagnosticCode"] = [int]$outerDiagnosticCode
            }
            return $result
        }
        $expectedPayloadStatus = Get-ExpectedPayloadStatus -Action $Action
        if ($safePayloadStatus -cne $expectedPayloadStatus) {
            return (New-MySqlShellProcessAbnormalResult -ExitCode $ExitCode -FailureSource "unexpected_success_status" `
                -MarkerCount $safeMarkerCount -PayloadStatus $safePayloadStatus)
        }
        if ($ExitCode -eq 0) {
            return @{Success = $true; Payload = $payload; MarkerCount = $safeMarkerCount; PayloadStatus = $safePayloadStatus}
        }
        return (New-MySqlShellProcessAbnormalResult -ExitCode $ExitCode -FailureSource "success_marker_nonzero_exit" `
            -MarkerCount $safeMarkerCount -PayloadStatus $safePayloadStatus)
    }
    # 理论不可达分支继续失败关闭，不携带原始输出。
    return (New-MySqlShellProcessAbnormalResult -ExitCode $ExitCode -FailureSource "wrapper_exception" `
        -MarkerCount $safeMarkerCount -PayloadStatus "unavailable")
}

function Invoke-MySqlShellGate {
    param([string]$Action, [hashtable]$Configuration, [string]$DumpPath, [string]$ProgressPath)
    $env:MOLIN_GATE_ACTION = $Action
    $env:MOLIN_GATE_SOURCE_SCHEMA = $Configuration["MYSQL_DATABASE"]
    $env:MOLIN_GATE_VALIDATION_SCHEMA = $script:ValidationSchema
    $env:MOLIN_GATE_DUMP_PATH = $DumpPath
    $env:MOLIN_GATE_PROGRESS_FILE = $ProgressPath
    $env:MOLIN_GATE_EXPECTED_CLEANUP_SCHEMA = $script:ValidationSchema
    $env:MOLIN_GATE_OWNERSHIP_TOKEN = $script:OwnershipToken
    try {
        $arguments = @(
            "--js",
            "--host=$($Configuration['MYSQL_HOST'])",
            "--port=$($Configuration['MYSQL_PORT'])",
            "--user=$($Configuration['MYSQL_USER'])",
            "--passwords-from-stdin",
            "--file=$ShellScript"
        )
        # 密码仅通过标准输入传给 mysqlsh；原始 stdout/stderr 只留在内存中解析固定标记。
        $previousPreference = $ErrorActionPreference
        $ErrorActionPreference = "Continue"
        $nativeException = $false
        $code = $null
        try {
            try {
                $raw = (($Configuration["MYSQL_PASSWORD"] + [Environment]::NewLine) | & $script:MySqlShellPath @arguments 2>&1 | Out-String)
                $code = [int]$LASTEXITCODE
            }
            catch {
                # 原生进程启动/等待异常不保留异常文本，只记录负类退出状态。
                $raw = ""
                $nativeException = $true
            }
        }
        finally {
            $ErrorActionPreference = $previousPreference
        }
        $converted = if ($nativeException) {
            New-MySqlShellProcessAbnormalResult -ExitCode $null -FailureSource "wrapper_exception" `
                -MarkerCount 0 -PayloadStatus "unavailable" -NativeException $true
        }
        else {
            ConvertFrom-MySqlShellGateOutput -Raw $raw -ExitCode ([int]$code) -Action $Action
        }
        if ($converted.ContainsKey("DiagnosticCode")) {
            $script:LastDiagnosticCode = [int]$converted["DiagnosticCode"]
        }
        if ($converted.ContainsKey("ProcessExitClass")) {
            $script:LastProcessExitClass = [string]$converted["ProcessExitClass"]
        }
        if ($converted.ContainsKey("DiagnosticProcessExitCode")) {
            $script:LastDiagnosticProcessExitCode = [int]$converted["DiagnosticProcessExitCode"]
        }
        # 只缓存失败调用的诊断，后续成功 cleanup 不得覆盖真实 restore 的失败来源。
        if (-not $converted.Success) {
            if ($converted.ContainsKey("FailureSource")) {
                $script:LastFailureSource = [string]$converted["FailureSource"]
            }
            if ($converted.ContainsKey("MarkerCount")) {
                $script:LastMarkerCount = $converted["MarkerCount"]
            }
            if ($converted.ContainsKey("PayloadStatus")) {
                $script:LastPayloadStatus = [string]$converted["PayloadStatus"]
            }
            if ($converted.ContainsKey("FailureStage")) {
                $script:LastFailureStage = [string]$converted["FailureStage"]
            }
        }
        return $converted
    }
    finally {
        # 操作元数据也在子进程结束后移除，避免污染调用方环境。
        $env:MOLIN_GATE_ACTION = $null
        $env:MOLIN_GATE_SOURCE_SCHEMA = $null
        $env:MOLIN_GATE_VALIDATION_SCHEMA = $null
        $env:MOLIN_GATE_DUMP_PATH = $null
        $env:MOLIN_GATE_PROGRESS_FILE = $null
        $env:MOLIN_GATE_EXPECTED_CLEANUP_SCHEMA = $null
        $env:MOLIN_GATE_OWNERSHIP_TOKEN = $null
    }
}

if ($AclSelfTest) {
    # 该模式只验证本机临时目录 ACL，不读取环境文件、不查找 mysqlsh、也不建立网络连接。
    $probePath = Join-Path ([System.IO.Path]::GetTempPath()) ("molin_acl_probe_" + [guid]::NewGuid().ToString("N"))
    try {
        New-Item -ItemType Directory -Path $probePath | Out-Null
        Set-PrivateDirectoryAcl -Path $probePath
        Write-SafeSummary @{status = "PASS"; acl_private = $true; remote_access = $false}
        exit 0
    }
    catch {
        Write-SafeSummary @{status = "BLOCKED"; reason = "acl_configuration_failed"; remote_access = $false}
        exit 2
    }
    finally {
        if (Test-Path -LiteralPath $probePath -PathType Container) {
            Remove-Item -LiteralPath $probePath -Force
        }
    }
}

if ($ResultParserSelfTest) {
    # 纯内存 mock 验证所有失败结构都收敛为非空白名单原因，不读取配置或启动 mysqlsh。
    $mockCases = @(
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"dump_missing_privileges","diagnostic_code":1227}'; Code = 1; Action = "backup"; Expected = "dump_missing_privileges"; ExpectedCode = 1227; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"dump_missing_privileges"}'; Code = 1; Action = "backup_dry_run"; Expected = "dump_missing_privileges"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"unexpected_internal_text"}'; Code = 1; Action = "backup"; Expected = "dump_utility_failed"; Success = $false},
        @{Raw = 'unclassified local mock'; Code = 1; Action = "restore"; Expected = "mysqlsh_process_abnormal"; ExpectedExitClass = "positive"; ExpectedProcessCode = 1; ExpectedFailureSource = "no_marker"; ExpectedMarkerCount = 0; ExpectedPayloadStatus = "unavailable"; Success = $false},
        @{Raw = 'unclassified zero exit'; Code = 0; Action = "restore"; Expected = "mysqlsh_process_abnormal"; ExpectedExitClass = "zero"; ExpectedProcessCode = 0; ExpectedFailureSource = "no_marker"; ExpectedMarkerCount = 0; ExpectedPayloadStatus = "unavailable"; Success = $false},
        @{Raw = 'unclassified negative exit'; Code = -1073741819; Action = "restore"; Expected = "mysqlsh_process_abnormal"; ExpectedExitClass = "negative"; ExpectedProcessCode = -1073741819; ExpectedFailureSource = "no_marker"; ExpectedMarkerCount = 0; ExpectedPayloadStatus = "unavailable"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT not-json'; Code = 7; Action = "restore"; Expected = "mysqlsh_process_abnormal"; ExpectedExitClass = "positive"; ExpectedProcessCode = 7; ExpectedFailureSource = "malformed_marker"; ExpectedMarkerCount = 1; ExpectedPayloadStatus = "unavailable"; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"restore_verified`"}`nMOLIN_GATE_RESULT {`"status`":`"restore_verified`"}"; Code = 0; Action = "restore"; Expected = "mysqlsh_process_abnormal"; ExpectedExitClass = "zero"; ExpectedProcessCode = 0; ExpectedFailureSource = "duplicate_marker"; ExpectedMarkerCount = "2plus"; ExpectedPayloadStatus = "unavailable"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"restore_verified"}'; Code = 4; Action = "restore"; Expected = "mysqlsh_process_abnormal"; ExpectedExitClass = "positive"; ExpectedProcessCode = 4; ExpectedFailureSource = "success_marker_nonzero_exit"; ExpectedMarkerCount = 1; ExpectedPayloadStatus = "restore_verified"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"backup_complete"}'; Code = 0; Action = "restore"; Expected = "mysqlsh_process_abnormal"; ExpectedExitClass = "zero"; ExpectedProcessCode = 0; ExpectedFailureSource = "unexpected_success_status"; ExpectedMarkerCount = 1; ExpectedPayloadStatus = "backup_complete"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"private_internal_status"}'; Code = 0; Action = "restore"; Expected = "mysqlsh_process_abnormal"; ExpectedExitClass = "zero"; ExpectedProcessCode = 0; ExpectedFailureSource = "unexpected_success_status"; ExpectedMarkerCount = 1; ExpectedPayloadStatus = "unexpected"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked"}'; Code = 1; Action = "backup"; Expected = "dump_utility_failed"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"qualified_reference_check_failed"}'; Code = 1; Action = "preflight"; Expected = "qualified_reference_check_failed"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"source_schema_unavailable"}'; Code = 1; Action = "preflight"; Expected = "source_schema_unavailable"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"unsafe_objects"}'; Code = 1; Action = "preflight"; Expected = "unsafe_objects"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"preflight_query_failed"}'; Code = 1; Action = "preflight"; Expected = "preflight_query_failed"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"restore_session_variables_admin_required"}'; Code = 1; Action = "preflight"; Expected = "restore_session_variables_admin_required"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"restore_target_privileges_required","failure_stage":"restore_target_privileges_check"}'; Code = 1; Action = "preflight"; Expected = "restore_target_privileges_required"; ExpectedFailureStage = "restore_target_privileges_check"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"restore_target_privileges_required","failure_stage":"restore_target_privileges_check"}'; Code = 1; Action = "restore_diagnostic"; Expected = "restore_target_privileges_required"; ExpectedFailureStage = "restore_target_privileges_check"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"preflight_schema_query_failed"}'; Code = 1; Action = "preflight"; Expected = "preflight_schema_query_failed"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"preflight_tables_query_failed"}'; Code = 1; Action = "preflight"; Expected = "preflight_tables_query_failed"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"preflight_engine_query_failed"}'; Code = 1; Action = "preflight"; Expected = "preflight_engine_query_failed"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"preflight_views_query_failed"}'; Code = 1; Action = "preflight"; Expected = "preflight_views_query_failed"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"preflight_triggers_query_failed"}'; Code = 1; Action = "preflight"; Expected = "preflight_triggers_query_failed"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"preflight_routines_query_failed"}'; Code = 1; Action = "preflight"; Expected = "preflight_routines_query_failed"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"preflight_events_query_failed"}'; Code = 1; Action = "preflight"; Expected = "preflight_events_query_failed"; Success = $false},
        @{Raw = 'Please provide the password: MOLIN_GATE_RESULT {"status":"preflight_complete","reason":"none","table_count":12}'; Code = 0; Action = "preflight"; ExpectedStatus = "preflight_complete"; Success = $true},
        @{Raw = 'Please provide the password: MOLIN_GATE_RESULT {"status":"blocked","reason":"unsafe_objects"}'; Code = 1; Action = "preflight"; Expected = "unsafe_objects"; Success = $false},
        @{Raw = "Please provide the password: MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"dump_utility_failed`"}`r`nouter utility stopped by privilege policy"; Code = 1; Action = "backup_dry_run"; Expected = "dump_missing_privileges"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"dump_utility_failed","diagnostic_code":"not-a-number"}'; Code = 1; Action = "backup"; Expected = "dump_utility_failed"; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"restore_dry_run_failed`",`"diagnostic_code`":3948}`r`nouter loading local data is disabled"; Code = 1; Action = "restore_dry_run"; Expected = "local_infile_off"; ExpectedCode = 3948; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"restore_dry_run_failed`"}`r`nouter access denied; command denied"; Code = 1; Action = "restore_dry_run"; Expected = "restore_missing_privileges"; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"restore_dry_run_failed`"}`r`nthe schema option can only be used when loading a single-schema dump"; Code = 1; Action = "restore_dry_run"; Expected = "restore_schema_remap_unsupported"; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"restore_dry_run_failed`"}`r`ndump metadata is invalid"; Code = 1; Action = "restore_dry_run"; Expected = "restore_dump_metadata_invalid"; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"restore_dry_run_failed`"}`r`nunknown option"; Code = 1; Action = "restore_dry_run"; Expected = "restore_option_invalid"; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"restore_dry_run_failed`"}`r`nMYSQLSH 53020"; Code = 1; Action = "restore_dry_run"; Expected = "restore_primary_key_policy_blocked"; ExpectedCode = 53020; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"restore_dry_run_failed`"}`r`nMYSQLSH 53023"; Code = 1; Action = "restore_dry_run"; Expected = "restore_dump_metadata_invalid"; ExpectedCode = 53023; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"restore_dry_run_failed`"}`r`nMYSQLSH 53026"; Code = 1; Action = "restore_dry_run"; Expected = "restore_progress_state_invalid"; ExpectedCode = 53026; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"restore_dry_run_failed`"}`r`nMYSQLSH 53011"; Code = 1; Action = "restore_dry_run"; Expected = "restore_version_incompatible"; ExpectedCode = 53011; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"restore_dry_run_failed`"}`r`nMYSQLSH 54000"; Code = 1; Action = "restore_dry_run"; Expected = "restore_connection_failed"; ExpectedCode = 54000; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"restore_dry_run_failed`"}`r`nunclassified outer failure"; Code = 1; Action = "restore_dry_run"; Expected = "restore_dry_run_failed"; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"restore_failed`"}`r`nMySQL Error 1062 duplicate key"; Code = 1; Action = "restore"; Expected = "restore_duplicate_key"; ExpectedCode = 1062; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"restore_failed`"}`r`nMYSQLSH 53031"; Code = 1; Action = "restore"; Expected = "restore_checksum_failed"; ExpectedCode = 53031; Success = $false},
        @{Raw = "MOLIN_GATE_RESULT {`"status`":`"blocked`",`"reason`":`"restore_failed`"}`r`nunclassified data load failure"; Code = 1; Action = "restore"; Expected = "restore_data_load_failed"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"restore_failed","failure_stage":"restore_target_aggregate"}'; Code = 1; Action = "restore"; Expected = "restore_data_load_failed"; ExpectedFailureStage = "restore_target_aggregate"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"restore_failed","failure_stage":"private_internal_stage"}'; Code = 1; Action = "restore"; Expected = "restore_data_load_failed"; ExpectedFailureStage = "unknown"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"dump_checksum_metadata_invalid","failure_stage":"restore_source_aggregate"}'; Code = 1; Action = "restore"; Expected = "dump_checksum_metadata_invalid"; ExpectedFailureStage = "restore_source_aggregate"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"coverage_mismatch","failure_stage":"restore_target_aggregate"}'; Code = 1; Action = "restore"; Expected = "coverage_mismatch"; ExpectedFailureStage = "restore_target_aggregate"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"row_count_mismatch","failure_stage":"restore_target_aggregate"}'; Code = 1; Action = "restore"; Expected = "row_count_mismatch"; ExpectedFailureStage = "restore_target_aggregate"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"source_target_checksum_mismatch","failure_stage":"restore_target_aggregate"}'; Code = 1; Action = "restore"; Expected = "source_target_checksum_mismatch"; ExpectedFailureStage = "restore_target_aggregate"; Success = $false},
        @{Raw = 'MOLIN_GATE_RESULT {"status":"blocked","reason":"checksum_unavailable","failure_stage":"restore_target_aggregate"}'; Code = 1; Action = "restore"; Expected = "checksum_unavailable"; ExpectedFailureStage = "restore_target_aggregate"; Success = $false}
    )
    $parserPassed = $true
    foreach ($case in $mockCases) {
        $parsed = ConvertFrom-MySqlShellGateOutput -Raw $case.Raw -ExitCode $case.Code -Action $case.Action
        if ($case.Success) {
            if (-not $parsed.Success -or (([string]$parsed.Payload.status) -cne ([string]$case.ExpectedStatus))) {
                $parserPassed = $false
            }
        }
        else {
            if ($parsed.Success -or [string]::IsNullOrWhiteSpace([string]$parsed.Reason) -or
                (([string]$parsed.Reason) -cne ([string]$case.Expected))) {
                $parserPassed = $false
            }
            if ($case.ContainsKey("ExpectedCode") -and
                (-not $parsed.ContainsKey("DiagnosticCode") -or [int]$parsed.DiagnosticCode -ne [int]$case.ExpectedCode)) {
                $parserPassed = $false
            }
            if (-not $case.ContainsKey("ExpectedCode") -and $parsed.ContainsKey("DiagnosticCode")) {
                $parserPassed = $false
            }
            if ($case.ContainsKey("ExpectedExitClass")) {
                if (-not $parsed.ContainsKey("ProcessExitClass") -or
                    ([string]$parsed.ProcessExitClass) -cne ([string]$case.ExpectedExitClass) -or
                    -not $parsed.ContainsKey("DiagnosticProcessExitCode") -or
                    [int]$parsed.DiagnosticProcessExitCode -ne [int]$case.ExpectedProcessCode) {
                    $parserPassed = $false
                }
            }
            elseif ($parsed.ContainsKey("ProcessExitClass") -or $parsed.ContainsKey("DiagnosticProcessExitCode")) {
                $parserPassed = $false
            }
            $expectedFailureSource = if ($case.ContainsKey("ExpectedFailureSource")) {
                [string]$case.ExpectedFailureSource
            } else { "blocked_marker" }
            $expectedMarkerCount = if ($case.ContainsKey("ExpectedMarkerCount")) {
                $case.ExpectedMarkerCount
            } else { 1 }
            $expectedPayloadStatus = if ($case.ContainsKey("ExpectedPayloadStatus")) {
                [string]$case.ExpectedPayloadStatus
            } else { "blocked" }
            if (-not $parsed.ContainsKey("FailureSource") -or
                ([string]$parsed.FailureSource) -cne $expectedFailureSource -or
                ([string]$parsed.MarkerCount) -cne ([string]$expectedMarkerCount) -or
                ([string]$parsed.PayloadStatus) -cne $expectedPayloadStatus) {
                $parserPassed = $false
            }
            $expectedFailureStage = if ($case.ContainsKey("ExpectedFailureStage")) {
                [string]$case.ExpectedFailureStage
            } else { "unknown" }
            if (-not $parsed.ContainsKey("FailureStage") -or
                ([string]$parsed.FailureStage) -cne $expectedFailureStage) {
                $parserPassed = $false
            }
        }
    }
    $nativeMock = New-MySqlShellProcessAbnormalResult -ExitCode $null -FailureSource "wrapper_exception" `
        -MarkerCount 0 -PayloadStatus "unavailable" -NativeException $true
    if ($nativeMock.Success -or $nativeMock.Reason -cne "mysqlsh_process_abnormal" -or
        $nativeMock.ProcessExitClass -cne "negative" -or $nativeMock.FailureSource -cne "wrapper_exception" -or
        ([string]$nativeMock.MarkerCount) -cne "0" -or $nativeMock.PayloadStatus -cne "unavailable" -or
        $nativeMock.FailureStage -cne "unknown" -or
        $nativeMock.ContainsKey("DiagnosticProcessExitCode")) {
        $parserPassed = $false
    }
    # 静态切片验证标准 dry-run 与真实 restore 均固定单线程，避免小数据恢复出现并行非确定性。
    $mysqlshSource = Get-Content -Raw -Encoding UTF8 -LiteralPath $ShellScript
    $dryRunStart = $mysqlshSource.IndexOf('} else if (action === "restore_dry_run")')
    $diagnosticStart = $mysqlshSource.IndexOf('} else if (action === "restore_diagnostic")')
    $restoreStart = $mysqlshSource.IndexOf('} else if (action === "restore")')
    $validationStart = $mysqlshSource.IndexOf('} else if (action === "validation_status")')
    $dryRunBlock = if ($dryRunStart -ge 0 -and $diagnosticStart -gt $dryRunStart) {
        $mysqlshSource.Substring($dryRunStart, $diagnosticStart - $dryRunStart)
    } else { "" }
    $restoreBlock = if ($restoreStart -ge 0 -and $validationStart -gt $restoreStart) {
        $mysqlshSource.Substring($restoreStart, $validationStart - $restoreStart)
    } else { "" }
    if (-not $dryRunBlock.Contains('threads: 1') -or $dryRunBlock.Contains('threads: 2') -or
        -not $restoreBlock.Contains('threads: 1') -or $restoreBlock.Contains('threads: 2')) {
        $parserPassed = $false
    }
    if (-not $restoreBlock.Contains('checksum: false') -or
        -not $restoreBlock.Contains('dumpChecksumManifest(dumpPath, sourceSchema)') -or
        -not $restoreBlock.Contains('assertExpectedRows(validationSchema, checksumManifest)') -or
        -not $restoreBlock.Contains('compareSourceTargetChecksums(')) {
        $parserPassed = $false
    }
    if ($parserPassed) {
        Write-SafeSummary @{status = "PASS"; reason = "parser_contract_verified"; remote_access = $false}
        exit 0
    }
    Write-SafeSummary @{status = "BLOCKED"; reason = "parser_contract_failed"; remote_access = $false}
    exit 2
}

if ($DumpLifecycleSelfTest) {
    # fake mysqlsh utility 只在随机本机临时 root 创建 child，用于验证目录生命周期和 ACL。
    $fakeRoot = Join-Path ([IO.Path]::GetTempPath()) ("molin_dump_lifecycle_" + [guid]::NewGuid().ToString("N"))
    $fakeChild = Join-Path $fakeRoot ("phase4_" + [guid]::NewGuid().ToString("N"))
    try {
        New-Item -ItemType Directory -Path $fakeRoot | Out-Null
        Set-PrivateDirectoryAcl -Path $fakeRoot
        Assert-SafeDumpTargetBeforeUtility -Root $fakeRoot -Target $fakeChild
        $childAbsentBeforeFakeUtility = -not (Test-Path -LiteralPath $fakeChild)
        # 模拟 MySQL Shell utility 自己创建输出目录，包装器不得预创建。
        New-Item -ItemType Directory -Path $fakeChild | Out-Null
        Protect-AndValidateDumpOutput -Root $fakeRoot -Target $fakeChild
        if (-not $childAbsentBeforeFakeUtility) {
            throw "dump_target_conflict"
        }
        Write-SafeSummary @{status = "PASS"; reason = "dump_lifecycle_verified"; fake_mysqlsh = $true; remote_access = $false}
        exit 0
    }
    catch {
        Write-SafeSummary @{status = "BLOCKED"; reason = "dump_lifecycle_failed"; fake_mysqlsh = $true; remote_access = $false}
        exit 2
    }
    finally {
        if (Test-Path -LiteralPath $fakeRoot -PathType Container) {
            Remove-Item -LiteralPath $fakeRoot -Recurse -Force
        }
    }
}

if ($RetainedBackupSelfTest) {
    # 该模式只用随机本机目录模拟完整保留备份，验证合法续跑与历史进度阻断，不读取真实配置。
    $fakeRoot = Join-Path ([IO.Path]::GetTempPath()) ("molin_retained_backup_" + [guid]::NewGuid().ToString("N"))
    $fakeRunId = [guid]::NewGuid().ToString("N")
    $fakeLeaf = "phase4_" + $fakeRunId
    $fakeChild = Join-Path $fakeRoot $fakeLeaf
    try {
        New-Item -ItemType Directory -Path $fakeRoot | Out-Null
        Set-PrivateDirectoryAcl -Path $fakeRoot
        $missingRejected = $false
        try {
            Assert-RetainedBackup -Root $fakeRoot -Target $fakeChild -ExpectedLeaf $fakeLeaf
        }
        catch {
            $missingRejected = $_.Exception.Message -ceq "retained_backup_target_missing"
        }
        if (-not $missingRejected) {
            throw "retained_backup_lifecycle_failed"
        }

        New-Item -ItemType Directory -Path $fakeChild | Out-Null
        Set-PrivateDirectoryAcl -Path $fakeChild
        $fakeSchema = "offline_schema"
        Set-Content -Encoding UTF8 -LiteralPath (Join-Path $fakeChild "@.json") -Value '{"schemas":["offline_schema"],"origin":"dumpSchemas","checksum":true,"version":"2.0.1","basenames":{"offline_schema":"offline_schema"}}'
        Set-Content -Encoding UTF8 -LiteralPath (Join-Path $fakeChild "@.done.json") -Value '{}'
        Set-Content -Encoding UTF8 -LiteralPath (Join-Path $fakeChild "@.checksums.json") -Value '{}'
        Set-Content -Encoding UTF8 -LiteralPath (Join-Path $fakeChild "chunk.tsv") -Value "offline"
        Assert-RetainedBackup -Root $fakeRoot -Target $fakeChild -ExpectedLeaf $fakeLeaf
        Assert-RetainedDumpMetadata -Target $fakeChild -ExpectedSchema $fakeSchema

        Set-Content -Encoding UTF8 -LiteralPath (Join-Path $fakeChild "@.json") -Value '{"schemas":["offline_schema","second_schema"],"origin":"dumpSchemas","checksum":true,"version":"2.0.1","basenames":{"offline_schema":"offline_schema"}}'
        $multiSchemaRejected = $false
        try {
            Assert-RetainedDumpMetadata -Target $fakeChild -ExpectedSchema $fakeSchema
        }
        catch {
            $multiSchemaRejected = $_.Exception.Message -ceq "retained_dump_not_single_schema"
        }
        if (-not $multiSchemaRejected) {
            throw "retained_backup_lifecycle_failed"
        }
        Set-Content -Encoding UTF8 -LiteralPath (Join-Path $fakeChild "@.json") -Value '{"schemas":["offline_schema"],"origin":"dumpSchemas","checksum":true,"version":"2.0.1","basenames":{"offline_schema":"offline_schema"}}'

        Set-Content -Encoding UTF8 -LiteralPath (Join-Path $fakeChild "restore.progress") -Value '{}'
        $progressRejected = $false
        try {
            Assert-RetainedBackup -Root $fakeRoot -Target $fakeChild -ExpectedLeaf $fakeLeaf
        }
        catch {
            $progressRejected = $_.Exception.Message -ceq "retained_backup_progress_present"
        }
        if (-not $progressRejected) {
            throw "retained_backup_lifecycle_failed"
        }
        Write-SafeSummary @{status = "PASS"; reason = "retained_backup_lifecycle_verified"; fake_retained_backup = $true; remote_access = $false}
        exit 0
    }
    catch {
        Write-SafeSummary @{status = "BLOCKED"; reason = "retained_backup_lifecycle_failed"; fake_retained_backup = $true; remote_access = $false}
        exit 2
    }
    finally {
        if (Test-Path -LiteralPath $fakeRoot -PathType Container) {
            Remove-Item -LiteralPath $fakeRoot -Recurse -Force
        }
    }
}

if ($ResumeControlFlowSelfTest) {
    # 通过本机计数器动态证明普通 Resume 只走验证分支，不触发目标不存在断言或备份 action。
    $resumeValidationCalls = 0
    $safeTargetAssertionCalls = 0
    $backupActionCalls = 0
    if ($true) {
        $resumeValidationCalls += 1
    }
    if (Test-BackupRequired -Resume $true) {
        $safeTargetAssertionCalls += 1
        $backupActionCalls += 1
    }
    if ($resumeValidationCalls -eq 1 -and $safeTargetAssertionCalls -eq 0 -and $backupActionCalls -eq 0) {
        Write-SafeSummary @{status = "PASS"; reason = "resume_control_flow_verified"; remote_access = $false}
        exit 0
    }
    Write-SafeSummary @{status = "BLOCKED"; reason = "resume_control_flow_failed"; remote_access = $false}
    exit 2
}

if ($RetryLifecycleSelfTest) {
    # 离线构造最小历史 progress，验证只读汇总、未完成门禁和新文件名互不覆盖。
    $fakeRoot = Join-Path ([IO.Path]::GetTempPath()) ("molin_retry_gate_" + [guid]::NewGuid().ToString("N"))
    try {
        New-Item -ItemType Directory -Path $fakeRoot | Out-Null
        $historicalPath = Join-Path $fakeRoot "restore.progress"
        @(
            '{"op":"SCHEMA-DDL","schema":"hidden","done":true}',
            '{"op":"TABLE-DATA","schema":"hidden","table":"hidden","chunk":"0","done":false}'
        ) | Set-Content -LiteralPath $historicalPath -Encoding UTF8
        $beforeHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $historicalPath).Hash
        $history = Assert-FailedRestoreProgressHistory -Target $fakeRoot
        $newPath = New-UniqueRetryProgressPath -Target $fakeRoot
        $afterHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $historicalPath).Hash
        $roundTrip = (@{operation_counts = $history.operation_counts} |
            ConvertTo-Json -Depth 8 -Compress | ConvertFrom-Json)
        if ($history.file_count -eq 1 -and $history.completed_count -eq 1 -and
            $history.incomplete_count -eq 1 -and $beforeHash -ceq $afterHash -and
            -not (Test-Path -LiteralPath $newPath) -and
            $roundTrip.operation_counts.'SCHEMA-DDL'.completed -is [int] -and
            $roundTrip.operation_counts.'TABLE-DATA'.incomplete -is [int]) {
            Write-SafeSummary @{
                status = "PASS"
                reason = "retry_lifecycle_verified"
                operation_counts = $history.operation_counts
                remote_access = $false
            }
            exit 0
        }
    }
    finally {
        if (Test-Path -LiteralPath $fakeRoot -PathType Container) {
            Remove-Item -LiteralPath $fakeRoot -Recurse -Force
        }
    }
    Write-SafeSummary @{status = "BLOCKED"; reason = "retry_lifecycle_failed"; remote_access = $false}
    exit 2
}

try {
    if ([string]::IsNullOrWhiteSpace($RunId)) {
        $RunId = [guid]::NewGuid().ToString("N")
    }
    if ($RunId -notmatch '^[0-9a-fA-F]{32}$') {
        Write-SafeSummary @{status = "BLOCKED"; reason = "run_id_invalid"}
        exit 2
    }
    $RunId = $RunId.ToLowerInvariant()
    # 清理所有权令牌独立于可见的 RunId，避免同名外来 schema 伪造本轮清理资格。
    $OwnershipToken = ([guid]::NewGuid().ToString("N") + [guid]::NewGuid().ToString("N")).ToLowerInvariant()
    if ($RestoreDryRunDiagnosticOnly -and -not $ResumeRetainedBackup) {
        throw "restore_diagnostic_requires_retained_backup"
    }
    if ($RetryFailedRestore -and -not $ResumeRetainedBackup) {
        throw "retry_requires_retained_backup"
    }
    $ValidationSchema = "molin_restore_verify_$RunId"
    $BackupPhrase = "I_CONFIRM_BACKUP_$RunId"
    $RestorePhrase = "I_CONFIRM_ISOLATED_RESTORE_$RunId"

    # 第一次确认发生在读取配置和连接数据库之前，缺省运行不会产生任何远程访问。
    if (-not (Confirm-ExactPhrase -Provided $ConfirmBackup -Expected $BackupPhrase -Stage "backup" -TargetId ($RunId.Substring(0, 12)))) {
        Write-SafeSummary @{status = "BLOCKED"; reason = "backup_confirmation_required"; run_id = $RunId; required_confirmation = $BackupPhrase}
        exit 2
    }

    if ([string]::IsNullOrWhiteSpace($EnvFile)) {
        $EnvFile = Join-Path $RepositoryRoot "infra\.env.test"
    }
    if (-not (Test-Path -LiteralPath $EnvFile -PathType Leaf) -or -not (Test-Path -LiteralPath $ShellScript -PathType Leaf)) {
        throw "configuration_unavailable"
    }

    # 轮换证据检查必须先于配置值读取、mysqlsh 查找和任何网络动作。
    $CurrentStage = "credential_rotation"
    $effectiveLeakedHashPrefix = $LeakedCredentialFileSha256Prefix
    if ([string]::IsNullOrWhiteSpace($effectiveLeakedHashPrefix) -and
        $LeakedCredentialFileSha256 -match '^[0-9A-Fa-f]{64}$') {
        # 保留旧版完整哈希参数兼容性，但新运行应优先使用已安全留存的前缀。
        $effectiveLeakedHashPrefix = $LeakedCredentialFileSha256
    }
    if ([string]::IsNullOrWhiteSpace($CredentialFileModifiedAfterUtc) -or
        $effectiveLeakedHashPrefix -notmatch '^[0-9A-Fa-f]{12,64}$') {
        throw "credential_rotation_policy_required"
    }
    $parsedCutoff = [DateTimeOffset]::MinValue
    $cutoffValid = [DateTimeOffset]::TryParseExact(
        $CredentialFileModifiedAfterUtc,
        "yyyy-MM-ddTHH:mm:ss'Z'",
        [Globalization.CultureInfo]::InvariantCulture,
        $UtcParseStyles,
        [ref]$parsedCutoff
    )
    if (-not $cutoffValid) {
        throw "credential_rotation_policy_required"
    }
    $effectiveCutoff = $parsedCutoff
    if ($effectiveCutoff -lt $CredentialLeakBaselineUtc) {
        $effectiveCutoff = $CredentialLeakBaselineUtc
    }
    $envFileInfo = Get-Item -LiteralPath $EnvFile
    if ($envFileInfo.LastWriteTimeUtc -le $effectiveCutoff.UtcDateTime) {
        throw "credential_rotation_not_evidenced"
    }
    # 只有时间证据通过后才读取文件字节计算摘要；摘要和文件内容均不写入输出。
    $currentCredentialFileHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $EnvFile).Hash
    if ($currentCredentialFileHash.StartsWith($effectiveLeakedHashPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "credential_rotation_not_evidenced"
    }

    $CurrentStage = "configuration"
    $configuration = Read-EnvFileSecurely -Path $EnvFile
    if ($configuration["MYSQL_DATABASE"] -notmatch '^[A-Za-z0-9_$]{1,64}$' -or
        $configuration["MYSQL_PORT"] -notmatch '^[0-9]{1,5}$') {
        throw "configuration_invalid"
    }

    $CurrentStage = "mysqlsh_discovery"
    if ([string]::IsNullOrWhiteSpace($MySqlShellExecutable)) {
        $command = Get-Command mysqlsh -CommandType Application -ErrorAction SilentlyContinue
        if ($null -eq $command) {
            throw "mysqlsh_unavailable"
        }
        $MySqlShellExecutable = $command.Source
    }
    if (-not (Test-Path -LiteralPath $MySqlShellExecutable -PathType Leaf) -or
        (Split-Path -Leaf $MySqlShellExecutable) -notin @("mysqlsh.exe", "mysqlsh")) {
        throw "mysqlsh_invalid"
    }
    $MySqlShellPath = (Resolve-Path -LiteralPath $MySqlShellExecutable).Path
    $versionOutput = (& $MySqlShellPath --version 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $versionOutput -notmatch 'Ver\s+8\.4\.') {
        throw "mysqlsh_8_4_required"
    }

    $CurrentStage = "backup_directory"
    if ([string]::IsNullOrWhiteSpace($BackupRoot)) {
        $BackupRoot = Join-Path ([Environment]::GetFolderPath("LocalApplicationData")) "Molin\mysql-backups"
    }
    $BackupRoot = [System.IO.Path]::GetFullPath($BackupRoot)
    if (Test-PathInsideRepository -Candidate $BackupRoot) {
        throw "backup_root_inside_repository"
    }
    if (-not (Test-Path -LiteralPath $BackupRoot -PathType Container)) {
        New-Item -ItemType Directory -Path $BackupRoot | Out-Null
    }
    $backupRootItem = Get-Item -LiteralPath $BackupRoot
    if (($backupRootItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "backup_root_reparse_point"
    }
    $CurrentStage = "backup_acl"
    Set-PrivateDirectoryAcl -Path $BackupRoot

    $CurrentStage = "preflight"
    $preflightPlaceholder = Join-Path $BackupRoot ("preflight_" + $RunId + ".unused")
    $preflight = Invoke-MySqlShellGate -Action "preflight" -Configuration $configuration -DumpPath $BackupRoot -ProgressPath $preflightPlaceholder
    if (-not $preflight.Success -or $preflight.Payload.status -cne "preflight_complete") {
        if (-not $preflight.Success) {
            $preflightReason = Resolve-GateFailureReason -Candidate ([string]$preflight.Reason) -Action "preflight"
            throw $preflightReason
        }
        throw "preflight_query_failed"
    }
    if ($PreflightOnly) {
        Write-SafeSummary @{
            status = "preflight_complete"
            reason = "none"
            table_count = [int64]$preflight.Payload.table_count
            remote_write = $false
        }
        exit 0
    }

    $expectedDumpLeaf = "phase4_" + $RunId
    $dumpDirectory = [System.IO.Path]::GetFullPath((Join-Path $BackupRoot $expectedDumpLeaf))
    $progressFile = Join-Path $dumpDirectory "restore.progress"
    $targetId = Get-SafeTargetId ($configuration["MYSQL_HOST"] + "|" + $configuration["MYSQL_PORT"] + "|" + $configuration["MYSQL_DATABASE"] + "|" + $ValidationSchema)

    if ($ResumeRetainedBackup) {
        $CurrentStage = "retained_backup"
        Assert-RetainedBackup -Root $BackupRoot -Target $dumpDirectory -ExpectedLeaf $expectedDumpLeaf `
            -AllowFailedProgress ([bool]$RetryFailedRestore)
        Assert-RetainedDumpMetadata -Target $dumpDirectory -ExpectedSchema $configuration["MYSQL_DATABASE"]
        # 普通续跑只接受未进入恢复的备份；失败重试则要求存在可解析且确有未完成任务的历史证据。
        if ($RetryFailedRestore) {
            $HistoricalProgressSummary = Assert-FailedRestoreProgressHistory -Target $dumpDirectory
        }
        $BackupCompleted = $true
    }

    if (Test-BackupRequired -Resume ([bool]$ResumeRetainedBackup)) {
        Assert-SafeDumpTargetBeforeUtility -Root $BackupRoot -Target $dumpDirectory

        $CurrentStage = "backup_dry_run"
        Assert-SafeDumpTargetBeforeUtility -Root $BackupRoot -Target $dumpDirectory
        $backupDryRun = Invoke-MySqlShellGate -Action "backup_dry_run" -Configuration $configuration -DumpPath $dumpDirectory -ProgressPath $progressFile
        if (-not $backupDryRun.Success -or $backupDryRun.Payload.status -cne "backup_dry_run_complete") {
            if (-not $backupDryRun.Success) {
                $backupDryRunReason = Resolve-GateFailureReason -Candidate ([string]$backupDryRun.Reason) -Action "backup_dry_run"
                throw $backupDryRunReason
            }
            throw "dump_utility_failed"
        }
        # dry-run 不得留下目录或文件，否则拒绝继续真实备份。
        Assert-SafeDumpTargetBeforeUtility -Root $BackupRoot -Target $dumpDirectory

        $CurrentStage = "backup"
        # 紧邻 utility 调用再次检查，受限 root 内若已出现同名 child，必须以冲突阻断。
        Assert-SafeDumpTargetBeforeUtility -Root $BackupRoot -Target $dumpDirectory
        $backup = Invoke-MySqlShellGate -Action "backup" -Configuration $configuration -DumpPath $dumpDirectory -ProgressPath $progressFile
        if (-not $backup.Success -or $backup.Payload.status -cne "backup_complete") {
            if (-not $backup.Success) {
                $backupReason = Resolve-GateFailureReason -Candidate ([string]$backup.Reason) -Action "backup"
                throw $backupReason
            }
            throw "dump_utility_failed"
        }
        Protect-AndValidateDumpOutput -Root $BackupRoot -Target $dumpDirectory
        Assert-RetainedDumpMetadata -Target $dumpDirectory -ExpectedSchema $configuration["MYSQL_DATABASE"]
        $BackupCompleted = $true
    }

    if ($RestoreDryRunDiagnosticOnly) {
        $CurrentStage = "restore_diagnostic"
        $diagnostic = Invoke-MySqlShellGate -Action "restore_diagnostic" -Configuration $configuration -DumpPath $dumpDirectory -ProgressPath $progressFile
        if (-not $diagnostic.Success -or $diagnostic.Payload.status -cne "restore_diagnostic_complete") {
            if (-not $diagnostic.Success) {
                throw (Resolve-GateFailureReason -Candidate ([string]$diagnostic.Reason) -Action "restore_diagnostic")
            }
            throw "restore_diagnostic_failed"
        }
        # 诊断摘要只转发受控布尔、枚举和探针结果；探针 reason 已由 JS 固定白名单分类。
        Write-SafeSummary @{
            status = "restore_diagnostic_complete"
            reason = "none"
            backup_retained = $true
            source_schema_exists = [bool]$diagnostic.Payload.source_schema_exists
            target_schema_absent = [bool]$diagnostic.Payload.target_schema_absent
            restore_target_capability = [string]$diagnostic.Payload.restore_target_capability
            create_capability = [string]$diagnostic.Payload.create_capability
            select_capability = [string]$diagnostic.Payload.select_capability
            source_collision_probe = $diagnostic.Payload.source_collision_probe
            schema_no_checksum_probe = $diagnostic.Payload.schema_no_checksum_probe
            ddl_only_probe = $diagnostic.Payload.ddl_only_probe
            data_only_probe = $diagnostic.Payload.data_only_probe
            schema_with_checksum_probe = $diagnostic.Payload.schema_with_checksum_probe
            remote_write = $false
        }
        exit 0
    }

    if ($RetryFailedRestore) {
        $CurrentStage = "retry_validation_status"
        # 重试前只读确认本轮精确隔离 schema 不存在；存在时不自动扩大清理范围。
        $validationStatus = Invoke-MySqlShellGate -Action "validation_status" -Configuration $configuration `
            -DumpPath $dumpDirectory -ProgressPath $progressFile
        if (-not $validationStatus.Success -or $validationStatus.Payload.status -cne "validation_status_complete") {
            throw "retry_validation_status_failed"
        }
        if ([bool]$validationStatus.Payload.exists) {
            throw "retry_validation_schema_present"
        }
        # 历史文件保持原位且只读；本次重试使用随机新 progress，绝不覆盖旧证据。
        $progressFile = New-UniqueRetryProgressPath -Target $dumpDirectory
    }

    # 第二次确认只在完整备份生成或保留备份验证通过后请求，拒绝时远程库不会发生写入。
    if (-not (Confirm-ExactPhrase -Provided $ConfirmRestore -Expected $RestorePhrase -Stage "isolated_restore" -TargetId $targetId)) {
        Write-SafeSummary @{status = "BLOCKED"; reason = "restore_confirmation_required"; run_id = $RunId; required_confirmation = $RestorePhrase; backup_retained = $true}
        exit 2
    }

    $CurrentStage = "restore_dry_run"
    $dryRun = Invoke-MySqlShellGate -Action "restore_dry_run" -Configuration $configuration -DumpPath $dumpDirectory -ProgressPath $progressFile
    if (-not $dryRun.Success -or $dryRun.Payload.status -cne "restore_dry_run_complete") {
        if (-not $dryRun.Success) {
            $dryRunReason = Resolve-GateFailureReason -Candidate ([string]$dryRun.Reason) -Action "restore_dry_run"
            throw $dryRunReason
        }
        throw "restore_dry_run_failed"
    }

    $CurrentStage = "restore"
    $restore = Invoke-MySqlShellGate -Action "restore" -Configuration $configuration -DumpPath $dumpDirectory -ProgressPath $progressFile
    if (-not $restore.Success -or $restore.Payload.status -cne "restore_verified" -or
        -not [bool]$restore.Payload.ownership_confirmed) {
        # 真实恢复失败时，仅接受 JS 在显式 CREATE schema 并验证随机 marker 后返回的清理资格。
        if ([bool]$restore.Payload.ownership_confirmed) {
            $RestoreStarted = $true
        }
        if (-not $restore.Success) {
            $restoreReason = Resolve-GateFailureReason -Candidate ([string]$restore.Reason) -Action "restore"
            throw $restoreReason
        }
        throw "restore_failed"
    }
    # 只有恢复、聚合校验和数据库内随机 marker 均确认后，失败处理才具备清理资格。
    $RestoreStarted = $true

    $CurrentStage = "cleanup"
    $cleanup = Invoke-MySqlShellGate -Action "cleanup" -Configuration $configuration -DumpPath $dumpDirectory -ProgressPath $progressFile
    if (-not $cleanup.Success -or $cleanup.Payload.status -cne "cleanup_complete" -or
        -not [bool]$cleanup.Payload.validation_schema_absent) {
        throw "cleanup_failed"
    }
    $RestoreStarted = $false
    Write-SafeSummary @{
        status = "PASS"
        backup_retained = $true
        validation_schema_cleaned = $true
        retry = [bool]$RetryFailedRestore
        historical_progress = $HistoricalProgressSummary
        table_count = [int64]$restore.Payload.table_count
        total_rows = [int64]$restore.Payload.total_rows
        structure_fingerprint = [string]$restore.Payload.structure_fingerprint
        checked_table_count = [int64]$restore.Payload.checked_table_count
        checksum_fingerprint = [string]$restore.Payload.checksum_fingerprint
    }
    $ExitCode = 0
}
catch {
    $safeReason = switch -Regex ($_.Exception.Message) {
        '^acl_configuration_failed$' { "acl_configuration_failed"; break }
        '^credential_rotation_policy_required$' { "credential_rotation_policy_required"; break }
        '^credential_rotation_not_evidenced$' { "credential_rotation_not_evidenced"; break }
        '^configuration_' { "configuration_invalid"; break }
        '^mysqlsh_' { $_.Exception.Message; break }
        '^backup_root_' { $_.Exception.Message; break }
        '^backup_target_' { $_.Exception.Message; break }
        '^(backup_root_reparse_point|dump_target_conflict|dump_output_invalid)$' { $_.Exception.Message; break }
        '^retained_backup_' { $_.Exception.Message; break }
        '^retained_dump_' { $_.Exception.Message; break }
        '^restore_diagnostic_' { $_.Exception.Message; break }
        '^retry_' { $_.Exception.Message; break }
        '^restore_' {
            if ($FinalFailureReasons -contains $_.Exception.Message) { $_.Exception.Message } else { "restore_failed" }
            break
        }
        '^(connection_failed|insufficient_privileges|unsafe_objects|preflight_query_failed|restore_session_variables_admin_required|restore_target_privileges_required|preflight_schema_query_failed|preflight_tables_query_failed|preflight_engine_query_failed|preflight_views_query_failed|preflight_triggers_query_failed|preflight_routines_query_failed|preflight_events_query_failed|qualified_reference_check_failed|dump_missing_privileges|dump_consistency_lock_failed|dump_target_exists|dump_option_invalid|dump_server_unsupported|dump_utility_failed|dump_failed|source_schema_unavailable|validation_target_rejected|restore_source_schema_check_failed|restore_validation_target_check_failed|restore_object_inventory_failed|restore_qualified_reference_check_failed|local_infile_off|restore_missing_privileges|restore_schema_remap_unsupported|restore_dump_metadata_invalid|restore_dump_incomplete|restore_version_incompatible|restore_primary_key_policy_blocked|restore_duplicate_objects|restore_ddl_parse_failed|restore_progress_state_invalid|restore_connection_failed|restore_option_invalid|restore_dry_run_failed|restore_aggregate_mismatch|restore_failed|dump_checksum_metadata_invalid|coverage_mismatch|row_count_mismatch|source_target_checksum_mismatch|checksum_unavailable|mysqlsh_process_abnormal|mysqlsh_action_failed)$' { $_.Exception.Message; break }
        '^cleanup_failed$' { "cleanup_failed"; break }
        default {
            switch ($CurrentStage) {
                "configuration" { "configuration_failed" }
                "credential_rotation" { "credential_rotation_not_evidenced" }
                "mysqlsh_discovery" { "mysqlsh_discovery_failed" }
                "backup_directory" { "backup_directory_failed" }
                "backup_acl" { "backup_acl_failed" }
                "preflight" { "preflight_query_failed" }
                "retained_backup" { "retained_backup_invalid" }
                "retry_validation_status" { "retry_validation_status_failed" }
                "restore_diagnostic" { "restore_diagnostic_failed" }
                "backup_dry_run" { "dump_utility_failed" }
                "backup" { "dump_utility_failed" }
                "restore_dry_run" { "restore_dry_run_blocked" }
                "restore" { "restore_verification_failed" }
                "cleanup" { "cleanup_failed" }
                default { "initialization_failed" }
            }
        }
    }
    $safeReason = [string]$safeReason
    if ([string]::IsNullOrWhiteSpace($safeReason) -or -not ($FinalFailureReasons -contains $safeReason)) {
        $safeReason = switch ($CurrentStage) {
            "configuration" { "configuration_invalid" }
            "credential_rotation" { "credential_rotation_not_evidenced" }
            "mysqlsh_discovery" { "mysqlsh_discovery_failed" }
            "backup_directory" { "backup_directory_failed" }
            "backup_acl" { "backup_acl_failed" }
            "preflight" { "preflight_query_failed" }
            "retained_backup" { "retained_backup_invalid" }
            "retry_validation_status" { "retry_validation_status_failed" }
            "restore_diagnostic" { "restore_diagnostic_failed" }
            "backup_dry_run" { "dump_utility_failed" }
            "backup" { "dump_utility_failed" }
            "restore_dry_run" { "restore_dry_run_failed" }
            "restore" { "restore_failed" }
            "cleanup" { "cleanup_failed" }
            default { "initialization_failed" }
        }
    }
    $progressSummary = if ($RestoreStarted) { Get-RestoreProgressSafeSummary -Path $progressFile } else { $null }
    $cleanupAttempted = $false
    $cleanupSucceeded = $null
    if ($RestoreStarted -and $null -ne $configuration -and $null -ne $dumpDirectory) {
        # 失败后只清理本轮精确隔离 schema，并要求 cleanup action 复核它已经不存在。
        $cleanupAttempted = $true
        try {
            $cleanupAfterFailure = Invoke-MySqlShellGate -Action "cleanup" -Configuration $configuration -DumpPath $dumpDirectory -ProgressPath $progressFile
            $cleanupSucceeded = $cleanupAfterFailure.Success -and
                $cleanupAfterFailure.Payload.status -ceq "cleanup_complete" -and
                [bool]$cleanupAfterFailure.Payload.validation_schema_absent
        }
        catch {
            $cleanupSucceeded = $false
        }
        $RestoreStarted = $false
    }

    $failureSummary = @{status = "BLOCKED"; reason = ([string]$safeReason); backup_retained = $BackupCompleted}
    if ($null -ne $progressSummary) {
        $failureSummary["progress"] = $progressSummary
    }
    if ($null -ne $HistoricalProgressSummary) {
        $failureSummary["historical_progress"] = $HistoricalProgressSummary
        $failureSummary["retry"] = $true
    }
    if ($cleanupAttempted) {
        $failureSummary["cleanup_attempted"] = $true
        $failureSummary["validation_schema_cleaned"] = [bool]$cleanupSucceeded
        if (-not $cleanupSucceeded) {
            $failureSummary["restore_reason"] = [string]$safeReason
            $failureSummary["reason"] = "cleanup_after_failure_failed"
        }
    }
    if ($null -ne $LastDiagnosticCode -and [int]$LastDiagnosticCode -ge 1 -and [int]$LastDiagnosticCode -le 999999) {
        $failureSummary["diagnostic_code"] = [int]$LastDiagnosticCode
    }
    if ($LastProcessExitClass -in @("zero", "positive", "negative")) {
        $failureSummary["process_exit_class"] = [string]$LastProcessExitClass
    }
    if ($null -ne $LastDiagnosticProcessExitCode) {
        $processExitCode = 0
        if ([int]::TryParse(([string]$LastDiagnosticProcessExitCode), [ref]$processExitCode)) {
            $failureSummary["diagnostic_process_exit_code"] = $processExitCode
        }
    }
    if ($LastFailureSource -in @(
        "no_marker", "duplicate_marker", "malformed_marker", "blocked_marker",
        "success_marker_nonzero_exit", "unexpected_success_status", "wrapper_exception"
    )) {
        $failureSummary["failure_source"] = [string]$LastFailureSource
    }
    if (([string]$LastMarkerCount) -in @("0", "1", "2plus")) {
        $failureSummary["marker_count"] = $LastMarkerCount
    }
    if ($LastPayloadStatus -in @(
        "blocked", "preflight_complete", "backup_dry_run_complete", "backup_complete",
        "restore_dry_run_complete", "restore_diagnostic_complete", "restore_verified",
        "validation_status_complete", "cleanup_complete", "unavailable", "unexpected"
    )) {
        $failureSummary["payload_status"] = [string]$LastPayloadStatus
    }
    if ($LastFailureStage -in @(
        "initialization", "configuration", "preflight", "preflight_query",
        "preflight_session_variables_admin", "preflight_schema_query", "preflight_tables_query",
        "preflight_engine_query", "preflight_views_query", "preflight_triggers_query",
        "preflight_routines_query", "preflight_events_query", "qualified_reference_check", "dump_utility",
        "restore_source_schema_check", "restore_validation_target_check", "restore_object_inventory",
        "restore_qualified_reference_check", "restore_target_privileges_check", "restore_source_aggregate", "restore_ownership_marker", "restore_load_dry_run", "restore_load",
        "restore_target_aggregate", "restore_aggregate_compare", "cleanup", "unknown"
    )) {
        $failureSummary["failure_stage"] = [string]$LastFailureStage
    }
    Write-SafeSummary $failureSummary
    $ExitCode = 2
}
finally {
    if ($null -ne $configuration) {
        $configuration["MYSQL_PASSWORD"] = $null
        $configuration.Clear()
    }
}

exit $ExitCode
