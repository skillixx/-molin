param(
    [string]$SourceStatePath = 'docs/evidence/video-gateway-vid-g7-source-state.json',
    [string]$EvidencePath = 'docs/evidence/video-gateway-vid-g7-sensitive-verification.json',
    [switch]$WriteEvidence
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$state = Get-Content -Raw -LiteralPath (Join-Path $repoRoot $SourceStatePath) | ConvertFrom-Json
if ($state.target_goal -ne 'VID-G7' -or -not $state.source_state_id -or @($state.manifest).Count -eq 0) { throw 'SOURCE_STATE无效' }

$rules = [ordered]@{
    pem_private = '-----BEGIN (RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----'
    github_pat = 'gh[pousr]_[A-Za-z0-9]{30,}'
    aws_access = 'AKIA[0-9A-Z]{16}'
    openai_key = '(?<![A-Za-z0-9_-])sk-(?:proj-|svcacct-)?(?=[A-Za-z0-9_-]*[0-9])[A-Za-z0-9_-]{20,}'
    signed_url = 'X-Amz-Signature=[0-9A-Fa-f]{32,}'
    bearer_token = 'Bearer\s+[A-Za-z0-9._~-]{40,}'
    provider_key_assignment = '(?im)^(RUNWARE|RUNWAY|OPENAI|BIFROST)_[A-Z0-9_]*KEY\s*=\s*[^\s<][^\r\n]{15,}$'
}
$scanned = 0
$binary = 0
$findings = @()
$strictUTF8 = [Text.UTF8Encoding]::new($false, $true)
$manifestByPath = @{}
foreach ($entry in @($state.manifest)) { $manifestByPath[$entry.path] = $entry.sha256 }
$changed = @(git -C $repoRoot diff --name-only --diff-filter=ACMRTUXB $state.base_commit -- | ForEach-Object { $_.Replace('\','/') })
$untracked = @(git -C $repoRoot ls-files --others --exclude-standard | ForEach-Object { $_.Replace('\','/') })
$candidatePaths = @(@($state.manifest.path) + $changed + $untracked | Where-Object { $_ -and (Test-Path -LiteralPath (Join-Path $repoRoot $_) -PathType Leaf) } | Sort-Object -Unique)
foreach ($candidatePath in $candidatePaths) {
    $path = Join-Path $repoRoot $candidatePath
    $actual = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($manifestByPath.ContainsKey($candidatePath) -and $actual -ne $manifestByPath[$candidatePath]) { throw ('SOURCE_STATE文件漂移: ' + $candidatePath) }
    $bytes = [IO.File]::ReadAllBytes($path)
    if ($bytes -contains 0) { $binary++; continue }
    try { $text = $strictUTF8.GetString($bytes) } catch { $binary++; continue }
    $scanned++
    foreach ($rule in $rules.GetEnumerator()) {
        foreach ($match in [regex]::Matches($text, $rule.Value)) {
            $matchHash = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($match.Value))).ToLowerInvariant()
            # 只允许环境模板中一条经人工复核的公开占位赋值；内容变化后立即恢复为有效命中。
            if ($rule.Key -eq 'provider_key_assignment' -and $candidatePath -eq 'infra/.env.example' -and $matchHash -eq '0e23a1ed08d1cae03fd6398be36c24f4f6d2e22131c1f4eb183cf44d72aeaa8e') {
                continue
            }
            $findings += [ordered]@{ rule = $rule.Key; path = $candidatePath }
        }
    }
}

Write-Output ('VIDEO_G7_SENSITIVE=OBSERVED source_state_id=' + $state.source_state_id + ' manifest=' + @($state.manifest).Count + ' candidates=' + $candidatePaths.Count + ' scanned=' + $scanned + ' binary=' + $binary + ' findings=' + $findings.Count)
foreach ($finding in $findings) { Write-Output ('VIDEO_G7_SENSITIVE=HIT rule=' + $finding.rule + ' path=' + $finding.path) }
$capturedAt = ([DateTime]$state.evidence_captured_at).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ss.fffffffZ')
$document = [ordered]@{
    target_goal = 'VID-G7'
    captured_at = $capturedAt
    source_state_id = $state.source_state_id
    manifest_count = @($state.manifest).Count
    candidate_count = $candidatePaths.Count
    evidence_and_current_change_files = $candidatePaths.Count - @($state.manifest).Count
    scanned_text_files = $scanned
    skipped_binary_files = $binary
    finding_count = $findings.Count
    findings = @($findings)
    status = $(if ($findings.Count -eq 0) { 'PASS' } else { 'FAIL' })
    evidence_boundary = 'SOURCE_STATE清单逐文件验hash；证据目录不参与SOURCE_STATE自引用，但全部当前变更仍进入泄漏扫描'
}
if ($WriteEvidence) {
    [IO.File]::WriteAllText((Join-Path $repoRoot $EvidencePath), ($document | ConvertTo-Json -Depth 6) + "`n", [Text.UTF8Encoding]::new($false))
}
if ($findings.Count -ne 0) { exit 2 }
Write-Output 'VIDEO_G7_SENSITIVE=PASS'
