#!/usr/bin/env bash
# DirectMail Phase 4 migration 宿主工具固定路径纯只读诊断。
set -Eeuo pipefail
exec 2>/dev/null
for item in \
  docker:/usr/bin/docker \
  sha256sum:/usr/bin/sha256sum \
  awk:/usr/bin/awk \
  stat:/usr/bin/stat \
  install:/usr/bin/install \
  mkdir:/usr/bin/mkdir \
  chmod:/usr/bin/chmod \
  rm:/usr/bin/rm \
  rmdir:/usr/bin/rmdir \
  wc:/usr/bin/wc; do
  name=${item%%:*}
  path=${item#*:}
  exists=false; executable=false; regular=false; symlink=false; resolved_regular=false; resolved_root_owned=false
  [[ -e "$path" || -L "$path" ]] && exists=true
  [[ -x "$path" ]] && executable=true
  [[ -f "$path" ]] && regular=true
  [[ -L "$path" ]] && symlink=true
  if [[ "$exists" == true ]]; then
    resolved=$(realpath -e -- "$path")
    [[ -f "$resolved" && ! -L "$resolved" ]] && resolved_regular=true
    [[ "$(stat -c '%U' -- "$resolved")" == root ]] && resolved_root_owned=true
  fi
  printf 'tool=%s exists=%s executable=%s regular=%s symlink=%s resolved_regular=%s resolved_root_owned=%s\n' \
    "$name" "$exists" "$executable" "$regular" "$symlink" "$resolved_regular" "$resolved_root_owned"
done
printf 'status=pass mode=email_migration_host_tool_readonly writes=false docker_access=false database_access=false retries=0\n'
