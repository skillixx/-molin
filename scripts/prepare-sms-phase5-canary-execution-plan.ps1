param(
    [string]$ChangeId = "",
    [ValidateSet("receipt_only")]
    [string]$AcceptanceScope = "",
    [string]$OutputDirectory = "",
    [switch]$Generate,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"

function Assert-LocalOutputDirectoryInput {
    param([Parameter(Mandatory = $true)][string]$Path)

    # 必须在 GetFullPath、Test-Path 等文件系统调用前拒绝 UNC 和 PowerShell Provider 路径，避免隐式 SMB 访问。
    if ([string]::IsNullOrWhiteSpace($Path) -or $Path -match '^(?:\\\\|//)' -or $Path.Contains("::")) {
        throw "候选输出目录必须是本地文件系统绝对路径"
    }
    $isWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
    if ($isWindowsPlatform) {
        if ($Path -cnotmatch '^[A-Za-z]:[\\/]') {
            throw "Windows 候选输出目录必须使用本地盘符绝对路径"
        }
        $drive = Get-PSDrive -Name $Path.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
        if (([string]$drive.Root).StartsWith("\\") -or ([string]$drive.DisplayRoot).StartsWith("\\")) {
            throw "候选输出目录不得使用网络映射盘"
        }
    } elseif (-not [IO.Path]::IsPathRooted($Path)) {
        throw "候选输出目录必须使用本地绝对路径"
    }
}

# 默认入口必须保持完全关闭，只有显式 Generate 才能创建本地脱敏候选。
if (-not $Generate -and -not $SelfTest) {
    Write-Output "canary_plan_candidate_authorized=false"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($Generate -and $SelfTest) {
    throw "Generate 与 SelfTest 必须互斥"
}

if ($SelfTest) {
    $selfTestChangeId = "20990103T010203Z"
    $selfTestDirectory = Join-Path ([IO.Path]::GetTempPath()) ("molin-sms-phase5-canary-plan-" + [Guid]::NewGuid().ToString("N"))
    $selfTestCandidate = Join-Path $selfTestDirectory "sms-phase5-canary-plan-$selfTestChangeId.json"
    $uncRejected = $false
    try {
        Assert-LocalOutputDirectoryInput -Path "\\phase5-invalid.example.invalid\candidate"
    } catch {
        $uncRejected = $true
    }
    if (-not $uncRejected) {
        throw "Canary 脱敏计划候选 SelfTest 未拒绝 UNC 输出目录"
    }
    try {
        $selfTestOutput = @(& $PSCommandPath `
            -Generate `
            -ChangeId $selfTestChangeId `
            -AcceptanceScope receipt_only `
            -OutputDirectory $selfTestDirectory)
        foreach ($marker in @(
            "canary_plan_candidate=passed",
            "canary_execution_plan=passed",
            "target_alias_count=2",
            "candidate_files_written=1",
            "network_connections=0",
            "uploads=0",
            "real_sms_sent=0"
        )) {
            if ($selfTestOutput -cnotcontains $marker) {
                throw "Canary 脱敏计划候选 SelfTest 缺少标记：$marker"
            }
        }
        if (-not (Test-Path -LiteralPath $selfTestCandidate -PathType Leaf) -or
            @(Get-ChildItem -LiteralPath $selfTestDirectory -Force).Count -ne 1) {
            throw "Canary 脱敏计划候选 SelfTest 文件集合不符合预期"
        }
    } finally {
        # SelfTest 只删除自身创建的精确候选和随后确认为空的临时目录。
        if (Test-Path -LiteralPath $selfTestCandidate -PathType Leaf) {
            Remove-Item -LiteralPath $selfTestCandidate -Force
        }
        if ((Test-Path -LiteralPath $selfTestDirectory -PathType Container) -and
            @(Get-ChildItem -LiteralPath $selfTestDirectory -Force).Count -eq 0) {
            Remove-Item -LiteralPath $selfTestDirectory -Force
        }
    }
    if (Test-Path -LiteralPath $selfTestDirectory) {
        throw "Canary 脱敏计划候选 SelfTest 留下了持久文件"
    }
    Write-Output "canary_plan_candidate_self_test=passed"
    Write-Output "unc_output_path_rejected=true"
    Write-Output "candidate_files_remaining=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ChangeId -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') {
    throw "Canary ChangeId 必须使用 UTC 基本格式"
}
if ($AcceptanceScope -cne "receipt_only") {
    throw "必须显式指定 AcceptanceScope=receipt_only"
}
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    throw "生成候选必须提供全新的 OutputDirectory"
}

Assert-LocalOutputDirectoryInput -Path $OutputDirectory
$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
$outputParent = Split-Path -Parent $outputPath
if ([string]::IsNullOrWhiteSpace($outputParent) -or -not (Test-Path -LiteralPath $outputParent -PathType Container)) {
    throw "候选输出目录的父目录必须已存在"
}
if (Test-Path -LiteralPath $outputPath) {
    throw "候选输出目录已存在，禁止覆盖"
}

$candidatePath = Join-Path $outputPath "sms-phase5-canary-plan-$ChangeId.json"
$directoryCreated = $false
$fileCreated = $false
try {
    # receipt-only 候选只保存两个低敏别名；未注册别名同时服务注册和换绑发码。
    $plan = [ordered]@{
        change_id = $ChangeId
        environment = "test"
        sms_test_mode = $true
        restore_sms_enabled = "false"
        no_retries = $true
        requested_sends = 5
        max_sends = 5
        acceptance_scope = $AcceptanceScope
        business_state_changes = $false
        business_state_rollback_approved = $false
        disposable_accounts = $false
        scenes = @(
            [ordered]@{ scene = "register"; target_alias = "target-new"; target_state = "unregistered" },
            [ordered]@{ scene = "login"; target_alias = "target-admin"; target_state = "registered" },
            [ordered]@{ scene = "reset_password"; target_alias = "target-admin"; target_state = "registered" },
            [ordered]@{ scene = "bind_phone"; target_alias = "target-new"; target_state = "unregistered" },
            [ordered]@{ scene = "admin_verify"; target_alias = "target-admin"; target_state = "registered_admin" }
        )
    }

    $null = New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop
    $directoryCreated = $true
    $json = $plan | ConvertTo-Json -Depth 8
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [IO.File]::WriteAllText($candidatePath, $json + [Environment]::NewLine, $utf8NoBom)
    $fileCreated = $true

    # 生成后必须通过既有公共校验入口，不能只信任生成器自身。
    & (Join-Path $PSScriptRoot "verify-sms-phase5-canary-execution-plan.ps1") -PlanFile $candidatePath

    $sha256 = (Get-FileHash -LiteralPath $candidatePath -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Output "canary_plan_candidate=passed"
    Write-Output "change_id=$ChangeId"
    Write-Output "acceptance_scope=receipt_only"
    Write-Output "target_alias_count=2"
    Write-Output "candidate_sha256=$sha256"
    Write-Output "candidate_path=$candidatePath"
    Write-Output "candidate_files_written=1"
    Write-Output "sensitive_values_persisted=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "real_sms_sent=0"
} catch {
    # 失败时只清理由本次调用创建的精确文件和空目录，不执行递归删除。
    if ($fileCreated -and (Test-Path -LiteralPath $candidatePath -PathType Leaf)) {
        Remove-Item -LiteralPath $candidatePath -Force
    }
    if ($directoryCreated -and (Test-Path -LiteralPath $outputPath -PathType Container) -and
        @(Get-ChildItem -LiteralPath $outputPath -Force).Count -eq 0) {
        Remove-Item -LiteralPath $outputPath -Force
    }
    throw
}
