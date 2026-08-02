#!/usr/bin/env bash
# DirectMail Phase 4 migration 上传失败 Stage 的纯只读分类。
set -Eeuo pipefail
exec 2>/dev/null
parent=/home/pc/molin-runtime
[[ -d "$parent" && ! -L "$parent" && "$(stat -c '%U' -- "$parent")" == pc ]]
shopt -s nullglob
stages=("$parent"/email-migration-matrix-*)
[[ ${#stages[@]} -eq 1 ]]
stage=${stages[0]}
[[ "$stage" =~ ^/home/pc/molin-runtime/email-migration-matrix-[a-f0-9]{32}$ ]]
[[ -d "$stage" && ! -L "$stage" && "$(stat -c '%U:%a' -- "$stage")" == pc:700 ]]
mapfile -t entries < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
if [[ ${#entries[@]} -eq 0 ]]; then
  printf 'status=pass classification=stage_empty stage_count=1 entry_count=0 package_present=false\n'
elif [[ ${#entries[@]} -eq 1 && "${entries[0]}" == package.tar.gz && -f "$stage/package.tar.gz" && ! -L "$stage/package.tar.gz" && "$(stat -c '%U' -- "$stage/package.tar.gz")" == pc ]]; then
  size=$(stat -c '%s' -- "$stage/package.tar.gz")
  [[ "$size" =~ ^[0-9]+$ ]]
  printf 'status=pass classification=package_present stage_count=1 entry_count=1 package_present=true package_size=%s\n' "$size"
else
  exit 3
fi
