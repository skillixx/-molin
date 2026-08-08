#!/usr/bin/env bash
set -Eeuo pipefail
exec 2>/dev/null

# 本验证器只读取固定候选、执行证据和当前运行态，不修改文件、不发信号、不调用任何写接口。
change_id='__CHANGE_ID__'
runner_sha256='__RUNNER_SHA256__'
candidate_sha256='__CANDIDATE_SHA256__'
old_binary_sha256='__OLD_BINARY_SHA256__'
current_binary_sha256='__CURRENT_BINARY_SHA256__'
alertmanager_config_sha256='__ALERTMANAGER_CONFIG_SHA256__'
api_path='/home/pc/molin/molin-api'
current_env='/home/pc/molin/infra/.env.test'
candidate_root='/home/pc/molin/rollback/sms-phase5'
staging_dir="$candidate_root/runtime-drill-staging/$change_id"
runner="$staging_dir/run-sms-phase5-test-server-rollback-drill.sh"
evidence_dir="$candidate_root/runtime-drills/drill-$change_id"
alertmanager_config='/home/pc/molin-alertmanager-phase5/20260805T084215Z/alertmanager.closed.yml'
prometheus_port=19090
alertmanager_port=19093
admin_port=3001
user_port=3000

fail() {
  printf 'rollback_runtime_evidence=failed\n'
  printf 'failure_stage=%s\n' "$1"
  printf 'remote_files_written=0\n'
  printf 'service_restarts=0\n'
  printf 'notification_posts=0\n'
  printf 'business_endpoint_posts=0\n'
  printf 'real_sms_sent=0\n'
  exit 2
}

read_process_env() {
  local pid="$1"
  local key="$2"
  tr '\0' '\n' < "/proc/${pid}/environ" | awk -F= -v wanted="$key" '$1 == wanted {sub(/^[^=]*=/, ""); print; exit}'
}

verify_exact_lines() {
  local path="$1"
  shift
  python3 - "$path" "$@" <<'PY'
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
expected = set(sys.argv[2:])
lines = path.read_text(encoding="utf-8").splitlines()
if len(lines) != len(expected) or set(lines) != expected:
    raise SystemExit(2)
PY
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

for command_name in awk curl docker find grep id pgrep python3 realpath sha256sum sort stat tr wc; do
  command -v "$command_name" >/dev/null || fail "command_${command_name}"
done
[ "$(id -un)" = pc ] || fail operator_identity

# 暂存 runner 必须仍是本次冻结字节，执行证据目录必须为固定用户的排他普通目录。
[ -d "$staging_dir" ] && [ ! -L "$staging_dir" ] && [ "$(realpath -- "$staging_dir")" = "$staging_dir" ] || fail staging_dir
[ "$(stat -c '%U:%a' "$staging_dir")" = pc:700 ] || fail staging_permissions
[ -f "$runner" ] && [ ! -L "$runner" ] && [ "$(stat -c '%U:%a:%h' "$runner")" = pc:600:1 ] || fail runner_identity
[ "$(sha256sum "$runner" | awk '{print $1}')" = "$runner_sha256" ] || fail runner_hash
[ -d "$evidence_dir" ] && [ ! -L "$evidence_dir" ] && [ "$(realpath -- "$evidence_dir")" = "$evidence_dir" ] || fail evidence_dir
[ "$(stat -c '%U:%a' "$evidence_dir")" = pc:700 ] || fail evidence_permissions

expected_files="$(printf '%s\n' current-api.log current-binary drill-result.txt exit-evidence.txt old-api.log old-runtime.txt preflight.txt start-api.py | LC_ALL=C sort)"
actual_files="$(find "$evidence_dir" -mindepth 1 -maxdepth 1 -printf '%f\n' | LC_ALL=C sort)"
[ "$actual_files" = "$expected_files" ] || fail evidence_file_set
[ -z "$(find "$evidence_dir" -maxdepth 1 -type l -print -quit)" ] || fail evidence_symlink
[ -z "$(find "$evidence_dir" -maxdepth 1 -type f ! -user pc -print -quit)" ] || fail evidence_owner
[ -z "$(find "$evidence_dir" -maxdepth 1 -type f ! -links 1 -print -quit)" ] || fail evidence_hardlink
[ "$(stat -c '%a' "$evidence_dir/current-binary")" = 700 ] || fail current_snapshot_mode
[ "$(stat -c '%a' "$evidence_dir/start-api.py")" = 700 ] || fail launcher_mode
for evidence_file in current-api.log drill-result.txt exit-evidence.txt old-api.log old-runtime.txt preflight.txt; do
  [ "$(stat -c '%a' "$evidence_dir/$evidence_file")" = 600 ] || fail "evidence_mode_${evidence_file}"
done
[ ! -e "$evidence_dir/original.environ" ] || fail sensitive_environment_retained
[ ! -e "$evidence_dir/candidate.env" ] || fail candidate_snapshot_retained
[ ! -e "$evidence_dir/molin-api.stage" ] || fail binary_stage_retained

verify_exact_lines "$evidence_dir/drill-result.txt" \
  'rollback_runtime_drill=passed' \
  "change_id=$change_id" \
  'old_binary_runtime_verified=true' \
  'current_binary_restored=true' \
  'current_environment_file_replaced=false' \
  'rollback_candidate_runtime_used=true' \
  'sms_enabled=false' \
  'sms_test_mode=true' \
  'alertmanager_route=discard' \
  'notification_delta_zero=true' \
  'sms_send_log_delta_zero=true' \
  'service_starts=2' \
  'service_stops=2' \
  'notification_posts=0' \
  'business_endpoint_posts=0' \
  'real_sms_sent=0' || fail drill_result
verify_exact_lines "$evidence_dir/old-runtime.txt" \
  'old_runtime_verified=true' \
  'old_runtime_seconds=10' || fail old_runtime
verify_exact_lines "$evidence_dir/exit-evidence.txt" \
  'exit_status=0' \
  'automatic_recovery_succeeded=false' \
  'sensitive_environment_snapshot_retained=false' || fail exit_evidence

for required in \
  'rollback_runtime_preflight=passed' \
  "change_id=$change_id" \
  'candidate_change_id=20260805T015043Z' \
  "candidate_sha256=$candidate_sha256" \
  "current_environment_sha256=$candidate_sha256" \
  'candidate_matches_running_environment=true' \
  "old_binary_sha256=$old_binary_sha256" \
  "current_binary_sha256=$current_binary_sha256" \
  'alertmanager_route=discard' \
  'alertmanager_notification_baseline=3:0:3:0' \
  'active_alerts=0:0' \
  'sms_enabled=false' \
  'sms_test_mode=true' \
  'business_configuration_mutations=0' \
  'service_restarts=0' \
  'notification_posts=0' \
  'real_sms_sent=0'; do
  grep -Fxq "$required" "$evidence_dir/preflight.txt" || fail preflight_evidence
done

# API 日志只能保留低敏运行证据，发现密钥名、Bearer、完整手机号或验证码形态即失败关闭。
python3 - \
  "$evidence_dir/old-api.log" \
  "$evidence_dir/current-api.log" \
  "$evidence_dir/preflight.txt" \
  "$evidence_dir/drill-result.txt" \
  "$evidence_dir/old-runtime.txt" \
  "$evidence_dir/exit-evidence.txt" <<'PY' || fail sensitive_log
import re
import sys

patterns = (
    re.compile(r"(?i)(?:SMS_ALIYUN_ACCESS_KEY_ID|SMS_ALIYUN_ACCESS_KEY_SECRET|MYSQL_PASSWORD|JWT_SECRET|REFRESH_TOKEN_SECRET|INTERNAL_API_TOKEN)\s*="),
    re.compile(r"(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{12,}"),
    re.compile(r"(?<!\d)1[3-9]\d{9}(?!\d)"),
    re.compile(r"(?i)\b(?:otp|verification[_ -]?code)\s*[:=]\s*\d{4,8}\b"),
)
for path in sys.argv[1:]:
    text = open(path, encoding="utf-8", errors="replace").read()
    if any(pattern.search(text) for pattern in patterns):
        raise SystemExit(2)
PY

# 当前磁盘、运行进程和环境文件必须全部恢复到部署前固定摘要与关闭态。
[ -f "$api_path" ] && [ ! -L "$api_path" ] || fail current_binary_identity
[ "$(sha256sum "$api_path" | awk '{print $1}')" = "$current_binary_sha256" ] || fail current_binary_hash
[ "$(sha256sum "$evidence_dir/current-binary" | awk '{print $1}')" = "$current_binary_sha256" ] || fail current_snapshot_hash
[ -f "$current_env" ] && [ ! -L "$current_env" ] && [ "$(stat -c '%U:%a' "$current_env")" = pc:600 ] || fail current_environment_identity
[ "$(sha256sum "$current_env" | awk '{print $1}')" = "$candidate_sha256" ] || fail current_environment_hash
mapfile -t api_pids < <(pgrep -f "^${api_path}$" 2>/dev/null || true)
[ "${#api_pids[@]}" -eq 1 ] || fail api_process_count
api_pid="${api_pids[0]}"
[ "$(sha256sum "/proc/${api_pid}/exe" | awk '{print $1}')" = "$current_binary_sha256" ] || fail running_binary_hash
[ "$(read_process_env "$api_pid" SMS_ENABLED | tr '[:upper:]' '[:lower:]')" = false ] || fail sms_enabled
[ "$(read_process_env "$api_pid" SMS_TEST_MODE | tr '[:upper:]' '[:lower:]')" = true ] || fail sms_test_mode
# 将当前进程中的敏感值仅在内存中与日志比较，禁止经命令参数或输出暴露。
python3 - "/proc/${api_pid}/environ" "$evidence_dir/old-api.log" "$evidence_dir/current-api.log" <<'PY' || fail runtime_secret_in_log
import sys

environment_path, *log_paths = sys.argv[1:]
keys = {
    b"MYSQL_PASSWORD",
    b"JWT_SECRET",
    b"REFRESH_TOKEN_SECRET",
    b"INTERNAL_API_TOKEN",
    b"SMS_ALIYUN_ACCESS_KEY_ID",
    b"SMS_ALIYUN_ACCESS_KEY_SECRET",
    b"SMS_PHONE_HMAC_SECRET",
}
values = []
for item in open(environment_path, "rb").read().split(b"\0"):
    key, separator, value = item.partition(b"=")
    if separator and key in keys and len(value) >= 6:
        values.append(value)
for path in log_paths:
    content = open(path, "rb").read()
    if any(value in content for value in values):
        raise SystemExit(2)
PY
for url in \
  'http://127.0.0.1:8080/api/health' \
  'http://127.0.0.1:8080/api/ready' \
  'http://127.0.0.1:8080/api/version' \
  "http://127.0.0.1:${admin_port}/api/health" \
  "http://127.0.0.1:${user_port}/api/health"; do
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "$url")" = 200 ] || fail api_health
done

db_host="$(read_process_env "$api_pid" MYSQL_HOST)"
db_port="$(read_process_env "$api_pid" MYSQL_PORT)"
db_user="$(read_process_env "$api_pid" MYSQL_USER)"
db_pass="$(read_process_env "$api_pid" MYSQL_PASSWORD)"
db_name="$(read_process_env "$api_pid" MYSQL_DATABASE)"
send_summary="$(run_mysql "SELECT CONCAT(COUNT(*),':',COALESCE(SUM(submit_status='accepted'),0),':',COALESCE(SUM(submit_status='failed'),0)) FROM sms_send_logs")" || fail send_summary
[ "$send_summary" = 13:13:0 ] || fail send_summary_drift
internal_token="$(read_process_env "$api_pid" INTERNAL_API_TOKEN)"
[ -n "$internal_token" ] || fail internal_token
provider_total="$(printf 'X-Internal-Token: %s\n' "$internal_token" | curl -fsS --max-time 5 -H @- http://127.0.0.1:8080/api/internal/metrics | awk '/^sms_provider_calls_total({| )/{sum += $NF} END{printf "%.0f", sum+0}')" || fail provider_metrics
[ "$provider_total" = 0 ] || fail provider_delta

[ "$(sha256sum "$alertmanager_config" | awk '{print $1}')" = "$alertmanager_config_sha256" ] || fail alertmanager_config_hash
[ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${alertmanager_port}/-/ready")" = 200 ] || fail alertmanager_ready
[ "$(notification_snapshot)" = 3:0:3:0 ] || fail notification_delta
alertmanager_active="$(curl -fsS --max-time 5 "http://127.0.0.1:${alertmanager_port}/api/v2/alerts" | python3 -c 'import json,sys; value=json.load(sys.stdin); print(len(value) if isinstance(value,list) else -1)')"
[ "$alertmanager_active" = 0 ] || fail alertmanager_active
prometheus_summary="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/alertmanagers" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("data",{}).get("activeAlertmanagers",[])))')"
[ "$prometheus_summary" = 1 ] || fail prometheus_alertmanager
prometheus_sms_alerts="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/alerts" | python3 -c 'import json,sys; items=json.load(sys.stdin).get("data",{}).get("alerts",[]); print(sum(1 for item in items if str(item.get("labels",{}).get("alertname","")).startswith("MolinSMS")))')"
[ "$prometheus_sms_alerts" = 0 ] || fail prometheus_sms_alerts
drill_processes="$({ pgrep -fa '[r]un-sms-phase5-test-server-rollback-drill.sh.*--execute' || true; } | wc -l)"
[ "$drill_processes" = 0 ] || fail drill_process

printf 'rollback_runtime_evidence=passed\n'
printf 'change_id=%s\n' "$change_id"
printf 'runner_sha256=%s\n' "$runner_sha256"
printf 'old_binary_runtime_verified=true\n'
printf 'current_binary_restored=true\n'
printf 'current_environment_unchanged=true\n'
printf 'rollback_restore_runtime_verified=true\n'
printf 'sms_enabled=false\n'
printf 'sms_test_mode=true\n'
printf 'send_summary=13:13:0\n'
printf 'provider_total=0\n'
printf 'alertmanager_route=discard\n'
printf 'notification_snapshot=3:0:3:0\n'
printf 'active_alerts=0:0\n'
printf 'sensitive_evidence_files=0\n'
printf 'sensitive_log_findings=0\n'
printf 'remote_files_written=0\n'
printf 'service_restarts=0\n'
printf 'notification_posts=0\n'
printf 'business_endpoint_posts=0\n'
printf 'real_sms_sent=0\n'
