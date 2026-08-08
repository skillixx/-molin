param(
    [string]$CandidateCIDR = "172.20.250.0/28",
    [string[]]$ExistingCIDRs = @(
        "100.93.0.0/16",
        "172.17.0.0/16",
        "172.18.0.0/16",
        "172.19.0.0/16",
        "172.31.250.0/24",
        "192.168.29.0/24"
    ),
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"

function ConvertTo-IPv4Number {
    param([string]$Address)
    $parsed = $null
    if (-not [System.Net.IPAddress]::TryParse($Address, [ref]$parsed) -or $parsed.AddressFamily -ne [System.Net.Sockets.AddressFamily]::InterNetwork) {
        throw "只允许规范 IPv4 地址"
    }
    $bytes = $parsed.GetAddressBytes()
    return ([uint64]$bytes[0] -shl 24) -bor ([uint64]$bytes[1] -shl 16) -bor ([uint64]$bytes[2] -shl 8) -bor [uint64]$bytes[3]
}

function ConvertFrom-IPv4Number {
    param([uint64]$Value)
    return "{0}.{1}.{2}.{3}" -f (($Value -shr 24) -band 255), (($Value -shr 16) -band 255), (($Value -shr 8) -band 255), ($Value -band 255)
}

function Get-CIDRRange {
    param([string]$CIDR)
    $parts = $CIDR.Split("/", 2)
    if ($parts.Count -ne 2) {
        throw "CIDR 格式无效"
    }
    $prefix = 0
    if (-not [int]::TryParse($parts[1], [ref]$prefix) -or $prefix -lt 0 -or $prefix -gt 32) {
        throw "CIDR 前缀无效"
    }
    $address = ConvertTo-IPv4Number -Address $parts[0]
    $hostCount = [uint64][math]::Pow(2, (32 - $prefix))
    $mask = if ($prefix -eq 0) { [uint64]0 } else { [uint64]4294967295 - ($hostCount - 1) }
    $network = $address -band $mask
    $broadcast = $network -bor ([uint64]4294967295 - $mask)
    if ($address -ne $network) {
        throw "CIDR 必须填写规范网络地址"
    }
    return [pscustomobject]@{ CIDR = $CIDR; Prefix = $prefix; Network = $network; Broadcast = $broadcast }
}

function Test-RangeOverlap {
    param($Left, $Right)
    return $Left.Network -le $Right.Broadcast -and $Right.Network -le $Left.Broadcast
}

function Assert-PrivateProxyRange {
    param($Range)
    # 代理专用网段固定使用 /28，既容纳两套 Nginx，又避免信任范围无边界扩大。
    if ($Range.Prefix -ne 28) {
        throw "代理专用网段必须精确为 /28"
    }
    $private10 = Get-CIDRRange -CIDR "10.0.0.0/8"
    $private172 = Get-CIDRRange -CIDR "172.16.0.0/12"
    $private192 = Get-CIDRRange -CIDR "192.168.0.0/16"
    $inside10 = Test-RangeOverlap -Left $Range -Right $private10
    $inside172 = Test-RangeOverlap -Left $Range -Right $private172
    $inside192 = Test-RangeOverlap -Left $Range -Right $private192
    $insidePrivate = $inside10 -or $inside172 -or $inside192
    if ($insidePrivate -eq $false) {
        throw "代理专用网段必须位于 RFC1918 私网范围"
    }
}

function Test-Plan {
    param([string]$Candidate, [string[]]$Existing)
    $candidateRange = Get-CIDRRange -CIDR $Candidate
    Assert-PrivateProxyRange -Range $candidateRange
    foreach ($existingCIDR in $Existing) {
        $existingRange = Get-CIDRRange -CIDR $existingCIDR
        if (Test-RangeOverlap -Left $candidateRange -Right $existingRange) {
            throw "候选网段与现有路由或 Docker 网络重叠"
        }
    }
    return $candidateRange
}

if ($SelfTest) {
    $safe = Test-Plan -Candidate "172.20.250.0/28" -Existing @("172.17.0.0/16", "172.31.250.0/24", "192.168.29.0/24")
    $overlapRejected = $false
    try {
        Test-Plan -Candidate "172.17.10.0/28" -Existing @("172.17.0.0/16") | Out-Null
    }
    catch {
        $overlapRejected = $true
    }
    if (-not $overlapRejected -or $safe.CIDR -ne "172.20.250.0/28") {
        throw "代理网络规划自测失败"
    }
    Write-Output "proxy_network_self_test=passed"
}

$plan = Test-Plan -Candidate $CandidateCIDR -Existing $ExistingCIDRs
$adminIP = ConvertFrom-IPv4Number -Value ($plan.Network + 2)
$userIP = ConvertFrom-IPv4Number -Value ($plan.Network + 3)

Write-Output "proxy_network_plan=passed"
Write-Output "candidate_cidr=$CandidateCIDR"
Write-Output "admin_proxy_ip=$adminIP"
Write-Output "user_proxy_ip=$userIP"
Write-Output "trusted_proxy_ips_target=$CandidateCIDR"
Write-Output "existing_cidr_count=$($ExistingCIDRs.Count)"
Write-Output "must_recheck_remote_routes_before_apply=true"
Write-Output "remote_connections=0"
Write-Output "network_writes=0"
Write-Output "configuration_writes=0"
Write-Output "service_restarts=0"
Write-Output "real_sms_sent=0"
