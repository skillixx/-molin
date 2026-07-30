param()

$ErrorActionPreference = 'Stop'
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = (Resolve-Path (Join-Path $scriptRoot '..\..')).Path

# 先运行完全离线的进程内矩阵；失败时立即保留原始退出码。
& python -B (Join-Path $scriptRoot 'in_memory_email_matrix.py')
if ($LASTEXITCODE -ne 0) {
    Write-Output 'mock_matrix=false'
    exit 1
}
Write-Output 'mock_matrix=true'

# Go 测试只能在本机已有工具链时运行，禁止联网安装或静默跳过。
$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
    Write-Output 'go_tests=blocked'
    Write-Output 'reason=go_toolchain_unavailable'
    exit 2
}

Push-Location (Join-Path $repoRoot 'server')
try {
    & $go.Source test -count=1 ./internal/modules/auth/service ./internal/modules/auth/repository ./internal/modules/auth/handler
    if ($LASTEXITCODE -ne 0) {
        Write-Output 'go_tests=false'
        exit 1
    }
} finally {
    Pop-Location
}

Write-Output 'go_tests=true'
exit 0
