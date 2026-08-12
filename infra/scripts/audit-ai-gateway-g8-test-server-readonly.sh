#!/usr/bin/env bash
# 本脚本仅采集测试服务器低敏聚合事实，不修改文件、服务、数据库、缓存或队列。
set -uo pipefail

readonly PRIVILEGED_INSTALL_PATH="/usr/local/libexec/molin/g8-test-readonly-audit"
readonly PRIVILEGED_RECONCILE_PATH="/usr/local/libexec/molin/ai-gateway-reconcile"
readonly FIXED_ROOT="/home/pc/molin"
readonly FIXED_ENV_FILE="/home/pc/molin/infra/.env.test"
readonly ROOT="$FIXED_ROOT"
readonly ENV_FILE="$FIXED_ENV_FILE"

# 固定可信命令搜索路径并清除 Bash 启动注入变量，避免 sudo 调用继承调用者控制的执行环境。
export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
unset BASH_ENV ENV CDPATH GLOBIGNORE PYTHONPATH PYTHONHOME || true
cd / || exit 41

section() {
  printf '%s\n' "---$1---"
}

unavailable() {
  printf '%s=UNAVAILABLE\n' "$1"
}

if [[ "${1:-}" == "--self-test" ]]; then
  [[ "$ROOT" == /* && "$ENV_FILE" == /* ]] || exit 2
  printf 'G8_TEST_READONLY_AUDIT_SELF_TEST=PASS\n'
  exit 0
fi

if (($# != 1)) || [[ "$1" != --change-id=* ]]; then
  printf 'invalid_arguments=true\n'
  exit 2
fi
readonly CHANGE_ID="${1#--change-id=}"
if [[ ! "$CHANGE_ID" =~ ^CHG-G8-TEST-READONLY-[0-9]{8}-[0-9]{3}$ ]]; then
  printf 'invalid_change_id=true\n'
  exit 2
fi

# 特权执行只接受 root 拥有、固定权限和固定绝对路径的安装副本，避免 pc 替换脚本后借 sudo 提权。
if ((EUID == 0)); then
  installed_path="$(readlink -f -- "$0" 2>/dev/null || true)"
  installed_meta="$(stat -Lc '%U:%G:%a' -- "$PRIVILEGED_INSTALL_PATH" 2>/dev/null || true)"
  if [[ "$installed_path" != "$PRIVILEGED_INSTALL_PATH" || "$installed_meta" != "root:root:755" ]]; then
    printf 'privileged_installation=INVALID\n'
    exit 42
  fi
  printf 'privileged_installation=VERIFIED\n'
  unset G8_LEGACY_TEST_CREDENTIAL_SHA256 || true
fi

printf 'CHANGE_ID=%s\n' "$CHANGE_ID"
printf 'AUDIT_MODE=READ_ONLY_SINGLE_SESSION\n'

section IDENTITY
printf 'audit_invoker=%s\n' "${SUDO_USER:-$(id -un 2>/dev/null || printf UNAVAILABLE)}"
printf 'effective_user=%s\n' "$(id -un 2>/dev/null || printf UNAVAILABLE)"
printf 'hostname=%s\n' "$(hostname 2>/dev/null || printf UNAVAILABLE)"
if [[ -r /etc/machine-id ]]; then
  printf 'machine_id_sha256=%s\n' "$(sha256sum /etc/machine-id | awk '{print $1}')"
else
  unavailable machine_id_sha256
fi
printf 'passwd_status=%s\n' "$(passwd -S pc 2>/dev/null | awk '{print $2}' || printf UNAVAILABLE)"
if id -nG pc 2>/dev/null | tr ' ' '\n' | grep -Fxq docker; then
  printf 'pc_docker_group_member=true\n'
else
  printf 'pc_docker_group_member=false\n'
fi
if [[ ! -d "$ROOT" ]]; then
  printf 'deploy_root=MISSING\n'
  exit 40
fi
printf 'deploy_root=EXISTS\n'

section SOURCE_AND_API
if GIT_OPTIONAL_LOCKS=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git -C "$ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf 'git_head=%s\n' "$(GIT_OPTIONAL_LOCKS=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git -C "$ROOT" rev-parse HEAD 2>/dev/null || printf UNAVAILABLE)"
  unavailable git_dirty_count_read_only_policy
else
  unavailable git_head
  unavailable git_dirty_count_read_only_policy
fi
mapfile -t api_pids < <(pgrep -x molin-api 2>/dev/null || true)
printf 'api_process_count=%d\n' "${#api_pids[@]}"
for pid in "${api_pids[@]}"; do
  ps -p "$pid" -o pid=,lstart=,etime=,comm= 2>/dev/null | sed 's/^/api_process=/' || true
done
if [[ -f "$ROOT/molin-api" ]]; then
  printf 'api_binary_sha256=%s\n' "$(sha256sum "$ROOT/molin-api" | awk '{print $1}')"
  stat -c 'api_binary_meta=type:%F owner:%U group:%G mode:%a size:%s mtime:%y' "$ROOT/molin-api" 2>/dev/null || true
else
  unavailable api_binary_sha256
fi
printf 'api_listener_count=%s\n' "$(ss -ltnp 2>/dev/null | grep -c 'molin-api' || true)"
printf 'api_health_http=%s\n' "$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:8080/api/health 2>/dev/null || printf 000)"
printf 'api_ready_http=%s\n' "$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:8080/api/ready 2>/dev/null || printf 000)"

section ENVIRONMENT_METADATA
if [[ -f "$ENV_FILE" ]]; then
  stat -c 'env_file_meta=type:%F owner:%U group:%G mode:%a size:%s mtime:%y' "$ENV_FILE" 2>/dev/null || true
  mapfile -t env_keys < <(awk -F= '/^[A-Za-z_][A-Za-z0-9_]*=/{print $1}' "$ENV_FILE" | sort -u)
  printf 'env_key_count=%d\n' "${#env_keys[@]}"
  printf 'env_keys=%s\n' "$(IFS=,; printf '%s' "${env_keys[*]}")"
else
  unavailable env_file_meta
fi

section CONTAINERS
docker_prefix=()
if docker info >/dev/null 2>&1; then
  docker_prefix=(docker)
elif sudo -n docker info >/dev/null 2>&1; then
  docker_prefix=(sudo -n docker)
fi
if ((${#docker_prefix[@]} == 0)); then
  unavailable docker_access
else
  printf 'docker_access=%s\n' "$((${#docker_prefix[@]} == 1))" | sed 's/1/direct/;s/0/sudo-n/'
  printf 'docker_version=%s\n' "$("${docker_prefix[@]}" version --format '{{.Server.Version}}' 2>/dev/null || printf UNAVAILABLE)"
  "${docker_prefix[@]}" ps --format 'container={{.Names}}|image={{.Image}}|status={{.Status}}|ports={{.Ports}}' 2>/dev/null | sort || true
fi

section MYSQL
if ((${#docker_prefix[@]} > 0)) && "${docker_prefix[@]}" inspect molin-mysql >/dev/null 2>&1; then
  "${docker_prefix[@]}" exec molin-mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" mysql --batch --skip-column-names -u"$MYSQL_USER" "$MYSQL_DATABASE" -e "SELECT CONCAT(\"mysql_version=\",VERSION()); SELECT CONCAT(\"schema=\",MAX(version),\":\",MAX(dirty)) FROM schema_migrations; SELECT CONCAT(\"table=\",table_name,\"|rows=\",table_rows,\"|data=\",data_length,\"|index=\",index_length) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN (\"token_models\",\"token_channels\",\"api_keys\",\"api_key_model_scopes\",\"ai_projects\",\"ai_requests\",\"ai_usage_items\",\"ai_execution_attempts\",\"ai_price_versions\",\"ai_price_model_locks\",\"ai_price_skus\",\"ai_request_wallet_links\",\"ai_outbox_events\",\"ai_safety_policy_versions\",\"ai_safety_events\",\"ai_safety_subject_actions\",\"ai_safety_appeals\",\"ai_resource_policies\",\"ai_budget_policies\",\"ai_budget_overrides\",\"ai_budget_reservations\",\"ai_budget_alerts\",\"ai_compensation_tasks\",\"ai_model_release_versions\",\"ai_model_routes\",\"ai_model_route_runtime_states\",\"ai_gateway_rejection_events\",\"ai_billing_disputes\",\"wallets\",\"wallet_holds\",\"wallet_transactions\") ORDER BY table_name;"' 2>/dev/null || unavailable mysql_query
else
  unavailable mysql_query
fi

section REDIS_RABBITMQ
if ((${#docker_prefix[@]} > 0)) && "${docker_prefix[@]}" inspect molin-redis >/dev/null 2>&1; then
  printf 'redis_version=%s\n' "$("${docker_prefix[@]}" exec molin-redis redis-server --version 2>/dev/null | sed -n 's/.*v=\([^ ]*\).*/\1/p' || printf UNAVAILABLE)"
  printf 'redis_ping=%s\n' "$("${docker_prefix[@]}" exec molin-redis sh -c 'REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli --no-auth-warning ping' 2>/dev/null || printf UNAVAILABLE)"
else
  unavailable redis_ping
fi
if ((${#docker_prefix[@]} > 0)) && "${docker_prefix[@]}" inspect molin-rabbitmq >/dev/null 2>&1; then
  printf 'rabbitmq_ping=%s\n' "$("${docker_prefix[@]}" exec molin-rabbitmq rabbitmq-diagnostics -q ping 2>/dev/null | tr '\n' ' ' || printf UNAVAILABLE)"
  "${docker_prefix[@]}" exec molin-rabbitmq rabbitmqctl -q list_queues messages_ready messages_unacknowledged 2>/dev/null |
    awk 'BEGIN{r=0;u=0;c=0} $1~/^[0-9]+$/&&$2~/^[0-9]+$/{r+=$1;u+=$2;c++} END{printf "rabbitmq_queue_count=%d\nrabbitmq_ready=%d\nrabbitmq_unacked=%d\n",c,r,u}' || unavailable rabbitmq_queues
else
  unavailable rabbitmq_ping
fi

section BIFROST
if ((${#docker_prefix[@]} > 0)); then
  mapfile -t bifrost_names < <("${docker_prefix[@]}" ps --format '{{.Names}}' 2>/dev/null | grep -E 'bifrost' || true)
  printf 'bifrost_container_count=%d\n' "${#bifrost_names[@]}"
  for name in "${bifrost_names[@]}"; do
    "${docker_prefix[@]}" inspect -f 'bifrost={{.Name}}|image_id={{.Image}}|state={{.State.Status}}|health={{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$name" 2>/dev/null || true
    image_id="$("${docker_prefix[@]}" inspect -f '{{.Image}}' "$name" 2>/dev/null || true)"
    container_env="$("${docker_prefix[@]}" inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$name" 2>/dev/null | grep -E '^[A-Z][A-Z0-9_]*=' | sort -u)"
    image_env="$("${docker_prefix[@]}" image inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$image_id" 2>/dev/null | grep -E '^[A-Z][A-Z0-9_]*=' | sort -u)"
    env_keys="$(comm -23 <(printf '%s\n' "$container_env") <(printf '%s\n' "$image_env") | awk -F= '{print $1}' | sed '/^$/d' | sort -u | paste -sd, -)"
    printf 'bifrost_env_keys=%s|keys=%s\n' "$name" "$env_keys"
    case "$name" in
      *bifrost-lb*) expected_keys="BIFROST_INTERNAL_TOKEN" ;;
      *) expected_keys="BAILIAN_API_KEY,BIFROST_ENCRYPTION_KEY,OPENROUTER_API_KEY" ;;
    esac
    if [[ "$env_keys" == "$expected_keys" ]]; then
      printf 'bifrost_env_scope=%s|exact=true\n' "$name"
    else
      printf 'bifrost_env_scope=%s|exact=false\n' "$name"
    fi
  done
else
  unavailable bifrost_container_count
fi

section MONITORING
prometheus_port="$(awk -F= '$1=="PROMETHEUS_PORT" && $2~/^[0-9]+$/{print $2; exit}' "$ENV_FILE" 2>/dev/null || true)"
grafana_port="$(awk -F= '$1=="GRAFANA_PORT" && $2~/^[0-9]+$/{print $2; exit}' "$ENV_FILE" 2>/dev/null || true)"
prometheus_port="${prometheus_port:-19090}"
grafana_port="${grafana_port:-13000}"
printf 'prometheus_ready_http=%s\n' "$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:${prometheus_port}/-/ready" 2>/dev/null || printf 000)"
PROMETHEUS_PORT="$prometheus_port" /usr/bin/python3 -I - <<'PY' 2>/dev/null || unavailable prometheus_targets
import json
import os
import urllib.request

try:
    url = f"http://127.0.0.1:{os.environ['PROMETHEUS_PORT']}/api/v1/targets?state=active"
    with urllib.request.urlopen(url, timeout=5) as response:
        targets = json.load(response)["data"]["activeTargets"]
    up = sum(1 for target in targets if target.get("health") == "up")
    print(f"prometheus_targets_total={len(targets)}")
    print(f"prometheus_targets_up={up}")
    print(f"prometheus_targets_down={len(targets) - up}")
except Exception:
    print("prometheus_targets=UNAVAILABLE")
PY
alert_file="$ROOT/infra/prometheus/ai-gateway-alerts.yml"
if [[ -r "$alert_file" ]]; then
  printf 'g8_alert_rule_count=%s\n' "$(grep -Ec '^[[:space:]]*- alert:' "$alert_file")"
  printf 'g8_alert_rule_sha256=%s\n' "$(sha256sum "$alert_file" | awk '{print $1}')"
else
  unavailable g8_alert_rule_count
fi
printf 'grafana_health_http=%s\n' "$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:${grafana_port}/api/health" 2>/dev/null || printf 000)"
/usr/bin/python3 -I - "$ROOT/infra/grafana/dashboards" <<'PY' 2>/dev/null || unavailable grafana_dashboards
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
files = list(root.glob("*.json")) if root.exists() else []

def count_panels(items):
    total = 0
    for item in items or []:
        total += 1
        if isinstance(item, dict):
            total += count_panels(item.get("panels", []))
    return total

matched = []
for path in files:
    try:
        dashboard = json.loads(path.read_text())
        if dashboard.get("uid") == "molin-ai-gateway-g7":
            matched.append(count_panels(dashboard.get("panels", [])))
    except Exception:
        pass
print(f"grafana_dashboard_files={len(files)}")
print("g8_grafana_panel_count=" + str(matched[0] if matched else "UNAVAILABLE"))
PY
if ((${#docker_prefix[@]} > 0)); then
  mapfile -t alertmanager_names < <("${docker_prefix[@]}" ps --format '{{.Names}}' 2>/dev/null | grep -E 'alertmanager' || true)
  printf 'alertmanager_container_count=%d\n' "${#alertmanager_names[@]}"
  for name in "${alertmanager_names[@]}"; do
    "${docker_prefix[@]}" inspect -f 'alertmanager={{.Name}}|image_id={{.Image}}|state={{.State.Status}}|health={{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$name" 2>/dev/null || true
  done
else
  unavailable alertmanager_container_count
fi
alertmanager_file="$ROOT/infra/alertmanager/ai-gateway-g8.yml"
if [[ -r "$alertmanager_file" ]]; then
  printf 'alertmanager_config_sha256=%s\n' "$(sha256sum "$alertmanager_file" | awk '{print $1}')"
  if grep -Eq 'receiver:.*discard' "$alertmanager_file"; then
    printf 'alertmanager_discard_configured=true\n'
  else
    printf 'alertmanager_discard_configured=false\n'
  fi
else
  unavailable alertmanager_config_sha256
fi

section RECONCILIATION
reconcile="$ROOT/ai-gateway-reconcile"
if ((EUID == 0)); then
  reconcile="$PRIVILEGED_RECONCILE_PATH"
  reconcile_meta="$(stat -Lc '%U:%G:%a' -- "$reconcile" 2>/dev/null || true)"
  if [[ "$reconcile_meta" != "root:root:755" ]]; then
    reconcile=""
  fi
fi
if [[ -n "$reconcile" && -x "$reconcile" && -r "$ENV_FILE" ]]; then
  /usr/bin/python3 -I - "$reconcile" "$ENV_FILE" <<'PY' 2>/dev/null || unavailable reconcile_execution
import os
import pathlib
import subprocess
import sys

allowed_keys = {"MYSQL_USER", "MYSQL_PASSWORD", "MYSQL_DATABASE"}
values = {}
try:
    content = pathlib.Path(sys.argv[2]).read_text(encoding="utf-8")
    for raw_line in content.splitlines():
        if not raw_line or raw_line.lstrip().startswith("#") or "=" not in raw_line:
            continue
        key, value = raw_line.split("=", 1)
        if key in allowed_keys:
            if key in values:
                print("reconcile_configuration=UNAVAILABLE")
                raise SystemExit(0)
            value = value.strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in {'"', "'"}:
                value = value[1:-1]
            values[key] = value
except Exception:
    print("reconcile_configuration=UNAVAILABLE")
    raise SystemExit(0)

required = {"MYSQL_USER", "MYSQL_PASSWORD", "MYSQL_DATABASE"}
if set(values) != required or values["MYSQL_USER"] != "molin" or values["MYSQL_DATABASE"] != "molin":
    print("reconcile_configuration=UNAVAILABLE")
    raise SystemExit(0)

child_env = {
    "PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
    "APP_ENV": "test",
    "AI_GATEWAY_RECONCILE_READ_ONLY": "YES",
    "MYSQL_HOST": "127.0.0.1",
    "MYSQL_PORT": "13306",
    "MYSQL_USER": "molin",
    "MYSQL_PASSWORD": values["MYSQL_PASSWORD"],
    "MYSQL_DATABASE": "molin",
}
try:
    result = subprocess.run(
        [sys.argv[1], "--format", "summary", "--timeout", "30s"],
        env=child_env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        timeout=35,
        check=False,
    )
except Exception:
    print("reconcile_execution=UNAVAILABLE")
    raise SystemExit(0)

if result.returncode in {0, 2}:
    print(result.stdout.rstrip())
    print(f"reconcile_exit={result.returncode}")
else:
    print("reconcile_execution=UNAVAILABLE")
PY
else
  unavailable reconcile_binary
fi

section BACKUP
latest="$({
  for directory in "$ROOT/backups" /home/pc/backups /home/pc/molin-backups; do
    if [[ -d "$directory" ]]; then
      find "$directory" -maxdepth 3 -type f \( -name '*.sql' -o -name '*.sql.gz' -o -name '*.dump' -o -name '*.tar.gz' \) -printf '%T@|%p|%s\n' 2>/dev/null
    fi
  done
} | sort -n | tail -1)"
if [[ -n "$latest" ]]; then
  backup_path="$(cut -d'|' -f2 <<<"$latest")"
  printf 'backup_latest_epoch=%s\n' "$(cut -d'|' -f1 <<<"$latest")"
  printf 'backup_latest_size=%s\n' "$(cut -d'|' -f3 <<<"$latest")"
  printf 'backup_latest_sha256=%s\n' "$(sha256sum "$backup_path" 2>/dev/null | awk '{print $1}' || printf UNAVAILABLE)"
  if [[ -r "$backup_path" ]]; then printf 'backup_readable=true\n'; else printf 'backup_readable=false\n'; fi
else
  unavailable backup_latest
fi

section CREDENTIAL_ROTATION
ENV_FILE="$ENV_FILE" G8_LEGACY_TEST_CREDENTIAL_SHA256="${G8_LEGACY_TEST_CREDENTIAL_SHA256:-}" /usr/bin/python3 -I - <<'PY' 2>/dev/null || unavailable credential_rotation
import hashlib
import os
import pathlib
import urllib.parse

allowed_keys = {"MYSQL_PASSWORD", "MINIO_SECRET_KEY", "RABBITMQ_URL", "REDIS_PASSWORD"}
values = {}
parse_ok = True
try:
    content = pathlib.Path(os.environ["ENV_FILE"]).read_text(encoding="utf-8")
    for raw_line in content.splitlines():
        if not raw_line or raw_line.lstrip().startswith("#") or "=" not in raw_line:
            continue
        key, value = raw_line.split("=", 1)
        if key in allowed_keys:
            if key in values:
                parse_ok = False
                break
            value = value.strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in {'"', "'"}:
                value = value[1:-1]
            values[key] = value
except Exception:
    parse_ok = False

expected = os.environ.get("G8_LEGACY_TEST_CREDENTIAL_SHA256", "")

def compare(label, value):
    if not value or not expected:
        print(f"{label}=UNAVAILABLE")
    elif hashlib.sha256(value.encode()).hexdigest() == expected:
        print(f"{label}=LEGACY_MATCH_ROTATION_REQUIRED")
    else:
        print(f"{label}=NOT_LEGACY_MATCH")

if not parse_ok:
    for label in ("mysql_credential", "minio_credential", "rabbitmq_credential", "redis_credential"):
        print(f"{label}=UNAVAILABLE")
    raise SystemExit(0)

compare("mysql_credential", values.get("MYSQL_PASSWORD", ""))
compare("minio_credential", values.get("MINIO_SECRET_KEY", ""))
try:
    rabbitmq_password = urllib.parse.unquote(urllib.parse.urlparse(values.get("RABBITMQ_URL", "")).password or "")
except ValueError:
    rabbitmq_password = ""
compare("rabbitmq_credential", rabbitmq_password)
if "REDIS_PASSWORD" not in values:
    print("redis_credential=UNAVAILABLE")
elif values["REDIS_PASSWORD"]:
    print("redis_credential=PASSWORD_CONFIGURED")
else:
    print("redis_credential=NO_PASSWORD_CONFIGURED")
PY
printf 'ssh_key_auth=UNVERIFIED_BY_AUDITOR\n'
if ((EUID == 0)); then
  ssh_password_auth="$(sshd -T 2>/dev/null | awk '$1=="passwordauthentication"{print $2; found=1} END{if(!found) print "UNAVAILABLE"}')"
else
  ssh_password_auth="$(sudo -n sshd -T 2>/dev/null | awk '$1=="passwordauthentication"{print $2; found=1} END{if(!found) print "UNAVAILABLE"}')"
fi
printf 'ssh_password_auth_config=%s\n' "$ssh_password_auth"
printf 'ssh_password_state=%s\n' "$(passwd -S pc 2>/dev/null | awk '{print $2}' || printf UNAVAILABLE)"
printf 'AUDIT_COMPLETE=true\n'
