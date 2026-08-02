#!/usr/bin/env bash
# 仅清理已确认为 schema56 migration_sql 失败、且没有发布基线的精确 Stage。
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
readonly cat_bin=/usr/bin/cat
readonly rm_bin=/usr/bin/rm
readonly realpath_bin=/usr/bin/realpath

fail() {
  printf 'status=failed mode=email_migration_schema56_sql_failure_cleanup classification=%s stage_retained=true database_access=false docker_access=false retries=0\n' "${1:?classification_required}"
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
readonly stdout_file="$output/baseline.stdout"
readonly stderr_file="$output/baseline.stderr"
readonly expected_summary='status=failed mode=email_migration_baseline_generation stage=schema56_build classification=migration_sql mysql_error_code=3819 sqlstate=HY000 sql_line=113 check_fingerprints=48:b3f6d38e6965b16c300e0057dc2074afef859b72e28d20f28bc1fde167dccfef,69:e05e098693ce41e8d9e204e823ea8f50f9fdcb45abdc3a7eae2236359bf04f02,48:80907c599c935b2bd8b2e9ef6b5a56530203ac83edb6ef05c96250dfbe33dd53,227:656ef4e1b29c3c481b43aed49b3279f0dfb8f9bb6ed4868e9ef57e55ff660385,176:ee9f0a7c0344ae5c6220d17b7043e58e8d0735b42fd5cd8caa739d1212e71e06,232:7345bcbc8d4592a7f62d24a0b3c6e6bbd8ae6fcc86da2533575df162932818d0,91:02aade59c6a977c09e3595f8e6a5f9b11a2a59ad3a129bb0bc4390edbdbc3ed7,75:fb3d30c4907cd8ac267ce323b7b2cc6c584d938ffd7fcc638bda58642541dd3d outputs_created=false retained=false'

for tool in "$sha256sum_bin" "$stat_bin" "$find_bin" "$sort_bin" "$wc_bin" "$cat_bin" "$rm_bin" "$realpath_bin"; do
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
[[ -f "$stdout_file" && ! -L "$stdout_file" && "$($stat_bin -c '%U' -- "$stdout_file")" = pc ]] || fail stdout_identity
[[ -f "$stderr_file" && ! -L "$stderr_file" && "$($stat_bin -c '%U:%s' -- "$stderr_file")" = pc:0 ]] || fail stderr_identity
[[ "$($cat_bin "$stdout_file")" = "$expected_summary" && "$($wc_bin -l < "$stdout_file")" -eq 1 ]] || fail summary_identity
[[ ! -e "$stage/assets" ]] || fail unexpected_assets
mapfile -t entries < <("$find_bin" "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n' | "$sort_bin")
[[ ${#entries[@]} -eq 5 && "${entries[0]}" = baselines && "${entries[1]}" = output && "${entries[2]}" = package.tar.gz && "${entries[3]}" = source && "${entries[4]}" = source-manifest.sha256 ]] || fail stage_shape
[[ -z "$($find_bin "$stage" -type l -print -quit)" ]] || fail symlink_present

"$rm_bin" -rf --one-file-system -- "$stage"
[[ ! -e "$stage" && ! -L "$stage" ]] || fail cleanup_verify
printf 'status=pass mode=email_migration_schema56_sql_failure_cleanup classification=verified_schema56_sql_failure_stage_removed removed_count=1 database_access=false docker_access=false retries=0\n'
