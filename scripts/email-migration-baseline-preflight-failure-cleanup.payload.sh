#!/usr/bin/env bash
# 仅清理生成器在本地工具预检阶段退出后留下的精确 migration Stage。
set -Eeuo pipefail
umask 077
exec 2>/dev/null
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

readonly sha256sum_bin=/usr/bin/sha256sum
readonly stat_bin=/usr/bin/stat
readonly find_bin=/usr/bin/find
readonly sort_bin=/usr/bin/sort
readonly wc_bin=/usr/bin/wc
readonly rm_bin=/usr/bin/rm
readonly realpath_bin=/usr/bin/realpath

fail() {
  printf 'status=failed mode=email_migration_baseline_preflight_failure_cleanup classification=%s stage_retained=true database_access=false docker_access=false retries=0\n' "${1:?classification_required}"
  exit 2
}

[[ $# -eq 3 && $1 = --execute ]] || fail argument_gate
readonly nonce=$2
readonly archive_sha=$3
[[ "$nonce" =~ ^[a-f0-9]{32}$ && "$archive_sha" =~ ^[a-f0-9]{64}$ ]] || fail argument_gate
readonly stage="/home/pc/molin-runtime/email-migration-matrix-${nonce}"
readonly package="$stage/package.tar.gz"
readonly source_root="$stage/source"
readonly manifest="$stage/source-manifest.sha256"
readonly baselines="$stage/baselines"
readonly output="$stage/output"

for tool in "$sha256sum_bin" "$stat_bin" "$find_bin" "$sort_bin" "$wc_bin" "$rm_bin" "$realpath_bin"; do
  [[ -x "$tool" && ! -L "$tool" ]] || fail host_tool
done
[[ -d "$stage" && ! -L "$stage" && "$($stat_bin -c '%U:%a' -- "$stage")" = pc:700 ]] || fail stage_identity
[[ "$($realpath_bin -e -- "$stage")" = "$stage" ]] || fail stage_identity
[[ -f "$package" && ! -L "$package" && "$($stat_bin -c '%U' -- "$package")" = pc ]] || fail package_identity
read -r actual_archive_sha _ < <("$sha256sum_bin" "$package")
[[ "$actual_archive_sha" = "$archive_sha" ]] || fail package_hash
[[ -f "$manifest" && ! -L "$manifest" && "$($wc_bin -l < "$manifest")" -eq 66 ]] || fail manifest_identity
[[ -d "$source_root" && ! -L "$source_root" && "$($stat_bin -c '%U' -- "$source_root")" = pc ]] || fail source_identity
(cd "$stage" && "$sha256sum_bin" --check --strict --status source-manifest.sha256) || fail source_hash
[[ -d "$baselines" && ! -L "$baselines" && "$($stat_bin -c '%U:%a' -- "$baselines")" = pc:700 ]] || fail baseline_identity
[[ -z "$($find_bin "$baselines" -mindepth 1 -print -quit)" ]] || fail baseline_not_empty
[[ -d "$output" && ! -L "$output" && "$($stat_bin -c '%U:%a' -- "$output")" = pc:700 ]] || fail output_identity
mapfile -t output_entries < <("$find_bin" "$output" -mindepth 1 -maxdepth 1 -printf '%f\n' | "$sort_bin")
[[ ${#output_entries[@]} -eq 2 && "${output_entries[0]}" = baseline.stderr && "${output_entries[1]}" = baseline.stdout ]] || fail output_shape
for capture in "$output/baseline.stdout" "$output/baseline.stderr"; do
  [[ -f "$capture" && ! -L "$capture" && "$($stat_bin -c '%U:%s' -- "$capture")" = pc:0 ]] || fail capture_identity
done
[[ ! -e "$stage/assets" ]] || fail unexpected_assets
mapfile -t entries < <("$find_bin" "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n' | "$sort_bin")
[[ ${#entries[@]} -eq 5 && "${entries[0]}" = baselines && "${entries[1]}" = output && "${entries[2]}" = package.tar.gz && "${entries[3]}" = source && "${entries[4]}" = source-manifest.sha256 ]] || fail stage_shape
[[ -z "$($find_bin "$stage" -type l -print -quit)" ]] || fail symlink_present

"$rm_bin" -rf --one-file-system -- "$stage"
[[ ! -e "$stage" && ! -L "$stage" ]] || fail cleanup_verify
printf 'status=pass mode=email_migration_baseline_preflight_failure_cleanup classification=verified_pre_container_stage_removed removed_count=1 database_access=false docker_access=false retries=0\n'
