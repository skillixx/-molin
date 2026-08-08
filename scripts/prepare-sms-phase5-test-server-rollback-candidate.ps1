param(
    [string]$ServerHost = "8.130.9.163",
    [int]$SSHPort = 10003,
    [string]$SSHUser = "pc",
    [string]$CurrentEnvironmentPath = "/home/pc/molin/infra/.env.test",
    [string]$ChangeId = "",
    [switch]$SelfTest,
    [switch]$Execute,
    [string]$ApprovalPhrase = ""
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1")
Assert-SmsPhase5FixedTestServerTarget -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser
$requiredApprovalPhrase = "我已批准生成阶段5测试服回滚候选配置"

# SelfTest 与真实执行必须互斥；不带开关时失败关闭，避免误连测试服务器。
if ($SelfTest -and $Execute) {
    throw "-SelfTest 与 -Execute 不能同时使用"
}
if (-not $SelfTest -and -not $Execute) {
    throw "必须显式使用 -SelfTest 或 -Execute"
}
# 该批准口令只适用于当前测试服，禁止把同一写入能力转向其他主机、账号或端口。
if ($CurrentEnvironmentPath -cne "/home/pc/molin/infra/.env.test") {
    throw "当前环境文件必须使用测试服固定路径"
}

$payloadPath = Join-Path $PSScriptRoot "prepare-sms-phase5-test-server-rollback-candidate.sh"
$payload = Get-Content -LiteralPath $payloadPath -Raw -Encoding UTF8

if ($SelfTest) {
    # 离线自检只读取仓库 payload，不解析 ssh.exe，也不创建本地或远端候选文件。
    $placeholderCounts = [ordered]@{
        "__CURRENT_ENVIRONMENT_PATH__" = 1
        "__CANDIDATE_PATH__" = 1
        "__CANDIDATE_ROOT__" = 2
    }
    foreach ($entry in $placeholderCounts.GetEnumerator()) {
        if ([regex]::Matches($payload, [regex]::Escape($entry.Key)).Count -ne $entry.Value) {
            throw "占位符数量异常：$($entry.Key)"
        }
    }
    if (-not $payload.StartsWith("#!/usr/bin/env bash`n") -or $payload.Contains("`r") -or $payload.Contains([char]0xFEFF)) {
        throw "远端脚本必须为无 BOM 的 LF UTF-8"
    }
    foreach ($forbidden in @(
        'SMS_ENABLED=true',
        '(?m)^\s*(source|\.)\s+',
        '(?m)^\s*(systemctl|docker|curl|wget|mysql|redis-cli)\b',
        '(?m)^\s*cat\s+'
    )) {
        if ($payload -match $forbidden) {
            throw "候选生成契约发现禁止模式：$forbidden"
        }
    }
    Write-Output "self_test=passed"
    Write-Output "remote_connections=0"
    Write-Output "remote_files_written=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ApprovalPhrase -cne $requiredApprovalPhrase) {
    throw "真实生成必须提供精确批准口令：$requiredApprovalPhrase"
}
if ($ChangeId -notmatch '^[0-9]{8}T[0-9]{6}Z$') {
    throw "ChangeId 必须为 UTC 时间格式 YYYYMMDDTHHMMSSZ"
}

# 真实生成门禁通过后才读取 known_hosts，离线 SelfTest 不读取 SSH 身份。
$knownHostsPath = Assert-SmsPhase5FixedTestServerIdentity -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser

$candidatePath = "/home/pc/molin/rollback/sms-phase5/candidate-$ChangeId.env"
$payload = $payload.Replace("__CURRENT_ENVIRONMENT_PATH__", $CurrentEnvironmentPath)
$payload = $payload.Replace("__CANDIDATE_PATH__", $candidatePath)
$payload = $payload.Replace("__CANDIDATE_ROOT__", "/home/pc/molin/rollback/sms-phase5")
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
    throw "阶段 5 测试服回滚候选配置生成失败"
}
