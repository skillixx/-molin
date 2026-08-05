param(
    [Parameter(Mandatory = $true)]
    [string]$ChangeId,
    [string]$CandidateChangeId = "20260805T015043Z",
    [string]$ExportOperatorPayload = "",
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"

if ($ChangeId -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') {
    throw "回滚演练 ChangeId 必须使用 UTC 基本格式"
}
if ($CandidateChangeId -cne "20260805T015043Z") {
    throw "只能使用已经独立验证的固定回滚候选"
}

$payloadPath = Join-Path $PSScriptRoot "run-sms-phase5-test-server-rollback-drill.sh"
$payload = Get-Content -LiteralPath $payloadPath -Raw -Encoding UTF8
$authorizationPhrase = "APPROVE_SMS_PHASE5_TEST_ROLLBACK_DRILL_$ChangeId"
$exportRequested = -not [string]::IsNullOrWhiteSpace($ExportOperatorPayload)
if ($SelfTest -and $exportRequested) {
    throw "SelfTest 与交接脚本导出必须互斥"
}
$replacements = [ordered]@{
    "__CHANGE_ID__" = $ChangeId
    "__AUTHORIZATION_PHRASE__" = $authorizationPhrase
    "__MACHINE_ID_SHA256__" = "b60555f0a2defd1c02b752b215989686592244e810e3d22c884ab5d5e8d578d4"
    "__CANDIDATE_CHANGE_ID__" = $CandidateChangeId
    "__CANDIDATE_SHA256__" = "8435f846ff2e5815bec889ac4e4c32d432acb06bb05c0e1e9c3bd6b02bb65494"
    "__OLD_BINARY_SHA256__" = "c18aa8d0efe51e2b9cccf924b275983741dcd5194fa3bb25e1d292888b926cc9"
    "__CURRENT_BINARY_SHA256__" = "4ade3d34a7b9473a23cbda80c4a4451192725da66caa2dc09aab454c05fdd8b0"
    "__ALERTMANAGER_CONFIG_SHA256__" = "2e906ed20a48d2585f7b7648892de1ee809afdf34c6e45b9a110722fab48239d"
    "__OLD_HOLD_SECONDS__" = "10"
    "__RESTORED_HOLD_SECONDS__" = "10"
}
foreach ($entry in $replacements.GetEnumerator()) {
    if ([regex]::Matches($payload, [regex]::Escape($entry.Key)).Count -lt 1) {
        throw "回滚演练 payload 缺少占位符：$($entry.Key)"
    }
    $payload = $payload.Replace($entry.Key, $entry.Value)
}
$payload = $payload -replace "`r`n", "`n"

if (-not $payload.StartsWith("#!/usr/bin/env bash`n") -or $payload.Contains("`r") -or
    $payload.Contains([char]0xFEFF) -or $payload -match '__[A-Z0-9_]+__') {
    throw "冻结后的回滚演练 payload 编码或占位符状态异常"
}

# SelfTest 只检查本地冻结资产，不读取 SSH 身份、不连接测试服，也不创建交接文件。
if ($SelfTest) {
    foreach ($marker in @(
        "rollback_runtime_preflight=passed",
        "old_binary_runtime_verified=true",
        "current_binary_restored=true",
        "current_environment_file_replaced=false",
        "notification_delta_zero=true",
        "sms_send_log_delta_zero=true",
        "real_sms_sent=0"
    )) {
        if (-not $payload.Contains($marker)) {
            throw "回滚演练 payload 缺少结果标记：$marker"
        }
    }
    foreach ($forbidden in @(
        'SMS_ENABLED=true',
        '/api/auth/verification-codes/phone',
        '/api/admin/sms/.+test-send',
        '\bcurl\b[^\n]*(--request|-X|--data|-d|--form|-F)\b',
        '\brm\s+(-[^\n]*r|--recursive)\b',
        '\bgit\s+(reset|push|checkout|restore)\b',
        '\bdocker\s+(restart|stop|kill|rm|run|create|pull)\b'
    )) {
        if ($payload -match $forbidden) {
            throw "回滚演练 payload 包含禁止模式：$forbidden"
        }
    }
    if ([regex]::Matches($payload, '(?m)^\s*kill -TERM ').Count -ne 1 -or
        [regex]::Matches($payload, '(?m)^\s*kill -KILL ').Count -ne 1) {
        throw "API 停止路径必须各自只有一个精确 TERM/KILL 信号点"
    }
    if ([regex]::Matches($payload, '(?m)^\s*nohup python3 ').Count -ne 1) {
        throw "API 启动路径必须收敛到唯一安全启动器"
    }
    Write-Output "rollback_runtime_candidate_self_test=passed"
    Write-Output "change_id=$ChangeId"
    Write-Output "candidate_change_id=$CandidateChangeId"
    Write-Output "remote_connections=0"
    Write-Output "local_files_written=0"
    Write-Output "service_restarts=0"
    Write-Output "notification_posts=0"
    Write-Output "business_endpoint_posts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if (-not $exportRequested) {
    Write-Output "rollback_runtime_candidate_ready=true"
    Write-Output "change_id=$ChangeId"
    Write-Output "candidate_change_id=$CandidateChangeId"
    Write-Output "authorization_phrase=$authorizationPhrase"
    Write-Output "export_required=true"
    Write-Output "remote_connections=0"
    Write-Output "local_files_written=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if (-not [IO.Path]::IsPathRooted($ExportOperatorPayload) -or $ExportOperatorPayload.StartsWith("\\")) {
    throw "交接脚本必须导出到本机完全限定的绝对路径"
}
$isWindowsRuntime = [IO.Path]::DirectorySeparatorChar -eq "\"
if (($isWindowsRuntime -and $ExportOperatorPayload -cnotmatch '^[A-Za-z]:[\\/]') -or
    (-not $isWindowsRuntime -and -not $ExportOperatorPayload.StartsWith("/"))) {
    throw "交接脚本不能使用驱动器相对路径或根相对路径"
}
$exportPath = [IO.Path]::GetFullPath($ExportOperatorPayload)
if ([IO.Path]::GetExtension($exportPath) -cne ".sh") {
    throw "交接脚本必须使用 .sh 扩展名"
}
$exportParent = [IO.Path]::GetDirectoryName($exportPath)
if ([string]::IsNullOrWhiteSpace($exportParent) -or -not (Test-Path -LiteralPath $exportParent -PathType Container)) {
    throw "交接脚本父目录不存在"
}
$currentParent = [IO.DirectoryInfo]$exportParent
while ($null -ne $currentParent) {
    if ($currentParent.Attributes.HasFlag([IO.FileAttributes]::ReparsePoint)) {
        throw "交接脚本目录链不能包含重解析点"
    }
    $currentParent = $currentParent.Parent
}
if (Test-Path -LiteralPath $exportPath) {
    throw "交接脚本已存在，禁止覆盖"
}
if ($isWindowsRuntime) {
    $exportDrive = New-Object IO.DriveInfo([IO.Path]::GetPathRoot($exportPath))
    if ($exportDrive.DriveType -eq [IO.DriveType]::Network) {
        throw "交接脚本不能导出到映射网络驱动器"
    }
}

$bytes = (New-Object Text.UTF8Encoding($false)).GetBytes($payload)
$created = $false
try {
    $stream = New-Object IO.FileStream(
        $exportPath,
        [IO.FileMode]::CreateNew,
        [IO.FileAccess]::Write,
        [IO.FileShare]::None
    )
    $created = $true
    try {
        $stream.Write($bytes, 0, $bytes.Length)
        $stream.Flush($true)
    }
    finally {
        $stream.Dispose()
    }
}
catch {
    if ($created -and (Test-Path -LiteralPath $exportPath -PathType Leaf)) {
        Remove-Item -LiteralPath $exportPath -Force
    }
    throw
}
$sha = [Security.Cryptography.SHA256]::Create()
try {
    $digest = ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace("-", "").ToLowerInvariant()
}
finally {
    $sha.Dispose()
}

Write-Output "rollback_runtime_candidate_exported=true"
Write-Output "change_id=$ChangeId"
Write-Output "candidate_change_id=$CandidateChangeId"
Write-Output "operator_payload_sha256=$digest"
Write-Output "authorization_phrase=$authorizationPhrase"
Write-Output "remote_connections=0"
Write-Output "local_files_written=1"
Write-Output "service_restarts=0"
Write-Output "notification_posts=0"
Write-Output "business_endpoint_posts=0"
Write-Output "real_sms_sent=0"
