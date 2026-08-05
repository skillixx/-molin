param(
    [string]$ServerHost = "8.130.9.163",
    [int]$SSHPort = 10003,
    [string]$SSHUser = "pc",
    [string]$ChangeId = "20260805T115540Z",
    [string]$ExpectedRunnerSHA256 = "2724b89ea0096b15e5c443a2f5dfdd7e80f93c971ff2fb22a3585a5a1ad2bb46",
    [switch]$Execute,
    [string]$Authorization = "",
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1")
Assert-SmsPhase5FixedTestServerTarget -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser

$requiredChangeId = "20260805T115540Z"
$requiredSHA256 = "2724b89ea0096b15e5c443a2f5dfdd7e80f93c971ff2fb22a3585a5a1ad2bb46"
$requiredAuthorization = "APPROVE_SMS_PHASE5_TEST_ROLLBACK_DRILL_20260805T115540Z"
if ($ChangeId -cne $requiredChangeId -or $ExpectedRunnerSHA256 -cne $requiredSHA256) {
    throw "实际回滚包装器只允许当前冻结候选"
}
if ($SelfTest -and $Execute) {
    throw "SelfTest 与实际回滚执行必须互斥"
}

# 远端载荷仅核验精确暂存 runner 后调用一次 --execute；服务恢复由冻结 runner 自身负责。
$payload = @'
#!/usr/bin/env bash
set -Eeuo pipefail
runner='/home/pc/molin/rollback/sms-phase5/runtime-drill-staging/__CHANGE_ID__/run-sms-phase5-test-server-rollback-drill.sh'
evidence='/home/pc/molin/rollback/sms-phase5/runtime-drills/drill-__CHANGE_ID__'
expected_sha256='__RUNNER_SHA256__'
authorization_phrase='__AUTHORIZATION_PHRASE__'

fail() {
  printf 'rollback_runtime_execution_wrapper=failed\n'
  printf 'failure_stage=%s\n' "$1"
  printf 'execution_retries=0\n'
  printf 'notification_posts=0\n'
  printf 'business_endpoint_posts=0\n'
  printf 'real_sms_sent=0\n'
  exit 2
}

[ "$(id -un)" = pc ] || fail operator_identity
[ -f "$runner" ] && [ ! -L "$runner" ] || fail runner_identity
[ "$(realpath -- "$runner")" = "$runner" ] || fail runner_realpath
[ "$(stat -c '%U:%a:%h' "$runner")" = pc:600:1 ] || fail runner_owner_mode
[ "$(sha256sum "$runner" | awk '{print $1}')" = "$expected_sha256" ] || fail runner_hash
[ ! -e "$evidence" ] || fail evidence_preexists
printf '%s\n' "$authorization_phrase" | bash "$runner" --execute || fail runner_execute
printf 'rollback_runtime_execution_wrapper=passed\n'
printf 'change_id=__CHANGE_ID__\n'
printf 'runner_sha256=__RUNNER_SHA256__\n'
printf 'execution_attempts=1\n'
printf 'execution_retries=0\n'
printf 'independent_verification_required=true\n'
printf 'notification_posts=0\n'
printf 'business_endpoint_posts=0\n'
printf 'real_sms_sent=0\n'
'@
$payload = $payload.Replace("__CHANGE_ID__", $ChangeId).
    Replace("__RUNNER_SHA256__", $ExpectedRunnerSHA256).
    Replace("__AUTHORIZATION_PHRASE__", $requiredAuthorization)
$payload = $payload -replace "`r`n", "`n"
if (-not $payload.StartsWith("#!/usr/bin/env bash`n") -or $payload.Contains("`r") -or
    $payload.Contains([char]0xFEFF) -or $payload -match '__[A-Z0-9_]+__') {
    throw "实际回滚载荷编码或占位符状态异常"
}

if ($SelfTest) {
    foreach ($marker in @(
        "rollback_runtime_execution_wrapper=passed",
        "execution_attempts=1",
        "execution_retries=0",
        "independent_verification_required=true",
        "notification_posts=0",
        "business_endpoint_posts=0",
        "real_sms_sent=0"
    )) {
        if (-not $payload.Contains($marker)) {
            throw "实际回滚载荷缺少安全标记：$marker"
        }
    }
    if ([regex]::Matches($payload, '(?m)\bbash "\$runner" --execute\b').Count -ne 1) {
        throw "实际回滚载荷必须恰好调用一次冻结 runner"
    }
    foreach ($forbidden in @(
        'SMS_ENABLED=true',
        '/api/auth/verification-codes/phone',
        '/api/admin/sms/.+test-send',
        '\bcurl\b[^\n]*(--request|-X|--data|-d|--form|-F)\b',
        '(?m)^\s*(rm|mv|cp|install|chmod|chown|truncate|touch|tee)\b',
        '\bkill\b',
        '\bsystemctl\s+(restart|reload|stop|start)\b',
        '\bdocker\s+(restart|stop|kill|rm|run|create|pull)\b'
    )) {
        if ($payload -match $forbidden) {
            throw "实际回滚包装载荷包含越权模式：$forbidden"
        }
    }
    Write-Output "rollback_runtime_execution_wrapper_self_test=passed"
    Write-Output "remote_connections=0"
    Write-Output "execution_attempts=0"
    Write-Output "service_restarts=0"
    Write-Output "notification_posts=0"
    Write-Output "business_endpoint_posts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if (-not $Execute) {
    Write-Output "rollback_runtime_execution_authorized=false"
    Write-Output "change_id=$ChangeId"
    Write-Output "runner_sha256=$ExpectedRunnerSHA256"
    Write-Output "remote_connections=0"
    Write-Output "execution_attempts=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($Authorization -cne $requiredAuthorization) {
    throw "实际回滚必须提供与冻结 ChangeId 绑定的精确批准短语"
}

$knownHostsPath = Assert-SmsPhase5FixedTestServerIdentity `
    -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser
$encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($payload))
$destination = "${SSHUser}@${ServerHost}"
$sshArguments = @(
    "-p", $SSHPort.ToString(),
    "-o", "BatchMode=yes",
    "-o", "ConnectTimeout=8",
    "-o", "StrictHostKeyChecking=yes",
    "-o", "HostKeyAlgorithms=ssh-ed25519",
    "-o", "UserKnownHostsFile=$knownHostsPath",
    $destination,
    "printf '%s' '$encoded' | base64 -d | bash"
)
& ssh.exe @sshArguments
if ($LASTEXITCODE -ne 0) {
    throw "阶段 5 测试服实际回滚执行失败；禁止自动重试，必须先核对自动恢复证据"
}

# 执行成功后立即使用独立只读验证器核对证据和当前恢复状态，不信任 runner 单一成功标记。
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
    (Join-Path $PSScriptRoot "verify-sms-phase5-test-server-rollback-drill.ps1") `
    -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser `
    -ChangeId $ChangeId -ExpectedRunnerSHA256 $ExpectedRunnerSHA256
if ($LASTEXITCODE -ne 0) {
    throw "实际回滚执行已返回成功，但独立只读验收失败"
}

Write-Output "rollback_runtime_execution_and_verification=passed"
Write-Output "change_id=$ChangeId"
Write-Output "execution_attempts=1"
Write-Output "execution_retries=0"
Write-Output "independent_verification=passed"
Write-Output "real_sms_sent=0"
