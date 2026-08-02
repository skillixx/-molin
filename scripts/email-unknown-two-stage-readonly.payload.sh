#!/usr/bin/env bash
# DirectMail Phase 4 两个保留 Stage 的纯元数据聚合诊断。
set -Eeuo pipefail
exec 2>/dev/null

parent=/home/pc/molin-runtime
expected_name=email-unknown-restart.test
expected_size=25573597
expected_sha=1179e29d9f43efea79f185e8d2319d015a627f69a48ef9ed7ce22e72ba6ad900

[[ -d "$parent" && ! -L "$parent" && "$(stat -c '%U' -- "$parent")" == pc && -w "$parent" ]]
shopt -s nullglob
stages=("$parent"/email-unknown-cycle-*)
[[ ${#stages[@]} -eq 2 ]]

empty_count=0
partial_count=0
complete_count=0
for stage in "${stages[@]}"; do
  [[ "$stage" =~ ^/home/pc/molin-runtime/email-unknown-cycle-[a-f0-9]{32}$ ]]
  [[ -d "$stage" && ! -L "$stage" && "$(stat -c '%U:%a' -- "$stage")" == pc:700 && -w "$stage" ]]
  stage_id=$(stat -c '%d:%i' -- "$stage")
  [[ "$stage_id" =~ ^[0-9]+:[0-9]+$ ]]
  mapfile -t entries < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
  if [[ ${#entries[@]} -eq 0 ]]; then
    ((empty_count += 1))
  elif [[ ${#entries[@]} -eq 1 && "${entries[0]}" == "$expected_name" ]]; then
    binary="$stage/$expected_name"
    [[ -f "$binary" && ! -L "$binary" && "$(stat -c '%U' -- "$binary")" == pc ]]
    size=$(stat -c '%s' -- "$binary")
    [[ "$size" =~ ^[0-9]+$ && "$size" -le "$expected_size" ]]
    if [[ "$size" -lt "$expected_size" ]]; then
      ((partial_count += 1))
    else
      [[ "$(sha256sum -- "$binary")" == "$expected_sha  $binary" ]]
      ((complete_count += 1))
    fi
  else
    exit 3
  fi
  [[ "$(stat -c '%d:%i' -- "$stage")" == "$stage_id" ]]
done

[[ $((empty_count + partial_count + complete_count)) -eq 2 ]]
printf 'status=pass mode=email_unknown_two_stage_readonly classification=two_stages_classified stage_count=2 empty_count=%s partial_count=%s complete_count=%s parent_writable=true stages_identity=true writes=false database_access=false redis_access=false cleanup=false restart=false scp=false retries=0\n' \
  "$empty_count" "$partial_count" "$complete_count"
