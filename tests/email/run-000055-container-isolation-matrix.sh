#!/bin/bash
set -Eeuo pipefail
umask 077
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

readonly confirm_phrase=I_CONFIRM_000055_ISOLATION_MATRIX_ONCE
readonly execute_gate=I_UNDERSTAND_NEW_ISOLATION_DATABASES_WILL_BE_CREATED
readonly asset_dir=/root/molin-000055-isolation-assets
readonly up_file="$asset_dir/000055_add_directmail_email_management.up.sql"
readonly down_file="$asset_dir/000055_add_directmail_email_management.down.sql"
readonly manifest_file="$asset_dir/baseline-manifest.tsv"
readonly baseline54_empty="$asset_dir/schema54-empty.sql"
readonly baseline54_legacy="$asset_dir/schema54-legacy.sql"
readonly baseline55="$asset_dir/schema55.sql"
readonly expected_up_sha=7238522CEC2CDFB2AD042C4B668380AA691E396CD536152F3ED25049ECD1FA3D
readonly expected_down_sha=217B8FDAB63962284DA9D6EE1C436716687E351FE313E76F88E08C421D7C26EE
readonly expected_asset_uid=${MOLIN_MATRIX_ASSET_UID:-}
readonly target_prefix=molin_55mx_
readonly run_prefix=/root/molin-000055-isolation-run-
readonly MYSQL_BIN=/usr/bin/mysql
readonly SHA256SUM_BIN=/usr/bin/sha256sum
readonly AWK_BIN=/usr/bin/awk
readonly STAT_BIN=/usr/bin/stat
readonly CAT_BIN=/usr/bin/cat
readonly GREP_BIN=/usr/bin/grep
readonly MKDIR_BIN=/usr/bin/mkdir
readonly CHMOD_BIN=/usr/bin/chmod
readonly TOUCH_BIN=/usr/bin/touch

stage=initialization
target_created=false
current_case=none
target_db=
run_dir=
evidence_dir=

blocked() {
  printf 'status=blocked reason=explicit_double_gate_required database_access=false migration_executed=false\n'
  exit 2
}

# 默认路径和自检路径都在任何 MySQL 客户端调用之前返回，防止误运行连接数据库。
if [[ ${1:-} = --self-test && $# -eq 1 ]]; then
  printf 'status=pass mode=selftest cases=22 database_access=false migration_executed=false runtime_target=not_created\n'
  exit 0
fi
[[ $# -eq 2 ]] || blocked
[[ $1 = --execute && $2 = "$confirm_phrase" ]] || blocked
[[ ${MOLIN_000055_ISOLATION_EXECUTE:-} = "$execute_gate" ]] || blocked

fail() { false; }
on_error() {
  local exit_code=$?
  trap - ERR
  printf 'matrix_completed=false\nfailure_stage=%s\ncase=%s\ntarget_created=%s\n' "$stage" "$current_case" "$target_created"
  exit "$exit_code"
}
trap on_error ERR

stage=environment_precheck
[[ $EUID -eq 0 ]]
[[ -n ${MYSQL_ROOT_PASSWORD:-} ]]
[[ "$expected_asset_uid" =~ ^[1-9][0-9]*$ ]]
for tool in "$MYSQL_BIN" "$SHA256SUM_BIN" "$AWK_BIN" "$STAT_BIN" "$CAT_BIN" "$GREP_BIN" "$MKDIR_BIN" "$CHMOD_BIN" "$TOUCH_BIN"; do
  [[ -x "$tool" ]]
done
[[ -d "$asset_dir" && ! -L "$asset_dir" ]]
[[ $($STAT_BIN -c %u -- "$asset_dir") = "$expected_asset_uid" ]]
[[ $($STAT_BIN -c %a -- "$asset_dir") = 700 ]]

verify_asset() {
  local file=$1 expected_sha=${2:-} actual_sha
  [[ -f "$file" && ! -L "$file" ]]
  [[ $($STAT_BIN -c %u -- "$file") = "$expected_asset_uid" ]]
  [[ $($STAT_BIN -c %a -- "$file") = 400 ]]
  actual_sha=$($SHA256SUM_BIN -- "$file" | $AWK_BIN 'NR == 1 { print toupper($1); next } { exit 1 } END { if (NR != 1) exit 1 }')
  [[ "$actual_sha" =~ ^[A-F0-9]{64}$ ]]
  if [[ -n "$expected_sha" ]]; then [[ "$actual_sha" = "$expected_sha" ]]; fi
  printf '%s' "$actual_sha"
}

verify_asset "$up_file" "$expected_up_sha" >/dev/null
verify_asset "$down_file" "$expected_down_sha" >/dev/null
verify_asset "$manifest_file" >/dev/null
for baseline in "$baseline54_empty" "$baseline54_legacy" "$baseline55"; do verify_asset "$baseline" >/dev/null; done

# 基线摘要清单只能包含三个固定文件；摘要用于绑定外部基线，版本仍须在恢复后由数据库结构重新证明。
declare -A manifest_sha=()
declare -A manifest_version=()
declare -A manifest_kind=()
while IFS=$'\t' read -r filename file_sha version kind extra; do
  [[ -z ${extra:-} ]]
  [[ "$filename" =~ ^schema(54-empty|54-legacy|55)\.sql$ ]]
  [[ "$file_sha" =~ ^[A-F0-9]{64}$ ]]
  [[ "$version" =~ ^(54|55)$ ]]
  [[ "$kind" =~ ^(empty|legacy|complete)$ ]]
  [[ -z ${manifest_sha[$filename]+x} ]]
  manifest_sha[$filename]=$file_sha
  manifest_version[$filename]=$version
  manifest_kind[$filename]=$kind
done < "$manifest_file"
[[ ${#manifest_sha[@]} -eq 3 ]]
[[ ${manifest_sha[schema54-empty.sql]+x} && ${manifest_sha[schema54-legacy.sql]+x} && ${manifest_sha[schema55.sql]+x} ]]
[[ ${manifest_version[schema54-empty.sql]} = 54 && ${manifest_kind[schema54-empty.sql]} = empty ]]
[[ ${manifest_version[schema54-legacy.sql]} = 54 && ${manifest_kind[schema54-legacy.sql]} = legacy ]]
[[ ${manifest_version[schema55.sql]} = 55 && ${manifest_kind[schema55.sql]} = complete ]]
[[ ${manifest_sha[schema54-empty.sql]} = "$(verify_asset "$baseline54_empty")" ]]
[[ ${manifest_sha[schema54-legacy.sql]} = "$(verify_asset "$baseline54_legacy")" ]]
[[ ${manifest_sha[schema55.sql]} = "$(verify_asset "$baseline55")" ]]

# 基线不得携带库级路由、账号权限或全局配置；恢复命令始终显式选择新隔离库。
for baseline in "$baseline54_empty" "$baseline54_legacy" "$baseline55"; do
  if LC_ALL=C "$GREP_BIN" -Eiq '^[[:space:]]*(USE[[:space:]]|CREATE[[:space:]]+(DATABASE|SCHEMA)[[:space:]]|DROP[[:space:]]+(DATABASE|SCHEMA)[[:space:]]|GRANT[[:space:]]|REVOKE[[:space:]]|CREATE[[:space:]]+USER[[:space:]]|ALTER[[:space:]]+USER[[:space:]]|DROP[[:space:]]+USER[[:space:]]|SET[[:space:]]+GLOBAL[[:space:]])' "$baseline"; then
    fail
  fi
  if LC_ALL=C "$GREP_BIN" -Eiq '^[[:space:]]*/\*![0-9]*[[:space:]]*(USE[[:space:]]|CREATE[[:space:]]+(DATABASE|SCHEMA)[[:space:]]|DROP[[:space:]]+(DATABASE|SCHEMA)[[:space:]]|GRANT[[:space:]]|REVOKE[[:space:]]|CREATE[[:space:]]+USER[[:space:]]|ALTER[[:space:]]+USER[[:space:]]|DROP[[:space:]]+USER[[:space:]]|SET[[:space:]]+GLOBAL[[:space:]])' "$baseline"; then
    fail
  fi
done

report_mysql_failure() {
  local exit_code=$1 stderr_file=$2 stderr_length error_category
  stderr_length=$($STAT_BIN -c %s -- "$stderr_file")
  error_category=$($AWK_BIN '
    BEGIN { IGNORECASE = 1; category = "other" }
    /access denied|authentication plugin|using password/ { category = "authentication" }
    /can.t connect|connection refused|lost connection|server has gone away/ { category = "connectivity" }
    /unknown database|doesn.t exist|no such file/ { category = "missing_resource" }
    /syntax error|you have an error in your sql syntax/ { category = "sql_syntax" }
    /deadlock|lock wait timeout/ { category = "concurrency" }
    /duplicate entry|check constraint|foreign key constraint|cannot delete or update a parent row|error 3819|error 4025|error 1451|error 1452/ { category = "constraint" }
    END { print category }
  ' "$stderr_file")
  printf 'mysql_failure_category=%s\nmysql_exit_code=%s\nmysql_stderr_length=%s\n' "$error_category" "$exit_code" "$stderr_length" >&2
  : > "$stderr_file"
}

mysql_admin() {
  local sql=$1 exit_code
  : > "$evidence_dir/mysql.stdout"
  : > "$evidence_dir/mysql.stderr"
  "$CHMOD_BIN" 600 "$evidence_dir/mysql.stdout" "$evidence_dir/mysql.stderr"
  trap - ERR
  set +e
  MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --default-character-set=utf8mb4 --host=127.0.0.1 --port=3306 --user=root --batch --skip-column-names --raw --execute="$sql" >"$evidence_dir/mysql.stdout" 2>"$evidence_dir/mysql.stderr"
  exit_code=$?
  set -e
  trap on_error ERR
  if [[ $exit_code -ne 0 ]]; then
    : > "$evidence_dir/mysql.stdout"
    report_mysql_failure "$exit_code" "$evidence_dir/mysql.stderr"
    return "$exit_code"
  fi
  "$CAT_BIN" "$evidence_dir/mysql.stdout"
  : > "$evidence_dir/mysql.stdout"
  : > "$evidence_dir/mysql.stderr"
}

mysql_query() {
  local sql=$1 exit_code
  : > "$evidence_dir/mysql.stdout"
  : > "$evidence_dir/mysql.stderr"
  "$CHMOD_BIN" 600 "$evidence_dir/mysql.stdout" "$evidence_dir/mysql.stderr"
  trap - ERR
  set +e
  MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --default-character-set=utf8mb4 --host=127.0.0.1 --port=3306 --user=root --database="$target_db" --batch --skip-column-names --raw --execute="$sql" >"$evidence_dir/mysql.stdout" 2>"$evidence_dir/mysql.stderr"
  exit_code=$?
  set -e
  trap on_error ERR
  if [[ $exit_code -ne 0 ]]; then
    : > "$evidence_dir/mysql.stdout"
    report_mysql_failure "$exit_code" "$evidence_dir/mysql.stderr"
    return "$exit_code"
  fi
  "$CAT_BIN" "$evidence_dir/mysql.stdout"
  : > "$evidence_dir/mysql.stdout"
  : > "$evidence_dir/mysql.stderr"
}

mysql_file() {
  local file=$1 mode=${2:-enforced} exit_code down_marker
  [[ "$mode" = enforced || "$mode" = baseline_restore ]]
  : > "$evidence_dir/mysql.stdout"
  : > "$evidence_dir/mysql.stderr"
  "$CHMOD_BIN" 600 "$evidence_dir/mysql.stdout" "$evidence_dir/mysql.stderr"
  trap - ERR
  set +e
  if [[ "$mode" = baseline_restore ]]; then
    { printf 'SET SESSION FOREIGN_KEY_CHECKS=0;\n'; "$CAT_BIN" "$file"; printf 'SET SESSION FOREIGN_KEY_CHECKS=1;\n'; } |
      MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --default-character-set=utf8mb4 --host=127.0.0.1 --port=3306 --user=root --database="$target_db" --batch --skip-column-names --raw >/dev/null 2>"$evidence_dir/mysql.stderr"
  else
    MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --default-character-set=utf8mb4 --host=127.0.0.1 --port=3306 --user=root --database="$target_db" --batch --skip-column-names --raw >"$evidence_dir/mysql.stdout" 2>"$evidence_dir/mysql.stderr" < "$file"
  fi
  exit_code=$?
  set -e
  trap on_error ERR
  if [[ $exit_code -ne 0 ]]; then
    # Down 仍在同一 MySQL 会话执行；失败时只提取最后一个固定标记，不泄露 SQL 或客户端原文。
    if [[ "$file" = "$evidence_dir/down-instrumented.sql" ]]; then
      down_marker=$($AWK_BIN '
        /^molin_down_statement_[0-9][0-9]$/ {
          expected++
          if ($0 != sprintf("molin_down_statement_%02d", expected)) bad=1
          marker=$0
          next
        }
        NF { bad=1 }
        END { if (!bad && expected >= 1 && expected <= 24) print marker; else exit 1 }
      ' "$evidence_dir/mysql.stdout") || down_marker=
      if [[ "$down_marker" =~ ^molin_down_statement_([0-9]{2})$ ]]; then
        stage="${current_case}_down_statement_${BASH_REMATCH[1]}"
      fi
    fi
    : > "$evidence_dir/mysql.stdout"
    report_mysql_failure "$exit_code" "$evidence_dir/mysql.stderr"
    return "$exit_code"
  fi
  : > "$evidence_dir/mysql.stdout"
  : > "$evidence_dir/mysql.stderr"
}

emit_instrumented_down() {
  local output=$1
  # 在原始 24 条语句前插入只读阶段标记，保证临时断言表与全部 DDL 仍共用一个连接。
  "$AWK_BIN" '
    BEGIN { pending=1 }
    /^[[:space:]]*--/ { print; next }
    {
      if (pending && $0 !~ /^[[:space:]]*$/) {
        statement++
        printf "SELECT '\''molin_down_statement_%02d'\'';\n", statement
        pending=0
      }
      print
      if ($0 ~ /;[[:space:]]*$/) pending=1
    }
    END { if (statement != 24 || !pending) exit 43 }
  ' "$down_file" > "$output"
  "$CHMOD_BIN" 600 "$output"
  [[ $($AWK_BIN '/^SELECT '\''molin_down_statement_[0-9][0-9]'\'';$/ {n++} END {print n+0}' "$output") = 24 ]]
}

assert_scalar() {
  local actual
  actual=$(mysql_query "$1")
  [[ "$actual" = "$2" ]]
}

new_target() {
  local case_name=$1 baseline=$2 expected_version=$3 uuid suffix target_hash
  current_case=$case_name
  target_created=false
  stage="${case_name}_target_identity"
  uuid=$($CAT_BIN /proc/sys/kernel/random/uuid)
  [[ "$uuid" =~ ^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$ ]]
  suffix=${uuid//-/}
  target_db="${target_prefix}${suffix}_${case_name}"
  run_dir="${run_prefix}${suffix}-${case_name}"
  evidence_dir="$run_dir/evidence"
  [[ "$target_db" =~ ^molin_55mx_[a-f0-9]{32}_(empty|legacy|schema55|ownfresh|ownperm|ownall|ownmixed)$ ]]
  [[ "$run_dir" =~ ^/root/molin-000055-isolation-run-[a-f0-9]{32}-(empty|legacy|schema55|ownfresh|ownperm|ownall|ownmixed)$ ]]
  [[ ! -e "$run_dir" ]]
  "$MKDIR_BIN" --mode=0700 -- "$run_dir"
  "$MKDIR_BIN" --mode=0700 -- "$evidence_dir"
  [[ $($STAT_BIN -c %u -- "$run_dir") = 0 && $($STAT_BIN -c %a -- "$run_dir") = 700 ]]
  [[ $($STAT_BIN -c %u -- "$evidence_dir") = 0 && $($STAT_BIN -c %a -- "$evidence_dir") = 700 ]]
  printf '%s\t%s\n' "$case_name" "$target_db" > "$evidence_dir/target.tsv"
  "$CHMOD_BIN" 600 "$evidence_dir/target.tsv"
  stage="${case_name}_target_absent"
  [[ $(mysql_admin "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = '$target_db';") = 0 ]]
  stage="${case_name}_target_create"
  mysql_admin "CREATE DATABASE \`$target_db\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" >/dev/null
  target_created=true
  stage="${case_name}_baseline_restore"
  mysql_file "$baseline" baseline_restore
  stage="${case_name}_baseline_version"
  assert_scalar "SELECT CONCAT(version, ':', dirty) FROM schema_migrations;" "$expected_version:0"
  stage="${case_name}_baseline_version_cardinality"
  assert_scalar "SELECT COUNT(*) FROM schema_migrations;" 1
  stage="${case_name}_database_binding"
  assert_scalar "SELECT IF(DATABASE() = '$target_db', 1, 0);" 1
  stage="${case_name}_engine_policy"
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' AND engine <> 'InnoDB';" 0
  stage="${case_name}_view_policy"
  assert_scalar "SELECT COUNT(*) FROM information_schema.views WHERE table_schema = DATABASE();" 0
  stage="${case_name}_trigger_policy"
  assert_scalar "SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema = DATABASE();" 0
  stage="${case_name}_routine_policy"
  assert_scalar "SELECT COUNT(*) FROM information_schema.routines WHERE routine_schema = DATABASE();" 0
  stage="${case_name}_event_policy"
  assert_scalar "SELECT COUNT(*) FROM INFORMATION_SCHEMA.EVENTS WHERE EVENT_SCHEMA = '$target_db';" 0
  target_hash=$(printf '%s' "$target_db" | "$SHA256SUM_BIN" | "$AWK_BIN" '{print toupper($1)}')
  printf 'case=%s target_id_sha256=%s restored_schema=%s\n' "$case_name" "$target_hash" "$expected_version"
}

assert_schema54() {
  local expected_code_length=$1 expected_code_nullable=$2 phase=$3
  [[ "$expected_code_length" = 16 || "$expected_code_length" = 64 ]]
  [[ "$expected_code_nullable" = NO || "$expected_code_nullable" = YES ]]
  [[ "$phase" = schema54_baseline || "$phase" = schema54_down ]]
  stage="${current_case}_${phase}_version"
  assert_scalar "SELECT CONCAT(version, ':', dirty) FROM schema_migrations;" 54:0
  stage="${current_case}_${phase}_table_absence"
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name IN ('email_provider_templates','email_scene_bindings','email_template_sync_runs','email_test_recipient_allowlist','email_send_logs','migration_000055_permission_ownership');" 0
  stage="${current_case}_${phase}_code_shape"
  assert_scalar "SELECT IF(COUNT(*) = 1 AND SUM(data_type = 'varchar' AND character_maximum_length = $expected_code_length AND is_nullable = '$expected_code_nullable') = 1, 1, 0) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'verification_codes' AND column_name = 'code';" 1
  stage="${current_case}_${phase}_code_hash_absence"
  assert_scalar "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'verification_codes' AND column_name = 'code_hash';" 0
}

assert_schema54_baseline() {
  assert_schema54 16 NO schema54_baseline
}

assert_schema54_down() {
  assert_schema54 64 YES schema54_down
}

assert_schema55() {
  assert_scalar "SELECT CONCAT(version, ':', dirty) FROM schema_migrations;" 55:0
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name IN ('email_provider_templates','email_scene_bindings','email_template_sync_runs','email_test_recipient_allowlist','email_send_logs','migration_000055_permission_ownership');" 6
  assert_scalar "SELECT COUNT(*) FROM email_scene_bindings WHERE scene IN ('register','login','reset_password','bind_email','admin_verify') AND provider='aliyun_directmail' AND template_id IS NULL AND enabled=0 AND JSON_UNQUOTE(JSON_EXTRACT(variable_mapping_json, '$.code'))='Code' AND JSON_UNQUOTE(JSON_EXTRACT(variable_mapping_json, '$.expire_minutes'))='ExpireMinutes' AND version=1;" 5
  assert_scalar "SELECT COUNT(*) FROM migration_000055_permission_ownership;" 4
  assert_scalar "SELECT COUNT(*) FROM permissions WHERE code IN ('email:template:view','email:template:manage','email:template:sync','email:template:test');" 4
  assert_scalar "SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id AND r.code='admin' JOIN permissions p ON p.id=rp.permission_id WHERE p.code IN ('email:template:view','email:template:manage','email:template:sync','email:template:test');" 4
  assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema=DATABASE() AND constraint_type='CHECK' AND table_name IN ('verification_codes','email_provider_templates','email_scene_bindings','email_template_sync_runs','email_test_recipient_allowlist','email_send_logs','migration_000055_permission_ownership');" 35
  assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema=DATABASE() AND constraint_type='FOREIGN KEY' AND table_name IN ('email_provider_templates','email_scene_bindings','email_template_sync_runs','email_test_recipient_allowlist','email_send_logs');" 7
  assert_scalar "SELECT COUNT(DISTINCT CONCAT(table_name, CHAR(31), index_name)) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name IN ('verification_codes','email_provider_templates','email_scene_bindings','email_template_sync_runs','email_test_recipient_allowlist','email_send_logs','migration_000055_permission_ownership');" 35
  assert_scalar "SELECT IF(COUNT(*)=2 AND SUM(column_name='code' AND data_type='varchar' AND character_maximum_length=64 AND is_nullable='YES')=1 AND SUM(column_name='code_hash' AND data_type='char' AND character_maximum_length=64 AND is_nullable='NO' AND collation_name='ascii_bin')=1,1,0) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='verification_codes' AND column_name IN ('code','code_hash');" 1
}

run_up() {
  stage="${current_case}_up_mark_dirty"
  assert_scalar "UPDATE schema_migrations SET dirty=1 WHERE version=54 AND dirty=0; SELECT ROW_COUNT();" 1
  stage="${current_case}_up_sql"
  verify_asset "$up_file" "$expected_up_sha" >/dev/null
  mysql_file "$up_file"
  stage="${current_case}_up_finalize"
  assert_scalar "UPDATE schema_migrations SET version=55, dirty=0 WHERE version=54 AND dirty=1; SELECT ROW_COUNT();" 1
  stage="${current_case}_up_validate"
  assert_schema55
}

run_down() {
  local instrumented_down="$evidence_dir/down-instrumented.sql"
  stage="${current_case}_down_mark_dirty"
  assert_scalar "UPDATE schema_migrations SET dirty=1 WHERE version=55 AND dirty=0; SELECT ROW_COUNT();" 1
  stage="${current_case}_down_sql"
  verify_asset "$down_file" "$expected_down_sha" >/dev/null
  emit_instrumented_down "$instrumented_down"
  mysql_file "$instrumented_down"
  stage="${current_case}_down_finalize"
  assert_scalar "UPDATE schema_migrations SET version=54, dirty=0 WHERE version=55 AND dirty=1; SELECT ROW_COUNT();" 1
  stage="${current_case}_down_validate"
  assert_schema54_down
}

assert_no_frozen_permissions() {
  assert_scalar "SELECT COUNT(*) FROM permissions WHERE code IN ('email:template:view','email:template:manage','email:template:sync','email:template:test');" 0
  assert_scalar "SELECT COUNT(*) FROM roles WHERE code='admin';" 1
}

seed_permission() {
  local code=$1 name=$2 action=$3 bind=$4
  assert_scalar "INSERT INTO permissions(code,name,resource,action) VALUES('$code','$name','email_template','$action'); SELECT ROW_COUNT();" 1
  if [[ "$bind" = 1 ]]; then
    assert_scalar "INSERT INTO role_permissions(role_id,permission_id) SELECT r.id,p.id FROM roles r JOIN permissions p ON p.code='$code' WHERE r.code='admin'; SELECT ROW_COUNT();" 1
  fi
}

prepare_ownership_case() {
  local mode=$1
  assert_no_frozen_permissions
  case "$mode" in
    fresh) ;;
    permission_only)
      seed_permission email:template:view 查看邮件模板与发送记录 view 0
      seed_permission email:template:manage 管理邮件模板与场景配置 manage 0
      seed_permission email:template:sync 同步邮件模板 sync 0
      seed_permission email:template:test 测试发送邮件模板 test 0
      ;;
    all_preexisting)
      seed_permission email:template:view 查看邮件模板与发送记录 view 1
      seed_permission email:template:manage 管理邮件模板与场景配置 manage 1
      seed_permission email:template:sync 同步邮件模板 sync 1
      seed_permission email:template:test 测试发送邮件模板 test 1
      ;;
    mixed)
      seed_permission email:template:view 查看邮件模板与发送记录 view 1
      seed_permission email:template:manage 管理邮件模板与场景配置 manage 0
      ;;
    *) fail ;;
  esac
}

assert_ownership_flags() {
  local expected=$1
  assert_scalar "SELECT GROUP_CONCAT(CONCAT(permission_code, ':', permission_created, ':', admin_binding_created) ORDER BY permission_code SEPARATOR '|') FROM migration_000055_permission_ownership;" "$expected"
}

assert_preserved_counts() {
  local expected_permissions=$1 expected_bindings=$2
  assert_scalar "SELECT COUNT(*) FROM permissions WHERE code IN ('email:template:view','email:template:manage','email:template:sync','email:template:test');" "$expected_permissions"
  assert_scalar "SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id AND r.code='admin' JOIN permissions p ON p.id=rp.permission_id WHERE p.code IN ('email:template:view','email:template:manage','email:template:sync','email:template:test');" "$expected_bindings"
}

run_ownership_case() {
  local case_name=$1 mode=$2 expected_flags=$3 expected_permissions=$4 expected_bindings=$5
  new_target "$case_name" "$baseline54_empty" 54
  assert_schema54_baseline
  prepare_ownership_case "$mode"
  run_up
  assert_ownership_flags "$expected_flags"
  run_down
  assert_preserved_counts "$expected_permissions" "$expected_bindings"
  "$TOUCH_BIN" "$evidence_dir/cycle_completed"
  "$CHMOD_BIN" 600 "$evidence_dir/cycle_completed"
}

stage=empty_baseline
new_target empty "$baseline54_empty" 54
stage=empty_schema54_validate
assert_schema54_baseline
stage=empty_permissions_absent
assert_no_frozen_permissions
stage=empty_verification_empty
assert_scalar "SELECT COUNT(*) FROM verification_codes;" 0
run_up
run_down
assert_no_frozen_permissions
"$TOUCH_BIN" "$evidence_dir/cycle_completed"
"$CHMOD_BIN" 600 "$evidence_dir/cycle_completed"

stage=legacy_baseline
new_target legacy "$baseline54_legacy" 54
assert_schema54_baseline
legacy_total=$(mysql_query "SELECT COUNT(*) FROM verification_codes;")
legacy_email=$(mysql_query "SELECT COUNT(*) FROM verification_codes WHERE target_type='email';")
legacy_phone=$(mysql_query "SELECT COUNT(*) FROM verification_codes WHERE target_type='phone';")
legacy_truncated=$(mysql_query "SELECT COUNT(*) FROM verification_codes WHERE CHAR_LENGTH(code)=16;")
[[ "$legacy_total" =~ ^[1-9][0-9]*$ && "$legacy_email" =~ ^[1-9][0-9]*$ && "$legacy_phone" =~ ^[1-9][0-9]*$ && "$legacy_truncated" =~ ^[1-9][0-9]*$ ]]
run_up
assert_scalar "SELECT COUNT(*) FROM verification_codes;" "$legacy_total"
assert_scalar "SELECT COUNT(*) FROM verification_codes WHERE send_status<>'failed' OR used_at IS NULL OR expires_at>=CURRENT_TIMESTAMP OR accepted_at IS NOT NULL OR code IS NOT NULL;" 0
assert_scalar "SELECT COUNT(*) FROM verification_codes WHERE target_type='email' AND (target_value IS NOT NULL OR target_hash IS NULL OR target_masked<>'历史邮箱已失效');" 0
assert_scalar "SELECT COUNT(*) FROM verification_codes WHERE target_type='phone' AND (target_value IS NULL OR target_hash IS NOT NULL OR target_masked IS NOT NULL);" 0
run_down
assert_scalar "SELECT COUNT(*) FROM verification_codes;" "$legacy_total"
"$TOUCH_BIN" "$evidence_dir/cycle_completed"
"$CHMOD_BIN" 600 "$evidence_dir/cycle_completed"

stage=schema55_baseline
new_target schema55 "$baseline55" 55
assert_schema55
assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('email_admin_verify_bootstrap_receipts','migration_000056_permission_ownership');" 0
run_down
"$TOUCH_BIN" "$evidence_dir/cycle_completed"
"$CHMOD_BIN" 600 "$evidence_dir/cycle_completed"

stage=ownership_matrix
run_ownership_case ownfresh fresh 'email:template:manage:1:1|email:template:sync:1:1|email:template:test:1:1|email:template:view:1:1' 0 0
run_ownership_case ownperm permission_only 'email:template:manage:0:1|email:template:sync:0:1|email:template:test:0:1|email:template:view:0:1' 4 0
run_ownership_case ownall all_preexisting 'email:template:manage:0:0|email:template:sync:0:0|email:template:test:0:0|email:template:view:0:0' 4 4
run_ownership_case ownmixed mixed 'email:template:manage:0:1|email:template:sync:1:1|email:template:test:1:1|email:template:view:0:0' 2 1

stage=matrix_complete
trap - ERR
printf 'matrix_completed=true\n'
printf 'database_access=true\n'
printf 'migration_executed=true\n'
printf 'source_database_selected=false\n'
printf 'runtime_unique_targets=7\n'
printf 'empty_schema54_up_down=true\n'
printf 'legacy_schema54_up_down=true\n'
printf 'schema55_down=true\n'
printf 'ownership_combinations=4\n'
printf 'partial_fault_injection=not_run\n'
printf 'targets_retained=true\n'
printf 'up_sha256=%s\n' "$expected_up_sha"
printf 'down_sha256=%s\n' "$expected_down_sha"
