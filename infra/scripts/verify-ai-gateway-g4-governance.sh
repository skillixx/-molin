#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${AI_GATEWAY_G4_ISOLATED_APPROVED:-NO}" != "YES" ]]; then
  echo "G4_VERIFY=APPROVAL_REQUIRED target=isolated_temporary_containers project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "G4_VERIFY=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "G4_VERIFY=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
g4_up="${repo_root}/server/migrations/000063_create_ai_gateway_g4_governance.up.sql"
g4_down="${repo_root}/server/migrations/000063_create_ai_gateway_g4_governance.down.sql"
test -f "${g4_up}" && test -f "${g4_down}" || { echo "G4_VERIFY=FAILED reason=migration_missing"; exit 2; }

suffix="${RANDOM}-$$"
network="molin-g4-net-${suffix}"
mysql_container="molin-g4-mysql-${suffix}"
redis_container="molin-g4-redis-${suffix}"
rabbit_container="molin-g4-rabbit-${suffix}"
database="molin_g4_contract"
mysql_password="$(openssl rand -hex 24)"
rabbit_password="$(openssl rand -hex 24)"
rabbit_cookie="$(openssl rand -hex 32)"
pull_policy="${G4_DOCKER_PULL_POLICY:-never}"
redis_image="${G4_REDIS_IMAGE:-redis:7}"

cleanup() {
  docker container rm -f "${rabbit_container}" >/dev/null 2>&1 || true
  docker container rm -f "${redis_container}" >/dev/null 2>&1 || true
  docker container rm -f "${mysql_container}" >/dev/null 2>&1 || true
  docker network rm "${network}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "G4_PREFLIGHT target=temporary_docker_mysql_redis_rabbit impact=isolated_only rollback=remove_containers_and_network"
docker network create "${network}" >/dev/null
docker run -d --pull="${pull_policy}" --network "${network}" --network-alias mysql --name "${mysql_container}" \
  --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g -e "MYSQL_ROOT_PASSWORD=${mysql_password}" -e "MYSQL_DATABASE=${database}" \
  mysql:8.0 --character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci >/dev/null
docker run -d --pull="${pull_policy}" --network "${network}" --network-alias redis --name "${redis_container}" \
  --tmpfs /data:rw,noexec,nosuid,size=128m "${redis_image}" redis-server --save '' --appendonly no >/dev/null
docker run -d --pull="${pull_policy}" --network "${network}" --network-alias rabbit --name "${rabbit_container}" \
  --tmpfs /var/lib/rabbitmq:rw,noexec,nosuid,size=256m -e RABBITMQ_DEFAULT_USER=g4test -e "RABBITMQ_DEFAULT_PASS=${rabbit_password}" \
  --entrypoint /bin/bash rabbitmq:3-management -c \
  "printf '%s' '${rabbit_cookie}' > /var/lib/rabbitmq/.erlang.cookie && chown rabbitmq:rabbitmq /var/lib/rabbitmq/.erlang.cookie && chmod 400 /var/lib/rabbitmq/.erlang.cookie && exec /usr/local/bin/docker-entrypoint.sh rabbitmq-server" >/dev/null

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" mysql --protocol=socket -uroot --database="${database}" --batch --skip-column-names "$@"
}

for _ in $(seq 1 60); do mysql_exec -e 'SELECT 1' >/dev/null 2>&1 && break; sleep 1; done
mysql_exec -e 'SELECT 1' >/dev/null 2>&1 || { echo "G4_VERIFY=FAILED reason=mysql_not_ready"; exit 2; }
for _ in $(seq 1 60); do docker exec "${redis_container}" redis-cli ping 2>/dev/null | grep -q PONG && break; sleep 1; done
docker exec "${redis_container}" redis-cli ping 2>/dev/null | grep -q PONG || { echo "G4_VERIFY=FAILED reason=redis_not_ready"; exit 2; }
for _ in $(seq 1 60); do docker exec "${rabbit_container}" rabbitmq-diagnostics -q check_running >/dev/null 2>&1 && break; sleep 1; done
docker exec "${rabbit_container}" rabbitmq-diagnostics -q check_running >/dev/null 2>&1 || { echo "G4_VERIFY=FAILED reason=rabbit_not_ready"; exit 2; }

# 从空库依次应用当前全部迁移，证明 G4 不依赖项目测试主库中的隐含数据。
for migration in "${repo_root}"/server/migrations/*.up.sql; do
  docker exec -i -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" mysql --protocol=socket -uroot --database="${database}" < "${migration}"
done
docker exec -i -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" mysql --protocol=socket -uroot --database="${database}" < "${g4_up}"
docker exec -i -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" mysql --protocol=socket -uroot --database="${database}" < "${g4_down}"
docker exec -i -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" mysql --protocol=socket -uroot --database="${database}" < "${g4_up}"

mysql_exec <<'SQL'
INSERT INTO users(id,email,password_hash,real_name_status,status) VALUES (1,'g4@example.invalid','test-only','verified','active');
INSERT INTO ai_projects(id,user_id,name,status,timezone) VALUES (1,1,'G4-Isolated','active','Asia/Shanghai');
INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status)
VALUES (1,1,1,'sk-g4-1','g4-hash-1','G4-1','postpaid','','all','active'),
       (2,1,1,'sk-g4-2','g4-hash-2','G4-2','postpaid','','all','active');
INSERT INTO users(id,email,password_hash,real_name_status,status) VALUES
  (16,'g4-cost-16@example.invalid','test-only','verified','active'),
  (17,'g4-cost-17@example.invalid','test-only','verified','active'),
  (18,'g4-cost-18@example.invalid','test-only','verified','active');
INSERT INTO token_models(id,logical_model_code,display_name,status,modality)
VALUES (101,'qwen-plus','G4 Cost Test','active','chat');
INSERT INTO ai_projects(id,user_id,name,status,timezone) VALUES
  (16,16,'G4-Cost-16','active','Asia/Shanghai'),
  (17,17,'G4-Cost-17','active','Asia/Shanghai'),
  (18,18,'G4-Cost-18','active','Asia/Shanghai');
INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status) VALUES
  (16,16,16,'sk-g4-16','g4-hash-16','G4-16','postpaid','','all','active'),
  (17,17,17,'sk-g4-17','g4-hash-17','G4-17','postpaid','','all','active'),
  (18,18,18,'sk-g4-18','g4-hash-18','G4-18','postpaid','','all','active');
INSERT INTO wallets(id,user_id,balance_amount,frozen_amount,currency) VALUES
  (16,16,1,0,'CNY'),(17,17,1,0,'CNY'),(18,18,1,0,'CNY');
INSERT INTO ai_price_versions(
  id,logical_model_code,version_no,currency,exchange_rate,status,min_margin_rate,
  max_input_tokens,max_output_tokens,failure_charge_policy,rounding_mode,
  cost_updated_at,cost_expires_at,effective_at,created_by,approved_by,approved_at,published_at
) VALUES(
  101,'qwen-plus',1,'CNY',1,'active',0.2,1000,100,'confirmed_usage','ceil_8',
  '2026-08-01 00:00:00','2030-01-01 00:00:00','2026-08-01 00:00:00',1,1,NOW(),NOW()
);
INSERT INTO ai_price_skus(price_version_id,meter_type,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES
  (101,'input_tokens',SHA2('g4-input',256),1,2,1000000,'CNY'),
  (101,'cached_tokens',SHA2('g4-cached',256),1,2,1000000,'CNY'),
  (101,'output_tokens',SHA2('g4-output',256),1,2,1000000,'CNY'),
  (101,'reasoning_tokens',SHA2('g4-reasoning',256),1,2,1000000,'CNY');
SQL

assert_scalar() {
  local sql="$1" expected="$2" label="$3" actual
  actual="$(mysql_exec -e "${sql}")"
  [[ "${actual}" == "${expected}" ]] || { echo "G4_VERIFY=FAILED reason=${label} expected=${expected} actual=${actual}"; exit 2; }
}
assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('ai_safety_policy_versions','ai_safety_events','ai_safety_subject_actions','ai_safety_appeals','ai_resource_policies','ai_budget_policies','ai_budget_overrides','ai_budget_reservations','ai_budget_alerts','ai_compensation_tasks')" "10" "g4_table_count"
assert_scalar "SELECT COUNT(*) FROM information_schema.check_constraints WHERE constraint_schema=DATABASE() AND constraint_name='chk_ai_usage_source' AND check_clause LIKE '%provider_cost%'" "1" "provider_cost_source_constraint"

# 在 000063 约束和最小 G4 fixture 下只运行内容审核财务子用例。
docker run --rm --network "${network}" -v "${repo_root}:/src:ro" -v molin-g4-go-mod-cache:/go/pkg/mod -v molin-g4-go-build-cache:/root/.cache/go-build \
  -w /src/server -e GOPROXY=https://goproxy.cn,direct -e G3_ISOLATED_TEST=YES \
  -e "G3_MYSQL_DSN=root:${mysql_password}@tcp(mysql:3306)/${database}?parseTime=true&charset=utf8mb4" \
  golang:1.25 go test -count=1 ./internal/modules/token_gateway/service -run '^TestG3MySQLBillingIntegration/输出审核'

docker run --rm --network "${network}" -v "${repo_root}:/src:ro" -v molin-g4-go-mod-cache:/go/pkg/mod -v molin-g4-go-build-cache:/root/.cache/go-build \
  -w /src/server -e GOPROXY=https://goproxy.cn,direct -e G4_ISOLATED_TEST=YES \
  -e "G4_MYSQL_DSN=root:${mysql_password}@tcp(mysql:3306)/${database}?parseTime=true&charset=utf8mb4" -e G4_REDIS_ADDR=redis:6379 \
  golang:1.25 go test -count=1 ./internal/modules/token_gateway/service -run '^TestG4(MySQLBudget|RedisResource)Integration$'

# 真实停止 Redis 后执行 fail-closed，再重启并重新跑资源测试，证明恢复后无幽灵租约。
docker stop "${redis_container}" >/dev/null
docker run --rm --network "${network}" -v "${repo_root}:/src:ro" -v molin-g4-go-mod-cache:/go/pkg/mod -v molin-g4-go-build-cache:/root/.cache/go-build \
  -w /src/server -e GOPROXY=https://goproxy.cn,direct -e G4_ISOLATED_TEST=YES -e G4_REDIS_DOWN_ADDR=redis:6379 \
  golang:1.25 go test -count=1 ./internal/modules/token_gateway/service -run '^TestG4RedisUnavailableIntegration$'
docker start "${redis_container}" >/dev/null
for _ in $(seq 1 60); do docker exec "${redis_container}" redis-cli ping 2>/dev/null | grep -q PONG && break; sleep 1; done
docker run --rm --network "${network}" -v "${repo_root}:/src:ro" -v molin-g4-go-mod-cache:/go/pkg/mod -v molin-g4-go-build-cache:/root/.cache/go-build \
  -w /src/server -e GOPROXY=https://goproxy.cn,direct -e G4_ISOLATED_TEST=YES -e G4_REDIS_ADDR=redis:6379 \
  golang:1.25 go test -count=1 ./internal/modules/token_gateway/service -run '^TestG4RedisResourceIntegration$'

# RabbitMQ 停止和恢复只验证基础设施；G3 Outbox 的保留与确认发布由 G3 全量回归脚本继续覆盖。
docker stop "${rabbit_container}" >/dev/null
docker start "${rabbit_container}" >/dev/null
for _ in $(seq 1 180); do docker exec "${rabbit_container}" rabbitmq-diagnostics -q check_running >/dev/null 2>&1 && break; sleep 1; done
docker exec "${rabbit_container}" rabbitmq-diagnostics -q check_running >/dev/null 2>&1 || {
  echo "G4_VERIFY=FAILED reason=rabbit_recovery_failed"
  docker inspect -f 'status={{.State.Status}} exit={{.State.ExitCode}} error={{.State.Error}}' "${rabbit_container}" || true
  docker logs --tail 120 "${rabbit_container}" 2>&1 || true
  exit 2
}

# 只统计 100 并发 hard 场景，避免把后续 soft 和补偿故障用例的 held 事实混入超卖断言。
assert_scalar "SELECT COUNT(*) FROM ai_budget_reservations WHERE request_id LIKE 'g4-budget-___' AND status IN ('held','released')" "10" "hard_budget_no_oversell"
# API Key 跨周期用例也会生成独立阈值事实；这里只验证 Project 1 的 80/90/100 各一次。
assert_scalar "SELECT COUNT(*) FROM ai_budget_alerts WHERE scope_type='project' AND scope_id=1 AND threshold_percent IN (80,90,100)" "3" "threshold_idempotency"
assert_scalar "SELECT COUNT(*) FROM token_usage_logs" "0" "released_request_has_no_settled_usage_log"

echo "G4_VERIFY=PASS isolated=true migrations=up_repeated_down_reup_preserved redis_nodes=8 concurrency=100/20 redis_down_fail_closed=true redis_recovered=true budget_multi_sk=true thresholds=80_90_100 rabbit_recovered=true project_database=false"
