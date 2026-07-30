param(
    [string]$ConfirmReadOnly = "",
    [string]$GoExecutable = "",
    [string]$GoModFile = ""
)

$ErrorActionPreference = "Stop"
$EnvFile = "D:\molingproject\molin\infra\.env.test"
$RequiredPhrase = "I_CONFIRM_SCHEMA_MIGRATIONS_SELECT_ONLY_NO_MIGRATION"
$AllowedNames = @("MYSQL_HOST", "MYSQL_PORT", "MYSQL_USER", "MYSQL_PASSWORD", "MYSQL_DATABASE")
$ServerRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\..\server"))

function Write-SafeStatus {
    param([string]$Json)
    Write-Host $Json
}

# 必须先匹配固定确认短语，避免误触真实数据库查询。
if ($ConfirmReadOnly -cne $RequiredPhrase) {
    Write-SafeStatus '{"reachable":false,"version":null,"dirty":null,"is_54_0":false,"reason":"confirmation_required"}'
    exit 2
}

try {
    # 只接受已安装的 go.exe，不执行下载或安装动作，也不向输出暴露工具路径。
    if ([string]::IsNullOrWhiteSpace($GoExecutable)) {
        $goCommand = Get-Command go -CommandType Application -ErrorAction SilentlyContinue
        if ($null -eq $goCommand) {
            Write-SafeStatus '{"reachable":false,"version":null,"dirty":null,"is_54_0":false,"reason":"go_unavailable"}'
            exit 2
        }
        $goPath = $goCommand.Source
    }
    else {
        if (-not (Test-Path -LiteralPath $GoExecutable -PathType Leaf)) {
            Write-SafeStatus '{"reachable":false,"version":null,"dirty":null,"is_54_0":false,"reason":"go_unavailable"}'
            exit 2
        }
        $goPath = (Resolve-Path -LiteralPath $GoExecutable).Path
    }

    if ((Split-Path -Leaf $goPath) -cne "go.exe" -or -not (Test-Path -LiteralPath $goPath -PathType Leaf)) {
        Write-SafeStatus '{"reachable":false,"version":null,"dirty":null,"is_54_0":false,"reason":"go_invalid"}'
        exit 2
    }

    if (-not (Test-Path -LiteralPath $EnvFile -PathType Leaf)) {
        Write-SafeStatus '{"reachable":false,"version":null,"dirty":null,"is_54_0":false,"reason":"configuration_unavailable"}'
        exit 2
    }

    # 固定只读模块模式、精确测试名和包路径，禁止 wrapper 接收任意 Go 测试参数。
    $goArguments = @("test", "-mod=readonly")
    if (-not [string]::IsNullOrWhiteSpace($GoModFile)) {
        if (-not (Test-Path -LiteralPath $GoModFile -PathType Leaf) -or [System.IO.Path]::GetExtension($GoModFile) -cne ".mod") {
            Write-SafeStatus '{"reachable":false,"version":null,"dirty":null,"is_54_0":false,"reason":"modfile_invalid"}'
            exit 2
        }
        $resolvedModFile = (Resolve-Path -LiteralPath $GoModFile).Path
        $goArguments += "-modfile=$resolvedModFile"
    }
    $goArguments += @("-run", "^TestEmailSchemaReadonlyGate54$", "-count=1", "-v", "./internal/modules/auth/repository")

    # 仅将五个 MYSQL 配置键注入当前 wrapper 的子进程，所有值均不写入输出。
    foreach ($line in Get-Content -LiteralPath $EnvFile -Encoding UTF8) {
        $trimmed = $line.Trim()
        if (-not $trimmed -or $trimmed.StartsWith("#") -or -not $trimmed.Contains("=")) {
            continue
        }
        $parts = $trimmed.Split("=", 2)
        $name = $parts[0].Trim()
        if ($AllowedNames -notcontains $name) {
            continue
        }
        $value = $parts[1].Trim()
        if ($value.Length -ge 2) {
            $doubleQuoted = $value[0] -eq [char]34 -and $value[-1] -eq [char]34
            $singleQuoted = $value[0] -eq [char]39 -and $value[-1] -eq [char]39
            if ($doubleQuoted -or $singleQuoted) {
                $value = $value.Substring(1, $value.Length - 2)
            }
        }
        [Environment]::SetEnvironmentVariable($name, $value, "Process")
    }

    $missingConfig = $false
    foreach ($name in $AllowedNames) {
        $currentValue = [Environment]::GetEnvironmentVariable($name, "Process")
        if ([string]::IsNullOrWhiteSpace($currentValue)) {
            $missingConfig = $true
        }
    }
    if ($missingConfig) {
        Write-SafeStatus '{"reachable":false,"version":null,"dirty":null,"is_54_0":false,"reason":"configuration_invalid"}'
        exit 2
    }

    # Go 测试还会校验第二层固定开关和确认值，避免被直接误调用。
    $env:RUN_EMAIL_SCHEMA_READONLY = "1"
    $env:EMAIL_SCHEMA_READONLY_ACK = "I_UNDERSTAND_READ_ONLY_SCHEMA_QUERY"
    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    Push-Location -LiteralPath $ServerRoot
    try {
        $testOutput = (& $goPath @goArguments 2>&1 | Out-String)
        $testExitCode = $LASTEXITCODE
    }
    finally {
        Pop-Location
        $ErrorActionPreference = $previousErrorActionPreference
    }

    # 原始测试输出仅在内存中匹配安全标记，对外只返回固定字段。
    if ($testExitCode -eq 0 -and $testOutput.Contains("schema_gate=PASS reachable=true version=54 dirty=false gate_54_0=true")) {
        Write-SafeStatus '{"reachable":true,"version":54,"dirty":false,"is_54_0":true}'
        exit 0
    }

    $mismatch = [regex]::Match($testOutput, "schema_gate=VERSION_MISMATCH reachable=true version=([0-9]+) dirty=(true|false) gate_54_0=false")
    if ($mismatch.Success) {
        $version = $mismatch.Groups[1].Value
        $dirty = $mismatch.Groups[2].Value
        Write-SafeStatus ("{`"reachable`":true,`"version`":" + $version + ",`"dirty`":" + $dirty + ",`"is_54_0`":false}")
        exit 1
    }

    if ($testOutput.Contains("schema_gate=CONFIG_MISSING")) {
        Write-SafeStatus '{"reachable":false,"version":null,"dirty":null,"is_54_0":false,"reason":"configuration_invalid"}'
        exit 2
    }
    if ($testOutput.Contains("schema_gate=CONNECT_INIT_FAILED") -or $testOutput.Contains("schema_gate=QUERY_FAILED")) {
        Write-SafeStatus '{"reachable":false,"version":null,"dirty":null,"is_54_0":false,"reason":"query_unavailable"}'
        exit 2
    }

    Write-SafeStatus '{"reachable":false,"version":null,"dirty":null,"is_54_0":false,"reason":"go_test_failed"}'
    exit 2
}
catch {
    Write-SafeStatus '{"reachable":false,"version":null,"dirty":null,"is_54_0":false,"reason":"wrapper_failed"}'
    exit 2
}
finally {
    # 无论成功或失败都清理当前进程变量，避免后续命令继承数据库配置。
    $env:RUN_EMAIL_SCHEMA_READONLY = $null
    $env:EMAIL_SCHEMA_READONLY_ACK = $null
    foreach ($name in $AllowedNames) {
        [Environment]::SetEnvironmentVariable($name, $null, "Process")
    }
}
