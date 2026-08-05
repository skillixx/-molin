param(
    [string]$EnvironmentFile = "",
    [ValidateSet("none", "test", "production")]
    [string]$ExpectedEnvironment = "none",
    [switch]$RunGoTests,
    [switch]$RunSensitiveScan,
    [string]$SensitiveScanBaseRef = "origin/main",
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path

function Read-KeyValueFile {
    param([string]$Path)
    $values = @{}
    foreach ($line in Get-Content -LiteralPath $Path -Encoding UTF8) {
        $trimmed = $line.Trim()
        if ($trimmed.Length -eq 0 -or $trimmed.StartsWith("#") -or -not $trimmed.Contains("=")) {
            continue
        }
        $key, $value = $trimmed.Split("=", 2)
        $values[$key.Trim()] = $value.Trim()
    }
    return $values
}

function Assert-Equal {
    param([string]$Name, [string]$Actual, [string]$Expected)
    if ($Actual -cne $Expected) {
        throw "$Name 不符合预期"
    }
}

function Assert-SMSReleaseEnvironment {
    param(
        [hashtable]$Environment,
        [ValidateSet("test", "production")]
        [string]$EnvironmentName
    )
    Assert-Equal -Name "环境 SMS_ENABLED" -Actual $Environment["SMS_ENABLED"] -Expected "false"
    Assert-Equal -Name "环境 SMS_TEST_MODE" -Actual $Environment["SMS_TEST_MODE"] -Expected "true"
    Assert-Equal -Name "短信供应商" -Actual $Environment["SMS_PROVIDER"] -Expected "aliyun"
    Assert-Equal -Name "阿里云短信端点" -Actual $Environment["SMS_ALIYUN_ENDPOINT"] -Expected "dysmsapi.aliyuncs.com"
    foreach ($key in @("SMS_ALIYUN_ACCESS_KEY_ID", "SMS_ALIYUN_ACCESS_KEY_SECRET", "SMS_ALIYUN_SIGN_NAME", "SMS_PHONE_HMAC_SECRET")) {
        if ([string]::IsNullOrWhiteSpace($Environment[$key])) {
            throw "$key 未安全注入"
        }
    }
    if ($Environment["SMS_PHONE_HMAC_SECRET"].Length -lt 32) {
        throw "SMS_PHONE_HMAC_SECRET 长度不足 32 字节"
    }
    if ($Environment.Keys | Where-Object { $_ -like "SMS_TEMPLATE_CODE_*" }) {
        throw "环境文件不得恢复 SMS_TEMPLATE_CODE_* 双真相源"
    }
    if ($EnvironmentName -eq "test" -and [string]::IsNullOrWhiteSpace($Environment["SMS_TEST_PHONE_WHITELIST"])) {
        throw "测试环境受控窗口前白名单不得为空"
    }
    Assert-Equal -Name "运行环境" -Actual $Environment["APP_ENV"] -Expected $EnvironmentName
}

function Invoke-SelfTest {
    $validTest = @{
        APP_ENV = "test"
        SMS_ENABLED = "false"
        SMS_TEST_MODE = "true"
        SMS_PROVIDER = "aliyun"
        SMS_ALIYUN_ENDPOINT = "dysmsapi.aliyuncs.com"
        SMS_ALIYUN_ACCESS_KEY_ID = "TEST_ACCESS_KEY_ID_PLACEHOLDER"
        SMS_ALIYUN_ACCESS_KEY_SECRET = "TEST_ACCESS_KEY_SECRET_PLACEHOLDER"
        SMS_ALIYUN_SIGN_NAME = "测试签名占位符"
        SMS_PHONE_HMAC_SECRET = "TEST_SMS_HMAC_SECRET_32_CHARS_ONLY"
        SMS_TEST_PHONE_WHITELIST = "TEST_PHONE_WHITELIST_PLACEHOLDER"
    }
    Assert-SMSReleaseEnvironment -Environment $validTest -EnvironmentName "test"

    $invalidProvider = $validTest.Clone()
    $invalidProvider["SMS_PROVIDER"] = "mock"
    $providerRejected = $false
    try {
        Assert-SMSReleaseEnvironment -Environment $invalidProvider -EnvironmentName "test"
    }
    catch {
        $providerRejected = $true
    }

    $invalidTemplateSource = $validTest.Clone()
    $invalidTemplateSource["SMS_TEMPLATE_CODE_REGISTER"] = "TEST_TEMPLATE_CODE_PLACEHOLDER"
    $templateSourceRejected = $false
    try {
        Assert-SMSReleaseEnvironment -Environment $invalidTemplateSource -EnvironmentName "test"
    }
    catch {
        $templateSourceRejected = $true
    }

    if (-not $providerRejected -or -not $templateSourceRejected) {
        throw "阶段 5 环境准备度反例自测失败"
    }
    Write-Output "readiness_self_test=passed"
}

$examplePath = Join-Path $root "infra\.env.example"
$example = Read-KeyValueFile -Path $examplePath
Assert-Equal -Name "示例 SMS_ENABLED" -Actual $example["SMS_ENABLED"] -Expected "false"
Assert-Equal -Name "示例 SMS_TEST_MODE" -Actual $example["SMS_TEST_MODE"] -Expected "true"

if (Select-String -LiteralPath $examplePath -Pattern '^SMS_TEMPLATE_CODE_' -Quiet) {
    throw "示例配置不得恢复 SMS_TEMPLATE_CODE_* 双真相源"
}

$requiredFiles = @(
    "server\migrations\000058_add_sms_phase1_foundation.up.sql",
    "server\migrations\000059_add_sms_phase2_management.up.sql",
    "infra\nginx\verify_forwarded_headers.py",
    "infra\prometheus\email-alerts.yml",
    "scripts\verify-sms-phase5-sensitive-data.py",
    "scripts\verify-sms-phase5-proxy-network-plan.ps1",
    "scripts\sms-phase5-test-server-ssh.ps1",
    "scripts\verify-sms-phase5-test-server-readonly.ps1",
    "scripts\verify-sms-phase5-test-server-readonly.sh",
    "scripts\verify-sms-phase5-test-server-recovery-readiness.ps1",
    "scripts\verify-sms-phase5-test-server-recovery-readiness.sh",
    "scripts\prepare-sms-phase5-test-server-rollback-candidate.ps1",
    "scripts\prepare-sms-phase5-test-server-rollback-candidate.sh",
    "scripts\verify-sms-phase5-test-server-rollback-candidate.ps1",
    "scripts\verify-sms-phase5-test-server-rollback-candidate.sh",
    "scripts\prepare-sms-phase5-test-server-rollback-drill.ps1",
    "scripts\run-sms-phase5-test-server-rollback-drill.sh",
    "scripts\verify-sms-phase5-test-server-log-retention.ps1",
    "scripts\verify-sms-phase5-test-server-log-retention.sh",
    "scripts\verify-sms-phase5-alertmanager-drill-readiness.ps1",
    "scripts\verify-sms-phase5-alertmanager-drill-readiness.sh",
    "scripts\verify-sms-phase5-alertmanager-drill-payload.ps1",
    "scripts\verify-sms-phase5-test-server-canary-preflight.ps1",
    "scripts\apply-sms-phase5-test-server-log-retention.ps1",
    "scripts\apply-sms-phase5-test-server-log-retention.sh"
)
foreach ($relative in $requiredFiles) {
    if (-not (Test-Path -LiteralPath (Join-Path $root $relative) -PathType Leaf)) {
        throw "缺少阶段 5 前置资产：$relative"
    }
}

& python (Join-Path $root "infra\nginx\verify_forwarded_headers.py")
if ($LASTEXITCODE -ne 0) {
    throw "Nginx 来源头静态校验失败"
}

$alerts = Get-Content -LiteralPath (Join-Path $root "infra\prometheus\email-alerts.yml") -Raw -Encoding UTF8
foreach ($marker in @("sms_provider_calls_total", "sms_provider_request_duration_seconds", "MolinSMSProviderFailureRateHigh")) {
    if (-not $alerts.Contains($marker)) {
        throw "短信观察规则缺少：$marker"
    }
}

if ($EnvironmentFile -ne "") {
    $resolvedEnvironment = (Resolve-Path -LiteralPath $EnvironmentFile).Path
    & git -C $root check-ignore --quiet -- $resolvedEnvironment
    if ($LASTEXITCODE -ne 0) {
        throw "环境文件必须被 Git 忽略，避免敏感配置进入版本库"
    }
    $environment = Read-KeyValueFile -Path $resolvedEnvironment
    if ($ExpectedEnvironment -eq "none") {
        throw "检查环境文件时必须显式指定 ExpectedEnvironment=test 或 production"
    }
    Assert-SMSReleaseEnvironment -Environment $environment -EnvironmentName $ExpectedEnvironment
    Write-Output "environment_file_check=passed"
}

if ($SelfTest) {
    Invoke-SelfTest
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
        (Join-Path $root "scripts\verify-sms-phase5-alertmanager-drill-payload.ps1") -SelfTest
    if ($LASTEXITCODE -ne 0) {
        throw "Alertmanager firing/resolved 载荷转换自测失败"
    }
}

if ($RunSensitiveScan) {
    $scanOutput = @(& python (Join-Path $root "scripts\verify-sms-phase5-sensitive-data.py") `
        --repo-root $root --base-ref $SensitiveScanBaseRef --require-dist)
    $scanExitCode = $LASTEXITCODE
    $scanOutput | Write-Output
    if ($scanExitCode -ne 0 -or $scanOutput -notcontains "phase5_sensitive_scan=passed") {
        throw "阶段 5 敏感信息与短信关闭态门禁失败"
    }
}

if ($RunGoTests) {
    Push-Location (Join-Path $root "server")
    try {
        & go test ./internal/modules/sms/service ./internal/modules/auth/handler ./internal/modules/auth ./internal/bootstrap -count=1
        if ($LASTEXITCODE -ne 0) {
            throw "阶段 5 后端专项测试失败"
        }
    }
    finally {
        Pop-Location
    }
}

Write-Output "readiness_static=passed"
Write-Output "sms_enabled_expected=false"
Write-Output "sms_test_mode_expected=true"
Write-Output "external_actions=0"
Write-Output "real_sms_sent=0"
