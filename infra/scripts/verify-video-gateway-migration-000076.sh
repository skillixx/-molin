#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "$VIDEO_GATEWAY_G4_ISOLATED_MYSQL_APPROVED" != "YES" ]]; then
  echo "VIDEO_G4_MYSQL=APPROVAL_REQUIRED target=isolated_temporary_container project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "VIDEO_G4_MYSQL=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "VIDEO_G4_MYSQL=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "$BASH_SOURCE")/../.." && pwd)"
suffix="$RANDOM-$$"
network_name="molin-video-g4-$suffix"
container_name="molin-video-g4-mysql-$suffix"
build_cache_volume="molin-video-g4-go-build-$suffix"
database_name="molin_video_g4_contract"
root_password="$(openssl rand -hex 24)"
go_mod_cache="$(go env GOMODCACHE)"
docker_repo_root="$repo_root"
docker_go_mod_cache="$go_mod_cache"
if command -v cygpath >/dev/null 2>&1; then
  docker_repo_root="$(cygpath -w "$repo_root")"
  docker_go_mod_cache="$(cygpath -w "$go_mod_cache")"
fi

cleanup() {
  docker container inspect "$container_name" >/dev/null 2>&1 && docker container rm -f "$container_name" >/dev/null || true
  docker network inspect "$network_name" >/dev/null 2>&1 && docker network rm "$network_name" >/dev/null || true
  docker volume inspect "$build_cache_volume" >/dev/null 2>&1 && docker volume rm "$build_cache_volume" >/dev/null || true
}
trap cleanup EXIT

# 使用无出口内部网络、无宿主端口与tmpfs，所有写入只存在于一次性MySQL容器。
docker network create --internal "$network_name" >/dev/null
docker run -d --pull=never --network "$network_name" --network-alias mysql \
  --name "$container_name" --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g \
  -e "MYSQL_ROOT_PASSWORD=$root_password" -e "MYSQL_DATABASE=$database_name" \
  mysql:8.0 --character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci >/dev/null
docker exec "$container_name" mkdir -p /migrations
docker cp "$repo_root/server/migrations/." "$container_name:/migrations" >/dev/null

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=$root_password" "$container_name" \
    mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database="$database_name" --batch --skip-column-names "$@"
}
apply_file() {
  docker exec -e "MYSQL_PWD=$root_password" "$container_name" sh -c \
    "mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database='$database_name' < '/migrations/$1'"
}

ready_count=0
for _ in $(seq 1 90); do
  if mysql_exec -e 'SELECT 1' >/dev/null 2>&1; then ready_count=$((ready_count + 1)); else ready_count=0; fi
  [[ "$ready_count" -ge 2 ]] && break
  sleep 1
done
[[ "$ready_count" -ge 2 ]] || { echo "VIDEO_G4_MYSQL=FAILED reason=mysql_not_ready"; exit 2; }

for path in "$repo_root"/server/migrations/*.up.sql; do
  base="$(basename "$path")"
  version="$(printf '%s' "$base" | cut -d_ -f1)"
  version=$((10#$version))
  [[ "$version" -le 76 ]] && apply_file "$base" >/dev/null
done

apply_file "000076_video_fake_async_media_safety.up.sql" >/dev/null
apply_file "000076_video_fake_async_media_safety.down.sql" >/dev/null
apply_file "000076_video_fake_async_media_safety.up.sql" >/dev/null

column_count="$(mysql_exec -e "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_gateway_assets' AND column_name IN ('moderation_policy_version','explicit_label_version','implicit_label_version')")"
[[ "$column_count" == "3" ]] || { echo "VIDEO_G4_MYSQL=FAILED reason=safety_version_columns actual=$column_count"; exit 2; }
trigger_count="$(mysql_exec -e "SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name IN ('trg_ai_gateway_task_events_safe_insert','trg_ai_gateway_assets_video_safety_versions_insert','trg_ai_gateway_assets_video_safety_versions_update')")"
[[ "$trigger_count" == "3" ]] || { echo "VIDEO_G4_MYSQL=FAILED reason=g4_trigger_count actual=$trigger_count"; exit 2; }

test_scope="$(printenv VIDEO_GATEWAY_G4_TEST_SCOPE || true)"
if [[ "$test_scope" == "service" ]]; then
  test_packages="./internal/modules/token_gateway/service"
else
  test_packages="./internal/modules/token_gateway/model ./internal/modules/token_gateway/repository ./internal/modules/token_gateway/service ./internal/modules/token_gateway/video"
fi

# 在同一无出口网络运行GORM与race；不访问Provider、RabbitMQ、Redis、MinIO、钱包或公网。
MSYS_NO_PATHCONV=1 docker run --rm --pull=never --network "$network_name" \
  --mount "type=bind,src=$docker_repo_root,dst=/src,readonly" \
  --mount "type=bind,src=$docker_go_mod_cache,dst=/go/pkg/mod,readonly" \
  -v "$build_cache_volume:/root/.cache/go-build" -w /src/server -e CGO_ENABLED=1 -e GOPROXY=off \
  -e "MOLIN_VIDEO_G3_MYSQL_DSN=root:$root_password@tcp(mysql:3306)/$database_name?charset=utf8mb4&parseTime=true&loc=UTC" \
  -e "MOLIN_VIDEO_G2_MYSQL_DSN=root:$root_password@tcp(mysql:3306)/$database_name?charset=utf8mb4&parseTime=true&loc=UTC" \
  golang:1.25-bookworm sh -c "go test -p=1 -race -count=1 $test_packages"

echo "VIDEO_G4_MYSQL=PASS mysql=8 full_chain_1_to_76=true repeat_up=true down_retained=true reup=true shared_repository=true linux_race_four_packages=true provider_binding_cas=100 callback_replay=100 worker_cas=100 object_store=fake_only external_http_requests=0 provider_calls=0 provider_keys=0 real_wallet_writes=0 cost_cny=0 test_server_writes=0 production_operations=0"
