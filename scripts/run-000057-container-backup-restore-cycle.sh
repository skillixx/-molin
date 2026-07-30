#!/bin/bash
set -Eeuo pipefail
umask 077
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

readonly source_db=molin
readonly target_prefix=molin_restore_57_reverify_
readonly protected_legacy_target_db=molin_restore_57_reverify_8fb6f25611b25d07a563f15105d0906a
target_db=
readonly work_dir=/root/molin-000057-container-cycle-3263e5469732436c910dd22f894d647b
readonly evidence_dir="$work_dir/evidence"
readonly dump_file="$evidence_dir/molin_source_schema56.sql"
readonly up_file="$work_dir/000057_fix_email_datetime_utc_seconds.up.sql"
readonly down_file="$work_dir/000057_fix_email_datetime_utc_seconds.down.sql"
readonly expected_up_sha=50DCD97A45D8ADCF2F7CAC316B44D942DDB880D4F922B8872CAA34BA01CFC67C
readonly expected_down_sha=EE05D166EB874D34A14A0D12FC17EE083CAC28DAFEEAC3772A8C14A4945495BB
readonly MYSQL_BIN=/usr/bin/mysql
readonly MYSQLDUMP_BIN=/usr/bin/mysqldump
readonly CAT_BIN=/usr/bin/cat
readonly SHA256SUM_BIN=/usr/bin/sha256sum
readonly AWK_BIN=/usr/bin/awk
readonly STAT_BIN=/usr/bin/stat
readonly TOUCH_BIN=/usr/bin/touch
readonly CHMOD_BIN=/usr/bin/chmod
readonly MKDIR_BIN=/usr/bin/mkdir

stage=initialization
target_created=false
fail() { false; }
on_error() {
  local exit_code=$?
  trap - ERR
  printf 'cycle_completed=false\nfailure_stage=%s\ntarget_created=%s\n' "$stage" "$target_created"
  exit "$exit_code"
}
trap on_error ERR

# 容器内只读取标准 root 密码环境，并拒绝覆盖既有目标或证据。
stage=environment_precheck
[[ $EUID -eq 0 ]]
[[ -n "${MYSQL_ROOT_PASSWORD:-}" ]]
for tool in "$MYSQL_BIN" "$MYSQLDUMP_BIN" "$CAT_BIN" "$SHA256SUM_BIN" "$AWK_BIN" "$STAT_BIN" "$TOUCH_BIN" "$CHMOD_BIN" "$MKDIR_BIN"; do
  [[ -x "$tool" ]]
done

# 启动时只生成一次严格格式的新隔离目标，立即冻结；旧 dirty1 目标只用于禁止复用比较，绝不查询、修改或清理。
target_uuid=$($CAT_BIN /proc/sys/kernel/random/uuid)
[[ "$target_uuid" =~ ^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$ ]]
target_db="${target_prefix}${target_uuid//-/}"
[[ "$target_db" =~ ^molin_restore_57_reverify_[a-f0-9]{32}$ ]]
[[ "$target_db" != "$protected_legacy_target_db" ]]
readonly target_db
unset target_uuid

[[ -d "$work_dir" && ! -L "$work_dir" ]]
[[ ! -e "$evidence_dir" ]]
for sql_file in "$up_file" "$down_file"; do [[ -f "$sql_file" && ! -L "$sql_file" ]]; done
"$MKDIR_BIN" --mode=0700 -- "$evidence_dir"
[[ "$($STAT_BIN -c %u -- "$evidence_dir")" = 0 ]]
[[ "$($STAT_BIN -c %a -- "$evidence_dir")" = 700 ]]
verify_sql_hash() {
  local actual_hash
  actual_hash=$($SHA256SUM_BIN -- "$1" | $AWK_BIN '{print toupper($1)}')
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
assert_source_migration_state_unchanged() {
  [[ $(source_version_dirty) = 56:0 ]]
  [[ "$(mysql_query_db "$source_db" "SELECT COUNT(*) FROM schema_migrations;")" = 1 ]]
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

# 源库只读门禁、单事务备份与唯一目标恢复。
stage=source_gate
assert_source_migration_state_unchanged
[[ "$(mysql_query_db "$source_db" "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE';")" = 68 ]]
[[ "$(mysql_query_db "$source_db" "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' AND engine <> 'InnoDB';")" = 0 ]]
[[ "$(mysql_query_db "$source_db" "SELECT COUNT(*) FROM information_schema.views WHERE table_schema = DATABASE();")" = 0 ]]
[[ "$(mysql_query_db "$source_db" "SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema = DATABASE();")" = 0 ]]
[[ "$(mysql_query_db "$source_db" "SELECT COUNT(*) FROM information_schema.routines WHERE routine_schema = DATABASE();")" = 0 ]]
[[ "$(mysql_query_db "$source_db" "SELECT COUNT(*) FROM information_schema.events WHERE event_schema = DATABASE();")" = 0 ]]
[[ "$(mysql_admin_query "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = '$target_db';")" = 0 ]]
source_charset=$(mysql_admin_query "SELECT default_character_set_name FROM information_schema.schemata WHERE schema_name = '$source_db';")
source_collation=$(mysql_admin_query "SELECT default_collation_name FROM information_schema.schemata WHERE schema_name = '$source_db';")
[[ "$source_charset" =~ ^[A-Za-z0-9_]+$ ]]
[[ "$source_collation" =~ ^[A-Za-z0-9_]+$ ]]

stage=source_dump
MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQLDUMP_BIN" --host=127.0.0.1 --port=3306 --user=root --single-transaction --quick --skip-lock-tables --routines --triggers --events --hex-blob --set-gtid-purged=OFF --no-tablespaces "$source_db" >"$dump_file" 2>/dev/null
"$CHMOD_BIN" 600 "$dump_file"
[[ -s "$dump_file" ]]
dump_sha=$($SHA256SUM_BIN -- "$dump_file" | $AWK_BIN '{print toupper($1)}')
[[ "$dump_sha" =~ ^[A-F0-9]{64}$ ]]
assert_source_migration_state_unchanged

# SQL 词法扫描忽略字符串、标识符和普通注释，但会解析 MySQL 可执行版本注释。
stage=dump_static_gate
if "$AWK_BIN" '
  function emit_token(upper) {
    if (token == "") return
    upper = toupper(token)
    if (upper == "USE") forbidden = 1
    if (previous == "CREATE" && upper == "DATABASE") forbidden = 1
    if (upper == "CREATE") previous = "CREATE"
    else previous = ""
    token = ""
  }
  function starts_mysql_dash_comment(following_character) {
    return following_character ~ /[[:space:][:cntrl:]]/
  }
  BEGIN {
    state = "NORMAL"
    token = ""
    previous = ""
    forbidden = 0
  }
  {
    line = $0 "\n"
    length_of_line = length(line)
    position = 1
    while (position <= length_of_line) {
      character = substr(line, position, 1)
      next_character = substr(line, position + 1, 1)
      third_character = substr(line, position + 2, 1)

      if (state == "LINE_COMMENT") {
        if (character == "\n") state = line_comment_return
        position++
        continue
      }
      if (state == "BLOCK_COMMENT") {
        if (character == "*" && next_character == "/") {
          state = "NORMAL"
          position += 2
        } else position++
        continue
      }
      if (state == "SINGLE_QUOTE" || state == "DOUBLE_QUOTE" || state == "BACKTICK") {
        quote = state == "SINGLE_QUOTE" ? "\047" : (state == "DOUBLE_QUOTE" ? "\042" : "\140")
        if (character == "\\") {
          position += 2
        } else if (character == quote && next_character == quote) {
          position += 2
        } else if (character == quote) {
          state = quote_return
          position++
        } else position++
        continue
      }

      if (state == "EXECUTABLE_COMMENT" && character == "*" && next_character == "/") {
        emit_token()
        state = "NORMAL"
        position += 2
        continue
      }
      if (state == "NORMAL" && character == "/" && next_character == "*") {
        emit_token()
        if (third_character == "!") {
          state = "EXECUTABLE_COMMENT"
          position += 3
          while (position <= length_of_line && substr(line, position, 1) ~ /[0-9]/) position++
        } else {
          state = "BLOCK_COMMENT"
          position += 2
        }
        continue
      }
      if (character == "-" && next_character == "-" && starts_mysql_dash_comment(third_character)) {
        emit_token()
        line_comment_return = state
        state = "LINE_COMMENT"
        position += 2
        continue
      }
      if (character == "#") {
        emit_token()
        line_comment_return = state
        state = "LINE_COMMENT"
        position++
        continue
      }
      if (character == "\047" || character == "\042" || character == "\140") {
        emit_token()
        previous = ""
        quote_return = state
        if (character == "\047") state = "SINGLE_QUOTE"
        else if (character == "\042") state = "DOUBLE_QUOTE"
        else state = "BACKTICK"
        position++
        continue
      }
      if (character ~ /[[:alnum:]_$]/) {
        token = token character
        position++
        continue
      }

      emit_token()
      if (character !~ /[[:space:]]/) previous = ""
      position++
    }
  }
  END {
    if (state != "NORMAL") exit 2
    emit_token()
    if (forbidden) exit 1
    exit 0
  }
' "$dump_file"; then
  :
else
  fail
fi

stage=target_create
mysql_admin_query "CREATE DATABASE \`$target_db\` CHARACTER SET $source_charset COLLATE $source_collation;" >/dev/null
target_created=true
stage=target_restore
mysql_from_file "$dump_file"
assert_source_migration_state_unchanged
# 数据库写入前完成目标、账号、恢复摘要、表数和总行数门禁。
stage=database_precheck
assert_scalar "SELECT IF(DATABASE() = '$target_db', 1, 0);" 1
assert_scalar "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = '$target_db';" 1
assert_scalar "SELECT COUNT(*) FROM schema_migrations;" 1
[[ $(version_dirty) = 56:0 ]]
schema56_shape
receipt_rows_before=$(mysql_query "SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts;")
scene_rows_before=$(mysql_query "SELECT COUNT(*) FROM email_scene_bindings;")
receipt_non_time_before=$(receipt_non_time_hash)
receipt_second_before=$(receipt_second_hash)
receipt_full_before=$(receipt_full_hash)
scene_values_before=$(scene_value_hash)
snapshot_tables "$evidence_dir/stable_before.tsv" stable
snapshot_tables "$evidence_dir/full_before.tsv" full
assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE';" 68
restored_total_rows=$($AWK_BIN -F '\t' '{sum += $2} END {print sum + 0}' "$evidence_dir/full_before.tsv")
[[ "$restored_total_rows" =~ ^[0-9]+$ ]]
stable_before_sha=$($SHA256SUM_BIN -- "$evidence_dir/stable_before.tsv" | "$AWK_BIN" '{print toupper($1)}')
assert_source_migration_state_unchanged

# cycle_started 创建后不允许重入；失败时保留现场，不自动修复。
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
assert_source_migration_state_unchanged

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
assert_source_migration_state_unchanged

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
assert_source_migration_state_unchanged

stage=cycle_complete
"$TOUCH_BIN" "$evidence_dir/cycle_completed"
"$CHMOD_BIN" 600 "$evidence_dir/cycle_completed"
trap - ERR
printf 'cycle_completed=true\n'
printf 'source_migration_state_unchanged=true\n'
printf 'source_write_commands_performed=false\n'
printf 'dump_retained=true\n'
printf 'dump_sha256=%s\n' "$dump_sha"
printf 'restored_total_rows=%s\n' "$restored_total_rows"
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
