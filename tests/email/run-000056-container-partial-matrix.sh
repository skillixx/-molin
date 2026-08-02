#!/bin/bash
set -Eeuo pipefail
umask 077
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

readonly confirm_phrase=I_CONFIRM_000056_PARTIAL_MATRIX_ONCE
readonly execute_gate=I_UNDERSTAND_000056_PARTIAL_CREATES_43_ISOLATION_DATABASES
readonly asset_dir=/root/molin-000056-partial-assets
readonly up_file="$asset_dir/000056_add_email_admin_verify_bootstrap.up.sql"
readonly down_file="$asset_dir/000056_add_email_admin_verify_bootstrap.down.sql"
readonly schema55_file="$asset_dir/schema55.sql"
readonly schema56_file="$asset_dir/schema56.sql"
readonly baseline_manifest="$asset_dir/baseline-manifest.tsv"
readonly boundary_manifest="$asset_dir/000056-partial-boundaries.tsv"
readonly expected_up_sha=9133212C61EB4AA89B72C77D0C353F4B0F8B483080CBFB1E85A0281379861D9B
readonly expected_down_sha=F42A30D70A95AD7BFD876F1515267C5FEE3DDCFD7AAC066453BDC020D201A5C2
readonly expected_boundary_sha=7B9E3132B2A09D939FD81E908C889EE6EE41A69B5D680B52A081D5A0A9BA4A62
readonly expected_schema55_sha=${MOLIN_000056_SCHEMA55_SHA:-}
readonly expected_schema56_sha=${MOLIN_000056_SCHEMA56_SHA:-}
readonly expected_baseline_manifest_sha=${MOLIN_000056_BASELINE_MANIFEST_SHA:-}
readonly expected_asset_uid=${MOLIN_MATRIX_ASSET_UID:-}
readonly target_prefix=molin_56pt_
readonly run_prefix=/root/molin-000056-partial-run-
readonly MYSQL_BIN=/usr/bin/mysql
readonly SHA256SUM_BIN=/usr/bin/sha256sum
readonly AWK_BIN=/usr/bin/awk
readonly CAT_BIN=/usr/bin/cat
readonly GREP_BIN=/usr/bin/grep
readonly STAT_BIN=/usr/bin/stat
readonly MKDIR_BIN=/usr/bin/mkdir
readonly CHMOD_BIN=/usr/bin/chmod
readonly TOUCH_BIN=/usr/bin/touch

stage=initialization
current_case=none
target_created=false
target_db=
run_dir=
evidence_dir=

blocked() {
  printf 'status=blocked reason=explicit_double_gate_required database_access=false migration_executed=false runtime_target=not_created\n'
  exit 2
}

# 默认、自检和单门禁路径必须在首次 MySQL 调用前结束。
if [[ ${1:-} = --self-test && $# -eq 1 ]]; then
  printf 'status=pass mode=selftest cases=43 database_access=false migration_executed=false runtime_target=not_created up_points=27 down_points=14 baselines=2\n'
  exit 0
fi
[[ $# -eq 2 ]] || blocked
[[ $1 = --execute && $2 = "$confirm_phrase" ]] || blocked
[[ ${MOLIN_000056_PARTIAL_EXECUTE:-} = "$execute_gate" ]] || blocked

fail() { false; }
on_error() {
  local exit_code=$?
  trap - ERR
  printf 'partial_matrix_completed=false\nfailure_stage=%s\ncase=%s\ntarget_created=%s\n' "$stage" "$current_case" "$target_created"
  exit "$exit_code"
}
trap on_error ERR

stage=environment_identity
[[ $EUID -eq 0 ]]
[[ -n ${MYSQL_ROOT_PASSWORD:-} ]]
[[ "$expected_asset_uid" =~ ^[1-9][0-9]*$ ]]

stage=environment_hash_inputs
[[ "$expected_schema55_sha" =~ ^[A-F0-9]{64}$ ]]
[[ "$expected_schema56_sha" =~ ^[A-F0-9]{64}$ ]]
[[ "$expected_baseline_manifest_sha" =~ ^[A-F0-9]{64}$ ]]

stage=environment_tools
for tool in "$MYSQL_BIN" "$SHA256SUM_BIN" "$AWK_BIN" "$CAT_BIN" "$GREP_BIN" "$STAT_BIN" "$MKDIR_BIN" "$CHMOD_BIN" "$TOUCH_BIN"; do
  [[ -x "$tool" ]]
done

stage=asset_directory_identity
[[ -d "$asset_dir" && ! -L "$asset_dir" ]]
[[ $($STAT_BIN -c %u -- "$asset_dir") = "$expected_asset_uid" ]]
[[ $($STAT_BIN -c %a -- "$asset_dir") = 700 ]]

verify_asset() {
  local file=$1 expected_sha=${2:-} actual_sha
  [[ -f "$file" && ! -L "$file" ]]
  [[ $($STAT_BIN -c %u -- "$file") = "$expected_asset_uid" ]]
  [[ $($STAT_BIN -c %a -- "$file") = 400 ]]
  actual_sha=$($SHA256SUM_BIN -- "$file" | $AWK_BIN 'NR==1{print toupper($1);next}{exit 1}END{if(NR!=1)exit 1}')
  [[ "$actual_sha" =~ ^[A-F0-9]{64}$ ]]
  if [[ -n "$expected_sha" ]]; then [[ "$actual_sha" = "$expected_sha" ]]; fi
  printf '%s' "$actual_sha"
}

stage=asset_hashes
verify_asset "$up_file" "$expected_up_sha" >/dev/null
verify_asset "$down_file" "$expected_down_sha" >/dev/null
verify_asset "$schema55_file" "$expected_schema55_sha" >/dev/null
verify_asset "$schema56_file" "$expected_schema56_sha" >/dev/null
verify_asset "$baseline_manifest" "$expected_baseline_manifest_sha" >/dev/null
verify_asset "$boundary_manifest" "$expected_boundary_sha" >/dev/null

stage=baseline_manifest_shape
[[ $($AWK_BIN -F '\t' '$1=="schema55.sql"&&$2~/^[A-F0-9]{64}$/&&$3==55&&$4=="complete"{n++}END{print n+0}' "$baseline_manifest") = 1 ]]
[[ $($AWK_BIN -F '\t' '$1=="schema56.sql"&&$2~/^[A-F0-9]{64}$/&&$3==56&&$4=="complete"{n++}END{print n+0}' "$baseline_manifest") = 1 ]]
[[ $($AWK_BIN -F '\t' 'NF!=4{bad++}END{print bad+0}' "$baseline_manifest") = 0 ]]
[[ $($AWK_BIN 'END {print NR+0}' "$baseline_manifest") -eq 2 ]]
[[ "$expected_schema55_sha" = "$($AWK_BIN -F '\t' '$1=="schema55.sql"{print $2}' "$baseline_manifest")" ]]
[[ "$expected_schema56_sha" = "$($AWK_BIN -F '\t' '$1=="schema56.sql"{print $2}' "$baseline_manifest")" ]]

stage=boundary_manifest_shape
[[ $($AWK_BIN -F '\t' '$1=="up"{n++}END{print n+0}' "$boundary_manifest") = 27 ]]
[[ $($AWK_BIN -F '\t' '$1=="down"{n++}END{print n+0}' "$boundary_manifest") = 14 ]]
[[ $($AWK_BIN -F '\t' 'NF!=12||$1!~/^(up|down)$/||$2!~/^(up|down)_[a-z0-9_]+$/ {bad++} {seen[$2]++} END{for(k in seen)if(seen[k]!=1)bad++;print bad+0}' "$boundary_manifest") = 0 ]]

# 先拒绝 MySQL 可执行注释和优化器提示，再净化普通注释与字符串后检查跨 schema 限定名。
baseline_without_literals_and_comments() {
  "$AWK_BIN" '
    BEGIN{block=0}
    {
      line=$0;out="";i=1;n=length(line)
      while(i<=n){
        two=substr(line,i,2);ch=substr(line,i,1)
        if(block){if(two=="*/"){block=0;i+=2}else i++;continue}
        if(two=="/*"){block=1;i+=2;continue}
        if(ch=="#"||(two=="--"&&(i+2>n||substr(line,i+2,1)~/[[:space:]]/)))break
        if(ch=="\047"||ch=="\042"){
          quote=ch;closed=0;i++
          while(i<=n){
            ch=substr(line,i,1)
            if(ch=="\\"){i+=2;continue}
            if(ch==quote){if(substr(line,i+1,1)==quote){i+=2;continue};i++;closed=1;break}
            i++
          }
          if(!closed)exit 44
          out=out " ";continue
        }
        out=out ch;i++
      }
      print out
    }
    END{if(block)exit 43}
  ' "$1"
}

for baseline in "$schema55_file" "$schema56_file"; do
  if LC_ALL=C "$GREP_BIN" -Eq '/\*!|/\*\+' "$baseline"; then fail; fi
  if baseline_without_literals_and_comments "$baseline" | LC_ALL=C "$GREP_BIN" -Eq '(`[^`]+`|[A-Za-z_][A-Za-z0-9_]*)[[:space:]]*\.[[:space:]]*(`[^`]+`|[A-Za-z_][A-Za-z0-9_]*)'; then fail; fi
  if LC_ALL=C "$GREP_BIN" -Eiq '^[[:space:]]*(USE[[:space:]]|CREATE[[:space:]]+(DATABASE|SCHEMA)[[:space:]]|DROP[[:space:]]+(DATABASE|SCHEMA)[[:space:]]|GRANT[[:space:]]|REVOKE[[:space:]]|CREATE[[:space:]]+USER[[:space:]]|ALTER[[:space:]]+USER[[:space:]]|DROP[[:space:]]+USER[[:space:]]|SET[[:space:]]+GLOBAL[[:space:]])' "$baseline"; then fail; fi
done

report_mysql_failure() {
  local exit_code=$1 stderr_file=$2 stderr_length category
  stderr_length=$($STAT_BIN -c %s -- "$stderr_file")
  category=$($AWK_BIN 'BEGIN{IGNORECASE=1;c="other"}/access denied|authentication plugin|using password/{c="authentication"}/can.t connect|connection refused|lost connection|server has gone away/{c="connectivity"}/unknown database|doesn.t exist|no such file/{c="missing_resource"}/syntax error|you have an error in your sql syntax/{c="sql_syntax"}/duplicate entry|check constraint/{c="constraint"}/molin_000056_injected_boundary/{c="injected_boundary"}END{print c}' "$stderr_file")
  printf 'mysql_failure_category=%s\nmysql_exit_code=%s\nmysql_stderr_length=%s\n' "$category" "$exit_code" "$stderr_length" >&2
  : > "$stderr_file"
}

mysql_admin() {
  local sql=$1 exit_code
  : > "$evidence_dir/mysql.stdout"; : > "$evidence_dir/mysql.stderr"; "$CHMOD_BIN" 600 "$evidence_dir/mysql.stdout" "$evidence_dir/mysql.stderr"
  trap - ERR
  set +e
  MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --default-character-set=utf8mb4 --host=127.0.0.1 --port=3306 --user=root --batch --skip-column-names --raw --execute="$sql" >"$evidence_dir/mysql.stdout" 2>"$evidence_dir/mysql.stderr"
  exit_code=$?
  set -e
  trap on_error ERR
  if [[ $exit_code -ne 0 ]]; then : > "$evidence_dir/mysql.stdout"; report_mysql_failure "$exit_code" "$evidence_dir/mysql.stderr"; return "$exit_code"; fi
  "$CAT_BIN" "$evidence_dir/mysql.stdout"; : > "$evidence_dir/mysql.stdout"; : > "$evidence_dir/mysql.stderr"
}

mysql_query() {
  local sql=$1 exit_code
  : > "$evidence_dir/mysql.stdout"; : > "$evidence_dir/mysql.stderr"; "$CHMOD_BIN" 600 "$evidence_dir/mysql.stdout" "$evidence_dir/mysql.stderr"
  trap - ERR
  set +e
  MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --default-character-set=utf8mb4 --host=127.0.0.1 --port=3306 --user=root --database="$target_db" --batch --skip-column-names --raw --execute="$sql" >"$evidence_dir/mysql.stdout" 2>"$evidence_dir/mysql.stderr"
  exit_code=$?
  set -e
  trap on_error ERR
  if [[ $exit_code -ne 0 ]]; then : > "$evidence_dir/mysql.stdout"; report_mysql_failure "$exit_code" "$evidence_dir/mysql.stderr"; return "$exit_code"; fi
  "$CAT_BIN" "$evidence_dir/mysql.stdout"; : > "$evidence_dir/mysql.stdout"; : > "$evidence_dir/mysql.stderr"
}

mysql_file() {
  local file=$1 mode=${2:-enforced} exit_code
  [[ "$mode" = enforced || "$mode" = baseline_restore ]]
  : > "$evidence_dir/mysql.stderr"; "$CHMOD_BIN" 600 "$evidence_dir/mysql.stderr"
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
  if [[ $exit_code -ne 0 ]]; then report_mysql_failure "$exit_code" "$evidence_dir/mysql.stderr"; return "$exit_code"; fi
  : > "$evidence_dir/mysql.stderr"
}

mysql_expect_injected_failure() {
  local exit_code
  : > "$evidence_dir/mysql.stderr"; "$CHMOD_BIN" 600 "$evidence_dir/mysql.stderr"
  trap - ERR
  set +e
  MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$MYSQL_BIN" --no-defaults --default-character-set=utf8mb4 --host=127.0.0.1 --port=3306 --user=root --database="$target_db" --batch --skip-column-names --raw --execute="SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='molin_000056_injected_boundary';" >/dev/null 2>"$evidence_dir/mysql.stderr"
  exit_code=$?
  set -e
  trap on_error ERR
  [[ $exit_code -ne 0 ]]
  report_mysql_failure "$exit_code" "$evidence_dir/mysql.stderr"
}

assert_scalar() { local actual; actual=$(mysql_query "$1"); [[ "$actual" = "$2" ]]; }

emit_prefix() {
  local source=$1 limit=$2 output=$3
  "$AWK_BIN" -v limit="$limit" '{print}/^[[:space:]]*--/{next}/;[[:space:]]*$/{count++;if(count==limit){done=1;exit}}END{if(!done)exit 41}' "$source" > "$output"
  "$CHMOD_BIN" 600 "$output"
}

new_target() {
  local case_name=$1 baseline=$2 uuid suffix baseline_file target_hash
  current_case=$case_name;target_created=false
  uuid=$($CAT_BIN /proc/sys/kernel/random/uuid)
  [[ "$uuid" =~ ^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$ ]]
  suffix=${uuid//-/};target_db="${target_prefix}${suffix}";run_dir="${run_prefix}${suffix}-${case_name}";evidence_dir="$run_dir/evidence"
  [[ "$target_db" =~ ^molin_56pt_[a-f0-9]{32}$ ]]
  [[ "$run_dir" =~ ^/root/molin-000056-partial-run-[a-f0-9]{32}-(up|down)_[a-z0-9_]+$ ]]
  [[ ! -e "$run_dir" ]]
  "$MKDIR_BIN" --mode=0700 -- "$run_dir"; "$MKDIR_BIN" --mode=0700 -- "$evidence_dir"
  printf '%s\t%s\n' "$case_name" "$target_db" > "$evidence_dir/target.tsv"; "$CHMOD_BIN" 600 "$evidence_dir/target.tsv"
  [[ $(mysql_admin "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = '$target_db';") = 0 ]]
  mysql_admin "CREATE DATABASE \`$target_db\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" >/dev/null
  target_created=true
  case "$baseline" in 55)baseline_file=$schema55_file;;56)baseline_file=$schema56_file;;*)fail;;esac
  mysql_file "$baseline_file" baseline_restore
  assert_scalar "SELECT CONCAT(version, ':', dirty) FROM schema_migrations;" "$baseline:0"
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('email_provider_templates','email_scene_bindings','email_template_sync_runs','email_test_recipient_allowlist','email_send_logs','migration_000055_permission_ownership');" 6
  if [[ $baseline = 55 ]]; then
    assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('email_admin_verify_bootstrap_receipts','migration_000056_permission_ownership','migration_000056_assertions');" 0
    assert_scalar "SELECT COUNT(*) FROM permissions WHERE code='email:template:bootstrap';" 0
  else
    assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('email_admin_verify_bootstrap_receipts','migration_000056_permission_ownership');" 2
    assert_scalar "SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts;" 0
    assert_scalar "SELECT CONCAT(permission_created,':',admin_binding_created,':',permission_id IS NOT NULL,':',admin_role_permission_id IS NOT NULL) FROM migration_000056_permission_ownership WHERE permission_code='email:template:bootstrap';" 1:1:1:1
  fi
  target_hash=$(printf '%s' "$target_db" | "$SHA256SUM_BIN" | "$AWK_BIN" '{print toupper($1)}')
  printf 'case=%s target_id_sha256=%s restored_schema=%s\n' "$case_name" "$target_hash" "$baseline"
}

assert_partial_state() {
  local direction=$1 assertion_table=$2 assertion_rows=$3 receipt_table=$4 ownership_table=$5 ownership_rows=$6 permissions=$7 bindings=$8 permission_ids=$9 binding_ids=${10} expected_version
  if [[ $direction = up ]]; then expected_version=55:1;else expected_version=56:1;fi
  assert_scalar "SELECT CONCAT(version, ':', dirty) FROM schema_migrations;" "$expected_version"
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='migration_000056_assertions';" "$assertion_table"
  if [[ $assertion_table = 1 ]]; then
    assert_scalar "SELECT COUNT(*) FROM migration_000056_assertions;" "$assertion_rows"
    assert_scalar "SELECT COUNT(*) FROM migration_000056_assertions WHERE passed=1;" "$assertion_rows"
    assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema=DATABASE() AND table_name='migration_000056_assertions' AND constraint_name='chk_migration_000056_assertion' AND constraint_type='CHECK';" 1
  fi
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='email_admin_verify_bootstrap_receipts';" "$receipt_table"
  if [[ $receipt_table = 1 ]]; then
    assert_scalar "SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts;" 0
    assert_scalar "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='email_admin_verify_bootstrap_receipts';" 9
    assert_scalar "SELECT COUNT(DISTINCT index_name) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='email_admin_verify_bootstrap_receipts';" 5
    assert_scalar "SELECT COUNT(*) FROM information_schema.referential_constraints WHERE constraint_schema=DATABASE() AND table_name='email_admin_verify_bootstrap_receipts';" 2
    assert_scalar "SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema=DATABASE() AND table_name='email_admin_verify_bootstrap_receipts' AND constraint_type='CHECK';" 5
  fi
  assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='migration_000056_permission_ownership';" "$ownership_table"
  if [[ $ownership_table = 1 ]]; then
    assert_scalar "SELECT COUNT(*) FROM migration_000056_permission_ownership;" "$ownership_rows"
    if [[ $ownership_rows = 1 ]]; then
      assert_scalar "SELECT CONCAT(permission_created,':',admin_binding_created) FROM migration_000056_permission_ownership WHERE permission_code='email:template:bootstrap';" 1:1
      assert_scalar "SELECT COUNT(*) FROM migration_000056_permission_ownership WHERE permission_id IS NOT NULL;" "$permission_ids"
      assert_scalar "SELECT COUNT(*) FROM migration_000056_permission_ownership WHERE admin_role_permission_id IS NOT NULL;" "$binding_ids"
    fi
  fi
  assert_scalar "SELECT COUNT(*) FROM permissions WHERE code='email:template:bootstrap';" "$permissions"
  assert_scalar "SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id AND r.code='admin' JOIN permissions p ON p.id=rp.permission_id AND p.code='email:template:bootstrap';" "$bindings"
}

run_partial_case() {
  local direction=$1 case_name=$2 boundary=$3 assertion_table=$4 assertion_rows=$5 receipt_table=$6 ownership_table=$7 ownership_rows=$8 permissions=$9 bindings=${10} permission_ids=${11} binding_ids=${12} plan_file
  if [[ $direction = up ]]; then new_target "$case_name" 55;else new_target "$case_name" 56;fi
  stage="${case_name}_mark_dirty"
  assert_scalar "UPDATE schema_migrations SET dirty=1 WHERE version=$([[ $direction = up ]]&&printf 55||printf 56) AND dirty=0; SELECT ROW_COUNT();" 1
  plan_file="$evidence_dir/executed-prefix.sql";stage="${case_name}_execute_boundary"
  if [[ $direction = up ]]; then emit_prefix "$up_file" "$boundary" "$plan_file";else emit_prefix "$down_file" "$boundary" "$plan_file";fi
  mysql_file "$plan_file";stage="${case_name}_inject_failure";mysql_expect_injected_failure
  stage="${case_name}_validate_state"
  assert_partial_state "$direction" "$assertion_table" "$assertion_rows" "$receipt_table" "$ownership_table" "$ownership_rows" "$permissions" "$bindings" "$permission_ids" "$binding_ids"
  printf '%s\t%s\t%s\n' "$direction" "$case_name" "$boundary" > "$evidence_dir/boundary.tsv"; "$CHMOD_BIN" 600 "$evidence_dir/boundary.tsv"
  "$TOUCH_BIN" "$evidence_dir/partial_verified"; "$CHMOD_BIN" 600 "$evidence_dir/partial_verified"
}

run_baseline() {
  local direction=$1 case_name="${1}_baseline"
  if [[ $direction = up ]]; then
    new_target "$case_name" 55
    assert_scalar "UPDATE schema_migrations SET dirty=1 WHERE version=55 AND dirty=0; SELECT ROW_COUNT();" 1
    verify_asset "$up_file" "$expected_up_sha" >/dev/null;mysql_file "$up_file"
    assert_scalar "UPDATE schema_migrations SET version=56,dirty=0 WHERE version=55 AND dirty=1; SELECT ROW_COUNT();" 1
    assert_scalar "SELECT CONCAT(version,':',dirty) FROM schema_migrations;" 56:0
    assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='migration_000056_assertions';" 0
    assert_scalar "SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts;" 0
    assert_scalar "SELECT CONCAT(permission_created,':',admin_binding_created,':',permission_id IS NOT NULL,':',admin_role_permission_id IS NOT NULL) FROM migration_000056_permission_ownership WHERE permission_code='email:template:bootstrap';" 1:1:1:1
    assert_scalar "SELECT COUNT(*) FROM permissions WHERE code='email:template:bootstrap';" 1
    assert_scalar "SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id AND r.code='admin' JOIN permissions p ON p.id=rp.permission_id AND p.code='email:template:bootstrap';" 1
  else
    new_target "$case_name" 56
    assert_scalar "UPDATE schema_migrations SET dirty=1 WHERE version=56 AND dirty=0; SELECT ROW_COUNT();" 1
    verify_asset "$down_file" "$expected_down_sha" >/dev/null;mysql_file "$down_file"
    assert_scalar "UPDATE schema_migrations SET version=55,dirty=0 WHERE version=56 AND dirty=1; SELECT ROW_COUNT();" 1
    assert_scalar "SELECT CONCAT(version,':',dirty) FROM schema_migrations;" 55:0
    assert_scalar "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('email_admin_verify_bootstrap_receipts','migration_000056_permission_ownership','migration_000056_assertions');" 0
    assert_scalar "SELECT COUNT(*) FROM permissions WHERE code='email:template:bootstrap';" 0
  fi
  "$TOUCH_BIN" "$evidence_dir/baseline_verified"; "$CHMOD_BIN" 600 "$evidence_dir/baseline_verified"
}

stage=statement_boundary_precheck
[[ $($AWK_BIN '/^[[:space:]]*--/{next}/;[[:space:]]*$/{n++}END{print n+0}' "$up_file") = 27 ]]
[[ $($AWK_BIN '/^[[:space:]]*--/{next}/;[[:space:]]*$/{n++}END{print n+0}' "$down_file") = 14 ]]

stage=up_partial_matrix
while IFS=$'\t' read -r direction case_name boundary assertion_table assertion_rows receipt_table ownership_table ownership_rows permissions bindings permission_ids binding_ids;do
  [[ $direction = up ]]||continue
  run_partial_case "$direction" "$case_name" "$boundary" "$assertion_table" "$assertion_rows" "$receipt_table" "$ownership_table" "$ownership_rows" "$permissions" "$bindings" "$permission_ids" "$binding_ids"
done < "$boundary_manifest"

stage=down_partial_matrix
while IFS=$'\t' read -r direction case_name boundary assertion_table assertion_rows receipt_table ownership_table ownership_rows permissions bindings permission_ids binding_ids;do
  [[ $direction = down ]]||continue
  run_partial_case "$direction" "$case_name" "$boundary" "$assertion_table" "$assertion_rows" "$receipt_table" "$ownership_table" "$ownership_rows" "$permissions" "$bindings" "$permission_ids" "$binding_ids"
done < "$boundary_manifest"

stage=no_injection_baselines
run_baseline up
run_baseline down

stage=partial_matrix_complete
trap - ERR
printf 'partial_matrix_completed=true\ndatabase_access=true\nmigration_executed=true\nsource_database_selected=false\nruntime_unique_targets=43\npartial_up_points=27\npartial_down_points=14\nno_injection_baselines=2\nboundary_state_assertions=true\nbase_runner_partial_status_unchanged=not_implemented\ncombined_partial_evidence=provided_by_separate_asset\ntargets_retained=true\n'
printf 'up_sha256=%s\ndown_sha256=%s\nboundary_sha256=%s\n' "$expected_up_sha" "$expected_down_sha" "$expected_boundary_sha"
