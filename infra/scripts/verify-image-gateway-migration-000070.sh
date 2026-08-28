#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${IMAGE_GATEWAY_G3_MYSQL_MIGRATION_APPROVED:-NO}" != "YES" ]]; then
  echo "IMAGE_G3_MYSQL_MIGRATION=APPROVAL_REQUIRED target=isolated_temporary_container project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "IMAGE_G3_MYSQL_MIGRATION=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "IMAGE_G3_MYSQL_MIGRATION=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
up_file="${repo_root}/server/migrations/000070_expand_image_task_asset_repository.up.sql"
down_file="${repo_root}/server/migrations/000070_expand_image_task_asset_repository.down.sql"
test -f "${up_file}" || { echo "IMAGE_G3_MYSQL_MIGRATION=FAILED reason=up_file_missing"; exit 2; }
test -f "${down_file}" || { echo "IMAGE_G3_MYSQL_MIGRATION=FAILED reason=down_file_missing"; exit 2; }

suffix="${RANDOM}-$$"
network_name="molin-image-g3-${suffix}"
container_name="molin-image-g3-mysql-${suffix}"
build_cache_volume="molin-image-g3-go-build-${suffix}"
database_name="molin_image_g3_contract"
root_password="$(openssl rand -hex 24)"
go_mod_cache="$(go env GOMODCACHE)"
test -d "${go_mod_cache}" || { echo "IMAGE_G3_MYSQL_MIGRATION=FAILED reason=go_mod_cache_missing"; exit 2; }
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
[[ "${ready_count}" -ge 2 ]] || { echo "IMAGE_G3_MYSQL_MIGRATION=FAILED reason=mysql_not_ready"; exit 2; }

apply_file() {
  local file="$1"
  docker exec -e "MYSQL_PWD=${root_password}" "${container_name}" sh -c \
    "mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database='${database_name}' < '/migrations/${file}'"
}

for path in "${repo_root}"/server/migrations/*.up.sql; do
  base="$(basename "${path}")"
  version_text="${base%%_*}"
  version=$((10#${version_text}))
  if [[ "${version}" -le 69 ]]; then
    apply_file "${base}"
  fi
done
apply_file "$(basename "${up_file}")" >/dev/null

assert_scalar() {
  local sql="$1" expected="$2" label="$3" actual
  actual="$(mysql_exec -e "${sql}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "IMAGE_G3_MYSQL_MIGRATION=FAILED reason=${label} expected=${expected} actual=${actual}"
    exit 2
  fi
}

assert_scalar "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND ((table_name='ai_gateway_tasks' AND column_name='version_no') OR (table_name='ai_gateway_assets' AND column_name IN ('version_no','dispute_status','dispute_opened_at','dispute_resolved_at')))" "5" "repository_columns"
assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND constraint_name IN ('chk_ai_gateway_assets_dispute','chk_ai_gateway_tasks_version','chk_ai_gateway_assets_version')" "3" "repository_checks"
assert_scalar "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_gateway_assets' AND index_name='idx_ai_gateway_assets_dispute'" "3" "dispute_index_parts"

apply_file "$(basename "${down_file}")" >/dev/null
assert_scalar "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_gateway_assets' AND column_name='dispute_status'" "1" "down_dispute_retained"
apply_file "$(basename "${up_file}")" >/dev/null
assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND constraint_name='chk_ai_gateway_assets_dispute'" "1" "reup_dispute_check"

# 阶段回滚断言仍停在000070；运行当前HEAD仓储测试前补装共享媒体、VID-G2 Quote与VID-G3资产兼容层。
apply_file "000072_expand_video_gateway_schema.up.sql" >/dev/null
apply_file "000074_expand_video_pricing_quotes.up.sql" >/dev/null
apply_file "000075_enforce_video_task_asset_events.up.sql" >/dev/null

MSYS_NO_PATHCONV=1 docker run --rm --pull=never --network "${network_name}" \
  --mount "type=bind,src=${docker_repo_root},dst=/src,readonly" \
  --mount "type=bind,src=${docker_go_mod_cache},dst=/go/pkg/mod,readonly" \
  -v "${build_cache_volume}:/root/.cache/go-build" \
  -w /src/server \
  -e CGO_ENABLED=1 \
  -e GOPROXY=off \
  -e "MOLIN_IMAGE_G3_MYSQL_DSN=root:${root_password}@tcp(mysql:3306)/${database_name}?charset=utf8mb4&parseTime=true&loc=UTC" \
  golang:1.25-bookworm \
  go test -race -count=1 ./internal/modules/token_gateway/repository ./internal/modules/token_gateway/image \
    -run '^(TestImageTaskAssetRepositoryMySQLIsolationAndStates|TestFakeObjectStore.*)$'

docker volume rm "${build_cache_volume}" >/dev/null

echo "IMAGE_G3_MYSQL_MIGRATION=PASS mysql=8.0.46 full_chain_1_to_70=true current_head_compat_72_74=true task_cas=100_one_winner asset_primary_unique=100_one_winner dispute_cas=100_one_winner horizontal_isolation=true quarantine_blocked=true dispute_blocked=true legal_hold_cleanup_blocked=true deleted_blocked=true fake_object_store=true down_retained=true reup=true project_database=false provider_calls=0 wallet_writes=0"
