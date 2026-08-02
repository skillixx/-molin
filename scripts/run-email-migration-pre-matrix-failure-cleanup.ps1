[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)][switch]$SelfTest,
    [Parameter(Mandatory = $false)][switch]$Execute,
    [Parameter(Mandatory = $false)][switch]$Unique,
    [Parameter(Mandatory = $false)][string]$Confirm,
    [Parameter(Mandatory = $false)][string]$Nonce,
    [Parameter(Mandatory = $false)][string]$ArchiveSHA
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$confirmPhrase = 'I_CONFIRM_EMAIL_MIGRATION_PRE_MATRIX_FAILURE_CLEANUP_ONCE'
$payload = Join-Path $PSScriptRoot 'email-migration-pre-matrix-failure-cleanup.payload.sh'
$payloadSHA = '0E1AFBE17EC5DAFA4E97B55A2E259FED21DE22E9D5C8EBB0CB221F2F4EAD2E6C'
$payloadFile = [IO.FileInfo]::new([IO.Path]::GetFullPath($payload))
if (-not $payloadFile.Exists -or
    ($payloadFile.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
    $payloadFile.DirectoryName -cne [IO.Path]::GetFullPath($PSScriptRoot) -or
    (Get-FileHash -Algorithm SHA256 -LiteralPath $payloadFile.FullName).Hash -cne $payloadSHA) {
    throw 'asset_identity'
}

function Assert-ControllerAst {
    $tokens = $null
    $errors = $null
    $path = [IO.Path]::GetFullPath($PSCommandPath)
    $ast = [Management.Automation.Language.Parser]::ParseFile($path, [ref]$tokens, [ref]$errors)
    if ($errors.Count -ne 0) { throw 'controller_ast_parse' }
    $source = [IO.File]::ReadAllText($path, [Text.Encoding]::UTF8)
    $offset = $source.IndexOf("if (-not `$Execute -or `$Confirm -cne `$confirmPhrase)", [StringComparison]::Ordinal)
    if ($offset -lt 0) { throw 'controller_execute_boundary' }
    $commands = @($ast.FindAll({
        param($node)
        $node -is [Management.Automation.Language.CommandAst] -and $node.Extent.StartOffset -gt $offset
    }, $true))
    $assignments = @($ast.FindAll({
        param($node)
        $node -is [Management.Automation.Language.AssignmentStatementAst] -and $node.Extent.StartOffset -gt $offset
    }, $true))
    if (@($commands | Where-Object { $_.GetCommandName() -ceq 'Invoke-FixedSsh' }).Count -ne 1 -or
        @($assignments | Where-Object { $_.Left.Extent.Text -ceq '$result' }).Count -ne 1) {
        throw 'controller_single_execution'
    }
}

function Get-PayloadText {
    $bytes = [IO.File]::ReadAllBytes($payloadFile.FullName)
    if ($bytes.Length -eq 0 -or ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) -or
        $bytes -contains 0 -or $bytes -contains 13) { throw 'payload_encoding' }
    return [Text.UTF8Encoding]::new($false, $true).GetString($bytes)
}

function Invoke-FixedSsh {
    param([string]$Ssh, [string[]]$Arguments, [string]$InputText)
    $temporary = [IO.Path]::GetFullPath((Join-Path ([IO.Path]::GetTempPath()) ('molin-email-pre-matrix-cleanup-' + [Guid]::NewGuid().ToString('N'))))
    [void][IO.Directory]::CreateDirectory($temporary)
    $stdin = Join-Path $temporary 'stdin.txt'; $stdout = Join-Path $temporary 'stdout.txt'; $stderr = Join-Path $temporary 'stderr.txt'; $process = $null
    try {
        [IO.File]::WriteAllBytes($stdin, [Text.UTF8Encoding]::new($false, $true).GetBytes($InputText))
        $process = Start-Process -FilePath $Ssh -ArgumentList $Arguments -RedirectStandardInput $stdin -RedirectStandardOutput $stdout -RedirectStandardError $stderr -NoNewWindow -PassThru
        if (-not $process.WaitForExit(60000)) { $process.Kill(); $process.WaitForExit(); throw 'ssh_timeout' }
        $process.Refresh()
        return [pscustomobject]@{
            ExitCode = $process.ExitCode
            Output = [Text.UTF8Encoding]::new($false, $true).GetString([IO.File]::ReadAllBytes($stdout))
            ErrorLength = [IO.File]::ReadAllBytes($stderr).Length
        }
    }
    finally {
        if ($null -ne $process) { $process.Dispose() }
        foreach ($path in @($stdin, $stdout, $stderr)) { if ([IO.File]::Exists($path)) { [IO.File]::Delete($path) } }
        if ([IO.Directory]::Exists($temporary)) { if ([IO.Directory]::GetFileSystemEntries($temporary).Length -ne 0) { throw 'temporary_not_empty' }; [IO.Directory]::Delete($temporary, $false) }
    }
}

if ($SelfTest) {
    if ($Execute -or $Unique -or $Confirm -or $Nonce -or $ArchiveSHA) { throw 'selftest_arguments' }
    Assert-ControllerAst
    $bash = 'C:\Program Files\Git\bin\bash.exe'
    if (-not [IO.File]::Exists($bash)) { throw 'bash_missing' }
    & $bash --noprofile --norc -n $payloadFile.FullName
    if ($LASTEXITCODE -ne 0) { throw 'bash_syntax' }
    $null = Get-PayloadText
    Write-Output 'status=pass mode=email_migration_pre_matrix_failure_cleanup_selftest external_access=false database_access=false docker_writes=false retries=0'
    exit 0
}

if (-not $Execute -or $Confirm -cne $confirmPhrase) { throw 'confirmation_required' }
if ($Unique) {
    if ($Nonce -or $ArchiveSHA) { throw 'argument_identity' }
    $executeTail = @('--execute', '--unique')
}
else {
    if ($Nonce -cnotmatch '^[a-f0-9]{32}$' -or $ArchiveSHA -cnotmatch '^[a-f0-9]{64}$') { throw 'argument_identity' }
    $executeTail = @('--execute', $Nonce, $ArchiveSHA)
}
$ssh = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
if (-not [IO.File]::Exists($ssh)) { throw 'ssh_missing' }
$arguments = @(
    '-T', '-p', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0',
    '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', 'pc@8.130.9.163',
    '/usr/bin/env', '-i', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'HOME=/home/pc',
    'USER=pc', 'LOGNAME=pc', 'LANG=C.UTF-8', '/bin/bash', '--noprofile', '--norc',
    '-s', '--'
)
$arguments += $executeTail

$result = Invoke-FixedSsh -Ssh $ssh -Arguments $arguments -InputText (Get-PayloadText)
if ($result.ErrorLength -ne 0 -or $result.Output -cnotmatch '^status=(?:pass|failed) mode=email_migration_pre_matrix_failure_cleanup .+retries=0\r?\n?$') { throw 'remote_output' }
Write-Output $result.Output.TrimEnd("`r", "`n")
exit $result.ExitCode
