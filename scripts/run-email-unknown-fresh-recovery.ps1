[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)][switch]$SelfTest,
    [Parameter(Mandatory = $false)][switch]$PreflightOnly,
    [Parameter(Mandatory = $false)][switch]$UploadedBinaryPreflightOnly,
    [Parameter(Mandatory = $false)][switch]$ResumeUploadedCleanup,
    [Parameter(Mandatory = $false)][switch]$Execute,
    [Parameter(Mandatory = $false)][string]$Confirm,
    [Parameter(Mandatory = $false)][string]$RecoveryBinaryPath
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:RecoveryConfirmPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_STAGE_NONCE_MISMATCH_EXACT_RECOVERY_ONCE'
$script:DiagnosticConfirmPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_STAGE_NONCE_MISMATCH_PREFLIGHT_METADATA_ONCE'
$script:UploadedBinaryDiagnosticConfirmPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_UPLOADED_BINARY_PREFLIGHT_METADATA_ONCE'
$script:ResumeUploadedCleanupConfirmPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_RESUME_UPLOADED_BINARY_EXACT_CLEANUP_ONCE'
$script:PayloadPath = Join-Path $PSScriptRoot 'email-unknown-fresh-recovery.payload.sh'
$script:PayloadSHA = '2e88d58e463c4540383d25405beb844ca4c147a1687a19d2e11768e57e4f3876'
$script:Remote = 'pc@8.130.9.163'
$script:OldBinarySHA = '98ce22c62a61ddd3d2a8cc9bae73f21fd0e36d240e873684d1626d68ef450e45'
$script:OldPayloadSHA = 'a4beccd8ed9fb0bfe7d5e23b01d550edd0798cb815e3b34162fda7ffce113d2e'
$script:RecoveryBinarySHA = '1179e29d9f43efea79f185e8d2319d015a627f69a48ef9ed7ce22e72ba6ad900'
$script:RecoveryBinarySize = 25573597
$script:OperatorID = '259'
$script:PreflightPattern = '^status=pass stage=mismatch_recovery_preflight operation_id=(?<operation>[a-f0-9]{32}) state_class=complete state_phase=phase1_created stage_nonce_match=false fixture_ownership=true redis_identity=true redis_key_exists=0 schema=57 writes=false retries=0\r?\n?$'
$script:CleanupPattern = '^status=pass stage=mismatch_recovery_cleanup operation_id=(?<operation>[a-f0-9]{32}) db_rows=0 scope_rows=0 state_absent=true artifacts_absent=true api_ready=true stage_nonce_match=false redis_delete=false retries=0\r?\n?$'
$script:UploadedBinaryPreflightPattern = '^status=pass stage=mismatch_recovery_uploaded_binary_preflight operation_id=(?<operation>[a-f0-9]{32}) state_class=complete state_phase=phase1_created stage_nonce_match=false fixture_ownership=true redis_identity=true redis_key_exists=0 binary_regular=true binary_symlink=false binary_owner=true binary_mode=(?<binary_mode>500|600|644|700|755|other) binary_hash_match=(?<binary_hash_match>true|false) retained=true writes=false retries=0\r?\n?$'

function Get-PayloadText {
    $path = [IO.Path]::GetFullPath($script:PayloadPath)
    if (-not [IO.File]::Exists($path) -or ([IO.FileInfo]::new($path).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw 'payload_invalid'
    }
    $bytes = [IO.File]::ReadAllBytes($path)
    if ($bytes.Length -lt 32 -or ($bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) -or $bytes -contains 0) {
        throw 'payload_encoding'
    }
    $text = [Text.UTF8Encoding]::new($false, $true).GetString($bytes)
    if ($text.Contains("`r") -or -not $text.StartsWith("#!/usr/bin/env bash`n", [StringComparison]::Ordinal) -or
        -not $text.Contains("`nset -Eeuo pipefail`n")) {
        throw 'payload_format'
    }
    if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -cne $script:PayloadSHA) {
        throw 'payload_hash'
    }
    return $text
}

function Assert-PayloadContract {
    param([Parameter(Mandatory = $true)][string]$Text)
    foreach ($required in @(
        "stage_nonce_match=false", "fixture_ownership=true", "cleanup_phase1", "phase1_created", "unexpected_send_log_id",
        'email_provider_templates WHERE id=${template_id}',
        'email_test_recipient_allowlist WHERE id=${allowlist_id}',
        'email_send_logs WHERE id=${send_log_id}',
        'rm -f -- "$old_binary" "$old_payload" "$recovery_binary"',
        'redis_key_exists=0', 'redis_delete=false', 'retries=0'
    )) {
        if (-not $Text.Contains($required)) { throw 'payload_contract' }
    }
    if ($Text -match '(?im)\b(?:FLUSHDB|FLUSHALL|KEYS|SCAN)\b' -or
        $Text -match '(?im)redis-cli[^\r\n]*(?:\bDEL\b|\bUNLINK\b|--scan)' -or
        $Text.Contains('docker restart') -or $Text.Contains('mysqldump')) {
        throw 'payload_forbidden'
    }
}

function Get-SHA256Lower {
    param([Parameter(Mandatory = $true)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Invoke-FixedProcess {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$InputText,
        [Parameter(Mandatory = $true)][int]$TimeoutMilliseconds
    )
    $normalized = $InputText.Replace("`r`n", "`n").Replace("`r", "`n")
    if ($normalized.IndexOf([char]0) -ge 0 -or ($normalized.Length -gt 0 -and [int][char]$normalized[0] -in @(0xFEFF, 0xFFFE))) {
        throw 'process_input_invalid'
    }
    $root = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $temporary = [IO.Path]::GetFullPath((Join-Path $root ('molin-email-fresh-recovery-' + [Guid]::NewGuid().ToString('N'))))
    if (-not $temporary.StartsWith($root + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -or
        [IO.Directory]::Exists($temporary) -or [IO.File]::Exists($temporary)) { throw 'process_temp_invalid' }
    [void][IO.Directory]::CreateDirectory($temporary)
    $stdin = Join-Path $temporary 'stdin.txt'
    $stdout = Join-Path $temporary 'stdout.txt'
    $stderr = Join-Path $temporary 'stderr.txt'
    $process = $null
    try {
        [IO.File]::WriteAllBytes($stdin, [Text.UTF8Encoding]::new($false, $true).GetBytes($normalized))
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
        try { $handle = $process.Handle; if ($handle -eq [IntPtr]::Zero) { throw 'process_handle' } }
        catch { try { if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() } } catch { }; throw 'process_handle' }
        if (-not $process.WaitForExit($TimeoutMilliseconds)) { $process.Kill(); $process.WaitForExit(); throw 'process_timeout' }
        $process.Refresh()
        $output = [Text.UTF8Encoding]::new($false, $true).GetString([IO.File]::ReadAllBytes($stdout))
        $errorOutput = [Text.UTF8Encoding]::new($false, $true).GetString([IO.File]::ReadAllBytes($stderr))
        if ($process.ExitCode -ne 0) {
            $failurePattern = '^status=failed stage=(?<remote_stage>argument_gate|stage_count|stage_path|stage_identity|file_identity|old_binary_hash|old_payload_hash|stage_shape|state_parse|state_nonce_relation|api_identity|required_environment|test_environment|container_identity|fixture_query|fixture_ownership|redis_identity|redis_exact_exists|cleanup_stage_shape|cleanup_state|recovery_binary_identity|recovery_binary_hash|cleanup_binary|cleanup_summary|state_remove|cleanup_postcheck_query|cleanup_postcheck|artifact_remove|stage_remove|api_health|api_ready) retained=true writes=unknown retries=0\r?\n?$'
            if ($process.ExitCode -eq 2 -and $output -cmatch $failurePattern) {
                $exception = [InvalidOperationException]::new('remote_gate_failed')
                $exception.Data['RemoteStage'] = $Matches.remote_stage
                $exception.Data['ExitCode'] = $process.ExitCode
                $exception.Data['StdoutLength'] = $output.Length
                $exception.Data['StderrLength'] = $errorOutput.Length
                throw $exception
            }
            $exception = [InvalidOperationException]::new('remote_gate_failed')
            $exception.Data['RemoteStage'] = 'exit_nonzero'
            $exception.Data['ExitCode'] = $process.ExitCode
            $exception.Data['StdoutLength'] = $output.Length
            $exception.Data['StderrLength'] = $errorOutput.Length
            throw $exception
        }
        if ($errorOutput.Length -ne 0) {
            $exception = [InvalidOperationException]::new('remote_gate_failed')
            $exception.Data['RemoteStage'] = 'stderr_nonempty'
            $exception.Data['ExitCode'] = $process.ExitCode
            $exception.Data['StdoutLength'] = $output.Length
            $exception.Data['StderrLength'] = $errorOutput.Length
            throw $exception
        }
        return $output
    }
    finally {
        if ($null -ne $process) { $process.Dispose() }
        foreach ($path in @($stdin, $stdout, $stderr)) {
            if ([IO.File]::Exists($path)) {
                $item = [IO.FileInfo]::new($path)
                if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.DirectoryName -cne $temporary) { throw 'temp_cleanup_invalid' }
                [IO.File]::Delete($path)
            }
        }
        if ([IO.Directory]::Exists($temporary)) {
            if ([IO.Directory]::GetFileSystemEntries($temporary).Length -ne 0) { throw 'temp_cleanup_not_empty' }
            [IO.Directory]::Delete($temporary, $false)
        }
    }
}

function Invoke-RemotePayload {
    param(
        [Parameter(Mandatory = $true)][string]$SshExe,
        [Parameter(Mandatory = $true)][string]$Payload,
        [Parameter(Mandatory = $true)][string[]]$TailArguments,
        [Parameter(Mandatory = $true)][int]$TimeoutMilliseconds
    )
    foreach ($argument in $TailArguments) {
        if ($argument -cnotmatch '^[A-Za-z0-9_./:-]+$') { throw 'remote_argument_invalid' }
    }
    $arguments = @(
        '-T', '-p', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0',
        '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', $script:Remote,
        '/usr/bin/env', '-i', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'HOME=/home/pc',
        'USER=pc', 'LOGNAME=pc', 'LANG=C.UTF-8', '/usr/bin/timeout', '--signal=TERM',
        '--kill-after=5s', '240s', '/bin/bash', '--noprofile', '--norc', '-s', '--'
    ) + $TailArguments
    return Invoke-FixedProcess -FilePath $SshExe -Arguments $arguments -InputText $Payload -TimeoutMilliseconds $TimeoutMilliseconds
}

function Invoke-SelfTest {
    $payload = Get-PayloadText
    Assert-PayloadContract -Text $payload
    $bash = 'C:\Program Files\Git\bin\bash.exe'
    if (-not [IO.File]::Exists($bash)) { throw 'bash_missing' }
    & $bash -n $script:PayloadPath
    if ($LASTEXITCODE -ne 0) { throw 'bash_syntax' }
    $transportProbe = Invoke-FixedProcess -FilePath $bash -Arguments @('-c', 'cat') -InputText "transport-probe`n" -TimeoutMilliseconds 10000
    if ($transportProbe -cne "transport-probe`n") { throw 'transport_probe' }
    if ($script:PreflightPattern -notmatch 'stage_nonce_match=false' -or $script:CleanupPattern -notmatch 'scope_rows=0') { throw 'parser_contract' }
    Write-Output 'status=pass mode=email_unknown_stage_nonce_mismatch_recovery_selftest external_access=false writes=false cleanup=false retries=0'
}

if ($SelfTest) {
    if ($PreflightOnly -or $UploadedBinaryPreflightOnly -or $ResumeUploadedCleanup -or $Execute -or $Confirm -or $RecoveryBinaryPath) { throw 'selftest_arguments' }
    Invoke-SelfTest
    exit 0
}
if (-not $Execute) { throw 'confirmation_required' }
if (($PreflightOnly -and $UploadedBinaryPreflightOnly) -or ($PreflightOnly -and $ResumeUploadedCleanup) -or ($UploadedBinaryPreflightOnly -and $ResumeUploadedCleanup)) { throw 'confirmation_required' }
if ($ResumeUploadedCleanup) {
    if ($Confirm -cne $script:ResumeUploadedCleanupConfirmPhrase -or -not [string]::IsNullOrWhiteSpace($RecoveryBinaryPath)) { throw 'confirmation_required' }
} elseif ($UploadedBinaryPreflightOnly) {
    if ($Confirm -cne $script:UploadedBinaryDiagnosticConfirmPhrase -or -not [string]::IsNullOrWhiteSpace($RecoveryBinaryPath)) { throw 'confirmation_required' }
} elseif ($PreflightOnly) {
    if ($Confirm -cne $script:DiagnosticConfirmPhrase -or -not [string]::IsNullOrWhiteSpace($RecoveryBinaryPath)) { throw 'confirmation_required' }
} else {
    if ($Confirm -cne $script:RecoveryConfirmPhrase) { throw 'confirmation_required' }
    if ([string]::IsNullOrWhiteSpace($RecoveryBinaryPath)) { throw 'recovery_binary_required' }
}
$payload = Get-PayloadText
Assert-PayloadContract -Text $payload
$binary = $null
$recoverySHA = $script:RecoveryBinarySHA
if (-not $PreflightOnly -and -not $UploadedBinaryPreflightOnly -and -not $ResumeUploadedCleanup) {
    $binary = [IO.Path]::GetFullPath($RecoveryBinaryPath)
    if (-not [IO.File]::Exists($binary) -or ([IO.FileInfo]::new($binary).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'recovery_binary_invalid' }
    $binaryBytes = [IO.File]::ReadAllBytes($binary)
    if ($binaryBytes.Length -ne $script:RecoveryBinarySize -or $binaryBytes[0] -ne 0x7F -or $binaryBytes[1] -ne 0x45 -or $binaryBytes[2] -ne 0x4C -or $binaryBytes[3] -ne 0x46) { throw 'recovery_binary_invalid' }
    $recoverySHA = Get-SHA256Lower -Path $binary
    if ($recoverySHA -cne $script:RecoveryBinarySHA) { throw 'recovery_binary_hash' }
}
$ssh = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
$scp = Join-Path $env:WINDIR 'System32\OpenSSH\scp.exe'
if (-not [IO.File]::Exists($ssh) -or (-not $PreflightOnly -and -not $UploadedBinaryPreflightOnly -and -not $ResumeUploadedCleanup -and -not [IO.File]::Exists($scp))) { throw 'ssh_tool_missing' }
$operation = 'unknown'
$currentStage = 'preflight'
$mode = if ($ResumeUploadedCleanup) { 'email_unknown_resume_uploaded_binary_exact_cleanup' } elseif ($UploadedBinaryPreflightOnly) { 'email_unknown_uploaded_binary_preflight_metadata' } elseif ($PreflightOnly) { 'email_unknown_stage_nonce_mismatch_preflight_metadata' } else { 'email_unknown_stage_nonce_mismatch_recovery' }
$sshAttempts = 0
$scpAttempts = 0
try {
    $sshAttempts++
    $remoteAction = if ($UploadedBinaryPreflightOnly -or $ResumeUploadedCleanup) { 'uploaded_preflight' } else { 'preflight' }
    $preflight = Invoke-RemotePayload -SshExe $ssh -Payload $payload -TailArguments @($remoteAction, $script:OldBinarySHA, $script:OldPayloadSHA, $recoverySHA, $script:OperatorID) -TimeoutMilliseconds 30000
    if ($UploadedBinaryPreflightOnly) {
        if ($preflight -cnotmatch $script:UploadedBinaryPreflightPattern) { throw 'uploaded_preflight_output_invalid' }
        $operation = $Matches.operation
        $binaryMode = $Matches.binary_mode
        $binaryHashMatch = $Matches.binary_hash_match
        Write-Output ("status=pass mode=email_unknown_uploaded_binary_preflight_metadata preflight=true state_class=complete state_phase=phase1_created stage_nonce_match=false fixture_ownership=true redis_identity=true redis_key_exists=0 binary_regular=true binary_symlink=false binary_owner=true binary_mode=$binaryMode binary_hash_match=$binaryHashMatch exit_code=0 stderr_length=0 retained=true ssh_attempts=1 scp_attempts=0 writes=false retries=0")
        return
    }
    if ($ResumeUploadedCleanup) {
        if ($preflight -cnotmatch $script:UploadedBinaryPreflightPattern -or $Matches.binary_hash_match -cne 'true') { throw 'uploaded_preflight_output_invalid' }
        $operation = $Matches.operation
        $currentStage = 'cleanup'
        $sshAttempts++
        $cleanup = Invoke-RemotePayload -SshExe $ssh -Payload $payload -TailArguments @('cleanup', $script:OldBinarySHA, $script:OldPayloadSHA, $recoverySHA, $script:OperatorID) -TimeoutMilliseconds 300000
        if ($cleanup -cnotmatch $script:CleanupPattern -or $Matches.operation -cne $operation) { throw 'cleanup_output_invalid' }
        Write-Output 'status=pass mode=email_unknown_resume_uploaded_binary_exact_cleanup preflight=true binary_hash_match=true cleanup=true retained=false ssh_attempts=2 scp_attempts=0 retries=0'
        return
    }
    if ($preflight -cnotmatch $script:PreflightPattern) { throw 'preflight_output_invalid' }
    $operation = $Matches.operation
    if ($PreflightOnly) {
        Write-Output 'status=pass mode=email_unknown_stage_nonce_mismatch_preflight_metadata preflight=true state_class=complete state_phase=phase1_created stage_nonce_match=false fixture_ownership=true redis_identity=true redis_key_exists=0 exit_code=0 stderr_length=0 retained=true ssh_attempts=1 scp_attempts=0 writes=false retries=0'
        return
    }
    $currentStage = 'upload_recovery_binary'
    $remoteTarget = $script:Remote + ":/home/pc/molin-runtime/email-unknown-cycle-$operation/email-unknown-phase1-recovery.test"
    $scpArguments = @('-q', '-O', '-P', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', $binary, $remoteTarget)
    $scpAttempts++
    [void](Invoke-FixedProcess -FilePath $scp -Arguments $scpArguments -InputText '' -TimeoutMilliseconds 120000)
    $currentStage = 'cleanup'
    $sshAttempts++
    $cleanup = Invoke-RemotePayload -SshExe $ssh -Payload $payload -TailArguments @('cleanup', $script:OldBinarySHA, $script:OldPayloadSHA, $recoverySHA, $script:OperatorID) -TimeoutMilliseconds 300000
    if ($cleanup -cnotmatch $script:CleanupPattern -or $Matches.operation -cne $operation) { throw 'cleanup_output_invalid' }
    Write-Output 'status=pass mode=email_unknown_stage_nonce_mismatch_recovery preflight=true fixture_ownership=true redis_identity=true redis_key_exists=0 cleanup=true retained=false ssh_attempts=2 scp_attempts=1 retries=0'
}
catch {
    $remoteStage = 'local_controller'
    $exitCode = 'unknown'
    $stdoutLength = 'unknown'
    $stderrLength = 'unknown'
    if ($_.Exception.Data.Contains('RemoteStage')) { $remoteStage = [string]$_.Exception.Data['RemoteStage'] }
    if ($_.Exception.Data.Contains('ExitCode')) { $exitCode = [string]$_.Exception.Data['ExitCode'] }
    if ($_.Exception.Data.Contains('StdoutLength')) { $stdoutLength = [string]$_.Exception.Data['StdoutLength'] }
    if ($_.Exception.Data.Contains('StderrLength')) { $stderrLength = [string]$_.Exception.Data['StderrLength'] }
    Write-Output ("status=failed mode=$mode stage=$currentStage remote_stage=$remoteStage exit_code=$exitCode stdout_length=$stdoutLength stderr_length=$stderrLength retained=true ssh_attempts=$sshAttempts scp_attempts=$scpAttempts retries=0")
    throw
}
