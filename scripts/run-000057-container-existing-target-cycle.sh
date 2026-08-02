#!/bin/bash
set -Eeuo pipefail
umask 077
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

readonly source_db=molin
readonly target_db=molin_restore_57_reverify_8fb6f25611b25d07a563f15105d0906a
readonly work_dir=/root/molin-000057-container-cycle
readonly evidence_dir="$work_dir/evidence-existing-target-cycle"
readonly up_file="$work_dir/000057_fix_email_datetime_utc_seconds.up.sql"
readonly down_file="$work_dir/000057_fix_email_datetime_utc_seconds.down.sql"
readonly expected_up_sha=50DCD97A45D8ADCF2F7CAC316B44D942DDB880D4F922B8872CAA34BA01CFC67C
readonly expected_down_sha=C7B7BF363DFBD214BCC3A53F84364F99162E00301CE4D04129796DF06FD5CAC1
readonly MYSQL_BIN=/usr/bin/mysql
readonly SHA256SUM_BIN=/usr/bin/sha256sum
readonly AWK_BIN=/usr/bin/awk
readonly STAT_BIN=/usr/bin/stat
readonly TOUCH_BIN=/usr/bin/touch
readonly CHMOD_BIN=/usr/bin/chmod
readonly MKDIR_BIN=/usr/bin/mkdir

stage=initialization
fail() { false; }
on_error() {
  local exit_code=$?
  trap - ERR
  printf 'cycle_completed=false\nfailure_stage=%s\n' "$stage"
  exit "$exit_code"
}
trap on_error ERR

# 仅接受容器内既有的 root 密码环境变量，并拒绝覆盖旧证据目录。
stage=environment_precheck
[[ $EUID -eq 0 ]]
[[ -n "${MYSQL_ROOT_PASSWORD:-}" ]]
for tool in "$MYSQL_BIN" "$SHA256SUM_BIN" "$AWK_BIN" "$STAT_BIN" "$TOUCH_BIN" "$CHMOD_BIN" "$MKDIR_BIN"; do
  [[ -x "$tool" ]]
done
[[ -d "$work_dir" && ! -L "$work_dir" ]]
[[ ! -e "$evidence_dir" ]]
for sql_file in "$up_file" "$down_file"; do [[ -f "$sql_file" && ! -L "$sql_file" ]]; done
"$MKDIR_BIN" --mode=0700 -- "$evidence_dir"
[[ "$($STAT_BIN -c %u -- "$evidence_dir")" = 0 ]]
[[ "$($STAT_BIN -c %a -- "$evidence_dir")" = 700 ]]

verify_sql_hash() {
  local actual_hash
  actual_hash=$("$SHA256SUM_BIN" -- "$1" | "$AWK_BIN" '{print toupper($1)}')
  [[ "$actual_hash" = "$2" ]]
}
files_equal() {
  local left_file=$1 right_file=$2 left_hash right_hash
  [[ -f "$left_file" && ! -L "$left_file" && -r "$left_file" ]] || return 1
  [[ -f "$right_file" && ! -L "$right_file" && -r "$right_file" ]] || return 1
  left_hash=$("$SHA256SUM_BIN" -- "$left_file" | "$AWK_BIN" 'NR == 1 { hash = toupper($1); next } { exit 1 } END { if (NR != 1) exit 1; print hash }') || return 1
  right_hash=$("$SHA256SUM_BIN" -- "$right_file" | "$AWK_BIN" 'NR == 1 { hash = toupper($1); next } { exit 1 } END { if (NR != 1) exit 1; print hash }') || return 1
  [[ "$left_hash" =~ ^[A-F0-9]{64}$ ]] || return 1
  [[ "$right_hash" =~ ^[A-F0-9]{64}$ ]] || return 1
  [[ "$left_hash" = "$right_hash" ]]
}
verify_sql_hash "$up_file" "$expected_up_sha"
verify_sql_hash "$down_file" "$expected_down_sha"

mysql_query_db() {
  local database_name=$1 sql=$2
  MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --host=127.0.0.1 --port=3306 --user=root --database="$database_name" --batch --skip-column-names --raw --execute="$sql" 2>/dev/null
}
mysql_query() { mysql_query_db "$target_db" "$1"; }
mysql_admin_query() {
  MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --host=127.0.0.1 --port=3306 --user=root --batch --skip-column-names --raw --execute="$1" 2>/dev/null
}
mysql_from_file() {
  MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --host=127.0.0.1 --port=3306 --user=root --database="$target_db" --batch --skip-column-names --raw >/dev/null 2>/dev/null < "$1"
}
assert_scalar() {
  local actual
  actual=$(mysql_query "$1")
  [[ "$actual" = "$2" ]]
}
version_dirty() { mysql_query "SELECT CONCAT(version, ':', dirty) FROM schema_migrations;"; }
source_version_dirty() { mysql_query_db "$source_db" "SELECT CONCAT(version, ':', dirty) FROM schema_migrations;"; }
assert_source_state_unchanged() {
  [[ $(source_version_dirty) = 56:0 ]]
  [[ "$(mysql_query_db "$source_db" "SELECT COUNT(*) FROM schema_migrations;")" = 1 ]]
  [[ "$(mysql_query_db "$source_db" "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE';")" = 68 ]]
  [[ "$(mysql_query_db "$source_db" "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' AND engine <> 'InnoDB';")" = 0 ]]
  [[ "$(mysql_query_db "$source_db" "SELECT COUNT(*) FROM information_schema.views WHERE table_schema = DATABASE();")" = 0 ]]
  [[ "$(mysql_query_db "$source_db" "SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema = DATABASE();")" = 0 ]]
  [[ "$(mysql_query_db "$source_db" "SELECT COUNT(*) FROM information_schema.routines WHERE routine_schema = DATABASE();")" = 0 ]]
  [[ "$(mysql_query_db "$source_db" "SELECT COUNT(*) FROM information_schema.events WHERE event_schema = DATABASE();")" = 0 ]]
}

schema56_shape() {
  assert_scalar "SELECT IF(COUNT(*) = 2 AND SUM(column_name = 'created_at' AND data_type = 'datetime' AND datetime_precision = 0 AND is_nullable = 'NO' AND UPPER(REPLACE(column_default, '()', '')) = 'CURRENT_TIMESTAMP' AND LOWER(extra) NOT LIKE '%on update%') = 1 AND SUM(column_name = 'updated_at' AND data_type = 'datetime' AND datetime_precision = 0 AND is_nullable = 'NO' AND UPPER(REPLACE(column_default, '()', '')) = 'CURRENT_TIMESTAMP' AND LOWER(extra) LIKE '%on update current_timestamp%') = 1, 1, 0) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'email_scene_bindings' AND column_name IN ('created_at', 'updated_at');" 1
  assert_scalar "SELECT IF(COUNT(*) = 1 AND SUM(data_type = 'datetime' AND datetime_precision = 3 AND is_nullable = 'NO' AND UPPER(column_default) = 'CURRENT_TIMESTAMP(3)' AND LOWER(extra) NOT LIKE '%on update%') = 1, 1, 0) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'email_admin_verify_bootstrap_receipts' AND column_name = 'created_at';" 1
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'migration_000057_email_receipt_time_backup';" 0
}
schema57_shape() {
  assert_scalar "SELECT IF(COUNT(*) = 2 AND SUM(data_type = 'datetime' AND datetime_precision = 0 AND is_nullable = 'NO' AND column_default IS NULL AND LOWER(extra) NOT LIKE '%on update%') = 2, 1, 0) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'email_scene_bindings' AND column_name IN ('created_at', 'updated_at');" 1
  assert_scalar "SELECT IF(COUNT(*) = 1 AND SUM(data_type = 'datetime' AND datetime_precision = 0 AND is_nullable = 'NO' AND column_default IS NULL AND LOWER(extra) NOT LIKE '%on update%') = 1, 1, 0) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'email_admin_verify_bootstrap_receipts' AND column_name = 'created_at';" 1
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'migration_000057_email_receipt_time_backup';" 1
  assert_scalar "SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'manifest' AND receipt_id = 0;" 1
  assert_scalar "SELECT IF((SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'receipt') = (SELECT expected_count FROM migration_000057_email_receipt_time_backup WHERE receipt_id = 0), 1, 0);" 1
  assert_scalar "SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts WHERE MICROSECOND(created_at) <> 0;" 0
  assert_scalar "SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup b LEFT JOIN email_admin_verify_bootstrap_receipts r ON r.id = b.receipt_id WHERE b.row_kind = 'receipt' AND (r.id IS NULL OR r.created_at <> b.created_at_second OR b.row_fingerprint <> LOWER(SHA2(CONCAT_WS(CHAR(31), CAST(r.id AS CHAR), HEX(r.scope), HEX(r.provider), HEX(r.provider_template_id), CAST(r.template_id AS CHAR), r.idempotency_key_hash, r.request_fingerprint, CAST(r.completed_by AS CHAR)), 256)));" 0
}

hash_query() { mysql_query "$1" | "$SHA256SUM_BIN" | "$AWK_BIN" '{print toupper($1)}'; }
receipt_non_time_hash() {
  hash_query "SELECT LOWER(SHA2(CONCAT_WS(CHAR(31), CAST(id AS CHAR), COALESCE(HEX(scope), 'NULL'), COALESCE(HEX(provider), 'NULL'), COALESCE(HEX(provider_template_id), 'NULL'), COALESCE(CAST(template_id AS CHAR), 'NULL'), COALESCE(idempotency_key_hash, 'NULL'), COALESCE(request_fingerprint, 'NULL'), COALESCE(CAST(completed_by AS CHAR), 'NULL')), 256)) FROM email_admin_verify_bootstrap_receipts ORDER BY id;"
}
receipt_second_hash() {
  hash_query "SELECT LOWER(SHA2(CONCAT_WS(CHAR(31), CAST(id AS CHAR), COALESCE(HEX(scope), 'NULL'), COALESCE(HEX(provider), 'NULL'), COALESCE(HEX(provider_template_id), 'NULL'), COALESCE(CAST(template_id AS CHAR), 'NULL'), COALESCE(idempotency_key_hash, 'NULL'), COALESCE(request_fingerprint, 'NULL'), COALESCE(CAST(completed_by AS CHAR), 'NULL'), DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s')), 256)) FROM email_admin_verify_bootstrap_receipts ORDER BY id;"
}
receipt_full_hash() {
  hash_query "SELECT LOWER(SHA2(CONCAT_WS(CHAR(31), CAST(id AS CHAR), COALESCE(HEX(scope), 'NULL'), COALESCE(HEX(provider), 'NULL'), COALESCE(HEX(provider_template_id), 'NULL'), COALESCE(CAST(template_id AS CHAR), 'NULL'), COALESCE(idempotency_key_hash, 'NULL'), COALESCE(request_fingerprint, 'NULL'), COALESCE(CAST(completed_by AS CHAR), 'NULL'), DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s.%f')), 256)) FROM email_admin_verify_bootstrap_receipts ORDER BY id;"
}
scene_value_hash() {
  hash_query "SELECT LOWER(SHA2(CONCAT_WS(CHAR(31), CAST(id AS CHAR), COALESCE(HEX(scene), 'NULL'), COALESCE(HEX(provider), 'NULL'), COALESCE(CAST(template_id AS CHAR), 'NULL'), COALESCE(CAST(enabled AS CHAR), 'NULL'), COALESCE(HEX(variable_mapping_json), 'NULL'), COALESCE(CAST(version AS CHAR), 'NULL'), COALESCE(CAST(updated_by AS CHAR), 'NULL'), DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s'), DATE_FORMAT(updated_at, '%Y-%m-%d %H:%i:%s')), 256)) FROM email_scene_bindings ORDER BY id;"
}

snapshot_tables() {
  local output_file=$1 snapshot_mode=$2 table_names table_name row_count checksum_value quote_char
  : > "$output_file"
  "$CHMOD_BIN" 600 "$output_file"
  quote_char=$(printf '\140')
  if [[ "$snapshot_mode" = stable ]]; then
    table_names=$(mysql_query "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' AND table_name NOT IN ('schema_migrations', 'email_scene_bindings', 'email_admin_verify_bootstrap_receipts', 'migration_000057_email_receipt_time_backup') ORDER BY table_name;")
  else
    [[ "$snapshot_mode" = full ]]
    table_names=$(mysql_query "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' ORDER BY table_name;")
  fi
  while IFS= read -r table_name; do
    [[ -n "$table_name" ]] || continue
    case "$table_name" in *[!A-Za-z0-9_]*) fail ;; esac
    row_count=$(mysql_query "SELECT COUNT(*) FROM $quote_char$table_name$quote_char;")
    checksum_value=$(mysql_query "CHECKSUM TABLE $quote_char$table_name$quote_char;" | "$AWK_BIN" '{print $2}')
    printf '%s\t%s\t%s\n' "$table_name" "$row_count" "$checksum_value" >> "$output_file"
  done <<< "$table_names"
}
assert_data_hashes() {
  [[ $(receipt_non_time_hash) = "$receipt_non_time_before" ]]
  [[ $(receipt_second_hash) = "$receipt_second_before" ]]
  [[ $(scene_value_hash) = "$scene_values_before" ]]
}

# 写入目标库前，完整核验源库只读基线和既有目标库的唯一前置状态。
stage=database_precheck
[[ "$(mysql_admin_query "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = '$source_db';")" = 1 ]]
[[ "$(mysql_admin_query "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = '$target_db';")" = 1 ]]
assert_source_state_unchanged
assert_scalar "SELECT IF(DATABASE() = '$target_db', 1, 0);" 1
assert_scalar "SELECT COUNT(*) FROM schema_migrations;" 1
[[ $(version_dirty) = 56:0 ]]
assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE';" 68
assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' AND engine <> 'InnoDB';" 0
schema56_shape

receipt_rows_before=$(mysql_query "SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts;")
scene_rows_before=$(mysql_query "SELECT COUNT(*) FROM email_scene_bindings;")
receipt_non_time_before=$(receipt_non_time_hash)
receipt_second_before=$(receipt_second_hash)
receipt_full_before=$(receipt_full_hash)
scene_values_before=$(scene_value_hash)
snapshot_tables "$evidence_dir/stable_before.tsv" stable
snapshot_tables "$evidence_dir/full_before.tsv" full
target_total_rows_before=$("$AWK_BIN" -F '\t' '{sum += $2} END {print sum + 0}' "$evidence_dir/full_before.tsv")
[[ "$target_total_rows_before" =~ ^[0-9]+$ ]]
stable_before_sha=$("$SHA256SUM_BIN" -- "$evidence_dir/stable_before.tsv" | "$AWK_BIN" '{print toupper($1)}')
[[ "$stable_before_sha" =~ ^[A-F0-9]{64}$ ]]
assert_source_state_unchanged

# 标记文件禁止同一证据目录重入；失败时保留现场，不执行自动清理或重试。
stage=cycle_start
"$TOUCH_BIN" "$evidence_dir/cycle_started"
"$CHMOD_BIN" 600 "$evidence_dir/cycle_started"

stage=first_up_mark_dirty
assert_scalar "UPDATE schema_migrations SET dirty = 1 WHERE version = 56 AND dirty = 0; SELECT ROW_COUNT();" 1
stage=first_up_sql
verify_sql_hash "$up_file" "$expected_up_sha"
mysql_from_file "$up_file"
stage=first_up_finalize
assert_scalar "UPDATE schema_migrations SET version = 57, dirty = 0 WHERE version = 56 AND dirty = 1; SELECT ROW_COUNT();" 1
stage=first_up_validate
[[ $(version_dirty) = 57:0 ]]
schema57_shape
assert_scalar "SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts;" "$receipt_rows_before"
assert_scalar "SELECT COUNT(*) FROM email_scene_bindings;" "$scene_rows_before"
assert_data_hashes
snapshot_tables "$evidence_dir/stable_after_first_up.tsv" stable
files_equal "$evidence_dir/stable_before.tsv" "$evidence_dir/stable_after_first_up.tsv"
first_backup_count=$(mysql_query "SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'receipt';")
assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE';" 69
assert_source_state_unchanged

stage=down_mark_dirty
assert_scalar "UPDATE schema_migrations SET dirty = 1 WHERE version = 57 AND dirty = 0; SELECT ROW_COUNT();" 1
stage=down_sql
verify_sql_hash "$down_file" "$expected_down_sha"
mysql_from_file "$down_file"
stage=down_finalize
assert_scalar "UPDATE schema_migrations SET version = 56, dirty = 0 WHERE version = 57 AND dirty = 1; SELECT ROW_COUNT();" 1
stage=down_validate
[[ $(version_dirty) = 56:0 ]]
schema56_shape
assert_scalar "SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts;" "$receipt_rows_before"
assert_scalar "SELECT COUNT(*) FROM email_scene_bindings;" "$scene_rows_before"
[[ $(receipt_full_hash) = "$receipt_full_before" ]]
assert_data_hashes
snapshot_tables "$evidence_dir/stable_after_down.tsv" stable
files_equal "$evidence_dir/stable_before.tsv" "$evidence_dir/stable_after_down.tsv"
snapshot_tables "$evidence_dir/full_after_down.tsv" full
files_equal "$evidence_dir/full_before.tsv" "$evidence_dir/full_after_down.tsv"
assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE';" 68
assert_source_state_unchanged

stage=second_up_mark_dirty
assert_scalar "UPDATE schema_migrations SET dirty = 1 WHERE version = 56 AND dirty = 0; SELECT ROW_COUNT();" 1
stage=second_up_sql
verify_sql_hash "$up_file" "$expected_up_sha"
mysql_from_file "$up_file"
stage=second_up_finalize
assert_scalar "UPDATE schema_migrations SET version = 57, dirty = 0 WHERE version = 56 AND dirty = 1; SELECT ROW_COUNT();" 1
stage=second_up_validate
[[ $(version_dirty) = 57:0 ]]
schema57_shape
assert_scalar "SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts;" "$receipt_rows_before"
assert_scalar "SELECT COUNT(*) FROM email_scene_bindings;" "$scene_rows_before"
assert_data_hashes
snapshot_tables "$evidence_dir/stable_after_second_up.tsv" stable
files_equal "$evidence_dir/stable_before.tsv" "$evidence_dir/stable_after_second_up.tsv"
second_backup_count=$(mysql_query "SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'receipt';")
[[ "$second_backup_count" = "$first_backup_count" ]]
assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE';" 69
assert_source_state_unchanged

stage=cycle_complete
"$TOUCH_BIN" "$evidence_dir/cycle_completed"
"$CHMOD_BIN" 600 "$evidence_dir/cycle_completed"
trap - ERR
printf 'cycle_completed=true\n'
printf 'existing_target_reused=true\n'
printf 'source_migration_state_unchanged=true\n'
printf 'source_write_commands_performed=false\n'
printf 'initial_table_count=68\n'
printf 'final_table_count=69\n'
printf 'target_total_rows_before=%s\n' "$target_total_rows_before"
printf 'final_backup_receipt_count=%s\n' "$second_backup_count"
printf 'final_version_dirty=57:0\n'
printf 'three_column_schema_target=true\n'
printf 'affected_row_counts_preserved=true\n'
printf 'affected_values_preserved=true\n'
printf 'down_full_snapshot_restored=true\n'
printf 'stable_tables_preserved=true\n'
printf 'stable_aggregate_sha256=%s\n' "$stable_before_sha"
printf 'up_sha256=%s\n' "$expected_up_sha"
printf 'down_sha256=%s\n' "$expected_down_sha"
printf 'account_or_privilege_changes_performed=false\n'
