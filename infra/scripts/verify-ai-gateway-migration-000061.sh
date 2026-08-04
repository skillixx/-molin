#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${AI_GATEWAY_G2_MYSQL_MIGRATION_APPROVED:-NO}" != "YES" ]]; then
  echo "G2_MYSQL_MIGRATION=APPROVAL_REQUIRED target=isolated_temporary_container project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "G2_MYSQL_MIGRATION=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "G2_MYSQL_MIGRATION=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
g1_up="${repo_root}/server/migrations/000060_create_ai_gateway_ledger_expand.up.sql"
g2_up="${repo_root}/server/migrations/000061_add_ai_gateway_g2_projects_keys.up.sql"
g2_down="${repo_root}/server/migrations/000061_add_ai_gateway_g2_projects_keys.down.sql"
for file in "${g1_up}" "${g2_up}" "${g2_down}"; do
  test -f "${file}" || { echo "G2_MYSQL_MIGRATION=FAILED reason=migration_file_missing"; exit 2; }
done

container_name="molin-g2-mysql-000061-${RANDOM}-$$"
database_name="molin_g2_contract"
root_password="$(openssl rand -hex 24)"

cleanup() {
  if docker container inspect "${container_name}" >/dev/null 2>&1; then
    docker container rm -f "${container_name}" >/dev/null
  fi
}
trap cleanup EXIT

# 只使用测试机已有镜像；容器无网络、无端口、数据在 tmpfs，绝不连接项目数据库。
docker run -d --pull=never --network none \
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
mysql_exec -e 'SELECT 1' >/dev/null 2>&1 || { echo "G2_MYSQL_MIGRATION=FAILED reason=mysql_not_ready"; exit 2; }

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

CREATE TABLE schema_migrations (version BIGINT NOT NULL PRIMARY KEY, dirty BOOLEAN NOT NULL);
INSERT INTO schema_migrations(version,dirty) VALUES(59,0);
SQL

docker cp "${g1_up}" "${container_name}:/tmp/000060.up.sql" >/dev/null
docker cp "${g2_up}" "${container_name}:/tmp/000061.up.sql" >/dev/null
docker cp "${g2_down}" "${container_name}:/tmp/000061.down.sql" >/dev/null

apply_file() {
  local file="$1"
  docker exec -e "MYSQL_PWD=${root_password}" "${container_name}" sh -c \
    "mysql --protocol=socket -uroot --database='${database_name}' < '${file}'"
}

assert_scalar() {
  local sql="$1" expected="$2" label="$3" actual
  actual="$(mysql_exec -e "${sql}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "G2_MYSQL_MIGRATION=FAILED reason=${label} expected=${expected} actual=${actual}"
    exit 2
  fi
}

apply_file /tmp/000060.up.sql
mysql_exec -e "UPDATE schema_migrations SET version=60,dirty=0; INSERT INTO users(id,status,real_name_status) VALUES(1,'active','verified'),(2,'active','verified'); INSERT INTO token_models(id,logical_model_code) VALUES(1,'molin/qwen-turbo'); INSERT INTO ai_projects(id,user_id,name) VALUES(1,1,'项目一'); INSERT INTO api_keys(id,user_id,key_prefix,key_hash,name,model_scope,status) VALUES(1,1,'sk-molin-old','old-hash','旧密钥','','active');"

apply_file /tmp/000061.up.sql
mysql_exec -e "UPDATE schema_migrations SET version=61,dirty=0;"

assert_scalar "SELECT CONCAT(version,':',dirty) FROM schema_migrations" "61:0" "first_up_version"
assert_scalar "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='api_keys' AND column_name IN ('project_id','scope_mode','expires_at','rotated_from_id')" "4" "api_key_columns"
assert_scalar "SELECT scope_mode FROM api_keys WHERE id=1" "legacy_all" "legacy_key_mode"
assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='api_key_model_scopes'" "1" "scope_table"
assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND constraint_name IN ('fk_api_keys_project_owner','fk_api_key_model_scope_owner','fk_ai_requests_api_key_project_owner')" "3" "owner_constraints"

mysql_exec -e "INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,model_scope,scope_mode,status) VALUES(2,1,1,'sk-molin-new','new-hash','Project密钥','','allowlist','active'); INSERT INTO api_key_model_scopes(api_key_id,project_id,user_id,logical_model_code) VALUES(2,1,1,'molin/qwen-turbo');"

if mysql_exec -e "INSERT INTO api_keys(user_id,project_id,key_prefix,key_hash,name,scope_mode,status) VALUES(2,1,'sk-molin-cross','cross-hash','越权','all','active')" >/dev/null 2>&1; then
  echo "G2_MYSQL_MIGRATION=FAILED reason=cross_tenant_project_key_allowed"
  exit 2
fi
if mysql_exec -e "INSERT INTO api_key_model_scopes(api_key_id,project_id,user_id,logical_model_code) VALUES(2,1,2,'molin/qwen-turbo')" >/dev/null 2>&1; then
  echo "G2_MYSQL_MIGRATION=FAILED reason=cross_tenant_scope_allowed"
  exit 2
fi

mysql_exec -e "INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code,billing_status) VALUES('req-valid-g2',1,1,2,'molin/qwen-turbo','unquoted');"
if mysql_exec -e "INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code) VALUES('req-cross-g2',2,1,2,'molin/qwen-turbo')" >/dev/null 2>&1; then
  echo "G2_MYSQL_MIGRATION=FAILED reason=cross_tenant_request_allowed"
  exit 2
fi
assert_scalar "SELECT CONCAT(billing_status,':',COALESCE(quoted_amount,'NULL'),':',COALESCE(held_amount,'NULL'),':',COALESCE(settled_amount,'NULL')) FROM ai_requests WHERE request_id='req-valid-g2'" "unquoted:NULL:NULL:NULL" "g2_unquoted_only"

apply_file /tmp/000061.down.sql
mysql_exec -e "UPDATE schema_migrations SET version=60,dirty=0;"
assert_scalar "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='api_keys' AND column_name='project_id'" "1" "down_retention"
apply_file /tmp/000061.up.sql
mysql_exec -e "UPDATE schema_migrations SET version=61,dirty=0;"
assert_scalar "SELECT CONCAT(version,':',dirty) FROM schema_migrations" "61:0" "reup_version"
assert_scalar "SELECT COUNT(*) FROM api_key_model_scopes WHERE api_key_id=2 AND logical_model_code='molin/qwen-turbo'" "1" "reup_scope_retention"

echo "G2_MYSQL_MIGRATION=PASS mysql=8.0 isolated=true project_database=false first_up=true down_retained=true reup=true tenant_constraints=true allowlist_constraints=true billing_unquoted=true"
