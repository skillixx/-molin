param(
    [string]$ChangeId = "",
    [string]$SourceCanaryChangeId = "",
    [string]$CanaryResultFile = "",
    [string]$ExpectedCanaryResultSHA256 = "",
    [int64]$ExpectedBaselineSendTotal = -1,
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
    # 候选生成只允许访问本机磁盘，拒绝 UNC、Provider 路径和网络映射盘。
    if ([string]::IsNullOrWhiteSpace($Path) -or $Path -match '^(?:\\\\|//)' -or $Path.Contains("::")) { throw "${Description}必须是本地绝对路径" }
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        if ($Path -cnotmatch '^[A-Za-z]:[\\/]') { throw "Windows ${Description}必须使用本地盘符绝对路径" }
        $drive = Get-PSDrive -Name $Path.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
        if (([string]$drive.Root).StartsWith([string][char]92) -or ([string]$drive.DisplayRoot).StartsWith([string][char]92)) { throw "${Description}不得使用网络映射盘" }
    }
    elseif (-not [IO.Path]::IsPathRooted($Path)) { throw "${Description}必须使用本地绝对路径" }
}

function Assert-PartialFailureResult {
    param([string]$Path)
    $allowed = @("scene_register_submitted", "scene_login_submitted", "canary_send", "failure_gate", "automatic_closed_state_restore", "service_stops", "service_starts", "sms_submission_requests", "automatic_retries", "remote_stderr_present", "canary_send_exit_code")
    $values = @{}
    foreach ($line in Get-Content -LiteralPath $Path -Encoding UTF8) {
        if ($line -cnotmatch '^(?<key>[a-z][a-z0-9_]*)=(?<value>[A-Za-z0-9_.:,-]+)$') { throw "源结果包含非低敏格式" }
        $key = $Matches['key']; $value = $Matches['value']
        if ($allowed -cnotcontains $key -or $values.ContainsKey($key)) { throw "源结果字段不在白名单或重复" }
        $values[$key] = $value
    }
    $expected = @{
        scene_register_submitted = "true"; scene_login_submitted = "true"; canary_send = "blocked";
        failure_gate = "scene_reset_password"; automatic_closed_state_restore = "true";
        service_stops = "2"; service_starts = "2"; sms_submission_requests = "3";
        automatic_retries = "0"; remote_stderr_present = "false"; canary_send_exit_code = "2"
    }
    foreach ($entry in $expected.GetEnumerator()) {
        if ($values[$entry.Key] -cne $entry.Value) { throw "源结果与已批准的部分失败不一致：$($entry.Key)" }
    }
}

if (-not $ExportCandidate -and -not $SelfTest) {
    Write-Output "canary_failure_readonly_candidate_authorized=false"
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
    Write-Output "canary_failure_readonly_candidate_self_test=passed"
    Write-Output "partial_failure_binding_required=true"
    Write-Output "baseline_send_total_required=true"
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

foreach ($id in @($ChangeId, $SourceCanaryChangeId)) { if ($id -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') { throw "ChangeId 必须使用 UTC 基本格式" } }
if ($ChangeId -ceq $SourceCanaryChangeId) { throw "诊断必须使用独立 ChangeId" }
if ($ExpectedCanaryResultSHA256 -cnotmatch '^[0-9a-f]{64}$') { throw "源结果摘要必须是小写完整 SHA-256" }
if ($ExpectedBaselineSendTotal -lt 0) { throw "必须提供发送前已验证的日志总数" }
Assert-LocalAbsolutePath -Path $CanaryResultFile -Description "源结果文件"
Assert-LocalAbsolutePath -Path $OutputDirectory -Description "候选输出目录"
$sourcePath = (Resolve-Path -LiteralPath $CanaryResultFile).Path
if ((Get-FileHash -LiteralPath $sourcePath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedCanaryResultSHA256) { throw "源结果摘要不匹配" }
Assert-PartialFailureResult -Path $sourcePath
$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
if (-not (Test-Path -LiteralPath (Split-Path -Parent $outputPath) -PathType Container)) { throw "候选输出父目录必须存在" }
if (Test-Path -LiteralPath $outputPath) { throw "候选输出目录已存在，禁止覆盖" }

$payloadTemplate = @'
set -euo pipefail
api_path='/home/pc/molin/molin-api'
env_file='/home/pc/molin/infra/.env.test'
alertmanager_config='/home/pc/molin-alertmanager-phase5/20260805T084215Z/alertmanager.closed.yml'
alertmanager_container='molin-alertmanager-phase5-closed'
alertmanager_port=19093
source_change_id='__SOURCE_CHANGE_ID__'
baseline_send_total=__BASELINE_SEND_TOTAL__
source_started_at='__SOURCE_STARTED_AT__'

fail_closed() {
  printf 'canary_failure_readonly_diagnostic=blocked\nfailure_gate=%s\n' "$1"
  printf 'configuration_mutations=0\nservice_signals=0\nservice_restarts=0\nbusiness_posts=0\nemails_sent=0\nsms_submission_requests=0\nreal_sms_sent=0\n'
  exit 3
}
api_pids() { pgrep -f "^${api_path}$" 2>/dev/null || true; }
read_process_env() { tr '\0' '\n' < "/proc/$1/environ" | sed -n "s/^$2=//p" | tail -n 1; }
read_file_env() { sed -n "s/^$1=//p" "$env_file" | tail -n 1; }
run_mysql_readonly() {
  local statement="$1" wrapped
  wrapped="SET SESSION TRANSACTION READ ONLY; START TRANSACTION READ ONLY; ${statement}; COMMIT;"
  if command -v mysql >/dev/null 2>&1; then
    printf '%s\n' "$wrapped" | MYSQL_PWD="$db_pass" mysql --batch --skip-column-names -h "$db_host" -P "${db_port:-3306}" -u "$db_user" "$db_name" 2>/dev/null
  else
    { printf '%s\n' "$db_pass"; printf '%s\n' "$wrapped"; } | docker exec -i molin-mysql sh -c 'IFS= read -r MYSQL_PWD; export MYSQL_PWD; exec mysql --batch --skip-column-names -u "$1" "$2"' sh "$db_user" "$db_name" 2>/dev/null
  fi
}
verify_discard() {
  [ -f "$alertmanager_config" ] && [ ! -L "$alertmanager_config" ] && python3 - "$alertmanager_config" <<'PY'
import re,sys
text=open(sys.argv[1],encoding="utf-8").read(); route=re.search(r"(?ms)^route:\s*\n(?P<body>(?:^[ \t]+.*\n?)*)",text); body=route.group("body") if route else ""
raise SystemExit(0 if re.search(r"(?m)^\s+receiver:\s*[\"\x27]?discard[\"\x27]?\s*$",body) and not re.search(r"(?m)^\s+routes:\s*$",body) else 2)
PY
  [ "$(docker inspect "$alertmanager_container" --format '{{.State.Running}}' 2>/dev/null)" = true ]
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${alertmanager_port}/-/ready" 2>/dev/null || true)" = 200 ]
}

mapfile -t running < <(api_pids); [ "${#running[@]}" -eq 1 ] || fail_closed api_process_count
pid="${running[0]}"
[ "$(read_process_env "$pid" SMS_ENABLED)" = false ] && [ "$(read_file_env SMS_ENABLED)" = false ] || fail_closed sms_not_closed
[ "$(read_process_env "$pid" SMS_TEST_MODE)" = true ] && [ "$(read_file_env SMS_TEST_MODE)" = true ] || fail_closed sms_test_mode
[ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)" = 200 ] || fail_closed ready
verify_discard || fail_closed alertmanager_discard
[ ! -e /home/pc/.molin-sms-phase5-canary-send.lock ] || fail_closed recovery_lock
[ ! -e "/home/pc/.molin-sms-phase5-canary-send-${source_change_id}" ] || fail_closed recovery_materials
db_host="$(read_process_env "$pid" MYSQL_HOST)"; db_port="$(read_process_env "$pid" MYSQL_PORT)"; db_user="$(read_process_env "$pid" MYSQL_USER)"; db_pass="$(read_process_env "$pid" MYSQL_PASSWORD)"; db_name="$(read_process_env "$pid" MYSQL_DATABASE)"
[ -n "$db_host" ] && [ -n "$db_user" ] && [ -n "$db_pass" ] && [ -n "$db_name" ] || fail_closed database_environment
summary="$(run_mysql_readonly "SELECT CONCAT((SELECT COUNT(*) FROM sms_send_logs),':',(SELECT COUNT(*) FROM sms_send_logs WHERE created_at>=STR_TO_DATE('${source_started_at}','%Y%m%dT%H%i%sZ')),':',(SELECT COUNT(*) FROM sms_send_logs WHERE created_at>=STR_TO_DATE('${source_started_at}','%Y%m%dT%H%i%sZ') AND submit_status='accepted'),':',(SELECT COUNT(*) FROM sms_send_logs WHERE created_at>=STR_TO_DATE('${source_started_at}','%Y%m%dT%H%i%sZ') AND submit_status='failed'),':',(SELECT COUNT(*) FROM sms_send_logs WHERE created_at>=STR_TO_DATE('${source_started_at}','%Y%m%dT%H%i%sZ') AND scene='register' AND submit_status='accepted'),':',(SELECT COUNT(*) FROM sms_send_logs WHERE created_at>=STR_TO_DATE('${source_started_at}','%Y%m%dT%H%i%sZ') AND scene='login' AND submit_status='accepted'),':',(SELECT COUNT(*) FROM sms_send_logs WHERE created_at>=STR_TO_DATE('${source_started_at}','%Y%m%dT%H%i%sZ') AND scene='reset_password' AND submit_status='failed'),':',(SELECT COUNT(*) FROM sms_send_logs WHERE created_at>=STR_TO_DATE('${source_started_at}','%Y%m%dT%H%i%sZ') AND scene='reset_password' AND failure_summary='短信供应商触发频率限制'),':',(SELECT COUNT(*) FROM verification_codes WHERE created_at>=STR_TO_DATE('${source_started_at}','%Y%m%dT%H%i%sZ') AND target_type='phone'),':',(SELECT COUNT(*) FROM verification_codes WHERE created_at>=STR_TO_DATE('${source_started_at}','%Y%m%dT%H%i%sZ') AND target_type='phone' AND used_at IS NULL))")" || fail_closed database_query
IFS=: read -r current_total event_logs event_accepted event_failed register_accepted login_accepted reset_failed reset_rate_limited event_codes event_unused <<<"$summary"
[[ "$summary" =~ ^[0-9]+(:[0-9]+){9}$ ]] || fail_closed summary_shape
delta=$((current_total-baseline_send_total)); [ "$delta" -ge 2 ] && [ "$delta" -le 3 ] || fail_closed send_delta

printf 'canary_failure_readonly_diagnostic=passed\nsms_enabled=false\nsms_test_mode=true\nready=true\nalertmanager_discard=true\n'
printf 'baseline_send_total=%s\ncurrent_send_total=%s\nsend_log_delta=%s\nevent_send_logs=%s\nevent_accepted=%s\nevent_failed=%s\n' "$baseline_send_total" "$current_total" "$delta" "$event_logs" "$event_accepted" "$event_failed"
printf 'register_accepted_logs=%s\nlogin_accepted_logs=%s\nreset_failed_logs=%s\nreset_provider_rate_limited_logs=%s\n' "$register_accepted" "$login_accepted" "$reset_failed" "$reset_rate_limited"
printf 'event_verification_codes=%s\nevent_unconsumed_codes=%s\nrecovery_lock_clear=true\nrecovery_materials_clear=true\n' "$event_codes" "$event_unused"
printf 'configuration_mutations=0\nservice_signals=0\nservice_restarts=0\nbusiness_posts=0\nemails_sent=0\nsms_submission_requests=0\nreal_sms_sent=0\n'
'@
$payload = $payloadTemplate.Replace("__SOURCE_CHANGE_ID__", $SourceCanaryChangeId).Replace("__BASELINE_SEND_TOTAL__", [string]$ExpectedBaselineSendTotal).Replace("__SOURCE_STARTED_AT__", $SourceCanaryChangeId)
$payloadBase64 = [Convert]::ToBase64String((New-Object Text.UTF8Encoding($false)).GetBytes($payload.Replace("`r`n", "`n")))
$sshHelperPath = Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1"
$sshHelperSHA256 = (Get-FileHash -LiteralPath $sshHelperPath -Algorithm SHA256).Hash.ToLowerInvariant()
$repoScripts = [IO.Path]::GetFullPath($PSScriptRoot)

$runnerTemplate = @'
param([switch]$ExecuteReadOnly, [switch]$SelfTest, [string]$ExpectedRunnerSHA256 = "")
$ErrorActionPreference = "Stop"
$ChangeId = "__CHANGE_ID__"; $SourceCanaryChangeId = "__SOURCE_CHANGE_ID__"
$SourceResultSHA256 = "__SOURCE_RESULT_SHA256__"; $RemotePayloadBase64 = "__PAYLOAD_BASE64__"
$ExpectedSSHHelperSHA256 = "__SSH_HELPER_SHA256__"; $ResultPath = Join-Path (Split-Path -Parent $PSCommandPath) "result-$ChangeId.txt"
if (-not $ExecuteReadOnly -and -not $SelfTest) {
    Write-Output "canary_failure_readonly_execution_authorized=false"; Write-Output "network_connections=0"; Write-Output "configuration_mutations=0"; Write-Output "business_posts=0"; Write-Output "real_sms_sent=0"; exit 0
}
if ($ExecuteReadOnly -and $SelfTest) { throw "ExecuteReadOnly 与 SelfTest 必须互斥" }
if ($SelfTest) {
    $payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64))
    foreach ($marker in @("START TRANSACTION READ ONLY", "reset_provider_rate_limited_logs", "business_posts=0", "real_sms_sent=0")) { if (-not $payload.Contains($marker)) { throw "诊断负载缺少安全标记：$marker" } }
    Write-Output "canary_failure_readonly_runner_self_test=passed"; Write-Output "network_connections=0"; Write-Output "configuration_mutations=0"; Write-Output "business_posts=0"; Write-Output "real_sms_sent=0"; exit 0
}
if ($ExpectedRunnerSHA256 -cnotmatch '^[0-9a-f]{64}$') { throw "只读执行必须提供获批 runner SHA-256" }
if ((Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedRunnerSHA256) { throw "runner SHA-256 不匹配" }
if (Test-Path -LiteralPath $ResultPath) { throw "结果文件已存在，禁止重复执行" }
$helper = Join-Path "__REPO_SCRIPTS__" "sms-phase5-test-server-ssh.ps1"
if ((Get-FileHash -LiteralPath $helper -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedSSHHelperSHA256) { throw "固定 SSH 辅助脚本摘要不匹配" }
. $helper; $knownHosts = Assert-SmsPhase5FixedTestServerIdentity -ServerHost '8.130.9.163' -SSHPort 10003 -SSHUser 'pc'
$utf8 = New-Object Text.UTF8Encoding($false); $inputBytes = $utf8.GetBytes([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64)))
$info = New-Object Diagnostics.ProcessStartInfo; $info.FileName="ssh.exe"; $info.UseShellExecute=$false; $info.RedirectStandardInput=$true; $info.RedirectStandardOutput=$true; $info.RedirectStandardError=$true; $info.CreateNoWindow=$true; $info.StandardOutputEncoding=$utf8; $info.StandardErrorEncoding=$utf8
$info.Arguments="-p 10003 -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 -o `"UserKnownHostsFile=$knownHosts`" -- pc@8.130.9.163 bash -s"
$process=New-Object Diagnostics.Process; $process.StartInfo=$info
try {
    if(-not $process.Start()){throw "无法启动固定 SSH 诊断进程"}; $outTask=$process.StandardOutput.ReadToEndAsync(); $errTask=$process.StandardError.ReadToEndAsync()
    try{$process.StandardInput.BaseStream.Write($inputBytes,0,$inputBytes.Length);$process.StandardInput.BaseStream.Flush()}finally{[Array]::Clear($inputBytes,0,$inputBytes.Length);$process.StandardInput.Close()}
    $process.WaitForExit();$stdout=$outTask.Result;$stderr=$errTask.Result;$exitCode=$process.ExitCode
}finally{$process.Dispose()}
$safeKeys=@("canary_failure_readonly_diagnostic","failure_gate","sms_enabled","sms_test_mode","ready","alertmanager_discard","baseline_send_total","current_send_total","send_log_delta","event_send_logs","event_accepted","event_failed","register_accepted_logs","login_accepted_logs","reset_failed_logs","reset_provider_rate_limited_logs","event_verification_codes","event_unconsumed_codes","recovery_lock_clear","recovery_materials_clear","configuration_mutations","service_signals","service_restarts","business_posts","emails_sent","sms_submission_requests","real_sms_sent")
$safe=@();foreach($line in @($stdout -split "`r?`n"|Where-Object{$_ -ne ""})){if($line -cnotmatch '^(?<key>[a-z][a-z0-9_]*)=(?:true|false|passed|blocked|[a-z][a-z0-9_]*|[0-9]+)$' -or $safeKeys -cnotcontains $Matches['key']){throw "远端输出不符合低敏白名单"};$safe+=$line}
foreach($marker in @("configuration_mutations=0","service_signals=0","service_restarts=0","business_posts=0","emails_sent=0","sms_submission_requests=0","real_sms_sent=0")){if($safe -cnotcontains $marker){throw "零副作用证据不完整：$marker"}}
$stderrPresent=(-not [string]::IsNullOrWhiteSpace($stderr)).ToString().ToLowerInvariant();$safe+="network_connections=1";$safe+="remote_stderr_present=$stderrPresent";$safe+="readonly_exit_code=$exitCode"
$bytes=$utf8.GetBytes(($safe -join "`n")+"`n");$stream=[IO.File]::Open($ResultPath,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None);try{$stream.Write($bytes,0,$bytes.Length)}finally{$stream.Dispose();[Array]::Clear($bytes,0,$bytes.Length)}
$safe|Write-Output;Write-Output "result_sha256=$((Get-FileHash -LiteralPath $ResultPath -Algorithm SHA256).Hash.ToLowerInvariant())"
if($exitCode -ne 0 -or $stderrPresent -ne "false"){throw "固定测试服部分失败只读诊断未通过，退出码：$exitCode"}
'@
$runnerText=$runnerTemplate.Replace("__CHANGE_ID__",$ChangeId).Replace("__SOURCE_CHANGE_ID__",$SourceCanaryChangeId).Replace("__SOURCE_RESULT_SHA256__",$ExpectedCanaryResultSHA256).Replace("__PAYLOAD_BASE64__",$payloadBase64).Replace("__SSH_HELPER_SHA256__",$sshHelperSHA256).Replace("__REPO_SCRIPTS__",$repoScripts)
$runnerPath=Join-Path $outputPath "run-sms-phase5-canary-failure-readonly-diagnostic-$ChangeId.ps1"
$directoryCreated=$false;$fileCreated=$false
try{
    $null=New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop;$directoryCreated=$true
    [IO.File]::WriteAllText($runnerPath,$runnerText,(New-Object Text.UTF8Encoding($true)));$fileCreated=$true
    $tokens=$null;$errors=$null;$null=[Management.Automation.Language.Parser]::ParseFile($runnerPath,[ref]$tokens,[ref]$errors);if(@($errors).Count-ne 0){throw "runner 语法失败"}
    foreach($forbidden in @("sleep ","kill ","systemctl","docker restart","curl -X POST","INSERT ","UPDATE ","DELETE ","SMS_ENABLED=true","scp ")){if($payload.Contains($forbidden)){throw "诊断负载包含禁止动作：$forbidden"}}
    $closed=@(& $runnerPath);$checked=@(& $runnerPath -SelfTest);if($closed -cnotcontains "canary_failure_readonly_execution_authorized=false" -or $checked -cnotcontains "canary_failure_readonly_runner_self_test=passed"){throw "runner 默认关闭或自测失败"}
    Write-Output "canary_failure_readonly_candidate=passed";Write-Output "change_id=$ChangeId";Write-Output "source_canary_change_id=$SourceCanaryChangeId";Write-Output "source_result_sha256=$ExpectedCanaryResultSHA256";Write-Output "baseline_send_total=$ExpectedBaselineSendTotal";Write-Output "runner_sha256=$((Get-FileHash -LiteralPath $runnerPath -Algorithm SHA256).Hash.ToLowerInvariant())";Write-Output "runner_path=$runnerPath";Write-Output "candidate_files_written=1";Write-Output "network_connections=0";Write-Output "configuration_mutations=0";Write-Output "business_posts=0";Write-Output "real_sms_sent=0"
}catch{if($fileCreated-and(Test-Path -LiteralPath $runnerPath)){Remove-Item -LiteralPath $runnerPath -Force};if($directoryCreated-and(Test-Path -LiteralPath $outputPath)-and@(Get-ChildItem -LiteralPath $outputPath -Force).Count-eq 0){Remove-Item -LiteralPath $outputPath -Force};throw}
