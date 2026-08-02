set -Eeuo pipefail
stage=shell_options

# 诊断只输出固定计数与布尔值；关闭标准错误，避免容器、路径或数据库细节进入终端。
exec 2>/dev/null
fail() {
  local failed_stage=$1
  trap - ERR
  /usr/bin/cat >/dev/null || true
  printf 'status=failed stage=%s writes=false backup=false cleanup=false restarts=false retries=0\n' "$failed_stage"
  exit 2
}
trap 'fail "$stage"' ERR

if ! shopt -qo errexit || ! shopt -qo nounset || ! shopt -qo pipefail; then
  fail shell_options
fi
export PATH=/usr/sbin:/usr/bin:/sbin:/bin

stage=state_gate
mapfile -t state_candidates < <(/usr/bin/find /home/pc -mindepth 1 -maxdepth 1 -name 'molin-email-unknown-*.state' -print)
(( ${#state_candidates[@]} == 1 ))
state_file=${state_candidates[0]}
[[ "$state_file" =~ ^/home/pc/molin-email-unknown-[a-f0-9]{32}\.state$ ]]
[[ -f "$state_file" && ! -L "$state_file" ]]
[[ "$(/usr/bin/stat -c '%u:%a' -- "$state_file")" == "$(/usr/bin/id -u):600" ]]

stage=container_gate
mapfile -t container_lines < <(/usr/bin/docker ps --format '{{.ID}}|{{.Image}}|{{.Names}}')
mysql_ids=()
for container_line in "${container_lines[@]}"; do
  container_id=${container_line%%|*}
  container_identity=${container_line#*|}
  container_identity=${container_identity,,}
  [[ "$container_id" =~ ^[a-f0-9]{12,64}$ ]]
  case "$container_identity" in
    *mysql*) mysql_ids+=("$container_id") ;;
  esac
done
(( ${#mysql_ids[@]} == 1 ))
mysql_id=${mysql_ids[0]}

stage=state_parse
# fixture nonce 只从 JSON 读取并用于派生夹具身份；状态文件名仅承担 artifact 定位，不参与业务派生。
state_values=$(/usr/bin/python3 - "$state_file" <<'PY'
import hashlib
import hmac
import json
import re
import sys

path = sys.argv[1]

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
if set(state) - allowed:
    raise ValueError("fields")
if state.get("version") != 1 or state.get("phase") != "phase1_created":
    raise ValueError("phase")
nonce = state.get("nonce")
if not isinstance(nonce, str) or not re.fullmatch(r"[a-f0-9]{32}", nonce):
    raise ValueError("nonce")
if not isinstance(state.get("redis_run_id"), str) or not re.fullmatch(r"[a-f0-9]{40}", state["redis_run_id"]):
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
recipient_masked = email[:2] + "***" + email[email.rfind("@"):]
template_text = "<p>验证码：${Code}，有效期 ${ExpireMinutes} 分钟。</p>"
recipient_hmac = hmac.new(b"qa-phase4-address-secret-32-bytes-only", email.encode(), hashlib.sha256).hexdigest()
scope = f"admin-email-template-test:admin:{state['operator_id']}:template:{state['template_id']}:scene:register:recipient:{recipient_hmac}"
fingerprint = hashlib.sha256(f"POST\n/api/admin/email/templates/{state['template_id']}/test-send\nregister\n{recipient_hmac}".encode()).hexdigest()
old_hash = hashlib.sha256(old_key.encode()).hexdigest()
new_hash = hashlib.sha256(new_key.encode()).hexdigest()
values = (
    str(state["operator_id"]), str(state["template_id"]), str(state["allowlist_id"]),
    str(state["send_log_id"]), str(state["unexpected_send_log_id"]), recipient_hmac,
    scope.encode().hex(), fingerprint, old_hash, new_hash, provider_template.encode().hex(),
    template_name.encode().hex(), template_subject.encode().hex(),
    recipient_masked.encode().hex(), template_text.encode().hex(), hashlib.sha256(template_text.encode()).hexdigest(),
)
print("\t".join(values))
PY
)
IFS=$'\t' read -r operator_id template_id allowlist_id primary_id unexpected_id recipient_hmac scope_hex fingerprint old_hash new_hash provider_template_hex template_name_hex template_subject_hex recipient_masked_hex template_text_hex template_text_hash <<< "$state_values"
for value in "$operator_id" "$template_id" "$allowlist_id" "$primary_id" "$unexpected_id"; do [[ "$value" =~ ^[1-9][0-9]*$ ]]; done
for value in "$recipient_hmac" "$fingerprint" "$old_hash" "$new_hash" "$template_text_hash"; do [[ "$value" =~ ^[a-f0-9]{64}$ ]]; done
for value in "$scope_hex" "$provider_template_hex" "$template_name_hex" "$template_subject_hex" "$recipient_masked_hex" "$template_text_hex"; do
  [[ "$value" =~ ^[a-f0-9]+$ && $(( ${#value} % 2 )) == 0 ]]
done

stage=database_snapshot
# 单条 SELECT 返回全归属计数和字段组计数；root 凭据只在容器内部读取，摘要本身不会输出。
snapshot=$(/usr/bin/docker exec -i "$mysql_id" /bin/bash -s -- \
  "$operator_id" "$template_id" "$allowlist_id" "$primary_id" "$unexpected_id" \
  "$recipient_hmac" "$scope_hex" "$fingerprint" "$old_hash" "$new_hash" \
  "$provider_template_hex" "$template_name_hex" "$template_subject_hex" \
  "$recipient_masked_hex" "$template_text_hex" "$template_text_hash" <<'MYSQL_SNAPSHOT'
set -Eeuo pipefail
operator_id=$1; template_id=$2; allowlist_id=$3; primary_id=$4; unexpected_id=$5
recipient_hmac=$6; scope_hex=$7; fingerprint=$8; old_hash=$9; new_hash=${10}
provider_template_hex=${11}; template_name_hex=${12}; template_subject_hex=${13}
recipient_masked_hex=${14}; template_text_hex=${15}; template_text_hash=${16}
for value in "$operator_id" "$template_id" "$allowlist_id" "$primary_id" "$unexpected_id"; do [[ "$value" =~ ^[1-9][0-9]*$ ]]; done
for value in "$recipient_hmac" "$fingerprint" "$old_hash" "$new_hash" "$template_text_hash"; do [[ "$value" =~ ^[a-f0-9]{64}$ ]]; done
for value in "$scope_hex" "$provider_template_hex" "$template_name_hex" "$template_subject_hex" "$recipient_masked_hex" "$template_text_hex"; do [[ "$value" =~ ^[a-f0-9]+$ && $(( ${#value} % 2 )) == 0 ]]; done
[[ -n "${MYSQL_ROOT_PASSWORD:-}" ]]
sql="SELECT CONCAT_WS(CHAR(9),
  (SELECT version FROM schema_migrations LIMIT 1),
  (SELECT IF(dirty,1,0) FROM schema_migrations LIMIT 1),
  (SELECT COUNT(*) FROM schema_migrations),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${primary_id} AND template_id=${template_id} AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4) AND provider='aliyun_directmail' AND verification_code_id IS NULL AND scene='register' AND purpose='test' AND recipient_hmac='${recipient_hmac}' AND idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4) AND idempotency_key_hash='${old_hash}' AND request_fingerprint='${fingerprint}' AND status='failed' AND failure_reason='provider_outcome_unknown'),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${primary_id}),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${primary_id} AND provider='aliyun_directmail'),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${primary_id} AND template_id=${template_id} AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4)),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${primary_id} AND verification_code_id IS NULL AND scene='register' AND purpose='test' AND recipient_hmac='${recipient_hmac}'),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${primary_id} AND idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4) AND idempotency_key_hash='${old_hash}' AND request_fingerprint='${fingerprint}'),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${primary_id} AND status='failed' AND failure_reason='provider_outcome_unknown'),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${unexpected_id} AND template_id=${template_id} AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4) AND provider='aliyun_directmail' AND verification_code_id IS NULL AND scene='register' AND purpose='test' AND recipient_hmac='${recipient_hmac}' AND idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4) AND idempotency_key_hash='${new_hash}' AND request_fingerprint='${fingerprint}' AND status='failed' AND failure_reason='provider_outcome_unknown'),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${unexpected_id}),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${unexpected_id} AND provider='aliyun_directmail'),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${unexpected_id} AND template_id=${template_id} AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4)),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${unexpected_id} AND verification_code_id IS NULL AND scene='register' AND purpose='test' AND recipient_hmac='${recipient_hmac}'),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${unexpected_id} AND idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4) AND idempotency_key_hash='${new_hash}' AND request_fingerprint='${fingerprint}'),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${unexpected_id} AND status='failed' AND failure_reason='provider_outcome_unknown'),
  (SELECT COUNT(*) FROM email_send_logs WHERE idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4)),
  (SELECT COUNT(*) FROM email_test_recipient_allowlist WHERE id=${allowlist_id} AND email_hmac='${recipient_hmac}' AND email_masked=CONVERT(0x${recipient_masked_hex} USING utf8mb4) AND status='active' AND version=1 AND created_by=${operator_id} AND updated_by=${operator_id} AND revoked_at IS NULL),
  (SELECT COUNT(*) FROM email_test_recipient_allowlist WHERE id=${allowlist_id}),
  (SELECT COUNT(*) FROM email_test_recipient_allowlist WHERE id=${allowlist_id} AND email_hmac='${recipient_hmac}' AND email_masked=CONVERT(0x${recipient_masked_hex} USING utf8mb4)),
  (SELECT COUNT(*) FROM email_test_recipient_allowlist WHERE id=${allowlist_id} AND status='active'),
  (SELECT COUNT(*) FROM email_test_recipient_allowlist WHERE id=${allowlist_id} AND created_by=${operator_id} AND updated_by=${operator_id}),
  (SELECT COUNT(*) FROM email_test_recipient_allowlist WHERE id=${allowlist_id} AND version=1 AND revoked_at IS NULL),
  (SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} AND provider='aliyun_directmail' AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4) AND name=CONVERT(0x${template_name_hex} USING utf8mb4) AND subject=CONVERT(0x${template_subject_hex} USING utf8mb4) AND sender_nickname IS NULL AND template_text=CONVERT(0x${template_text_hex} USING utf8mb4) AND JSON_LENGTH(variables_json)=2 AND JSON_CONTAINS(variables_json, JSON_QUOTE('Code')) AND JSON_CONTAINS(variables_json, JSON_QUOTE('ExpireMinutes')) AND content_sha256='${template_text_hash}' AND provider_status='approved' AND review_comment IS NULL AND variables_complete=1 AND local_enabled=1 AND missing=0 AND missing_since IS NULL AND provider_created_at IS NULL AND version=1),
  (SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id}),
  (SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} AND provider='aliyun_directmail' AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4)),
  (SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} AND name=CONVERT(0x${template_name_hex} USING utf8mb4)),
  (SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} AND subject=CONVERT(0x${template_subject_hex} USING utf8mb4)),
  (SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} AND sender_nickname IS NULL AND template_text=CONVERT(0x${template_text_hex} USING utf8mb4)),
  (SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} AND JSON_LENGTH(variables_json)=2 AND JSON_CONTAINS(variables_json, JSON_QUOTE('Code')) AND JSON_CONTAINS(variables_json, JSON_QUOTE('ExpireMinutes'))),
  (SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} AND content_sha256='${template_text_hash}'),
  (SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} AND provider_status='approved' AND review_comment IS NULL),
  (SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} AND variables_complete=1 AND local_enabled=1 AND missing=0 AND missing_since IS NULL AND provider_created_at IS NULL),
  (SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} AND version=1),
  LOWER(SHA2(CONCAT_WS(CHAR(31),
    COALESCE((SELECT LOWER(SHA2(CONCAT_WS(CHAR(30),CAST(id AS CHAR),CAST(template_id AS CHAR),HEX(provider_template_id),provider,COALESCE(CAST(verification_code_id AS CHAR),'NULL'),scene,purpose,recipient_hmac,HEX(idempotency_scope),idempotency_key_hash,request_fingerprint,status,failure_reason),256)) FROM email_send_logs WHERE id=${primary_id}),'MISSING'),
    COALESCE((SELECT LOWER(SHA2(CONCAT_WS(CHAR(30),CAST(id AS CHAR),CAST(template_id AS CHAR),HEX(provider_template_id),provider,COALESCE(CAST(verification_code_id AS CHAR),'NULL'),scene,purpose,recipient_hmac,HEX(idempotency_scope),idempotency_key_hash,request_fingerprint,status,failure_reason),256)) FROM email_send_logs WHERE id=${unexpected_id}),'MISSING'),
    COALESCE((SELECT LOWER(SHA2(CONCAT_WS(CHAR(30),CAST(id AS CHAR),email_hmac,HEX(email_masked),status,CAST(version AS CHAR),CAST(created_by AS CHAR),CAST(updated_by AS CHAR),COALESCE(CAST(revoked_at AS CHAR),'NULL')),256)) FROM email_test_recipient_allowlist WHERE id=${allowlist_id}),'MISSING'),
    COALESCE((SELECT LOWER(SHA2(CONCAT_WS(CHAR(30),CAST(id AS CHAR),provider,HEX(provider_template_id),HEX(name),HEX(subject),COALESCE(HEX(sender_nickname),'NULL'),HEX(template_text),variables_json,content_sha256,provider_status,COALESCE(HEX(review_comment),'NULL'),CAST(variables_complete AS CHAR),CAST(local_enabled AS CHAR),CAST(missing AS CHAR),COALESCE(CAST(missing_since AS CHAR),'NULL'),COALESCE(CAST(provider_created_at AS CHAR),'NULL'),CAST(version AS CHAR)),256)) FROM email_provider_templates WHERE id=${template_id}),'MISSING')
  ),256))
);"
result=$(MYSQL_PWD="$MYSQL_ROOT_PASSWORD" /usr/bin/mysql --no-defaults --host=127.0.0.1 --port=3306 --user=root --database=molin --batch --skip-column-names --raw --execute="$sql")
[[ "$result" != *$'\n'* ]]
printf '%s' "$result"
MYSQL_SNAPSHOT
)

stage=snapshot_parse
IFS=$'\t' read -r version dirty migration_rows \
  primary_count primary_id_match primary_provider_match primary_template_match primary_target_match primary_idempotency_match primary_status_match \
  unexpected_count unexpected_id_match unexpected_provider_match unexpected_template_match unexpected_target_match unexpected_idempotency_match unexpected_status_match \
  scope_count allowlist_count allowlist_id_match allowlist_recipient_match allowlist_state_match allowlist_ownership_match allowlist_lifecycle_match \
  template_count template_id_match template_provider_match template_name_match template_subject_match template_text_match semantic_variables_match template_content_sha_match template_status_match template_flags_match template_version_match digest <<< "$snapshot"
for value in "$version" "$dirty" "$migration_rows" \
  "$primary_count" "$primary_id_match" "$primary_provider_match" "$primary_template_match" "$primary_target_match" "$primary_idempotency_match" "$primary_status_match" \
  "$unexpected_count" "$unexpected_id_match" "$unexpected_provider_match" "$unexpected_template_match" "$unexpected_target_match" "$unexpected_idempotency_match" "$unexpected_status_match" \
  "$scope_count" "$allowlist_count" "$allowlist_id_match" "$allowlist_recipient_match" "$allowlist_state_match" "$allowlist_ownership_match" "$allowlist_lifecycle_match" \
  "$template_count" "$template_id_match" "$template_provider_match" "$template_name_match" "$template_subject_match" "$template_text_match" "$semantic_variables_match" "$template_content_sha_match" "$template_status_match" "$template_flags_match" "$template_version_match"; do
  [[ "$value" =~ ^[0-9]+$ ]]
done
if [[ "$digest" =~ ^[a-f0-9]{64}$ ]]; then digest_valid=true; else digest_valid=false; fi

trap - ERR
printf 'status=pass version=%s dirty=%s migration_rows=%s primary_count=%s primary_id_match=%s primary_provider_match=%s primary_template_match=%s primary_target_match=%s primary_idempotency_match=%s primary_status_match=%s unexpected_count=%s unexpected_id_match=%s unexpected_provider_match=%s unexpected_template_match=%s unexpected_target_match=%s unexpected_idempotency_match=%s unexpected_status_match=%s scope_count=%s allowlist_count=%s allowlist_id_match=%s allowlist_recipient_match=%s allowlist_state_match=%s allowlist_ownership_match=%s allowlist_lifecycle_match=%s template_count=%s template_id_match=%s template_provider_match=%s template_name_match=%s template_subject_match=%s template_text_match=%s semantic_variables_match=%s template_content_sha_match=%s template_status_match=%s template_flags_match=%s template_version_match=%s digest_valid=%s writes=false backup=false cleanup=false restarts=false ssh_attempts=1 retries=0\n' \
  "$version" "$dirty" "$migration_rows" \
  "$primary_count" "$primary_id_match" "$primary_provider_match" "$primary_template_match" "$primary_target_match" "$primary_idempotency_match" "$primary_status_match" \
  "$unexpected_count" "$unexpected_id_match" "$unexpected_provider_match" "$unexpected_template_match" "$unexpected_target_match" "$unexpected_idempotency_match" "$unexpected_status_match" \
  "$scope_count" "$allowlist_count" "$allowlist_id_match" "$allowlist_recipient_match" "$allowlist_state_match" "$allowlist_ownership_match" "$allowlist_lifecycle_match" \
  "$template_count" "$template_id_match" "$template_provider_match" "$template_name_match" "$template_subject_match" "$template_text_match" "$semantic_variables_match" "$template_content_sha_match" "$template_status_match" "$template_flags_match" "$template_version_match" "$digest_valid"
