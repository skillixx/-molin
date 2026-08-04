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

# 仅输出恢复相关布尔状态；禁止输出环境变量值、容器命令、挂载内容或镜像详情。
restore_static_status="$(python3 - "$backup/env.test" "$backup/molin-admin.inspect.json" "$backup/molin-user.inspect.json" "$backup/molin-prometheus.inspect.json" <<'PY'
import json
import re
import subprocess
import sys


def parse_env(path):
    values = {}
    syntax_valid = True
    with open(path, "r", encoding="utf-8") as stream:
        for raw_line in stream:
            line = raw_line.strip()
            if not line or line.startswith("#"):
                continue
            match = re.fullmatch(r"(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)", line)
            if match is None or match.group(1) in values:
                syntax_valid = False
                continue
            value = match.group(2).strip()
            if len(value) not in (0, 1) and value[0] == value[-1] and value[0] in "'\"":
                value = value[1:-1]
            values[match.group(1)] = value
    return syntax_valid, values


env_syntax_valid, env_values = parse_env(sys.argv[1])
base_keys = {
    "APP_ENV", "API_HOST", "API_PORT", "MYSQL_HOST", "MYSQL_PORT", "MYSQL_DATABASE",
    "MYSQL_USER", "MYSQL_PASSWORD", "REDIS_ADDR", "JWT_SECRET", "REFRESH_TOKEN_SECRET",
    "SMS_ENABLED", "SMS_TEST_MODE",
}
sms_closed = (
    env_values.get("SMS_ENABLED", "").strip().lower() == "false"
    and env_values.get("SMS_TEST_MODE", "").strip().lower() == "true"
)
trusted_items = {
    item.strip() for item in env_values.get("TRUSTED_PROXY_IPS", "").split(",") if item.strip()
}
fixed_proxy_compatible = (
    trusted_items == {"172.20.250.0/28"}
    or trusted_items == {"172.20.250.2", "172.20.250.3"}
)
legacy_template_keys_present = any(
    key.startswith("SMS_TEMPLATE_CODE_") for key in env_values
)

container_specs_verified = True
container_images_present = True
for path in sys.argv[2:]:
    with open(path, "r", encoding="utf-8") as stream:
        value = json.load(stream)
    item = value[0] if isinstance(value, list) and len(value) == 1 else value
    structure_valid = (
        isinstance(item, dict)
        and isinstance(item.get("Id"), str)
        and isinstance(item.get("Image"), str)
        and isinstance(item.get("Config"), dict)
        and isinstance(item.get("HostConfig"), dict)
        and isinstance(item.get("NetworkSettings"), dict)
        and isinstance(item.get("Config", {}).get("Image"), str)
        and isinstance(item.get("HostConfig", {}).get("RestartPolicy"), dict)
        and isinstance(item.get("NetworkSettings", {}).get("Networks"), dict)
    )
    container_specs_verified = container_specs_verified and structure_valid
    image_id = item.get("Image", "") if isinstance(item, dict) else ""
    image_id_valid = re.fullmatch(r"sha256:[a-f0-9]{64}", image_id) is not None
    image_present = image_id_valid and subprocess.run(
        ["docker", "image", "inspect", image_id],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    ).returncode == 0
    container_images_present = container_images_present and image_present

print(f"environment_syntax={str(env_syntax_valid).lower()}")
print(f"environment_base_keys={str(base_keys <= env_values.keys()).lower()}")
print(f"environment_sms_closed={str(sms_closed).lower()}")
print(f"environment_fixed_proxy_compatible={str(fixed_proxy_compatible).lower()}")
print(f"environment_legacy_template_keys_present={str(legacy_template_keys_present).lower()}")
print(f"container_specs={str(container_specs_verified).lower()}")
print(f"container_images={str(container_images_present).lower()}")
PY
 )" || fail restore_static_parse

marker_value() {
  printf '%s\n' "$restore_static_status" | awk -F= -v marker="$1" '$1 == marker {print $2}'
}
rollback_environment_syntax_verified="$(marker_value environment_syntax)"
rollback_environment_base_keys_verified="$(marker_value environment_base_keys)"
rollback_environment_sms_closed_verified="$(marker_value environment_sms_closed)"
rollback_environment_fixed_proxy_compatible="$(marker_value environment_fixed_proxy_compatible)"
rollback_environment_legacy_template_keys_present="$(marker_value environment_legacy_template_keys_present)"
rollback_container_specs_verified="$(marker_value container_specs)"
rollback_container_images_present="$(marker_value container_images)"

rollback_static_prerequisites_verified=false
rollback_static_blocker=none
rollback_environment_wholesale_restore_allowed=false
rollback_environment_restore_strategy=current_env_preserve_proxy_no_legacy_template_keys
if [ "$rollback_environment_syntax_verified" != true ]; then
  rollback_static_blocker=backup_env_syntax_invalid
elif [ "$rollback_environment_base_keys_verified" != true ]; then
  rollback_static_blocker=backup_env_base_keys_missing
elif [ "$rollback_environment_sms_closed_verified" != true ]; then
  rollback_static_blocker=backup_env_sms_not_closed
elif [ "$rollback_environment_fixed_proxy_compatible" != true ]; then
  rollback_static_blocker=backup_env_missing_fixed_proxy_trust
elif [ "$rollback_environment_legacy_template_keys_present" = true ]; then
  rollback_static_blocker=backup_env_contains_legacy_template_keys
elif [ "$rollback_container_specs_verified" != true ]; then
  rollback_static_blocker=backup_container_specs_invalid
elif [ "$rollback_container_images_present" != true ]; then
  rollback_static_blocker=backup_container_images_missing
else
  rollback_static_prerequisites_verified=true
  rollback_environment_wholesale_restore_allowed=true
  rollback_environment_restore_strategy=backup_env_verified
fi

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
printf 'rollback_environment_syntax_verified=%s\n' "$rollback_environment_syntax_verified"
printf 'rollback_environment_base_keys_verified=%s\n' "$rollback_environment_base_keys_verified"
printf 'rollback_environment_sms_closed_verified=%s\n' "$rollback_environment_sms_closed_verified"
printf 'rollback_environment_fixed_proxy_compatible=%s\n' "$rollback_environment_fixed_proxy_compatible"
printf 'rollback_environment_legacy_template_keys_present=%s\n' "$rollback_environment_legacy_template_keys_present"
printf 'rollback_environment_wholesale_restore_allowed=%s\n' "$rollback_environment_wholesale_restore_allowed"
printf 'rollback_environment_restore_strategy=%s\n' "$rollback_environment_restore_strategy"
printf 'rollback_container_specs_verified=%s\n' "$rollback_container_specs_verified"
printf 'rollback_container_images_present=%s\n' "$rollback_container_images_present"
printf 'rollback_static_prerequisites_verified=%s\n' "$rollback_static_prerequisites_verified"
printf 'rollback_static_blocker=%s\n' "$rollback_static_blocker"
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
