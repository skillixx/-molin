set -Eeuo pipefail

target=/home/pc/molin
changed=false
rollback_attempted=false
rolled_back=false
owner_unchanged=false
symlink=false
group_other_writable=true
health_http_200=false
ready_http_200=false
original_mode=
original_owner=
original_device_inode=

safe_bool() {
  [[ "$1" == true ]] && /usr/bin/printf true || /usr/bin/printf false
}

fail() {
  trap - ERR
  set +e
  if [[ "$changed" == true && -n "$original_mode" ]]; then
    rollback_attempted=true
    /usr/bin/chmod "$original_mode" -- "$target"
    rollback_exit=$?
    if (( rollback_exit == 0 )) && [[ -d "$target" && ! -L "$target" ]] && \
      [[ "$(/usr/bin/stat -c '%u' -- "$target" 2>/dev/null)" == "$original_owner" ]] && \
      [[ "$(/usr/bin/stat -c '%d:%i' -- "$target" 2>/dev/null)" == "$original_device_inode" ]] && \
      [[ "$(/usr/bin/stat -c '%a' -- "$target" 2>/dev/null)" == "$original_mode" ]]; then
      rolled_back=true
    fi
  fi
  /usr/bin/printf 'status=failed changed=%s rollback_attempted=%s rolled_back=%s owner_unchanged=%s symlink=%s group_other_writable=%s health_http_200=%s ready_http_200=%s target_scope=project_directory_only retries=0\n' \
    "$(safe_bool "$changed")" "$(safe_bool "$rollback_attempted")" "$(safe_bool "$rolled_back")" \
    "$(safe_bool "$owner_unchanged")" "$(safe_bool "$symlink")" "$(safe_bool "$group_other_writable")" \
    "$(safe_bool "$health_http_200")" "$(safe_bool "$ready_http_200")"
  exit 2
}
trap fail ERR

[[ -d "$target" && ! -L "$target" ]]
original_owner=$(/usr/bin/stat -c '%u' -- "$target")
[[ "$original_owner" == "$(/usr/bin/id -u)" ]]
original_mode=$(/usr/bin/stat -c '%a' -- "$target")
[[ "$original_mode" =~ ^[0-7]{3,4}$ ]]
original_device_inode=$(/usr/bin/stat -c '%d:%i' -- "$target")
[[ "$original_device_inode" =~ ^[0-9]+:[0-9]+$ ]]

/usr/bin/chmod go-w -- "$target"
changed=true

[[ -d "$target" && ! -L "$target" ]]
[[ "$(/usr/bin/stat -c '%u' -- "$target")" == "$original_owner" ]]
[[ "$(/usr/bin/stat -c '%d:%i' -- "$target")" == "$original_device_inode" ]]
owner_unchanged=true
current_mode=$(/usr/bin/stat -c '%a' -- "$target")
[[ "$current_mode" =~ ^[0-7]{3,4}$ ]]
(( (8#$current_mode & 022) == 0 ))
group_other_writable=false

health_code=$(/usr/bin/curl --silent --show-error --output /dev/null --write-out '%{http_code}' --max-time 10 http://127.0.0.1:8080/api/health)
[[ "$health_code" == 200 ]]
health_http_200=true
ready_code=$(/usr/bin/curl --silent --show-error --output /dev/null --write-out '%{http_code}' --max-time 10 http://127.0.0.1:8080/api/ready)
[[ "$ready_code" == 200 ]]
ready_http_200=true

trap - ERR
/usr/bin/printf 'status=pass changed=true rollback_attempted=false rolled_back=false owner_unchanged=true symlink=false group_other_writable=false health_http_200=true ready_http_200=true target_scope=project_directory_only retries=0\n'
