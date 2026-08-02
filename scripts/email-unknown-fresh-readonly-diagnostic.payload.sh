#!/usr/bin/env bash
# DirectMail Phase 4 Redis unknown 保留现场纯只读诊断载荷。
set -Eeuo pipefail
umask 077

# 标准错误在远端进程内关闭，防止容器、数据库或路径细节进入验收输出。
exec 2>/dev/null

stage=argument_gate
phase=unknown
stage_count=0
stage_identity=false
file_count=0
files_identity=false
hashes_match=false
state_class=unknown
state_identity=false
stage_nonce_match=false

fail() {
  local classification=${1:-$stage}
  trap - ERR
  printf 'status=failed classification=%s phase=%s stage_count=%s stage_identity=%s file_count=%s files_identity=%s hashes_match=%s state_class=%s state_identity=%s stage_nonce_match=%s writes=false cleanup=false restart=false retries=0\n' \
    "$classification" "$phase" "$stage_count" "$stage_identity" "$file_count" "$files_identity" "$hashes_match" "$state_class" "$state_identity" "$stage_nonce_match"
  exit 2
}
trap 'fail "$stage"' ERR

[[ $# -eq 3 ]]
readonly old_binary_sha=$1
readonly old_payload_sha=$2
readonly expected_operator_id=$3
[[ "$old_binary_sha" =~ ^[a-f0-9]{64}$ ]]
[[ "$old_payload_sha" =~ ^[a-f0-9]{64}$ ]]
[[ "$expected_operator_id" =~ ^[1-9][0-9]*$ ]]
export PATH=/usr/sbin:/usr/bin:/sbin:/bin

stage=stage_count
mapfile -t stage_candidates < <(/usr/bin/find /home/pc/molin-runtime -mindepth 1 -maxdepth 1 -type d -name 'email-unknown-cycle-*' -print)
mapfile -t stage_links < <(/usr/bin/find /home/pc/molin-runtime -mindepth 1 -maxdepth 1 -type l -name 'email-unknown-cycle-*' -print)
stage_count=${#stage_candidates[@]}
[[ $stage_count -eq 1 && ${#stage_links[@]} -eq 0 ]]
readonly retained_stage=${stage_candidates[0]}

stage=stage_identity
[[ "$retained_stage" =~ ^/home/pc/molin-runtime/email-unknown-cycle-([a-f0-9]{32})$ ]]
readonly operation_id=${BASH_REMATCH[1]}
[[ ! -L "$retained_stage" ]]
[[ "$(/usr/bin/stat -c '%U:%a' -- "$retained_stage")" == 'pc:700' ]]
stage_identity=true

readonly old_binary="${retained_stage}/email-unknown-restart.test"
readonly old_payload="${retained_stage}/cycle.payload.sh"
readonly state_file="${retained_stage}/cycle.state"

stage=file_identity
mapfile -t retained_names < <(/usr/bin/find "$retained_stage" -mindepth 1 -maxdepth 1 -printf '%f\n' | /usr/bin/sort)
file_count=${#retained_names[@]}
[[ $file_count -eq 3 ]]
[[ "${retained_names[0]}" == cycle.payload.sh ]]
[[ "${retained_names[1]}" == cycle.state ]]
[[ "${retained_names[2]}" == email-unknown-restart.test ]]
for specification in "$old_binary:500" "$old_payload:500" "$state_file:600"; do
  path=${specification%:*}
  mode=${specification##*:}
  [[ -f "$path" && ! -L "$path" ]]
  [[ "$(/usr/bin/stat -c '%U:%a' -- "$path")" == "pc:${mode}" ]]
done
files_identity=true

stage=asset_hash
[[ "$(/usr/bin/sha256sum -- "$old_binary")" == "${old_binary_sha}  ${old_binary}" ]]
[[ "$(/usr/bin/sha256sum -- "$old_payload")" == "${old_payload_sha}  ${old_payload}" ]]
hashes_match=true

stage=state_parse
# 状态解析只把派生值交给本进程后续查询，最终摘要不会输出任何原始身份值。
state_values=$(/usr/bin/python3 -I -B - "$state_file" "$expected_operator_id" "$operation_id" <<'PY'
import hashlib
import hmac
import json
import os
import re
import stat
import sys

path, operator_text, operation_id = sys.argv[1:]
expected_operator = int(operator_text)
info = os.lstat(path)
if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600 or info.st_uid != os.geteuid():
    raise SystemExit(2)

def strict_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate")
        result[key] = value
    return result

with open(path, "r", encoding="utf-8") as stream:
    state = json.load(stream, object_pairs_hook=strict_object)
required = {
    "version", "phase", "nonce", "redis_run_id", "operator_id",
    "template_id", "allowlist_id", "send_log_id",
}
if set(state) not in (required, required | {"unexpected_send_log_id"}):
    raise SystemExit(2)
if state.get("version") != 1 or state.get("phase") not in {"initializing", "phase1_created"}:
    raise SystemExit(2)
nonce = state.get("nonce")
run_id = state.get("redis_run_id")
if not isinstance(nonce, str) or re.fullmatch(r"[a-f0-9]{32}", nonce) is None:
    raise SystemExit(2)
if not isinstance(run_id, str) or re.fullmatch(r"[a-f0-9]{40}", run_id) is None:
    raise SystemExit(2)
if state.get("operator_id") != expected_operator:
    raise SystemExit(2)
ids = [state.get("template_id"), state.get("allowlist_id"), state.get("send_log_id")]
if any(type(value) is not int or value < 0 for value in ids):
    raise SystemExit(2)
if type(state.get("unexpected_send_log_id", 0)) is not int or state.get("unexpected_send_log_id", 0) != 0:
    raise SystemExit(2)
if ids[0] == 0 and (ids[1] != 0 or ids[2] != 0):
    raise SystemExit(2)
if ids[1] == 0 and ids[2] != 0:
    raise SystemExit(2)
if state["phase"] == "phase1_created" and any(value <= 0 for value in ids):
    raise SystemExit(2)
if state["phase"] == "initializing" and ids[2] != 0:
    raise SystemExit(2)

email = f"phase4-{nonce}@example.invalid"
old_key = f"phase4-old-{nonce}"
provider_template = f"qa-phase4-{nonce}"
recipient_hmac = hmac.new(b"qa-phase4-address-secret-32-bytes-only", email.encode(), hashlib.sha256).hexdigest()
scope = f"admin-email-template-test:admin:{expected_operator}:template:{ids[0]}:scene:register:recipient:{recipient_hmac}"
fingerprint = hashlib.sha256(f"POST\n/api/admin/email/templates/{ids[0]}/test-send\nregister\n{recipient_hmac}".encode()).hexdigest()
old_hash = hashlib.sha256(old_key.encode()).hexdigest()
lock_digest = hmac.new(b"qa-phase4-idempotency-secret-32-bytes", scope.encode(), hashlib.sha256).hexdigest()
masked = email[:2] + "***" + email[email.rfind("@"):]
template_text = "<p>验证码：${Code}，有效期 ${ExpireMinutes} 分钟。</p>"

def as_hex(value):
    return value.encode().hex()

values = (
    state["phase"], "complete" if state["phase"] == "phase1_created" else "partial",
    "true" if nonce == operation_id else "false", run_id,
    str(ids[0]), str(ids[1]), str(ids[2]), recipient_hmac, as_hex(scope), fingerprint,
    old_hash, as_hex(provider_template), as_hex(masked), as_hex(template_text),
    hashlib.sha256(template_text.encode()).hexdigest(), lock_digest,
)
print("\t".join(values))
PY
)
IFS=$'\t' read -r phase state_class stage_nonce_match recorded_run_id template_id allowlist_id send_log_id recipient_hmac scope_hex fingerprint old_hash provider_template_hex recipient_masked_hex template_text_hex template_text_hash lock_digest <<< "$state_values"
[[ "$phase" =~ ^(initializing|phase1_created)$ ]]
[[ "$state_class" =~ ^(partial|complete)$ ]]
[[ "$stage_nonce_match" =~ ^(true|false)$ ]]
for value in "$template_id" "$allowlist_id" "$send_log_id"; do [[ "$value" =~ ^[0-9]+$ ]]; done
for value in "$recorded_run_id" "$recipient_hmac" "$fingerprint" "$old_hash" "$template_text_hash" "$lock_digest"; do [[ "$value" =~ ^[a-f0-9]{40}$|^[a-f0-9]{64}$ ]]; done
for value in "$scope_hex" "$provider_template_hex" "$recipient_masked_hex" "$template_text_hex"; do [[ "$value" =~ ^[a-f0-9]+$ && $(( ${#value} % 2 )) -eq 0 ]]; done
state_identity=true

stage=api_identity
mapfile -t api_pids < <(/usr/bin/pgrep -x molin-api || true)
[[ ${#api_pids[@]} -eq 1 && -r "/proc/${api_pids[0]}/environ" ]]
while IFS= read -r -d '' entry; do export "$entry"; done < "/proc/${api_pids[0]}/environ"
for key in MYSQL_USER MYSQL_PASSWORD MYSQL_DATABASE REDIS_DB; do [[ -n "${!key:-}" ]]; done
[[ "${APP_ENV:-}" == test && "$MYSQL_DATABASE" == molin ]]
[[ "$REDIS_DB" =~ ^[0-9]+$ ]]

stage=container_identity
mapfile -t mysql_ids < <(/usr/bin/timeout 3 /usr/bin/docker ps --filter 'name=^/molin-mysql$' --format '{{.ID}}')
mapfile -t redis_ids < <(/usr/bin/timeout 3 /usr/bin/docker ps --filter 'name=^/molin-redis$' --format '{{.ID}}')
[[ ${#mysql_ids[@]} -eq 1 && ${#redis_ids[@]} -eq 1 ]]
readonly mysql_id=${mysql_ids[0]}
readonly redis_id=${redis_ids[0]}

stage=database_snapshot
# 单条 SELECT 同时证明迁移状态、真实表名和三条夹具的完整业务归属。
snapshot=$(/usr/bin/docker exec -e MYSQL_PWD="$MYSQL_PASSWORD" "$mysql_id" mysql --no-defaults \
  --host=127.0.0.1 --port=3306 --user="$MYSQL_USER" --database="$MYSQL_DATABASE" \
  --batch --skip-column-names --raw --execute="SELECT CONCAT_WS(CHAR(9),
  (SELECT version FROM schema_migrations LIMIT 1),
  (SELECT IF(dirty,1,0) FROM schema_migrations LIMIT 1),
  (SELECT COUNT(*) FROM schema_migrations),
  (SELECT COUNT(*) FROM users WHERE id=${expected_operator_id}),
  (SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='email_test_recipient_allowlist'),
  (SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='email_test_recipient_allowlists'),
  (SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} AND provider='aliyun_directmail' AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4) AND name=CONVERT(0x50686173653420526564697320e9878de590afe99a94e7a6bbe6a8a1e69dbf USING utf8mb4) AND subject=CONVERT(0x50686173653420e99a94e7a6bbe9aa8ce8af81 USING utf8mb4) AND sender_nickname IS NULL AND template_text=CONVERT(0x${template_text_hex} USING utf8mb4) AND JSON_LENGTH(variables_json)=2 AND JSON_CONTAINS(variables_json,JSON_QUOTE('Code')) AND JSON_CONTAINS(variables_json,JSON_QUOTE('ExpireMinutes')) AND content_sha256='${template_text_hash}' AND provider_status='approved' AND review_comment IS NULL AND variables_complete=1 AND local_enabled=1 AND missing=0 AND missing_since IS NULL AND provider_created_at IS NULL AND version=1),
  (SELECT COUNT(*) FROM email_test_recipient_allowlist WHERE id=${allowlist_id} AND email_hmac='${recipient_hmac}' AND email_masked=CONVERT(0x${recipient_masked_hex} USING utf8mb4) AND status='active' AND version=1 AND created_by=${expected_operator_id} AND updated_by=${expected_operator_id} AND revoked_at IS NULL),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${send_log_id} AND template_id=${template_id} AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4) AND provider='aliyun_directmail' AND verification_code_id IS NULL AND scene='register' AND purpose='test' AND recipient_hmac='${recipient_hmac}' AND idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4) AND idempotency_key_hash='${old_hash}' AND request_fingerprint='${fingerprint}' AND status='failed' AND failure_reason='provider_outcome_unknown'),
  (SELECT COUNT(*) FROM email_send_logs WHERE idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4))
);" )
[[ "$snapshot" != *$'\n'* ]]
IFS=$'\t' read -r schema_version dirty migration_rows operator_rows singular_table_rows plural_table_rows template_rows allowlist_rows send_log_rows scope_rows <<< "$snapshot"
for value in "$schema_version" "$dirty" "$migration_rows" "$operator_rows" "$singular_table_rows" "$plural_table_rows" "$template_rows" "$allowlist_rows" "$send_log_rows" "$scope_rows"; do [[ "$value" =~ ^[0-9]+$ ]]; done
[[ "$schema_version" == 57 && "$dirty" == 0 && "$migration_rows" == 1 && "$operator_rows" == 1 ]]
[[ "$singular_table_rows" == 1 && "$plural_table_rows" == 0 ]]

stage=fixture_ownership
expected_template_rows=$(( template_id > 0 ? 1 : 0 ))
expected_allowlist_rows=$(( allowlist_id > 0 ? 1 : 0 ))
expected_send_log_rows=$(( send_log_id > 0 ? 1 : 0 ))
[[ "$template_rows" -eq "$expected_template_rows" ]]
[[ "$allowlist_rows" -eq "$expected_allowlist_rows" ]]
[[ "$send_log_rows" -eq "$expected_send_log_rows" ]]
[[ "$scope_rows" -eq "$expected_send_log_rows" ]]

stage=redis_ping
redis_ping=$(/usr/bin/docker exec -e REDISCLI_AUTH="${REDIS_PASSWORD:-}" "$redis_id" redis-cli -n "$REDIS_DB" --raw PING)
[[ "$redis_ping" == PONG ]]

stage=redis_identity
current_run_id=$(/usr/bin/docker exec -e REDISCLI_AUTH="${REDIS_PASSWORD:-}" "$redis_id" redis-cli -n "$REDIS_DB" --raw INFO server | /usr/bin/sed -n 's/^run_id:\([a-f0-9]\{40\}\)\r\{0,1\}$/\1/p')
[[ "$current_run_id" =~ ^[a-f0-9]{40}$ ]]
if [[ "$current_run_id" == "$recorded_run_id" ]]; then redis_identity=true; else redis_identity=false; fi

stage=redis_exact_exists
readonly exact_key="lock:email:dispatch:${lock_digest}"
redis_key_exists=$(/usr/bin/docker exec -e REDISCLI_AUTH="${REDIS_PASSWORD:-}" "$redis_id" redis-cli -n "$REDIS_DB" --raw EXISTS "$exact_key")
[[ "$redis_key_exists" =~ ^[01]$ ]]

trap - ERR
printf 'status=pass classification=diagnostic_complete phase=%s stage_count=%s stage_identity=%s file_count=%s files_identity=%s hashes_match=%s state_class=%s state_identity=%s stage_nonce_match=%s schema=%s dirty=%s migration_rows=%s operator_rows=%s singular_table_rows=%s plural_table_rows=%s template_rows=%s allowlist_rows=%s send_log_rows=%s scope_rows=%s redis_ping=true redis_identity=%s redis_key_exists=%s writes=false cleanup=false restart=false retries=0\n' \
  "$phase" "$stage_count" "$stage_identity" "$file_count" "$files_identity" "$hashes_match" "$state_class" "$state_identity" "$stage_nonce_match" \
  "$schema_version" "$dirty" "$migration_rows" "$operator_rows" "$singular_table_rows" "$plural_table_rows" "$template_rows" "$allowlist_rows" "$send_log_rows" "$scope_rows" "$redis_identity" "$redis_key_exists"
