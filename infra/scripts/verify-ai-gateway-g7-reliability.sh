#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${AI_GATEWAY_G7_ISOLATED_APPROVED:-NO}" != "YES" ]]; then
  echo "G7_VERIFY=APPROVAL_REQUIRED target=isolated_temporary_mysql_redis project_database=false paid_upstream=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "G7_VERIFY=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "G7_VERIFY=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
suffix="${RANDOM}-$$"
network="molin-g7-net-${suffix}"
mysql_container="molin-g7-mysql-${suffix}"
redis_container="molin-g7-redis-${suffix}"
database="molin_g7_reliability"
mysql_password="$(openssl rand -hex 24)"
pull_policy="${G7_DOCKER_PULL_POLICY:-never}"

cleanup() {
  docker container rm -f "${redis_container}" >/dev/null 2>&1 || true
  docker container rm -f "${mysql_container}" >/dev/null 2>&1 || true
  docker network rm "${network}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "G7_PREFLIGHT target=temporary_docker_mysql_redis impact=isolated_only rollback=remove_containers_and_network paid_upstream=false"
docker network create "${network}" >/dev/null
docker run -d --pull="${pull_policy}" --network "${network}" --network-alias mysql --name "${mysql_container}" \
  --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g \
  -e "MYSQL_ROOT_PASSWORD=${mysql_password}" -e "MYSQL_DATABASE=${database}" \
  mysql:8.0 --character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci >/dev/null
docker run -d --pull="${pull_policy}" --network "${network}" --network-alias redis --name "${redis_container}" \
  --tmpfs /data:rw,noexec,nosuid,size=128m \
  redis:7 redis-server --save '' --appendonly no >/dev/null

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" \
    mysql --protocol=socket -uroot --database="${database}" --batch --skip-column-names "$@"
}

for _ in $(seq 1 60); do mysql_exec -e 'SELECT 1' >/dev/null 2>&1 && break; sleep 1; done
mysql_exec -e 'SELECT 1' >/dev/null 2>&1 || { echo "G7_VERIFY=FAILED reason=mysql_not_ready"; exit 2; }
for _ in $(seq 1 60); do docker exec "${redis_container}" redis-cli ping 2>/dev/null | grep -q PONG && break; sleep 1; done
docker exec "${redis_container}" redis-cli ping 2>/dev/null | grep -q PONG || { echo "G7_VERIFY=FAILED reason=redis_not_ready"; exit 2; }

# 从空库按文件名顺序应用当前全部扩展迁移，禁止依赖项目测试库中的历史夹具。
for migration in "${repo_root}"/server/migrations/*.up.sql; do
  docker exec -i -e "MYSQL_PWD=${mysql_password}" "${mysql_container}" \
    mysql --protocol=socket -uroot --database="${database}" < "${migration}"
done

# 103 个测试租户分别持有独立钱包；前 100 个用于并发负载，后三个用于幂等、断连和混沌恢复。
# 所有身份、密钥摘要、模型和金额均为隔离测试夹具，不包含真实客户或上游凭据。
mysql_exec <<'SQL'
INSERT INTO users(id,email,password_hash,real_name_status,status)
WITH RECURSIVE seq AS (SELECT 701 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 803)
SELECT n, CONCAT('g7-',n,'@example.invalid'), 'test-only', 'verified', 'active' FROM seq;
INSERT INTO token_models(id,logical_model_code,display_name,status,modality)
VALUES (701,'qwen-plus','G7 Fake','active','chat');
INSERT INTO ai_projects(id,user_id,name,status,timezone)
WITH RECURSIVE seq AS (SELECT 701 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 803)
SELECT n,n,CONCAT('G7-',n),'active','Asia/Shanghai' FROM seq;
INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status)
WITH RECURSIVE seq AS (SELECT 701 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 803)
SELECT n,n,n,CONCAT('sk-g7-',n),CONCAT('g7-test-hash-',n),CONCAT('G7-',n),'postpaid','','all','active' FROM seq;
INSERT INTO wallets(id,user_id,balance_amount,frozen_amount,currency)
WITH RECURSIVE seq AS (SELECT 701 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 803)
SELECT n,n,10,0,'CNY' FROM seq;
INSERT INTO ai_price_versions(
  id,logical_model_code,version_no,currency,exchange_rate,status,min_margin_rate,
  max_input_tokens,max_output_tokens,failure_charge_policy,rounding_mode,
  cost_updated_at,cost_expires_at,effective_at,created_by,approved_by,approved_at,published_at
) VALUES(
  701,'qwen-plus',1,'CNY',1,'active',0.2,1000,100,'confirmed_usage','ceil_8',
  '2026-08-01 00:00:00','2030-01-01 00:00:00','2026-08-01 00:00:00',701,701,NOW(),NOW()
);
INSERT INTO ai_price_skus(price_version_id,meter_type,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES
  (701,'input_tokens',SHA2('g7-input',256),1,2,1000000,'CNY'),
  (701,'cached_tokens',SHA2('g7-cached',256),1,2,1000000,'CNY'),
  (701,'output_tokens',SHA2('g7-output',256),1,2,1000000,'CNY'),
  (701,'reasoning_tokens',SHA2('g7-reasoning',256),1,2,1000000,'CNY');
SQL

go_test() {
  docker run --rm --network "${network}" \
    -v "${repo_root}:/src:ro" -v molin-g7-go-mod-cache:/go/pkg/mod -v molin-g7-go-build-cache:/root/.cache/go-build \
    -w /src/server -e GOPROXY=https://goproxy.cn,direct "$@" golang:1.25 \
    go test -count=1 ./internal/modules/token_gateway/service
}

go_test -e G7_ISOLATED_TEST=YES \
  -e "G7_MYSQL_DSN=root:${mysql_password}@tcp(mysql:3306)/${database}?parseTime=true&charset=utf8mb4" \
  -run '^TestG7MySQLReliabilityIntegration$'

go_test -e G4_ISOLATED_TEST=YES -e G4_REDIS_ADDR=redis:6379 \
  -run '^TestG4RedisResourceIntegration$'

# 真实停止隔离 Redis，验证失败关闭；恢复后重跑并发治理，证明幽灵租约不会阻断服务。
docker stop "${redis_container}" >/dev/null
go_test -e G4_ISOLATED_TEST=YES -e G4_REDIS_DOWN_ADDR=redis:6379 \
  -run '^TestG4RedisUnavailableIntegration$'
docker start "${redis_container}" >/dev/null
for _ in $(seq 1 60); do docker exec "${redis_container}" redis-cli ping 2>/dev/null | grep -q PONG && break; sleep 1; done
docker exec "${redis_container}" redis-cli ping 2>/dev/null | grep -q PONG || { echo "G7_VERIFY=FAILED reason=redis_recovery_failed"; exit 2; }
go_test -e G4_ISOLATED_TEST=YES -e G4_REDIS_ADDR=redis:6379 \
  -run '^TestG4RedisResourceIntegration$'

# 使用正式 CLI 在 MySQL READ ONLY 事务中再次核对，命令无修账、退款、补扣或释放预占能力。
reconcile_json="$(docker run --rm --network "${network}" \
  -v "${repo_root}:/src:ro" -v molin-g7-go-mod-cache:/go/pkg/mod -v molin-g7-go-build-cache:/root/.cache/go-build \
  -w /src/server -e GOPROXY=https://goproxy.cn,direct \
  -e APP_ENV=test -e AI_GATEWAY_RECONCILE_READ_ONLY=YES \
  -e MYSQL_HOST=mysql -e MYSQL_PORT=3306 -e MYSQL_USER=root -e "MYSQL_PASSWORD=${mysql_password}" -e "MYSQL_DATABASE=${database}" \
  golang:1.25 go run ./cmd/ai-gateway-reconcile --format json)"
grep -q '"status": "PASS"' <<<"${reconcile_json}" || { echo "G7_VERIFY=FAILED reason=reconciliation_status"; exit 2; }
grep -q '"has_mismatch": false' <<<"${reconcile_json}" || { echo "G7_VERIFY=FAILED reason=reconciliation_mismatch"; exit 2; }

assert_scalar() {
  local sql="$1" expected="$2" label="$3" actual
  actual="$(mysql_exec -e "${sql}")"
  [[ "${actual}" == "${expected}" ]] || { echo "G7_VERIFY=FAILED reason=${label} expected=${expected} actual=${actual}"; exit 2; }
}

assert_scalar "SELECT COUNT(*) FROM ai_requests WHERE request_id LIKE 'g7-load-%' AND billing_status='settled'" "100" "settled_load_requests"
assert_scalar "SELECT COUNT(*) FROM ai_requests WHERE request_id LIKE 'g7-%' AND price_snapshot_json IS NULL" "0" "price_snapshot_missing"
assert_scalar "SELECT COUNT(*) FROM wallet_holds WHERE user_id BETWEEN 701 AND 803 AND status='holding'" "0" "unreleased_holds"
assert_scalar "SELECT COUNT(*) FROM wallets WHERE user_id BETWEEN 701 AND 803 AND (balance_amount < 0 OR frozen_amount <> 0)" "0" "wallet_invariant"
assert_scalar "SELECT COUNT(*) FROM ai_outbox_events WHERE status IN ('pending','publishing','dead')" "0" "outbox_backlog"
assert_scalar "SELECT COUNT(*) FROM ai_compensation_tasks WHERE status IN ('pending','retry','dead','manual_review')" "0" "compensation_backlog"
assert_scalar "SELECT COUNT(*) FROM token_usage_logs" "0" "legacy_ledger_not_written"

echo "G7_VERIFY=PASS isolated=true current_migrations=true fake_upstream=true paid_upstream=false concurrency=100 idempotency=20 stream_disconnect=true fake_upstream_stop_recover=true redis_stop_recover=true request_usage_difference=0 request_hold_difference=0 request_wallet_difference=0 billing_anomalies=0 unreleased_holds=0 outbox_backlog=0 compensation_backlog=0 project_database=false"
