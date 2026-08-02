#!/usr/bin/env bash
# 只读确认上一次清理后的精确 migration Stage 是否仍然存在。
set -Eeuo pipefail
exec 2>/dev/null
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

[[ $# -eq 1 && $1 =~ ^[a-f0-9]{32}$ ]] || {
  printf 'status=failed mode=email_migration_failed_stage_postcheck classification=argument_gate writes=false database_access=false docker_access=false retries=0\n'
  exit 2
}
readonly stage="/home/pc/molin-runtime/email-migration-matrix-$1"
if [[ ! -e "$stage" && ! -L "$stage" ]]; then
  printf 'status=pass mode=email_migration_failed_stage_postcheck classification=stage_absent writes=false database_access=false docker_access=false retries=0\n'
  exit 0
fi
printf 'status=pass mode=email_migration_failed_stage_postcheck classification=stage_retained writes=false database_access=false docker_access=false retries=0\n'
