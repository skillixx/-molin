#!/usr/bin/env bash
# DirectMail Phase 4 全新 Redis unknown 周期；每个动作只输出固定脱敏摘要。
set -Eeuo pipefail
umask 077
exec 2>/dev/null

fail() {
  printf 'status=failed stage=%s retained=true retries=0\n' "${1:?stage_required}"
  exit 2
}

[[ $# -eq 4 ]] || fail argument_gate
readonly action=$1
readonly nonce=$2
readonly binary_sha256=$3
readonly operator_id=$4
[[ "$action" =~ ^(preflight|phase1|restart|phase2|cleanup_verified|finalize)$ ]] || fail argument_gate
[[ "$nonce" =~ ^[a-f0-9]{32}$ ]] || fail argument_gate
[[ "$binary_sha256" =~ ^[a-f0-9]{64}$ ]] || fail argument_gate
[[ "$operator_id" =~ ^[1-9][0-9]*$ ]] || fail argument_gate

readonly stage="/home/pc/molin-runtime/email-unknown-cycle-${nonce}"
readonly payload="${stage}/cycle.payload.sh"
readonly binary="${stage}/email-unknown-restart.test"
readonly state_file="${stage}/cycle.state"

assert_stage() {
  [[ -d "$stage" && ! -L "$stage" ]] || fail stage_identity
  [[ "$(stat -c '%U:%a' -- "$stage")" == 'pc:700' ]] || fail stage_identity
  [[ -f "$payload" && ! -L "$payload" && "$(stat -c '%U:%a' -- "$payload")" == 'pc:500' ]] || fail payload_identity
}

assert_binary() {
  [[ -f "$binary" && ! -L "$binary" && "$(stat -c '%U:%a' -- "$binary")" == 'pc:500' ]] || fail binary_identity
  [[ "$(sha256sum -- "$binary")" == "${binary_sha256}  ${binary}" ]] || fail binary_hash
}

load_runtime_environment() {
  mapfile -t api_pids < <(pgrep -x molin-api || true)
  [[ ${#api_pids[@]} -eq 1 ]] || fail api_identity
  readonly api_pid=${api_pids[0]}
  [[ -r "/proc/${api_pid}/environ" ]] || fail api_environment
  while IFS= read -r -d '' entry; do
    export "$entry"
  done < "/proc/${api_pid}/environ"
  for key in MYSQL_HOST MYSQL_PORT MYSQL_USER MYSQL_PASSWORD MYSQL_DATABASE REDIS_ADDR REDIS_DB; do
    [[ -n "${!key:-}" ]] || fail required_environment
  done
  [[ "${APP_ENV:-}" == test && "$MYSQL_DATABASE" == molin ]] || fail test_environment
}

resolve_containers() {
  mapfile -t mysql_ids < <(timeout 3 docker ps --filter 'name=^/molin-mysql$' --format '{{.ID}}' || true)
  mapfile -t redis_ids < <(timeout 3 docker ps --filter 'name=^/molin-redis$' --format '{{.ID}}' || true)
  [[ ${#mysql_ids[@]} -eq 1 && ${#redis_ids[@]} -eq 1 ]] || fail container_identity
  readonly mysql_id=${mysql_ids[0]}
  readonly redis_id=${redis_ids[0]}
}

mysql_exec() {
  local query=${1:?query_required}
  docker exec -e MYSQL_PWD="$MYSQL_PASSWORD" "$mysql_id" mysql \
    --no-defaults --batch --skip-column-names --raw --host=127.0.0.1 --port=3306 --user="$MYSQL_USER" "$MYSQL_DATABASE" \
    --execute "$query" 2>/dev/null
}

redis_run_id_via_api_target() {
  local redis_host=${REDIS_ADDR%:*}
  local redis_port=${REDIS_ADDR##*:}
  [[ "$redis_host" =~ ^[A-Za-z0-9.-]+$ && "$redis_port" =~ ^[1-9][0-9]{0,4}$ && "$redis_port" -le 65535 ]] || return 1
  docker exec -e REDISCLI_AUTH="${REDIS_PASSWORD:-}" "$redis_id" redis-cli \
    -h "$redis_host" -p "$redis_port" INFO server 2>/dev/null |
    sed -n 's/^run_id:\([a-f0-9]\{40\}\)\r\{0,1\}$/\1/p'
}

redis_run_id_in_container() {
  docker exec -e REDISCLI_AUTH="${REDIS_PASSWORD:-}" "$redis_id" redis-cli INFO server 2>/dev/null |
    sed -n 's/^run_id:\([a-f0-9]\{40\}\)\r\{0,1\}$/\1/p'
}

read_state_fields() {
  local path=${1:?path_required}
  python3 -B - "$path" <<'PY'
import json
import os
import re
import stat
import sys

path = sys.argv[1]
flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
fd = os.open(path, flags)
info = os.fstat(fd)
if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600 or info.st_uid != os.getuid() or info.st_nlink != 1:
    os.close(fd)
    raise SystemExit(2)

def strict_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate")
        result[key] = value
    return result

with os.fdopen(fd, "r", encoding="utf-8") as handle:
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
if not isinstance(values[0], str) or values[0] not in {"phase1_created", "phase2_verified"}:
    raise SystemExit(2)
if not isinstance(values[1], str) or re.fullmatch(r"[a-f0-9]{32}", values[1]) is None:
    raise SystemExit(2)
if not isinstance(values[2], str) or re.fullmatch(r"[a-f0-9]{40}", values[2]) is None:
    raise SystemExit(2)
if any(not isinstance(value, int) or value <= 0 for value in values[3:7]):
    raise SystemExit(2)
if not isinstance(values[7], int) or values[7] < 0:
    raise SystemExit(2)
print("\t".join(str(value) for value in values))
PY
}

assert_runtime_identity() {
  local schema
  local operator_count
  local configured_run_id
  local container_run_id
  schema=$(mysql_exec 'SELECT CONCAT(version,":",dirty) FROM schema_migrations LIMIT 1;')
  [[ "$schema" == '57:0' ]] || fail schema_gate
  operator_count=$(mysql_exec "SELECT COUNT(*) FROM users WHERE id=${operator_id};")
  [[ "$operator_count" == 1 ]] || fail operator_gate
  configured_run_id=$(redis_run_id_via_api_target) || fail redis_target
  container_run_id=$(redis_run_id_in_container) || fail redis_target
  [[ -n "$configured_run_id" && "$configured_run_id" == "$container_run_id" ]] || fail redis_target
  [[ "$(timeout 3 docker exec -e REDISCLI_AUTH="${REDIS_PASSWORD:-}" "$redis_id" redis-cli ping 2>/dev/null || true)" == PONG ]] || fail redis_ping
  [[ "$(curl --connect-timeout 2 --max-time 3 -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health 2>/dev/null || true)" == 200 ]] || fail api_health
  [[ "$(curl --connect-timeout 2 --max-time 3 -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)" == 200 ]] || fail api_ready
}

run_binary_phase() {
  local phase=${1:?phase_required}
  local expected=${2:?expected_required}
  local output
  export APP_ENV=test
  export EMAIL_ADAPTER=mock
  export RUN_EMAIL_UNKNOWN_RESTART_INTEGRATION=1
  export EMAIL_UNKNOWN_RESTART_ACK=I_UNDERSTAND_ISOLATED_EMAIL_UNKNOWN_RESTART_TEST
  export EMAIL_UNKNOWN_RESTART_STATE_FILE="$state_file"
  export EMAIL_UNKNOWN_RESTART_OPERATOR_ID="$operator_id"
  export EMAIL_UNKNOWN_RESTART_NONCE="$nonce"
  export EMAIL_UNKNOWN_RESTART_PHASE="$phase"
  if [[ "$phase" == cleanup_verified ]]; then
    export RUN_EMAIL_UNKNOWN_RESTART_CLEANUP=1
    export EMAIL_UNKNOWN_RESTART_CLEANUP_ACK=I_UNDERSTAND_EXACT_EMAIL_UNKNOWN_RESTART_CLEANUP
  else
    unset RUN_EMAIL_UNKNOWN_RESTART_CLEANUP EMAIL_UNKNOWN_RESTART_CLEANUP_ACK
  fi
  if ! output=$("$binary" -test.run '^TestEmailUnknownTombstoneSurvivesRedisRestart$' -test.count=1 -test.v 2>&1); then
    fail "${phase}_binary"
  fi
  grep -Fq -- "$expected" <<<"$output" || fail "${phase}_summary"
}

assert_stage

case "$action" in
  preflight)
    assert_binary
    [[ ! -e "$state_file" && ! -L "$state_file" ]] || fail target_collision
    load_runtime_environment
    resolve_containers
    assert_runtime_identity
    printf 'status=pass stage=preflight api=ready schema=57 redis_identity=unique writes=false\n'
    ;;
  phase1)
    assert_binary
    [[ ! -e "$state_file" && ! -L "$state_file" ]] || fail target_collision
    load_runtime_environment
    resolve_containers
    assert_runtime_identity
    run_binary_phase phase1 '[PASS] mode=email_unknown_restart phase=phase1 classification=tombstone_created schema=57 dirty=false adapter_calls=1 recovery_state=retained redis_restart_required=true'
    mapfile -t state < <(read_state_fields "$state_file")
    [[ ${#state[@]} -eq 1 && "${state[0]}" == phase1_created$'\t'"$nonce"$'\t'*$'\t'"$operator_id"$'\t'*$'\t0' ]] || fail phase1_state
    printf 'status=pass stage=phase1 tombstone=created adapter_calls=1 state=retained restart_required=true\n'
    ;;
  restart)
    assert_binary
    mapfile -t state < <(read_state_fields "$state_file")
    [[ ${#state[@]} -eq 1 && "${state[0]}" == phase1_created$'\t'"$nonce"$'\t'*$'\t'"$operator_id"$'\t'*$'\t0' ]] || fail restart_state
    load_runtime_environment
    resolve_containers
    before=$(redis_run_id_in_container) || fail redis_before
    state_run_id=$(cut -f3 <<<"${state[0]}")
    [[ -n "$before" && "$before" == "$state_run_id" ]] || fail redis_before
    docker restart "$redis_id" >/dev/null || fail redis_restart
    ready=false
    for _ in $(seq 1 30); do
      if [[ "$(timeout 3 docker exec -e REDISCLI_AUTH="${REDIS_PASSWORD:-}" "$redis_id" redis-cli ping 2>/dev/null || true)" == PONG ]]; then
        ready=true
        break
      fi
      sleep 1
    done
    [[ "$ready" == true ]] || fail redis_ready
    after=$(redis_run_id_in_container) || fail redis_after
    [[ -n "$after" && "$after" != "$before" ]] || fail redis_after
    for _ in $(seq 1 30); do
      if [[ "$(curl --connect-timeout 2 --max-time 3 -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)" == 200 ]]; then
        break
      fi
      sleep 1
    done
    [[ "$(curl --connect-timeout 2 --max-time 3 -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)" == 200 ]] || fail api_ready_after_restart
    printf 'status=pass stage=restart redis_unique=true run_id_changed=true api_ready=true\n'
    ;;
  phase2)
    assert_binary
    mapfile -t state < <(read_state_fields "$state_file")
    [[ ${#state[@]} -eq 1 && "${state[0]}" == phase1_created$'\t'"$nonce"$'\t'*$'\t'"$operator_id"$'\t'*$'\t0' ]] || fail phase2_state
    load_runtime_environment
    resolve_containers
    run_binary_phase phase2 '[PASS] mode=email_unknown_restart phase=phase2 classification=db_tombstone_blocked schema=57 dirty=false old_key_blocked=true new_key_blocked=true adapter_calls=0 cleanup_performed=false test_data=retained recovery_state=retained cleanup_authorization_required=true'
    mapfile -t verified < <(read_state_fields "$state_file")
    [[ ${#verified[@]} -eq 1 && "${verified[0]}" == phase2_verified$'\t'"$nonce"$'\t'*$'\t'"$operator_id"$'\t'*$'\t0' ]] || fail phase2_verified_state
    printf 'status=pass stage=phase2 old_key_blocked=true new_key_blocked=true adapter_calls=0 cleanup_required=true\n'
    ;;
  cleanup_verified)
    assert_binary
    mapfile -t state < <(read_state_fields "$state_file")
    [[ ${#state[@]} -eq 1 && "${state[0]}" == phase2_verified$'\t'"$nonce"$'\t'*$'\t'"$operator_id"$'\t'*$'\t0' ]] || fail cleanup_state
    template_id=$(cut -f5 <<<"${state[0]}")
    allowlist_id=$(cut -f6 <<<"${state[0]}")
    send_log_id=$(cut -f7 <<<"${state[0]}")
    [[ "$template_id" =~ ^[1-9][0-9]*$ && "$allowlist_id" =~ ^[1-9][0-9]*$ && "$send_log_id" =~ ^[1-9][0-9]*$ ]] || fail cleanup_ids
    load_runtime_environment
    resolve_containers
    run_binary_phase cleanup_verified '[PASS] mode=email_unknown_restart phase=cleanup_verified classification=exact_cleanup_complete cleanup_db=true redis_key_absent=true state_removed=true'
    [[ ! -e "$state_file" && ! -L "$state_file" ]] || fail state_retained
    counts=$(mysql_exec "SELECT (SELECT COUNT(*) FROM email_send_logs WHERE id=${send_log_id}),(SELECT COUNT(*) FROM email_test_recipient_allowlist WHERE id=${allowlist_id}),(SELECT COUNT(*) FROM email_provider_templates WHERE id=${template_id});")
    [[ "$counts" == $'0\t0\t0' ]] || fail cleanup_postcheck
    rm -f -- "$binary"
    [[ ! -e "$binary" && ! -L "$binary" ]] || fail artifact_cleanup
    printf 'status=pass stage=cleanup_verified db_rows=0 state_absent=true binary_absent=true\n'
    ;;
  finalize)
    [[ ! -e "$state_file" && ! -L "$state_file" && ! -e "$binary" && ! -L "$binary" ]] || fail finalize_artifacts
    load_runtime_environment
    resolve_containers
    [[ "$(curl --connect-timeout 2 --max-time 3 -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/health 2>/dev/null || true)" == 200 ]] || fail finalize_health
    [[ "$(curl --connect-timeout 2 --max-time 3 -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)" == 200 ]] || fail finalize_ready
    printf 'status=pass stage=finalize api_health=true api_ready=true retained_payload=true\n'
    ;;
esac
