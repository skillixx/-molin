#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${AI_GATEWAY_G8_ISOLATED_APPROVED:-NO}" != "YES" ]]; then
  echo "G8_PRODUCTION_REHEARSAL=APPROVAL_REQUIRED target=isolated_temporary_stack production=false paid_upstream=false"
  exit 3
fi

for command_name in docker git openssl curl sha256sum; do
  command -v "${command_name}" >/dev/null 2>&1 || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=${command_name}_missing"; exit 2; }
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
suffix="${RANDOM}-$$"
network="molin-g8-prod-net-${suffix}"
mysql_container="molin-g8-prod-mysql-${suffix}"
redis_container="molin-g8-prod-redis-${suffix}"
rabbit_container="molin-g8-prod-rabbit-${suffix}"
api_container="molin-g8-prod-api-${suffix}"
nginx_container="molin-g8-prod-nginx-${suffix}"
candidate_image="molin-g8-candidate:${suffix}"
baseline_image="molin-g8-baseline:${suffix}"
work_dir="$(mktemp -d)"
docker_work_dir="${work_dir}"
if [[ "${OSTYPE:-}" == msys* || "${OSTYPE:-}" == cygwin* ]]; then
  docker_work_dir="$(cygpath -w "${work_dir}")"
fi
baseline_dir="${work_dir}/baseline"
database="molin_g8_prod_${suffix//-/_}"
restore_database="${database}_restore"
mysql_password="$(openssl rand -hex 24)"
jwt_secret="$(openssl rand -hex 32)"
refresh_secret="$(openssl rand -hex 32)"
id_card_secret="$(openssl rand -hex 32)"
email_address_secret="$(openssl rand -hex 32)"
email_idempotency_secret="$(openssl rand -hex 32)"
pull_policy="${G8_DOCKER_PULL_POLICY:-missing}"

cleanup() {
  docker container rm -f "${api_container}-extract" "${nginx_container}" "${api_container}" "${rabbit_container}" "${redis_container}" "${mysql_container}" >/dev/null 2>&1 || true
  docker network rm "${network}" >/dev/null 2>&1 || true
  docker image rm -f "${candidate_image}" "${baseline_image}" >/dev/null 2>&1 || true
  rm -rf "${work_dir}"
}
trap cleanup EXIT

echo "G8_PRODUCTION_REHEARSAL_PREFLIGHT target=temporary_production_shape impact=isolated_only rollback=remove_exact_temporary_resources production=false paid_upstream=false"
mkdir -p "${baseline_dir}" "${work_dir}/tls"
git -C "${repo_root}" archive origin/main | tar -x -C "${baseline_dir}"

docker build --pull=false -f "${repo_root}/infra/Dockerfile.server" -t "${candidate_image}" "${repo_root}" >/dev/null
docker build --pull=false -f "${baseline_dir}/infra/Dockerfile.server" -t "${baseline_image}" "${baseline_dir}" >/dev/null
candidate_image_id="$(docker image inspect "${candidate_image}" --format '{{.Id}}')"
baseline_image_id="$(docker image inspect "${baseline_image}" --format '{{.Id}}')"
docker create --name "${api_container}-extract" "${candidate_image}" >/dev/null
MSYS_NO_PATHCONV=1 docker cp "${api_container}-extract:/app/api" "${docker_work_dir}/candidate-api"
docker rm "${api_container}-extract" >/dev/null
candidate_binary_sha="$(sha256sum "${work_dir}/candidate-api" | awk '{print $1}')"

docker network create "${network}" >/dev/null
docker run -d --pull="${pull_policy}" --network "${network}" --network-alias mysql --name "${mysql_container}" --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g -e "MYSQL_ROOT_PASSWORD=${mysql_password}" -e "MYSQL_DATABASE=${database}" mysql:8.0 >/dev/null
docker run -d --pull="${pull_policy}" --network "${network}" --network-alias redis --name "${redis_container}" --tmpfs /data:rw,noexec,nosuid,size=128m redis:7 redis-server --save '' --appendonly no >/dev/null
docker run -d --pull="${pull_policy}" --network "${network}" --network-alias rabbitmq --name "${rabbit_container}" --tmpfs /var/lib/rabbitmq:rw,noexec,nosuid,size=256m rabbitmq:3-management-alpine >/dev/null

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" mysql --protocol=socket -uroot --database="${database}" --batch --skip-column-names "$@"
}
for _ in $(seq 1 90); do mysql_exec -e 'SELECT 1' >/dev/null 2>&1 && break; sleep 1; done
mysql_exec -e 'SELECT 1' >/dev/null 2>&1 || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=mysql_not_ready"; exit 2; }
for migration in "${repo_root}"/server/migrations/*.up.sql; do mysql_exec < "${migration}"; done
# 在真实 MySQL 8 解析七类审核策略门禁，避免仅靠字符串单测遗漏 JSON_TABLE 方言错误。
mysql_exec -e "SELECT COUNT(*) FROM ai_safety_policy_versions sp WHERE JSON_VALID(sp.rules_json) AND (SELECT COUNT(DISTINCT rules.category) FROM JSON_TABLE(sp.rules_json, '\$[*]' COLUMNS(category VARCHAR(32) PATH '\$.category')) AS rules WHERE rules.category IN ('illegal','sexual','gambling','drugs','terror','hate','self_harm'))=7;" >/dev/null
schema_before="$(mysql_exec -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='${database}';")"

# 备份只包含隔离库结构；恢复到同一临时 MySQL 的独立库，验证备份可读且恢复步骤可执行。
docker exec -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" mysqldump -uroot --no-data --skip-comments "${database}" > "${work_dir}/schema.sql"
backup_sha="$(sha256sum "${work_dir}/schema.sql" | awk '{print $1}')"
docker exec -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" mysql -uroot -e "CREATE DATABASE ${restore_database};"
docker exec -i -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" mysql -uroot "${restore_database}" < "${work_dir}/schema.sql"
restore_tables="$(docker exec -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" mysql -uroot --batch --skip-column-names -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='${restore_database}';")"
[[ "${schema_before}" == "${restore_tables}" ]] || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=backup_restore_mismatch"; exit 2; }

run_api() {
  local image="$1"
  docker container rm -f "${api_container}" >/dev/null 2>&1 || true
  docker run -d --network "${network}" --network-alias api --name "${api_container}" --log-driver json-file --log-opt max-size=1m --log-opt max-file=2 \
    -e APP_ENV=production -e API_HOST=0.0.0.0 -e API_PORT=8080 -e AI_GATEWAY_TRAFFIC_ENABLED=false \
    -e MYSQL_HOST=mysql -e MYSQL_PORT=3306 -e MYSQL_USER=root -e "MYSQL_PASSWORD=${mysql_password}" -e "MYSQL_DATABASE=${database}" \
    -e REDIS_ADDR=redis:6379 -e RABBITMQ_URL=amqp://guest:guest@rabbitmq:5672/ \
    -e "JWT_SECRET=${jwt_secret}" -e "REFRESH_TOKEN_SECRET=${refresh_secret}" -e "ID_CARD_HMAC_SECRET=${id_card_secret}" \
    -e EMAIL_ADAPTER=mock -e "EMAIL_ADDRESS_HMAC_SECRET=${email_address_secret}" -e "EMAIL_IDEMPOTENCY_SECRET=${email_idempotency_secret}" \
    "${image}" >/dev/null
  for _ in $(seq 1 60); do docker exec "${api_container}" wget -q -O- http://127.0.0.1:8080/api/health >/dev/null 2>&1 && return 0; sleep 1; done
  docker logs --tail 60 "${api_container}" >&2 || true
  return 1
}

run_api "${baseline_image}" || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=baseline_not_ready"; exit 2; }
run_api "${candidate_image}" || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=candidate_not_ready"; exit 2; }

openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '//CN=g8-rehearsal.invalid' -keyout "${work_dir}/tls/key.pem" -out "${work_dir}/tls/cert.pem" >/dev/null 2>&1
cat > "${work_dir}/nginx.conf" <<'NGINX'
events { worker_connections 128; }
http {
  server {
    listen 443 ssl;
    server_name _;
    ssl_certificate /etc/nginx/tls/cert.pem;
    ssl_certificate_key /etc/nginx/tls/key.pem;
    client_max_body_size 20m;
    location /v1/ { proxy_pass http://api:8080; proxy_http_version 1.1; proxy_buffering off; proxy_request_buffering off; proxy_connect_timeout 10s; proxy_send_timeout 300s; proxy_read_timeout 300s; }
    location /api/ { proxy_pass http://api:8080; proxy_http_version 1.1; proxy_buffering off; proxy_connect_timeout 10s; proxy_read_timeout 60s; }
    location = /api/internal/metrics { return 404; }
  }
}
NGINX
MSYS_NO_PATHCONV=1 docker run -d --pull="${pull_policy}" --network "${network}" -p 127.0.0.1::443 --name "${nginx_container}" --read-only --tmpfs /var/cache/nginx --tmpfs /var/run --tmpfs /tmp -v "${docker_work_dir}/nginx.conf:/etc/nginx/nginx.conf:ro" -v "${docker_work_dir}/tls:/etc/nginx/tls:ro" nginx:1.29.1-alpine >/dev/null
tls_address="$(docker port "${nginx_container}" 443/tcp | head -n1)"
for _ in $(seq 1 30); do curl -kfsS "https://${tls_address}/api/health" >/dev/null 2>&1 && break; sleep 1; done
curl -kfsS "https://${tls_address}/api/health" >/dev/null || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=tls_health"; exit 2; }
metrics_status="$(curl -ksS -o /dev/null -w '%{http_code}' "https://${tls_address}/api/internal/metrics")"
traffic_status="$(curl -ksS -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -d '{"model":"molin/closed","messages":[{"role":"user","content":"closed"}]}' "https://${tls_address}/v1/chat/completions")"
# 未注入任何 Project SK，鉴权中间件会先以 404 隐藏入口；已认证请求的总闸 503 由后端回归测试覆盖。
[[ "${metrics_status}" == "404" && "${traffic_status}" == "404" ]] || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=fail_closed metrics=${metrics_status} traffic=${traffic_status}"; exit 2; }

# 回滚只切换应用制品，数据库和财务事实保持原位；随后再次恢复候选版本。
run_api "${baseline_image}" || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=rollback_baseline_not_ready"; exit 2; }
schema_after_rollback="$(mysql_exec -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='${database}';")"
[[ "${schema_after_rollback}" == "${schema_before}" ]] || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=rollback_schema_changed"; exit 2; }
run_api "${candidate_image}" || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=candidate_restore_not_ready"; exit 2; }

log_config="$(docker inspect "${api_container}" --format '{{.HostConfig.LogConfig.Type}}:{{index .HostConfig.LogConfig.Config "max-size"}}:{{index .HostConfig.LogConfig.Config "max-file"}}')"
[[ "${log_config}" == "json-file:1m:2" ]] || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=log_rotation"; exit 2; }

echo "G8_PRODUCTION_REHEARSAL=PASS source_commit=$(git -C "${repo_root}" rev-parse HEAD) candidate_image_id=${candidate_image_id} baseline_image_id=${baseline_image_id} candidate_binary_sha256=${candidate_binary_sha} backup_sha256=${backup_sha} schema_tables=${schema_before} readiness_sql_mysql8=true tls=true sse_buffering=false request_body_limit=20m log_rotation=true metrics_public_status=404 anonymous_traffic_status=404 authenticated_traffic_gate_test=503 old_version_started=true candidate_started=true rollback_started=true candidate_restored=true database_preserved=true production=false paid_upstream=false"
