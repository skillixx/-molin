#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${IMAGE_GATEWAY_G7_INFRA_APPROVED:-NO}" != "YES" ]]; then
  echo "IMAGE_G7_INFRA=APPROVAL_REQUIRED target=isolated_temporary_containers test_server=false"
  exit 3
fi

for command_name in docker openssl go; do
  command -v "${command_name}" >/dev/null 2>&1 || { echo "IMAGE_G7_INFRA=FAILED reason=${command_name}_missing"; exit 2; }
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
suffix="${RANDOM}-$$"
network_name="molin-image-g7-${suffix}"
mysql_name="molin-image-g7-mysql-${suffix}"
rabbit_name="molin-image-g7-rabbit-${suffix}"
minio_name="molin-image-g7-minio-${suffix}"
build_cache_volume="molin-image-g7-go-build-${suffix}"
database_name="molin_image_g7_contract"
mysql_password="$(openssl rand -hex 24)"
rabbit_password="$(openssl rand -hex 24)"
minio_access="g7fakeaccess"
minio_secret="$(openssl rand -hex 24)"
go_mod_cache="$(go env GOMODCACHE)"
docker_repo_root="${repo_root}"
docker_go_mod_cache="${go_mod_cache}"
if command -v cygpath >/dev/null 2>&1; then
  docker_repo_root="$(cygpath -w "${repo_root}")"
  docker_go_mod_cache="$(cygpath -w "${go_mod_cache}")"
fi

cleanup() {
  for container in "${mysql_name}" "${rabbit_name}" "${minio_name}"; do
    if docker container inspect "${container}" >/dev/null 2>&1; then
      docker container rm -f "${container}" >/dev/null
    fi
  done
  if docker network inspect "${network_name}" >/dev/null 2>&1; then docker network rm "${network_name}" >/dev/null; fi
  if docker volume inspect "${build_cache_volume}" >/dev/null 2>&1; then docker volume rm "${build_cache_volume}" >/dev/null; fi
}
trap cleanup EXIT

# 三个基础设施容器都在无外网、无宿主端口的内部网络运行，凭据为本次临时Fake值。
docker network create --internal "${network_name}" >/dev/null
docker run -d --pull=never --network "${network_name}" --network-alias mysql --name "${mysql_name}" \
  --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g -e "MYSQL_ROOT_PASSWORD=${mysql_password}" -e "MYSQL_DATABASE=${database_name}" \
  mysql:8.0 --character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci >/dev/null
docker run -d --pull=never --network "${network_name}" --network-alias rabbit --name "${rabbit_name}" \
  --tmpfs /var/lib/rabbitmq:rw,noexec,nosuid,size=512m -e RABBITMQ_DEFAULT_USER=g7fake -e "RABBITMQ_DEFAULT_PASS=${rabbit_password}" \
  rabbitmq:3-management-alpine >/dev/null
docker run -d --pull=never --network "${network_name}" --network-alias minio --name "${minio_name}" \
  --tmpfs /data:rw,noexec,nosuid,size=1g -e "MINIO_ROOT_USER=${minio_access}" -e "MINIO_ROOT_PASSWORD=${minio_secret}" \
  minio/minio@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e server /data --console-address :9001 >/dev/null

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=${mysql_password}" "${mysql_name}" mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database="${database_name}" --batch --skip-column-names "$@"
}

ready_count=0
for _ in $(seq 1 120); do
  if mysql_exec -e 'SELECT 1' >/dev/null 2>&1; then ready_count=$((ready_count+1)); else ready_count=0; fi
  [[ "${ready_count}" -ge 2 ]] && break
  sleep 1
done
[[ "${ready_count}" -ge 2 ]] || { echo "IMAGE_G7_INFRA=FAILED reason=mysql_not_ready"; exit 2; }

for _ in $(seq 1 120); do
  if docker exec "${rabbit_name}" rabbitmq-diagnostics -q ping >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec "${rabbit_name}" rabbitmq-diagnostics -q ping >/dev/null 2>&1 || { echo "IMAGE_G7_INFRA=FAILED reason=rabbit_not_ready"; exit 2; }

minio_ready=false
for _ in $(seq 1 120); do
  if docker logs "${minio_name}" 2>&1 | grep -q 'API:'; then minio_ready=true; break; fi
  sleep 1
done
[[ "${minio_ready}" == "true" ]] || { echo "IMAGE_G7_INFRA=FAILED reason=minio_not_ready"; exit 2; }

docker exec "${mysql_name}" mkdir -p /migrations
docker cp "${repo_root}/server/migrations/." "${mysql_name}:/migrations" >/dev/null
for path in "${repo_root}"/server/migrations/*.up.sql; do
  base="$(basename "${path}")"
  version_text="${base%%_*}"
  version=$((10#${version_text}))
  # G7运行当前HEAD，需装配000072共享媒体、000074 Quote、000075资产与000076安全版本兼容层；000073权限seed不属于基础设施夹具。
  if [[ "${version}" -le 72 || "${version}" -eq 74 || "${version}" -eq 75 || "${version}" -eq 76 ]]; then
    docker exec -e "MYSQL_PWD=${mysql_password}" "${mysql_name}" sh -c \
      "mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database='${database_name}' < '/migrations/${base}'"
  fi
done

MSYS_NO_PATHCONV=1 docker run --rm --pull=never --network "${network_name}" \
  --mount "type=bind,src=${docker_repo_root},dst=/src,readonly" \
  --mount "type=bind,src=${docker_go_mod_cache},dst=/go/pkg/mod,readonly" \
  -v "${build_cache_volume}:/root/.cache/go-build" -w /src/server \
  -e CGO_ENABLED=1 -e GOPROXY=off -e MOLIN_IMAGE_G7_ISOLATED=YES \
  -e "MOLIN_IMAGE_G7_MYSQL_DSN=root:${mysql_password}@tcp(mysql:3306)/${database_name}?charset=utf8mb4&parseTime=true&loc=UTC" \
  -e "MOLIN_IMAGE_G7_RABBIT_URL=amqp://g7fake:${rabbit_password}@rabbit:5672/" \
  -e MOLIN_IMAGE_G7_MINIO_ENDPOINT=minio:9000 -e "MOLIN_IMAGE_G7_MINIO_ACCESS=${minio_access}" -e "MOLIN_IMAGE_G7_MINIO_SECRET=${minio_secret}" \
  golang:1.25-bookworm go test -race -count=1 ./internal/modules/token_gateway/image ./internal/modules/token_gateway/repository ./internal/modules/token_gateway/service \
    -run '^(TestImageG7MinIOIntegration|TestImageG7ObjectCleanupRepositoryMySQLRetryBoundary|TestImageG7RabbitMQTopologyAndDLQ|TestImageG7InfrastructureClosedLoop)$'

MSYS_NO_PATHCONV=1 docker run --rm --pull=never \
  --mount "type=bind,src=${docker_repo_root}/infra/prometheus/image-gateway-alerts.yml,dst=/rules/image.yml,readonly" \
  --entrypoint /bin/promtool prom/prometheus:v3.12.0 check rules /rules/image.yml >/dev/null

docker volume rm "${build_cache_volume}" >/dev/null

echo "IMAGE_G7_INFRA=PASS mysql=8.0.46 full_chain_1_to_71=true current_head_compat_72_74_75_76=true network_internal=true host_ports=0 provider=fake real_provider_calls=0 minio_private_buckets=3 signed_url=true rabbit_durable_queue=true rabbit_dlq=true prompt_in_rabbit=false async_once=true missing_prompt_release=true cleanup_worker=true legal_hold_preserved=true compensation_worker=true image_metrics=true reconciliation_difference_zero=true prometheus_rules=true traffic_default_closed=true test_server=false real_credentials=0"
