param(
    [string]$ServerHost = "8.130.9.163",
    [int]$SSHPort = 10003,
    [string]$SSHUser = "pc",
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1")
Assert-SmsPhase5FixedTestServerTarget -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser
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
        "business_configuration_mutations=0",
        "access_audit_logs_may_increase=true",
        "real_sms_delivery_not_verified=true"
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
    Write-Output "business_configuration_mutations=0"
    Write-Output "access_audit_logs_may_increase=false"
    Write-Output "real_sms_delivery_not_verified=true"
    exit 0
}

# 只读执行前统一核对固定测试服与唯一 ED25519 指纹。
$knownHostsPath = Assert-SmsPhase5FixedTestServerIdentity -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser

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
