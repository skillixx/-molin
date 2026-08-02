[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)][switch]$SelfTest,
    [Parameter(Mandatory = $false)][string]$Confirm,
    [Parameter(Mandatory = $false)][string]$OutputDirectory,
    [Parameter(Mandatory = $false)][string]$Schema54EmptySHA256,
    [Parameter(Mandatory = $false)][string]$Schema54LegacySHA256,
    [Parameter(Mandatory = $false)][string]$Schema55SHA256,
    [Parameter(Mandatory = $false)][string]$Schema56SHA256,
    [Parameter(Mandatory = $false)][string]$Baseline000055ManifestSHA256,
    [Parameter(Mandatory = $false)][string]$Baseline000056ManifestSHA256
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:ConfirmPhrase = 'I_CONFIRM_LOCAL_EMAIL_MIGRATION_ISOLATION_BUNDLE'
$script:DefaultManifest = Join-Path $PSScriptRoot 'email-migration-isolation-bundle.manifest.tsv'
$script:RepositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$script:Header = "entry_kind`tbundle_path`tsource_path`tsha256"
$script:IncludedRows = @(
    'runner|tests/email/run-000055-container-isolation-matrix.sh|2A7076740F9EB12DE7BC317874A4C820784699591EF5C53F908CB9E85BADC7CB',
    'runner|tests/email/run-000055-container-partial-matrix.sh|134D1935BFAEAF3F87037BC06386FC46E257255A279CF15AB96BDA1A03399E65',
    'runner|tests/email/run-000056-container-isolation-matrix.sh|903C7C0EE442EA118D3A4F1ED2D4B8FB70373E8F9077EA8B90EA37646295E882',
    'runner|tests/email/run-000056-container-partial-matrix.sh|CC2588F354503355555EC338F2864B7754B5D8572448E15AADE231FD277B3DD3',
    'contract|tests/email/run_000055_container_isolation_matrix_contract.py|55613832EAFB7C8FA7F0AD850FCB7DFCD5425BBC022B135227968FD77493FED8',
    'contract|tests/email/run_000055_container_partial_matrix_contract.py|3C43597B25D8548E1017ACEA8B6F5B17AD0D922EF42AE0C2B91E6631E1E7B4F0',
    'contract|tests/email/run_000056_container_isolation_matrix_contract.py|2E301CE970DCA62C2A0F98E4AFDD6D0A30B79E0F0ED1AB51B368161D45C3843B',
    'contract|tests/email/run_000056_container_partial_matrix_contract.py|C80A28AD5674AE4E1F12E7B294533B9948794FC71AABC5517FA0F58CCB0C015B',
    'boundary|tests/email/000055-partial-boundaries.tsv|4B5E02DC0C72490B168A47637E1DD8E6298DFEBE18AC22CD9DCAF663B8E18585',
    'boundary|tests/email/000056-partial-boundaries.tsv|7B9E3132B2A09D939FD81E908C889EE6EE41A69B5D680B52A081D5A0A9BA4A62',
    'migration|server/migrations/000055_add_directmail_email_management.up.sql|7238522CEC2CDFB2AD042C4B668380AA691E396CD536152F3ED25049ECD1FA3D',
    'migration|server/migrations/000055_add_directmail_email_management.down.sql|217B8FDAB63962284DA9D6EE1C436716687E351FE313E76F88E08C421D7C26EE',
    'migration|server/migrations/000056_add_email_admin_verify_bootstrap.up.sql|9133212C61EB4AA89B72C77D0C353F4B0F8B483080CBFB1E85A0281379861D9B',
    'migration|server/migrations/000056_add_email_admin_verify_bootstrap.down.sql|F42A30D70A95AD7BFD876F1515267C5FEE3DDCFD7AAC066453BDC020D201A5C2'
)
$script:ExternalRows = @(
    'external/schema54-empty.sql|MOLIN_000055_SCHEMA54_EMPTY_SHA256',
    'external/schema54-legacy.sql|MOLIN_000055_SCHEMA54_SHA256',
    'external/schema55.sql|MOLIN_EMAIL_SCHEMA55_SHA256',
    'external/schema56.sql|MOLIN_000056_SCHEMA56_SHA256',
    'external/000055-baseline-manifest.tsv|MOLIN_000055_BASELINE_MANIFEST_SHA256',
    'external/000056-baseline-manifest.tsv|MOLIN_000056_BASELINE_MANIFEST_SHA256'
)

function Assert-SafeRelativePath {
    param([Parameter(Mandatory = $true)][string]$Value)
    if ($Value.Length -eq 0 -or $Value.Contains('\') -or $Value.StartsWith('/') -or $Value.Contains(':') -or
        $Value.IndexOf([char]0) -ge 0 -or $Value -match '(^|/)(\.|\.\.)($|/)' -or
        $Value -match '(?i)(^|/)(\.env(?:\.|$)|[^/]*(?:secret|credential|token)[^/]*)') {
        throw 'manifest_path_invalid'
    }
}

function Get-StrictManifest {
    param([Parameter(Mandatory = $true)][string]$Path)
    $item = [IO.FileInfo]::new([IO.Path]::GetFullPath($Path))
    if (-not $item.Exists -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'manifest_file_invalid' }
    $bytes = [IO.File]::ReadAllBytes($item.FullName)
    if ($bytes.Length -lt 8 -or ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF)) { throw 'manifest_encoding_invalid' }
    $text = (New-Object Text.UTF8Encoding($false, $true)).GetString($bytes)
    if ($text.IndexOf([char]0) -ge 0 -or $text -match '[^\u0009\u000A\u000D\u0020-\u007E]') { throw 'manifest_encoding_invalid' }
    $lines = @($text.Replace("`r`n", "`n").Replace("`r", "`n").TrimEnd("`n").Split("`n"))
    if ($lines.Count -ne 21 -or $lines[0] -cne $script:Header) { throw 'manifest_shape_invalid' }

    $expectedIncluded = @{}
    foreach ($definition in $script:IncludedRows) {
        $parts = $definition.Split('|')
        $expectedIncluded[$parts[1]] = [pscustomobject]@{ Kind = $parts[0]; SHA256 = $parts[2] }
    }
    $expectedExternal = @{}
    foreach ($definition in $script:ExternalRows) {
        $parts = $definition.Split('|')
        $expectedExternal[$parts[0]] = $parts[1]
    }
    $seenPaths = @{}
    $seenSources = @{}
    $entries = New-Object Collections.Generic.List[object]
    for ($index = 1; $index -lt $lines.Count; $index++) {
        $columns = $lines[$index].Split("`t")
        if ($columns.Count -ne 4) { throw 'manifest_shape_invalid' }
        $kind, $bundlePath, $sourcePath, $sha = $columns
        Assert-SafeRelativePath $bundlePath
        if ($seenPaths.ContainsKey($bundlePath)) { throw 'manifest_duplicate_key' }
        $seenPaths[$bundlePath] = $true
        if ($kind -ceq 'external_baseline') {
            if (-not $expectedExternal.ContainsKey($bundlePath) -or $sourcePath -cne '-' -or
                $sha -cne ('${' + $expectedExternal[$bundlePath] + '}')) { throw 'manifest_external_contract_invalid' }
        }
        else {
            Assert-SafeRelativePath $sourcePath
            if (-not $expectedIncluded.ContainsKey($bundlePath) -or $sourcePath -cne $bundlePath -or
                $kind -cne $expectedIncluded[$bundlePath].Kind -or $sha -cne $expectedIncluded[$bundlePath].SHA256) { throw 'manifest_entry_invalid' }
            if ($seenSources.ContainsKey($sourcePath)) { throw 'manifest_duplicate_source' }
            $seenSources[$sourcePath] = $true
            $sourceFullPath = [IO.Path]::GetFullPath((Join-Path $script:RepositoryRoot $sourcePath))
            if (-not $sourceFullPath.StartsWith($script:RepositoryRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'manifest_path_invalid' }
            $sourceItem = [IO.FileInfo]::new($sourceFullPath)
            if (-not $sourceItem.Exists -or ($sourceItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'source_file_invalid' }
            $actualSHA = (Get-FileHash -LiteralPath $sourceFullPath -Algorithm SHA256).Hash
            if ($actualSHA -cne $sha) { throw 'source_sha_drift' }
        }
        [void]$entries.Add([pscustomobject]@{ Kind = $kind; BundlePath = $bundlePath; SourcePath = $sourcePath; SHA256 = $sha })
    }
    if ($seenPaths.Count -ne 20 -or $seenSources.Count -ne 14) { throw 'manifest_entry_count_invalid' }
    foreach ($pathKey in $expectedIncluded.Keys) { if (-not $seenPaths.ContainsKey($pathKey)) { throw 'manifest_entry_missing' } }
    foreach ($pathKey in $expectedExternal.Keys) { if (-not $seenPaths.ContainsKey($pathKey)) { throw 'manifest_entry_missing' } }
    # Windows PowerShell 5.1 对泛型 List 直接套数组表达式可能触发参数类型错误，先固化为普通数组。
    return $entries.ToArray()
}

function Assert-OfflineScriptContract {
    $tokens = $null
    $errors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile($PSCommandPath, [ref]$tokens, [ref]$errors)
    if ($errors.Count -ne 0) { throw 'self_ast_invalid' }
    $blocked = @('ssh', 'ssh.exe', 'scp', 'scp.exe', 'mysql', 'mysql.exe', 'migrate', 'Invoke-WebRequest', 'Invoke-RestMethod')
    foreach ($command in $ast.FindAll({ param($node) $node -is [Management.Automation.Language.CommandAst] }, $true)) {
        $name = $command.GetCommandName()
        if ($null -ne $name -and $name -in $blocked) { throw 'offline_contract_invalid' }
    }
}

function Get-ExternalSHAValues {
    $values = @{
        MOLIN_000055_SCHEMA54_EMPTY_SHA256 = $Schema54EmptySHA256
        MOLIN_000055_SCHEMA54_SHA256 = $Schema54LegacySHA256
        MOLIN_EMAIL_SCHEMA55_SHA256 = $Schema55SHA256
        MOLIN_000056_SCHEMA56_SHA256 = $Schema56SHA256
        MOLIN_000055_BASELINE_MANIFEST_SHA256 = $Baseline000055ManifestSHA256
        MOLIN_000056_BASELINE_MANIFEST_SHA256 = $Baseline000056ManifestSHA256
    }
    foreach ($key in $values.Keys) {
        if ([string]$values[$key] -cnotmatch '^[A-F0-9]{64}$' -or $values[$key] -ceq ('0' * 64)) { throw 'external_sha_invalid' }
    }
    return $values
}

function Remove-LocalStage {
    param([Parameter(Mandatory = $true)][string]$Path)
    $fullPath = [IO.Path]::GetFullPath($Path)
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
    if (-not $fullPath.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -or
        [IO.Path]::GetFileName($fullPath) -cnotmatch '^molin-email-migration-bundle-[a-f0-9]{32}$') { throw 'stage_cleanup_path_invalid' }
    if ([IO.Directory]::Exists($fullPath)) { [IO.Directory]::Delete($fullPath, $true) }
}

function Publish-FileCreateNew {
    param(
        [Parameter(Mandatory = $true)][string]$SourcePath,
        [Parameter(Mandatory = $true)][string]$DestinationPath,
        [Parameter(Mandatory = $true)][ref]$CreatedByThisRun
    )
    $sourceStream = $null
    $destinationStream = $null
    try {
        $sourceStream = [IO.File]::Open($SourcePath, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
        try {
            # CreateNew 在目标已存在或竞态占位时原子失败，绝不覆盖调用方已有文件。
            $destinationStream = [IO.File]::Open($DestinationPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
        }
        catch [IO.IOException] {
            throw 'output_exists'
        }
        $CreatedByThisRun.Value = $true
        $sourceStream.CopyTo($destinationStream)
        $destinationStream.Flush($true)
    }
    finally {
        if ($null -ne $destinationStream) { $destinationStream.Dispose() }
        if ($null -ne $sourceStream) { $sourceStream.Dispose() }
    }
}

if ($SelfTest) {
    try {
        # 攻击契约只通过本地进程环境覆盖系统临时清单；普通 SelfTest 始终读取仓库冻结清单。
        if ([string]::IsNullOrWhiteSpace($env:MOLIN_BUNDLE_SELFTEST_MANIFEST)) {
            Get-StrictManifest -Path $script:DefaultManifest | Out-Null
        }
        else {
            Get-StrictManifest -Path $env:MOLIN_BUNDLE_SELFTEST_MANIFEST | Out-Null
        }
        Assert-OfflineScriptContract
        Write-Output 'status=pass mode=selftest entries=20 runners=4 contracts=4 boundaries=2 migrations=4 external_baselines=6 external_access=false workspace_writes=false package_created=false'
        exit 0
    }
    catch {
        $known = @('self_ast_invalid', 'offline_contract_invalid', 'manifest_file_invalid', 'manifest_encoding_invalid', 'manifest_shape_invalid', 'manifest_path_invalid', 'manifest_duplicate_key', 'manifest_external_contract_invalid', 'manifest_entry_invalid', 'manifest_duplicate_source', 'source_file_invalid', 'source_sha_drift', 'manifest_entry_count_invalid', 'manifest_entry_missing', 'manifest_kind_count_invalid')
        $classification = if ($_.Exception.Message -in $known) { $_.Exception.Message } else { 'selftest_failed' }
        Write-Output "status=failed mode=selftest classification=$classification external_access=false workspace_writes=false package_created=false"
        exit 2
    }
}

$stage = $null
$archivePath = $null
$outputManifestPath = $null
$archiveCreatedByThisRun = $false
$manifestCreatedByThisRun = $false
try {
    if ($Confirm -cne $script:ConfirmPhrase) { throw 'confirmation_required' }
    if ([string]::IsNullOrWhiteSpace($OutputDirectory) -or -not [IO.Path]::IsPathRooted($OutputDirectory)) { throw 'output_directory_invalid' }
    $outputItem = [IO.DirectoryInfo]::new([IO.Path]::GetFullPath($OutputDirectory))
    if (-not $outputItem.Exists -or ($outputItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'output_directory_invalid' }
    $entries = Get-StrictManifest $script:DefaultManifest
    $externalValues = Get-ExternalSHAValues
    $archivePath = Join-Path $outputItem.FullName 'molin-email-migration-isolation-bundle.tar.gz'
    $outputManifestPath = Join-Path $outputItem.FullName 'molin-email-migration-isolation-bundle.manifest.tsv'
    $tarExe = Join-Path $env:WINDIR 'System32\tar.exe'
    if (-not [IO.File]::Exists($tarExe) -or ([IO.FileInfo]::new($tarExe).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'tar_tool_invalid' }

    $stage = Join-Path ([IO.Path]::GetTempPath()) ('molin-email-migration-bundle-' + [guid]::NewGuid().ToString('N'))
    [void][IO.Directory]::CreateDirectory($stage)
    $resolvedLines = New-Object Collections.Generic.List[string]
    [void]$resolvedLines.Add($script:Header)
    $archiveEntries = New-Object Collections.Generic.List[string]
    foreach ($entry in $entries) {
        $resolvedSHA = $entry.SHA256
        if ($entry.Kind -ceq 'external_baseline') {
            $placeholder = $entry.SHA256.Substring(2, $entry.SHA256.Length - 3)
            $resolvedSHA = $externalValues[$placeholder]
        }
        else {
            $destination = Join-Path $stage $entry.BundlePath.Replace('/', [IO.Path]::DirectorySeparatorChar)
            [void][IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($destination))
            [IO.File]::Copy((Join-Path $script:RepositoryRoot $entry.SourcePath), $destination, $false)
            [void]$archiveEntries.Add($entry.BundlePath)
        }
        [void]$resolvedLines.Add(($entry.Kind, $entry.BundlePath, $entry.SourcePath, $resolvedSHA -join "`t"))
    }
    $stageManifest = Join-Path $stage 'bundle-manifest.tsv'
    $stageArchive = Join-Path $stage 'bundle.tar.gz'
    [IO.File]::WriteAllText($stageManifest, (($resolvedLines -join "`n") + "`n"), (New-Object Text.UTF8Encoding($false, $true)))
    [void]$archiveEntries.Add('bundle-manifest.tsv')
    & $tarExe -czf $stageArchive -C $stage @archiveEntries
    if ($LASTEXITCODE -ne 0 -or -not [IO.File]::Exists($stageArchive)) { throw 'archive_create_failed' }
    $listed = @(& $tarExe -tzf $stageArchive)
    if ($LASTEXITCODE -ne 0 -or $null -ne (Compare-Object (@($archiveEntries | Sort-Object)) (@($listed | Sort-Object)))) { throw 'archive_content_invalid' }
    Publish-FileCreateNew -SourcePath $stageArchive -DestinationPath $archivePath -CreatedByThisRun ([ref]$archiveCreatedByThisRun)
    Publish-FileCreateNew -SourcePath $stageManifest -DestinationPath $outputManifestPath -CreatedByThisRun ([ref]$manifestCreatedByThisRun)
    $archiveSHA = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash
    $manifestSHA = (Get-FileHash -LiteralPath $outputManifestPath -Algorithm SHA256).Hash
    Write-Output "status=pass archive_sha256=$archiveSHA manifest_sha256=$manifestSHA files=15 external_baselines=6 external_access=false"
    exit 0
}
catch {
    # 失败清理严格服从独立 ownership；调用前文件及竞态占位永不删除。
    if ($archiveCreatedByThisRun -and $null -ne $archivePath -and [IO.File]::Exists($archivePath)) { [IO.File]::Delete($archivePath) }
    if ($manifestCreatedByThisRun -and $null -ne $outputManifestPath -and [IO.File]::Exists($outputManifestPath)) { [IO.File]::Delete($outputManifestPath) }
    $known = @('confirmation_required', 'output_directory_invalid', 'output_exists', 'external_sha_invalid', 'tar_tool_invalid', 'archive_create_failed', 'archive_content_invalid')
    $classification = if ($_.Exception.Message -in $known) { $_.Exception.Message } else { 'local_bundle_failed' }
    Write-Output "status=failed classification=$classification package_created=false external_access=false"
    exit 2
}
finally {
    if ($null -ne $stage) { Remove-LocalStage $stage }
}
