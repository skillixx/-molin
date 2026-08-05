param(
    [string]$ChangeId = "20260805T015043Z",
    [string]$NotificationDrillConfirmation = "",
    [string]$NotificationDrillChangeId = "",
    [string]$NotificationDrillEvidencePath = "",
    [string]$NotificationDrillEvidenceSHA256 = "",
    [switch]$ValidateNotificationEvidenceOnly,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
$notificationDrillPhrase = "我已确认阶段5测试服告警通知演练成功"

if ($ChangeId -notmatch '^[0-9]{8}T[0-9]{6}Z$') {
    throw "必须提供 UTC ChangeId，例如 20260805T015043Z"
}

$notificationDrillAttested = $false
$validatedNotificationEvidenceSHA256 = ""

function ConvertFrom-SafeKeyValueOutput {
    param([string[]]$Lines)

    $values = @{}
    foreach ($rawLine in $Lines) {
        $line = ([string]$rawLine).Trim()
        if ($line.Length -eq 0) {
            continue
        }
        # 聚合器只接受现有只读工具的低敏键值协议；任何额外文本都失败关闭且不会转发到终端。
        if ($line -notmatch '^([a-z0-9_]+)=([A-Za-z0-9_.:/-]+)$') {
            throw "只读依赖输出不符合安全键值协议"
        }
        $key = $Matches[1]
        if ($values.ContainsKey($key)) {
            throw "只读依赖输出包含重复键"
        }
        $values[$key] = $Matches[2]
    }
    return $values
}

function Test-SafeValue {
    param(
        [hashtable]$Values,
        [string]$Key,
        [string]$Expected
    )
    return $Values.ContainsKey($Key) -and [string]$Values[$Key] -ceq $Expected
}

function Read-VerifiedLocalEvidenceFile {
    param(
        [string]$Path,
        [string]$ExpectedSHA256,
        [long]$MaximumBytes,
        [string[]]$AllowedExtensions
    )

    if ($Path -match '^[A-Za-z]:[^\\/]' -or $Path.StartsWith('\\?\') -or $Path.StartsWith('\\.\') -or
        -not [IO.Path]::IsPathRooted($Path) -or [Uri]::new([IO.Path]::GetFullPath($Path)).IsUnc) {
        throw "演练证据必须位于本机绝对路径"
    }
    $fullPath = [IO.Path]::GetFullPath($Path)
    $driveRoot = [IO.Path]::GetPathRoot($fullPath)
    if ($driveRoot -and [IO.DriveInfo]::new($driveRoot).DriveType -eq [IO.DriveType]::Network) {
        throw "演练证据不得位于映射网络驱动器"
    }
    if ($AllowedExtensions -notcontains [IO.Path]::GetExtension($fullPath).ToLowerInvariant()) {
        throw "演练证据文件扩展名不符合契约"
    }
    $repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path.TrimEnd([IO.Path]::DirectorySeparatorChar)
    $repositoryPrefix = $repositoryRoot + [IO.Path]::DirectorySeparatorChar
    if ($fullPath.StartsWith($repositoryPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "演练证据不得放入 Git 工作区"
    }
    if (-not (Test-Path -LiteralPath $fullPath -PathType Leaf)) {
        throw "演练证据文件不存在"
    }

    $file = Get-Item -LiteralPath $fullPath -Force
    if (($file.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "演练证据不得为重解析点"
    }
    $parent = $file.Directory
    while ($null -ne $parent) {
        if (($parent.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "演练证据路径不得包含重解析祖先"
        }
        $parent = $parent.Parent
    }
    if ($file.Length -lt 1 -or $file.Length -gt $MaximumBytes) {
        throw "演练证据文件大小超出允许范围"
    }

    # 读取期间禁止其他进程写入或删除，摘要和后续校验始终使用同一份已读取字节。
    $stream = [IO.File]::Open($fullPath, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        $bytes = New-Object byte[] $stream.Length
        $offset = 0
        while ($offset -lt $bytes.Length) {
            $read = $stream.Read($bytes, $offset, $bytes.Length - $offset)
            if ($read -le 0) {
                throw "演练证据读取不完整"
            }
            $offset += $read
        }
    }
    finally {
        $stream.Dispose()
    }
    $after = Get-Item -LiteralPath $fullPath -Force
    if (($after.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $after.Length -ne $bytes.Length) {
        throw "演练证据在读取期间发生变化"
    }

    $sha256 = [Security.Cryptography.SHA256]::Create()
    try {
        $actualSHA256 = ([BitConverter]::ToString($sha256.ComputeHash($bytes))).Replace("-", "").ToLowerInvariant()
    }
    finally {
        $sha256.Dispose()
    }
    if ($actualSHA256 -cne $ExpectedSHA256.ToLowerInvariant()) {
        throw "演练证据 SHA-256 不匹配"
    }
    return [ordered]@{
        bytes = $bytes
        sha256 = $actualSHA256
        full_path = $fullPath
    }
}

function Read-NotificationDrillEvidence {
    param(
        [string]$Path,
        [string]$ExpectedSHA256,
        [string]$ExpectedChangeId
    )

    $manifestFile = Read-VerifiedLocalEvidenceFile `
        -Path $Path -ExpectedSHA256 $ExpectedSHA256 -MaximumBytes 65536 -AllowedExtensions @(".json")
    $bytes = [byte[]]$manifestFile.bytes
    $actualSHA256 = [string]$manifestFile.sha256
    if ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) {
        throw "告警通知演练证据必须为无 BOM UTF-8"
    }
    try {
        $text = [Text.UTF8Encoding]::new($false, $true).GetString($bytes)
        # 禁止属性名转义，避免同一语义键以字面和 Unicode 形式重复后被 JSON 解析器静默覆盖。
        if ([regex]::IsMatch($text, '"(?:[^"\\]|\\.)*\\(?:u[0-9A-Fa-f]{4}|["\\/bfnrt])(?:[^"\\]|\\.)*"\s*:')) {
            throw "告警通知演练证据属性名不得使用转义"
        }
        $convertFromJsonCommand = Get-Command ConvertFrom-Json -ErrorAction Stop
        if ($convertFromJsonCommand.Parameters.ContainsKey("DateKind")) {
            # PowerShell 7.5+ 必须保留 JSON 日期字符串，防止自动转换掩盖原始格式差异。
            $evidence = ConvertFrom-Json -InputObject $text -DateKind String -ErrorAction Stop
        }
        else {
            $evidence = ConvertFrom-Json -InputObject $text -ErrorAction Stop
        }
    }
    catch {
        throw "告警通知演练证据不是严格 UTF-8 JSON"
    }

    $expectedProperties = @(
        "schema", "environment", "change_id", "created_at_utc", "result",
        "sms_enabled", "synthetic_alert_firing_count", "synthetic_alert_resolved_count",
        "alertmanager_received", "route_matched", "notification_attempted", "receiver_delivered",
        "on_call_acknowledged", "notification_queue_empty", "provider_call_delta", "real_sms_sent",
        "contains_sensitive_values", "alertmanager_evidence_path", "alertmanager_evidence_sha256",
        "route_evidence_path", "route_evidence_sha256", "notification_attempt_evidence_path",
        "notification_attempt_evidence_sha256", "receiver_delivery_evidence_path",
        "receiver_delivery_evidence_sha256", "on_call_ack_evidence_path", "on_call_ack_evidence_sha256"
    )
    $actualProperties = @($evidence.PSObject.Properties.Name)
    if (@(Compare-Object ($expectedProperties | Sort-Object) ($actualProperties | Sort-Object)).Count -ne 0) {
        throw "告警通知演练证据字段集合不符合契约"
    }
    foreach ($property in $expectedProperties) {
        if ([regex]::Matches($text, '"' + [regex]::Escape($property) + '"\s*:').Count -ne 1) {
            throw "告警通知演练证据包含缺失键或重复键"
        }
    }

    if ($evidence.schema -cne "molin.sms.phase5.notification-drill.v1" -or
        $evidence.environment -cne "test" -or $evidence.change_id -cne $ExpectedChangeId -or
        $evidence.result -cne "passed") {
        throw "告警通知演练证据身份或结果不符合契约"
    }
    foreach ($property in @(
        "alertmanager_received", "route_matched", "notification_attempted", "receiver_delivered",
        "on_call_acknowledged", "notification_queue_empty"
    )) {
        if ($evidence.$property -isnot [bool] -or -not $evidence.$property) {
            throw "告警通知演练五层证据未全部通过"
        }
    }
    foreach ($property in @(
        "synthetic_alert_firing_count", "synthetic_alert_resolved_count", "provider_call_delta", "real_sms_sent"
    )) {
        if ($evidence.$property -isnot [int] -and $evidence.$property -isnot [long]) {
            throw "告警通知演练计数字段必须为整数"
        }
    }
    if ($evidence.sms_enabled -isnot [bool] -or $evidence.sms_enabled -or
        $evidence.contains_sensitive_values -isnot [bool] -or $evidence.contains_sensitive_values -or
        [long]$evidence.synthetic_alert_firing_count -ne 1 -or
        [long]$evidence.synthetic_alert_resolved_count -ne 1 -or
        [long]$evidence.provider_call_delta -ne 0 -or [long]$evidence.real_sms_sent -ne 0) {
        throw "告警通知演练证据违反关闭态或单次演练约束"
    }

    $layerDigests = @()
    $layerPaths = @()
    $layerPairs = @(
        @("alertmanager_evidence_path", "alertmanager_evidence_sha256"),
        @("route_evidence_path", "route_evidence_sha256"),
        @("notification_attempt_evidence_path", "notification_attempt_evidence_sha256"),
        @("receiver_delivery_evidence_path", "receiver_delivery_evidence_sha256"),
        @("on_call_ack_evidence_path", "on_call_ack_evidence_sha256")
    )
    foreach ($pair in $layerPairs) {
        $pathValue = [string]$evidence.($pair[0])
        $digest = [string]$evidence.($pair[1])
        if ($digest -notmatch '^[a-fA-F0-9]{64}$' -or $digest -eq ("0" * 64)) {
            throw "告警通知演练分层证据摘要格式无效"
        }
        $verifiedLayer = Read-VerifiedLocalEvidenceFile `
            -Path $pathValue -ExpectedSHA256 $digest -MaximumBytes 10485760 `
            -AllowedExtensions @(".json", ".txt", ".log", ".png", ".pdf")
        $layerDigests += [string]$verifiedLayer.sha256
        $layerPaths += [string]$verifiedLayer.full_path
    }
    if (@($layerDigests | Select-Object -Unique).Count -ne 5 -or
        @($layerPaths | Select-Object -Unique).Count -ne 5) {
        throw "告警通知演练五层证据文件和摘要必须相互独立"
    }

    # PowerShell 版本可能把 ISO JSON 字符串自动转换为 DateTime；从原始 JSON 提取可避免平台差异放宽格式。
    $createdAtMatches = [regex]::Matches(
        $text,
        '"created_at_utc"\s*:\s*"(?<value>[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z)"'
    )
    if ($createdAtMatches.Count -ne 1) {
        throw "告警通知演练证据时间格式无效"
    }
    $createdAtText = $createdAtMatches[0].Groups["value"].Value
    if ([string]$evidence.created_at_utc -cne $createdAtText) {
        throw "告警通知演练证据顶层时间字段与原始 JSON 不一致"
    }
    $createdAt = [DateTimeOffset]::MinValue
    $parsed = [DateTimeOffset]::TryParseExact(
        $createdAtText,
        "yyyy-MM-dd'T'HH:mm:ss'Z'",
        [Globalization.CultureInfo]::InvariantCulture,
        [Globalization.DateTimeStyles]::AssumeUniversal,
        [ref]$createdAt
    )
    if (-not $parsed) {
        throw "告警通知演练证据时间格式无效"
    }
    $age = [DateTimeOffset]::UtcNow - $createdAt.ToUniversalTime()
    if ($age.TotalMinutes -lt -5 -or $age.TotalHours -gt 24) {
        throw "告警通知演练证据不在 24 小时有效窗口内"
    }

    return [ordered]@{
        sha256 = $actualSHA256
        change_id = $ExpectedChangeId
    }
}

function Get-CanaryPreflightResult {
    param(
        [hashtable]$ClosedState,
        [hashtable]$Candidate,
        [hashtable]$Recovery,
        [hashtable]$LogRetention,
        [bool]$NotificationDrillAttested
    )

    # Canary 前必须仍处于关闭态，并且数据库和 Provider 观察均保持零增量。
    $closedStateReady =
        (Test-SafeValue $ClosedState "phase5_closed_state_release_ready" "true") -and
        (Test-SafeValue $ClosedState "sms_enabled" "false") -and
        (Test-SafeValue $ClosedState "sms_test_mode" "true") -and
        (Test-SafeValue $ClosedState "sms_test_whitelist_count_matches_expected" "true") -and
        (Test-SafeValue $ClosedState "trusted_proxy_matches_expected" "true") -and
        (Test-SafeValue $ClosedState "template_summary_total_approved_enabled" "5:5:5") -and
        (Test-SafeValue $ClosedState "binding_summary_total_enabled_distinct" "5:5:5") -and
        (Test-SafeValue $ClosedState "sensitive_metric_labels" "0") -and
        (Test-SafeValue $ClosedState "observation_send_delta_zero" "true") -and
        (Test-SafeValue $ClosedState "observation_provider_delta_zero" "true")

    # 回滚候选只能使用当前环境派生的关闭态候选，不能恢复缺少固定代理信任的旧环境文件。
    $rollbackCandidateReady =
        (Test-SafeValue $Candidate "candidate_verification" "passed") -and
        (Test-SafeValue $Candidate "candidate_sms_enabled" "false") -and
        (Test-SafeValue $Candidate "candidate_sms_test_mode" "true") -and
        (Test-SafeValue $Candidate "candidate_fixed_proxy_preserved" "true") -and
        (Test-SafeValue $Candidate "candidate_legacy_template_keys" "0") -and
        (Test-SafeValue $Candidate "candidate_duplicate_keys" "0") -and
        (Test-SafeValue $Candidate "candidate_sensitive_values_printed" "0")

    $rollbackMaterialsReady =
        (Test-SafeValue $Recovery "rollback_materials_verified" "true") -and
        (Test-SafeValue $Recovery "rollback_container_specs_verified" "true") -and
        (Test-SafeValue $Recovery "rollback_container_images_present" "true")

    $monitoringReady =
        (Test-SafeValue $ClosedState "prometheus_ready_http" "200") -and
        (Test-SafeValue $ClosedState "loaded_sms_alert_rules" "4") -and
        (Test-SafeValue $ClosedState "prometheus_target_health_after" "up")

    # 真实 Canary 前必须有可达通知演练和已验证的日志留存，不能仅依赖规则已加载或磁盘当前有空余。
    # 只读运行态只能证明传输已配置，演练成功必须由独立窗口产生证据摘要并由负责人显式确认。
    $notificationDrillReady =
        (Test-SafeValue $Recovery "notification_chain_status" "transport_present_receiver_unverified") -and
        $NotificationDrillAttested
    $logRetentionReady = Test-SafeValue $LogRetention "log_retention_policy_verified" "true"
    $ready = $closedStateReady -and $rollbackCandidateReady -and $rollbackMaterialsReady -and
        $monitoringReady -and $notificationDrillReady -and $logRetentionReady

    return [ordered]@{
        closed_state_ready = $closedStateReady
        rollback_candidate_ready = $rollbackCandidateReady
        rollback_materials_ready = $rollbackMaterialsReady
        monitoring_ready = $monitoringReady
        notification_drill_ready = $notificationDrillReady
        log_retention_policy_verified = $logRetentionReady
        canary_preflight_ready = $ready
    }
}

function Invoke-ReadOnlyDependency {
    param(
        [string]$ScriptName,
        [string[]]$Arguments = @()
    )

    $scriptPath = Join-Path $PSScriptRoot $ScriptName
    if (-not (Test-Path -LiteralPath $scriptPath -PathType Leaf)) {
        throw "缺少阶段 5 只读依赖"
    }
    # 新进程隔离依赖脚本中的 exit 语义；仅捕获输出，不向终端转发潜在异常文本。
    $engine = (Get-Process -Id $PID).Path
    $commandArguments = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $scriptPath) + $Arguments
    $output = @(& $engine @commandArguments 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw "阶段 5 只读依赖执行失败"
    }
    return ConvertFrom-SafeKeyValueOutput -Lines ($output | ForEach-Object { [string]$_ })
}

function Assert-SelfTestCase {
    param(
        [string]$Name,
        [hashtable]$ClosedState,
        [hashtable]$Candidate,
        [hashtable]$Recovery,
        [hashtable]$LogRetention,
        [bool]$NotificationDrillAttested,
        [bool]$Expected
    )
    $result = Get-CanaryPreflightResult $ClosedState $Candidate $Recovery $LogRetention $NotificationDrillAttested
    if ([bool]$result.canary_preflight_ready -ne $Expected) {
        throw "SelfTest 用例失败：$Name"
    }
    Write-Output "$Name=passed"
}

function Invoke-SelfTest {
    $closed = ConvertFrom-SafeKeyValueOutput @(
        "phase5_closed_state_release_ready=true",
        "sms_enabled=false",
        "sms_test_mode=true",
        "sms_test_whitelist_count_matches_expected=true",
        "trusted_proxy_matches_expected=true",
        "template_summary_total_approved_enabled=5:5:5",
        "binding_summary_total_enabled_distinct=5:5:5",
        "sensitive_metric_labels=0",
        "observation_send_delta_zero=true",
        "observation_provider_delta_zero=true",
        "prometheus_ready_http=200",
        "loaded_sms_alert_rules=4",
        "prometheus_target_health_after=up"
    )
    $candidate = ConvertFrom-SafeKeyValueOutput @(
        "candidate_verification=passed",
        "candidate_sms_enabled=false",
        "candidate_sms_test_mode=true",
        "candidate_fixed_proxy_preserved=true",
        "candidate_legacy_template_keys=0",
        "candidate_duplicate_keys=0",
        "candidate_sensitive_values_printed=0"
    )
    $recovery = ConvertFrom-SafeKeyValueOutput @(
        "rollback_materials_verified=true",
        "rollback_container_specs_verified=true",
        "rollback_container_images_present=true",
        "notification_chain_status=transport_present_receiver_unverified"
    )
    $retention = ConvertFrom-SafeKeyValueOutput @("log_retention_policy_verified=true")

    Assert-SelfTestCase "ready_case" $closed $candidate $recovery $retention $true $true

    $closedBlocked = $closed.Clone()
    $closedBlocked["sms_enabled"] = "true"
    Assert-SelfTestCase "closed_state_blocker_case" $closedBlocked $candidate $recovery $retention $true $false

    $candidateBlocked = $candidate.Clone()
    $candidateBlocked["candidate_fixed_proxy_preserved"] = "false"
    Assert-SelfTestCase "rollback_candidate_blocker_case" $closed $candidateBlocked $recovery $retention $true $false

    Assert-SelfTestCase "notification_blocker_case" $closed $candidate $recovery $retention $false $false

    $retentionBlocked = $retention.Clone()
    $retentionBlocked["log_retention_policy_verified"] = "false"
    Assert-SelfTestCase "log_retention_blocker_case" $closed $candidate $recovery $retentionBlocked $true $false

    $malformedRejected = $false
    try {
        $null = ConvertFrom-SafeKeyValueOutput @("sms_enabled=false", "unexpected output")
    }
    catch {
        $malformedRejected = $true
    }
    if (-not $malformedRejected) {
        throw "SelfTest 未拒绝非键值输出"
    }
    Write-Output "malformed_output_blocker_case=passed"
    Write-Output "self_test=passed"
    Write-Output "remote_connections=0"
    Write-Output "business_configuration_mutations=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
}

if ($SelfTest -and $ValidateNotificationEvidenceOnly) {
    throw "SelfTest 与 ValidateNotificationEvidenceOnly 不能同时使用"
}
$notificationInputs = @(
    $NotificationDrillConfirmation,
    $NotificationDrillChangeId,
    $NotificationDrillEvidencePath,
    $NotificationDrillEvidenceSHA256
)
$providedNotificationInputs = @($notificationInputs | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }).Count
if ($providedNotificationInputs -gt 0) {
    if ($providedNotificationInputs -ne 4) {
        throw "告警通知演练确认、ChangeId、证据路径和 SHA-256 必须同时提供"
    }
    if ($NotificationDrillConfirmation -cne $notificationDrillPhrase) {
        throw "告警通知演练确认短语不匹配"
    }
    if ($NotificationDrillChangeId -notmatch '^[0-9]{8}T[0-9]{6}Z$') {
        throw "告警通知演练 ChangeId 格式无效"
    }
    if ($NotificationDrillEvidenceSHA256 -notmatch '^[a-fA-F0-9]{64}$') {
        throw "告警通知演练证据 SHA-256 格式无效"
    }
    $validatedEvidence = Read-NotificationDrillEvidence `
        -Path $NotificationDrillEvidencePath `
        -ExpectedSHA256 $NotificationDrillEvidenceSHA256 `
        -ExpectedChangeId $NotificationDrillChangeId
    $validatedNotificationEvidenceSHA256 = [string]$validatedEvidence.sha256
    $notificationDrillAttested = $true
}

if ($ValidateNotificationEvidenceOnly) {
    if (-not $notificationDrillAttested) {
        throw "仅验证演练证据时必须提供完整演练证据参数"
    }
    Write-Output "notification_evidence_validation=passed"
    Write-Output "notification_drill_change_id=$NotificationDrillChangeId"
    Write-Output "notification_drill_evidence_sha256=$validatedNotificationEvidenceSHA256"
    Write-Output "remote_connections=0"
    Write-Output "business_configuration_mutations=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($SelfTest) {
    Invoke-SelfTest
    exit 0
}

try {
    $closedState = Invoke-ReadOnlyDependency "verify-sms-phase5-test-server-readonly.ps1"
    $candidate = Invoke-ReadOnlyDependency "verify-sms-phase5-test-server-rollback-candidate.ps1" @("-ChangeId", $ChangeId)
    $recovery = Invoke-ReadOnlyDependency "verify-sms-phase5-test-server-recovery-readiness.ps1"
    $logRetention = Invoke-ReadOnlyDependency "verify-sms-phase5-test-server-log-retention.ps1"
    $result = Get-CanaryPreflightResult $closedState $candidate $recovery $logRetention $notificationDrillAttested
}
catch {
    Write-Output "canary_preflight=blocked"
    Write-Output "canary_preflight_ready=false"
    Write-Output "preflight_dependency_execution_failed=true"
    Write-Output "business_configuration_mutations=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
    exit 2
}

if ($result.canary_preflight_ready) {
    Write-Output "canary_preflight=passed"
}
else {
    Write-Output "canary_preflight=blocked"
}
foreach ($entry in $result.GetEnumerator()) {
    Write-Output ("{0}={1}" -f $entry.Key, ([string]$entry.Value).ToLowerInvariant())
}
Write-Output "business_configuration_mutations=0"
Write-Output "access_audit_logs_may_increase=true"
Write-Output "service_restarts=0"
Write-Output "real_sms_sent=0"
if ($notificationDrillAttested) {
    Write-Output "notification_drill_change_id=$NotificationDrillChangeId"
    Write-Output "notification_drill_evidence_sha256=$validatedNotificationEvidenceSHA256"
}

if (-not $result.canary_preflight_ready) {
    exit 2
}
