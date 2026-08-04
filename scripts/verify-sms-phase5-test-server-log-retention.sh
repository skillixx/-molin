#!/usr/bin/env bash
set -Eeuo pipefail
exec 2>/dev/null

# 本脚本只读取 journald 运行态和合并后的配置，不修改日志、配置或服务状态。
fail() {
  printf 'log_retention_preflight=failed\n'
  printf 'failure_stage=%s\n' "$1"
  printf 'remote_mutations=0\n'
  printf 'real_sms_sent=0\n'
  exit 2
}

if systemctl is-active --quiet systemd-journald; then
  journald_active=true
else
  journald_active=false
fi

if [ -d /var/log/journal ] && [ ! -L /var/log/journal ]; then
  journald_persistent_storage_present=true
else
  journald_persistent_storage_present=false
fi

if journalctl --disk-usage >/dev/null; then
  journal_disk_usage_observed=true
else
  journal_disk_usage_observed=false
fi

# systemd-analyze 会按 systemd 实际加载顺序合并主配置与 drop-in；这里只输出设置是否存在，不输出配置值。
merged_config="$(systemd-analyze cat-config systemd/journald.conf)" || fail merged_config
policy_status="$(printf '%s\n' "$merged_config" | python3 -c '
import sys

settings = {}
section = ""
for raw_line in sys.stdin:
    line = raw_line.strip()
    if not line or line.startswith("#"):
        continue
    if line.startswith("[") and line.endswith("]"):
        section = line
        continue
    if section != "[Journal]" or "=" not in line:
        continue
    key, value = line.split("=", 1)
    settings[key.strip()] = value.strip()

def configured(name):
    value = settings.get(name, "").strip().lower()
    return value not in {"", "0", "0s", "infinity", "infinite"}

for marker, name in (
    ("capacity", "SystemMaxUse"),
    ("keep_free", "SystemKeepFree"),
    ("retention", "MaxRetentionSec"),
    ("rotation", "MaxFileSec"),
):
    print(f"{marker}={str(configured(name)).lower()}")
')" || fail policy_parse

marker_value() {
  printf '%s\n' "$policy_status" | awk -F= -v marker="$1" '$1 == marker {print $2}'
}

journald_capacity_limit_configured="$(marker_value capacity)"
journald_keep_free_configured="$(marker_value keep_free)"
journald_retention_limit_configured="$(marker_value retention)"
journald_rotation_limit_configured="$(marker_value rotation)"

log_retention_policy_verified=false
log_retention_change_authorization_required=true
if [ "$journald_active" = true ] \
  && [ "$journald_persistent_storage_present" = true ] \
  && [ "$journal_disk_usage_observed" = true ] \
  && [ "$journald_capacity_limit_configured" = true ] \
  && [ "$journald_keep_free_configured" = true ] \
  && [ "$journald_retention_limit_configured" = true ] \
  && [ "$journald_rotation_limit_configured" = true ]; then
  log_retention_policy_verified=true
  log_retention_change_authorization_required=false
fi

printf 'log_retention_preflight=passed\n'
printf 'journald_active=%s\n' "$journald_active"
printf 'journald_persistent_storage_present=%s\n' "$journald_persistent_storage_present"
printf 'journal_disk_usage_observed=%s\n' "$journal_disk_usage_observed"
printf 'journald_capacity_limit_configured=%s\n' "$journald_capacity_limit_configured"
printf 'journald_keep_free_configured=%s\n' "$journald_keep_free_configured"
printf 'journald_retention_limit_configured=%s\n' "$journald_retention_limit_configured"
printf 'journald_rotation_limit_configured=%s\n' "$journald_rotation_limit_configured"
printf 'log_retention_policy_verified=%s\n' "$log_retention_policy_verified"
printf 'log_retention_change_authorization_required=%s\n' "$log_retention_change_authorization_required"
printf 'remote_mutations=0\n'
printf 'real_sms_sent=0\n'
