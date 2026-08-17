#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${AI_GATEWAY_G3_MYSQL_APPROVED:-NO}" != "YES" ]]; then
  echo "G3_MYSQL=APPROVAL_REQUIRED target=isolated_temporary_container project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "G3_MYSQL=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "G3_MYSQL=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# Windows Git Bash 调用 Docker Desktop 时必须传 Windows 绝对路径，并关闭 MSYS 对容器路径的二次改写。
if [[ "$(uname -s)" == MINGW* || "$(uname -s)" == MSYS* ]]; then
  repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -W)"
  export MSYS_NO_PATHCONV=1
fi
g1_up="${repo_root}/server/migrations/000060_create_ai_gateway_ledger_expand.up.sql"
g2_up="${repo_root}/server/migrations/000061_add_ai_gateway_g2_projects_keys.up.sql"
g3_up="${repo_root}/server/migrations/000062_create_ai_gateway_g3_billing.up.sql"
g3_down="${repo_root}/server/migrations/000062_create_ai_gateway_g3_billing.down.sql"
g8_evidence_up="${repo_root}/server/migrations/000067_align_token_usage_log_money_precision.up.sql"
for file in "${g1_up}" "${g2_up}" "${g3_up}" "${g3_down}" "${g8_evidence_up}"; do
  test -f "${file}" || { echo "G3_MYSQL=FAILED reason=migration_file_missing"; exit 2; }
done

suffix="${RANDOM}-$$"
container_name="molin-g3-mysql-${suffix}"
rabbit_container_name="molin-g3-rabbit-${suffix}"
network_name="molin-g3-net-${suffix}"
database_name="molin_g3_contract"
root_password="$(openssl rand -hex 24)"
rabbit_password="$(openssl rand -hex 24)"
rabbit_cookie="$(openssl rand -hex 32)"
docker_pull_policy="${G3_DOCKER_PULL_POLICY:-never}"

cleanup() {
	docker container rm -f "${rabbit_container_name}" >/dev/null 2>&1 || true
  docker container rm -f "${container_name}" >/dev/null 2>&1 || true
  docker network rm "${network_name}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker network create "${network_name}" >/dev/null
docker run -d --pull="${docker_pull_policy}" --network "${network_name}" --network-alias mysql \
  --name "${container_name}" \
  --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g \
  -e "MYSQL_ROOT_PASSWORD=${root_password}" \
  -e "MYSQL_DATABASE=${database_name}" \
  mysql:8.0 \
  --character-set-server=utf8mb4 \
  --collation-server=utf8mb4_0900_ai_ci >/dev/null

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=${root_password}" "${container_name}" \
    mysql --protocol=socket -uroot --database="${database_name}" --batch --skip-column-names "$@"
}

for _ in $(seq 1 60); do
  if mysql_exec -e 'SELECT 1' >/dev/null 2>&1; then break; fi
  sleep 1
done
mysql_exec -e 'SELECT 1' >/dev/null 2>&1 || { echo "G3_MYSQL=FAILED reason=mysql_not_ready"; exit 2; }

mysql_exec <<'SQL'
CREATE TABLE users (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  real_name_status VARCHAR(32) NOT NULL DEFAULT 'unverified',
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE api_keys (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  key_prefix VARCHAR(32) NOT NULL,
  key_hash VARCHAR(128) NOT NULL,
  name VARCHAR(128) NOT NULL DEFAULT '',
  billing_mode VARCHAR(16) NOT NULL DEFAULT 'postpaid',
  source_id BIGINT UNSIGNED NULL,
  model_scope VARCHAR(512) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  last_used_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_api_keys_hash (key_hash),
  KEY idx_api_keys_user (user_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE token_models (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  logical_model_code VARCHAR(128) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  modality VARCHAR(32) NOT NULL DEFAULT 'chat',
  PRIMARY KEY (id),
  UNIQUE KEY uk_token_models_code (logical_model_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE wallets (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  balance_amount DECIMAL(18,6) NOT NULL DEFAULT 0,
  frozen_amount DECIMAL(18,6) NOT NULL DEFAULT 0,
  currency VARCHAR(16) NOT NULL DEFAULT 'CNY',
  version BIGINT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_wallets_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE wallet_transactions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  wallet_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  type VARCHAR(32) NOT NULL,
  direction VARCHAR(8) NOT NULL,
  amount DECIMAL(18,6) NOT NULL,
  balance_after DECIMAL(18,6) NOT NULL,
  related_order_id BIGINT UNSIGNED NULL,
  remark VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_wallet_transactions_wallet_id (wallet_id),
  KEY idx_wallet_transactions_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE wallet_holds (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  wallet_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  hold_amount DECIMAL(18,6) NOT NULL,
  settled_amount DECIMAL(18,6) NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'holding',
  idempotency_key VARCHAR(191) NOT NULL,
  freeze_txn_id BIGINT UNSIGNED NULL,
  settle_txn_id BIGINT UNSIGNED NULL,
  remark VARCHAR(512) NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  settled_at DATETIME NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_wallet_holds_idem (idempotency_key),
  KEY idx_wallet_holds_user (user_id),
  KEY idx_wallet_holds_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE token_usage_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  request_id VARCHAR(128) NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  api_key_id BIGINT UNSIGNED NULL,
  logical_model_code VARCHAR(128) NOT NULL,
  modality VARCHAR(32) NOT NULL DEFAULT 'chat',
  input_tokens BIGINT NOT NULL DEFAULT 0,
  output_tokens BIGINT NOT NULL DEFAULT 0,
  total_tokens BIGINT NOT NULL DEFAULT 0,
  units DECIMAL(18,6) NOT NULL DEFAULT 0,
  sale_amount DECIMAL(18,6) NOT NULL DEFAULT 0,
  is_stream TINYINT(1) NOT NULL DEFAULT 0,
  status VARCHAR(32) NOT NULL,
  error_code VARCHAR(64) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_token_usage_logs_request_id (request_id),
  KEY idx_token_usage_logs_user_created (user_id, created_at),
  KEY idx_token_usage_logs_apikey_created (api_key_id, created_at),
  KEY idx_token_usage_logs_model (logical_model_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
SQL

for file in "${g1_up}" "${g2_up}" "${g3_up}" "${g3_down}" "${g8_evidence_up}"; do
  docker cp "${file}" "${container_name}:/tmp/$(basename "${file}")" >/dev/null
done

apply_file() {
  docker exec -e "MYSQL_PWD=${root_password}" "${container_name}" sh -c \
    "mysql --protocol=socket -uroot --database='${database_name}' < '/tmp/$1'"
}

assert_scalar() {
  local sql="$1" expected="$2" label="$3" actual
  actual="$(mysql_exec -e "${sql}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "G3_MYSQL=FAILED reason=${label} expected=${expected} actual=${actual}"
    exit 2
  fi
}

apply_file "$(basename "${g1_up}")"
apply_file "$(basename "${g2_up}")"
apply_file "$(basename "${g3_up}")"
apply_file "$(basename "${g3_up}")"
apply_file "$(basename "${g8_evidence_up}")"

assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('ai_price_versions','ai_price_model_locks','ai_price_skus','ai_request_wallet_links','ai_outbox_events')" "5" "g3_tables"
assert_scalar "SELECT CONCAT(numeric_precision,':',numeric_scale) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='wallets' AND column_name='balance_amount'" "20:8" "wallet_precision"
assert_scalar "SELECT CONCAT(numeric_precision,':',numeric_scale) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='token_usage_logs' AND column_name='sale_amount'" "20:8" "usage_log_sale_amount_precision"
assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND constraint_name IN ('chk_wallets_non_negative','chk_wallet_holds_amounts')" "2" "wallet_checks"

if mysql_exec -e "INSERT INTO wallets(user_id,balance_amount,frozen_amount) VALUES(99,-1,0)" >/dev/null 2>&1; then
  echo "G3_MYSQL=FAILED reason=negative_wallet_allowed"
  exit 2
fi

mysql_exec <<'SQL'
INSERT INTO users(id,status,real_name_status) VALUES
  (1,'active','verified'),(2,'active','verified'),(3,'active','verified'),(4,'active','verified'),
  (5,'active','verified'),(6,'active','verified'),(7,'active','verified'),(8,'active','verified'),
  (9,'active','verified'),(10,'active','verified'),(11,'active','verified'),(12,'active','verified'),
  (13,'active','verified'),(14,'active','verified'),(15,'active','verified'),
  (16,'active','verified'),(17,'active','verified'),(18,'active','verified');
INSERT INTO token_models(id,logical_model_code,status,modality) VALUES
  (1,'qwen-plus','active','chat'),(2,'qwen-concurrent','active','chat');
INSERT INTO ai_projects(id,user_id,name) VALUES
  (1,1,'G3-1'),(2,2,'G3-2'),(3,3,'G3-3'),(4,4,'G3-4'),(5,5,'G3-5'),
  (6,6,'G3-6'),(7,7,'G3-7'),(8,8,'G3-8'),(9,9,'G3-9'),(10,10,'G3-10'),(11,11,'G3-11'),(12,12,'G3-12'),
  (13,13,'G3-13'),(14,14,'G3-14'),(15,15,'G3-15'),(16,16,'G3-16'),(17,17,'G3-17'),(18,18,'G3-18');
INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,model_scope,scope_mode,status) VALUES
  (1,1,1,'sk-g3-1','hash-1','G3-1','','all','active'),
  (2,2,2,'sk-g3-2','hash-2','G3-2','','all','active'),
  (3,3,3,'sk-g3-3','hash-3','G3-3','','all','active'),
  (4,4,4,'sk-g3-4','hash-4','G3-4','','all','active'),
  (5,5,5,'sk-g3-5','hash-5','G3-5','','all','active'),
  (6,6,6,'sk-g3-6','hash-6','G3-6','','all','active'),
  (7,7,7,'sk-g3-7','hash-7','G3-7','','all','active'),
  (8,8,8,'sk-g3-8','hash-8','G3-8','','all','active'),
  (9,9,9,'sk-g3-9','hash-9','G3-9','','all','active'),
  (10,10,10,'sk-g3-10','hash-10','G3-10','','all','active'),
  (11,11,11,'sk-g3-11','hash-11','G3-11','','all','active'),
  (12,12,12,'sk-g3-12','hash-12','G3-12','','all','active'),
  (13,13,13,'sk-g3-13','hash-13','G3-13','','all','active'),
  (14,14,14,'sk-g3-14','hash-14','G3-14','','all','active'),
  (15,15,15,'sk-g3-15','hash-15','G3-15','','all','active'),
  (16,16,16,'sk-g3-16','hash-16','G3-16','','all','active'),
  (17,17,17,'sk-g3-17','hash-17','G3-17','','all','active'),
  (18,18,18,'sk-g3-18','hash-18','G3-18','','all','active');
INSERT INTO wallets(id,user_id,balance_amount,frozen_amount,currency) VALUES
  (1,1,0,0,'CNY'),(2,2,0.14,0,'CNY'),(3,3,1,0,'CNY'),(4,4,1,0,'CNY'),
  (5,5,1,0,'CNY'),(6,6,1,0,'CNY'),(7,7,1,0,'CNY'),(8,8,1,0,'CNY');
INSERT INTO wallets(id,user_id,balance_amount,frozen_amount,currency) VALUES
  (9,9,1,0,'CNY'),(10,10,1,0,'CNY'),(11,11,1,0,'CNY'),(12,12,1,0,'CNY'),
  (13,13,1,0,'CNY'),(14,14,1,0,'CNY'),(15,15,1,0,'CNY'),(16,16,1,0,'CNY'),(17,17,1,0,'CNY'),(18,18,1,0,'CNY');
INSERT INTO ai_price_versions(
  id,logical_model_code,version_no,currency,exchange_rate,status,min_margin_rate,
  max_input_tokens,max_output_tokens,failure_charge_policy,rounding_mode,
  cost_updated_at,cost_expires_at,effective_at,created_by,approved_by,approved_at,published_at
) VALUES(
  1,'qwen-plus',1,'CNY',1,'active',0.2,1000,100,'confirmed_usage','ceil_8',
  '2026-08-01 00:00:00','2030-01-01 00:00:00','2026-08-01 00:00:00',1,1,NOW(),NOW()
);
INSERT INTO ai_price_skus(price_version_id,meter_type,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES
  (1,'input_tokens',SHA2('input',256),5,10,1000000,'CNY'),
  (1,'cached_tokens',SHA2('cached',256),1,2,1000000,'CNY'),
  (1,'output_tokens',SHA2('output',256),10,20,1000000,'CNY'),
  (1,'reasoning_tokens',SHA2('reasoning',256),20,40,1000000,'CNY');
SQL

docker run --rm --network "${network_name}" \
  -v "${repo_root}:/src:ro" \
  -v molin-g3-go-mod-cache:/go/pkg/mod \
  -v molin-g3-go-build-cache:/root/.cache/go-build \
  -w /src/server \
  -e CGO_ENABLED=1 \
  -e GOPROXY=https://goproxy.cn,direct \
  -e "G3_MYSQL_DSN=root:${root_password}@tcp(mysql:3306)/${database_name}?parseTime=true&charset=utf8mb4" \
  golang:1.25 \
  go test -count=1 ./internal/modules/token_gateway/service -run '^TestG3MySQLBillingIntegration$'

# RabbitMQ 尚未启动时先运行失败阶段，事件必须保留在 MySQL；随后启动 Broker 验证恢复发布。
docker run --rm --network "${network_name}" \
  -v "${repo_root}:/src:ro" \
  -v molin-g3-go-mod-cache:/go/pkg/mod \
  -v molin-g3-go-build-cache:/root/.cache/go-build \
  -w /src/server \
  -e CGO_ENABLED=1 \
  -e GOPROXY=https://goproxy.cn,direct \
  -e "G3_MYSQL_DSN=root:${root_password}@tcp(mysql:3306)/${database_name}?parseTime=true&charset=utf8mb4" \
  -e "G3_RABBITMQ_URL=amqp://g3test:${rabbit_password}@rabbit:5672/" \
  -e G3_RABBIT_PHASE=down \
  golang:1.25 \
  go test -count=1 ./internal/modules/token_gateway/service -run '^TestG3RabbitMQOutboxIntegration$'

docker run -d --pull="${docker_pull_policy}" --network "${network_name}" --network-alias rabbit \
  --name "${rabbit_container_name}" \
  --tmpfs /var/lib/rabbitmq:rw,noexec,nosuid,uid=999,gid=999,mode=0750,size=256m \
  --entrypoint /bin/bash rabbitmq:3-management -c \
  "printf '%s' '${rabbit_cookie}' > /var/lib/rabbitmq/.erlang.cookie && chown rabbitmq:rabbitmq /var/lib/rabbitmq/.erlang.cookie && chmod 400 /var/lib/rabbitmq/.erlang.cookie && exec /usr/local/bin/docker-entrypoint.sh rabbitmq-server" >/dev/null
for _ in $(seq 1 60); do
  if docker exec "${rabbit_container_name}" rabbitmq-diagnostics -q check_running >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec "${rabbit_container_name}" rabbitmq-diagnostics -q check_running >/dev/null 2>&1 || {
  echo "G3_RABBITMQ=FAILED reason=rabbit_not_ready"
  docker ps -a --filter "name=${rabbit_container_name}" --format 'status={{.Status}}'
  docker logs --tail 80 "${rabbit_container_name}" 2>&1 || true
  exit 2
}
docker exec "${rabbit_container_name}" rabbitmqctl add_user g3test "${rabbit_password}" >/dev/null
docker exec "${rabbit_container_name}" rabbitmqctl set_permissions -p / g3test '.*' '.*' '.*' >/dev/null

docker run --rm --network "${network_name}" \
  -v "${repo_root}:/src:ro" \
  -v molin-g3-go-mod-cache:/go/pkg/mod \
  -v molin-g3-go-build-cache:/root/.cache/go-build \
  -w /src/server \
  -e CGO_ENABLED=1 \
  -e GOPROXY=https://goproxy.cn,direct \
  -e "G3_MYSQL_DSN=root:${root_password}@tcp(mysql:3306)/${database_name}?parseTime=true&charset=utf8mb4" \
  -e "G3_RABBITMQ_URL=amqp://g3test:${rabbit_password}@rabbit:5672/" \
  -e G3_RABBIT_PHASE=recover \
  golang:1.25 \
  go test -count=1 ./internal/modules/token_gateway/service -run '^TestG3RabbitMQOutboxIntegration$'

# 在 down 前记录集成测试生成的全部请求事实；后续只验证迁移不会丢失事实，避免新增合法用例时维护脆弱的固定总数。
retained_requests_before="$(mysql_exec -e "SELECT COUNT(*) FROM ai_requests WHERE request_id LIKE 'g3-%'")"
if [[ ! "${retained_requests_before}" =~ ^[1-9][0-9]*$ ]]; then
  echo "G3_MYSQL=FAILED reason=pre_down_fact_count_invalid actual=${retained_requests_before}"
  exit 2
fi

apply_file "$(basename "${g3_down}")"
apply_file "$(basename "${g3_up}")"
# down/re-up 后请求事实总数必须与执行前完全一致，证明扩展迁移不会删除财务事实。
assert_scalar "SELECT COUNT(*) FROM ai_requests WHERE request_id LIKE 'g3-%'" "${retained_requests_before}" "down_reup_fact_retention"
assert_scalar "SELECT COUNT(*) FROM ai_price_versions WHERE id=1 AND status='suspended'" "1" "price_fact_retention"
assert_scalar "SELECT COUNT(*) FROM wallets WHERE balance_amount < 0 OR frozen_amount < 0" "0" "non_negative_wallets"
# 每个 settled 请求必须恰好存在一条汇总日志；唯一索引负责阻止并发重复写入。
assert_scalar "SELECT COUNT(*) FROM ai_requests r LEFT JOIN token_usage_logs l ON l.request_id=r.request_id WHERE r.billing_status='settled' AND l.id IS NULL" "0" "settled_usage_log_missing"
assert_scalar "SELECT COUNT(*) FROM token_usage_logs l LEFT JOIN ai_requests r ON r.request_id=l.request_id WHERE r.request_id IS NULL OR r.billing_status<>'settled'" "0" "usage_log_without_settled_request"

echo "G3_MYSQL=PASS mysql=8.0 isolated=true project_database=false first_up=true repeated_up=true retained_down=true reup=true go_integration=true concurrent_wallet=100 idempotency=20 terminal_once=true over_hold_exception=true"
echo "G3_RABBITMQ=PASS broker_confirm=true stopped_retained=true recovered_published=true"
