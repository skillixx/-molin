#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${IMAGE_GATEWAY_G1_MYSQL_MIGRATION_APPROVED:-NO}" != "YES" ]]; then
  echo "IMAGE_G1_MYSQL_MIGRATION=APPROVAL_REQUIRED target=isolated_temporary_container project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "IMAGE_G1_MYSQL_MIGRATION=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "IMAGE_G1_MYSQL_MIGRATION=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
up_file="${repo_root}/server/migrations/000068_expand_image_gateway_schema.up.sql"
down_file="${repo_root}/server/migrations/000068_expand_image_gateway_schema.down.sql"
test -f "${up_file}" || { echo "IMAGE_G1_MYSQL_MIGRATION=FAILED reason=up_file_missing"; exit 2; }
test -f "${down_file}" || { echo "IMAGE_G1_MYSQL_MIGRATION=FAILED reason=down_file_missing"; exit 2; }

container_name="molin-image-g1-mysql-000068-${RANDOM}-$$"
database_name="molin_image_g1_contract"
root_password="$(openssl rand -hex 24)"

cleanup() {
  if docker container inspect "${container_name}" >/dev/null 2>&1; then
    docker container rm -f "${container_name}" >/dev/null
  fi
}
trap cleanup EXIT

# 只使用本机已有镜像；容器不联网、不映射端口，数据使用 tmpfs，退出时只删除本轮精确容器。
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
    mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database="${database_name}" --batch --skip-column-names "$@"
}

for _ in $(seq 1 60); do
  if mysql_exec -e 'SELECT 1' >/dev/null 2>&1; then break; fi
  sleep 1
done
mysql_exec -e 'SELECT 1' >/dev/null 2>&1 || { echo "IMAGE_G1_MYSQL_MIGRATION=FAILED reason=mysql_not_ready"; exit 2; }

# 构造精确的 schema67 依赖面，避免连接项目库，也避免把无关历史 migration 的外部依赖带入本阶段。
mysql_exec <<'SQL'
CREATE TABLE users (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE ai_projects (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  name VARCHAR(191) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_projects_id_user (id, user_id),
  CONSTRAINT fk_ai_projects_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE api_keys (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_api_keys_id_project_user (id, project_id, user_id),
  CONSTRAINT fk_api_keys_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
  CONSTRAINT fk_api_keys_project_owner FOREIGN KEY (project_id, user_id) REFERENCES ai_projects(id, user_id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE token_models (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  logical_model_code VARCHAR(128) NOT NULL,
  modality VARCHAR(32) NOT NULL DEFAULT 'chat',
  PRIMARY KEY (id),
  UNIQUE KEY uk_token_models_code (logical_model_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE ai_price_versions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  logical_model_code VARCHAR(128) NOT NULL,
  version_no BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_price_model_version (logical_model_code, version_no),
  CONSTRAINT fk_ai_price_model FOREIGN KEY (logical_model_code) REFERENCES token_models(logical_model_code) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE ai_requests (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  request_id VARCHAR(128) NOT NULL,
  idempotency_key VARCHAR(191) NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NULL,
  api_key_id BIGINT UNSIGNED NULL,
  logical_model_code VARCHAR(128) NOT NULL,
  modality VARCHAR(32) NOT NULL DEFAULT 'chat',
  is_stream TINYINT(1) NOT NULL DEFAULT 0,
  moderation_status VARCHAR(32) NOT NULL DEFAULT 'pending',
  execution_status VARCHAR(32) NOT NULL DEFAULT 'pending',
  billing_status VARCHAR(32) NOT NULL DEFAULT 'unquoted',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_requests_request_id (request_id),
  UNIQUE KEY uk_ai_requests_request_user (request_id, user_id),
  UNIQUE KEY uk_ai_requests_user_idempotency (user_id, idempotency_key),
  CONSTRAINT fk_ai_requests_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_requests_project_owner FOREIGN KEY (project_id, user_id) REFERENCES ai_projects(id, user_id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_requests_key_owner FOREIGN KEY (api_key_id, project_id, user_id) REFERENCES api_keys(id, project_id, user_id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_requests_modality CHECK (modality = 'chat')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE ai_usage_items (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  request_id VARCHAR(128) NOT NULL,
  meter_type VARCHAR(64) NOT NULL,
  source VARCHAR(32) NOT NULL,
  sequence_no INT UNSIGNED NOT NULL DEFAULT 0,
  quantity DECIMAL(30,10) NOT NULL,
  unit_price DECIMAL(20,8) NULL,
  amount DECIMAL(20,8) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_usage_request_meter_source_seq (request_id, meter_type, source, sequence_no),
  CONSTRAINT fk_ai_usage_request FOREIGN KEY (request_id) REFERENCES ai_requests(request_id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE schema_migrations (version BIGINT NOT NULL PRIMARY KEY, dirty BOOLEAN NOT NULL);
INSERT INTO schema_migrations(version, dirty) VALUES(67, 0);

INSERT INTO users(id) VALUES(1),(2);
INSERT INTO ai_projects(id,user_id,name) VALUES(1,1,'图片项目'),(2,2,'其他项目');
INSERT INTO api_keys(id,user_id,project_id) VALUES(1,1,1),(2,2,2);
INSERT INTO token_models(id,logical_model_code,modality) VALUES(1,'molin/chat-test','chat'),(2,'molin/image-test','image');
INSERT INTO ai_price_versions(id,logical_model_code,version_no) VALUES(1,'molin/chat-test',1),(2,'molin/image-test',1);
INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code,modality) VALUES('chat-before-68',1,1,1,'molin/chat-test','chat');
INSERT INTO ai_usage_items(request_id,meter_type,source,sequence_no,quantity) VALUES('chat-before-68','input_tokens','provider',0,12);
SQL

docker cp "${up_file}" "${container_name}:/tmp/000068.up.sql" >/dev/null
docker cp "${down_file}" "${container_name}:/tmp/000068.down.sql" >/dev/null

apply_file() {
  local file="$1"
  docker exec -e "MYSQL_PWD=${root_password}" "${container_name}" sh -c \
    "mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database='${database_name}' < '${file}'"
}

assert_scalar() {
  local sql="$1" expected="$2" label="$3" actual
  actual="$(mysql_exec -e "${sql}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "IMAGE_G1_MYSQL_MIGRATION=FAILED reason=${label} expected=${expected} actual=${actual}"
    exit 2
  fi
}

apply_file /tmp/000068.up.sql
mysql_exec -e 'UPDATE schema_migrations SET version=68,dirty=0 WHERE version=67 AND dirty=0'

assert_scalar "SELECT CONCAT(version,':',dirty) FROM schema_migrations" "68:0" "first_up_version"
assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('ai_gateway_quotes','ai_gateway_tasks','ai_gateway_assets')" "3" "image_table_count"
assert_scalar "SELECT CONCAT(modality,':',capability,':',delivery_status) FROM ai_requests WHERE request_id='chat-before-68'" "chat:chat.completions:not_applicable" "existing_chat_defaults"
assert_scalar "SELECT CONCAT(record_kind,':',variant_hash,':',usage_unit,':',unit_size) FROM ai_usage_items WHERE request_id='chat-before-68'" "legacy_chat:0000000000000000000000000000000000000000000000000000000000000000:tokens:1.0000000000" "existing_chat_usage_defaults"

# 模拟旧 Chat 二进制：不提交任何新列时仍应使用安全默认值并保持原幂等唯一约束。
mysql_exec -e "INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code,modality) VALUES('chat-after-68',1,1,1,'molin/chat-test','chat'); INSERT INTO ai_usage_items(request_id,meter_type,source,sequence_no,quantity) VALUES('chat-after-68','input_tokens','provider',0,5);"
if mysql_exec -e "INSERT INTO ai_usage_items(request_id,meter_type,source,sequence_no,quantity) VALUES('chat-after-68','input_tokens','provider',0,5)" >/dev/null 2>&1; then
  echo "IMAGE_G1_MYSQL_MIGRATION=FAILED reason=legacy_chat_usage_idempotency"
  exit 2
fi

if mysql_exec -e "INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code,modality,capability,delivery_status,is_stream) VALUES('image-stream',1,1,1,'molin/image-test','image','image.generate','pending',1)" >/dev/null 2>&1; then
  echo "IMAGE_G1_MYSQL_MIGRATION=FAILED reason=image_stream_allowed"
  exit 2
fi
if mysql_exec -e "INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code,modality,capability,delivery_status) VALUES('image-wrong-owner',1,2,2,'molin/image-test','image','image.generate','pending')" >/dev/null 2>&1; then
  echo "IMAGE_G1_MYSQL_MIGRATION=FAILED reason=cross_tenant_image_request_allowed"
  exit 2
fi

mysql_exec -e "INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code,modality,capability,delivery_status) VALUES('image-valid',1,1,1,'molin/image-test','image','image.generate','pending');"
mysql_exec -e "INSERT INTO ai_gateway_quotes(id,public_id,user_id,project_id,api_key_id,logical_model_code,request_fingerprint,price_version_id,price_snapshot_json,quoted_amount,currency,expires_at) VALUES(1,'quote-public',1,1,1,'molin/image-test',REPEAT('a',64),2,JSON_OBJECT('schema_version',2),0.50000000,'CNY',DATE_ADD(NOW(),INTERVAL 5 MINUTE));"
mysql_exec -e "UPDATE ai_gateway_quotes SET consumed_request_id='image-valid',consumed_at=NOW() WHERE id=1; INSERT INTO ai_gateway_tasks(id,public_id,request_id,quote_id,user_id,project_id,api_key_id,logical_model_code,status,progress,input_json) VALUES(1,'task-public','image-valid',1,1,1,1,'molin/image-test','created',0,JSON_OBJECT('resolution','2K'));"

if mysql_exec -e "INSERT INTO ai_gateway_tasks(public_id,request_id,quote_id,user_id,project_id,api_key_id,logical_model_code,status,progress,input_json) VALUES('task-cross','image-valid',1,2,2,2,'molin/image-test','created',0,JSON_OBJECT())" >/dev/null 2>&1; then
  echo "IMAGE_G1_MYSQL_MIGRATION=FAILED reason=cross_tenant_task_allowed"
  exit 2
fi
if mysql_exec -e "INSERT INTO ai_gateway_assets(public_id,user_id,project_id,request_id,task_id,result_index,asset_role,is_billable_output,bucket,object_key,mime_type,size_bytes,sha256,width,height,source,moderation_status,explicit_label_status,implicit_label_status,lifecycle_state,retention_policy_id,expires_at) VALUES('asset-unlabelled',1,1,'image-valid',1,0,'primary_output',1,'ai-result','object','image/jpeg',100,REPEAT('b',64),2048,2048,'provider_base64','passed','pending','pending','available','result-30d',DATE_ADD(NOW(),INTERVAL 30 DAY))" >/dev/null 2>&1; then
  echo "IMAGE_G1_MYSQL_MIGRATION=FAILED reason=unlabelled_asset_available"
  exit 2
fi

mysql_exec -e "INSERT INTO ai_gateway_assets(public_id,user_id,project_id,request_id,task_id,result_index,asset_role,is_billable_output,bucket,object_key,mime_type,size_bytes,sha256,width,height,source,moderation_status,explicit_label_status,implicit_label_status,lifecycle_state,retention_policy_id,expires_at) VALUES('asset-valid',1,1,'image-valid',1,0,'primary_output',1,'ai-result','object','image/jpeg',100,REPEAT('b',64),2048,2048,'provider_base64','passed','applied','applied','available','result-30d',DATE_ADD(NOW(),INTERVAL 30 DAY));"
if mysql_exec -e "INSERT INTO ai_gateway_assets(public_id,user_id,project_id,request_id,task_id,result_index,asset_role,is_billable_output,source,moderation_status,lifecycle_state,retention_policy_id,expires_at) VALUES('asset-duplicate',1,1,'image-valid',1,0,'primary_output',0,'provider_base64','pending','temporary','temp-24h',DATE_ADD(NOW(),INTERVAL 1 DAY))" >/dev/null 2>&1; then
  echo "IMAGE_G1_MYSQL_MIGRATION=FAILED reason=duplicate_primary_asset_allowed"
  exit 2
fi

mysql_exec -e "INSERT INTO ai_usage_items(request_id,meter_type,source,record_kind,price_version_id,variant_hash,variant_json,sequence_no,quantity,usage_unit,unit_size,unit_price,amount,currency) VALUES('image-valid','image_count','gateway','sale_line',2,REPEAT('c',64),JSON_OBJECT('resolution','2K'),0,1,'count',1,0.50000000,0.50000000,'CNY');"
if mysql_exec -e "INSERT INTO ai_usage_items(request_id,meter_type,source,record_kind,price_version_id,variant_hash,variant_json,sequence_no,quantity,usage_unit,unit_size,unit_price,amount,currency) VALUES('image-valid','image_count','gateway','sale_line',2,REPEAT('c',64),JSON_OBJECT('resolution','2K'),0,1,'count',1,0.50000000,0.50000000,'CNY')" >/dev/null 2>&1; then
  echo "IMAGE_G1_MYSQL_MIGRATION=FAILED reason=image_usage_idempotency"
  exit 2
fi

apply_file /tmp/000068.down.sql
mysql_exec -e 'UPDATE schema_migrations SET version=67,dirty=0 WHERE version=68 AND dirty=0'
assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('ai_gateway_quotes','ai_gateway_tasks','ai_gateway_assets')" "3" "down_fact_retention"
assert_scalar "SELECT COUNT(*) FROM ai_gateway_assets WHERE public_id='asset-valid'" "1" "down_asset_retention"

apply_file /tmp/000068.up.sql
mysql_exec -e 'UPDATE schema_migrations SET version=68,dirty=0 WHERE version=67 AND dirty=0'
assert_scalar "SELECT CONCAT(version,':',dirty) FROM schema_migrations" "68:0" "reup_version"
assert_scalar "SELECT COUNT(*) FROM ai_gateway_quotes WHERE public_id='quote-public'" "1" "reup_quote_retention"

echo "IMAGE_G1_MYSQL_MIGRATION=PASS mysql=8.0 isolated=true project_database=false first_up=true legacy_chat=true image_constraints=true labels_fail_closed=true down_retained=true reup=true"
