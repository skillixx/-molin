param(
    [string]$ServerHost = "8.130.9.163",
    [int]$SSHPort = 10003,
    [string]$SSHUser = "pc",
    [string]$ChangeId = "20260805T115540Z",
    [string]$ExpectedRunnerSHA256 = "2724b89ea0096b15e5c443a2f5dfdd7e80f93c971ff2fb22a3585a5a1ad2bb46",
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1")
Assert-SmsPhase5FixedTestServerTarget -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser

if ($ChangeId -cne "20260805T115540Z" -or
    $ExpectedRunnerSHA256 -cne "2724b89ea0096b15e5c443a2f5dfdd7e80f93c971ff2fb22a3585a5a1ad2bb46") {
    throw "独立验收器只允许当前冻结回滚演练"
}

$payloadPath = Join-Path $PSScriptRoot "verify-sms-phase5-test-server-rollback-drill.sh"
$payload = Get-Content -LiteralPath $payloadPath -Raw -Encoding UTF8
$replacements = [ordered]@{
    "__CHANGE_ID__" = $ChangeId
    "__RUNNER_SHA256__" = $ExpectedRunnerSHA256
    "__CANDIDATE_SHA256__" = "8435f846ff2e5815bec889ac4e4c32d432acb06bb05c0e1e9c3bd6b02bb65494"
    "__OLD_BINARY_SHA256__" = "c18aa8d0efe51e2b9cccf924b275983741dcd5194fa3bb25e1d292888b926cc9"
    "__CURRENT_BINARY_SHA256__" = "4ade3d34a7b9473a23cbda80c4a4451192725da66caa2dc09aab454c05fdd8b0"
    "__ALERTMANAGER_CONFIG_SHA256__" = "2e906ed20a48d2585f7b7648892de1ee809afdf34c6e45b9a110722fab48239d"
}
foreach ($entry in $replacements.GetEnumerator()) {
    if ([regex]::Matches($payload, [regex]::Escape($entry.Key)).Count -lt 1) {
        throw "独立验收 payload 缺少占位符：$($entry.Key)"
    }
    $payload = $payload.Replace($entry.Key, $entry.Value)
}
$payload = $payload -replace "`r`n", "`n"
if (-not $payload.StartsWith("#!/usr/bin/env bash`n") -or $payload.Contains("`r") -or
    $payload.Contains([char]0xFEFF) -or $payload -match '__[A-Z0-9_]+__') {
    throw "独立验收 payload 编码或占位符状态异常"
}

if ($SelfTest) {
    foreach ($marker in @(
        "rollback_runtime_evidence=passed",
        "old_binary_runtime_verified=true",
        "current_binary_restored=true",
        "current_environment_unchanged=true",
        "rollback_restore_runtime_verified=true",
        "notification_snapshot=3:0:3:0",
        "sensitive_evidence_files=0",
        "sensitive_log_findings=0",
        "remote_files_written=0",
        "service_restarts=0",
        "real_sms_sent=0"
    )) {
        if (-not $payload.Contains($marker)) {
            throw "独立验收 payload 缺少安全标记：$marker"
        }
    }
    foreach ($forbidden in @(
        'SMS_ENABLED=true',
        '/api/auth/verification-codes/phone',
        '/api/admin/sms/.+test-send',
        '\bcurl\b[^\n]*(--request|-X|--data|-d|--form|-F)\b',
        '(?m)^\s*(rm|mv|cp|install|chmod|chown|truncate|touch|tee)\b',
        '\bsed\s+-i\b',
        '\bkill\b',
        '\bsystemctl\s+(restart|reload|stop|start)\b',
        '\bdocker\s+(restart|stop|kill|rm|run|create|pull)\b'
    )) {
        if ($payload -match $forbidden) {
            throw "独立验收 payload 包含禁止模式：$forbidden"
        }
    }
    Write-Output "rollback_runtime_evidence_self_test=passed"
    Write-Output "remote_connections=0"
    Write-Output "remote_files_written=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

$knownHostsPath = Assert-SmsPhase5FixedTestServerIdentity `
    -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser
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
& ssh.exe @sshArguments
if ($LASTEXITCODE -ne 0) {
    throw "阶段 5 测试服实际回滚恢复独立验收失败"
}
