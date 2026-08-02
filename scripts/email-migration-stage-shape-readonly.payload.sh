#!/usr/bin/env bash
# 只读分类手工迁移包装器的保留 Stage，不读取捕获文件原文或业务数据。
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

fail() {
  printf 'status=failed mode=email_migration_stage_shape_readonly classification=%s stage_present=%s writes=false database_access=false docker_access=false retries=0\n' \
    "${1:?classification_required}" "${stage_present:-false}"
  exit 2
}

[[ $# -eq 2 && $1 =~ ^[a-f0-9]{32}$ && $2 =~ ^[a-f0-9]{64}$ ]] || fail argument_gate
readonly nonce=$1
readonly archive_sha=$2
readonly stage="/home/pc/molin-runtime/email-migration-matrix-${nonce}"
readonly package="$stage/package.tar.gz"
stage_present=false

for tool in "$stat_bin" "$find_bin" "$sort_bin" "$wc_bin" "$sha256sum_bin" "$realpath_bin"; do
  [[ -x "$tool" && ! -L "$tool" ]] || fail host_tool
done

if [[ ! -e "$stage" && ! -L "$stage" ]]; then
  printf 'status=pass mode=email_migration_stage_shape_readonly classification=stage_absent stage_present=false top_entries=0 source_present=false manifest_valid=false baselines_present=false baseline_files=0 output_present=false output_files=0 assets_present=false asset_entries=0 symlinks=0 writes=false database_access=false docker_access=false retries=0\n'
  exit 0
fi
stage_present=true
[[ -d "$stage" && ! -L "$stage" && "$($stat_bin -c '%U:%a' -- "$stage")" = pc:700 ]] || fail stage_identity
[[ "$($realpath_bin -e -- "$stage")" = "$stage" ]] || fail stage_identity
[[ -f "$package" && ! -L "$package" && "$($stat_bin -c '%U' -- "$package")" = pc ]] || fail package_identity
read -r actual_archive_sha _ < <("$sha256sum_bin" -- "$package")
[[ "$actual_archive_sha" = "$archive_sha" ]] || fail package_hash

readonly symlinks=$($find_bin "$stage" -type l -printf '.' | "$wc_bin" -c)
[[ "$symlinks" -eq 0 ]] || fail symlink_present
mapfile -t top_names < <("$find_bin" "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n' | "$sort_bin")
readonly top_entries=${#top_names[@]}

source_present=false
manifest_valid=false
baselines_present=false
output_present=false
assets_present=false
baseline_files=0
output_files=0
asset_entries=0

if [[ -d "$stage/source" && ! -L "$stage/source" && -f "$stage/source-manifest.sha256" && ! -L "$stage/source-manifest.sha256" ]]; then
  source_present=true
  if [[ "$($wc_bin -l < "$stage/source-manifest.sha256")" -eq 66 ]] &&
     (cd "$stage" && "$sha256sum_bin" --check --strict --status source-manifest.sha256); then
    manifest_valid=true
  fi
fi
if [[ -d "$stage/baselines" && ! -L "$stage/baselines" ]]; then
  baselines_present=true
  baseline_files=$($find_bin "$stage/baselines" -mindepth 1 -maxdepth 1 -type f -printf '.' | "$wc_bin" -c)
fi
if [[ -d "$stage/output" && ! -L "$stage/output" ]]; then
  output_present=true
  output_files=$($find_bin "$stage/output" -mindepth 1 -maxdepth 1 -type f -printf '.' | "$wc_bin" -c)
fi
if [[ -d "$stage/assets" && ! -L "$stage/assets" ]]; then
  assets_present=true
  asset_entries=$($find_bin "$stage/assets" -mindepth 1 -maxdepth 1 -printf '.' | "$wc_bin" -c)
fi

classification=unclassified
if [[ $top_entries -eq 1 && "${top_names[0]}" = package.tar.gz ]]; then
  classification=package_only
elif [[ $top_entries -eq 3 && $source_present = true && $manifest_valid = true && $baselines_present = false && $output_present = false && $assets_present = false ]]; then
  classification=source_ready
elif [[ $source_present = true && $manifest_valid = true && $baselines_present = true && $output_present = true && $assets_present = false ]]; then
  classification=baseline_stage
elif [[ $source_present = true && $manifest_valid = true && $baselines_present = true && $output_present = true && $assets_present = true ]]; then
  classification=matrix_stage
fi

printf 'status=pass mode=email_migration_stage_shape_readonly classification=%s stage_present=true top_entries=%s source_present=%s manifest_valid=%s baselines_present=%s baseline_files=%s output_present=%s output_files=%s assets_present=%s asset_entries=%s symlinks=0 writes=false database_access=false docker_access=false retries=0\n' \
  "$classification" "$top_entries" "$source_present" "$manifest_valid" "$baselines_present" "$baseline_files" "$output_present" "$output_files" "$assets_present" "$asset_entries"
