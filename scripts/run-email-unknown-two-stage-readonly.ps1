[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)][switch]$SelfTest,
    [Parameter(Mandatory = $false)][switch]$Execute,
    [Parameter(Mandatory = $false)][string]$Confirm
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:ConfirmPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_TWO_STAGE_READONLY_ONCE'
$script:PayloadPath = Join-Path $PSScriptRoot 'email-unknown-two-stage-readonly.payload.sh'
$script:PayloadSHA = 'c6e42ab04b8403716ff906a4b20ff81497dcf527ad61bd813e55c9a77a7c994f'
$script:Remote = 'pc@8.130.9.163'
$script:OutputPattern = '^status=pass mode=email_unknown_two_stage_readonly classification=two_stages_classified stage_count=2 empty_count=(?<empty>[0-2]) partial_count=(?<partial>[0-2]) complete_count=(?<complete>[0-2]) parent_writable=true stages_identity=true writes=false database_access=false redis_access=false cleanup=false restart=false scp=false retries=0\r?\n?$'

function Read-VerifiedPayload {
    $path = [IO.Path]::GetFullPath($script:PayloadPath)
    $item = [IO.FileInfo]::new($path)
    if (-not $item.Exists -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.DirectoryName -cne [IO.Path]::GetFullPath($PSScriptRoot)) { throw 'payload_identity' }
    $bytes = [IO.File]::ReadAllBytes($path)
    if ($bytes.Length -lt 512 -or ($bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) -or $bytes -contains 0) { throw 'payload_encoding' }
    [void][Text.UTF8Encoding]::new($false, $true).GetString($bytes)
    if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -cne $script:PayloadSHA) { throw 'payload_hash' }
    return ,$bytes
}

function Invoke-OneSSH {
    param([Parameter(Mandatory = $true)][byte[]]$Payload)
    $ssh = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
    if (-not [IO.File]::Exists($ssh)) { throw 'ssh_missing' }
    $root = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $temporary = [IO.Path]::GetFullPath((Join-Path $root ('molin-two-stage-readonly-' + [Guid]::NewGuid().ToString('N'))))
    [void][IO.Directory]::CreateDirectory($temporary)
    $stdin = Join-Path $temporary 'stdin.bin'; $stdout = Join-Path $temporary 'stdout.bin'; $stderr = Join-Path $temporary 'stderr.bin'
    $process = $null
    try {
        [IO.File]::WriteAllBytes($stdin, $Payload)
        $arguments = @('-T', '-p', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', $script:Remote, '/usr/bin/env', '-i', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'HOME=/home/pc', 'USER=pc', 'LOGNAME=pc', 'LANG=C.UTF-8', '/usr/bin/timeout', '--signal=TERM', '--kill-after=5s', '60s', '/bin/bash', '--noprofile', '--norc', '-s', '--')
        $process = Start-Process -FilePath $ssh -ArgumentList $arguments -RedirectStandardInput $stdin -RedirectStandardOutput $stdout -RedirectStandardError $stderr -NoNewWindow -PassThru
        $handle = $process.Handle
        if ($handle -eq [IntPtr]::Zero) { throw 'process_handle' }
        if (-not $process.WaitForExit(90000)) { $process.Kill(); $process.WaitForExit(); throw 'process_timeout' }
        $process.WaitForExit(); $process.Refresh()
        $stdoutBytes = [IO.File]::ReadAllBytes($stdout); $stderrBytes = [IO.File]::ReadAllBytes($stderr)
        return [pscustomobject]@{ ExitCode=$process.ExitCode; Stdout=[Text.UTF8Encoding]::new($false,$true).GetString($stdoutBytes); StdoutLength=$stdoutBytes.Length; StderrLength=$stderrBytes.Length }
    }
    finally {
        if ($null -ne $process) { $process.Dispose() }
        foreach ($path in @($stdin,$stdout,$stderr)) { if ([IO.File]::Exists($path)) { [IO.File]::Delete($path) } }
        if ([IO.Directory]::Exists($temporary)) {
            if ([IO.Directory]::GetFileSystemEntries($temporary).Length -ne 0) { throw 'temp_not_empty' }
            [IO.Directory]::Delete($temporary,$false)
        }
    }
}

function Invoke-SelfTest {
    $payload = Read-VerifiedPayload
    $text = [Text.UTF8Encoding]::new($false,$true).GetString($payload)
    if ($text -match '(?im)\b(?:rm|rmdir|unlink|chmod|chown|touch|mkdir|mysql|redis-cli|docker)\b') { throw 'readonly_contract' }
    $bash = 'C:\Program Files\Git\bin\bash.exe'
    if (-not [IO.File]::Exists($bash)) { throw 'bash_missing' }
    $syntax = Join-Path ([IO.Path]::GetTempPath()) ('molin-two-stage-' + [Guid]::NewGuid().ToString('N') + '.sh')
    try { [IO.File]::WriteAllBytes($syntax,$payload); & $bash -n $syntax; if ($LASTEXITCODE -ne 0) { throw 'bash_syntax' } }
    finally { if ([IO.File]::Exists($syntax)) { [IO.File]::Delete($syntax) } }
    Write-Output 'status=pass mode=email_unknown_two_stage_readonly_selftest external_access=false writes=false database_access=false redis_access=false cleanup=false restart=false scp=false retries=0'
}

if ($SelfTest) {
    if ($Execute -or $Confirm) { throw 'selftest_arguments' }
    Invoke-SelfTest
    exit 0
}
if (-not $Execute -or $Confirm -cne $script:ConfirmPhrase) { throw 'confirmation_required' }
$result = Invoke-OneSSH -Payload (Read-VerifiedPayload)
$match = [regex]::Match($result.Stdout,$script:OutputPattern)
if ($result.ExitCode -ne 0 -or $result.StderrLength -ne 0 -or -not $match.Success) {
    Write-Output ("status=failed mode=email_unknown_two_stage_readonly classification=closed exit_code=$($result.ExitCode) stdout_length=$($result.StdoutLength) stderr_length=$($result.StderrLength) retained=true ssh_attempts=1 scp_attempts=0 retries=0")
    throw 'two_stage_readonly_failed'
}
$empty=[int]$match.Groups['empty'].Value; $partial=[int]$match.Groups['partial'].Value; $complete=[int]$match.Groups['complete'].Value
if (($empty + $partial + $complete) -ne 2) { throw 'count_invariant' }
Write-Output ("status=pass mode=email_unknown_two_stage_readonly classification=two_stages_classified stage_count=2 empty_count=$empty partial_count=$partial complete_count=$complete retained=true ssh_attempts=1 scp_attempts=0 writes=false database_access=false redis_access=false cleanup=false restart=false retries=0")
