$ErrorActionPreference = "Stop"
$RepositoryRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
$GateScript = Join-Path $RepositoryRoot "scripts\backup-verify-test-mysql.ps1"
$ShellScript = Join-Path $RepositoryRoot "scripts\mysql-backup-restore-gate.js"
$Failures = New-Object System.Collections.Generic.List[string]

function Assert-True {
    param([bool]$Condition, [string]$Name)
    if (-not $Condition) {
        $Failures.Add($Name)
    }
}

$gateSource = Get-Content -Raw -Encoding UTF8 -LiteralPath $GateScript
$shellSource = Get-Content -Raw -Encoding UTF8 -LiteralPath $ShellScript

function Invoke-LocalPrivilegeMock {
    param(
        [string[]]$GrantStatements,
        [object]$PartialRevokesValue = 0,
        [switch]$PartialRevokesQueryFails
    )
    $grantsJson = ConvertTo-Json -Compress -InputObject @($GrantStatements)
    $partialRevokesJson = ConvertTo-Json -Compress -InputObject $PartialRevokesValue
    $partialRevokesQueryFailsJson = if ($PartialRevokesQueryFails) { "true" } else { "false" }
    # 用纯本地 Node mock 执行真实 JS 预检分支；任何 utility 调用都会立即失败，保证测试不产生远程写。
    $mockPrelude = @"
const mockGrantStatements = $grantsJson;
function mockRows(rows) {
    let index = 0;
    return {fetchOne: function () { return index < rows.length ? rows[index++] : null; }};
}
const os = {getenv: function (name) {
    const values = {
        MOLIN_GATE_SOURCE_SCHEMA: "source_fixture",
        MOLIN_GATE_VALIDATION_SCHEMA: "molin_restore_verify_0123456789abcdef0123456789abcdef",
        MOLIN_GATE_DUMP_PATH: "unused_fixture",
        MOLIN_GATE_PROGRESS_FILE: "unused_fixture",
        MOLIN_GATE_OWNERSHIP_TOKEN: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        MOLIN_GATE_ACTION: "preflight"
    };
    return values[name];
}};
const session = {runSql: function (sql) {
    const text = String(sql);
    if (text.indexOf("@@GLOBAL.partial_revokes") !== -1) {
        if ($partialRevokesQueryFailsJson) { throw new Error("local_partial_revokes_query_failed"); }
        return mockRows([[$partialRevokesJson]]);
    }
    if (text.indexOf("CURRENT_ROLE()") !== -1) { return mockRows([["NONE"]]); }
    if (text.indexOf("SHOW GRANTS") === 0) {
        return mockRows(mockGrantStatements.map(function (grant) { return [grant]; }));
    }
    if (text.indexOf("information_schema.user_privileges") !== -1) { return mockRows([[1]]); }
    if (text.indexOf("information_schema.schemata") !== -1) { return mockRows([[1]]); }
    if (text.indexOf("SELECT table_name") === 0) { return mockRows([]); }
    if (text.indexOf("COUNT(*)") !== -1) { return mockRows([[0]]); }
    throw new Error("unexpected_local_query");
}};
const util = {
    dumpSchemas: function () { throw new Error("remote_write_forbidden"); },
    loadDump: function () { throw new Error("remote_write_forbidden"); }
};
function print(value) { console.log(value); }
"@
    $previousErrorActionPreference = $ErrorActionPreference
    try {
        # 缺权限用例预期 Node 非零退出，临时允许收集受控 marker，避免 PowerShell 把原生 stderr 提升为测试异常。
        $ErrorActionPreference = "Continue"
        $output = (($mockPrelude + "`n" + $shellSource) | & node - 2>&1 | Out-String)
        $nodeExitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    return @{Output = $output; ExitCode = $nodeExitCode}
}

function Invoke-LocalCleanupOwnershipMock {
    param([ValidateSet("foreign_schema", "marker_mismatch")][string]$Scenario)
    $markerTableCount = if ($Scenario -ceq "marker_mismatch") { 1 } else { 0 }
    $matchingMarkerCount = 0
    # 本地 mock 只执行 cleanup 分支并记录 DROP 次数，用于证明外来同名 schema 和错误 marker 均失败关闭。
    $mockPrelude = @"
let mockDropCount = 0;
function mockRows(rows) {
    let index = 0;
    return {fetchOne: function () { return index < rows.length ? rows[index++] : null; }};
}
const os = {getenv: function (name) {
    const values = {
        MOLIN_GATE_SOURCE_SCHEMA: "source_fixture",
        MOLIN_GATE_VALIDATION_SCHEMA: "molin_restore_verify_0123456789abcdef0123456789abcdef",
        MOLIN_GATE_DUMP_PATH: "unused_fixture",
        MOLIN_GATE_PROGRESS_FILE: "unused_fixture",
        MOLIN_GATE_ACTION: "cleanup",
        MOLIN_GATE_EXPECTED_CLEANUP_SCHEMA: "molin_restore_verify_0123456789abcdef0123456789abcdef",
        MOLIN_GATE_OWNERSHIP_TOKEN: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    };
    return values[name];
}};
const session = {runSql: function (sql) {
    const text = String(sql);
    if (text.indexOf("DROP SCHEMA") === 0) { mockDropCount += 1; return mockRows([]); }
    if (text.indexOf("information_schema.schemata") !== -1) { return mockRows([[1]]); }
    if (text.indexOf("information_schema.tables") !== -1) { return mockRows([[$markerTableCount]]); }
    if (text.indexOf("WHERE ownership_token = ?") !== -1) { return mockRows([[$matchingMarkerCount]]); }
    if (text.indexOf("SELECT COUNT(*) FROM `molin_restore_verify_") === 0) { return mockRows([[1]]); }
    throw new Error("unexpected_local_query");
}};
const util = {
    dumpSchemas: function () { throw new Error("remote_write_forbidden"); },
    loadDump: function () { throw new Error("remote_write_forbidden"); }
};
function print(value) { console.log(value); }
process.on("exit", function () { console.log("MOCK_DROP_COUNT=" + mockDropCount); });
"@
    $previousErrorActionPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        $output = (($mockPrelude + "`n" + $shellSource) | & node - 2>&1 | Out-String)
        $nodeExitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    return @{Output = $output; ExitCode = $nodeExitCode}
}

# 创建 ScriptBlock 只解析语法，不执行被测脚本。
try {
    $null = [scriptblock]::Create($gateSource)
}
catch {
    $Failures.Add("powershell_syntax")
}

# 静态红线保证脚本不会夹带 migration，也不会使用宽泛 schema 清理。
Assert-True (-not $gateSource.Contains("000055")) "migration_reference_forbidden"
Assert-True (-not $shellSource.Contains("000055")) "mysqlsh_migration_reference_forbidden"
Assert-True ($shellSource.Contains('validationSchema.indexOf("molin_restore_verify_") !== 0')) "cleanup_prefix_guard"
Assert-True ($shellSource.Contains('validationSchema !== requireEnv("MOLIN_GATE_EXPECTED_CLEANUP_SCHEMA")')) "cleanup_exact_target_guard"
Assert-True ($shellSource.Contains('assertOwnershipMarker(validationSchema, ownershipToken);')) "cleanup_ownership_marker_guard"
Assert-True ($shellSource.Contains('session.runSql("DROP SCHEMA " + quoteIdentifier(validationSchema))')) "cleanup_drop_without_if_exists"
Assert-True ($shellSource.Contains('session.runSql("CREATE SCHEMA " + quoteIdentifier(schemaName));') -and
    -not $shellSource.Contains('CREATE SCHEMA IF NOT EXISTS')) "owned_schema_create_fails_on_name_race"
Assert-True ($shellSource.Contains('createOwnedValidationSchema(validationSchema, ownershipToken);') -and
    $shellSource.IndexOf('ownershipConfirmed = true;') -gt
    $shellSource.IndexOf('createOwnedValidationSchema(validationSchema, ownershipToken);')) "cleanup_eligibility_after_owned_schema_marker"
Assert-True ($shellSource.Contains('ignoreExistingObjects: true')) "restore_allows_only_precreated_owned_schema"
Assert-True ($shellSource.Contains('if (ownershipConfirmed) {') -and
    $shellSource.Contains('blockedResult.ownership_confirmed = true;')) "failed_restore_reports_only_confirmed_cleanup_eligibility"
Assert-True ($gateSource.IndexOf('$RestoreStarted = $true', $gateSource.IndexOf('$CurrentStage = "restore"')) -gt
    $gateSource.IndexOf('-not [bool]$restore.Payload.ownership_confirmed')) "cleanup_eligibility_after_restore_and_marker_confirmation"
$foreignSchemaCleanup = Invoke-LocalCleanupOwnershipMock -Scenario "foreign_schema"
Assert-True ($foreignSchemaCleanup.ExitCode -ne 0 -and
    $foreignSchemaCleanup.Output.Contains('"reason":"cleanup_failed"') -and
    $foreignSchemaCleanup.Output.Contains('MOCK_DROP_COUNT=0')) "foreign_schema_race_cleanup_fails_closed"
$markerMismatchCleanup = Invoke-LocalCleanupOwnershipMock -Scenario "marker_mismatch"
Assert-True ($markerMismatchCleanup.ExitCode -ne 0 -and
    $markerMismatchCleanup.Output.Contains('"reason":"cleanup_failed"') -and
    $markerMismatchCleanup.Output.Contains('MOCK_DROP_COUNT=0')) "ownership_marker_mismatch_cleanup_fails_closed"
Assert-True ($gateSource.Contains('Test-PathInsideRepository')) "repository_backup_guard"
Assert-True ($gateSource.Contains('ConvertTo-Json -Depth 8 -Compress')) "safe_summary_nested_json_depth"
Assert-True ($gateSource.Contains('Set-PrivateDirectoryAcl')) "backup_acl_guard"
Assert-True ($gateSource.Contains('[System.Security.AccessControl.FileSystemAccessRule]::new(')) "acl_constructor_guard"
Assert-True ($gateSource.Contains('$verifiedAcl.GetAccessRules(')) "acl_runtime_verification"
Assert-True ($gateSource.Contains('"backup_acl" { "backup_acl_failed" }')) "acl_failure_classification"
Assert-True ($gateSource.Contains('"--passwords-from-stdin"')) "password_stdin_guard"
Assert-True (-not $gateSource.Contains('"--password=$')) "password_argument_forbidden"
Assert-True ($shellSource.Contains('checksum: true')) "checksum_required"
Assert-True ($shellSource.Contains('dryRun: true')) "restore_dry_run_required"
Assert-True ($shellSource.Contains('schema: validationSchema')) "schema_remap_required"
Assert-True ($shellSource.Contains('schema_remap_unsafe_objects')) "unsafe_object_fail_closed"
Assert-True ($shellSource.Contains('non_innodb_tables')) "non_innodb_fail_closed"
Assert-True ($shellSource.Contains('assertNoQualifiedSourceReferences(sourceSchema)')) "qualified_reference_fail_closed"
Assert-True ($shellSource.Contains('MOLIN_GATE_RESULT')) "mysqlsh_fixed_result_marker"
Assert-True ($shellSource.Contains('classifySafeReason(error, action, failureStage)')) "mysqlsh_safe_error_classifier"
Assert-True ($shellSource.Contains('action === "preflight"')) "mysqlsh_preflight_action"
Assert-True ($shellSource.Contains('status: "preflight_complete", table_count: summary.tables')) "preflight_safe_summary"
Assert-True ($shellSource.Contains('failureStage = "dump_utility"')) "dump_utility_stage"
Assert-True ($shellSource.Contains('return "dump_utility_failed"')) "dump_utility_fixed_reason"
Assert-True ($shellSource.Contains('action === "backup_dry_run"')) "backup_dry_run_action"
Assert-True ($shellSource.Contains('safeDiagnosticCode(error)')) "diagnostic_code_sanitized"
Assert-True ($gateSource.Contains('$failureSummary["diagnostic_code"]')) "diagnostic_code_failure_only"
Assert-True ($gateSource.Contains('Resolve-DumpRawFailureReason -Raw $Raw')) "generic_dump_outer_raw_refinement"
$genericPrivilegePlaceholder = "dump operation stopped by privilege policy"
Assert-True ($genericPrivilegePlaceholder -match '\bprivileges?\b' -and
    $shellSource.Contains('\bprivileges?\b')) "dump_generic_privilege_phrase"
Assert-True ($shellSource.Contains('stageSetter("preflight_tables_query")')) "preflight_table_stage"
Assert-True ($shellSource.Contains('function assertSessionVariablesAdminEvidence()')) "session_variables_admin_preflight"
Assert-True ($shellSource.Contains("privilege_type = 'SESSION_VARIABLES_ADMIN'")) "session_variables_admin_exact_evidence"
Assert-True ($shellSource.Contains("REPLACE(grantee, CHAR(39), '') = CURRENT_USER()")) "session_variables_admin_current_account_only"
Assert-True (-not $shellSource.Contains("privilege_type = 'SYSTEM_VARIABLES_ADMIN'")) "system_variables_admin_not_accepted"
Assert-True ($shellSource.Contains('preflight_session_variables_admin: "restore_session_variables_admin_required"')) "session_variables_admin_fixed_reason"
Assert-True ($shellSource.Contains('const requiredPrivileges = ["ALTER", "CREATE", "DROP", "INDEX", "INSERT", "REFERENCES", "SELECT"]')) "restore_target_required_privilege_set"
Assert-True ($shellSource.Contains('scalar("SELECT @@GLOBAL.partial_revokes")')) "partial_revokes_state_read_only_query"
Assert-True ($shellSource.Contains('throw new Error("partial_revokes_state_unknown")')) "partial_revokes_unknown_fail_closed"
Assert-True ($shellSource.Contains('function assertRestoreTargetPrivileges(schemaName)')) "restore_target_privilege_assertion"
Assert-True ($shellSource.Contains('restore_target_privileges_check: "restore_target_privileges_required"')) "restore_target_privilege_fixed_reason"
Assert-True ($gateSource.Contains('"restore_target_privileges_required"')) "wrapper_restore_target_privilege_reason_whitelist"
$requiredRestorePrivileges = @("ALTER", "CREATE", "DROP", "INDEX", "INSERT", "REFERENCES", "SELECT")
foreach ($missingPrivilege in $requiredRestorePrivileges) {
    $remainingPrivileges = @($requiredRestorePrivileges | Where-Object { $_ -cne $missingPrivilege }) -join ", "
    $missingPrivilegeMock = Invoke-LocalPrivilegeMock @(
        ("GRANT {0} ON ``molin\_restore\_verify\_%``.* TO ``fixture``@``fixture``" -f $remainingPrivileges)
    )
    Assert-True ($missingPrivilegeMock.ExitCode -ne 0 -and
        $missingPrivilegeMock.Output.Contains('"reason":"restore_target_privileges_required"') -and
        $missingPrivilegeMock.Output.Contains('"failure_stage":"restore_target_privileges_check"') -and
        -not $missingPrivilegeMock.Output.Contains('"status":"preflight_complete"')) ("missing_{0}_fails_before_preflight_success" -f $missingPrivilege.ToLowerInvariant())
}
$exactTargetScope = 'molin\_restore\_verify\_0123456789abcdef0123456789abcdef'
$partialRevokeMock = Invoke-LocalPrivilegeMock -PartialRevokesValue 1 -GrantStatements @(
    'GRANT ALTER, CREATE, DROP, INDEX, INSERT, REFERENCES, SELECT ON *.* TO `fixture`@`fixture`',
    ("REVOKE SELECT ON ``{0}``.* FROM ``fixture``@``fixture``" -f $exactTargetScope)
)
Assert-True ($partialRevokeMock.ExitCode -ne 0 -and
    $partialRevokeMock.Output.Contains('"reason":"restore_target_privileges_required"') -and
    -not $partialRevokeMock.Output.Contains('"status":"preflight_complete"')) "target_partial_revoke_overrides_global_grant"
$schemaCaseMismatchMock = Invoke-LocalPrivilegeMock @(
    'GRANT ALTER, CREATE, DROP, INDEX, INSERT, REFERENCES, SELECT ON `Molin\_restore\_verify\_%`.* TO `fixture`@`fixture`'
)
Assert-True ($schemaCaseMismatchMock.ExitCode -ne 0 -and
    $schemaCaseMismatchMock.Output.Contains('"reason":"restore_target_privileges_required"') -and
    -not $schemaCaseMismatchMock.Output.Contains('"status":"preflight_complete"')) "schema_scope_case_sensitive"
$completePrivilegeMock = Invoke-LocalPrivilegeMock @(
    'GRANT ALTER, CREATE, DROP, INDEX, INSERT, REFERENCES, SELECT ON `molin\_restore\_verify\_%`.* TO `fixture`@`fixture`'
)
Assert-True ($completePrivilegeMock.ExitCode -eq 0 -and
    $completePrivilegeMock.Output.Contains('"status":"preflight_complete"')) "partial_revokes_off_unescaped_percent_matches_range"
$partialRevokesOnRangeMock = Invoke-LocalPrivilegeMock -PartialRevokesValue 1 -GrantStatements @(
    'GRANT ALTER, CREATE, DROP, INDEX, INSERT, REFERENCES, SELECT ON `molin\_restore\_verify\_%`.* TO `fixture`@`fixture`'
)
Assert-True ($partialRevokesOnRangeMock.ExitCode -ne 0 -and
    $partialRevokesOnRangeMock.Output.Contains('"reason":"restore_target_privileges_required"') -and
    -not $partialRevokesOnRangeMock.Output.Contains('"status":"preflight_complete"')) "partial_revokes_on_unescaped_percent_is_literal"
$partialRevokesOnExactMock = Invoke-LocalPrivilegeMock -PartialRevokesValue 1 -GrantStatements @(
    ("GRANT ALTER, CREATE, DROP, INDEX, INSERT, REFERENCES, SELECT ON ``{0}``.* TO ``fixture``@``fixture``" -f $exactTargetScope)
)
Assert-True ($partialRevokesOnExactMock.ExitCode -eq 0 -and
    $partialRevokesOnExactMock.Output.Contains('"status":"preflight_complete"')) "partial_revokes_on_exact_escaped_scope_passes"
$unescapedUnderscoreRange = 'molin\_restore\_verify\_0_23456789abcdef0123456789abcdef'
$partialRevokesOffUnderscoreMock = Invoke-LocalPrivilegeMock -PartialRevokesValue 0 -GrantStatements @(
    ("GRANT ALTER, CREATE, DROP, INDEX, INSERT, REFERENCES, SELECT ON ``{0}``.* TO ``fixture``@``fixture``" -f $unescapedUnderscoreRange)
)
Assert-True ($partialRevokesOffUnderscoreMock.ExitCode -eq 0 -and
    $partialRevokesOffUnderscoreMock.Output.Contains('"status":"preflight_complete"')) "partial_revokes_off_unescaped_underscore_is_wildcard"
$partialRevokesOnUnderscoreMock = Invoke-LocalPrivilegeMock -PartialRevokesValue 1 -GrantStatements @(
    ("GRANT ALTER, CREATE, DROP, INDEX, INSERT, REFERENCES, SELECT ON ``{0}``.* TO ``fixture``@``fixture``" -f $unescapedUnderscoreRange)
)
Assert-True ($partialRevokesOnUnderscoreMock.ExitCode -ne 0 -and
    $partialRevokesOnUnderscoreMock.Output.Contains('"reason":"restore_target_privileges_required"')) "partial_revokes_on_unescaped_underscore_is_literal"
$escapedWildcardLiteralMock = Invoke-LocalPrivilegeMock -PartialRevokesValue 0 -GrantStatements @(
    'GRANT ALTER, CREATE, DROP, INDEX, INSERT, REFERENCES, SELECT ON `molin\_restore\_verify\_0123\%`.* TO `fixture`@`fixture`'
)
Assert-True ($escapedWildcardLiteralMock.ExitCode -ne 0 -and
    $escapedWildcardLiteralMock.Output.Contains('"reason":"restore_target_privileges_required"')) "escaped_percent_remains_literal"
$unknownPartialRevokesMock = Invoke-LocalPrivilegeMock -PartialRevokesValue "unexpected" -GrantStatements @(
    ("GRANT ALTER, CREATE, DROP, INDEX, INSERT, REFERENCES, SELECT ON ``{0}``.* TO ``fixture``@``fixture``" -f $exactTargetScope)
)
Assert-True ($unknownPartialRevokesMock.ExitCode -ne 0 -and
    $unknownPartialRevokesMock.Output.Contains('"reason":"restore_target_privileges_required"') -and
    -not $unknownPartialRevokesMock.Output.Contains('"status":"preflight_complete"')) "partial_revokes_unknown_value_fails_closed"
$failedPartialRevokesQueryMock = Invoke-LocalPrivilegeMock -PartialRevokesQueryFails -GrantStatements @(
    ("GRANT ALTER, CREATE, DROP, INDEX, INSERT, REFERENCES, SELECT ON ``{0}``.* TO ``fixture``@``fixture``" -f $exactTargetScope)
)
Assert-True ($failedPartialRevokesQueryMock.ExitCode -ne 0 -and
    $failedPartialRevokesQueryMock.Output.Contains('"reason":"restore_target_privileges_required"') -and
    -not $failedPartialRevokesQueryMock.Output.Contains('"status":"preflight_complete"')) "partial_revokes_query_failure_fails_closed"
Assert-True ($shellSource.Contains('preflight_events_query: "preflight_events_query_failed"')) "preflight_stage_reason_map"
Assert-True ($shellSource.Contains('restore_source_schema_check: "restore_source_schema_check_failed"') -and
    $shellSource.Contains('restore_validation_target_check: "restore_validation_target_check_failed"') -and
    $shellSource.Contains('restore_object_inventory: "restore_object_inventory_failed"') -and
    $shellSource.Contains('restore_qualified_reference_check: "restore_qualified_reference_check_failed"')) "restore_dry_run_stage_reason_map"
Assert-True ($shellSource.Contains('return "local_infile_off"') -and
    $shellSource.Contains('return "restore_missing_privileges"') -and
    $shellSource.Contains('return "restore_schema_remap_unsupported"') -and
    $shellSource.Contains('return "restore_dump_metadata_invalid"') -and
    $shellSource.Contains('return "restore_option_invalid"')) "restore_dry_run_controlled_classifier"
Assert-True ($gateSource.Contains('Resolve-RestoreDryRunRawFailureReason -Raw $Raw')) "restore_outer_raw_refinement"
Assert-True ($gateSource.Contains('Resolve-RestoreRawFailureReason -Raw $Raw')) "real_restore_outer_raw_refinement"
Assert-True ($gateSource.Contains('Get-RestoreDiagnosticCodeFromRaw -Raw $Raw') -and
    $gateSource.Contains('Resolve-RestoreReasonFromDiagnosticCode')) "restore_numeric_diagnostic_refinement"
Assert-True ($gateSource.Contains('"local_infile_off", "restore_missing_privileges", "restore_schema_remap_unsupported"')) "restore_reason_whitelist"
Assert-True ($gateSource.Contains('function Assert-RetainedDumpMetadata')) "retained_dump_metadata_validator"
Assert-True ($gateSource.Contains('retained_dump_not_single_schema') -and
    $gateSource.Contains('retained_dump_schema_mismatch') -and
    $gateSource.Contains('retained_dump_checksum_missing')) "retained_dump_metadata_fixed_reasons"
Assert-True ($gateSource.Contains('retained_backup_target_missing') -and
    $gateSource.Contains('retained_backup_acl_invalid') -and
    $gateSource.Contains('retained_backup_progress_present')) "retained_backup_fixed_subreasons"
Assert-True ($gateSource.Contains('retained_dump_metadata_missing') -and
    $gateSource.Contains('retained_dump_done_invalid') -and
    $gateSource.Contains('retained_dump_checksum_empty') -and
    $gateSource.Contains('retained_dump_basenames_missing')) "retained_metadata_fixed_subreasons"
Assert-True ($gateSource.Contains('$checksumItem.Length -le 0') -and
    $gateSource.Contains('Windows PowerShell 5 无法反序列化的键形态')) "checksum_file_ps5_compatible_validation"
Assert-True ($gateSource.Contains('[switch]$PreflightOnly')) "wrapper_preflight_only_switch"
Assert-True ($gateSource.Contains('status = "preflight_complete"')) "wrapper_preflight_only_summary"
Assert-True ($gateSource.Contains('[switch]$ResumeRetainedBackup')) "resume_retained_backup_switch"
Assert-True ($gateSource.Contains('[switch]$RetryFailedRestore')) "retry_failed_restore_switch"
Assert-True ($gateSource.Contains('[switch]$RetryLifecycleSelfTest')) "retry_lifecycle_selftest_switch"
Assert-True ($gateSource.Contains('[switch]$RetainedBackupSelfTest')) "retained_backup_selftest_switch"
Assert-True ($gateSource.Contains('[switch]$RestoreDryRunDiagnosticOnly')) "restore_diagnostic_switch"
Assert-True ($gateSource.Contains('[switch]$ResumeControlFlowSelfTest')) "resume_control_flow_selftest_switch"
Assert-True ($shellSource.Contains('action === "restore_diagnostic"')) "restore_diagnostic_action"
Assert-True ($shellSource.Contains('source_collision_probe: sourceCollision') -and
    $shellSource.Contains('schema_no_checksum_probe: schemaNoChecksum') -and
    $shellSource.Contains('ddl_only_probe: ddlOnly') -and $shellSource.Contains('data_only_probe: dataOnly') -and
    $shellSource.Contains('schema_with_checksum_probe: schemaWithChecksum')) "restore_diagnostic_matrix"
Assert-True ($shellSource.Contains('restore_target_capability: targetPrivilegeEvidence.overall') -and
    $shellSource.Contains('create_capability: targetPrivilegeEvidence.create') -and
    $shellSource.Contains('select_capability: targetPrivilegeEvidence.select')) "restore_target_capability_enums"
Assert-True ($gateSource.Contains('Resolve-GateFailureReason -Candidate ([string]$diagnostic.Reason) -Action "restore_diagnostic"')) "restore_diagnostic_preserves_privilege_failure"
Assert-True ($gateSource.Contains('remote_write = $false') -and
    $gateSource.Contains('status = "restore_diagnostic_complete"')) "restore_diagnostic_safe_summary"
Assert-True ($gateSource.Contains('function Assert-RetainedBackup')) "retained_backup_validator"
Assert-True ($gateSource.Contains('Assert-PrivateDirectoryAcl -Path $fullTarget')) "retained_backup_acl_readback"
Assert-True ($gateSource.Contains('"@.json", "@.done.json"')) "retained_backup_markers_required"
Assert-True ($gateSource.Contains('$_.Name -like "*.progress"')) "retained_backup_progress_rejected"
Assert-True ($gateSource.Contains('-AllowFailedProgress ([bool]$RetryFailedRestore)')) "retry_explicit_progress_exception"
Assert-True ($gateSource.Contains('function Assert-FailedRestoreProgressHistory')) "retry_history_validator"
Assert-True ($gateSource.Contains('retry_progress_reparse') -and
    $gateSource.Contains('retry_progress_invalid') -and
    $gateSource.Contains('retry_progress_no_incomplete')) "retry_history_fixed_reasons"
Assert-True ($gateSource.Contains('"restore_retry_" + [guid]::NewGuid().ToString("N") + ".progress"')) "retry_unique_progress_name"
Assert-True ($gateSource.Contains('Invoke-MySqlShellGate -Action "validation_status"')) "retry_validation_status_gate"
Assert-True ($gateSource.Contains('if ([bool]$validationStatus.Payload.exists)')) "retry_validation_schema_absent_required"
Assert-True ($gateSource.Contains('historical_progress = $HistoricalProgressSummary')) "retry_safe_history_summary"
Assert-True (-not $gateSource.Contains('New-Item -ItemType Directory -Path $dumpDirectory')) "dump_child_not_precreated"
$dumpAbsentCheckPosition = $gateSource.LastIndexOf('Assert-SafeDumpTargetBeforeUtility -Root $BackupRoot -Target $dumpDirectory')
$dumpInvokePosition = $gateSource.IndexOf('$backup = Invoke-MySqlShellGate -Action "backup"')
$dumpAclPosition = $gateSource.IndexOf('Protect-AndValidateDumpOutput -Root $BackupRoot -Target $dumpDirectory')
$backupCompletePosition = $gateSource.LastIndexOf('$BackupCompleted = $true')
Assert-True ($dumpAbsentCheckPosition -ge 0 -and $dumpAbsentCheckPosition -lt $dumpInvokePosition -and
    $dumpAclPosition -gt $dumpInvokePosition -and $dumpAclPosition -lt $backupCompletePosition) "dump_lifecycle_order"
$resumeBranchPosition = $gateSource.IndexOf('if ($ResumeRetainedBackup)')
$resumeValidationPosition = $gateSource.IndexOf('Assert-RetainedBackup -Root $BackupRoot -Target $dumpDirectory')
$backupGuardPosition = $gateSource.IndexOf('if (Test-BackupRequired -Resume ([bool]$ResumeRetainedBackup))')
$diagnosticBranchPosition = $gateSource.IndexOf('if ($RestoreDryRunDiagnosticOnly)', $resumeValidationPosition)
$restoreConfirmationPosition = $gateSource.IndexOf('Confirm-ExactPhrase -Provided $ConfirmRestore')
Assert-True ($resumeBranchPosition -ge 0 -and $resumeValidationPosition -gt $resumeBranchPosition -and
    $backupGuardPosition -gt $resumeValidationPosition -and $dumpInvokePosition -gt $backupGuardPosition) "resume_skips_dump_branch"
Assert-True ($diagnosticBranchPosition -gt $dumpAclPosition -and
    $diagnosticBranchPosition -lt $restoreConfirmationPosition) "diagnostic_after_exclusive_backup_decision"
Assert-True (-not [regex]::IsMatch($gateSource,
    '(?s)if \(\$RestoreDryRunDiagnosticOnly\).*?\}\s*else\s*\{\s*Assert-SafeDumpTargetBeforeUtility')) "diagnostic_not_backup_else_owner"
Assert-True ($gateSource.Contains('# 普通续跑只接受未进入恢复的备份；失败重试则要求存在可解析且确有未完成任务的历史证据。')) "resume_no_overwrite_contract"
Assert-True (-not $gateSource.Contains('Remove-Item -LiteralPath $dumpDirectory')) "retained_backup_not_deleted"
Assert-True (-not $gateSource.Contains('Remove-Item -LiteralPath $progressFile')) "retained_progress_not_deleted"
Assert-True ($gateSource.Contains('$GateFailureReasons')) "wrapper_failure_reason_whitelist"
Assert-True ($gateSource.Contains('if ($markerOccurrences.Count -eq 1)')) "single_marker_required"
Assert-True ($gateSource.Contains('(?m)MOLIN_GATE_RESULT')) "prompt_prefix_marker_supported"
Assert-True ($gateSource.Contains('Resolve-GateFailureReason -Candidate ([string]$backup.Reason)')) "backup_reason_normalized_before_throw"
Assert-True ($gateSource.Contains('reason = ([string]$safeReason)')) "final_reason_explicit_cast"
Assert-True ($shellSource.Contains('payload.reason = payload.status === "blocked" ? "mysqlsh_action_failed" : "none"')) "mysqlsh_marker_reason_required"
Assert-True ($gateSource.Contains('"2026-06-26T12:18:59Z"')) "credential_leak_time_baseline"
Assert-True ($gateSource.Contains('$envFileInfo.LastWriteTimeUtc -le $effectiveCutoff.UtcDateTime')) "credential_time_gate"
Assert-True ($gateSource.Contains('$effectiveLeakedHashPrefix -notmatch ''^[0-9A-Fa-f]{12,64}$''')) "credential_prefix_format_gate"
Assert-True ($gateSource.Contains('$currentCredentialFileHash.StartsWith($effectiveLeakedHashPrefix')) "credential_hash_prefix_gate"
$rotationGatePosition = $gateSource.IndexOf('$CurrentStage = "credential_rotation"')
$configurationReadPosition = $gateSource.IndexOf('$configuration = Read-EnvFileSecurely')
$mysqlshDiscoveryPosition = $gateSource.IndexOf('$CurrentStage = "mysqlsh_discovery"')
Assert-True ($rotationGatePosition -ge 0 -and $rotationGatePosition -lt $configurationReadPosition -and
    $rotationGatePosition -lt $mysqlshDiscoveryPosition) "credential_gate_before_values_and_mysqlsh"
Assert-True ($shellSource.Contains('assertAggregateEqual(sourceAggregate, targetAggregate)')) "aggregate_comparison_required"
Assert-True ($shellSource.Contains('sourceAggregate.table_count !== targetAggregate.table_count')) "table_count_comparison_required"
Assert-True ($shellSource.Contains('sourceAggregate.total_rows !== targetAggregate.total_rows')) "total_rows_comparison_required"
Assert-True ($shellSource.Contains('sourceAggregate.structure_fingerprint !== targetAggregate.structure_fingerprint')) "fingerprint_comparison_required"
$restoreDryRunStart = $shellSource.IndexOf('} else if (action === "restore_dry_run")')
$restoreDiagnosticStart = $shellSource.IndexOf('} else if (action === "restore_diagnostic")')
$restoreStart = $shellSource.IndexOf('} else if (action === "restore")')
$preflightStart = $shellSource.IndexOf('if (action === "preflight")')
$backupDryRunStart = $shellSource.IndexOf('} else if (action === "backup_dry_run")')
$preflightBlock = if ($preflightStart -ge 0 -and $backupDryRunStart -gt $preflightStart) {
    $shellSource.Substring($preflightStart, $backupDryRunStart - $preflightStart)
} else { "" }
Assert-True ($preflightBlock.Contains('assertRestoreTargetPrivileges(validationSchema);') -and
    $preflightBlock.IndexOf('assertRestoreTargetPrivileges(validationSchema);') -lt
    $preflightBlock.IndexOf('status: "preflight_complete"')) "preflight_target_privilege_gate_before_success"
$restoreDryRunBlock = if ($restoreDryRunStart -ge 0 -and $restoreDiagnosticStart -gt $restoreDryRunStart) {
    $shellSource.Substring($restoreDryRunStart, $restoreDiagnosticStart - $restoreDryRunStart)
} else { "" }
Assert-True ($restoreDryRunBlock.Contains('failureStage = "restore_source_schema_check";') -and
    $restoreDryRunBlock.Contains('assertRestoreTargetPrivileges(validationSchema);') -and
    $restoreDryRunBlock.Contains('failureStage = "restore_validation_target_check";') -and
    $restoreDryRunBlock.Contains('failureStage = "restore_object_inventory";') -and
    $restoreDryRunBlock.Contains('failureStage = "restore_qualified_reference_check";') -and
    $restoreDryRunBlock.Contains('failureStage = "restore_load_dry_run";')) "restore_dry_run_stage_sequence"
Assert-True ($restoreDryRunBlock.Contains('schema: validationSchema') -and
    $restoreDryRunBlock.Contains('dryRun: true') -and -not $restoreDryRunBlock.Contains('checksum:')) "restore_dry_run_options"
Assert-True (-not $restoreDryRunBlock.Contains('progressFile: progressFile')) "restore_dry_run_progress_file_omitted"
Assert-True ($restoreDryRunBlock.Contains('progressFile: ""')) "restore_dry_run_progress_tracking_disabled"
$restoreDiagnosticBlock = if ($restoreDiagnosticStart -ge 0 -and $restoreStart -gt $restoreDiagnosticStart) {
    $shellSource.Substring($restoreDiagnosticStart, $restoreStart - $restoreDiagnosticStart)
} else { "" }
Assert-True ($restoreDiagnosticBlock.Contains('assertRestoreTargetPrivileges(validationSchema);') -and
    $restoreDiagnosticBlock.IndexOf('assertRestoreTargetPrivileges(validationSchema);') -lt
    $restoreDiagnosticBlock.IndexOf('runRestoreDryRunProbe(')) "restore_diagnostic_privilege_gate_before_probes"
Assert-True ($shellSource.Contains('code === 53020') -and $shellSource.Contains('code === 53025') -and
    $shellSource.Contains('code === 53026 || code === 53027') -and
    $shellSource.Contains('code >= 54000 && code <= 54511')) "mysqlsh_official_load_error_code_map"
$restoreBlock = if ($restoreStart -ge 0) { $shellSource.Substring($restoreStart) } else { "" }
Assert-True ($restoreBlock.Contains('assertRestoreTargetPrivileges(validationSchema);') -and
    $restoreBlock.IndexOf('assertRestoreTargetPrivileges(validationSchema);') -lt
    $restoreBlock.IndexOf('util.loadDump(dumpPath')) "real_restore_privilege_preflight_before_write"
Assert-True ($restoreBlock.Contains('checksum: true') -and
    $restoreBlock.Contains('progressFile: progressFile')) "real_restore_checksum_preserved"
Assert-True ($restoreBlock.Contains('failureStage = "restore_source_aggregate";') -and
    $restoreBlock.Contains('failureStage = "restore_load";') -and
    $restoreBlock.Contains('failureStage = "restore_target_aggregate";') -and
    $restoreBlock.Contains('failureStage = "restore_aggregate_compare";')) "real_restore_stage_sequence"
Assert-True ($shellSource.Contains('return "restore_duplicate_key"') -and
    $shellSource.Contains('return "restore_data_constraint_failed"') -and
    $shellSource.Contains('failureStage === "restore_load" ? "restore_data_load_failed"')) "real_restore_controlled_classifier"
Assert-True ($shellSource.Contains('status: "cleanup_complete", validation_schema_absent: true')) "cleanup_absence_evidence"
Assert-True ($gateSource.Contains('-not [bool]$cleanup.Payload.validation_schema_absent')) "cleanup_success_requires_absence"
$backupPreflightPosition = $shellSource.IndexOf('assertRemapSafe(summary);')
$dumpPosition = $shellSource.IndexOf('util.dumpSchemas([sourceSchema]')
Assert-True ($backupPreflightPosition -ge 0 -and $backupPreflightPosition -lt $dumpPosition) "unsafe_object_preflight_before_dump"

# 使用严格 UTF-8 解码并检查中文注释，避免 Windows 默认代码页造成文件实际乱码。
try {
    $strictUtf8 = New-Object System.Text.UTF8Encoding($false, $true)
    $decodedShellSource = $strictUtf8.GetString([IO.File]::ReadAllBytes($ShellScript))
    Assert-True ($decodedShellSource.Contains("在产生含敏感数据的备份前先确认它具备安全隔离恢复条件")) "mysqlsh_chinese_comment_utf8"
}
catch {
    $Failures.Add("mysqlsh_utf8_decode")
}

# 固定 run_id 的无确认运行必须在读取不存在的 env 文件或查找 mysqlsh 之前失败关闭。
$runId = "0123456789abcdef0123456789abcdef"
$missingEnv = Join-Path ([System.IO.Path]::GetTempPath()) ("molin_missing_" + [guid]::NewGuid().ToString("N") + ".env")
$output = (& powershell -NoProfile -ExecutionPolicy Bypass -File $GateScript -RunId $runId -EnvFile $missingEnv -NonInteractive 2>&1 | Out-String).Trim()
$exitCode = $LASTEXITCODE
Assert-True ($exitCode -eq 2) "default_fail_closed_exit"
try {
    $summary = $output | ConvertFrom-Json
    Assert-True ($summary.status -ceq "BLOCKED") "default_fail_closed_status"
    Assert-True ($summary.reason -ceq "backup_confirmation_required") "confirmation_precedes_configuration"
    Assert-True ($summary.required_confirmation -ceq ("I_CONFIRM_BACKUP_" + $runId)) "exact_backup_confirmation"
}
catch {
    $Failures.Add("safe_json_output")
}

# 默认阻断摘要不得出现敏感配置键、连接地址或 schema 名。
Assert-True ($output -notmatch 'MYSQL_PASSWORD|MYSQL_HOST|MYSQL_USER|MYSQL_DATABASE') "summary_configuration_name_leak"
Assert-True ($output -notmatch 'molin_restore_verify_') "summary_schema_name_leak"

# 使用不含凭据的临时文件验证泄露时间边界；必须在配置解析与 mysqlsh 查找前阻断。
$rotationProbe = Join-Path ([IO.Path]::GetTempPath()) ("molin_rotation_probe_" + [guid]::NewGuid().ToString("N") + ".env")
try {
    Set-Content -Encoding ASCII -LiteralPath $rotationProbe -Value "# offline rotation probe"
    $baselineUtc = [datetime]::Parse("2026-06-26T12:18:59Z").ToUniversalTime()
    (Get-Item -LiteralPath $rotationProbe).LastWriteTimeUtc = $baselineUtc
    $backupConfirmation = "I_CONFIRM_BACKUP_" + $runId
    $oldTimeOutput = (& powershell -NoProfile -ExecutionPolicy Bypass -File $GateScript `
        -RunId $runId -EnvFile $rotationProbe -NonInteractive -ConfirmBackup $backupConfirmation `
        -CredentialFileModifiedAfterUtc "2026-06-26T12:18:59Z" `
        -LeakedCredentialFileSha256Prefix ("A" * 12) 2>&1 | Out-String).Trim()
    $oldTimeExit = $LASTEXITCODE
    $oldTimeSummary = $oldTimeOutput | ConvertFrom-Json
    Assert-True ($oldTimeExit -eq 2 -and $oldTimeSummary.reason -ceq "credential_rotation_not_evidenced") "credential_time_boundary_blocked"

    # 即使文件时间较新，只要摘要仍命中显式传入的 12 位旧前缀也必须阻断。
    (Get-Item -LiteralPath $rotationProbe).LastWriteTimeUtc = [datetime]::UtcNow
    $probeHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $rotationProbe).Hash
    $sameHashOutput = (& powershell -NoProfile -ExecutionPolicy Bypass -File $GateScript `
        -RunId $runId -EnvFile $rotationProbe -NonInteractive -ConfirmBackup $backupConfirmation `
        -CredentialFileModifiedAfterUtc "2026-06-26T12:18:59Z" `
        -LeakedCredentialFileSha256Prefix ($probeHash.Substring(0, 12)) 2>&1 | Out-String).Trim()
    $sameHashExit = $LASTEXITCODE
    $sameHashSummary = $sameHashOutput | ConvertFrom-Json
    Assert-True ($sameHashExit -eq 2 -and $sameHashSummary.reason -ceq "credential_rotation_not_evidenced") "credential_same_prefix_blocked"

    # 不同前缀必须放行到下一本地门禁；临时文件没有配置值，因此应由配置完整性门禁阻断。
    $differentPrefix = if ($probeHash.StartsWith("A", [StringComparison]::OrdinalIgnoreCase)) { "B" * 12 } else { "A" * 12 }
    $differentHashOutput = (& powershell -NoProfile -ExecutionPolicy Bypass -File $GateScript `
        -RunId $runId -EnvFile $rotationProbe -NonInteractive -ConfirmBackup $backupConfirmation `
        -CredentialFileModifiedAfterUtc "2026-06-26T12:18:59Z" `
        -LeakedCredentialFileSha256Prefix $differentPrefix 2>&1 | Out-String).Trim()
    $differentHashExit = $LASTEXITCODE
    $differentHashSummary = $differentHashOutput | ConvertFrom-Json
    Assert-True ($differentHashExit -eq 2 -and $differentHashSummary.reason -ceq "configuration_invalid") "credential_different_prefix_reaches_configuration_gate"

    $missingPolicyOutput = (& powershell -NoProfile -ExecutionPolicy Bypass -File $GateScript `
        -RunId $runId -EnvFile $rotationProbe -NonInteractive -ConfirmBackup $backupConfirmation 2>&1 | Out-String).Trim()
    $missingPolicyExit = $LASTEXITCODE
    $missingPolicySummary = $missingPolicyOutput | ConvertFrom-Json
    Assert-True ($missingPolicyExit -eq 2 -and $missingPolicySummary.reason -ceq "credential_rotation_policy_required") "credential_policy_required"
}
catch {
    $Failures.Add("credential_rotation_selftest")
}
finally {
    if (Test-Path -LiteralPath $rotationProbe -PathType Leaf) {
        Remove-Item -LiteralPath $rotationProbe -Force
    }
}

# ACL 自测只创建并精确删除随机本机临时目录，不读取真实配置或启动 mysqlsh。
$aclOutput = (& powershell -NoProfile -ExecutionPolicy Bypass -File $GateScript -AclSelfTest 2>&1 | Out-String).Trim()
$aclExitCode = $LASTEXITCODE
Assert-True ($aclExitCode -eq 0) "acl_selftest_exit"
try {
    $aclSummary = $aclOutput | ConvertFrom-Json
    Assert-True ($aclSummary.status -ceq "PASS" -and $aclSummary.acl_private -eq $true -and
        $aclSummary.remote_access -eq $false) "acl_selftest_summary"
}
catch {
    $Failures.Add("acl_selftest_safe_json")
}

$parserOutput = (& powershell -NoProfile -ExecutionPolicy Bypass -File $GateScript -ResultParserSelfTest 2>&1 | Out-String).Trim()
$parserExitCode = $LASTEXITCODE
Assert-True ($parserExitCode -eq 0) "result_parser_selftest_exit"
try {
    $parserSummary = $parserOutput | ConvertFrom-Json
    Assert-True ($parserSummary.status -ceq "PASS" -and $parserSummary.reason -ceq "parser_contract_verified" -and
        $parserSummary.remote_access -eq $false) "result_parser_selftest_summary"
}
catch {
    $Failures.Add("result_parser_selftest_safe_json")
}

$lifecycleOutput = (& powershell -NoProfile -ExecutionPolicy Bypass -File $GateScript -DumpLifecycleSelfTest 2>&1 | Out-String).Trim()
$lifecycleExitCode = $LASTEXITCODE
Assert-True ($lifecycleExitCode -eq 0) "dump_lifecycle_selftest_exit"
try {
    $lifecycleSummary = $lifecycleOutput | ConvertFrom-Json
    Assert-True ($lifecycleSummary.status -ceq "PASS" -and $lifecycleSummary.reason -ceq "dump_lifecycle_verified" -and
        $lifecycleSummary.fake_mysqlsh -eq $true -and $lifecycleSummary.remote_access -eq $false) "dump_lifecycle_selftest_summary"
}
catch {
    $Failures.Add("dump_lifecycle_selftest_safe_json")
}

$retainedOutput = (& powershell -NoProfile -ExecutionPolicy Bypass -File $GateScript -RetainedBackupSelfTest 2>&1 | Out-String).Trim()
$retainedExitCode = $LASTEXITCODE
Assert-True ($retainedExitCode -eq 0) "retained_backup_selftest_exit"
try {
    $retainedSummary = $retainedOutput | ConvertFrom-Json
    Assert-True ($retainedSummary.status -ceq "PASS" -and
        $retainedSummary.reason -ceq "retained_backup_lifecycle_verified" -and
        $retainedSummary.fake_retained_backup -eq $true -and
        $retainedSummary.remote_access -eq $false) "retained_backup_selftest_summary"
    Assert-True ($retainedOutput -notmatch 'phase4_|molin_retained_backup_|restore\.progress') "retained_backup_selftest_no_path_leak"
}
catch {
    $Failures.Add("retained_backup_selftest_safe_json")
}

$resumeFlowOutput = (& powershell -NoProfile -ExecutionPolicy Bypass -File $GateScript -ResumeControlFlowSelfTest 2>&1 | Out-String).Trim()
$resumeFlowExitCode = $LASTEXITCODE
Assert-True ($resumeFlowExitCode -eq 0) "resume_control_flow_selftest_exit"
try {
    $resumeFlowSummary = $resumeFlowOutput | ConvertFrom-Json
    Assert-True ($resumeFlowSummary.status -ceq "PASS" -and
        $resumeFlowSummary.reason -ceq "resume_control_flow_verified" -and
        $resumeFlowSummary.remote_access -eq $false) "resume_control_flow_selftest_summary"
}
catch {
    $Failures.Add("resume_control_flow_selftest_safe_json")
}

$retryOutput = (& powershell -NoProfile -ExecutionPolicy Bypass -File $GateScript -RetryLifecycleSelfTest 2>&1 | Out-String).Trim()
$retryExitCode = $LASTEXITCODE
Assert-True ($retryExitCode -eq 0) "retry_lifecycle_selftest_exit"
try {
    $retrySummary = $retryOutput | ConvertFrom-Json
    Assert-True ($retrySummary.status -ceq "PASS" -and
        $retrySummary.reason -ceq "retry_lifecycle_verified" -and
        $retrySummary.remote_access -eq $false) "retry_lifecycle_selftest_summary"
    Assert-True ($retrySummary.operation_counts.'SCHEMA-DDL'.completed -is [int] -and
        $retrySummary.operation_counts.'TABLE-DATA'.incomplete -is [int]) "retry_operation_counts_numeric_objects"
    Assert-True ($retryOutput -notmatch 'molin_retry_gate_|restore_retry_|restore\.progress|hidden') "retry_lifecycle_selftest_no_path_or_name_leak"
}
catch {
    $Failures.Add("retry_lifecycle_selftest_safe_json")
}

if ($Failures.Count -gt 0) {
    Write-Output ("FAIL checks=" + ($Failures -join ","))
    exit 1
}

Write-Output "PASS checks=131 remote_access=false"
exit 0
