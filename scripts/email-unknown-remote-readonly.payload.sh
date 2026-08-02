set -Eeuo pipefail
stage=shell_options

# 远端标准错误始终关闭，所有失败只通过固定阶段枚举返回，避免泄露原始错误或配置值。
exec 2>/dev/null
fail() {
  local failed_stage=$1
  trap - ERR
  # 失败可能发生在 SSH 尚未写完脚本时；先耗尽剩余 stdin，避免客户端因远端提前关闭产生 Broken pipe stderr。
  /usr/bin/cat >/dev/null || true
  printf 'status=failed stage=%s\n' "$failed_stage"
  exit 2
}
trap 'fail "$stage"' ERR

if ! shopt -qo errexit || ! shopt -qo nounset || ! shopt -qo pipefail; then
  fail shell_options
fi
export PATH=/usr/sbin:/usr/bin:/sbin:/bin

read_process_env() {
  local process_id=$1 key=$2
  local -a values=()
  mapfile -t values < <(/usr/bin/tr '\0' '\n' < "/proc/${process_id}/environ" | /usr/bin/sed -n "s/^${key}=//p")
  (( ${#values[@]} == 1 ))
  printf '%s' "${values[0]}"
}

mysql_scalar() {
  local sql=$1 normalized result
  normalized=${sql//$'\n'/ }
  normalized=${normalized^^}
  [[ "$normalized" =~ ^[[:space:]]*SELECT[[:space:]] ]]
  [[ ! "$normalized" =~ (^|[[:space:]])(INSERT|UPDATE|DELETE|REPLACE|ALTER|CREATE|DROP|TRUNCATE|RENAME|GRANT|REVOKE|CALL|LOAD|LOCK|UNLOCK|SET)([[:space:]]|$) ]]
  result=$(MYSQL_PWD="$mysql_password" /usr/bin/docker exec -e MYSQL_PWD="$mysql_password" "$mysql_id" /usr/bin/mysql --no-defaults --host=127.0.0.1 --port=3306 --user="$mysql_user" --database="$mysql_database" --batch --skip-column-names --raw --execute="$sql")
  [[ "$result" != *$'\n'* ]]
  printf '%s' "$result"
}

# 隔离库由容器内 root 创建且不授权给应用账号；该通道只接收已冻结格式的 schema 名，
# SQL 在容器内按固定模板生成，root 密码不会离开容器环境或进入输出。
cycle_schema_root_scalar() {
  local schema_name=$1 result
  [[ "$schema_name" =~ ^molin_restore_57_reverify_[a-f0-9]{32}$ ]]
  result=$(/usr/bin/docker exec -i "$mysql_id" /bin/bash -s -- "$schema_name" <<'ROOT_SCHEMA_QUERY'
set -Eeuo pipefail
schema_name=$1
[[ "$schema_name" =~ ^molin_restore_57_reverify_[a-f0-9]{32}$ ]]
[[ -n "${MYSQL_ROOT_PASSWORD:-}" ]]
sql="SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='${schema_name}';"
[[ "${sql%% *}" == SELECT ]]
[[ "$sql" == "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='${schema_name}';" ]]
result=$(MYSQL_PWD="$MYSQL_ROOT_PASSWORD" /usr/bin/mysql --no-defaults --host=127.0.0.1 --port=3306 --user=root --batch --skip-column-names --raw --execute="$sql")
[[ "$result" =~ ^[01]$ ]]
printf '%s' "$result"
ROOT_SCHEMA_QUERY
  )
  [[ "$result" =~ ^[01]$ ]]
  printf '%s' "$result"
}

stage=api_identity
mapfile -t api_pids < <(/usr/bin/pgrep -x molin-api)
(( ${#api_pids[@]} == 1 ))
api_pid=${api_pids[0]}
[[ "$api_pid" =~ ^[1-9][0-9]*$ ]]

stage=api_environment
app_env=$(read_process_env "$api_pid" APP_ENV)
email_adapter=$(read_process_env "$api_pid" EMAIL_ADAPTER)
[[ "$app_env" == test ]]
[[ -n "$email_adapter" && "$email_adapter" != mock ]]

stage=health_transport
health_status=$(/usr/bin/curl --request GET --silent --show-error --max-time 5 --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8080/api/health)
stage=health
[[ "$health_status" == 200 ]]
health=true
stage=ready_transport
ready_status=$(/usr/bin/curl --request GET --silent --show-error --max-time 5 --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8080/api/ready)
stage=ready
[[ "$ready_status" == 200 ]]
ready=true

stage=required_environment
mysql_database=$(read_process_env "$api_pid" MYSQL_DATABASE)
mysql_user=$(read_process_env "$api_pid" MYSQL_USER)
mysql_password=$(read_process_env "$api_pid" MYSQL_PASSWORD)
redis_password=$(read_process_env "$api_pid" REDIS_PASSWORD)
redis_db=$(read_process_env "$api_pid" REDIS_DB)
[[ "$mysql_database" == molin ]]
[[ "$mysql_user" =~ ^[A-Za-z0-9_]+$ ]]
[[ -n "$mysql_password" ]]
[[ "$redis_db" =~ ^[0-9]+$ ]]

stage=mysql_identity
mapfile -t container_lines < <(/usr/bin/docker ps --format '{{.ID}}|{{.Image}}|{{.Names}}')
mysql_ids=()
redis_ids=()
for container_line in "${container_lines[@]}"; do
  container_id=${container_line%%|*}
  container_identity=${container_line#*|}
  container_identity=${container_identity,,}
  [[ "$container_id" =~ ^[a-f0-9]{12,64}$ ]]
  case "$container_identity" in
    *mysql*) mysql_ids+=("$container_id") ;;
    *redis*) redis_ids+=("$container_id") ;;
  esac
done
(( ${#mysql_ids[@]} == 1 ))
mysql_id=${mysql_ids[0]}
stage=redis_identity
(( ${#redis_ids[@]} == 1 ))
redis_id=${redis_ids[0]}

stage=schema_query
schema_result=$(mysql_scalar 'SELECT CONCAT(version, CHAR(9), IF(dirty, 1, 0), CHAR(9), (SELECT COUNT(*) FROM schema_migrations)) FROM schema_migrations LIMIT 1;')
IFS=$'\t' read -r schema_version schema_dirty schema_rows <<< "$schema_result"
stage=schema_gate
[[ "$schema_version" == 57 && "$schema_dirty" == 0 && "$schema_rows" == 1 ]]

stage=clock_query
mysql_epoch=$(mysql_scalar 'SELECT UNIX_TIMESTAMP(UTC_TIMESTAMP());')
stage=clock_format
[[ "$mysql_epoch" =~ ^[0-9]{10}$ ]]
system_epoch=$(/usr/bin/date -u +%s)
[[ "$system_epoch" =~ ^[0-9]{10}$ ]]
clock_delta=$(( system_epoch - mysql_epoch ))
(( clock_delta < 0 )) && clock_delta=$(( -clock_delta ))
stage=clock_drift
(( clock_delta <= 5 ))

# 只枚举带完成 marker 的两个 000057 证据目录，不读取或查询其他隔离资产。
stage=cycle_metadata
mapfile -t cycle_markers < <(/usr/bin/docker exec "$mysql_id" /usr/bin/find /root -mindepth 3 -maxdepth 3 -type f -path '/root/molin-000057-schema57-cycle-run-*/evidence/cycle_completed' -print)
stage=cycle_count
(( ${#cycle_markers[@]} == 2 ))
cycle_targets=()
cycle_valid_count=0
for cycle_marker in "${cycle_markers[@]}"; do
  [[ "$cycle_marker" =~ ^(/root/molin-000057-schema57-cycle-run-([a-f0-9]{32}))/evidence/cycle_completed$ ]]
  cycle_dir=${BASH_REMATCH[1]}
  cycle_suffix=${BASH_REMATCH[2]}
  cycle_target="molin_restore_57_reverify_${cycle_suffix}"
  stage=cycle_name
  [[ "$cycle_target" =~ ^molin_restore_57_reverify_[a-f0-9]{32}$ ]]
  stage=cycle_target_source
  [[ "$cycle_target" != "$mysql_database" ]]
  stage=cycle_dir_metadata
  cycle_dir_type=$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_dir" -mindepth 0 -maxdepth 0 -type d -print)
  [[ "$cycle_dir_type" == "$cycle_dir" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a' -- "$cycle_dir")" == 0:700 ]]
  stage=cycle_marker_metadata
  cycle_marker_type=$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_marker" -mindepth 0 -maxdepth 0 -type f -print)
  [[ "$cycle_marker_type" == "$cycle_marker" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a' -- "$cycle_marker")" == 0:600 ]]
  dump_path="${cycle_dir}/evidence/molin_source_schema57.sql"
  stage=cycle_dump_symlink
  dump_symlink=$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$dump_path" -mindepth 0 -maxdepth 0 -type l -print)
  [[ -z "$dump_symlink" ]]
  stage=cycle_dump_metadata
  dump_type=$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$dump_path" -mindepth 0 -maxdepth 0 -type f -print)
  [[ "$dump_type" == "$dump_path" ]]
  dump_metadata=$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%s' -- "$dump_path")
  [[ "$dump_metadata" =~ ^0:600:([1-9][0-9]*)$ ]]
  cycle_targets+=("$cycle_target")
  cycle_valid_count=$(( cycle_valid_count + 1 ))
done
stage=cycle_targets_duplicate
[[ "${cycle_targets[0]}" != "${cycle_targets[1]}" ]]
[[ "$cycle_valid_count" == 2 ]]

stage=cycle_schema_query
cycle_schema_count=0
for cycle_target in "${cycle_targets[@]}"; do
  cycle_schema_exists=$(cycle_schema_root_scalar "$cycle_target")
  stage=cycle_schema_missing
  [[ "$cycle_schema_exists" == 1 ]]
  cycle_schema_count=$(( cycle_schema_count + 1 ))
done
[[ "$cycle_schema_count" == 2 ]]

stage=state_type
mapfile -t state_candidates < <(/usr/bin/find /home/pc -mindepth 1 -maxdepth 1 -type f -name 'molin-email-unknown-*.state' -print)
state_files=()
for state_candidate in "${state_candidates[@]}"; do
  if [[ "$state_candidate" =~ ^/home/pc/molin-email-unknown-[a-f0-9]{32}\.state$ ]]; then
    state_files+=("$state_candidate")
  fi
done
(( ${#state_files[@]} == 1 ))
state_file=${state_files[0]}
[[ ! -L "$state_file" ]]
stage=state_owner
[[ "$(/usr/bin/stat -c %u -- "$state_file")" == "$(/usr/bin/id -u)" ]]
stage=state_mode
[[ "$(/usr/bin/stat -c %a -- "$state_file")" == 600 ]]

# Python 只解析固定状态文件并在内存派生只读查询身份，不联网、不写文件、不启动子进程。
stage=state_parse
state_values=$(/usr/bin/python3 - "$state_file" "${cycle_targets[0]}" "${cycle_targets[1]}" <<'PY'
import hashlib
import hmac
import json
import re
import sys

path, first_cycle, second_cycle = sys.argv[1:]

def no_duplicates(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate")
        result[key] = value
    return result

with open(path, "r", encoding="utf-8") as stream:
    raw = stream.read()
state = json.loads(raw, object_pairs_hook=no_duplicates)
allowed = {
    "version", "phase", "nonce", "redis_run_id", "operator_id", "template_id",
    "allowlist_id", "send_log_id", "unexpected_send_log_id",
}
if set(state) - allowed or first_cycle in raw or second_cycle in raw:
    raise ValueError("fields")
if state.get("version") != 1 or state.get("phase") not in {"phase1_created", "phase2_verified"}:
    raise ValueError("phase")
nonce = state.get("nonce")
run_id = state.get("redis_run_id")
if not isinstance(nonce, str) or not re.fullmatch(r"[a-f0-9]{32}", nonce):
    raise ValueError("nonce")
if not isinstance(run_id, str) or not re.fullmatch(r"[a-f0-9]{40}", run_id):
    raise ValueError("run_id")
numeric_names = ("operator_id", "template_id", "allowlist_id", "send_log_id", "unexpected_send_log_id")
if any(type(state.get(name)) is not int or state[name] <= 0 for name in numeric_names):
    raise ValueError("ids")

email = f"phase4-{nonce}@example.invalid"
old_key = f"phase4-old-{nonce}"
new_key = f"phase4-new-{nonce}"
provider_template = f"qa-phase4-{nonce}"
recipient_hmac = hmac.new(b"qa-phase4-address-secret-32-bytes-only", email.encode(), hashlib.sha256).hexdigest()
scope = f"admin-email-template-test:admin:{state['operator_id']}:template:{state['template_id']}:scene:register:recipient:{recipient_hmac}"
fingerprint = hashlib.sha256(f"POST\n/api/admin/email/templates/{state['template_id']}/test-send\nregister\n{recipient_hmac}".encode()).hexdigest()
old_hash = hashlib.sha256(old_key.encode()).hexdigest()
new_hash = hashlib.sha256(new_key.encode()).hexdigest()
lock_hash = hmac.new(b"qa-phase4-idempotency-secret-32-bytes", scope.encode(), hashlib.sha256).hexdigest()
values = (
    state["phase"], run_id, str(state["template_id"]), str(state["allowlist_id"]),
    str(state["send_log_id"]), str(state["unexpected_send_log_id"]), recipient_hmac,
    scope.encode().hex(), fingerprint, old_hash, new_hash, provider_template.encode().hex(),
    "lock:email:dispatch:" + lock_hash,
)
print("\t".join(values))
PY
)
IFS=$'\t' read -r state_phase state_run_id template_id allowlist_id primary_log_id unexpected_log_id recipient_hmac scope_hex fingerprint old_hash new_hash provider_template_hex lock_key <<< "$state_values"
stage=state_values
[[ "$state_phase" =~ ^phase(1_created|2_verified)$ ]]
[[ "$state_run_id" =~ ^[a-f0-9]{40}$ ]]
for numeric_value in "$template_id" "$allowlist_id" "$primary_log_id" "$unexpected_log_id"; do [[ "$numeric_value" =~ ^[1-9][0-9]*$ ]]; done
for digest in "$recipient_hmac" "$fingerprint" "$old_hash" "$new_hash"; do [[ "$digest" =~ ^[a-f0-9]{64}$ ]]; done
[[ "$scope_hex" =~ ^[a-f0-9]+$ && $(( ${#scope_hex} % 2 )) == 0 ]]
[[ "$provider_template_hex" =~ ^[a-f0-9]+$ && $(( ${#provider_template_hex} % 2 )) == 0 ]]
[[ "$lock_key" =~ ^lock:email:dispatch:[a-f0-9]{64}$ ]]

stage=primary_query
primary_owned=$(mysql_scalar "SELECT COUNT(*) FROM email_send_logs WHERE id=${primary_log_id} AND template_id=${template_id} AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4) AND provider='aliyun_directmail' AND scene='register' AND purpose='test' AND recipient_hmac='${recipient_hmac}' AND idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4) AND idempotency_key_hash='${old_hash}' AND request_fingerprint='${fingerprint}' AND status='failed' AND failure_reason='provider_outcome_unknown';")
[[ "$primary_owned" == 1 ]]
stage=unexpected_query
unexpected_owned=$(mysql_scalar "SELECT COUNT(*) FROM email_send_logs WHERE id=${unexpected_log_id} AND template_id=${template_id} AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4) AND provider='aliyun_directmail' AND scene='register' AND purpose='test' AND recipient_hmac='${recipient_hmac}' AND idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4) AND idempotency_key_hash='${new_hash}' AND request_fingerprint='${fingerprint}' AND status='failed' AND failure_reason='provider_outcome_unknown';")
[[ "$unexpected_owned" == 1 ]]
stage=scope_query
scope_rows=$(mysql_scalar "SELECT COUNT(*) FROM email_send_logs WHERE idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4);")
[[ "$scope_rows" == 2 ]]
stage=template_query
template_owned=$(mysql_scalar "SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} AND provider='aliyun_directmail' AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4) AND provider_status='approved' AND variables_complete=1 AND local_enabled=1 AND version=1 AND JSON_LENGTH(variables_json)=2 AND JSON_CONTAINS(variables_json, JSON_QUOTE('Code')) AND JSON_CONTAINS(variables_json, JSON_QUOTE('ExpireMinutes'));")
[[ "$template_owned" == 1 ]]
stage=allowlist_query
allowlist_owned=$(mysql_scalar "SELECT COUNT(*) FROM email_test_recipient_allowlist WHERE id=${allowlist_id} AND email_hmac='${recipient_hmac}' AND status='active';")
[[ "$allowlist_owned" == 1 ]]

stage=redis_ping_transport
redis_ping=$(REDISCLI_AUTH="$redis_password" /usr/bin/docker exec -e REDISCLI_AUTH="$redis_password" "$redis_id" /usr/local/bin/redis-cli --raw -n "$redis_db" PING)
stage=redis_ping
[[ "$redis_ping" == PONG ]]
stage=redis_info
redis_info=$(REDISCLI_AUTH="$redis_password" /usr/bin/docker exec -e REDISCLI_AUTH="$redis_password" "$redis_id" /usr/local/bin/redis-cli --raw -n "$redis_db" INFO server)
mapfile -t current_run_ids < <(printf '%s\n' "$redis_info" | /usr/bin/sed -n 's/^run_id:\([a-f0-9]\{40\}\)\r\{0,1\}$/\1/p')
stage=redis_run_id
(( ${#current_run_ids[@]} == 1 ))
current_run_id=${current_run_ids[0]}
if [[ "$current_run_id" == "$state_run_id" ]]; then run_id_changed=false; else run_id_changed=true; fi
stage=redis_exists
lock_exists=$(REDISCLI_AUTH="$redis_password" /usr/bin/docker exec -e REDISCLI_AUTH="$redis_password" "$redis_id" /usr/local/bin/redis-cli --raw -n "$redis_db" EXISTS "$lock_key")
stage=redis_exists_value
[[ "$lock_exists" == 0 || "$lock_exists" == 1 ]]

stage=runtime_parent
mapfile -t orphan_candidates < <(/usr/bin/find /home/pc/molin-runtime -mindepth 1 -maxdepth 1 -type d -name 'email-unknown-*' -print)
orphan_dirs=()
for orphan_candidate in "${orphan_candidates[@]}"; do
  if [[ "$orphan_candidate" =~ ^/home/pc/molin-runtime/email-unknown-[a-f0-9]{32}$ ]]; then
    orphan_dirs+=("$orphan_candidate")
  fi
done
orphan_count=${#orphan_dirs[@]}
orphan_safe_count=0
for orphan_dir in "${orphan_dirs[@]}"; do
  stage=orphan_metadata
  [[ ! -L "$orphan_dir" ]]
  [[ "$(/usr/bin/stat -c %u -- "$orphan_dir")" == "$(/usr/bin/id -u)" ]]
  orphan_mode=$(/usr/bin/stat -c %a -- "$orphan_dir")
  [[ "$orphan_mode" == 700 || "$orphan_mode" == 750 ]]
  orphan_safe_count=$(( orphan_safe_count + 1 ))
done

stage=cycle_exclusion
cycle_evidence_count=${#cycle_markers[@]}
cycle_excluded_count=0
for cycle_target in "${cycle_targets[@]}"; do
  [[ "$cycle_target" != "$mysql_database" ]]
  [[ "$cycle_target" != "$lock_key" ]]
  cycle_excluded_count=$(( cycle_excluded_count + 1 ))
done
[[ "$cycle_excluded_count" == 2 ]]

trap - ERR
printf 'status=pass api_count=1 health=%s ready=%s live_adapter_mock=false mysql_count=1 redis_count=1 schema=57 dirty=false clock_drift_ok=true state_safe=true state_phase=%s primary_owned=%s unexpected_owned=%s scope_rows=%s template_owned=%s allowlist_owned=%s redis_ping=true run_id_changed=%s lock_exists=%s orphan_count=%s orphan_safe_count=%s cycle_evidence_count=%s cycle_valid_count=%s cycle_schema_count=%s cycle_excluded_count=%s writes=false restart=false cleanup=false\n' \
  "$health" "$ready" "$state_phase" "$primary_owned" "$unexpected_owned" "$scope_rows" "$template_owned" "$allowlist_owned" "$run_id_changed" "$lock_exists" "$orphan_count" "$orphan_safe_count" "$cycle_evidence_count" "$cycle_valid_count" "$cycle_schema_count" "$cycle_excluded_count"
