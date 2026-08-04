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
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"

# 本变更资产只允许固定测试服；生产环境和其他账号必须另建审批与脚本。
if ($ServerHost -cne "8.130.9.163" -or $SSHUser -cne "pc" -or $SSHPort -ne 10003) {
    throw "SSH 目标必须固定为阶段 5 测试服务器"
}
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

if (-not $Apply) {
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
    throw "远端变更必须同时提供固定授权短语"
}

# 只有双门禁通过后才读取固定 SSH 身份，避免计划模式产生任何远端或身份副作用。
$knownHostsPath = [IO.Path]::GetFullPath((Join-Path $env:USERPROFILE ".ssh\known_hosts"))
if (-not (Test-Path -LiteralPath $knownHostsPath -PathType Leaf) -or
    ([IO.FileInfo]$knownHostsPath).Attributes.HasFlag([IO.FileAttributes]::ReparsePoint)) {
    throw "固定 known_hosts 文件不存在或属于重解析路径"
}
$knownHostLines = @(& ssh-keygen -F "[8.130.9.163]:10003" -f $knownHostsPath)
if ($LASTEXITCODE -ne 0) {
    throw "known_hosts 中缺少固定测试服身份"
}
$ed25519Keys = @()
foreach ($line in $knownHostLines) {
    $trimmed = $line.Trim()
    if ($trimmed.Length -eq 0 -or $trimmed.StartsWith("#")) {
        continue
    }
    $parts = @($trimmed -split '\s+')
    if ($parts.Count -ge 3 -and $parts[1] -ceq "ssh-ed25519") {
        $ed25519Keys += $parts[2]
    }
}
if ($ed25519Keys.Count -ne 1) {
    throw "固定测试服 ED25519 公钥数量异常"
}
$sha256 = [Security.Cryptography.SHA256]::Create()
try {
    $fingerprint = "SHA256:" + [Convert]::ToBase64String(
        $sha256.ComputeHash([Convert]::FromBase64String($ed25519Keys[0]))
    ).TrimEnd('=')
}
finally {
    $sha256.Dispose()
}
if ($fingerprint -cne "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I") {
    throw "固定测试服 ED25519 公钥指纹不匹配"
}

$replacements = [ordered]@{
    "__SYSTEM_MAX_USE__" = $SystemMaxUse
    "__SYSTEM_KEEP_FREE__" = $SystemKeepFree
    "__MAX_RETENTION_SEC__" = $MaxRetentionSec
    "__MAX_FILE_SEC__" = $MaxFileSec
}
foreach ($entry in $replacements.GetEnumerator()) {
    $payload = $payload.Replace($entry.Key, $entry.Value)
}
$payload = $payload -replace "`r`n", "`n"
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
