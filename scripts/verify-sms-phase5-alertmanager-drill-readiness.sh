#!/usr/bin/env bash
set -Eeuo pipefail

# 本脚本只读取 Alertmanager 关闭态与通知基线，不提交告警、不重载配置、不读取 Secret 内容。
deployment_dir='__DEPLOYMENT_DIR__'
container_name='__CONTAINER_NAME__'
alertmanager_port='__ALERTMANAGER_PORT__'
prometheus_port='__PROMETHEUS_PORT__'
expected_image_id='__EXPECTED_IMAGE_ID__'

for command_name in docker curl python3 awk stat pgrep tr; do
  command -v "$command_name" >/dev/null
done

[ -d "$deployment_dir" ] && [ ! -L "$deployment_dir" ]
[ "$(stat -c '%U:%G:%a' "$deployment_dir")" = 'pc:pc:700' ]
config_file="$deployment_dir/alertmanager.closed.yml"
secret_file="$deployment_dir/smtp_password"
[ -f "$config_file" ] && [ ! -L "$config_file" ]
[ -f "$secret_file" ] && [ ! -L "$secret_file" ] && [ -s "$secret_file" ]
[ "$(stat -c '%U:%G:%a' "$secret_file")" = 'pc:pc:400' ]

# 解析时只输出结构布尔值和计数，禁止输出接收地址、用户名或 Secret。
config_summary="$(python3 - "$config_file" <<'PY'
import re
import sys

text = open(sys.argv[1], encoding="utf-8").read()
route = re.search(r"(?ms)^route:\s*\n(?P<body>(?:^[ \t]+.*\n?)*)", text)
body = route.group("body") if route else ""
values = {
    "closed_route_discard_only": bool(re.search(r"(?m)^\s+receiver:\s*['\"]?discard['\"]?\s*$", body))
        and len(re.findall(r"(?m)^\s+routes:\s*$", body)) == 0,
    "receiver_configuration_loaded": bool(re.search(
        r"(?m)^\s*-\s+name:\s*['\"]?phase5-test-email['\"]?\s*$", text
    )),
    "inline_secret_count": len(re.findall(r"(?m)^\s*smtp_auth_password:\s*\S+", text)),
    "secret_file_ref_count": len(re.findall(r"(?m)^\s*smtp_auth_password_file:\s*\S+", text)),
}
for key, value in values.items():
    print(f"{key}={str(value).lower() if isinstance(value, bool) else value}")
PY
)"
printf '%s\n' "$config_summary" | grep -Fxq 'closed_route_discard_only=true'
printf '%s\n' "$config_summary" | grep -Fxq 'receiver_configuration_loaded=true'
printf '%s\n' "$config_summary" | grep -Fxq 'inline_secret_count=0'
printf '%s\n' "$config_summary" | grep -Fxq 'secret_file_ref_count=1'

[ "$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${alertmanager_port}/-/healthy")" = 200 ]
[ "$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${alertmanager_port}/-/ready")" = 200 ]
[ "$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${prometheus_port}/-/healthy")" = 200 ]
[ "$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${prometheus_port}/-/ready")" = 200 ]

[ "$(docker inspect "$container_name" --format '{{.State.Running}}')" = true ]
[ "$(docker inspect "$container_name" --format '{{.Image}}')" = "$expected_image_id" ]
[ "$(docker inspect "$container_name" --format '{{.HostConfig.ReadonlyRootfs}}')" = true ]
[ "$(docker inspect "$container_name" --format '{{.HostConfig.RestartPolicy.Name}}')" = unless-stopped ]
[ "$(docker inspect "$container_name" --format '{{json .HostConfig.CapDrop}}')" = '["ALL"]' ]
[ "$(docker inspect "$container_name" --format '{{json .HostConfig.SecurityOpt}}')" = '["no-new-privileges=true"]' ]
[ "$(docker inspect "$container_name" --format '{{(index (index .NetworkSettings.Ports "9093/tcp") 0).HostIp}}')" = '127.0.0.1' ]

alertmanager_active_alerts="$(curl -fsS --max-time 5 "http://127.0.0.1:${alertmanager_port}/api/v2/alerts" | python3 -c '
import json
import sys
items = json.load(sys.stdin)
print(len(items) if isinstance(items, list) else -1)
')"
notification_total="$(curl -fsS --max-time 5 "http://127.0.0.1:${alertmanager_port}/metrics" | awk '
/^alertmanager_notifications_total({| )/ {sum += $NF}
END {printf "%.0f\n", sum + 0}
')"
prometheus_summary="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/alertmanagers" | python3 -c '
import json
import sys
data = json.load(sys.stdin).get("data", {})
print(len(data.get("activeAlertmanagers", [])))
')"
prometheus_sms_alerts="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/alerts" | python3 -c '
import json
import sys
items = json.load(sys.stdin).get("data", {}).get("alerts", [])
print(sum(1 for item in items if str(item.get("labels", {}).get("alertname", "")).startswith("MolinSMS")))
')"
[ "$alertmanager_active_alerts" -eq 0 ]
[ "$notification_total" -eq 0 ]
[ "$prometheus_summary" -eq 1 ]
[ "$prometheus_sms_alerts" -eq 0 ]

mapfile -t api_pids < <(pgrep -f '^/home/pc/molin/molin-api$')
[ "${#api_pids[@]}" -eq 1 ]
api_environ="/proc/${api_pids[0]}/environ"
[ -r "$api_environ" ]
sms_enabled="$(tr '\0' '\n' < "$api_environ" | awk -F= '$1 == "SMS_ENABLED" {print tolower($2)}')"
sms_test_mode="$(tr '\0' '\n' < "$api_environ" | awk -F= '$1 == "SMS_TEST_MODE" {print tolower($2)}')"
[ "$sms_enabled" = false ]
[ "$sms_test_mode" = true ]

printf 'notification_drill_preflight=passed\n'
printf '%s\n' "$config_summary"
printf 'smtp_secret_file_secure=true\n'
printf 'alertmanager_health_ready=200:200\n'
printf 'prometheus_health_ready=200:200\n'
printf 'prometheus_active_alertmanagers=%s\n' "$prometheus_summary"
printf 'alertmanager_active_alerts=%s\n' "$alertmanager_active_alerts"
printf 'prometheus_sms_active_alerts=%s\n' "$prometheus_sms_alerts"
printf 'notification_baseline_total=%s\n' "$notification_total"
printf 'sms_enabled=%s\n' "$sms_enabled"
printf 'sms_test_mode=%s\n' "$sms_test_mode"
printf 'receiver_delivery_unverified=true\n'
printf 'notification_drill_execution_authorization_required=true\n'
printf 'business_configuration_mutations=0\n'
printf 'service_restarts=0\n'
printf 'notification_attempts=0\n'
printf 'notifications_sent=0\n'
printf 'real_sms_sent=0\n'
