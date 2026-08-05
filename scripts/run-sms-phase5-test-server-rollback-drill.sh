#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

# 本执行器只验证固定测试服旧二进制能否在关闭态启动，并在同一窗口恢复当前二进制；它不调用任何业务写接口。
change_id='__CHANGE_ID__'
authorization_phrase='__AUTHORIZATION_PHRASE__'
machine_id_sha256='__MACHINE_ID_SHA256__'
candidate_change_id='__CANDIDATE_CHANGE_ID__'
candidate_sha256='__CANDIDATE_SHA256__'
old_binary_sha256='__OLD_BINARY_SHA256__'
current_binary_sha256='__CURRENT_BINARY_SHA256__'
alertmanager_config_sha256='__ALERTMANAGER_CONFIG_SHA256__'
old_hold_seconds='__OLD_HOLD_SECONDS__'
restored_hold_seconds='__RESTORED_HOLD_SECONDS__'

app_root='/home/pc/molin'
api_path='/home/pc/molin/molin-api'
current_env='/home/pc/molin/infra/.env.test'
backup_root='/home/pc/molin/backups/sms-phase5-20260804T120056Z'
old_binary="$backup_root/molin-api"
candidate_root='/home/pc/molin/rollback/sms-phase5'
candidate="$candidate_root/candidate-${candidate_change_id}.env"
alertmanager_root='/home/pc/molin-alertmanager-phase5/20260805T084215Z'
alertmanager_config="$alertmanager_root/alertmanager.closed.yml"
alertmanager_container='molin-alertmanager-phase5-closed'
evidence_parent="$candidate_root/runtime-drills"
evidence_dir="$evidence_parent/drill-${change_id}"
lock_file="$candidate_root/runtime-drill.lock"
prometheus_port=19090
alertmanager_port=19093
admin_port=3001
user_port=3000
rollback_armed=false
original_pid=''
original_env_snapshot=''
current_binary_snapshot=''
runtime_candidate_snapshot=''
launcher_path=''
notification_before=''
send_summary_before=''

fail() {
  printf 'rollback_runtime_drill=failed\n'
  printf 'failure_stage=%s\n' "$1"
  printf 'automatic_recovery_required=%s\n' "$rollback_armed"
  return 2
}

read_process_env() {
  local pid="$1"
  local key="$2"
  tr '\0' '\n' < "/proc/${pid}/environ" | awk -F= -v wanted="$key" '$1 == wanted {sub(/^[^=]*=/, ""); print; exit}'
}

api_pids() {
  pgrep -f "^${api_path}$" 2>/dev/null || true
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
    # 仅对刚刚再次核验身份的精确 PID 使用 KILL，避免无界停机阻断自动恢复。
    [ "$(tr '\0' ' ' < "/proc/${pid}/cmdline" 2>/dev/null | sed 's/ $//')" = "$api_path" ] || return 1
    kill -KILL "$pid"
    wait_for_exit "$pid"
  fi
}

install_binary_atomically() {
  local source="$1"
  local expected_sha="$2"
  local staged="$evidence_dir/molin-api.stage"
  [ -f "$source" ] && [ ! -L "$source" ]
  [ "$(sha256sum "$source" | awk '{print $1}')" = "$expected_sha" ]
  [ ! -e "$staged" ]
  cp --reflink=auto --preserve=mode,timestamps -- "$source" "$staged"
  chmod 700 "$staged"
  [ "$(sha256sum "$staged" | awk '{print $1}')" = "$expected_sha" ]
  mv -fT -- "$staged" "$api_path"
  [ "$(sha256sum "$api_path" | awk '{print $1}')" = "$expected_sha" ]
}

write_launcher() {
  launcher_path="$evidence_dir/start-api.py"
  [ ! -e "$launcher_path" ]
  cat > "$launcher_path" <<'PY'
#!/usr/bin/env python3
import os
import re
import sys


def load_text_environment(path: str) -> dict[str, str]:
    """按候选生成器同一规则解析环境文件，禁止执行 shell 展开。"""
    values: dict[str, str] = {}
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
            if len(value) not in (0, 1) and value[0] == value[-1] and value[0] in "'\"":
                value = value[1:-1]
            values[key] = value
    return values


mode, environment_path, binary_path = sys.argv[1:]
if mode == "text":
    environment = os.environ.copy()
    environment.update(load_text_environment(environment_path))
elif mode == "nul":
    environment = {}
    with open(environment_path, "rb") as stream:
        for item in stream.read().split(b"\0"):
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
  local expected_sha="$2"
  local index
  for index in $(seq 1 40); do
    if kill -0 "$expected_pid" 2>/dev/null &&
       [ "$(sha256sum "/proc/${expected_pid}/exe" 2>/dev/null | awk '{print $1}')" = "$expected_sha" ] &&
       [ "$(curl -sS --max-time 2 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health 2>/dev/null || true)" = 200 ] &&
       [ "$(curl -sS --max-time 2 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)" = 200 ]; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

verify_running_api() {
  local expected_pid="$1"
  local expected_sha="$2"
  mapfile -t running < <(api_pids)
  [ "${#running[@]}" -eq 1 ] && [ "${running[0]}" = "$expected_pid" ]
  [ "$(sha256sum "/proc/${expected_pid}/exe" | awk '{print $1}')" = "$expected_sha" ]
  [ "$(read_process_env "$expected_pid" SMS_ENABLED | tr '[:upper:]' '[:lower:]')" = false ]
  [ "$(read_process_env "$expected_pid" SMS_TEST_MODE | tr '[:upper:]' '[:lower:]')" = true ]
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health 2>/dev/null || true)" = 200 ]
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)" = 200 ]
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/version 2>/dev/null || true)" = 200 ]
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${admin_port}/api/health" 2>/dev/null || true)" = 200 ]
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${user_port}/api/health" 2>/dev/null || true)" = 200 ]
}

notification_snapshot() {
  curl -fsS --max-time 5 "http://127.0.0.1:${alertmanager_port}/metrics" | awk '
    /^alertmanager_notifications_total({| )/ {notification += $NF}
    /^alertmanager_notifications_failed_total({| )/ {notification_failed += $NF}
    /^alertmanager_notification_requests_total({| )/ {request += $NF}
    /^alertmanager_notification_requests_failed_total({| )/ {request_failed += $NF}
    END {printf "%.0f:%.0f:%.0f:%.0f", notification+0, notification_failed+0, request+0, request_failed+0}
  '
}

alert_snapshot() {
  local alertmanager_active prometheus_sms_active
  alertmanager_active="$(curl -fsS --max-time 5 "http://127.0.0.1:${alertmanager_port}/api/v2/alerts" | python3 -c 'import json,sys; value=json.load(sys.stdin); print(len(value) if isinstance(value,list) else -1)')"
  prometheus_sms_active="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/alerts" | python3 -c 'import json,sys; value=json.load(sys.stdin).get("data",{}).get("alerts",[]); print(sum(1 for item in value if str(item.get("labels",{}).get("alertname","")).startswith("MolinSMS")))')"
  printf '%s:%s' "$alertmanager_active" "$prometheus_sms_active"
}

run_mysql() {
  local statement="$1"
  if command -v mysql >/dev/null 2>&1; then
    MYSQL_PWD="$db_pass" mysql --batch --skip-column-names -h "$db_host" -P "${db_port:-3306}" -u "$db_user" "$db_name" -e "$statement" 2>/dev/null
    return
  fi
  printf '%s\n' "$db_pass" | docker exec -i molin-mysql sh -c '
    IFS= read -r MYSQL_PWD
    export MYSQL_PWD
    exec mysql --batch --skip-column-names -u "$1" "$2" -e "$3"
  ' sh "$db_user" "$db_name" "$statement" 2>/dev/null
}

send_summary() {
  run_mysql "SELECT CONCAT(COUNT(*),':',COALESCE(SUM(submit_status='accepted'),0),':',COALESCE(SUM(submit_status='failed'),0)) FROM sms_send_logs"
}

verify_candidate() {
  python3 - "$candidate" <<'PY'
import hashlib
import os
import pathlib
import re
import stat
import sys

path = pathlib.Path(sys.argv[1])
root = path.parent
if root.resolve(strict=True) != root or path.is_symlink():
    raise SystemExit(2)
root_stat = root.stat()
path_stat = path.stat()
if root_stat.st_uid != os.getuid() or path_stat.st_uid != os.getuid():
    raise SystemExit(2)
if stat.S_IMODE(root_stat.st_mode) != 0o700 or stat.S_IMODE(path_stat.st_mode) != 0o600 or path_stat.st_nlink != 1:
    raise SystemExit(2)
raw = path.read_bytes()
if hashlib.sha256(raw).hexdigest() != '__CANDIDATE_SHA256__' or raw.startswith(b'\xef\xbb\xbf') or b'\0' in raw or b'\r' in raw:
    raise SystemExit(2)
values = {}
for raw_line in raw.decode('utf-8').splitlines():
    line = raw_line.strip()
    if not line or line.startswith('#'):
        continue
    match = re.fullmatch(r'(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)', line)
    if match is None or match.group(1) in values:
        raise SystemExit(2)
    key, value = match.groups()
    value = value.strip()
    if len(value) not in (0, 1) and value[0] == value[-1] and value[0] in "'\"":
        value = value[1:-1]
    values[key] = value
trusted = {item.strip() for item in values.get('TRUSTED_PROXY_IPS', '').split(',') if item.strip()}
if values.get('APP_ENV') != 'test' or values.get('SMS_ENABLED') != 'false' or values.get('SMS_TEST_MODE') != 'true':
    raise SystemExit(2)
if trusted != {'172.20.250.0/28'} or any(key.startswith('SMS_TEMPLATE_CODE_') for key in values):
    raise SystemExit(2)
if values.get('SMS_PROVIDER') != 'aliyun' or values.get('SMS_ALIYUN_ENDPOINT') != 'dysmsapi.aliyuncs.com':
    raise SystemExit(2)
PY
}

verify_closed_alertmanager() {
  [ "$(sha256sum "$alertmanager_config" | awk '{print $1}')" = "$alertmanager_config_sha256" ]
  python3 - "$alertmanager_config" <<'PY'
import re
import sys

text = open(sys.argv[1], encoding='utf-8').read()
route = re.search(r'(?ms)^route:\s*\n(?P<body>(?:^[ \t]+.*\n?)*)', text)
body = route.group('body') if route else ''
if not re.search(r'(?m)^\s+receiver:\s*["\']?discard["\']?\s*$', body):
    raise SystemExit(2)
if re.search(r'(?m)^\s+routes:\s*$', body):
    raise SystemExit(2)
PY
  [ "$(docker inspect "$alertmanager_container" --format '{{.State.Running}}')" = true ]
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${alertmanager_port}/-/ready")" = 200 ]
}

preflight_checks() {
  local machine_hash current_hash running_hash disk_free
  for command_name in awk cat chmod cp curl df docker flock grep id kill mv nohup pgrep python3 rm sed seq sha256sum sleep stat tee tr; do
    command -v "$command_name" >/dev/null || fail "command_${command_name}"
  done
  [ "$(id -un)" = pc ] || fail operator_identity
  machine_hash="$(sha256sum /etc/machine-id | awk '{print $1}')"
  [ "$machine_hash" = "$machine_id_sha256" ] || fail machine_identity
  [ -d "$backup_root" ] && [ ! -L "$backup_root" ] || fail backup_root
  [ -f "$old_binary" ] && [ ! -L "$old_binary" ] || fail old_binary
  [ "$(sha256sum "$old_binary" | awk '{print $1}')" = "$old_binary_sha256" ] || fail old_binary_hash
  [ -f "$api_path" ] && [ ! -L "$api_path" ] || fail current_binary
  current_hash="$(sha256sum "$api_path" | awk '{print $1}')"
  [ "$current_hash" = "$current_binary_sha256" ] || fail current_binary_hash
  [ -f "$current_env" ] && [ ! -L "$current_env" ] && [ "$(stat -c '%U:%a' "$current_env")" = pc:600 ] || fail current_environment
  verify_candidate || fail rollback_candidate
  verify_closed_alertmanager || fail alertmanager_closed
  mapfile -t running < <(api_pids)
  [ "${#running[@]}" -eq 1 ] || fail current_api_count
  original_pid="${running[0]}"
  running_hash="$(sha256sum "/proc/${original_pid}/exe" | awk '{print $1}')"
  [ "$running_hash" = "$current_binary_sha256" ] || fail current_running_hash
  verify_running_api "$original_pid" "$current_binary_sha256" || fail current_api_health
  [ "$(alert_snapshot)" = 0:0 ] || fail active_alerts
  notification_before="$(notification_snapshot)" || fail notification_metrics
  [ "$notification_before" = '3:0:3:0' ] || fail notification_baseline
  db_host="$(read_process_env "$original_pid" MYSQL_HOST)"
  db_port="$(read_process_env "$original_pid" MYSQL_PORT)"
  db_user="$(read_process_env "$original_pid" MYSQL_USER)"
  db_pass="$(read_process_env "$original_pid" MYSQL_PASSWORD)"
  db_name="$(read_process_env "$original_pid" MYSQL_DATABASE)"
  [ -n "$db_host" ] && [ -n "$db_user" ] && [ -n "$db_pass" ] && [ -n "$db_name" ] || fail database_environment
  send_summary_before="$(send_summary)" || fail send_summary
  [ -n "$send_summary_before" ] || fail send_summary_empty
  disk_free="$(df -Pk "$app_root" | awk 'NR==2 {print $4}')"
  [ "${disk_free:-0}" -ge 524288 ] || fail disk_space
  printf 'rollback_runtime_preflight=passed\n'
  printf 'change_id=%s\n' "$change_id"
  printf 'candidate_change_id=%s\n' "$candidate_change_id"
  printf 'candidate_sha256=%s\n' "$candidate_sha256"
  printf 'old_binary_sha256=%s\n' "$old_binary_sha256"
  printf 'current_binary_sha256=%s\n' "$current_binary_sha256"
  printf 'alertmanager_route=discard\n'
  printf 'alertmanager_notification_baseline=%s\n' "$notification_before"
  printf 'active_alerts=0:0\n'
  printf 'sms_enabled=false\n'
  printf 'sms_test_mode=true\n'
  printf 'business_configuration_mutations=0\n'
  printf 'service_restarts=0\n'
  printf 'notification_posts=0\n'
  printf 'real_sms_sent=0\n'
}

restore_current() {
  local current_pid=''
  local running_hash=''
  local restored_pid=''
  # 恢复阶段忽略交互中断，确保当前二进制和关闭态健康检查完成后才交还控制权。
  trap '' INT TERM HUP
  mapfile -t running < <(api_pids)
  if [ "${#running[@]}" -gt 1 ]; then
    return 1
  fi
  if [ "${#running[@]}" -eq 1 ]; then
    current_pid="${running[0]}"
    running_hash="$(sha256sum "/proc/${current_pid}/exe" 2>/dev/null | awk '{print $1}')"
    if [ "$running_hash" != "$current_binary_sha256" ] ||
       ! verify_running_api "$current_pid" "$current_binary_sha256"; then
      stop_exact_api "$current_pid" || return 1
      current_pid=''
    fi
  fi
  if [ "$(sha256sum "$api_path" 2>/dev/null | awk '{print $1}')" != "$current_binary_sha256" ]; then
    install_binary_atomically "$current_binary_snapshot" "$current_binary_sha256" || return 1
  fi
  if [ -z "$current_pid" ]; then
    restored_pid="$(start_api nul "$original_env_snapshot" "$evidence_dir/current-api.log")"
    wait_for_api "$restored_pid" "$current_binary_sha256" || return 1
    current_pid="$restored_pid"
  fi
  verify_running_api "$current_pid" "$current_binary_sha256" || return 1
  sleep "$restored_hold_seconds"
  verify_running_api "$current_pid" "$current_binary_sha256" || return 1
  trap 'exit 130' INT TERM HUP
  return 0
}

handle_exit() {
  local status="$1"
  local recovered=false
  set +e
  if [ "$status" -ne 0 ] && [ "$rollback_armed" = true ]; then
    trap '' INT TERM HUP
    if restore_current; then
      recovered=true
      rollback_armed=false
    fi
  fi
  if [ -n "$evidence_dir" ] && [ -d "$evidence_dir" ]; then
    if [ -n "$runtime_candidate_snapshot" ]; then
      rm -f -- "$runtime_candidate_snapshot"
    fi
    if [ "$rollback_armed" = false ] || [ "$recovered" = true ] || [ "$status" -eq 0 ]; then
      rm -f -- "$original_env_snapshot"
    fi
    {
      printf 'exit_status=%s\n' "$status"
      printf 'automatic_recovery_succeeded=%s\n' "$recovered"
      printf 'sensitive_environment_snapshot_retained=%s\n' "$([ -f "$original_env_snapshot" ] && printf true || printf false)"
    } >> "$evidence_dir/exit-evidence.txt"
    chmod 600 "$evidence_dir/exit-evidence.txt"
  fi
  trap - EXIT INT TERM HUP
  exit "$status"
}

run_execute() {
  local approval old_pid restored_pid notification_after send_summary_after
  printf '请输入批准短语 %s：' "$authorization_phrase" >&2
  IFS= read -r approval
  [ "$approval" = "$authorization_phrase" ] || fail authorization
  # 在任何远端写入前先完整执行一次只读门禁；失败时不创建锁或证据目录。
  preflight_checks >/dev/null
  [ -d "$candidate_root" ] && [ ! -L "$candidate_root" ] &&
    [ "$(realpath -- "$candidate_root")" = "$candidate_root" ] &&
    [ "$(stat -c '%U:%a' "$candidate_root")" = pc:700 ] || fail candidate_root_identity
  if [ -e "$evidence_parent" ]; then
    [ -d "$evidence_parent" ] && [ ! -L "$evidence_parent" ] &&
      [ "$(stat -c '%U:%a' "$evidence_parent")" = pc:700 ] || fail evidence_parent_identity
  else
    mkdir -- "$evidence_parent"
    chmod 700 "$evidence_parent"
  fi
  mkdir -- "$evidence_dir"
  chmod 700 "$evidence_dir"
  exec 9>"$lock_file"
  flock -n 9 || fail concurrent_drill
  preflight_checks > "$evidence_dir/preflight.txt"
  chmod 600 "$evidence_dir/preflight.txt"
  trap 'handle_exit $?' EXIT
  trap 'exit 130' INT TERM HUP

  original_env_snapshot="$evidence_dir/original.environ"
  current_binary_snapshot="$evidence_dir/current-binary"
  runtime_candidate_snapshot="$evidence_dir/candidate.env"
  cat "/proc/${original_pid}/environ" > "$original_env_snapshot"
  chmod 600 "$original_env_snapshot"
  cp --reflink=auto --preserve=mode,timestamps -- "$api_path" "$current_binary_snapshot"
  chmod 700 "$current_binary_snapshot"
  [ "$(sha256sum "$current_binary_snapshot" | awk '{print $1}')" = "$current_binary_sha256" ] || fail current_snapshot
  cp --no-dereference -- "$candidate" "$runtime_candidate_snapshot"
  [ -f "$runtime_candidate_snapshot" ] && [ ! -L "$runtime_candidate_snapshot" ] || fail candidate_snapshot_identity
  chmod 600 "$runtime_candidate_snapshot"
  [ "$(sha256sum "$runtime_candidate_snapshot" | awk '{print $1}')" = "$candidate_sha256" ] || fail candidate_snapshot_hash
  write_launcher
  rollback_armed=true

  install_binary_atomically "$old_binary" "$old_binary_sha256" || fail old_binary_install
  stop_exact_api "$original_pid" || fail current_api_stop
  old_pid="$(start_api text "$runtime_candidate_snapshot" "$evidence_dir/old-api.log")"
  wait_for_api "$old_pid" "$old_binary_sha256" || fail old_api_start
  rm -f -- "$runtime_candidate_snapshot"
  verify_running_api "$old_pid" "$old_binary_sha256" || fail old_api_verify
  sleep "$old_hold_seconds"
  verify_running_api "$old_pid" "$old_binary_sha256" || fail old_api_stability
  printf 'old_runtime_verified=true\nold_runtime_seconds=%s\n' "$old_hold_seconds" > "$evidence_dir/old-runtime.txt"
  chmod 600 "$evidence_dir/old-runtime.txt"

  restore_current || fail current_recovery
  rollback_armed=false
  mapfile -t running < <(api_pids)
  [ "${#running[@]}" -eq 1 ] || fail restored_api_count
  restored_pid="${running[0]}"
  verify_running_api "$restored_pid" "$current_binary_sha256" || fail restored_api_verify
  verify_closed_alertmanager || fail restored_alertmanager_closed
  [ "$(alert_snapshot)" = 0:0 ] || fail restored_active_alerts
  notification_after="$(notification_snapshot)" || fail restored_notification_metrics
  [ "$notification_after" = "$notification_before" ] || fail notification_delta
  send_summary_after="$(send_summary)" || fail restored_send_summary
  [ "$send_summary_after" = "$send_summary_before" ] || fail sms_send_delta
  rm -f -- "$original_env_snapshot"

  {
    printf 'rollback_runtime_drill=passed\n'
    printf 'change_id=%s\n' "$change_id"
    printf 'old_binary_runtime_verified=true\n'
    printf 'current_binary_restored=true\n'
    printf 'current_environment_file_replaced=false\n'
    printf 'rollback_candidate_runtime_used=true\n'
    printf 'sms_enabled=false\n'
    printf 'sms_test_mode=true\n'
    printf 'alertmanager_route=discard\n'
    printf 'notification_delta_zero=true\n'
    printf 'sms_send_log_delta_zero=true\n'
    printf 'service_starts=2\n'
    printf 'service_stops=2\n'
    printf 'notification_posts=0\n'
    printf 'business_endpoint_posts=0\n'
    printf 'real_sms_sent=0\n'
  } | tee "$evidence_dir/drill-result.txt"
  chmod 600 "$evidence_dir/drill-result.txt"
}

case "${1:-}" in
  --self-test)
    [[ "$change_id" =~ ^[0-9]{8}T[0-9]{6}Z$ ]]
    [[ "$candidate_change_id" =~ ^[0-9]{8}T[0-9]{6}Z$ ]]
    for hash in "$machine_id_sha256" "$candidate_sha256" "$old_binary_sha256" "$current_binary_sha256" "$alertmanager_config_sha256"; do
      [[ "$hash" =~ ^[a-f0-9]{64}$ ]]
    done
    printf 'rollback_runtime_runner_self_test=passed\n'
    printf 'remote_connections=0\n'
    printf 'service_restarts=0\n'
    printf 'notification_posts=0\n'
    printf 'business_endpoint_posts=0\n'
    printf 'real_sms_sent=0\n'
    ;;
  --preflight)
    preflight_checks
    ;;
  --execute)
    run_execute
    ;;
  *)
    printf 'rollback_runtime_execution_authorized=false\n'
    printf 'required_mode=--preflight-or---execute\n'
    printf 'service_restarts=0\n'
    printf 'notification_posts=0\n'
    printf 'real_sms_sent=0\n'
    exit 2
    ;;
esac
