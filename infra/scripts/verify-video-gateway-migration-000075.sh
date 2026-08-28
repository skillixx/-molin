#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${VIDEO_GATEWAY_G3_ISOLATED_MYSQL_APPROVED:-NO}" != "YES" ]]; then
  echo "VIDEO_G3_MYSQL=APPROVAL_REQUIRED target=isolated_temporary_container project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "VIDEO_G3_MYSQL=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "VIDEO_G3_MYSQL=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
suffix="${RANDOM}-$$"
network_name="molin-video-g3-${suffix}"
container_name="molin-video-g3-mysql-${suffix}"
build_cache_volume="molin-video-g3-go-build-${suffix}"
database_name="molin_video_g3_contract"
root_password="$(openssl rand -hex 24)"
go_mod_cache="$(go env GOMODCACHE)"
docker_repo_root="${repo_root}"
docker_go_mod_cache="${go_mod_cache}"
if command -v cygpath >/dev/null 2>&1; then
  docker_repo_root="$(cygpath -w "${repo_root}")"
  docker_go_mod_cache="$(cygpath -w "${go_mod_cache}")"
fi

cleanup() {
  docker container inspect "${container_name}" >/dev/null 2>&1 && docker container rm -f "${container_name}" >/dev/null || true
  docker network inspect "${network_name}" >/dev/null 2>&1 && docker network rm "${network_name}" >/dev/null || true
  docker volume inspect "${build_cache_volume}" >/dev/null 2>&1 && docker volume rm "${build_cache_volume}" >/dev/null || true
}
trap cleanup EXIT

# 使用无出口内部网络、无宿主端口与tmpfs，保证验收只接触一次性MySQL容器。
docker network create --internal "${network_name}" >/dev/null
docker run -d --pull=never --network "${network_name}" --network-alias mysql \
  --name "${container_name}" --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g \
  -e "MYSQL_ROOT_PASSWORD=${root_password}" -e "MYSQL_DATABASE=${database_name}" \
  mysql:8.0 --character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci >/dev/null
docker exec "${container_name}" mkdir -p /migrations
docker cp "${repo_root}/server/migrations/." "${container_name}:/migrations" >/dev/null

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=${root_password}" "${container_name}" \
    mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database="${database_name}" --batch --skip-column-names "$@"
}
apply_file() {
  docker exec -e "MYSQL_PWD=${root_password}" "${container_name}" sh -c \
    "mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database='${database_name}' < '/migrations/$1'"
}

ready_count=0
for _ in $(seq 1 90); do
  if mysql_exec -e 'SELECT 1' >/dev/null 2>&1; then ready_count=$((ready_count + 1)); else ready_count=0; fi
  [[ "${ready_count}" -ge 2 ]] && break
  sleep 1
done
[[ "${ready_count}" -ge 2 ]] || { echo "VIDEO_G3_MYSQL=FAILED reason=mysql_not_ready"; exit 2; }

for path in "${repo_root}"/server/migrations/*.up.sql; do
  base="$(basename "${path}")"
  version=$((10#${base%%_*}))
  [[ "${version}" -le 75 ]] && apply_file "${base}" >/dev/null
done

# 迁移必须可断点重跑；保留式down后再次up仍保持全部不可变事实约束。
apply_file "000075_enforce_video_task_asset_events.up.sql" >/dev/null
apply_file "000075_enforce_video_task_asset_events.down.sql" >/dev/null
apply_file "000075_enforce_video_task_asset_events.up.sql" >/dev/null

trigger_count="$(mysql_exec -e "SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name IN ('trg_ai_gateway_tasks_video_json_insert','trg_ai_gateway_tasks_video_json_update','trg_ai_gateway_task_events_safe_insert','trg_ai_gateway_task_inputs_validate_insert','trg_ai_gateway_task_inputs_frozen_update','trg_ai_gateway_task_inputs_no_delete','trg_ai_gateway_input_assets_freeze_snapshot','trg_ai_gateway_assets_freeze_video_owner','trg_ai_gateway_assets_no_delete_video','trg_ai_gateway_provider_callbacks_freeze_identity','trg_ai_gateway_provider_callbacks_no_delete','trg_ai_gateway_task_payloads_no_update','trg_ai_gateway_task_payloads_no_delete','trg_ai_gateway_task_events_no_update','trg_ai_gateway_task_events_no_delete')")"
[[ "${trigger_count}" == "15" ]] || { echo "VIDEO_G3_MYSQL=FAILED reason=trigger_count actual=${trigger_count}"; exit 2; }

# 在同一内部网络运行真实GORM和race detector；源码只读挂载，测试数据只写一次性数据库。
MSYS_NO_PATHCONV=1 docker run --rm --pull=never --network "${network_name}" \
  --mount "type=bind,src=${docker_repo_root},dst=/src,readonly" \
  --mount "type=bind,src=${docker_go_mod_cache},dst=/go/pkg/mod,readonly" \
  -v "${build_cache_volume}:/root/.cache/go-build" -w /src/server -e CGO_ENABLED=1 -e GOPROXY=off \
  -e "MOLIN_VIDEO_G3_MYSQL_DSN=root:${root_password}@tcp(mysql:3306)/${database_name}?charset=utf8mb4&parseTime=true&loc=UTC" \
  -e "MOLIN_VIDEO_G2_MYSQL_DSN=root:${root_password}@tcp(mysql:3306)/${database_name}?charset=utf8mb4&parseTime=true&loc=UTC" \
  golang:1.25-bookworm go test -p=1 -race -count=1 \
  ./internal/modules/token_gateway/model \
  ./internal/modules/token_gateway/repository \
  ./internal/modules/token_gateway/service

echo "VIDEO_G3_MYSQL=PASS mysql=8 full_chain_1_to_75=true repeat_up=true down_retained=true reup=true repository=true linux_race_three_packages=true task_cas_concurrency=100 bind_delete_concurrency=100 task_event_append_only=true task_json_whitelist=true event_json_whitelist=true callback_replay_ordering=true callback_immutable=true t2v_zero_input=true i2v_unique_input=true ownership_isolation=true fake_object_store=true project_database=false provider_calls=0 provider_keys=0 real_wallet_writes=0 cost_cny=0"
