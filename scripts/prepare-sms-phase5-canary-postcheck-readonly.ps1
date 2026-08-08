param(
    [string]$ChangeId = "",
    [string]$SourceCanaryChangeId = "",
    [string]$PlanFile = "",
    [string]$ExpectedPlanSHA256 = "",
    [string]$CanaryRunnerFile = "",
    [string]$ExpectedCanaryRunnerSHA256 = "",
    [string]$CanaryResultFile = "",
    [string]$ExpectedCanaryResultSHA256 = "",
    [string]$OutputDirectory = "",
    [switch]$ExportCandidate,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
$FixedServerHost = "8.130.9.163"
$FixedSSHPort = 10003
$FixedSSHUser = "pc"

function Assert-LocalAbsolutePath {
    param([string]$Path, [string]$Description)
    # 所有输入和输出都必须来自本机磁盘，避免候选生成阶段因 UNC 或 Provider 路径隐式联网。
    if ([string]::IsNullOrWhiteSpace($Path) -or $Path -match '^(?:\\\\|//)' -or $Path.Contains("::")) {
        throw "${Description}必须是本地文件系统绝对路径"
    }
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        if ($Path -cnotmatch '^[A-Za-z]:[\\/]') { throw "Windows ${Description}必须使用本地盘符绝对路径" }
        $drive = Get-PSDrive -Name $Path.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
        if (([string]$drive.Root).StartsWith([string][char]92) -or
            ([string]$drive.DisplayRoot).StartsWith([string][char]92)) {
            throw "${Description}不得使用网络映射盘"
        }
    }
    elseif (-not [IO.Path]::IsPathRooted($Path)) { throw "${Description}必须使用本地绝对路径" }
}

function Read-StrictLowSensitivityResult {
    param([string]$Path)
    $allowed = @(
        "canary_send", "scene_register_submitted", "scene_login_submitted",
        "scene_reset_password_submitted", "scene_bind_phone_submitted", "scene_admin_verify_submitted",
        "requested_sends", "completed_scenes", "sms_enabled", "sms_test_mode",
        "same_target_min_interval_seconds", "scheduled_waits", "completed_pacing_waits",
        "baseline_send_log_id", "baseline_verification_code_id", "sensitive_values_persisted",
        "baseline_send_total", "baseline_send_accepted", "baseline_send_failed",
        "baseline_provider_calls_total", "baseline_provider_nonaccepted_total", "canary_completed_at",
        "real_sms_receipt_confirmed", "service_stops", "service_starts", "sms_submission_requests",
        "automatic_retries", "remote_stderr_present", "canary_send_exit_code"
    )
    $values = @{}
    $duplicateCounts = @{}
    foreach ($line in Get-Content -LiteralPath $Path -Encoding UTF8) {
        if ($line -cnotmatch '^(?<key>[a-z][a-z0-9_]*)=(?<value>[A-Za-z0-9_.:,-]+)$') {
            throw "Canary 结果包含非预定义低敏格式"
        }
        $key = $Matches['key']; $value = $Matches['value']
        if ($allowed -cnotcontains $key) { throw "Canary 结果字段不在白名单" }
        if ($values.ContainsKey($key)) {
            # 已执行 runner 的退出汇总重复输出了三个节奏字段；仅接受一次、值完全相同的已知重复。
            if (@("same_target_min_interval_seconds", "scheduled_waits", "completed_pacing_waits") -cnotcontains $key -or
                $values[$key] -cne $value -or [int]$duplicateCounts[$key] -ge 1) {
                throw "Canary 结果包含未批准的重复字段"
            }
            $duplicateCounts[$key] = 1
            continue
        }
        $values[$key] = $value
    }
    $required = @{
        canary_send = "awaiting_manual_receipt_confirmation"; scene_register_submitted = "true";
        scene_login_submitted = "true"; scene_reset_password_submitted = "true";
        scene_bind_phone_submitted = "true"; scene_admin_verify_submitted = "true";
        requested_sends = "5"; completed_scenes = "5"; sms_enabled = "false"; sms_test_mode = "true";
        same_target_min_interval_seconds = "65"; scheduled_waits = "2"; completed_pacing_waits = "2";
        sensitive_values_persisted = "0"; real_sms_receipt_confirmed = "false";
        sms_submission_requests = "5"; automatic_retries = "0"; remote_stderr_present = "false";
        canary_send_exit_code = "0"
    }
    foreach ($entry in $required.GetEnumerator()) {
        if ($values[$entry.Key] -cne $entry.Value) { throw "Canary 结果未证明成功且已恢复关闭态：$($entry.Key)" }
    }
    foreach ($key in @("baseline_send_log_id", "baseline_verification_code_id")) {
        if ($values[$key] -cnotmatch '^[0-9]+$') { throw "Canary 结果缺少合法低敏数据库游标：$key" }
    }
    foreach ($key in @("baseline_send_total", "baseline_send_accepted", "baseline_send_failed", "baseline_provider_calls_total", "baseline_provider_nonaccepted_total")) {
        if ($values[$key] -cnotmatch '^[0-9]+$') { throw "Canary 结果缺少合法观察基线：$key" }
    }
    if ($values["canary_completed_at"] -cnotmatch '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$') { throw "Canary 结果缺少 UTC 完成时间" }
    return $values
}

if (-not $ExportCandidate -and -not $SelfTest) {
    Write-Output "canary_postcheck_readonly_candidate_authorized=false"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExportCandidate -and $SelfTest) { throw "ExportCandidate 与 SelfTest 必须互斥" }

if ($SelfTest) {
    if ($FixedServerHost -cne "8.130.9.163" -or $FixedSSHPort -ne 10003 -or $FixedSSHUser -cne "pc") {
        throw "固定测试服 SSH 目标发生漂移"
    }
    Write-Output "canary_postcheck_readonly_candidate_self_test=passed"
    Write-Output "source_result_binding_required=true"
    Write-Output "database_cursor_binding_required=true"
    Write-Output "fixed_ssh_target_frozen=true"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

foreach ($id in @($ChangeId, $SourceCanaryChangeId)) {
    if ($id -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') { throw "ChangeId 必须使用 UTC 基本格式" }
}
if ($ChangeId -ceq $SourceCanaryChangeId) { throw "事后核验必须使用独立 ChangeId" }
foreach ($hash in @($ExpectedPlanSHA256, $ExpectedCanaryRunnerSHA256, $ExpectedCanaryResultSHA256)) {
    if ($hash -cnotmatch '^[0-9a-f]{64}$') { throw "所有批准摘要必须是小写完整 SHA-256" }
}
foreach ($item in @(
    @($PlanFile, "计划文件"), @($CanaryRunnerFile, "Canary runner 文件"),
    @($CanaryResultFile, "Canary 低敏结果文件"), @($OutputDirectory, "候选输出目录")
)) { Assert-LocalAbsolutePath -Path $item[0] -Description $item[1] }

$planPath = (Resolve-Path -LiteralPath $PlanFile).Path
$canaryRunnerPath = (Resolve-Path -LiteralPath $CanaryRunnerFile).Path
$canaryResultPath = (Resolve-Path -LiteralPath $CanaryResultFile).Path
if ((Get-FileHash -LiteralPath $planPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedPlanSHA256) { throw "计划摘要不匹配" }
if ((Get-FileHash -LiteralPath $canaryRunnerPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedCanaryRunnerSHA256) { throw "Canary runner 摘要不匹配" }
if ((Get-FileHash -LiteralPath $canaryResultPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedCanaryResultSHA256) { throw "Canary 结果摘要不匹配" }
$plan = Get-Content -LiteralPath $planPath -Raw -Encoding UTF8 | ConvertFrom-Json
if ($plan.change_id -cne $SourceCanaryChangeId -or $plan.requested_sends -ne 5 -or $plan.max_sends -ne 5 -or
    $plan.no_retries -ne $true -or $plan.same_target_min_interval_seconds -ne 65 -or $plan.scheduled_waits -ne 2 -or
    $plan.acceptance_scope -cne "receipt_only") { throw "源 Canary 计划不符合五场景 receipt-only 与频控节奏契约" }
$source = Read-StrictLowSensitivityResult -Path $canaryResultPath
$baselineSendLogId = $source["baseline_send_log_id"]
$baselineVerificationCodeId = $source["baseline_verification_code_id"]

$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
$outputParent = Split-Path -Parent $outputPath
if (-not (Test-Path -LiteralPath $outputParent -PathType Container)) { throw "候选输出目录的父目录必须已存在" }
if (Test-Path -LiteralPath $outputPath) { throw "候选输出目录已存在，禁止覆盖" }

# 远端负载只读取进程、环境、数据库、内部指标和监控 API；所有查询只输出布尔值与计数。
$remotePayloadTemplate = @'
set -euo pipefail
api_path='/home/pc/molin/molin-api'
env_file='/home/pc/molin/infra/.env.test'
alertmanager_config='/home/pc/molin-alertmanager-phase5/20260805T084215Z/alertmanager.closed.yml'
alertmanager_container='molin-alertmanager-phase5-closed'
alertmanager_port=19093
prometheus_port=19090
source_change_id='__SOURCE_CHANGE_ID__'
baseline_send_log_id=__BASELINE_SEND_LOG_ID__
baseline_verification_code_id=__BASELINE_VERIFICATION_CODE_ID__

emit_bool() { if [ "$2" = true ]; then printf '%s=true\n' "$1"; else printf '%s=false\n' "$1"; fi; }
fail_closed() {
  printf 'canary_postcheck_readonly=blocked\n'
  printf 'failure_gate=%s\n' "$1"
  printf 'configuration_mutations=0\nservice_signals=0\nservice_restarts=0\nbusiness_posts=0\nemails_sent=0\nsms_submission_requests=0\nreal_sms_sent=0\n'
  exit 3
}
api_pids() { pgrep -f "^${api_path}$" 2>/dev/null || true; }
read_process_env() { tr '\0' '\n' < "/proc/$1/environ" | sed -n "s/^$2=//p" | tail -n 1; }
read_file_env() { sed -n "s/^$1=//p" "$env_file" | tail -n 1; }
run_mysql_readonly() {
  local statement="$1"
  local wrapped="SET SESSION TRANSACTION READ ONLY; START TRANSACTION READ ONLY; ${statement}; COMMIT;"
  if command -v mysql >/dev/null 2>&1; then
    printf '%s\n' "$wrapped" | MYSQL_PWD="$db_pass" mysql --batch --skip-column-names -h "$db_host" -P "${db_port:-3306}" -u "$db_user" "$db_name" 2>/dev/null
    return
  fi
  { printf '%s\n' "$db_pass"; printf '%s\n' "$wrapped"; } | docker exec -i molin-mysql sh -c '
    IFS= read -r MYSQL_PWD; export MYSQL_PWD; exec mysql --batch --skip-column-names -u "$1" "$2"
  ' sh "$db_user" "$db_name" 2>/dev/null
}
verify_alertmanager_discard() {
  [ -f "$alertmanager_config" ] && [ ! -L "$alertmanager_config" ] &&
  python3 - "$alertmanager_config" <<'PY'
import re,sys
text=open(sys.argv[1],encoding="utf-8").read()
route=re.search(r"(?ms)^route:\s*\n(?P<body>(?:^[ \t]+.*\n?)*)",text)
body=route.group("body") if route else ""
raise SystemExit(0 if re.search(r"(?m)^\s+receiver:\s*[\"\x27]?discard[\"\x27]?\s*$",body) and not re.search(r"(?m)^\s+routes:\s*$",body) else 2)
PY
  [ "$(docker inspect "$alertmanager_container" --format '{{.State.Running}}' 2>/dev/null)" = true ] &&
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${alertmanager_port}/-/ready" 2>/dev/null || true)" = 200 ]
}

for command_name in awk curl docker mysql pgrep python3 sed tr; do
  if [ "$command_name" = mysql ] && ! command -v mysql >/dev/null 2>&1; then continue; fi
  command -v "$command_name" >/dev/null || fail_closed "command_${command_name}"
done
mapfile -t running < <(api_pids)
[ "${#running[@]}" -eq 1 ] || fail_closed api_process_count
pid="${running[0]}"
[ "$(read_process_env "$pid" SMS_ENABLED)" = false ] || fail_closed sms_not_closed
[ "$(read_process_env "$pid" SMS_TEST_MODE)" = true ] || fail_closed sms_test_mode
[ "$(read_file_env SMS_ENABLED)" = false ] || fail_closed file_not_closed
[ "$(read_file_env SMS_TEST_MODE)" = true ] || fail_closed file_test_mode
[ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health 2>/dev/null || true)" = 200 ] || fail_closed health
[ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)" = 200 ] || fail_closed ready

whitelist_count="$(read_process_env "$pid" SMS_TEST_PHONE_WHITELIST | awk -F, '{count=0; for(i=1;i<=NF;i++) if($i!="") count++; print count}')"
[ "$whitelist_count" = 2 ] || fail_closed whitelist_count
db_host="$(read_process_env "$pid" MYSQL_HOST)"; db_port="$(read_process_env "$pid" MYSQL_PORT)"
db_user="$(read_process_env "$pid" MYSQL_USER)"; db_pass="$(read_process_env "$pid" MYSQL_PASSWORD)"; db_name="$(read_process_env "$pid" MYSQL_DATABASE)"
[ -n "$db_host" ] && [ -n "$db_user" ] && [ -n "$db_pass" ] && [ -n "$db_name" ] || fail_closed database_environment

summary="$(run_mysql_readonly "SELECT CONCAT((SELECT COUNT(*) FROM sms_send_logs WHERE id>${baseline_send_log_id}),':',(SELECT COUNT(*) FROM sms_send_logs WHERE id>${baseline_send_log_id} AND purpose='otp' AND submit_status='accepted'),':',(SELECT COUNT(DISTINCT scene) FROM sms_send_logs WHERE id>${baseline_send_log_id}),':',(SELECT COUNT(*) FROM sms_send_logs WHERE id>${baseline_send_log_id} AND provider='aliyun' AND provider_request_id IS NOT NULL AND provider_request_id<>'' AND provider_code='OK'),':',(SELECT COUNT(*) FROM verification_codes WHERE id>${baseline_verification_code_id} AND target_type='phone'),':',(SELECT COUNT(*) FROM verification_codes WHERE id>${baseline_verification_code_id} AND target_type='phone' AND send_status='accepted' AND used_at IS NULL AND business_request_no IS NOT NULL AND provider='aliyun' AND provider_request_id IS NOT NULL),':',(SELECT COUNT(*) FROM sms_send_logs l JOIN verification_codes v ON v.business_request_no=l.business_request_id WHERE l.id>${baseline_send_log_id} AND v.id>${baseline_verification_code_id} AND v.used_at IS NULL),':',(SELECT COUNT(*) FROM (SELECT scene,COUNT(*) c FROM sms_send_logs WHERE id>${baseline_send_log_id} GROUP BY scene HAVING c=1) exact_scenes))")" || fail_closed database_query
IFS=: read -r send_total accepted_total distinct_scenes provider_complete verification_total verification_unused join_total exact_scenes <<<"$summary"
[ "$send_total:$accepted_total:$distinct_scenes:$provider_complete:$verification_total:$verification_unused:$join_total:$exact_scenes" = '5:5:5:5:5:5:5:5' ] || fail_closed five_scene_evidence

internal_token="$(read_process_env "$pid" INTERNAL_API_TOKEN)"; [ -n "$internal_token" ] || fail_closed internal_metrics_token
provider_total="$(printf 'X-Internal-Token: %s\n' "$internal_token" | curl -fsS --max-time 5 -H @- http://127.0.0.1:8080/api/internal/metrics 2>/dev/null | awk '/^sms_provider_calls_total\{/{sum += $NF} END{printf "%.0f",sum+0}')" || fail_closed provider_metrics
# Provider 指标是进程内计数，Canary 恢复关闭态会重启 API；这里只证明指标可读和形状合法，五次受理由数据库持久证据证明。
[[ "$provider_total" =~ ^[0-9]+$ ]] || fail_closed provider_metrics_shape
verify_alertmanager_discard || fail_closed alertmanager_discard
active_alertmanager="$(curl -fsS --max-time 5 "http://127.0.0.1:${alertmanager_port}/api/v2/alerts" | python3 -c 'import json,sys; v=json.load(sys.stdin); print(len(v) if isinstance(v,list) else -1)')" || fail_closed alertmanager_alerts
active_sms="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/alerts" | python3 -c 'import json,sys; a=json.load(sys.stdin).get("data",{}).get("alerts",[]); print(sum(1 for x in a if str(x.get("labels",{}).get("alertname","")).startswith("MolinSMS")))')" || fail_closed prometheus_alerts
notification_failed="$(curl -fsS --max-time 5 "http://127.0.0.1:${alertmanager_port}/metrics" | awk '/^alertmanager_notifications_failed_total({| )/{sum += $NF} END{printf "%.0f",sum+0}')" || fail_closed notification_metrics
[ "$active_alertmanager:$active_sms:$notification_failed" = '0:0:0' ] || fail_closed monitoring_state
[ ! -e /home/pc/.molin-sms-phase5-canary-send.lock ] || fail_closed recovery_lock
[ ! -e "/home/pc/.molin-sms-phase5-canary-send-${source_change_id}" ] || fail_closed recovery_materials

printf 'canary_postcheck_readonly=passed\n'
printf 'sms_enabled=false\nsms_test_mode=true\nhealth_ready_verified=true\nwhitelist_count=2\n'
printf 'post_baseline_send_logs=5\naccepted_send_logs=5\ndistinct_scenes=5\nprovider_acceptance_fields_complete=true\n'
printf 'post_baseline_verification_codes=5\notp_unconsumed_verified=true\nlog_verification_join_verified=true\n'
printf 'provider_metrics_read_verified=true\ncurrent_process_provider_metric_total=%s\nalertmanager_discard_verified=true\nactive_alertmanager_alerts=0\nactive_sms_alerts=0\nnotification_failures=0\n' "$provider_total"
printf 'recovery_lock_clear=true\nrecovery_materials_clear=true\n'
printf 'configuration_mutations=0\nservice_signals=0\nservice_restarts=0\nbusiness_posts=0\nemails_sent=0\nsms_submission_requests=0\nreal_sms_sent=0\n'
'@
$remotePayload = $remotePayloadTemplate.Replace("__SOURCE_CHANGE_ID__", $SourceCanaryChangeId).
    Replace("__BASELINE_SEND_LOG_ID__", $baselineSendLogId).
    Replace("__BASELINE_VERIFICATION_CODE_ID__", $baselineVerificationCodeId)
$payloadBase64 = [Convert]::ToBase64String((New-Object Text.UTF8Encoding($false)).GetBytes($remotePayload.Replace("`r`n", "`n")))
$sshHelperPath = Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1"
$sshHelperSHA256 = (Get-FileHash -LiteralPath $sshHelperPath -Algorithm SHA256).Hash.ToLowerInvariant()
$repoScripts = [IO.Path]::GetFullPath($PSScriptRoot)

$runnerTemplate = @'
param([switch]$ExecuteReadOnly, [switch]$SelfTest, [string]$ExpectedRunnerSHA256 = "")
$ErrorActionPreference = "Stop"
$ChangeId = "__CHANGE_ID__"
$SourceCanaryChangeId = "__SOURCE_CHANGE_ID__"
$ExpectedPlanSHA256 = "__PLAN_SHA256__"
$ExpectedCanaryRunnerSHA256 = "__CANARY_RUNNER_SHA256__"
$ExpectedCanaryResultSHA256 = "__CANARY_RESULT_SHA256__"
$RemotePayloadBase64 = "__PAYLOAD_BASE64__"
$ExpectedSSHHelperSHA256 = "__SSH_HELPER_SHA256__"
$ResultPath = Join-Path (Split-Path -Parent $PSCommandPath) "result-$ChangeId.txt"

if (-not $ExecuteReadOnly -and -not $SelfTest) {
    Write-Output "canary_postcheck_readonly_execution_authorized=false"
    Write-Output "network_connections=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExecuteReadOnly -and $SelfTest) { throw "ExecuteReadOnly 与 SelfTest 必须互斥" }
if ($SelfTest) {
    $payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64))
    foreach ($marker in @("START TRANSACTION READ ONLY", "baseline_send_log_id", "otp_unconsumed_verified=true", "business_posts=0", "real_sms_sent=0")) {
        if (-not $payload.Contains($marker)) { throw "事后只读负载缺少安全标记：$marker" }
    }
    Write-Output "canary_postcheck_readonly_runner_self_test=passed"
    Write-Output "source_result_binding_verified=true"
    Write-Output "network_connections=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExpectedRunnerSHA256 -cnotmatch '^[0-9a-f]{64}$') { throw "只读执行必须提供获批的完整 runner SHA-256" }
if ((Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedRunnerSHA256) { throw "runner SHA-256 与批准值不匹配" }
if (Test-Path -LiteralPath $ResultPath) { throw "低敏结果文件已存在，禁止重复执行" }
$sshHelperPath = Join-Path "__REPO_SCRIPTS__" "sms-phase5-test-server-ssh.ps1"
if ((Get-FileHash -LiteralPath $sshHelperPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedSSHHelperSHA256) { throw "固定 SSH 身份辅助脚本摘要不匹配" }
. $sshHelperPath
$knownHosts = Assert-SmsPhase5FixedTestServerIdentity -ServerHost '8.130.9.163' -SSHPort 10003 -SSHUser 'pc'
$utf8 = New-Object Text.UTF8Encoding($false)
$inputBytes = $utf8.GetBytes([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64)))
$startInfo = New-Object Diagnostics.ProcessStartInfo
$startInfo.FileName = "ssh.exe"; $startInfo.UseShellExecute = $false; $startInfo.RedirectStandardInput = $true
$startInfo.RedirectStandardOutput = $true; $startInfo.RedirectStandardError = $true; $startInfo.CreateNoWindow = $true
$startInfo.StandardOutputEncoding = $utf8; $startInfo.StandardErrorEncoding = $utf8
$startInfo.Arguments = "-p 10003 -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 -o `"UserKnownHostsFile=$knownHosts`" -- pc@8.130.9.163 bash -s"
$process = New-Object Diagnostics.Process; $process.StartInfo = $startInfo
try {
    if (-not $process.Start()) { throw "无法启动固定 SSH 事后只读核验进程" }
    $stdoutTask = $process.StandardOutput.ReadToEndAsync(); $stderrTask = $process.StandardError.ReadToEndAsync()
    try { $process.StandardInput.BaseStream.Write($inputBytes, 0, $inputBytes.Length); $process.StandardInput.BaseStream.Flush() }
    finally { [Array]::Clear($inputBytes, 0, $inputBytes.Length); $process.StandardInput.Close() }
    $process.WaitForExit(); $stdout = $stdoutTask.Result; $stderr = $stderrTask.Result; $remoteExitCode = $process.ExitCode
}
finally { $process.Dispose(); $inputBytes = $null }
$safeKeys = @(
    "canary_postcheck_readonly", "failure_gate", "sms_enabled", "sms_test_mode", "health_ready_verified", "whitelist_count",
    "post_baseline_send_logs", "accepted_send_logs", "distinct_scenes", "provider_acceptance_fields_complete",
    "post_baseline_verification_codes", "otp_unconsumed_verified", "log_verification_join_verified", "provider_metrics_read_verified",
    "current_process_provider_metric_total",
    "alertmanager_discard_verified", "active_alertmanager_alerts", "active_sms_alerts", "notification_failures",
    "recovery_lock_clear", "recovery_materials_clear", "configuration_mutations", "service_signals", "service_restarts",
    "business_posts", "emails_sent", "sms_submission_requests", "real_sms_sent"
)
$safeLines = @()
foreach ($line in @($stdout -split "`r?`n" | Where-Object { $_ -ne "" })) {
    if ($line -cnotmatch '^(?<key>[a-z][a-z0-9_]*?)=(?:true|false|passed|blocked|[0-9]+|[a-z][a-z0-9_]*)$' -or $safeKeys -cnotcontains $Matches['key']) {
        throw "远端输出不符合低敏字段白名单"
    }
    $safeLines += $line
}
foreach ($marker in @("configuration_mutations=0", "service_signals=0", "service_restarts=0", "business_posts=0", "emails_sent=0", "sms_submission_requests=0", "real_sms_sent=0")) {
    if ($safeLines -cnotcontains $marker) { throw "远端只读零副作用证据不完整：$marker" }
}
$stderrPresent = (-not [string]::IsNullOrWhiteSpace($stderr)).ToString().ToLowerInvariant()
$safeLines += "network_connections=1"; $safeLines += "remote_stderr_present=$stderrPresent"; $safeLines += "readonly_exit_code=$remoteExitCode"
$content = ($safeLines -join "`r`n") + "`r`n"; $bytes = [Text.Encoding]::UTF8.GetBytes($content)
$stream = [IO.File]::Open($ResultPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
try { $stream.Write($bytes, 0, $bytes.Length) } finally { $stream.Dispose(); [Array]::Clear($bytes, 0, $bytes.Length) }
$safeLines | Write-Output
Write-Output "low_sensitivity_result_persisted=true"
Write-Output "result_sha256=$((Get-FileHash -LiteralPath $ResultPath -Algorithm SHA256).Hash.ToLowerInvariant())"
if ($remoteExitCode -ne 0 -or $stderrPresent -cne "false") { throw "固定测试服 Canary 事后只读核验未通过，退出码：$remoteExitCode" }
'@

$runnerText = $runnerTemplate.Replace("__CHANGE_ID__", $ChangeId).
    Replace("__SOURCE_CHANGE_ID__", $SourceCanaryChangeId).
    Replace("__PLAN_SHA256__", $ExpectedPlanSHA256).
    Replace("__CANARY_RUNNER_SHA256__", $ExpectedCanaryRunnerSHA256).
    Replace("__CANARY_RESULT_SHA256__", $ExpectedCanaryResultSHA256).
    Replace("__PAYLOAD_BASE64__", $payloadBase64).
    Replace("__SSH_HELPER_SHA256__", $sshHelperSHA256).
    Replace("__REPO_SCRIPTS__", $repoScripts)
$runnerPath = Join-Path $outputPath "run-sms-phase5-canary-postcheck-readonly-$ChangeId.ps1"
$directoryCreated = $false; $fileCreated = $false
try {
    $null = New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop; $directoryCreated = $true
    [IO.File]::WriteAllText($runnerPath, $runnerText, (New-Object Text.UTF8Encoding($true))); $fileCreated = $true
    $tokens = $null; $parseErrors = $null
    $null = [Management.Automation.Language.Parser]::ParseFile($runnerPath, [ref]$tokens, [ref]$parseErrors)
    if (@($parseErrors).Count -ne 0) { throw "runner PowerShell 语法校验失败" }
    foreach ($forbidden in @("kill ", "systemctl", "docker restart", "curl -X POST", "INSERT ", "UPDATE ", "DELETE ", "SMS_ENABLED=true", "scp ")) {
        if ($remotePayload.Contains($forbidden)) { throw "远端负载包含禁止动作：$forbidden" }
    }
    $closedOutput = @(& $runnerPath); $selfTestOutput = @(& $runnerPath -SelfTest)
    if ($closedOutput -cnotcontains "canary_postcheck_readonly_execution_authorized=false" -or
        $selfTestOutput -cnotcontains "canary_postcheck_readonly_runner_self_test=passed") { throw "runner 默认关闭或离线自测失败" }
    Write-Output "canary_postcheck_readonly_candidate=passed"
    Write-Output "change_id=$ChangeId"
    Write-Output "source_canary_change_id=$SourceCanaryChangeId"
    Write-Output "source_plan_sha256=$ExpectedPlanSHA256"
    Write-Output "source_runner_sha256=$ExpectedCanaryRunnerSHA256"
    Write-Output "source_result_sha256=$ExpectedCanaryResultSHA256"
    Write-Output "runner_sha256=$((Get-FileHash -LiteralPath $runnerPath -Algorithm SHA256).Hash.ToLowerInvariant())"
    Write-Output "runner_path=$runnerPath"
    Write-Output "candidate_files_written=1"
    Write-Output "network_connections=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
}
catch {
    if ($fileCreated -and (Test-Path -LiteralPath $runnerPath)) { Remove-Item -LiteralPath $runnerPath -Force }
    if ($directoryCreated -and (Test-Path -LiteralPath $outputPath) -and @(Get-ChildItem -LiteralPath $outputPath -Force).Count -eq 0) { Remove-Item -LiteralPath $outputPath -Force }
    throw
}
