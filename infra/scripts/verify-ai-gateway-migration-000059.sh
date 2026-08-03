#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${AI_GATEWAY_G1_MYSQL_MIGRATION_APPROVED:-NO}" != "YES" ]]; then
  echo "G1_MYSQL_MIGRATION=APPROVAL_REQUIRED target=isolated_temporary_container project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "G1_MYSQL_MIGRATION=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "G1_MYSQL_MIGRATION=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
up_file="${repo_root}/server/migrations/000059_create_ai_gateway_ledger_expand.up.sql"
down_file="${repo_root}/server/migrations/000059_create_ai_gateway_ledger_expand.down.sql"
test -f "${up_file}" || { echo "G1_MYSQL_MIGRATION=FAILED reason=up_file_missing"; exit 2; }
test -f "${down_file}" || { echo "G1_MYSQL_MIGRATION=FAILED reason=down_file_missing"; exit 2; }

container_name="molin-g1-mysql-000059-${RANDOM}-$$"
database_name="molin_g1_contract"
root_password="$(openssl rand -hex 24)"

cleanup() {
  if docker container inspect "${container_name}" >/dev/null 2>&1; then
    docker container rm -f "${container_name}" >/dev/null
  fi
}
trap cleanup EXIT

# 独立容器不映射端口，数据目录使用 tmpfs；脚本退出时只删除自己创建的精确容器名。
docker run -d \
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

mysql_exec_database() {
  local target_database="$1"
  shift
  docker exec -i -e "MYSQL_PWD=${root_password}" "${container_name}" \
    mysql --protocol=socket -uroot --database="${target_database}" --batch --skip-column-names "$@"
}

for _ in $(seq 1 60); do
  if mysql_exec -e 'SELECT 1' >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
mysql_exec -e 'SELECT 1' >/dev/null 2>&1 || { echo "G1_MYSQL_MIGRATION=FAILED reason=mysql_not_ready"; exit 2; }

mysql_exec <<'SQL'
CREATE TABLE users (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE api_keys (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (id),
  KEY idx_api_keys_user (user_id),
  CONSTRAINT fk_api_keys_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE schema_migrations (
  version BIGINT NOT NULL PRIMARY KEY,
  dirty BOOLEAN NOT NULL
);
INSERT INTO schema_migrations (version, dirty) VALUES (58, 0);
SQL

docker cp "${up_file}" "${container_name}:/tmp/000059.up.sql" >/dev/null
docker cp "${down_file}" "${container_name}:/tmp/000059.down.sql" >/dev/null

apply_up() {
  mysql_exec -e 'UPDATE schema_migrations SET dirty=1 WHERE version=58 AND dirty=0'
  docker exec -e "MYSQL_PWD=${root_password}" "${container_name}" sh -c \
    "mysql --protocol=socket -uroot --database='${database_name}' < /tmp/000059.up.sql"
  mysql_exec -e 'UPDATE schema_migrations SET version=59,dirty=0 WHERE version=58 AND dirty=1'
}

apply_down() {
  mysql_exec -e 'UPDATE schema_migrations SET dirty=1 WHERE version=59 AND dirty=0'
  docker exec -e "MYSQL_PWD=${root_password}" "${container_name}" sh -c \
    "mysql --protocol=socket -uroot --database='${database_name}' < /tmp/000059.down.sql"
  mysql_exec -e 'UPDATE schema_migrations SET version=58,dirty=0 WHERE version=59 AND dirty=1'
}

assert_scalar() {
  local sql="$1" expected="$2" label="$3" actual
  actual="$(mysql_exec -e "${sql}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "G1_MYSQL_MIGRATION=FAILED reason=${label} expected=${expected} actual=${actual}"
    exit 2
  fi
}

apply_up
assert_scalar "SELECT CONCAT(version,':',dirty) FROM schema_migrations" "59:0" "first_up_version"
assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('ai_projects','ai_requests','ai_usage_items','ai_execution_attempts')" "4" "table_count"
assert_scalar "SELECT COUNT(*) FROM (SELECT index_name,non_unique,GROUP_CONCAT(column_name ORDER BY seq_in_index) columns_in_order,SUM(sub_part IS NOT NULL) prefix_parts FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='api_keys' AND index_name='uk_api_keys_id_user' GROUP BY index_name,non_unique HAVING non_unique=0 AND columns_in_order='id,user_id' AND prefix_parts=0) exact_index" "1" "api_key_owner_index"

mysql_exec -e "INSERT INTO users(id) VALUES(1),(2); INSERT INTO api_keys(id,user_id) VALUES(1,1); INSERT INTO ai_projects(id,user_id,name,budget_mode,monthly_budget) VALUES(1,1,'项目一','hard',10.00000000);"

if mysql_exec -e "INSERT INTO ai_projects(user_id,name,budget_mode,monthly_budget) VALUES(1,'非法停用预算','disabled',1.00000000)" >/dev/null 2>&1; then
  echo "G1_MYSQL_MIGRATION=FAILED reason=disabled_budget_constraint"
  exit 2
fi
if mysql_exec -e "INSERT INTO ai_projects(user_id,name,budget_mode,monthly_budget) VALUES(1,'非法软预算','soft',NULL)" >/dev/null 2>&1; then
  echo "G1_MYSQL_MIGRATION=FAILED reason=soft_budget_constraint"
  exit 2
fi
if mysql_exec -e "INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code) VALUES('req-cross-project',2,1,NULL,'molin/qwen-turbo')" >/dev/null 2>&1; then
  echo "G1_MYSQL_MIGRATION=FAILED reason=cross_tenant_project_allowed"
  exit 2
fi
if mysql_exec -e "INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code) VALUES('req-cross-key',2,NULL,1,'molin/qwen-turbo')" >/dev/null 2>&1; then
  echo "G1_MYSQL_MIGRATION=FAILED reason=cross_tenant_api_key_allowed"
  exit 2
fi

mysql_exec -e "INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code) VALUES('req-valid',1,1,1,'molin/qwen-turbo'); INSERT INTO ai_usage_items(request_id,meter_type,source,sequence_no,quantity) VALUES('req-valid','input_tokens','provider',0,2);"
if mysql_exec -e "INSERT INTO ai_usage_items(request_id,meter_type,source,sequence_no,quantity) VALUES('req-valid','input_tokens','provider',0,2)" >/dev/null 2>&1; then
  echo "G1_MYSQL_MIGRATION=FAILED reason=usage_idempotency_constraint"
  exit 2
fi

apply_down
assert_scalar "SELECT CONCAT(version,':',dirty) FROM schema_migrations" "58:0" "down_version"
assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('ai_projects','ai_requests','ai_usage_items','ai_execution_attempts')" "4" "down_retention"
apply_up
assert_scalar "SELECT CONCAT(version,':',dirty) FROM schema_migrations" "59:0" "reup_version"
assert_scalar "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='api_keys' AND index_name='uk_api_keys_id_user'" "2" "reup_index_parts"

# 独立漂移库预置同名错误索引，Migration 必须因重复索引名失败关闭，不能把错误结构当成已完成。
mysql_exec -e 'CREATE DATABASE molin_g1_drift CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci'
mysql_exec_database molin_g1_drift <<'SQL'
CREATE TABLE users (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE api_keys (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_api_keys_id_user (user_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
SQL
if docker exec -e "MYSQL_PWD=${root_password}" "${container_name}" sh -c \
  "mysql --protocol=socket -uroot --database='molin_g1_drift' < /tmp/000059.up.sql" >/dev/null 2>&1; then
  echo "G1_MYSQL_MIGRATION=FAILED reason=drift_index_not_rejected"
  exit 2
fi

echo "G1_MYSQL_MIGRATION=PASS mysql=8.0 first_up=true down_retained=true reup=true tenant_constraints=true budget_constraints=true idempotency=true drift_rejected=true"
