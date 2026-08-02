#!/usr/bin/env bash
# DirectMail Phase 4 保留 unknown stage 的 nonce 不一致精确恢复脚本。
set -Eeuo pipefail
umask 077

# 远端命令的标准错误不进入传输层；所有允许失败都必须折叠为固定分类。
exec 2>/dev/null

fail() {
  printf 'status=failed stage=%s retained=true writes=unknown retries=0\n' "${1:?stage_required}"
  exit 2
}

[[ $# -eq 5 ]] || fail argument_gate
readonly action=$1
readonly old_binary_sha=$2
readonly old_payload_sha=$3
readonly recovery_binary_sha=$4
readonly expected_operator_id=$5
[[ "$action" =~ ^(preflight|uploaded_preflight|cleanup)$ ]] || fail argument_gate
[[ "$old_binary_sha" =~ ^[a-f0-9]{64}$ && "$old_payload_sha" =~ ^[a-f0-9]{64}$ ]] || fail argument_gate
[[ "$recovery_binary_sha" =~ ^[a-f0-9]{64}$ && "$expected_operator_id" =~ ^[1-9][0-9]*$ ]] || fail argument_gate

mapfile -t stage_candidates < <(find /home/pc/molin-runtime -mindepth 1 -maxdepth 1 -type d -name 'email-unknown-cycle-*' -print)
mapfile -t stage_links < <(find /home/pc/molin-runtime -mindepth 1 -maxdepth 1 -type l -name 'email-unknown-cycle-*' -print)
[[ ${#stage_candidates[@]} -eq 1 && ${#stage_links[@]} -eq 0 ]] || fail stage_count
readonly stage=${stage_candidates[0]}
[[ "$stage" =~ ^/home/pc/molin-runtime/email-unknown-cycle-([a-f0-9]{32})$ ]] || fail stage_path
readonly operation_id=${BASH_REMATCH[1]}
[[ ! -L "$stage" && "$(stat -c '%U:%a' -- "$stage")" == 'pc:700' ]] || fail stage_identity

readonly old_binary="${stage}/email-unknown-restart.test"
readonly old_payload="${stage}/cycle.payload.sh"
readonly state_file="${stage}/cycle.state"
readonly recovery_binary="${stage}/email-unknown-phase1-recovery.test"

assert_file() {
  local path=$1
  local mode=$2
  [[ -f "$path" && ! -L "$path" && "$(stat -c '%U:%a' -- "$path")" == "pc:${mode}" ]] || fail file_identity
}

assert_old_assets() {
  assert_file "$old_binary" 500
  assert_file "$old_payload" 500
  [[ "$(sha256sum -- "$old_binary")" == "${old_binary_sha}  ${old_binary}" ]] || fail old_binary_hash
  [[ "$(sha256sum -- "$old_payload")" == "${old_payload_sha}  ${old_payload}" ]] || fail old_payload_hash
}

read_state_fields() {
  python3 -B - "$state_file" "$expected_operator_id" "$operation_id" <<'PY'
import json
import hashlib
import hmac
import os
import re
import stat
import sys

path = sys.argv[1]
expected_operator = int(sys.argv[2])
expected_operation = sys.argv[3]
descriptor = os.open(path, os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW)
info = os.fstat(descriptor)
if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600 or info.st_uid != os.getuid() or info.st_nlink != 1:
    os.close(descriptor)
    raise SystemExit(2)

def strict_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate")
        result[key] = value
    return result

with os.fdopen(descriptor, "r", encoding="utf-8") as handle:
    state = json.load(handle, object_pairs_hook=strict_object)
expected = {
    "version", "phase", "nonce", "redis_run_id", "operator_id",
    "template_id", "allowlist_id", "send_log_id",
}
if set(state) not in (expected, expected | {"unexpected_send_log_id"}):
    raise SystemExit(2)
values = [
    state.get("phase"), state.get("nonce"), state.get("redis_run_id"),
    state.get("operator_id"), state.get("template_id"), state.get("allowlist_id"),
    state.get("send_log_id"), state.get("unexpected_send_log_id", 0),
]
if state.get("version") != 1 or values[0] not in {"initializing", "phase1_created"}:
    raise SystemExit(2)
if not isinstance(values[1], str) or re.fullmatch(r"[a-f0-9]{32}", values[1]) is None:
    raise SystemExit(2)
if values[1] == expected_operation:
    raise SystemExit(2)
if not isinstance(values[2], str) or re.fullmatch(r"[a-f0-9]{40}", values[2]) is None:
    raise SystemExit(2)
if values[3] != expected_operator or any(not isinstance(value, int) or value < 0 for value in values[4:]):
    raise SystemExit(2)
if values[7] != 0:
    raise SystemExit(2)
if values[4] == 0 and (values[5] != 0 or values[6] != 0):
    raise SystemExit(2)
if values[5] == 0 and values[6] != 0:
    raise SystemExit(2)
if values[0] == "phase1_created" and any(value <= 0 for value in values[4:7]):
    raise SystemExit(2)
if values[0] == "initializing" and values[6] != 0:
    raise SystemExit(2)
email = f"phase4-{values[1]}@example.invalid"
old_key = f"phase4-old-{values[1]}"
provider_template = f"qa-phase4-{values[1]}"
recipient_hmac = hmac.new(b"qa-phase4-address-secret-32-bytes-only", email.encode(), hashlib.sha256).hexdigest()
scope = f"admin-email-template-test:admin:{expected_operator}:template:{values[4]}:scene:register:recipient:{recipient_hmac}"
fingerprint = hashlib.sha256(f"POST\n/api/admin/email/templates/{values[4]}/test-send\nregister\n{recipient_hmac}".encode()).hexdigest()
old_hash = hashlib.sha256(old_key.encode()).hexdigest()
lock_digest = hmac.new(b"qa-phase4-idempotency-secret-32-bytes", scope.encode(), hashlib.sha256).hexdigest()
masked = email[:2] + "***" + email[email.rfind("@"):]
template_text = "<p>验证码：${Code}，有效期 ${ExpireMinutes} 分钟。</p>"
as_hex = lambda value: value.encode().hex()
derived = [recipient_hmac, as_hex(scope), fingerprint, old_hash, as_hex(provider_template), as_hex(masked), as_hex(template_text), hashlib.sha256(template_text.encode()).hexdigest(), lock_digest]
print("\t".join(str(value) for value in values + derived))
PY
}

load_runtime_environment() {
  mapfile -t api_pids < <(pgrep -x molin-api || true)
  [[ ${#api_pids[@]} -eq 1 && -r "/proc/${api_pids[0]}/environ" ]] || fail api_identity
  while IFS= read -r -d '' entry; do export "$entry"; done < "/proc/${api_pids[0]}/environ"
  for key in MYSQL_USER MYSQL_PASSWORD MYSQL_DATABASE REDIS_ADDR REDIS_DB; do
    [[ -n "${!key:-}" ]] || fail required_environment
  done
  [[ "${APP_ENV:-}" == test && "$MYSQL_DATABASE" == molin ]] || fail test_environment
}

resolve_containers() {
  mapfile -t mysql_ids < <(timeout 3 docker ps --filter 'name=^/molin-mysql$' --format '{{.ID}}' || true)
  mapfile -t redis_ids < <(timeout 3 docker ps --filter 'name=^/molin-redis$' --format '{{.ID}}' || true)
  [[ ${#mysql_ids[@]} -eq 1 && ${#redis_ids[@]} -eq 1 ]] || fail container_identity
  mysql_id=${mysql_ids[0]}
  redis_id=${redis_ids[0]}
}

mysql_exec() {
  docker exec -e MYSQL_PWD="$MYSQL_PASSWORD" "$mysql_id" mysql --no-defaults \
    --batch --skip-column-names --raw --host=127.0.0.1 --port=3306 --user="$MYSQL_USER" "$MYSQL_DATABASE" \
    --execute "$1" 2>/dev/null
}

redis_run_id() {
  docker exec -e REDISCLI_AUTH="${REDIS_PASSWORD:-}" "$redis_id" redis-cli INFO server 2>/dev/null |
    sed -n 's/^run_id:\([a-f0-9]\{40\}\)\r\{0,1\}$/\1/p'
}

# 读取完整 state 并冻结 mismatch 现场的所有派生身份，不输出任何原始值。
load_complete_mismatch_state() {
  mapfile -t state < <(read_state_fields)
  [[ ${#state[@]} -eq 1 ]] || fail state_parse
  IFS=$'\t' read -r phase state_nonce recorded_run_id recorded_operator template_id allowlist_id send_log_id unexpected_send_log_id \
    recipient_hmac scope_hex fingerprint old_hash provider_template_hex recipient_masked_hex template_text_hex template_text_hash lock_digest <<< "${state[0]}"
  [[ "$phase" == phase1_created && "$state_nonce" != "$operation_id" ]] || fail state_nonce_relation
  [[ "$recorded_operator" == "$expected_operator_id" && "$template_id" =~ ^[1-9][0-9]*$ && "$allowlist_id" =~ ^[1-9][0-9]*$ && "$send_log_id" =~ ^[1-9][0-9]*$ && "$unexpected_send_log_id" == 0 ]] || fail state_parse
  for value in "$recorded_run_id" "$recipient_hmac" "$fingerprint" "$old_hash" "$template_text_hash" "$lock_digest"; do [[ "$value" =~ ^[a-f0-9]{40}$|^[a-f0-9]{64}$ ]] || fail state_parse; done
  for value in "$scope_hex" "$provider_template_hex" "$recipient_masked_hex" "$template_text_hex"; do [[ "$value" =~ ^[a-f0-9]+$ && $(( ${#value} % 2 )) -eq 0 ]] || fail state_parse; done
}

# 单条 SELECT 严格证明 state 派生身份与三条夹具及唯一 scope 完整一致。
assert_fixture_ownership() {
  local snapshot
  if ! snapshot=$(mysql_exec "SELECT CONCAT_WS(CHAR(9),
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
  );"); then fail fixture_query; fi
  [[ "$snapshot" == $'57\t0\t1\t1\t1\t0\t1\t1\t1\t1' ]] || fail fixture_ownership
}

# mismatch 恢复只接受 Redis 未重启且派生精确锁 key 已不存在的现场，不执行任何 Redis 删除。
assert_redis_ownership() {
  local current_run_id exact_key_exists
  if ! current_run_id=$(redis_run_id); then fail redis_identity; fi
  [[ "$current_run_id" == "$recorded_run_id" ]] || fail redis_identity
  if ! exact_key_exists=$(docker exec -e REDISCLI_AUTH="${REDIS_PASSWORD:-}" "$redis_id" redis-cli -n "$REDIS_DB" --raw EXISTS "lock:email:dispatch:${lock_digest}" 2>/dev/null); then fail redis_exact_exists; fi
  [[ "$exact_key_exists" == 0 ]] || fail redis_exact_exists
}

assert_old_assets

case "$action" in
  preflight)
    mapfile -t names < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n' | sort)
    [[ ${#names[@]} -eq 3 && "${names[0]}" == cycle.payload.sh && "${names[1]}" == cycle.state && "${names[2]}" == email-unknown-restart.test ]] || fail stage_shape
    assert_file "$state_file" 600
    load_complete_mismatch_state
    load_runtime_environment
    resolve_containers
    assert_fixture_ownership
    assert_redis_ownership
    printf 'status=pass stage=mismatch_recovery_preflight operation_id=%s state_class=complete state_phase=phase1_created stage_nonce_match=false fixture_ownership=true redis_identity=true redis_key_exists=0 schema=57 writes=false retries=0\n' "$operation_id"
    ;;
  uploaded_preflight)
    mapfile -t names < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n' | sort)
    [[ ${#names[@]} -eq 4 && "${names[0]}" == cycle.payload.sh && "${names[1]}" == cycle.state && "${names[2]}" == email-unknown-phase1-recovery.test && "${names[3]}" == email-unknown-restart.test ]] || fail uploaded_stage_shape
    assert_file "$state_file" 600
    load_complete_mismatch_state
    [[ -f "$recovery_binary" && ! -L "$recovery_binary" ]] || fail uploaded_binary_identity
    [[ "$(stat -c '%U' -- "$recovery_binary")" == pc ]] || fail uploaded_binary_identity
    binary_mode=$(stat -c '%a' -- "$recovery_binary") || fail uploaded_binary_identity
    case "$binary_mode" in 500|600|644|700|755) ;; *) binary_mode=other ;; esac
    if [[ "$(sha256sum -- "$recovery_binary")" == "${recovery_binary_sha}  ${recovery_binary}" ]]; then binary_hash_match=true; else binary_hash_match=false; fi
    load_runtime_environment
    resolve_containers
    assert_fixture_ownership
    assert_redis_ownership
    printf 'status=pass stage=mismatch_recovery_uploaded_binary_preflight operation_id=%s state_class=complete state_phase=phase1_created stage_nonce_match=false fixture_ownership=true redis_identity=true redis_key_exists=0 binary_regular=true binary_symlink=false binary_owner=true binary_mode=%s binary_hash_match=%s retained=true writes=false retries=0\n' "$operation_id" "$binary_mode" "$binary_hash_match"
    ;;
  cleanup)
    mapfile -t names < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n' | sort)
    [[ ${#names[@]} -eq 4 && "${names[0]}" == cycle.payload.sh && "${names[1]}" == cycle.state && "${names[2]}" == email-unknown-phase1-recovery.test && "${names[3]}" == email-unknown-restart.test ]] || fail cleanup_stage_shape
    assert_file "$state_file" 600
    load_complete_mismatch_state
    [[ -f "$recovery_binary" && ! -L "$recovery_binary" ]] || fail recovery_binary_identity
    [[ "$(stat -c '%U' -- "$recovery_binary")" == pc ]] || fail recovery_binary_identity
    binary_mode=$(stat -c '%a' -- "$recovery_binary") || fail recovery_binary_identity
    [[ "$binary_mode" =~ ^[0-7]{3}$ ]] || fail recovery_binary_identity
    [[ "$(sha256sum -- "$recovery_binary")" == "${recovery_binary_sha}  ${recovery_binary}" ]] || fail recovery_binary_hash
    load_runtime_environment
    resolve_containers
    assert_fixture_ownership
    assert_redis_ownership
    chmod 500 -- "$recovery_binary"
    assert_file "$recovery_binary" 500
    export APP_ENV=test EMAIL_ADAPTER=mock
    export RUN_EMAIL_UNKNOWN_RESTART_INTEGRATION=1
    export EMAIL_UNKNOWN_RESTART_ACK=I_UNDERSTAND_ISOLATED_EMAIL_UNKNOWN_RESTART_TEST
    export RUN_EMAIL_UNKNOWN_RESTART_CLEANUP=1
    export EMAIL_UNKNOWN_RESTART_CLEANUP_ACK=I_UNDERSTAND_EXACT_EMAIL_UNKNOWN_RESTART_CLEANUP
    export EMAIL_UNKNOWN_RESTART_STATE_FILE="$state_file"
    export EMAIL_UNKNOWN_RESTART_OPERATOR_ID="$expected_operator_id"
    export EMAIL_UNKNOWN_RESTART_PHASE=cleanup_phase1
    output=$($recovery_binary -test.run '^TestEmailUnknownTombstoneSurvivesRedisRestart$' -test.count=1 -test.v 2>&1) || fail cleanup_binary
    grep -Fq '[PASS] mode=email_unknown_restart phase=cleanup_phase1 classification=exact_cleanup_complete cleanup_db=true redis_key_absent=true state_removed=true' <<<"$output" || fail cleanup_summary
    [[ ! -e "$state_file" && ! -L "$state_file" ]] || fail state_remove
    if ! counts=$(mysql_exec "SELECT CONCAT_WS(CHAR(9),(SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id}),(SELECT COUNT(*) FROM email_test_recipient_allowlist WHERE id=${allowlist_id}),(SELECT COUNT(*) FROM email_send_logs WHERE id=${send_log_id}),(SELECT COUNT(*) FROM email_send_logs WHERE idempotency_scope=CONVERT(0x${scope_hex} USING utf8mb4)));"); then fail cleanup_postcheck_query; fi
    [[ "$counts" == $'0\t0\t0\t0' ]] || fail cleanup_postcheck
    rm -f -- "$old_binary" "$old_payload" "$recovery_binary"
    [[ ! -e "$old_binary" && ! -e "$old_payload" && ! -e "$recovery_binary" ]] || fail artifact_remove
    rmdir -- "$stage"
    [[ ! -e "$stage" && ! -L "$stage" ]] || fail stage_remove
    [[ "$(curl --connect-timeout 2 --max-time 3 -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health 2>/dev/null || true)" == 200 ]] || fail api_health
    [[ "$(curl --connect-timeout 2 --max-time 3 -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)" == 200 ]] || fail api_ready
    printf 'status=pass stage=mismatch_recovery_cleanup operation_id=%s db_rows=0 scope_rows=0 state_absent=true artifacts_absent=true api_ready=true stage_nonce_match=false redis_delete=false retries=0\n' "$operation_id"
    ;;
esac
