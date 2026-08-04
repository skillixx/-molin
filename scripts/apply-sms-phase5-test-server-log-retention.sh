#!/usr/bin/env bash
set -Eeuo pipefail
exec 2>/dev/null

# 本 payload 只允许在包装器双门禁后执行；任何异常都恢复原 drop-in 并重新确认 journald 运行态。
system_max_use='__SYSTEM_MAX_USE__'
system_keep_free='__SYSTEM_KEEP_FREE__'
max_retention_sec='__MAX_RETENTION_SEC__'
max_file_sec='__MAX_FILE_SEC__'
target='/etc/systemd/journald.conf.d/90-molin-sms-phase5-retention.conf'
api_path='/home/pc/molin/molin-api'
backup_root='/home/pc/molin/backups'
candidate=''
backup_dir=''
previous_state='unknown'
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
  trap - ERR
  local rollback_ok=true
  if [ "$previous_state" = present ]; then
    sudo -n install -m 0644 "$backup_dir/previous.conf" "$target" || rollback_ok=false
  elif [ "$previous_state" = absent ]; then
    sudo -n rm -f -- "$target" || rollback_ok=false
  else
    rollback_ok=false
  fi
  sudo -n systemctl restart systemd-journald || rollback_ok=false
  systemctl is-active --quiet systemd-journald || rollback_ok=false
  if [ "$previous_state" = present ]; then
    current_hash="$(sudo -n sha256sum "$target" 2>/dev/null | awk '{print $1}')" || rollback_ok=false
    previous_hash="$(sha256sum "$backup_dir/previous.conf" 2>/dev/null | awk '{print $1}')" || rollback_ok=false
    [ "$current_hash" = "$previous_hash" ] || rollback_ok=false
  elif [ "$previous_state" = absent ]; then
    sudo -n test ! -e "$target" || rollback_ok=false
  fi
  if [ "$rollback_ok" != true ]; then
    printf 'journald_configuration_rollback_verified=false\n'
    return 1
  fi
  printf 'journald_configuration_rollback_verified=true\n'
}

on_error() {
  local status=$?
  trap - ERR
  if [ "$rollback_armed" = true ] && ! rollback_journald_configuration; then
    exit 90
  fi
  exit "$status"
}
trap on_error ERR
trap 'if [ -n "$candidate" ] && [ -f "$candidate" ]; then rm -f -- "$candidate"; fi' EXIT

sudo -n true || fail sudo_unavailable
mapfile -t api_pids < <(pgrep -f "^${api_path}$" 2>/dev/null || true)
[ "${#api_pids[@]}" = 1 ] || fail api_process
api_pid="${api_pids[0]}"
verify_api_closed "$api_pid" || fail sms_closed_before
printf 'sms_closed_before=true\n'

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
if sudo -n test -L /etc/systemd/journald.conf.d; then
  fail unsafe_config_directory
fi
if sudo -n test -e /etc/systemd/journald.conf.d && ! sudo -n test -d /etc/systemd/journald.conf.d; then
  fail unsafe_config_directory
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
sudo -n install -d -m 0755 /etc/systemd/journald.conf.d
sudo -n install -m 0644 "$candidate" "$target"
verify_installed_values
sudo -n systemctl restart systemd-journald
systemctl is-active --quiet systemd-journald
verify_installed_values
verify_api_closed "$api_pid"
printf 'sms_closed_after=true\n'
rollback_armed=false
trap - ERR

printf 'log_retention_change_applied=true\n'
printf 'journald_configuration_rollback_verified=not_required\n'
printf 'backup_created=true\n'
printf 'business_configuration_mutations=0\n'
printf 'access_audit_logs_may_increase=true\n'
printf 'real_sms_delivery_not_verified=true\n'
