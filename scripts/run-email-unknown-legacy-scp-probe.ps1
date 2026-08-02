[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)][switch]$SelfTest,
    [Parameter(Mandatory = $false)][switch]$Execute,
    [Parameter(Mandatory = $false)][string]$Confirm
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:ConfirmPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_LEGACY_SCP_PROBE_ONCE'
$script:Remote = 'pc@8.130.9.163'
$script:ProbeName = 'legacy-scp-probe.bin'
$script:ProbeText = "molin-directmail-phase4-legacy-scp-probe-v1`n"
$script:ProbeSize = 44
$script:ProbeSHA = 'c365cddf1551b4392727480d283fa07a7e1cd944e6ecde64fdf1b87fcca8af69'
$script:PreflightPattern = '^status=pass stage=preflight stage_count=1 stage_identity=true parent_writable=true stage_writable=true stage_empty=true nonce=(?<nonce>[a-f0-9]{32})\r?\n?$'
$script:VerifyPattern = '^status=pass stage=verify_cleanup probe_regular=true probe_symlink=false probe_owner=true probe_size_match=true probe_hash_match=true probe_removed=true stage_empty=true stage_retained=true\r?\n?$'

$script:RemotePayload = @'
#!/usr/bin/env bash
set -Eeuo pipefail
exec 2>/dev/null
action=$1
nonce=${2:-}
expected_sha=${3:-}
expected_size=${4:-}
parent=/home/pc/molin-runtime
probe_name=legacy-scp-probe.bin

[[ -d "$parent" && ! -L "$parent" && "$(stat -c '%U' -- "$parent")" == pc && -w "$parent" ]]
shopt -s nullglob
stages=("$parent"/email-unknown-cycle-*)
[[ ${#stages[@]} -eq 1 ]]
stage=${stages[0]}
[[ "$stage" =~ ^/home/pc/molin-runtime/email-unknown-cycle-([a-f0-9]{32})$ ]]
actual_nonce=${BASH_REMATCH[1]}
[[ -d "$stage" && ! -L "$stage" && "$(stat -c '%U:%a' -- "$stage")" == pc:700 && -w "$stage" ]]
stage_id=$(stat -c '%d:%i' -- "$stage")
[[ "$stage_id" =~ ^[0-9]+:[0-9]+$ ]]

if [[ "$action" == preflight ]]; then
  [[ -z "$nonce" && -z "$expected_sha" && -z "$expected_size" ]]
  mapfile -t before < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
  [[ ${#before[@]} -eq 0 ]]
  sleep 1
  [[ "$(stat -c '%d:%i' -- "$stage")" == "$stage_id" ]]
  mapfile -t after < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
  [[ ${#after[@]} -eq 0 ]]
  printf 'status=pass stage=preflight stage_count=1 stage_identity=true parent_writable=true stage_writable=true stage_empty=true nonce=%s\n' "$actual_nonce"
elif [[ "$action" == verify_cleanup ]]; then
  [[ "$nonce" == "$actual_nonce" && "$expected_sha" =~ ^[a-f0-9]{64}$ && "$expected_size" =~ ^[1-9][0-9]*$ ]]
  probe="$stage/$probe_name"
  mapfile -t entries < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
  [[ ${#entries[@]} -eq 1 && "${entries[0]}" == "$probe_name" ]]
  [[ -f "$probe" && ! -L "$probe" && "$(stat -c '%U' -- "$probe")" == pc ]]
  [[ "$(stat -c '%s' -- "$probe")" == "$expected_size" ]]
  [[ "$(sha256sum -- "$probe")" == "$expected_sha  $probe" ]]
  rm -f -- "$probe"
  [[ ! -e "$probe" && ! -L "$probe" && -d "$stage" && ! -L "$stage" ]]
  [[ "$(stat -c '%d:%i' -- "$stage")" == "$stage_id" ]]
  mapfile -t final_entries < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
  [[ ${#final_entries[@]} -eq 0 ]]
  printf 'status=pass stage=verify_cleanup probe_regular=true probe_symlink=false probe_owner=true probe_size_match=true probe_hash_match=true probe_removed=true stage_empty=true stage_retained=true\n'
else
  exit 2
fi
'@

function Get-SHA256Lower {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $sha = [Security.Cryptography.SHA256]::Create()
    try { return (($sha.ComputeHash($Bytes) | ForEach-Object { $_.ToString('x2') }) -join '') }
    finally { $sha.Dispose() }
}

function Invoke-CapturedProcess {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][byte[]]$InputBytes,
        [Parameter(Mandatory = $true)][int]$TimeoutMilliseconds
    )
    $root = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $temporary = [IO.Path]::GetFullPath((Join-Path $root ('molin-legacy-scp-process-' + [Guid]::NewGuid().ToString('N'))))
    if (-not $temporary.StartsWith($root + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -or [IO.Directory]::Exists($temporary)) { throw 'process_temp_invalid' }
    [void][IO.Directory]::CreateDirectory($temporary)
    $stdin = Join-Path $temporary 'stdin.bin'; $stdout = Join-Path $temporary 'stdout.bin'; $stderr = Join-Path $temporary 'stderr.bin'
    $process = $null
    try {
        [IO.File]::WriteAllBytes($stdin, $InputBytes)
        $process = Start-Process -FilePath $FilePath -ArgumentList $Arguments -RedirectStandardInput $stdin -RedirectStandardOutput $stdout -RedirectStandardError $stderr -NoNewWindow -PassThru
        $handle = $process.Handle
        if ($handle -eq [IntPtr]::Zero) { throw 'process_handle_invalid' }
        if (-not $process.WaitForExit($TimeoutMilliseconds)) { $process.Kill(); $process.WaitForExit(); throw 'process_timeout' }
        $process.WaitForExit()
        $process.Refresh()
        $stdoutBytes = [IO.File]::ReadAllBytes($stdout); $stderrBytes = [IO.File]::ReadAllBytes($stderr)
        $stdoutText = [Text.UTF8Encoding]::new($false, $true).GetString($stdoutBytes)
        return [pscustomobject]@{ ExitCode = $process.ExitCode; Stdout = $stdoutText; StdoutLength = $stdoutBytes.Length; StderrLength = $stderrBytes.Length }
    }
    finally {
        if ($null -ne $process) { $process.Dispose() }
        foreach ($path in @($stdin, $stdout, $stderr)) { if ([IO.File]::Exists($path)) { [IO.File]::Delete($path) } }
        if ([IO.Directory]::Exists($temporary)) {
            if ([IO.Directory]::GetFileSystemEntries($temporary).Length -ne 0) { throw 'process_temp_not_empty' }
            [IO.Directory]::Delete($temporary, $false)
        }
    }
}

function Invoke-OneSSH {
    param([Parameter(Mandatory = $true)][string[]]$TailArguments)
    foreach ($argument in $TailArguments) { if ($argument -cnotmatch '^[A-Za-z0-9_./:-]+$') { throw 'remote_argument_invalid' } }
    $ssh = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
    $arguments = @('-T', '-p', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', $script:Remote, '/usr/bin/env', '-i', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'HOME=/home/pc', 'USER=pc', 'LOGNAME=pc', 'LANG=C.UTF-8', '/usr/bin/timeout', '--signal=TERM', '--kill-after=5s', '60s', '/bin/bash', '--noprofile', '--norc', '-s', '--') + $TailArguments
    return Invoke-CapturedProcess -FilePath $ssh -Arguments $arguments -InputBytes ([Text.UTF8Encoding]::new($false).GetBytes($script:RemotePayload)) -TimeoutMilliseconds 90000
}

function Invoke-SelfTest {
    $probe = [Text.UTF8Encoding]::new($false).GetBytes($script:ProbeText)
    if ($probe.Length -ne $script:ProbeSize -or (Get-SHA256Lower -Bytes $probe) -cne $script:ProbeSHA) { throw 'probe_identity' }
    if ($script:RemotePayload -notmatch 'rm -f -- "\$probe"') { throw 'remote_contract' }
    $bash = 'C:\Program Files\Git\bin\bash.exe'
    if (-not [IO.File]::Exists($bash)) { throw 'bash_missing' }
    $syntaxPath = Join-Path ([IO.Path]::GetFullPath([IO.Path]::GetTempPath())) ('molin-legacy-scp-syntax-' + [Guid]::NewGuid().ToString('N') + '.sh')
    try {
        [IO.File]::WriteAllBytes($syntaxPath, [Text.UTF8Encoding]::new($false).GetBytes($script:RemotePayload + "`n"))
        & $bash -n $syntaxPath
        if ($LASTEXITCODE -ne 0) { throw 'bash_syntax' }
    }
    finally {
        if ([IO.File]::Exists($syntaxPath)) { [IO.File]::Delete($syntaxPath) }
    }
    $processProbe = Invoke-CapturedProcess -FilePath $bash -Arguments @('-c', 'cat') -InputBytes ([Text.UTF8Encoding]::new($false).GetBytes("process-probe`n")) -TimeoutMilliseconds 10000
    if ($processProbe.ExitCode -ne 0 -or $processProbe.Stdout -cne "process-probe`n" -or $processProbe.StderrLength -ne 0) { throw 'process_exit_code_regression' }
    # SCP 不需要标准输入；固定回归空字节数组，防止参数绑定在启动进程前失败。
    $emptyInputProbe = Invoke-CapturedProcess -FilePath $bash -Arguments @('-c', 'exit 0') -InputBytes ([byte[]]@()) -TimeoutMilliseconds 10000
    if ($emptyInputProbe.ExitCode -ne 0 -or $emptyInputProbe.StdoutLength -ne 0 -or $emptyInputProbe.StderrLength -ne 0) { throw 'empty_input_process_regression' }
    Write-Output 'status=pass mode=email_unknown_legacy_scp_probe_selftest probe_identity=true ssh_attempts=0 scp_attempts=0 writes=false database_access=false redis_access=false restart=false retries=0'
}

if ($SelfTest) {
    if ($Execute -or $Confirm) { throw 'selftest_arguments' }
    Invoke-SelfTest
    exit 0
}
if (-not $Execute -or $Confirm -cne $script:ConfirmPhrase) { throw 'confirmation_required' }

$probeBytes = [Text.UTF8Encoding]::new($false).GetBytes($script:ProbeText)
if ($probeBytes.Length -ne $script:ProbeSize -or (Get-SHA256Lower -Bytes $probeBytes) -cne $script:ProbeSHA) { throw 'probe_identity' }
$scp = Join-Path $env:WINDIR 'System32\OpenSSH\scp.exe'
if (-not [IO.File]::Exists($scp)) { throw 'scp_missing' }
$sshAttempts = 0; $scpAttempts = 0; $stage = 'preflight'; $lastExit = -1; $stdoutLength = 0; $stderrLength = 0
$probeRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar)
$probeDirectory = [IO.Path]::GetFullPath((Join-Path $probeRoot ('molin-legacy-scp-probe-' + [Guid]::NewGuid().ToString('N'))))
$probePath = Join-Path $probeDirectory $script:ProbeName
try {
    $sshAttempts++
    $preflight = Invoke-OneSSH -TailArguments @('preflight')
    $lastExit = $preflight.ExitCode; $stdoutLength = $preflight.StdoutLength; $stderrLength = $preflight.StderrLength
    $match = [regex]::Match($preflight.Stdout, $script:PreflightPattern)
    if (-not $match.Success -or $preflight.ExitCode -ne 0 -or $preflight.StderrLength -ne 0) { throw 'preflight_failed' }
    $nonce = $match.Groups['nonce'].Value

    if ([IO.Directory]::Exists($probeDirectory) -or -not $probeDirectory.StartsWith($probeRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'probe_temp_invalid' }
    [void][IO.Directory]::CreateDirectory($probeDirectory)
    [IO.File]::WriteAllBytes($probePath, $probeBytes)
    $remoteTarget = $script:Remote + ":/home/pc/molin-runtime/email-unknown-cycle-$nonce/$($script:ProbeName)"
    $stage = 'legacy_scp_upload'
    $scpAttempts++
    $scpResult = Invoke-CapturedProcess -FilePath $scp -Arguments @('-q', '-O', '-P', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', $probePath, $remoteTarget) -InputBytes ([byte[]]@()) -TimeoutMilliseconds 30000
    $lastExit = $scpResult.ExitCode; $stdoutLength = $scpResult.StdoutLength; $stderrLength = $scpResult.StderrLength
    if ($scpResult.ExitCode -ne 0 -or $scpResult.StdoutLength -ne 0 -or $scpResult.StderrLength -ne 0) { throw 'legacy_scp_failed' }

    $stage = 'verify_cleanup'
    $sshAttempts++
    $verify = Invoke-OneSSH -TailArguments @('verify_cleanup', $nonce, $script:ProbeSHA, [string]$script:ProbeSize)
    $lastExit = $verify.ExitCode; $stdoutLength = $verify.StdoutLength; $stderrLength = $verify.StderrLength
    if ($verify.ExitCode -ne 0 -or $verify.StderrLength -ne 0 -or $verify.Stdout -cnotmatch $script:VerifyPattern) { throw 'verify_cleanup_failed' }
    Write-Output 'status=pass mode=email_unknown_legacy_scp_probe legacy_scp=true probe_uploaded=true probe_hash_match=true probe_removed=true stage_empty=true stage_retained=true ssh_attempts=2 scp_attempts=1 retries=0 database_access=false redis_access=false restart=false'
}
catch {
    Write-Output ("status=failed mode=email_unknown_legacy_scp_probe stage=$stage exit_code=$lastExit stdout_length=$stdoutLength stderr_length=$stderrLength retained=true ssh_attempts=$sshAttempts scp_attempts=$scpAttempts retries=0 database_access=false redis_access=false restart=false")
    throw 'legacy_scp_probe_failed'
}
finally {
    if ([IO.File]::Exists($probePath)) { [IO.File]::Delete($probePath) }
    if ([IO.Directory]::Exists($probeDirectory)) {
        if ([IO.Directory]::GetFileSystemEntries($probeDirectory).Length -ne 0) { throw 'probe_temp_not_empty' }
        [IO.Directory]::Delete($probeDirectory, $false)
    }
}
