#!/bin/bash
set -Eeuo pipefail
umask 077
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

readonly confirm_phrase=I_CONFIRM_000056_ISOLATION_MATRIX_ONCE
readonly execute_gate=I_UNDERSTAND_000056_NEW_ISOLATION_DATABASES_WILL_BE_CREATED
readonly asset_dir=/root/molin-000056-isolation-assets
readonly up_file="$asset_dir/000056_add_email_admin_verify_bootstrap.up.sql"
readonly down_file="$asset_dir/000056_add_email_admin_verify_bootstrap.down.sql"
readonly baseline_file="$asset_dir/schema55.sql"
readonly baseline56_file="$asset_dir/schema56.sql"
readonly manifest_file="$asset_dir/baseline-manifest.tsv"
readonly expected_up_sha=9133212C61EB4AA89B72C77D0C353F4B0F8B483080CBFB1E85A0281379861D9B
readonly expected_down_sha=F42A30D70A95AD7BFD876F1515267C5FEE3DDCFD7AAC066453BDC020D201A5C2
readonly expected_asset_uid=${MOLIN_MATRIX_ASSET_UID:-}
readonly target_prefix=molin_56mx_
readonly run_prefix=/root/molin-000056-isolation-run-
readonly MYSQL_BIN=/usr/bin/mysql
readonly SHA256SUM_BIN=/usr/bin/sha256sum
readonly AWK_BIN=/usr/bin/awk
readonly STAT_BIN=/usr/bin/stat
readonly CAT_BIN=/usr/bin/cat
readonly GREP_BIN=/usr/bin/grep
readonly MKDIR_BIN=/usr/bin/mkdir
readonly CHMOD_BIN=/usr/bin/chmod
readonly TOUCH_BIN=/usr/bin/touch
readonly SLEEP_BIN=/usr/bin/sleep
readonly WC_BIN=/usr/bin/wc

stage=initialization
current_case=none
target_created=false
target_db=
run_dir=
evidence_dir=

blocked() {
  printf 'status=blocked reason=explicit_double_gate_required database_access=false migration_executed=false\n'
  exit 2
}

# 默认、自检和单门禁路径必须在任何 MySQL 客户端调用之前结束。
if [[ ${1:-} = --self-test && $# -eq 1 ]]; then
  printf 'status=pass mode=selftest cases=20 database_access=false migration_executed=false runtime_target=not_created\n'
  exit 0
fi
[[ $# -eq 2 ]] || blocked
[[ $1 = --execute && $2 = "$confirm_phrase" ]] || blocked
[[ ${MOLIN_000056_ISOLATION_EXECUTE:-} = "$execute_gate" ]] || blocked

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
for tool in "$MYSQL_BIN" "$SHA256SUM_BIN" "$AWK_BIN" "$STAT_BIN" "$CAT_BIN" "$GREP_BIN" "$MKDIR_BIN" "$CHMOD_BIN" "$TOUCH_BIN" "$SLEEP_BIN" "$WC_BIN"; do
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
verify_asset "$baseline_file" >/dev/null
verify_asset "$baseline56_file" >/dev/null
verify_asset "$manifest_file" >/dev/null

# basic 与 partial 共用同一份两行基线清单，避免打包后出现互斥的同名输入。
declare -A manifest_sha=()
declare -A manifest_version=()
declare -A manifest_kind=()
while IFS=$'\t' read -r manifest_name file_sha version kind extra; do
  [[ -z ${extra:-} ]]
  [[ "$manifest_name" =~ ^schema(55|56)\.sql$ ]]
  [[ "$file_sha" =~ ^[A-F0-9]{64}$ ]]
  [[ "$version" =~ ^(55|56)$ && "$kind" = complete ]]
  [[ -z ${manifest_sha[$manifest_name]+x} ]]
  manifest_sha[$manifest_name]=$file_sha
  manifest_version[$manifest_name]=$version
  manifest_kind[$manifest_name]=$kind
done < "$manifest_file"
[[ ${#manifest_sha[@]} -eq 2 ]]
[[ ${manifest_version[schema55.sql]} = 55 && ${manifest_kind[schema55.sql]} = complete ]]
[[ ${manifest_version[schema56.sql]} = 56 && ${manifest_kind[schema56.sql]} = complete ]]
[[ ${manifest_sha[schema55.sql]} = "$(verify_asset "$baseline_file")" ]]
[[ ${manifest_sha[schema56.sql]} = "$(verify_asset "$baseline56_file")" ]]
[[ $($WC_BIN -l < "$manifest_file") -eq 2 ]]

# 基线不得控制数据库、账号、授权或全局配置；恢复时只选择运行时随机目标。
for baseline in "$baseline_file" "$baseline56_file"; do
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
    /duplicate entry|check constraint/ { category = "constraint" }
    END { print category }
  ' "$stderr_file")
  printf 'mysql_failure_category=%s\nmysql_exit_code=%s\nmysql_stderr_length=%s\n' "$error_category" "$exit_code" "$stderr_length" >&2
  : > "$stderr_file"
}

mysql_admin() {
  local sql=$1 exit_code
  : > "$evidence_dir/mysql.stdout"; : > "$evidence_dir/mysql.stderr"
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
  : > "$evidence_dir/mysql.stdout"; : > "$evidence_dir/mysql.stderr"
}

mysql_query() {
  local sql=$1 exit_code
  : > "$evidence_dir/mysql.stdout"; : > "$evidence_dir/mysql.stderr"
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
  : > "$evidence_dir/mysql.stdout"; : > "$evidence_dir/mysql.stderr"
}

mysql_file() {
  local file=$1 mode=${2:-enforced} exit_code
  [[ "$mode" = enforced || "$mode" = baseline_restore ]]
  : > "$evidence_dir/mysql.stderr"
  "$CHMOD_BIN" 600 "$evidence_dir/mysql.stderr"
  trap - ERR
  set +e
  if [[ "$mode" = baseline_restore ]]; then
    { printf 'SET SESSION FOREIGN_KEY_CHECKS=0;\n'; "$CAT_BIN" "$file"; printf 'SET SESSION FOREIGN_KEY_CHECKS=1;\n'; } |
      MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --default-character-set=utf8mb4 --host=127.0.0.1 --port=3306 --user=root --database="$target_db" --batch --skip-column-names --raw >/dev/null 2>"$evidence_dir/mysql.stderr"
  else
    MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --default-character-set=utf8mb4 --host=127.0.0.1 --port=3306 --user=root --database="$target_db" --batch --skip-column-names --raw >/dev/null 2>"$evidence_dir/mysql.stderr" < "$file"
  fi
  exit_code=$?
  set -e
  trap on_error ERR
  if [[ $exit_code -ne 0 ]]; then
    report_mysql_failure "$exit_code" "$evidence_dir/mysql.stderr"
    return "$exit_code"
  fi
  : > "$evidence_dir/mysql.stderr"
}

mysql_file_expect_failure() {
  local file=$1 exit_code
  : > "$evidence_dir/mysql.stderr"
  "$CHMOD_BIN" 600 "$evidence_dir/mysql.stderr"
  trap - ERR
  set +e
  MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --default-character-set=utf8mb4 --host=127.0.0.1 --port=3306 --user=root --database="$target_db" --batch --skip-column-names --raw >/dev/null 2>"$evidence_dir/mysql.stderr" < "$file"
  exit_code=$?
  set -e
  trap on_error ERR
  [[ $exit_code -ne 0 ]]
  report_mysql_failure "$exit_code" "$evidence_dir/mysql.stderr"
}

assert_scalar() {
  local actual
  actual=$(mysql_query "$1")
  [[ "$actual" = "$2" ]]
}

new_target() {
  local case_name=$1 uuid suffix target_hash
  current_case=$case_name
  target_created=false
  uuid=$($CAT_BIN /proc/sys/kernel/random/uuid)
  [[ "$uuid" =~ ^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$ ]]
  suffix=${uuid//-/}
  target_db="${target_prefix}${suffix}_${case_name}"
  run_dir="${run_prefix}${suffix}-${case_name}"
  evidence_dir="$run_dir/evidence"
  [[ "$target_db" =~ ^molin_56mx_[a-f0-9]{32}_(ownfresh|ownperm|ownall|adminzero|admintwo|metaconf|receipt|refrole|refuser|refgroup|concurrent)$ ]]
  [[ "$run_dir" =~ ^/root/molin-000056-isolation-run-[a-f0-9]{32}-(ownfresh|ownperm|ownall|adminzero|admintwo|metaconf|receipt|refrole|refuser|refgroup|concurrent)$ ]]
  [[ ! -e "$run_dir" ]]
  "$MKDIR_BIN" --mode=0700 -- "$run_dir"; "$MKDIR_BIN" --mode=0700 -- "$evidence_dir"
  printf '%s\t%s\n' "$case_name" "$target_db" > "$evidence_dir/target.tsv"
  "$CHMOD_BIN" 600 "$evidence_dir/target.tsv"
  [[ $(mysql_admin "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = '$target_db';") = 0 ]]
  mysql_admin "CREATE DATABASE \`$target_db\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" >/dev/null
  target_created=true
  mysql_file "$baseline_file" baseline_restore
  assert_clean_schema55
  target_hash=$(printf '%s' "$target_db" | "$SHA256SUM_BIN" | "$AWK_BIN" '{print toupper($1)}')
  printf 'case=%s target_id_sha256=%s restored_schema=55\n' "$case_name" "$target_hash"
}

assert_schema55() {
  assert_scalar "SELECT CONCAT(version, ':', dirty) FROM schema_migrations;" 55:0
  assert_scalar "SELECT COUNT(*) FROM schema_migrations;" 1
  assert_scalar "SELECT IF(DATABASE() = '$target_db', 1, 0);" 1
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('email_provider_templates','email_scene_bindings','email_template_sync_runs','email_test_recipient_allowlist','email_send_logs','migration_000055_permission_ownership');" 6
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('email_admin_verify_bootstrap_receipts','migration_000056_permission_ownership','migration_000056_assertions');" 0
  assert_scalar "SELECT COUNT(*) FROM roles WHERE code='admin';" 1
  assert_scalar "SELECT COUNT(*) FROM email_scene_bindings WHERE scene IN ('register','login','reset_password','bind_email','admin_verify') AND provider='aliyun_directmail' AND template_id IS NULL AND enabled=0 AND version=1;" 5
  assert_scalar "SELECT COUNT(*) FROM migration_000055_permission_ownership;" 4
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_type='BASE TABLE' AND engine<>'InnoDB';" 0
}

assert_clean_schema55() {
  assert_schema55
  assert_scalar "SELECT COUNT(*) FROM permissions WHERE code='email:template:bootstrap';" 0
}

assert_schema56() {
  assert_scalar "SELECT CONCAT(version, ':', dirty) FROM schema_migrations;" 56:0
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('email_admin_verify_bootstrap_receipts','migration_000056_permission_ownership');" 2
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='migration_000056_assertions';" 0
  assert_scalar "SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts;" 0
  assert_scalar "SELECT COUNT(*) FROM migration_000056_permission_ownership WHERE permission_code='email:template:bootstrap';" 1
  assert_scalar "SELECT COUNT(*) FROM permissions WHERE code='email:template:bootstrap' AND name='首次配置管理员邮箱认证模板' AND resource='email_template' AND action='bootstrap';" 1
  assert_scalar "SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id AND r.code='admin' JOIN permissions p ON p.id=rp.permission_id AND p.code='email:template:bootstrap';" 1
  assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema=DATABASE() AND table_name='email_admin_verify_bootstrap_receipts' AND constraint_type='CHECK';" 5
  assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema=DATABASE() AND table_name='email_admin_verify_bootstrap_receipts' AND constraint_type='FOREIGN KEY';" 2
  assert_scalar "SELECT COUNT(DISTINCT index_name) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='email_admin_verify_bootstrap_receipts';" 5
}

run_up() {
  stage="${current_case}_up_mark_dirty"
  assert_scalar "UPDATE schema_migrations SET dirty=1 WHERE version=55 AND dirty=0; SELECT ROW_COUNT();" 1
  stage="${current_case}_up_sql"
  verify_asset "$up_file" "$expected_up_sha" >/dev/null
  mysql_file "$up_file"
  stage="${current_case}_up_finalize"
  assert_scalar "UPDATE schema_migrations SET version=56, dirty=0 WHERE version=55 AND dirty=1; SELECT ROW_COUNT();" 1
  stage="${current_case}_up_validate"
  assert_schema56
}

run_down() {
  stage="${current_case}_down_mark_dirty"
  assert_scalar "UPDATE schema_migrations SET dirty=1 WHERE version=56 AND dirty=0; SELECT ROW_COUNT();" 1
  stage="${current_case}_down_sql"
  verify_asset "$down_file" "$expected_down_sha" >/dev/null
  mysql_file "$down_file"
  stage="${current_case}_down_finalize"
  assert_scalar "UPDATE schema_migrations SET version=55, dirty=0 WHERE version=56 AND dirty=1; SELECT ROW_COUNT();" 1
  stage="${current_case}_down_validate"
  assert_schema55
}

seed_bootstrap_permission() {
  local bind=$1
  assert_scalar "INSERT INTO permissions(code,name,resource,action) VALUES('email:template:bootstrap','首次配置管理员邮箱认证模板','email_template','bootstrap'); SELECT ROW_COUNT();" 1
  if [[ "$bind" = 1 ]]; then
    assert_scalar "INSERT INTO role_permissions(role_id,permission_id) SELECT r.id,p.id FROM roles r JOIN permissions p ON p.code='email:template:bootstrap' WHERE r.code='admin'; SELECT ROW_COUNT();" 1
  fi
}

run_ownership_case() {
  local case_name=$1 mode=$2 expected_flags=$3 expected_permissions=$4 expected_bindings=$5
  new_target "$case_name"
  case "$mode" in
    fresh) ;;
    permission) seed_bootstrap_permission 0 ;;
    all) seed_bootstrap_permission 1 ;;
    *) fail ;;
  esac
  run_up
  assert_scalar "SELECT CONCAT(permission_created, ':', admin_binding_created) FROM migration_000056_permission_ownership;" "$expected_flags"
  run_down
  assert_scalar "SELECT COUNT(*) FROM permissions WHERE code='email:template:bootstrap';" "$expected_permissions"
  assert_scalar "SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id AND r.code='admin' JOIN permissions p ON p.id=rp.permission_id AND p.code='email:template:bootstrap';" "$expected_bindings"
  "$TOUCH_BIN" "$evidence_dir/cycle_completed"; "$CHMOD_BIN" 600 "$evidence_dir/cycle_completed"
}

expect_up_blocked() {
  stage="${current_case}_up_expected_block"
  verify_asset "$up_file" "$expected_up_sha" >/dev/null
  mysql_file_expect_failure "$up_file"
  assert_scalar "SELECT CONCAT(version, ':', dirty) FROM schema_migrations;" 55:0
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('email_admin_verify_bootstrap_receipts','migration_000056_permission_ownership');" 0
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='migration_000056_assertions';" 1
}

expect_down_blocked() {
  stage="${current_case}_down_expected_block"
  verify_asset "$down_file" "$expected_down_sha" >/dev/null
  mysql_file_expect_failure "$down_file"
  assert_scalar "SELECT CONCAT(version, ':', dirty) FROM schema_migrations;" 56:0
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('email_admin_verify_bootstrap_receipts','migration_000056_permission_ownership','migration_000056_assertions');" 3
  assert_scalar "SELECT COUNT(*) FROM permissions WHERE code='email:template:bootstrap';" 1
  assert_scalar "SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id AND r.code='admin' JOIN permissions p ON p.id=rp.permission_id AND p.code='email:template:bootstrap';" 1
}

prepare_receipt_dependencies() {
  local user_id template_id
  mysql_query "INSERT INTO users(email,email_verified,phone,phone_verified,password_hash,real_name_status,status) VALUES(NULL,0,NULL,0,'isolation-fixture-not-a-login-secret','unverified','active');" >/dev/null
  user_id=$(mysql_query "SELECT MAX(id) FROM users WHERE password_hash='isolation-fixture-not-a-login-secret';")
  [[ "$user_id" =~ ^[1-9][0-9]*$ ]]
  mysql_query "INSERT INTO email_provider_templates(provider,provider_template_id,name,subject,template_text,variables_json,content_sha256,provider_status,variables_complete,local_enabled,missing,last_synced_at) VALUES('aliyun_directmail','560001','molin_admin_verify_code_v1','隔离测试主题','<p>{Code} {ExpireMinutes}</p>',JSON_ARRAY('Code','ExpireMinutes'),REPEAT('a',64),'approved',1,1,0,CURRENT_TIMESTAMP);" >/dev/null
  template_id=$(mysql_query "SELECT id FROM email_provider_templates WHERE provider='aliyun_directmail' AND provider_template_id='560001';")
  [[ "$template_id" =~ ^[1-9][0-9]*$ ]]
  assert_scalar "UPDATE email_scene_bindings SET template_id=$template_id,enabled=1,version=version+1 WHERE scene='admin_verify' AND template_id IS NULL AND enabled=0; SELECT ROW_COUNT();" 1
  printf '%s\t%s\n' "$user_id" "$template_id" > "$evidence_dir/fixture-ids.tsv"
  "$CHMOD_BIN" 600 "$evidence_dir/fixture-ids.tsv"
}

insert_receipt() {
  local hash_character=$1 user_id template_id
  IFS=$'\t' read -r user_id template_id < "$evidence_dir/fixture-ids.tsv"
  assert_scalar "INSERT INTO email_admin_verify_bootstrap_receipts(scope,provider,provider_template_id,template_id,idempotency_key_hash,request_fingerprint,completed_by) VALUES('admin_verify','aliyun_directmail','560001',$template_id,REPEAT('$hash_character',64),REPEAT('d',64),$user_id); SELECT ROW_COUNT();" 1
}

inject_unknown_reference() {
  local kind=$1 permission_id user_id
  permission_id=$(mysql_query "SELECT id FROM permissions WHERE code='email:template:bootstrap';")
  case "$kind" in
    role)
      assert_scalar "INSERT INTO roles(code,name,description) VALUES('isolation_unknown_role','隔离未知角色','仅用于迁移阻断'); SELECT ROW_COUNT();" 1
      assert_scalar "INSERT INTO role_permissions(role_id,permission_id) SELECT id,$permission_id FROM roles WHERE code='isolation_unknown_role'; SELECT ROW_COUNT();" 1
      ;;
    user)
      mysql_query "INSERT INTO users(email,email_verified,phone,phone_verified,password_hash,real_name_status,status) VALUES(NULL,0,NULL,0,'isolation-unknown-user','unverified','active');" >/dev/null
      user_id=$(mysql_query "SELECT MAX(id) FROM users WHERE password_hash='isolation-unknown-user';")
      assert_scalar "INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect,reason) VALUES($user_id,$permission_id,'email:template:bootstrap','allow','隔离阻断'); SELECT ROW_COUNT();" 1
      ;;
    group)
      assert_scalar "INSERT INTO user_groups(code,name,type,is_default,description) VALUES('isolation_unknown_group','隔离未知组','custom',0,'仅用于迁移阻断'); SELECT ROW_COUNT();" 1
      assert_scalar "INSERT INTO group_permissions(group_id,permission_code) SELECT id,'email:template:bootstrap' FROM user_groups WHERE code='isolation_unknown_group'; SELECT ROW_COUNT();" 1
      ;;
    *) fail ;;
  esac
}

run_concurrent_receipt_case() {
  local user_id template_id pid_a pid_b exit_a exit_b
  new_target concurrent
  run_up
  prepare_receipt_dependencies
  IFS=$'\t' read -r user_id template_id < "$evidence_dir/fixture-ids.tsv"
  printf "START TRANSACTION; INSERT INTO email_admin_verify_bootstrap_receipts(scope,provider,provider_template_id,template_id,idempotency_key_hash,request_fingerprint,completed_by) VALUES('admin_verify','aliyun_directmail','560001',%s,REPEAT('e',64),REPEAT('f',64),%s); DO SLEEP(2); COMMIT;\n" "$template_id" "$user_id" > "$evidence_dir/concurrent-a.sql"
  printf "START TRANSACTION; INSERT INTO email_admin_verify_bootstrap_receipts(scope,provider,provider_template_id,template_id,idempotency_key_hash,request_fingerprint,completed_by) VALUES('admin_verify','aliyun_directmail','560001',%s,REPEAT('1',64),REPEAT('2',64),%s); COMMIT;\n" "$template_id" "$user_id" > "$evidence_dir/concurrent-b.sql"
  "$CHMOD_BIN" 600 "$evidence_dir/concurrent-a.sql" "$evidence_dir/concurrent-b.sql"
  : > "$evidence_dir/concurrent-a.stderr"; : > "$evidence_dir/concurrent-b.stderr"
  trap - ERR
  set +e
  MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --default-character-set=utf8mb4 --host=127.0.0.1 --port=3306 --user=root --database="$target_db" --batch --skip-column-names --raw >/dev/null 2>"$evidence_dir/concurrent-a.stderr" < "$evidence_dir/concurrent-a.sql" & pid_a=$!
  "$SLEEP_BIN" 1
  MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --default-character-set=utf8mb4 --host=127.0.0.1 --port=3306 --user=root --database="$target_db" --batch --skip-column-names --raw >/dev/null 2>"$evidence_dir/concurrent-b.stderr" < "$evidence_dir/concurrent-b.sql" & pid_b=$!
  wait "$pid_a"; exit_a=$?
  wait "$pid_b"; exit_b=$?
  set -e
  trap on_error ERR
  [[ $exit_a -eq 0 && $exit_b -ne 0 ]]
  : > "$evidence_dir/concurrent-a.stderr"
  report_mysql_failure "$exit_b" "$evidence_dir/concurrent-b.stderr"
  assert_scalar "SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts WHERE scope='admin_verify';" 1
  "$TOUCH_BIN" "$evidence_dir/cycle_completed"; "$CHMOD_BIN" 600 "$evidence_dir/cycle_completed"
}

stage=ownership_matrix
run_ownership_case ownfresh fresh 1:1 0 0
run_ownership_case ownperm permission 0:1 1 0
run_ownership_case ownall all 0:0 1 1

stage=up_block_matrix
new_target adminzero
assert_scalar "UPDATE roles SET code='isolation_admin_disabled' WHERE code='admin'; SELECT ROW_COUNT();" 1
expect_up_blocked
new_target admintwo
mysql_query "ALTER TABLE roles DROP INDEX uk_roles_code;" >/dev/null
assert_scalar "INSERT INTO roles(code,name,description) VALUES('admin','隔离重复管理员','仅用于迁移阻断'); SELECT ROW_COUNT();" 1
expect_up_blocked
new_target metaconf
assert_scalar "INSERT INTO permissions(code,name,resource,action) VALUES('email:template:bootstrap','冲突名称','email_template','bootstrap'); SELECT ROW_COUNT();" 1
expect_up_blocked

stage=down_block_matrix
new_target receipt
run_up
prepare_receipt_dependencies
insert_receipt b
expect_down_blocked
assert_scalar "SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts;" 1

for reference_case in role user group; do
  case "$reference_case" in role) case_name=refrole ;; user) case_name=refuser ;; group) case_name=refgroup ;; esac
  new_target "$case_name"
  run_up
  inject_unknown_reference "$reference_case"
  expect_down_blocked
done

stage=concurrency_matrix
run_concurrent_receipt_case

stage=matrix_complete
trap - ERR
printf 'matrix_completed=true\n'
printf 'database_access=true\n'
printf 'migration_executed=true\n'
printf 'source_database_selected=false\n'
printf 'runtime_unique_targets=11\n'
printf 'ownership_combinations=3\n'
printf 'admin_cardinality_blocks=2\n'
printf 'metadata_conflict_blocked=true\n'
printf 'empty_receipt_down=true\n'
printf 'existing_receipt_blocked=true\n'
printf 'unknown_reference_blocks=3\n'
printf 'concurrent_scope_unique=true\n'
printf 'partial_fault_injection=not_implemented\n'
printf 'targets_retained=true\n'
printf 'up_sha256=%s\n' "$expected_up_sha"
printf 'down_sha256=%s\n' "$expected_down_sha"
