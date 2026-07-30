param(
    [string]$EnvFile = "D:\molingproject\molin\infra\.env.test",
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"

if (-not $SelfTest -and -not (Test-Path -LiteralPath $EnvFile -PathType Leaf)) {
    Write-Host "[SKIP] mode=redis classification=config_missing keys=0"
    exit 0
}

# 只向当前子进程加载 Redis 白名单配置，禁止输出任何配置值。
if (-not $SelfTest) {
    $allowedNames = @("REDIS_ADDR", "REDIS_PASSWORD", "REDIS_DB", "REDIS_TLS")
    foreach ($line in Get-Content -LiteralPath $EnvFile -Encoding UTF8) {
        $trimmed = $line.Trim()
        if (-not $trimmed -or $trimmed.StartsWith("#") -or -not $trimmed.Contains("=")) { continue }
        $name, $value = $trimmed.Split("=", 2)
        $name = $name.Trim()
        if ($allowedNames -notcontains $name) { continue }
        $value = $value.Trim()
        if ($value.Length -ge 2 -and (($value[0] -eq [char]34 -and $value[-1] -eq [char]34) -or ($value[0] -eq [char]39 -and $value[-1] -eq [char]39))) {
            $value = $value.Substring(1, $value.Length - 2)
        }
        [Environment]::SetEnvironmentVariable($name, $value, "Process")
    }
}

# 前缀只在内存生成并冻结；Python 只输出其 SHA-256 摘要。
if (-not $SelfTest) {
    $env:EMAIL_REDIS_TEST_PREFIX = "qa:email:phase4:$([Guid]::NewGuid().ToString('N'))"
    $env:EMAIL_REDIS_TEST_ACK = "I_CONFIRM_PHASE4_EXACT_THREE_KEYS"
}

# 使用重定向内存捕获子进程输出，只有固定安全格式才允许写回终端。
try {
    $pythonCommand = Get-Command python -ErrorAction Stop
    $scriptPath = Join-Path $PSScriptRoot "redis_lock_phase4_isolated.py"
    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = $pythonCommand.Source
    $escapedPath = $scriptPath.Replace('"', '\"')
    if ($SelfTest) {
        $startInfo.Arguments = "-B `"$escapedPath`" --self-test"
    } else {
        $startInfo.Arguments = "-B `"$escapedPath`""
    }
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $startInfo
    [void]$process.Start()
    $stdout = $process.StandardOutput.ReadToEnd()
    $stderr = $process.StandardError.ReadToEnd()
    $process.WaitForExit()
    $exitCode = $process.ExitCode
} catch {
    Write-Host "[FAIL] mode=redis classification=runner_output_invalid keys=0"
    exit 1
}

$realPattern = '^\[(PASS|FAIL)\] mode=redis prefix_sha256=[0-9a-f]{64} classification=[a-z0-9_]+ keys=3 pre_exists_zero=[0-3] post_exists_zero=[0-3]\r?\n?$'
$selfTestPattern = '^\[(PASS|FAIL)\] mode=selftest cases=6 passed=[0-6] external_access=false keys_created=0\r?\n?$'
if ($SelfTest) {
    $selectedPattern = $selfTestPattern
} else {
    $selectedPattern = $realPattern
}
if ($stderr.Length -ne 0 -or $stdout.Length -eq 0 -or $stdout -notmatch $selectedPattern) {
    Write-Host "[FAIL] mode=redis classification=runner_output_invalid keys=0"
    exit 1
}

Write-Host $stdout.TrimEnd("`r", "`n")
exit $exitCode
