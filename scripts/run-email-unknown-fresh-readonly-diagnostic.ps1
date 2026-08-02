[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)][switch]$SelfTest,
    [Parameter(Mandatory = $false)][switch]$Execute,
    [Parameter(Mandatory = $false)][string]$Confirm
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:ConfirmPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_RETAINED_STAGE_READONLY_DIAGNOSTIC_ONCE'
$script:PayloadPath = Join-Path $PSScriptRoot 'email-unknown-fresh-readonly-diagnostic.payload.sh'
$script:PayloadSHA = '8a99e4b52c1b32413b2bac59c4f3dac169e37e482145538fb3ac62307644ddf5'
$script:Remote = 'pc@8.130.9.163'
$script:OldBinarySHA = '98ce22c62a61ddd3d2a8cc9bae73f21fd0e36d240e873684d1626d68ef450e45'
$script:OldPayloadSHA = 'a4beccd8ed9fb0bfe7d5e23b01d550edd0798cb815e3b34162fda7ffce113d2e'
$script:OperatorID = '259'
$script:FailurePattern = '^status=failed classification=(?<classification>argument_gate|stage_count|stage_identity|file_identity|asset_hash|state_parse|api_identity|container_identity|database_snapshot|fixture_ownership|redis_ping|redis_identity|redis_exact_exists) phase=(?<phase>unknown|initializing|phase1_created) stage_count=(?<stage_count>[0-9]+) stage_identity=(?<stage_identity>true|false) file_count=(?<file_count>[0-9]+) files_identity=(?<files_identity>true|false) hashes_match=(?<hashes_match>true|false) state_class=(?<state_class>unknown|partial|complete) state_identity=(?<state_identity>true|false) stage_nonce_match=(?<stage_nonce_match>true|false) writes=false cleanup=false restart=false retries=0\r?\n?$'
$script:SuccessPattern = '^status=pass classification=diagnostic_complete phase=(?<phase>initializing|phase1_created) stage_count=(?<stage_count>[0-9]+) stage_identity=(?<stage_identity>true|false) file_count=(?<file_count>[0-9]+) files_identity=(?<files_identity>true|false) hashes_match=(?<hashes_match>true|false) state_class=(?<state_class>partial|complete) state_identity=(?<state_identity>true|false) stage_nonce_match=(?<stage_nonce_match>true|false) schema=(?<schema>[0-9]+) dirty=(?<dirty>[0-9]+) migration_rows=(?<migration_rows>[0-9]+) operator_rows=(?<operator_rows>[0-9]+) singular_table_rows=(?<singular_table_rows>[0-9]+) plural_table_rows=(?<plural_table_rows>[0-9]+) template_rows=(?<template_rows>[0-9]+) allowlist_rows=(?<allowlist_rows>[0-9]+) send_log_rows=(?<send_log_rows>[0-9]+) scope_rows=(?<scope_rows>[0-9]+) redis_ping=(?<redis_ping>true|false) redis_identity=(?<redis_identity>true|false) redis_key_exists=(?<redis_key_exists>[0-9]+) writes=false cleanup=false restart=false retries=0\r?\n?$'

function Read-VerifiedPayloadBytes {
    $path = [IO.Path]::GetFullPath($script:PayloadPath)
    $root = [IO.Path]::GetFullPath($PSScriptRoot).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $item = [IO.FileInfo]::new($path)
    if (-not $item.Exists -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        $item.DirectoryName -cne $root -or $item.FullName -cne $path) {
        throw 'payload_identity_invalid'
    }
    $bytes = [IO.File]::ReadAllBytes($path)
    if ($bytes.Length -lt 256 -or ($bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) -or $bytes -contains 0) {
        throw 'payload_encoding_invalid'
    }
    $sha = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($sha -cne $script:PayloadSHA) { throw 'payload_hash_invalid' }
    return ,$bytes
}

function Assert-ReadonlyContract {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $text = [Text.UTF8Encoding]::new($false, $true).GetString($Bytes)
    foreach ($required in @(
        'email-unknown-cycle-', 'email_test_recipient_allowlist', 'email_test_recipient_allowlists',
        'redis-cli -n "$REDIS_DB" --raw PING', 'redis-cli -n "$REDIS_DB" --raw INFO server',
        'redis-cli -n "$REDIS_DB" --raw EXISTS "$exact_key"', 'stage_nonce_match=',
        'writes=false cleanup=false restart=false retries=0'
    )) {
        if (-not $text.Contains($required)) { throw 'payload_contract_missing' }
    }
    foreach ($forbidden in @(
        'rm -', 'unlink ', 'docker restart', 'docker stop', 'docker kill', 'docker rm',
        'DELETE ', 'UPDATE ', 'INSERT ', 'REPLACE ', 'ALTER ', 'DROP ', 'TRUNCATE ',
        'FLUSHDB', 'FLUSHALL', 'redis-cli KEYS', 'redis-cli SCAN', 'redis-cli DEL', 'redis-cli UNLINK',
        'EMAIL_UNKNOWN_RESTART_PHASE=phase1', 'cleanup_phase1', 'SingleSendMail'
    )) {
        if ($text.Contains($forbidden)) { throw 'payload_contract_forbidden' }
    }
    if ([regex]::Matches($text, '(?m)^snapshot=\$\(/usr/bin/docker exec').Count -ne 1 -or
        [regex]::Matches($text, '(?m)^redis_ping=\$\(').Count -ne 1 -or
        [regex]::Matches($text, '(?m)^current_run_id=\$\(').Count -ne 1 -or
        [regex]::Matches($text, '(?m)^redis_key_exists=\$\(').Count -ne 1) {
        throw 'payload_command_count_invalid'
    }
}

function New-RestrictedTempDirectory {
    $root = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $path = [IO.Path]::GetFullPath((Join-Path $root ('molin-email-retained-readonly-' + [Guid]::NewGuid().ToString('N'))))
    if (-not $path.StartsWith($root + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -or
        [IO.Directory]::Exists($path) -or [IO.File]::Exists($path)) { throw 'temp_identity_invalid' }
    [void][IO.Directory]::CreateDirectory($path)
    return $path
}

function Remove-RestrictedTempDirectory {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not ([IO.Path]::GetFileName($Path) -cmatch '^molin-email-retained-readonly-[a-f0-9]{32}$')) { throw 'temp_cleanup_identity_invalid' }
    foreach ($name in @('stdin.bin', 'stdout.bin', 'stderr.bin')) {
        $target = Join-Path $Path $name
        if ([IO.File]::Exists($target)) { [IO.File]::Delete($target) }
    }
    if ([IO.Directory]::Exists($Path)) {
        if ([IO.Directory]::GetFileSystemEntries($Path).Length -ne 0) { throw 'temp_cleanup_not_empty' }
        [IO.Directory]::Delete($Path, $false)
    }
}

function New-CaptureResult {
    param(
        [Parameter(Mandatory = $true)][int]$ExitCode,
        [Parameter(Mandatory = $true)][int]$StdoutBytes,
        [Parameter(Mandatory = $true)][int]$StderrBytes,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stdout
    )
    # Windows PowerShell 5.1 会把空集合展开为无输出，必须直接构造带字段的非空对象。
    $result = $null
    $result = [pscustomobject]([ordered]@{ ExitCode = $ExitCode; StdoutBytes = $StdoutBytes; StderrBytes = $StderrBytes; Stdout = $Stdout })
    if ($null -eq $result) { throw 'capture_factory_missing' }
    return $result
}

function Invoke-CapturedProcess {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][byte[]]$Payload,
        [Parameter(Mandatory = $true)][int]$TimeoutMilliseconds
    )
    if (-not [IO.File]::Exists($FilePath) -or $Arguments.Count -lt 2 -or $Payload.Length -lt 1 -or
        $TimeoutMilliseconds -lt 1000) { throw 'captured_process_arguments_invalid' }
    foreach ($argument in $Arguments) {
        if ([string]::IsNullOrWhiteSpace($argument) -or $argument -match '[\s"'']') {
            throw 'captured_process_argument_unsafe'
        }
    }
    $temporary = New-RestrictedTempDirectory
    $stdin = Join-Path $temporary 'stdin.bin'
    $stdout = Join-Path $temporary 'stdout.bin'
    $stderr = Join-Path $temporary 'stderr.bin'
    $process = $null
    $rawExitCode = $null
    $exitCodeValue = $null
    $stdoutLengthValue = $null
    $stderrLengthValue = $null
    $stdoutTextValue = $null
    try {
        [IO.File]::WriteAllBytes($stdin, $Payload)
        if ([IO.FileInfo]::new($stdin).Length -ne $Payload.Length) { throw 'captured_process_stdin_invalid' }
        $startParameters = @{
            FilePath = $FilePath
            ArgumentList = $Arguments
            RedirectStandardInput = $stdin
            RedirectStandardOutput = $stdout
            RedirectStandardError = $stderr
            NoNewWindow = $true
            PassThru = $true
        }
        $process = Microsoft.PowerShell.Management\Start-Process @startParameters
        try { $handle = $process.Handle; if ($handle -eq [IntPtr]::Zero) { throw 'process_handle_invalid' } }
        catch { try { if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() } } catch { }; throw }
        if (-not $process.WaitForExit($TimeoutMilliseconds)) { $process.Kill(); $process.WaitForExit(); throw 'process_timeout' }
        # 有界等待确认退出后再完成一次无参等待，确保 Windows PowerShell 刷新异步重定向与 ExitCode。
        $process.WaitForExit()
        $process.Refresh()
        $stdoutBytes = [IO.File]::ReadAllBytes($stdout)
        $stderrBytes = [IO.File]::ReadAllBytes($stderr)
        $stdoutText = [Text.UTF8Encoding]::new($false, $true).GetString($stdoutBytes)
        [void][Text.UTF8Encoding]::new($false, $true).GetString($stderrBytes)
        # 先读取原始属性再单独转换，避免直接强制转换被解析为类型成员访问。
        try { $rawExitCode = $process.ExitCode } catch { throw 'process_exit_code_unavailable' }
        if ($null -eq $rawExitCode) { throw 'process_exit_code_missing' }
        $exitCodeValue = [int]$rawExitCode
        $stdoutLengthValue = [int]$stdoutBytes.Length
        $stderrLengthValue = [int]$stderrBytes.Length
        $stdoutTextValue = $stdoutText
    } finally {
        if ($null -ne $process) { $process.Dispose() }
        Remove-RestrictedTempDirectory -Path $temporary
    }
    if ($null -eq $exitCodeValue) { throw 'capture_exit_missing' }
    if ($null -eq $stdoutLengthValue) { throw 'capture_stdout_length_missing' }
    if ($null -eq $stderrLengthValue) { throw 'capture_stderr_length_missing' }
    if ($null -eq $stdoutTextValue) { throw 'capture_stdout_missing' }
    $captureResult = New-CaptureResult -ExitCode $exitCodeValue -StdoutBytes $stdoutLengthValue -StderrBytes $stderrLengthValue -Stdout $stdoutTextValue
    if ($captureResult.ExitCode -ne $exitCodeValue) { throw 'capture_exit_value_changed' }
    return $captureResult
}

function Invoke-OneSSH {
    param([Parameter(Mandatory = $true)][byte[]]$Payload)
    $ssh = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
    if (-not [IO.File]::Exists($ssh)) { throw 'ssh_missing' }
    $arguments = @(
        '-T', '-p', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0',
        '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', $script:Remote,
        '/usr/bin/env', '-i', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'HOME=/home/pc',
        'USER=pc', 'LOGNAME=pc', 'LANG=C.UTF-8', '/usr/bin/timeout', '--signal=TERM',
        '--kill-after=5s', '180s', '/bin/bash', '--noprofile', '--norc', '-s', '--',
        $script:OldBinarySHA, $script:OldPayloadSHA, $script:OperatorID
    )
    return Invoke-CapturedProcess -FilePath $ssh -Arguments $arguments -Payload $Payload -TimeoutMilliseconds 210000
}

function ConvertTo-SafeSummary {
    param([Parameter(Mandatory = $true)]$Result)
    $fields = [ordered]@{
        status = 'pass'
        mode = 'email_unknown_retained_readonly_diagnostic'
        classification = 'output_structure_invalid'
        phase = 'unknown'
        exit_code = $Result.ExitCode
        stdout_length = $Result.StdoutBytes
        stderr_length = $Result.StderrBytes
        ssh_attempts = 1
        retries = 0
        writes = $false
        cleanup = $false
        restart = $false
        remote_artifact = $false
        retained = $true
    }
    $success = [regex]::Match($Result.Stdout, $script:SuccessPattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    $failure = [regex]::Match($Result.Stdout, $script:FailurePattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if ($success.Success -and $Result.ExitCode -eq 0) {
        $fields.classification = if ($Result.StderrBytes -eq 0) { 'diagnostic_complete' } else { 'diagnostic_complete_with_transport_stderr' }
        foreach ($name in @('phase','stage_count','stage_identity','file_count','files_identity','hashes_match','state_class','state_identity','stage_nonce_match','schema','dirty','migration_rows','operator_rows','singular_table_rows','plural_table_rows','template_rows','allowlist_rows','send_log_rows','scope_rows','redis_ping','redis_identity','redis_key_exists')) {
            $fields[$name] = $success.Groups[$name].Value
        }
    }
    elseif ($failure.Success -and $Result.ExitCode -eq 2) {
        $fields.classification = $failure.Groups['classification'].Value
        foreach ($name in @('phase','stage_count','stage_identity','file_count','files_identity','hashes_match','state_class','state_identity','stage_nonce_match')) {
            $fields[$name] = $failure.Groups[$name].Value
        }
    }
    elseif ($Result.ExitCode -ne 0) {
        $fields.classification = 'transport_or_protocol_failure'
    }
    return ($fields | ConvertTo-Json -Compress)
}

if ($SelfTest) {
    if ($Execute -or $Confirm) { throw 'selftest_arguments_invalid' }
    $payload = Read-VerifiedPayloadBytes
    Assert-ReadonlyContract -Bytes $payload
    $bash = 'C:\Program Files\Git\bin\bash.exe'
    if (-not [IO.File]::Exists($bash)) { throw 'bash_missing' }
    & $bash -n $script:PayloadPath
    if ($LASTEXITCODE -ne 0) { throw 'payload_syntax_invalid' }
    $mock = [pscustomobject]@{
        ExitCode = 2
        StdoutBytes = 219
        StderrBytes = 0
        Stdout = 'status=failed classification=database_snapshot phase=phase1_created stage_count=1 stage_identity=true file_count=3 files_identity=true hashes_match=true state_class=complete state_identity=true stage_nonce_match=false writes=false cleanup=false restart=false retries=0' + "`n"
    }
    $mockSummary = ConvertTo-SafeSummary -Result $mock | ConvertFrom-Json
    if ($mockSummary.classification -cne 'database_snapshot' -or $mockSummary.exit_code -ne 2 -or $mockSummary.ssh_attempts -ne 1) {
        throw 'summary_regression'
    }
    $captureMock = New-CaptureResult -ExitCode 2 -StdoutBytes 219 -StderrBytes 0 -Stdout $mock.Stdout
    if ($null -eq $captureMock -or $captureMock.GetType().FullName -cne 'System.Management.Automation.PSCustomObject' -or
        $captureMock.ExitCode -ne 2 -or $captureMock.StdoutBytes -ne 219 -or $captureMock.StderrBytes -ne 0 -or
        $captureMock.Stdout -cne $mock.Stdout) {
        throw 'capture_shape_regression'
    }
    # 使用本地 Bash 走完与正式 SSH 相同的文件重定向、进程等待、捕获、解析和 finally 清理链路。
    $beforeTemps = @(Get-ChildItem ([IO.Path]::GetTempPath()) -Directory -Filter 'molin-email-retained-readonly-*' -ErrorAction SilentlyContinue).Count
    $fixtureScript = "printf '%s\n' '$($mock.Stdout.Trim())'`nexit 2`n"
    $fixtureBytes = [Text.UTF8Encoding]::new($false, $true).GetBytes($fixtureScript)
    $pipelineCapture = Invoke-CapturedProcess -FilePath $bash -Arguments @('-s', '--') -Payload $fixtureBytes -TimeoutMilliseconds 10000
    $afterTemps = @(Get-ChildItem ([IO.Path]::GetTempPath()) -Directory -Filter 'molin-email-retained-readonly-*' -ErrorAction SilentlyContinue).Count
    $pipelineSummary = ConvertTo-SafeSummary -Result $pipelineCapture | ConvertFrom-Json
    if ($pipelineCapture.ExitCode -ne 2) {
        throw ('local_pipeline_exit_regression_' + [string]$pipelineCapture.ExitCode + '_stdout_' +
            [string]$pipelineCapture.StdoutBytes + '_stderr_' + [string]$pipelineCapture.StderrBytes)
    }
    if ($pipelineCapture.StderrBytes -ne 0) { throw 'local_pipeline_stderr_regression' }
    if ($pipelineCapture.Stdout -cne $mock.Stdout) { throw 'local_pipeline_stdout_regression' }
    if ($pipelineSummary.classification -cne 'database_snapshot') { throw 'local_pipeline_summary_regression' }
    if ($beforeTemps -ne $afterTemps) { throw 'local_pipeline_cleanup_regression' }
    Write-Output 'status=pass mode=email_unknown_retained_readonly_diagnostic_selftest external_access=false writes=false cleanup=false restart=false retries=0'
    exit 0
}

if (-not $Execute -or $Confirm -cne $script:ConfirmPhrase) { throw 'confirmation_required' }
$payload = Read-VerifiedPayloadBytes
Assert-ReadonlyContract -Bytes $payload
# 正式路径只调用一次 SSH；没有循环、SCP、重试或第二阶段分支。
$result = $null
$result = Invoke-OneSSH -Payload $payload
if ($null -eq $result) { throw 'capture_result_missing' }
Write-Output (ConvertTo-SafeSummary -Result $result)
