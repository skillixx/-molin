#!/usr/bin/env bash
# DirectMail Phase 4 两个空 Stage 的精确目录清理。
set -Eeuo pipefail
exec 2>/dev/null

parent=/home/pc/molin-runtime
[[ -d "$parent" && ! -L "$parent" && "$(stat -c '%U' -- "$parent")" == pc && -w "$parent" ]]
shopt -s nullglob
stages=("$parent"/email-unknown-cycle-*)
[[ ${#stages[@]} -eq 2 ]]
ids=()
for stage in "${stages[@]}"; do
  [[ "$stage" =~ ^/home/pc/molin-runtime/email-unknown-cycle-[a-f0-9]{32}$ ]]
  [[ -d "$stage" && ! -L "$stage" && "$(stat -c '%U:%a' -- "$stage")" == pc:700 && -w "$stage" ]]
  id=$(stat -c '%d:%i' -- "$stage")
  [[ "$id" =~ ^[0-9]+:[0-9]+$ ]]
  ids+=("$id")
  mapfile -t first < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
  [[ ${#first[@]} -eq 0 ]]
done

sleep 1
for index in 0 1; do
  stage=${stages[$index]}
  [[ -d "$stage" && ! -L "$stage" && "$(stat -c '%d:%i' -- "$stage")" == "${ids[$index]}" ]]
  mapfile -t second < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
  [[ ${#second[@]} -eq 0 ]]
done

rmdir -- "${stages[0]}" "${stages[1]}"
[[ ! -e "${stages[0]}" && ! -L "${stages[0]}" && ! -e "${stages[1]}" && ! -L "${stages[1]}" ]]
remaining=("$parent"/email-unknown-cycle-*)
[[ ${#remaining[@]} -eq 0 ]]
printf 'status=pass mode=email_unknown_two_empty_stage_cleanup classification=two_empty_stages_removed stage_count=2 empty_checks=4 removed_count=2 remaining_count=0 writes=true database_access=false redis_access=false restart=false scp=false retries=0\n'
