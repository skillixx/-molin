param(
    [string]$ChangeId = "",
    [string]$TargetCandidateFile = "",
    [string]$ExpectedTargetCandidateSHA256 = "",
    [string]$OutputDirectory = "",
    [switch]$ExportCandidate,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"

function Assert-LocalFileSystemPathInput {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Description
    )

    # 在任何文件系统解析前拒绝 UNC、Provider 路径和网络映射盘，防止候选生成意外联网。
    if ([string]::IsNullOrWhiteSpace($Path) -or $Path -match '^(?:\\\\|//)' -or $Path.Contains("::")) {
        throw "${Description}必须是本地文件系统绝对路径"
    }
    $isWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
    if ($isWindowsPlatform) {
        if ($Path -cnotmatch '^[A-Za-z]:[\\/]') { throw "Windows ${Description}必须使用本地盘符绝对路径" }
        $drive = Get-PSDrive -Name $Path.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
        if (([string]$drive.Root).StartsWith("\\") -or ([string]$drive.DisplayRoot).StartsWith("\\")) {
            throw "${Description}不得使用网络映射盘"
        }
    }
    elseif (-not [IO.Path]::IsPathRooted($Path)) {
        throw "${Description}必须使用本地绝对路径"
    }
}

function Assert-TargetCandidate {
    param([Parameter(Mandatory = $true)][psobject]$Candidate)

    $allowedKeys = @(
        "schema_version", "change_id", "environment", "target_alias", "server_host", "ssh_port", "ssh_user",
        "expected_ed25519_fingerprint", "project_root", "environment_file", "service_kind", "api_service_identifier",
        "api_local_port", "prometheus_local_port", "alertmanager_local_port", "rollback_operator_alias", "observer_alias",
        "expected_sms_enabled", "expected_sms_test_mode", "readonly_baseline_requires_separate_approval",
        "deployment_requires_separate_approval", "canary_requires_separate_approval",
        "production_enable_requires_separate_approval", "automatic_retries", "business_posts", "real_sms_sent"
    )
    $actualKeys = @($Candidate.PSObject.Properties.Name)
    if (@($actualKeys | Where-Object { $_ -cnotin $allowedKeys }).Count -ne 0 -or
        @($allowedKeys | Where-Object { $_ -cnotin $actualKeys }).Count -ne 0) {
        throw "生产目标候选字段集合不符合冻结契约"
    }
    if ($Candidate.schema_version -ne 1 -or $Candidate.change_id -cnotmatch '^[0-9]{8}T[0-9]{6}Z$' -or
        $Candidate.environment -cne "production" -or $Candidate.target_alias -cnotmatch '^[a-z][a-z0-9-]{2,31}$') {
        throw "生产目标候选基础身份无效"
    }
    if ($Candidate.server_host -cnotmatch '^[A-Za-z0-9.-]+$' -or $Candidate.server_host.Contains("..") -or
        $Candidate.ssh_port -lt 1 -or $Candidate.ssh_port -gt 65535 -or
        $Candidate.ssh_user -cnotmatch '^[a-z_][a-z0-9_-]{0,31}$' -or
        $Candidate.expected_ed25519_fingerprint -cnotmatch '^SHA256:[A-Za-z0-9+/]{43}$') {
        throw "生产目标候选 SSH 身份无效"
    }
    try { $fingerprintBytes = [Convert]::FromBase64String($Candidate.expected_ed25519_fingerprint.Substring(7) + "=") }
    catch { throw "生产目标候选 ED25519 指纹不是有效 Base64" }
    if ($fingerprintBytes.Length -ne 32 -or @($fingerprintBytes | Where-Object { $_ -ne 0 }).Count -eq 0) {
        throw "生产目标候选 ED25519 指纹长度无效或属于弱占位值"
    }
    if ($Candidate.project_root -cnotmatch '^/[A-Za-z0-9._/-]+$' -or
        $Candidate.environment_file -cnotmatch '^/[A-Za-z0-9._/-]+$' -or
        $Candidate.project_root.Contains("//") -or $Candidate.environment_file.Contains("//") -or
        @($Candidate.project_root.Split('/') + $Candidate.environment_file.Split('/') | Where-Object { $_ -in @(".", "..") }).Count -ne 0 -or
        $Candidate.environment_file -cne ($Candidate.project_root.TrimEnd('/') + "/.env.prod")) {
        throw "生产目标候选路径无效"
    }
    switch ($Candidate.service_kind) {
        "host-binary" {
            if ($Candidate.api_service_identifier -cnotmatch '^/[A-Za-z0-9._/-]+$' -or
                -not $Candidate.api_service_identifier.StartsWith($Candidate.project_root.TrimEnd('/') + "/", [StringComparison]::Ordinal)) {
                throw "生产目标候选二进制身份无效"
            }
        }
        "systemd" {
            if ($Candidate.api_service_identifier -cnotmatch '^[A-Za-z0-9_.@-]+\.service$') {
                throw "生产目标候选 systemd 身份无效"
            }
        }
        "docker-compose" {
            if ($Candidate.api_service_identifier -cnotmatch '^[A-Za-z0-9_.-]+$') {
                throw "生产目标候选 Compose 身份无效"
            }
        }
        default { throw "生产目标候选服务形态无效" }
    }
    foreach ($port in @($Candidate.api_local_port, $Candidate.prometheus_local_port, $Candidate.alertmanager_local_port)) {
        if ($port -lt 1 -or $port -gt 65535) { throw "生产目标候选本机端口无效" }
    }
    if (@(@($Candidate.api_local_port, $Candidate.prometheus_local_port, $Candidate.alertmanager_local_port) |
            Select-Object -Unique).Count -ne 3) {
        throw "生产目标候选本机端口必须互异"
    }
    if ($Candidate.rollback_operator_alias -cnotmatch '^[a-z][a-z0-9-]{2,31}$' -or
        $Candidate.observer_alias -cnotmatch '^[a-z][a-z0-9-]{2,31}$') {
        throw "生产目标候选操作者别名无效"
    }
    if ($Candidate.expected_sms_enabled -ne $false -or $Candidate.expected_sms_test_mode -ne $true -or
        $Candidate.readonly_baseline_requires_separate_approval -ne $true -or
        $Candidate.deployment_requires_separate_approval -ne $true -or
        $Candidate.canary_requires_separate_approval -ne $true -or
        $Candidate.production_enable_requires_separate_approval -ne $true -or
        $Candidate.automatic_retries -ne 0 -or $Candidate.business_posts -ne 0 -or $Candidate.real_sms_sent -ne 0) {
        throw "生产目标候选关闭态或独立授权边界无效"
    }
}

function New-RemotePayload {
    param([Parameter(Mandatory = $true)][psobject]$Candidate)

    $payload = @'
#!/usr/bin/env bash
set -u

# 远端负载只读取环境、进程、健康、数据库聚合与监控状态，不输出任何配置值或敏感正文。
project_root='__PROJECT_ROOT__'
environment_file='__ENVIRONMENT_FILE__'
service_kind='__SERVICE_KIND__'
service_identifier='__SERVICE_IDENTIFIER__'
api_port='__API_PORT__'
prometheus_port='__PROMETHEUS_PORT__'
alertmanager_port='__ALERTMANAGER_PORT__'

bool() { if "$@"; then printf true; else printf false; fi; }
read_file_env() {
  awk -v key="$1" '
    $0 !~ /^[[:space:]]*#/ && index($0, "=") > 0 {
      name=substr($0, 1, index($0, "=")-1); gsub(/^[[:space:]]+|[[:space:]]+$/, "", name)
      if (name == key) {
        value=substr($0, index($0, "=")+1); gsub(/\r$/, "", value); gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
        if ((substr(value,1,1) == "\"" && substr(value,length(value),1) == "\"") ||
            (substr(value,1,1) == "\047" && substr(value,length(value),1) == "\047")) {
          value=substr(value,2,length(value)-2)
        }
        last=value
      }
    }
    END { print last }
  ' "$environment_file" 2>/dev/null
}

service_running=false
pid=''
runtime_env=''
case "$service_kind" in
  host-binary)
    mapfile -t pids < <(pgrep -f "^${service_identifier}$" 2>/dev/null || true)
    if [ "${#pids[@]}" -eq 1 ]; then pid="${pids[0]}"; service_running=true; fi
    ;;
  systemd)
    pid="$(systemctl show "$service_identifier" --property MainPID --value 2>/dev/null || true)"
    if [ "$(systemctl is-active "$service_identifier" 2>/dev/null || true)" = active ] && [[ "$pid" =~ ^[1-9][0-9]*$ ]]; then service_running=true; fi
    ;;
  docker-compose)
    compose_file="${project_root}/infra/docker-compose.prod.yml"
    mapfile -t containers < <(docker compose --project-directory "$project_root" -f "$compose_file" ps -q "$service_identifier" 2>/dev/null || true)
    if [ "${#containers[@]}" -eq 1 ] && [ "$(docker inspect --format '{{.State.Running}}' "${containers[0]}" 2>/dev/null || true)" = true ]; then
      pid="$(docker inspect --format '{{.State.Pid}}' "${containers[0]}" 2>/dev/null || true)"
      runtime_env="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${containers[0]}" 2>/dev/null || true)"
      service_running=true
    fi
    ;;
esac
if [ -z "$runtime_env" ] && [[ "$pid" =~ ^[1-9][0-9]*$ ]] && [ -r "/proc/${pid}/environ" ]; then
  runtime_env="$(tr '\0' '\n' < "/proc/${pid}/environ" 2>/dev/null || true)"
fi
runtime_value() { printf '%s\n' "$runtime_env" | sed -n "s/^$1=//p" | tail -n 1; }

environment_file_secure=false
if [ -f "$environment_file" ] && [ ! -L "$environment_file" ]; then
  mode="$(stat -c '%a' "$environment_file" 2>/dev/null || true)"
  if [[ "$mode" =~ ^[0-7]{3,4}$ ]] && [ $((8#$mode & 077)) -eq 0 ]; then environment_file_secure=true; fi
fi

app_env="$(read_file_env APP_ENV)"
sms_enabled="$(read_file_env SMS_ENABLED)"
sms_test_mode="$(read_file_env SMS_TEST_MODE)"
sms_provider="$(read_file_env SMS_PROVIDER)"
sms_endpoint="$(read_file_env SMS_ALIYUN_ENDPOINT)"
required_sms_config_present=true
for key in SMS_ALIYUN_ACCESS_KEY_ID SMS_ALIYUN_ACCESS_KEY_SECRET SMS_ALIYUN_SIGN_NAME SMS_PHONE_HMAC_SECRET INTERNAL_API_TOKEN; do
  if [ -z "$(read_file_env "$key")" ]; then required_sms_config_present=false; fi
done
legacy_count="$(grep -Ec '^[[:space:]]*(SMS_ACCESS_KEY|SMS_ACCESS_SECRET|SMS_SIGN_NAME)=' "$environment_file" 2>/dev/null || true)"
template_override_count="$(grep -Ec '^[[:space:]]*SMS_TEMPLATE_CODE_' "$environment_file" 2>/dev/null || true)"
duplicate_sms_config_count="$(awk '
  $0 !~ /^[[:space:]]*#/ && index($0,"=") > 0 {
    key=substr($0,1,index($0,"=")-1); gsub(/^[[:space:]]+|[[:space:]]+$/, "", key)
    if (key ~ /^(APP_ENV|SMS_ENABLED|SMS_TEST_MODE|SMS_PROVIDER|SMS_ALIYUN_ENDPOINT|SMS_ALIYUN_ACCESS_KEY_ID|SMS_ALIYUN_ACCESS_KEY_SECRET|SMS_ALIYUN_SIGN_NAME|SMS_PHONE_HMAC_SECRET|INTERNAL_API_TOKEN)$/) seen[key]++
  }
  END { duplicates=0; for (key in seen) if (seen[key] != 1) duplicates++; print duplicates }
' "$environment_file" 2>/dev/null || true)"
process_environment_readable=false
file_process_sms_config_match=false
if [ -n "$runtime_env" ]; then
  process_environment_readable=true
  file_process_sms_config_match=true
  for key in APP_ENV SMS_ENABLED SMS_TEST_MODE SMS_PROVIDER SMS_ALIYUN_ENDPOINT SMS_ALIYUN_ACCESS_KEY_ID SMS_ALIYUN_ACCESS_KEY_SECRET SMS_ALIYUN_SIGN_NAME SMS_PHONE_HMAC_SECRET; do
    if [ "$(read_file_env "$key")" != "$(runtime_value "$key")" ]; then file_process_sms_config_match=false; fi
  done
fi

health_http="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${api_port}/api/health" 2>/dev/null || true)"
ready_http="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${api_port}/api/ready" 2>/dev/null || true)"

db_host="$(runtime_value MYSQL_HOST)"; db_port="$(runtime_value MYSQL_PORT)"; db_user="$(runtime_value MYSQL_USER)"
db_pass="$(runtime_value MYSQL_PASSWORD)"; db_name="$(runtime_value MYSQL_DATABASE)"
run_mysql() {
  local statement="$1"
  if command -v mysql >/dev/null 2>&1; then
    MYSQL_PWD="$db_pass" mysql --batch --skip-column-names -h "$db_host" -P "${db_port:-3306}" -u "$db_user" "$db_name" -e "$statement" 2>/dev/null
    return
  fi
  if [ "$service_kind" = docker-compose ] && command -v docker >/dev/null 2>&1; then
    local mysql_container
    mysql_container="$(docker compose --project-directory "$project_root" -f "${project_root}/infra/docker-compose.prod.yml" ps -q mysql 2>/dev/null || true)"
    if [ -n "$mysql_container" ]; then
      printf '%s\n' "$db_pass" | docker exec -i "$mysql_container" sh -c '
        IFS= read -r MYSQL_PWD; export MYSQL_PWD
        exec mysql --batch --skip-column-names -u "$1" "$2" -e "$3"
      ' sh "$db_user" "$db_name" "$statement" 2>/dev/null
    fi
  fi
}
schema="$(run_mysql "SELECT CONCAT(version,':',dirty) FROM schema_migrations LIMIT 1" || true)"
template_summary="$(run_mysql "SELECT CONCAT(COUNT(*),':',SUM(provider_audit_status='approved'),':',SUM(local_enabled=1)) FROM sms_templates" || true)"
binding_summary="$(run_mysql "SELECT CONCAT(COUNT(*),':',SUM(enabled=1),':',COUNT(DISTINCT CASE WHEN enabled=1 THEN template_id END)) FROM sms_scene_bindings WHERE scene IN ('register','login','reset_password','bind_phone','admin_verify')" || true)"
send_summary="$(run_mysql "SELECT CONCAT(COUNT(*),':',SUM(submit_status='accepted'),':',SUM(submit_status='failed')) FROM sms_send_logs" || true)"
IFS=: read -r schema_version schema_dirty <<<"$schema"
IFS=: read -r template_total template_approved template_enabled <<<"$template_summary"
IFS=: read -r binding_total binding_enabled binding_distinct_templates <<<"$binding_summary"
IFS=: read -r send_total send_accepted send_failed <<<"$send_summary"

internal_token="$(runtime_value INTERNAL_API_TOKEN)"
metrics_response=''; metrics_http='unavailable'; metrics_text=''
if [ -n "$internal_token" ]; then
  metrics_response="$(printf 'X-Internal-Token: %s\n' "$internal_token" | curl -sS --max-time 5 -H @- -w '\n__HTTP_STATUS__:%{http_code}\n' "http://127.0.0.1:${api_port}/api/internal/metrics" 2>/dev/null || true)"
  metrics_http="$(printf '%s\n' "$metrics_response" | sed -n 's/^__HTTP_STATUS__://p' | tail -n 1)"
  metrics_text="$(printf '%s\n' "$metrics_response" | sed '/^__HTTP_STATUS__:/d')"
fi
sms_calls_series="$(printf '%s\n' "$metrics_text" | grep -c '^sms_provider_calls_total{' || true)"
sms_duration_sum_series="$(printf '%s\n' "$metrics_text" | grep -c '^sms_provider_request_duration_seconds_sum{' || true)"
sms_duration_count_series="$(printf '%s\n' "$metrics_text" | grep -c '^sms_provider_request_duration_seconds_count{' || true)"
sensitive_metric_labels="$( (printf '%s\n' "$metrics_text" | grep -E '^sms_[a-z_]+\{[^}]*(phone|mobile|request_id|provider_request_id|token|secret)=' || true) | wc -l | tr -d ' ')"

prometheus_ready_http="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${prometheus_port}/-/ready" 2>/dev/null || true)"
prometheus_rules="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/rules?type=alert" 2>/dev/null | python3 -c 'import json,sys; d=json.load(sys.stdin); print(sum(1 for g in d.get("data",{}).get("groups",[]) for r in g.get("rules",[]) if str(r.get("name","")).startswith("MolinSMS")))' 2>/dev/null || true)"
prometheus_target_health="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/targets" 2>/dev/null | python3 -c 'import json,sys; d=json.load(sys.stdin); x=[t for t in d.get("data",{}).get("activeTargets",[]) if t.get("labels",{}).get("job")=="molin-email-adapter"]; print("missing" if not x else ",".join(sorted(set(t.get("health","") for t in x))))' 2>/dev/null || true)"
active_sms_alerts="$(curl -fsS --max-time 5 "http://127.0.0.1:${prometheus_port}/api/v1/alerts" 2>/dev/null | python3 -c 'import json,sys; d=json.load(sys.stdin); print(sum(1 for a in d.get("data",{}).get("alerts",[]) if str(a.get("labels",{}).get("alertname","")).startswith("MolinSMS") and a.get("state") in {"pending","firing"}))' 2>/dev/null || true)"
notification_failures_total="$(curl -fsS --max-time 5 --get --data-urlencode 'query=sum(alertmanager_notifications_failed_total)' "http://127.0.0.1:${prometheus_port}/api/v1/query" 2>/dev/null | python3 -c 'import json,sys; r=json.load(sys.stdin).get("data",{}).get("result",[]); print("0" if not r else r[0]["value"][1])' 2>/dev/null || true)"
alertmanager_ready_http="$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${alertmanager_port}/-/ready" 2>/dev/null || true)"

schema_ready=false
if [[ "$schema" =~ ^[0-9]+:0$ ]] && [ "${schema%%:*}" -ge 59 ]; then schema_ready=true; fi
template_bindings_ready=false
if [ "$template_summary" = 5:5:5 ] && [ "$binding_summary" = 5:5:5 ]; then template_bindings_ready=true; fi
send_log_readable=false
if [[ "$send_summary" =~ ^[0-9]+:[0-9]+:[0-9]+$ ]]; then send_log_readable=true; fi
sms_metric_shape_ready=false
if [ "$sms_calls_series" = 40 ] && [ "$sms_duration_sum_series" = 5 ] && [ "$sms_duration_count_series" = 5 ] && [ "$sensitive_metric_labels" = 0 ]; then sms_metric_shape_ready=true; fi

production_readonly_baseline=passed
if [ "$environment_file_secure" != true ] || [ "$service_running" != true ] || [ "$process_environment_readable" != true ] ||
   [ "$file_process_sms_config_match" != true ] || [ "$app_env" != production ] || [ "$sms_enabled" != false ] ||
   [ "$sms_test_mode" != true ] || [ "$sms_provider" != aliyun ] || [ "$sms_endpoint" != dysmsapi.aliyuncs.com ] ||
   [ "$required_sms_config_present" != true ] || [ "$legacy_count" != 0 ] || [ "$template_override_count" != 0 ] ||
   [ "$duplicate_sms_config_count" != 0 ] ||
   [ "$health_http" != 200 ] || [ "$ready_http" != 200 ] || [ "$schema_ready" != true ] ||
   [ "$template_bindings_ready" != true ] || [ "$send_log_readable" != true ] || [ "$metrics_http" != 200 ] ||
   [ "$sms_metric_shape_ready" != true ] || [ "$prometheus_ready_http" != 200 ] || [ "$prometheus_rules" != 4 ] ||
   [ "$prometheus_target_health" != up ] || [ "${active_sms_alerts:-unavailable}" != 0 ] ||
   [ "${notification_failures_total:-unavailable}" != 0 ] || [ "$alertmanager_ready_http" != 200 ]; then
  production_readonly_baseline=blocked
fi

printf 'production_readonly_baseline=%s\n' "$production_readonly_baseline"
printf 'app_env_production=%s\n' "$(bool test "$app_env" = production)"
printf 'sms_enabled_false=%s\n' "$(bool test "$sms_enabled" = false)"
printf 'sms_test_mode_true=%s\n' "$(bool test "$sms_test_mode" = true)"
printf 'provider_aliyun=%s\n' "$(bool test "$sms_provider" = aliyun)"
printf 'endpoint_official=%s\n' "$(bool test "$sms_endpoint" = dysmsapi.aliyuncs.com)"
printf 'required_sms_config_present=%s\n' "$required_sms_config_present"
printf 'legacy_sms_keys_absent=%s\n' "$(bool test "$legacy_count" = 0)"
printf 'template_env_overrides_absent=%s\n' "$(bool test "$template_override_count" = 0)"
printf 'duplicate_sms_config_absent=%s\n' "$(bool test "$duplicate_sms_config_count" = 0)"
printf 'environment_file_secure=%s\n' "$environment_file_secure"
printf 'service_running=%s\n' "$service_running"
printf 'process_environment_readable=%s\n' "$process_environment_readable"
printf 'file_process_sms_config_match=%s\n' "$file_process_sms_config_match"
printf 'health_ready=%s\n' "$(bool test "$health_http" = 200 -a "$ready_http" = 200)"
printf 'schema_ready=%s\n' "$schema_ready"
printf 'schema_version=%s\n' "${schema_version:-unavailable}"
printf 'schema_dirty=%s\n' "${schema_dirty:-unavailable}"
printf 'template_bindings_ready=%s\n' "$template_bindings_ready"
printf 'template_total=%s\n' "${template_total:-unavailable}"
printf 'template_approved=%s\n' "${template_approved:-unavailable}"
printf 'template_enabled=%s\n' "${template_enabled:-unavailable}"
printf 'binding_total=%s\n' "${binding_total:-unavailable}"
printf 'binding_enabled=%s\n' "${binding_enabled:-unavailable}"
printf 'binding_distinct_templates=%s\n' "${binding_distinct_templates:-unavailable}"
printf 'send_log_readable=%s\n' "$send_log_readable"
printf 'send_total=%s\n' "${send_total:-unavailable}"
printf 'send_accepted=%s\n' "${send_accepted:-unavailable}"
printf 'send_failed=%s\n' "${send_failed:-unavailable}"
printf 'metrics_ready=%s\n' "$(bool test "$metrics_http" = 200)"
printf 'sms_metric_shape_ready=%s\n' "$sms_metric_shape_ready"
printf 'prometheus_ready=%s\n' "$(bool test "$prometheus_ready_http" = 200)"
printf 'sms_alert_rules_loaded=%s\n' "$(bool test "$prometheus_rules" = 4)"
printf 'prometheus_target_up=%s\n' "$(bool test "$prometheus_target_health" = up)"
printf 'active_sms_alerts=%s\n' "${active_sms_alerts:-unavailable}"
printf 'notification_failures_total=%s\n' "${notification_failures_total:-unavailable}"
printf 'alertmanager_ready=%s\n' "$(bool test "$alertmanager_ready_http" = 200)"
printf 'rollback_operator_declared=true\n'
printf 'observer_declared=true\n'
printf 'backup_capability_verified=false\n'
printf 'configuration_mutations=0\n'
printf 'service_operations=0\n'
printf 'business_posts=0\n'
printf 'uploads=0\n'
printf 'emails_sent=0\n'
printf 'real_sms_sent=0\n'
if [ "$production_readonly_baseline" != passed ]; then exit 3; fi
'@

    $replacements = [ordered]@{
        "__PROJECT_ROOT__" = [string]$Candidate.project_root
        "__ENVIRONMENT_FILE__" = [string]$Candidate.environment_file
        "__SERVICE_KIND__" = [string]$Candidate.service_kind
        "__SERVICE_IDENTIFIER__" = [string]$Candidate.api_service_identifier
        "__API_PORT__" = [string]$Candidate.api_local_port
        "__PROMETHEUS_PORT__" = [string]$Candidate.prometheus_local_port
        "__ALERTMANAGER_PORT__" = [string]$Candidate.alertmanager_local_port
    }
    foreach ($entry in $replacements.GetEnumerator()) {
        if ([regex]::Matches($payload, [regex]::Escape($entry.Key)).Count -ne 1) {
            throw "生产只读负载占位符数量异常：$($entry.Key)"
        }
        $payload = $payload.Replace($entry.Key, $entry.Value)
    }
    return $payload.Replace("`r`n", "`n").Replace("`r", "`n")
}

# 默认入口不读取目标候选、不生成 runner，也不解析本机 SSH 身份。
if (-not $ExportCandidate -and -not $SelfTest) {
    Write-Output "production_readonly_candidate_authorized=false"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExportCandidate -and $SelfTest) { throw "ExportCandidate 与 SelfTest 必须互斥" }

if ($SelfTest) {
    $synthetic = [pscustomobject][ordered]@{
        schema_version = 1; change_id = "20990105T010203Z"; environment = "production"; target_alias = "prod-primary"
        server_host = "prod.example.invalid"; ssh_port = 2222; ssh_user = "deploy"
        expected_ed25519_fingerprint = "SHA256:gn1sP8Of5P2kSxYGP9vSPiLjeJ1AsLJoPh20rKvhkWY"
        project_root = "/srv/molin"; environment_file = "/srv/molin/.env.prod"; service_kind = "systemd"
        api_service_identifier = "molin-api.service"; api_local_port = 8080; prometheus_local_port = 19090
        alertmanager_local_port = 19093; rollback_operator_alias = "operator-a"; observer_alias = "observer-a"
        expected_sms_enabled = $false; expected_sms_test_mode = $true
        readonly_baseline_requires_separate_approval = $true; deployment_requires_separate_approval = $true
        canary_requires_separate_approval = $true; production_enable_requires_separate_approval = $true
        automatic_retries = 0; business_posts = 0; real_sms_sent = 0
    }
    Assert-TargetCandidate -Candidate $synthetic
    $payload = New-RemotePayload -Candidate $synthetic
    if (-not $payload.StartsWith("#!/usr/bin/env bash`n") -or $payload.Contains([char]0xFEFF) -or $payload.Contains("`r")) {
        throw "生产只读负载必须是无 BOM 的 LF UTF-8"
    }
    foreach ($pattern in @(
        '(?m)^\s*(rm|mv|cp|install|chmod|chown|truncate|touch|tee)\b',
        '\bsed\s+-i\b',
        '\b(curl|wget)\b[^\n]*\s-X\s*(POST|PUT|PATCH|DELETE)',
        '\bdocker\s+(run|create|restart|stop|kill|rm)\b',
        '\bsystemctl\s+(restart|stop|start|enable|disable|reload)\b',
        'SMS_ENABLED=true'
    )) {
        if ($payload -match $pattern) { throw "生产只读负载发现禁止模式：$pattern" }
    }
    Write-Output "production_readonly_candidate_self_test=passed"
    Write-Output "target_candidate_contract_verified=true"
    Write-Output "single_ssh_connection_contract=true"
    Write-Output "readonly_payload_verified=true"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ChangeId -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') { throw "生产只读 ChangeId 必须使用 UTC 基本格式" }
if ($ExpectedTargetCandidateSHA256 -cnotmatch '^[0-9a-f]{64}$') { throw "必须提供小写生产目标候选 SHA-256" }
if ([string]::IsNullOrWhiteSpace($TargetCandidateFile) -or [string]::IsNullOrWhiteSpace($OutputDirectory)) {
    throw "导出生产只读候选必须提供目标候选文件与全新输出目录"
}
Assert-LocalFileSystemPathInput -Path $TargetCandidateFile -Description "生产目标候选文件"
Assert-LocalFileSystemPathInput -Path $OutputDirectory -Description "候选输出目录"
$resolvedTarget = (Resolve-Path -LiteralPath $TargetCandidateFile -ErrorAction Stop).Path
$actualTargetSHA256 = (Get-FileHash -LiteralPath $resolvedTarget -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualTargetSHA256 -cne $ExpectedTargetCandidateSHA256) { throw "生产目标候选摘要不匹配" }
$targetCandidate = Get-Content -LiteralPath $resolvedTarget -Raw -Encoding UTF8 | ConvertFrom-Json
Assert-TargetCandidate -Candidate $targetCandidate

$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
$outputParent = Split-Path -Parent $outputPath
if ([string]::IsNullOrWhiteSpace($outputParent) -or -not (Test-Path -LiteralPath $outputParent -PathType Container)) {
    throw "候选输出目录的父目录必须已存在"
}
if (Test-Path -LiteralPath $outputPath) { throw "候选输出目录已存在，禁止覆盖" }

$runnerPath = Join-Path $outputPath "run-sms-phase5-production-readonly-$ChangeId.ps1"
$directoryCreated = $false
$fileCreated = $false
try {
    $remotePayload = New-RemotePayload -Candidate $targetCandidate
    $remotePayloadBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($remotePayload))
    $runnerTemplate = @'
param(
    [switch]$ExecuteReadOnly,
    [switch]$SelfTest,
    [string]$ResultFile = ""
)

$ErrorActionPreference = "Stop"
$ChangeId = "__CHANGE_ID__"
$TargetChangeId = "__TARGET_CHANGE_ID__"
$TargetCandidateSHA256 = "__TARGET_SHA256__"
$ServerHost = "__SERVER_HOST__"
$SSHPort = __SSH_PORT__
$SSHUser = "__SSH_USER__"
$ExpectedFingerprint = "__FINGERPRINT__"
$RemotePayloadBase64 = "__REMOTE_PAYLOAD_BASE64__"

$allowedKeys = @(
    "production_readonly_baseline", "app_env_production", "sms_enabled_false", "sms_test_mode_true", "provider_aliyun",
    "endpoint_official", "required_sms_config_present", "legacy_sms_keys_absent", "template_env_overrides_absent",
    "duplicate_sms_config_absent",
    "environment_file_secure", "service_running", "process_environment_readable", "file_process_sms_config_match",
    "health_ready", "schema_ready", "schema_version", "schema_dirty", "template_bindings_ready", "template_total",
    "template_approved", "template_enabled", "binding_total", "binding_enabled", "binding_distinct_templates",
    "send_log_readable", "send_total", "send_accepted",
    "send_failed", "metrics_ready", "sms_metric_shape_ready", "prometheus_ready", "sms_alert_rules_loaded",
    "prometheus_target_up", "active_sms_alerts", "notification_failures_total", "alertmanager_ready",
    "rollback_operator_declared", "observer_declared", "backup_capability_verified", "configuration_mutations",
    "service_operations", "business_posts", "uploads", "emails_sent", "real_sms_sent"
)

if (-not $ExecuteReadOnly -and -not $SelfTest) {
    Write-Output "production_readonly_change_id=$ChangeId"
    Write-Output "target_change_id=$TargetChangeId"
    Write-Output "target_candidate_sha256=$TargetCandidateSHA256"
    Write-Output "production_readonly_authorized=false"
    Write-Output "low_sensitivity_result_persisted=false"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExecuteReadOnly -and $SelfTest) { throw "ExecuteReadOnly 与 SelfTest 必须互斥" }
if (-not $ExecuteReadOnly -and -not [string]::IsNullOrWhiteSpace($ResultFile)) {
    throw "仅 ExecuteReadOnly 可以指定 ResultFile"
}
if ($SelfTest) {
    $decoded = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64))
    if (-not $decoded.StartsWith("#!/usr/bin/env bash`n") -or $decoded.Contains("`r") -or $decoded.Contains([char]0xFEFF)) {
        throw "内嵌生产只读负载编码无效"
    }
    Write-Output "production_readonly_runner_self_test=passed"
    Write-Output "low_sensitivity_result_persisted=false"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ([string]::IsNullOrWhiteSpace($ResultFile) -or $ResultFile -match '^(?:\\\\|//)' -or $ResultFile.Contains("::")) {
    throw "生产只读执行必须提供本地文件系统结果绝对路径"
}
$isWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
if ($isWindowsPlatform -and $ResultFile -cnotmatch '^[A-Za-z]:[\\/]') {
    throw "Windows 结果文件必须使用本地盘符绝对路径"
}
if ($isWindowsPlatform) {
    $resultDrive = Get-PSDrive -Name $ResultFile.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
    if (([string]$resultDrive.Root).StartsWith("\\") -or ([string]$resultDrive.DisplayRoot).StartsWith("\\")) {
        throw "结果文件不得使用网络映射盘"
    }
}
if (-not $isWindowsPlatform -and -not [IO.Path]::IsPathRooted($ResultFile)) {
    throw "结果文件必须使用本地绝对路径"
}
$resultPath = [IO.Path]::GetFullPath($ResultFile)
$resultParent = Split-Path -Parent $resultPath
if ([string]::IsNullOrWhiteSpace($resultParent) -or -not (Test-Path -LiteralPath $resultParent -PathType Container)) {
    throw "结果文件父目录必须已存在"
}
$resultParentItem = Get-Item -LiteralPath $resultParent -Force -ErrorAction Stop
if (($resultParentItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    throw "结果文件父目录不得是符号链接或重解析点"
}
if (Test-Path -LiteralPath $resultPath) { throw "结果文件已存在，禁止覆盖" }
$runnerSHA256 = (Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash.ToLowerInvariant()

# 执行前只接受本机普通 known_hosts，并重新计算唯一 ED25519 公钥指纹。
$knownHostsPath = [IO.Path]::GetFullPath((Join-Path $env:USERPROFILE ".ssh\known_hosts"))
if (-not (Test-Path -LiteralPath $knownHostsPath -PathType Leaf) -or
    ([IO.FileInfo]$knownHostsPath).Attributes.HasFlag([IO.FileAttributes]::ReparsePoint)) {
    throw "生产固定 known_hosts 文件不存在或属于重解析路径"
}
$knownHostLines = @(& ssh-keygen -F "[$ServerHost]:$SSHPort" -f $knownHostsPath)
if ($LASTEXITCODE -ne 0) { throw "known_hosts 中缺少生产目标身份" }
$ed25519Keys = @()
foreach ($line in $knownHostLines) {
    $parts = @($line.Trim() -split '\s+')
    if ($parts.Count -ge 3 -and $parts[1] -ceq "ssh-ed25519") { $ed25519Keys += $parts[2] }
}
if ($ed25519Keys.Count -ne 1) { throw "生产目标 ED25519 公钥数量异常" }
$sha256 = [Security.Cryptography.SHA256]::Create()
try {
    $actualFingerprint = "SHA256:" + [Convert]::ToBase64String(
        $sha256.ComputeHash([Convert]::FromBase64String($ed25519Keys[0]))
    ).TrimEnd('=')
}
finally { $sha256.Dispose() }
if ($actualFingerprint -cne $ExpectedFingerprint) { throw "生产目标 ED25519 指纹不匹配" }

$sshCandidates = @(Get-Command ssh.exe -CommandType Application -All -ErrorAction SilentlyContinue)
if ($sshCandidates.Count -eq 0) { $sshCandidates = @(Get-Command ssh -CommandType Application -All -ErrorAction Stop) }
$sshCommand = @($sshCandidates) | Select-Object -First 1
$sshPath = $sshCommand.Source
$destination = "${SSHUser}@${ServerHost}"
$sshArguments = @(
    "-p", $SSHPort.ToString(), "-o", "BatchMode=yes", "-o", "ConnectTimeout=8",
    "-o", "StrictHostKeyChecking=yes", "-o", "HostKeyAlgorithms=ssh-ed25519",
    "-o", "UserKnownHostsFile=$knownHostsPath", "--", $destination,
    "printf '%s' '$RemotePayloadBase64' | base64 -d | bash"
)
$remoteLines = @(& $sshPath @sshArguments 2>&1)
$readonlyExitCode = $LASTEXITCODE
$result = [ordered]@{}
$outputValid = $true
foreach ($lineObject in $remoteLines) {
    $line = [string]$lineObject
    if ($line -cnotmatch '^([a-z0-9_]+)=([a-z0-9_.-]+)$') { $outputValid = $false; continue }
    $key = $Matches[1]; $value = $Matches[2]
    if ($key -cnotin $allowedKeys -or $result.Contains($key)) { $outputValid = $false; continue }
    $result[$key] = $value
}
if (-not $outputValid -or @($allowedKeys | Where-Object { -not $result.Contains($_) }).Count -ne 0) {
    throw "生产只读远端输出不符合低敏白名单协议"
}
foreach ($key in $allowedKeys) { Write-Output "$key=$($result[$key])" }
Write-Output "network_connections=1"
Write-Output "remote_stderr_present=false"
Write-Output "readonly_exit_code=$readonlyExitCode"
$evidence = [ordered]@{
    schema_version = 1
    change_id = $ChangeId
    target_change_id = $TargetChangeId
    target_candidate_sha256 = $TargetCandidateSHA256
    runner_sha256 = $runnerSHA256
    observed = $result
    network_connections = 1
    remote_stderr_present = $false
    readonly_exit_code = $readonlyExitCode
    uploads = 0
    configuration_mutations = 0
    service_operations = 0
    business_posts = 0
    emails_sent = 0
    real_sms_sent = 0
    sensitive_values_persisted = 0
}
$evidenceBytes = [Text.Encoding]::UTF8.GetBytes(($evidence | ConvertTo-Json -Depth 5) + "`n")
$stream = $null
$resultCreated = $false
try {
    # 使用 CreateNew 排他创建，禁止覆盖既有证据；文件只包含已通过字段白名单的低敏结果。
    $stream = New-Object IO.FileStream($resultPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    $resultCreated = $true
    $stream.Write($evidenceBytes, 0, $evidenceBytes.Length)
    $stream.Flush($true)
}
catch {
    if ($null -ne $stream) { $stream.Dispose(); $stream = $null }
    if ($resultCreated -and (Test-Path -LiteralPath $resultPath -PathType Leaf)) {
        Remove-Item -LiteralPath $resultPath -Force
    }
    throw
}
finally {
    if ($null -ne $stream) { $stream.Dispose() }
    [Array]::Clear($evidenceBytes, 0, $evidenceBytes.Length)
}
$resultSHA256 = (Get-FileHash -LiteralPath $resultPath -Algorithm SHA256).Hash.ToLowerInvariant()
Write-Output "low_sensitivity_result_persisted=true"
Write-Output "runner_sha256=$runnerSHA256"
Write-Output "result_sha256=$resultSHA256"
Write-Output "sensitive_values_persisted=0"
if ($readonlyExitCode -ne 0 -or $result["production_readonly_baseline"] -cne "passed") {
    throw "生产关闭态只读基线未通过，退出码：$readonlyExitCode"
}
'@
    $runnerReplacements = [ordered]@{
        "__CHANGE_ID__" = $ChangeId
        "__TARGET_CHANGE_ID__" = [string]$targetCandidate.change_id
        "__TARGET_SHA256__" = $ExpectedTargetCandidateSHA256
        "__SERVER_HOST__" = [string]$targetCandidate.server_host
        "__SSH_PORT__" = [string]$targetCandidate.ssh_port
        "__SSH_USER__" = [string]$targetCandidate.ssh_user
        "__FINGERPRINT__" = [string]$targetCandidate.expected_ed25519_fingerprint
        "__REMOTE_PAYLOAD_BASE64__" = $remotePayloadBase64
    }
    foreach ($entry in $runnerReplacements.GetEnumerator()) {
        if ([regex]::Matches($runnerTemplate, [regex]::Escape($entry.Key)).Count -ne 1) {
            throw "生产只读 runner 占位符数量异常：$($entry.Key)"
        }
        $runnerTemplate = $runnerTemplate.Replace($entry.Key, $entry.Value)
    }

    $null = New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop
    $directoryCreated = $true
    [IO.File]::WriteAllText($runnerPath, $runnerTemplate, (New-Object Text.UTF8Encoding($true)))
    $fileCreated = $true

    $tokens = $null; $parseErrors = $null
    $null = [Management.Automation.Language.Parser]::ParseFile($runnerPath, [ref]$tokens, [ref]$parseErrors)
    if (@($parseErrors).Count -ne 0) { throw "生产只读 runner PowerShell 语法校验失败" }
    $closedOutput = @(& $runnerPath)
    $selfTestOutput = @(& $runnerPath -SelfTest)
    if ($closedOutput -cnotcontains "production_readonly_authorized=false" -or
        $selfTestOutput -cnotcontains "production_readonly_runner_self_test=passed") {
        throw "生产只读 runner 默认关闭或自测失败"
    }

    $runnerSHA256 = (Get-FileHash -LiteralPath $runnerPath -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Output "production_readonly_candidate=passed"
    Write-Output "change_id=$ChangeId"
    Write-Output "target_change_id=$($targetCandidate.change_id)"
    Write-Output "target_candidate_sha256=$ExpectedTargetCandidateSHA256"
    Write-Output "runner_sha256=$runnerSHA256"
    Write-Output "runner_path=$runnerPath"
    Write-Output "execute_readonly_authorized=false"
    Write-Output "candidate_files_written=1"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_operations=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
}
catch {
    if ($fileCreated -and (Test-Path -LiteralPath $runnerPath -PathType Leaf)) {
        Remove-Item -LiteralPath $runnerPath -Force
    }
    if ($directoryCreated -and (Test-Path -LiteralPath $outputPath -PathType Container) -and
        @(Get-ChildItem -LiteralPath $outputPath -Force).Count -eq 0) {
        Remove-Item -LiteralPath $outputPath -Force
    }
    throw
}
