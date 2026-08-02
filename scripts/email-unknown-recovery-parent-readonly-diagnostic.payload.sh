set -Eeuo pipefail

stage=shell_options
fail() {
  local failed_stage=$stage
  trap - ERR
  /usr/bin/printf 'status=failed stage=%s writes=false postcheck=false cleanup=false database=false redis=false docker=false restarts=false retries=0\n' "$failed_stage"
  exit 2
}
trap fail ERR

[[ $- == *e* && $- == *u* ]]
[[ "$(set -o | /usr/bin/awk '$1=="pipefail"{print $2}')" == on ]]

bool() {
  if "$@"; then
    /usr/bin/printf true
  else
    /usr/bin/printf false
  fi
}

stage=parent_snapshot
current_uid=$(/usr/bin/id -u)
parent_paths=(/home/pc /home/pc/molin /home/pc/molin/rollback)
parent_labels=(home project rollback)
parent_count=${#parent_paths[@]}
[[ "$parent_count" == 3 ]]

parent_exists=()
parent_owner=()
parent_symlink=()
parent_writable=()
parent_identity_one=()
for parent_path in "${parent_paths[@]}"; do
  exists=$(bool test -e "$parent_path")
  symlink=$(bool test -L "$parent_path")
  owner=false
  writable=false
  identity=missing
  if [[ "$exists" == true || "$symlink" == true ]]; then
    [[ "$(/usr/bin/stat -c '%u' -- "$parent_path")" =~ ^[0-9]+$ ]]
    [[ "$(/usr/bin/stat -c '%a' -- "$parent_path")" =~ ^[0-7]{3,4}$ ]]
    [[ "$(/usr/bin/stat -c '%u:%a:%d:%i:%F' -- "$parent_path")" != *$'\n'* ]]
    [[ "$(/usr/bin/stat -c '%u' -- "$parent_path")" == "$current_uid" ]] && owner=true
    parent_mode=$(/usr/bin/stat -c '%a' -- "$parent_path")
    (( (8#$parent_mode & 022) != 0 )) && writable=true
    identity=$(/usr/bin/stat -c '%u:%a:%d:%i:%F' -- "$parent_path")
  fi
  parent_exists+=("$exists")
  parent_owner+=("$owner")
  parent_symlink+=("$symlink")
  parent_writable+=("$writable")
  parent_identity_one+=("$identity")
done

stage=recovery_snapshot
recovery_candidates=()
if [[ "${parent_exists[2]}" == true && "${parent_symlink[2]}" == false ]]; then
  mapfile -t recovery_candidates < <(/usr/bin/find /home/pc/molin/rollback -mindepth 1 -maxdepth 1 -name 'molin-email-unknown-*.sql' -print | /usr/bin/sort)
fi
recovery_count=${#recovery_candidates[@]}
recovery_exists=false
recovery_owner=false
recovery_symlink=false
recovery_writable=false
recovery_identity_one=missing
if (( recovery_count == 1 )); then
  recovery_file=${recovery_candidates[0]}
  [[ "$recovery_file" =~ ^/home/pc/molin/rollback/molin-email-unknown-[a-f0-9]{32}\.sql$ ]]
  recovery_exists=$(bool test -e "$recovery_file")
  recovery_symlink=$(bool test -L "$recovery_file")
  if [[ "$recovery_exists" == true || "$recovery_symlink" == true ]]; then
    [[ "$(/usr/bin/stat -c '%u' -- "$recovery_file")" == "$current_uid" ]] && recovery_owner=true
    recovery_mode=$(/usr/bin/stat -c '%a' -- "$recovery_file")
    (( (8#$recovery_mode & 022) != 0 )) && recovery_writable=true
    recovery_identity_one=$(/usr/bin/stat -c '%u:%a:%d:%i:%s:%F' -- "$recovery_file")
  fi
fi

stage=stability_snapshot
parent_stable=()
for index in 0 1 2; do
  identity_two=missing
  second_exists=$(bool test -e "${parent_paths[$index]}")
  second_symlink=$(bool test -L "${parent_paths[$index]}")
  if [[ "$second_exists" == true || "$second_symlink" == true ]]; then
    identity_two=$(/usr/bin/stat -c '%u:%a:%d:%i:%F' -- "${parent_paths[$index]}")
  fi
  parent_stable+=("$(bool test "${parent_identity_one[$index]}" = "$identity_two")")
done
recovery_identity_two=missing
if (( recovery_count == 1 )); then
  recovery_second_exists=$(bool test -e "$recovery_file")
  recovery_second_symlink=$(bool test -L "$recovery_file")
  if [[ "$recovery_second_exists" == true || "$recovery_second_symlink" == true ]]; then
    recovery_identity_two=$(/usr/bin/stat -c '%u:%a:%d:%i:%s:%F' -- "$recovery_file")
  fi
fi
recovery_stable=$(bool test "$recovery_identity_one" = "$recovery_identity_two")

trap - ERR
/usr/bin/printf 'status=pass parent_count=%s home_exists=%s home_owner=%s home_symlink=%s home_group_other_writable=%s home_stable=%s project_exists=%s project_owner=%s project_symlink=%s project_group_other_writable=%s project_stable=%s rollback_exists=%s rollback_owner=%s rollback_symlink=%s rollback_group_other_writable=%s rollback_stable=%s recovery_count=%s recovery_exists=%s recovery_owner=%s recovery_symlink=%s recovery_group_other_writable=%s recovery_stable=%s writes=false postcheck=false cleanup=false database=false redis=false docker=false restarts=false retries=0\n' \
  "$parent_count" "${parent_exists[0]}" "${parent_owner[0]}" "${parent_symlink[0]}" "${parent_writable[0]}" "${parent_stable[0]}" \
  "${parent_exists[1]}" "${parent_owner[1]}" "${parent_symlink[1]}" "${parent_writable[1]}" "${parent_stable[1]}" \
  "${parent_exists[2]}" "${parent_owner[2]}" "${parent_symlink[2]}" "${parent_writable[2]}" "${parent_stable[2]}" \
  "$recovery_count" "$recovery_exists" "$recovery_owner" "$recovery_symlink" "$recovery_writable" "$recovery_stable"
