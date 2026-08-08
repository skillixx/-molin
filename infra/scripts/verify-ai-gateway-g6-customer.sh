#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${AI_GATEWAY_G6_ISOLATED_APPROVED:-NO}" != "YES" ]]; then
  echo "G6_VERIFY=APPROVAL_REQUIRED target=isolated_temporary_mysql project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "G6_VERIFY=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "G6_VERIFY=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
up65="${repo_root}/server/migrations/000065_create_ai_gateway_g6_customer_journey.up.sql"
down65="${repo_root}/server/migrations/000065_create_ai_gateway_g6_customer_journey.down.sql"
suffix="${RANDOM}-$$"
container="molin-g6-mysql-${suffix}"
database="molin_g6_contract"
password="$(openssl rand -hex 24)"

cleanup() {
  docker container rm -f "${container}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "G6_PREFLIGHT target=temporary_docker_mysql impact=isolated_only rollback=remove_container"
docker run -d --pull="${G6_DOCKER_PULL_POLICY:-never}" --name "${container}" \
  -p 127.0.0.1::3306 \
  --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g \
  -e "MYSQL_ROOT_PASSWORD=${password}" \
  -e "MYSQL_DATABASE=${database}" \
  mysql:8.0 \
  --character-set-server=utf8mb4 \
  --collation-server=utf8mb4_0900_ai_ci >/dev/null

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=${password}" "${container}" \
    mysql -uroot --database="${database}" --batch --skip-column-names "$@"
}

for _ in $(seq 1 60); do
  if mysql_exec -e 'SELECT 1' >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
mysql_exec -e 'SELECT 1' >/dev/null 2>&1 || {
  echo "G6_VERIFY=FAILED reason=mysql_not_ready"
  exit 2
}

# 空库先应用到 G5，再显式验证 64 -> 65 的版本流转。
for migration in "${repo_root}"/server/migrations/*.up.sql; do
  [[ "${migration}" == "${up65}" ]] && continue
  docker exec -i -e "MYSQL_PWD=${password}" "${container}" \
    mysql -uroot --database="${database}" < "${migration}" >/dev/null
done
mysql_exec <<'SQL'
INSERT INTO users(id,email,password_hash,real_name_status,status)
VALUES (964,'g6-legacy@example.invalid','test-only','verified','active');
INSERT INTO token_channels(id,code,name,type,base_url,api_key_encrypted,status,priority,health_status)
VALUES (964,'g6-legacy','G6 Legacy','openai_compatible','http://legacy.invalid','encrypted-test-only','active',100,'healthy');
INSERT INTO token_models(id,logical_model_code,display_name,provider_name,modality,channel_id,upstream_model,status,docs_url,quick_start_url,updated_by)
VALUES (964,'molin/g6-legacy','G6 Legacy','Test','chat',964,'legacy/test/model','inactive','https://docs.invalid/legacy-api','https://docs.invalid/legacy-quick',964);
SQL
mysql_exec -e 'CREATE TABLE schema_migrations (version BIGINT NOT NULL PRIMARY KEY, dirty BOOLEAN NOT NULL); INSERT INTO schema_migrations(version,dirty) VALUES(64,0);'
mysql_exec -e 'UPDATE schema_migrations SET dirty=1 WHERE version=64 AND dirty=0;'
docker exec -i -e "MYSQL_PWD=${password}" "${container}" \
  mysql -uroot --database="${database}" < "${up65}" >/dev/null
mysql_exec -e 'UPDATE schema_migrations SET version=65,dirty=0 WHERE version=64 AND dirty=1;'

assert_scalar() {
  local sql="$1"
  local expected="$2"
  local label="$3"
  local actual
  actual="$(mysql_exec -e "${sql}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "G6_VERIFY=FAILED reason=${label} expected=${expected} actual=${actual}"
    exit 2
  fi
}

assert_rejected() {
  local sql="$1"
  local label="$2"
  if mysql_exec -e "${sql}" >/dev/null 2>&1; then
    echo "G6_VERIFY=FAILED reason=${label}_not_enforced"
    exit 2
  fi
}

assert_scalar "SELECT CONCAT(version, CHAR(58), dirty) FROM schema_migrations" "65:0" "first_up_version"
assert_scalar "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='token_models' AND column_name IN ('intro_url_health_status','docs_url_health_status','quick_start_url_health_status')" "3" "document_health_columns"
assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND constraint_name IN ('chk_token_models_intro_url_health','chk_token_models_docs_url_health','chk_token_models_quick_start_url_health','uk_ai_billing_disputes_request_user','chk_ai_billing_disputes_status','chk_ai_billing_disputes_reason')" "6" "named_constraints"
assert_scalar "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_requests' AND index_name='idx_ai_requests_user_states_created'" "4" "request_index_columns"
assert_scalar "SELECT CONCAT(docs_url_health_status, CHAR(58), quick_start_url_health_status) FROM token_models WHERE id=964" "unknown:unknown" "legacy_document_health_unknown"

mysql_exec <<'SQL'
INSERT INTO users(id,email,password_hash,real_name_status,status)
VALUES (965,'g6@example.invalid','test-only','verified','active');
INSERT INTO token_channels(id,code,name,type,base_url,api_key_encrypted,status,priority,health_status)
VALUES (965,'g6-bifrost','G6 Bifrost','openai_compatible','http://bifrost.invalid','encrypted-test-only','active',100,'healthy');
INSERT INTO token_models(id,logical_model_code,display_name,provider_name,modality,channel_id,upstream_model,status,docs_url,quick_start_url,updated_by)
VALUES (965,'molin/g6-test','G6 Test','Test','chat',965,'openrouter/test/model','inactive','https://docs.invalid/api','https://docs.invalid/quick',965);
INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone)
VALUES (965,965,'G6 Isolated','active','disabled','Asia/Shanghai');
INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone)
VALUES (966,965,'G6 Archived','archived','disabled','Asia/Shanghai');
INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status)
VALUES (965,965,965,'sk-g6-test','test-hash-only','G6 Test Key','postpaid','','allowlist','active');
INSERT INTO ai_budget_policies(scope_type,scope_id,mode,monthly_limit,updated_by)
VALUES ('project',965,'hard',100,965),('project',966,'hard',999,965);
INSERT INTO ai_budget_overrides(scope_type,scope_id,extra_amount,reason,operator_id,expires_at)
VALUES ('project',965,5,'G6 有效临时增额',965,'2026-08-09 00:00:00'),
       ('project',965,50,'G6 已过期临时增额',965,'2026-08-07 00:00:00');
INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code,modality,moderation_status,execution_status,billing_status)
VALUES ('req_g6_isolated_965',965,965,965,'molin/g6-test','chat','passed','succeeded','settled');
INSERT INTO ai_billing_disputes(dispute_no,request_id,user_id,reason,status)
VALUES ('DSP-G6-ISOLATED','req_g6_isolated_965',965,'隔离测试账单申诉说明不少于十个字符','submitted');
SQL

# 使用真实 GORM/MySQL 查询验证请求账本的本人隔离、详情和重复申诉错误映射。
host_port="$(docker port "${container}" 3306/tcp | sed -E 's/.*:([0-9]+)$/\1/' | head -n 1)"
(
  cd "${repo_root}/server"
  AI_GATEWAY_G6_MYSQL_DSN="root:${password}@tcp(127.0.0.1:${host_port})/${database}?parseTime=true&charset=utf8mb4" \
    go test -count=1 ./internal/modules/token_gateway/repository -run '^TestG6UserRepositoryMySQLIsolation$' >/dev/null
)

# 重复执行 up 不得覆盖文档健康状态或删除申诉事实。
mysql_exec -e "UPDATE token_models SET docs_url_health_status='unhealthy' WHERE id=965"
docker exec -i -e "MYSQL_PWD=${password}" "${container}" \
  mysql -uroot --database="${database}" < "${up65}" >/dev/null
assert_scalar "SELECT COUNT(*) FROM ai_billing_disputes WHERE dispute_no='DSP-G6-ISOLATED'" "1" "repeat_up_dispute_fact"
assert_rejected "INSERT INTO ai_billing_disputes(dispute_no,request_id,user_id,reason,status) VALUES ('DSP-G6-DUP','req_g6_isolated_965',965,'重复申诉必须被唯一约束拒绝','submitted')" "dispute_unique"
assert_rejected "UPDATE token_models SET docs_url_health_status='invalid' WHERE id=965" "document_health_check"

# down 采用事实保留策略，随后重新 up 验证版本可恢复且事实不丢失。
mysql_exec -e 'UPDATE schema_migrations SET dirty=1 WHERE version=65 AND dirty=0;'
docker exec -i -e "MYSQL_PWD=${password}" "${container}" \
  mysql -uroot --database="${database}" < "${down65}" >/dev/null
mysql_exec -e 'UPDATE schema_migrations SET version=64,dirty=0 WHERE version=65 AND dirty=1;'
assert_scalar "SELECT COUNT(*) FROM ai_billing_disputes WHERE dispute_no='DSP-G6-ISOLATED'" "1" "down_dispute_fact"

mysql_exec -e 'UPDATE schema_migrations SET dirty=1 WHERE version=64 AND dirty=0;'
docker exec -i -e "MYSQL_PWD=${password}" "${container}" \
  mysql -uroot --database="${database}" < "${up65}" >/dev/null
mysql_exec -e 'UPDATE schema_migrations SET version=65,dirty=0 WHERE version=64 AND dirty=1;'
assert_scalar "SELECT COUNT(*) FROM ai_billing_disputes WHERE dispute_no='DSP-G6-ISOLATED'" "1" "reup_dispute_fact"
assert_scalar "SELECT CONCAT(version, CHAR(58), dirty) FROM schema_migrations" "65:0" "reup_version"

echo "G6_VERIFY=PASS mysql=8.0 isolated=true schema=65:0 repeated_up=true fact_preserving_down=true reup=true document_health=true dispute_constraints=true request_index=true project_database=false"
