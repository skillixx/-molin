param(
    [string]$ServerHost = "8.130.9.163",
    [int]$SSHPort = 10003,
    [string]$SSHUser = "pc",
    [string]$ChangeId = "",
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1")
Assert-SmsPhase5FixedTestServerTarget -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser

$payloadPath = Join-Path $PSScriptRoot "verify-sms-phase5-test-server-rollback-candidate.sh"
$payload = Get-Content -LiteralPath $payloadPath -Raw -Encoding UTF8

if ($SelfTest) {
    # SelfTest 只检查本地冻结资产，不读取 known_hosts，也不启动 SSH。
    if ([regex]::Matches($payload, [regex]::Escape("__CANDIDATE_PATH__")).Count -ne 1) {
        throw "候选路径占位符数量异常"
    }
    if (-not $payload.StartsWith("#!/usr/bin/env bash`n") -or $payload.Contains("`r") -or $payload.Contains([char]0xFEFF)) {
        throw "候选验证 payload 必须为无 BOM 的 LF UTF-8"
    }
    foreach ($forbidden in @(
        'SMS_ENABLED=true',
        '(?m)^\s*(rm|mv|cp|install|chmod|chown|touch|truncate)\b',
        '(?m)^\s*(systemctl|docker|curl|wget|mysql|redis-cli)\b'
    )) {
        if ($payload -match $forbidden) {
            throw "候选验证 payload 包含禁止模式：$forbidden"
        }
    }
    Write-Output "self_test=passed"
    Write-Output "remote_connections=0"
    Write-Output "business_configuration_mutations=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_delivery_not_verified=true"
    exit 0
}

if ($ChangeId -notmatch '^[0-9]{8}T[0-9]{6}Z$') {
    throw "必须提供 UTC ChangeId，格式为 YYYYMMDDTHHMMSSZ"
}

# 只有真实只读验证才读取固定 known_hosts；验证器没有写入或重启入口。
$knownHostsPath = Assert-SmsPhase5FixedTestServerIdentity -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser
$candidatePath = "/home/pc/molin/rollback/sms-phase5/candidate-$ChangeId.env"
$payload = $payload.Replace("__CANDIDATE_PATH__", $candidatePath)
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
    throw "阶段 5 测试服回滚候选只读验证失败"
}
