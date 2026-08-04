#!/usr/bin/env bash
set -Eeuo pipefail
exec 2>/dev/null

# 本脚本只读取固定回滚点、当前 API 和监控进程；禁止写配置、重启、回滚、触发告警或发送短信。
backup='__BACKUP_PATH__'
expected_old_binary_sha256='__EXPECTED_OLD_BINARY_SHA256__'
expected_current_binary_sha256='__EXPECTED_CURRENT_BINARY_SHA256__'
expected_manifest_sha256='__EXPECTED_MANIFEST_SHA256__'
prometheus_port='__PROMETHEUS_PORT__'
api_path='/home/pc/molin/molin-api'

fail() {
  printf 'recovery_notification_preflight=failed\n'
  printf 'failure_stage=%s\n' "$1"
  printf 'business_configuration_mutations=0\n'
  printf 'access_audit_logs_may_increase=true\n'
  printf 'real_sms_sent=0\n'
  exit 2
}

# 备份目录必须保持部署时冻结的绝对路径、属主和权限，符号链接或路径漂移一律失败关闭。
[ -d "$backup" ] && [ ! -L "$backup" ] || fail backup_identity
[ "$(realpath -- "$backup")" = "$backup" ] || fail backup_path
[ "$(stat -c '%U:%a' "$backup")" = 'pc:700' ] || fail backup_permissions

expected_files="$(printf '%s\n' \
  SHA256SUMS \
  docker-networks.txt \
  email-alerts.yml \
  env.test \
  molin-admin.inspect.json \
  molin-api \
  molin-prometheus.inspect.json \
  molin-user.inspect.json \
  prometheus.yml \
  routes.txt \
  sms-tables.sql | LC_ALL=C sort)"
actual_files="$(find "$backup" -maxdepth 1 -type f -printf '%f\n' | LC_ALL=C sort)"
[ "$actual_files" = "$expected_files" ] || fail backup_file_set
[ -z "$(find "$backup" -maxdepth 1 -type l -print -quit)" ] || fail backup_symlink
[ -z "$(find "$backup" -maxdepth 1 -type f \( ! -user pc -o ! -perm 0600 \) -print -quit)" ] || fail backup_file_permissions
[ "$(sha256sum "$backup/SHA256SUMS" | awk '{print $1}')" = "$expected_manifest_sha256" ] || fail backup_manifest_anchor
(cd "$backup" && sha256sum -c SHA256SUMS >/dev/null) || fail backup_manifest

# 三份 inspect 必须仍是有效 JSON；这里只解析结构，不输出容器环境或其他原始内容。
python3 - "$backup/molin-admin.inspect.json" "$backup/molin-prometheus.inspect.json" "$backup/molin-user.inspect.json" <<'PY' >/dev/null || fail inspect_json
import json
import sys
for path in sys.argv[1:]:
    with open(path, "r", encoding="utf-8") as stream:
        value = json.load(stream)
    if not isinstance(value, (list, dict)):
        raise SystemExit(2)
PY

mapfile -t api_pids < <(pgrep -f "^${api_path}$" || true)
[ "${#api_pids[@]}" -eq 1 ] || fail current_api_process
[ -f "$api_path" ] && [ ! -L "$api_path" ] || fail current_api_identity
running_api_path="/proc/${api_pids[0]}/exe"
[ -e "$running_api_path" ] || fail current_running_api_identity
old_binary_sha256="$(sha256sum "$backup/molin-api" | awk '{print $1}')"
current_binary_sha256="$(sha256sum "$api_path" | awk '{print $1}')"
running_binary_sha256="$(sha256sum "$running_api_path" | awk '{print $1}')"
[ "$old_binary_sha256" = "$expected_old_binary_sha256" ] || fail old_binary_hash
[ "$current_binary_sha256" = "$expected_current_binary_sha256" ] || fail current_binary_hash
[ "$running_binary_sha256" = "$expected_current_binary_sha256" ] || fail current_running_binary_hash
file -b "$backup/molin-api" | grep -q 'ELF 64-bit.*x86-64' || fail old_binary_architecture
file -Lb "$running_api_path" | grep -q 'ELF 64-bit.*x86-64' || fail current_binary_architecture
api_listener="$(ss -lntpH 'sport = :8080' || true)"
printf '%s\n' "$api_listener" | grep -Fq "pid=${api_pids[0]}," || fail current_api_listener_owner
health_http="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health || true)"
ready_http="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready || true)"
[ "$health_http" = 200 ] && [ "$ready_http" = 200 ] || fail current_api_health

# 查询 Prometheus 实际加载配置，不以仓库文件推定运行态；只统计 Alertmanager 引用，不输出完整配置。
runtime_config="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/status/config")" || fail prometheus_runtime_config
prometheus_alertmanager_config_refs="$(printf '%s' "$runtime_config" | python3 -c '
import json
import re
import sys
data = json.load(sys.stdin)
yaml_text = str(data.get("data", {}).get("yaml", ""))
print(len(re.findall(r"(?m)^\s*alertmanagers\s*:", yaml_text)))
')" || fail prometheus_runtime_parse
prometheus_sms_rules="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/rules?type=alert" | python3 -c '
import json
import sys
data = json.load(sys.stdin)
print(sum(1 for group in data.get("data", {}).get("groups", []) for rule in group.get("rules", []) if str(rule.get("name", "")).startswith("MolinSMS")))
')" || fail prometheus_rule_query
[ "$prometheus_sms_rules" -eq 4 ] || fail prometheus_rule_count

alertmanager_containers="$(docker ps --format '{{.Names}} {{.Image}} {{.Command}}' | grep -Eic 'alertmanager' || true)"
alertmanager_processes="$(pgrep -fa '[/]alertmanager([[:space:]]|$)' | wc -l || true)"
alertmanager_listener_9093="$(ss -lntH | awk '$4 ~ /:9093$/{count++} END{print count+0}')"

notification_drill_ready=false
notification_configuration_authorization_required=true
if [ "$prometheus_alertmanager_config_refs" -eq 0 ] && [ "$alertmanager_containers" -eq 0 ] && [ "$alertmanager_processes" -eq 0 ] && [ "$alertmanager_listener_9093" -eq 0 ]; then
  notification_chain_status='receiver_configuration_required'
elif [ "$prometheus_alertmanager_config_refs" -gt 0 ] && { [ "$alertmanager_containers" -gt 0 ] || [ "$alertmanager_processes" -gt 0 ] || [ "$alertmanager_listener_9093" -gt 0 ]; }; then
  notification_chain_status='transport_present_receiver_unverified'
else
  notification_chain_status='partial_configuration_detected'
fi

printf 'recovery_notification_preflight=passed\n'
printf 'backup_directory_identity=pc:700\n'
printf 'backup_file_count=11\n'
printf 'backup_manifest_ok=true\n'
printf 'backup_symlink_count=0\n'
printf 'backup_unsafe_file_mode_count=0\n'
printf 'old_binary_sha_matches_expected=true\n'
printf 'current_binary_sha_matches_expected=true\n'
printf 'current_running_binary_sha_matches_expected=true\n'
printf 'current_api_listener_owner_verified=true\n'
printf 'old_current_arch_compatible=true\n'
printf 'current_health_ready=200:200\n'
printf 'rollback_materials_verified=true\n'
printf 'rollback_restore_runtime_verified=false\n'
printf 'actual_rollback_authorization_required=true\n'
printf 'prometheus_sms_rules=%s\n' "$prometheus_sms_rules"
printf 'prometheus_alertmanager_config_refs=%s\n' "$prometheus_alertmanager_config_refs"
printf 'alertmanager_containers=%s\n' "$alertmanager_containers"
printf 'alertmanager_processes=%s\n' "$alertmanager_processes"
printf 'alertmanager_listener_9093=%s\n' "$alertmanager_listener_9093"
printf 'notification_chain_status=%s\n' "$notification_chain_status"
printf 'notification_drill_ready=%s\n' "$notification_drill_ready"
printf 'notification_configuration_authorization_required=%s\n' "$notification_configuration_authorization_required"
printf 'business_configuration_mutations=0\n'
printf 'access_audit_logs_may_increase=true\n'
printf 'real_sms_sent=0\n'
