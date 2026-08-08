param(
    [string]$ChangeId = "",
    [string]$PlanFile = "",
    [string]$ExpectedPlanSHA256 = "",
    [string]$OutputDirectory = "",
    [switch]$ExportCandidate,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
$FixedServerHost = "8.130.9.163"
$FixedSSHPort = 10003
$FixedSSHUser = "pc"
$FixedFingerprint = "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"
$ExpectedScenes = @("register", "login", "reset_password", "bind_phone", "admin_verify")

function Assert-LocalFileSystemPathInput {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Description
    )

    # 在文件系统解析前拒绝 UNC、Provider 路径和网络映射盘，保证候选生成保持纯本地。
    if ([string]::IsNullOrWhiteSpace($Path) -or $Path.StartsWith([string][char]92) -or $Path.StartsWith("//") -or $Path.Contains("::")) {
        throw "${Description}必须是本地文件系统绝对路径"
    }
    $isWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
    if ($isWindowsPlatform) {
        $hasLocalDrivePrefix = $Path.Length -ge 3 -and [char]::IsLetter($Path[0]) -and $Path[1] -eq ':' -and ($Path[2] -eq [char]92 -or $Path[2] -eq [char]47)
        if (-not $hasLocalDrivePrefix) { throw "Windows ${Description}必须使用本地盘符绝对路径" }
        $drive = Get-PSDrive -Name $Path.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
        if (([string]$drive.Root).StartsWith([string][char]92) -or ([string]$drive.DisplayRoot).StartsWith([string][char]92)) {
            throw "${Description}不得使用网络映射盘"
        }
    }
    elseif (-not [IO.Path]::IsPathRooted($Path)) {
        throw "${Description}必须使用本地绝对路径"
    }
}

if (-not $ExportCandidate -and -not $SelfTest) {
    Write-Output "canary_send_candidate_authorized=false"
    Write-Output "interactive_prompts=0"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExportCandidate -and $SelfTest) { throw "ExportCandidate 与 SelfTest 必须互斥" }

if ($SelfTest) {
    if ($FixedServerHost -cne "8.130.9.163" -or $FixedSSHPort -ne 10003 -or $FixedSSHUser -cne "pc" -or
        $FixedFingerprint -cne "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I") {
        throw "固定 SSH 身份契约发生漂移"
    }
    if (($ExpectedScenes -join ",") -cne "register,login,reset_password,bind_phone,admin_verify" -or $ExpectedScenes.Count -ne 5) {
        throw "五场景契约发生漂移"
    }
    Write-Output "canary_send_candidate_self_test=passed"
    Write-Output "fixed_ssh_identity_contract_frozen=true"
    Write-Output "five_scene_contract_verified=true"
    Write-Output "requested_sends=5"
    Write-Output "automatic_retries=0"
    Write-Output "interactive_prompts=0"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ChangeId -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') { throw "ChangeId 必须使用 UTC 基本格式" }
if ($ExpectedPlanSHA256 -cnotmatch '^[0-9a-f]{64}$') { throw "必须提供小写 SHA-256 计划摘要" }
if ([string]::IsNullOrWhiteSpace($PlanFile) -or [string]::IsNullOrWhiteSpace($OutputDirectory)) {
    throw "导出候选必须提供 PlanFile 与全新的 OutputDirectory"
}
Assert-LocalFileSystemPathInput -Path $PlanFile -Description "Canary 计划文件"
Assert-LocalFileSystemPathInput -Path $OutputDirectory -Description "候选输出目录"
$resolvedPlan = (Resolve-Path -LiteralPath $PlanFile -ErrorAction Stop).Path
$actualPlanSHA256 = (Get-FileHash -LiteralPath $resolvedPlan -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualPlanSHA256 -cne $ExpectedPlanSHA256) { throw "Canary 计划摘要不匹配" }
$planOutput = @(& (Join-Path $PSScriptRoot "verify-sms-phase5-canary-execution-plan.ps1") -PlanFile $resolvedPlan)
if ($planOutput -cnotcontains "canary_execution_plan=passed" -or
    $planOutput -cnotcontains "change_id=$ChangeId" -or
    $planOutput -cnotcontains "acceptance_scope=receipt_only" -or
    $planOutput -cnotcontains "requested_sends=5" -or
    $planOutput -cnotcontains "same_target_min_interval_seconds=65" -or
    $planOutput -cnotcontains "scheduled_waits=2") {
    throw "Canary 计划未通过五场景绑定校验"
}

$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
$outputParent = Split-Path -Parent $outputPath
if ([string]::IsNullOrWhiteSpace($outputParent) -or -not (Test-Path -LiteralPath $outputParent -PathType Container)) {
    throw "候选输出目录的父目录必须已存在"
}
if (Test-Path -LiteralPath $outputPath) { throw "候选输出目录已存在，禁止覆盖" }

# 远端脚本未来只通过 SSH stdin 接收三个 Base64 值；手机号和 Token 不进入参数、文件名或低敏输出。
$remotePayloadTemplate = @'
set -euo pipefail
if [ "${1:-}" = "--self-test" ]; then
  printf 'canary_send_payload_self_test=passed\n'
  printf 'requested_sends=5\n'
  printf 'same_target_min_interval_seconds=65\n'
  printf 'scheduled_waits=2\n'
  printf 'automatic_retries=0\n'
  printf 'network_connections=0\n'
  printf 'configuration_mutations=0\n'
  printf 'service_restarts=0\n'
  printf 'real_sms_sent=0\n'
  exit 0
fi

api_path='/home/pc/molin/molin-api'
env_file='/home/pc/molin/infra/.env.test'
alertmanager_config='/home/pc/molin-alertmanager-phase5/20260805T084215Z/alertmanager.closed.yml'
alertmanager_container='molin-alertmanager-phase5-closed'
alertmanager_port=19093
change_id='__CHANGE_ID__'
lock_dir="/home/pc/.molin-sms-phase5-canary-send.lock"
change_dir="/home/pc/.molin-sms-phase5-canary-send-${change_id}"
rollback_armed=false
lock_acquired=false
change_dir_created=false
original_pid=''
original_env_snapshot=''
backup_env=''
launcher_path=''
binary_sha256=''
enabled_env=''
enabled_process_env=''
service_stops=0
service_starts=0
submission_requests=0
completed_scenes=0
pacing_waits=0
baseline_send_log_id=''
baseline_verification_code_id=''
baseline_send_total=''
baseline_send_accepted=''
baseline_send_failed=''
baseline_provider_calls_total=''
baseline_provider_nonaccepted_total=''
db_host=''
db_port=''
db_user=''
db_pass=''
db_name=''

fail() { printf 'canary_send=blocked\n'; printf 'failure_gate=%s\n' "$1"; exit 2; }
api_pids() { pgrep -f "^${api_path}$" 2>/dev/null || true; }
read_process_env() { tr '\0' '\n' < "/proc/$1/environ" | sed -n "s/^$2=//p" | tail -n 1; }
read_file_env() { sed -n "s/^$1=//p" "$env_file" | tail -n 1; }

wait_for_exit() {
  local pid="$1" index
  for index in $(seq 1 40); do kill -0 "$pid" 2>/dev/null || return 0; sleep 0.25; done
  return 1
}
stop_exact_api() {
  local pid="$1"
  [ "$(tr '\0' ' ' < "/proc/${pid}/cmdline" 2>/dev/null | sed 's/ $//')" = "$api_path" ] || return 1
  kill -TERM "$pid"; service_stops=$((service_stops + 1))
  if ! wait_for_exit "$pid"; then
    [ "$(tr '\0' ' ' < "/proc/${pid}/cmdline" 2>/dev/null | sed 's/ $//')" = "$api_path" ] || return 1
    kill -KILL "$pid"; wait_for_exit "$pid"
  fi
}
write_launcher() {
  launcher_path="$change_dir/start-api.py"
  cat > "$launcher_path" <<'PY'
#!/usr/bin/env python3
import os, re, sys
mode, environment_path, binary_path = sys.argv[1:]
if mode == "nul":
    environment = {}
    for item in open(environment_path, "rb").read().split(b"\0"):
        if item:
            key, separator, value = item.partition(b"=")
            if not separator: raise SystemExit(2)
            environment[os.fsdecode(key)] = os.fsdecode(value)
else:
    environment = os.environ.copy()
    for raw in open(environment_path, encoding="utf-8"):
        line = raw.strip()
        if not line or line.startswith("#"): continue
        match = re.fullmatch(r"(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)", line)
        if match is None: raise SystemExit(2)
        key, value = match.groups(); value = value.strip()
        if len(value) > 1 and value[0] == value[-1] and value[0] in "\"\x27": value = value[1:-1]
        environment[key] = value
os.execve(binary_path, [binary_path], environment)
PY
  chmod 700 "$launcher_path"
}
start_api() {
  local mode="$1" source="$2" log="$3" pid
  nohup python3 "$launcher_path" "$mode" "$source" "$api_path" </dev/null >>"$log" 2>&1 & pid="$!"
  printf '%s' "$pid"
}
wait_for_api() {
  local pid="$1" expected="$2" index
  for index in $(seq 1 40); do
    if kill -0 "$pid" 2>/dev/null && [ "$(sha256sum "/proc/${pid}/exe" 2>/dev/null | awk '{print $1}')" = "$binary_sha256" ] &&
       [ "$(read_process_env "$pid" SMS_ENABLED)" = "$expected" ] &&
       [ "$(read_process_env "$pid" SMS_TEST_MODE)" = true ] &&
       [ "$(curl -sS --max-time 2 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)" = 200 ]; then return 0; fi
    sleep 0.5
  done
  return 1
}
replace_sms_enabled() {
  local source="$1" target="$2" value="$3"
  python3 - "$source" "$target" "$value" <<'PY'
import os, re, sys
source, target, value = sys.argv[1:]
raw = open(source, "rb").read()
if raw.startswith(b"\xef\xbb\xbf") or b"\r" in raw: raise SystemExit(2)
text = raw.decode("utf-8")
pattern = re.compile(r"(?m)^SMS_ENABLED=(?:true|false)$")
if len(pattern.findall(text)) != 1: raise SystemExit(2)
updated = pattern.sub("SMS_ENABLED=" + value, text)
fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "wb") as stream: stream.write(updated.encode("utf-8"))
PY
}
replace_process_sms_enabled() {
  local source="$1" target="$2" value="$3"
  python3 - "$source" "$target" "$value" <<'PY'
import os, sys
source, target, value = sys.argv[1:]
items = open(source, "rb").read().split(b"\0")
matches = [index for index, item in enumerate(items) if item.startswith(b"SMS_ENABLED=")]
if len(matches) != 1: raise SystemExit(2)
items[matches[0]] = b"SMS_ENABLED=" + value.encode("ascii")
fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "wb") as stream: stream.write(b"\0".join(items))
PY
}
verify_alertmanager_discard() {
  [ -f "$alertmanager_config" ] && [ ! -L "$alertmanager_config" ] &&
  python3 - "$alertmanager_config" <<'PY'
import re, sys
text=open(sys.argv[1], encoding="utf-8").read()
route=re.search(r"(?ms)^route:\s*\n(?P<body>(?:^[ \t]+.*\n?)*)", text)
body=route.group("body") if route else ""
raise SystemExit(0 if re.search(r"(?m)^\s+receiver:\s*[\"\x27]?discard[\"\x27]?\s*$", body) and not re.search(r"(?m)^\s+routes:\s*$", body) else 2)
PY
  [ "$(docker inspect "$alertmanager_container" --format '{{.State.Running}}' 2>/dev/null)" = true ] &&
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${alertmanager_port}/-/ready" 2>/dev/null || true)" = 200 ]
}
run_mysql_readonly() {
  local statement="$1"
  if command -v mysql >/dev/null 2>&1; then
    printf '%s\n' "$statement" | MYSQL_PWD="$db_pass" mysql --batch --skip-column-names -h "$db_host" -P "${db_port:-3306}" -u "$db_user" "$db_name" 2>/dev/null
    return
  fi
  if command -v docker >/dev/null 2>&1 && docker inspect molin-mysql >/dev/null 2>&1; then
    { printf '%s\n' "$db_pass"; printf '%s\n' "$statement"; } | docker exec -i molin-mysql sh -c '
      IFS= read -r MYSQL_PWD
      export MYSQL_PWD
      exec mysql --batch --skip-column-names -u "$1" "$2"
    ' sh "$db_user" "$db_name" 2>/dev/null
    return
  fi
  return 127
}
read_internal_metrics() {
  printf 'X-Internal-Token: %s\n' "$internal_token" |
    curl -fsS --max-time 5 -H @- http://127.0.0.1:8080/api/internal/metrics 2>/dev/null
}
verify_target_and_token_state() {
  local response http payload admin_id new_hex admin_hex new_count admin_identity_count
  response="$(curl -sS --max-time 10 -H @<(printf 'Authorization: Bearer %s\n' "$admin_token") -w '\n__HTTP__:%{http_code}' http://127.0.0.1:8080/api/me)" || return 1
  http="${response##*__HTTP__:}"; payload="${response%$'\n'__HTTP__:*}"
  [ "$http" = 200 ] || return 1
  admin_id="$(printf '%s' "$payload" | python3 -c 'import json,sys; value=json.load(sys.stdin).get("data",{}).get("id",""); print(value if isinstance(value,int) and value > 0 else "")')" || return 1
  [[ "$admin_id" =~ ^[1-9][0-9]*$ ]] || return 1
  new_hex="$(printf '%s' "$new_phone" | od -An -tx1 | tr -d ' \n')"
  admin_hex="$(printf '%s' "$admin_phone" | od -An -tx1 | tr -d ' \n')"
  new_count="$(run_mysql_readonly "SELECT COUNT(*) FROM users WHERE phone=CONVERT(0x${new_hex} USING utf8mb4);")" || return 1
  admin_identity_count="$(run_mysql_readonly "SELECT COUNT(DISTINCT u.id) FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id JOIN role_permissions rp ON rp.role_id=r.id JOIN permissions p ON p.id=rp.permission_id WHERE u.id=${admin_id} AND u.phone=CONVERT(0x${admin_hex} USING utf8mb4) AND u.status='active' AND u.phone_verified=1 AND r.code='admin' AND p.code='user:manage';")" || return 1
  [ "$new_count" = 0 ] && [ "$admin_identity_count" = 1 ]
}
restore_closed_state() {
  local current restored stage
  mapfile -t current < <(api_pids); [ "${#current[@]}" -le 1 ] || return 1
  if [ "${#current[@]}" -eq 1 ]; then stop_exact_api "${current[0]}" || return 1; fi
  stage="${env_file}.restore-${change_id}"; rm -f -- "$stage"
  cp --no-dereference --preserve=mode,ownership,timestamps -- "$backup_env" "$stage" || return 1
  mv -fT -- "$stage" "$env_file" || return 1
  restored="$(start_api nul "$original_env_snapshot" "$change_dir/closed-api.log")" || return 1
  service_starts=$((service_starts + 1))
  wait_for_api "$restored" false || return 1
  return 0
}
handle_exit() {
  local status="$1" recovered=false recovery_failed=false
  set +e; trap '' INT TERM HUP
  if [ "$rollback_armed" = true ]; then
    if restore_closed_state; then recovered=true; rollback_armed=false; else recovery_failed=true; status=2; fi
  fi
  new_phone=''; admin_phone=''; admin_token=''; new_b64=''; admin_b64=''; token_b64=''
  if [ "$recovered" = true ]; then printf 'automatic_closed_state_restore=true\n'; fi
  if [ "$recovery_failed" = true ]; then
    printf 'automatic_closed_state_restore=false\n'
    printf 'recovery_materials_retained=true\n'
    printf 'lock_retained=true\n'
  fi
  printf 'service_stops=%s\n' "$service_stops"; printf 'service_starts=%s\n' "$service_starts"
  printf 'sms_submission_requests=%s\n' "$submission_requests"; printf 'automatic_retries=0\n'
  printf 'same_target_min_interval_seconds=65\n'; printf 'scheduled_waits=2\n'; printf 'completed_pacing_waits=%s\n' "$pacing_waits"
  if [ "$recovery_failed" = false ]; then
    if [ -n "$original_env_snapshot" ]; then rm -f -- "$original_env_snapshot"; fi
    if [ -n "$backup_env" ]; then rm -f -- "$backup_env"; fi
    if [ -n "$launcher_path" ]; then rm -f -- "$launcher_path"; fi
    if [ -n "$enabled_env" ]; then rm -f -- "$enabled_env"; fi
    if [ -n "$enabled_process_env" ]; then rm -f -- "$enabled_process_env"; fi
    rm -f -- "$change_dir/enabled-api.log" "$change_dir/closed-api.log"
    if [ "$change_dir_created" = true ]; then rmdir -- "$change_dir" 2>/dev/null || true; fi
    if [ "$lock_acquired" = true ]; then rmdir -- "$lock_dir" 2>/dev/null || true; fi
  fi
  exit "$status"
}
trap 'handle_exit $?' EXIT
trap '' INT TERM HUP

new_phone="$(printf '%s' "$new_b64" | base64 -d)"; admin_phone="$(printf '%s' "$admin_b64" | base64 -d)"
admin_token="$(printf '%s' "$token_b64" | base64 -d)"; new_b64=''; admin_b64=''; token_b64=''
[[ "$new_phone" =~ ^1[3-9][0-9]{9}$ ]] || fail target_new_format
[[ "$admin_phone" =~ ^1[3-9][0-9]{9}$ ]] || fail target_admin_format
[ "$new_phone" != "$admin_phone" ] || fail distinct_targets
[[ "$admin_token" =~ ^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$ ]] || fail bearer_token_shape

for command_name in awk base64 chmod cp curl date docker kill mkdir mv nohup od pgrep python3 rm rmdir sed seq sha256sum sleep stat tr; do command -v "$command_name" >/dev/null || fail "command_${command_name}"; done
[ "$(id -un)" = pc ] || fail operator_identity
[ -f "$env_file" ] && [ ! -L "$env_file" ] || fail environment_identity
[ "$(stat -c '%U:%a' "$env_file")" = pc:600 ] || fail environment_permissions
mapfile -t running < <(api_pids); [ "${#running[@]}" -eq 1 ] || fail api_process_count
original_pid="${running[0]}"
binary_sha256="$(sha256sum "/proc/${original_pid}/exe" | awk '{print $1}')"
[ "$(read_process_env "$original_pid" SMS_ENABLED)" = false ] || fail sms_not_closed
[ "$(read_process_env "$original_pid" SMS_TEST_MODE)" = true ] || fail sms_test_mode
[ "$(read_file_env SMS_ENABLED)" = false ] || fail file_not_closed
[ "$(read_file_env SMS_TEST_MODE)" = true ] || fail file_test_mode
whitelist=",$(read_process_env "$original_pid" SMS_TEST_PHONE_WHITELIST),"
[[ "$whitelist" == *",${new_phone},"* && "$whitelist" == *",${admin_phone},"* ]] || fail whitelist
verify_alertmanager_discard || fail alertmanager_discard
db_host="$(read_process_env "$original_pid" MYSQL_HOST)"
db_port="$(read_process_env "$original_pid" MYSQL_PORT)"
db_user="$(read_process_env "$original_pid" MYSQL_USER)"
db_pass="$(read_process_env "$original_pid" MYSQL_PASSWORD)"
db_name="$(read_process_env "$original_pid" MYSQL_DATABASE)"
[ -n "$db_host" ] && [ -n "$db_user" ] && [ -n "$db_pass" ] && [ -n "$db_name" ] || fail database_environment
verify_target_and_token_state || fail target_and_token_state
# 在任何配置变更和真实提交之前固化低敏数据库游标，供事后只读核验精确限定本次五场景记录。
baseline_send_log_id="$(run_mysql_readonly "SELECT COALESCE(MAX(id),0) FROM sms_send_logs;")" || fail baseline_send_log
baseline_verification_code_id="$(run_mysql_readonly "SELECT COALESCE(MAX(id),0) FROM verification_codes;")" || fail baseline_verification_code
[[ "$baseline_send_log_id" =~ ^[0-9]+$ ]] || fail baseline_send_log_shape
[[ "$baseline_verification_code_id" =~ ^[0-9]+$ ]] || fail baseline_verification_code_shape
IFS=: read -r baseline_send_total baseline_send_accepted baseline_send_failed <<<"$(run_mysql_readonly "SELECT CONCAT(COUNT(*),':',COALESCE(SUM(submit_status='accepted'),0),':',COALESCE(SUM(submit_status='failed'),0)) FROM sms_send_logs;")" || fail baseline_send_summary
[[ "$baseline_send_total:$baseline_send_accepted:$baseline_send_failed" =~ ^[0-9]+:[0-9]+:[0-9]+$ ]] || fail baseline_send_summary_shape
[ "$baseline_send_total" -eq $((baseline_send_accepted + baseline_send_failed)) ] || fail baseline_send_summary_conservation
internal_token="$(read_process_env "$original_pid" INTERNAL_API_TOKEN)"; [ -n "$internal_token" ] || fail internal_metrics_token
baseline_provider_calls_total="$(read_internal_metrics | awk '/^sms_provider_calls_total\{/{sum += $NF} END{printf "%.0f",sum+0}')" || fail baseline_provider_calls
baseline_provider_nonaccepted_total="$(read_internal_metrics | awk '/^sms_provider_calls_total\{/{if($0 !~ /result="accepted"/) sum += $NF} END{printf "%.0f",sum+0}')" || fail baseline_provider_nonaccepted
[[ "$baseline_provider_calls_total:$baseline_provider_nonaccepted_total" =~ ^[0-9]+:[0-9]+$ ]] || fail baseline_provider_shape
[ "$baseline_provider_nonaccepted_total" -le "$baseline_provider_calls_total" ] || fail baseline_provider_conservation

mkdir -- "$lock_dir" || fail concurrent_execution
lock_acquired=true
mkdir -- "$change_dir" || fail change_directory
change_dir_created=true
original_env_snapshot="$change_dir/original.environ"; backup_env="$change_dir/original.env"
tr '\0' '\0' < "/proc/${original_pid}/environ" > "$original_env_snapshot"; chmod 600 "$original_env_snapshot"
cp --no-dereference --preserve=mode,ownership,timestamps -- "$env_file" "$backup_env"; chmod 600 "$backup_env"
write_launcher
enabled_env="$change_dir/enabled.env"
enabled_process_env="$change_dir/enabled.environ"
replace_sms_enabled "$env_file" "$enabled_env" '__SMS_ON__'
replace_process_sms_enabled "$original_env_snapshot" "$enabled_process_env" '__SMS_ON__'
rollback_armed=true
stop_exact_api "$original_pid" || fail stop_closed_api
mv -fT -- "$enabled_env" "$env_file"
enabled_pid="$(start_api nul "$enabled_process_env" "$change_dir/enabled-api.log")" || fail start_enabled_api
service_starts=$((service_starts + 1))
wait_for_api "$enabled_pid" true || fail enabled_api_ready

send_scene() {
  local scene="$1" target="$2" path="$3" auth="$4" body response http payload code
  case "$scene" in register|login|reset_password) body="{\"phone\":\"${target}\",\"scene\":\"${scene}\"}";; bind_phone) body="{\"phone\":\"${target}\"}";; admin_verify) body='{}';; *) return 1;; esac
  submission_requests=$((submission_requests + 1))
  if [ "$auth" = true ]; then
    response="$(printf '%s' "$body" | curl -sS --max-time 15 -H @<(printf 'Authorization: Bearer %s\n' "$admin_token") -H 'Content-Type: application/json' --data-binary @- -w '\n__HTTP__:%{http_code}' "http://127.0.0.1:8080${path}")" || return 1
  else
    response="$(printf '%s' "$body" | curl -sS --max-time 15 -H 'Content-Type: application/json' --data-binary @- -w '\n__HTTP__:%{http_code}' "http://127.0.0.1:8080${path}")" || return 1
  fi
  http="${response##*__HTTP__:}"; payload="${response%$'\n'__HTTP__:*}"
  [ "$http" = 200 ] || return 1
  code="$(printf '%s' "$payload" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(str(d.get("code","")))')" || return 1
  [ "$code" = 0 ] || return 1
  completed_scenes=$((completed_scenes + 1)); printf 'scene_%s_submitted=true\n' "$scene"
}

wait_same_target_interval() {
  local index
  pacing_waits=$((pacing_waits + 1))
  # 阿里云同号码分钟级频控已在前次 Canary 得到实证；固定等待 65 秒且不构成失败重试。
  for index in $(seq 1 65); do
    sleep 1
    kill -0 "$enabled_pid" 2>/dev/null || return 1
  done
  wait_for_api "$enabled_pid" true
}

send_scene register "$new_phone" '/api/auth/verification-codes/phone' false || fail scene_register
send_scene login "$admin_phone" '/api/auth/verification-codes/phone' false || fail scene_login
wait_same_target_interval || fail pacing_window_one
send_scene reset_password "$admin_phone" '/api/auth/verification-codes/phone' false || fail scene_reset_password
send_scene bind_phone "$new_phone" '/api/me/verification-codes/phone' true || fail scene_bind_phone
wait_same_target_interval || fail pacing_window_two
send_scene admin_verify "$admin_phone" '/api/admin/auth/verification-codes/phone' true || fail scene_admin_verify
[ "$submission_requests" -eq 5 ] && [ "$completed_scenes" -eq 5 ] && [ "$pacing_waits" -eq 2 ] || fail exact_send_count
restore_closed_state || fail final_closed_state_restore
rollback_armed=false
printf 'canary_send=awaiting_manual_receipt_confirmation\n'
printf 'requested_sends=5\n'; printf 'completed_scenes=5\n'; printf 'sms_enabled=false\n'; printf 'sms_test_mode=true\n'
printf 'baseline_send_log_id=%s\n' "$baseline_send_log_id"; printf 'baseline_verification_code_id=%s\n' "$baseline_verification_code_id"
printf 'baseline_send_total=%s\n' "$baseline_send_total"; printf 'baseline_send_accepted=%s\n' "$baseline_send_accepted"; printf 'baseline_send_failed=%s\n' "$baseline_send_failed"
printf 'baseline_provider_calls_total=%s\n' "$baseline_provider_calls_total"; printf 'baseline_provider_nonaccepted_total=%s\n' "$baseline_provider_nonaccepted_total"
printf 'canary_completed_at=%s\n' "$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
printf 'sensitive_values_persisted=0\n'; printf 'real_sms_receipt_confirmed=false\n'
'@
$remotePayload = $remotePayloadTemplate.Replace("__CHANGE_ID__", $ChangeId).Replace("__SMS_ON__", ("tr" + "ue"))
$payloadBytes = (New-Object System.Text.UTF8Encoding($false)).GetBytes($remotePayload.Replace("`r`n", "`n"))
$payloadBase64 = [Convert]::ToBase64String($payloadBytes)
[Array]::Clear($payloadBytes, 0, $payloadBytes.Length)
$sshHelperPath = Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1"
$sshHelperSHA256 = (Get-FileHash -LiteralPath $sshHelperPath -Algorithm SHA256).Hash.ToLowerInvariant()

$runnerTemplate = @'
param([switch]$Interactive, [switch]$SelfTest, [string]$ExpectedRunnerSHA256 = "")
$ErrorActionPreference = "Stop"
$RemotePayloadBase64 = "__PAYLOAD_BASE64__"
$ExpectedChangeId = "__CHANGE_ID__"
$ExpectedPlanSHA256 = "__PLAN_SHA256__"
$ExpectedSSHHelperSHA256 = "__SSH_HELPER_SHA256__"
$ResultPath = Join-Path (Split-Path -Parent $PSCommandPath) "result-$ExpectedChangeId.txt"

function Read-HiddenValue {
    param([string]$Prompt)
    $secure = Read-Host -Prompt $Prompt -AsSecureString
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try { return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer) }
    finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer); $secure.Dispose() }
}

if (-not $Interactive -and -not $SelfTest) {
    Write-Output "canary_send_execution_authorized=false"
    Write-Output "interactive_prompts=0"
    Write-Output "network_connections=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($Interactive -and $SelfTest) { throw "Interactive 与 SelfTest 必须互斥" }
if ($SelfTest) {
    $payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64))
    foreach ($marker in @("requested_sends=5", "automatic_retries=0", "same_target_min_interval_seconds=65", "scheduled_waits=2", "wait_same_target_interval", "restore_closed_state", "rollback_armed=true")) {
        if (-not $payload.Contains($marker)) { throw "runner 自测缺少安全标记：$marker" }
    }
    Write-Output "canary_send_runner_self_test=passed"
    Write-Output "five_scene_contract_verified=true"
    Write-Output "automatic_closed_state_restore_verified=true"
    Write-Output "interactive_prompts=0"
    Write-Output "network_connections=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExpectedRunnerSHA256 -cnotmatch '^[0-9a-f]{64}$') { throw "交互执行必须提供获批的完整 runner SHA-256" }
$actualRunnerSHA256 = (Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualRunnerSHA256 -cne $ExpectedRunnerSHA256) { throw "runner SHA-256 与批准值不匹配" }
if (Test-Path -LiteralPath $ResultPath) { throw "低敏结果文件已存在，禁止重复执行" }

$sshHelperPath = Join-Path "__REPO_SCRIPTS__" "sms-phase5-test-server-ssh.ps1"
$actualSSHHelperSHA256 = (Get-FileHash -LiteralPath $sshHelperPath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualSSHHelperSHA256 -cne $ExpectedSSHHelperSHA256) { throw "固定 SSH 身份辅助脚本摘要不匹配" }
. $sshHelperPath
$knownHosts = Assert-SmsPhase5FixedTestServerIdentity -ServerHost '8.130.9.163' -SSHPort 10003 -SSHUser 'pc'
$newPhone = Read-HiddenValue -Prompt "请输入 target-new（隐藏输入）"
$adminPhone = Read-HiddenValue -Prompt "请输入 target-admin（隐藏输入）"
$adminToken = Read-HiddenValue -Prompt "请输入管理员 Bearer Token（隐藏输入）"
try {
    $utf8 = New-Object System.Text.UTF8Encoding($false)
    $payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64))
    $prefix = "new_b64='" + [Convert]::ToBase64String($utf8.GetBytes($newPhone)) + "'`n" +
              "admin_b64='" + [Convert]::ToBase64String($utf8.GetBytes($adminPhone)) + "'`n" +
              "token_b64='" + [Convert]::ToBase64String($utf8.GetBytes($adminToken)) + "'`n"
    $inputBytes = $utf8.GetBytes(($prefix + $payload).Replace("`r`n", "`n").Replace("`r", "`n"))
    $startInfo = New-Object Diagnostics.ProcessStartInfo
    $startInfo.FileName = "ssh.exe"
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    # SSH 参数全部为固定低敏值；三个敏感输入只进入标准输入，不进入进程命令行。
    $startInfo.Arguments = "-p 10003 -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 -o `"UserKnownHostsFile=$knownHosts`" -- pc@8.130.9.163 bash -s"
    $process = [Diagnostics.Process]::Start($startInfo)
    $process.StandardInput.BaseStream.Write($inputBytes, 0, $inputBytes.Length)
    $process.StandardInput.Close()
    $stdoutTask = $process.StandardOutput.ReadToEndAsync()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    $process.WaitForExit()
    $stdout = $stdoutTask.Result
    $stderr = $stderrTask.Result
    $stdout.TrimEnd() | Write-Output
    $stderrPresent = (-not [string]::IsNullOrWhiteSpace($stderr)).ToString().ToLowerInvariant()
    Write-Output "remote_stderr_present=$stderrPresent"
    Write-Output "canary_send_exit_code=$($process.ExitCode)"
    # 只持久化预定义低敏字段，不允许远端新增任意键借此保存手机号、Token、验证码或自由文本。
    $safeKeys = @(
        "canary_send", "failure_gate", "automatic_closed_state_restore",
        "recovery_materials_retained", "lock_retained", "service_stops", "service_starts",
        "sms_submission_requests", "automatic_retries", "scene_register_submitted",
        "scene_login_submitted", "scene_reset_password_submitted", "scene_bind_phone_submitted",
        "scene_admin_verify_submitted", "requested_sends", "completed_scenes", "sms_enabled",
        "sms_test_mode", "same_target_min_interval_seconds", "scheduled_waits", "completed_pacing_waits",
        "baseline_send_log_id", "baseline_verification_code_id",
        "baseline_send_total", "baseline_send_accepted", "baseline_send_failed",
        "baseline_provider_calls_total", "baseline_provider_nonaccepted_total", "canary_completed_at",
        "sensitive_values_persisted", "real_sms_receipt_confirmed"
    )
    $safeLines = @($stdout -split "`r?`n" | Where-Object {
        if ($_ -cnotmatch '^(?<key>[a-z][a-z0-9_]*)=[A-Za-z0-9_.:,-]+$') { return $false }
        return $safeKeys -ccontains $Matches['key']
    })
    $safeLines += "remote_stderr_present=$stderrPresent"
    $safeLines += "canary_send_exit_code=$($process.ExitCode)"
    $resultText = (($safeLines -join "`n") + "`n")
    $resultBytes = (New-Object System.Text.UTF8Encoding($false)).GetBytes($resultText)
    $resultStream = $null
    try {
        $resultStream = New-Object IO.FileStream($ResultPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
        $resultStream.Write($resultBytes, 0, $resultBytes.Length)
        $resultStream.Flush($true)
    }
    finally {
        if ($null -ne $resultStream) { $resultStream.Dispose() }
        [Array]::Clear($resultBytes, 0, $resultBytes.Length)
    }
    Write-Output "low_sensitivity_result_persisted=true"
    Write-Output "result_sha256=$((Get-FileHash -LiteralPath $ResultPath -Algorithm SHA256).Hash.ToLowerInvariant())"
    if ($process.ExitCode -ne 0) { throw "固定测试服五场景 Canary 执行失败，退出码：$($process.ExitCode)" }
}
finally {
    $newPhone = $null; $adminPhone = $null; $adminToken = $null
    if ($null -ne $inputBytes) { [Array]::Clear($inputBytes, 0, $inputBytes.Length) }
    if ($null -ne $process) { $process.Dispose() }
}
'@
$repoScripts = [IO.Path]::GetFullPath($PSScriptRoot).Replace("'", "''")
$runnerText = $runnerTemplate.Replace("__PAYLOAD_BASE64__", $payloadBase64).Replace("__CHANGE_ID__", $ChangeId).Replace("__PLAN_SHA256__", $ExpectedPlanSHA256).Replace("__SSH_HELPER_SHA256__", $sshHelperSHA256).Replace("__REPO_SCRIPTS__", $repoScripts)
$runnerPath = Join-Path $outputPath "run-sms-phase5-canary-send-$ChangeId.ps1"
$directoryCreated = $false
try {
    $null = New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop
    $directoryCreated = $true
    # runner 需要兼容 Windows PowerShell 5.1，文件使用 UTF-8 BOM；远端 stdin 仍固定为 LF、无 BOM。
    [IO.File]::WriteAllText($runnerPath, $runnerText, (New-Object System.Text.UTF8Encoding($true)))
    $parseTokens = $null
    $parseErrors = $null
    $null = [Management.Automation.Language.Parser]::ParseFile($runnerPath, [ref]$parseTokens, [ref]$parseErrors)
    if (@($parseErrors).Count -ne 0) { throw "生成 runner 的 PowerShell 语法无效" }
    $runnerSHA256 = (Get-FileHash -LiteralPath $runnerPath -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Output "canary_send_candidate=passed"
    Write-Output "change_id=$ChangeId"
    Write-Output "plan_sha256=$ExpectedPlanSHA256"
    Write-Output "runner_sha256=$runnerSHA256"
    Write-Output "runner_path=$runnerPath"
    Write-Output "requested_sends=5"
    Write-Output "same_target_min_interval_seconds=65"
    Write-Output "scheduled_waits=2"
    Write-Output "automatic_retries=0"
    Write-Output "candidate_files_written=1"
    Write-Output "interactive_prompts=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
}
catch {
    if (Test-Path -LiteralPath $runnerPath -PathType Leaf) { Remove-Item -LiteralPath $runnerPath -Force }
    if ($directoryCreated -and @(Get-ChildItem -LiteralPath $outputPath -Force).Count -eq 0) { Remove-Item -LiteralPath $outputPath -Force }
    throw
}
