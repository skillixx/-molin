param(
    [string]$BaseCommit = '4d80d1cf0966d876c6c2171dce1a337afd2aa05b',
    [string]$OutputPath = 'docs/evidence/video-gateway-vid-g7-source-state.json',
    [switch]$FreshFetch
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path

function Get-SHA256([byte[]]$Bytes) {
    return [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($Bytes)).ToLowerInvariant()
}

function Get-GitOutputBytes([string[]]$Arguments) {
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = 'git'
    $start.WorkingDirectory = $repoRoot
    $start.UseShellExecute = $false
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    foreach ($argument in $Arguments) { [void]$start.ArgumentList.Add($argument) }
    $process = [Diagnostics.Process]::Start($start)
    $buffer = [IO.MemoryStream]::new()
    $process.StandardOutput.BaseStream.CopyTo($buffer)
    $errorText = $process.StandardError.ReadToEnd()
    $process.WaitForExit()
    if ($process.ExitCode -ne 0) { throw ('Git命令失败: ' + $errorText.Trim()) }
    return $buffer.ToArray()
}

function Get-SafeRemoteURL([string]$Value) {
    $trimmed = $Value.Trim()
    if ($trimmed -match '^https?://') {
        $uri = [Uri]$trimmed
        $port = if ($uri.IsDefaultPort) { '' } else { ':' + $uri.Port }
        return $uri.Scheme + '://' + $uri.Host + $port + $uri.AbsolutePath
    }
    if ($trimmed -match '^[^@]+@([^:]+):(.+)$') { return 'ssh://' + $Matches[1] + '/' + $Matches[2] }
    if ($trimmed -match '^[A-Za-z0-9.-]+/[A-Za-z0-9._/-]+$') { return $trimmed }
    throw 'origin远程地址无法安全脱敏'
}

$observedAt = (Get-Date -AsUTC -Format 'yyyy-MM-ddTHH:mm:ss.fffffffZ')
$provenance = 'CACHED'
if ($FreshFetch) {
    git -C $repoRoot fetch --no-tags origin main *> $null
    if ($LASTEXITCODE -ne 0) { throw 'fresh fetch失败' }
    $observedAt = (Get-Date -AsUTC -Format 'yyyy-MM-ddTHH:mm:ss.fffffffZ')
    $provenance = 'FRESH_FETCH'
}

$head = (git -C $repoRoot rev-parse HEAD).Trim()
$originMain = (git -C $repoRoot rev-parse origin/main).Trim()
$remoteURL = Get-SafeRemoteURL ((git -C $repoRoot remote get-url origin).Trim())
$branch = (git -C $repoRoot branch --show-current).Trim()

# 证据目录与SOURCE_STATE自身分层；补丁使用Git原始字节，避免PowerShell换行转换改变哈希。
$patchBytes = Get-GitOutputBytes @('diff', '--binary', '--no-ext-diff', 'HEAD', '--', '.', ':(exclude)docs/evidence/**')
$trackedPatchSHA = if ($patchBytes.Length -eq 0) { 'EMPTY' } else { Get-SHA256 $patchBytes }
$untracked = @(git -C $repoRoot ls-files --others --exclude-standard | ForEach-Object { $_.Replace('\', '/') } | Where-Object { -not $_.StartsWith('docs/evidence/') } | Sort-Object -Unique)
$untrackedRecords = foreach ($path in $untracked) {
    if (Test-Path -LiteralPath (Join-Path $repoRoot $path) -PathType Leaf) {
        $path + '|' + (Get-FileHash -LiteralPath (Join-Path $repoRoot $path) -Algorithm SHA256).Hash.ToLowerInvariant()
    }
}
$untrackedBytes = [Text.Encoding]::UTF8.GetBytes((@($untrackedRecords) -join "`n") + $(if (@($untrackedRecords).Count -gt 0) { "`n" } else { '' }))
$untrackedManifestSHA = if (@($untrackedRecords).Count -eq 0) { 'EMPTY' } else { Get-SHA256 $untrackedBytes }

$changed = @(git -C $repoRoot diff --name-only --diff-filter=ACMRTUXB $BaseCommit -- | ForEach-Object { $_.Replace('\', '/') })
$paths = @($changed + $untracked | Where-Object {
    $_ -and -not $_.StartsWith('docs/evidence/') -and (Test-Path -LiteralPath (Join-Path $repoRoot $_) -PathType Leaf)
} | Sort-Object -Unique)
if ($paths.Count -eq 0) { throw 'SOURCE_STATE清单为空' }
$manifest = foreach ($path in $paths) {
    [ordered]@{ path = $path; sha256 = (Get-FileHash -LiteralPath (Join-Path $repoRoot $path) -Algorithm SHA256).Hash.ToLowerInvariant() }
}
$manifestText = (($manifest | ForEach-Object { $_.path + '|' + $_.sha256 }) -join "`n") + "`n"
$manifestSHA = Get-SHA256 ([Text.Encoding]::UTF8.GetBytes($manifestText))
$capturedAt = (Get-Date -AsUTC -Format 'yyyy-MM-ddTHH:mm:ss.fffffffZ')
$identityFields = [ordered]@{
    HEAD_COMMIT = $head
    BASE_COMMIT = $BaseCommit
    ORIGIN_MAIN_COMMIT = $originMain
    ORIGIN_MAIN_REMOTE_URL = $remoteURL
    ORIGIN_MAIN_PROVENANCE = $provenance
    ORIGIN_MAIN_OBSERVED_AT = $observedAt
    TRACKED_PATCH_SHA256 = $trackedPatchSHA
    UNTRACKED_MANIFEST_SHA256 = $untrackedManifestSHA
    EVIDENCE_CAPTURED_AT = $capturedAt
}
$identityText = (($identityFields.GetEnumerator() | ForEach-Object { $_.Key + '=' + $_.Value }) -join "`n") + "`n"
$sourceStateID = Get-SHA256 ([Text.Encoding]::UTF8.GetBytes($identityText))

$document = [ordered]@{
    schema_version = 3
    target_goal = 'VID-G7'
    status = 'WORKTREE_SNAPSHOT'
    source_state_id = $sourceStateID
    head_commit = $head
    base_commit = $BaseCommit
    origin_main_commit = $originMain
    origin_main_remote_url = $remoteURL
    origin_main_provenance = $provenance
    origin_main_observed_at = $observedAt
    tracked_patch_sha256 = $trackedPatchSHA
    untracked_manifest_sha256 = $untrackedManifestSHA
    evidence_captured_at = $capturedAt
    branch = $branch
    uncommitted_changes = @((git -C $repoRoot status --porcelain=v1)).Count -gt 0
    manifest_sha256 = $manifestSHA
    manifest_count = $manifest.Count
    manifest = @($manifest)
    evidence_boundary = '本文件只证明源码身份，不证明测试、审查、测试服或发布门禁通过'
    test_server_authorization = 'NOT_GRANTED'
    test_server_writes = 0
    g7_acceptance = 'INCOMPLETE'
    vid_g8_started = $false
}
$absoluteOutput = Join-Path $repoRoot $OutputPath
$json = $document | ConvertTo-Json -Depth 8
[IO.File]::WriteAllText($absoluteOutput, $json + "`n", [Text.UTF8Encoding]::new($false))
Write-Output ('VIDEO_G7_SOURCE_STATE=PASS id=' + $sourceStateID + ' manifest=' + $manifest.Count + ' patch=' + $trackedPatchSHA + ' untracked=' + $untrackedManifestSHA + ' provenance=' + $provenance)
