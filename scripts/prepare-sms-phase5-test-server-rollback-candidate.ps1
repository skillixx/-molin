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
$requiredApprovalPhrase = "我已批准生成阶段5测试服回滚候选配置"

# SelfTest 与真实执行必须互斥；不带开关时失败关闭，避免误连测试服务器。
if ($SelfTest -and $Execute) {
    throw "-SelfTest 与 -Execute 不能同时使用"
}
if (-not $SelfTest -and -not $Execute) {
    throw "必须显式使用 -SelfTest 或 -Execute"
}
# 该批准口令只适用于当前测试服，禁止把同一写入能力转向其他主机、账号或端口。
if ($ServerHost -cne "8.130.9.163" -or $SSHUser -cne "pc" -or $SSHPort -ne 10003) {
    throw "SSH 目标必须固定为阶段 5 测试服务器"
}
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

# 除固定地址外再冻结 ED25519 公钥指纹，防止本地 known_hosts 被替换后把写入能力导向错误主机。
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
