param(
    [string]$ServerHost = "8.130.9.163",
    [int]$SSHPort = 10003,
    [string]$SSHUser = "pc",
    [int]$PrometheusPort = 19090,
    [int]$AdminPort = 3001,
    [int]$UserPort = 3000,
    [string]$ExpectedTrustedProxyCIDR = "172.20.250.0/28",
    [string]$ExpectedProxyNetwork = "molin-sms-proxy",
    [string]$ExpectedProxySubnet = "172.20.250.0/28",
    [string]$ExpectedAdminIP = "172.20.250.2",
    [string]$ExpectedUserIP = "172.20.250.3",
    [string]$ExpectedBinarySHA256 = "",
    [ValidateRange(0, 86400)]
    [int]$ObservationSeconds = 0
)

$ErrorActionPreference = "Stop"

# 所有替换值先限定为数字、规范网络标识或哈希，避免参数进入远端 Bash 源码后产生命令注入。
foreach ($port in @($PrometheusPort, $AdminPort, $UserPort)) {
    if ($port -lt 1 -or $port -gt 65535) {
        throw "端口必须位于 1-65535"
    }
}
foreach ($cidr in @($ExpectedTrustedProxyCIDR, $ExpectedProxySubnet)) {
    if ($cidr -notmatch '^\d{1,3}(\.\d{1,3}){3}/\d{1,2}$') {
        throw "代理 CIDR 格式无效"
    }
}
foreach ($ip in @($ExpectedAdminIP, $ExpectedUserIP)) {
    if ($ip -notmatch '^\d{1,3}(\.\d{1,3}){3}$') {
        throw "固定代理 IP 格式无效"
    }
}
if ($ExpectedProxyNetwork -notmatch '^[A-Za-z0-9_.-]+$') {
    throw "Docker 网络名称格式无效"
}
if ($ExpectedBinarySHA256 -and $ExpectedBinarySHA256 -notmatch '^[a-fA-F0-9]{64}$') {
    throw "预期二进制 SHA-256 格式无效"
}

# 远端脚本只读取进程、健康接口、聚合数据库计数、Docker 网络和 Prometheus，不输出任何凭据值。
$remoteScriptPath = Join-Path $PSScriptRoot "verify-sms-phase5-test-server-readonly.sh"
$remoteScript = Get-Content -LiteralPath $remoteScriptPath -Raw -Encoding UTF8
$replacements = [ordered]@{
    "__PROMETHEUS_PORT__" = $PrometheusPort.ToString()
    "__ADMIN_PORT__" = $AdminPort.ToString()
    "__USER_PORT__" = $UserPort.ToString()
    "__EXPECTED_TRUSTED_PROXY_CIDR__" = $ExpectedTrustedProxyCIDR
    "__EXPECTED_PROXY_NETWORK__" = $ExpectedProxyNetwork
    "__EXPECTED_PROXY_SUBNET__" = $ExpectedProxySubnet
    "__EXPECTED_ADMIN_IP__" = $ExpectedAdminIP
    "__EXPECTED_USER_IP__" = $ExpectedUserIP
    "__EXPECTED_BINARY_SHA256__" = $ExpectedBinarySHA256.ToLowerInvariant()
    "__OBSERVATION_SECONDS__" = $ObservationSeconds.ToString()
}
foreach ($entry in $replacements.GetEnumerator()) {
    $remoteScript = $remoteScript.Replace($entry.Key, $entry.Value)
}
$remoteScript = $remoteScript -replace "`r`n", "`n"
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
