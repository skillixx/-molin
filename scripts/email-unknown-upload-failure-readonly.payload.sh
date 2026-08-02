#!/usr/bin/env bash
# DirectMail Phase 4 fresh cycle 上传失败现场纯元数据诊断。
set -Eeuo pipefail
exec 2>/dev/null

classification=initializing
stage_count=0
stage_identity=false
entry_count=0
stage_empty=false
parent_writable=false
stage_writable=false
scp_tool=false
free_space=unknown
free_inodes=unknown
binary_size_class=absent
binary_hash_match=not_checked

fail() {
  classification=${1:?classification_required}
  printf 'status=failed classification=%s stage_count=%s stage_identity=%s entry_count=%s stage_empty=%s parent_writable=%s stage_writable=%s scp_tool=%s free_space=%s free_inodes=%s binary_size_class=%s binary_hash_match=%s writes=false database_access=false redis_access=false cleanup=false restart=false scp=false retries=0\n' \
    "$classification" "$stage_count" "$stage_identity" "$entry_count" "$stage_empty" "$parent_writable" "$stage_writable" "$scp_tool" "$free_space" "$free_inodes" "$binary_size_class" "$binary_hash_match"
  exit 2
}
trap 'fail unexpected' ERR

readonly parent=/home/pc/molin-runtime
[[ -d "$parent" && ! -L "$parent" ]] || fail parent_identity
[[ "$(/usr/bin/stat -c '%U' -- "$parent")" == pc ]] || fail parent_identity
[[ -w "$parent" ]] && parent_writable=true
[[ "$parent_writable" == true ]] || fail parent_not_writable

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
[[ -w "$stage" ]] && stage_writable=true
[[ "$stage_writable" == true ]] || fail stage_not_writable

mapfile -t stage_entries < <(/usr/bin/find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
entry_count=${#stage_entries[@]}
readonly expected_binary_name=email-unknown-restart.test
readonly expected_binary_size=25573597
readonly expected_binary_sha=1179e29d9f43efea79f185e8d2319d015a627f69a48ef9ed7ce22e72ba6ad900
readonly binary_path="${stage}/${expected_binary_name}"
binary_file_id=absent
binary_size=0
if [[ $entry_count -eq 0 ]]; then
  stage_empty=true
  classification=upload_failure_stage_empty
elif [[ $entry_count -eq 1 && "${stage_entries[0]}" == "$expected_binary_name" ]]; then
  [[ -f "$binary_path" && ! -L "$binary_path" ]] || fail binary_identity
  [[ "$(/usr/bin/stat -c '%U' -- "$binary_path")" == pc ]] || fail binary_identity
  binary_file_id=$(/usr/bin/stat -c '%d:%i' -- "$binary_path")
  binary_size=$(/usr/bin/stat -c '%s' -- "$binary_path")
  [[ "$binary_file_id" =~ ^[0-9]+:[0-9]+$ && "$binary_size" =~ ^[0-9]+$ ]] || fail binary_identity
  if (( binary_size == 0 )); then
    binary_size_class=zero
    classification=upload_failure_stage_partial_binary
  elif (( binary_size < expected_binary_size )); then
    binary_size_class=partial
    classification=upload_failure_stage_partial_binary
  elif (( binary_size == expected_binary_size )); then
    binary_size_class=expected
    if [[ "$(/usr/bin/sha256sum -- "$binary_path")" == "${expected_binary_sha}  ${binary_path}" ]]; then
      binary_hash_match=true
      classification=upload_failure_stage_complete_binary
    else
      binary_hash_match=false
      fail binary_identity
    fi
  else
    binary_size_class=oversize
    fail binary_identity
  fi
else
  fail stage_contents_unexpected
fi

if [[ -x /usr/bin/scp && ! -L /usr/bin/scp ]]; then
  scp_tool=true
elif [[ -x /bin/scp && ! -L /bin/scp ]]; then
  scp_tool=true
fi
[[ "$scp_tool" == true ]] || fail scp_tool

read -r space_blocks space_available < <(/usr/bin/df -Pk -- "$parent" | /usr/bin/awk 'NR==2 {print $2, $4}')
[[ "$space_blocks" =~ ^[1-9][0-9]*$ && "$space_available" =~ ^[0-9]+$ ]] || fail space_metadata
if (( space_available >= 1048576 )); then free_space=adequate; else free_space=low; fi

read -r inode_total inode_available < <(/usr/bin/df -Pi -- "$parent" | /usr/bin/awk 'NR==2 {print $2, $4}')
[[ "$inode_total" =~ ^[1-9][0-9]*$ && "$inode_available" =~ ^[0-9]+$ ]] || fail inode_metadata
if (( inode_available >= 1024 )); then free_inodes=adequate; else free_inodes=low; fi

[[ -d "$stage" && ! -L "$stage" ]] || fail stage_changed
[[ "$(/usr/bin/stat -c '%U:%a:%d:%i' -- "$stage")" == "pc:700:${stage_file_id}" ]] || fail stage_changed
mapfile -t final_entries < <(/usr/bin/find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n')
[[ ${#final_entries[@]} -eq $entry_count ]] || fail stage_changed
if [[ $entry_count -eq 0 ]]; then
  [[ ! -e "$binary_path" && ! -L "$binary_path" ]] || fail stage_changed
else
  [[ "${final_entries[0]}" == "$expected_binary_name" && -f "$binary_path" && ! -L "$binary_path" ]] || fail stage_changed
  [[ "$(/usr/bin/stat -c '%U:%d:%i:%s' -- "$binary_path")" == "pc:${binary_file_id}:${binary_size}" ]] || fail stage_changed
fi

printf 'status=pass classification=%s stage_count=1 stage_identity=true entry_count=%s stage_empty=%s parent_writable=%s stage_writable=%s scp_tool=true free_space=%s free_inodes=%s binary_size_class=%s binary_hash_match=%s writes=false database_access=false redis_access=false cleanup=false restart=false scp=false retries=0\n' \
  "$classification" "$entry_count" "$stage_empty" "$parent_writable" "$stage_writable" "$free_space" "$free_inodes" "$binary_size_class" "$binary_hash_match"
