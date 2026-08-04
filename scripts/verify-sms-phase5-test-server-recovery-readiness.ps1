param(
    [string]$ServerHost = "8.130.9.163",
    [int]$SSHPort = 10003,
    [string]$SSHUser = "pc",
    [string]$BackupPath = "/home/pc/molin/backups/sms-phase5-20260804T120056Z",
    [string]$ExpectedOldBinarySHA256 = "c18aa8d0efe51e2b9cccf924b275983741dcd5194fa3bb25e1d292888b926cc9",
    [string]$ExpectedCurrentBinarySHA256 = "4ade3d34a7b9473a23cbda80c4a4451192725da66caa2dc09aab454c05fdd8b0",
    [int]$PrometheusPort = 19090,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"

# 所有参数都先收敛为固定格式，避免替换到远端 Bash 后形成命令注入。
if ($ServerHost -notmatch '^[A-Za-z0-9.-]+$' -or $SSHUser -notmatch '^[A-Za-z0-9._-]+$') {
    throw "SSH 目标格式无效"
}
if ($SSHPort -lt 1 -or $SSHPort -gt 65535 -or $PrometheusPort -lt 1 -or $PrometheusPort -gt 65535) {
    throw "端口必须位于 1-65535"
}
if ($BackupPath -notmatch '^/home/pc/molin/backups/sms-phase5-[0-9]{8}T[0-9]{6}Z$') {
    throw "备份路径不符合阶段 5 固定格式"
}
foreach ($hash in @($ExpectedOldBinarySHA256, $ExpectedCurrentBinarySHA256)) {
    if ($hash -notmatch '^[a-fA-F0-9]{64}$') {
        throw "二进制 SHA-256 格式无效"
    }
}

$payloadPath = Join-Path $PSScriptRoot "verify-sms-phase5-test-server-recovery-readiness.sh"
$payload = Get-Content -LiteralPath $payloadPath -Raw -Encoding UTF8

if ($SelfTest) {
    # SelfTest 只检查本地冻结资产；不得解析或启动 ssh.exe。
    $required = @(
        "__BACKUP_PATH__",
        "__EXPECTED_OLD_BINARY_SHA256__",
        "__EXPECTED_CURRENT_BINARY_SHA256__",
        "__PROMETHEUS_PORT__"
    )
    foreach ($placeholder in $required) {
        if ([regex]::Matches($payload, [regex]::Escape($placeholder)).Count -ne 1) {
            throw "占位符数量异常：$placeholder"
        }
    }
    if (-not $payload.StartsWith("#!/usr/bin/env bash`n") -or $payload.Contains("`r") -or $payload.Contains([char]0xFEFF)) {
        throw "远端脚本必须为无 BOM 的 LF UTF-8"
    }
    $forbidden = @(
        '(?m)^\s*(rm|mv|cp|install|chmod|chown|truncate|touch)\b',
        '\bsed\s+-i\b',
        '\bdocker\s+(run|create|restart|stop|kill|rm|exec)\b',
        '\bsystemctl\s+(restart|stop|start|enable|disable)\b',
        '/-/reload',
        '/api/v1/alerts',
        'SMS_ENABLED=true'
    )
    foreach ($pattern in $forbidden) {
        if ($payload -match $pattern) {
            throw "只读契约发现禁止模式：$pattern"
        }
    }
    Write-Output "self_test=passed"
    Write-Output "contract_checks=15"
    Write-Output "remote_connections=0"
    Write-Output "remote_mutations=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

$replacements = [ordered]@{
    "__BACKUP_PATH__" = $BackupPath
    "__EXPECTED_OLD_BINARY_SHA256__" = $ExpectedOldBinarySHA256.ToLowerInvariant()
    "__EXPECTED_CURRENT_BINARY_SHA256__" = $ExpectedCurrentBinarySHA256.ToLowerInvariant()
    "__PROMETHEUS_PORT__" = $PrometheusPort.ToString()
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
    $destination,
    "printf '%s' '$encoded' | base64 -d | bash"
)

& ssh @sshArguments
if ($LASTEXITCODE -ne 0) {
    throw "阶段 5 测试服回滚与通知只读预检失败"
}
