#!/usr/bin/env bash
set -u

pid="$(pgrep -xo molin-api 2>/dev/null || true)"
if [ -z "$pid" ]; then
  printf 'api_process=missing\n'
  exit 2
fi

read_env() {
  tr '\0' '\n' < "/proc/$pid/environ" | sed -n "s/^$1=//p" | tail -n 1
}

app_env="$(read_env APP_ENV)"
sms_enabled="$(read_env SMS_ENABLED)"
sms_test_mode="$(read_env SMS_TEST_MODE)"
trusted_proxy="$(read_env TRUSTED_PROXY_IPS)"
health_http="$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health || true)"
ready_http="$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready || true)"

printf 'api_process=running\n'
printf 'api_pid_count=%s\n' "$(pgrep -xc molin-api 2>/dev/null || true)"
printf 'app_env=%s\n' "$app_env"
printf 'sms_enabled=%s\n' "$sms_enabled"
printf 'sms_test_mode=%s\n' "$sms_test_mode"
if [ -n "$trusted_proxy" ]; then printf 'trusted_proxy_configured=true\n'; else printf 'trusted_proxy_configured=false\n'; fi
printf 'health_http=%s\n' "$health_http"
printf 'ready_http=%s\n' "$ready_http"
printf 'version_response_sha256=%s\n' "$(curl -fsS http://127.0.0.1:8080/api/version 2>/dev/null | sha256sum | awk '{print $1}')"
printf 'binary_sha256=%s\n' "$(sha256sum "/proc/$pid/exe" 2>/dev/null | awk '{print $1}')"

db_host="$(read_env MYSQL_HOST)"
db_port="$(read_env MYSQL_PORT)"
db_user="$(read_env MYSQL_USER)"
db_pass="$(read_env MYSQL_PASSWORD)"
db_name="$(read_env MYSQL_DATABASE)"
schema=""
template_summary=""
binding_summary=""
send_summary=""
if command -v mysql >/dev/null 2>&1 && [ -n "$db_host" ] && [ -n "$db_user" ] && [ -n "$db_name" ]; then
  export MYSQL_PWD="$db_pass"
  mysql_args=(--batch --skip-column-names -h "$db_host" -P "${db_port:-3306}" -u "$db_user" "$db_name")
  schema="$(mysql "${mysql_args[@]}" -e "SELECT CONCAT(version,':',dirty) FROM schema_migrations LIMIT 1" 2>/dev/null || true)"
  template_summary="$(mysql "${mysql_args[@]}" -e "SELECT CONCAT(COUNT(*),':',SUM(provider_audit_status='approved'),':',SUM(local_enabled=1)) FROM sms_templates" 2>/dev/null || true)"
  binding_summary="$(mysql "${mysql_args[@]}" -e "SELECT CONCAT(COUNT(*),':',SUM(enabled=1),':',COUNT(DISTINCT CASE WHEN enabled=1 THEN template_id END)) FROM sms_scene_bindings WHERE scene IN ('register','login','reset_password','bind_phone','admin_verify')" 2>/dev/null || true)"
  send_summary="$(mysql "${mysql_args[@]}" -e "SELECT CONCAT(SUM(submit_status='accepted'),':',SUM(submit_status='failed')) FROM sms_send_logs" 2>/dev/null || true)"
  unset MYSQL_PWD db_pass
fi
printf 'schema=%s\n' "$schema"
printf 'template_summary_total_approved_enabled=%s\n' "$template_summary"
printf 'binding_summary_total_enabled_distinct=%s\n' "$binding_summary"
printf 'send_summary_accepted_failed=%s\n' "$send_summary"

printf 'docker_networks_begin\n'
if command -v docker >/dev/null 2>&1; then
  for network in $(docker network ls --format '{{.Name}}'); do
    docker network inspect "$network" --format '{{.Name}} {{range .IPAM.Config}}{{.Subnet}} {{end}}' 2>/dev/null || true
  done
fi
printf 'docker_networks_end\n'

prometheus_port="__PROMETHEUS_PORT__"
prometheus_rules="$(curl -fsS "http://127.0.0.1:${prometheus_port}/api/v1/rules" 2>/dev/null | python3 -c 'import json,sys; d=json.load(sys.stdin); print(sum(1 for g in d.get("data",{}).get("groups",[]) for r in g.get("rules",[]) if str(r.get("name","")).startswith("MolinSMS")))' 2>/dev/null || true)"
if [ -n "$prometheus_rules" ]; then printf 'loaded_sms_alert_rules=%s\n' "$prometheus_rules"; else printf 'loaded_sms_alert_rules=unavailable\n'; fi

release_ready=true
if [ "$app_env" != "test" ] || [ "$sms_enabled" != "false" ] || [ "$sms_test_mode" != "true" ]; then release_ready=false; fi
if [ "$health_http" != "200" ] || [ "$ready_http" != "200" ]; then release_ready=false; fi
if [ -z "$trusted_proxy" ] || [ "$template_summary" != "5:5:5" ] || [ "$binding_summary" != "5:5:5" ]; then release_ready=false; fi
if [ "$prometheus_rules" != "4" ]; then release_ready=false; fi
printf 'phase5_closed_state_release_ready=%s\n' "$release_ready"
printf 'remote_mutations=0\n'
printf 'real_sms_sent=0\n'
