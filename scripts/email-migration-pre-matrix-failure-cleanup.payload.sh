#!/usr/bin/env bash
# 精确清理基线成功但四场矩阵均未启动的独立 MySQL 8 临时 Stage。
set -Eeuo pipefail
umask 077
exec 2>/dev/null
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

readonly stat_bin=/usr/bin/stat
readonly find_bin=/usr/bin/find
readonly sort_bin=/usr/bin/sort
readonly wc_bin=/usr/bin/wc
readonly sha256sum_bin=/usr/bin/sha256sum
readonly realpath_bin=/usr/bin/realpath
readonly docker_bin=/usr/bin/docker
readonly rm_bin=/usr/bin/rm

fail() {
  printf 'status=failed mode=email_migration_pre_matrix_failure_cleanup classification=%s stage_retained=true database_access=false retries=0\n' "${1:?classification_required}"
  exit 2
}

for tool in "$stat_bin" "$find_bin" "$sort_bin" "$wc_bin" "$sha256sum_bin" "$realpath_bin" "$docker_bin" "$rm_bin"; do
  [[ -x "$tool" && ! -L "$tool" ]] || fail host_tool
done

nonce=
archive_sha=
if [[ $# -eq 2 && $1 = --execute && $2 = --unique ]]; then
  mapfile -t retained_stages < <("$find_bin" /home/pc/molin-runtime -regextype posix-extended -mindepth 1 -maxdepth 1 -type d \
    -regex '/home/pc/molin-runtime/email-migration-matrix-[a-f0-9]{32}' -printf '%p\n' | "$sort_bin")
  [[ ${#retained_stages[@]} -eq 1 ]] || fail stage_count
  nonce=${retained_stages[0]##*-}
  read -r archive_sha _ < <("$sha256sum_bin" -- "${retained_stages[0]}/package.tar.gz")
elif [[ $# -eq 3 && $1 = --execute && $2 =~ ^[a-f0-9]{32}$ && $3 =~ ^[a-f0-9]{64}$ ]]; then
  nonce=$2
  archive_sha=$3
else
  fail argument_gate
fi
[[ "$nonce" =~ ^[a-f0-9]{32}$ && "$archive_sha" =~ ^[a-f0-9]{64}$ ]] || fail argument_identity
readonly nonce archive_sha
readonly stage="/home/pc/molin-runtime/email-migration-matrix-${nonce}"
readonly package="$stage/package.tar.gz"
readonly source_root="$stage/source"
readonly manifest="$stage/source-manifest.sha256"
readonly baselines="$stage/baselines"
readonly output="$stage/output"
readonly assets="$stage/assets"

[[ -d "$stage" && ! -L "$stage" && "$($stat_bin -c '%U:%a' -- "$stage")" = pc:700 ]] || fail stage_identity
[[ "$($realpath_bin -e -- "$stage")" = "$stage" ]] || fail stage_identity
[[ -f "$package" && ! -L "$package" && "$($stat_bin -c '%U' -- "$package")" = pc ]] || fail package_identity
read -r actual_archive_sha _ < <("$sha256sum_bin" -- "$package")
[[ "$actual_archive_sha" = "$archive_sha" ]] || fail package_hash
[[ -f "$manifest" && ! -L "$manifest" && "$($wc_bin -l < "$manifest")" -eq 66 ]] || fail manifest_identity
[[ -d "$source_root" && ! -L "$source_root" ]] || fail source_identity
(cd "$stage" && "$sha256sum_bin" --check --strict --status source-manifest.sha256) || fail source_hash
[[ -z "$($find_bin "$stage" -type l -print -quit)" ]] || fail symlink_present

mapfile -t stage_entries < <("$find_bin" "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n' | "$sort_bin")
[[ ${#stage_entries[@]} -eq 6 ]] || fail stage_shape
[[ "${stage_entries[*]}" = 'assets baselines output package.tar.gz source source-manifest.sha256' ]] || fail stage_shape

mapfile -t baseline_entries < <("$find_bin" "$baselines" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | "$sort_bin")
[[ ${#baseline_entries[@]} -eq 6 ]] || fail baseline_shape
[[ "${baseline_entries[*]}" = '000055-baseline-manifest.tsv 000056-baseline-manifest.tsv schema54-empty.sql schema54-legacy.sql schema55.sql schema56.sql' ]] || fail baseline_shape

mapfile -t output_entries < <("$find_bin" "$output" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | "$sort_bin")
[[ ${#output_entries[@]} -eq 3 ]] || fail output_shape
[[ "${output_entries[*]}" = 'baseline.stderr baseline.stdout matrix-container.stderr' ]] || fail output_shape
[[ "$($stat_bin -c '%s' -- "$output/baseline.stderr")" -eq 0 ]] || fail baseline_stderr
[[ "$($stat_bin -c '%s' -- "$output/matrix-container.stderr")" -eq 0 ]] || fail container_stderr
read -r baseline_stdout_sha _ < <("$sha256sum_bin" -- "$output/baseline.stdout")
[[ "${baseline_stdout_sha^^}" = BF12EDE2B73010EDA1939CB8A113ED970B2E9E202058B9A86038AD7347D02319 ]] || fail baseline_summary

for specification in \
  'molin-000055-isolation-assets:7' \
  'molin-000055-partial-assets:7' \
  'molin-000056-isolation-assets:6' \
  'molin-000056-partial-assets:7'; do
  asset_name=${specification%%:*}
  expected_count=${specification#*:}
  asset_dir="$assets/$asset_name"
  [[ -d "$asset_dir" && ! -L "$asset_dir" && "$($stat_bin -c '%U:%a' -- "$asset_dir")" = pc:700 ]] || fail asset_identity
  [[ "$($find_bin "$asset_dir" -mindepth 1 -maxdepth 1 -type f -printf '.' | "$wc_bin" -c)" -eq "$expected_count" ]] || fail asset_shape
done

mapfile -t named_containers < <("$docker_bin" ps --all --filter "name=^/molin-email-matrix-${nonce}$" --format '{{.ID}}')
mapfile -t labeled_containers < <("$docker_bin" ps --all --filter "label=molin.phase4.matrix=${nonce}" --format '{{.ID}}')
[[ ${#named_containers[@]} -eq 0 && ${#labeled_containers[@]} -eq 0 ]] || fail temporary_container_present

"$rm_bin" -rf --one-file-system -- "$stage"
[[ ! -e "$stage" && ! -L "$stage" ]] || fail cleanup_verify
printf 'status=pass mode=email_migration_pre_matrix_failure_cleanup classification=verified_pre_matrix_stage_removed baseline_outputs=6 matrix_outputs=0 removed_count=1 database_access=false retries=0\n'
