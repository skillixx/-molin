param(
    [string]$ServerHost = "8.130.9.163",
    [int]$SSHPort = 10003,
    [string]$SSHUser = "pc",
    [string]$DeploymentChangeId = "20260805T084215Z",
    [int]$AlertmanagerPort = 19093,
    [int]$PrometheusPort = 19090,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1")
Assert-SmsPhase5FixedTestServerTarget -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser

if ($DeploymentChangeId -notmatch '^[0-9]{8}T[0-9]{6}Z$') {
    throw "Alertmanager 部署 ChangeId 格式无效"
}
foreach ($port in @($AlertmanagerPort, $PrometheusPort)) {
    if ($port -lt 1 -or $port -gt 65535) {
        throw "监控端口必须位于 1-65535"
    }
}

$payloadPath = Join-Path $PSScriptRoot "verify-sms-phase5-alertmanager-drill-readiness.sh"
$payload = Get-Content -LiteralPath $payloadPath -Raw -Encoding UTF8

if ($SelfTest) {
    # SelfTest 只检查冻结资产，禁止解析 known_hosts 或启动 SSH。
    if (-not $payload.StartsWith("#!/usr/bin/env bash`n") -or $payload.Contains("`r") -or $payload.Contains([char]0xFEFF)) {
        throw "远端脚本必须为无 BOM、LF 的 UTF-8 文件"
    }
    foreach ($placeholder in @(
        "__DEPLOYMENT_DIR__", "__CONTAINER_NAME__", "__ALERTMANAGER_PORT__",
        "__PROMETHEUS_PORT__", "__EXPECTED_IMAGE_ID__"
    )) {
        if ([regex]::Matches($payload, [regex]::Escape($placeholder)).Count -ne 1) {
            throw "占位符数量异常：$placeholder"
        }
    }
    foreach ($marker in @(
        "notification_drill_preflight=passed",
        "closed_route_discard_only",
        "receiver_configuration_loaded",
        "smtp_secret_file_secure=true",
        "receiver_delivery_unverified=true",
        "notification_drill_execution_authorization_required=true",
        "notifications_sent=0",
        "real_sms_sent=0"
    )) {
        if (-not $payload.Contains($marker)) {
            throw "通知演练预检缺少安全标记：$marker"
        }
    }
    foreach ($pattern in @(
        '(?m)^\s*(rm|mv|cp|install|chmod|chown|truncate|touch|tee)\b',
        '\bsed\s+-i\b',
        '\bdocker\s+(run|create|restart|stop|kill|rm|exec|pull)\b',
        '\bsystemctl\s+(restart|stop|start|enable|disable|reload)\b',
        '\bcurl\b[^\n]*(--request|-X|--data|-d|--form|-F)\b',
        '/api/v[12]/alerts[^"\n]*\s+(POST|PUT|DELETE)',
        'SMS_ENABLED=true'
    )) {
        if ($payload -match $pattern) {
            throw "只读契约发现禁止模式：$pattern"
        }
    }
    Write-Output "self_test=passed"
    Write-Output "remote_connections=0"
    Write-Output "business_configuration_mutations=0"
    Write-Output "service_restarts=0"
    Write-Output "notification_attempts=0"
    Write-Output "notifications_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

# 所有运行参数均冻结为固定测试服值，实际演练仍须取得另一份人工授权。
$deploymentDir = "/home/pc/molin-alertmanager-phase5/$DeploymentChangeId"
$replacements = [ordered]@{
    "__DEPLOYMENT_DIR__" = $deploymentDir
    "__CONTAINER_NAME__" = "molin-alertmanager-phase5-closed"
    "__ALERTMANAGER_PORT__" = $AlertmanagerPort.ToString()
    "__PROMETHEUS_PORT__" = $PrometheusPort.ToString()
    "__EXPECTED_IMAGE_ID__" = "sha256:82c38dcc97cd0fbf5d5e31ddfb304dbb3a6e411194477de5de82ec71b328bb40"
}
foreach ($entry in $replacements.GetEnumerator()) {
    if ([regex]::Matches($payload, [regex]::Escape($entry.Key)).Count -ne 1) {
        throw "占位符数量异常：$($entry.Key)"
    }
    $payload = $payload.Replace($entry.Key, $entry.Value)
}

# 只读执行前统一核对固定测试服与唯一 ED25519 指纹。
$knownHostsPath = Assert-SmsPhase5FixedTestServerIdentity `
    -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser
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
    throw "阶段 5 Alertmanager 通知演练只读预检失败"
}
