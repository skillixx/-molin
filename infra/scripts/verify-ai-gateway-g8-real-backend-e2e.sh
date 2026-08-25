#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${AI_GATEWAY_G8_ISOLATED_APPROVED:-NO}" != "YES" ]]; then
  echo "G8_REAL_E2E=APPROVAL_REQUIRED target=isolated_temporary_stack paid_upstream=false project_database=false"
  exit 3
fi

for command_name in docker openssl curl python; do
  command -v "${command_name}" >/dev/null 2>&1 || { echo "G8_REAL_E2E=FAILED reason=${command_name}_missing"; exit 2; }
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
docker_repo_root="${repo_root}"
npm_command="npm"
if [[ "${OSTYPE:-}" == msys* || "${OSTYPE:-}" == cygwin* ]]; then
  docker_repo_root="$(cygpath -w "${repo_root}")"
  npm_command="npm.cmd"
  export MSYS_NO_PATHCONV=1
  export MSYS2_ARG_CONV_EXCL='*'
fi

suffix="${RANDOM}-$$"
network="molin-g8-real-net-${suffix}"
mysql_container="molin-g8-real-mysql-${suffix}"
redis_container="molin-g8-real-redis-${suffix}"
rabbit_container="molin-g8-real-rabbit-${suffix}"
minio_container="molin-g8-real-minio-${suffix}"
upstream_container="molin-g8-real-upstream-${suffix}"
api_container="molin-g8-real-api-${suffix}"
image_secret_volume="molin-g8-image-secrets-${suffix}"
database="molin_g8_real_${suffix//-/_}"
mysql_password="$(openssl rand -hex 24)"
jwt_secret="$(openssl rand -hex 32)"
refresh_secret="$(openssl rand -hex 32)"
id_card_secret="$(openssl rand -hex 32)"
provider_key="$(openssl rand -hex 16)"
api_key_secret="$(openssl rand -hex 32)"
internal_token="$(openssl rand -hex 32)"
email_address_secret="$(openssl rand -hex 32)"
email_idempotency_secret="$(openssl rand -hex 32)"
image_quote_secret="$(openssl rand -hex 32)"
image_prompt_secret="$(openssl rand -hex 32)"
minio_access="g8fakeaccess"
minio_secret="$(openssl rand -hex 24)"
pull_policy="${G8_DOCKER_PULL_POLICY:-missing}"

cleanup() {
  docker container rm -f "${api_container}" "${upstream_container}" "${minio_container}" "${rabbit_container}" "${redis_container}" "${mysql_container}" >/dev/null 2>&1 || true
  docker volume rm "${image_secret_volume}" >/dev/null 2>&1 || true
  docker network rm "${network}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "G8_REAL_E2E_PREFLIGHT target=temporary_mysql_redis_api_fake_upstream impact=isolated_only rollback=remove_containers_and_network paid_upstream=false real_customer=false"
docker network create "${network}" >/dev/null
docker run -d --pull="${pull_policy}" --network "${network}" --network-alias mysql --name "${mysql_container}" \
  --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g \
  -e "MYSQL_ROOT_PASSWORD=${mysql_password}" -e "MYSQL_DATABASE=${database}" \
  mysql:8.0 --character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci >/dev/null
docker run -d --pull="${pull_policy}" --network "${network}" --network-alias redis --name "${redis_container}" \
  --tmpfs /data:rw,noexec,nosuid,size=128m redis:7 redis-server --save '' --appendonly no >/dev/null
docker run -d --pull="${pull_policy}" --network "${network}" --network-alias rabbitmq --name "${rabbit_container}" \
  --tmpfs /var/lib/rabbitmq:rw,noexec,nosuid,size=256m rabbitmq:3-management-alpine >/dev/null
docker run -d --pull="${pull_policy}" --network "${network}" --network-alias minio --name "${minio_container}" \
  -p 127.0.0.1::9000 \
  --tmpfs /data:rw,noexec,nosuid,size=1g -e "MINIO_ROOT_USER=${minio_access}" -e "MINIO_ROOT_PASSWORD=${minio_secret}" \
  minio/minio@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e server /data --console-address :9001 >/dev/null
docker run -d --pull="${pull_policy}" --network "${network}" --network-alias g8-fake-upstream --name "${upstream_container}" \
  -v "${docker_repo_root}:/workspace:ro" -w /workspace python:3.12-alpine python infra/scripts/g8_fake_text_upstream.py >/dev/null

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" \
    mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database="${database}" --batch --skip-column-names "$@"
}

for _ in $(seq 1 90); do
  docker exec "${mysql_container}" sh -c 'test "$(cat /proc/1/comm)" = mysqld' >/dev/null 2>&1 && mysql_exec -e 'SELECT 1' >/dev/null 2>&1 && break
  sleep 1
done
mysql_exec -e 'SELECT 1' >/dev/null 2>&1 || { echo "G8_REAL_E2E=FAILED reason=mysql_not_ready"; exit 2; }
for _ in $(seq 1 60); do docker exec "${redis_container}" redis-cli ping 2>/dev/null | grep -q PONG && break; sleep 1; done
docker exec "${redis_container}" redis-cli ping 2>/dev/null | grep -q PONG || { echo "G8_REAL_E2E=FAILED reason=redis_not_ready"; exit 2; }
for _ in $(seq 1 90); do docker exec "${rabbit_container}" rabbitmq-diagnostics -q ping >/dev/null 2>&1 && break; sleep 1; done
docker exec "${rabbit_container}" rabbitmq-diagnostics -q ping >/dev/null 2>&1 || { echo "G8_REAL_E2E=FAILED reason=rabbitmq_not_ready"; exit 2; }
for _ in $(seq 1 90); do docker logs "${minio_container}" 2>&1 | grep -q 'API:' && break; sleep 1; done
docker run --rm --pull="${pull_policy}" --network "${network}" --entrypoint /bin/sh minio/mc@sha256:a7fe349ef4bd8521fb8497f55c6042871b2ae640607cf99d9bede5e9bdf11727 -c \
  "mc alias set local http://minio:9000 '${minio_access}' '${minio_secret}' >/dev/null && mc mb local/ai-upload-temp local/ai-result local/ai-quarantine >/dev/null"
minio_address="$(docker port "${minio_container}" 9000/tcp 2>/dev/null | head -n1 || true)"
[[ -n "${minio_address}" ]] || { echo "G8_REAL_E2E=FAILED reason=minio_public_port_missing"; exit 2; }

# 四份Fake Secret只存在于本次临时Docker volume，权限为0600，容器退出后删除。
docker volume create "${image_secret_volume}" >/dev/null
docker run --rm -v "${image_secret_volume}:/secrets" alpine:3.21 sh -c \
  "umask 077; printf '%s' '${image_quote_secret}' > /secrets/quote; printf '%s' '${image_prompt_secret}' > /secrets/prompt; printf '%s' '${minio_access}' > /secrets/minio-access; printf '%s' '${minio_secret}' > /secrets/minio-secret"

for migration in "${repo_root}"/server/migrations/*.up.sql; do
  docker exec -i -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" \
    mysql --protocol=socket -uroot --database="${database}" < "${migration}"
done

# 只写入一次性隔离身份和钱包；邮箱使用保留域，密码字段不可用于登录，浏览器令牌由运行时随机密钥签发。
mysql_exec <<'SQL'
INSERT INTO users(id,username,email,email_verified,phone,phone_verified,password_hash,real_name_status,status,admin_phone_verified_at,admin_email_verified_at)
VALUES(9001,'g8_browser','g8-browser@example.invalid',1,CONCAT('139','0000','9001'),1,'disabled-login','verified','active',UTC_TIMESTAMP(),UTC_TIMESTAMP());
INSERT INTO wallets(id,user_id,balance_amount,frozen_amount,currency,version) VALUES(9001,9001,100.00000000,0,'CNY',0);
UPDATE users SET wallet_id=9001 WHERE id=9001;
INSERT INTO user_roles(user_id,role_id) SELECT 9001,id FROM roles WHERE code='admin';
INSERT INTO ai_projects(id,user_id,name,status,budget_mode,monthly_budget,timezone)
VALUES(88001,9001,'G8 图片隔离项目','active','disabled',NULL,'Asia/Shanghai');
INSERT INTO token_models(id,logical_model_code,display_name,provider_name,description,capabilities_json,context_window,modality,status,visible_scope,release_version_no,published_at)
VALUES(88001,'molin/g8-image','G8 隔离图片模型','隔离 Fake 图片上游','仅用于 IMG-G8 本地真实后端浏览器验收。',JSON_ARRAY('image.generate'),0,'image','active','all',1,UTC_TIMESTAMP());
INSERT INTO ai_model_release_versions(model_id,version_no,status,snapshot_json,reason,created_by,published_at)
VALUES(88001,1,'active',JSON_OBJECT('logical_model_code','molin/g8-image','display_name','G8 隔离图片模型','provider_name','隔离 Fake 图片上游','description','仅用于 IMG-G8 本地真实后端浏览器验收。','capabilities',JSON_ARRAY('image.generate'),'context_window',0,'intro_url_health_status','unpublished','docs_url_health_status','unpublished','quick_start_url_health_status','unpublished','modality','image','visible_scope','all'),'IMG-G8隔离测试夹具',9001,UTC_TIMESTAMP());
INSERT INTO ai_price_versions(id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by,published_at)
VALUES(88001,'molin/g8-image','image.generate','image_variant',1,'CNY',1,'active',0.2,NULL,NULL,JSON_OBJECT('max_count',1,'variants',JSON_ARRAY(JSON_OBJECT('resolution','2K','aspect_ratio','1:1','quality','standard','output_format','provider_default','delivery','url'))),0.01,'test_fixture','img-g8-browser','test_fixture','confirmed_usage','ceil_8',UTC_TIMESTAMP(),DATE_ADD(UTC_TIMESTAMP(),INTERVAL 1 DAY),DATE_SUB(UTC_TIMESTAMP(),INTERVAL 1 MINUTE),9001,UTC_TIMESTAMP());
SET @image_variant=JSON_OBJECT('resolution','2K','aspect_ratio','1:1','quality','standard','output_format','provider_default','delivery','url');
INSERT INTO ai_price_skus(price_version_id,meter_type,variant_json,variant_hash,cost_unit_price,sale_unit_price,scale,currency)
VALUES(88001,'image_count',@image_variant,'7080954fe5f6ffae205a84da3a59dc244f498524497f990ab8f0b89865ef03ea',0.3,0.5,1,'CNY');
SQL

docker run -d --pull="${pull_policy}" --network "${network}" --network-alias api --name "${api_container}" \
  -p 127.0.0.1::8080 -v "${docker_repo_root}:/workspace" -v "${image_secret_volume}:/run/molin-image:ro" -w /workspace/server \
  -e APP_ENV=test -e API_HOST=0.0.0.0 -e API_PORT=8080 \
  -e MYSQL_HOST=mysql -e MYSQL_PORT=3306 -e MYSQL_USER=root -e "MYSQL_PASSWORD=${mysql_password}" -e "MYSQL_DATABASE=${database}" \
  -e REDIS_ADDR=redis:6379 -e RABBITMQ_URL=amqp://guest:guest@rabbitmq:5672/ -e AI_OUTBOX_EXCHANGE=molin.ai.g8.real \
  -e "JWT_SECRET=${jwt_secret}" -e "REFRESH_TOKEN_SECRET=${refresh_secret}" \
  -e "ID_CARD_HMAC_SECRET=${id_card_secret}" -e "TOKEN_PROVIDER_KEY=${provider_key}" -e "API_KEY_HMAC_SECRET=${api_key_secret}" \
  -e "INTERNAL_API_TOKEN=${internal_token}" -e INTERNAL_ALLOWED_IPS=127.0.0.1/32 \
  -e EMAIL_ADAPTER=mock -e "EMAIL_ADDRESS_HMAC_SECRET=${email_address_secret}" -e "EMAIL_IDEMPOTENCY_SECRET=${email_idempotency_secret}" \
  -e TOKEN_EXECUTION_DRIVER=native -e AI_GATEWAY_TRAFFIC_ENABLED=true \
  -e IMAGE_GATEWAY_ENABLED=true -e IMAGE_GATEWAY_TRAFFIC_ENABLED=true -e IMAGE_GATEWAY_LOCAL_FAKE_TEST=true \
  -e IMAGE_GATEWAY_PROVIDER=fake -e IMAGE_GATEWAY_OPENROUTER_ENABLED=false \
  -e IMAGE_GATEWAY_QUOTE_SECRET_FILE=/run/molin-image/quote -e IMAGE_GATEWAY_PROMPT_SECRET_FILE=/run/molin-image/prompt \
  -e IMAGE_GATEWAY_MINIO_ENDPOINT=minio:9000 -e IMAGE_GATEWAY_MINIO_USE_SSL=false \
  -e "IMAGE_GATEWAY_MINIO_PUBLIC_DOWNLOAD_ENDPOINT=http://${minio_address}" \
  -e IMAGE_GATEWAY_MINIO_ACCESS_KEY_FILE=/run/molin-image/minio-access -e IMAGE_GATEWAY_MINIO_SECRET_KEY_FILE=/run/molin-image/minio-secret \
  -e AI_GATEWAY_HEALTH_INTERNAL_ALLOWLIST=g8-fake-upstream:8000 -e ASSET_INTERNAL_BASE_URL=http://api:8080 \
  golang:1.25-bookworm go run ./cmd/api >/dev/null

api_address=""
for _ in $(seq 1 120); do
  api_address="$(docker port "${api_container}" 8080/tcp 2>/dev/null | head -n1 || true)"
  if [[ -n "${api_address}" ]] && curl --silent --fail "http://${api_address}/api/health" >/dev/null 2>&1; then break; fi
  sleep 1
done
if [[ -z "${api_address}" ]] || ! curl --silent --fail "http://${api_address}/api/health" >/dev/null 2>&1; then
  docker logs --tail 80 "${api_container}" 2>&1 || true
  echo "G8_REAL_E2E=FAILED reason=api_not_ready"
  exit 2
fi

export G8_REAL_API_URL="http://${api_address}"
export G8_REAL_JWT_SECRET="${jwt_secret}"
export G8_REAL_USER_ID=9001
export VITE_API_PROXY_TARGET="${G8_REAL_API_URL}"

(cd "${repo_root}/web/admin-console" && "${npm_command}" run test:g8-real-e2e)
(cd "${repo_root}/web/user-console" && "${npm_command}" run test:g8-real-e2e)

for _ in $(seq 1 20); do
  pending_outbox="$(mysql_exec -e "SELECT COUNT(*) FROM ai_outbox_events WHERE status<>'published';")"
  [[ "${pending_outbox}" == "0" ]] && break
  sleep 1
done
[[ "${pending_outbox}" == "0" ]] || { echo "G8_REAL_E2E=FAILED reason=outbox_not_converged"; exit 2; }

set +e
reconciliation_json="$(docker exec -e AI_GATEWAY_RECONCILE_READ_ONLY=YES "${api_container}" go run ./cmd/ai-gateway-reconcile --format json)"
reconciliation_exit=$?
set -e
if [[ "${reconciliation_exit}" != "0" ]]; then
  printf '%s' "${reconciliation_json}" | python -c 'import json,sys; r=json.load(sys.stdin); print("G8_REAL_E2E_RECONCILIATION_DIAGNOSTIC", json.dumps({"status":r.get("status"),"differences_cny":r.get("differences_cny"),"anomalies":r.get("anomalies"),"unreleased_holds":r.get("unreleased_holds"),"outbox_backlog":r.get("outbox_backlog"),"compensation_backlog":r.get("compensation_backlog"),"issue_count":r.get("issue_count"),"issues":r.get("issues")}, ensure_ascii=False, separators=(",",":")))'
  echo "G8_REAL_E2E_USAGE_DIAGNOSTIC_BEGIN"
  mysql_exec -e "SELECT source,sequence_no,meter_type,CAST(quantity AS CHAR),COALESCE(CAST(unit_price AS CHAR),'NULL'),COALESCE(CAST(amount AS CHAR),'NULL') FROM ai_usage_items ORDER BY source,sequence_no,meter_type;"
  echo "G8_REAL_E2E_USAGE_DIAGNOSTIC_END"
  echo "G8_REAL_E2E_USAGE_FACT_DIAGNOSTIC_BEGIN"
  mysql_exec -e "SELECT r.request_id,r.execution_status,r.billing_status,JSON_UNQUOTE(JSON_EXTRACT(r.price_snapshot_json,'$.minimum_charge')) AS minimum_charge,(SELECT COUNT(*) FROM ai_usage_items u WHERE u.request_id=r.request_id AND u.source='provider' AND u.sequence_no=1) AS sale_rows,(SELECT COUNT(DISTINCT u.meter_type) FROM ai_usage_items u WHERE u.request_id=r.request_id AND u.source='provider' AND u.sequence_no=1) AS sale_meters,(SELECT SUM(u.amount) FROM ai_usage_items u WHERE u.request_id=r.request_id AND u.source='provider' AND u.sequence_no=1) AS sale_amount,(SELECT SUM(u.quantity) FROM ai_usage_items u WHERE u.request_id=r.request_id AND u.source='provider' AND u.sequence_no=1 AND u.meter_type IN ('input_tokens','cached_tokens')) AS sale_input,(SELECT SUM(u.quantity) FROM ai_usage_items u WHERE u.request_id=r.request_id AND u.source='provider' AND u.sequence_no=1 AND u.meter_type IN ('output_tokens','reasoning_tokens')) AS sale_output,(SELECT SUM(u.quantity) FROM ai_usage_items u WHERE u.request_id=r.request_id AND u.source='provider' AND u.sequence_no=0 AND u.meter_type='input_tokens') AS raw_input,(SELECT SUM(u.quantity) FROM ai_usage_items u WHERE u.request_id=r.request_id AND u.source='provider' AND u.sequence_no=0 AND u.meter_type='output_tokens') AS raw_output FROM ai_requests r ORDER BY r.id;"
  mysql_exec -e "SELECT r.request_id,(SELECT SUM(u.quantity<0 OR u.unit_price IS NULL OR u.unit_price<=0 OR u.amount IS NULL OR u.amount<0) FROM ai_usage_items u WHERE u.request_id=r.request_id AND u.source='provider' AND u.sequence_no=1) AS incomplete_count,(SELECT SUM(u.meter_type<>'output_tokens' AND NOT(u.amount <=> CEIL(u.quantity*u.unit_price/CAST(JSON_UNQUOTE(JSON_EXTRACT(r.price_snapshot_json,CONCAT('$.skus.',u.meter_type,'.scale'))) AS DECIMAL(30,10))*100000000)/100000000)) FROM ai_usage_items u WHERE u.request_id=r.request_id AND u.source='provider' AND u.sequence_no=1) AS non_output_amount_mismatch,(SELECT SUM(NOT(u.unit_price <=> CAST(JSON_UNQUOTE(JSON_EXTRACT(r.price_snapshot_json,CONCAT('$.skus.',u.meter_type,'.sale_unit_price'))) AS DECIMAL(20,8)))) FROM ai_usage_items u WHERE u.request_id=r.request_id AND u.source='provider' AND u.sequence_no=1) AS unit_price_mismatch,(SELECT SUM(CEIL(u.quantity*u.unit_price/CAST(JSON_UNQUOTE(JSON_EXTRACT(r.price_snapshot_json,CONCAT('$.skus.',u.meter_type,'.scale'))) AS DECIMAL(30,10))*100000000)/100000000) FROM ai_usage_items u WHERE u.request_id=r.request_id AND u.source='provider' AND u.sequence_no=1) AS computed_base FROM ai_requests r ORDER BY r.id;"
  echo "G8_REAL_E2E_USAGE_FACT_DIAGNOSTIC_END"
  echo "G8_REAL_E2E=FAILED reason=reconciliation_nonzero"
  exit 2
fi
grep -q '"status": "PASS"' <<<"${reconciliation_json}" || { echo "G8_REAL_E2E=FAILED reason=reconciliation_status"; exit 2; }
for difference in request_usage request_hold request_wallet; do
  grep -q "\"${difference}\": \"0.00000000\"" <<<"${reconciliation_json}" || { echo "G8_REAL_E2E=FAILED reason=${difference}"; exit 2; }
done
echo "G8_REAL_E2E=PASS browser_api_mock=false admin_publish=true project_sk=true fake_text_call=1 fake_image_runtime=true image_quote_task_asset_billing=true signed_image_get=true image_mime_png=true usage_billing_dispute=true viewports=1440,768,390,375 paid_upstream=false real_customer=false request_usage_difference=0 request_hold_difference=0 request_wallet_difference=0 outbox_backlog=0 project_database=false"
