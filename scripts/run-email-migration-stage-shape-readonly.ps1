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

$confirmPhrase = 'I_CONFIRM_EMAIL_MIGRATION_STAGE_SHAPE_READONLY_ONCE'
$payload = Join-Path $PSScriptRoot 'email-migration-stage-shape-readonly.payload.sh'
$payloadSHA = '2B4A5AECDCC8A1D426B4F182B3E12B6ED8C2BBEE4E263CBA2A5ADC3C368EEF22'

$payloadFile = [IO.FileInfo]::new([IO.Path]::GetFullPath($payload))
if (-not $payloadFile.Exists -or
    ($payloadFile.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
    $payloadFile.DirectoryName -cne [IO.Path]::GetFullPath($PSScriptRoot) -or
    (Get-FileHash -Algorithm SHA256 -LiteralPath $payloadFile.FullName).Hash -cne $payloadSHA) {
    throw 'asset_identity'
}

if ($SelfTest) {
    if ($Execute -or $Confirm -or $Nonce -or $ArchiveSHA) { throw 'selftest_arguments' }
    $bash = 'C:\Program Files\Git\bin\bash.exe'
    if (-not [IO.File]::Exists($bash)) { throw 'bash_missing' }
    & $bash --noprofile --norc -n $payloadFile.FullName
    if ($LASTEXITCODE -ne 0) { throw 'bash_syntax' }
    $tokens = $null
    $errors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile($PSCommandPath, [ref]$tokens, [ref]$errors)
    if ($errors.Count -ne 0) { throw 'powershell_ast' }
    $assignments = $ast.FindAll({
        param($node)
        $node -is [Management.Automation.Language.AssignmentStatementAst] -and
        $node.Left.Extent.Text -eq '$global:LASTEXITCODE'
    }, $true)
    if ($assignments.Count -ne 1) { throw 'formal_branch_assignments' }
    Write-Output 'status=pass mode=email_migration_stage_shape_readonly_selftest external_access=false writes=false database_access=false docker_access=false retries=0'
    exit 0
}

if (-not $Execute -or $Confirm -cne $confirmPhrase) { throw 'confirmation_required' }
if ($Nonce -cnotmatch '^[a-f0-9]{32}$' -or $ArchiveSHA -cnotmatch '^[a-f0-9]{64}$') { throw 'argument_identity' }
$ssh = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
if (-not [IO.File]::Exists($ssh)) { throw 'ssh_missing' }
$arguments = @(
    '-T', '-p', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0',
    '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', 'pc@8.130.9.163',
    '/usr/bin/env', '-i', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'HOME=/home/pc',
    'USER=pc', 'LOGNAME=pc', 'LANG=C.UTF-8', '/bin/bash', '--noprofile', '--norc',
    '-s', '--', $Nonce, $ArchiveSHA
)

$global:LASTEXITCODE = 0
Get-Content -Raw -Encoding UTF8 -LiteralPath $payloadFile.FullName | & $ssh @arguments
exit $LASTEXITCODE
