[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Nonce
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
if($Nonce-cnotmatch'^[a-f0-9]{32}$'){throw 'argument_identity'}
$payload=Join-Path $PSScriptRoot 'email-migration-failed-stage-postcheck.payload.sh'
$bash='C:\Program Files\Git\bin\bash.exe';if(-not[IO.File]::Exists($bash)){throw 'bash_missing'}
& $bash -n $payload;if($LASTEXITCODE-ne 0){throw 'bash_syntax'}
$ssh=Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe';if(-not[IO.File]::Exists($ssh)){throw 'ssh_missing'}
$arguments=@('-T','-p','10003','-o','BatchMode=yes','-o','NumberOfPasswordPrompts=0','-o','StrictHostKeyChecking=yes','-o','ConnectTimeout=10','pc@8.130.9.163','/usr/bin/env','-i','PATH=/usr/sbin:/usr/bin:/sbin:/bin','HOME=/home/pc','USER=pc','LOGNAME=pc','LANG=C.UTF-8','/bin/bash','--noprofile','--norc','-s','--',$Nonce)
$global:LASTEXITCODE=0
Get-Content -Raw -Encoding UTF8 -LiteralPath $payload | & $ssh @arguments
$exitCode=$global:LASTEXITCODE
exit $exitCode
