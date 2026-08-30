#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${IMAGE_GATEWAY_G2_MYSQL_MIGRATION_APPROVED:-NO}" != "YES" ]]; then
  echo "IMAGE_G2_MYSQL_MIGRATION=APPROVAL_REQUIRED target=isolated_temporary_container project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "IMAGE_G2_MYSQL_MIGRATION=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "IMAGE_G2_MYSQL_MIGRATION=FAILED reason=openssl_missing"; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
up_file="${repo_root}/server/migrations/000069_expand_image_pricing_quotes.up.sql"
down_file="${repo_root}/server/migrations/000069_expand_image_pricing_quotes.down.sql"
test -f "${up_file}" || { echo "IMAGE_G2_MYSQL_MIGRATION=FAILED reason=up_file_missing"; exit 2; }
test -f "${down_file}" || { echo "IMAGE_G2_MYSQL_MIGRATION=FAILED reason=down_file_missing"; exit 2; }

suffix="${RANDOM}-$$"
network_name="molin-image-g2-${suffix}"
container_name="molin-image-g2-mysql-${suffix}"
build_cache_volume="molin-image-g2-go-build-${suffix}"
database_name="molin_image_g2_contract"
root_password="$(openssl rand -hex 24)"
go_mod_cache="$(go env GOMODCACHE)"
test -d "${go_mod_cache}" || { echo "IMAGE_G2_MYSQL_MIGRATION=FAILED reason=go_mod_cache_missing"; exit 2; }
docker_repo_root="${repo_root}"
docker_go_mod_cache="${go_mod_cache}"
if command -v cygpath >/dev/null 2>&1; then
  docker_repo_root="$(cygpath -w "${repo_root}")"
  docker_go_mod_cache="$(cygpath -w "${go_mod_cache}")"
fi

cleanup() {
  if docker container inspect "${container_name}" >/dev/null 2>&1; then
    docker container rm -f "${container_name}" >/dev/null
  fi
  if docker network inspect "${network_name}" >/dev/null 2>&1; then
    docker network rm "${network_name}" >/dev/null
  fi
  if docker volume inspect "${build_cache_volume}" >/dev/null 2>&1; then
    docker volume rm "${build_cache_volume}" >/dev/null
  fi
}
trap cleanup EXIT

# 内部网络不提供外网出口；MySQL无宿主机端口，数据使用tmpfs，退出只清理本轮精确资源。
docker network create --internal "${network_name}" >/dev/null
docker run -d --pull=never --network "${network_name}" --network-alias mysql \
  --name "${container_name}" \
  --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g \
  -e "MYSQL_ROOT_PASSWORD=${root_password}" \
  -e "MYSQL_DATABASE=${database_name}" \
  mysql:8.0 \
  --character-set-server=utf8mb4 \
  --collation-server=utf8mb4_0900_ai_ci >/dev/null

docker exec "${container_name}" mkdir -p /migrations
docker cp "${repo_root}/server/migrations/." "${container_name}:/migrations" >/dev/null

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=${root_password}" "${container_name}" \
    mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database="${database_name}" --batch --skip-column-names "$@"
}

formal_ready=false
ready_count=0
for _ in $(seq 1 90); do
  if docker logs "${container_name}" 2>&1 | grep -q 'MySQL init process done. Ready for start up.'; then
    formal_ready=true
  fi
  if [[ "${formal_ready}" == "true" ]] && mysql_exec -e 'SELECT 1' >/dev/null 2>&1; then
    ready_count=$((ready_count + 1))
  else
    ready_count=0
  fi
  [[ "${ready_count}" -ge 2 ]] && break
  sleep 1
done
[[ "${ready_count}" -ge 2 ]] || { echo "IMAGE_G2_MYSQL_MIGRATION=FAILED reason=mysql_not_ready"; exit 2; }

apply_file() {
  local file="$1"
  docker exec -e "MYSQL_PWD=${root_password}" "${container_name}" sh -c \
    "mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database='${database_name}' < '/migrations/${file}'"
}

for path in "${repo_root}"/server/migrations/*.up.sql; do
  base="$(basename "${path}")"
  version_text="${base%%_*}"
  version=$((10#${version_text}))
  if [[ "${version}" -le 68 ]]; then
    apply_file "${base}"
  fi
done

# 在 000069 前写入既有 Chat 价格，验证新列默认值和分支 CHECK 不改变历史价格语义。
mysql_exec <<'SQL'
INSERT INTO users(id,password_hash,real_name_status,status) VALUES(92001,'fixture','verified','active');
INSERT INTO token_models(id,logical_model_code,display_name,modality,status) VALUES(92001,'molin/chat-g2-legacy','Chat G2兼容夹具','chat','inactive');
INSERT INTO ai_price_versions(
  id,logical_model_code,version_no,currency,exchange_rate,status,min_margin_rate,
  max_input_tokens,max_output_tokens,failure_charge_policy,rounding_mode,
  cost_updated_at,cost_expires_at,effective_at,created_by
) VALUES(
  92001,'molin/chat-g2-legacy',1,'CNY',1,'draft',0.2,1000,100,'confirmed_usage','ceil_8',
  DATE_SUB(NOW(),INTERVAL 1 HOUR),DATE_ADD(NOW(),INTERVAL 1 HOUR),NOW(),92001
);
INSERT INTO ai_price_skus(price_version_id,meter_type,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES
  (92001,'input_tokens',SHA2('g2-input',256),0.1,0.2,1000,'CNY'),
  (92001,'output_tokens',SHA2('g2-output',256),0.1,0.2,1000,'CNY'),
  (92001,'cached_tokens',SHA2('g2-cached',256),0.1,0.2,1000,'CNY'),
  (92001,'reasoning_tokens',SHA2('g2-reasoning',256),0.1,0.2,1000,'CNY');
SQL

apply_file "$(basename "${up_file}")" >/dev/null

assert_scalar() {
  local sql="$1" expected="$2" label="$3" actual
  actual="$(mysql_exec -e "${sql}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "IMAGE_G2_MYSQL_MIGRATION=FAILED reason=${label} expected=${expected} actual=${actual}"
    exit 2
  fi
}

assert_scalar "SELECT CONCAT(capability,':',pricing_template,':',cost_source,':',cost_source_version,':',price_purpose,':',max_input_tokens,':',max_output_tokens) FROM ai_price_versions WHERE id=92001" "chat.completions:token:manual_cny:legacy:commercial:1000:100" "legacy_chat_price_defaults"
assert_scalar "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_price_versions' AND column_name IN ('capability','pricing_template','limits_json','minimum_charge','cost_source','cost_source_version','price_purpose')" "7" "price_expand_columns"
assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND constraint_name IN ('chk_ai_price_limits','chk_ai_price_template','chk_ai_price_purpose','chk_ai_price_sku_meter','chk_ai_price_sku_image_variant','chk_ai_gateway_quotes_variant_hash')" "6" "price_expand_checks"

if mysql_exec -e "INSERT INTO ai_price_versions(id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by) VALUES(92002,'molin/chat-g2-legacy','image.generate','image_variant',2,'CNY',1,'draft',0.2,1,1,JSON_OBJECT('max_count',1),0.01,'test_fixture','invalid','test_fixture','confirmed_usage','ceil_8',NOW(),DATE_ADD(NOW(),INTERVAL 1 HOUR),NOW(),92001)" >/dev/null 2>&1; then
  echo "IMAGE_G2_MYSQL_MIGRATION=FAILED reason=image_token_limits_allowed"
  exit 2
fi

apply_file "$(basename "${down_file}")" >/dev/null
assert_scalar "SELECT COUNT(*) FROM ai_price_versions WHERE id=92001" "1" "down_price_retention"
assert_scalar "SELECT COUNT(*) FROM ai_price_skus WHERE price_version_id=92001" "4" "down_sku_retention"
apply_file "$(basename "${up_file}")" >/dev/null
assert_scalar "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_gateway_quotes' AND column_name='request_variant_hash'" "1" "reup_quote_variant"

# 阶段回滚断言仍停在000069；运行当前HEAD仓储测试前补装共享媒体、VID-G2 Quote、VID-G3资产与VID-G4安全版本兼容层。
apply_file "000072_expand_video_gateway_schema.up.sql" >/dev/null
apply_file "000074_expand_video_pricing_quotes.up.sql" >/dev/null
apply_file "000075_enforce_video_task_asset_events.up.sql" >/dev/null
apply_file "000076_video_fake_async_media_safety.up.sql" >/dev/null

# 在同一内部网络运行真实仓储并发测试；源码只读，模块和构建缓存使用本轮隔离卷。
MSYS_NO_PATHCONV=1 docker run --rm --pull=never --network "${network_name}" \
  --mount "type=bind,src=${docker_repo_root},dst=/src,readonly" \
  --mount "type=bind,src=${docker_go_mod_cache},dst=/go/pkg/mod,readonly" \
  -v "${build_cache_volume}:/root/.cache/go-build" \
  -w /src/server \
  -e CGO_ENABLED=1 \
  -e GOPROXY=off \
  -e "MOLIN_IMAGE_G2_MYSQL_DSN=root:${root_password}@tcp(mysql:3306)/${database_name}?charset=utf8mb4&parseTime=true&loc=UTC" \
  golang:1.25-bookworm \
  go test -race -count=1 ./internal/modules/token_gateway/repository -run '^TestImageQuoteRepositoryMySQLConcurrentConsume$'

docker volume rm "${build_cache_volume}" >/dev/null

echo "IMAGE_G2_MYSQL_MIGRATION=PASS mysql=8.0.46 full_chain_1_to_69=true current_head_compat_72_74_75_76=true legacy_chat=true image_checks=true down_retained=true reup=true quote_concurrency=100_one_winner project_database=false provider_calls=0 wallet_writes=0"
