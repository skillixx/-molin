#!/usr/bin/env bash
# 仅清理由 host_tool 门禁失败后保留、且内容完整可归属的 migration Stage。
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
  printf 'status=failed mode=email_migration_failed_stage_cleanup classification=%s stage_retained=true database_access=false docker_access=false retries=0\n' "${1:?classification_required}"
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

for tool in "$sha256sum_bin" "$stat_bin" "$find_bin" "$sort_bin" "$wc_bin" "$rm_bin" "$realpath_bin"; do
  [[ -x "$tool" && ! -L "$tool" ]] || fail host_tool
done
[[ -d "$stage" && ! -L "$stage" && "$($stat_bin -c '%U:%a' -- "$stage")" = pc:700 ]] || fail stage_identity
[[ "$($realpath_bin -e -- "$stage")" = "$stage" ]] || fail stage_identity
[[ -f "$package" && ! -L "$package" && "$($stat_bin -c '%U' -- "$package")" = pc ]] || fail package_identity
[[ -f "$manifest" && ! -L "$manifest" && "$($stat_bin -c '%U' -- "$manifest")" = pc ]] || fail manifest_identity
[[ -d "$source_root" && ! -L "$source_root" && "$($stat_bin -c '%U' -- "$source_root")" = pc ]] || fail source_identity
[[ ! -e "$stage/baselines" && ! -e "$stage/assets" && ! -e "$stage/output" ]] || fail execution_artifact_present
read -r actual_archive_sha _ < <("$sha256sum_bin" "$package")
[[ "$actual_archive_sha" = "$archive_sha" ]] || fail package_hash
[[ "$($wc_bin -l < "$manifest")" -eq 66 ]] || fail manifest_shape
(cd "$stage" && "$sha256sum_bin" --check --strict --status source-manifest.sha256) || fail source_hash
mapfile -t entries < <("$find_bin" "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n' | "$sort_bin")
[[ ${#entries[@]} -eq 3 && "${entries[0]}" = package.tar.gz && "${entries[1]}" = source && "${entries[2]}" = source-manifest.sha256 ]] || fail stage_shape
[[ -z "$($find_bin "$stage" -type l -print -quit)" ]] || fail symlink_present

"$rm_bin" -rf --one-file-system -- "$stage"
[[ ! -e "$stage" && ! -L "$stage" ]] || fail cleanup_verify
printf 'status=pass mode=email_migration_failed_stage_cleanup classification=verified_unexecuted_stage_removed removed_count=1 database_access=false docker_access=false retries=0\n'
