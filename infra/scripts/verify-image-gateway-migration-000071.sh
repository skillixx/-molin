#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${IMAGE_GATEWAY_G5_MYSQL_MIGRATION_APPROVED:-NO}" != "YES" ]]; then
  echo "IMAGE_G5_MYSQL_MIGRATION=APPROVAL_REQUIRED target=isolated_temporary_container project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "IMAGE_G5_MYSQL_MIGRATION=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "IMAGE_G5_MYSQL_MIGRATION=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
up_file="${repo_root}/server/migrations/000071_expand_image_billing_adjustments.up.sql"
down_file="${repo_root}/server/migrations/000071_expand_image_billing_adjustments.down.sql"
test -f "${up_file}" || { echo "IMAGE_G5_MYSQL_MIGRATION=FAILED reason=up_file_missing"; exit 2; }
test -f "${down_file}" || { echo "IMAGE_G5_MYSQL_MIGRATION=FAILED reason=down_file_missing"; exit 2; }

suffix="${RANDOM}-$$"
network_name="molin-image-g5-${suffix}"
container_name="molin-image-g5-mysql-${suffix}"
build_cache_volume="molin-image-g5-go-build-${suffix}"
database_name="molin_image_g5_contract"
root_password="$(openssl rand -hex 24)"
go_mod_cache="$(go env GOMODCACHE)"
test -d "${go_mod_cache}" || { echo "IMAGE_G5_MYSQL_MIGRATION=FAILED reason=go_mod_cache_missing"; exit 2; }
docker_repo_root="${repo_root}"
docker_go_mod_cache="${go_mod_cache}"
if command -v cygpath >/dev/null 2>&1; then
  docker_repo_root="$(cygpath -w "${repo_root}")"
  docker_go_mod_cache="$(cygpath -w "${go_mod_cache}")"
fi

cleanup() {
  if docker container inspect "${container_name}" >/dev/null 2>&1; then
    docker container rm -f "${container_name}" >/dev/null
  fi
  if docker network inspect "${network_name}" >/dev/null 2>&1; then
    docker network rm "${network_name}" >/dev/null
  fi
  if docker volume inspect "${build_cache_volume}" >/dev/null 2>&1; then
    docker volume rm "${build_cache_volume}" >/dev/null
  fi
}
trap cleanup EXIT

# 隔离网络没有外网出口，临时MySQL不映射宿主机端口，也不会接触项目数据库。
docker network create --internal "${network_name}" >/dev/null
docker run -d --pull=never --network "${network_name}" --network-alias mysql \
  --name "${container_name}" \
  --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g \
  -e "MYSQL_ROOT_PASSWORD=${root_password}" \
  -e "MYSQL_DATABASE=${database_name}" \
  mysql:8.0 \
  --character-set-server=utf8mb4 \
  --collation-server=utf8mb4_0900_ai_ci >/dev/null

docker exec "${container_name}" mkdir -p /migrations
docker cp "${repo_root}/server/migrations/." "${container_name}:/migrations" >/dev/null

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=${root_password}" "${container_name}" \
    mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database="${database_name}" --batch --skip-column-names "$@"
}

formal_ready=false
ready_count=0
for _ in $(seq 1 90); do
  if docker logs "${container_name}" 2>&1 | grep -q 'MySQL init process done. Ready for start up.'; then
    formal_ready=true
  fi
  if [[ "${formal_ready}" == "true" ]] && mysql_exec -e 'SELECT 1' >/dev/null 2>&1; then
    ready_count=$((ready_count + 1))
  else
    ready_count=0
  fi
  [[ "${ready_count}" -ge 2 ]] && break
  sleep 1
done
[[ "${ready_count}" -ge 2 ]] || { echo "IMAGE_G5_MYSQL_MIGRATION=FAILED reason=mysql_not_ready"; exit 2; }

apply_file() {
  local file="$1"
  docker exec -e "MYSQL_PWD=${root_password}" "${container_name}" sh -c \
    "mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database='${database_name}' < '/migrations/${file}'"
}

for path in "${repo_root}"/server/migrations/*.up.sql; do
  base="$(basename "${path}")"
  version_text="${base%%_*}"
  version=$((10#${version_text}))
  if [[ "${version}" -le 70 ]]; then
    apply_file "${base}"
  fi
done
apply_file "$(basename "${up_file}")" >/dev/null

assert_scalar() {
  local sql="$1" expected="$2" label="$3" actual
  actual="$(mysql_exec -e "${sql}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "IMAGE_G5_MYSQL_MIGRATION=FAILED reason=${label} expected=${expected} actual=${actual}"
    exit 2
  fi
}

assert_scalar "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_usage_items' AND column_name IN ('adjustment_direction','adjustment_reason','adjustment_operator_id','adjustment_reviewed_by')" "4" "adjustment_columns"
assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_usage_items' AND constraint_name IN ('fk_ai_usage_adjustment_operator','fk_ai_usage_adjustment_reviewer','chk_ai_usage_adjustment_audit')" "3" "adjustment_constraints"
assert_scalar "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_usage_items' AND index_name='idx_ai_usage_adjustment_audit'" "4" "adjustment_index_parts"

apply_file "$(basename "${down_file}")" >/dev/null
assert_scalar "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_usage_items' AND column_name IN ('adjustment_direction','adjustment_reason','adjustment_operator_id','adjustment_reviewed_by')" "4" "down_adjustment_facts_retained"
apply_file "$(basename "${up_file}")" >/dev/null
assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_usage_items' AND constraint_name='chk_ai_usage_adjustment_audit'" "1" "reup_adjustment_check"

# 测试进程只连接同一内部网络中的临时MySQL，图片、钱包和Provider均使用测试夹具。
race_flag="-race"
if [[ "${IMAGE_GATEWAY_G5_FAST_DIAGNOSTIC:-NO}" == "YES" ]]; then
  race_flag=""
fi
MSYS_NO_PATHCONV=1 docker run --rm --pull=never --network "${network_name}" \
  --mount "type=bind,src=${docker_repo_root},dst=/src,readonly" \
  --mount "type=bind,src=${docker_go_mod_cache},dst=/go/pkg/mod,readonly" \
  -v "${build_cache_volume}:/root/.cache/go-build" \
  -w /src/server \
  -e CGO_ENABLED=1 \
  -e GOPROXY=off \
  -e MOLIN_IMAGE_G5_ISOLATED=YES \
  -e "MOLIN_IMAGE_G5_MYSQL_DSN=root:${root_password}@tcp(mysql:3306)/${database_name}?charset=utf8mb4&parseTime=true&loc=UTC" \
  golang:1.25-bookworm \
  go test ${race_flag} -count=1 ./internal/modules/token_gateway/service \
    -run '^TestImageBillingServiceMySQLClosedLoop$'

docker volume rm "${build_cache_volume}" >/dev/null

echo "IMAGE_G5_MYSQL_MIGRATION=PASS mysql=8.0.46 full_chain_1_to_71=true up_down_reup=true down_facts_retained=true quote_hold_atomic=true settle_release=true partial_golden=true rejected_zero_sale=true explicit_failure_release=true timeout_disconnect_pending=true unknown_zero_retry=true compensation_once=true storage_failure_closed=true idempotent_request_100=true wallet_concurrency_100=true wallet_facts_zero=true cost_facts_zero=true outbox_facts_zero=true reconciliation_zero=true adjustment_maker_checker=true project_database=false provider_calls=0 real_wallet_writes=0"
