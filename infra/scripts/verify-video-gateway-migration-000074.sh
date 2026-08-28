#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${VIDEO_GATEWAY_G2_MYSQL_MIGRATION_APPROVED:-NO}" != "YES" ]]; then
  echo "VIDEO_G2_MYSQL_MIGRATION=APPROVAL_REQUIRED target=isolated_temporary_container project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "VIDEO_G2_MYSQL_MIGRATION=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "VIDEO_G2_MYSQL_MIGRATION=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
suffix="${RANDOM}-$$"
network_name="molin-video-g2-${suffix}"
container_name="molin-video-g2-mysql-${suffix}"
build_cache_volume="molin-video-g2-go-build-${suffix}"
database_name="molin_video_g2_contract"
root_password="$(openssl rand -hex 24)"
go_mod_cache="$(go env GOMODCACHE)"
docker_repo_root="${repo_root}"
docker_go_mod_cache="${go_mod_cache}"
if command -v cygpath >/dev/null 2>&1; then
  docker_repo_root="$(cygpath -w "${repo_root}")"
  docker_go_mod_cache="$(cygpath -w "${go_mod_cache}")"
fi

cleanup() {
  docker container inspect "${container_name}" >/dev/null 2>&1 && docker container rm -f "${container_name}" >/dev/null || true
  docker network inspect "${network_name}" >/dev/null 2>&1 && docker network rm "${network_name}" >/dev/null || true
  docker volume inspect "${build_cache_volume}" >/dev/null 2>&1 && docker volume rm "${build_cache_volume}" >/dev/null || true
}
trap cleanup EXIT

# 无出口内部网络、无宿主端口和tmpfs保证本验收不会接触项目数据库或持久数据。
docker network create --internal "${network_name}" >/dev/null
docker run -d --pull=never --network "${network_name}" --network-alias mysql \
  --name "${container_name}" --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g \
  -e "MYSQL_ROOT_PASSWORD=${root_password}" -e "MYSQL_DATABASE=${database_name}" \
  mysql:8.0 --character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci >/dev/null
docker exec "${container_name}" mkdir -p /migrations
docker cp "${repo_root}/server/migrations/." "${container_name}:/migrations" >/dev/null

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=${root_password}" "${container_name}" \
    mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database="${database_name}" --batch --skip-column-names "$@"
}
apply_file() {
  docker exec -e "MYSQL_PWD=${root_password}" "${container_name}" sh -c \
    "mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database='${database_name}' < '/migrations/$1'"
}
assert_scalar() {
  local sql="$1" expected="$2" label="$3" actual
  actual="$(mysql_exec -e "${sql}")"
  [[ "${actual}" == "${expected}" ]] || { echo "VIDEO_G2_MYSQL_MIGRATION=FAILED reason=${label} expected=${expected} actual=${actual}"; exit 2; }
}

ready_count=0
for _ in $(seq 1 90); do
  if mysql_exec -e 'SELECT 1' >/dev/null 2>&1; then ready_count=$((ready_count + 1)); else ready_count=0; fi
  [[ "${ready_count}" -ge 2 ]] && break
  sleep 1
done
[[ "${ready_count}" -ge 2 ]] || { echo "VIDEO_G2_MYSQL_MIGRATION=FAILED reason=mysql_not_ready"; exit 2; }

for path in "${repo_root}"/server/migrations/*.up.sql; do
  base="$(basename "${path}")"
  version=$((10#${base%%_*}))
  [[ "${version}" -le 73 ]] && apply_file "${base}" >/dev/null
done

# 在000074前写入旧图片价格与Quote，验证新增列和CHECK不会改写既有事实。
mysql_exec <<'SQL'
INSERT INTO users(id,password_hash,real_name_status,status) VALUES(92999,'fixture','verified','active');
INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(92999,92999,'旧图片兼容项目','active','disabled','Asia/Shanghai');
INSERT INTO token_models(id,logical_model_code,display_name,modality,status) VALUES(92999,'molin/image-vid-g2-legacy','旧图片兼容模型','image','inactive');
INSERT INTO token_models(id,logical_model_code,display_name,modality,status) VALUES(92998,'molin/chat-vid-g2-legacy','旧文字兼容模型','chat','inactive');
INSERT INTO ai_price_versions(id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by)
VALUES(92998,'molin/chat-vid-g2-legacy','chat.completions','token',1,'CNY',1,'draft',0.2,1000,100,NULL,0.01,'manual_cny','legacy-chat','commercial','confirmed_usage','ceil_8',NOW(),DATE_ADD(NOW(),INTERVAL 1 HOUR),NOW(),92999);
INSERT INTO ai_price_skus(price_version_id,meter_type,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES
(92998,'input_tokens',SHA2('legacy-chat-input',256),0.1,0.2,1000,'CNY'),
(92998,'output_tokens',SHA2('legacy-chat-output',256),0.1,0.2,1000,'CNY'),
(92998,'cached_tokens',SHA2('legacy-chat-cached',256),0.1,0.2,1000,'CNY'),
(92998,'reasoning_tokens',SHA2('legacy-chat-reasoning',256),0.1,0.2,1000,'CNY');
INSERT INTO ai_price_versions(id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by)
VALUES(92999,'molin/image-vid-g2-legacy','image.generate','image_variant',1,'CNY',1,'draft',0.2,NULL,NULL,JSON_OBJECT('max_count',1,'variants',JSON_ARRAY(JSON_OBJECT('resolution','2K','aspect_ratio','1:1','quality','standard','output_format','provider_default','delivery','url'))),0.01,'test_fixture','legacy-image','test_fixture','confirmed_usage','ceil_8',NOW(),DATE_ADD(NOW(),INTERVAL 1 HOUR),NOW(),92999);
INSERT INTO ai_price_skus(price_version_id,meter_type,variant_json,variant_hash,cost_unit_price,sale_unit_price,scale,currency)
VALUES(92999,'image_count',JSON_OBJECT('resolution','2K','aspect_ratio','1:1','quality','standard','output_format','provider_default','delivery','url'),SHA2('legacy-image-variant',256),0.3,0.5,1,'CNY');
INSERT INTO ai_gateway_quotes(id,public_id,user_id,project_id,logical_model_code,capability,operation,request_fingerprint,request_variant_hash,price_version_id,price_snapshot_json,quoted_amount,currency,expires_at,created_at)
VALUES(92999,'quote_legacy_image_vid_g2',92999,92999,'molin/image-vid-g2-legacy','image.generate',NULL,SHA2('legacy-image-request',256),SHA2('legacy-image-variant',256),92999,JSON_OBJECT('schema_version',2),0.5,'CNY',DATE_ADD(NOW(),INTERVAL 1 HOUR),NOW());
SQL

apply_file "000074_expand_video_pricing_quotes.up.sql" >/dev/null

assert_scalar "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_gateway_quotes' AND column_name IN ('command_kind','idempotency_key')" "2" "quote_columns"
assert_scalar "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_gateway_quotes' AND index_name='uk_ai_gateway_quotes_idempotency'" "4" "quote_unique_index"
assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND constraint_name IN ('chk_ai_price_video_fixture_only','chk_ai_gateway_quotes_command_scope')" "2" "video_g2_checks"
assert_scalar "SELECT CONCAT(capability,':',quoted_amount,':',COALESCE(command_kind,'NULL'),':',COALESCE(idempotency_key,'NULL')) FROM ai_gateway_quotes WHERE id=92999" "image.generate:0.50000000:NULL:NULL" "legacy_image_quote_unchanged"
assert_scalar "SELECT CONCAT(capability,':',pricing_template,':',price_purpose) FROM ai_price_versions WHERE id=92998" "chat.completions:token:commercial" "legacy_chat_price_unchanged"
assert_scalar "SELECT COUNT(*) FROM ai_price_skus WHERE price_version_id=92998" "4" "legacy_chat_skus_unchanged"

mysql_exec <<'SQL'
INSERT INTO users(id,password_hash,real_name_status,status) VALUES(93001,'fixture','verified','active');
INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(93001,93001,'视频G2隔离项目','active','disabled','Asia/Shanghai');
INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status)
VALUES(93001,93001,93001,'fixture','vid-g2-fixture-hash','视频G2隔离密钥','postpaid','','allowlist','active');
INSERT INTO token_models(id,logical_model_code,display_name,modality,status)
VALUES(93001,'molin/video-g2-mysql','视频G2隔离模型','video','inactive');
SQL

limits_json='{"meter_type":"video_seconds","variants":[{"operation":"text_to_video","resolution":"1280x720","duration_seconds":5,"aspect_ratio":"16:9","frame_rate":24,"audio":false},{"operation":"image_to_video","resolution":"1280x720","duration_seconds":5,"aspect_ratio":"16:9","frame_rate":24,"audio":false}]}'
mysql_exec -e "INSERT INTO ai_price_versions(id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by) VALUES(93001,'molin/video-g2-mysql','video.generate','video_seconds',1,'CNY',1,'active',0.2,NULL,NULL,'${limits_json}',0.1,'non_commercial_test_fixture','vid-g2-fixture','non_commercial_test_fixture','confirmed_usage','ceil_8',NOW(),DATE_ADD(NOW(),INTERVAL 1 HOUR),NOW(),93001)"
mysql_exec <<'SQL'
INSERT INTO ai_price_skus(price_version_id,meter_type,variant_json,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES
(93001,'video_seconds',JSON_OBJECT('operation','text_to_video','resolution','1280x720','duration_seconds',5,'aspect_ratio','16:9','frame_rate',24,'audio',FALSE),SHA2('vid-g2-t2v',256),0.06,0.10,1,'CNY'),
(93001,'video_seconds',JSON_OBJECT('operation','image_to_video','resolution','1280x720','duration_seconds',5,'aspect_ratio','16:9','frame_rate',24,'audio',FALSE),SHA2('vid-g2-i2v',256),0.06,0.10,1,'CNY');
SQL
assert_scalar "SELECT COUNT(DISTINCT JSON_UNQUOTE(JSON_EXTRACT(variant_json,'$.operation'))) FROM ai_price_skus WHERE price_version_id=93001" "2" "operation_prices_independent"

if mysql_exec -e "INSERT INTO ai_price_versions(id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by) VALUES(93002,'molin/video-g2-mysql','video.generate','video_seconds',2,'CNY',1,'active',0.2,NULL,NULL,'${limits_json}',0.1,'manual_cny','forbidden','commercial','confirmed_usage','ceil_8',NOW(),DATE_ADD(NOW(),INTERVAL 1 HOUR),NOW(),93001)" >/dev/null 2>&1; then
  echo "VIDEO_G2_MYSQL_MIGRATION=FAILED reason=commercial_video_price_activated"
  exit 2
fi

# 重复up必须成功，保留式down后列、索引、价格和Quote事实仍存在。
apply_file "000074_expand_video_pricing_quotes.up.sql" >/dev/null
apply_file "000074_expand_video_pricing_quotes.down.sql" >/dev/null
assert_scalar "SELECT COUNT(*) FROM ai_price_skus WHERE price_version_id=93001" "2" "down_price_retention"
apply_file "000074_expand_video_pricing_quotes.up.sql" >/dev/null

# 在同一内部网络运行真实GORM并发：100并发幂等创建一条，100并发消费一个赢家。
MSYS_NO_PATHCONV=1 docker run --rm --pull=never --network "${network_name}" \
  --mount "type=bind,src=${docker_repo_root},dst=/src,readonly" \
  --mount "type=bind,src=${docker_go_mod_cache},dst=/go/pkg/mod,readonly" \
  -v "${build_cache_volume}:/root/.cache/go-build" -w /src/server -e CGO_ENABLED=1 -e GOPROXY=off \
  -e "MOLIN_VIDEO_G2_MYSQL_DSN=root:${root_password}@tcp(mysql:3306)/${database_name}?charset=utf8mb4&parseTime=true&loc=UTC" \
  golang:1.25-bookworm go test -race -count=1 ./internal/modules/token_gateway/repository \
  -run '^TestVideoQuoteRepositoryMySQLConcurrentCreateAndConsume$'

MSYS_NO_PATHCONV=1 docker run --rm --pull=never --network "${network_name}" \
  --mount "type=bind,src=${docker_repo_root},dst=/src,readonly" \
  --mount "type=bind,src=${docker_go_mod_cache},dst=/go/pkg/mod,readonly" \
  -v "${build_cache_volume}:/root/.cache/go-build" -w /src/server -e CGO_ENABLED=1 -e GOPROXY=off \
  -e "MOLIN_VIDEO_G2_MYSQL_DSN=root:${root_password}@tcp(mysql:3306)/${database_name}?charset=utf8mb4&parseTime=true&loc=UTC" \
  golang:1.25-bookworm go test -race -count=1 ./internal/modules/token_gateway/service \
  -run '^TestVideoReservationServiceMySQLAtomicQuoteHoldAndTask$'

echo "VIDEO_G2_MYSQL_MIGRATION=PASS mysql=8 full_chain_1_to_74=true repeat_up=true down_retained=true reup=true legacy_chat_image=true t2v_i2v_unique=true quote_create_concurrency=100_one_fact quote_consume_concurrency=100_one_winner auto_explicit_atomic_reservation=true project_database=false provider_calls=0 real_wallet_writes=0 fixture_wallet_writes=true cost_cny=0"
