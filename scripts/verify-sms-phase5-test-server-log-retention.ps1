param(
    [string]$ServerHost = "8.130.9.163",
    [int]$SSHPort = 10003,
    [string]$SSHUser = "pc",
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
$payloadPath = Join-Path $PSScriptRoot "verify-sms-phase5-test-server-log-retention.sh"
$payload = Get-Content -LiteralPath $payloadPath -Raw -Encoding UTF8

if ($SelfTest) {
    # SelfTest 只验证冻结资产，不解析或启动 ssh.exe。
    if (-not $payload.StartsWith("#!/usr/bin/env bash`n") -or $payload.Contains("`r") -or $payload.Contains([char]0xFEFF)) {
        throw "Payload must be UTF-8 with LF and without BOM"
    }
    foreach ($marker in @(
        "log_retention_preflight",
        "journald_capacity_limit_configured",
        "journald_retention_limit_configured",
        "log_retention_policy_verified",
        "remote_mutations=0",
        "real_sms_sent=0"
    )) {
        if (-not $payload.Contains($marker)) {
            throw "Log retention payload marker is missing: $marker"
        }
    }
    foreach ($pattern in @(
        '(?m)^\s*(rm|mv|cp|install|chmod|chown|truncate|touch|tee)\b',
        '\bsed\s+-i\b',
        '\bsystemctl\s+(restart|stop|start|enable|disable|reload)\b',
        '\bjournalctl\s+(--vacuum|--rotate|--flush)\b',
        'SMS_ENABLED=true'
    )) {
        if ($payload -match $pattern) {
            throw "Read-only contract rejected pattern: $pattern"
        }
    }
    Write-Output "self_test=passed"
    Write-Output "remote_connections=0"
    Write-Output "remote_mutations=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

# 该只读授权仅适用于阶段 5 固定测试服，禁止转向其他主机、账号或端口。
if ($ServerHost -cne "8.130.9.163" -or $SSHUser -cne "pc" -or $SSHPort -ne 10003) {
    throw "SSH target must be the fixed phase 5 test server"
}

# 固定 known_hosts 普通文件并核对唯一 ED25519 指纹，避免地址被劫持后泄露运行状态。
$knownHostsPath = [IO.Path]::GetFullPath((Join-Path $env:USERPROFILE ".ssh\known_hosts"))
if (-not (Test-Path -LiteralPath $knownHostsPath -PathType Leaf) -or
    ([IO.FileInfo]$knownHostsPath).Attributes.HasFlag([IO.FileAttributes]::ReparsePoint)) {
    throw "Fixed known_hosts file is missing or is a reparse path"
}
$knownHostLines = @(& ssh-keygen -F "[8.130.9.163]:10003" -f $knownHostsPath)
if ($LASTEXITCODE -ne 0) {
    throw "Fixed test server identity is missing from known_hosts"
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
    throw "Fixed test server ED25519 key count is invalid"
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
    throw "Fixed test server ED25519 fingerprint does not match"
}

$payload = $payload -replace "`r`n", "`n"
$encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($payload))
$sshArguments = @(
    "-p", "10003",
    "-o", "BatchMode=yes",
    "-o", "ConnectTimeout=8",
    "-o", "StrictHostKeyChecking=yes",
    "-o", "HostKeyAlgorithms=ssh-ed25519",
    "-o", "UserKnownHostsFile=$knownHostsPath",
    "pc@8.130.9.163",
    "printf '%s' '$encoded' | base64 -d | bash"
)

& ssh @sshArguments
if ($LASTEXITCODE -ne 0) {
    throw "Phase 5 test server log retention read-only audit failed"
}
