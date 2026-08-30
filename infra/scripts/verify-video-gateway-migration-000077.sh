#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "${VIDEO_GATEWAY_G5_ISOLATED_MYSQL_APPROVED:-NO}" != "YES" ]]; then
  echo "VIDEO_G5_MYSQL=APPROVAL_REQUIRED target=isolated_temporary_container project_database=false"
  exit 3
fi

command -v docker >/dev/null 2>&1 || { echo "VIDEO_G5_MYSQL=FAILED reason=docker_missing"; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "VIDEO_G5_MYSQL=FAILED reason=openssl_missing"; exit 2; }
command -v awk >/dev/null 2>&1 || { echo "VIDEO_G5_MYSQL=FAILED reason=awk_missing"; exit 2; }

# 故障定位可只跑取消用例，但不能把局部结果标为完整切片回归；最终验收始终使用默认all。
test_focus="${VIDEO_GATEWAY_G5_TEST_FOCUS:-all}"
test_flags=()
test_package='./internal/modules/token_gateway/service'
test_extra_packages=()
compatibility_seed=''
required_tests=''
test_scope='implemented_video_g5_tests'
case "$test_focus" in
  all) test_filter='^TestVideoG5(Reserve|Usage|Cancel|Media|Settle|Release|Compensation|Delivery|Reconciliation|Unknown|Submission|Adjustment|Golden|Compatibility)' ;;
  cancel) test_filter='^TestVideoG5Cancel' ;;
  unknown) test_filter='^TestVideoG5Unknown' ;;
  submission) test_filter='^TestVideoG5Submission' ;;
  adjustment) test_filter='^TestVideoG5Adjustment' ;;
  facade) test_filter='^TestVideoG5ReserveMySQL(CrossFacade|AutoQuote|Facade)' ;;
  golden) test_filter='^TestVideoG5Golden'; test_flags=(-v) ;;
  terminal_race) test_filter='^TestVideoG5SettleMySQLOppositeFinancialTerminalRace$'; test_flags=(-v) ;;
  outbox_integrity) test_filter='^TestVideoG5ReconciliationMySQLRejectsForeignFinancialOutbox$'; test_flags=(-v) ;;
  compatibility) test_filter='^TestVideoG5Compatibility'; test_flags=(-v) ;;
  compatibility_cleanup) test_filter='^TestImageG7ObjectCleanupRepositoryMySQLRetryBoundary$'; test_package='./internal/modules/token_gateway/repository'; test_scope='legacy_image_cleanup_mysql_only' ;;
  compatibility_chat_g4) test_filter='^TestG4MySQLBudgetIntegration$'; test_flags=(-v); test_scope='legacy_chat_g4_mysql_only'; compatibility_seed='video-g5-legacy-chat-g4-governance.sql' ;;
  compatibility_chat_g5) test_filter='^TestG5MySQLIntegration$'; test_package='./internal/modules/token_gateway/repository'; test_flags=(-v); test_scope='legacy_chat_g5_mysql_only'; compatibility_seed='video-g5-legacy-chat-g5-admin.sql' ;;
  compatibility_chat_g6) test_filter='^TestG6User(RepositoryMySQLIsolation|ServiceMySQLReconciledDetail)$'; test_package='./internal/modules/token_gateway/repository'; test_extra_packages=('./internal/modules/token_gateway/service'); test_flags=(-v -p 1); test_scope='legacy_chat_g6_mysql_only'; compatibility_seed='video-g5-legacy-chat-g6-customer.sql' ;;
  compatibility_chat_g7) test_filter='^TestG7MySQLReliabilityIntegration$'; test_flags=(-v); test_scope='legacy_chat_g7_mysql_fake_http'; compatibility_seed='video-g5-legacy-chat-g7-reliability.sql' ;;
  *) echo "VIDEO_G5_MYSQL=FAILED reason=invalid_test_focus"; exit 2 ;;
esac

case "$test_focus" in
  compatibility_chat_g4) required_tests='TestG4MySQLBudgetIntegration' ;;
  compatibility_chat_g5) required_tests='TestG5MySQLIntegration' ;;
  compatibility_chat_g6) required_tests='TestG6UserRepositoryMySQLIsolation,TestG6UserServiceMySQLReconciledDetail' ;;
  compatibility_chat_g7) required_tests='TestG7MySQLReliabilityIntegration' ;;
  outbox_integrity) required_tests='TestVideoG5ReconciliationMySQLRejectsForeignFinancialOutbox' ;;
esac

repo_root="$(cd "$(dirname "$BASH_SOURCE")/../.." && pwd)"
suffix="$RANDOM-$$"
network_name="molin-video-g5-$suffix"
container_name="molin-video-g5-mysql-$suffix"
build_cache_volume="molin-video-g5-go-build-$suffix"
database_name="molin_video_g5_contract"
compatibility_env=()
if [[ "$test_focus" == "compatibility_cleanup" ]]; then
  # 旧图片清理测试固定校验库名；仍是本脚本创建的无端口、无出口临时MySQL，不启动对象存储或队列。
  database_name="molin_image_g7_contract"
fi
if [[ "$test_focus" == "compatibility_chat_g7" ]]; then
  # 原G7用例强制隔离库名前缀；每次仍新建独立MySQL，不复用视频金样所在库。
  database_name="molin_g7_reliability_${suffix//-/_}"
fi
root_password="$(openssl rand -hex 24)"
if [[ "$test_focus" == "compatibility_cleanup" ]]; then
  compatibility_env=(-e MOLIN_IMAGE_G7_ISOLATED=YES -e "MOLIN_IMAGE_G7_MYSQL_DSN=root:$root_password@tcp(mysql:3306)/$database_name?charset=utf8mb4&parseTime=true&loc=UTC")
fi
compatibility_dsn="root:$root_password@tcp(mysql:3306)/$database_name?charset=utf8mb4&parseTime=true&loc=UTC"
case "$test_focus" in
  compatibility_chat_g4) compatibility_env=(-e G4_ISOLATED_TEST=YES -e "G4_MYSQL_DSN=$compatibility_dsn") ;;
  compatibility_chat_g5) compatibility_env=(-e G5_ISOLATED_TEST=YES -e "G5_MYSQL_DSN=$compatibility_dsn") ;;
  compatibility_chat_g6) compatibility_env=(-e "AI_GATEWAY_G6_MYSQL_DSN=$compatibility_dsn") ;;
  compatibility_chat_g7) compatibility_env=(-e G7_ISOLATED_TEST=YES -e "G7_ISOLATED_DATABASE=$database_name" -e "G7_MYSQL_DSN=$compatibility_dsn") ;;
esac
go_mod_cache="$(go env GOMODCACHE)"
docker_repo_root="$repo_root"
docker_go_mod_cache="$go_mod_cache"
if command -v cygpath >/dev/null 2>&1; then
  docker_repo_root="$(cygpath -w "$repo_root")"
  docker_go_mod_cache="$(cygpath -w "$go_mod_cache")"
fi

cleanup() {
  docker container inspect "$container_name" >/dev/null 2>&1 && docker container rm -f "$container_name" >/dev/null || true
  docker network inspect "$network_name" >/dev/null 2>&1 && docker network rm "$network_name" >/dev/null || true
  docker volume inspect "$build_cache_volume" >/dev/null 2>&1 && docker volume rm "$build_cache_volume" >/dev/null || true
}
trap cleanup EXIT

# 使用无出口内部网络、无宿主端口与tmpfs，所有写入只存在于一次性MySQL容器。
docker network create --internal "$network_name" >/dev/null
docker run -d --pull=never --network "$network_name" --network-alias mysql \
  --name "$container_name" --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g \
  -e "MYSQL_ROOT_PASSWORD=$root_password" -e "MYSQL_DATABASE=$database_name" \
  mysql:8.0 --character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci >/dev/null
docker exec "$container_name" mkdir -p /migrations
docker cp "$repo_root/server/migrations/." "$container_name:/migrations" >/dev/null

mysql_exec() {
  docker exec -i -e "MYSQL_PWD=$root_password" "$container_name" \
    mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database="$database_name" --batch --skip-column-names "$@"
}
apply_file() {
  docker exec -e "MYSQL_PWD=$root_password" "$container_name" sh -c \
    "mysql --protocol=socket --default-character-set=utf8mb4 -uroot --database='$database_name' < '/migrations/$1'"
}

ready_count=0
for _ in $(seq 1 90); do
  if mysql_exec -e 'SELECT 1' >/dev/null 2>&1; then ready_count=$((ready_count + 1)); else ready_count=0; fi
  [[ "$ready_count" -ge 2 ]] && break
  sleep 1
done
[[ "$ready_count" -ge 2 ]] || { echo "VIDEO_G5_MYSQL=FAILED reason=mysql_not_ready"; exit 2; }

for path in "$repo_root"/server/migrations/*.up.sql; do
  base="$(basename "$path")"
  version="$(printf '%s' "$base" | cut -d_ -f1)"
  version=$((10#$version))
  [[ "$version" -le 77 ]] && apply_file "$base" >/dev/null
done

apply_file "000077_video_billing_outbox_reconcile.up.sql" >/dev/null
apply_file "000077_video_billing_outbox_reconcile.down.sql" >/dev/null
apply_file "000077_video_billing_outbox_reconcile.up.sql" >/dev/null

column_count="$(mysql_exec -e "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_requests' AND column_name IN ('command_kind','intent_key_hash','intent_version','rights_policy_version')")"
[[ "$column_count" == "4" ]] || { echo "VIDEO_G5_MYSQL=FAILED reason=intent_columns actual=$column_count"; exit 2; }
index_count="$(mysql_exec -e "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_requests' AND index_name='uk_ai_requests_video_intent'")"
[[ "$index_count" == "4" ]] || { echo "VIDEO_G5_MYSQL=FAILED reason=intent_index actual=$index_count"; exit 2; }

if [[ -n "$compatibility_seed" ]]; then
  # 旧Chat测试有全表清理和固定ID，必须只装入本次新建的空夹具库，不能与视频任务事实混跑。
  mysql_exec < "$repo_root/infra/scripts/fixtures/$compatibility_seed" >/dev/null
fi

# 在同一无出口网络运行GORM与race；只写合成钱包，不访问真实Provider、队列、对象存储或公网。
MSYS_NO_PATHCONV=1 docker run --rm --pull=never --network "$network_name" \
  --mount "type=bind,src=$docker_repo_root,dst=/src,readonly" \
  --mount "type=bind,src=$docker_go_mod_cache,dst=/go/pkg/mod,readonly" \
  -v "$build_cache_volume:/root/.cache/go-build" -w /src/server -e CGO_ENABLED=1 -e GOPROXY=off \
  -e MOLIN_VIDEO_G5_ISOLATED=YES \
  -e "MOLIN_VIDEO_G5_MYSQL_DSN=root:$root_password@tcp(mysql:3306)/$database_name?charset=utf8mb4&parseTime=true&loc=UTC" \
  "${compatibility_env[@]}" \
  golang:1.25-bookworm go test -race -count=1 "${test_flags[@]}" "$test_package" "${test_extra_packages[@]}" -run "$test_filter" \
  | awk -v "required=$required_tests" -f "$repo_root/infra/scripts/verify-video-g5-test-execution.awk"

echo "VIDEO_G5_MYSQL_SLICE=PASS focus=$test_focus scope=$test_scope full_stage=false full_chain_1_to_77=true repeat_up=true down_retained=true reup=true race=true external_http_requests=0 real_provider_calls=0 real_wallet_writes=0 cost_cny=0 test_server_writes=0 production_operations=0"
