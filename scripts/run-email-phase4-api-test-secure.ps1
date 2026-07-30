param(
    # API 地址只作为普通运行参数传入，不包含任何认证信息。
    [string]$ApiBase = "http://127.0.0.1:18080",
    # 默认使用 PATH 中的 Python；如需指定，只传本机受控解释器路径。
    [string]$PythonExecutable = "python",
    # 写接口测试仍需由调用者另外准备隔离模板等非本脚本负责的条件。
    [switch]$AllowMutations,
    # 只运行内存假传输自测，不读取机密、不访问 API。
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

# 本脚本只在当前 PowerShell 进程中临时设置四个验收变量，不写入 .env、日志或命令行参数。
# 若系统中存在同用户高权限进程，它仍可能读取进程环境或内存，因此只能在受控测试机上运行。
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$testScript = [IO.Path]::GetFullPath((Join-Path $repoRoot "tests/email/phase2_email_api.py"))
if (-not (Test-Path -LiteralPath $testScript -PathType Leaf)) {
    throw "未找到固定的邮件 API 验收脚本，拒绝继续。"
}

if ($ApiBase -notmatch '^https?://[^\s/]+(?::\d+)?(?:/[^\s]*)?$') {
    throw "ApiBase 必须是合法的 HTTP 或 HTTPS 地址。"
}

if ($SelfTest) {
    & $PythonExecutable -B $testScript --self-test-permission-matrix
    $selfTestExitCode = $LASTEXITCODE
    if ($null -eq $selfTestExitCode) {
        throw "Python 离线自测进程未返回有效退出码。"
    }
    exit $selfTestExitCode
}

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

function Assert-SafeSingleLine {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Value
    )

    # 错误消息只包含变量名，绝不拼接敏感值。
    if ([string]::IsNullOrWhiteSpace($Value) -or $Value.Contains("`r") -or
        $Value.Contains("`n") -or $Value.Contains([char]0)) {
        throw "$Name 不能为空，也不能包含换行或空字符。"
    }
}

function Assert-OptionalSafeSingleLine {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [AllowEmptyString()][string]$Value
    )

    # 可选 Token 允许直接回车跳过，但非空值仍必须是单行安全文本。
    if ($null -eq $Value -or $Value.Length -eq 0) {
        return
    }
    if ([string]::IsNullOrWhiteSpace($Value) -or $Value.Contains("`r") -or
        $Value.Contains("`n") -or $Value.Contains([char]0)) {
        throw "$Name 不能只含空白，也不能包含换行或空字符。"
    }
}

$managedNames = @(
    "EMAIL_ADMIN_MFA_TOKEN",
    "EMAIL_ADMIN_NO_MFA_TOKEN",
    "EMAIL_ADMIN_NO_PERMISSION_TOKEN",
    "EMAIL_ADMIN_VIEW_ONLY_TOKEN",
    "EMAIL_ADMIN_VIEW_MANAGE_TOKEN",
    "EMAIL_ADMIN_VIEW_SYNC_TOKEN",
    "EMAIL_ADMIN_VIEW_TEST_TOKEN",
    "EMAIL_TEST_RECIPIENT",
    "API_BASE",
    "EMAIL_ALLOW_MUTATIONS"
)
$previousValues = @{}
$previousPresence = @{}
foreach ($name in $managedNames) {
    $previousPresence[$name] = Test-Path -LiteralPath "Env:$name"
    if ($previousPresence[$name]) {
        $previousValues[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
    }
}

$mfaToken = $null
$noMfaToken = $null
$noPermissionToken = $null
$viewOnlyToken = $null
$viewManageToken = $null
$viewSyncToken = $null
$viewTestToken = $null
$testRecipient = $null

try {
    Write-Host "将安全读取四项必填与四项可选临时验收输入；输入不回显、不落盘，也不会作为命令行参数传递。"
    $mfaToken = Read-SecureText "双 MFA 且具备邮件权限的临时管理员 Token"
    $noMfaToken = Read-SecureText "未完成双 MFA 的临时管理员 Token"
    $noPermissionToken = Read-SecureText "双 MFA 但无邮件权限的临时管理员 Token"
    $testRecipient = Read-SecureText "受控测试收件邮箱"
    Write-Host "以下四项用于最小权限隔离，可直接回车跳过；跳过项会在报告中明确标记 SKIP。"
    $viewOnlyToken = Read-SecureText "仅具备 email:template:view 的双 MFA Token（可选）"
    $viewManageToken = Read-SecureText "具备 view+manage 的双 MFA Token（可选）"
    $viewSyncToken = Read-SecureText "具备 view+sync 的双 MFA Token（可选）"
    $viewTestToken = Read-SecureText "具备 view+test 的双 MFA Token（可选）"

    Assert-SafeSingleLine "EMAIL_ADMIN_MFA_TOKEN" $mfaToken
    Assert-SafeSingleLine "EMAIL_ADMIN_NO_MFA_TOKEN" $noMfaToken
    Assert-SafeSingleLine "EMAIL_ADMIN_NO_PERMISSION_TOKEN" $noPermissionToken
    Assert-SafeSingleLine "EMAIL_TEST_RECIPIENT" $testRecipient
    Assert-OptionalSafeSingleLine "EMAIL_ADMIN_VIEW_ONLY_TOKEN" $viewOnlyToken
    Assert-OptionalSafeSingleLine "EMAIL_ADMIN_VIEW_MANAGE_TOKEN" $viewManageToken
    Assert-OptionalSafeSingleLine "EMAIL_ADMIN_VIEW_SYNC_TOKEN" $viewSyncToken
    Assert-OptionalSafeSingleLine "EMAIL_ADMIN_VIEW_TEST_TOKEN" $viewTestToken
    if ($testRecipient -notmatch '^[^\s@]+@[^\s@]+\.[^\s@]+$') {
        throw "EMAIL_TEST_RECIPIENT 必须是单个合法邮箱地址。"
    }

    [Environment]::SetEnvironmentVariable("EMAIL_ADMIN_MFA_TOKEN", $mfaToken, "Process")
    [Environment]::SetEnvironmentVariable("EMAIL_ADMIN_NO_MFA_TOKEN", $noMfaToken, "Process")
    [Environment]::SetEnvironmentVariable("EMAIL_ADMIN_NO_PERMISSION_TOKEN", $noPermissionToken, "Process")
    [Environment]::SetEnvironmentVariable("EMAIL_ADMIN_VIEW_ONLY_TOKEN", $(if ($viewOnlyToken) { $viewOnlyToken } else { $null }), "Process")
    [Environment]::SetEnvironmentVariable("EMAIL_ADMIN_VIEW_MANAGE_TOKEN", $(if ($viewManageToken) { $viewManageToken } else { $null }), "Process")
    [Environment]::SetEnvironmentVariable("EMAIL_ADMIN_VIEW_SYNC_TOKEN", $(if ($viewSyncToken) { $viewSyncToken } else { $null }), "Process")
    [Environment]::SetEnvironmentVariable("EMAIL_ADMIN_VIEW_TEST_TOKEN", $(if ($viewTestToken) { $viewTestToken } else { $null }), "Process")
    [Environment]::SetEnvironmentVariable("EMAIL_TEST_RECIPIENT", $testRecipient, "Process")
    [Environment]::SetEnvironmentVariable("API_BASE", $ApiBase.TrimEnd('/'), "Process")
    if ($AllowMutations) {
        [Environment]::SetEnvironmentVariable("EMAIL_ALLOW_MUTATIONS", "1", "Process")
    }
    else {
        [Environment]::SetEnvironmentVariable("EMAIL_ALLOW_MUTATIONS", $null, "Process")
    }

    Write-Host "临时输入校验完成，开始执行邮件 API 验收；控制台不会显示 Token 或完整邮箱。"
    & $PythonExecutable $testScript
    $testExitCode = $LASTEXITCODE
    if ($null -eq $testExitCode) {
        throw "Python 验收进程未返回有效退出码。"
    }
    exit $testExitCode
}
finally {
    # 无论正常、失败或用户中断，都恢复脚本进入前的进程环境，避免短期 Token 残留在会话中。
    foreach ($name in $managedNames) {
        if ($previousPresence[$name]) {
            [Environment]::SetEnvironmentVariable($name, [string]$previousValues[$name], "Process")
        }
        else {
            [Environment]::SetEnvironmentVariable($name, $null, "Process")
        }
    }

    # 托管字符串无法保证立即从内存抹除，因此仅缩短引用生命周期，不宣称实现物理清零。
    $mfaToken = $null
    $noMfaToken = $null
    $noPermissionToken = $null
    $viewOnlyToken = $null
    $viewManageToken = $null
    $viewSyncToken = $null
    $viewTestToken = $null
    $testRecipient = $null
    $previousValues.Clear()
}
