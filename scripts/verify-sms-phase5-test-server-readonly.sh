#!/usr/bin/env bash
set -u

# 本脚本仅做聚合只读检查；不得输出环境文件、内部 Token、数据库密码、手机号或验证码。
api_path='/home/pc/molin/molin-api'
prometheus_port='__PROMETHEUS_PORT__'
admin_port='__ADMIN_PORT__'
user_port='__USER_PORT__'
expected_trusted_proxy='__EXPECTED_TRUSTED_PROXY_CIDR__'
expected_network='__EXPECTED_PROXY_NETWORK__'
expected_subnet='__EXPECTED_PROXY_SUBNET__'
expected_admin_ip='__EXPECTED_ADMIN_IP__'
expected_user_ip='__EXPECTED_USER_IP__'
expected_binary_sha256='__EXPECTED_BINARY_SHA256__'
expected_whitelist_count='__EXPECTED_WHITELIST_COUNT__'
observation_seconds='__OBSERVATION_SECONDS__'

mapfile -t api_pids < <(pgrep -f "^${api_path}$" 2>/dev/null || true)
if [ "${#api_pids[@]}" -ne 1 ]; then
  printf 'api_process_count=%s\n' "${#api_pids[@]}"
  printf 'phase5_closed_state_release_ready=false\n'
  printf 'business_configuration_mutations=0\n'
  printf 'access_audit_logs_may_increase=true\n'
  printf 'real_sms_delivery_not_verified=true\n'
  exit 2
fi
pid="${api_pids[0]}"

read_env() {
  tr '\0' '\n' < "/proc/$pid/environ" | sed -n "s/^$1=//p" | tail -n 1
}

app_env="$(read_env APP_ENV)"
sms_enabled="$(read_env SMS_ENABLED)"
sms_test_mode="$(read_env SMS_TEST_MODE)"
sms_test_whitelist="$(read_env SMS_TEST_PHONE_WHITELIST)"
trusted_proxy="$(read_env TRUSTED_PROXY_IPS)"
internal_token="$(read_env INTERNAL_API_TOKEN)"
template_override_count="$(tr '\0' '\n' < "/proc/$pid/environ" | grep -c '^SMS_TEMPLATE_CODE_' || true)"
sms_test_whitelist_count="$(python3 -c 'import sys; items = [item.strip() for item in sys.stdin.read().split(",") if item.strip()]; print(len(set(items)))' <<<"$sms_test_whitelist")"
health_http="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health 2>/dev/null || true)"
ready_http="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)"
admin_health_http="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${admin_port}/api/health" 2>/dev/null || true)"
user_health_http="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${user_port}/api/health" 2>/dev/null || true)"
binary_sha256="$(sha256sum "/proc/$pid/exe" 2>/dev/null | awk '{print $1}')"

printf 'api_process=running\n'
printf 'api_pid_count=1\n'
printf 'app_env=%s\n' "$app_env"
printf 'sms_enabled=%s\n' "$sms_enabled"
printf 'sms_test_mode=%s\n' "$sms_test_mode"
printf 'sms_test_whitelist_count=%s\n' "$sms_test_whitelist_count"
printf 'sms_test_whitelist_count_matches_expected=%s\n' "$([ "$sms_test_whitelist_count" = "$expected_whitelist_count" ] && printf true || printf false)"
printf 'trusted_proxy_matches_expected=%s\n' "$([ "$trusted_proxy" = "$expected_trusted_proxy" ] && printf true || printf false)"
printf 'template_env_override_count=%s\n' "$template_override_count"
printf 'health_http=%s\n' "$health_http"
printf 'ready_http=%s\n' "$ready_http"
printf 'admin_proxy_health_http=%s\n' "$admin_health_http"
printf 'user_proxy_health_http=%s\n' "$user_health_http"
printf 'version_response_sha256=%s\n' "$(curl -fsS --max-time 3 http://127.0.0.1:8080/api/version 2>/dev/null | sha256sum | awk '{print $1}')"
printf 'binary_sha256=%s\n' "$binary_sha256"

db_host="$(read_env MYSQL_HOST)"
db_port="$(read_env MYSQL_PORT)"
db_user="$(read_env MYSQL_USER)"
db_pass="$(read_env MYSQL_PASSWORD)"
db_name="$(read_env MYSQL_DATABASE)"

run_mysql() {
  local statement="$1"
  if command -v mysql >/dev/null 2>&1; then
    MYSQL_PWD="$db_pass" mysql --batch --skip-column-names -h "$db_host" -P "${db_port:-3306}" -u "$db_user" "$db_name" -e "$statement" 2>/dev/null
    return
  fi
  if command -v docker >/dev/null 2>&1 && docker inspect molin-mysql >/dev/null 2>&1; then
    # 密码只经 stdin 进入容器进程环境，不得出现在宿主机 docker 命令参数中。
    printf '%s\n' "$db_pass" | docker exec -i molin-mysql sh -c '
      IFS= read -r MYSQL_PWD
      export MYSQL_PWD
      exec mysql --batch --skip-column-names -u "$1" "$2" -e "$3"
    ' sh "$db_user" "$db_name" "$statement" 2>/dev/null
  fi
}

schema="$(run_mysql "SELECT CONCAT(version,':',dirty) FROM schema_migrations LIMIT 1" || true)"
template_summary="$(run_mysql "SELECT CONCAT(COUNT(*),':',SUM(provider_audit_status='approved'),':',SUM(local_enabled=1)) FROM sms_templates" || true)"
binding_summary="$(run_mysql "SELECT CONCAT(COUNT(*),':',SUM(enabled=1),':',COUNT(DISTINCT CASE WHEN enabled=1 THEN template_id END)) FROM sms_scene_bindings WHERE scene IN ('register','login','reset_password','bind_phone','admin_verify')" || true)"
read_send_summary() {
  run_mysql "SELECT CONCAT(COUNT(*),':',SUM(submit_status='accepted'),':',SUM(submit_status='failed')) FROM sms_send_logs" || true
}
send_summary_before="$(read_send_summary)"

printf 'schema=%s\n' "$schema"
printf 'template_summary_total_approved_enabled=%s\n' "$template_summary"
printf 'binding_summary_total_enabled_distinct=%s\n' "$binding_summary"
printf 'send_summary_total_accepted_failed=%s\n' "$send_summary_before"

network_subnet=''
admin_ip=''
user_ip=''
if command -v docker >/dev/null 2>&1; then
  network_subnet="$(docker network inspect "$expected_network" --format '{{(index .IPAM.Config 0).Subnet}}' 2>/dev/null || true)"
  admin_ip="$(docker inspect molin-admin --format "{{(index .NetworkSettings.Networks \"${expected_network}\").IPAddress}}" 2>/dev/null || true)"
  user_ip="$(docker inspect molin-user --format "{{(index .NetworkSettings.Networks \"${expected_network}\").IPAddress}}" 2>/dev/null || true)"
fi
printf 'proxy_network_matches_expected=%s\n' "$([ "$network_subnet" = "$expected_subnet" ] && printf true || printf false)"
printf 'admin_fixed_ip_matches_expected=%s\n' "$([ "$admin_ip" = "$expected_admin_ip" ] && printf true || printf false)"
printf 'user_fixed_ip_matches_expected=%s\n' "$([ "$user_ip" = "$expected_user_ip" ] && printf true || printf false)"

metrics_http='unavailable'
metrics_text=''
read_internal_metrics() {
  # Header 从 stdin 读取，避免内部 Token 出现在 curl 进程参数或临时文件中。
  printf 'X-Internal-Token: %s\n' "$internal_token" |
    curl -fsS --max-time 5 -H @- http://127.0.0.1:8080/api/internal/metrics 2>/dev/null
}
if [ -n "$internal_token" ]; then
  # 响应正文和状态码都保留在内存变量中，远端不创建临时文件。
  metrics_response="$(printf 'X-Internal-Token: %s\n' "$internal_token" | curl -sS --max-time 5 -H @- -w '\n__HTTP_STATUS__:%{http_code}\n' http://127.0.0.1:8080/api/internal/metrics 2>/dev/null || true)"
  metrics_http="$(printf '%s\n' "$metrics_response" | sed -n 's/^__HTTP_STATUS__://p' | tail -n 1)"
  metrics_text="$(printf '%s\n' "$metrics_response" | sed '/^__HTTP_STATUS__:/d')"
fi
sms_calls_series="$(printf '%s\n' "$metrics_text" | grep -c '^sms_provider_calls_total{' || true)"
sms_duration_sum_series="$(printf '%s\n' "$metrics_text" | grep -c '^sms_provider_request_duration_seconds_sum{' || true)"
sms_duration_count_series="$(printf '%s\n' "$metrics_text" | grep -c '^sms_provider_request_duration_seconds_count{' || true)"
sensitive_metric_labels="$( (printf '%s\n' "$metrics_text" | grep -E '^sms_[a-z_]+\{[^}]*(phone|mobile|request_id|provider_request_id|token|secret)=' || true) | wc -l)"
read_provider_total() {
  if [ -n "$internal_token" ]; then
    read_internal_metrics |
      awk '/^sms_provider_calls_total\{/{sum += $NF} END{printf "%.0f", sum + 0}'
  fi
}
provider_total_before="$(read_provider_total)"

printf 'metrics_http=%s\n' "$metrics_http"
printf 'sms_provider_calls_series=%s\n' "$sms_calls_series"
printf 'sms_duration_sum_series=%s\n' "$sms_duration_sum_series"
printf 'sms_duration_count_series=%s\n' "$sms_duration_count_series"
printf 'sensitive_metric_labels=%s\n' "$sensitive_metric_labels"
printf 'sms_provider_metric_total_before=%s\n' "$provider_total_before"

prometheus_ready="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${prometheus_port}/-/ready" 2>/dev/null || true)"
prometheus_rules="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/rules?type=alert" 2>/dev/null | python3 -c 'import json,sys; d=json.load(sys.stdin); print(sum(1 for g in d.get("data",{}).get("groups",[]) for r in g.get("rules",[]) if str(r.get("name","")).startswith("MolinSMS")))' 2>/dev/null || true)"
prometheus_target_health="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/targets" 2>/dev/null | python3 -c 'import json,sys; d=json.load(sys.stdin); x=[t for t in d.get("data",{}).get("activeTargets",[]) if t.get("labels",{}).get("job")=="molin-email-adapter"]; print("missing" if not x else ",".join(sorted(set(t.get("health","") for t in x))))' 2>/dev/null || true)"
prometheus_sms_series="$(curl -fsS --max-time 5 --get --data-urlencode 'query=count(sms_provider_calls_total)' "http://127.0.0.1:${prometheus_port}/api/v1/query" 2>/dev/null | python3 -c 'import json,sys; r=json.load(sys.stdin).get("data",{}).get("result",[]); print("0" if not r else r[0]["value"][1])' 2>/dev/null || true)"
printf 'prometheus_ready_http=%s\n' "$prometheus_ready"
printf 'loaded_sms_alert_rules=%s\n' "${prometheus_rules:-unavailable}"
printf 'prometheus_target_health=%s\n' "${prometheus_target_health:-unavailable}"
printf 'prometheus_sms_series_count=%s\n' "${prometheus_sms_series:-unavailable}"

if [ "$observation_seconds" -gt 0 ]; then
  sleep "$observation_seconds"
fi
mapfile -t api_pids_after < <(pgrep -f "^${api_path}$" 2>/dev/null || true)
process_stable=false
if [ "${#api_pids_after[@]}" -eq 1 ] && [ "${api_pids_after[0]}" = "$pid" ] && [ -r "/proc/$pid/environ" ]; then
  process_stable=true
fi
sms_enabled_after='unavailable'
if [ "$process_stable" = true ]; then
  sms_enabled_after="$(read_env SMS_ENABLED)"
fi
health_http_after="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health 2>/dev/null || true)"
admin_health_http_after="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${admin_port}/api/health" 2>/dev/null || true)"
user_health_http_after="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${user_port}/api/health" 2>/dev/null || true)"
prometheus_target_health_after="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/targets" 2>/dev/null | python3 -c 'import json,sys; d=json.load(sys.stdin); x=[t for t in d.get("data",{}).get("activeTargets",[]) if t.get("labels",{}).get("job")=="molin-email-adapter"]; print("missing" if not x else ",".join(sorted(set(t.get("health","") for t in x))))' 2>/dev/null || true)"
send_summary_after="$(read_send_summary)"
provider_total_after="$(read_provider_total)"
printf 'observation_seconds=%s\n' "$observation_seconds"
printf 'observation_process_stable=%s\n' "$process_stable"
printf 'sms_enabled_after=%s\n' "$sms_enabled_after"
printf 'health_http_after=%s\n' "$health_http_after"
printf 'admin_proxy_health_http_after=%s\n' "$admin_health_http_after"
printf 'user_proxy_health_http_after=%s\n' "$user_health_http_after"
printf 'prometheus_target_health_after=%s\n' "${prometheus_target_health_after:-unavailable}"
printf 'send_summary_after=%s\n' "$send_summary_after"
printf 'sms_provider_metric_total_after=%s\n' "$provider_total_after"
printf 'observation_send_delta_zero=%s\n' "$([ -n "$send_summary_before" ] && [ "$send_summary_before" = "$send_summary_after" ] && printf true || printf false)"
printf 'observation_provider_delta_zero=%s\n' "$([ -n "$provider_total_before" ] && [ "$provider_total_before" = "$provider_total_after" ] && printf true || printf false)"

schema_version="${schema%%:*}"
schema_dirty="${schema#*:}"
release_ready=true
if [ "$app_env" != test ] || [ "$sms_enabled" != false ] || [ "$sms_test_mode" != true ]; then release_ready=false; fi
if [ "$sms_test_whitelist_count" != "$expected_whitelist_count" ]; then release_ready=false; fi
if [ "$trusted_proxy" != "$expected_trusted_proxy" ] || [ "$template_override_count" != 0 ]; then release_ready=false; fi
if [ "$health_http" != 200 ] || [ "$ready_http" != 200 ] || [ "$admin_health_http" != 200 ] || [ "$user_health_http" != 200 ]; then release_ready=false; fi
if ! [[ "$schema_version" =~ ^[0-9]+$ ]] || [ "$schema_version" -lt 59 ] || [ "$schema_dirty" != 0 ]; then release_ready=false; fi
if [ "$template_summary" != 5:5:5 ] || [ "$binding_summary" != 5:5:5 ] || [ -z "$send_summary_before" ]; then release_ready=false; fi
if [ "$network_subnet" != "$expected_subnet" ] || [ "$admin_ip" != "$expected_admin_ip" ] || [ "$user_ip" != "$expected_user_ip" ]; then release_ready=false; fi
if [ "$metrics_http" != 200 ] || [ "$sms_calls_series" != 40 ] || [ "$sms_duration_sum_series" != 5 ] || [ "$sms_duration_count_series" != 5 ] || [ "$sensitive_metric_labels" != 0 ]; then release_ready=false; fi
if [ "$prometheus_ready" != 200 ] || [ "$prometheus_rules" != 4 ] || [ "$prometheus_target_health" != up ] || [ "$prometheus_sms_series" != 40 ]; then release_ready=false; fi
if [ "$process_stable" != true ] || [ "$sms_enabled_after" != false ]; then release_ready=false; fi
if [ "$health_http_after" != 200 ] || [ "$admin_health_http_after" != 200 ] || [ "$user_health_http_after" != 200 ] || [ "$prometheus_target_health_after" != up ]; then release_ready=false; fi
if [ "$send_summary_before" != "$send_summary_after" ] || [ "$provider_total_before" != "$provider_total_after" ]; then release_ready=false; fi
if [ -n "$expected_binary_sha256" ] && [ "$binary_sha256" != "$expected_binary_sha256" ]; then release_ready=false; fi

printf 'phase5_closed_state_release_ready=%s\n' "$release_ready"
printf 'business_configuration_mutations=0\n'
printf 'access_audit_logs_may_increase=true\n'
printf 'real_sms_delivery_not_verified=true\n'
if [ "$release_ready" != true ]; then exit 3; fi
