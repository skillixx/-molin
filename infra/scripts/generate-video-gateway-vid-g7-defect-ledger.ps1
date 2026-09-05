param(
    [string]$Source = 'docs/evidence/video-gateway-vid-g7-defects.md',
    [string]$SourceState = 'docs/evidence/video-gateway-vid-g7-source-state.json',
    [string]$Output = 'docs/evidence/video-gateway-vid-g7-defect-ledger.json',
    [ValidateSet('FIXED_PENDING_VERIFY','CLOSED_VERIFIED')][string]$Status = 'FIXED_PENDING_VERIFY',
    [string]$ReviewedSourceState = 'PENDING_FINAL_REVIEW'
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$state = Get-Content -Raw -LiteralPath (Join-Path $repoRoot $SourceState) | ConvertFrom-Json
if (-not $state.source_state_id) { throw 'SOURCE_STATE缺少ID' }
if ($Status -eq 'CLOSED_VERIFIED' -and $ReviewedSourceState -ne $state.source_state_id) { throw '关闭缺陷必须绑定当前独立复核SOURCE_STATE' }

$entries = [ordered]@{}
foreach ($line in Get-Content -LiteralPath (Join-Path $repoRoot $Source)) {
    $id, $severity = '', ''
    $body, $rootCause = '', ''
    if ($line -match '`(G7-[A-Z0-9-]+) / (P[012])`') {
        $id, $severity = $Matches[1], $Matches[2]
        $plain = ($line -replace '^[- ]+','' -replace '`','').Trim()
        $body = if ($plain.Contains('：')) { $plain.Substring($plain.IndexOf('：') + 1) } else { $plain }
        $body = ($body -replace '^G7-[A-Z0-9-]+\s*/\s*P[012][。.:：\s]+','').Trim()
        $rootCause = ($body -split '。', 2)[0].Trim()
    } elseif ($line -match '^\|\s*(G7-[A-Z0-9-]+)\s*\|\s*(P[012])\s*\|') {
        $id, $severity = $Matches[1], $Matches[2]
        $columns = @($line.Trim().Trim('|').Split('|') | ForEach-Object { $_.Trim() })
        if ($columns.Count -lt 4) { throw ('缺陷表格列不足: ' + $id) }
        $rootCause = $columns[2]
        $body = $columns[2] + '；' + $columns[3]
    }
    if (-not $id) { continue }
    if ([string]::IsNullOrWhiteSpace($rootCause) -or $rootCause -eq $id -or $rootCause -match '^G7-[A-Z0-9-]+\s*/\s*P[012]$' -or $rootCause.Contains('|') -or [string]::IsNullOrWhiteSpace($body)) { throw ('缺陷摘要解析失败: ' + $id) }
    $entries[$id] = [ordered]@{
        DEFECT_ID = $id
        SEVERITY = $severity
        DEFECT_STATUS = $Status
        RESOLUTION = 'FIXED'
        SUMMARY = $rootCause
        ROOT_CAUSE = $rootCause
        EVIDENCE = $body
        TESTED_SOURCE_STATE = $state.source_state_id
        REVIEWED_SOURCE_STATE = $ReviewedSourceState
        CLOSED_AT = $(if ($Status -eq 'CLOSED_VERIFIED') { Get-Date -AsUTC -Format 'yyyy-MM-ddTHH:mm:ss.fffffffZ' } else { $null })
    }
}
if ($entries.Count -eq 0) { throw '缺陷清单为空' }
$items = @($entries.Values | Sort-Object DEFECT_ID)
$open = @($items | Where-Object { $_.DEFECT_STATUS -ne 'CLOSED_VERIFIED' })
$document = [ordered]@{
    target_goal = 'VID-G7'
    source_state_id = $state.source_state_id
    generated_at = Get-Date -AsUTC -Format 'yyyy-MM-ddTHH:mm:ss.fffffffZ'
    allowed_statuses = @('OPEN','IN_PROGRESS','FIXED_PENDING_VERIFY','CLOSED_VERIFIED')
    allowed_resolutions = @('UNRESOLVED','FIXED','NOT_REPRODUCED','NOT_APPLICABLE')
    total = $items.Count
    open_counts = [ordered]@{
        P0 = @($open | Where-Object SEVERITY -eq 'P0').Count
        P1 = @($open | Where-Object SEVERITY -eq 'P1').Count
        P2 = @($open | Where-Object SEVERITY -eq 'P2').Count
    }
    defects = $items
}
[IO.File]::WriteAllText((Join-Path $repoRoot $Output), ($document | ConvertTo-Json -Depth 8) + "`n", [Text.UTF8Encoding]::new($false))
Write-Output ('VIDEO_G7_DEFECT_LEDGER=PASS source_state_id=' + $state.source_state_id + ' total=' + $items.Count + ' p0=' + $document.open_counts.P0 + ' p1=' + $document.open_counts.P1 + ' p2=' + $document.open_counts.P2 + ' status=' + $Status)
