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
    [ValidateRange(1, 100)]
    [int]$ExpectedWhitelistCount = 1,
    [ValidateRange(0, 86400)]
    [int]$ObservationSeconds = 0,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"

# 关闭态只读批准仅适用于固定测试服，参数转向其他目标时失败关闭。
if ($ServerHost -cne "8.130.9.163" -or $SSHUser -cne "pc" -or $SSHPort -ne 10003) {
    throw "SSH 目标必须固定为阶段 5 测试服务器"
}

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

if ($SelfTest) {
    # SelfTest 只检查本地冻结资产，不解析 known_hosts，也不启动 ssh.exe。
    foreach ($placeholder in @(
        "__PROMETHEUS_PORT__",
        "__ADMIN_PORT__",
        "__USER_PORT__",
        "__EXPECTED_TRUSTED_PROXY_CIDR__",
        "__EXPECTED_PROXY_NETWORK__",
        "__EXPECTED_PROXY_SUBNET__",
        "__EXPECTED_ADMIN_IP__",
        "__EXPECTED_USER_IP__",
        "__EXPECTED_BINARY_SHA256__",
        "__EXPECTED_WHITELIST_COUNT__",
        "__OBSERVATION_SECONDS__"
    )) {
        if ([regex]::Matches($remoteScript, [regex]::Escape($placeholder)).Count -ne 1) {
            throw "占位符数量异常：$placeholder"
        }
    }
    if (-not $remoteScript.StartsWith("#!/usr/bin/env bash`n") -or $remoteScript.Contains("`r") -or $remoteScript.Contains([char]0xFEFF)) {
        throw "远端脚本必须为无 BOM 的 LF UTF-8"
    }
    foreach ($pattern in @(
        '(?m)^\s*(rm|mv|cp|install|chmod|chown|truncate|touch|tee)\b',
        '\bsed\s+-i\b',
        '\bdocker\s+(run|create|restart|stop|kill|rm)\b',
        '\bsystemctl\s+(restart|stop|start|enable|disable|reload)\b',
        'SMS_ENABLED=true'
    )) {
        if ($remoteScript -match $pattern) {
            throw "只读契约发现禁止模式：$pattern"
        }
    }
    Write-Output "self_test=passed"
    Write-Output "remote_connections=0"
    Write-Output "business_configuration_mutations=0"
    Write-Output "access_audit_logs_may_increase=false"
    Write-Output "real_sms_delivery_not_verified=true"
    exit 0
}

# 固定 known_hosts 普通文件并核对唯一 ED25519 指纹，避免审计连接到错误目标。
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
    "__EXPECTED_WHITELIST_COUNT__" = $ExpectedWhitelistCount.ToString()
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
    "-o", "HostKeyAlgorithms=ssh-ed25519",
    "-o", "UserKnownHostsFile=$knownHostsPath",
    $destination,
    "printf '%s' '$encoded' | base64 -d | bash"
)

& ssh @sshArguments
if ($LASTEXITCODE -ne 0) {
    throw "阶段 5 测试服只读审计执行失败"
}
