#!/usr/bin/env bash
set -Eeuo pipefail
exec 2>/dev/null
target='/etc/systemd/journald.conf.d/90-molin-sms-phase5-retention.conf'

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

# 只输出文件系统总量、可用量和 journal 目录聚合占用，不读取或输出任何日志正文。
journal_disk_total_bytes=0
journal_disk_available_bytes=0
journal_directory_usage_bytes=0
journal_directory_usage_percent=0
journal_filesystem_capacity_observed=false
disk_summary="$(df -B1 -P /var/log/journal 2>/dev/null | awk 'NR == 2 {print $2 ":" $4}')" || true
journal_directory_usage_bytes="$(du -sb /var/log/journal 2>/dev/null | awk 'NR == 1 {print $1}')" || true
if [[ "$disk_summary" =~ ^([1-9][0-9]*):([0-9]+)$ ]] \
  && [[ "$journal_directory_usage_bytes" =~ ^[0-9]+$ ]]; then
  journal_disk_total_bytes="${disk_summary%%:*}"
  journal_disk_available_bytes="${disk_summary#*:}"
  journal_directory_usage_percent="$(python3 - "$journal_directory_usage_bytes" "$journal_disk_total_bytes" <<'PY'
from decimal import Decimal, ROUND_HALF_UP
import sys

used = Decimal(sys.argv[1])
total = Decimal(sys.argv[2])
print((used * 100 / total).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP))
PY
)"
  journal_filesystem_capacity_observed=true
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
    if not line:
        continue
    if line.startswith("#"):
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
approved_values = (
    settings.get("SystemMaxUse", "") == "8G"
    and settings.get("SystemKeepFree", "") == "50G"
    and settings.get("MaxRetentionSec", "") == "14day"
    and settings.get("MaxFileSec", "") == "1day"
)
print("approved_values=" + str(approved_values).lower())
')" || fail policy_parse

# 不信任 cat-config 的注释作为来源边界；直接枚举所有 journald 配置源，其他文件出现四项键即失败关闭。
policy_source_status="$(python3 - "$target" \
  /etc/systemd/journald.conf \
  /etc/systemd/journald.conf.d \
  /run/systemd/journald.conf.d \
  /usr/local/lib/systemd/journald.conf.d \
  /usr/lib/systemd/journald.conf.d <<'PY'
import glob
import os
import sys

target = os.path.abspath(sys.argv[1])
sources = []
for candidate in sys.argv[2:]:
    if os.path.isdir(candidate):
        sources.extend(sorted(glob.glob(os.path.join(candidate, "*.conf"))))
    elif os.path.isfile(candidate) or os.path.islink(candidate):
        sources.append(candidate)

expected = {
    "SystemMaxUse": "8G",
    "SystemKeepFree": "50G",
    "MaxRetentionSec": "14day",
    "MaxFileSec": "1day",
}
target_counts = {key: 0 for key in expected}
exclusive = True
target_seen = False

for path in sources:
    absolute = os.path.abspath(path)
    if absolute == target:
        target_seen = True
    try:
        with open(path, "r", encoding="utf-8", errors="strict") as handle:
            lines = handle.readlines()
    except (OSError, UnicodeError):
        exclusive = False
        continue
    section = ""
    for raw_line in lines:
        line = raw_line.strip()
        if not line or line.startswith("#") or line.startswith(";"):
            continue
        if line.startswith("[") and line.endswith("]"):
            section = line
            continue
        if section != "[Journal]" or "=" not in line:
            continue
        key, value = (part.strip() for part in line.split("=", 1))
        if key not in expected:
            continue
        if absolute != target:
            exclusive = False
            continue
        target_counts[key] += 1
        if value != expected[key]:
            exclusive = False

exclusive = exclusive and target_seen and all(count == 1 for count in target_counts.values())
print("source_is_exclusive=" + str(exclusive).lower())
PY
)" || fail policy_source_parse

marker_value() {
  printf '%s\n' "$policy_status" | awk -F= -v marker="$1" '$1 == marker {print $2}'
}

journald_storage_mode_persistent="$(marker_value storage_mode_persistent)"
journald_capacity_limit_configured="$(marker_value capacity)"
journald_keep_free_configured="$(marker_value keep_free)"
journald_retention_limit_configured="$(marker_value retention)"
journald_rotation_limit_configured="$(marker_value rotation)"
journald_policy_source_exclusive="$(printf '%s\n' "$policy_source_status" | awk -F= '$1 == "source_is_exclusive" {print $2}')"
journald_policy_values_match_approved=false
if [ "$(marker_value approved_values)" = true ] && [ "$journald_policy_source_exclusive" = true ]; then
  journald_policy_values_match_approved=true
fi

# 只接受受控脚本安装的固定 root:644 普通文件；符号链接或权限漂移均失败关闭。
journald_policy_file_identity_verified=false
if [ -f "$target" ] && [ ! -L "$target" ] \
  && [ "$(stat -c '%U:%a' "$target" 2>/dev/null || true)" = 'root:644' ]; then
  journald_policy_file_identity_verified=true
fi

log_retention_configuration_complete=false
if [ "$journald_active" = true ] \
  && [ "$journald_persistent_storage_present" = true ] \
  && [ "$journald_storage_mode_persistent" = true ] \
  && [ "$journal_disk_usage_observed" = true ] \
  && [ "$journal_filesystem_capacity_observed" = true ] \
  && [ "$journald_capacity_limit_configured" = true ] \
  && [ "$journald_keep_free_configured" = true ] \
  && [ "$journald_retention_limit_configured" = true ] \
  && [ "$journald_rotation_limit_configured" = true ]; then
  log_retention_configuration_complete=true
fi

# 当前 journald 的进入 active 时间必须不早于受控配置文件 mtime，证明进程至少在配置落盘后重启过。
log_retention_runtime_reload_verified=false
active_enter="$(systemctl show systemd-journald --property=ActiveEnterTimestamp --value 2>/dev/null || true)"
active_enter_epoch="$(date -d "$active_enter" +%s 2>/dev/null || true)"
policy_file_mtime="$(stat -c '%Y' "$target" 2>/dev/null || true)"
if [ "$journald_active" = true ] \
  && [ "$journald_policy_file_identity_verified" = true ] \
  && [ "$journald_policy_values_match_approved" = true ] \
  && [[ "$active_enter_epoch" =~ ^[0-9]+$ ]] \
  && [[ "$policy_file_mtime" =~ ^[0-9]+$ ]] \
  && [ "$active_enter_epoch" -ge "$policy_file_mtime" ]; then
  log_retention_runtime_reload_verified=true
fi

log_retention_policy_verified=false
log_retention_change_authorization_required=true
if [ "$log_retention_configuration_complete" = true ] \
  && [ "$journald_policy_values_match_approved" = true ] \
  && [ "$journald_policy_file_identity_verified" = true ] \
  && [ "$log_retention_runtime_reload_verified" = true ]; then
  log_retention_policy_verified=true
  log_retention_change_authorization_required=false
fi

printf 'log_retention_preflight=passed\n'
printf 'journald_active=%s\n' "$journald_active"
printf 'journald_persistent_storage_present=%s\n' "$journald_persistent_storage_present"
printf 'journald_storage_mode_persistent=%s\n' "$journald_storage_mode_persistent"
printf 'journal_disk_usage_observed=%s\n' "$journal_disk_usage_observed"
printf 'journal_filesystem_capacity_observed=%s\n' "$journal_filesystem_capacity_observed"
printf 'journal_disk_total_bytes=%s\n' "$journal_disk_total_bytes"
printf 'journal_disk_available_bytes=%s\n' "$journal_disk_available_bytes"
printf 'journal_directory_usage_bytes=%s\n' "$journal_directory_usage_bytes"
printf 'journal_directory_usage_percent=%s\n' "$journal_directory_usage_percent"
printf 'journald_capacity_limit_configured=%s\n' "$journald_capacity_limit_configured"
printf 'journald_keep_free_configured=%s\n' "$journald_keep_free_configured"
printf 'journald_retention_limit_configured=%s\n' "$journald_retention_limit_configured"
printf 'journald_rotation_limit_configured=%s\n' "$journald_rotation_limit_configured"
printf 'journald_policy_file_identity_verified=%s\n' "$journald_policy_file_identity_verified"
printf 'journald_policy_source_exclusive=%s\n' "$journald_policy_source_exclusive"
printf 'journald_policy_values_match_approved=%s\n' "$journald_policy_values_match_approved"
printf 'log_retention_configuration_complete=%s\n' "$log_retention_configuration_complete"
printf 'log_retention_runtime_reload_verified=%s\n' "$log_retention_runtime_reload_verified"
printf 'log_retention_policy_verified=%s\n' "$log_retention_policy_verified"
printf 'log_retention_change_authorization_required=%s\n' "$log_retention_change_authorization_required"
printf 'business_configuration_mutations=0\n'
printf 'access_audit_logs_may_increase=true\n'
printf 'real_sms_delivery_not_verified=true\n'
