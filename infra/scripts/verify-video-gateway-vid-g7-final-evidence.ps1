param()

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
& (Join-Path $PSScriptRoot 'verify-video-gateway-vid-g7-source-state.ps1')
if ($LASTEXITCODE -ne 0) { throw 'SOURCE_STATE复算失败' }
$state = Get-Content -Raw -LiteralPath (Join-Path $repoRoot 'docs/evidence/video-gateway-vid-g7-source-state.json') | ConvertFrom-Json
$runtime = Get-Content -Raw -LiteralPath (Join-Path $repoRoot 'docs/evidence/video-gateway-vid-g7-runtime-verification.json') | ConvertFrom-Json
$finance = Get-Content -Raw -LiteralPath (Join-Path $repoRoot 'docs/evidence/video-gateway-vid-g7-finance-regression-verification.json') | ConvertFrom-Json
$sensitive = Get-Content -Raw -LiteralPath (Join-Path $repoRoot 'docs/evidence/video-gateway-vid-g7-sensitive-verification.json') | ConvertFrom-Json
$facts = Get-Content -Raw -LiteralPath (Join-Path $repoRoot 'docs/evidence/video-gateway-vid-g7-fact-snapshot-verification.json') | ConvertFrom-Json
$ledger = Get-Content -Raw -LiteralPath (Join-Path $repoRoot 'docs/evidence/video-gateway-vid-g7-defect-ledger.json') | ConvertFrom-Json
$qa = Get-Content -Raw -LiteralPath (Join-Path $repoRoot 'docs/evidence/video-gateway-vid-g7-qa-final-review.json') | ConvertFrom-Json
$pm = Get-Content -Raw -LiteralPath (Join-Path $repoRoot 'docs/evidence/video-gateway-vid-g7-pm-final-review.json') | ConvertFrom-Json
$engineering = Get-Content -Raw -LiteralPath (Join-Path $repoRoot 'docs/evidence/video-gateway-vid-g7-engineering-final-review.json') | ConvertFrom-Json

function Get-PathSetHash([string[]]$Paths) {
    $records = foreach ($path in @($Paths | Sort-Object -Unique)) {
        $path + '|' + (Get-FileHash -LiteralPath (Join-Path $repoRoot $path) -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes(($records -join "`n") + "`n"))).ToLowerInvariant()
}

$serverPaths = @(git -C $repoRoot ls-files --cached --others --exclude-standard -- server | Sort-Object -Unique)
$runtimePaths = @(git -C $repoRoot ls-files --cached --others --exclude-standard -- server infra/scripts/verify-video-gateway-vid-g7-runtime.ps1 infra/prometheus/video-gateway-alerts.yml infra/grafana/dashboards/video-gateway-g7.json | Sort-Object -Unique)
$serverHash = Get-PathSetHash $serverPaths
$runtimeHash = Get-PathSetHash $runtimePaths
if ($finance.source_server_sha256 -ne $serverHash -or $runtime.source_tree_sha256 -ne $runtimeHash) { throw '测试回执与当前源码哈希不一致' }
if ([int]$runtime.result.required -lt 1 -or $runtime.result.required -ne $runtime.result.run -or $runtime.result.required -ne $runtime.result.pass -or $runtime.result.skip -ne 0 -or $runtime.result.exit_code -ne 0 -or $runtime.result.cleanup -ne 'PASS') { throw '运行时回执计数不完整' }
if ([int]$finance.result.required -lt 1 -or $finance.result.required -ne $finance.result.run -or $finance.result.required -ne $finance.result.pass -or $finance.result.skip -ne 0 -or $finance.result.finance_required -ne $finance.result.finance_pass -or $finance.result.exit_code -ne 0 -or $finance.result.cleanup -ne 'PASS') { throw '财务回执计数不完整' }
if ($sensitive.source_state_id -ne $state.source_state_id -or $sensitive.status -ne 'PASS' -or $sensitive.finding_count -ne 0 -or ($sensitive.scanned_text_files + $sensitive.skipped_binary_files) -ne $sensitive.candidate_count -or $sensitive.manifest_count -ne $state.manifest_count -or $sensitive.candidate_count -lt $sensitive.manifest_count) { throw '敏感扫描回执不完整或未绑定当前源码和证据范围' }
$runnerHash = (Get-FileHash -LiteralPath (Join-Path $repoRoot 'infra/scripts/verify-video-gateway-vid-g7-outbox.ps1') -Algorithm SHA256).Hash.ToLowerInvariant()
if ($finance.runner_sha256 -ne $runnerHash) { throw '财务回执未绑定当前运行器' }
$factScriptHash = (Get-FileHash -LiteralPath (Join-Path $repoRoot 'infra/scripts/video-gateway-vid-g7-fact-snapshot.sh') -Algorithm SHA256).Hash.ToLowerInvariant()
$factRunnerHash = (Get-FileHash -LiteralPath (Join-Path $repoRoot 'infra/scripts/verify-video-gateway-vid-g7-fact-snapshot.ps1') -Algorithm SHA256).Hash.ToLowerInvariant()
$factPaths = @(git -C $repoRoot ls-files --cached --others --exclude-standard -- infra/scripts/video-gateway-vid-g7-fact-snapshot.sh infra/scripts/verify-video-gateway-vid-g7-fact-snapshot.ps1 server | Sort-Object -Unique)
$factSourceHash = Get-PathSetHash $factPaths
if ($facts.source_state_id -ne $state.source_state_id -or $facts.snapshot_script_sha256 -ne $factScriptHash -or $facts.runner_sha256 -ne $factRunnerHash -or $facts.source_scope_sha256 -ne $factSourceHash -or $facts.mysql_image_digest -ne 'mysql@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b' -or $facts.go_image_digest -ne 'golang@sha256:908f8ff2ec296df2f349563072c7925775cd28b50361a52ed834a8a37399b9bf') { throw '事实快照回执未绑定当前Runner、脚本、完整服务源码或锁定镜像' }
if ($facts.result.required -ne 2 -or $facts.result.run -ne 2 -or $facts.result.pass -ne 2 -or $facts.result.skip -ne 0 -or $facts.result.exit_code -ne 0 -or $facts.result.cleanup -ne 'PASS' -or $facts.result.seed_tests_per_group -ne 4 -or $facts.result.seed_test_groups -ne 2 -or $facts.result.representative_nonempty_fact_types -ne 13 -or $facts.result.operations -ne 2 -or $facts.result.malicious_manifest -ne 'REJECTED' -or $facts.result.sensitivity_tamper -ne 'DETECTED' -or $facts.result.host_ports -ne 0 -or @($facts.groups).Count -ne 2) { throw '事实快照运行、种子、manifest攻击、敏感性或清理证据不完整' }
foreach ($group in @($facts.groups)) { if ($group.status -ne 'PASS' -or $group.cleanup -ne 'PASS') { throw '事实快照分组未通过' } }
if (-not $facts.groups[0].dump_restored_to_independent_schema -or -not $facts.groups[0].digest_equal -or -not $facts.groups[0].binary_columns_restored_with_hex_blob -or -not $facts.groups[0].wallet_tamper_detected -or -not $facts.groups[1].pre_post_install_base_equal -or -not $facts.groups[1].expanded_post_rollback_equal -or (($facts.groups[1].runtime_expand_fences_replayed -join ',') -ne '116,117') -or -not $facts.result.single_repeatable_read_snapshot -or -not $facts.result.low_sensitive_table_digest) { throw '事实快照恢复、升级回滚或篡改检测证据不完整' }
if ($ledger.source_state_id -ne $state.source_state_id -or $ledger.open_counts.P0 -ne 0 -or $ledger.open_counts.P1 -ne 0 -or $ledger.open_counts.P2 -ne 0 -or @($ledger.defects).Count -ne $ledger.total -or @($ledger.defects | Where-Object { $_.DEFECT_STATUS -ne 'CLOSED_VERIFIED' -or $_.REVIEWED_SOURCE_STATE -ne $state.source_state_id }).Count -ne 0) { throw '缺陷台账未在当前SOURCE_STATE全部关闭' }
if ($qa.reviewed_source_state -ne $state.source_state_id -or $qa.acceptance -ne 'PASS' -or $qa.p0 -ne 0 -or $qa.p1 -ne 0 -or $qa.p2 -ne 0) { throw '独立QA回执未绑定当前SOURCE_STATE或未通过' }
if ($pm.reviewed_source_state -ne $state.source_state_id -or $pm.confirmation -ne 'PASS' -or $pm.p0 -ne 0 -or $pm.p1 -ne 0 -or $pm.p2 -ne 0) { throw '独立产品回执未绑定当前SOURCE_STATE或未通过' }
if ($engineering.reviewed_source_state -ne $state.source_state_id -or $engineering.standards_review -ne 'PASS' -or $engineering.spec_review -ne 'PASS' -or $engineering.dev_code_review -ne 'PASS' -or $engineering.p0 -ne 0 -or $engineering.p1 -ne 0 -or $engineering.p2 -ne 0) { throw '独立工程回执未绑定当前SOURCE_STATE或未通过' }
Write-Output ('VIDEO_G7_FINAL_EVIDENCE=PASS source_state_id=' + $state.source_state_id + ' runtime=' + $runtime.result.pass + '/' + $runtime.result.required + ' finance=' + $finance.result.pass + '/' + $finance.result.required + ' scan=' + $sensitive.scanned_text_files + '+' + $sensitive.skipped_binary_files + '/' + $sensitive.candidate_count + ' fact_snapshot_runs=2 defects=' + $ledger.total + '/' + $ledger.total + ' reviews=QA+PM+STANDARDS+SPEC+DEV findings=0')
