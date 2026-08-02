[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)][switch]$SelfTest,
    [Parameter(Mandatory = $false)][switch]$PrepareOnly,
    [Parameter(Mandatory = $false)][switch]$Execute,
    [Parameter(Mandatory = $false)][switch]$RecoverKnownFailure,
    [Parameter(Mandatory = $false)][string]$Confirm
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:ConfirmPhrase = 'I_CONFIRM_EMAIL_MIGRATION_FULL_ISOLATION_MATRIX_ONCE'
$script:RecoveryConfirmPhrase = 'I_CONFIRM_EMAIL_MIGRATION_PARTIAL55_BOUNDARY_AWK_STDERR_RECOVERY_ONCE'
$script:PreparePhrase = 'I_CONFIRM_LOCAL_EMAIL_MIGRATION_MANUAL_PACKAGE_ONCE'
$script:ControllerPath = [IO.Path]::GetFullPath($PSCommandPath)
$script:RepositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$script:Remote = 'pc@8.130.9.163'
$script:MigrationSetSHA = 'DE8D942A3C8BBB3E96456C1B85AE0BADAE7542E2A3E6FE0C34FD47C6140D914D'
$script:PayloadSHA = '5B6727610E34C7AC3810040B273927414E53E34EA1A5FE0DAC97850B0E4C3966'
$script:GeneratorSHA = '72FD289456FAE939136F026E322A01F0A308AE34CD34C58C4F7DD5B5F20FF3ED'
$script:ExecuteWrapperSHA = '0F3D58226276D025D92D7882F793B339F805AAA43F959B586013BD86250EC92E'
$script:RecoveryCleanupSHA = '139CC510DCD4D1D1146AB1433F041A8A1DA84047D07D72F02D07DC5DE997D1B3'
$script:FixedSources = [ordered]@{
    'scripts/email-migration-matrix-remote.payload.sh' = $script:PayloadSHA
    'scripts/generate-email-migration-baselines.sh' = $script:GeneratorSHA
    'tests/email/run-000055-container-isolation-matrix.sh' = '2A7076740F9EB12DE7BC317874A4C820784699591EF5C53F908CB9E85BADC7CB'
    'tests/email/run-000055-container-partial-matrix.sh' = '134D1935BFAEAF3F87037BC06386FC46E257255A279CF15AB96BDA1A03399E65'
    'tests/email/run-000056-container-isolation-matrix.sh' = '903C7C0EE442EA118D3A4F1ED2D4B8FB70373E8F9077EA8B90EA37646295E882'
    'tests/email/run-000056-container-partial-matrix.sh' = 'CC2588F354503355555EC338F2864B7754B5D8572448E15AADE231FD277B3DD3'
    'tests/email/000055-partial-boundaries.tsv' = '4B5E02DC0C72490B168A47637E1DD8E6298DFEBE18AC22CD9DCAF663B8E18585'
    'tests/email/000056-partial-boundaries.tsv' = '7B9E3132B2A09D939FD81E908C889EE6EE41A69B5D680B52A081D5A0A9BA4A62'
    'server/migrations/000055_add_directmail_email_management.down.sql' = '217B8FDAB63962284DA9D6EE1C436716687E351FE313E76F88E08C421D7C26EE'
    'server/migrations/000056_add_email_admin_verify_bootstrap.down.sql' = 'F42A30D70A95AD7BFD876F1515267C5FEE3DDCFD7AAC066453BDC020D201A5C2'
}
$script:SuccessPattern = '^(?:status=pass mode=email_migration_full_isolation_matrix mysql8_image_bound=true mysql8_runtime_verified=true baseline_generation=true baseline_outputs=6 matrix55=true partial55=true matrix56=true partial56=true runtime_unique_targets=94 temporary_containers_removed=true main_database_access=false main_database_modified=false retries=0\r?\n)(?:status=pass stage=remote_stage_removed\r?\n?)$'

function Get-SHA256Upper {
    param([Parameter(Mandatory = $true)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash
}

function Get-SHA256BytesUpper {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    return ([Security.Cryptography.SHA256]::Create().ComputeHash($Bytes) | ForEach-Object { $_.ToString('X2') }) -join ''
}

function Get-ExecutionWrapper {
    $path = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'email-migration-manual-execute.payload.sh'))
    $file = [IO.FileInfo]::new($path)
    if (-not $file.Exists -or ($file.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        $file.DirectoryName -cne [IO.Path]::GetFullPath($PSScriptRoot) -or
        (Get-SHA256Upper $path) -cne $script:ExecuteWrapperSHA) {
        throw 'execute_wrapper_identity'
    }
    $bytes = [IO.File]::ReadAllBytes($path)
    if ($bytes.Length -eq 0 -or ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) -or
        $bytes -contains 0 -or $bytes -contains 13) {
        throw 'execute_wrapper_encoding'
    }
    return [Text.UTF8Encoding]::new($false, $true).GetString($bytes)
}

function Get-RecoveryCleanupScript {
    $path = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'email-migration-matrix55-environment-failure-cleanup.payload.sh'))
    $file = [IO.FileInfo]::new($path)
    if (-not $file.Exists -or ($file.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        $file.DirectoryName -cne [IO.Path]::GetFullPath($PSScriptRoot) -or
        (Get-SHA256Upper $path) -cne $script:RecoveryCleanupSHA) {
        throw 'recovery_cleanup_identity'
    }
    $bytes = [IO.File]::ReadAllBytes($path)
    if ($bytes.Length -eq 0 -or ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) -or
        $bytes -contains 0 -or $bytes -contains 13) {
        throw 'recovery_cleanup_encoding'
    }
    return [Text.UTF8Encoding]::new($false, $true).GetString($bytes)
}

function Assert-ControllerAst {
    $tokens = $null
    $errors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile($script:ControllerPath, [ref]$tokens, [ref]$errors)
    if ($errors.Count -ne 0) { throw 'controller_ast_parse' }
    $source = [IO.File]::ReadAllText($script:ControllerPath, [Text.Encoding]::UTF8)
    $increments = @($ast.FindAll({
        param($node)
        $node -is [Management.Automation.Language.UnaryExpressionAst] -and $node.TokenKind -eq 'PostfixPlusPlus'
    }, $true) | ForEach-Object { $_.Child.Extent.Text })
    if (@($increments | Where-Object { $_ -ceq '$sshAttempts' }).Count -ne 2 -or
        @($increments | Where-Object { $_ -ceq '$scpAttempts' }).Count -ne 1) {
        throw 'controller_transport_budget'
    }
    $executeOffset = $source.IndexOf("if(-not`$Execute){throw 'confirmation_required'}", [StringComparison]::Ordinal)
    if ($executeOffset -lt 0) { throw 'controller_execute_boundary' }
    if ($source.IndexOf("if(`$RecoverKnownFailure){", $executeOffset, [StringComparison]::Ordinal) -lt 0 -or
        $source.IndexOf("`$script:RecoveryConfirmPhrase", $executeOffset, [StringComparison]::Ordinal) -lt 0) {
        throw 'controller_recovery_confirmation_boundary'
    }
    $formalAssignments = @($ast.FindAll({
        param($node)
        $node -is [Management.Automation.Language.AssignmentStatementAst] -and $node.Extent.StartOffset -gt $executeOffset
    }, $true))
    $formalCommands = @($ast.FindAll({
        param($node)
        $node -is [Management.Automation.Language.CommandAst] -and $node.Extent.StartOffset -gt $executeOffset
    }, $true))
    if (@($formalAssignments | Where-Object { $_.Left.Extent.Text -ceq '$nonce' }).Count -ne 1 -or
        @($formalAssignments | Where-Object { $_.Left.Extent.Text -ceq '$remoteStage' }).Count -ne 1 -or
        @($formalCommands | Where-Object { $_.GetCommandName() -ceq 'Invoke-RemoteScript' }).Count -ne 2 -or
        @($formalCommands | Where-Object { $_.GetCommandName() -ceq 'Invoke-NativeScp' }).Count -ne 1) {
        throw 'controller_single_execution'
    }
}

function Assert-RelativeSourcePath {
    param([Parameter(Mandatory = $true)][string]$Path)
    if ($Path -cnotmatch '^(?:scripts|tests/email|server/migrations)/[A-Za-z0-9._/-]+$' -or $Path.Contains('..') -or $Path.Contains('\')) {
        throw 'source_path_invalid'
    }
}

function Get-SourcePlan {
    $entries = New-Object Collections.Generic.List[object]
    foreach ($relative in $script:FixedSources.Keys) {
        Assert-RelativeSourcePath $relative
        $full = [IO.Path]::GetFullPath((Join-Path $script:RepositoryRoot $relative))
        $item = [IO.FileInfo]::new($full)
        if (-not $item.Exists -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or (Get-SHA256Upper $full) -cne $script:FixedSources[$relative]) {
            throw 'fixed_source_identity'
        }
        [void]$entries.Add([pscustomobject]@{ Relative = $relative; Full = $full; SHA = $script:FixedSources[$relative] })
    }

    $migrationLines = New-Object Text.StringBuilder
    for ($version = 1; $version -le 56; $version++) {
        $prefix = $version.ToString('000000') + '_'
        $migrationFiles = @(Get-ChildItem -LiteralPath (Join-Path $script:RepositoryRoot 'server/migrations') -File |
            Where-Object { $_.Name.StartsWith($prefix, [StringComparison]::Ordinal) -and $_.Name.EndsWith('.up.sql', [StringComparison]::Ordinal) })
        if ($migrationFiles.Count -ne 1 -or ($migrationFiles[0].Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
            $migrationFiles[0].Name -cnotmatch '^[0-9]{6}_[a-z0-9_]+\.up\.sql$') { throw 'migration_set_shape' }
        $relative = 'server/migrations/' + $migrationFiles[0].Name
        $sha = Get-SHA256Upper $migrationFiles[0].FullName
        [void]$migrationLines.Append($sha + "`t" + $migrationFiles[0].Name + "`n")
        [void]$entries.Add([pscustomobject]@{ Relative = $relative; Full = $migrationFiles[0].FullName; SHA = $sha })
    }
    $setBytes = [Text.Encoding]::ASCII.GetBytes($migrationLines.ToString())
    if ((Get-SHA256BytesUpper $setBytes) -cne $script:MigrationSetSHA) { throw 'migration_set_hash' }
    if ($entries.Count -ne 66 -or (@($entries | Group-Object Relative | Where-Object Count -ne 1)).Count -ne 0) { throw 'source_count' }
    return @($entries | Sort-Object Relative)
}

function Remove-LocalStage {
    param([Parameter(Mandatory = $true)][string]$Path)
    $full = [IO.Path]::GetFullPath($Path)
    $root = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
    if (-not $full.StartsWith($root, [StringComparison]::OrdinalIgnoreCase) -or [IO.Path]::GetFileName($full) -cnotmatch '^molin-email-matrix-package-[a-f0-9]{32}$') {
        throw 'local_stage_identity'
    }
    if ([IO.Directory]::Exists($full)) { [IO.Directory]::Delete($full, $true) }
}

function Assert-PackageMembers {
    param(
        [Parameter(Mandatory = $true)][string[]]$Listed,
        [Parameter(Mandatory = $true)][object[]]$Plan
    )
    $expected = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    [void]$expected.Add('source')
    [void]$expected.Add('source-manifest.sha256')
    foreach ($entry in $Plan) {
        $member = 'source/' + $entry.Relative
        [void]$expected.Add($member)
        $parts = $member.Split('/')
        for ($index = 1; $index -lt $parts.Length; $index++) {
            [void]$expected.Add(($parts[0..($index - 1)] -join '/'))
        }
    }
    $actual = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    foreach ($rawMember in $Listed) {
        $member = $rawMember.Replace('\', '/').Trim()
        while ($member.StartsWith('./', [StringComparison]::Ordinal)) { $member = $member.Substring(2) }
        $member = $member.TrimEnd('/')
        if ($member.Length -eq 0 -or $member.StartsWith('/', [StringComparison]::Ordinal) -or
            $member.Contains('..') -or $member.Contains(':') -or -not $actual.Add($member)) {
            throw 'package_member_invalid'
        }
    }
    if ($actual.Count -ne $expected.Count -or -not $actual.SetEquals($expected)) { throw 'package_member_set' }
}

function New-SourcePackage {
    param([Parameter(Mandatory = $true)][object[]]$Plan)
    $root = Join-Path ([IO.Path]::GetTempPath()) ('molin-email-matrix-package-' + [Guid]::NewGuid().ToString('N'))
    $sourceRoot = Join-Path $root 'source'
    [void][IO.Directory]::CreateDirectory($sourceRoot)
    try {
        $manifest = New-Object Collections.Generic.List[string]
        foreach ($entry in $Plan) {
            $destination = Join-Path $sourceRoot $entry.Relative.Replace('/', [IO.Path]::DirectorySeparatorChar)
            [void][IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($destination))
            [IO.File]::Copy($entry.Full, $destination, $false)
            if ((Get-SHA256Upper $destination) -cne $entry.SHA) { throw 'package_copy_hash' }
            [void]$manifest.Add($entry.SHA.ToLowerInvariant() + '  source/' + $entry.Relative)
        }
        $manifestPath = Join-Path $root 'source-manifest.sha256'
        [IO.File]::WriteAllText($manifestPath, (($manifest -join "`n") + "`n"), [Text.UTF8Encoding]::new($false, $true))
        $archive = Join-Path $root 'package.tar.gz'
        $tar = Join-Path $env:WINDIR 'System32\tar.exe'
        if (-not [IO.File]::Exists($tar)) { throw 'tar_missing' }
        & $tar -czf $archive -C $root source source-manifest.sha256
        if ($LASTEXITCODE -ne 0 -or -not [IO.File]::Exists($archive)) { throw 'package_create' }
        $listed = @(& $tar -tzf $archive)
        if ($LASTEXITCODE -ne 0) { throw 'package_list' }
        Assert-PackageMembers -Listed $listed -Plan $Plan
        return [pscustomobject]@{ Root = $root; Archive = $archive; SHA = (Get-SHA256Upper $archive).ToLowerInvariant() }
    }
    catch {
        Remove-LocalStage $root
        throw
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
    if ($normalized.IndexOf([char]0) -ge 0 -or ($normalized.Length -gt 0 -and [int][char]$normalized[0] -in @(0xFEFF, 0xFFFE))) { throw 'process_input_invalid' }
    $root = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $temporary = [IO.Path]::GetFullPath((Join-Path $root ('molin-email-matrix-process-' + [Guid]::NewGuid().ToString('N'))))
    if (-not $temporary.StartsWith($root + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -or [IO.Directory]::Exists($temporary)) { throw 'process_temp_invalid' }
    [void][IO.Directory]::CreateDirectory($temporary)
    $stdin = Join-Path $temporary 'stdin.txt'; $stdout = Join-Path $temporary 'stdout.txt'; $stderr = Join-Path $temporary 'stderr.txt'
    $process = $null
    try {
        [IO.File]::WriteAllBytes($stdin, [Text.UTF8Encoding]::new($false, $true).GetBytes($normalized))
        $parameters = @{ FilePath=$FilePath; ArgumentList=$Arguments; RedirectStandardInput=$stdin; RedirectStandardOutput=$stdout; RedirectStandardError=$stderr; NoNewWindow=$true; PassThru=$true }
        $process = Microsoft.PowerShell.Management\Start-Process @parameters
        try { $handle=$process.Handle; if($handle -eq [IntPtr]::Zero){throw 'process_handle'} } catch { try{if(-not $process.HasExited){$process.Kill();$process.WaitForExit()}}catch{}; throw 'process_handle' }
        if (-not $process.WaitForExit($TimeoutMilliseconds)) { $process.Kill(); $process.WaitForExit(); throw 'process_timeout' }
        $process.Refresh()
        $output = [Text.UTF8Encoding]::new($false, $true).GetString([IO.File]::ReadAllBytes($stdout))
        $errorOutput = [Text.UTF8Encoding]::new($false, $true).GetString([IO.File]::ReadAllBytes($stderr))
        if ($process.ExitCode -ne 0 -or $errorOutput.Length -ne 0) {
            $failureClassification='transport_or_output_failure'
            if($errorOutput.Length-eq 0){
                $failurePatterns=@(
                    '^status=failed mode=email_migration_stage_setup classification=([a-z0-9_]+) writes=false database_access=false docker_access=false retries=0\r?\n?$',
                    '^status=failed mode=email_migration_matrix55_environment_failure_cleanup classification=([a-z0-9_]+) stage_retained=true database_access=false retries=0\r?\n?$',
                    '^status=failed mode=email_migration_manual_execute stage=([a-z0-9_]+) stage_retained=true writes=false database_access=false docker_access=false retries=0\r?\n?$',
                    '^status=failed mode=email_migration_full_isolation_matrix stage=([a-z0-9_]+) stage_retained=true temporary_container_retained=false retries=0\r?\n?$'
                )
                foreach($pattern in $failurePatterns){$match=[regex]::Match($output,$pattern);if($match.Success){$failureClassification=$match.Groups[1].Value;break}}
            }
            $exception = [InvalidOperationException]::new('remote_gate_failed')
            $exception.Data['ExitCode']=$process.ExitCode; $exception.Data['StdoutLength']=$output.Length; $exception.Data['StderrLength']=$errorOutput.Length
            $exception.Data['FailureClassification']=$failureClassification
            throw $exception
        }
        return $output
    }
    finally {
        if ($null -ne $process) { $process.Dispose() }
        foreach ($path in @($stdin,$stdout,$stderr)) { if([IO.File]::Exists($path)){ $item=[IO.FileInfo]::new($path); if(($item.Attributes-band[IO.FileAttributes]::ReparsePoint)-ne 0-or$item.DirectoryName-cne$temporary){throw 'temp_cleanup_invalid'}; [IO.File]::Delete($path) } }
        if([IO.Directory]::Exists($temporary)){ if([IO.Directory]::GetFileSystemEntries($temporary).Length-ne 0){throw 'temp_cleanup_not_empty'}; [IO.Directory]::Delete($temporary,$false) }
    }
}

function Invoke-RemoteScript {
    param([string]$Ssh,[string]$Script,[string[]]$Tail,[int]$TimeoutMilliseconds)
    foreach($value in $Tail){if($value-cnotmatch'^[A-Za-z0-9_./:-]+$'){throw 'remote_argument_invalid'}}
    $arguments=@('-T','-p','10003','-o','BatchMode=yes','-o','NumberOfPasswordPrompts=0','-o','StrictHostKeyChecking=yes','-o','ConnectTimeout=10',$script:Remote,
      '/usr/bin/env','-i','PATH=/usr/sbin:/usr/bin:/sbin:/bin','HOME=/home/pc','USER=pc','LOGNAME=pc','LANG=C.UTF-8','/usr/bin/timeout','--signal=TERM','--kill-after=10s','7200s','/bin/bash','--noprofile','--norc','-s','--')+$Tail
    return Invoke-FixedProcess -FilePath $Ssh -Arguments $arguments -InputText $Script -TimeoutMilliseconds $TimeoutMilliseconds
}

function Invoke-NativeScp {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )
    $root=[IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $temporary=[IO.Path]::GetFullPath((Join-Path $root ('molin-email-matrix-scp-'+[Guid]::NewGuid().ToString('N'))))
    if(-not$temporary.StartsWith($root+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)-or[IO.Directory]::Exists($temporary)){throw 'scp_temp_invalid'}
    [void][IO.Directory]::CreateDirectory($temporary)
    $stdout=Join-Path $temporary 'stdout.txt';$stderr=Join-Path $temporary 'stderr.txt'
    try{
        # 原生调用运算符逐项传递 argv，避免 Start-Process 把 Windows 路径和远端冒号目标重新拼接。
        $previousErrorAction=$ErrorActionPreference
        try{
            $ErrorActionPreference='Continue'
            & $FilePath @Arguments 1> $stdout 2> $stderr
            $exitCode=$LASTEXITCODE
        }finally{$ErrorActionPreference=$previousErrorAction}
        $stdoutLength=if([IO.File]::Exists($stdout)){[IO.File]::ReadAllBytes($stdout).Length}else{0}
        $stderrLength=if([IO.File]::Exists($stderr)){[IO.File]::ReadAllBytes($stderr).Length}else{0}
        if($exitCode-ne 0-or$stderrLength-ne 0){
            $exception=[InvalidOperationException]::new('native_scp_failed')
            $exception.Data['ExitCode']=$exitCode;$exception.Data['StdoutLength']=$stdoutLength;$exception.Data['StderrLength']=$stderrLength
            throw $exception
        }
    }finally{
        foreach($path in @($stdout,$stderr)){if([IO.File]::Exists($path)){[IO.File]::Delete($path)}}
        if([IO.Directory]::Exists($temporary)){if([IO.Directory]::GetFileSystemEntries($temporary).Length-ne 0){throw 'scp_temp_not_empty'};[IO.Directory]::Delete($temporary,$false)}
    }
}

function Invoke-SelfTest {
    Assert-ControllerAst
    $plan=Get-SourcePlan
    $package=New-SourcePackage $plan
    try {
        $bash='C:\Program Files\Git\bin\bash.exe'; if(-not[IO.File]::Exists($bash)){throw 'bash_missing'}
        & $bash -n (Join-Path $script:RepositoryRoot 'scripts/email-migration-matrix-remote.payload.sh'); if($LASTEXITCODE-ne 0){throw 'bash_syntax'}
        $executeWrapper=Get-ExecutionWrapper
        $wrapperPath=Join-Path $script:RepositoryRoot 'scripts/email-migration-manual-execute.payload.sh'
        & $bash -n $wrapperPath; if($LASTEXITCODE-ne 0){throw 'execute_wrapper_syntax'}
        if($executeWrapper-cnotmatch'fail stage_contents'){throw 'execute_wrapper_contract'}
        $probe=Invoke-FixedProcess -FilePath $bash -Arguments @('-c','cat') -InputText "迁移传输探针`n" -TimeoutMilliseconds 10000
        if($probe-cne"迁移传输探针`n"){throw 'transport_probe'}
        $python=(Get-Command python -ErrorAction Stop).Source
        $probeRemote='pc@example';$probeStage='/tmp'
        $probeDestination=($probeRemote+':'+$probeStage+'/package.tar.gz')
        Invoke-NativeScp -FilePath $python -Arguments @('-c','import sys; raise SystemExit(0 if sys.argv[1:] == [r''C:\probe\package.tar.gz'', ''pc@example:/tmp/package.tar.gz''] else 1)','C:\probe\package.tar.gz',$probeDestination)
        Write-Output 'status=pass mode=email_migration_full_isolation_matrix_selftest sources=66 migrations=56 package=true hash_transport=true external_access=false docker_access=false database_access=false migration_executed=false retries=0'
    }
    finally { Remove-LocalStage $package.Root }
}

if($SelfTest){if($PrepareOnly-or$Execute-or$RecoverKnownFailure-or$Confirm){throw 'selftest_arguments'};Invoke-SelfTest;exit 0}
if($PrepareOnly){
  if($Execute-or$RecoverKnownFailure-or$Confirm-cne$script:PreparePhrase){throw 'prepare_confirmation_required'}
  $preparePlan=Get-SourcePlan
  $preparedPackage=New-SourcePackage $preparePlan
  [pscustomobject]@{
    status='pass';mode='email_migration_manual_package';sources=66;migrations=56
    package_path=$preparedPackage.Archive;package_sha256=$preparedPackage.SHA
    local_stage=$preparedPackage.Root;external_access=$false;docker_access=$false;database_access=$false;migration_executed=$false
  }|ConvertTo-Json -Compress
  exit 0
}
if(-not$Execute){throw 'confirmation_required'}
if($RecoverKnownFailure){
  if($Confirm-cne$script:RecoveryConfirmPhrase){throw 'recovery_confirmation_required'}
}elseif($Confirm-cne$script:ConfirmPhrase){throw 'confirmation_required'}
$plan=Get-SourcePlan
$package=New-SourcePackage $plan
$ssh=Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'; $scp=Join-Path $env:WINDIR 'System32\OpenSSH\scp.exe'
if(-not[IO.File]::Exists($ssh)-or-not[IO.File]::Exists($scp)){Remove-LocalStage $package.Root;throw 'ssh_tool_missing'}
$nonce=[Guid]::NewGuid().ToString('N'); $remoteStage="/home/pc/molin-runtime/email-migration-matrix-$nonce"
$sshAttempts=0; $scpAttempts=0; $currentStage='stage_setup'
$executeScript=Get-ExecutionWrapper
$setup=@'
set -Eeuo pipefail
exec 2>/dev/null
phase=argument_gate
fail(){ trap - ERR; printf 'status=failed mode=email_migration_stage_setup classification=%s writes=false database_access=false docker_access=false retries=0\n' "${1:?classification_required}"; exit 2; }
trap 'fail "$phase"' ERR
nonce=$1
[[ "$nonce" =~ ^[a-f0-9]{32}$ ]] || fail argument_gate
stage="/home/pc/molin-runtime/email-migration-matrix-${nonce}"
phase=retained_stage_gate
mapfile -t retained_stages < <(find /home/pc/molin-runtime -regextype posix-extended -mindepth 1 -maxdepth 1 -type d -regex '/home/pc/molin-runtime/email-migration-matrix-[a-f0-9]{32}' -printf '%p\n')
[[ ${#retained_stages[@]} -eq 0 ]] || fail retained_stage_present
[[ ! -e "$stage" && ! -L "$stage" ]] || fail stage_collision
phase=stage_create
mkdir -m 700 -- "$stage" || fail stage_create
[[ "$(stat -c '%U:%a' -- "$stage")" == pc:700 ]] || fail stage_identity
printf 'status=pass stage=remote_stage_created\n'
'@
if($RecoverKnownFailure){
  $recoveryCleanup=Get-RecoveryCleanupScript
  $recoveryStageSetup=@'

# 清理器完成精确旧 Stage 删除后，使用本地生成的新 nonce 创建唯一 Stage。
fail(){ trap - ERR; printf 'status=failed mode=email_migration_stage_setup classification=%s writes=false database_access=false docker_access=false retries=0\n' "${1:?classification_required}"; exit 2; }
trap 'fail "$phase"' ERR
phase=recovery_stage_argument
new_nonce=$3
[[ "$new_nonce" =~ ^[a-f0-9]{32}$ ]] || fail recovery_stage_argument
new_stage="/home/pc/molin-runtime/email-migration-matrix-${new_nonce}"
phase=recovery_retained_stage_gate
mapfile -t remaining_stages < <(find /home/pc/molin-runtime -regextype posix-extended -mindepth 1 -maxdepth 1 -type d -regex '/home/pc/molin-runtime/email-migration-matrix-[a-f0-9]{32}' -printf '%p\n')
[[ ${#remaining_stages[@]} -eq 0 ]] || fail recovery_retained_stage_present
[[ ! -e "$new_stage" && ! -L "$new_stage" ]] || fail recovery_stage_collision
phase=recovery_stage_create
mkdir -m 700 -- "$new_stage" || fail recovery_stage_create
[[ "$(stat -c '%U:%a' -- "$new_stage")" == pc:700 ]] || fail recovery_stage_identity
printf 'status=pass stage=remote_stage_created\n'
'@
  $setup=$recoveryCleanup+$recoveryStageSetup
  $setupTail=@('--execute','I_CONFIRM_EMAIL_MIGRATION_MATRIX55_ENVIRONMENT_FAILURE_CLEANUP_ONCE',$nonce)
  $setupPattern='^(?:status=pass mode=email_migration_matrix55_environment_failure_cleanup classification=verified_matrix55_known_failure_stage_removed baseline_outputs=6 matrix_outputs=2 removed_count=1 database_access=false retries=0\r?\n)(?:status=pass stage=remote_stage_created\r?\n?)$'
}else{
  $setupTail=@($nonce)
  $setupPattern='^status=pass stage=remote_stage_created\r?\n?$'
}
try{
  $sshAttempts++;$setupOutput=Invoke-RemoteScript -Ssh $ssh -Script $setup -Tail $setupTail -TimeoutMilliseconds 60000
  if($setupOutput-cnotmatch$setupPattern){throw 'setup_output'}
  $currentStage='upload_package';$scpAttempts++
  $scpDestination=($script:Remote+':'+$remoteStage+'/package.tar.gz')
  $scpArguments=@('-q','-O','-P','10003','-o','BatchMode=yes','-o','NumberOfPasswordPrompts=0','-o','StrictHostKeyChecking=yes','-o','ConnectTimeout=10','--',$package.Archive,$scpDestination)
  Invoke-NativeScp -FilePath $scp -Arguments $scpArguments
  $currentStage='full_matrix';$sshAttempts++
  $output=Invoke-RemoteScript -Ssh $ssh -Script $executeScript -Tail @($nonce,$package.SHA,$script:PayloadSHA,$script:GeneratorSHA) -TimeoutMilliseconds 7300000
  if($output-cnotmatch$script:SuccessPattern){throw 'matrix_output'}
  Write-Output 'status=pass mode=email_migration_full_isolation_matrix baseline_generation=true full55=true partial55=true full56=true partial56=true targets=94 temporary_containers_removed=true stage_retained=false ssh_attempts=2 scp_attempts=1 retries=0 main_database_modified=false'
}
catch{
  $exitCode=if($_.Exception.Data.Contains('ExitCode')){$_.Exception.Data['ExitCode']}else{-1};$stdoutLength=if($_.Exception.Data.Contains('StdoutLength')){$_.Exception.Data['StdoutLength']}else{0};$stderrLength=if($_.Exception.Data.Contains('StderrLength')){$_.Exception.Data['StderrLength']}else{0}
  $failureClassification=if($_.Exception.Data.Contains('FailureClassification')){[string]$_.Exception.Data['FailureClassification']}else{'controller_failure'}
  Write-Output("status=failed mode=email_migration_full_isolation_matrix stage=$currentStage classification=$failureClassification exit_code=$exitCode stdout_length=$stdoutLength stderr_length=$stderrLength stage_retained=true ssh_attempts=$sshAttempts scp_attempts=$scpAttempts retries=0")
  throw 'email_migration_matrix_failed'
}
finally{Remove-LocalStage $package.Root}
