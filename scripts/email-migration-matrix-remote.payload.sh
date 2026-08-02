#!/usr/bin/env bash
# 在两个顺序执行的无网络临时 MySQL 8 容器中生成基线并完成 000055/000056 隔离矩阵。
set -Eeuo pipefail
umask 077
exec 2>/dev/null
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

readonly confirm_phrase=I_CONFIRM_EMAIL_MIGRATION_FULL_ISOLATION_MATRIX_ONCE
readonly docker_bin=/usr/bin/docker
readonly sha256sum_bin=/usr/bin/sha256sum
readonly awk_link=/usr/bin/awk
readonly stat_bin=/usr/bin/stat
readonly id_bin=/usr/bin/id
readonly realpath_bin=/usr/bin/realpath
readonly install_bin=/usr/bin/install
readonly mkdir_bin=/usr/bin/mkdir
readonly chmod_bin=/usr/bin/chmod
readonly rm_bin=/usr/bin/rm
readonly rmdir_bin=/usr/bin/rmdir
readonly wc_bin=/usr/bin/wc
readonly sort_bin=/usr/bin/sort
readonly tail_bin=/usr/bin/tail

stage=argument_gate
matrix_container_id=
matrix_container_name=
stage_root=
matrix_password=

fail() {
  trap - ERR
  printf 'status=failed mode=email_migration_full_isolation_matrix stage=%s stage_retained=true temporary_container_retained=false retries=0\n' "${1:?stage_required}"
  exit 2
}

cleanup_container() {
  local exit_code=$?
  trap - EXIT
  if [[ -n "$matrix_container_id" && "$matrix_container_id" =~ ^[a-f0-9]{64}$ ]]; then
    "$docker_bin" rm --force -- "$matrix_container_id" >/dev/null 2>&1 || true
  fi
  matrix_password=
  exit "$exit_code"
}
trap cleanup_container EXIT
trap 'fail "$stage"' ERR

[[ $# -eq 3 && $1 = --execute && $2 = "$confirm_phrase" ]] || fail argument_gate
readonly nonce=$3
[[ "$nonce" =~ ^[a-f0-9]{32}$ ]] || fail argument_gate
stage_root="/home/pc/molin-runtime/email-migration-matrix-${nonce}"
readonly source_root="$stage_root/source"
readonly baseline_root="$stage_root/baselines"
readonly asset_root="$stage_root/assets"
readonly output_root="$stage_root/output"

stage=host_identity
[[ $(uname -s) = Linux ]]
for tool in "$docker_bin" "$sha256sum_bin" "$stat_bin" "$id_bin" "$realpath_bin" "$install_bin" "$mkdir_bin" "$chmod_bin" "$rm_bin" "$rmdir_bin" "$wc_bin" "$sort_bin" "$tail_bin"; do
  [[ -x "$tool" && ! -L "$tool" ]] || fail host_tool
done
[[ -x "$awk_link" ]] || fail host_tool
awk_bin=$("$realpath_bin" -e -- "$awk_link") || fail host_tool
[[ "$awk_bin" =~ ^/usr/bin/[A-Za-z0-9._+-]+$ ]] || fail host_tool
[[ -f "$awk_bin" && ! -L "$awk_bin" && -x "$awk_bin" ]] || fail host_tool
[[ "$("$stat_bin" -c '%U:%G' -- "$awk_bin")" = root:root ]] || fail host_tool
readonly awk_bin
[[ -d "$stage_root" && ! -L "$stage_root" && "$($stat_bin -c '%U:%a' -- "$stage_root")" = pc:700 ]] || fail stage_identity
[[ -d "$source_root" && ! -L "$source_root" && "$($stat_bin -c '%U' -- "$source_root")" = pc ]] || fail source_identity
[[ ! -e "$baseline_root" && ! -L "$baseline_root" && ! -e "$asset_root" && ! -L "$asset_root" && ! -e "$output_root" && ! -L "$output_root" ]] || fail target_collision

readonly generator="$source_root/scripts/generate-email-migration-baselines.sh"
readonly runner55="$source_root/tests/email/run-000055-container-isolation-matrix.sh"
readonly partial55="$source_root/tests/email/run-000055-container-partial-matrix.sh"
readonly runner56="$source_root/tests/email/run-000056-container-isolation-matrix.sh"
readonly partial56="$source_root/tests/email/run-000056-container-partial-matrix.sh"
readonly boundary55="$source_root/tests/email/000055-partial-boundaries.tsv"
readonly boundary56="$source_root/tests/email/000056-partial-boundaries.tsv"
readonly up55="$source_root/server/migrations/000055_add_directmail_email_management.up.sql"
readonly down55="$source_root/server/migrations/000055_add_directmail_email_management.down.sql"
readonly up56="$source_root/server/migrations/000056_add_email_admin_verify_bootstrap.up.sql"
readonly down56="$source_root/server/migrations/000056_add_email_admin_verify_bootstrap.down.sql"

for source_file in "$generator" "$runner55" "$partial55" "$runner56" "$partial56" "$boundary55" "$boundary56" "$up55" "$down55" "$up56" "$down56"; do
  [[ -f "$source_file" && ! -L "$source_file" && "$($stat_bin -c '%U' -- "$source_file")" = pc ]] || fail source_identity
done

stage=image_identity
mapfile -t main_mysql_ids < <("$docker_bin" ps --filter 'name=^/molin-mysql$' --format '{{.ID}}')
[[ ${#main_mysql_ids[@]} -eq 1 && "${main_mysql_ids[0]}" =~ ^[a-f0-9]{12,64}$ ]] || fail main_mysql_identity
readonly main_mysql_id=${main_mysql_ids[0]}
readonly image_tag=$("$docker_bin" inspect --format '{{.Config.Image}}' "$main_mysql_id")
[[ "$image_tag" =~ ^mysql:[A-Za-z0-9._-]+$ || "$image_tag" =~ ^mysql@sha256:[a-f0-9]{64}$ ]] || fail image_tag
readonly image_id=$("$docker_bin" image inspect --format '{{.Id}}' "$image_tag")
[[ "$image_id" =~ ^sha256:[a-f0-9]{64}$ ]] || fail image_id
mapfile -t image_digests < <("$docker_bin" image inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "$image_tag" | "$awk_bin" '/^mysql@sha256:[a-f0-9]{64}$/{print}')
[[ ${#image_digests[@]} -eq 1 ]] || fail image_digest
readonly image_ref=${image_digests[0]}
[[ "$("$docker_bin" image inspect --format '{{.Id}}' "$image_ref")" = "$image_id" ]] || fail image_binding

stage=baseline_generation
"$mkdir_bin" --mode=0700 -- "$baseline_root" "$output_root"
"$chmod_bin" 500 "$generator"
set +e
MOLIN_EMAIL_BASELINE_GENERATION_EXECUTE=I_UNDERSTAND_TEMPORARY_NETWORKLESS_MYSQL8_WILL_BE_CREATED \
MOLIN_MYSQL8_IMAGE_REF="$image_ref" MOLIN_MYSQL8_IMAGE_ID="$image_id" MOLIN_BASELINE_OUTPUT_DIR="$baseline_root" \
  "$generator" --execute I_CONFIRM_EMAIL_MIGRATION_BASELINE_GENERATION_ONCE >"$output_root/baseline.stdout" 2>"$output_root/baseline.stderr"
baseline_exit=$?
set -e
[[ $baseline_exit -eq 0 && ! -s "$output_root/baseline.stderr" ]] || fail baseline_generation
[[ "$(cat "$output_root/baseline.stdout")" = 'status=pass mode=email_migration_baseline_generation migrations=56 mysql8_image_bound=true mysql8_runtime_verified=true network=none outputs=6 schema54_empty=true schema54_legacy=true schema55=true schema56=true manifests=2 container_removed_on_exit=true' ]] || fail baseline_summary

stage=asset_prepare
readonly asset55_full="$asset_root/molin-000055-isolation-assets"
readonly asset55_partial="$asset_root/molin-000055-partial-assets"
readonly asset56_full="$asset_root/molin-000056-isolation-assets"
readonly asset56_partial="$asset_root/molin-000056-partial-assets"
"$mkdir_bin" --mode=0700 -- "$asset_root" "$asset55_full" "$asset55_partial" "$asset56_full" "$asset56_partial"

install_readonly() {
  local source=${1:?source_required} destination=${2:?destination_required}
  "$install_bin" --mode=0400 -- "$source" "$destination"
  [[ -f "$destination" && ! -L "$destination" && "$($stat_bin -c '%a' -- "$destination")" = 400 ]]
}

install_runner() {
  local source=${1:?source_required} destination=${2:?destination_required}
  "$install_bin" --mode=0500 -- "$source" "$destination"
  [[ -f "$destination" && ! -L "$destination" && "$($stat_bin -c '%a' -- "$destination")" = 500 ]]
}

install_readonly "$up55" "$asset55_full/000055_add_directmail_email_management.up.sql"
install_readonly "$down55" "$asset55_full/000055_add_directmail_email_management.down.sql"
install_readonly "$baseline_root/schema54-empty.sql" "$asset55_full/schema54-empty.sql"
install_readonly "$baseline_root/schema54-legacy.sql" "$asset55_full/schema54-legacy.sql"
install_readonly "$baseline_root/schema55.sql" "$asset55_full/schema55.sql"
install_readonly "$baseline_root/000055-baseline-manifest.tsv" "$asset55_full/baseline-manifest.tsv"
install_runner "$runner55" "$asset55_full/runner.sh"

install_readonly "$up55" "$asset55_partial/000055_add_directmail_email_management.up.sql"
install_readonly "$down55" "$asset55_partial/000055_add_directmail_email_management.down.sql"
install_readonly "$baseline_root/schema54-legacy.sql" "$asset55_partial/schema54-legacy.sql"
install_readonly "$baseline_root/schema55.sql" "$asset55_partial/schema55.sql"
install_readonly "$boundary55" "$asset55_partial/000055-partial-boundaries.tsv"
"$awk_bin" -F '\t' '$1=="schema54-legacy.sql" || $1=="schema55.sql"' "$baseline_root/000055-baseline-manifest.tsv" > "$asset55_partial/baseline-manifest.tsv"
"$chmod_bin" 400 "$asset55_partial/baseline-manifest.tsv"
[[ $("$wc_bin" -l < "$asset55_partial/baseline-manifest.tsv") -eq 2 ]] || fail partial55_manifest
install_runner "$partial55" "$asset55_partial/runner.sh"

for target in "$asset56_full" "$asset56_partial"; do
  install_readonly "$up56" "$target/000056_add_email_admin_verify_bootstrap.up.sql"
  install_readonly "$down56" "$target/000056_add_email_admin_verify_bootstrap.down.sql"
  install_readonly "$baseline_root/schema55.sql" "$target/schema55.sql"
  install_readonly "$baseline_root/schema56.sql" "$target/schema56.sql"
  install_readonly "$baseline_root/000056-baseline-manifest.tsv" "$target/baseline-manifest.tsv"
done
install_runner "$runner56" "$asset56_full/runner.sh"
install_readonly "$boundary56" "$asset56_partial/000056-partial-boundaries.tsv"
install_runner "$partial56" "$asset56_partial/runner.sh"

# bind mount 会保留宿主机属主；先在宿主端冻结 pc 的数值 UID，再由容器内逐项复核。
asset_uid=$("$id_bin" -u pc) || fail asset_owner
[[ "$asset_uid" =~ ^[1-9][0-9]*$ ]] || fail asset_owner
[[ "$("$stat_bin" -c '%u' -- "$stage_root")" = "$asset_uid" ]] || fail asset_owner
for asset_dir in "$asset55_full" "$asset55_partial" "$asset56_full" "$asset56_partial"; do
  [[ "$("$stat_bin" -c '%u:%a' -- "$asset_dir")" = "$asset_uid:700" ]] || fail asset_owner
  for asset_file in "$asset_dir"/*; do
    [[ -f "$asset_file" && ! -L "$asset_file" ]] || fail asset_owner
    [[ "$("$stat_bin" -c '%u' -- "$asset_file")" = "$asset_uid" ]] || fail asset_owner
    asset_mode=$("$stat_bin" -c '%a' -- "$asset_file") || fail asset_owner
    [[ "$asset_mode" = 400 || ( "${asset_file##*/}" = runner.sh && "$asset_mode" = 500 ) ]] || fail asset_owner
  done
done
readonly asset_uid

readonly sha54_legacy=$("$sha256sum_bin" "$baseline_root/schema54-legacy.sql" | "$awk_bin" '{print toupper($1)}')
readonly sha55=$("$sha256sum_bin" "$baseline_root/schema55.sql" | "$awk_bin" '{print toupper($1)}')
readonly sha56=$("$sha256sum_bin" "$baseline_root/schema56.sql" | "$awk_bin" '{print toupper($1)}')
readonly manifest55_partial_sha=$("$sha256sum_bin" "$asset55_partial/baseline-manifest.tsv" | "$awk_bin" '{print toupper($1)}')
readonly manifest56_sha=$("$sha256sum_bin" "$baseline_root/000056-baseline-manifest.tsv" | "$awk_bin" '{print toupper($1)}')
for value in "$sha54_legacy" "$sha55" "$sha56" "$manifest55_partial_sha" "$manifest56_sha"; do [[ "$value" =~ ^[A-F0-9]{64}$ ]] || fail baseline_hash; done

stage=matrix_container_start
matrix_password=$(tr -d '-' < /proc/sys/kernel/random/uuid)
[[ "$matrix_password" =~ ^[a-f0-9]{32}$ ]] || fail matrix_password
matrix_container_name="molin-email-matrix-${nonce}"
set +e
matrix_container_id=$("$docker_bin" run --detach --name "$matrix_container_name" --label "molin.phase4.matrix=$nonce" \
  --network none --read-only --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=2g \
  --tmpfs /var/run/mysqld:rw,noexec,nosuid,size=16m --tmpfs /tmp:rw,noexec,nosuid,size=128m \
  --tmpfs /root:rw,nosuid,nodev,size=512m \
  --mount "type=bind,src=$asset55_full,dst=/root/molin-000055-isolation-assets,readonly" \
  --mount "type=bind,src=$asset55_partial,dst=/root/molin-000055-partial-assets,readonly" \
  --mount "type=bind,src=$asset56_full,dst=/root/molin-000056-isolation-assets,readonly" \
  --mount "type=bind,src=$asset56_partial,dst=/root/molin-000056-partial-assets,readonly" \
  --env "MYSQL_ROOT_PASSWORD=$matrix_password" "$image_ref" --skip-log-bin \
  2>"$output_root/matrix-container.stderr")
container_exit=$?
set -e
[[ $container_exit -eq 0 && "$matrix_container_id" =~ ^[a-f0-9]{64}$ ]] || fail matrix_container_start
[[ "$("$docker_bin" inspect --format '{{.Name}}|{{.HostConfig.NetworkMode}}|{{.Image}}' "$matrix_container_id")" = "/$matrix_container_name|none|$image_id" ]] || fail matrix_container_identity
mapfile -t asset_mounts < <("$docker_bin" inspect --format '{{range .Mounts}}{{printf "%s|%s|%s|%t\n" .Type .Source .Destination .RW}}{{end}}' "$matrix_container_id" |
  "$awk_bin" -F '|' '$3 ~ /^\/root\/molin-0000(55|56)-(isolation|partial)-assets$/ {print}' | "$sort_bin")
[[ ${#asset_mounts[@]} -eq 4 ]] || fail asset_mount_identity
[[ "${asset_mounts[0]}" = "bind|$asset55_full|/root/molin-000055-isolation-assets|false" ]] || fail asset_mount_identity
[[ "${asset_mounts[1]}" = "bind|$asset55_partial|/root/molin-000055-partial-assets|false" ]] || fail asset_mount_identity
[[ "${asset_mounts[2]}" = "bind|$asset56_full|/root/molin-000056-isolation-assets|false" ]] || fail asset_mount_identity
[[ "${asset_mounts[3]}" = "bind|$asset56_partial|/root/molin-000056-partial-assets|false" ]] || fail asset_mount_identity

stage=matrix_mysql_ready
ready=false
for _ in $(seq 1 60); do
  if "$docker_bin" exec -e "MYSQL_PWD=$matrix_password" "$matrix_container_id" mysqladmin --no-defaults --host=127.0.0.1 --user=root ping >/dev/null 2>&1; then ready=true; break; fi
  sleep 1
done
[[ "$ready" = true ]] || fail matrix_mysql_ready
matrix_version=$("$docker_bin" exec "$matrix_container_id" mysql --no-defaults --version) || fail matrix_mysql_version
[[ "$matrix_version" =~ ^mysql[[:space:]]+Ver[[:space:]]+8\.[0-9]+\.[0-9]+[[:space:]]+for[[:space:]]+Linux[[:space:]]+on[[:space:]]+[A-Za-z0-9_.-]+[[:space:]]+\(MySQL[[:space:]]+Community[[:space:]]+Server[[:space:]]+-[[:space:]]+GPL\)$ ]] || fail matrix_mysql_version
unset matrix_version

validate_stderr() {
  local file=${1:?file_required}
  "$awk_bin" '
    /^mysql_failure_category=(authentication|connectivity|missing_resource|sql_syntax|concurrency|constraint|injected_boundary|other)$/ {next}
    /^mysql_exit_code=[0-9]+$/ {next}
    /^mysql_stderr_length=[0-9]+$/ {next}
    NF == 0 {next}
    {bad=1}
    END {exit bad}
  ' "$file"
}

validate_stdout() {
  local file case_spec expected
  file=${1:?file_required}
  case_spec=${2:?case_spec_required}
  expected=${3:?expected_required}
  local expected_count terminal
  expected_count=$($awk_bin -v spec="$case_spec" 'BEGIN {print split(spec, entries, ",")}') || return 1
  [[ "$expected_count" =~ ^[1-9][0-9]*$ ]] || return 1
  "$awk_bin" -v spec="$case_spec" '
    BEGIN {
      expected_count=split(spec, entries, ",")
      for (i=1; i<=expected_count; i++) {
        split(entries[i], pair, ":")
        expected_case[i]=pair[1]
        expected_schema[i]=pair[2]
      }
    }
    NR<=expected_count {
      if ($0 !~ /^case=[a-z0-9_]+ target_id_sha256=[A-F0-9]+ restored_schema=(54|55|56)$/) {bad=1; next}
      split($1, case_field, "="); split($2, hash_field, "="); split($3, schema_field, "=")
      if (length(hash_field[2])!=64 || case_field[2]!=expected_case[NR] || schema_field[2]!=expected_schema[NR] || seen[hash_field[2]]++) bad=1
      progress++
      next
    }
    END {if (bad || progress!=expected_count || NR<=expected_count) exit 1}
  ' "$file" || return 1
  terminal=$("$tail_bin" -n "+$((expected_count + 1))" -- "$file") || return 1
  [[ "$terminal" = "$expected" ]]
}

# 子 runner 失败时只解析冻结的四行摘要，避免上层把可诊断错误压缩成通用执行失败。
classify_runner_failure() {
  local name file
  name=${1:?name_required}
  file=${2:?file_required}
  [[ "$name" = partial55 ]] || return 1
  "$awk_bin" '
    NR == 1 {if ($0 != "partial_matrix_completed=false") bad=1; next}
    NR == 2 {
      split($0, field, "="); failure_stage=field[2]
      if ($0 ~ /^failure_stage=(environment_identity|environment_hash_inputs|environment_tools|asset_directory_identity|asset_hashes|baseline_manifest_shape|boundary_manifest_shape|statement_boundary_precheck)$/) family="precheck"
      else if ($0 ~ /^failure_stage=(up|down)_[a-z0-9_]+_(mark_dirty|execute_boundary|inject_failure|validate_state)$/) family="dynamic"
      else if ($0 ~ /^failure_stage=(up_partial_matrix|down_partial_matrix|no_injection_baselines)$/) family="matrix"
      else bad=1
      next
    }
    NR == 3 {if ($0 !~ /^case=(none|(up|down)_[a-z0-9_]+|(up|down)_baseline)$/) bad=1; split($0, field, "="); case_value=field[2]; next}
    NR == 4 {if ($0 !~ /^target_created=(true|false)$/) bad=1; split($0, field, "="); target_value=field[2]; next}
    NR > 4 {bad=1}
    END {
      dynamic_case=failure_stage; sub(/_(mark_dirty|execute_boundary|inject_failure|validate_state)$/, "", dynamic_case)
      pair_ok=(family=="precheck" && case_value=="none" && target_value=="false") ||
              (family=="dynamic" && case_value==dynamic_case && target_value=="true") ||
              (family=="matrix" && case_value!="none")
      if (bad || NR != 4 || failure_stage == "" || !pair_ok) exit 1
      print "partial55_" failure_stage
    }
  ' "$file"
}

run_matrix() {
  local name path gate_name gate_value phrase case_spec expected
  name=${1:?name_required}; path=${2:?path_required}; gate_name=${3:?gate_name_required}; gate_value=${4:?gate_value_required}
  phrase=${5:?phrase_required}; case_spec=${6:?case_spec_required}; expected=${7:?expected_required}
  stage="$name"
  local stdout_file="$output_root/${name}.stdout" stderr_file="$output_root/${name}.stderr"
  trap - ERR
  set +e
  "$docker_bin" exec -e "MYSQL_ROOT_PASSWORD=$matrix_password" -e "MOLIN_MATRIX_ASSET_UID=$asset_uid" \
    -e "MOLIN_000055_SCHEMA54_SHA=$sha54_legacy" -e "MOLIN_000055_SCHEMA55_SHA=$sha55" \
    -e "MOLIN_000055_BASELINE_MANIFEST_SHA=$manifest55_partial_sha" \
    -e "MOLIN_000056_SCHEMA55_SHA=$sha55" -e "MOLIN_000056_SCHEMA56_SHA=$sha56" \
    -e "MOLIN_000056_BASELINE_MANIFEST_SHA=$manifest56_sha" -e "$gate_name=$gate_value" "$matrix_container_id" \
    "$path/runner.sh" --execute "$phrase" >"$stdout_file" 2>"$stderr_file"
  local exit_code=$?
  set -e
  trap 'fail "$stage"' ERR
  if [[ $exit_code -ne 0 ]]; then
    local failure_classification
    failure_classification=$(classify_runner_failure "$name" "$stdout_file") || failure_classification="${name}_execution_unclassified"
    fail "$failure_classification"
  fi
  validate_stderr "$stderr_file" || fail "${name}_stderr"
  validate_stdout "$stdout_file" "$case_spec" "$expected" || fail "${name}_summary"
}

expected55=$(printf '%s\n' \
  'matrix_completed=true' 'database_access=true' 'migration_executed=true' 'source_database_selected=false' \
  'runtime_unique_targets=7' 'empty_schema54_up_down=true' 'legacy_schema54_up_down=true' 'schema55_down=true' \
  'ownership_combinations=4' 'partial_fault_injection=not_run' 'targets_retained=true' \
  'up_sha256=7238522CEC2CDFB2AD042C4B668380AA691E396CD536152F3ED25049ECD1FA3D' \
  'down_sha256=217B8FDAB63962284DA9D6EE1C436716687E351FE313E76F88E08C421D7C26EE')
expected55_partial=$(printf '%s\n' \
  'partial_matrix_completed=true' 'database_access=true' 'migration_executed=true' 'source_database_selected=false' \
  'runtime_unique_targets=33' 'partial_up_points=16' 'partial_down_points=15' 'no_injection_baselines=2' \
  'boundary_state_assertions=true' 'base_runner_partial_status_unchanged=not_implemented' \
  'combined_partial_evidence=provided_by_separate_asset' 'targets_retained=true' \
  'up_sha256=7238522CEC2CDFB2AD042C4B668380AA691E396CD536152F3ED25049ECD1FA3D' \
  'down_sha256=217B8FDAB63962284DA9D6EE1C436716687E351FE313E76F88E08C421D7C26EE' \
  'boundary_sha256=4B5E02DC0C72490B168A47637E1DD8E6298DFEBE18AC22CD9DCAF663B8E18585')
expected56=$(printf '%s\n' \
  'matrix_completed=true' 'database_access=true' 'migration_executed=true' 'source_database_selected=false' \
  'runtime_unique_targets=11' 'ownership_combinations=3' 'admin_cardinality_blocks=2' 'metadata_conflict_blocked=true' \
  'empty_receipt_down=true' 'existing_receipt_blocked=true' 'unknown_reference_blocks=3' 'concurrent_scope_unique=true' \
  'partial_fault_injection=not_implemented' 'targets_retained=true' \
  'up_sha256=9133212C61EB4AA89B72C77D0C353F4B0F8B483080CBFB1E85A0281379861D9B' \
  'down_sha256=F42A30D70A95AD7BFD876F1515267C5FEE3DDCFD7AAC066453BDC020D201A5C2')
expected56_partial=$(printf '%s\n' \
  'partial_matrix_completed=true' 'database_access=true' 'migration_executed=true' 'source_database_selected=false' \
  'runtime_unique_targets=43' 'partial_up_points=27' 'partial_down_points=14' 'no_injection_baselines=2' \
  'boundary_state_assertions=true' 'base_runner_partial_status_unchanged=not_implemented' \
  'combined_partial_evidence=provided_by_separate_asset' 'targets_retained=true' \
  'up_sha256=9133212C61EB4AA89B72C77D0C353F4B0F8B483080CBFB1E85A0281379861D9B' \
  'down_sha256=F42A30D70A95AD7BFD876F1515267C5FEE3DDCFD7AAC066453BDC020D201A5C2' \
  'boundary_sha256=7B9E3132B2A09D939FD81E908C889EE6EE41A69B5D680B52A081D5A0A9BA4A62')

case_spec55='empty:54,legacy:54,schema55:55,ownfresh:54,ownperm:54,ownall:54,ownmixed:54'
case_spec55_partial=$($awk_bin -F '\t' 'BEGIN {sep=""} {printf "%s%s:%s",sep,$2,($1=="up"?"54":"55");sep=","} END {printf "%sup_baseline:54,down_baseline:55",sep}' "$boundary55") || fail partial55_case_spec
case_spec56='ownfresh:55,ownperm:55,ownall:55,adminzero:55,admintwo:55,metaconf:55,receipt:55,refrole:55,refuser:55,refgroup:55,concurrent:55'
case_spec56_partial=$($awk_bin -F '\t' 'BEGIN {sep=""} {printf "%s%s:%s",sep,$2,($1=="up"?"55":"56");sep=","} END {printf "%sup_baseline:55,down_baseline:56",sep}' "$boundary56") || fail partial56_case_spec

run_matrix matrix55 /root/molin-000055-isolation-assets MOLIN_000055_ISOLATION_EXECUTE I_UNDERSTAND_NEW_ISOLATION_DATABASES_WILL_BE_CREATED I_CONFIRM_000055_ISOLATION_MATRIX_ONCE "$case_spec55" "$expected55"
run_matrix partial55 /root/molin-000055-partial-assets MOLIN_000055_PARTIAL_EXECUTE I_UNDERSTAND_000055_PARTIAL_CREATES_33_ISOLATION_DATABASES I_CONFIRM_000055_PARTIAL_MATRIX_ONCE "$case_spec55_partial" "$expected55_partial"
run_matrix matrix56 /root/molin-000056-isolation-assets MOLIN_000056_ISOLATION_EXECUTE I_UNDERSTAND_000056_NEW_ISOLATION_DATABASES_WILL_BE_CREATED I_CONFIRM_000056_ISOLATION_MATRIX_ONCE "$case_spec56" "$expected56"
run_matrix partial56 /root/molin-000056-partial-assets MOLIN_000056_PARTIAL_EXECUTE I_UNDERSTAND_000056_PARTIAL_CREATES_43_ISOLATION_DATABASES I_CONFIRM_000056_PARTIAL_MATRIX_ONCE "$case_spec56_partial" "$expected56_partial"

stage=target_count
target_counts=$("$docker_bin" exec -e "MYSQL_PWD=$matrix_password" "$matrix_container_id" mysql --no-defaults --host=127.0.0.1 --user=root --batch --skip-column-names --raw --execute="SELECT SUM(schema_name LIKE 'molin_55mx_%'),SUM(schema_name LIKE 'molin_55pt_%'),SUM(schema_name LIKE 'molin_56mx_%'),SUM(schema_name LIKE 'molin_56pt_%') FROM information_schema.schemata;") || fail target_count
[[ "$target_counts" = $'7\t33\t11\t43' ]] || fail target_count

stage=container_remove
"$docker_bin" rm --force -- "$matrix_container_id" >/dev/null || fail matrix_container_remove
[[ -z "$("$docker_bin" ps --all --quiet --filter "id=$matrix_container_id")" ]] || fail matrix_container_remove
matrix_container_id=
matrix_password=

stage=complete
printf 'status=pass mode=email_migration_full_isolation_matrix mysql8_image_bound=true mysql8_runtime_verified=true baseline_generation=true baseline_outputs=6 matrix55=true partial55=true matrix56=true partial56=true runtime_unique_targets=94 temporary_containers_removed=true main_database_access=false main_database_modified=false retries=0\n'
