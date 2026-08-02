#!/usr/bin/env bash
# DirectMail Phase 4 上传失败空 Stage 精确清理。
set -Eeuo pipefail
exec 2>/dev/null

classification=initializing
stage_count=0
stage_identity=false
entry_count=0
stage_empty=false
stage_removed=false

fail() {
  classification=${1:?classification_required}
  printf 'status=failed classification=%s stage_count=%s stage_identity=%s entry_count=%s stage_empty=%s stage_removed=%s writes=%s database_access=false redis_access=false restart=false scp=false retries=0\n' \
    "$classification" "$stage_count" "$stage_identity" "$entry_count" "$stage_empty" "$stage_removed" "$stage_removed"
  exit 2
}
trap 'fail unexpected' ERR

readonly parent=/home/pc/molin-runtime
[[ -d "$parent" && ! -L "$parent" ]] || fail parent_identity
[[ "$(/usr/bin/stat -c '%U' -- "$parent")" == pc ]] || fail parent_identity

shopt -s nullglob
stage_candidates=("$parent"/email-unknown-cycle-*)
stage_count=${#stage_candidates[@]}
[[ $stage_count -eq 1 ]] || fail stage_count
readonly stage=${stage_candidates[0]}
[[ "$stage" =~ ^/home/pc/molin-runtime/email-unknown-cycle-[a-f0-9]{32}$ ]] || fail stage_identity
[[ -d "$stage" && ! -L "$stage" ]] || fail stage_identity
[[ "$(/usr/bin/stat -c '%U:%a' -- "$stage")" == pc:700 ]] || fail stage_identity
readonly stage_file_id=$(/usr/bin/stat -c '%d:%i' -- "$stage")
[[ "$stage_file_id" =~ ^[0-9]+:[0-9]+$ ]] || fail stage_identity
stage_identity=true

mapfile -t initial_entries < <(/usr/bin/find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
entry_count=${#initial_entries[@]}
[[ $entry_count -eq 0 ]] || fail stage_not_empty
stage_empty=true

[[ -d "$stage" && ! -L "$stage" ]] || fail stage_changed
[[ "$(/usr/bin/stat -c '%U:%a:%d:%i' -- "$stage")" == "pc:700:${stage_file_id}" ]] || fail stage_changed
mapfile -t final_entries < <(/usr/bin/find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
[[ ${#final_entries[@]} -eq 0 ]] || fail stage_changed

/usr/bin/rmdir -- "$stage"
stage_removed=true
[[ ! -e "$stage" && ! -L "$stage" ]] || fail removal_not_verified

printf 'status=pass classification=empty_stage_removed stage_count=1 stage_identity=true entry_count=0 stage_empty=true stage_removed=true writes=true database_access=false redis_access=false restart=false scp=false retries=0\n'
