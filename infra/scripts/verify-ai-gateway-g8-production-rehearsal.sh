#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${AI_GATEWAY_G8_ISOLATED_APPROVED:-NO}" != "YES" ]]; then
  echo "G8_PRODUCTION_REHEARSAL=APPROVAL_REQUIRED target=isolated_temporary_stack production=false paid_upstream=false"
  exit 3
fi

for command_name in docker git openssl curl sha256sum python; do
  command -v "${command_name}" >/dev/null 2>&1 || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=${command_name}_missing"; exit 2; }
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
docker_repo_root="${repo_root}"
if [[ "${OSTYPE:-}" == msys* || "${OSTYPE:-}" == cygwin* ]]; then
  docker_repo_root="$(cygpath -w "${repo_root}")"
fi
suffix="${RANDOM}-$$"
data_network="molin-g8-prod-data-${suffix}"
lb_network="molin-g8-prod-lb-${suffix}"
nodes_network="molin-g8-prod-nodes-${suffix}"
egress_network="molin-g8-prod-egress-${suffix}"
public_network="molin-g8-prod-public-${suffix}"
mysql_container="molin-g8-prod-mysql-${suffix}"
redis_container="molin-g8-prod-redis-${suffix}"
rabbit_container="molin-g8-prod-rabbit-${suffix}"
bifrost_1_container="molin-g8-prod-bifrost-1-${suffix}"
bifrost_2_container="molin-g8-prod-bifrost-2-${suffix}"
bifrost_lb_container="molin-g8-prod-bifrost-lb-${suffix}"
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
provider_key="$(openssl rand -hex 16)"
api_key_secret="$(openssl rand -hex 32)"
internal_token="$(openssl rand -hex 32)"
bifrost_internal_token="$(openssl rand -hex 32)"
pull_policy="${G8_DOCKER_PULL_POLICY:-missing}"

cleanup() {
  docker container rm -f "${api_container}-extract" "${nginx_container}" "${api_container}" "${bifrost_lb_container}" "${bifrost_2_container}" "${bifrost_1_container}" "${rabbit_container}" "${redis_container}" "${mysql_container}" >/dev/null 2>&1 || true
  docker network rm "${public_network}" "${egress_network}" "${nodes_network}" "${lb_network}" "${data_network}" >/dev/null 2>&1 || true
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

docker network create --internal "${data_network}" >/dev/null
docker network create --internal "${lb_network}" >/dev/null
docker network create --internal "${nodes_network}" >/dev/null
docker network create "${egress_network}" >/dev/null
docker network create "${public_network}" >/dev/null
docker run -d --pull="${pull_policy}" --network "${data_network}" --network-alias mysql --name "${mysql_container}" --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g -e "MYSQL_ROOT_PASSWORD=${mysql_password}" -e "MYSQL_DATABASE=${database}" mysql:8.0 >/dev/null
docker run -d --pull="${pull_policy}" --network "${data_network}" --network-alias redis --name "${redis_container}" --tmpfs /data:rw,noexec,nosuid,size=128m redis:7 redis-server --save '' --appendonly no >/dev/null
docker run -d --pull="${pull_policy}" --network "${data_network}" --network-alias rabbitmq --name "${rabbit_container}" --tmpfs /var/lib/rabbitmq:rw,noexec,nosuid,size=256m rabbitmq:3-management-alpine >/dev/null
MSYS_NO_PATHCONV=1 docker run -d --pull="${pull_policy}" --network "${nodes_network}" --network-alias bifrost-1 --name "${bifrost_1_container}" -e G8_FAKE_PORT=8080 -e G8_REQUIRE_EMPTY_AUTHORIZATION=true -v "${docker_repo_root}:/workspace:ro" -w /workspace python:3.12-alpine python infra/scripts/g8_fake_text_upstream.py >/dev/null
MSYS_NO_PATHCONV=1 docker run -d --pull="${pull_policy}" --network "${nodes_network}" --network-alias bifrost-2 --name "${bifrost_2_container}" -e G8_FAKE_PORT=8080 -e G8_REQUIRE_EMPTY_AUTHORIZATION=true -v "${docker_repo_root}:/workspace:ro" -w /workspace python:3.12-alpine python infra/scripts/g8_fake_text_upstream.py >/dev/null
docker network connect "${egress_network}" "${bifrost_1_container}"
docker network connect "${egress_network}" "${bifrost_2_container}"
MSYS_NO_PATHCONV=1 docker run -d --pull="${pull_policy}" --network "${nodes_network}" --name "${bifrost_lb_container}" -e "BIFROST_INTERNAL_TOKEN=${bifrost_internal_token}" -v "${docker_repo_root}/infra/bifrost/nginx.conf.template:/etc/nginx/nginx.conf.template:ro" -v "${docker_repo_root}/infra/bifrost/start-nginx.sh:/usr/local/bin/start-bifrost-nginx.sh:ro" nginx:1.29.1-alpine /bin/sh /usr/local/bin/start-bifrost-nginx.sh >/dev/null
docker network connect --alias bifrost-lb "${lb_network}" "${bifrost_lb_container}"
for _ in $(seq 1 30); do docker exec "${bifrost_lb_container}" wget -q -O- http://127.0.0.1:8080/health >/dev/null 2>&1 && break; sleep 1; done
docker exec "${bifrost_lb_container}" wget -q -O- http://127.0.0.1:8080/health >/dev/null || { docker logs --tail 60 "${bifrost_lb_container}" >&2 || true; echo "G8_PRODUCTION_REHEARSAL=FAILED reason=bifrost_lb_not_ready"; exit 2; }
bifrost_unauthorized="$(docker run --rm --network "${lb_network}" curlimages/curl:8.12.1 -sS -o /dev/null -w '%{http_code}' http://bifrost-lb:8080/v1/chat/completions)"
bifrost_authorized="$(docker run --rm --network "${lb_network}" curlimages/curl:8.12.1 -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer ${bifrost_internal_token}" -H 'Content-Type: application/json' -d '{"model":"fake/g8-text","messages":[{"role":"user","content":"topology"}]}' http://bifrost-lb:8080/v1/chat/completions)"
[[ "${bifrost_unauthorized}" == "401" && "${bifrost_authorized}" == "200" ]] || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=bifrost_lb_auth unauthorized=${bifrost_unauthorized} authorized=${bifrost_authorized}"; exit 2; }

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" mysql --protocol=socket -uroot --database="${database}" --batch --skip-column-names "$@"
}
for _ in $(seq 1 90); do mysql_exec -e 'SELECT 1' >/dev/null 2>&1 && break; sleep 1; done
mysql_exec -e 'SELECT 1' >/dev/null 2>&1 || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=mysql_not_ready"; exit 2; }
for migration in "${repo_root}"/server/migrations/*.up.sql; do mysql_exec < "${migration}"; done
# 在真实 MySQL 8 解析七类审核策略门禁，避免仅靠字符串单测遗漏 JSON_TABLE 方言错误。
mysql_exec -e "SELECT COUNT(*) FROM ai_safety_policy_versions sp WHERE JSON_VALID(sp.rules_json) AND (SELECT COUNT(DISTINCT rules.category) FROM JSON_TABLE(sp.rules_json, '\$[*]' COLUMNS(category VARCHAR(32) PATH '\$.category')) AS rules WHERE rules.category IN ('illegal','sexual','gambling','drugs','terror','hate','self_harm'))=7;" >/dev/null
mysql_exec -e "INSERT INTO users(id,username,email,email_verified,password_hash,real_name_status,status) VALUES(9001,'g8_rehearsal','g8-rehearsal@example.invalid',1,'disabled-login','verified','active');"

# 写入五个文字模型、两个健康渠道、逐模型价格/路由和一份七类审核策略，供生产启动门禁在真实 MySQL 8 上核验。
mysql_exec -e "INSERT INTO token_channels(id,code,name,type,base_url,api_key_encrypted,status,priority,health_status,last_health_check_at) VALUES
  (9101,'g8-provider-a','G8 隔离渠道 A','openai_compatible','https://provider-a.example.invalid','isolated-ciphertext','active',100,'healthy',UTC_TIMESTAMP()),
  (9102,'g8-provider-b','G8 隔离渠道 B','openai_compatible','https://provider-b.example.invalid','isolated-ciphertext','active',90,'healthy',UTC_TIMESTAMP());"
for model_index in $(seq 1 5); do
  model_code="g8/text-${model_index}"
  channel_id=9101
  [[ $((model_index % 2)) -eq 0 ]] && channel_id=9102
  mysql_exec -e "INSERT INTO token_models(logical_model_code,display_name,modality,status,capabilities_json,context_window,release_version_no,published_at) VALUES('${model_code}','G8 隔离文字模型 ${model_index}','chat','active',JSON_OBJECT('stream',TRUE),8192,1,UTC_TIMESTAMP());
    INSERT INTO ai_model_routes(logical_model_code,channel_id,provider_model,priority,weight,timeout_ms,max_retries,circuit_breaker_threshold,fallback_order,status,version_no,updated_by) VALUES('${model_code}',${channel_id},'fake-${model_index}',100,100,30000,0,5,0,'active',1,9001);
    INSERT INTO ai_model_route_runtime_states(route_id,consecutive_failures,circuit_open_until) VALUES(LAST_INSERT_ID(),0,NULL);
    INSERT INTO ai_price_versions(logical_model_code,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by,approved_by,approved_at,published_at) VALUES('${model_code}',1,'CNY',1,'active',0.15,8192,4096,'confirmed_usage','ceil_8',UTC_TIMESTAMP(),UTC_TIMESTAMP()+INTERVAL 30 DAY,UTC_TIMESTAMP()-INTERVAL 1 MINUTE,9001,9001,UTC_TIMESTAMP(),UTC_TIMESTAMP());
    SET @g8_price_version_id=LAST_INSERT_ID();
    INSERT INTO ai_price_skus(price_version_id,meter_type,variant_json,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES
      (@g8_price_version_id,'input_tokens',NULL,SHA2(CONCAT('${model_code}','-input'),256),0.00000100,0.00000200,1000,'CNY'),
      (@g8_price_version_id,'output_tokens',NULL,SHA2(CONCAT('${model_code}','-output'),256),0.00000200,0.00000400,1000,'CNY'),
      (@g8_price_version_id,'cached_tokens',NULL,SHA2(CONCAT('${model_code}','-cached'),256),0.00000050,0.00000100,1000,'CNY'),
      (@g8_price_version_id,'reasoning_tokens',NULL,SHA2(CONCAT('${model_code}','-reasoning'),256),0.00000200,0.00000400,1000,'CNY');"
done
mysql_exec -e "INSERT INTO ai_safety_policy_versions(version_no,status,refusal_message,rules_json,created_by,approved_by,effective_at) VALUES(1,'active','隔离审核拒绝',JSON_ARRAY(JSON_OBJECT('category','illegal'),JSON_OBJECT('category','sexual'),JSON_OBJECT('category','gambling'),JSON_OBJECT('category','drugs'),JSON_OBJECT('category','terror'),JSON_OBJECT('category','hate'),JSON_OBJECT('category','self_harm')),9001,9001,UTC_TIMESTAMP()-INTERVAL 1 MINUTE);"
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
  local traffic_enabled="${2:-false}"
  local max_attempts="${3:-60}"
  docker container rm -f "${api_container}" >/dev/null 2>&1 || true
  docker run -d --network "${data_network}" --network-alias api --name "${api_container}" --log-driver json-file --log-opt max-size=1m --log-opt max-file=2 \
    -e APP_ENV=production -e API_HOST=0.0.0.0 -e API_PORT=8080 -e "AI_GATEWAY_TRAFFIC_ENABLED=${traffic_enabled}" \
    -e MYSQL_HOST=mysql -e MYSQL_PORT=3306 -e MYSQL_USER=root -e "MYSQL_PASSWORD=${mysql_password}" -e "MYSQL_DATABASE=${database}" \
    -e REDIS_ADDR=redis:6379 -e RABBITMQ_URL=amqp://guest:guest@rabbitmq:5672/ \
    -e "JWT_SECRET=${jwt_secret}" -e "REFRESH_TOKEN_SECRET=${refresh_secret}" -e "ID_CARD_HMAC_SECRET=${id_card_secret}" \
    -e "TOKEN_PROVIDER_KEY=${provider_key}" -e "API_KEY_HMAC_SECRET=${api_key_secret}" \
    -e "INTERNAL_API_TOKEN=${internal_token}" -e INTERNAL_ALLOWED_IPS=127.0.0.1/32 -e AI_OUTBOX_EXCHANGE=ai.billing -e ASSET_INTERNAL_BASE_URL=http://api:8080 \
    -e EMAIL_ADAPTER=mock -e "EMAIL_ADDRESS_HMAC_SECRET=${email_address_secret}" -e "EMAIL_IDEMPOTENCY_SECRET=${email_idempotency_secret}" \
    "${image}" >/dev/null
  docker network connect --alias api "${lb_network}" "${api_container}"
  for _ in $(seq 1 "${max_attempts}"); do docker exec "${api_container}" wget -q -O- http://127.0.0.1:8080/api/health >/dev/null 2>&1 && return 0; sleep 1; done
  docker logs --tail 60 "${api_container}" >&2 || true
  return 1
}

run_api "${baseline_image}" || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=baseline_not_ready"; exit 2; }
run_api "${candidate_image}" || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=candidate_not_ready"; exit 2; }

access_token="$(JWT_SECRET_VALUE="${jwt_secret}" python - <<'PY'
import base64, hashlib, hmac, json, os, time
encode = lambda value: base64.urlsafe_b64encode(value).rstrip(b'=').decode()
header = encode(json.dumps({'alg': 'HS256', 'typ': 'JWT'}, separators=(',', ':')).encode())
now = int(time.time())
payload = encode(json.dumps({'user_id': 9001, 'email': 'g8-rehearsal@example.invalid', 'iat': now, 'exp': now + 300}, separators=(',', ':')).encode())
message = f'{header}.{payload}'
signature = encode(hmac.new(os.environ['JWT_SECRET_VALUE'].encode(), message.encode(), hashlib.sha256).digest())
print(f'{message}.{signature}')
PY
)"

assert_app_gate_closed() {
  local stage="$1"
  local response status body
  response="$(docker run --rm --network "${lb_network}" curlimages/curl:8.12.1 -sS -w $'\n%{http_code}' -H "Authorization: Bearer ${access_token}" -H 'Content-Type: application/json' -d '{"model":"molin/closed","messages":[{"role":"user","content":"closed"}]}' http://api:8080/v1/chat/completions)"
  status="${response##*$'\n'}"
  body="${response%$'\n'*}"
  [[ "${status}" == "503" ]] && grep -q '50330' <<<"${body}" || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=${stage}_app_gate status=${status}"; exit 2; }
}

# 候选应用自身必须失败关闭，不能只依赖边缘保险丝掩盖应用总闸缺陷。
assert_app_gate_closed candidate

# 运行态证明 API 和模拟公网容器均无法解析或直连 Bifrost 节点，只有 LB 可访问节点健康与模型端口。
if docker exec "${api_container}" wget -q -T 2 -O- http://bifrost-1:8080/health >/dev/null 2>&1; then
  echo "G8_PRODUCTION_REHEARSAL=FAILED reason=api_can_bypass_bifrost_lb"
  exit 2
fi
if docker run --rm --network "${public_network}" curlimages/curl:8.12.1 -fsS --connect-timeout 2 http://bifrost-1:8080/health >/dev/null 2>&1; then
  echo "G8_PRODUCTION_REHEARSAL=FAILED reason=public_container_can_reach_bifrost_node"
  exit 2
fi

openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '//CN=g8-rehearsal.invalid' -keyout "${work_dir}/tls/key.pem" -out "${work_dir}/tls/cert.pem" >/dev/null 2>&1
cat > "${work_dir}/nginx.conf" <<'NGINX'
events { worker_connections 128; }
http {
  server {
    listen 443 ssl;
    server_name _;
    ssl_certificate /etc/nginx/tls/cert.pem;
    ssl_certificate_key /etc/nginx/tls/key.pem;
    client_max_body_size 1m;
    # 边缘保险丝独立于应用版本，旧版回滚期间仍阻断所有可触发文字上游调用的入口。
    location = /v1/chat/completions { default_type application/json; return 503 '{"code":50330,"message":"AI 网关商业流量暂未开放","error_type":"ai_gateway_traffic_closed","request_id":"$request_id"}'; }
    location = /api/token/chat/completions { default_type application/json; return 503 '{"code":50330,"message":"AI 网关商业流量暂未开放","error_type":"ai_gateway_traffic_closed","request_id":"$request_id"}'; }
    location ~ ^/api/agents/[^/]+/chat$ { default_type application/json; return 503 '{"code":50330,"message":"AI 网关商业流量暂未开放","error_type":"ai_gateway_traffic_closed","request_id":"$request_id"}'; }
    location ~ ^/api/conversations/[^/]+/chat$ { default_type application/json; return 503 '{"code":50330,"message":"AI 网关商业流量暂未开放","error_type":"ai_gateway_traffic_closed","request_id":"$request_id"}'; }
    location /v1/ { proxy_pass http://api:8080; proxy_http_version 1.1; proxy_buffering off; proxy_request_buffering off; proxy_connect_timeout 10s; proxy_send_timeout 300s; proxy_read_timeout 300s; }
    location /api/ { proxy_pass http://api:8080; proxy_http_version 1.1; proxy_buffering off; proxy_connect_timeout 10s; proxy_read_timeout 60s; }
    location = /api/internal/metrics { return 404; }
  }
}
NGINX
MSYS_NO_PATHCONV=1 docker create --pull="${pull_policy}" --network "${public_network}" -p 127.0.0.1::443 --name "${nginx_container}" --read-only --tmpfs /var/cache/nginx --tmpfs /var/run --tmpfs /tmp -v "${docker_work_dir}/nginx.conf:/etc/nginx/nginx.conf:ro" -v "${docker_work_dir}/tls:/etc/nginx/tls:ro" nginx:1.29.1-alpine >/dev/null
docker network connect "${lb_network}" "${nginx_container}"
docker start "${nginx_container}" >/dev/null
tls_address="$(docker port "${nginx_container}" 443/tcp | head -n1)"
for _ in $(seq 1 30); do curl -kfsS "https://${tls_address}/api/health" >/dev/null 2>&1 && break; sleep 1; done
curl -kfsS "https://${tls_address}/api/health" >/dev/null || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=tls_health"; exit 2; }
metrics_status="$(curl -ksS -o /dev/null -w '%{http_code}' "https://${tls_address}/api/internal/metrics")"

assert_edge_gate_closed() {
  local stage="$1"
  local index=0 path status response_file
  for path in /v1/chat/completions /api/token/chat/completions /api/agents/1/chat /api/conversations/1/chat; do
    index=$((index + 1))
    response_file="${work_dir}/${stage}-edge-${index}.json"
    status="$(curl -ksS -o "${response_file}" -w '%{http_code}' -H "Authorization: Bearer ${access_token}" -H 'Content-Type: application/json' -d '{}' "https://${tls_address}${path}")"
    [[ "${status}" == "503" ]] && grep -q '50330' "${response_file}" && grep -q 'ai_gateway_traffic_closed' "${response_file}" || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=${stage}_edge_gate path=${path} status=${status}"; exit 2; }
  done
}

[[ "${metrics_status}" == "404" ]] || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=metrics_public status=${metrics_status}"; exit 2; }
assert_edge_gate_closed candidate

assert_readiness_rejected() {
  local stage="$1"
  if run_api "${candidate_image}" true 15; then
    echo "G8_PRODUCTION_REHEARSAL=FAILED reason=${stage}_readiness_false_negative"
    exit 2
  fi
}

# 边缘保险丝保持关闭时开启候选应用总闸，真实执行 MySQL 8 发布事实门禁；有效数据必须能够启动。
run_api "${candidate_image}" true || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=valid_readiness_rejected"; exit 2; }
assert_edge_gate_closed readiness_valid

# 四类负向数据必须逐项阻止候选启动，避免 SQL 方言通过但生产失败关闭语义未被执行。
mysql_exec -e "ALTER TABLE ai_price_versions DROP CHECK chk_ai_price_exchange; UPDATE ai_price_versions SET exchange_rate=1.10000000 WHERE logical_model_code='g8/text-1';"
assert_readiness_rejected invalid_exchange_rate
mysql_exec -e "UPDATE ai_price_versions SET exchange_rate=1.00000000 WHERE logical_model_code='g8/text-1'; ALTER TABLE ai_price_versions ADD CONSTRAINT chk_ai_price_exchange CHECK (exchange_rate = 1);"

mysql_exec -e "INSERT INTO ai_price_skus(price_version_id,meter_type,variant_json,variant_hash,cost_unit_price,sale_unit_price,scale,currency) SELECT id,'input_tokens',JSON_OBJECT('tier','duplicate'),SHA2('g8-duplicate-meter',256),0.00000100,0.00000200,1000,'CNY' FROM ai_price_versions WHERE logical_model_code='g8/text-1';"
assert_readiness_rejected duplicate_meter
mysql_exec -e "DELETE FROM ai_price_skus WHERE variant_hash=SHA2('g8-duplicate-meter',256);"

mysql_exec -e "UPDATE token_channels SET last_health_check_at=UTC_TIMESTAMP()-INTERVAL 10 MINUTE WHERE id IN (9101,9102);"
assert_readiness_rejected stale_channel_health
mysql_exec -e "UPDATE token_channels SET last_health_check_at=UTC_TIMESTAMP() WHERE id IN (9101,9102);"

mysql_exec -e "UPDATE ai_model_route_runtime_states SET circuit_open_until=UTC_TIMESTAMP()+INTERVAL 10 MINUTE;"
assert_readiness_rejected circuit_open
mysql_exec -e "UPDATE ai_model_route_runtime_states SET circuit_open_until=NULL;"

# 负向门禁结束后恢复关闭态候选，继续验证旧版本回滚及财务事实保留。
run_api "${candidate_image}" || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=candidate_after_readiness_not_ready"; exit 2; }

# 回滚只切换应用制品，数据库和财务事实保持原位；边缘保险丝必须保护不认识应用总闸的旧版本。
run_api "${baseline_image}" || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=rollback_baseline_not_ready"; exit 2; }
assert_edge_gate_closed rollback
schema_after_rollback="$(mysql_exec -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='${database}';")"
[[ "${schema_after_rollback}" == "${schema_before}" ]] || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=rollback_schema_changed"; exit 2; }
run_api "${candidate_image}" || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=candidate_restore_not_ready"; exit 2; }
assert_app_gate_closed candidate_restore

log_config="$(docker inspect "${api_container}" --format '{{.HostConfig.LogConfig.Type}}:{{index .HostConfig.LogConfig.Config "max-size"}}:{{index .HostConfig.LogConfig.Config "max-file"}}')"
[[ "${log_config}" == "json-file:1m:2" ]] || { echo "G8_PRODUCTION_REHEARSAL=FAILED reason=log_rotation"; exit 2; }

echo "G8_PRODUCTION_REHEARSAL=PASS source_commit=$(git -C "${repo_root}" rev-parse HEAD) candidate_image_id=${candidate_image_id} baseline_image_id=${baseline_image_id} candidate_binary_sha256=${candidate_binary_sha} backup_sha256=${backup_sha} schema_tables=${schema_before} readiness_sql_mysql8=true readiness_valid=true readiness_invalid_exchange_rate=true readiness_duplicate_meter=true readiness_stale_health=true readiness_circuit_open=true production_shape_networks=5 api_node_direct=false public_node_direct=false lb_node_direct=true bifrost_nodes=2 bifrost_lb_auth=true bifrost_auth_header_stripped=true bifrost_upstream=fake tls=true sse_buffering=false request_body_limit=1m log_rotation=true metrics_public_status=404 candidate_app_gate=true edge_kill_switch_routes=4 edge_error_type=true authenticated_traffic_gate_status=503 authenticated_traffic_gate_code=50330 old_version_started=true rollback_edge_gate_routes=4 candidate_started=true rollback_started=true candidate_restored=true database_preserved=true production=false paid_upstream=false"
