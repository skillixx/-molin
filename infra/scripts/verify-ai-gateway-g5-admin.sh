#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${AI_GATEWAY_G5_ISOLATED_APPROVED:-NO}" != "YES" ]]; then
  echo "G5_VERIFY=APPROVAL_REQUIRED target=isolated_temporary_mysql project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "G5_VERIFY=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "G5_VERIFY=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
up64="${repo_root}/server/migrations/000064_create_ai_gateway_g5_admin_workbench.up.sql"
down64="${repo_root}/server/migrations/000064_create_ai_gateway_g5_admin_workbench.down.sql"
suffix="${RANDOM}-$$"
container="molin-g5-mysql-${suffix}"
database="molin_g5_contract"
password="$(openssl rand -hex 24)"

cleanup() { docker container rm -f "${container}" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "G5_PREFLIGHT target=temporary_docker_mysql impact=isolated_only rollback=remove_container"
docker run -d --pull="${G5_DOCKER_PULL_POLICY:-never}" --name "${container}" --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g \
  -e "MYSQL_ROOT_PASSWORD=${password}" -e "MYSQL_DATABASE=${database}" mysql:8.0 \
  --character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci >/dev/null

mysql_exec() { docker exec -i -e "MYSQL_PWD=${password}" "${container}" mysql -uroot --database="${database}" --batch --skip-column-names "$@"; }
for _ in $(seq 1 60); do mysql_exec -e 'SELECT 1' >/dev/null 2>&1 && break; sleep 1; done
mysql_exec -e 'SELECT 1' >/dev/null 2>&1 || { echo "G5_VERIFY=FAILED reason=mysql_not_ready"; exit 2; }

# 从空库应用至 G4，并显式模拟 migrate 的 63:0 -> 64:0、down 与 re-up 状态流转。
for migration in "${repo_root}"/server/migrations/*.up.sql; do
  [[ "${migration}" == "${up64}" ]] && continue
  docker exec -i -e "MYSQL_PWD=${password}" "${container}" mysql -uroot --database="${database}" < "${migration}"
done
mysql_exec -e 'CREATE TABLE schema_migrations (version BIGINT NOT NULL PRIMARY KEY, dirty BOOLEAN NOT NULL); INSERT INTO schema_migrations(version,dirty) VALUES(63,0);'
mysql_exec -e 'UPDATE schema_migrations SET dirty=1 WHERE version=63 AND dirty=0;'
docker exec -i -e "MYSQL_PWD=${password}" "${container}" mysql -uroot --database="${database}" < "${up64}"
mysql_exec -e 'UPDATE schema_migrations SET version=64,dirty=0 WHERE version=63 AND dirty=1;'
assert_version_after_first_up="$(mysql_exec -e 'SELECT CONCAT(version, CHAR(58), dirty) FROM schema_migrations')"
[[ "${assert_version_after_first_up}" == "64:0" ]] || { echo "G5_VERIFY=FAILED reason=first_up_version actual=${assert_version_after_first_up}"; exit 2; }

# 在首次 up 后立即写入事实，随后验证重复 up、down 与 re-up 都不会丢失这些审计数据。
mysql_exec <<'SQL'
INSERT INTO users(id,email,password_hash,real_name_status,status) VALUES (901,'g5@example.invalid','test-only','verified','active');
INSERT INTO token_channels(id,code,name,type,base_url,api_key_encrypted,status,priority,health_status) VALUES (901,'g5-bifrost','G5 Bifrost','openai_compatible','http://bifrost.invalid','encrypted-test-only','active',100,'healthy');
INSERT INTO token_models(id,logical_model_code,display_name,provider_name,modality,channel_id,upstream_model,status,docs_url,quick_start_url,updated_by)
VALUES (901,'molin/g5-test','G5 Test','Test','chat',901,'openrouter/test/model','inactive','https://docs.invalid/api','https://docs.invalid/quick',901);
INSERT INTO ai_model_routes(logical_model_code,channel_id,provider_model,priority,weight,timeout_ms,max_retries,circuit_breaker_threshold,fallback_order,status,updated_by)
VALUES ('molin/g5-test',901,'openrouter/test/model',100,100,30000,0,5,0,'active',901);
INSERT INTO ai_model_route_runtime_states(route_id,consecutive_failures,circuit_open_until)
SELECT id,5,DATE_ADD(NOW(), INTERVAL 30 SECOND) FROM ai_model_routes WHERE logical_model_code='molin/g5-test' AND provider_model='openrouter/test/model';
INSERT INTO ai_model_release_versions(model_id,version_no,status,snapshot_json,reason,created_by,published_at)
VALUES (901,1,'active',JSON_OBJECT('logical_model_code','molin/g5-test'),'G5 isolated verify',901,NOW());
INSERT INTO ai_gateway_rejection_events(request_id,logical_model_code,reason_code,scope_type,scope_id)
VALUES ('g5-rejection-901','molin/g5-test','rpm_limit_exceeded','project','901');
SQL

assert_scalar() {
  local sql="$1" expected="$2" label="$3" actual
  actual="$(mysql_exec -e "${sql}")"
  [[ "${actual}" == "${expected}" ]] || { echo "G5_VERIFY=FAILED reason=${label} expected=${expected} actual=${actual}"; exit 2; }
}

assert_facts() {
  local phase="$1"
  assert_scalar "SELECT COUNT(*) FROM ai_model_routes WHERE logical_model_code='molin/g5-test' AND provider_model='openrouter/test/model'" "1" "${phase}_route_fact"
  assert_scalar "SELECT COUNT(*) FROM ai_model_release_versions WHERE model_id=901 AND version_no=1" "1" "${phase}_release_fact"
  assert_scalar "SELECT COUNT(*) FROM ai_gateway_rejection_events WHERE request_id='g5-rejection-901' AND reason_code='rpm_limit_exceeded'" "1" "${phase}_rejection_fact"
}

assert_rejected() {
  local sql="$1" label="$2"
  if mysql_exec -e "${sql}" >/dev/null 2>&1; then
    echo "G5_VERIFY=FAILED reason=${label}_not_enforced"
    exit 2
  fi
}

assert_facts "first_up"

# 额外直接重复执行 up，验证条件 DDL 和 seed 可重入，但不伪造第二次 migrate 版本迁移。
docker exec -i -e "MYSQL_PWD=${password}" "${container}" mysql -uroot --database="${database}" < "${up64}"
assert_facts "repeated_up"
mysql_exec -e 'UPDATE schema_migrations SET dirty=1 WHERE version=64 AND dirty=0;'
docker exec -i -e "MYSQL_PWD=${password}" "${container}" mysql -uroot --database="${database}" < "${down64}"
mysql_exec -e 'UPDATE schema_migrations SET version=63,dirty=0 WHERE version=64 AND dirty=1;'
assert_version_after_down="$(mysql_exec -e 'SELECT CONCAT(version, CHAR(58), dirty) FROM schema_migrations')"
[[ "${assert_version_after_down}" == "63:0" ]] || { echo "G5_VERIFY=FAILED reason=down_version actual=${assert_version_after_down}"; exit 2; }
assert_facts "down"
mysql_exec -e 'UPDATE schema_migrations SET dirty=1 WHERE version=63 AND dirty=0;'
docker exec -i -e "MYSQL_PWD=${password}" "${container}" mysql -uroot --database="${database}" < "${up64}"
mysql_exec -e 'UPDATE schema_migrations SET version=64,dirty=0 WHERE version=63 AND dirty=1;'
assert_facts "reup"

assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('ai_model_release_versions','ai_model_routes','ai_model_route_runtime_states','ai_gateway_rejection_events')" "4" "table_count"
assert_scalar "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='token_models' AND column_name IN ('provider_name','description','capabilities_json','context_window','intro_url','docs_url','quick_start_url','release_version_no','published_at','updated_by')" "10" "model_columns"
assert_scalar "SELECT COUNT(*) FROM permissions WHERE code IN ('ai_gateway:model_manage','ai_gateway:price_manage','ai_gateway:route_manage')" "3" "permissions"
assert_scalar "SELECT CONCAT(version, CHAR(58), dirty) FROM schema_migrations" "64:0" "reup_version"
assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND constraint_name IN ('fk_token_model_updated_by','chk_token_channel_health_status','uk_ai_model_release_version','fk_ai_model_release_model','fk_ai_model_release_operator','chk_ai_model_release_status','uk_ai_model_route_channel_model','fk_ai_model_route_model','fk_ai_model_route_channel','fk_ai_model_route_operator','chk_ai_model_route_status','chk_ai_model_route_values','fk_ai_route_runtime_route','uk_ai_gateway_rejection_request_reason','chk_ai_gateway_rejection_reason')" "15" "named_constraints"
assert_scalar "SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id JOIN permissions p ON p.id=rp.permission_id WHERE r.code='admin' AND p.code IN ('ai_gateway:model_manage','ai_gateway:price_manage','ai_gateway:route_manage')" "3" "admin_permission_bindings"

assert_scalar "SELECT COUNT(*) FROM ai_model_route_runtime_states s JOIN ai_model_routes r ON r.id=s.route_id WHERE r.logical_model_code='molin/g5-test' AND s.consecutive_failures=5 AND s.circuit_open_until>NOW()" "1" "route_runtime_fact"
assert_rejected "UPDATE token_channels SET health_status='invalid' WHERE id=901" "health_check"
assert_rejected "INSERT INTO ai_model_routes(logical_model_code,channel_id,provider_model,priority,weight,timeout_ms,max_retries,circuit_breaker_threshold,fallback_order,status,updated_by) VALUES ('molin/g5-test',901,'openrouter/test/model',100,100,30000,0,5,0,'active',901)" "route_unique"
assert_rejected "INSERT INTO ai_model_routes(logical_model_code,channel_id,provider_model,priority,weight,timeout_ms,max_retries,circuit_breaker_threshold,fallback_order,status,updated_by) VALUES ('molin/missing',999999,'openrouter/missing',100,100,30000,0,5,0,'active',901)" "route_foreign_key"
assert_rejected "UPDATE ai_model_routes SET weight=0 WHERE logical_model_code='molin/g5-test'" "route_check"

# 使用项目正式采用的 golang-migrate MySQL driver 在第二个隔离库执行完整 up/down/up，验证真实版本和 dirty 处理链路。
migrate_database="molin_g5_migrate_driver"
docker exec -i -e "MYSQL_PWD=${password}" "${container}" mysql -uroot -e "CREATE DATABASE ${migrate_database} CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"
migrate_image="${G5_MIGRATE_IMAGE:-migrate/migrate:v4.18.3}"
migrate_run() {
  docker run --rm --pull="${G5_MIGRATE_PULL_POLICY:-missing}" --network "container:${container}" \
    -v "${repo_root}/server/migrations:/migrations:ro" \
    "${migrate_image}" -path=/migrations -database "mysql://root:${password}@tcp(127.0.0.1:3306)/${migrate_database}?multiStatements=true" "$@"
}
migrate_version() {
  # migrate 镜像会把 version 输出到标准错误，合并后仅提取最后一个纯数字版本号。
  migrate_run version 2>&1 | tr -d '\r' | awk '/^[0-9]+$/ { version=$0 } END { print version }'
}
migrate_run up
[[ "$(migrate_version)" == "64" ]] || { echo "G5_VERIFY=FAILED reason=migrate_driver_up_version"; exit 2; }
migrate_run down 1
[[ "$(migrate_version)" == "63" ]] || { echo "G5_VERIFY=FAILED reason=migrate_driver_down_version"; exit 2; }
migrate_run up 1
[[ "$(migrate_version)" == "64" ]] || { echo "G5_VERIFY=FAILED reason=migrate_driver_reup_version"; exit 2; }

# 在同一个临时 MySQL 网络命名空间中运行真实仓储集成测试，覆盖指标 SQL、熔断和并发版本控制。
docker run --rm --network "container:${container}" \
  -v "${repo_root}:/src:ro" \
  -w /src/server \
  -e GOPROXY=https://goproxy.cn,direct \
  -e G5_ISOLATED_TEST=YES \
  -e "G5_MYSQL_DSN=root:${password}@tcp(127.0.0.1:3306)/${database}?charset=utf8mb4&parseTime=True&loc=UTC" \
  golang:1.25 go test -count=1 ./internal/modules/token_gateway/repository -run '^TestG5MySQLIntegration$'

echo "G5_VERIFY=PASS mysql=8.0 isolated=true schema=64:0 migrations=63_to_64_repeated_up_down_to_63_reup_to_64 migration_driver=golang-migrate model_release=true route_constraints=true route_runtime=true rejection_metrics=true concurrency=true go_integration=true permissions=true admin_bindings=true project_database=false"
