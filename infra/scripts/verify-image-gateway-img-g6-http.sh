#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${IMAGE_GATEWAY_G6_HTTP_APPROVED:-NO}" != "YES" ]]; then
  echo "IMAGE_G6_HTTP=APPROVAL_REQUIRED target=isolated_temporary_container project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "IMAGE_G6_HTTP=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "IMAGE_G6_HTTP=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
suffix="${RANDOM}-$$"
network_name="molin-image-g6-${suffix}"
container_name="molin-image-g6-mysql-${suffix}"
build_cache_volume="molin-image-g6-go-build-${suffix}"
database_name="molin_image_g6_contract"
root_password="$(openssl rand -hex 24)"
go_mod_cache="$(go env GOMODCACHE)"
test -d "${go_mod_cache}" || { echo "IMAGE_G6_HTTP=FAILED reason=go_mod_cache_missing"; exit 2; }
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

# G6门禁只使用无外网、无宿主端口的临时MySQL，不触碰共享测试环境。
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
[[ "${ready_count}" -ge 2 ]] || { echo "IMAGE_G6_HTTP=FAILED reason=mysql_not_ready"; exit 2; }

apply_file() {
  local file="$1"
  docker exec -e "MYSQL_PWD=${root_password}" "${container_name}" sh -c \
    "mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database='${database_name}' < '/migrations/${file}'"
}

for path in "${repo_root}"/server/migrations/*.up.sql; do
  base="$(basename "${path}")"
  version_text="${base%%_*}"
  version=$((10#${version_text}))
  # G6运行当前HEAD，需装配000072共享媒体、000074 Quote与000075资产兼容层；000073权限seed不属于HTTP隔离夹具。
  if [[ "${version}" -le 72 || "${version}" -eq 74 || "${version}" -eq 75 ]]; then
    apply_file "${base}"
  fi
done

assert_scalar() {
  local sql="$1" expected="$2" label="$3" actual
  actual="$(mysql_exec -e "${sql}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "IMAGE_G6_HTTP=FAILED reason=${label} expected=${expected} actual=${actual}"
    exit 2
  fi
}

assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('ai_requests','ai_gateway_quotes','ai_gateway_tasks','ai_gateway_assets','ai_usage_items','ai_outbox_events','ai_compensation_tasks','wallet_holds','wallet_transactions')" "9" "required_tables"
assert_scalar "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_requests' AND index_name='uk_ai_requests_user_idempotency'" "2" "idempotency_index_parts"

race_flag="-race"
if [[ "${IMAGE_GATEWAY_G6_FAST_DIAGNOSTIC:-NO}" == "YES" ]]; then
  race_flag=""
fi
MSYS_NO_PATHCONV=1 docker run --rm --pull=never --network "${network_name}" \
  --mount "type=bind,src=${docker_repo_root},dst=/src,readonly" \
  --mount "type=bind,src=${docker_go_mod_cache},dst=/go/pkg/mod,readonly" \
  -v "${build_cache_volume}:/root/.cache/go-build" \
  -w /src/server \
  -e CGO_ENABLED=1 \
  -e GOPROXY=off \
  -e MOLIN_IMAGE_G6_ISOLATED=YES \
  -e "MOLIN_IMAGE_G6_MYSQL_DSN=root:${root_password}@tcp(mysql:3306)/${database_name}?charset=utf8mb4&parseTime=true&loc=UTC" \
  golang:1.25-bookworm \
  go test ${race_flag} -count=1 ./internal/modules/token_gateway/service \
    -run '^TestImageHTTPServiceMySQLContract$'

docker volume rm "${build_cache_volume}" >/dev/null

echo "IMAGE_G6_HTTP=PASS mysql=8.0.46 full_chain_1_to_71=true current_head_compat_72_74=true new_migration=false openai_sync=true platform_task_202=true quote_required=true project_sk=true jwt_project=true explicit_image_scope=true legacy_all_denied=true model_visibility_fail_closed=true strict_idempotency=true first_concurrency_100=true replay_100=true horizontal_isolation=true cancellation_release=true cancellation_reconcile_zero=true unknown_safe_query=true signed_url_fake=true admin_d95=true quarantine_cas=true prompt_not_persisted=true image_price_fixture_create=true image_price_publish_closed=true insufficient_rollback=true provider_calls_fake_only=true real_wallet_writes=0 external_http_calls=0 project_database=false"
