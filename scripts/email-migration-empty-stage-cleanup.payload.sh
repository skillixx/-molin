#!/usr/bin/env bash
# DirectMail Phase 4 migration 唯一空 Stage 的精确清理。
set -Eeuo pipefail
exec 2>/dev/null
parent=/home/pc/molin-runtime
[[ -d "$parent" && ! -L "$parent" && "$(stat -c '%U' -- "$parent")" == pc && -w "$parent" ]]
shopt -s nullglob
stages=("$parent"/email-migration-matrix-*)
[[ ${#stages[@]} -eq 1 ]]
stage=${stages[0]}
[[ "$stage" =~ ^/home/pc/molin-runtime/email-migration-matrix-[a-f0-9]{32}$ ]]
[[ -d "$stage" && ! -L "$stage" && "$(stat -c '%U:%a' -- "$stage")" == pc:700 ]]
stage_id=$(stat -c '%d:%i' -- "$stage")
[[ "$stage_id" =~ ^[0-9]+:[0-9]+$ ]]
mapfile -t first < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
[[ ${#first[@]} -eq 0 ]]
sleep 1
[[ -d "$stage" && ! -L "$stage" && "$(stat -c '%d:%i' -- "$stage")" == "$stage_id" ]]
mapfile -t second < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
[[ ${#second[@]} -eq 0 ]]
rmdir -- "$stage"
[[ ! -e "$stage" && ! -L "$stage" ]]
remaining=("$parent"/email-migration-matrix-*)
[[ ${#remaining[@]} -eq 0 ]]
printf 'status=pass classification=empty_migration_stage_removed stage_count=1 empty_checks=2 removed_count=1 remaining_count=0 database_access=false redis_access=false docker_access=false retries=0\n'
