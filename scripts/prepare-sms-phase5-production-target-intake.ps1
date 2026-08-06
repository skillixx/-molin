param(
    [string]$ChangeId = "",
    [string]$TargetAlias = "",
    [string]$ServerHost = "",
    [int]$SSHPort = 0,
    [string]$SSHUser = "",
    [string]$ExpectedEd25519Fingerprint = "",
    [string]$ProjectRoot = "",
    [string]$EnvironmentFile = "",
    [ValidateSet("host-binary", "systemd", "docker-compose")]
    [string]$ServiceKind = "host-binary",
    [string]$ApiServiceIdentifier = "",
    [int]$ApiLocalPort = 0,
    [int]$PrometheusLocalPort = 0,
    [int]$AlertmanagerLocalPort = 0,
    [string]$RollbackOperatorAlias = "",
    [string]$ObserverAlias = "",
    [string]$OutputDirectory = "",
    [switch]$ExportCandidate,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"

function Assert-LocalFileSystemPathInput {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Description
    )

    # 在解析路径前拒绝 UNC、Provider 路径和网络映射盘，保证候选生成始终离线。
    if ([string]::IsNullOrWhiteSpace($Path) -or $Path -match '^(?:\\\\|//)' -or $Path.Contains("::")) {
        throw "${Description}必须是本地文件系统绝对路径"
    }
    $isWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
    if ($isWindowsPlatform) {
        if ($Path -cnotmatch '^[A-Za-z]:[\\/]') { throw "Windows ${Description}必须使用本地盘符绝对路径" }
        $drive = Get-PSDrive -Name $Path.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
        if (([string]$drive.Root).StartsWith("\\") -or ([string]$drive.DisplayRoot).StartsWith("\\")) {
            throw "${Description}不得使用网络映射盘"
        }
    }
    elseif (-not [IO.Path]::IsPathRooted($Path)) {
        throw "${Description}必须使用本地绝对路径"
    }
}

function Assert-LinuxAbsolutePath {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Description
    )

    # 生产远端路径只允许规范化绝对路径，禁止控制字符、空段和父目录跳转。
    if ($Path -cnotmatch '^/[A-Za-z0-9._/-]+$' -or $Path.Contains("//")) {
        throw "${Description}必须是规范化 Linux 绝对路径"
    }
    foreach ($segment in @($Path.Split('/', [StringSplitOptions]::RemoveEmptyEntries))) {
        if ($segment -in @(".", "..")) { throw "${Description}不得包含相对路径段" }
    }
}

function Assert-ProductionTargetMetadata {
    param(
        [Parameter(Mandatory = $true)][string]$Alias,
        [Parameter(Mandatory = $true)][string]$HostName,
        [Parameter(Mandatory = $true)][int]$Port,
        [Parameter(Mandatory = $true)][string]$UserName,
        [Parameter(Mandatory = $true)][string]$Fingerprint,
        [Parameter(Mandatory = $true)][string]$RemoteProjectRoot,
        [Parameter(Mandatory = $true)][string]$RemoteEnvironmentFile,
        [Parameter(Mandatory = $true)][string]$RemoteServiceKind,
        [Parameter(Mandatory = $true)][string]$RemoteServiceIdentifier,
        [Parameter(Mandatory = $true)][int]$RemoteApiLocalPort,
        [Parameter(Mandatory = $true)][int]$RemotePrometheusLocalPort,
        [Parameter(Mandatory = $true)][int]$RemoteAlertmanagerLocalPort,
        [Parameter(Mandatory = $true)][string]$RollbackAlias,
        [Parameter(Mandatory = $true)][string]$ObservationAlias
    )

    if ($Alias -cnotmatch '^[a-z][a-z0-9-]{2,31}$') { throw "生产目标别名格式无效" }
    if ($HostName -match '^[0-9.]+$') {
        $parsedAddress = $null
        if (-not [Net.IPAddress]::TryParse($HostName, [ref]$parsedAddress) -or
            $parsedAddress.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork -or
            [Net.IPAddress]::IsLoopback($parsedAddress) -or
            $parsedAddress.Equals([Net.IPAddress]::Any)) {
            throw "生产 SSH IPv4 地址无效或属于本机地址"
        }
    }
    elseif ($HostName -cnotmatch '^(?=.{1,253}$)(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$' -or
        $HostName -ceq "localhost") {
        throw "生产 SSH 主机名格式无效或属于本机地址"
    }
    if ($Port -lt 1 -or $Port -gt 65535) { throw "生产 SSH 端口超出有效范围" }
    if ($UserName -cnotmatch '^[a-z_][a-z0-9_-]{0,31}$') { throw "生产 SSH 用户格式无效" }
    if ($Fingerprint -cnotmatch '^SHA256:[A-Za-z0-9+/]{43}$') { throw "必须提供完整 ED25519 SHA-256 指纹" }
    try { $fingerprintBytes = [Convert]::FromBase64String($Fingerprint.Substring(7) + "=") }
    catch { throw "ED25519 SHA-256 指纹不是有效 Base64" }
    if ($fingerprintBytes.Length -ne 32 -or @($fingerprintBytes | Where-Object { $_ -ne 0 }).Count -eq 0) {
        throw "ED25519 SHA-256 指纹长度无效或属于弱占位值"
    }
    if ($RollbackAlias -cnotmatch '^[a-z][a-z0-9-]{2,31}$' -or
        $ObservationAlias -cnotmatch '^[a-z][a-z0-9-]{2,31}$') {
        throw "回滚与观察操作者只能使用低敏别名"
    }
    Assert-LinuxAbsolutePath -Path $RemoteProjectRoot -Description "生产项目目录"
    Assert-LinuxAbsolutePath -Path $RemoteEnvironmentFile -Description "生产环境文件"
    $rootPrefix = $RemoteProjectRoot.TrimEnd('/') + "/"
    if (-not $RemoteEnvironmentFile.StartsWith($rootPrefix, [StringComparison]::Ordinal) -or
        [IO.Path]::GetFileName($RemoteEnvironmentFile) -cne ".env.prod") {
        throw "生产环境文件必须是项目目录内的 .env.prod"
    }
    if ($RemoteApiLocalPort -lt 1 -or $RemoteApiLocalPort -gt 65535) { throw "生产 API 本机端口超出有效范围" }
    foreach ($monitoringPort in @($RemotePrometheusLocalPort, $RemoteAlertmanagerLocalPort)) {
        if ($monitoringPort -lt 1 -or $monitoringPort -gt 65535) { throw "生产监控本机端口超出有效范围" }
    }
    if (@(@($RemoteApiLocalPort, $RemotePrometheusLocalPort, $RemoteAlertmanagerLocalPort) |
            Select-Object -Unique).Count -ne 3) {
        throw "生产 API、Prometheus 与 Alertmanager 本机端口必须互异"
    }
    switch ($RemoteServiceKind) {
        "host-binary" {
            Assert-LinuxAbsolutePath -Path $RemoteServiceIdentifier -Description "生产 API 二进制路径"
            if (-not $RemoteServiceIdentifier.StartsWith($rootPrefix, [StringComparison]::Ordinal)) {
                throw "生产 API 二进制必须位于项目目录内"
            }
        }
        "systemd" {
            if ($RemoteServiceIdentifier -cnotmatch '^[A-Za-z0-9_.@-]+\.service$') {
                throw "生产 systemd 单元名称格式无效"
            }
        }
        "docker-compose" {
            if ($RemoteServiceIdentifier -cnotmatch '^[A-Za-z0-9_.-]+$') {
                throw "生产 Compose 服务名称格式无效"
            }
        }
        default { throw "生产服务形态无效" }
    }
}

# 默认入口不接受目标信息、不写文件、不联网，避免把“生成器存在”误判为生产授权。
if (-not $ExportCandidate -and -not $SelfTest) {
    Write-Output "production_target_intake_authorized=false"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExportCandidate -and $SelfTest) { throw "ExportCandidate 与 SelfTest 必须互斥" }

if ($SelfTest) {
    $sha256 = [Security.Cryptography.SHA256]::Create()
    try {
        $syntheticFingerprint = "SHA256:" + [Convert]::ToBase64String(
            $sha256.ComputeHash([Text.Encoding]::UTF8.GetBytes("synthetic-production-host-key"))
        ).TrimEnd('=')
    }
    finally { $sha256.Dispose() }
    Assert-ProductionTargetMetadata `
        -Alias "prod-primary" `
        -HostName "prod.example.invalid" `
        -Port 2222 `
        -UserName "deploy" `
        -Fingerprint $syntheticFingerprint `
        -RemoteProjectRoot "/srv/molin" `
        -RemoteEnvironmentFile "/srv/molin/.env.prod" `
        -RemoteServiceKind "systemd" `
        -RemoteServiceIdentifier "molin-api.service" `
        -RemoteApiLocalPort 8080 `
        -RemotePrometheusLocalPort 19090 `
        -RemoteAlertmanagerLocalPort 19093 `
        -RollbackAlias "operator-a" `
        -ObservationAlias "observer-a"

    $loopbackRejected = $false
    try {
        Assert-ProductionTargetMetadata `
            -Alias "prod-primary" -HostName "127.0.0.1" -Port 22 -UserName "deploy" `
            -Fingerprint $syntheticFingerprint -RemoteProjectRoot "/srv/molin" `
            -RemoteEnvironmentFile "/srv/molin/.env.prod" -RemoteServiceKind "systemd" `
            -RemoteServiceIdentifier "molin-api.service" -RemoteApiLocalPort 8080 `
            -RemotePrometheusLocalPort 19090 -RemoteAlertmanagerLocalPort 19093 `
            -RollbackAlias "operator-a" -ObservationAlias "observer-a"
    }
    catch { $loopbackRejected = $true }
    if (-not $loopbackRejected) { throw "生产回环目标反例未被阻断" }

    $pathEscapeRejected = $false
    try {
        Assert-ProductionTargetMetadata `
            -Alias "prod-primary" -HostName "prod.example.invalid" -Port 22 -UserName "deploy" `
            -Fingerprint $syntheticFingerprint -RemoteProjectRoot "/srv/molin" `
            -RemoteEnvironmentFile "/srv/molin/../.env.prod" -RemoteServiceKind "systemd" `
            -RemoteServiceIdentifier "molin-api.service" -RemoteApiLocalPort 8080 `
            -RemotePrometheusLocalPort 19090 -RemoteAlertmanagerLocalPort 19093 `
            -RollbackAlias "operator-a" -ObservationAlias "observer-a"
    }
    catch { $pathEscapeRejected = $true }
    if (-not $pathEscapeRejected) { throw "生产路径跳转反例未被阻断" }

    $duplicatePortRejected = $false
    try {
        Assert-ProductionTargetMetadata `
            -Alias "prod-primary" -HostName "prod.example.invalid" -Port 22 -UserName "deploy" `
            -Fingerprint $syntheticFingerprint -RemoteProjectRoot "/srv/molin" `
            -RemoteEnvironmentFile "/srv/molin/.env.prod" -RemoteServiceKind "systemd" `
            -RemoteServiceIdentifier "molin-api.service" -RemoteApiLocalPort 8080 `
            -RemotePrometheusLocalPort 8080 -RemoteAlertmanagerLocalPort 19093 `
            -RollbackAlias "operator-a" -ObservationAlias "observer-a"
    }
    catch { $duplicatePortRejected = $true }
    if (-not $duplicatePortRejected) { throw "生产本机端口重复反例未被阻断" }

    Write-Output "production_target_intake_self_test=passed"
    Write-Output "fixed_identity_required=true"
    Write-Output "loopback_target_rejected=true"
    Write-Output "path_escape_rejected=true"
    Write-Output "duplicate_local_ports_rejected=true"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ChangeId -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') { throw "生产目标 ChangeId 必须使用 UTC 基本格式" }
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) { throw "必须提供全新的候选输出目录" }
Assert-LocalFileSystemPathInput -Path $OutputDirectory -Description "候选输出目录"
Assert-ProductionTargetMetadata `
    -Alias $TargetAlias `
    -HostName $ServerHost `
    -Port $SSHPort `
    -UserName $SSHUser `
    -Fingerprint $ExpectedEd25519Fingerprint `
    -RemoteProjectRoot $ProjectRoot `
    -RemoteEnvironmentFile $EnvironmentFile `
    -RemoteServiceKind $ServiceKind `
    -RemoteServiceIdentifier $ApiServiceIdentifier `
    -RemoteApiLocalPort $ApiLocalPort `
    -RemotePrometheusLocalPort $PrometheusLocalPort `
    -RemoteAlertmanagerLocalPort $AlertmanagerLocalPort `
    -RollbackAlias $RollbackOperatorAlias `
    -ObservationAlias $ObserverAlias

$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
$outputParent = Split-Path -Parent $outputPath
if ([string]::IsNullOrWhiteSpace($outputParent) -or -not (Test-Path -LiteralPath $outputParent -PathType Container)) {
    throw "候选输出目录的父目录必须已存在"
}
if (Test-Path -LiteralPath $outputPath) { throw "候选输出目录已存在，禁止覆盖" }

$candidatePath = Join-Path $outputPath "sms-phase5-production-target-$ChangeId.json"
$directoryCreated = $false
$fileCreated = $false
try {
    # 候选只冻结非密钥运行元数据和后续门禁，不包含密码、私钥、Token、手机号或环境值。
    $candidate = [ordered]@{
        schema_version = 1
        change_id = $ChangeId
        environment = "production"
        target_alias = $TargetAlias
        server_host = $ServerHost
        ssh_port = $SSHPort
        ssh_user = $SSHUser
        expected_ed25519_fingerprint = $ExpectedEd25519Fingerprint
        project_root = $ProjectRoot
        environment_file = $EnvironmentFile
        service_kind = $ServiceKind
        api_service_identifier = $ApiServiceIdentifier
        api_local_port = $ApiLocalPort
        prometheus_local_port = $PrometheusLocalPort
        alertmanager_local_port = $AlertmanagerLocalPort
        rollback_operator_alias = $RollbackOperatorAlias
        observer_alias = $ObserverAlias
        expected_sms_enabled = $false
        expected_sms_test_mode = $true
        readonly_baseline_requires_separate_approval = $true
        deployment_requires_separate_approval = $true
        canary_requires_separate_approval = $true
        production_enable_requires_separate_approval = $true
        automatic_retries = 0
        business_posts = 0
        real_sms_sent = 0
    }
    $null = New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop
    $directoryCreated = $true
    [IO.File]::WriteAllText(
        $candidatePath,
        ($candidate | ConvertTo-Json -Depth 4) + "`n",
        (New-Object Text.UTF8Encoding($false))
    )
    $fileCreated = $true

    # 重新读取候选以确认 JSON 完整且关闭态、零发送和独立授权边界没有在序列化时漂移。
    $verified = Get-Content -LiteralPath $candidatePath -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($verified.change_id -cne $ChangeId -or $verified.environment -cne "production" -or
        $verified.expected_sms_enabled -ne $false -or $verified.expected_sms_test_mode -ne $true -or
        $verified.automatic_retries -ne 0 -or $verified.real_sms_sent -ne 0) {
        throw "生产目标候选静态复核失败"
    }

    $candidateSHA256 = (Get-FileHash -LiteralPath $candidatePath -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Output "production_target_intake_candidate=passed"
    Write-Output "change_id=$ChangeId"
    Write-Output "target_alias=$TargetAlias"
    Write-Output "candidate_sha256=$candidateSHA256"
    Write-Output "candidate_path=$candidatePath"
    Write-Output "fixed_identity_required=true"
    Write-Output "readonly_baseline_authorized=false"
    Write-Output "deployment_authorized=false"
    Write-Output "canary_authorized=false"
    Write-Output "production_enable_authorized=false"
    Write-Output "candidate_files_written=1"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
}
catch {
    # 失败时仅删除本次精确文件和已确认空的目录，禁止递归清理其他候选。
    if ($fileCreated -and (Test-Path -LiteralPath $candidatePath -PathType Leaf)) {
        Remove-Item -LiteralPath $candidatePath -Force
    }
    if ($directoryCreated -and (Test-Path -LiteralPath $outputPath -PathType Container) -and
        @(Get-ChildItem -LiteralPath $outputPath -Force).Count -eq 0) {
        Remove-Item -LiteralPath $outputPath -Force
    }
    throw
}
