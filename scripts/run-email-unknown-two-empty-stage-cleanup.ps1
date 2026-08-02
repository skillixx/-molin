[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)][switch]$SelfTest,
    [Parameter(Mandatory = $false)][switch]$Execute,
    [Parameter(Mandatory = $false)][string]$Confirm
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:ConfirmPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_TWO_EMPTY_STAGE_CLEANUP_ONCE'
$script:PayloadPath = Join-Path $PSScriptRoot 'email-unknown-two-empty-stage-cleanup.payload.sh'
$script:PayloadSHA = '3dd5eed1f73490ff0e23ab55166d205ebf2a2228346d9fdb0bdaa73a2e4fcad6'
$script:Remote = 'pc@8.130.9.163'
$script:SuccessPattern = '^status=pass mode=email_unknown_two_empty_stage_cleanup classification=two_empty_stages_removed stage_count=2 empty_checks=4 removed_count=2 remaining_count=0 writes=true database_access=false redis_access=false restart=false scp=false retries=0\r?\n?$'

function Read-VerifiedPayload {
    $path=[IO.Path]::GetFullPath($script:PayloadPath); $item=[IO.FileInfo]::new($path)
    if(-not $item.Exists -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.DirectoryName -cne [IO.Path]::GetFullPath($PSScriptRoot)){throw 'payload_identity'}
    $bytes=[IO.File]::ReadAllBytes($path)
    if($bytes.Length -lt 512 -or ($bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) -or $bytes -contains 0){throw 'payload_encoding'}
    [void][Text.UTF8Encoding]::new($false,$true).GetString($bytes)
    if((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -cne $script:PayloadSHA){throw 'payload_hash'}
    return ,$bytes
}

function Invoke-OneSSH {
    param([Parameter(Mandatory=$true)][byte[]]$Payload)
    $ssh=Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'; if(-not [IO.File]::Exists($ssh)){throw 'ssh_missing'}
    $root=[IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $temporary=[IO.Path]::GetFullPath((Join-Path $root ('molin-two-empty-cleanup-'+[Guid]::NewGuid().ToString('N')))); [void][IO.Directory]::CreateDirectory($temporary)
    $stdin=Join-Path $temporary 'stdin.bin';$stdout=Join-Path $temporary 'stdout.bin';$stderr=Join-Path $temporary 'stderr.bin';$process=$null
    try{
        [IO.File]::WriteAllBytes($stdin,$Payload)
        $arguments=@('-T','-p','10003','-o','BatchMode=yes','-o','NumberOfPasswordPrompts=0','-o','StrictHostKeyChecking=yes','-o','ConnectTimeout=10',$script:Remote,'/usr/bin/env','-i','PATH=/usr/sbin:/usr/bin:/sbin:/bin','HOME=/home/pc','USER=pc','LOGNAME=pc','LANG=C.UTF-8','/usr/bin/timeout','--signal=TERM','--kill-after=5s','60s','/bin/bash','--noprofile','--norc','-s','--')
        $process=Start-Process -FilePath $ssh -ArgumentList $arguments -RedirectStandardInput $stdin -RedirectStandardOutput $stdout -RedirectStandardError $stderr -NoNewWindow -PassThru
        $handle=$process.Handle;if($handle -eq [IntPtr]::Zero){throw 'process_handle'}
        if(-not $process.WaitForExit(90000)){$process.Kill();$process.WaitForExit();throw 'process_timeout'}
        $process.WaitForExit();$process.Refresh();$stdoutBytes=[IO.File]::ReadAllBytes($stdout);$stderrBytes=[IO.File]::ReadAllBytes($stderr)
        return [pscustomobject]@{ExitCode=$process.ExitCode;Stdout=[Text.UTF8Encoding]::new($false,$true).GetString($stdoutBytes);StdoutLength=$stdoutBytes.Length;StderrLength=$stderrBytes.Length}
    }finally{
        if($null -ne $process){$process.Dispose()};foreach($path in @($stdin,$stdout,$stderr)){if([IO.File]::Exists($path)){[IO.File]::Delete($path)}}
        if([IO.Directory]::Exists($temporary)){if([IO.Directory]::GetFileSystemEntries($temporary).Length -ne 0){throw 'temp_not_empty'};[IO.Directory]::Delete($temporary,$false)}
    }
}

function Invoke-SelfTest {
    $payload=Read-VerifiedPayload;$text=[Text.UTF8Encoding]::new($false,$true).GetString($payload)
    if($text -notmatch 'rmdir -- "\$\{stages\[0\]\}" "\$\{stages\[1\]\}"' -or $text -match '(?im)\b(?:rm|unlink|mysql|redis-cli|docker)\b'){throw 'cleanup_contract'}
    $bash='C:\Program Files\Git\bin\bash.exe';if(-not [IO.File]::Exists($bash)){throw 'bash_missing'}
    $syntax=Join-Path ([IO.Path]::GetTempPath()) ('molin-two-empty-'+[Guid]::NewGuid().ToString('N')+'.sh')
    try{[IO.File]::WriteAllBytes($syntax,$payload);& $bash -n $syntax;if($LASTEXITCODE -ne 0){throw 'bash_syntax'}}finally{if([IO.File]::Exists($syntax)){[IO.File]::Delete($syntax)}}
    Write-Output 'status=pass mode=email_unknown_two_empty_stage_cleanup_selftest external_access=false writes=false database_access=false redis_access=false restart=false scp=false retries=0'
}

if($SelfTest){if($Execute -or $Confirm){throw 'selftest_arguments'};Invoke-SelfTest;exit 0}
if(-not $Execute -or $Confirm -cne $script:ConfirmPhrase){throw 'confirmation_required'}
$result=Invoke-OneSSH -Payload (Read-VerifiedPayload)
if($result.ExitCode -ne 0 -or $result.StderrLength -ne 0 -or $result.Stdout -cnotmatch $script:SuccessPattern){
    Write-Output ("status=failed mode=email_unknown_two_empty_stage_cleanup classification=closed exit_code=$($result.ExitCode) stdout_length=$($result.StdoutLength) stderr_length=$($result.StderrLength) retained=unknown ssh_attempts=1 scp_attempts=0 retries=0")
    throw 'two_empty_stage_cleanup_failed'
}
Write-Output 'status=pass mode=email_unknown_two_empty_stage_cleanup classification=two_empty_stages_removed stage_count=2 empty_checks=4 removed_count=2 remaining_count=0 ssh_attempts=1 scp_attempts=0 retries=0 database_access=false redis_access=false restart=false'
