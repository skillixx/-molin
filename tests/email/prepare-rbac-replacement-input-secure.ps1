param([switch]$SelfTest)

# 自测只调用既有准备器的离线自测，不读取机密或访问网络。

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$existingRunner = Join-Path $PSScriptRoot 'prepare-rbac-phase4-input-secure.ps1'
$fixedPowerShell = Join-Path $PSHOME 'powershell.exe'
$realArguments = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $existingRunner, '-GenerateTestIdentities', '-GeneratePasswords', '-AccessOnly')
if (-not (Test-Path -LiteralPath $existingRunner -PathType Leaf) -or
    -not (Test-Path -LiteralPath $fixedPowerShell -PathType Leaf)) {
    Write-Output '[FAIL] replacement_prepared=false classification=base_runner_missing values_exposed=false'
    exit 1
}

if ($SelfTest) {
    # 既有准备器已覆盖参数泄露、输出泄露、重复目标和远端校验攻击面。
    $output = @()
    # 固定当前 PowerShell 安装目录，避免同名函数或别名劫持；先初始化退出码以兼容严格模式。
    $global:LASTEXITCODE = 1
    $output = & $fixedPowerShell -NoProfile -ExecutionPolicy Bypass -File $existingRunner -SelfTest 2>&1
    $childExitCode = [int]$global:LASTEXITCODE
    $joined = ($output | ForEach-Object { [string]$_ }) -join "`n"
    $nonPathArguments = @($realArguments[0], $realArguments[1], $realArguments[2], $realArguments[3], $realArguments[5], $realArguments[6], $realArguments[7])
    $argumentContract = $realArguments.Count -eq 8 -and
        $realArguments[5] -ceq '-GenerateTestIdentities' -and $realArguments[6] -ceq '-GeneratePasswords' -and
        $realArguments[7] -ceq '-AccessOnly' -and
        -not (($nonPathArguments -join ' ') -match '(?i)email=|phone=|password=|token=')
    $safe = $childExitCode -eq 0 -and $argumentContract -and
        $joined.Contains('[PASS] mode=selftest cases=27') -and $joined.Contains('external_access=false') -and
        $joined.Contains('sensitive_output=false')
    $joined = $null; $output = $null; $nonPathArguments = $null
    Write-Output ("[" + $(if ($safe) { 'PASS' } else { 'FAIL' }) + "] mode=replacement-selftest cases=5 external_access=false remote_writes=false local_writes=false sensitive_output=false")
    exit $(if ($safe) { 0 } else { 1 })
}

# 组合生成模式只提示已完成双 MFA 的管理员 Access Token；四组身份和强密码均在内存生成。
# 原脚本在严格模式下直接读取未初始化的 LASTEXITCODE 会失败，因此这里显式初始化并立即捕获。
$global:LASTEXITCODE = 1
& $fixedPowerShell @realArguments
$childExitCode = [int]$global:LASTEXITCODE
exit $childExitCode
