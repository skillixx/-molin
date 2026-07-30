param(
    # 仅供自动化验证“取消不写入”；正常交互使用时不要传此参数。
    [string]$Confirmation = "",
    # 可选择同一 Git 仓库已注册 worktree 下的精确 infra/.env.test；默认使用当前 worktree。
    [string]$EnvironmentFile = ""
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

# 只允许修改同一 Git 仓库已注册 worktree 中被忽略的 infra/.env.test，禁止任意外部路径。
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$worktreeLines = @(& git -C $repoRoot worktree list --porcelain)
if ($LASTEXITCODE -ne 0) {
    throw "安全检查失败：无法读取当前仓库的 worktree 注册信息。"
}
$registeredRoots = @(
    foreach ($line in $worktreeLines) {
        if ($line.StartsWith("worktree ", [StringComparison]::Ordinal)) {
            $listedRoot = $line.Substring("worktree ".Length)
            # 只接受当前操作系统可解析且实际存在的注册 worktree，跳过 prunable/异机残留记录。
            if (-not [string]::IsNullOrWhiteSpace($listedRoot) -and
                (Test-Path -LiteralPath $listedRoot -PathType Container)) {
                [IO.Path]::GetFullPath((Resolve-Path -LiteralPath $listedRoot).Path)
            }
        }
    }
)
if ($registeredRoots.Count -eq 0) {
    throw "安全检查失败：当前仓库没有可验证的已注册 worktree。"
}

if ([string]::IsNullOrWhiteSpace($EnvironmentFile)) {
    $requestedFile = [IO.Path]::GetFullPath((Join-Path $repoRoot "infra/.env.test"))
}
else {
    if (-not [IO.Path]::IsPathRooted($EnvironmentFile)) {
        throw "安全检查失败：EnvironmentFile 必须是绝对路径。"
    }
    $requestedFile = [IO.Path]::GetFullPath($EnvironmentFile)
}

$targetWorktreeRoot = $null
foreach ($registeredRoot in $registeredRoots) {
    $expectedFile = [IO.Path]::GetFullPath((Join-Path $registeredRoot "infra/.env.test"))
    if ([StringComparer]::OrdinalIgnoreCase.Equals($requestedFile, $expectedFile)) {
        $targetWorktreeRoot = $registeredRoot
        break
    }
}
if ($null -eq $targetWorktreeRoot) {
    throw "安全检查失败：目标不是同一仓库已注册 worktree 下的精确 infra/.env.test。"
}

$infraDir = [IO.Path]::GetFullPath((Join-Path $targetWorktreeRoot "infra"))
$targetFile = [IO.Path]::GetFullPath((Join-Path $infraDir ".env.test"))

if (-not [StringComparer]::OrdinalIgnoreCase.Equals([IO.Path]::GetDirectoryName($targetFile), $infraDir) -or
    [IO.Path]::GetFileName($targetFile) -cne ".env.test" -or
    -not [StringComparer]::OrdinalIgnoreCase.Equals($targetFile, $requestedFile)) {
    throw "安全检查失败：目标文件不符合固定规则。"
}
function Assert-GitIgnored {
    param([Parameter(Mandatory = $true)][string]$RepoRelativePath)

    # git check-ignore 只检查路径规则，不读取或输出环境变量内容。
    & git -C $targetWorktreeRoot check-ignore --quiet -- $RepoRelativePath
    if ($LASTEXITCODE -ne 0) {
        throw "安全检查失败：目标、备份或临时文件未被 Git 忽略，拒绝继续。"
    }
}

Assert-GitIgnored "infra/.env.test"

function Read-SecureText {
    param([Parameter(Mandatory = $true)][string]$Prompt)

    $secureValue = Read-Host -Prompt $Prompt -AsSecureString
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureValue)
    try {
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
    }
    finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
    }
}

function New-RandomSecret {
    $bytes = New-Object byte[] 32
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($bytes)
        return [Convert]::ToBase64String($bytes)
    }
    finally {
        $generator.Dispose()
        [Array]::Clear($bytes, 0, $bytes.Length)
    }
}

function Assert-SingleLineValue {
    param(
        [Parameter(Mandatory = $true)][string]$KeyName,
        [Parameter(Mandatory = $true)][string]$Value
    )

    if ([string]::IsNullOrWhiteSpace($Value) -or $Value.Contains("`r") -or
        $Value.Contains("`n") -or $Value.Contains([char]0)) {
        throw "$KeyName 不能为空，也不能包含换行或空字符。"
    }
}

function ConvertTo-EnvValue {
    param([Parameter(Mandatory = $true)][string]$Value)

    # 使用双引号保护 #、等号及非 ASCII 字符；只转义反斜杠和双引号本身。
    return '"' + $Value.Replace('\', '\\').Replace('"', '\"') + '"'
}

Write-Host "本向导只更新被 Git 忽略的测试环境配置，并会轮换邮件 HMAC 与内部指标 Token。"
if ([string]::IsNullOrEmpty($Confirmation)) {
    $Confirmation = Read-Host "输入 CONFIGURE 确认继续"
}
if ($Confirmation -cne "CONFIGURE") {
    Write-Host "用户取消，未修改任何文件。"
    exit 0
}
if (-not (Test-Path -LiteralPath $targetFile -PathType Leaf)) {
    throw "未找到测试环境配置文件。请先从模板创建，并确认其未入库。"
}

$timestamp = Get-Date -Format "yyyyMMdd-HHmmssfff"
$backupName = ".env.test.backup.$timestamp"
$temporaryName = ".env.test.tmp.$([Guid]::NewGuid().ToString('N'))"
$backupFile = [IO.Path]::GetFullPath((Join-Path $infraDir $backupName))
$temporaryFile = [IO.Path]::GetFullPath((Join-Path $infraDir $temporaryName))

# 读取凭据前逐一确认备份和临时路径仍位于目标 infra 且被该 worktree 忽略。
foreach ($candidate in @($backupFile, $temporaryFile)) {
    if (-not [StringComparer]::OrdinalIgnoreCase.Equals([IO.Path]::GetDirectoryName($candidate), $infraDir)) {
        throw "安全检查失败：备份或临时文件越出 infra 目录。"
    }
}
Assert-GitIgnored "infra/$backupName"
Assert-GitIgnored "infra/$temporaryName"

$accessKeyID = Read-SecureText "DirectMail AccessKey ID（输入不回显）"
$accessKeySecret = Read-SecureText "DirectMail AccessKey Secret（输入不回显）"
$accountName = Read-SecureText "DirectMail 已验证发信地址（输入不回显）"
$region = Read-Host "DirectMail Region（直接回车使用 cn-hangzhou）"
if ([string]::IsNullOrWhiteSpace($region)) { $region = "cn-hangzhou" }
$fromAlias = Read-Host "发件人别名（直接回车使用 墨灵）"
if ([string]::IsNullOrWhiteSpace($fromAlias)) { $fromAlias = "墨灵" }

Assert-SingleLineValue "DIRECTMAIL_ACCESS_KEY_ID" $accessKeyID
Assert-SingleLineValue "DIRECTMAIL_ACCESS_KEY_SECRET" $accessKeySecret
Assert-SingleLineValue "DIRECTMAIL_ACCOUNT_NAME" $accountName
Assert-SingleLineValue "DIRECTMAIL_REGION" $region
Assert-SingleLineValue "DIRECTMAIL_FROM_ALIAS" $fromAlias
if ($region -cne "cn-hangzhou") {
    throw "DIRECTMAIL_REGION 必须为已冻结的 cn-hangzhou。"
}
if ($accountName -notmatch '^[^\s@]+@[^\s@]+\.[^\s@]+$') {
    throw "DIRECTMAIL_ACCOUNT_NAME 必须是单个合法邮箱地址。"
}

$addressHMACSecret = New-RandomSecret
do { $idempotencyHMACSecret = New-RandomSecret } while ($idempotencyHMACSecret -ceq $addressHMACSecret)
do { $internalAPIToken = New-RandomSecret } while ($internalAPIToken -ceq $addressHMACSecret -or $internalAPIToken -ceq $idempotencyHMACSecret)

$managedValues = [ordered]@{
    DIRECTMAIL_ACCESS_KEY_ID       = $accessKeyID
    DIRECTMAIL_ACCESS_KEY_SECRET   = $accessKeySecret
    DIRECTMAIL_REGION              = $region
    DIRECTMAIL_ACCOUNT_NAME        = $accountName
    DIRECTMAIL_FROM_ALIAS          = $fromAlias
    DIRECTMAIL_ENDPOINT            = "https://dm.aliyuncs.com/"
    EMAIL_ADAPTER                  = "production"
    EMAIL_ADDRESS_HMAC_SECRET      = $addressHMACSecret
    EMAIL_IDEMPOTENCY_SECRET       = $idempotencyHMACSecret
    EMAIL_DEBUG_RETURN_CODE        = "false"
    INTERNAL_API_TOKEN             = $internalAPIToken
}

$rawText = [IO.File]::ReadAllText($targetFile, [Text.Encoding]::UTF8)
$newline = if ($rawText.Contains("`r`n")) { "`r`n" } else { "`n" }
$originalLines = [Text.RegularExpressions.Regex]::Split($rawText, "`r?`n")
$writtenKeys = @{}
$outputLines = New-Object 'Collections.Generic.List[string]'

foreach ($line in $originalLines) {
    if ($line -match '^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=') {
        $keyName = $Matches[1]
        if ($managedValues.Contains($keyName)) {
            if (-not $writtenKeys.ContainsKey($keyName)) {
                $outputLines.Add("$keyName=$(ConvertTo-EnvValue ([string]$managedValues[$keyName]))")
                $writtenKeys[$keyName] = $true
            }
            # 删除同一受管键的重复定义，避免运行时读取到旧值。
            continue
        }
    }
    $outputLines.Add($line)
}

$missingKeys = @($managedValues.Keys | Where-Object { -not $writtenKeys.ContainsKey($_) })
if ($missingKeys.Count -gt 0) {
    if ($outputLines.Count -gt 0 -and $outputLines[$outputLines.Count - 1] -ne "") { $outputLines.Add("") }
    $outputLines.Add("# DirectMail 测试环境安全配置，由 configure-directmail-test.ps1 管理")
    foreach ($keyName in $missingKeys) {
        $outputLines.Add("$keyName=$(ConvertTo-EnvValue ([string]$managedValues[$keyName]))")
    }
}

try {
    [IO.File]::Copy($targetFile, $backupFile, $false)
    $newText = [string]::Join($newline, $outputLines)
    [IO.File]::WriteAllText($temporaryFile, $newText, (New-Object Text.UTF8Encoding($false)))
    Move-Item -LiteralPath $temporaryFile -Destination $targetFile -Force

    Write-Host "安全配置更新成功。已更新键："
    foreach ($keyName in $managedValues.Keys) { Write-Host "- $keyName：成功" }
    Write-Host "- 同目录时间戳备份：成功"
}
finally {
    if (Test-Path -LiteralPath $temporaryFile) { Remove-Item -LiteralPath $temporaryFile -Force }
    # 尽早释放脚本变量引用；PowerShell/.NET 无法保证托管字符串立即从内存清除。
    $accessKeyID = $null
    $accessKeySecret = $null
    $accountName = $null
    $addressHMACSecret = $null
    $idempotencyHMACSecret = $null
    $internalAPIToken = $null
}
