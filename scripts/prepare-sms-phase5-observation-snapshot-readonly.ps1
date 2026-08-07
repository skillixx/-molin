param(
    [string]$ChangeId = "",
    [string]$SourceCanaryChangeId = "",
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
$Windows = @{ "5m" = 300; "15m" = 900; "30m" = 1800; "2h" = 7200; "24h" = 86400 }

function Assert-LocalAbsolutePath {
    param([string]$Path, [string]$Description)
    # 生成阶段只能访问本机磁盘，避免 UNC、Provider 路径或网络映射盘造成隐式联网。
    if ([string]::IsNullOrWhiteSpace($Path) -or $Path -match '^(?:\\\\|//)' -or $Path.Contains("::")) {
        throw "${Description}必须是本地文件系统绝对路径"
    }
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        if ($Path -cnotmatch '^[A-Za-z]:[\\/]') { throw "Windows ${Description}必须使用本地盘符绝对路径" }
        $drive = Get-PSDrive -Name $Path.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
        if (([string]$drive.Root).StartsWith([string][char]92) -or
            ([string]$drive.DisplayRoot).StartsWith([string][char]92)) { throw "${Description}不得使用网络映射盘" }
    }
    elseif (-not [IO.Path]::IsPathRooted($Path)) { throw "${Description}必须使用本地绝对路径" }
}

function Read-CanaryResult {
    param([string]$Path)
    $allowed = @(
        "canary_send", "scene_register_submitted", "scene_login_submitted", "scene_reset_password_submitted",
        "scene_bind_phone_submitted", "scene_admin_verify_submitted", "requested_sends", "completed_scenes",
        "sms_enabled", "sms_test_mode", "same_target_min_interval_seconds", "scheduled_waits", "completed_pacing_waits",
        "baseline_send_log_id", "baseline_verification_code_id",
        "baseline_send_total", "baseline_send_accepted", "baseline_send_failed", "baseline_provider_calls_total",
        "baseline_provider_nonaccepted_total", "canary_completed_at", "sensitive_values_persisted",
        "real_sms_receipt_confirmed", "service_stops", "service_starts", "sms_submission_requests",
        "automatic_retries", "remote_stderr_present", "canary_send_exit_code"
    )
    $values = @{}
    $duplicateCounts = @{}
    foreach ($line in Get-Content -LiteralPath $Path -Encoding UTF8) {
        if ($line -cnotmatch '^(?<key>[a-z][a-z0-9_]*)=(?<value>[A-Za-z0-9_.:,-]+)$') { throw "Canary 结果包含非低敏格式" }
        $key = $Matches['key']; $value = $Matches['value']
        if ($allowed -cnotcontains $key) { throw "Canary 结果字段不在白名单" }
        if ($values.ContainsKey($key)) {
            if (@("same_target_min_interval_seconds", "scheduled_waits", "completed_pacing_waits") -cnotcontains $key -or
                $values[$key] -cne $value -or [int]$duplicateCounts[$key] -ge 1) { throw "Canary 结果包含未批准的重复字段" }
            $duplicateCounts[$key] = 1
            continue
        }
        $values[$key] = $value
    }
    $required = @{
        canary_send = "awaiting_manual_receipt_confirmation"; requested_sends = "5"; completed_scenes = "5";
        sms_enabled = "false"; sms_test_mode = "true"; sms_submission_requests = "5"; automatic_retries = "0";
        same_target_min_interval_seconds = "65"; scheduled_waits = "2"; completed_pacing_waits = "2";
        sensitive_values_persisted = "0"; remote_stderr_present = "false"; canary_send_exit_code = "0"
    }
    foreach ($item in $required.GetEnumerator()) {
        if ($values[$item.Key] -cne $item.Value) { throw "Canary 结果没有证明成功与关闭态恢复：$($item.Key)" }
    }
    foreach ($scene in @("register", "login", "reset_password", "bind_phone", "admin_verify")) {
        if ($values["scene_${scene}_submitted"] -cne "true") { throw "Canary 场景未完成：$scene" }
    }
    foreach ($key in @("baseline_send_total", "baseline_send_accepted", "baseline_send_failed", "baseline_provider_calls_total", "baseline_provider_nonaccepted_total")) {
        if ($values[$key] -cnotmatch '^[0-9]+$') { throw "Canary 结果缺少观察基线：$key" }
    }
    if ($values["canary_completed_at"] -cnotmatch '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$') { throw "Canary 完成时间不符合 UTC 格式" }
    return $values
}

if (-not $ExportCandidate -and -not $SelfTest) {
    Write-Output "observation_snapshot_candidate_authorized=false"
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
    if ($Windows.Count -ne 5 -or $Windows["5m"] -ne 300 -or $Windows["24h"] -ne 86400) { throw "五档观察窗口发生漂移" }
    Write-Output "observation_snapshot_candidate_self_test=passed"
    Write-Output "observation_windows=5m,15m,30m,2h,24h"
    Write-Output "no_internal_sleep=true"
    Write-Output "one_snapshot_per_window=true"
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
if ($ChangeId -ceq $SourceCanaryChangeId) { throw "观察快照候选必须使用独立 ChangeId" }
if ($ExpectedCanaryResultSHA256 -cnotmatch '^[0-9a-f]{64}$') { throw "Canary 结果摘要必须是小写完整 SHA-256" }
Assert-LocalAbsolutePath -Path $CanaryResultFile -Description "Canary 结果文件"
Assert-LocalAbsolutePath -Path $OutputDirectory -Description "候选输出目录"
$sourcePath = (Resolve-Path -LiteralPath $CanaryResultFile).Path
if ((Get-FileHash -LiteralPath $sourcePath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedCanaryResultSHA256) { throw "Canary 结果摘要不匹配" }
$source = Read-CanaryResult -Path $sourcePath
$completedAt = $source["canary_completed_at"]
$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
if (-not (Test-Path -LiteralPath (Split-Path -Parent $outputPath) -PathType Container)) { throw "候选输出父目录必须已存在" }
if (Test-Path -LiteralPath $outputPath) { throw "候选输出目录已存在，禁止覆盖" }

# 远端负载不 sleep、不写文件，只读取当前关闭态、数据库汇总、内部 metrics 与监控状态。
$remotePayloadTemplate = @'
set -euo pipefail
api_path='/home/pc/molin/molin-api'
env_file='/home/pc/molin/infra/.env.test'
alertmanager_config='/home/pc/molin-alertmanager-phase5/20260805T084215Z/alertmanager.closed.yml'
alertmanager_container='molin-alertmanager-phase5-closed'
alertmanager_port=19093
prometheus_port=19090
completed_at='__COMPLETED_AT__'
window='__WINDOW__'
minimum_elapsed=__MINIMUM_ELAPSED__

fail_closed() {
  printf 'observation_snapshot=blocked\nfailure_gate=%s\n' "$1"
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
verify_alertmanager_discard() {
  [ -f "$alertmanager_config" ] && [ ! -L "$alertmanager_config" ] &&
  python3 - "$alertmanager_config" <<'PY'
import re,sys
text=open(sys.argv[1],encoding="utf-8").read(); route=re.search(r"(?ms)^route:\s*\n(?P<body>(?:^[ \t]+.*\n?)*)",text)
body=route.group("body") if route else ""
raise SystemExit(0 if re.search(r"(?m)^\s+receiver:\s*[\"\x27]?discard[\"\x27]?\s*$",body) and not re.search(r"(?m)^\s+routes:\s*$",body) else 2)
PY
  [ "$(docker inspect "$alertmanager_container" --format '{{.State.Running}}' 2>/dev/null)" = true ]
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${alertmanager_port}/-/ready" 2>/dev/null || true)" = 200 ]
}

observed_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
completed_epoch="$(date -u -d "$completed_at" +%s)" || fail_closed completed_time
observed_epoch="$(date -u -d "$observed_at" +%s)" || fail_closed observed_time
elapsed_seconds=$((observed_epoch-completed_epoch))
[ "$elapsed_seconds" -ge "$minimum_elapsed" ] || fail_closed window_not_reached
mapfile -t running < <(api_pids); [ "${#running[@]}" -eq 1 ] || fail_closed api_process_count
pid="${running[0]}"
[ "$(read_process_env "$pid" SMS_ENABLED)" = false ] && [ "$(read_file_env SMS_ENABLED)" = false ] || fail_closed sms_not_closed
[ "$(read_process_env "$pid" SMS_TEST_MODE)" = true ] && [ "$(read_file_env SMS_TEST_MODE)" = true ] || fail_closed sms_test_mode
health_http="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health 2>/dev/null || true)"
ready_http="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)"
[ "$health_http:$ready_http" = 200:200 ] || fail_closed api_health
db_host="$(read_process_env "$pid" MYSQL_HOST)"; db_port="$(read_process_env "$pid" MYSQL_PORT)"; db_user="$(read_process_env "$pid" MYSQL_USER)"; db_pass="$(read_process_env "$pid" MYSQL_PASSWORD)"; db_name="$(read_process_env "$pid" MYSQL_DATABASE)"
[ -n "$db_host" ] && [ -n "$db_user" ] && [ -n "$db_pass" ] && [ -n "$db_name" ] || fail_closed database_environment
send_summary="$(run_mysql_readonly "SELECT CONCAT(COUNT(*),':',COALESCE(SUM(submit_status='accepted'),0),':',COALESCE(SUM(submit_status='failed'),0)) FROM sms_send_logs")" || fail_closed send_summary
IFS=: read -r send_total send_accepted send_failed <<<"$send_summary"
[[ "$send_total:$send_accepted:$send_failed" =~ ^[0-9]+:[0-9]+:[0-9]+$ ]] || fail_closed send_shape
[ "$send_total" -eq $((send_accepted+send_failed)) ] || fail_closed send_conservation
internal_token="$(read_process_env "$pid" INTERNAL_API_TOKEN)"; [ -n "$internal_token" ] || fail_closed internal_token
metrics="$(printf 'X-Internal-Token: %s\n' "$internal_token" | curl -fsS --max-time 5 -H @- http://127.0.0.1:8080/api/internal/metrics 2>/dev/null)" || fail_closed internal_metrics
provider_calls_total="$(printf '%s\n' "$metrics" | awk '/^sms_provider_calls_total\{/{sum+=$NF} END{printf "%.0f",sum+0}')"
provider_nonaccepted_total="$(printf '%s\n' "$metrics" | awk '/^sms_provider_calls_total\{/{if($0 !~ /result="accepted"/) sum+=$NF} END{printf "%.0f",sum+0}')"
duration_pair="$(printf '%s\n' "$metrics" | awk '/^sms_provider_request_duration_seconds_sum\{/{sum+=$NF} /^sms_provider_request_duration_seconds_count\{/{count+=$NF} END{printf "%.6f:%d",sum+0,count+0}')"
IFS=: read -r duration_sum duration_count <<<"$duration_pair"
avg_duration="$(awk -v s="$duration_sum" -v c="$duration_count" 'BEGIN{if(c==0) printf "0.000000"; else printf "%.6f",s/c}')"
verify_alertmanager_discard || fail_closed alertmanager_discard
active_alertmanager="$(curl -fsS --max-time 5 "http://127.0.0.1:${alertmanager_port}/api/v2/alerts" | python3 -c 'import json,sys; v=json.load(sys.stdin); print(len(v) if isinstance(v,list) else -1)')" || fail_closed alertmanager_alerts
active_sms="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/alerts" | python3 -c 'import json,sys; a=json.load(sys.stdin).get("data",{}).get("alerts",[]); print(sum(1 for x in a if str(x.get("labels",{}).get("alertname","")).startswith("MolinSMS")))')" || fail_closed prometheus_alerts
notification_failed="$(curl -fsS --max-time 5 "http://127.0.0.1:${alertmanager_port}/metrics" | awk '/^alertmanager_notifications_failed_total({| )/{sum+=$NF} END{printf "%.0f",sum+0}')" || fail_closed notification_metrics
[ "$active_alertmanager:$active_sms:$notification_failed" = 0:0:0 ] || fail_closed monitoring_state

printf 'observation_snapshot=passed\nwindow=%s\nobserved_at=%s\nelapsed_seconds=%s\n' "$window" "$observed_at" "$elapsed_seconds"
printf 'api_health_http=%s\napi_ready_http=%s\nsend_total=%s\nsend_accepted=%s\nsend_failed=%s\n' "$health_http" "$ready_http" "$send_total" "$send_accepted" "$send_failed"
printf 'provider_calls_total=%s\nprovider_nonaccepted_total=%s\navg_provider_duration_seconds=%s\n' "$provider_calls_total" "$provider_nonaccepted_total" "$avg_duration"
printf 'active_sms_alerts=0\nactive_alertmanager_alerts=0\nnotification_failed_delta=0\n'
printf 'configuration_mutations=0\nservice_signals=0\nservice_restarts=0\nbusiness_posts=0\nemails_sent=0\nsms_submission_requests=0\nreal_sms_sent=0\n'
'@

$sshHelperPath = Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1"
$sshHelperSHA256 = (Get-FileHash -LiteralPath $sshHelperPath -Algorithm SHA256).Hash.ToLowerInvariant()
$repoScripts = [IO.Path]::GetFullPath($PSScriptRoot)
$payloads = @{}
foreach ($window in $Windows.Keys) {
    $payload = $remotePayloadTemplate.Replace("__COMPLETED_AT__", $completedAt).Replace("__WINDOW__", $window).Replace("__MINIMUM_ELAPSED__", [string]$Windows[$window])
    $payloads[$window] = [Convert]::ToBase64String((New-Object Text.UTF8Encoding($false)).GetBytes($payload.Replace("`r`n", "`n")))
}
$payloadJson = ConvertTo-Json $payloads -Compress

$runnerTemplate = @'
param(
    [ValidateSet("5m", "15m", "30m", "2h", "24h")][string]$Window = "",
    [switch]$ExecuteReadOnly,
    [switch]$SelfTest,
    [string]$ExpectedRunnerSHA256 = ""
)
$ErrorActionPreference = "Stop"
function Get-SafeStderrMetadata {
    param([string]$Text)
    # 仅返回固定分类和不可逆摘要，禁止把远端 stderr 正文写入控制台、文件或异常消息。
    $normalized = $Text.Replace("`r`n", "`n").Replace("`r", "`n").TrimEnd("`n")
    $lines = if ([string]::IsNullOrEmpty($normalized)) { @() } else { @($normalized -split "`n") }
    $classification = "other"
    if ($normalized -ceq "Pseudo-terminal will not be allocated because stdin is not a terminal.") {
        $classification = "pty_warning"
    }
    elseif ($lines.Count -gt 0 -and @($lines | Where-Object { $_ -cnotmatch '^(?:bash: )?warning: setlocale: [A-Z_]+: cannot change locale .+$' }).Count -eq 0) {
        $classification = "locale_warning"
    }
    elseif ($lines.Count -gt 0 -and @($lines | Where-Object { $_ -cnotmatch '^Warning: Permanently added .+ to the list of known hosts\.$' }).Count -eq 0) {
        $classification = "known_hosts_warning"
    }
    elseif ($lines.Count -gt 0 -and @($lines | Where-Object { $_ -cnotmatch '^Warning: .+$' }).Count -eq 0) {
        $classification = "ssh_warning"
    }
    elseif ($lines.Count -gt 0 -and @($lines | Where-Object { $_ -cnotmatch '^bash: (?:warning: )?.+$' }).Count -eq 0) {
        $classification = "bash_warning"
    }
    $bytes = [Text.Encoding]::UTF8.GetBytes($Text)
    try {
        $sha = [Security.Cryptography.SHA256]::Create()
        try { $digest = ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace("-", "").ToLowerInvariant() }
        finally { $sha.Dispose() }
    }
    finally { [Array]::Clear($bytes, 0, $bytes.Length) }
    [pscustomobject]@{ Classification = $classification; ByteCount = [Text.Encoding]::UTF8.GetByteCount($Text); LineCount = $lines.Count; SHA256 = $digest }
}

function New-ObservationTransportDirectory {
    # Windows PowerShell 5.1 的 StandardInput StreamWriter 会在原始字节前注入 BOM；改用受限临时文件句柄传输。
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    $leaf = "molin-sms-observation-" + [Guid]::NewGuid().ToString("N")
    $path = [IO.Path]::GetFullPath((Join-Path $tempRoot $leaf))
    if (-not $path.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -or
        [IO.Directory]::Exists($path) -or [IO.File]::Exists($path)) {
        throw "观察传输临时目录路径异常"
    }
    [void][IO.Directory]::CreateDirectory($path)
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
        $security = New-Object Security.AccessControl.DirectorySecurity
        $security.SetOwner($currentSid)
        $security.SetAccessRuleProtection($true, $false)
        $rule = New-Object Security.AccessControl.FileSystemAccessRule(
            $currentSid,
            [Security.AccessControl.FileSystemRights]::FullControl,
            [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit',
            [Security.AccessControl.PropagationFlags]::None,
            [Security.AccessControl.AccessControlType]::Allow
        )
        [void]$security.AddAccessRule($rule)
        [IO.Directory]::SetAccessControl($path, $security)
    }
    $item = [IO.DirectoryInfo]::new($path)
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.FullName -cne $path) {
        throw "观察传输临时目录身份异常"
    }
    return $path
}

function Remove-ObservationTransportDirectory {
    param([Parameter(Mandatory = $true)][string]$Path)

    $leaf = [IO.Path]::GetFileName($Path)
    if (-not [IO.Path]::IsPathRooted($Path) -or $leaf -cnotmatch '^molin-sms-observation-[a-f0-9]{32}$') {
        throw "观察传输临时目录清理路径异常"
    }
    foreach ($name in @("stdin.bin", "stdout.bin", "stderr.bin")) {
        $target = Join-Path $Path $name
        if ([IO.File]::Exists($target)) { [IO.File]::Delete($target) }
    }
    if ([IO.Directory]::Exists($Path)) {
        if ([IO.Directory]::GetFileSystemEntries($Path).Length -ne 0) { throw "观察传输临时目录存在未知文件" }
        [IO.Directory]::Delete($Path, $false)
    }
}

function Invoke-ObservationSshProcess {
    param(
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][byte[]]$InputBytes
    )

    $transportDirectory = New-ObservationTransportDirectory
    $stdinPath = Join-Path $transportDirectory "stdin.bin"
    $stdoutPath = Join-Path $transportDirectory "stdout.bin"
    $stderrPath = Join-Path $transportDirectory "stderr.bin"
    try {
        [IO.File]::WriteAllBytes($stdinPath, $InputBytes)
        $readBack = [IO.File]::ReadAllBytes($stdinPath)
        try {
            if ($readBack.Length -ne $InputBytes.Length) { throw "观察 stdin 写入长度不一致" }
            for ($index = 0; $index -lt $InputBytes.Length; $index++) {
                if ($readBack[$index] -ne $InputBytes[$index]) { throw "观察 stdin 写入内容不一致" }
            }
        }
        finally { [Array]::Clear($readBack, 0, $readBack.Length); $readBack = $null }
        $process = Microsoft.PowerShell.Management\Start-Process -FilePath "ssh.exe" -ArgumentList $Arguments `
            -RedirectStandardInput $stdinPath -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath `
            -NoNewWindow -PassThru
        try {
            # 等待前先取得原生句柄，避免 Windows PowerShell 5.1 丢失真实退出码。
            $processHandle = $process.Handle
            if ($processHandle -eq [IntPtr]::Zero) { throw "观察 SSH 进程句柄不可用" }
            if (-not $process.WaitForExit(120000)) {
                $process.Kill(); $process.WaitForExit(); throw "观察 SSH 进程超时"
            }
            $process.Refresh()
            $exitCode = $process.ExitCode
            if ($null -eq $exitCode) { throw "观察 SSH 退出码不可用" }
        }
        catch {
            # 取得句柄或等待过程异常时也必须终止本次精确 SSH 子进程，避免后台残留连接。
            try {
                if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() }
            }
            catch { }
            throw
        }
        finally { $process.Dispose() }
        $strictUtf8 = New-Object Text.UTF8Encoding($false, $true)
        return [pscustomobject]@{
            ExitCode = [int]$exitCode
            Stdout = $strictUtf8.GetString([IO.File]::ReadAllBytes($stdoutPath))
            Stderr = $strictUtf8.GetString([IO.File]::ReadAllBytes($stderrPath))
        }
    }
    finally { Remove-ObservationTransportDirectory -Path $transportDirectory }
}
$ChangeId = "__CHANGE_ID__"
$SourceCanaryChangeId = "__SOURCE_CHANGE_ID__"
$SourceResultSHA256 = "__SOURCE_RESULT_SHA256__"
$CanaryCompletedAt = "__COMPLETED_AT__"
$Payloads = ConvertFrom-Json '__PAYLOAD_JSON__'
$ExpectedSSHHelperSHA256 = "__SSH_HELPER_SHA256__"
$MinimumElapsed = @{ "5m" = 300; "15m" = 900; "30m" = 1800; "2h" = 7200; "24h" = 86400 }

if (-not $ExecuteReadOnly -and -not $SelfTest) {
    Write-Output "observation_snapshot_execution_authorized=false"
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
    foreach ($name in $MinimumElapsed.Keys) {
        $payloadBytes = [Convert]::FromBase64String($Payloads.$name)
        if ($payloadBytes.Length -lt 3 -or $payloadBytes[0] -ne 0x73 -or $payloadBytes[1] -ne 0x65 -or $payloadBytes[2] -ne 0x74) {
            throw "窗口 $name 负载首字节不是无 BOM 的 set"
        }
        $payload = [Text.Encoding]::UTF8.GetString($payloadBytes)
        foreach ($marker in @("START TRANSACTION READ ONLY", "minimum_elapsed=$($MinimumElapsed[$name])", "business_posts=0", "real_sms_sent=0")) {
            if (-not $payload.Contains($marker)) { throw "窗口 $name 负载缺少安全标记：$marker" }
        }
    }
    # 使用纯合成文本验证分类器，不读取任何真实 stderr 或外部状态。
    if ((Get-SafeStderrMetadata -Text "Pseudo-terminal will not be allocated because stdin is not a terminal.").Classification -cne "pty_warning") { throw "stderr 伪终端分类自测失败" }
    if ((Get-SafeStderrMetadata -Text "bash: warning: setlocale: LC_ALL: cannot change locale C").Classification -cne "locale_warning") { throw "stderr locale 分类自测失败" }
    if ((Get-SafeStderrMetadata -Text "未分类合成文本").Classification -cne "other") { throw "stderr 未知分类自测失败" }
    Write-Output "observation_snapshot_runner_self_test=passed"
    Write-Output "stderr_metadata_self_test=passed"
    Write-Output "stdin_transport=file_redirect_no_bom"
    Write-Output "observation_windows=5m,15m,30m,2h,24h"
    Write-Output "no_internal_sleep=true"
    Write-Output "network_connections=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ([string]::IsNullOrWhiteSpace($Window)) { throw "只读执行必须指定一个观察窗口" }
if ($ExpectedRunnerSHA256 -cnotmatch '^[0-9a-f]{64}$') { throw "只读执行必须提供获批的完整 runner SHA-256" }
if ((Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedRunnerSHA256) { throw "runner SHA-256 与批准值不匹配" }
$snapshotPath = Join-Path (Split-Path -Parent $PSCommandPath) "snapshot-$Window.json"
if (Test-Path -LiteralPath $snapshotPath) { throw "该观察窗口快照已存在，禁止重复执行" }
$completed = [DateTimeOffset]::ParseExact($CanaryCompletedAt, "yyyy-MM-dd'T'HH:mm:ss'Z'", [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::AssumeUniversal)
$elapsed = [int64]([DateTimeOffset]::UtcNow - $completed).TotalSeconds
if ($elapsed -lt $MinimumElapsed[$Window]) { throw "观察窗口尚未到达，禁止提前连接测试服" }
$sshHelperPath = Join-Path "__REPO_SCRIPTS__" "sms-phase5-test-server-ssh.ps1"
if ((Get-FileHash -LiteralPath $sshHelperPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedSSHHelperSHA256) { throw "固定 SSH 身份辅助脚本摘要不匹配" }
. $sshHelperPath
$knownHosts = Assert-SmsPhase5FixedTestServerIdentity -ServerHost '8.130.9.163' -SSHPort 10003 -SSHUser 'pc'
$utf8 = New-Object Text.UTF8Encoding($false)
$inputBytes = [Convert]::FromBase64String($Payloads.$Window)
if ($inputBytes.Length -lt 3 -or $inputBytes[0] -ne 0x73 -or $inputBytes[1] -ne 0x65 -or $inputBytes[2] -ne 0x74) {
    [Array]::Clear($inputBytes, 0, $inputBytes.Length)
    throw "观察负载首字节不是无 BOM 的 set"
}
# 明确禁用伪终端，并使用隔离环境和无启动文件 Bash，避免继承远端 locale 或启动脚本产生非业务 stderr。
$sshArguments = @(
    "-T", "-p", "10003", "-o", "BatchMode=yes", "-o", "NumberOfPasswordPrompts=0", "-o", "ConnectTimeout=8",
    "-o", "StrictHostKeyChecking=yes", "-o", "HostKeyAlgorithms=ssh-ed25519", "-o", "UserKnownHostsFile=$knownHosts", "--",
    "pc@8.130.9.163", "/usr/bin/env", "-i", "PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/home/pc", "USER=pc", "LOGNAME=pc",
    "LANG=C", "LC_ALL=C", "/bin/bash", "--noprofile", "--norc", "-s", "--"
)
try {
    $execution = Invoke-ObservationSshProcess -Arguments $sshArguments -InputBytes $inputBytes
    $stdout = $execution.Stdout; $stderr = $execution.Stderr; $remoteExitCode = $execution.ExitCode
}
finally { [Array]::Clear($inputBytes, 0, $inputBytes.Length); $inputBytes = $null }
$safeKeys = @(
    "observation_snapshot", "failure_gate", "window", "observed_at", "elapsed_seconds", "api_health_http", "api_ready_http",
    "send_total", "send_accepted", "send_failed", "provider_calls_total", "provider_nonaccepted_total",
    "avg_provider_duration_seconds", "active_sms_alerts", "active_alertmanager_alerts", "notification_failed_delta",
    "configuration_mutations", "service_signals", "service_restarts", "business_posts", "emails_sent", "sms_submission_requests", "real_sms_sent"
)
$values = @{}; $safeLines = @()
foreach ($line in @($stdout -split "`r?`n" | Where-Object { $_ -ne "" })) {
    if ($line -cnotmatch '^(?<key>[a-z][a-z0-9_]*)=(?<value>[A-Za-z0-9_.:,-]+)$' -or $safeKeys -cnotcontains $Matches['key'] -or $values.ContainsKey($Matches['key'])) { throw "远端输出不符合低敏白名单" }
    $values[$Matches['key']] = $Matches['value']; $safeLines += $line
}
foreach ($marker in @("configuration_mutations=0", "service_signals=0", "service_restarts=0", "business_posts=0", "emails_sent=0", "sms_submission_requests=0", "real_sms_sent=0")) {
    if ($safeLines -cnotcontains $marker) { throw "观察快照零副作用证据不完整：$marker" }
}
$stderrPresent = -not [string]::IsNullOrWhiteSpace($stderr)
if ($remoteExitCode -ne 0 -or $stderrPresent -or $values["observation_snapshot"] -cne "passed" -or $values["window"] -cne $Window) {
    # 失败时只回显已经通过白名单校验的远端摘要和布尔状态，禁止输出 stderr 正文或其他未批准内容。
    $safeLines | Write-Output
    Write-Output "network_connections=1"
    Write-Output "remote_stderr_present=$($stderrPresent.ToString().ToLowerInvariant())"
    if ($stderrPresent) {
        $stderrMetadata = Get-SafeStderrMetadata -Text $stderr
        Write-Output "remote_stderr_classification=$($stderrMetadata.Classification)"
        Write-Output "remote_stderr_byte_count=$($stderrMetadata.ByteCount)"
        Write-Output "remote_stderr_line_count=$($stderrMetadata.LineCount)"
        Write-Output "remote_stderr_sha256=$($stderrMetadata.SHA256)"
    }
    Write-Output "readonly_exit_code=$remoteExitCode"
    throw "固定测试服观察快照未通过，退出码：$remoteExitCode"
}
$snapshot = [ordered]@{
    schema_version = 1
    source_canary_change_id = $SourceCanaryChangeId
    snapshot = [ordered]@{
        window = $Window; observed_at = $values["observed_at"]; elapsed_seconds = [int64]$values["elapsed_seconds"]
        api_health_http = [int]$values["api_health_http"]; api_ready_http = [int]$values["api_ready_http"]
        send_total = [int64]$values["send_total"]; send_accepted = [int64]$values["send_accepted"]; send_failed = [int64]$values["send_failed"]
        provider_calls_total = [int64]$values["provider_calls_total"]; provider_nonaccepted_total = [int64]$values["provider_nonaccepted_total"]
        avg_provider_duration_seconds = [double]::Parse($values["avg_provider_duration_seconds"], [Globalization.CultureInfo]::InvariantCulture)
        active_sms_alerts = [int]$values["active_sms_alerts"]; active_alertmanager_alerts = [int]$values["active_alertmanager_alerts"]
        notification_failed_delta = [int64]$values["notification_failed_delta"]
    }
}
$json = ($snapshot | ConvertTo-Json -Depth 4) + "`n"; $bytes = $utf8.GetBytes($json.Replace("`r`n", "`n"))
$stream = [IO.File]::Open($snapshotPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
try { $stream.Write($bytes, 0, $bytes.Length) } finally { $stream.Dispose(); [Array]::Clear($bytes, 0, $bytes.Length) }
$safeLines | Write-Output
Write-Output "network_connections=1"
Write-Output "remote_stderr_present=false"
Write-Output "readonly_exit_code=0"
Write-Output "snapshot_sha256=$((Get-FileHash -LiteralPath $snapshotPath -Algorithm SHA256).Hash.ToLowerInvariant())"
Write-Output "snapshot_path=$snapshotPath"
'@

$runnerText = $runnerTemplate.Replace("__CHANGE_ID__", $ChangeId).
    Replace("__SOURCE_CHANGE_ID__", $SourceCanaryChangeId).
    Replace("__SOURCE_RESULT_SHA256__", $ExpectedCanaryResultSHA256).
    Replace("__COMPLETED_AT__", $completedAt).
    Replace("__PAYLOAD_JSON__", $payloadJson.Replace("'", "''")).
    Replace("__SSH_HELPER_SHA256__", $sshHelperSHA256).
    Replace("__REPO_SCRIPTS__", $repoScripts)
$runnerPath = Join-Path $outputPath "run-sms-phase5-observation-snapshot-$ChangeId.ps1"
$directoryCreated = $false; $fileCreated = $false
try {
    $null = New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop; $directoryCreated = $true
    [IO.File]::WriteAllText($runnerPath, $runnerText, (New-Object Text.UTF8Encoding($true))); $fileCreated = $true
    $tokens = $null; $errors = $null
    $null = [Management.Automation.Language.Parser]::ParseFile($runnerPath, [ref]$tokens, [ref]$errors)
    if (@($errors).Count -ne 0) { throw "runner PowerShell 语法校验失败" }
    foreach ($encoded in $payloads.Values) {
        $payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($encoded))
        foreach ($forbidden in @("sleep ", "kill ", "systemctl", "docker restart", "curl -X POST", "INSERT ", "UPDATE ", "DELETE ", "SMS_ENABLED=true", "scp ")) {
            if ($payload.Contains($forbidden)) { throw "观察负载包含禁止动作：$forbidden" }
        }
    }
    $closed = @(& $runnerPath); $checked = @(& $runnerPath -SelfTest)
    if ($closed -cnotcontains "observation_snapshot_execution_authorized=false" -or $checked -cnotcontains "observation_snapshot_runner_self_test=passed") { throw "runner 默认关闭或自测失败" }
    Write-Output "observation_snapshot_candidate=passed"
    Write-Output "change_id=$ChangeId"
    Write-Output "source_canary_change_id=$SourceCanaryChangeId"
    Write-Output "source_result_sha256=$ExpectedCanaryResultSHA256"
    Write-Output "runner_sha256=$((Get-FileHash -LiteralPath $runnerPath -Algorithm SHA256).Hash.ToLowerInvariant())"
    Write-Output "runner_path=$runnerPath"
    Write-Output "observation_windows=5m,15m,30m,2h,24h"
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
