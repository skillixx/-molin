#!/usr/bin/env bash
# 仅清理已精确证明为 matrix55 环境预检失败且无归属容器的唯一迁移 Stage。
set -Eeuo pipefail
umask 077
exec 2>/dev/null
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

readonly root=/home/pc/molin-runtime
readonly find_bin=/usr/bin/find
readonly stat_bin=/usr/bin/stat
readonly sort_bin=/usr/bin/sort
readonly wc_bin=/usr/bin/wc
readonly sha256sum_bin=/usr/bin/sha256sum
readonly realpath_bin=/usr/bin/realpath
readonly docker_bin=/usr/bin/docker
readonly rm_bin=/usr/bin/rm
readonly awk_link=/usr/bin/awk

fail() {
  trap - ERR
  printf 'status=failed mode=email_migration_matrix55_environment_failure_cleanup classification=%s stage_retained=true database_access=false retries=0\n' "${1:?classification_required}"
  exit 2
}
trap 'fail unexpected_failure' ERR

[[ ( $# -eq 2 || $# -eq 3 ) && $1 = --execute && $2 = I_CONFIRM_EMAIL_MIGRATION_MATRIX55_ENVIRONMENT_FAILURE_CLEANUP_ONCE ]] || fail argument_gate
for tool in "$find_bin" "$stat_bin" "$sort_bin" "$wc_bin" "$sha256sum_bin" "$realpath_bin" "$docker_bin" "$rm_bin"; do
  [[ -x "$tool" && ! -L "$tool" ]] || fail host_tool
done
[[ -x "$awk_link" ]] || fail host_tool
awk_bin=$("$realpath_bin" -e -- "$awk_link") || fail host_tool
[[ "$awk_bin" =~ ^/usr/bin/[A-Za-z0-9._+-]+$ && -f "$awk_bin" && ! -L "$awk_bin" && -x "$awk_bin" ]] || fail host_tool
[[ "$("$stat_bin" -c '%U:%G' -- "$awk_bin")" = root:root ]] || fail host_tool
readonly awk_bin
[[ -d "$root" && ! -L "$root" && "$("$realpath_bin" -e -- "$root")" = "$root" ]] || fail root_identity

mapfile -t stages < <("$find_bin" "$root" -regextype posix-extended -mindepth 1 -maxdepth 1 -type d \
  -regex '/home/pc/molin-runtime/email-migration-matrix-[a-f0-9]{32}' -printf '%p\n' | "$sort_bin")
[[ ${#stages[@]} -eq 1 ]] || fail stage_count
readonly stage=${stages[0]}
readonly nonce=${stage##*-}
[[ "$nonce" =~ ^[a-f0-9]{32}$ && ! -L "$stage" && "$("$realpath_bin" -e -- "$stage")" = "$stage" ]] || fail stage_identity
[[ "$("$stat_bin" -c '%U:%a' -- "$stage")" = pc:700 ]] || fail stage_identity
[[ -z "$("$find_bin" "$stage" -type l -print -quit)" ]] || fail symlink_present

readonly package="$stage/package.tar.gz"
readonly source_root="$stage/source"
readonly manifest="$stage/source-manifest.sha256"
readonly baselines="$stage/baselines"
readonly assets="$stage/assets"
readonly output="$stage/output"
[[ -f "$package" && ! -L "$package" && "$("$stat_bin" -c '%U' -- "$package")" = pc ]] || fail package_identity
[[ -d "$source_root" && ! -L "$source_root" && -f "$manifest" && ! -L "$manifest" ]] || fail source_identity
[[ "$("$wc_bin" -l < "$manifest")" -eq 66 ]] || fail source_manifest
(cd "$stage" && "$sha256sum_bin" --check --strict --status source-manifest.sha256) || fail source_hash

mapfile -t top_entries < <("$find_bin" "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n' | "$sort_bin")
[[ ${#top_entries[@]} -eq 6 && "${top_entries[*]}" = 'assets baselines output package.tar.gz source source-manifest.sha256' ]] || fail stage_shape
mapfile -t baseline_entries < <("$find_bin" "$baselines" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | "$sort_bin")
[[ ${#baseline_entries[@]} -eq 6 && "${baseline_entries[*]}" = '000055-baseline-manifest.tsv 000056-baseline-manifest.tsv schema54-empty.sql schema54-legacy.sql schema55.sql schema56.sql' ]] || fail baseline_shape
mapfile -t output_entries < <("$find_bin" "$output" -mindepth 1 -maxdepth 1 -printf '%f\n' | "$sort_bin")
output_profile=unknown
if [[ ${#output_entries[@]} -eq 5 && "${output_entries[*]}" = 'baseline.stderr baseline.stdout matrix-container.stderr matrix55.stderr matrix55.stdout' ]]; then
  output_profile=matrix55
elif [[ ${#output_entries[@]} -eq 7 && "${output_entries[*]}" = 'baseline.stderr baseline.stdout matrix-container.stderr matrix55.stderr matrix55.stdout partial55.stderr partial55.stdout' ]]; then
  output_profile=partial55_failure
else
  fail output_shape
fi
[[ "$("$stat_bin" -c '%s' -- "$output/baseline.stderr")" -eq 0 && "$("$stat_bin" -c '%s' -- "$output/matrix-container.stderr")" -eq 0 ]] || fail stderr_state
matrix55_error=none
if [[ "$("$stat_bin" -c '%s' -- "$output/matrix55.stderr")" -ne 0 ]]; then
  matrix55_error=$("$awk_bin" '
    /^mysql_failure_category=(authentication|connectivity|missing_resource|sql_syntax|concurrency|constraint|injected_boundary|other)$/ {category_count++; category=substr($0,24); next}
    /^mysql_exit_code=[1-9][0-9]*$/ {exit_count++; next}
    /^mysql_stderr_length=[1-9][0-9]*$/ {length_count++; next}
    {bad=1}
    END {if (!bad && category_count==1 && exit_count==1 && length_count==1 && NR==3) print category; else exit 1}
  ' "$output/matrix55.stderr") || fail matrix55_stderr
fi
read -r baseline_sha _ < <("$sha256sum_bin" -- "$output/baseline.stdout")
[[ "${baseline_sha^^}" = BF12EDE2B73010EDA1939CB8A113ED970B2E9E202058B9A86038AD7347D02319 ]] || fail baseline_summary
matrix55_stage=$("$awk_bin" '
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
  terminal==0 && $0=="matrix_completed=false" {terminal=1; completed++; next}
  terminal==1 && /^failure_stage=(environment_precheck|empty_baseline|empty_baseline_restore|empty_event_policy|empty_schema54_validate|empty_schema54_code_shape|schema55_down_sql|schema55_down_statement_05)$/ {terminal=2; stage=substr($0,15); stage_count++; next}
  terminal==2 && /^case=(none|empty|schema55)$/ {terminal=3; case_value=substr($0,6); case_count++; next}
  terminal==3 && /^target_created=(true|false)$/ {terminal=4; target_value=substr($0,16); target_count++; next}
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
    pair_ok=(stage=="environment_precheck" && case_value=="none" && target_value=="false" && progress==0) ||
            ((stage=="empty_baseline" || stage=="empty_baseline_restore") && case_value=="empty" && target_value=="true" && progress==0) ||
            ((stage=="empty_event_policy" || stage=="empty_schema54_validate" || stage=="empty_schema54_code_shape") && case_value=="empty" && target_value=="true" && progress==1) ||
            ((stage=="schema55_down_sql" || stage=="schema55_down_statement_05") && case_value=="schema55" && target_value=="true" && progress==3)
    if (!bad && terminal==4 && completed==1 && stage_count==1 && case_count==1 && target_count==1 && NR==progress+4 && pair_ok) print stage;
    else if (!bad && terminal==22 && completed==1 && progress==7 && NR==20) print "summary_contract_mismatch";
    else exit 1
  }
' "$output/matrix55.stdout") || fail matrix55_summary
if [[ "$matrix55_stage" = empty_event_policy || "$matrix55_stage" = empty_schema54_validate || "$matrix55_stage" = schema55_down_sql ]]; then
  [[ "$matrix55_error" = other ]] || fail matrix55_error_pair
elif [[ "$matrix55_stage" = schema55_down_statement_05 ]]; then
  [[ "$matrix55_error" = constraint ]] || fail matrix55_error_pair
elif [[ "$matrix55_stage" = summary_contract_mismatch ]]; then
  [[ "$matrix55_error" = none ]] || fail matrix55_error_pair
else
  [[ "$matrix55_error" = none ]] || fail matrix55_error_pair
fi

if [[ "$output_profile" = partial55_failure ]]; then
  [[ "$matrix55_stage" = summary_contract_mismatch && "$matrix55_error" = none ]] || fail partial55_matrix55_pair
  partial55_stage=$(
    "$awk_bin" '
      NR==1 && $0=="partial_matrix_completed=false" {completed++; next}
      NR==2 && /^failure_stage=(environment_precheck|boundary_manifest_shape)$/ {stage_count++; stage=substr($0,15); next}
      NR==3 && $0=="case=none" {case_count++; next}
      NR==4 && $0=="target_created=false" {target_count++; next}
      {bad=1}
      END {if (!bad && completed==1 && stage_count==1 && case_count==1 && target_count==1 && NR==4) print stage; else exit 1}
    ' "$output/partial55.stdout"
  ) || fail partial55_summary
  partial55_stderr_size=$("$stat_bin" -c '%s' -- "$output/partial55.stderr") || fail partial55_stderr
  if [[ "$partial55_stage" = environment_precheck ]]; then
    [[ "$partial55_stderr_size" =~ ^[1-9][0-9]*$ && "$partial55_stderr_size" -le 4096 ]] || fail partial55_stderr
    partial55_stderr_class=$(
      "$awk_bin" '
        /\/usr\/bin\/wc: (No such file or directory|not found)/ {wc_missing++; next}
        /Permission denied/ {permission++; next}
        /Read-only file system/ {readonly_fs++; next}
        /warning: setlocale:/ {locale_warning++; next}
        {other++}
        END {
          kinds=(wc_missing>0)+(permission>0)+(readonly_fs>0)+(locale_warning>0)+(other>0)
          if (NR<1 || kinds!=1) print "mixed_or_invalid"
          else if (wc_missing==NR) print "wc_missing"
          else if (permission==NR) print "permission"
          else if (readonly_fs==NR) print "readonly_filesystem"
          else if (locale_warning==NR) print "locale_warning"
          else print "other"
        }
      ' "$output/partial55.stderr"
    ) || fail partial55_stderr
    [[ "$partial55_stderr_class" = other ]] || fail partial55_stderr_pair
  elif [[ "$partial55_stage" = boundary_manifest_shape ]]; then
    [[ "$partial55_stderr_size" =~ ^[1-9][0-9]*$ && "$partial55_stderr_size" -le 4096 ]] || fail partial55_stderr
    partial55_stderr_class=$(
      "$awk_bin" '
        /^(awk|mawk|gawk): (cmd\. line:|line )[0-9]+:/ {awk_syntax++; next}
        {bad=1}
        END {if (!bad && NR>=1 && awk_syntax==NR) print "awk_syntax"; else print "mixed_or_invalid"}
      ' "$output/partial55.stderr"
    ) || fail partial55_stderr
    [[ "$partial55_stderr_class" = awk_syntax ]] || fail partial55_stderr_pair
  else
    fail partial55_summary
  fi
fi

for specification in 'molin-000055-isolation-assets:7' 'molin-000055-partial-assets:7' 'molin-000056-isolation-assets:6' 'molin-000056-partial-assets:7'; do
  asset_name=${specification%%:*}; expected_count=${specification#*:}; asset_dir="$assets/$asset_name"
  [[ -d "$asset_dir" && ! -L "$asset_dir" && "$("$stat_bin" -c '%U:%a' -- "$asset_dir")" = pc:700 ]] || fail asset_identity
  [[ "$("$find_bin" "$asset_dir" -mindepth 1 -maxdepth 1 -type f -printf '.' | "$wc_bin" -c)" -eq "$expected_count" ]] || fail asset_shape
done

mapfile -t named_containers < <("$docker_bin" ps --all --filter "name=^/molin-email-matrix-${nonce}$" --format '{{.ID}}')
mapfile -t labeled_containers < <("$docker_bin" ps --all --filter "label=molin.phase4.matrix=${nonce}" --format '{{.ID}}')
[[ ${#named_containers[@]} -eq 0 && ${#labeled_containers[@]} -eq 0 ]] || fail temporary_container_present

"$rm_bin" -rf --one-file-system -- "$stage"
[[ ! -e "$stage" && ! -L "$stage" ]] || fail cleanup_verify
matrix_outputs=1; [[ "$output_profile" = partial55_failure ]] && matrix_outputs=2
printf 'status=pass mode=email_migration_matrix55_environment_failure_cleanup classification=verified_matrix55_known_failure_stage_removed baseline_outputs=6 matrix_outputs=%s removed_count=1 database_access=false retries=0\n' "$matrix_outputs"
