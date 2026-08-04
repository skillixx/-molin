#!/usr/bin/env bash
set -Eeuo pipefail
exec 2>/dev/null

# 本 payload 只允许在包装器双门禁后执行；任何异常都恢复原 drop-in 并重新确认 journald 运行态。
system_max_use='__SYSTEM_MAX_USE__'
system_keep_free='__SYSTEM_KEEP_FREE__'
max_retention_sec='__MAX_RETENTION_SEC__'
max_file_sec='__MAX_FILE_SEC__'
target='/etc/systemd/journald.conf.d/90-molin-sms-phase5-retention.conf'
config_dir='/etc/systemd/journald.conf.d'
api_path='/home/pc/molin/molin-api'
prometheus_port=19090
backup_root='/home/pc/molin/backups'
candidate=''
backup_dir=''
previous_state='unknown'
config_directory_was_present=false
staged_target=''
rollback_armed=false

fail() {
  printf 'log_retention_change_applied=false\n'
  printf 'failure_stage=%s\n' "$1"
  printf 'real_sms_delivery_not_verified=true\n'
  exit 2
}

read_sms_enabled() {
  local pid="$1"
  tr '\0' '\n' < "/proc/$pid/environ" | sed -n 's/^SMS_ENABLED=//p' | tail -n 1
}

read_env() {
  local pid="$1"
  local key="$2"
  tr '\0' '\n' < "/proc/$pid/environ" | sed -n "s/^${key}=//p" | tail -n 1
}

read_provider_snapshot() {
  local token="$1"
  printf 'X-Internal-Token: %s\n' "$token" |
    curl -fsS --max-time 5 -H @- http://127.0.0.1:8080/api/internal/metrics 2>/dev/null |
    awk '/^sms_provider_calls_total\{/{series += 1; sum += $NF} END{printf "%d:%.0f", series, sum + 0}'
}

read_prometheus_target_health() {
  curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/targets" 2>/dev/null |
    python3 -c 'import json,sys; d=json.load(sys.stdin); x=[t for t in d.get("data",{}).get("activeTargets",[]) if t.get("labels",{}).get("job")=="molin-email-adapter"]; print("missing" if not x else ",".join(sorted(set(t.get("health","") for t in x))))'
}

read_prometheus_provider_snapshot() {
  local count_response
  local sum_response
  count_response="$(curl -fsS --max-time 5 --get --data-urlencode 'query=count(sms_provider_calls_total)' "http://127.0.0.1:${prometheus_port}/api/v1/query")" || return 1
  sum_response="$(curl -fsS --max-time 5 --get --data-urlencode 'query=sum(sms_provider_calls_total)' "http://127.0.0.1:${prometheus_port}/api/v1/query")" || return 1
  python3 - "$count_response" "$sum_response" <<'PY'
import json
import sys

def scalar(payload):
    result = json.loads(payload).get("data", {}).get("result", [])
    if len(result) != 1:
        raise SystemExit(1)
    return result[0]["value"][1]

count = scalar(sys.argv[1])
total = scalar(sys.argv[2])
print(f"{count}:{total}")
PY
}

verify_api_closed() {
  local expected_pid="$1"
  mapfile -t current_pids < <(pgrep -f "^${api_path}$" 2>/dev/null || true)
  [ "${#current_pids[@]}" = 1 ] || return 1
  [ "${current_pids[0]}" = "$expected_pid" ] || return 1
  [ "$(read_sms_enabled "$expected_pid")" = false ] || return 1
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health || true)" = 200 ] || return 1
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready || true)" = 200 ] || return 1
}

verify_candidate_values() {
  python3 - "$1" "$system_max_use" "$system_keep_free" "$max_retention_sec" "$max_file_sec" <<'PY'
import pathlib
import sys

path, max_use, keep_free, retention, rotation = sys.argv[1:]
expected = {
    "Storage": "persistent",
    "SystemMaxUse": max_use,
    "SystemKeepFree": keep_free,
    "MaxRetentionSec": retention,
    "MaxFileSec": rotation,
}
actual = {}
section = ""
for raw in pathlib.Path(path).read_text(encoding="utf-8").splitlines():
    line = raw.strip()
    if not line or line.startswith("#"):
        continue
    if line.startswith("[") and line.endswith("]"):
        section = line
        continue
    if section == "[Journal]" and "=" in line:
        key, value = line.split("=", 1)
        actual[key.strip()] = value.strip()
raise SystemExit(0 if actual == expected else 1)
PY
}

verify_installed_values() {
  local merged
  merged="$(systemd-analyze cat-config systemd/journald.conf)" || return 1
  printf '%s\n' "$merged" | python3 -c '
import sys

max_use, keep_free, retention, rotation = sys.argv[1:]
expected = {
    "Storage": "persistent",
    "SystemMaxUse": max_use,
    "SystemKeepFree": keep_free,
    "MaxRetentionSec": retention,
    "MaxFileSec": rotation,
}
actual = {}
section = ""
for raw in sys.stdin:
    line = raw.strip()
    if not line or line.startswith("#"):
        continue
    if line.startswith("[") and line.endswith("]"):
        section = line
        continue
    if section == "[Journal]" and "=" in line:
        key, value = line.split("=", 1)
        if key.strip() in expected:
            actual[key.strip()] = value.strip()
raise SystemExit(0 if actual == expected else 1)
' "$system_max_use" "$system_keep_free" "$max_retention_sec" "$max_file_sec"
}

rollback_journald_configuration() {
  trap - ERR HUP INT TERM
  local rollback_ok=true
  rollback_failed() {
    printf 'rollback_failure_stage=%s\n' "$1"
    rollback_ok=false
  }
  if [ "$previous_state" = present ]; then
    local restore_stage="${target}.rollback.$$"
    sudo -n test ! -e "$restore_stage" || rollback_failed restore_stage_exists
    sudo -n install -m 0644 "$backup_dir/previous.conf" "$restore_stage" || rollback_failed restore_install
    sudo -n mv -f -- "$restore_stage" "$target" || rollback_failed restore_move
  elif [ "$previous_state" = absent ]; then
    sudo -n rm -f -- "$target" || rollback_failed restore_remove
  else
    rollback_failed unknown_previous_state
  fi
  sudo -n systemctl restart systemd-journald || rollback_failed journald_restart
  systemctl is-active --quiet systemd-journald || rollback_failed journald_inactive
  if [ "$previous_state" = present ]; then
    current_hash="$(sudo -n sha256sum "$target" 2>/dev/null | awk '{print $1}')" || rollback_failed current_hash
    previous_hash="$(sha256sum "$backup_dir/previous.conf" 2>/dev/null | awk '{print $1}')" || rollback_failed previous_hash
    [ "$current_hash" = "$previous_hash" ] || rollback_failed hash_mismatch
  elif [ "$previous_state" = absent ]; then
    sudo -n test ! -e "$target" || rollback_failed target_still_present
  fi
  verify_api_closed "$api_pid" || rollback_failed api_not_closed
  if [ "$config_directory_was_present" = false ]; then
    sudo -n rmdir "$config_dir" || rollback_failed config_directory_removal
  fi
  if [ "$rollback_ok" != true ]; then
    printf 'journald_configuration_rollback_verified=false\n'
    return 1
  fi
  printf 'journald_configuration_rollback_verified=true\n'
}

on_error() {
  local status=$?
  trap - ERR HUP INT TERM
  printf 'log_retention_change_applied=false\n'
  printf 'failure_stage=apply_or_postcheck\n'
  if [ "$rollback_armed" = true ] && ! rollback_journald_configuration; then
    exit 90
  fi
  exit "$status"
}
on_signal() {
  local signal="$1"
  local status="$2"
  trap - ERR HUP INT TERM
  printf 'log_retention_change_applied=false\n'
  printf 'failure_stage=signal_%s\n' "$signal"
  if [ "$rollback_armed" = true ] && ! rollback_journald_configuration; then
    exit 90
  fi
  exit "$status"
}

install_candidate_and_restart() {
  sudo -n install -d -m 0755 "$config_dir"
  staged_target="${target}.new.${timestamp}.$$"
  sudo -n test ! -e "$staged_target"
  sudo -n install -m 0644 "$candidate" "$staged_target"
  sudo -n mv -f -- "$staged_target" "$target"
  staged_target=''
  verify_installed_values
  sudo -n systemctl restart systemd-journald
  systemctl is-active --quiet systemd-journald
}

run_payload_self_test() {
  # 行为自测仅在调用方显式传入 --self-test 时运行，所有读写限制在随机临时目录。
  local fixture
  fixture="$(mktemp -d)"
  target="$fixture/target.conf"
  config_dir="$fixture/config"
  backup_dir="$fixture/backup"
  mkdir -m 700 "$backup_dir"
  config_directory_was_present=true
  api_pid=12345

  sudo() {
    if [ "${1:-}" = -n ]; then shift; fi
    if [ "${1:-}" = systemctl ]; then
      shift
      systemctl "$@"
      return
    fi
    command "$@"
  }
  systemctl() { return 0; }
  verify_installed_values() { return 0; }
  verify_api_closed() {
    self_test_api_closed_calls=$((self_test_api_closed_calls + 1))
    return 0
  }

  printf 'old\n' > "$backup_dir/previous.conf"
  printf 'new\n' > "$target"
  previous_state=present
  self_test_api_closed_calls=0
  rollback_journald_configuration >/dev/null
  cmp -s "$target" "$backup_dir/previous.conf"
  [ "$self_test_api_closed_calls" = 1 ]
  printf 'existing_config_rollback=passed\n'

  rm -f "$target"
  previous_state=absent
  self_test_api_closed_calls=0
  printf 'new\n' > "$target"
  rollback_journald_configuration >/dev/null
  [ ! -e "$target" ]
  [ "$self_test_api_closed_calls" = 1 ]
  printf 'absent_config_rollback=passed\n'

  previous_state=present
  printf 'new\n' > "$target"
  rollback_armed=true
  self_test_api_closed_calls=0
  set +e
  (false; on_error) >/dev/null
  local error_status=$?
  set -e
  printf 'error_path_status=%s\n' "$error_status"
  [ "$error_status" = 1 ]
  cmp -s "$target" "$backup_dir/previous.conf"
  printf 'error_path_rollback=passed\n'

  # 在受控子 shell 中实际投递三类信号，逐一验证 trap、退出码和文件恢复。
  local signal_name
  local expected_status
  for signal_name in HUP INT TERM; do
    case "$signal_name" in
      HUP) expected_status=129 ;;
      INT) expected_status=130 ;;
      TERM) expected_status=143 ;;
    esac
    printf 'new\n' > "$target"
    rollback_armed=true
    set +e
    (
      trap 'on_signal HUP 129' HUP
      trap 'on_signal INT 130' INT
      trap 'on_signal TERM 143' TERM
      kill -s "$signal_name" "$BASHPID"
      exit 99
    ) >/dev/null
    local signal_status=$?
    set -e
    [ "$signal_status" = "$expected_status" ]
    cmp -s "$target" "$backup_dir/previous.conf"
    printf 'signal_%s_rollback=passed\n' "$signal_name"
  done

  sudo() { return 1; }
  printf 'new\n' > "$target"
  rollback_armed=true
  set +e
  (on_signal TERM 143) >/dev/null
  local rollback_failure_status=$?
  set -e
  printf 'rollback_failure_status=%s\n' "$rollback_failure_status"
  [ "$rollback_failure_status" = 90 ]
  printf 'rollback_failure_exit_90=passed\n'

  # 对真实安装函数注入一次性 install 失败，确认 ERR trap 会恢复旧配置。
  sudo() {
    if [ "${1:-}" = -n ]; then shift; fi
    if [ "${self_test_fail_once:-}" = install ] && [ "${1:-}" = install ] && [[ "${*: -1}" = *.new.* ]]; then
      self_test_fail_once=''
      return 1
    fi
    if [ "${1:-}" = systemctl ]; then
      if [ "${self_test_fail_once:-}" = restart ] && [ "${2:-}" = restart ]; then
        self_test_fail_once=''
        return 1
      fi
      return 0
    fi
    command "$@"
  }
  mkdir -p "$config_dir"
  candidate="$fixture/candidate.conf"
  printf 'candidate\n' > "$candidate"
  timestamp=selftest
  printf 'old\n' > "$backup_dir/previous.conf"
  printf 'old\n' > "$target"
  previous_state=present
  config_directory_was_present=true
  rollback_armed=true
  self_test_fail_once=install
  set +e
  (trap on_error ERR; install_candidate_and_restart)
  local install_failure_status=$?
  set -e
  printf 'install_failure_status=%s\n' "$install_failure_status"
  [ "$install_failure_status" = 1 ]
  cmp -s "$target" "$backup_dir/previous.conf"
  printf 'install_failure_rollback=passed\n'

  # 再注入一次 journald restart 失败；安装已经替换目标，回滚必须恢复旧文件。
  systemctl() { return 0; }
  printf 'old\n' > "$target"
  rollback_armed=true
  self_test_fail_once=restart
  set +e
  (trap on_error ERR; install_candidate_and_restart)
  local restart_failure_status=$?
  set -e
  printf 'restart_failure_status=%s\n' "$restart_failure_status"
  [ "$restart_failure_status" = 1 ]
  cmp -s "$target" "$backup_dir/previous.conf"
  printf 'restart_failure_rollback=passed\n'

  command rm -rf -- "$fixture"
  printf 'payload_self_test=passed\n'
  printf 'system_paths_written=0\n'
  printf 'service_restarts=0\n'
}

if [ "${1:-}" = --self-test ]; then
  run_payload_self_test
  exit 0
fi

trap on_error ERR
trap 'on_signal HUP 129' HUP
trap 'on_signal INT 130' INT
trap 'on_signal TERM 143' TERM
trap 'if [ -n "$candidate" ] && [ -f "$candidate" ]; then rm -f -- "$candidate"; fi; if [ -n "$staged_target" ]; then sudo -n rm -f -- "$staged_target" >/dev/null 2>&1 || true; fi' EXIT

sudo -n true || fail sudo_unavailable
mapfile -t api_pids < <(pgrep -f "^${api_path}$" 2>/dev/null || true)
[ "${#api_pids[@]}" = 1 ] || fail api_process
api_pid="${api_pids[0]}"
verify_api_closed "$api_pid" || fail sms_closed_before
printf 'sms_closed_before=true\n'
internal_token="$(read_env "$api_pid" INTERNAL_API_TOKEN)"
[ -n "$internal_token" ] || fail internal_metrics_token
provider_snapshot_before="$(read_provider_snapshot "$internal_token")"
[[ "$provider_snapshot_before" =~ ^40:[0-9]+$ ]] || fail provider_metrics_before
[ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${prometheus_port}/-/ready" || true)" = 200 ] || fail prometheus_before
[ "$(read_prometheus_target_health)" = up ] || fail prometheus_target_before
prometheus_provider_snapshot_before="$(read_prometheus_provider_snapshot)"
[[ "$prometheus_provider_snapshot_before" =~ ^40([.]0+)?:[0-9]+([.]0+)?$ ]] || fail prometheus_provider_metrics_before

# 容量门禁防止候选值小于当前 journal 占用，或把保留空间设置到当前可用空间以上。
read -r disk_total disk_available < <(df -B1 -P /var/log/journal | awk 'NR == 2 {print $2, $4}')
journal_usage="$(du -sb /var/log/journal | awk 'NR == 1 {print $1}')"
read -r max_use_bytes keep_free_bytes < <(python3 - "$system_max_use" "$system_keep_free" <<'PY'
import re
import sys

units = {"K": 1024, "M": 1024**2, "G": 1024**3, "T": 1024**4}
def parse(value):
    match = re.fullmatch(r"([1-9][0-9]*)([KMGT])", value)
    if not match:
        raise SystemExit(1)
    return int(match.group(1)) * units[match.group(2)]
print(parse(sys.argv[1]), parse(sys.argv[2]))
PY
)
[[ "$disk_total" =~ ^[1-9][0-9]*$ && "$disk_available" =~ ^[0-9]+$ && "$journal_usage" =~ ^[0-9]+$ ]] || fail disk_capacity
[ "$max_use_bytes" -ge "$journal_usage" ] || fail max_use_below_current_usage
[ "$keep_free_bytes" -lt "$disk_available" ] || fail keep_free_above_available
[ $((max_use_bytes + keep_free_bytes)) -lt "$disk_total" ] || fail capacity_sum

# 固定配置目录和目标必须是普通路径，禁止通过符号链接把写入重定向到其他系统文件。
if sudo -n test -L "$config_dir"; then
  fail unsafe_config_directory
fi
if sudo -n test -e "$config_dir" && ! sudo -n test -d "$config_dir"; then
  fail unsafe_config_directory
fi
if sudo -n test -d "$config_dir"; then
  config_directory_was_present=true
fi
if sudo -n test -L "$target"; then
  fail unsafe_config_target
fi
if sudo -n test -e "$target" && ! sudo -n test -f "$target"; then
  fail unsafe_config_target
fi
[ -d "$backup_root" ] && [ ! -L "$backup_root" ] || fail unsafe_backup_root

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_dir="$backup_root/sms-phase5-journald-$timestamp"
umask 077
mkdir -m 700 -- "$backup_dir"
if sudo -n test -e "$target"; then
  sudo -n cat "$target" > "$backup_dir/previous.conf"
  chmod 600 "$backup_dir/previous.conf"
  previous_state=present
else
  : > "$backup_dir/previous.absent"
  chmod 600 "$backup_dir/previous.absent"
  previous_state=absent
fi
# 备份只保存合并配置摘要和非敏感健康状态，不保存日志正文或环境值。
systemd-analyze cat-config systemd/journald.conf | sha256sum | awk '{print $1}' > "$backup_dir/merged-config.sha256"
systemctl is-active systemd-journald > "$backup_dir/journald-active.txt"
printf 'health_http=%s\nready_http=%s\n' \
  "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health || true)" \
  "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready || true)" \
  > "$backup_dir/api-health.txt"
printf 'prometheus_ready_http=200\nprometheus_target_health=up\nprovider_snapshot=%s\nprometheus_provider_snapshot=%s\n' \
  "$provider_snapshot_before" "$prometheus_provider_snapshot_before" \
  > "$backup_dir/monitoring-health.txt"
chmod 600 "$backup_dir/merged-config.sha256" "$backup_dir/journald-active.txt" "$backup_dir/api-health.txt" "$backup_dir/monitoring-health.txt"
candidate="$(mktemp)"
cat > "$candidate" <<EOF
[Journal]
Storage=persistent
SystemMaxUse=__SYSTEM_MAX_USE__
SystemKeepFree=__SYSTEM_KEEP_FREE__
MaxRetentionSec=__MAX_RETENTION_SEC__
MaxFileSec=__MAX_FILE_SEC__
EOF
chmod 600 "$candidate"
verify_candidate_values "$candidate" || fail candidate_validation

rollback_armed=true
install_candidate_and_restart
verify_installed_values
verify_api_closed "$api_pid"
printf 'sms_closed_after=true\n'
provider_snapshot_after="$(read_provider_snapshot "$internal_token")"
[ "$provider_snapshot_after" = "$provider_snapshot_before" ]
[ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${prometheus_port}/-/ready" || true)" = 200 ]
[ "$(read_prometheus_target_health)" = up ]
prometheus_provider_snapshot_after="$(read_prometheus_provider_snapshot)"
[ "$prometheus_provider_snapshot_after" = "$prometheus_provider_snapshot_before" ]
printf 'provider_metric_delta_zero=true\n'
printf 'prometheus_provider_metric_delta_zero=true\n'
printf 'prometheus_postcheck=true\n'
rollback_armed=false
trap - ERR HUP INT TERM

printf 'log_retention_change_applied=true\n'
printf 'journald_configuration_rollback_verified=not_required\n'
printf 'backup_created=true\n'
printf 'business_configuration_mutations=0\n'
printf 'access_audit_logs_may_increase=true\n'
printf 'real_sms_delivery_not_verified=true\n'
