set -Eeuo pipefail
stage=shell_options

# 远端标准错误始终关闭；失败只暴露固定阶段，不输出凭据、主键或业务原始数据。
exec 2>/dev/null
fail() {
  local failed_stage=$1
  trap - ERR
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

# 所有前置和后置数据库核验只接受 SELECT；写入仅能由已冻结测试二进制的 cleanup 分支完成。
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

# 000057 隔离库只通过容器内 root 环境执行固定 SELECT，密码不会离开容器或进入输出。
cycle_schema_exists() {
  local schema_name=$1 result
  [[ "$schema_name" =~ ^molin_restore_57_reverify_[a-f0-9]{32}$ ]]
  result=$(/usr/bin/docker exec -i "$mysql_id" /bin/bash -s -- "$schema_name" <<'ROOT_SCHEMA_QUERY'
set -Eeuo pipefail
schema_name=$1
[[ "$schema_name" =~ ^molin_restore_57_reverify_[a-f0-9]{32}$ ]]
[[ -n "${MYSQL_ROOT_PASSWORD:-}" ]]
sql="SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='${schema_name}';"
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
[[ -n "$email_adapter" ]]

stage=health_transport
health_status=$(/usr/bin/curl --request GET --silent --show-error --max-time 5 --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8080/api/health)
stage=health
[[ "$health_status" == 200 ]]
stage=ready_transport
ready_status=$(/usr/bin/curl --request GET --silent --show-error --max-time 5 --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8080/api/ready)
stage=ready
[[ "$ready_status" == 200 ]]

stage=required_environment
for key in MYSQL_HOST MYSQL_PORT MYSQL_USER MYSQL_PASSWORD MYSQL_DATABASE REDIS_ADDR REDIS_PASSWORD REDIS_DB; do
  value=$(read_process_env "$api_pid" "$key")
  [[ -n "$value" || "$key" == REDIS_PASSWORD ]]
  printf -v "$key" '%s' "$value"
  export "$key"
done
mysql_user=$MYSQL_USER
mysql_password=$MYSQL_PASSWORD
mysql_database=$MYSQL_DATABASE
redis_password=$REDIS_PASSWORD
redis_db=$REDIS_DB
[[ "$mysql_database" == molin ]]
[[ "$mysql_user" =~ ^[A-Za-z0-9_]+$ ]]
[[ -n "$mysql_password" ]]
[[ "$redis_db" =~ ^[0-9]+$ ]]

stage=container_identity
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
(( ${#mysql_ids[@]} == 1 && ${#redis_ids[@]} == 1 ))
mysql_id=${mysql_ids[0]}
redis_id=${redis_ids[0]}

stage=schema_query
schema_result=$(mysql_scalar 'SELECT CONCAT(version, CHAR(9), IF(dirty, 1, 0), CHAR(9), (SELECT COUNT(*) FROM schema_migrations)) FROM schema_migrations LIMIT 1;')
IFS=$'\t' read -r schema_version schema_dirty schema_rows <<< "$schema_result"
stage=schema_gate
[[ "$schema_version" == 57 && "$schema_dirty" == 0 && "$schema_rows" == 1 ]]

stage=state_gate
mapfile -t state_candidates < <(/usr/bin/find /home/pc -mindepth 1 -maxdepth 1 -name 'molin-email-unknown-*.state' -print)
(( ${#state_candidates[@]} == 1 ))
state_file=${state_candidates[0]}
[[ "$state_file" =~ ^/home/pc/molin-email-unknown-([a-f0-9]{32})\.state$ ]]
operation_nonce=${BASH_REMATCH[1]}
[[ -f "$state_file" && ! -L "$state_file" ]]
[[ "$(/usr/bin/stat -c '%u:%a' -- "$state_file")" == "$(/usr/bin/id -u):600" ]]

stage=recovery_gate
# 恢复点不能独立枚举后碰巧取唯一值；只能由 state 文件名中的 operation nonce 推导精确文件名。
recovery_file="/home/pc/molin/rollback/molin-email-unknown-${operation_nonce}.sql"
[[ "$recovery_file" =~ ^/home/pc/molin/rollback/molin-email-unknown-([a-f0-9]{32})\.sql$ ]]
recovery_nonce=${BASH_REMATCH[1]}
[[ "$recovery_nonce" == "$operation_nonce" ]]
[[ "$(/usr/bin/find "$recovery_file" -mindepth 0 -maxdepth 0 -print)" == "$recovery_file" ]]
[[ -f "$recovery_file" && ! -L "$recovery_file" ]]
recovery_metadata=$(/usr/bin/stat -c '%u:%a:%s' -- "$recovery_file")
[[ "$recovery_metadata" =~ ^$(/usr/bin/id -u):600:([1-9][0-9]*)$ ]]
recovery_sha=$(/usr/bin/sha256sum -- "$recovery_file" | /usr/bin/awk '{print $1}')
[[ "$recovery_sha" =~ ^[a-f0-9]{64}$ ]]

stage=cycle_metadata
mapfile -t cycle_markers < <(/usr/bin/docker exec "$mysql_id" /usr/bin/find /root -mindepth 3 -maxdepth 3 -type f -path '/root/molin-000057-schema57-cycle-run-*/evidence/cycle_completed' -print)
(( ${#cycle_markers[@]} == 2 ))
cycle_dirs=()
cycle_dumps=()
cycle_targets=()
cycle_dump_shas=()
cycle_dir_metadata=()
cycle_marker_metadata=()
cycle_dump_metadata=()
for cycle_marker in "${cycle_markers[@]}"; do
  [[ "$cycle_marker" =~ ^(/root/molin-000057-schema57-cycle-run-([a-f0-9]{32}))/evidence/cycle_completed$ ]]
  cycle_dir=${BASH_REMATCH[1]}
  cycle_target="molin_restore_57_reverify_${BASH_REMATCH[2]}"
  cycle_dump="${cycle_dir}/evidence/molin_source_schema57.sql"
  [[ "$cycle_target" != "$mysql_database" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_dir" -mindepth 0 -maxdepth 0 -type d -print)" == "$cycle_dir" ]]
  current_cycle_dir_metadata=$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a' -- "$cycle_dir")
  [[ "$current_cycle_dir_metadata" == 0:700 ]]
  [[ -z "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_marker" -mindepth 0 -maxdepth 0 -type l -print)" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_marker" -mindepth 0 -maxdepth 0 -type f -print)" == "$cycle_marker" ]]
  # cycle_completed 只表达完成标记是否存在，允许零字节，但所有者和权限仍必须严格固定。
  current_cycle_marker_metadata=$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a' -- "$cycle_marker")
  [[ "$current_cycle_marker_metadata" == 0:600 ]]
  [[ -z "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_dump" -mindepth 0 -maxdepth 0 -type l -print)" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_dump" -mindepth 0 -maxdepth 0 -type f -print)" == "$cycle_dump" ]]
  current_cycle_dump_metadata=$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%s' -- "$cycle_dump")
  [[ "$current_cycle_dump_metadata" =~ ^0:600:[1-9][0-9]*$ ]]
  cycle_dump_sha=$(/usr/bin/docker exec "$mysql_id" /usr/bin/sha256sum -- "$cycle_dump" | /usr/bin/awk '{print $1}')
  [[ "$cycle_dump_sha" =~ ^[a-f0-9]{64}$ ]]
  [[ "$(cycle_schema_exists "$cycle_target")" == 1 ]]
  cycle_dirs+=("$cycle_dir")
  cycle_dumps+=("$cycle_dump")
  cycle_targets+=("$cycle_target")
  cycle_dump_shas+=("$cycle_dump_sha")
  cycle_dir_metadata+=("$current_cycle_dir_metadata")
  cycle_marker_metadata+=("$current_cycle_marker_metadata")
  cycle_dump_metadata+=("$current_cycle_dump_metadata")
done
[[ "${cycle_targets[0]}" != "${cycle_targets[1]}" ]]

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
required = {
    "version", "phase", "nonce", "redis_run_id", "operator_id", "template_id",
    "allowlist_id", "send_log_id", "unexpected_send_log_id",
}
if set(state) != required or first_cycle in raw or second_cycle in raw:
    raise ValueError("fields")
if state.get("version") != 1 or state.get("phase") != "phase1_created":
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
if state["send_log_id"] == state["unexpected_send_log_id"]:
    raise ValueError("duplicate_log")

email = f"phase4-{nonce}@example.invalid"
old_key = f"phase4-old-{nonce}"
new_key = f"phase4-new-{nonce}"
provider_template = f"qa-phase4-{nonce}"
template_name = "Phase4 Redis 重启隔离模板"
template_subject = "Phase4 隔离验证"
recipient_masked = email[:2] + "***" + email[email.rfind("@") :]
template_text = "<p>验证码：${Code}，有效期 ${ExpireMinutes} 分钟。</p>"
recipient_hmac = hmac.new(b"qa-phase4-address-secret-32-bytes-only", email.encode(), hashlib.sha256).hexdigest()
scope = f"admin-email-template-test:admin:{state['operator_id']}:template:{state['template_id']}:scene:register:recipient:{recipient_hmac}"
fingerprint = hashlib.sha256(f"POST\n/api/admin/email/templates/{state['template_id']}/test-send\nregister\n{recipient_hmac}".encode()).hexdigest()
old_hash = hashlib.sha256(old_key.encode()).hexdigest()
new_hash = hashlib.sha256(new_key.encode()).hexdigest()
lock_hash = hmac.new(b"qa-phase4-idempotency-secret-32-bytes", scope.encode(), hashlib.sha256).hexdigest()
values = (
    str(state["operator_id"]), str(state["template_id"]), str(state["allowlist_id"]),
    str(state["send_log_id"]), str(state["unexpected_send_log_id"]), recipient_hmac,
    scope.encode().hex(), fingerprint, old_hash, new_hash, provider_template.encode().hex(),
    template_name.encode().hex(), template_subject.encode().hex(),
    recipient_masked.encode().hex(), template_text.encode().hex(), hashlib.sha256(template_text.encode()).hexdigest(),
    "lock:email:dispatch:" + lock_hash,
)
print("\t".join(values))
PY
)
IFS=$'\t' read -r operator_id template_id allowlist_id primary_id unexpected_id recipient_hmac scope_hex fingerprint old_hash new_hash provider_template_hex template_name_hex template_subject_hex recipient_masked_hex template_text_hex template_text_hash lock_key <<< "$state_values"
for numeric_value in "$operator_id" "$template_id" "$allowlist_id" "$primary_id" "$unexpected_id"; do [[ "$numeric_value" =~ ^[1-9][0-9]*$ ]]; done
for digest in "$recipient_hmac" "$fingerprint" "$old_hash" "$new_hash"; do [[ "$digest" =~ ^[a-f0-9]{64}$ ]]; done
[[ "$scope_hex" =~ ^[a-f0-9]+$ && $(( ${#scope_hex} % 2 )) == 0 ]]
[[ "$provider_template_hex" =~ ^[a-f0-9]+$ && $(( ${#provider_template_hex} % 2 )) == 0 ]]
[[ "$template_name_hex" =~ ^[a-f0-9]+$ && $(( ${#template_name_hex} % 2 )) == 0 ]]
[[ "$template_subject_hex" =~ ^[a-f0-9]+$ && $(( ${#template_subject_hex} % 2 )) == 0 ]]
[[ "$recipient_masked_hex" =~ ^[a-f0-9]+$ && $(( ${#recipient_masked_hex} % 2 )) == 0 ]]
[[ "$template_text_hex" =~ ^[a-f0-9]+$ && $(( ${#template_text_hex} % 2 )) == 0 ]]
[[ "$template_text_hash" =~ ^[a-f0-9]{64}$ ]]
[[ "$lock_key" =~ ^lock:email:dispatch:[a-f0-9]{64}$ ]]

stage=fixture_ownership
primary_owned=$(mysql_scalar "SELECT COUNT(*) FROM email_send_logs WHERE id=${primary_id} AND template_id=${template_id} AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4) AND provider='aliyun_directmail' AND verification_code_id IS NULL AND scene='register' AND purpose='test' AND recipient_hmac='${recipient_hmac}' AND idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4) AND idempotency_key_hash='${old_hash}' AND request_fingerprint='${fingerprint}' AND status='failed' AND failure_reason='provider_outcome_unknown';")
unexpected_owned=$(mysql_scalar "SELECT COUNT(*) FROM email_send_logs WHERE id=${unexpected_id} AND template_id=${template_id} AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4) AND provider='aliyun_directmail' AND verification_code_id IS NULL AND scene='register' AND purpose='test' AND recipient_hmac='${recipient_hmac}' AND idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4) AND idempotency_key_hash='${new_hash}' AND request_fingerprint='${fingerprint}' AND status='failed' AND failure_reason='provider_outcome_unknown';")
scope_rows=$(mysql_scalar "SELECT COUNT(*) FROM email_send_logs WHERE idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4);")
template_owned=$(mysql_scalar "SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} AND provider='aliyun_directmail' AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4) AND name=CONVERT(0x${template_name_hex} USING utf8mb4) AND subject=CONVERT(0x${template_subject_hex} USING utf8mb4) AND sender_nickname IS NULL AND template_text=CONVERT(0x${template_text_hex} USING utf8mb4) AND JSON_LENGTH(variables_json)=2 AND JSON_CONTAINS(variables_json, JSON_QUOTE('Code')) AND JSON_CONTAINS(variables_json, JSON_QUOTE('ExpireMinutes')) AND content_sha256='${template_text_hash}' AND provider_status='approved' AND review_comment IS NULL AND variables_complete=1 AND local_enabled=1 AND missing=0 AND missing_since IS NULL AND provider_created_at IS NULL AND version=1;")
allowlist_owned=$(mysql_scalar "SELECT COUNT(*) FROM email_test_recipient_allowlist WHERE id=${allowlist_id} AND email_hmac='${recipient_hmac}' AND email_masked=CONVERT(0x${recipient_masked_hex} USING utf8mb4) AND status='active' AND version=1 AND created_by=${operator_id} AND updated_by=${operator_id} AND revoked_at IS NULL;")
[[ "$primary_owned" == 1 && "$unexpected_owned" == 1 && "$scope_rows" == 2 && "$template_owned" == 1 && "$allowlist_owned" == 1 ]]

stage=redis_exists
lock_exists=$(REDISCLI_AUTH="$redis_password" /usr/bin/docker exec -e REDISCLI_AUTH="$redis_password" "$redis_id" /usr/local/bin/redis-cli --raw -n "$redis_db" EXISTS "$lock_key")
[[ "$lock_exists" == 0 ]]

stage=binary_gate
cleanup_binary=/home/pc/molin/rollback/email-unknown-restart-cleanup.test
expected_binary_sha=__MOLIN_EXPECTED_CLEANUP_BINARY_SHA256__
[[ "$expected_binary_sha" =~ ^[a-f0-9]{64}$ && "$expected_binary_sha" != 0000000000000000000000000000000000000000000000000000000000000000 ]]
[[ -f "$cleanup_binary" && ! -L "$cleanup_binary" ]]
binary_metadata=$(/usr/bin/stat -c '%u:%a:%s' -- "$cleanup_binary")
[[ "$binary_metadata" =~ ^$(/usr/bin/id -u):500:([1-9][0-9]*)$ ]]
binary_sha=$(/usr/bin/sha256sum -- "$cleanup_binary" | /usr/bin/awk '{print $1}')
[[ "$binary_sha" =~ ^[a-f0-9]{64}$ && "$binary_sha" == "$expected_binary_sha" ]]

stage=cleanup_execute
export RUN_EMAIL_UNKNOWN_RESTART_INTEGRATION=1
export EMAIL_UNKNOWN_RESTART_ACK=I_UNDERSTAND_ISOLATED_EMAIL_UNKNOWN_RESTART_TEST
export RUN_EMAIL_UNKNOWN_RESTART_CLEANUP=1
export EMAIL_UNKNOWN_RESTART_CLEANUP_ACK=I_UNDERSTAND_EXACT_EMAIL_UNKNOWN_RESTART_CLEANUP
export EMAIL_UNKNOWN_RESTART_PHASE=cleanup
export EMAIL_UNKNOWN_RESTART_STATE_FILE="$state_file"
export EMAIL_UNKNOWN_RESTART_OPERATOR_ID="$operator_id"
export APP_ENV=test
export EMAIL_ADAPTER=mock
cleanup_output=$("$cleanup_binary" -test.run '^TestEmailUnknownTombstoneSurvivesRedisRestart$' -test.count=1 -test.v)
[[ "$cleanup_output" == *'classification=exact_cleanup_complete cleanup_db=true redis_key_absent=true state_removed=true'* ]]

stage=postflight
[[ ! -e "$state_file" ]]
[[ -f "$recovery_file" && ! -L "$recovery_file" ]]
[[ "$(/usr/bin/stat -c '%u:%a:%s' -- "$recovery_file")" == "$recovery_metadata" ]]
[[ "$(/usr/bin/sha256sum -- "$recovery_file" | /usr/bin/awk '{print $1}')" == "$recovery_sha" ]]
[[ -f "$cleanup_binary" && ! -L "$cleanup_binary" ]]
[[ "$(/usr/bin/stat -c '%u:%a:%s' -- "$cleanup_binary")" == "$binary_metadata" ]]
[[ "$(/usr/bin/sha256sum -- "$cleanup_binary" | /usr/bin/awk '{print $1}')" == "$expected_binary_sha" ]]
[[ "$(mysql_scalar "SELECT COUNT(*) FROM email_send_logs WHERE id IN (${primary_id},${unexpected_id}) OR idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4);")" == 0 ]]
[[ "$(mysql_scalar "SELECT COUNT(*) FROM email_test_recipient_allowlist WHERE id=${allowlist_id};")" == 0 ]]
[[ "$(mysql_scalar "SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id};")" == 0 ]]
[[ "$(REDISCLI_AUTH="$redis_password" /usr/bin/docker exec -e REDISCLI_AUTH="$redis_password" "$redis_id" /usr/local/bin/redis-cli --raw -n "$redis_db" EXISTS "$lock_key")" == 0 ]]
for index in 0 1; do
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "${cycle_dirs[$index]}" -mindepth 0 -maxdepth 0 -type d -print)" == "${cycle_dirs[$index]}" ]]
  [[ -z "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "${cycle_markers[$index]}" -mindepth 0 -maxdepth 0 -type l -print)" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "${cycle_markers[$index]}" -mindepth 0 -maxdepth 0 -type f -print)" == "${cycle_markers[$index]}" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "${cycle_dumps[$index]}" -mindepth 0 -maxdepth 0 -type f -print)" == "${cycle_dumps[$index]}" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a' -- "${cycle_dirs[$index]}")" == "${cycle_dir_metadata[$index]}" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a' -- "${cycle_markers[$index]}")" == "${cycle_marker_metadata[$index]}" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%s' -- "${cycle_dumps[$index]}")" == "${cycle_dump_metadata[$index]}" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/sha256sum -- "${cycle_dumps[$index]}" | /usr/bin/awk '{print $1}')" == "${cycle_dump_shas[$index]}" ]]
  [[ "$(cycle_schema_exists "${cycle_targets[$index]}")" == 1 ]]
done

trap - ERR
printf 'status=pass preflight_schema=57 preflight_dirty=false state_phase=phase1_created fixture_logs=2 fixture_allowlist=1 fixture_template=1 redis_key_preexisting=false cleanup_binary_launches=1 cleanup_db_logs=2 cleanup_allowlist=1 cleanup_template=1 redis_key_untouched=true state_removed=true backup_retained=true cycle_assets_retained=2 retries=0\n'
