[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)]
    [string]$Confirm,

    [Parameter(Mandatory = $false)]
    [switch]$SelfTest,

    [Parameter(Mandatory = $false)]
    [switch]$LocalPreflightOnly
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:RequiredPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_RECOVERY_PARENT_READONLY_DIAGNOSTIC_ONCE'
$script:SuccessPattern = '^status=pass parent_count=3 home_exists=(?:true|false) home_owner=(?:true|false) home_symlink=(?:true|false) home_group_other_writable=(?:true|false) home_stable=(?:true|false) project_exists=(?:true|false) project_owner=(?:true|false) project_symlink=(?:true|false) project_group_other_writable=(?:true|false) project_stable=(?:true|false) rollback_exists=(?:true|false) rollback_owner=(?:true|false) rollback_symlink=(?:true|false) rollback_group_other_writable=(?:true|false) rollback_stable=(?:true|false) recovery_count=[0-9]+ recovery_exists=(?:true|false) recovery_owner=(?:true|false) recovery_symlink=(?:true|false) recovery_group_other_writable=(?:true|false) recovery_stable=(?:true|false) writes=false postcheck=false cleanup=false database=false redis=false docker=false restarts=false retries=0\r?\n?\z'
$script:FailurePattern = '^status=failed stage=(?<stage>shell_options|parent_snapshot|recovery_snapshot|stability_snapshot) writes=false postcheck=false cleanup=false database=false redis=false docker=false restarts=false retries=0\r?\n?\z'

function Read-VerifiedPayload {
    $expected = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'email-unknown-recovery-parent-readonly-diagnostic.payload.sh'))
    if (-not [IO.File]::Exists($expected)) { throw 'payload_missing' }
    $item = [IO.FileInfo]::new($expected)
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.FullName -cne $expected) { throw 'payload_path_invalid' }
    return [IO.File]::ReadAllText($expected, (New-Object Text.UTF8Encoding($false, $true)))
}

function ConvertTo-Utf8PayloadBytes {
    param([Parameter(Mandatory = $true)][string]$Payload)
    # stdin 固定为 LF、无 BOM、无 NUL，并要求首行立即启用 Bash 严格模式。
    $normalized = $Payload.Replace("`r`n", "`n").Replace("`r", "`n")
    if ($normalized.Length -eq 0 -or [int][char]$normalized[0] -in @(0xFEFF, 0xFFFE) -or
        $normalized.IndexOf([char]0) -ge 0 -or -not $normalized.StartsWith("set -Eeuo pipefail`n", [StringComparison]::Ordinal)) {
        throw 'payload_encoding_invalid'
    }
    $bytes = (New-Object Text.UTF8Encoding($false, $true)).GetBytes($normalized)
    if ($bytes.Length -lt 4 -or ($bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF)) { throw 'payload_encoding_invalid' }
    return ,$bytes
}

function New-RestrictedTempDirectory {
    $root = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    $path = [IO.Path]::GetFullPath((Join-Path $root ('molin-email-parent-diagnostic-' + [Guid]::NewGuid().ToString('N'))))
    if (-not $path.StartsWith($root, [StringComparison]::OrdinalIgnoreCase) -or [IO.Directory]::Exists($path) -or [IO.File]::Exists($path)) { throw 'temp_path_invalid' }
    [void][IO.Directory]::CreateDirectory($path)
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $security = New-Object Security.AccessControl.DirectorySecurity
    $security.SetOwner($sid)
    $security.SetAccessRuleProtection($true, $false)
    [void]$security.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sid, [Security.AccessControl.FileSystemRights]::FullControl, [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit', [Security.AccessControl.PropagationFlags]::None, [Security.AccessControl.AccessControlType]::Allow)))
    [IO.Directory]::SetAccessControl($path, $security)
    if (([IO.DirectoryInfo]::new($path).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'temp_path_invalid' }
    return $path
}

function Write-RestrictedBytes {
    param([Parameter(Mandatory = $true)][string]$Path, [Parameter(Mandatory = $true)][AllowEmptyCollection()][byte[]]$Bytes)
    if (-not [IO.Path]::IsPathRooted($Path) -or [IO.File]::Exists($Path) -or [IO.Directory]::Exists($Path)) { throw 'temp_file_invalid' }
    [IO.File]::WriteAllBytes($Path, $Bytes)
    $actual = [IO.File]::ReadAllBytes($Path)
    if ($actual.Length -ne $Bytes.Length -or ([IO.FileInfo]::new($Path).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'temp_file_invalid' }
    for ($index = 0; $index -lt $Bytes.Length; $index++) { if ($actual[$index] -ne $Bytes[$index]) { throw 'temp_file_invalid' } }
}

function Remove-RestrictedTempDirectory {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not [IO.Path]::IsPathRooted($Path) -or [IO.Path]::GetFileName($Path) -cnotmatch '^molin-email-parent-diagnostic-[a-f0-9]{32}$') { throw 'temp_cleanup_path_invalid' }
    if ([IO.Directory]::Exists($Path)) {
        foreach ($file in [IO.Directory]::GetFiles($Path)) {
            if ([IO.Path]::GetFileName($file) -cnotin @('stdin.bin', 'stdout.txt', 'stderr.txt')) { throw 'temp_cleanup_file_invalid' }
            [IO.File]::Delete($file)
        }
        if ([IO.Directory]::GetFileSystemEntries($Path).Length -ne 0) { throw 'temp_cleanup_not_empty' }
        [IO.Directory]::Delete($Path, $false)
    }
}

function Start-FixedRedirectedProcess {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$ArgumentList,
        [Parameter(Mandatory = $true)][string]$InputPath,
        [Parameter(Mandatory = $true)][string]$OutputPath,
        [Parameter(Mandatory = $true)][string]$ErrorPath,
        [Parameter(Mandatory = $true)][int]$TimeoutMilliseconds
    )
    $process = Microsoft.PowerShell.Management\Start-Process -FilePath $FilePath -ArgumentList $ArgumentList -RedirectStandardInput $InputPath -RedirectStandardOutput $OutputPath -RedirectStandardError $ErrorPath -NoNewWindow -PassThru
    try {
        $handle = $process.Handle
        if ($handle -eq [IntPtr]::Zero) { throw 'process_handle_unavailable' }
    }
    catch {
        try { if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() } } catch { }
        throw 'process_handle_unavailable'
    }
    if (-not $process.WaitForExit($TimeoutMilliseconds)) { $process.Kill(); $process.WaitForExit(); throw 'process_timeout' }
    $process.Refresh()
    try { $exitCode = $process.ExitCode } catch { throw 'process_exit_code_unavailable' }
    if ($null -eq $exitCode) { throw 'process_exit_code_unavailable' }
    return [pscustomobject]@{ ExitCode = [int]$exitCode }
}

function Test-RemoteSummary {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stdout,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stderr,
        [Parameter(Mandatory = $true)][int]$ExitCode
    )
    if ($Stderr.Length -ne 0) { return [pscustomobject]@{ Status = 'failed'; Classification = 'remote_stderr_nonempty'; DiagnosticStage = $null } }
    if ($ExitCode -eq 0 -and [regex]::IsMatch($Stdout, $script:SuccessPattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)) {
        return [pscustomobject]@{ Status = 'pass'; Classification = 'pass'; DiagnosticStage = $null }
    }
    $failure = [regex]::Match($Stdout, $script:FailurePattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if ($ExitCode -eq 2 -and $failure.Success) {
        return [pscustomobject]@{ Status = 'failed'; Classification = 'remote_gate_failed'; DiagnosticStage = $failure.Groups['stage'].Value }
    }
    if ($ExitCode -ne 0) { return [pscustomobject]@{ Status = 'failed'; Classification = 'remote_exit_nonzero'; DiagnosticStage = $null } }
    return [pscustomobject]@{ Status = 'failed'; Classification = 'remote_output_invalid'; DiagnosticStage = $null }
}

function Invoke-LocalPreflightCheck {
    $temp = $null
    try {
        $payload = Read-VerifiedPayload
        $bytes = ConvertTo-Utf8PayloadBytes $payload
        $sshExe = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
        if (-not [IO.File]::Exists($sshExe) -or ([IO.FileInfo]::new($sshExe).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'ssh_tool_missing' }
        $temp = New-RestrictedTempDirectory
        Write-RestrictedBytes (Join-Path $temp 'stdin.bin') $bytes
        Write-RestrictedBytes (Join-Path $temp 'stdout.txt') ([byte[]]@())
        Write-RestrictedBytes (Join-Path $temp 'stderr.txt') ([byte[]]@())
        return [pscustomobject]@{ Status = 'pass'; Classification = 'pass'; FilesVerified = 2 }
    }
    catch {
        $known = @('payload_missing', 'payload_path_invalid', 'payload_encoding_invalid', 'ssh_tool_missing', 'temp_path_invalid', 'temp_file_invalid')
        $classification = if ($_.Exception.Message -cin $known) { $_.Exception.Message } else { 'local_preflight_failed' }
        return [pscustomobject]@{ Status = 'failed'; Classification = $classification; FilesVerified = 0 }
    }
    finally { if ($null -ne $temp) { Remove-RestrictedTempDirectory $temp } }
}

if ($SelfTest) {
    # SelfTest 只运行内存摘要夹具和本机进程退出码探针，不发现或启动 SSH。
    $cases = 0
    $payload = Read-VerifiedPayload
    $bytes = ConvertTo-Utf8PayloadBytes $payload
    if ($bytes[0] -ne 0x73) { throw 'selftest_payload_encoding' }
    $cases++
    foreach ($required in @('parent_paths=(/home/pc /home/pc/molin /home/pc/molin/rollback)', 'recovery_count=${#recovery_candidates[@]}', 'parent_stable', 'recovery_stable', 'writes=false postcheck=false cleanup=false database=false redis=false docker=false restarts=false retries=0')) {
        if (-not $payload.Contains($required)) { throw 'selftest_payload_contract' }
        $cases++
    }
    foreach ($forbidden in @('redis-cli', '/usr/bin/mysql', '/usr/bin/docker', 'chmod', 'chown', 'systemctl', 'docker restart', 'docker stop', 'docker kill', 'docker rm', 'rm -', 'mv ', 'cp ', 'touch ')) {
        if ($payload.Contains($forbidden)) { throw 'selftest_payload_side_effect' }
    }
    $cases++
    $valid = 'status=pass parent_count=3 home_exists=true home_owner=true home_symlink=false home_group_other_writable=false home_stable=true project_exists=true project_owner=true project_symlink=false project_group_other_writable=true project_stable=true rollback_exists=true rollback_owner=true rollback_symlink=false rollback_group_other_writable=false rollback_stable=true recovery_count=1 recovery_exists=true recovery_owner=true recovery_symlink=false recovery_group_other_writable=false recovery_stable=true writes=false postcheck=false cleanup=false database=false redis=false docker=false restarts=false retries=0' + "`n"
    if ((Test-RemoteSummary $valid '' 0).Status -cne 'pass') { throw 'selftest_valid_summary' }
    $cases++
    foreach ($attack in @(($valid + "extra=true`n"), $valid.Replace(' recovery_count=1', ''), $valid.Replace('home_owner=true', 'home_owner=TRUE'))) {
        if ((Test-RemoteSummary $attack '' 0).Classification -cne 'remote_output_invalid') { throw 'selftest_summary_attack' }
        $cases++
    }
    $failure = 'status=failed stage=parent_snapshot writes=false postcheck=false cleanup=false database=false redis=false docker=false restarts=false retries=0' + "`n"
    $parsedFailure = Test-RemoteSummary $failure '' 2
    if ($parsedFailure.Classification -cne 'remote_gate_failed' -or $parsedFailure.DiagnosticStage -cne 'parent_snapshot') { throw 'selftest_failure_summary' }
    $cases++
    if ((Test-RemoteSummary $valid 'warning' 0).Classification -cne 'remote_stderr_nonempty') { throw 'selftest_stderr_attack' }
    $cases++
    $preflight = Invoke-LocalPreflightCheck
    if ($preflight.Status -cne 'pass' -or $preflight.FilesVerified -ne 2) { throw ('selftest_preflight_' + $preflight.Classification) }
    $cases++
    Write-Output "status=pass mode=selftest cases=$cases external_access=false ssh_attempt_count=0 postcheck=false cleanup=false database=false redis=false docker=false retries=0"
    exit 0
}

if ($LocalPreflightOnly) {
    $preflight = Invoke-LocalPreflightCheck
    if ($preflight.Status -ceq 'pass') {
        Write-Output 'status=pass stage=local_preflight files_verified=2 ssh_started=false writes=false retries=0'
        exit 0
    }
    Write-Output ("status=failed stage=local_preflight classification={0} ssh_started=false writes=false retries=0" -f $preflight.Classification)
    exit 2
}

$temp = $null
$attemptCount = 0
$completedCount = 0
$finalLine = $null
$exitCode = 2
try {
    if ($Confirm -cne $script:RequiredPhrase) { throw 'confirmation_required' }
    $payloadBytes = ConvertTo-Utf8PayloadBytes (Read-VerifiedPayload)
    $sshExe = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
    if (-not [IO.File]::Exists($sshExe)) { throw 'ssh_tool_missing' }
    $temp = New-RestrictedTempDirectory
    $inputPath = Join-Path $temp 'stdin.bin'; $outputPath = Join-Path $temp 'stdout.txt'; $errorPath = Join-Path $temp 'stderr.txt'
    Write-RestrictedBytes $inputPath $payloadBytes
    Write-RestrictedBytes $outputPath ([byte[]]@())
    Write-RestrictedBytes $errorPath ([byte[]]@())
    $arguments = @('-T', '-p', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', 'pc@8.130.9.163', '/usr/bin/env', '-i', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'HOME=/home/pc', 'USER=pc', 'LOGNAME=pc', 'LANG=C.UTF-8', '/usr/bin/timeout', '--signal=TERM', '--kill-after=5s', '45s', '/bin/bash', '--noprofile', '--norc', '-s', '--')
    $attemptCount = 1
    $process = Start-FixedRedirectedProcess $sshExe $arguments $inputPath $outputPath $errorPath 60000
    $completedCount = 1
    $encoding = New-Object Text.UTF8Encoding($false, $true)
    $stdout = [IO.File]::ReadAllText($outputPath, $encoding); $stderr = [IO.File]::ReadAllText($errorPath, $encoding)
    $result = Test-RemoteSummary $stdout $stderr $process.ExitCode
    if ($result.Status -ceq 'pass') {
        $finalLine = $stdout.TrimEnd([char[]]@("`r", "`n"))
        $exitCode = 0
    }
    elseif ($result.Classification -ceq 'remote_gate_failed') {
        $finalLine = "status=failed stage=remote classification=remote_gate_failed diagnostic_stage=$($result.DiagnosticStage) ssh_attempt_count=1 ssh_completed_count=1 writes=false retries=0"
    }
    else {
        $finalLine = "status=failed stage=remote classification=$($result.Classification) ssh_attempt_count=1 ssh_completed_count=1 writes=false retries=0"
    }
}
catch {
    $known = @('confirmation_required', 'payload_missing', 'payload_path_invalid', 'payload_encoding_invalid', 'ssh_tool_missing', 'temp_path_invalid', 'temp_file_invalid', 'process_timeout', 'process_handle_unavailable', 'process_exit_code_unavailable')
    $classification = if ($_.Exception.Message -cin $known) { $_.Exception.Message } else { 'local_gate_failed' }
    $finalLine = "status=failed stage=local classification=$classification ssh_attempt_count=$attemptCount ssh_completed_count=$completedCount writes=false retries=0"
}
finally {
    if ($null -ne $temp) {
        try { Remove-RestrictedTempDirectory $temp }
        catch { $finalLine = "status=failed stage=local classification=temp_cleanup_failed ssh_attempt_count=$attemptCount ssh_completed_count=$completedCount writes=false retries=0"; $exitCode = 2 }
    }
}

Write-Output $finalLine
exit $exitCode
