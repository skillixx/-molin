param(
    [string]$ChangeId = "",
    [string]$OutputDirectory = "",
    [ValidateSet("base-transport", "isolated-bash", "isolated-bash-stdin")]
    [string]$DiagnosticMode = "base-transport",
    [switch]$ExportCandidate,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
$FixedServerHost = "8.130.9.163"
$FixedSSHPort = 10003
$FixedSSHUser = "pc"

function Assert-LocalAbsolutePath {
    param([string]$Path)

    # 候选只能生成到本地绝对路径，避免生成阶段通过 UNC 或映射盘发生外部访问。
    if ([string]::IsNullOrWhiteSpace($Path) -or $Path -match '^(?:\\\\|//)' -or $Path.Contains("::")) {
        throw "候选输出目录必须是本地文件系统绝对路径"
    }
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        if ($Path -cnotmatch '^[A-Za-z]:[\\/]') { throw "Windows 候选输出目录必须使用本地盘符绝对路径" }
        $drive = Get-PSDrive -Name $Path.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
        if (([string]$drive.Root).StartsWith("\\") -or ([string]$drive.DisplayRoot).StartsWith("\\")) {
            throw "候选输出目录不得使用网络映射盘"
        }
    }
    elseif (-not [IO.Path]::IsPathRooted($Path)) {
        throw "候选输出目录必须使用本地绝对路径"
    }
}

if (-not $ExportCandidate -and -not $SelfTest) {
    Write-Output "ssh_stderr_diagnostic_candidate_authorized=false"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "business_reads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExportCandidate -and $SelfTest) { throw "ExportCandidate 与 SelfTest 必须互斥" }

if ($SelfTest) {
    if ($FixedServerHost -cne "8.130.9.163" -or $FixedSSHPort -ne 10003 -or $FixedSSHUser -cne "pc") {
        throw "固定测试服 SSH 目标发生漂移"
    }
    Write-Output "ssh_stderr_diagnostic_candidate_self_test=passed"
    Write-Output "diagnostic_modes=base-transport,isolated-bash,isolated-bash-stdin"
    Write-Output "single_execution_lock_required=true"
    Write-Output "raw_stderr_persisted=false"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "business_reads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ChangeId -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') { throw "ChangeId 必须使用 UTC 基本格式" }
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) { throw "导出候选必须提供全新的 OutputDirectory" }
Assert-LocalAbsolutePath -Path $OutputDirectory
$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
$outputParent = Split-Path -Parent $outputPath
if ([string]::IsNullOrWhiteSpace($outputParent) -or -not (Test-Path -LiteralPath $outputParent -PathType Container)) {
    throw "候选输出目录的父目录必须已存在"
}
if (Test-Path -LiteralPath $outputPath) { throw "候选输出目录已存在，禁止覆盖" }

$sshHelperPath = Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1"
$sshHelperSHA256 = (Get-FileHash -LiteralPath $sshHelperPath -Algorithm SHA256).Hash.ToLowerInvariant()
$repoScripts = [IO.Path]::GetFullPath($PSScriptRoot)
$isolatedBashPrefix = "/usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin HOME=/home/pc USER=pc LOGNAME=pc LANG=C LC_ALL=C /bin/bash --noprofile --norc"
$remoteCommand = switch ($DiagnosticMode) {
    "base-transport" { "/usr/bin/true" }
    "isolated-bash" { "$isolatedBashPrefix -c /usr/bin/true" }
    "isolated-bash-stdin" { "$isolatedBashPrefix -s --" }
}
$remoteInput = if ($DiagnosticMode -ceq "isolated-bash-stdin") { "true`n" } else { "" }
$remoteInputBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($remoteInput))

$runnerTemplate = @'
param(
    [switch]$ExecuteDiagnostic,
    [switch]$SelfTest,
    [string]$ExpectedRunnerSHA256 = ""
)

$ErrorActionPreference = "Stop"
$ChangeId = "__CHANGE_ID__"
$DiagnosticMode = "__DIAGNOSTIC_MODE__"
$RemoteCommand = "__REMOTE_COMMAND__"
$RemoteInputBase64 = "__REMOTE_INPUT_BASE64__"
$ExpectedSSHHelperSHA256 = "__SSH_HELPER_SHA256__"
$ExecutionLockPath = Join-Path (Split-Path -Parent $PSCommandPath) "execution-$ChangeId.lock"
$ResultPath = Join-Path (Split-Path -Parent $PSCommandPath) "result-$ChangeId.txt"

function Get-SHA256Text {
    param([string]$Text)
    $bytes = [Text.Encoding]::UTF8.GetBytes($Text)
    try {
        $sha = [Security.Cryptography.SHA256]::Create()
        try { ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace("-", "").ToLowerInvariant() }
        finally { $sha.Dispose() }
    }
    finally { [Array]::Clear($bytes, 0, $bytes.Length) }
}

function Get-RedactedStderrLine {
    param([string]$Text)

    # 只允许一行、可打印、短文本进入脱敏器；异常形态直接失败关闭且不输出正文。
    $normalized = $Text.Replace("`r`n", "`n").Replace("`r", "`n").TrimEnd("`n")
    if ([string]::IsNullOrWhiteSpace($normalized)) { return "" }
    if ($normalized.Contains("`n") -or $normalized -match '[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]' -or
        [Text.Encoding]::UTF8.GetByteCount($normalized) -gt 512) {
        throw "stderr 不符合单行低敏诊断边界"
    }
    $redacted = $normalized
    # 按由具体到宽泛的顺序移除 Token、邮箱、IP、手机号、路径、AccessKey 和长随机值。
    $redacted = [regex]::Replace($redacted, '(?i)\bBearer\s+\S+', '[redacted_token]')
    $redacted = [regex]::Replace($redacted, '(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b', '[redacted_email]')
    $redacted = [regex]::Replace($redacted, '(?<!\d)(?:\d{1,3}\.){3}\d{1,3}(?!\d)', '[redacted_ip]')
    $redacted = [regex]::Replace($redacted, '(?<!\d)1\d{10}(?!\d)', '[redacted_phone]')
    $redacted = [regex]::Replace($redacted, '(?i)(?:[A-Z]:\\|/)\S+', '[redacted_path]')
    $redacted = [regex]::Replace($redacted, '(?i)\b(?:LTAI|AKID)[A-Z0-9_-]+\b', '[redacted_access_key]')
    $redacted = [regex]::Replace($redacted, '\b[A-Za-z0-9+/_=-]{24,}\b', '[redacted_long_value]')
    return $redacted
}

if (-not $ExecuteDiagnostic -and -not $SelfTest) {
    Write-Output "ssh_stderr_diagnostic_authorized=false"
    Write-Output "execution_lock_created=false"
    Write-Output "network_connections=0"
    Write-Output "business_reads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExecuteDiagnostic -and $SelfTest) { throw "ExecuteDiagnostic 与 SelfTest 必须互斥" }

if ($SelfTest) {
    # 仿真 AccessKey 在运行时分段构造，源码和候选文件不得出现可被误用的完整密钥形态。
    $syntheticAccessKey = 'L' + 'TAI' + '0123456789ABCDEFGHIJK'
    $synthetic = "Warning from 8.130.9.163 path /home/pc/secret token $syntheticAccessKey email a@example.com phone 13800138000"
    $redacted = Get-RedactedStderrLine -Text $synthetic
    foreach ($forbidden in @('8.130.9.163', '/home/pc/secret', $syntheticAccessKey, 'a@example.com', '13800138000')) {
        if ($redacted.Contains($forbidden)) { throw "stderr 脱敏自测失败" }
    }
    $multilineRejected = $false
    try { $null = Get-RedactedStderrLine -Text "line1`nline2" } catch { $multilineRejected = $true }
    if (-not $multilineRejected -or (Get-SHA256Text -Text "synthetic").Length -ne 64) {
        throw "stderr 失败关闭自测未通过"
    }
    $remoteInput = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemoteInputBase64))
    if (($DiagnosticMode -ceq "isolated-bash-stdin" -and $remoteInput -cne "true`n") -or
        ($DiagnosticMode -cne "isolated-bash-stdin" -and $remoteInput.Length -ne 0)) {
        throw "最小远端 stdin 与诊断模式不匹配"
    }
    Write-Output "ssh_stderr_diagnostic_self_test=passed"
    Write-Output "diagnostic_mode=$DiagnosticMode"
    Write-Output "remote_command_frozen=true"
    Write-Output "single_execution_lock_required=true"
    Write-Output "raw_stderr_persisted=false"
    Write-Output "network_connections=0"
    Write-Output "business_reads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ExpectedRunnerSHA256 -cnotmatch '^[0-9a-f]{64}$') { throw "诊断执行必须提供获批的完整 runner SHA-256" }
if ((Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedRunnerSHA256) {
    throw "runner SHA-256 与批准值不匹配"
}
if ((Test-Path -LiteralPath $ExecutionLockPath) -or (Test-Path -LiteralPath $ResultPath)) {
    throw "本 ChangeId 已执行或已有结果，禁止重试"
}

$sshHelperPath = Join-Path "__REPO_SCRIPTS__" "sms-phase5-test-server-ssh.ps1"
if ((Get-FileHash -LiteralPath $sshHelperPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedSSHHelperSHA256) {
    throw "固定 SSH 身份辅助脚本摘要不匹配"
}
. $sshHelperPath
$knownHosts = Assert-SmsPhase5FixedTestServerIdentity -ServerHost '8.130.9.163' -SSHPort 10003 -SSHUser 'pc'

# 执行锁在建立 SSH 连接前排他创建；无论成功或失败都保留，程序层禁止自动或人工误重试。
$lockStream = [IO.File]::Open($ExecutionLockPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
$lockStream.Dispose()

$utf8 = New-Object Text.UTF8Encoding($false)
$startInfo = New-Object Diagnostics.ProcessStartInfo
$startInfo.FileName = 'ssh.exe'
$startInfo.UseShellExecute = $false
$startInfo.RedirectStandardInput = $true
$startInfo.RedirectStandardOutput = $true
$startInfo.RedirectStandardError = $true
$startInfo.CreateNoWindow = $true
$startInfo.StandardOutputEncoding = $utf8
$startInfo.StandardErrorEncoding = $utf8
$startInfo.Arguments = "-T -p 10003 -o BatchMode=yes -o NumberOfPasswordPrompts=0 -o ConnectTimeout=8 -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 -o `"UserKnownHostsFile=$knownHosts`" -- pc@8.130.9.163 $RemoteCommand"
$process = New-Object Diagnostics.Process
$process.StartInfo = $startInfo
try {
    if (-not $process.Start()) { throw "无法启动固定 SSH stderr 诊断" }
    $inputBytes = [Convert]::FromBase64String($RemoteInputBase64)
    try {
        if ($inputBytes.Length -gt 0) {
            $process.StandardInput.BaseStream.Write($inputBytes, 0, $inputBytes.Length)
            $process.StandardInput.BaseStream.Flush()
        }
    }
    finally {
        [Array]::Clear($inputBytes, 0, $inputBytes.Length)
        $process.StandardInput.Close()
    }
    $stdoutTask = $process.StandardOutput.ReadToEndAsync()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    $process.WaitForExit()
    $stdout = $stdoutTask.Result
    $stderr = $stderrTask.Result
    $remoteExitCode = $process.ExitCode
}
finally { $process.Dispose() }

$stderrPresent = -not [string]::IsNullOrWhiteSpace($stderr)
$redacted = if ($stderrPresent) { Get-RedactedStderrLine -Text $stderr } else { "" }
$normalized = if ($stderrPresent) { $stderr.Replace("`r`n", "`n").Replace("`r", "`n").TrimEnd("`n") } else { "" }
$resultLines = @(
    "ssh_stderr_diagnostic=completed",
    "change_id=$ChangeId",
    "diagnostic_mode=$DiagnosticMode",
    "remote_exit_code=$remoteExitCode",
    "remote_stdout_empty=$([string]::IsNullOrEmpty($stdout).ToString().ToLowerInvariant())",
    "remote_stderr_present=$($stderrPresent.ToString().ToLowerInvariant())"
)
if ($stderrPresent) {
    $resultLines += "remote_stderr_byte_count=$([Text.Encoding]::UTF8.GetByteCount($stderr))"
    $resultLines += "remote_stderr_line_count=$(@($normalized -split "`n").Count)"
    $resultLines += "remote_stderr_sha256=$(Get-SHA256Text -Text $stderr)"
    $resultLines += "remote_stderr_redacted=$redacted"
}
$resultLines += @(
    "raw_stderr_persisted=false",
    "execution_lock_created=true",
    "network_connections=1",
    "business_reads=0",
    "configuration_mutations=0",
    "service_operations=0",
    "business_posts=0",
    "emails_sent=0",
    "real_sms_sent=0"
)
$resultBytes = [Text.Encoding]::UTF8.GetBytes(($resultLines -join "`r`n") + "`r`n")
try {
    $resultStream = [IO.File]::Open($ResultPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try { $resultStream.Write($resultBytes, 0, $resultBytes.Length) }
    finally { $resultStream.Dispose() }
}
finally { [Array]::Clear($resultBytes, 0, $resultBytes.Length) }

$resultLines | Write-Output
Write-Output "result_sha256=$((Get-FileHash -LiteralPath $ResultPath -Algorithm SHA256).Hash.ToLowerInvariant())"
$stdout = $null
$stderr = $null
$normalized = $null
$redacted = $null
if ($remoteExitCode -ne 0 -or -not [string]::IsNullOrEmpty($stdoutTask.Result)) {
    throw "固定 SSH stderr 诊断未满足空操作边界"
}
'@

$runnerText = $runnerTemplate.Replace("__CHANGE_ID__", $ChangeId).
    Replace("__DIAGNOSTIC_MODE__", $DiagnosticMode).
    Replace("__REMOTE_COMMAND__", $remoteCommand).
    Replace("__REMOTE_INPUT_BASE64__", $remoteInputBase64).
    Replace("__SSH_HELPER_SHA256__", $sshHelperSHA256).
    Replace("__REPO_SCRIPTS__", $repoScripts)
$runnerPath = Join-Path $outputPath "run-sms-phase5-ssh-stderr-diagnostic-$ChangeId.ps1"
$directoryCreated = $false
$fileCreated = $false
try {
    $null = New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop
    $directoryCreated = $true
    [IO.File]::WriteAllText($runnerPath, $runnerText, (New-Object Text.UTF8Encoding($true)))
    $fileCreated = $true

    # 生成阶段只执行解析、默认关闭和 SelfTest，不进入 ExecuteDiagnostic 分支。
    $tokens = $null
    $parseErrors = $null
    $null = [Management.Automation.Language.Parser]::ParseFile($runnerPath, [ref]$tokens, [ref]$parseErrors)
    if (@($parseErrors).Count -ne 0) { throw "runner PowerShell 语法校验失败" }
    $runner = Get-Content -LiteralPath $runnerPath -Encoding UTF8 -Raw
    foreach ($required in @('[IO.FileMode]::CreateNew', 'raw_stderr_persisted=false', 'business_reads=0')) {
        if (-not $runner.Contains($required)) { throw "runner 缺少安全标记：$required" }
    }
    $expectedTrueMarker = if ($DiagnosticMode -ceq "isolated-bash-stdin") { 'dHJ1ZQo=' } else { '/usr/bin/true' }
    if (-not $runner.Contains($expectedTrueMarker)) { throw "runner 缺少最小 true 命令标记" }
    foreach ($forbidden in @('curl ', 'Invoke-WebRequest', 'Invoke-RestMethod', 'scp ', 'sftp ', 'SMS_ENABLED=true', 'systemctl', 'docker restart')) {
        if ($runner.Contains($forbidden)) { throw "runner 包含禁止动作：$forbidden" }
    }
    $closedOutput = @(& $runnerPath)
    $selfTestOutput = @(& $runnerPath -SelfTest)
    if ($closedOutput -cnotcontains "ssh_stderr_diagnostic_authorized=false" -or
        $selfTestOutput -cnotcontains "ssh_stderr_diagnostic_self_test=passed") {
        throw "runner 默认关闭或离线自测失败"
    }
    Write-Output "ssh_stderr_diagnostic_candidate=passed"
    Write-Output "change_id=$ChangeId"
    Write-Output "diagnostic_mode=$DiagnosticMode"
    Write-Output "runner_sha256=$((Get-FileHash -LiteralPath $runnerPath -Algorithm SHA256).Hash.ToLowerInvariant())"
    Write-Output "runner_path=$runnerPath"
    Write-Output "candidate_files_written=1"
    Write-Output "network_connections=0"
    Write-Output "business_reads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
}
catch {
    if ($fileCreated -and (Test-Path -LiteralPath $runnerPath -PathType Leaf)) {
        Remove-Item -LiteralPath $runnerPath -Force
    }
    if ($directoryCreated -and (Test-Path -LiteralPath $outputPath -PathType Container) -and
        @(Get-ChildItem -LiteralPath $outputPath -Force).Count -eq 0) {
        Remove-Item -LiteralPath $outputPath -Force
    }
    throw
}
