[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)][switch]$SelfTest,
    [Parameter(Mandatory = $false)][switch]$Execute,
    [Parameter(Mandatory = $false)][string]$Confirm,
    [Parameter(Mandatory = $false)][string]$Nonce,
    [Parameter(Mandatory = $false)][string]$ArchiveSHA
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$confirmPhrase='I_CONFIRM_EMAIL_MIGRATION_MANUAL_REMOTE_ONCE'
$root=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$controllerPath=[IO.Path]::GetFullPath($PSCommandPath)
$wrapper=Join-Path $PSScriptRoot 'email-migration-manual-execute.payload.sh'
$matrixPayload=Join-Path $PSScriptRoot 'email-migration-matrix-remote.payload.sh'
$generator=Join-Path $PSScriptRoot 'generate-email-migration-baselines.sh'
$wrapperSHA='0F3D58226276D025D92D7882F793B339F805AAA43F959B586013BD86250EC92E'
$matrixPayloadSHA='5B6727610E34C7AC3810040B273927414E53E34EA1A5FE0DAC97850B0E4C3966'
$generatorSHA='72FD289456FAE939136F026E322A01F0A308AE34CD34C58C4F7DD5B5F20FF3ED'

foreach($item in @(@($wrapper,$wrapperSHA),@($matrixPayload,$matrixPayloadSHA),@($generator,$generatorSHA))){
    $file=[IO.FileInfo]::new([IO.Path]::GetFullPath($item[0]))
    if(-not$file.Exists-or($file.Attributes-band[IO.FileAttributes]::ReparsePoint)-ne 0-or$file.DirectoryName-cne[IO.Path]::GetFullPath($PSScriptRoot)-or(Get-FileHash -Algorithm SHA256 -LiteralPath $file.FullName).Hash-cne$item[1]){throw 'asset_identity'}
}

function Get-WrapperText {
    $bytes=[IO.File]::ReadAllBytes($wrapper)
    if($bytes.Length-eq 0-or($bytes.Length-ge 3-and$bytes[0]-eq 0xEF-and$bytes[1]-eq 0xBB-and$bytes[2]-eq 0xBF)-or$bytes-contains 0-or$bytes-contains 13){throw 'wrapper_encoding'}
    return [Text.UTF8Encoding]::new($false,$true).GetString($bytes)
}

function Assert-ControllerAst {
    $tokens=$null;$errors=$null
    $ast=[Management.Automation.Language.Parser]::ParseFile($controllerPath,[ref]$tokens,[ref]$errors)
    if($errors.Count-ne 0){throw 'controller_ast_parse'}
    $source=[IO.File]::ReadAllText($controllerPath,[Text.Encoding]::UTF8)
    $offset=$source.IndexOf("if(-not`$Execute-or`$Confirm-cne`$confirmPhrase)",[StringComparison]::Ordinal)
    if($offset-lt 0){throw 'controller_execute_boundary'}
    $commands=@($ast.FindAll({param($node)$node-is[Management.Automation.Language.CommandAst]-and$node.Extent.StartOffset-gt$offset},$true))
    if(@($commands|Where-Object{$_.GetCommandName()-ceq'Invoke-FixedSsh'}).Count-ne 1){throw 'controller_ssh_budget'}
}

function Invoke-FixedSsh {
    param([string]$Ssh,[string[]]$Arguments,[string]$InputText)
    $temporary=[IO.Path]::GetFullPath((Join-Path ([IO.Path]::GetTempPath()) ('molin-email-manual-remote-'+[Guid]::NewGuid().ToString('N'))))
    [void][IO.Directory]::CreateDirectory($temporary)
    $stdin=Join-Path $temporary 'stdin.txt';$stdout=Join-Path $temporary 'stdout.txt';$stderr=Join-Path $temporary 'stderr.txt';$process=$null
    try{
        [IO.File]::WriteAllBytes($stdin,[Text.UTF8Encoding]::new($false,$true).GetBytes($InputText))
        $process=Start-Process -FilePath $Ssh -ArgumentList $Arguments -RedirectStandardInput $stdin -RedirectStandardOutput $stdout -RedirectStandardError $stderr -NoNewWindow -PassThru
        if(-not$process.WaitForExit(7300000)){$process.Kill();$process.WaitForExit();throw 'ssh_timeout'}
        $process.Refresh();$output=[Text.UTF8Encoding]::new($false,$true).GetString([IO.File]::ReadAllBytes($stdout));$errorLength=[IO.File]::ReadAllBytes($stderr).Length
        if($process.ExitCode-ne 0-or$errorLength-ne 0){throw 'ssh_failed'}
        return $output
    }finally{
        if($null-ne$process){$process.Dispose()}
        foreach($path in @($stdin,$stdout,$stderr)){if([IO.File]::Exists($path)){[IO.File]::Delete($path)}}
        if([IO.Directory]::Exists($temporary)){if([IO.Directory]::GetFileSystemEntries($temporary).Length-ne 0){throw 'temporary_not_empty'};[IO.Directory]::Delete($temporary,$false)}
    }
}

if($SelfTest){
    if($Execute-or$Confirm-or$Nonce-or$ArchiveSHA){throw 'selftest_arguments'}
    Assert-ControllerAst
    $bash='C:\Program Files\Git\bin\bash.exe';if(-not[IO.File]::Exists($bash)){throw 'bash_missing'}
    & $bash -n $wrapper;if($LASTEXITCODE-ne 0){throw 'bash_syntax'}
    $null=Get-WrapperText
    Write-Output 'status=pass mode=email_migration_manual_remote_selftest external_access=false docker_access=false database_access=false migration_executed=false retries=0'
    exit 0
}

if(-not$Execute-or$Confirm-cne$confirmPhrase){throw 'confirmation_required'}
if($Nonce-cnotmatch'^[a-f0-9]{32}$'-or$ArchiveSHA-cnotmatch'^[a-f0-9]{64}$'){throw 'argument_identity'}
$ssh=Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe';if(-not[IO.File]::Exists($ssh)){throw 'ssh_missing'}
$arguments=@('-T','-p','10003','-o','BatchMode=yes','-o','NumberOfPasswordPrompts=0','-o','StrictHostKeyChecking=yes','-o','ConnectTimeout=10','pc@8.130.9.163','/usr/bin/env','-i','PATH=/usr/sbin:/usr/bin:/sbin:/bin','HOME=/home/pc','USER=pc','LOGNAME=pc','LANG=C.UTF-8','/usr/bin/timeout','--signal=TERM','--kill-after=10s','7200s','/bin/bash','--noprofile','--norc','-s','--',$Nonce,$ArchiveSHA,$matrixPayloadSHA,$generatorSHA)
# 使用进程对象读取固定退出码，避免管道子作用域和旧 LASTEXITCODE 污染验收结果。
$output=Invoke-FixedSsh -Ssh $ssh -Arguments $arguments -InputText (Get-WrapperText)
if($output-cnotmatch'(?s)^status=pass mode=email_migration_full_isolation_matrix .+\r?\nstatus=pass stage=remote_stage_removed\r?\n?$'){throw 'remote_output'}
Write-Output $output.TrimEnd("`r","`n")
