param(
    [string]$ServerHost = "8.130.9.163",
    [int]$SSHPort = 10003,
    [string]$SSHUser = "pc",
    [int]$PrometheusPort = 19090
)

$ErrorActionPreference = "Stop"

# 远端脚本只读取进程、健康接口、聚合数据库计数、Docker 网络和 Prometheus 规则，不输出任何凭据值。
$remoteScriptPath = Join-Path $PSScriptRoot "verify-sms-phase5-test-server-readonly.sh"
$remoteScript = Get-Content -LiteralPath $remoteScriptPath -Raw -Encoding UTF8
$remoteScript = $remoteScript.Replace("__PROMETHEUS_PORT__", $PrometheusPort.ToString()) -replace "`r`n", "`n"
$encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($remoteScript))
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
    throw "阶段 5 测试服只读审计执行失败"
}
