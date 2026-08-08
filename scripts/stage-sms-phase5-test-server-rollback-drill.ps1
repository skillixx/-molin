param(
    [string]$ServerHost = "8.130.9.163",
    [int]$SSHPort = 10003,
    [string]$SSHUser = "pc",
    [string]$ChangeId = "20260805T115540Z",
    [string]$CandidatePath = "D:\molingproject\molin-phase5-rollback-runtime-candidate-20260805T115540Z\run-sms-phase5-test-server-rollback-drill.sh",
    [string]$ExpectedSHA256 = "2724b89ea0096b15e5c443a2f5dfdd7e80f93c971ff2fb22a3585a5a1ad2bb46",
    [switch]$StageAndPreflight,
    [string]$Authorization = "",
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1")
Assert-SmsPhase5FixedTestServerTarget -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser

if ($ChangeId -cne "20260805T115540Z" -or
    $ExpectedSHA256 -cne "2724b89ea0096b15e5c443a2f5dfdd7e80f93c971ff2fb22a3585a5a1ad2bb46") {
    throw "暂存包装器只允许当前冻结候选 ChangeId 与摘要"
}
if ($SelfTest -and $StageAndPreflight) {
    throw "SelfTest 与远端暂存预检必须互斥"
}

$payloadPath = Join-Path $PSScriptRoot "stage-sms-phase5-test-server-rollback-drill.sh"
$payload = Get-Content -LiteralPath $payloadPath -Raw -Encoding UTF8
$payload = $payload.Replace("__CHANGE_ID__", $ChangeId).Replace("__EXPECTED_SHA256__", $ExpectedSHA256)
$payload = $payload -replace "`r`n", "`n"
if (-not $payload.StartsWith("#!/usr/bin/env bash`n") -or $payload.Contains("`r") -or
    $payload.Contains([char]0xFEFF) -or $payload -match '__[A-Z0-9_]+__') {
    throw "暂存 payload 编码或占位符状态异常"
}

if ($SelfTest) {
    foreach ($marker in @(
        "rollback_runtime_staging_prepared=true",
        "rollback_runtime_staging_validation=passed",
        "runner_sha256=%s",
        "bash_syntax=passed",
        "runner_self_test=passed",
        "closed_state_readonly_preflight=passed",
        "service_restarts=0",
        "real_sms_sent=0"
    )) {
        if (-not $payload.Contains($marker)) {
            throw "暂存 payload 缺少安全标记：$marker"
        }
    }
    foreach ($forbidden in @(
        '--execute',
        'SMS_ENABLED=true',
        '/api/auth/verification-codes/phone',
        '/api/admin/sms/.+test-send',
        '\bcurl\b[^\n]*(--request|-X|--data|-d|--form|-F)\b',
        '\brm\s+(?:-[A-Za-z]*r[A-Za-z]*\b|--recursive\b)',
        '\bkill\b',
        '\bsystemctl\s+(restart|reload|stop|start)\b',
        '\bdocker\s+(restart|stop|kill|rm|run|create|pull)\b'
    )) {
        if ($payload -match $forbidden) {
            throw "暂存 payload 包含禁止模式：$forbidden"
        }
    }
    Write-Output "rollback_runtime_staging_self_test=passed"
    Write-Output "remote_connections=0"
    Write-Output "remote_files_written=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if (-not $StageAndPreflight) {
    Write-Output "rollback_runtime_staging_authorized=false"
    Write-Output "change_id=$ChangeId"
    Write-Output "expected_sha256=$ExpectedSHA256"
    Write-Output "remote_connections=0"
    Write-Output "remote_files_written=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($Authorization -cne "APPROVE_SMS_PHASE5_TEST_ROLLBACK_DRILL_STAGE_20260805T115540Z") {
    throw "暂存和只读预检必须提供与最终候选绑定的精确批准短语"
}
if (-not [IO.Path]::IsPathRooted($CandidatePath) -or $CandidatePath.StartsWith("\\")) {
    throw "本地候选必须使用本机完全限定的绝对路径"
}
$isWindowsRuntime = [IO.Path]::DirectorySeparatorChar -eq "\"
if (($isWindowsRuntime -and $CandidatePath -cnotmatch '^[A-Za-z]:[\\/]') -or
    (-not $isWindowsRuntime -and -not $CandidatePath.StartsWith("/"))) {
    throw "本地候选不能使用驱动器相对路径或根相对路径"
}
$candidateFullPath = [IO.Path]::GetFullPath($CandidatePath)
if (-not (Test-Path -LiteralPath $candidateFullPath -PathType Leaf)) {
    throw "本地候选不存在"
}
$candidateInfo = Get-Item -LiteralPath $candidateFullPath -Force
if ($candidateInfo.Attributes.HasFlag([IO.FileAttributes]::ReparsePoint)) {
    throw "本地候选不能是重解析点"
}
if ($isWindowsRuntime) {
    $candidateDrive = New-Object IO.DriveInfo([IO.Path]::GetPathRoot($candidateFullPath))
    if ($candidateDrive.DriveType -eq [IO.DriveType]::Network) {
        throw "本地候选不能位于映射网络驱动器"
    }
}
$candidateParent = $candidateInfo.Directory
while ($null -ne $candidateParent) {
    if ($candidateParent.Attributes.HasFlag([IO.FileAttributes]::ReparsePoint)) {
        throw "本地候选目录链不能包含重解析点"
    }
    $candidateParent = $candidateParent.Parent
}
$actualSHA256 = (Get-FileHash -LiteralPath $candidateFullPath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualSHA256 -cne $ExpectedSHA256) {
    throw "本地候选摘要不匹配，禁止上传"
}

$knownHostsPath = Assert-SmsPhase5FixedTestServerIdentity `
    -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser
$encodedPayload = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($payload))
$destination = "${SSHUser}@${ServerHost}"
$commonSSH = @(
    "-p", $SSHPort.ToString(),
    "-o", "BatchMode=yes",
    "-o", "ConnectTimeout=8",
    "-o", "StrictHostKeyChecking=yes",
    "-o", "HostKeyAlgorithms=ssh-ed25519",
    "-o", "UserKnownHostsFile=$knownHostsPath",
    $destination
)
$stagePrepared = $false
try {
    & ssh.exe @commonSSH "printf '%s' '$encodedPayload' | base64 -d | bash -s -- prepare"
    if ($LASTEXITCODE -ne 0) {
        throw "测试服暂存目录准备失败"
    }
    $stagePrepared = $true
    $remotePath = "${destination}:/home/pc/molin/rollback/sms-phase5/runtime-drill-staging/$ChangeId/run-sms-phase5-test-server-rollback-drill.sh"
    $scpArguments = @(
        "-P", $SSHPort.ToString(),
        "-o", "BatchMode=yes",
        "-o", "ConnectTimeout=8",
        "-o", "StrictHostKeyChecking=yes",
        "-o", "HostKeyAlgorithms=ssh-ed25519",
        "-o", "UserKnownHostsFile=$knownHostsPath",
        "--", $candidateFullPath, $remotePath
    )
    & scp.exe @scpArguments
    if ($LASTEXITCODE -ne 0) {
        throw "测试服候选上传失败"
    }
    & ssh.exe @commonSSH "printf '%s' '$encodedPayload' | base64 -d | bash -s -- verify"
    if ($LASTEXITCODE -ne 0) {
        throw "测试服候选静态校验或关闭态只读预检失败"
    }
}
catch {
    if ($stagePrepared) {
        & ssh.exe @commonSSH "printf '%s' '$encodedPayload' | base64 -d | bash -s -- cleanup" | Out-Null
    }
    throw
}

Write-Output "rollback_runtime_staging=passed"
Write-Output "change_id=$ChangeId"
Write-Output "runner_sha256=$ExpectedSHA256"
Write-Output "remote_stage_directory_created=true"
Write-Output "remote_files_written=1"
Write-Output "service_restarts=0"
Write-Output "notification_posts=0"
Write-Output "business_endpoint_posts=0"
Write-Output "real_sms_sent=0"
