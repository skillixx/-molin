param(
    [string]$ChangeId = "",
    [string]$PlanFile = "",
    [string]$ExpectedPlanSHA256 = "",
    [string]$OutputDirectory = "",
    [switch]$ExportCandidate,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"

function Assert-TargetPair {
    param(
        [Parameter(Mandatory = $true)][string]$NewTarget,
        [Parameter(Mandatory = $true)][string]$AdminTarget
    )

    # 本地候选只验证中国大陆手机号格式与两个目标互异，不推断注册、管理员或白名单状态。
    if ($NewTarget -cnotmatch '^1[3-9]\d{9}$' -or $AdminTarget -cnotmatch '^1[3-9]\d{9}$') {
        throw "目标号码格式无效"
    }
    if ($NewTarget -ceq $AdminTarget) {
        throw "未注册目标与管理员目标必须使用不同号码"
    }
}

function Assert-LocalFileSystemPathInput {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Description
    )

    # 在任何文件系统解析前拒绝 UNC、Provider 路径和网络映射盘，避免静态生成意外联网。
    if ([string]::IsNullOrWhiteSpace($Path) -or $Path -match '^(?:\\\\|//)' -or $Path.Contains("::")) {
        throw "${Description}必须是本地文件系统绝对路径"
    }
    $isWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
    if ($isWindowsPlatform) {
        if ($Path -cnotmatch '^[A-Za-z]:[\\/]') {
            throw "Windows ${Description}必须使用本地盘符绝对路径"
        }
        $drive = Get-PSDrive -Name $Path.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
        if (([string]$drive.Root).StartsWith("\\") -or ([string]$drive.DisplayRoot).StartsWith("\\")) {
            throw "${Description}不得使用网络映射盘"
        }
    } elseif (-not [IO.Path]::IsPathRooted($Path)) {
        throw "${Description}必须使用本地绝对路径"
    }
}

# 默认入口保持关闭，避免误触发交互输入或任何外部副作用。
if (-not $ExportCandidate -and -not $SelfTest) {
    Write-Output "target_preflight_candidate_authorized=false"
    Write-Output "interactive_prompts=0"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExportCandidate -and $SelfTest) {
    throw "ExportCandidate 与 SelfTest 必须互斥"
}

if ($SelfTest) {
    # 合成号码仅在内存中由片段构造，既不提示输入，也不写入文件或输出原值。
    $syntheticNew = "1" + "38" + ("0" * 8)
    $syntheticAdmin = "1" + "39" + ("0" * 8)
    Assert-TargetPair -NewTarget $syntheticNew -AdminTarget $syntheticAdmin

    $duplicateRejected = $false
    try { Assert-TargetPair -NewTarget $syntheticNew -AdminTarget $syntheticNew } catch { $duplicateRejected = $true }
    if (-not $duplicateRejected) { throw "相同号码反例未被阻断" }

    $invalidFormatRejected = $false
    try { Assert-TargetPair -NewTarget ("2" + "38" + ("0" * 8)) -AdminTarget $syntheticAdmin } catch { $invalidFormatRejected = $true }
    if (-not $invalidFormatRejected) { throw "无效格式反例未被阻断" }

    $syntheticNew = $null
    $syntheticAdmin = $null
    Write-Output "target_preflight_candidate_self_test=passed"
    Write-Output "valid_distinct_pair_accepted=true"
    Write-Output "duplicate_pair_rejected=true"
    Write-Output "invalid_format_rejected=true"
    Write-Output "interactive_prompts=0"
    Write-Output "sensitive_values_persisted=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ChangeId -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') { throw "目标预检 ChangeId 必须使用 UTC 基本格式" }
if ($ExpectedPlanSHA256 -cnotmatch '^[0-9a-f]{64}$') { throw "必须提供小写 SHA-256 计划摘要" }
if ([string]::IsNullOrWhiteSpace($PlanFile) -or [string]::IsNullOrWhiteSpace($OutputDirectory)) {
    throw "导出候选必须提供 PlanFile 与全新的 OutputDirectory"
}

Assert-LocalFileSystemPathInput -Path $OutputDirectory -Description "候选输出目录"
Assert-LocalFileSystemPathInput -Path $PlanFile -Description "Canary 计划文件"
$resolvedPlan = (Resolve-Path -LiteralPath $PlanFile -ErrorAction Stop).Path
$actualPlanSHA256 = (Get-FileHash -LiteralPath $resolvedPlan -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualPlanSHA256 -cne $ExpectedPlanSHA256) { throw "Canary 计划摘要不匹配，禁止生成目标预检候选" }

# 复用公共计划校验器，确保候选只绑定测试服 receipt-only 双别名计划。
$planOutput = @(& (Join-Path $PSScriptRoot "verify-sms-phase5-canary-execution-plan.ps1") -PlanFile $resolvedPlan)
if ($planOutput -cnotcontains "canary_execution_plan=passed" -or
    $planOutput -cnotcontains "change_id=$ChangeId" -or
    $planOutput -cnotcontains "acceptance_scope=receipt_only") {
    throw "Canary 计划未通过 ChangeId 或 receipt-only 绑定校验"
}

$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
$outputParent = Split-Path -Parent $outputPath
if ([string]::IsNullOrWhiteSpace($outputParent) -or -not (Test-Path -LiteralPath $outputParent -PathType Container)) {
    throw "候选输出目录的父目录必须已存在"
}
if (Test-Path -LiteralPath $outputPath) { throw "候选输出目录已存在，禁止覆盖" }

$runnerPath = Join-Path $outputPath "run-sms-phase5-canary-target-preflight-$ChangeId.ps1"
$directoryCreated = $false
$fileCreated = $false
try {
    $runnerTemplate = @'
param(
    [switch]$Interactive,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
$ChangeId = "__CHANGE_ID__"
$PlanSHA256 = "__PLAN_SHA256__"

function Assert-TargetPair {
    param(
        [Parameter(Mandatory = $true)][string]$NewTarget,
        [Parameter(Mandatory = $true)][string]$AdminTarget
    )

    # 这里只验证格式与互异性；注册状态、管理员身份和白名单必须由后续独立只读预检确认。
    if ($NewTarget -cnotmatch '^1[3-9]\d{9}$' -or $AdminTarget -cnotmatch '^1[3-9]\d{9}$') {
        throw "目标号码格式无效"
    }
    if ($NewTarget -ceq $AdminTarget) {
        throw "target-new 与 target-admin 必须使用不同号码"
    }
}

function Read-HiddenTarget {
    param([Parameter(Mandatory = $true)][string]$Prompt)

    $secureValue = Read-Host -Prompt $Prompt -AsSecureString
    $pointer = [IntPtr]::Zero
    try {
        $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureValue)
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
    } finally {
        if ($pointer -ne [IntPtr]::Zero) {
            [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
        }
        if ($null -ne $secureValue) {
            $secureValue.Dispose()
        }
    }
}

if (-not $Interactive -and -not $SelfTest) {
    Write-Output "target_preflight_change_id=$ChangeId"
    Write-Output "plan_sha256=$PlanSHA256"
    Write-Output "interactive_authorized=false"
    Write-Output "interactive_prompts=0"
    Write-Output "sensitive_values_persisted=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($Interactive -and $SelfTest) { throw "Interactive 与 SelfTest 必须互斥" }
if ($SelfTest) {
    $syntheticNew = "1" + "38" + ("0" * 8)
    $syntheticAdmin = "1" + "39" + ("0" * 8)
    Assert-TargetPair -NewTarget $syntheticNew -AdminTarget $syntheticAdmin
    $syntheticNew = $null
    $syntheticAdmin = $null
    Write-Output "target_preflight_runner_self_test=passed"
    Write-Output "interactive_prompts=0"
    Write-Output "sensitive_values_persisted=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

$newTarget = $null
$adminTarget = $null
try {
    $newTarget = Read-HiddenTarget -Prompt "请输入 target-new（隐藏输入）"
    $adminTarget = Read-HiddenTarget -Prompt "请输入 target-admin（隐藏输入）"
    Assert-TargetPair -NewTarget $newTarget -AdminTarget $adminTarget
    Write-Output "target_preflight=passed"
    Write-Output "target_aliases=target-new,target-admin"
    Write-Output "format_verified=true"
    Write-Output "distinct_targets_verified=true"
    Write-Output "registration_state_verified=false"
    Write-Output "admin_identity_verified=false"
    Write-Output "whitelist_verified=false"
    Write-Output "interactive_prompts=2"
    Write-Output "sensitive_values_persisted=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "real_sms_sent=0"
} finally {
    # 托管字符串无法保证物理清零，因此只缩短引用生命周期；脚本从不输出、保存或传输其内容。
    $newTarget = $null
    $adminTarget = $null
}
'@
    $runner = $runnerTemplate.Replace("__CHANGE_ID__", $ChangeId).Replace("__PLAN_SHA256__", $ExpectedPlanSHA256)
    $null = New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop
    $directoryCreated = $true
    [IO.File]::WriteAllText($runnerPath, $runner, (New-Object Text.UTF8Encoding($true)))
    $fileCreated = $true

    # 只做语法、默认关闭和合成值自测；绝不进入 Interactive 分支。
    $tokens = $null
    $parseErrors = $null
    $null = [Management.Automation.Language.Parser]::ParseFile($runnerPath, [ref]$tokens, [ref]$parseErrors)
    if (@($parseErrors).Count -ne 0) { throw "目标预检 runner PowerShell 语法校验失败" }
    $closedOutput = @(& $runnerPath)
    $selfTestOutput = @(& $runnerPath -SelfTest)
    if ($closedOutput -cnotcontains "interactive_authorized=false" -or
        $selfTestOutput -cnotcontains "target_preflight_runner_self_test=passed") {
        throw "目标预检 runner 默认关闭或合成值自测失败"
    }

    $runnerSHA256 = (Get-FileHash -LiteralPath $runnerPath -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Output "target_preflight_candidate=passed"
    Write-Output "change_id=$ChangeId"
    Write-Output "plan_sha256=$ExpectedPlanSHA256"
    Write-Output "runner_sha256=$runnerSHA256"
    Write-Output "runner_path=$runnerPath"
    Write-Output "candidate_files_written=1"
    Write-Output "interactive_prompts=0"
    Write-Output "sensitive_values_persisted=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "real_sms_sent=0"
} catch {
    # 失败时只删除本次创建的精确文件及确认为空的目录，禁止递归清理。
    if ($fileCreated -and (Test-Path -LiteralPath $runnerPath -PathType Leaf)) {
        Remove-Item -LiteralPath $runnerPath -Force
    }
    if ($directoryCreated -and (Test-Path -LiteralPath $outputPath -PathType Container) -and
        @(Get-ChildItem -LiteralPath $outputPath -Force).Count -eq 0) {
        Remove-Item -LiteralPath $outputPath -Force
    }
    throw
}
