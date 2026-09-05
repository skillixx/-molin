param([string]$SourceStatePath = 'docs/evidence/video-gateway-vid-g7-source-state.json')

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
function Get-SHA256([byte[]]$Bytes) { [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($Bytes)).ToLowerInvariant() }
function Get-GitOutputBytes([string[]]$Arguments) {
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = 'git'; $start.WorkingDirectory = $repoRoot; $start.UseShellExecute = $false
    $start.RedirectStandardOutput = $true; $start.RedirectStandardError = $true
    foreach ($argument in $Arguments) { [void]$start.ArgumentList.Add($argument) }
    $process = [Diagnostics.Process]::Start($start); $buffer = [IO.MemoryStream]::new()
    $process.StandardOutput.BaseStream.CopyTo($buffer); $errorText = $process.StandardError.ReadToEnd(); $process.WaitForExit()
    if ($process.ExitCode -ne 0) { throw ('Git命令失败: ' + $errorText.Trim()) }
    $buffer.ToArray()
}

$state = Get-Content -Raw -LiteralPath (Join-Path $repoRoot $SourceStatePath) | ConvertFrom-Json
foreach ($name in @('source_state_id','head_commit','base_commit','origin_main_commit','origin_main_remote_url','origin_main_provenance','origin_main_observed_at','tracked_patch_sha256','untracked_manifest_sha256','evidence_captured_at','manifest_sha256')) {
    if (-not $state.$name) { throw ('SOURCE_STATE缺少字段: ' + $name) }
}
$currentHead = (git -C $repoRoot rev-parse HEAD).Trim()
if ($currentHead -ne $state.head_commit) {
    # 允许源码快照后追加纯证据提交；任何非证据文件变化仍视为源码身份漂移。
    git -C $repoRoot merge-base --is-ancestor $state.head_commit $currentHead
    if ($LASTEXITCODE -ne 0) { throw '当前HEAD不是SOURCE_STATE源码提交的后代' }
    $commitChanges = @(git -C $repoRoot diff --name-only --diff-filter=ACDMRTUXB ($state.head_commit + '..' + $currentHead) -- | ForEach-Object { $_.Replace('\','/') })
    if ($LASTEXITCODE -ne 0 -or @($commitChanges | Where-Object { -not $_.StartsWith('docs/evidence/') }).Count -ne 0) { throw 'SOURCE_STATE后存在非证据提交漂移' }
    git -C $repoRoot diff --quiet ($state.head_commit + '..' + $currentHead) -- . ':(exclude)docs/evidence/**'
    if ($LASTEXITCODE -ne 0) { throw 'SOURCE_STATE后存在非证据提交内容变化' }
}
if ((git -C $repoRoot rev-parse origin/main).Trim() -ne $state.origin_main_commit) { throw 'origin/main已漂移' }

$patchBytes = Get-GitOutputBytes @('diff','--binary','--no-ext-diff','HEAD','--','.',':(exclude)docs/evidence/**')
$patchSHA = if ($patchBytes.Length -eq 0) { 'EMPTY' } else { Get-SHA256 $patchBytes }
$untracked = @(git -C $repoRoot ls-files --others --exclude-standard | ForEach-Object { $_.Replace('\','/') } | Where-Object { -not $_.StartsWith('docs/evidence/') } | Sort-Object -Unique)
$untrackedRecords = foreach ($path in $untracked) { if (Test-Path -LiteralPath (Join-Path $repoRoot $path) -PathType Leaf) { $path + '|' + (Get-FileHash -LiteralPath (Join-Path $repoRoot $path) -Algorithm SHA256).Hash.ToLowerInvariant() } }
$untrackedSHA = if (@($untrackedRecords).Count -eq 0) { 'EMPTY' } else { Get-SHA256 ([Text.Encoding]::UTF8.GetBytes((@($untrackedRecords) -join "`n") + "`n")) }
if ($patchSHA -ne $state.tracked_patch_sha256 -or $untrackedSHA -ne $state.untracked_manifest_sha256) { throw '补丁或未跟踪清单已漂移' }

$manifestRecords = @($state.manifest | ForEach-Object {
    $path = Join-Path $repoRoot $_.path
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw ('manifest文件缺失: ' + $_.path) }
    $actual = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $_.sha256) { throw ('manifest文件漂移: ' + $_.path) }
    $_.path + '|' + $actual
})
$manifestSHA = Get-SHA256 ([Text.Encoding]::UTF8.GetBytes(($manifestRecords -join "`n") + "`n"))
if ($manifestSHA -ne $state.manifest_sha256 -or $manifestRecords.Count -ne [int]$state.manifest_count) { throw 'manifest聚合不一致' }
$observedAt = ([DateTime]$state.origin_main_observed_at).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ss.fffffffZ')
$capturedAt = ([DateTime]$state.evidence_captured_at).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ss.fffffffZ')
$identity = @()
$identity += 'HEAD_COMMIT=' + $state.head_commit
$identity += 'BASE_COMMIT=' + $state.base_commit
$identity += 'ORIGIN_MAIN_COMMIT=' + $state.origin_main_commit
$identity += 'ORIGIN_MAIN_REMOTE_URL=' + $state.origin_main_remote_url
$identity += 'ORIGIN_MAIN_PROVENANCE=' + $state.origin_main_provenance
$identity += 'ORIGIN_MAIN_OBSERVED_AT=' + $observedAt
$identity += 'TRACKED_PATCH_SHA256=' + $state.tracked_patch_sha256
$identity += 'UNTRACKED_MANIFEST_SHA256=' + $state.untracked_manifest_sha256
$identity += 'EVIDENCE_CAPTURED_AT=' + $capturedAt
$id = Get-SHA256 ([Text.Encoding]::UTF8.GetBytes(($identity -join "`n") + "`n"))
if ($id -ne $state.source_state_id) { throw 'SOURCE_STATE_ID复算不一致' }
Write-Output ('VIDEO_G7_SOURCE_STATE_VERIFY=PASS id=' + $id + ' manifest=' + $manifestRecords.Count + ' patch=' + $patchSHA + ' untracked=' + $untrackedSHA + ' provenance=' + $state.origin_main_provenance)
