param(
    [string]$EnvFile = "D:\molingproject\molin\infra\.env.test"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $EnvFile -PathType Leaf)) {
    Write-Host "[SKIP] 受控 infra/.env.test 不存在，未执行真实 Redis 测试。"
    exit 0
}

# 只把 allowlist 中的 Redis 配置加载到当前子进程，绝不打印值，也不复制 env 文件。
$allowed = @("REDIS_ADDR", "REDIS_PASSWORD", "REDIS_DB", "REDIS_TLS")
foreach ($line in Get-Content -LiteralPath $EnvFile -Encoding UTF8) {
    $trimmed = $line.Trim()
    if (-not $trimmed -or $trimmed.StartsWith("#") -or -not $trimmed.Contains("=")) { continue }
    $name, $value = $trimmed.Split("=", 2)
    $name = $name.Trim()
    if ($allowed -notcontains $name) { continue }
    $value = $value.Trim()
    if ($value.Length -ge 2 -and (($value[0] -eq [char]34 -and $value[-1] -eq [char]34) -or ($value[0] -eq [char]39 -and $value[-1] -eq [char]39))) {
        $value = $value.Substring(1, $value.Length - 2)
    }
    [Environment]::SetEnvironmentVariable($name, $value, "Process")
}

$env:EMAIL_REDIS_TEST_ACK = "I_UNDERSTAND_EXACT_CLEANUP"
python (Join-Path $PSScriptRoot "redis_lock_contract.py")
exit $LASTEXITCODE
