param(
    [string]$ChangeId = "",
    [string]$TargetCandidateFile = "",
    [string]$ExpectedTargetCandidateSHA256 = "",
    [string]$ReadonlyResultFile = "",
    [string]$ExpectedReadonlyResultSHA256 = "",
    [string]$ReadonlyRunnerFile = "",
    [string]$ExpectedReadonlyRunnerSHA256 = "",
    [string]$ReleaseCommitSHA = "",
    [string]$ApiArtifactSHA256 = "",
    [string]$AdminImageDigest = "",
    [string]$UserImageDigest = "",
    [ValidateSet("verify-only", "apply-up-to-59")]
    [string]$MigrationAction = "verify-only",
    [string]$BackupEvidenceSHA256 = "",
    [string]$RollbackEvidenceSHA256 = "",
    [string]$OutputDirectory = "",
    [switch]$ExportPlan,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"

function Assert-LocalFileSystemPathInput {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Description
    )

    # 计划生成只允许本地绝对路径，先拒绝 UNC、Provider 路径和网络映射盘。
    if ([string]::IsNullOrWhiteSpace($Path) -or $Path -match '^(?:\\\\|//)' -or $Path.Contains("::")) {
        throw "${Description}必须是本地文件系统绝对路径"
    }
    $isWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
    if ($isWindowsPlatform) {
        if ($Path -cnotmatch '^[A-Za-z]:[\\/]') { throw "Windows ${Description}必须使用本地盘符绝对路径" }
        $drive = Get-PSDrive -Name $Path.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
        if (([string]$drive.Root).StartsWith("\\") -or ([string]$drive.DisplayRoot).StartsWith("\\")) {
            throw "${Description}不得使用网络映射盘"
        }
    }
    elseif (-not [IO.Path]::IsPathRooted($Path)) {
        throw "${Description}必须使用本地绝对路径"
    }
}

function Assert-TargetCandidate {
    param([Parameter(Mandatory = $true)][psobject]$Candidate)

    $requiredKeys = @(
        "schema_version", "change_id", "environment", "target_alias", "server_host", "ssh_port", "ssh_user",
        "expected_ed25519_fingerprint", "project_root", "environment_file", "service_kind", "api_service_identifier",
        "api_local_port", "prometheus_local_port", "alertmanager_local_port", "rollback_operator_alias", "observer_alias",
        "expected_sms_enabled", "expected_sms_test_mode", "readonly_baseline_requires_separate_approval",
        "deployment_requires_separate_approval", "canary_requires_separate_approval",
        "production_enable_requires_separate_approval", "automatic_retries", "business_posts", "real_sms_sent"
    )
    $actualKeys = @($Candidate.PSObject.Properties.Name)
    if (@($actualKeys | Where-Object { $_ -cnotin $requiredKeys }).Count -ne 0 -or
        @($requiredKeys | Where-Object { $_ -cnotin $actualKeys }).Count -ne 0) {
        throw "生产目标候选字段集合不符合冻结契约"
    }
    if ($Candidate.schema_version -ne 1 -or $Candidate.change_id -cnotmatch '^[0-9]{8}T[0-9]{6}Z$' -or
        $Candidate.environment -cne "production" -or $Candidate.target_alias -cnotmatch '^[a-z][a-z0-9-]{2,31}$' -or
        $Candidate.expected_sms_enabled -ne $false -or $Candidate.expected_sms_test_mode -ne $true -or
        $Candidate.readonly_baseline_requires_separate_approval -ne $true -or
        $Candidate.deployment_requires_separate_approval -ne $true -or
        $Candidate.canary_requires_separate_approval -ne $true -or
        $Candidate.production_enable_requires_separate_approval -ne $true -or
        $Candidate.automatic_retries -ne 0 -or $Candidate.business_posts -ne 0 -or $Candidate.real_sms_sent -ne 0) {
        throw "生产目标候选基础门禁无效"
    }
}

function Assert-CanonicalReadonlyRunner {
    param(
        [Parameter(Mandatory = $true)][string]$TargetCandidateFile,
        [Parameter(Mandatory = $true)][string]$ExpectedTargetCandidateSHA256,
        [Parameter(Mandatory = $true)][string]$ReadonlyChangeId,
        [Parameter(Mandatory = $true)][string]$ExpectedReadonlyRunnerSHA256,
        [Parameter(Mandatory = $true)][string]$VerificationParent
    )

    # 必须由同一权威生成器重新生成 runner 并比较完整文件摘要，避免注释、死代码或额外网络调用伪造结构标记。
    $generatorPath = Join-Path $PSScriptRoot "prepare-sms-phase5-production-readonly-baseline.ps1"
    $generatorItem = Get-Item -LiteralPath $generatorPath -Force -ErrorAction Stop
    if ($generatorItem.PSIsContainer -or
        ($generatorItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "生产只读 runner 权威生成器必须是普通文件"
    }
    $generatorSHA256 = (Get-FileHash -LiteralPath $generatorPath -Algorithm SHA256).Hash.ToLowerInvariant()
    $verificationDirectory = Join-Path $VerificationParent (".sms-phase5-runner-verify-" + [Guid]::NewGuid().ToString("N"))
    $verificationFullPath = [IO.Path]::GetFullPath($verificationDirectory)
    $verificationRoot = [IO.Path]::GetFullPath($VerificationParent).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    if (-not $verificationFullPath.StartsWith($verificationRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "生产只读 runner 临时复核目录越界"
    }
    try {
        $null = & $generatorPath -ExportCandidate -ChangeId $ReadonlyChangeId `
            -TargetCandidateFile $TargetCandidateFile `
            -ExpectedTargetCandidateSHA256 $ExpectedTargetCandidateSHA256 `
            -OutputDirectory $verificationFullPath
        $generatedRunners = @(Get-ChildItem -LiteralPath $verificationFullPath -File -Force)
        if ($generatedRunners.Count -ne 1) { throw "权威生成器未生成唯一生产只读 runner" }
        $canonicalSHA256 = (Get-FileHash -LiteralPath $generatedRunners[0].FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($canonicalSHA256 -cne $ExpectedReadonlyRunnerSHA256) {
            throw "生产只读 runner 与权威生成器逐文件摘要不一致"
        }
        return $generatorSHA256
    }
    finally {
        # 临时目录由本函数以随机名称在已核验父目录内创建，只删除该目录中的权威生成器单一产物。
        if (Test-Path -LiteralPath $verificationFullPath -PathType Container) {
            Get-ChildItem -LiteralPath $verificationFullPath -File -Force | Remove-Item -Force
            if (@(Get-ChildItem -LiteralPath $verificationFullPath -Force).Count -eq 0) {
                Remove-Item -LiteralPath $verificationFullPath -Force
            }
        }
    }
}

function Assert-ReadonlyResult {
    param(
        [Parameter(Mandatory = $true)][psobject]$Evidence,
        [Parameter(Mandatory = $true)][string]$ExpectedTargetChangeId,
        [Parameter(Mandatory = $true)][string]$ExpectedTargetSHA256,
        [Parameter(Mandatory = $true)][string]$ExpectedRunnerSHA256,
        [Parameter(Mandatory = $true)][string]$MigrationAction
    )

    $topLevelKeys = @(
        "schema_version", "change_id", "target_change_id", "target_candidate_sha256", "runner_sha256", "observed",
        "network_connections", "remote_stderr_present", "readonly_exit_code", "uploads", "configuration_mutations",
        "service_operations", "business_posts", "emails_sent", "real_sms_sent", "sensitive_values_persisted"
    )
    $actualTopLevelKeys = @($Evidence.PSObject.Properties.Name)
    if (@($actualTopLevelKeys | Where-Object { $_ -cnotin $topLevelKeys }).Count -ne 0 -or
        @($topLevelKeys | Where-Object { $_ -cnotin $actualTopLevelKeys }).Count -ne 0) {
        throw "生产只读结果顶层字段集合无效"
    }
    if ($Evidence.schema_version -ne 1 -or $Evidence.change_id -cnotmatch '^[0-9]{8}T[0-9]{6}Z$' -or
        $Evidence.target_change_id -cne $ExpectedTargetChangeId -or
        $Evidence.target_candidate_sha256 -cne $ExpectedTargetSHA256 -or
        $Evidence.runner_sha256 -cne $ExpectedRunnerSHA256) {
        throw "生产只读结果与目标候选绑定不匹配"
    }
    if ($Evidence.network_connections -ne 1 -or $Evidence.remote_stderr_present -ne $false -or
        $Evidence.readonly_exit_code -notin @(0, 3) -or $Evidence.uploads -ne 0 -or
        $Evidence.configuration_mutations -ne 0 -or $Evidence.service_operations -ne 0 -or
        $Evidence.business_posts -ne 0 -or $Evidence.emails_sent -ne 0 -or $Evidence.real_sms_sent -ne 0 -or
        $Evidence.sensitive_values_persisted -ne 0) {
        throw "生产只读结果包含未批准副作用或异常连接"
    }
    $requiredObservedKeys = @(
        "production_readonly_baseline", "app_env_production", "sms_enabled_false", "sms_test_mode_true",
        "provider_aliyun", "endpoint_official", "required_sms_config_present", "legacy_sms_keys_absent",
        "template_env_overrides_absent", "duplicate_sms_config_absent", "environment_file_secure", "service_running",
        "process_environment_readable", "file_process_sms_config_match", "health_ready", "schema_ready",
        "schema_version", "schema_dirty", "template_bindings_ready", "template_total", "template_approved",
        "template_enabled", "binding_total", "binding_enabled", "binding_distinct_templates", "send_log_readable",
        "send_total", "send_accepted", "send_failed", "metrics_ready", "sms_metric_shape_ready", "prometheus_ready",
        "sms_alert_rules_loaded", "prometheus_target_up", "active_sms_alerts", "notification_failures_total",
        "alertmanager_ready", "rollback_operator_declared", "observer_declared", "backup_capability_verified",
        "configuration_mutations", "service_operations", "business_posts", "uploads", "emails_sent", "real_sms_sent"
    )
    $observedKeys = @($Evidence.observed.PSObject.Properties.Name)
    if (@($observedKeys | Where-Object { $_ -cnotin $requiredObservedKeys }).Count -ne 0 -or
        @($requiredObservedKeys | Where-Object { $_ -cnotin $observedKeys }).Count -ne 0) {
        throw "生产只读结果观察字段集合无效"
    }
    $requiredTrueKeys = @(
        "app_env_production", "sms_enabled_false", "sms_test_mode_true", "provider_aliyun", "endpoint_official",
        "required_sms_config_present", "legacy_sms_keys_absent", "template_env_overrides_absent",
        "duplicate_sms_config_absent", "environment_file_secure", "service_running", "process_environment_readable",
        "file_process_sms_config_match", "health_ready", "template_bindings_ready", "send_log_readable", "metrics_ready",
        "sms_metric_shape_ready", "prometheus_ready", "sms_alert_rules_loaded", "prometheus_target_up",
        "alertmanager_ready", "rollback_operator_declared", "observer_declared"
    )
    foreach ($key in $requiredTrueKeys) {
        if ([string]$Evidence.observed.$key -cne "true") { throw "生产只读结果缺少部署计划基础门禁：$key" }
    }
    if ([string]$Evidence.observed.schema_dirty -cne "0" -or
        [string]$Evidence.observed.schema_version -cnotmatch '^[0-9]+$') {
        throw "生产 schema 基线不可用于部署计划"
    }
    $schemaVersion = [int]$Evidence.observed.schema_version
    $verifyOnlyReady = $MigrationAction -ceq "verify-only" -and $schemaVersion -ge 59 -and
        [string]$Evidence.observed.schema_ready -ceq "true" -and $Evidence.readonly_exit_code -eq 0 -and
        [string]$Evidence.observed.production_readonly_baseline -ceq "passed"
    $migrationOnlyBlocked = $MigrationAction -ceq "apply-up-to-59" -and $schemaVersion -eq 58 -and
        [string]$Evidence.observed.schema_ready -ceq "false" -and $Evidence.readonly_exit_code -eq 3 -and
        [string]$Evidence.observed.production_readonly_baseline -ceq "blocked"
    if (-not $verifyOnlyReady -and -not $migrationOnlyBlocked) {
        throw "生产只读基线状态与 migration 决策不一致"
    }
    if ([string]$Evidence.observed.template_total -cne "5" -or
        [string]$Evidence.observed.template_approved -cne "5" -or
        [string]$Evidence.observed.template_enabled -cne "5" -or
        [string]$Evidence.observed.binding_total -cne "5" -or
        [string]$Evidence.observed.binding_enabled -cne "5" -or
        [string]$Evidence.observed.binding_distinct_templates -cne "5" -or
        [string]$Evidence.observed.active_sms_alerts -cne "0" -or
        [string]$Evidence.observed.notification_failures_total -cne "0" -or
        [string]$Evidence.observed.backup_capability_verified -cne "false") {
        throw "生产只读基线计数或人工备份门禁不符合部署计划要求"
    }
    foreach ($key in @("send_total", "send_accepted", "send_failed")) {
        if ([string]$Evidence.observed.$key -cnotmatch '^[0-9]+$') {
            throw "生产发送聚合不是非负整数：$key"
        }
    }
    if ([int64]$Evidence.observed.send_total -ne
        ([int64]$Evidence.observed.send_accepted + [int64]$Evidence.observed.send_failed)) {
        throw "生产发送聚合不守恒"
    }
    foreach ($key in @("configuration_mutations", "service_operations", "business_posts", "uploads", "emails_sent", "real_sms_sent")) {
        if ([string]$Evidence.observed.$key -cne "0") { throw "生产只读观察结果存在副作用：$key" }
    }
}

function Assert-MigrationDecision {
    param(
        [Parameter(Mandatory = $true)][int]$CurrentSchemaVersion,
        [Parameter(Mandatory = $true)][string]$Action
    )

    if ($Action -ceq "verify-only" -and $CurrentSchemaVersion -lt 59) {
        throw "schema 低于 59 时不能选择 verify-only"
    }
    if ($Action -ceq "apply-up-to-59" -and $CurrentSchemaVersion -ne 58) {
        throw "阶段 5 migration 计划仅接受 schema 58 升级至 59"
    }
}

# 默认入口不读取任何证据文件、不生成计划，也不连接生产。
if (-not $ExportPlan -and -not $SelfTest) {
    Write-Output "production_closed_deployment_plan_authorized=false"
    Write-Output "plan_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExportPlan -and $SelfTest) { throw "ExportPlan 与 SelfTest 必须互斥" }

if ($SelfTest) {
    Assert-MigrationDecision -CurrentSchemaVersion 59 -Action "verify-only"
    Assert-MigrationDecision -CurrentSchemaVersion 58 -Action "apply-up-to-59"
    $invalidVerifyRejected = $false
    try { Assert-MigrationDecision -CurrentSchemaVersion 57 -Action "verify-only" } catch { $invalidVerifyRejected = $true }
    $unsupportedMigrationRejected = $false
    try { Assert-MigrationDecision -CurrentSchemaVersion 57 -Action "apply-up-to-59" } catch { $unsupportedMigrationRejected = $true }
    if (-not $invalidVerifyRejected -or -not $unsupportedMigrationRejected) {
        throw "生产 migration 决策反例未被阻断"
    }
    Write-Output "production_closed_deployment_plan_self_test=passed"
    Write-Output "verify_only_requires_schema_59=true"
    Write-Output "migration_requires_schema_58=true"
    Write-Output "unsupported_migration_start_rejected=true"
    Write-Output "deployment_authorized=false"
    Write-Output "plan_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ChangeId -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') { throw "生产关闭态部署 ChangeId 必须使用 UTC 基本格式" }
foreach ($sha in @(
    $ExpectedTargetCandidateSHA256, $ExpectedReadonlyResultSHA256, $ExpectedReadonlyRunnerSHA256, $ApiArtifactSHA256,
    $BackupEvidenceSHA256, $RollbackEvidenceSHA256
)) {
    if ($sha -cnotmatch '^[0-9a-f]{64}$') { throw "部署计划摘要必须是小写 SHA-256" }
}
if ($ReleaseCommitSHA -cnotmatch '^[0-9a-f]{40}$') { throw "发布提交必须是完整小写 Git SHA" }
foreach ($digest in @($AdminImageDigest, $UserImageDigest)) {
    if ($digest -cnotmatch '^sha256:[0-9a-f]{64}$') { throw "前端镜像必须使用完整 sha256 digest" }
}
foreach ($pathSpec in @(
    @{ Path = $TargetCandidateFile; Description = "生产目标候选文件" },
    @{ Path = $ReadonlyResultFile; Description = "生产只读结果文件" },
    @{ Path = $ReadonlyRunnerFile; Description = "生产只读 runner 文件" },
    @{ Path = $OutputDirectory; Description = "部署计划输出目录" }
)) {
    Assert-LocalFileSystemPathInput -Path $pathSpec.Path -Description $pathSpec.Description
}
$resolvedTarget = (Resolve-Path -LiteralPath $TargetCandidateFile -ErrorAction Stop).Path
$resolvedReadonly = (Resolve-Path -LiteralPath $ReadonlyResultFile -ErrorAction Stop).Path
$resolvedReadonlyRunner = (Resolve-Path -LiteralPath $ReadonlyRunnerFile -ErrorAction Stop).Path
foreach ($inputPath in @($resolvedTarget, $resolvedReadonly, $resolvedReadonlyRunner)) {
    $inputItem = Get-Item -LiteralPath $inputPath -Force -ErrorAction Stop
    if (-not $inputItem.PSIsContainer -and
        ($inputItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -eq 0) {
        continue
    }
    throw "部署计划输入必须是普通文件且不能是符号链接或重解析点"
}
if ((Get-FileHash -LiteralPath $resolvedTarget -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedTargetCandidateSHA256) {
    throw "生产目标候选摘要不匹配"
}
if ((Get-FileHash -LiteralPath $resolvedReadonly -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedReadonlyResultSHA256) {
    throw "生产只读结果摘要不匹配"
}
if ((Get-FileHash -LiteralPath $resolvedReadonlyRunner -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedReadonlyRunnerSHA256) {
    throw "生产只读 runner 摘要不匹配"
}
$target = Get-Content -LiteralPath $resolvedTarget -Raw -Encoding UTF8 | ConvertFrom-Json
$readonly = Get-Content -LiteralPath $resolvedReadonly -Raw -Encoding UTF8 | ConvertFrom-Json
Assert-TargetCandidate -Candidate $target
$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
$outputParent = Split-Path -Parent $outputPath
if ([string]::IsNullOrWhiteSpace($outputParent) -or -not (Test-Path -LiteralPath $outputParent -PathType Container)) {
    throw "部署计划输出目录的父目录必须已存在"
}
$outputParentItem = Get-Item -LiteralPath $outputParent -Force -ErrorAction Stop
if (($outputParentItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    throw "部署计划输出目录的父目录不得是符号链接或重解析点"
}
if (Test-Path -LiteralPath $outputPath) { throw "部署计划输出目录已存在，禁止覆盖" }
$readonlyRunnerGeneratorSHA256 = Assert-CanonicalReadonlyRunner `
    -TargetCandidateFile $resolvedTarget `
    -ExpectedTargetCandidateSHA256 $ExpectedTargetCandidateSHA256 `
    -ReadonlyChangeId $readonly.change_id `
    -ExpectedReadonlyRunnerSHA256 $ExpectedReadonlyRunnerSHA256 `
    -VerificationParent $outputParent
Assert-ReadonlyResult -Evidence $readonly -ExpectedTargetChangeId $target.change_id `
    -ExpectedTargetSHA256 $ExpectedTargetCandidateSHA256 -ExpectedRunnerSHA256 $ExpectedReadonlyRunnerSHA256 `
    -MigrationAction $MigrationAction
$currentSchemaVersion = [int]$readonly.observed.schema_version
Assert-MigrationDecision -CurrentSchemaVersion $currentSchemaVersion -Action $MigrationAction

$planPath = Join-Path $outputPath "sms-phase5-production-closed-deployment-$ChangeId.json"
$directoryCreated = $false
$fileCreated = $false
$planStream = $null
$planBytes = $null
try {
    # 计划只冻结摘要、计数和授权边界，不保存服务器地址、凭据、环境值、手机号或 Token。
    $plan = [ordered]@{
        schema_version = 1
        change_id = $ChangeId
        environment = "production"
        acceptance_scope = "closed_state_deployment"
        target_alias = $target.target_alias
        target_change_id = $target.change_id
        target_candidate_sha256 = $ExpectedTargetCandidateSHA256
        readonly_change_id = $readonly.change_id
        readonly_result_sha256 = $ExpectedReadonlyResultSHA256
        readonly_runner_sha256 = $ExpectedReadonlyRunnerSHA256
        readonly_runner_generator_sha256 = $readonlyRunnerGeneratorSHA256
        release_commit_sha = $ReleaseCommitSHA
        api_artifact_sha256 = $ApiArtifactSHA256
        admin_image_digest = $AdminImageDigest
        user_image_digest = $UserImageDigest
        current_schema_version = $currentSchemaVersion
        current_schema_dirty = 0
        migration_action = $MigrationAction
        target_schema_minimum = 59
        backup_evidence_sha256 = $BackupEvidenceSHA256
        rollback_evidence_sha256 = $RollbackEvidenceSHA256
        backup_recovery_verified = $false
        rollback_operator_alias = $target.rollback_operator_alias
        observer_alias = $target.observer_alias
        expected_sms_enabled = $false
        expected_sms_test_mode = $true
        whitelist_mode = "empty_or_separately_approved"
        template_source = "database"
        expected_template_count = 5
        expected_binding_count = 5
        expected_sms_alert_rule_count = 4
        automatic_rollback_required = $true
        max_service_stops = 2
        max_service_starts = 2
        deployment_requires_separate_approval = $true
        migration_requires_separate_approval = ($MigrationAction -ceq "apply-up-to-59")
        canary_requires_separate_approval = $true
        production_enable_requires_separate_approval = $true
        automatic_retries = 0
        uploads = 0
        configuration_mutations = 0
        service_operations = 0
        business_posts = 0
        emails_sent = 0
        real_sms_sent = 0
    }
    $null = New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop
    $directoryCreated = $true
    $planBytes = (New-Object Text.UTF8Encoding($false)).GetBytes(($plan | ConvertTo-Json -Depth 4) + "`n")
    # 即使输出目录被并发替换，最终计划文件仍以 CreateNew 排他创建，绝不覆盖既有证据。
    $planStream = New-Object IO.FileStream($planPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    $fileCreated = $true
    $planStream.Write($planBytes, 0, $planBytes.Length)
    $planStream.Flush($true)
    $planStream.Dispose()
    $planStream = $null
    [Array]::Clear($planBytes, 0, $planBytes.Length)
    $planBytes = $null
    $verified = Get-Content -LiteralPath $planPath -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($verified.environment -cne "production" -or $verified.acceptance_scope -cne "closed_state_deployment" -or
        $verified.expected_sms_enabled -ne $false -or $verified.expected_sms_test_mode -ne $true -or
        $verified.deployment_requires_separate_approval -ne $true -or $verified.real_sms_sent -ne 0) {
        throw "生产关闭态部署计划静态复核失败"
    }
    $planSHA256 = (Get-FileHash -LiteralPath $planPath -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Output "production_closed_deployment_plan=passed"
    Write-Output "change_id=$ChangeId"
    Write-Output "target_candidate_sha256=$ExpectedTargetCandidateSHA256"
    Write-Output "readonly_result_sha256=$ExpectedReadonlyResultSHA256"
    Write-Output "plan_sha256=$planSHA256"
    Write-Output "plan_path=$planPath"
    Write-Output "deployment_authorized=false"
    Write-Output "migration_authorized=false"
    Write-Output "canary_authorized=false"
    Write-Output "production_enable_authorized=false"
    Write-Output "plan_files_written=1"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
}
catch {
    if ($null -ne $planStream) { $planStream.Dispose(); $planStream = $null }
    if ($null -ne $planBytes) { [Array]::Clear($planBytes, 0, $planBytes.Length); $planBytes = $null }
    if ($fileCreated -and (Test-Path -LiteralPath $planPath -PathType Leaf)) {
        Remove-Item -LiteralPath $planPath -Force
    }
    if ($directoryCreated -and (Test-Path -LiteralPath $outputPath -PathType Container) -and
        @(Get-ChildItem -LiteralPath $outputPath -Force).Count -eq 0) {
        Remove-Item -LiteralPath $outputPath -Force
    }
    throw
}
finally {
    if ($null -ne $planStream) { $planStream.Dispose() }
    if ($null -ne $planBytes) { [Array]::Clear($planBytes, 0, $planBytes.Length) }
}
