param(
    [string]$ServerHost = "8.130.9.163",
    [int]$SSHPort = 10003,
    [string]$SSHUser = "pc",
    [string]$SystemMaxUse = "8G",
    [string]$SystemKeepFree = "50G",
    [string]$MaxRetentionSec = "14day",
    [string]$MaxFileSec = "1day",
    [switch]$Apply,
    [string]$Authorization = "",
    [string]$ExportOperatorPayload = "",
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1")
Assert-SmsPhase5FixedTestServerTarget -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser

foreach ($item in @($SystemMaxUse, $SystemKeepFree)) {
    if ($item -cnotmatch '^[1-9][0-9]{0,3}(K|M|G|T)$') {
        throw "journald 容量值必须使用非零整数和 K/M/G/T 单位"
    }
}
foreach ($item in @($MaxRetentionSec, $MaxFileSec)) {
    if ($item -cnotmatch '^[1-9][0-9]{0,3}(s|min|h|day|week)$') {
        throw "journald 时间值必须使用非零整数和 s/min/h/day/week 单位"
    }
}
# 固定授权短语只绑定当前经过容量审计的四项候选；调整任何值都必须先修改资产并重新评审。
if ($SystemMaxUse -cne "8G" -or $SystemKeepFree -cne "50G" -or
    $MaxRetentionSec -cne "14day" -or $MaxFileSec -cne "1day") {
    throw "journald 参数必须与当前冻结候选完全一致"
}

$payloadPath = Join-Path $PSScriptRoot "apply-sms-phase5-test-server-log-retention.sh"
$payload = Get-Content -LiteralPath $payloadPath -Raw -Encoding UTF8
$expectedMachineIdSHA256 = "b60555f0a2defd1c02b752b215989686592244e810e3d22c884ab5d5e8d578d4"

if ($SelfTest) {
    # SelfTest 只检查冻结的本地资产，不读取 known_hosts，也不启动 ssh.exe。
    foreach ($placeholder in @(
        "__SYSTEM_MAX_USE__",
        "__SYSTEM_KEEP_FREE__",
        "__MAX_RETENTION_SEC__",
        "__MAX_FILE_SEC__"
    )) {
        if ([regex]::Matches($payload, [regex]::Escape($placeholder)).Count -lt 2) {
            throw "变更 payload 占位符覆盖不足：$placeholder"
        }
    }
    if ([regex]::Matches($payload, [regex]::Escape("__EXPECTED_MACHINE_ID_SHA256__")).Count -ne 1) {
        throw "测试服 machine-id 摘要占位符数量异常"
    }
    if (-not $payload.StartsWith("#!/usr/bin/env bash`n") -or $payload.Contains("`r") -or $payload.Contains([char]0xFEFF)) {
        throw "远端脚本必须为无 BOM 的 LF UTF-8"
    }
    foreach ($forbidden in @(
        'SMS_ENABLED=true',
        '/api/auth/verification-codes/phone',
        '/api/admin/sms/.+test-send',
        '\bjournalctl\s+(--vacuum|--rotate|--flush)\b'
    )) {
        if ($payload -match $forbidden) {
            throw "变更 payload 包含禁止模式：$forbidden"
        }
    }
    Write-Output "self_test=passed"
    Write-Output "remote_connections=0"
    Write-Output "configuration_writes=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_delivery_not_verified=true"
    exit 0
}

if ($Apply -and -not [string]::IsNullOrWhiteSpace($ExportOperatorPayload)) {
    throw "远端执行与运维脚本导出不能同时启用"
}
$exportRequested = -not [string]::IsNullOrWhiteSpace($ExportOperatorPayload)

if (-not $Apply -and -not $exportRequested) {
    # 默认仅展示非敏感计划，不读取 SSH 身份且不连接远端。
    Write-Output "apply_authorized=false"
    Write-Output "planned_system_max_use=$SystemMaxUse"
    Write-Output "planned_system_keep_free=$SystemKeepFree"
    Write-Output "planned_max_retention_sec=$MaxRetentionSec"
    Write-Output "planned_max_file_sec=$MaxFileSec"
    Write-Output "remote_connections=0"
    Write-Output "configuration_writes=0"
    Write-Output "service_restarts=0"
    exit 0
}
if ($Authorization -cne "APPROVE_TEST_JOURNALD_RETENTION") {
    throw "远端变更或运维脚本导出必须同时提供固定授权短语"
}

$replacements = [ordered]@{
    "__SYSTEM_MAX_USE__" = $SystemMaxUse
    "__SYSTEM_KEEP_FREE__" = $SystemKeepFree
    "__MAX_RETENTION_SEC__" = $MaxRetentionSec
    "__MAX_FILE_SEC__" = $MaxFileSec
    "__EXPECTED_MACHINE_ID_SHA256__" = $expectedMachineIdSHA256
}
foreach ($entry in $replacements.GetEnumerator()) {
    $payload = $payload.Replace($entry.Key, $entry.Value)
}
$payload = $payload -replace "`r`n", "`n"

if ($exportRequested) {
    # 离线导出只生成冻结后的无敏感脚本，不读取 known_hosts、不连接远端，也不覆盖已有文件。
    if (-not [IO.Path]::IsPathRooted($ExportOperatorPayload)) {
        throw "运维脚本导出路径必须为绝对路径"
    }
    $exportPath = [IO.Path]::GetFullPath($ExportOperatorPayload)
    if ([IO.Path]::GetExtension($exportPath) -cne ".sh") {
        throw "运维脚本导出路径必须使用 .sh 扩展名"
    }
    $exportParent = [IO.Path]::GetDirectoryName($exportPath)
    if ([string]::IsNullOrWhiteSpace($exportParent) -or
        -not (Test-Path -LiteralPath $exportParent -PathType Container) -or
        ([IO.DirectoryInfo]$exportParent).Attributes.HasFlag([IO.FileAttributes]::ReparsePoint)) {
        throw "运维脚本导出目录不存在或属于重解析路径"
    }
    if (Test-Path -LiteralPath $exportPath) {
        throw "运维脚本导出目标已存在，禁止覆盖"
    }

    $payloadBytes = (New-Object Text.UTF8Encoding($false)).GetBytes($payload)
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
            $stream.Write($payloadBytes, 0, $payloadBytes.Length)
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

    $payloadHash = [Security.Cryptography.SHA256]::Create()
    try {
        $exportSHA256 = ([BitConverter]::ToString($payloadHash.ComputeHash($payloadBytes))).Replace("-", "").ToLowerInvariant()
    }
    finally {
        $payloadHash.Dispose()
    }
    Write-Output "operator_payload_exported=true"
    Write-Output "operator_payload_sha256=$exportSHA256"
    Write-Output "remote_connections=0"
    Write-Output "configuration_writes=0"
    Write-Output "service_restarts=0"
    Write-Output "local_operator_payload_writes=1"
    Write-Output "real_sms_delivery_not_verified=true"
    exit 0
}

# 只有远端执行双门禁通过后才读取 known_hosts；计划、SelfTest 和导出模式均不读取 SSH 身份。
$knownHostsPath = Assert-SmsPhase5FixedTestServerIdentity -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser

$encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($payload))
$destination = "${SSHUser}@${ServerHost}"
$sshArguments = @(
    "-p", $SSHPort.ToString(),
    "-o", "BatchMode=yes",
    "-o", "ConnectTimeout=8",
    "-o", "StrictHostKeyChecking=yes",
    "-o", "HostKeyAlgorithms=ssh-ed25519",
    "-o", "UserKnownHostsFile=$knownHostsPath",
    $destination,
    "printf '%s' '$encoded' | base64 -d | bash"
)

& ssh @sshArguments
if ($LASTEXITCODE -ne 0) {
    throw "阶段 5 测试服 journald 留存策略变更失败"
}
