param(
    [string]$ChangeId = "",
    [string]$OutputDirectory = "",
    [switch]$ExportCandidate,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
$FixedServerHost = "8.130.9.163"
$FixedSSHPort = 10003
$FixedSSHUser = "pc"
$FixedFingerprint = "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"

function Assert-LocalFileSystemPathInput {
    param([Parameter(Mandatory = $true)][string]$Path)

    # 在任何路径解析前拒绝 UNC、Provider 路径和网络映射盘，确保本地候选生成不会意外联网。
    if ([string]::IsNullOrWhiteSpace($Path) -or $Path -match '^(?:\\\\|//)' -or $Path.Contains("::")) {
        throw "候选输出目录必须是本地文件系统绝对路径"
    }
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        if ($Path -cnotmatch '^[A-Za-z]:[\\/]') { throw "Windows 候选目录必须使用本地盘符绝对路径" }
        $drive = Get-PSDrive -Name $Path.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
        if (([string]$drive.Root).StartsWith("\\") -or ([string]$drive.DisplayRoot).StartsWith("\\")) {
            throw "候选目录不得使用网络映射盘"
        }
    }
    elseif (-not [IO.Path]::IsPathRooted($Path)) {
        throw "候选目录必须使用本地绝对路径"
    }
}

function Assert-SyntheticTargetPair {
    param([string]$NewTarget, [string]$AdminTarget)
    if ($NewTarget -cnotmatch '^1[3-9]\d{9}$' -or $AdminTarget -cnotmatch '^1[3-9]\d{9}$' -or
        $NewTarget -ceq $AdminTarget) {
        throw "合成目标对无效"
    }
}

if (-not $ExportCandidate -and -not $SelfTest) {
    Write-Output "whitelist_change_candidate_authorized=false"
    Write-Output "candidate_files_written=0"
    Write-Output "interactive_prompts=0"
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
    Assert-SyntheticTargetPair -NewTarget ("1" + "38" + ("0" * 8)) -AdminTarget ("1" + "39" + ("0" * 8))
    Write-Output "whitelist_change_candidate_self_test=passed"
    Write-Output "fixed_ssh_identity_contract_frozen=true"
    Write-Output "hidden_target_contract_verified=true"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ChangeId -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') { throw "ChangeId 必须使用 UTC 基本格式" }
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) { throw "导出候选必须提供全新的输出目录" }
Assert-LocalFileSystemPathInput -Path $OutputDirectory
$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
$outputParent = Split-Path -Parent $outputPath
if ([string]::IsNullOrWhiteSpace($outputParent) -or -not (Test-Path -LiteralPath $outputParent -PathType Container)) {
    throw "候选输出目录的父目录必须已存在"
}
if (Test-Path -LiteralPath $outputPath) { throw "候选输出目录已存在，禁止覆盖" }

# 远端负载只在未来独立执行授权下运行；本轮仅把它冻结为本地候选，不建立任何网络连接。
$remotePayload = @'
set -Eeuo pipefail
umask 077

change_id='__CHANGE_ID__'
api_path='/home/pc/molin/molin-api'
env_file='/home/pc/molin/infra/.env.test'
rollback_root='/home/pc/molin/rollback/sms-phase5/whitelist-changes'
change_dir="$rollback_root/change-$change_id"
lock_dir='/home/pc/molin/rollback/sms-phase5/whitelist-change.lock'
prometheus_port=19090
alertmanager_port=19093
alertmanager_config='/home/pc/molin-alertmanager-phase5/20260805T084215Z/alertmanager.closed.yml'
alertmanager_container='molin-alertmanager-phase5-closed'

rollback_armed=false
mutation_started=false
original_pid=''
new_pid=''
binary_sha256=''
backup_env=''
original_env_snapshot=''
candidate_env=''
launcher_path=''
helper_path=''
stage=''
send_before=''
provider_before=''
notification_before=''
failure_stage='none'
lock_acquired=false
change_dir_created=false
change_dir_verified=false

fail() {
  failure_stage="$1"
  printf 'whitelist_change=failed\n'
  printf 'failure_stage=%s\n' "$failure_stage"
  exit 2
}

api_pids() {
  pgrep -f "^${api_path}$" 2>/dev/null || true
}

read_process_env() {
  local pid="$1"
  local key="$2"
  tr '\0' '\n' < "/proc/${pid}/environ" | awk -F= -v wanted="$key" '$1 == wanted {sub(/^[^=]*=/, ""); print; exit}'
}

read_file_env() {
  local key="$1"
  python3 -c '
import re, sys
path, wanted = sys.argv[1:]
values = {}
for raw in open(path, encoding="utf-8", newline=""):
    line = raw.strip()
    if not line or line.startswith("#"):
        continue
    match = re.fullmatch(r"(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)", line)
    if match is None or match.group(1) in values:
        raise SystemExit(2)
    key, value = match.groups()
    value = value.strip()
    if len(value) not in (0, 1) and value[0] == value[-1] and value[0] in "\"\x27":
        value = value[1:-1]
    values[key] = value
print(values.get(wanted, ""))
' "$env_file" "$key"
}

whitelist_state() {
  local value="$1"
  printf '%s\n%s\n%s\n' "$value" "$new_phone" "$admin_phone" | python3 -c '
import sys
value, new_phone, admin_phone = [line.rstrip("\n") for line in sys.stdin.readlines()]
items = [item.strip() for item in value.split(",") if item.strip()]
if len(items) != len(set(items)):
    print("invalid")
elif items == [new_phone]:
    print("new_only")
elif len(items) == 2 and set(items) == {new_phone, admin_phone}:
    print("both")
else:
    print("other")
'
}

wait_for_exit() {
  local pid="$1"
  local index
  for index in $(seq 1 40); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.25
  done
  return 1
}

stop_exact_api() {
  local pid="$1"
  [ -r "/proc/${pid}/cmdline" ] || return 0
  [ "$(tr '\0' ' ' < "/proc/${pid}/cmdline" | sed 's/ $//')" = "$api_path" ] || return 1
  kill -TERM "$pid"
  if ! wait_for_exit "$pid"; then
    # 仅在再次核验同一 PID 身份后才允许 KILL，避免影响任何其他进程。
    [ "$(tr '\0' ' ' < "/proc/${pid}/cmdline" 2>/dev/null | sed 's/ $//')" = "$api_path" ] || return 1
    kill -KILL "$pid"
    wait_for_exit "$pid"
  fi
}

write_launcher() {
  launcher_path="$change_dir/start-api.py"
  [ ! -e "$launcher_path" ] || return 1
  cat > "$launcher_path" <<'PY'
#!/usr/bin/env python3
import os
import re
import sys


def load_text_environment(path):
    values = {}
    with open(path, "r", encoding="utf-8", newline="") as stream:
        for raw in stream:
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            match = re.fullmatch(r"(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)", line)
            if match is None or match.group(1) in values:
                raise SystemExit(2)
            key, value = match.groups()
            value = value.strip()
            if len(value) not in (0, 1) and value[0] == value[-1] and value[0] in "\"\x27":
                value = value[1:-1]
            values[key] = value
    return values


mode, environment_path, binary_path = sys.argv[1:]
if mode == "text":
    environment = os.environ.copy()
    environment.update(load_text_environment(environment_path))
elif mode == "nul":
    environment = {}
    for item in open(environment_path, "rb").read().split(b"\0"):
        if not item:
            continue
        key, separator, value = item.partition(b"=")
        if not separator or not key:
            raise SystemExit(2)
        environment[os.fsdecode(key)] = os.fsdecode(value)
else:
    raise SystemExit(2)
os.execve(binary_path, [binary_path], environment)
PY
  chmod 700 "$launcher_path"
}

start_api() {
  local mode="$1"
  local environment_path="$2"
  local log_path="$3"
  nohup python3 "$launcher_path" "$mode" "$environment_path" "$api_path" </dev/null >>"$log_path" 2>&1 &
  printf '%s' "$!"
}

wait_for_api() {
  local expected_pid="$1"
  local index
  for index in $(seq 1 40); do
    if kill -0 "$expected_pid" 2>/dev/null &&
       [ "$(sha256sum "/proc/${expected_pid}/exe" 2>/dev/null | awk '{print $1}')" = "$binary_sha256" ] &&
       [ "$(curl -sS --max-time 2 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health 2>/dev/null || true)" = 200 ] &&
       [ "$(curl -sS --max-time 2 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)" = 200 ]; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

verify_api_state() {
  local expected_pid="$1"
  local expected_whitelist_state="$2"
  mapfile -t running < <(api_pids)
  [ "${#running[@]}" -eq 1 ] && [ "${running[0]}" = "$expected_pid" ]
  [ "$(sha256sum "/proc/${expected_pid}/exe" | awk '{print $1}')" = "$binary_sha256" ]
  [ "$(read_process_env "$expected_pid" SMS_ENABLED)" = false ]
  [ "$(read_process_env "$expected_pid" SMS_TEST_MODE)" = true ]
  [ "$(whitelist_state "$(read_process_env "$expected_pid" SMS_TEST_PHONE_WHITELIST)")" = "$expected_whitelist_state" ]
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health 2>/dev/null || true)" = 200 ]
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)" = 200 ]
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/version 2>/dev/null || true)" = 200 ]
}

run_mysql() {
  local statement="$1"
  if command -v mysql >/dev/null 2>&1; then
    # SQL 只经标准输入交给固定参数的客户端，避免号码或密码进入进程命令行。
    printf '%s\n' "$statement" |
      MYSQL_PWD="$db_pass" mysql --batch --skip-column-names -h "$db_host" -P "${db_port:-3306}" -u "$db_user" "$db_name" 2>/dev/null
    return
  fi
  {
    printf '%s\n' "$db_pass"
    printf '%s\n' "$statement"
  } | docker exec -i molin-mysql sh -c '
    IFS= read -r MYSQL_PWD
    export MYSQL_PWD
    exec mysql --batch --skip-column-names -u "$1" "$2"
  ' sh "$db_user" "$db_name" 2>/dev/null
}

send_summary() {
  run_mysql "SELECT CONCAT(COUNT(*),':',COALESCE(SUM(submit_status='accepted'),0),':',COALESCE(SUM(submit_status='failed'),0)) FROM sms_send_logs"
}

read_internal_metrics() {
  printf 'X-Internal-Token: %s\n' "$internal_token" |
    curl -fsS --max-time 5 -H @- http://127.0.0.1:8080/api/internal/metrics 2>/dev/null
}

provider_total() {
  read_internal_metrics | awk '/^sms_provider_calls_total\{/{sum += $NF} END{printf "%.0f", sum + 0}'
}

notification_snapshot() {
  curl -fsS --max-time 5 "http://127.0.0.1:${alertmanager_port}/metrics" | awk '
    /^alertmanager_notifications_total({| )/ {notification += $NF}
    /^alertmanager_notifications_failed_total({| )/ {failed += $NF}
    END {printf "%.0f:%.0f", notification+0, failed+0}
  '
}

alert_snapshot() {
  local alertmanager_active prometheus_sms_active
  alertmanager_active="$(curl -fsS --max-time 5 "http://127.0.0.1:${alertmanager_port}/api/v2/alerts" | python3 -c 'import json,sys; value=json.load(sys.stdin); print(len(value) if isinstance(value,list) else -1)')"
  prometheus_sms_active="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/alerts" | python3 -c 'import json,sys; value=json.load(sys.stdin).get("data",{}).get("alerts",[]); print(sum(1 for item in value if str(item.get("labels",{}).get("alertname","")).startswith("MolinSMS")))')"
  printf '%s:%s' "$alertmanager_active" "$prometheus_sms_active"
}

verify_alertmanager_discard() {
  [ -f "$alertmanager_config" ] && [ ! -L "$alertmanager_config" ]
  python3 - "$alertmanager_config" <<'PY'
import re
import sys

text = open(sys.argv[1], encoding="utf-8").read()
route = re.search(r"(?ms)^route:\s*\n(?P<body>(?:^[ \t]+.*\n?)*)", text)
body = route.group("body") if route else ""
if not re.search(r"(?m)^\s+receiver:\s*[\"\x27]?discard[\"\x27]?\s*$", body):
    raise SystemExit(2)
if re.search(r"(?m)^\s+routes:\s*$", body):
    raise SystemExit(2)
PY
  [ "$(docker inspect "$alertmanager_container" --format '{{.State.Running}}')" = true ]
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${alertmanager_port}/-/ready")" = 200 ]
}

write_candidate_helper() {
  helper_path="$change_dir/build-candidate.py"
  [ ! -e "$helper_path" ] || return 1
  cat > "$helper_path" <<'PY'
#!/usr/bin/env python3
import os
import re
import sys

source_path, target_path = sys.argv[1:]
new_phone = sys.stdin.readline().rstrip("\n")
admin_phone = sys.stdin.readline().rstrip("\n")
if not re.fullmatch(r"1[3-9][0-9]{9}", new_phone) or not re.fullmatch(r"1[3-9][0-9]{9}", admin_phone) or new_phone == admin_phone:
    raise SystemExit(2)
raw = open(source_path, "rb").read()
if raw.startswith(b"\xef\xbb\xbf") or b"\r" in raw:
    raise SystemExit(2)
text = raw.decode("utf-8")
lines = text.splitlines(keepends=True)
values = {}
positions = {}
for index, raw_line in enumerate(lines):
    line = raw_line.strip()
    if not line or line.startswith("#"):
        continue
    match = re.fullmatch(r"(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)", line)
    if match is None or match.group(1) in values:
        raise SystemExit(2)
    key, value = match.groups()
    value = value.strip()
    if len(value) not in (0, 1) and value[0] == value[-1] and value[0] in "\"\x27":
        value = value[1:-1]
    values[key] = value
    positions[key] = index
if values.get("APP_ENV") != "test" or values.get("SMS_ENABLED") != "false" or values.get("SMS_TEST_MODE") != "true":
    raise SystemExit(2)
items = [item.strip() for item in values.get("SMS_TEST_PHONE_WHITELIST", "").split(",") if item.strip()]
if items != [new_phone]:
    raise SystemExit(2)
lines[positions["SMS_TEST_PHONE_WHITELIST"]] = f"SMS_TEST_PHONE_WHITELIST={new_phone},{admin_phone}\n"
flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
descriptor = os.open(target_path, flags, 0o600)
with os.fdopen(descriptor, "wb") as stream:
    stream.write("".join(lines).encode("utf-8"))
PY
  chmod 700 "$helper_path"
}

preflight_checks() {
  local process_whitelist file_whitelist new_hex admin_hex new_count admin_count role_count permission_count
  for command_name in awk base64 cat chmod cp curl docker grep id kill mkdir mktemp mv nohup od pgrep python3 rm rmdir sed seq sha256sum sleep stat tr; do
    command -v "$command_name" >/dev/null || fail "command_${command_name}"
  done
  [ "$(id -un)" = pc ] || fail operator_identity
  [ -f "$env_file" ] && [ ! -L "$env_file" ] || fail environment_identity
  [ "$(stat -c '%U:%a' "$env_file")" = pc:600 ] || fail environment_permissions
  mapfile -t running < <(api_pids)
  [ "${#running[@]}" -eq 1 ] || fail api_process_count
  original_pid="${running[0]}"
  binary_sha256="$(sha256sum "/proc/${original_pid}/exe" | awk '{print $1}')"
  [ "$(read_process_env "$original_pid" SMS_ENABLED)" = false ] || fail sms_enabled
  [ "$(read_process_env "$original_pid" SMS_TEST_MODE)" = true ] || fail sms_test_mode
  process_whitelist="$(read_process_env "$original_pid" SMS_TEST_PHONE_WHITELIST)"
  file_whitelist="$(read_file_env SMS_TEST_PHONE_WHITELIST)"
  [ "$(whitelist_state "$process_whitelist")" = new_only ] || fail process_whitelist
  [ "$(whitelist_state "$file_whitelist")" = new_only ] || fail file_whitelist
  [ "$(read_file_env SMS_ENABLED)" = false ] || fail file_sms_enabled
  [ "$(read_file_env SMS_TEST_MODE)" = true ] || fail file_sms_test_mode
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health 2>/dev/null || true)" = 200 ] || fail health
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)" = 200 ] || fail ready

  db_host="$(read_process_env "$original_pid" MYSQL_HOST)"
  db_port="$(read_process_env "$original_pid" MYSQL_PORT)"
  db_user="$(read_process_env "$original_pid" MYSQL_USER)"
  db_pass="$(read_process_env "$original_pid" MYSQL_PASSWORD)"
  db_name="$(read_process_env "$original_pid" MYSQL_DATABASE)"
  internal_token="$(read_process_env "$original_pid" INTERNAL_API_TOKEN)"
  [ -n "$db_host" ] && [ -n "$db_user" ] && [ -n "$db_pass" ] && [ -n "$db_name" ] && [ -n "$internal_token" ] || fail runtime_dependencies
  new_hex="$(printf '%s' "$new_phone" | od -An -tx1 | tr -d ' \n')"
  admin_hex="$(printf '%s' "$admin_phone" | od -An -tx1 | tr -d ' \n')"
  new_count="$(run_mysql "SELECT COUNT(*) FROM users WHERE phone=CONVERT(0x${new_hex} USING utf8mb4);")"
  admin_count="$(run_mysql "SELECT COUNT(*) FROM users WHERE phone=CONVERT(0x${admin_hex} USING utf8mb4) AND status='active' AND phone_verified=1;")"
  role_count="$(run_mysql "SELECT COUNT(DISTINCT u.id) FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.phone=CONVERT(0x${admin_hex} USING utf8mb4) AND u.status='active' AND u.phone_verified=1 AND r.code='admin';")"
  permission_count="$(run_mysql "SELECT COUNT(DISTINCT u.id) FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id JOIN role_permissions rp ON rp.role_id=r.id JOIN permissions p ON p.id=rp.permission_id WHERE u.phone=CONVERT(0x${admin_hex} USING utf8mb4) AND r.code='admin' AND p.code='user:manage';")"
  [ "$new_count" = 0 ] && [ "$admin_count" = 1 ] && [ "$role_count" = 1 ] && [ "$permission_count" = 1 ] || fail target_state
  send_before="$(send_summary)"
  provider_before="$(provider_total)"
  verify_alertmanager_discard || fail alertmanager_discard
  [ "$(alert_snapshot)" = 0:0 ] || fail active_alerts
  notification_before="$(notification_snapshot)"
}

restore_original() {
  local current_pid restored_pid stage
  mapfile -t running < <(api_pids)
  [ "${#running[@]}" -le 1 ] || return 1
  if [ "${#running[@]}" -eq 1 ]; then
    current_pid="${running[0]}"
    stop_exact_api "$current_pid" || return 1
  fi
  stage="${env_file}.rollback-${change_id}"
  [ ! -e "$stage" ] || return 1
  cp --no-dereference --preserve=mode,ownership,timestamps -- "$backup_env" "$stage" || return 1
  chmod 600 "$stage" || return 1
  mv -fT -- "$stage" "$env_file" || return 1
  [ "$(sha256sum "$env_file" | awk '{print $1}')" = "$(sha256sum "$backup_env" | awk '{print $1}')" ] || return 1
  restored_pid="$(start_api nul "$original_env_snapshot" "$change_dir/rollback-api.log")" || return 1
  wait_for_api "$restored_pid" || return 1
  verify_api_state "$restored_pid" new_only || return 1
  return 0
}

handle_exit() {
  local status="$1"
  local recovered=false
  set +e
  trap '' INT TERM HUP
  if [ "$status" -ne 0 ] && [ "$rollback_armed" = true ]; then
    if restore_original; then
      recovered=true
      rollback_armed=false
      rm -f -- "$original_env_snapshot"
    fi
  fi
  new_phone=''
  admin_phone=''
  new_b64=''
  admin_b64=''
  db_pass=''
  internal_token=''
  if [ -n "$candidate_env" ]; then rm -f -- "$candidate_env"; fi
  if [ -n "$stage" ]; then rm -f -- "$stage"; fi
  if [ -n "$helper_path" ]; then rm -f -- "$helper_path"; fi
  if [ -n "$launcher_path" ]; then rm -f -- "$launcher_path"; fi
  if [ "$lock_acquired" = true ] && [ -d "$lock_dir" ] && [ ! -L "$lock_dir" ] &&
     [ "$(stat -c '%U:%a' "$lock_dir" 2>/dev/null)" = pc:700 ]; then
    rmdir -- "$lock_dir"
    lock_acquired=false
  fi
  if [ "$change_dir_verified" = true ] && [ -d "$change_dir" ] && [ ! -L "$change_dir" ]; then
    {
      printf 'exit_status=%s\n' "$status"
      printf 'mutation_started=%s\n' "$mutation_started"
      printf 'automatic_rollback_succeeded=%s\n' "$recovered"
      printf 'sensitive_environment_snapshot_retained=%s\n' "$([ -n "$original_env_snapshot" ] && [ -f "$original_env_snapshot" ] && printf true || printf false)"
    } >> "$change_dir/exit-evidence.txt"
    chmod 600 "$change_dir/exit-evidence.txt"
  elif [ "$change_dir_created" = true ]; then
    # 未完成身份核验的本次新目录只允许在为空时移除，绝不写入同名历史证据。
    rmdir -- "$change_dir" 2>/dev/null || true
  fi
  trap - EXIT INT TERM HUP
  exit "$status"
}

run_payload_self_test() {
  local temporary backup
  # Windows Git Bash 可能把 python3 解析为应用商店占位符；仅本地自测允许回退到已安装的 python。
  if ! python3 --version >/dev/null 2>&1; then
    command -v python >/dev/null
    python3() { command python "$@"; }
  fi
  temporary="$(mktemp -d)"
  change_dir="$temporary"
  env_file="$temporary/current.env"
  candidate_env="$temporary/candidate.env"
  backup="$temporary/previous.env"
  helper_path=''
  new_phone="1""38""00000000"
  admin_phone="1""39""00000000"
  {
    printf 'APP_ENV=test\n'
    printf 'SMS_ENABLED=false\n'
    printf 'SMS_TEST_MODE=true\n'
    printf 'SMS_TEST_PHONE_WHITELIST=%s\n' "$new_phone"
  } > "$env_file"
  chmod 600 "$env_file"
  cp --no-dereference -- "$env_file" "$backup"
  chmod 600 "$backup"
  write_candidate_helper
  printf '%s\n%s\n' "$new_phone" "$admin_phone" | python3 "$helper_path" "$env_file" "$candidate_env"
  [ "$(whitelist_state "$(python3 -c 'import re,sys; text=open(sys.argv[1],encoding="utf-8").read(); value=re.search(r"(?m)^SMS_TEST_PHONE_WHITELIST=(.*)$",text); print(value.group(1) if value else "")' "$candidate_env")")" = both ]
  cp --no-dereference -- "$candidate_env" "$env_file"
  [ "$(whitelist_state "$(read_file_env SMS_TEST_PHONE_WHITELIST)")" = both ]
  cp --no-dereference -- "$backup" "$env_file"
  [ "$(whitelist_state "$(read_file_env SMS_TEST_PHONE_WHITELIST)")" = new_only ]
  rm -f -- "$helper_path" "$candidate_env" "$backup" "$env_file"
  rmdir -- "$temporary"
  new_phone=''
  admin_phone=''
  printf 'whitelist_change_payload_self_test=passed\n'
  printf 'candidate_add_only_test=passed\n'
  printf 'automatic_file_rollback_test=passed\n'
  printf 'network_connections=0\n'
  printf 'configuration_mutations=0\n'
  printf 'service_restarts=0\n'
  printf 'real_sms_sent=0\n'
}

if [ "${1:-}" = "--self-test" ]; then
  run_payload_self_test
  exit 0
fi

new_phone="$(printf '%s' "$new_b64" | base64 -d)"
admin_phone="$(printf '%s' "$admin_b64" | base64 -d)"
new_b64=''
admin_b64=''
if ! [[ "$new_phone" =~ ^1[3-9][0-9]{9}$ ]] || ! [[ "$admin_phone" =~ ^1[3-9][0-9]{9}$ ]] || [ "$new_phone" = "$admin_phone" ]; then
  fail target_format
fi

# 任何远端写入前完成全部状态、监控和零发送基线核验。
preflight_checks
[ -d '/home/pc/molin/rollback/sms-phase5' ] && [ ! -L '/home/pc/molin/rollback/sms-phase5' ] &&
  [ "$(stat -c '%U:%a' '/home/pc/molin/rollback/sms-phase5')" = pc:700 ] || fail rollback_parent
if [ -e "$rollback_root" ]; then
  [ -d "$rollback_root" ] && [ ! -L "$rollback_root" ] && [ "$(stat -c '%U:%a' "$rollback_root")" = pc:700 ] || fail rollback_root_identity
fi
# 原子创建专用目录作为排他锁；符号链接、文件和并发执行都会使 mkdir 直接失败。
trap 'handle_exit $?' EXIT
trap 'exit 130' INT TERM HUP
# 创建目录与登记持有状态必须形成不可中断临界区，避免信号落在两条命令之间留下孤儿锁。
trap '' INT TERM HUP
if ! mkdir -- "$lock_dir"; then
  trap 'exit 130' INT TERM HUP
  fail concurrent_change
fi
lock_acquired=true
trap 'exit 130' INT TERM HUP
chmod 700 "$lock_dir"
[ -d "$lock_dir" ] && [ ! -L "$lock_dir" ] && [ "$(stat -c '%U:%a' "$lock_dir")" = pc:700 ] || fail lock_directory_identity

if [ ! -e "$rollback_root" ]; then
  mkdir -- "$rollback_root"
  chmod 700 "$rollback_root"
fi
# 同名变更目录也使用相同临界区；创建失败时不会触碰已有证据。
trap '' INT TERM HUP
if ! mkdir -- "$change_dir"; then
  trap 'exit 130' INT TERM HUP
  fail change_directory_exists
fi
change_dir_created=true
trap 'exit 130' INT TERM HUP
chmod 700 "$change_dir"
[ -d "$change_dir" ] && [ ! -L "$change_dir" ] && [ "$(stat -c '%U:%a' "$change_dir")" = pc:700 ] || fail change_directory_identity
change_dir_verified=true

backup_env="$change_dir/previous.env"
original_env_snapshot="$change_dir/original.environ"
candidate_env="$change_dir/candidate.env"
cp --no-dereference --preserve=mode,ownership,timestamps -- "$env_file" "$backup_env"
chmod 600 "$backup_env"
cat "/proc/${original_pid}/environ" > "$original_env_snapshot"
chmod 600 "$original_env_snapshot"
write_launcher || fail launcher
write_candidate_helper || fail candidate_helper
printf '%s\n%s\n' "$new_phone" "$admin_phone" | python3 "$helper_path" "$env_file" "$candidate_env" || fail candidate_build
[ -f "$candidate_env" ] && [ ! -L "$candidate_env" ] && [ "$(stat -c '%U:%a' "$candidate_env")" = pc:600 ] || fail candidate_identity
[ "$(whitelist_state "$(python3 -c 'import re,sys; text=open(sys.argv[1],encoding="utf-8").read(); value=re.search(r"(?m)^SMS_TEST_PHONE_WHITELIST=(.*)$",text); print(value.group(1) if value else "")' "$candidate_env")")" = both ] || fail candidate_whitelist

stage="${env_file}.stage-${change_id}"
[ ! -e "$stage" ] || fail environment_stage_exists
cp --no-dereference -- "$candidate_env" "$stage"
chmod 600 "$stage"
rollback_armed=true
mv -fT -- "$stage" "$env_file"
stage=''
mutation_started=true
[ "$(whitelist_state "$(read_file_env SMS_TEST_PHONE_WHITELIST)")" = both ] || fail environment_install
stop_exact_api "$original_pid" || fail api_stop
new_pid="$(start_api text "$env_file" "$change_dir/current-api.log")" || fail api_start
wait_for_api "$new_pid" || fail api_health
verify_api_state "$new_pid" both || fail api_state
sleep 10
verify_api_state "$new_pid" both || fail api_stability
[ "$(send_summary)" = "$send_before" ] || fail send_log_delta
[ "$(provider_total)" = "$provider_before" ] || fail provider_delta
verify_alertmanager_discard || fail alertmanager_restore
[ "$(alert_snapshot)" = 0:0 ] || fail alert_delta
[ "$(notification_snapshot)" = "$notification_before" ] || fail notification_delta

rm -f -- "$original_env_snapshot" "$candidate_env" "$helper_path" "$launcher_path"
new_phone=''
admin_phone=''
db_pass=''
internal_token=''
{
  printf 'whitelist_change=passed\n'
  printf 'change_id=%s\n' "$change_id"
  printf 'target_aliases=target-new,target-admin\n'
  printf 'sms_enabled=false\n'
  printf 'sms_test_mode=true\n'
  printf 'previous_whitelist_count=1\n'
  printf 'current_whitelist_count=2\n'
  printf 'target_new_preserved=true\n'
  printf 'target_admin_added=true\n'
  printf 'send_log_delta_zero=true\n'
  printf 'provider_call_delta_zero=true\n'
  printf 'notification_delta_zero=true\n'
  printf 'active_alerts=0:0\n'
  printf 'configuration_mutations=1\n'
  printf 'service_stops=1\n'
  printf 'service_starts=1\n'
  printf 'rollback_available=true\n'
  printf 'sms_submission_requests=0\n'
  printf 'real_sms_sent=0\n'
} > "$change_dir/result.txt"
chmod 600 "$change_dir/result.txt"
# 结果证据安全落盘后再最终化；短暂屏蔽信号，避免释放锁后、关闭回滚前出现无锁恢复窗口。
trap '' INT TERM HUP
rmdir -- "$lock_dir" || fail lock_release
lock_acquired=false
rollback_armed=false
trap 'exit 130' INT TERM HUP
cat "$change_dir/result.txt"
'@
$remotePayload = $remotePayload.Replace("__CHANGE_ID__", $ChangeId)
$remotePayloadBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($remotePayload))

$runnerTemplate = @'
param(
    [switch]$ExecuteChange,
    [string]$ApprovalToken = "",
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
$ChangeId = "__CHANGE_ID__"
$RemotePayloadBase64 = "__REMOTE_PAYLOAD_BASE64__"
$ExpectedApprovalToken = "APPROVE_SMS_PHASE5_WHITELIST_CHANGE___CHANGE_ID__"
$ServerHost = "__SERVER_HOST__"
$SSHPort = __SSH_PORT__
$SSHUser = "__SSH_USER__"
$ExpectedFingerprint = "__SSH_FINGERPRINT__"

function Assert-TargetPair {
    param([string]$NewTarget, [string]$AdminTarget)
    if ($NewTarget -cnotmatch '^1[3-9]\d{9}$' -or $AdminTarget -cnotmatch '^1[3-9]\d{9}$') { throw "目标号码格式无效" }
    if ($NewTarget -ceq $AdminTarget) { throw "两个目标号码必须互异" }
}

function Read-HiddenTarget {
    param([Parameter(Mandatory = $true)][string]$Prompt)
    $secureValue = Read-Host -Prompt $Prompt -AsSecureString
    $pointer = [IntPtr]::Zero
    try {
        $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureValue)
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
    }
    finally {
        if ($pointer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer) }
        if ($null -ne $secureValue) { $secureValue.Dispose() }
    }
}

function Assert-FixedTarget {
    if ($ServerHost -cne "8.130.9.163" -or $SSHPort -ne 10003 -or $SSHUser -cne "pc" -or
        $ExpectedFingerprint -cne "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I") {
        throw "SSH 身份契约不是固定阶段 5 测试服"
    }
}

function Get-VerifiedKnownHosts {
    Assert-FixedTarget
    $knownHosts = [IO.Path]::GetFullPath((Join-Path $env:USERPROFILE ".ssh\known_hosts"))
    if (-not (Test-Path -LiteralPath $knownHosts -PathType Leaf) -or ([IO.FileInfo]$knownHosts).Attributes.HasFlag([IO.FileAttributes]::ReparsePoint)) {
        throw "固定 known_hosts 不存在或属于重解析路径"
    }
    $lines = @(& ssh-keygen.exe -F "[8.130.9.163]:10003" -f $knownHosts)
    if ($LASTEXITCODE -ne 0) { throw "known_hosts 缺少固定测试服身份" }
    $keys = @()
    foreach ($line in $lines) {
        $parts = @(($line.Trim()) -split '\s+')
        if ($parts.Count -ge 3 -and $parts[1] -ceq "ssh-ed25519") { $keys += $parts[2] }
    }
    if ($keys.Count -ne 1) { throw "固定测试服 ED25519 公钥数量异常" }
    $sha256 = [Security.Cryptography.SHA256]::Create()
    try { $fingerprint = "SHA256:" + [Convert]::ToBase64String($sha256.ComputeHash([Convert]::FromBase64String($keys[0]))).TrimEnd('=') }
    finally { $sha256.Dispose() }
    if ($fingerprint -cne $ExpectedFingerprint) { throw "固定测试服 ED25519 指纹不匹配" }
    return $knownHosts
}

function Invoke-FixedSshChangeScript {
    param(
        [Parameter(Mandatory = $true)][string]$KnownHosts,
        [Parameter(Mandatory = $true)][string]$Script
    )
    # 直接向标准输入底层流写入 LF/无 BOM 字节，避免 SSH 参数重组破坏 Bash 语法。
    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = "ssh.exe"
    $startInfo.Arguments = "-p $SSHPort -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 -o `"UserKnownHostsFile=$KnownHosts`" -- ${SSHUser}@${ServerHost} bash -s"
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    $startInfo.StandardOutputEncoding = $utf8NoBom
    $startInfo.StandardErrorEncoding = $utf8NoBom
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $startInfo
    try {
        if (-not $process.Start()) { throw "无法启动固定 SSH 白名单变更进程" }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        $normalizedScript = $Script.Replace("`r`n", "`n").Replace("`r", "`n")
        $inputBytes = $utf8NoBom.GetBytes($normalizedScript)
        try {
            $process.StandardInput.BaseStream.Write($inputBytes, 0, $inputBytes.Length)
            $process.StandardInput.BaseStream.Flush()
        }
        finally {
            if ($null -ne $inputBytes) { [Array]::Clear($inputBytes, 0, $inputBytes.Length) }
            $inputBytes = $null
            $normalizedScript = $null
            $process.StandardInput.Close()
        }
        $process.WaitForExit()
        $stdout = $stdoutTask.Result
        $stderr = $stderrTask.Result
        return [PSCustomObject]@{
            ExitCode = $process.ExitCode
            Output = @($stdout -split "`r?`n" | Where-Object { $_ -ne "" })
            StderrPresent = -not [string]::IsNullOrWhiteSpace($stderr)
        }
    }
    finally { $process.Dispose() }
}

if (-not $ExecuteChange -and -not $SelfTest) {
    Write-Output "whitelist_change_id=$ChangeId"
    Write-Output "whitelist_change_execution_authorized=false"
    Write-Output "interactive_prompts=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExecuteChange -and $SelfTest) { throw "ExecuteChange 与 SelfTest 必须互斥" }
if ($SelfTest) {
    Assert-FixedTarget
    Assert-TargetPair -NewTarget ("1" + "38" + ("0" * 8)) -AdminTarget ("1" + "39" + ("0" * 8))
    $payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64))
    foreach ($marker in @("rollback_armed=true", "restore_original", "SMS_TEST_PHONE_WHITELIST", "whitelist_change_payload_self_test=passed", "service_stops=1", "real_sms_sent=0")) {
        if (-not $payload.Contains($marker)) { throw "内嵌白名单负载不完整" }
    }
    if ($payload.Contains("SMS_ENABLED=true")) { throw "内嵌负载不得开启短信" }
    Write-Output "whitelist_change_runner_self_test=passed"
    Write-Output "fixed_ssh_identity_contract_frozen=true"
    Write-Output "automatic_rollback_contract_verified=true"
    Write-Output "interactive_prompts=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ApprovalToken -cne $ExpectedApprovalToken) { throw "白名单变更授权口令不匹配" }
$knownHosts = Get-VerifiedKnownHosts
$newTarget = $null
$adminTarget = $null
try {
    $newTarget = Read-HiddenTarget -Prompt "请输入 target-new（隐藏输入）"
    $adminTarget = Read-HiddenTarget -Prompt "请输入 target-admin（隐藏输入）"
    Assert-TargetPair -NewTarget $newTarget -AdminTarget $adminTarget
    $newBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($newTarget))
    $adminBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($adminTarget))
    $payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64))
    $executionScript = "new_b64='$newBase64'`nadmin_b64='$adminBase64'`n$payload`n"
    $result = Invoke-FixedSshChangeScript -KnownHosts $knownHosts -Script $executionScript
    $result.Output | Write-Output
    Write-Output "interactive_prompts=2"
    Write-Output "sensitive_values_persisted_in_local_candidate=0"
    Write-Output "network_connections=1"
    Write-Output "uploads=0"
    Write-Output "business_posts=0"
    Write-Output "remote_stderr_present=$($result.StderrPresent.ToString().ToLowerInvariant())"
    Write-Output "whitelist_change_exit_code=$($result.ExitCode)"
    if ($result.ExitCode -ne 0) { throw "固定测试服白名单变更失败，退出码：$($result.ExitCode)" }
}
finally {
    $newTarget = $null
    $adminTarget = $null
    $newBase64 = $null
    $adminBase64 = $null
    $payload = $null
    $executionScript = $null
}
'@

$runner = $runnerTemplate.Replace("__CHANGE_ID__", $ChangeId).
    Replace("__REMOTE_PAYLOAD_BASE64__", $remotePayloadBase64).
    Replace("__SERVER_HOST__", $FixedServerHost).
    Replace("__SSH_PORT__", [string]$FixedSSHPort).
    Replace("__SSH_USER__", $FixedSSHUser).
    Replace("__SSH_FINGERPRINT__", $FixedFingerprint)
$runnerPath = Join-Path $outputPath "run-sms-phase5-canary-whitelist-change-$ChangeId.ps1"
$directoryCreated = $false
$fileCreated = $false
try {
    $null = New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop
    $directoryCreated = $true
    [IO.File]::WriteAllText($runnerPath, $runner, (New-Object Text.UTF8Encoding($true)))
    $fileCreated = $true
    $tokens = $null
    $parseErrors = $null
    $null = [Management.Automation.Language.Parser]::ParseFile($runnerPath, [ref]$tokens, [ref]$parseErrors)
    if (@($parseErrors).Count -ne 0) { throw "runner PowerShell 语法校验失败" }
    $decodedPayload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($remotePayloadBase64))
    foreach ($marker in @("rollback_armed=true", "restore_original", "SMS_TEST_PHONE_WHITELIST", "whitelist_change_payload_self_test=passed", "sms_enabled=false", "sms_test_mode=true")) {
        if (-not $decodedPayload.Contains($marker)) { throw "远端白名单变更负载不完整" }
    }
    if ($decodedPayload.Contains("SMS_ENABLED=true")) { throw "远端负载不得开启短信" }
    $closedOutput = @(& $runnerPath)
    $selfTestOutput = @(& $runnerPath -SelfTest)
    if ($closedOutput -cnotcontains "whitelist_change_execution_authorized=false" -or
        $selfTestOutput -cnotcontains "whitelist_change_runner_self_test=passed") {
        throw "runner 默认关闭或自测失败"
    }
    $runnerSHA256 = (Get-FileHash -LiteralPath $runnerPath -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Output "whitelist_change_candidate=passed"
    Write-Output "change_id=$ChangeId"
    Write-Output "runner_sha256=$runnerSHA256"
    Write-Output "runner_path=$runnerPath"
    Write-Output "candidate_files_written=1"
    Write-Output "interactive_prompts=0"
    Write-Output "sensitive_values_persisted=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_restarts=0"
    Write-Output "real_sms_sent=0"
}
catch {
    if ($fileCreated -and (Test-Path -LiteralPath $runnerPath -PathType Leaf)) { Remove-Item -LiteralPath $runnerPath -Force }
    if ($directoryCreated -and (Test-Path -LiteralPath $outputPath -PathType Container) -and
        @(Get-ChildItem -LiteralPath $outputPath -Force).Count -eq 0) {
        Remove-Item -LiteralPath $outputPath -Force
    }
    throw
}
