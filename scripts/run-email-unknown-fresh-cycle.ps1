[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)][switch]$SelfTest,
    [Parameter(Mandatory = $false)][switch]$Execute,
    [Parameter(Mandatory = $false)][string]$Confirm,
    [Parameter(Mandatory = $false)][string]$BinaryPath
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:RequiredPhrase = 'I_CONFIRM_DIRECTMAIL_PHASE4_FRESH_CYCLE_LEGACY_SCP_ONCE'
$script:UploadTransport = 'legacy_scp'
$script:PayloadPath = Join-Path $PSScriptRoot 'email-unknown-fresh-cycle.payload.sh'
$script:PayloadSHA = '29eaa0b18959d9abccdcf10d3793aa6a0c8574b85028714ab7d6eb4e429def54'
$script:BinarySHA = '1179e29d9f43efea79f185e8d2319d015a627f69a48ef9ed7ce22e72ba6ad900'
$script:BinarySize = 25573597
$script:OperatorID = '259'
$script:Remote = 'pc@8.130.9.163'
$script:CyclePattern = '^(?:status=pass stage=upload_hash_verified\r?\n)(?:status=pass stage=preflight api=ready schema=57 redis_identity=unique writes=false\r?\n)(?:status=pass stage=phase1 tombstone=created adapter_calls=1 state=retained restart_required=true\r?\n)(?:status=pass stage=restart redis_unique=true run_id_changed=true api_ready=true\r?\n)(?:status=pass stage=phase2 old_key_blocked=true new_key_blocked=true adapter_calls=0 cleanup_required=true\r?\n)(?:status=pass stage=cleanup_verified db_rows=0 state_absent=true binary_absent=true\r?\n)(?:status=pass stage=finalize api_health=true api_ready=true retained_payload=true\r?\n)(?:status=pass stage=remote_stage_removed\r?\n?)$'

function Get-SHA256Lower {
    param([Parameter(Mandatory = $true)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

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
    if ((Get-SHA256Lower -Path $path) -cne $script:PayloadSHA) { throw 'payload_hash' }
    return $text
}

function Assert-PayloadContract {
    param([Parameter(Mandatory = $true)][string]$Text)
    foreach ($required in @(
        'exec 2>/dev/null', 'docker restart "$redis_id"', 'EMAIL_ADAPTER=mock',
        'EMAIL_UNKNOWN_RESTART_NONCE="$nonce"', 'cleanup_verified', '--no-defaults',
        'os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)', 'os.fstat(fd)', 'info.st_nlink != 1',
        'email_send_logs WHERE id=${send_log_id}',
        'email_test_recipient_allowlist WHERE id=${allowlist_id}',
        'email_provider_templates WHERE id=${template_id}',
        'status=failed stage=%s retained=true retries=0'
    )) {
        if (-not $Text.Contains($required)) { throw 'payload_contract' }
    }
    if ($Text.Contains('email_test_recipient_allowlists') -or $Text.Contains('mysqldump') -or $Text.Contains('mysqladmin') -or
        $Text -match '(?im)\b(?:FLUSHDB|FLUSHALL|KEYS|SCAN)\b' -or
        $Text -match '(?im)redis-cli[^\r\n]*(?:\bDEL\b|\bUNLINK\b|--scan)') {
        throw 'payload_forbidden'
    }
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
    $temporary = [IO.Path]::GetFullPath((Join-Path $root ('molin-email-fresh-cycle-' + [Guid]::NewGuid().ToString('N'))))
    if (-not $temporary.StartsWith($root + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -or
        [IO.Directory]::Exists($temporary) -or [IO.File]::Exists($temporary)) { throw 'process_temp_invalid' }
    [void][IO.Directory]::CreateDirectory($temporary)
    $stdin = Join-Path $temporary 'stdin.txt'
    $stdout = Join-Path $temporary 'stdout.txt'
    $stderr = Join-Path $temporary 'stderr.txt'
    $process = $null
    try {
        # 使用无 BOM UTF-8 写入标准输入，确保中文注释不会改变远端 Bash 的首行。
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
        if ($process.ExitCode -ne 0 -or $errorOutput.Length -ne 0) {
            $exception = [InvalidOperationException]::new('remote_gate_failed')
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

function Invoke-RemoteScript {
    param(
        [Parameter(Mandatory = $true)][string]$SshExe,
        [Parameter(Mandatory = $true)][string]$Script,
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
        '--kill-after=5s', '360s', '/bin/bash', '--noprofile', '--norc', '-s', '--'
    ) + $TailArguments
    return Invoke-FixedProcess -FilePath $SshExe -Arguments $arguments -InputText $Script -TimeoutMilliseconds $TimeoutMilliseconds
}

function Invoke-SelfTest {
    $payload = Get-PayloadText
    Assert-PayloadContract -Text $payload
    $bash = 'C:\Program Files\Git\bin\bash.exe'
    if (-not [IO.File]::Exists($bash)) { throw 'bash_missing' }
    & $bash -n $script:PayloadPath
    if ($LASTEXITCODE -ne 0) { throw 'bash_syntax' }
    $probe = Invoke-FixedProcess -FilePath $bash -Arguments @('-c', 'cat') -InputText "传输探针`n" -TimeoutMilliseconds 10000
    if ($probe -cne "传输探针`n") { throw 'transport_probe' }
    if ($script:CyclePattern -notmatch 'remote_stage_removed' -or $script:BinarySize -ne 25573597 -or $script:UploadTransport -cne 'legacy_scp') { throw 'selftest_contract' }
    Write-Output 'status=pass mode=email_unknown_fresh_cycle_selftest upload_transport=legacy_scp hash_transport=pass external_access=false writes=false restarts=false retries=0'
}

if ($SelfTest) {
    if ($Execute -or $Confirm -or $BinaryPath) { throw 'selftest_arguments' }
    Invoke-SelfTest
    exit 0
}

# 真实执行必须显式提供单次确认词，且确认发生在 SSH/SCP 工具发现之前。
if (-not $Execute -or $Confirm -cne $script:RequiredPhrase) { throw 'confirmation_required' }
if ([string]::IsNullOrWhiteSpace($BinaryPath)) { throw 'binary_required' }
$payload = Get-PayloadText
Assert-PayloadContract -Text $payload
$binary = [IO.Path]::GetFullPath($BinaryPath)
if (-not [IO.File]::Exists($binary) -or ([IO.FileInfo]::new($binary).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'binary_invalid' }
$binaryBytes = [IO.File]::ReadAllBytes($binary)
if ($binaryBytes.Length -ne $script:BinarySize -or $binaryBytes[0] -ne 0x7F -or $binaryBytes[1] -ne 0x45 -or
    $binaryBytes[2] -ne 0x4C -or $binaryBytes[3] -ne 0x46 -or (Get-SHA256Lower -Path $binary) -cne $script:BinarySHA) {
    throw 'binary_identity'
}

$ssh = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
$scp = Join-Path $env:WINDIR 'System32\OpenSSH\scp.exe'
if (-not [IO.File]::Exists($ssh) -or -not [IO.File]::Exists($scp)) { throw 'ssh_tool_missing' }
$nonce = [Guid]::NewGuid().ToString('N')
$stage = "/home/pc/molin-runtime/email-unknown-cycle-$nonce"
$sshAttempts = 0
$scpAttempts = 0
$currentStage = 'stage_setup'
$setup = @'
set -Eeuo pipefail
exec 2>/dev/null
nonce=$1
[[ "$nonce" =~ ^[a-f0-9]{32}$ ]]
parent=/home/pc/molin-runtime
stage="${parent}/email-unknown-cycle-${nonce}"
[[ -d "$parent" && ! -L "$parent" && "$(stat -c '%U' -- "$parent")" == pc ]]
[[ ! -e "$stage" && ! -L "$stage" ]]
mkdir -m 700 -- "$stage"
[[ "$(stat -c '%U:%a' -- "$stage")" == pc:700 ]]
printf 'status=pass stage=remote_stage_created\n'
'@
$cycle = @'
set -Eeuo pipefail
exec 2>/dev/null
nonce=$1; binary_sha=$2; payload_sha=$3; operator_id=$4
[[ "$nonce" =~ ^[a-f0-9]{32}$ && "$binary_sha" =~ ^[a-f0-9]{64}$ && "$payload_sha" =~ ^[a-f0-9]{64}$ && "$operator_id" =~ ^[1-9][0-9]*$ ]]
stage="/home/pc/molin-runtime/email-unknown-cycle-${nonce}"
binary="${stage}/email-unknown-restart.test"; payload="${stage}/cycle.payload.sh"; state="${stage}/cycle.state"
[[ -d "$stage" && ! -L "$stage" && "$(stat -c '%U:%a' -- "$stage")" == pc:700 ]]
[[ -f "$binary" && ! -L "$binary" && "$(stat -c '%U' -- "$binary")" == pc ]]
[[ -f "$payload" && ! -L "$payload" && "$(stat -c '%U' -- "$payload")" == pc ]]
[[ "$(stat -c '%a' -- "$binary")" =~ ^[0-7]{3}$ && "$(stat -c '%a' -- "$payload")" =~ ^[0-7]{3}$ ]]
[[ "$(sha256sum -- "$binary")" == "${binary_sha}  ${binary}" ]]
[[ "$(sha256sum -- "$payload")" == "${payload_sha}  ${payload}" ]]
chmod 500 -- "$binary" "$payload"
[[ "$(stat -c '%U:%a' -- "$binary")" == pc:500 && "$(stat -c '%U:%a' -- "$payload")" == pc:500 ]]
printf 'status=pass stage=upload_hash_verified\n'
for action in preflight phase1 restart phase2 cleanup_verified finalize; do
  "$payload" "$action" "$nonce" "$binary_sha" "$operator_id"
done
[[ ! -e "$state" && ! -L "$state" && ! -e "$binary" && ! -L "$binary" ]]
mapfile -t names < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
[[ ${#names[@]} -eq 1 && "${names[0]}" == cycle.payload.sh ]]
rm -f -- "$payload"
rmdir -- "$stage"
printf 'status=pass stage=remote_stage_removed\n'
'@

try {
    $sshAttempts++
    $setupOutput = Invoke-RemoteScript -SshExe $ssh -Script $setup -TailArguments @($nonce) -TimeoutMilliseconds 30000
    if ($setupOutput -cnotmatch '^status=pass stage=remote_stage_created\r?\n?$') { throw 'stage_setup_output' }
    # 使用已由固定 44 字节探针验证通过的 legacy SCP 协议，保持用户指定的上传路径。
    $scpBase = @('-q', '-O', '-P', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', '--')
    $currentStage = 'upload_binary'
    $scpAttempts++
    [void](Invoke-FixedProcess -FilePath $scp -Arguments ($scpBase + @($binary, $script:Remote + ':' + $stage + '/email-unknown-restart.test')) -InputText '' -TimeoutMilliseconds 120000)
    $currentStage = 'upload_payload'
    $scpAttempts++
    [void](Invoke-FixedProcess -FilePath $scp -Arguments ($scpBase + @([IO.Path]::GetFullPath($script:PayloadPath), $script:Remote + ':' + $stage + '/cycle.payload.sh')) -InputText '' -TimeoutMilliseconds 30000)
    $currentStage = 'fresh_cycle'
    $sshAttempts++
    $cycleOutput = Invoke-RemoteScript -SshExe $ssh -Script $cycle -TailArguments @($nonce, $script:BinarySHA, $script:PayloadSHA, $script:OperatorID) -TimeoutMilliseconds 390000
    if ($cycleOutput -cnotmatch $script:CyclePattern) { throw 'cycle_output_invalid' }
    Write-Output 'status=pass mode=email_unknown_fresh_cycle upload_transport=legacy_scp preflight=true phase1=true redis_restart=true phase2=true adapter_delta_zero=true cleanup=true retained=false ssh_attempts=2 scp_attempts=2 retries=0 real_mail=false'
}
catch {
    $exitCode = if ($_.Exception.Data.Contains('ExitCode')) { $_.Exception.Data['ExitCode'] } else { -1 }
    $stdoutLength = if ($_.Exception.Data.Contains('StdoutLength')) { $_.Exception.Data['StdoutLength'] } else { 0 }
    $stderrLength = if ($_.Exception.Data.Contains('StderrLength')) { $_.Exception.Data['StderrLength'] } else { 0 }
    Write-Output ("status=failed mode=email_unknown_fresh_cycle upload_transport=legacy_scp stage=$currentStage exit_code=$exitCode stdout_length=$stdoutLength stderr_length=$stderrLength retained=true ssh_attempts=$sshAttempts scp_attempts=$scpAttempts retries=0")
    throw 'fresh_cycle_failed'
}
