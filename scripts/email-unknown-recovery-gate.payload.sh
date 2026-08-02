set -Eeuo pipefail
stage=shell_options
published=false
temp_file=
temp_identity=

# 所有新文件默认仅允许当前用户访问；远端标准错误始终关闭，避免泄露容器或数据库细节。
umask 077
exec 2>/dev/null

cleanup_unpublished_temp() {
  [[ -n "$temp_file" && "$published" == false ]] || return 0
  [[ "$temp_file" =~ ^/home/pc/molin/rollback/\.molin-email-recovery\.[A-Za-z0-9]{10}$ ]] || return 1
  if [[ -e "$temp_file" || -L "$temp_file" ]]; then
    [[ -f "$temp_file" && ! -L "$temp_file" ]] || return 1
    [[ "$(/usr/bin/stat -c '%u:%d:%i' -- "$temp_file")" == "$temp_identity" ]] || return 1
    /usr/bin/unlink -- "$temp_file"
  fi
}

fail() {
  local failed_stage=$1
  trap - ERR
  cleanup_unpublished_temp || true
  /usr/bin/cat >/dev/null || true
  printf 'status=failed stage=%s backup_published=%s\n' "$failed_stage" "$published"
  exit 2
}
trap 'fail "$stage"' ERR

if ! shopt -qo errexit || ! shopt -qo nounset || ! shopt -qo pipefail; then
  fail shell_options
fi
export PATH=/usr/sbin:/usr/bin:/sbin:/bin

stage=state_gate
# 必须恰好存在一个候选状态文件；即使候选是符号链接也计入并随后拒绝，不能静默绕过。
mapfile -t state_candidates < <(/usr/bin/find /home/pc -mindepth 1 -maxdepth 1 -name 'molin-email-unknown-*.state' -print)
(( ${#state_candidates[@]} == 1 ))
state_file=${state_candidates[0]}
[[ "$state_file" =~ ^/home/pc/molin-email-unknown-([a-f0-9]{32})\.state$ ]]
operation_nonce=${BASH_REMATCH[1]}
[[ -f "$state_file" && ! -L "$state_file" ]]
[[ "$(/usr/bin/stat -c '%u:%a' -- "$state_file")" == "$(/usr/bin/id -u):600" ]]
state_identity=$(/usr/bin/stat -c '%u:%d:%i:%s' -- "$state_file")
state_sha=$(/usr/bin/sha256sum -- "$state_file" | /usr/bin/awk '{print $1}')
[[ "$state_sha" =~ ^[a-f0-9]{64}$ ]]

stage=target_gate
# 最终路径只能从唯一状态文件名推导；禁止枚举恢复文件后选择目标，也禁止覆盖任何历史恢复点。
rollback_dir=/home/pc/molin/rollback
recovery_file="${rollback_dir}/molin-email-unknown-${operation_nonce}.sql"
[[ "$recovery_file" =~ ^/home/pc/molin/rollback/molin-email-unknown-([a-f0-9]{32})\.sql$ ]]
[[ "${BASH_REMATCH[1]}" == "$operation_nonce" ]]
[[ -d /home/pc && ! -L /home/pc && -d /home/pc/molin && ! -L /home/pc/molin ]]
[[ -d "$rollback_dir" && ! -L "$rollback_dir" ]]
[[ "$(/usr/bin/stat -c '%u' -- "$rollback_dir")" == "$(/usr/bin/id -u)" ]]
rollback_mode=$(/usr/bin/stat -c '%a' -- "$rollback_dir")
[[ "$rollback_mode" =~ ^[0-7]{3,4}$ && $(( 8#$rollback_mode & 022 )) == 0 ]]
[[ ! -e "$recovery_file" && ! -L "$recovery_file" ]]

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
# 状态文件只在内存中解析；输出仅为强类型标识和固定长度摘要，完整 nonce 与业务原始值不会写到标准输出。
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
for numeric_value in "$operator_id" "$template_id" "$allowlist_id" "$primary_id" "$unexpected_id"; do [[ "$numeric_value" =~ ^[1-9][0-9]*$ ]]; done
for digest in "$recipient_hmac" "$fingerprint" "$old_hash" "$new_hash" "$template_text_hash"; do [[ "$digest" =~ ^[a-f0-9]{64}$ ]]; done
for hex_value in "$scope_hex" "$provider_template_hex" "$template_name_hex" "$template_subject_hex" "$recipient_masked_hex" "$template_text_hex"; do
  [[ "$hex_value" =~ ^[a-f0-9]+$ && $(( ${#hex_value} % 2 )) == 0 ]]
done

database_snapshot() {
  # 固定 SELECT 在一个语句快照内同时核验 schema 与四类目标夹具；root 密码只在容器内环境中读取。
  /usr/bin/docker exec -i "$mysql_id" /bin/bash -s -- \
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
sql="SELECT CONCAT(
  (SELECT version FROM schema_migrations LIMIT 1), CHAR(9),
  (SELECT IF(dirty,1,0) FROM schema_migrations LIMIT 1), CHAR(9),
  (SELECT COUNT(*) FROM schema_migrations), CHAR(9),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${primary_id} AND template_id=${template_id} AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4) AND provider='aliyun_directmail' AND verification_code_id IS NULL AND scene='register' AND purpose='test' AND recipient_hmac='${recipient_hmac}' AND idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4) AND idempotency_key_hash='${old_hash}' AND request_fingerprint='${fingerprint}' AND status='failed' AND failure_reason='provider_outcome_unknown'), CHAR(9),
  (SELECT COUNT(*) FROM email_send_logs WHERE id=${unexpected_id} AND template_id=${template_id} AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4) AND provider='aliyun_directmail' AND verification_code_id IS NULL AND scene='register' AND purpose='test' AND recipient_hmac='${recipient_hmac}' AND idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4) AND idempotency_key_hash='${new_hash}' AND request_fingerprint='${fingerprint}' AND status='failed' AND failure_reason='provider_outcome_unknown'), CHAR(9),
  (SELECT COUNT(*) FROM email_send_logs WHERE idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4)), CHAR(9),
  (SELECT COUNT(*) FROM email_test_recipient_allowlist WHERE id=${allowlist_id} AND email_hmac='${recipient_hmac}' AND email_masked=CONVERT(0x${recipient_masked_hex} USING utf8mb4) AND status='active' AND version=1 AND created_by=${operator_id} AND updated_by=${operator_id} AND revoked_at IS NULL), CHAR(9),
  (SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id} AND provider='aliyun_directmail' AND provider_template_id=CONVERT(0x${provider_template_hex} USING utf8mb4) AND name=CONVERT(0x${template_name_hex} USING utf8mb4) AND subject=CONVERT(0x${template_subject_hex} USING utf8mb4) AND sender_nickname IS NULL AND template_text=CONVERT(0x${template_text_hex} USING utf8mb4) AND JSON_LENGTH(variables_json)=2 AND JSON_CONTAINS(variables_json, JSON_QUOTE('Code')) AND JSON_CONTAINS(variables_json, JSON_QUOTE('ExpireMinutes')) AND content_sha256='${template_text_hash}' AND provider_status='approved' AND review_comment IS NULL AND variables_complete=1 AND local_enabled=1 AND missing=0 AND missing_since IS NULL AND provider_created_at IS NULL AND version=1), CHAR(9),
  LOWER(SHA2(CONCAT_WS(CHAR(31),
    (SELECT LOWER(SHA2(CONCAT_WS(CHAR(30),CAST(id AS CHAR),CAST(template_id AS CHAR),HEX(provider_template_id),provider,scene,purpose,recipient_hmac,HEX(idempotency_scope),idempotency_key_hash,request_fingerprint,status,failure_reason),256)) FROM email_send_logs WHERE id=${primary_id}),
    (SELECT LOWER(SHA2(CONCAT_WS(CHAR(30),CAST(id AS CHAR),CAST(template_id AS CHAR),HEX(provider_template_id),provider,scene,purpose,recipient_hmac,HEX(idempotency_scope),idempotency_key_hash,request_fingerprint,status,failure_reason),256)) FROM email_send_logs WHERE id=${unexpected_id}),
    (SELECT LOWER(SHA2(CONCAT_WS(CHAR(30),CAST(id AS CHAR),email_hmac,HEX(email_masked),status,CAST(version AS CHAR),CAST(created_by AS CHAR),CAST(updated_by AS CHAR),COALESCE(CAST(revoked_at AS CHAR),'NULL')),256)) FROM email_test_recipient_allowlist WHERE id=${allowlist_id}),
    (SELECT LOWER(SHA2(CONCAT_WS(CHAR(30),CAST(id AS CHAR),provider,HEX(provider_template_id),HEX(name),HEX(subject),COALESCE(HEX(sender_nickname),'NULL'),HEX(template_text),variables_json,content_sha256,provider_status,COALESCE(HEX(review_comment),'NULL'),CAST(variables_complete AS CHAR),CAST(local_enabled AS CHAR),CAST(missing AS CHAR),COALESCE(CAST(missing_since AS CHAR),'NULL'),COALESCE(CAST(provider_created_at AS CHAR),'NULL'),CAST(version AS CHAR)),256)) FROM email_provider_templates WHERE id=${template_id})
  ),256))
);"
result=$(MYSQL_PWD="$MYSQL_ROOT_PASSWORD" /usr/bin/mysql --no-defaults --host=127.0.0.1 --port=3306 --user=root --database=molin --batch --skip-column-names --raw --execute="$sql")
[[ "$result" != *$'\n'* ]]
printf '%s' "$result"
MYSQL_SNAPSHOT
}

assert_database_snapshot() {
  local snapshot=$1 version dirty migration_rows primary_count unexpected_count scope_count allowlist_count template_count digest
  IFS=$'\t' read -r version dirty migration_rows primary_count unexpected_count scope_count allowlist_count template_count digest <<< "$snapshot"
  [[ "$version" == 57 && "$dirty" == 0 && "$migration_rows" == 1 ]]
  [[ "$primary_count" == 1 && "$unexpected_count" == 1 && "$scope_count" == 2 ]]
  [[ "$allowlist_count" == 1 && "$template_count" == 1 && "$digest" =~ ^[a-f0-9]{64}$ ]]
}

cycle_snapshot() {
  # 两套 000057 隔离证据只读生成组合摘要；目录、完成标记、dump 与对应隔离 schema 均纳入核验。
  /usr/bin/docker exec -i "$mysql_id" /bin/bash -s <<'CYCLE_SNAPSHOT'
set -Eeuo pipefail
[[ -n "${MYSQL_ROOT_PASSWORD:-}" ]]
mapfile -t markers < <(/usr/bin/find /root -mindepth 3 -maxdepth 3 -type f -path '/root/molin-000057-schema57-cycle-run-*/evidence/cycle_completed' -print | /usr/bin/sort)
(( ${#markers[@]} == 2 ))
{
  for marker in "${markers[@]}"; do
    [[ "$marker" =~ ^(/root/molin-000057-schema57-cycle-run-([a-f0-9]{32}))/evidence/cycle_completed$ ]]
    cycle_dir=${BASH_REMATCH[1]}
    cycle_schema="molin_restore_57_reverify_${BASH_REMATCH[2]}"
    cycle_dump="${cycle_dir}/evidence/molin_source_schema57.sql"
    [[ -d "$cycle_dir" && ! -L "$cycle_dir" && -f "$marker" && ! -L "$marker" && -f "$cycle_dump" && ! -L "$cycle_dump" ]]
    [[ "$(/usr/bin/stat -c '%u:%a' -- "$cycle_dir")" == 0:700 ]]
    [[ "$(/usr/bin/stat -c '%u:%a' -- "$marker")" == 0:600 ]]
    [[ "$(/usr/bin/stat -c '%u:%a' -- "$cycle_dump")" == 0:600 ]]
    dump_sha=$(/usr/bin/sha256sum -- "$cycle_dump" | /usr/bin/awk '{print $1}')
    [[ "$dump_sha" =~ ^[a-f0-9]{64}$ ]]
    schema_count=$(MYSQL_PWD="$MYSQL_ROOT_PASSWORD" /usr/bin/mysql --no-defaults --host=127.0.0.1 --port=3306 --user=root --batch --skip-column-names --raw --execute="SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='${cycle_schema}';")
    [[ "$schema_count" == 1 ]]
    printf '%s\t%s\t%s\t%s\t%s\n' "$cycle_dir" "$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$marker")" "$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$cycle_dump")" "$dump_sha" "$schema_count"
  done
} | /usr/bin/sha256sum | /usr/bin/awk '{print $1}'
CYCLE_SNAPSHOT
}

stage=preflight_snapshot
database_before=$(database_snapshot)
assert_database_snapshot "$database_before"
cycle_before=$(cycle_snapshot)
[[ "$cycle_before" =~ ^[a-f0-9]{64}$ ]]

stage=temp_create
temp_file=$(/usr/bin/mktemp -- "$rollback_dir/.molin-email-recovery.XXXXXXXXXX")
[[ "$temp_file" =~ ^/home/pc/molin/rollback/\.molin-email-recovery\.[A-Za-z0-9]{10}$ ]]
[[ -f "$temp_file" && ! -L "$temp_file" ]]
[[ "$(/usr/bin/stat -c '%u:%a' -- "$temp_file")" == "$(/usr/bin/id -u):600" ]]
temp_identity=$(/usr/bin/stat -c '%u:%d:%i' -- "$temp_file")

stage=dump
# 逻辑备份在唯一 MySQL 容器内使用 root 环境完成；root 密码不会进入宿主机变量、命令行或输出。
/usr/bin/docker exec -i "$mysql_id" /bin/bash -s <<'MYSQL_DUMP' > "$temp_file"
set -Eeuo pipefail
[[ -n "${MYSQL_ROOT_PASSWORD:-}" ]]
export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"
/usr/bin/mysqldump --no-defaults --single-transaction --quick --skip-lock-tables --routines --triggers --events --hex-blob --set-gtid-purged=OFF --no-tablespaces molin
MYSQL_DUMP

stage=temp_verify
[[ -f "$temp_file" && ! -L "$temp_file" ]]
[[ "$(/usr/bin/stat -c '%u:%d:%i' -- "$temp_file")" == "$temp_identity" ]]
/usr/bin/chmod 600 -- "$temp_file"
temp_size=$(/usr/bin/stat -c '%s' -- "$temp_file")
[[ "$temp_size" =~ ^[1-9][0-9]*$ ]]
[[ "$(/usr/bin/stat -c '%u:%a' -- "$temp_file")" == "$(/usr/bin/id -u):600" ]]
/usr/bin/grep -aq '^-- Dump completed on ' "$temp_file"
for required_table in email_send_logs email_test_recipient_allowlist email_provider_templates schema_migrations; do
  /usr/bin/grep -Fq "Table structure for table \`${required_table}\`" "$temp_file"
done
temp_sha=$(/usr/bin/sha256sum -- "$temp_file" | /usr/bin/awk '{print $1}')
[[ "$temp_sha" =~ ^[a-f0-9]{64}$ ]]
/usr/bin/python3 - "$temp_file" <<'PY'
import os
import sys

fd = os.open(sys.argv[1], os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
try:
    os.fsync(fd)
finally:
    os.close(fd)
PY

stage=postdump_snapshot
database_after=$(database_snapshot)
assert_database_snapshot "$database_after"
[[ "$database_after" == "$database_before" ]]
cycle_after=$(cycle_snapshot)
[[ "$cycle_after" =~ ^[a-f0-9]{64}$ && "$cycle_after" == "$cycle_before" ]]
[[ "$(/usr/bin/stat -c '%u:%d:%i:%s' -- "$state_file")" == "$state_identity" ]]
[[ "$(/usr/bin/sha256sum -- "$state_file" | /usr/bin/awk '{print $1}')" == "$state_sha" ]]
[[ ! -e "$recovery_file" && ! -L "$recovery_file" ]]

stage=atomic_publish
# renameat2 的 NOREPLACE 标志同时满足原子命名和绝不覆盖；不支持该能力时直接失败并保留既有文件。
/usr/bin/python3 - "$temp_file" "$recovery_file" <<'PY'
import ctypes
import errno
import os
import sys

source, target = sys.argv[1:]
libc = ctypes.CDLL(None, use_errno=True)
renameat2 = getattr(libc, "renameat2", None)
if renameat2 is None:
    raise OSError(errno.ENOSYS, "renameat2 unavailable")
renameat2.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
renameat2.restype = ctypes.c_int
result = renameat2(-100, os.fsencode(source), -100, os.fsencode(target), 1)
if result != 0:
    error = ctypes.get_errno()
    raise OSError(error, os.strerror(error))
PY
published=true

stage=published_verify
[[ ! -e "$temp_file" && ! -L "$temp_file" ]]
[[ -f "$recovery_file" && ! -L "$recovery_file" ]]
recovery_metadata=$(/usr/bin/stat -c '%u:%a:%s' -- "$recovery_file")
[[ "$recovery_metadata" == "$(/usr/bin/id -u):600:${temp_size}" ]]
recovery_sha=$(/usr/bin/sha256sum -- "$recovery_file" | /usr/bin/awk '{print $1}')
[[ "$recovery_sha" =~ ^[a-f0-9]{64}$ && "$recovery_sha" == "$temp_sha" ]]
database_final=$(database_snapshot)
assert_database_snapshot "$database_final"
[[ "$database_final" == "$database_before" ]]
cycle_final=$(cycle_snapshot)
[[ "$cycle_final" =~ ^[a-f0-9]{64}$ && "$cycle_final" == "$cycle_before" ]]
[[ "$(/usr/bin/stat -c '%u:%d:%i:%s' -- "$state_file")" == "$state_identity" ]]
[[ "$(/usr/bin/sha256sum -- "$state_file" | /usr/bin/awk '{print $1}')" == "$state_sha" ]]

trap - ERR
printf 'status=pass schema=57 dirty=false fixture_logs=2 fixture_allowlist=1 fixture_template=1 snapshot_stable=true backup_published=true backup_mode=600 backup_sha256_valid=true cycle_evidence_retained=2 cleanup=false database_writes=false restarts=false retries=0\n'
