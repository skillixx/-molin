#!/usr/bin/env bash
set -Eeuo pipefail
exec 2>/dev/null

# 本脚本只读取 journald 运行态和合并后的配置，不修改日志、配置或服务状态。
fail() {
  printf 'log_retention_preflight=failed\n'
  printf 'failure_stage=%s\n' "$1"
  printf 'business_configuration_mutations=0\n'
  printf 'access_audit_logs_may_increase=true\n'
  printf 'real_sms_delivery_not_verified=true\n'
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
import re
import sys
from decimal import Decimal, InvalidOperation

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

def valid_size(name):
    value = settings.get(name, "").strip()
    match = re.fullmatch(r"([1-9][0-9]*(?:[.][0-9]+)?)([KMGTPE]?)", value, re.IGNORECASE)
    if match is None:
        return False
    multipliers = {"": 1, "K": 1024, "M": 1024**2, "G": 1024**3, "T": 1024**4, "P": 1024**5, "E": 1024**6}
    try:
        byte_count = Decimal(match.group(1)) * multipliers[match.group(2).upper()]
    except (InvalidOperation, OverflowError):
        return False
    return 0 < byte_count <= 2**64 - 1

def valid_duration(name):
    value = settings.get(name, "").strip()
    unit = r"(?:us|ms|s|sec|second|seconds|m|min|minute|minutes|h|hr|hour|hours|d|day|days|w|week|weeks|month|months|y|year|years)"
    return re.fullmatch(rf"[1-9][0-9]*(?:[.][0-9]+)?{unit}", value, re.IGNORECASE) is not None

storage = settings.get("Storage", "auto").strip().lower()
print("storage_mode_persistent=" + str(storage in {"auto", "persistent", ""}).lower())
print("capacity=" + str(valid_size("SystemMaxUse")).lower())
print("keep_free=" + str(valid_size("SystemKeepFree")).lower())
print("retention=" + str(valid_duration("MaxRetentionSec")).lower())
print("rotation=" + str(valid_duration("MaxFileSec")).lower())
')" || fail policy_parse

marker_value() {
  printf '%s\n' "$policy_status" | awk -F= -v marker="$1" '$1 == marker {print $2}'
}

journald_storage_mode_persistent="$(marker_value storage_mode_persistent)"
journald_capacity_limit_configured="$(marker_value capacity)"
journald_keep_free_configured="$(marker_value keep_free)"
journald_retention_limit_configured="$(marker_value retention)"
journald_rotation_limit_configured="$(marker_value rotation)"

log_retention_configuration_complete=false
if [ "$journald_active" = true ] \
  && [ "$journald_persistent_storage_present" = true ] \
  && [ "$journald_storage_mode_persistent" = true ] \
  && [ "$journal_disk_usage_observed" = true ] \
  && [ "$journald_capacity_limit_configured" = true ] \
  && [ "$journald_keep_free_configured" = true ] \
  && [ "$journald_retention_limit_configured" = true ] \
  && [ "$journald_rotation_limit_configured" = true ]; then
  log_retention_configuration_complete=true
fi

# 只读预检无法证明配置值已经过批准，也无法证明运行中的 journald 已在变更后 reload/restart。
log_retention_runtime_reload_verified=false
log_retention_policy_verified=false
log_retention_change_authorization_required=true

printf 'log_retention_preflight=passed\n'
printf 'journald_active=%s\n' "$journald_active"
printf 'journald_persistent_storage_present=%s\n' "$journald_persistent_storage_present"
printf 'journald_storage_mode_persistent=%s\n' "$journald_storage_mode_persistent"
printf 'journal_disk_usage_observed=%s\n' "$journal_disk_usage_observed"
printf 'journald_capacity_limit_configured=%s\n' "$journald_capacity_limit_configured"
printf 'journald_keep_free_configured=%s\n' "$journald_keep_free_configured"
printf 'journald_retention_limit_configured=%s\n' "$journald_retention_limit_configured"
printf 'journald_rotation_limit_configured=%s\n' "$journald_rotation_limit_configured"
printf 'log_retention_configuration_complete=%s\n' "$log_retention_configuration_complete"
printf 'log_retention_runtime_reload_verified=%s\n' "$log_retention_runtime_reload_verified"
printf 'log_retention_policy_verified=%s\n' "$log_retention_policy_verified"
printf 'log_retention_change_authorization_required=%s\n' "$log_retention_change_authorization_required"
printf 'business_configuration_mutations=0\n'
printf 'access_audit_logs_may_increase=true\n'
printf 'real_sms_delivery_not_verified=true\n'
