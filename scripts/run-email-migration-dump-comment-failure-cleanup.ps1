[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)][switch]$SelfTest,
    [Parameter(Mandatory = $false)][switch]$Execute,
    [Parameter(Mandatory = $false)][string]$Confirm,
    [Parameter(Mandatory = $false)][string]$Nonce,
    [Parameter(Mandatory = $false)][string]$ArchiveSHA
)

Set-StrictMode -Version 2.0
$ErrorActionPreference='Stop'
$confirmPhrase='I_CONFIRM_EMAIL_MIGRATION_DUMP_COMMENT_FAILURE_CLEANUP_ONCE'
$payload=Join-Path $PSScriptRoot 'email-migration-dump-comment-failure-cleanup.payload.sh'
if($SelfTest){
    if($Execute-or$Confirm-or$Nonce-or$ArchiveSHA){throw 'selftest_arguments'}
    $bash='C:\Program Files\Git\bin\bash.exe';if(-not[IO.File]::Exists($bash)){throw 'bash_missing'}
    & $bash -n $payload;if($LASTEXITCODE-ne 0){throw 'bash_syntax'}
    Write-Output 'status=pass mode=email_migration_dump_comment_failure_cleanup_selftest external_access=false database_access=false docker_access=false retries=0'
    exit 0
}
if(-not$Execute-or$Confirm-cne$confirmPhrase){throw 'confirmation_required'}
if($Nonce-cnotmatch'^[a-f0-9]{32}$'-or$ArchiveSHA-cnotmatch'^[a-f0-9]{64}$'){throw 'argument_identity'}
$ssh=Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe';if(-not[IO.File]::Exists($ssh)){throw 'ssh_missing'}
$arguments=@('-T','-p','10003','-o','BatchMode=yes','-o','NumberOfPasswordPrompts=0','-o','StrictHostKeyChecking=yes','-o','ConnectTimeout=10','pc@8.130.9.163','/usr/bin/env','-i','PATH=/usr/sbin:/usr/bin:/sbin:/bin','HOME=/home/pc','USER=pc','LOGNAME=pc','LANG=C.UTF-8','/bin/bash','--noprofile','--norc','-s','--','--execute',$Nonce,$ArchiveSHA)
$global:LASTEXITCODE=0
Get-Content -Raw -Encoding UTF8 -LiteralPath $payload | & $ssh @arguments
$exitCode=$global:LASTEXITCODE
exit $exitCode
