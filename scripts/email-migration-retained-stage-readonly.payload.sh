#!/usr/bin/env bash
# 只读定位并分类唯一保留的迁移矩阵 Stage，不输出路径、nonce、哈希或文件原文。
set -Eeuo pipefail
umask 077
exec 2>/dev/null
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

readonly root=/home/pc/molin-runtime
readonly find_bin=/usr/bin/find
readonly stat_bin=/usr/bin/stat
readonly realpath_bin=/usr/bin/realpath
readonly sha256sum_bin=/usr/bin/sha256sum
readonly wc_bin=/usr/bin/wc
readonly sort_bin=/usr/bin/sort
readonly awk_link=/usr/bin/awk

fail() {
  trap - ERR
  printf 'status=failed mode=email_migration_retained_stage_readonly classification=%s writes=false database_access=false docker_access=false retries=0\n' "${1:?classification_required}"
  exit 2
}
trap 'fail unexpected_failure' ERR

[[ $# -eq 0 ]] || fail argument_gate
for tool in "$find_bin" "$stat_bin" "$realpath_bin" "$sha256sum_bin" "$wc_bin" "$sort_bin"; do
  [[ -x "$tool" && ! -L "$tool" ]] || fail host_tool
done
[[ -x "$awk_link" ]] || fail host_tool
awk_bin=$("$realpath_bin" -e -- "$awk_link") || fail host_tool
[[ "$awk_bin" =~ ^/usr/bin/[A-Za-z0-9._+-]+$ && -f "$awk_bin" && ! -L "$awk_bin" && -x "$awk_bin" ]] || fail host_tool
[[ "$($stat_bin -c '%U:%G' -- "$awk_bin")" = root:root ]] || fail host_tool
readonly awk_bin
[[ -d "$root" && ! -L "$root" && "$($realpath_bin -e -- "$root")" = "$root" ]] || fail root_identity

mapfile -t stages < <("$find_bin" "$root" -regextype posix-extended -mindepth 1 -maxdepth 1 -type d \
  -regex '/home/pc/molin-runtime/email-migration-matrix-[a-f0-9]{32}' -printf '%p\n' | "$sort_bin")
[[ ${#stages[@]} -eq 1 ]] || fail stage_count
readonly stage=${stages[0]}
[[ ! -L "$stage" && "$($realpath_bin -e -- "$stage")" = "$stage" && "$($stat_bin -c '%U:%a' -- "$stage")" = pc:700 ]] || fail stage_identity
[[ -z "$($find_bin "$stage" -type l -print -quit)" ]] || fail symlink_present

readonly package="$stage/package.tar.gz"
readonly source_root="$stage/source"
readonly manifest="$stage/source-manifest.sha256"
[[ -f "$package" && ! -L "$package" && "$($stat_bin -c '%U' -- "$package")" = pc ]] || fail package_identity
read -r archive_sha _ < <("$sha256sum_bin" -- "$package")
[[ "$archive_sha" =~ ^[a-f0-9]{64}$ ]] || fail package_hash

source_verified=false
if [[ -d "$source_root" && ! -L "$source_root" && -f "$manifest" && ! -L "$manifest" && "$($wc_bin -l < "$manifest")" -eq 66 ]]; then
  if (cd "$stage" && "$sha256sum_bin" --check --strict --status source-manifest.sha256); then source_verified=true; fi
fi

count_entries() {
  local directory=${1:?directory_required}
  if [[ ! -e "$directory" && ! -L "$directory" ]]; then printf '0'; return; fi
  [[ -d "$directory" && ! -L "$directory" ]] || fail target_identity
  "$find_bin" "$directory" -mindepth 1 -maxdepth 1 -printf '.' | "$wc_bin" -c
}

readonly baselines_count=$(count_entries "$stage/baselines")
readonly assets_count=$(count_entries "$stage/assets")
readonly output_count=$(count_entries "$stage/output")
readonly top_count=$("$find_bin" "$stage" -mindepth 1 -maxdepth 1 -printf '.' | "$wc_bin" -c)
for value in "$top_count" "$baselines_count" "$assets_count" "$output_count"; do [[ "$value" =~ ^[0-9]+$ ]] || fail count_shape; done

baseline_summary=absent
baseline_stderr=absent
container_stderr=absent
matrix55_failure=absent
matrix55_case=absent
matrix55_target_created=absent
matrix55_error=absent
partial55_failure=absent
partial55_case=absent
partial55_target_created=absent
partial55_error=absent
partial55_stderr_class=absent
partial55_assets_verified=false
if [[ -d "$stage/output" && ! -L "$stage/output" ]]; then
  if [[ -f "$stage/output/baseline.stdout" && ! -L "$stage/output/baseline.stdout" ]]; then
    read -r value _ < <("$sha256sum_bin" -- "$stage/output/baseline.stdout")
    if [[ "${value^^}" = BF12EDE2B73010EDA1939CB8A113ED970B2E9E202058B9A86038AD7347D02319 ]]; then baseline_summary=expected; else baseline_summary=other; fi
  fi
  for specification in baseline.stderr:baseline_stderr matrix-container.stderr:container_stderr; do
    name=${specification%%:*}; variable=${specification#*:}; state=absent
    if [[ -f "$stage/output/$name" && ! -L "$stage/output/$name" ]]; then
      if [[ "$($stat_bin -c '%s' -- "$stage/output/$name")" -eq 0 ]]; then state=empty; else state=nonempty; fi
    fi
    printf -v "$variable" '%s' "$state"
  done
  if [[ -f "$stage/output/matrix55.stdout" && ! -L "$stage/output/matrix55.stdout" ]]; then
    matrix55_summary=$("$awk_bin" '
      BEGIN {
        expected_case[1]="empty"; expected_version[1]="54"
        expected_case[2]="legacy"; expected_version[2]="54"
        expected_case[3]="schema55"; expected_version[3]="55"
        expected_case[4]="ownfresh"; expected_version[4]="54"
        expected_case[5]="ownperm"; expected_version[5]="54"
        expected_case[6]="ownall"; expected_version[6]="54"
        expected_case[7]="ownmixed"; expected_version[7]="54"
      }
      terminal==0 && /^case=(empty|legacy|schema55|ownfresh|ownperm|ownall|ownmixed) target_id_sha256=[A-F0-9]+ restored_schema=(54|55)$/ {
        progress++
        split($1, case_field, "="); split($2, hash_field, "="); split($3, version_field, "=")
        if (progress>7 || length(hash_field[2])!=64 || seen[hash_field[2]]++ || case_field[2]!=expected_case[progress] || version_field[2]!=expected_version[progress]) bad=1
        next
      }
      terminal==0 && /^matrix_completed=false$/ {terminal=1; completed++; next}
      terminal==1 && /^failure_stage=(initialization|environment_precheck|empty_baseline|empty_schema54_validate|empty_schema54_version|empty_schema54_table_absence|empty_schema54_code_shape|empty_schema54_code_hash_absence|empty_permissions_absent|empty_verification_empty|legacy_baseline|schema55_baseline|ownership_matrix|matrix_complete|(empty|legacy|schema55|ownfresh|ownperm|ownall|ownmixed)_(target_identity|target_absent|target_create|baseline_restore|baseline_version|baseline_version_cardinality|database_binding|engine_policy|view_policy|trigger_policy|routine_policy|event_policy|schema54_(baseline|down)_(version|table_absence|code_shape|code_hash_absence)|up_(mark_dirty|sql|finalize|validate)|down_(mark_dirty|sql|finalize|validate|statement_[0-9]{2})))$/ {terminal=2; stage_count++; stage_value=substr($0,15); next}
      terminal==2 && /^case=(none|empty|legacy|schema55|ownfresh|ownperm|ownall|ownmixed)$/ {terminal=3; case_count++; case_value=substr($0,6); next}
      terminal==3 && /^target_created=(true|false)$/ {terminal=4; target_count++; target_value=substr($0,16); next}
      terminal==0 && $0=="matrix_completed=true" {terminal=10; completed++; next}
      terminal==10 && $0=="database_access=true" {terminal=11; next}
      terminal==11 && $0=="migration_executed=true" {terminal=12; next}
      terminal==12 && $0=="source_database_selected=false" {terminal=13; next}
      terminal==13 && $0=="runtime_unique_targets=7" {terminal=14; next}
      terminal==14 && $0=="empty_schema54_up_down=true" {terminal=15; next}
      terminal==15 && $0=="legacy_schema54_up_down=true" {terminal=16; next}
      terminal==16 && $0=="schema55_down=true" {terminal=17; next}
      terminal==17 && $0=="ownership_combinations=4" {terminal=18; next}
      terminal==18 && $0=="partial_fault_injection=not_run" {terminal=19; next}
      terminal==19 && $0=="targets_retained=true" {terminal=20; next}
      terminal==20 && $0=="up_sha256=7238522CEC2CDFB2AD042C4B668380AA691E396CD536152F3ED25049ECD1FA3D" {terminal=21; next}
      terminal==21 && $0=="down_sha256=217B8FDAB63962284DA9D6EE1C436716687E351FE313E76F88E08C421D7C26EE" {terminal=22; next}
      {bad=1}
      END {
        if (!bad && terminal==4 && completed==1 && stage_count==1 && case_count==1 && target_count==1 && NR==progress+4) print stage_value "|" case_value "|" target_value;
        else if (!bad && terminal==22 && completed==1 && progress==7 && NR==20) print "summary_contract_mismatch|none|false";
        else print "invalid";
      }
    ' "$stage/output/matrix55.stdout")
    if [[ "$matrix55_summary" = invalid ]]; then
      matrix55_failure=invalid; matrix55_case=invalid; matrix55_target_created=invalid
    else
      IFS='|' read -r matrix55_failure matrix55_case matrix55_target_created <<<"$matrix55_summary"
      [[ "$matrix55_failure" =~ ^(summary_contract_mismatch|initialization|environment_precheck|empty_baseline|empty_schema54_validate|empty_schema54_version|empty_schema54_table_absence|empty_schema54_code_shape|empty_schema54_code_hash_absence|empty_permissions_absent|empty_verification_empty|legacy_baseline|schema55_baseline|ownership_matrix|matrix_complete|(empty|legacy|schema55|ownfresh|ownperm|ownall|ownmixed)_(target_identity|target_absent|target_create|baseline_restore|baseline_version|baseline_version_cardinality|database_binding|engine_policy|view_policy|trigger_policy|routine_policy|event_policy|schema54_(baseline|down)_(version|table_absence|code_shape|code_hash_absence)|up_(mark_dirty|sql|finalize|validate)|down_(mark_dirty|sql|finalize|validate|statement_[0-9]{2})))$ && "$matrix55_case" =~ ^(none|empty|legacy|schema55|ownfresh|ownperm|ownall|ownmixed)$ && "$matrix55_target_created" =~ ^(true|false)$ ]] || fail matrix55_state
    fi
  fi
  if [[ -f "$stage/output/matrix55.stderr" && ! -L "$stage/output/matrix55.stderr" ]]; then
    if [[ "$("$stat_bin" -c '%s' -- "$stage/output/matrix55.stderr")" -eq 0 ]]; then
      matrix55_error=none
    else
      matrix55_error=$("$awk_bin" '
        /^mysql_failure_category=(authentication|connectivity|missing_resource|sql_syntax|concurrency|constraint|injected_boundary|other)$/ {category_count++; category=substr($0,24); next}
        /^mysql_exit_code=[1-9][0-9]*$/ {exit_count++; next}
        /^mysql_stderr_length=[1-9][0-9]*$/ {length_count++; next}
        {bad=1}
        END {if (!bad && category_count==1 && exit_count==1 && length_count==1 && NR==3) print category; else print "invalid"}
      ' "$stage/output/matrix55.stderr")
    fi
  fi
fi

if [[ "$source_verified" = true && -f "$stage/output/partial55.stdout" && ! -L "$stage/output/partial55.stdout" ]]; then
  readonly partial55_manifest="$source_root/tests/email/000055-partial-boundaries.tsv"
  [[ -f "$partial55_manifest" && ! -L "$partial55_manifest" ]] || fail partial55_manifest
  partial55_summary=$("$awk_bin" -F '\t' '
    FILENAME==ARGV[1] {
      if (NF!=14 || $1!~/^(up|down)$/ || $2!~/^(up|down)_[a-z0-9_]+$/) manifest_bad=1
      boundary_count++
      expected_case[boundary_count]=$2
      expected_version[boundary_count]=($1=="up" ? "54" : "55")
      next
    }
    FNR==1 {
      expected_count=boundary_count+2
      expected_case[boundary_count+1]="up_baseline"; expected_version[boundary_count+1]="54"
      expected_case[boundary_count+2]="down_baseline"; expected_version[boundary_count+2]="55"
    }
    terminal==0 && /^case=[a-z0-9_]+ target_id_sha256=[A-F0-9]+ restored_schema=(54|55)$/ {
      progress++
      split($0, output_fields, " ")
      split(output_fields[1], case_field, "="); split(output_fields[2], hash_field, "="); split(output_fields[3], version_field, "=")
      if (progress>expected_count || length(hash_field[2])!=64 || seen_hash[hash_field[2]]++ || case_field[2]!=expected_case[progress] || version_field[2]!=expected_version[progress]) bad=1
      next
    }
    terminal==0 && $0=="partial_matrix_completed=false" {terminal=1; completed++; next}
    terminal==1 && /^failure_stage=(initialization|environment_precheck|environment_identity|environment_hash_inputs|environment_tools|asset_directory_identity|asset_hashes|baseline_manifest_shape|boundary_manifest_shape|statement_boundary_precheck|up_partial_matrix|down_partial_matrix|no_injection_baselines|partial_matrix_complete|(up|down)_[a-z0-9_]+_(mark_dirty|execute_boundary|inject_failure|validate_state))$/ {terminal=2; stage_value=substr($0,15); stage_count++; next}
    terminal==2 && /^case=(none|(up|down)_[a-z0-9_]+)$/ {terminal=3; case_value=substr($0,6); case_count++; next}
    terminal==3 && /^target_created=(true|false)$/ {terminal=4; target_value=substr($0,16); target_count++; next}
    {bad=1}
    END {
      case_known=(case_value=="none")
      for (i=1; i<=expected_count; i++) if (case_value==expected_case[i]) case_known=1
      stage_case_ok=(stage_value !~ /_(mark_dirty|execute_boundary|inject_failure|validate_state)$/ || index(stage_value, case_value "_")==1)
      if (!manifest_bad && boundary_count==31 && expected_count==33 && !bad && terminal==4 && completed==1 && stage_count==1 && case_count==1 && target_count==1 && FNR==progress+4 && case_known && stage_case_ok) print stage_value "|" case_value "|" target_value;
      else print "invalid";
    }
  ' "$partial55_manifest" "$stage/output/partial55.stdout") || fail partial55_summary
  if [[ "$partial55_summary" = invalid ]]; then
    partial55_failure=invalid; partial55_case=invalid; partial55_target_created=invalid
  else
    IFS='|' read -r partial55_failure partial55_case partial55_target_created <<<"$partial55_summary"
  fi
fi
if [[ -f "$stage/output/partial55.stderr" && ! -L "$stage/output/partial55.stderr" ]]; then
  if [[ "$($stat_bin -c '%s' -- "$stage/output/partial55.stderr")" -eq 0 ]]; then
    partial55_error=none; partial55_stderr_class=empty
  else
    partial55_error=$("$awk_bin" '
      /^mysql_failure_category=(authentication|connectivity|missing_resource|sql_syntax|concurrency|constraint|injected_boundary|other)$/ {category_count++; category=substr($0,24); next}
      /^mysql_exit_code=[1-9][0-9]*$/ {exit_count++; next}
      /^mysql_stderr_length=[1-9][0-9]*$/ {length_count++; next}
      {bad=1}
      END {if (!bad && category_count==1 && exit_count==1 && length_count==1 && NR==3) print category; else print "invalid"}
    ' "$stage/output/partial55.stderr")
    partial55_stderr_class=$("$awk_bin" '
      /^(awk|mawk|gawk): (cmd\. line:|line )[0-9]+:/ {awk_syntax++; next}
      /\/usr\/bin\/wc: (No such file or directory|not found)/ {wc_missing++; next}
      /Permission denied/ {permission++; next}
      /Read-only file system/ {readonly_fs++; next}
      /warning: setlocale:/ {locale_warning++; next}
      {other++}
      END {
        kinds=(awk_syntax>0)+(wc_missing>0)+(permission>0)+(readonly_fs>0)+(locale_warning>0)+(other>0)
        if (NR<1 || kinds!=1) print "mixed_or_invalid"
        else if (awk_syntax==NR) print "awk_syntax"
        else if (wc_missing==NR) print "wc_missing"
        else if (permission==NR) print "permission"
        else if (readonly_fs==NR) print "readonly_filesystem"
        else if (locale_warning==NR) print "locale_warning"
        else print "other"
      }
    ' "$stage/output/partial55.stderr")
  fi
fi

partial55_asset_dir="$stage/assets/molin-000055-partial-assets"
if [[ -d "$partial55_asset_dir" && ! -L "$partial55_asset_dir" && "$($stat_bin -c '%U:%a' -- "$partial55_asset_dir")" = pc:700 ]]; then
  mapfile -t partial55_entries < <("$find_bin" "$partial55_asset_dir" -mindepth 1 -maxdepth 1 -printf '%f\n' | "$sort_bin")
  if [[ ${#partial55_entries[@]} -eq 7 && "${partial55_entries[*]}" = '000055-partial-boundaries.tsv 000055_add_directmail_email_management.down.sql 000055_add_directmail_email_management.up.sql baseline-manifest.tsv runner.sh schema54-legacy.sql schema55.sql' ]]; then
    partial55_assets_verified=true
    for asset_name in "${partial55_entries[@]}"; do
      asset_file="$partial55_asset_dir/$asset_name"
      expected_mode=400; [[ "$asset_name" = runner.sh ]] && expected_mode=500
      [[ ! -L "$asset_file" && "$($stat_bin -c '%U:%a' -- "$asset_file")" = "pc:$expected_mode" ]] || partial55_assets_verified=false
    done
    for pair in \
      '000055-partial-boundaries.tsv:tests/email/000055-partial-boundaries.tsv' \
      '000055_add_directmail_email_management.down.sql:server/migrations/000055_add_directmail_email_management.down.sql' \
      '000055_add_directmail_email_management.up.sql:server/migrations/000055_add_directmail_email_management.up.sql' \
      'runner.sh:tests/email/run-000055-container-partial-matrix.sh'; do
      asset_name=${pair%%:*}; source_name=${pair#*:}
      read -r asset_hash _ < <("$sha256sum_bin" -- "$partial55_asset_dir/$asset_name")
      read -r source_hash _ < <("$sha256sum_bin" -- "$source_root/$source_name")
      [[ "$asset_hash" = "$source_hash" ]] || partial55_assets_verified=false
    done
    for baseline_name in schema54-legacy.sql schema55.sql; do
      read -r asset_hash _ < <("$sha256sum_bin" -- "$partial55_asset_dir/$baseline_name")
      read -r source_hash _ < <("$sha256sum_bin" -- "$stage/baselines/$baseline_name")
      [[ "$asset_hash" = "$source_hash" ]] || partial55_assets_verified=false
    done
    expected_manifest_hash=$("$awk_bin" -F '\t' '$1=="schema54-legacy.sql" || $1=="schema55.sql"' "$stage/baselines/000055-baseline-manifest.tsv" | "$sha256sum_bin" | "$awk_bin" '{print $1}')
    read -r actual_manifest_hash _ < <("$sha256sum_bin" -- "$partial55_asset_dir/baseline-manifest.tsv")
    [[ "$actual_manifest_hash" = "$expected_manifest_hash" ]] || partial55_assets_verified=false
  fi
fi

classification=retained_stage_other
if [[ "$source_verified" = true && "$top_count" -eq 6 && "$baselines_count" -eq 6 && "$assets_count" -eq 4 && "$output_count" -eq 3 &&
      "$baseline_summary" = expected && "$baseline_stderr" = empty && "$container_stderr" = empty ]]; then
  classification=target_collision_pre_matrix_retained
fi
if [[ "$source_verified" = true && "$baseline_summary" = expected && "$matrix55_failure" != absent && "$matrix55_failure" != invalid ]]; then
  classification=matrix55_runner_failure_retained
fi
if [[ "$source_verified" = true && "$top_count" -eq 6 && "$baselines_count" -eq 6 && "$assets_count" -eq 4 && "$output_count" -eq 5 &&
      "$baseline_summary" = expected && "$baseline_stderr" = empty && "$container_stderr" = empty &&
      "$matrix55_failure" = summary_contract_mismatch && "$matrix55_case" = none && "$matrix55_target_created" = false && "$matrix55_error" = none ]]; then
  classification=matrix55_success_summary_contract_mismatch_retained
fi
if [[ "$source_verified" = true && "$top_count" -eq 6 && "$baselines_count" -eq 6 && "$assets_count" -eq 4 && "$output_count" -eq 7 &&
      "$baseline_summary" = expected && "$baseline_stderr" = empty && "$container_stderr" = empty &&
      "$matrix55_failure" = summary_contract_mismatch && "$matrix55_case" = none && "$matrix55_target_created" = false && "$matrix55_error" = none &&
      "$partial55_failure" != absent && "$partial55_failure" != invalid && "$partial55_error" != absent && "$partial55_error" != invalid ]]; then
  classification=partial55_runner_failure_retained
fi
if [[ "$source_verified" = true && "$top_count" -eq 6 && "$baselines_count" -eq 6 && "$assets_count" -eq 4 && "$output_count" -eq 7 &&
      "$baseline_summary" = expected && "$baseline_stderr" = empty && "$container_stderr" = empty &&
      "$matrix55_failure" = summary_contract_mismatch && "$matrix55_case" = none && "$matrix55_target_created" = false && "$matrix55_error" = none &&
      "$partial55_failure" = environment_precheck && "$partial55_case" = none && "$partial55_target_created" = false &&
      "$partial55_error" = invalid && "$partial55_stderr_class" != absent && "$partial55_stderr_class" != mixed_or_invalid &&
      "$partial55_assets_verified" = true ]]; then
  classification=partial55_environment_precheck_classified_retained
fi
if [[ "$source_verified" = true && "$top_count" -eq 6 && "$baselines_count" -eq 6 && "$assets_count" -eq 4 && "$output_count" -eq 7 &&
      "$baseline_summary" = expected && "$baseline_stderr" = empty && "$container_stderr" = empty &&
      "$matrix55_failure" = summary_contract_mismatch && "$matrix55_case" = none && "$matrix55_target_created" = false && "$matrix55_error" = none &&
      "$partial55_failure" =~ ^(environment_identity|environment_hash_inputs|environment_tools|asset_directory_identity|asset_hashes|baseline_manifest_shape|boundary_manifest_shape)$ &&
      "$partial55_case" = none && "$partial55_target_created" = false && "$partial55_assets_verified" = true ]]; then
  classification=partial55_precheck_stage_classified_retained
fi
printf 'status=pass mode=email_migration_retained_stage_readonly classification=%s source_verified=%s top_count=%s baselines_count=%s assets_count=%s output_count=%s baseline_summary=%s baseline_stderr=%s container_stderr=%s matrix55_failure=%s matrix55_case=%s matrix55_target_created=%s matrix55_error=%s partial55_failure=%s partial55_case=%s partial55_target_created=%s partial55_error=%s partial55_stderr_class=%s partial55_assets_verified=%s writes=false database_access=false docker_access=false retries=0\n' \
  "$classification" "$source_verified" "$top_count" "$baselines_count" "$assets_count" "$output_count" "$baseline_summary" "$baseline_stderr" "$container_stderr" "$matrix55_failure" "$matrix55_case" "$matrix55_target_created" "$matrix55_error" "$partial55_failure" "$partial55_case" "$partial55_target_created" "$partial55_error" "$partial55_stderr_class" "$partial55_assets_verified"
