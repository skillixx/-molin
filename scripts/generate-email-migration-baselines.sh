#!/bin/bash
# 生成 DirectMail 000055/000056 隔离矩阵所需的六项受控基线。
set -Eeuo pipefail
umask 077
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

readonly confirm_phrase=I_CONFIRM_EMAIL_MIGRATION_BASELINE_GENERATION_ONCE
readonly execute_gate=I_UNDERSTAND_TEMPORARY_NETWORKLESS_MYSQL8_WILL_BE_CREATED
readonly expected_migration_set_sha=DE8D942A3C8BBB3E96456C1B85AE0BADAE7542E2A3E6FE0C34FD47C6140D914D
readonly script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly repository_root=$(cd -- "$script_dir/.." && pwd -P)
readonly migration_dir="$repository_root/server/migrations"
readonly docker_bin=/usr/bin/docker
readonly sha256sum_bin=/usr/bin/sha256sum
readonly awk_link=/usr/bin/awk
readonly grep_bin=/usr/bin/grep
readonly stat_bin=/usr/bin/stat
readonly realpath_bin=/usr/bin/realpath
readonly cat_bin=/usr/bin/cat
readonly mkdir_bin=/usr/bin/mkdir
readonly chmod_bin=/usr/bin/chmod
readonly rmdir_bin=/usr/bin/rmdir
readonly rm_bin=/usr/bin/rm
awk_bin=$awk_link

stage=gate
container_id=
container_name=
temporary_dir=
output_dir=
dump_raw=
created_outputs=()

blocked() {
  printf 'status=blocked mode=email_migration_baseline_generation reason=explicit_double_gate_required docker_access=false database_access=false migration_executed=false outputs_created=false\n'
  exit 2
}

migration_set_sha() {
  local index version file file_sha
  for ((index=1; index<=56; index++)); do
    printf -v version '%06d' "$index"
    local matches=("$migration_dir/${version}_"*.up.sql)
    [[ ${#matches[@]} -eq 1 && -f ${matches[0]} && ! -L ${matches[0]} ]]
    file=${matches[0]}
    [[ $(basename -- "$file") =~ ^[0-9]{6}_[a-z0-9_]+\.up\.sql$ ]]
    file_sha=$($sha256sum_bin -- "$file" | $awk_bin 'NR==1{print toupper($1);next}{exit 1}END{if(NR!=1)exit 1}')
    [[ "$file_sha" =~ ^[A-F0-9]{64}$ ]]
    printf '%s\t%s\n' "$file_sha" "$(basename -- "$file")"
  done | $sha256sum_bin | $awk_bin 'NR==1{print toupper($1);next}{exit 1}END{if(NR!=1)exit 1}'
}

if [[ ${1:-} = --self-test && $# -eq 1 ]]; then
  [[ "$(migration_set_sha)" = "$expected_migration_set_sha" ]]
  printf 'status=pass mode=email_migration_baseline_generation_selftest migrations=56 external_access=false docker_access=false database_access=false migration_executed=false outputs_created=false\n'
  exit 0
fi
[[ $# -eq 2 ]] || blocked
[[ $1 = --execute && $2 = "$confirm_phrase" ]] || blocked
[[ ${MOLIN_EMAIL_BASELINE_GENERATION_EXECUTE:-} = "$execute_gate" ]] || blocked

fail() {
  local classification=${1:?classification_required}
  printf 'status=failed mode=email_migration_baseline_generation stage=%s classification=%s outputs_created=false retained=false\n' "$stage" "$classification"
  exit 2
}

cleanup() {
  local exit_code=$?
  trap - EXIT
  if [[ -n "$container_id" ]]; then
    "$docker_bin" rm --force -- "$container_id" >/dev/null 2>&1 || true
  fi
  if [[ $exit_code -ne 0 ]]; then
    local output
    for output in "${created_outputs[@]}"; do
      [[ -f "$output" && ! -L "$output" ]] && "$rm_bin" -f -- "$output"
    done
  fi
  if [[ -n "$temporary_dir" && "$temporary_dir" =~ ^/tmp/molin-email-baseline-[a-f0-9]{32}$ && -d "$temporary_dir" && ! -L "$temporary_dir" ]]; then
    case "$dump_raw" in
      "$temporary_dir/schema54-empty.sql.raw"|"$temporary_dir/schema54-legacy.sql.raw"|"$temporary_dir/schema55.sql.raw"|"$temporary_dir/schema56.sql.raw")
        [[ -f "$dump_raw" && ! -L "$dump_raw" ]] && "$rm_bin" -f -- "$dump_raw"
        ;;
    esac
    local file
    for file in schema54-empty.sql schema54-legacy.sql schema55.sql schema56.sql 000055-baseline-manifest.tsv 000056-baseline-manifest.tsv docker.stderr mysql.stderr; do
      [[ -f "$temporary_dir/$file" && ! -L "$temporary_dir/$file" ]] && "$rm_bin" -f -- "$temporary_dir/$file"
    done
    "$rmdir_bin" -- "$temporary_dir" 2>/dev/null || true
  fi
  exit "$exit_code"
}
trap cleanup EXIT

stage=local_preflight
[[ $(uname -s) = Linux ]]
for tool in "$docker_bin" "$sha256sum_bin" "$grep_bin" "$stat_bin" "$realpath_bin" "$cat_bin" "$mkdir_bin" "$chmod_bin" "$rmdir_bin" "$rm_bin"; do
  [[ -x "$tool" && ! -L "$tool" ]]
done
[[ -x "$awk_link" ]]
awk_bin=$("$realpath_bin" -e -- "$awk_link")
[[ "$awk_bin" =~ ^/usr/bin/[A-Za-z0-9._+-]+$ ]]
[[ -f "$awk_bin" && ! -L "$awk_bin" && -x "$awk_bin" ]]
[[ "$("$stat_bin" -c '%U:%G' -- "$awk_bin")" = root:root ]]
readonly awk_bin
[[ "$(migration_set_sha)" = "$expected_migration_set_sha" ]]
readonly image_ref=${MOLIN_MYSQL8_IMAGE_REF:-}
readonly expected_image_id=${MOLIN_MYSQL8_IMAGE_ID:-}
[[ "$image_ref" =~ ^mysql@sha256:[a-f0-9]{64}$ ]]
[[ "$expected_image_id" =~ ^sha256:[a-f0-9]{64}$ ]]
[[ "$("$docker_bin" image inspect --format '{{.Id}}' "$image_ref" 2>/dev/null)" = "$expected_image_id" ]] || fail image_identity

output_dir=${MOLIN_BASELINE_OUTPUT_DIR:-}
[[ "$output_dir" = /* && -d "$output_dir" && ! -L "$output_dir" ]]
output_dir=$(cd -- "$output_dir" && pwd -P)
[[ "$output_dir" != / && "$output_dir" != /tmp && $($stat_bin -c %u -- "$output_dir") = "$EUID" && $($stat_bin -c %a -- "$output_dir") = 700 ]]
[[ -z "$(find "$output_dir" -mindepth 1 -maxdepth 1 -print -quit)" ]] || fail output_not_empty

readonly nonce=$(tr -d '-' < /proc/sys/kernel/random/uuid)
[[ "$nonce" =~ ^[a-f0-9]{32}$ ]]
temporary_dir="/tmp/molin-email-baseline-$nonce"
container_name="molin-email-baseline-$nonce"
[[ ! -e "$temporary_dir" ]]
"$mkdir_bin" --mode=0700 -- "$temporary_dir"
: > "$temporary_dir/docker.stderr"
: > "$temporary_dir/mysql.stderr"
"$chmod_bin" 600 "$temporary_dir/docker.stderr" "$temporary_dir/mysql.stderr"

stage=container_start
set +e
container_id=$("$docker_bin" run --detach --name "$container_name" --label "molin.phase4.baseline=$nonce" \
  --network none --read-only --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g \
  --tmpfs /var/run/mysqld:rw,noexec,nosuid,size=16m --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --env MYSQL_ALLOW_EMPTY_PASSWORD=yes "$image_ref" --skip-log-bin 2>"$temporary_dir/docker.stderr")
start_exit=$?
set -e
[[ $start_exit -eq 0 && "$container_id" =~ ^[a-f0-9]{64}$ ]] || fail container_start
: > "$temporary_dir/docker.stderr"
[[ "$("$docker_bin" inspect --format '{{.Name}}|{{.HostConfig.NetworkMode}}|{{.Config.Image}}' "$container_id")" = "/$container_name|none|$image_ref" ]] || fail container_identity

stage=mysql_ready
ready=false
for _ in $(seq 1 60); do
  if "$docker_bin" exec "$container_id" mysqladmin --no-defaults --protocol=socket --user=root ping >/dev/null 2>&1; then ready=true; break; fi
  sleep 1
done
[[ "$ready" = true ]] || fail mysql_ready
mysql_version=$("$docker_bin" exec "$container_id" mysql --no-defaults --version 2>/dev/null) || fail mysql_version
[[ "$mysql_version" =~ ^mysql[[:space:]]+Ver[[:space:]]+8\.[0-9]+\.[0-9]+[[:space:]]+for[[:space:]]+Linux[[:space:]]+on[[:space:]]+[A-Za-z0-9_.-]+[[:space:]]+\(MySQL[[:space:]]+Community[[:space:]]+Server[[:space:]]+-[[:space:]]+GPL\)$ ]] || fail mysql_version
unset mysql_version

mysql_admin() {
  "$docker_bin" exec --interactive "$container_id" mysql --no-defaults --default-character-set=utf8mb4 --protocol=socket --user=root --batch --skip-column-names --raw
}

mysql_database() {
  "$docker_bin" exec --interactive "$container_id" mysql --no-defaults --default-character-set=utf8mb4 --protocol=socket --user=root --database=molin_baseline --batch --skip-column-names --raw
}

mysql_scalar() {
  local sql=${1:?sql_required}
  printf '%s\n' "$sql" | mysql_database 2>"$temporary_dir/mysql.stderr"
}

apply_migration() {
  local version=${1:?version_required} file mysql_error mysql_error_code mysql_sqlstate mysql_sql_line check_fingerprints
  file=("$migration_dir/${version}_"*.up.sql)
  [[ ${#file[@]} -eq 1 ]]
  if ! mysql_database < "${file[0]}" 2>"$temporary_dir/mysql.stderr"; then
    mysql_error=$("$awk_bin" 'NR==1{print; exit}' "$temporary_dir/mysql.stderr")
    if [[ "$mysql_error" =~ ^ERROR[[:space:]]+([0-9]{4})[[:space:]]+\(([A-Z0-9]{5})\)[[:space:]]+at[[:space:]]+line[[:space:]]+([0-9]{1,6}): ]]; then
      mysql_error_code=${BASH_REMATCH[1]}
      mysql_sqlstate=${BASH_REMATCH[2]}
      mysql_sql_line=${BASH_REMATCH[3]}
      # 仅对已冻结的 000056 CHECK 断言失败输出长度和哈希，不泄露约束原文。
      if [[ "$version:$mysql_error_code:$mysql_sqlstate:$mysql_sql_line" = 000056:3819:HY000:113 ]]; then
        check_fingerprints=$(mysql_scalar "SELECT GROUP_CONCAT(CONCAT(OCTET_LENGTH(clause_compact), ':', LOWER(SHA2(clause_compact, 256))) ORDER BY FIELD(constraint_name, 'chk_verification_code_hash', 'chk_verification_send_status', 'chk_verification_target_type', 'chk_verification_target_shape', 'chk_verification_email_acceptance', 'chk_verification_email_idempotency', 'chk_verification_request_fingerprint', 'chk_verification_target_hash') SEPARATOR ',') FROM (SELECT tc.constraint_name, REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(cc.check_clause, CONCAT(CHAR(92), CHAR(39)), CHAR(39)), '_utf8mb4', ''), ' ', ''), CHAR(9), ''), CHAR(10), ''), CHAR(13), '') AS clause_compact FROM information_schema.table_constraints tc JOIN information_schema.check_constraints cc ON cc.constraint_schema = tc.constraint_schema AND cc.constraint_name = tc.constraint_name WHERE tc.table_schema = DATABASE() AND tc.table_name = 'verification_codes' AND tc.constraint_type = 'CHECK' AND tc.enforced = 'YES' AND tc.constraint_name IN ('chk_verification_code_hash', 'chk_verification_send_status', 'chk_verification_target_type', 'chk_verification_target_shape', 'chk_verification_email_acceptance', 'chk_verification_email_idempotency', 'chk_verification_request_fingerprint', 'chk_verification_target_hash')) fixed_checks") || fail check_fingerprint_query
        if [[ "$check_fingerprints" =~ ^[0-9]+:[a-f0-9]{64}(,[0-9]+:[a-f0-9]{64}){7}$ ]]; then
          printf 'status=failed mode=email_migration_baseline_generation stage=%s classification=migration_sql mysql_error_code=%s sqlstate=%s sql_line=%s check_fingerprints=%s outputs_created=false retained=false\n' \
            "$stage" "$mysql_error_code" "$mysql_sqlstate" "$mysql_sql_line" "$check_fingerprints"
          exit 2
        fi
        fail check_fingerprint_shape
      fi
      printf 'status=failed mode=email_migration_baseline_generation stage=%s classification=migration_sql mysql_error_code=%s sqlstate=%s sql_line=%s outputs_created=false retained=false\n' \
        "$stage" "$mysql_error_code" "$mysql_sqlstate" "$mysql_sql_line"
      exit 2
    fi
    fail migration_sql
  fi
  : > "$temporary_dir/mysql.stderr"
}

dump_database() {
  local destination=${1:?destination_required} sanitize_exit
  [[ ! -e "$destination" && ! -L "$destination" ]]
  dump_raw="${destination}.raw"
  [[ ! -e "$dump_raw" && ! -L "$dump_raw" ]]
  "$docker_bin" exec "$container_id" mysqldump --no-defaults --default-character-set=utf8mb4 --protocol=socket --user=root \
    --single-transaction --quick --skip-lock-tables --skip-add-locks --skip-disable-keys \
    --compact --skip-comments --skip-dump-date --hex-blob --order-by-primary \
    --set-gtid-purged=OFF --no-tablespaces molin_baseline > "$dump_raw" 2>"$temporary_dir/mysql.stderr" || fail dump_failed
  : > "$temporary_dir/mysql.stderr"
  [[ -s "$dump_raw" && ! -L "$dump_raw" ]]
  set +e
  "$awk_bin" '
    $0 == "/*!999999" sprintf("%c", 92) "- enable the sandbox mode */" { next }
    /^\/\*!40101 SET @saved_cs_client[[:space:]]*=[[:space:]]*@@character_set_client \*\/;$/ { next }
    /^\/\*!50503 SET character_set_client[[:space:]]*=[[:space:]]*utf8mb4 \*\/;$/ { next }
    /^\/\*!40101 SET character_set_client[[:space:]]*=[[:space:]]*@saved_cs_client \*\/;$/ { next }
    /\/\*!|\/\*\+/ { exit 42 }
    { print }
  ' "$dump_raw" > "$destination"
  sanitize_exit=$?
  set -e
  [[ $sanitize_exit -eq 0 ]] || fail dump_executable_comment
  "$rm_bin" -f -- "$dump_raw"
  dump_raw=
  [[ -s "$destination" && ! -L "$destination" ]]
  # 仅移除 MySQL 固定 sandbox 与字符集包装行，其余可执行注释继续失败关闭。
  if LC_ALL=C "$grep_bin" -Eq '/\*!|/\*\+' "$destination"; then fail dump_executable_comment; fi
  if LC_ALL=C "$grep_bin" -Eiq '^[[:space:]]*(USE|CREATE[[:space:]]+(DATABASE|SCHEMA)|DROP[[:space:]]+(DATABASE|SCHEMA)|GRANT|REVOKE|CREATE[[:space:]]+USER|ALTER[[:space:]]+USER|DROP[[:space:]]+USER|SET[[:space:]]+GLOBAL)[[:space:]]' "$destination"; then fail dump_scope; fi
}

stage=schema54_build
printf '%s\n' 'CREATE DATABASE molin_baseline CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;' | mysql_admin 2>"$temporary_dir/mysql.stderr" || fail database_create
for index in $(seq 1 54); do printf -v version '%06d' "$index"; apply_migration "$version"; done
printf '%s\n' 'CREATE TABLE schema_migrations (version BIGINT NOT NULL PRIMARY KEY, dirty BOOLEAN NOT NULL); INSERT INTO schema_migrations(version,dirty) VALUES(54,0);' | mysql_database 2>"$temporary_dir/mysql.stderr" || fail version_seed
[[ "$(mysql_scalar 'SELECT CONCAT(version,":",dirty) FROM schema_migrations;')" = 54:0 ]]
[[ "$(mysql_scalar 'SELECT COUNT(*) FROM verification_codes;')" = 0 ]]
[[ "$(mysql_scalar "SELECT COUNT(*) FROM roles WHERE code='admin';")" = 1 ]]
dump_database "$temporary_dir/schema54-empty.sql"

stage=schema54_legacy
cat <<'SQL' | mysql_database 2>"$temporary_dir/mysql.stderr" || fail legacy_fixture
INSERT INTO verification_codes(id,target_type,target_value,code,scene,expires_at,used_at,created_at) VALUES
  (900001,'email','phase4-baseline@example.com','A1B2C3','login','2025-01-01 00:10:00',NULL,'2025-01-01 00:00:00'),
  (900002,'phone','phase4-phone-fixture','D4E5F6','register','2025-01-01 00:10:00',NULL,'2025-01-01 00:00:00'),
  (900003,'phone','phase4-legacy-code','A1B2C3D4E5F6G7H8','reset_password','2025-01-01 00:10:00',NULL,'2025-01-01 00:00:00');
SQL
[[ "$(mysql_scalar "SELECT COUNT(*) FROM verification_codes WHERE target_type='email';")" = 1 ]]
[[ "$(mysql_scalar "SELECT COUNT(*) FROM verification_codes WHERE target_type='phone';")" = 2 ]]
[[ "$(mysql_scalar 'SELECT COUNT(*) FROM verification_codes WHERE CHAR_LENGTH(code)=16;')" = 1 ]]
dump_database "$temporary_dir/schema54-legacy.sql"

stage=schema55_build
printf '%s\n' 'UPDATE schema_migrations SET dirty=1 WHERE version=54 AND dirty=0;' | mysql_database 2>"$temporary_dir/mysql.stderr" || fail version_dirty
apply_migration 000055
printf '%s\n' 'UPDATE schema_migrations SET version=55,dirty=0 WHERE version=54 AND dirty=1;' | mysql_database 2>"$temporary_dir/mysql.stderr" || fail version_commit
[[ "$(mysql_scalar 'SELECT CONCAT(version,":",dirty) FROM schema_migrations;')" = 55:0 ]]
[[ "$(mysql_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='email_admin_verify_bootstrap_receipts';")" = 0 ]]
dump_database "$temporary_dir/schema55.sql"

stage=schema56_build
printf '%s\n' 'UPDATE schema_migrations SET dirty=1 WHERE version=55 AND dirty=0;' | mysql_database 2>"$temporary_dir/mysql.stderr" || fail version_dirty
apply_migration 000056
printf '%s\n' 'UPDATE schema_migrations SET version=56,dirty=0 WHERE version=55 AND dirty=1;' | mysql_database 2>"$temporary_dir/mysql.stderr" || fail version_commit
[[ "$(mysql_scalar 'SELECT CONCAT(version,":",dirty) FROM schema_migrations;')" = 56:0 ]]
[[ "$(mysql_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('email_admin_verify_bootstrap_receipts','migration_000056_permission_ownership');")" = 2 ]]
dump_database "$temporary_dir/schema56.sql"

stage=manifest_create
sha54_empty=$($sha256sum_bin "$temporary_dir/schema54-empty.sql" | $awk_bin '{print toupper($1)}')
sha54_legacy=$($sha256sum_bin "$temporary_dir/schema54-legacy.sql" | $awk_bin '{print toupper($1)}')
sha55=$($sha256sum_bin "$temporary_dir/schema55.sql" | $awk_bin '{print toupper($1)}')
sha56=$($sha256sum_bin "$temporary_dir/schema56.sql" | $awk_bin '{print toupper($1)}')
printf 'schema54-empty.sql\t%s\t54\tempty\nschema54-legacy.sql\t%s\t54\tlegacy\nschema55.sql\t%s\t55\tcomplete\n' "$sha54_empty" "$sha54_legacy" "$sha55" > "$temporary_dir/000055-baseline-manifest.tsv"
printf 'schema55.sql\t%s\t55\tcomplete\nschema56.sql\t%s\t56\tcomplete\n' "$sha55" "$sha56" > "$temporary_dir/000056-baseline-manifest.tsv"

stage=publish
set -o noclobber
for name in schema54-empty.sql schema54-legacy.sql schema55.sql schema56.sql 000055-baseline-manifest.tsv 000056-baseline-manifest.tsv; do
  destination="$output_dir/$name"
  : > "$destination"
  created_outputs+=("$destination")
  "$cat_bin" "$temporary_dir/$name" >> "$destination"
  "$chmod_bin" 400 "$destination"
done
set +o noclobber

stage=complete
printf 'status=pass mode=email_migration_baseline_generation migrations=56 mysql8_image_bound=true mysql8_runtime_verified=true network=none outputs=6 schema54_empty=true schema54_legacy=true schema55=true schema56=true manifests=2 container_removed_on_exit=true\n'
