#!/usr/bin/env bash
# DirectMail Phase 4 手工迁移包的唯一远端 Stage 创建。
set -Eeuo pipefail
exec 2>/dev/null
nonce=$1
[[ "$nonce" =~ ^[a-f0-9]{32}$ ]]
parent=/home/pc/molin-runtime
stage="$parent/email-migration-matrix-$nonce"
[[ -d "$parent" && ! -L "$parent" && "$(stat -c '%U' -- "$parent")" == pc && -w "$parent" ]]
shopt -s nullglob
existing=("$parent"/email-migration-matrix-*)
[[ ${#existing[@]} -eq 0 ]]
[[ ! -e "$stage" && ! -L "$stage" ]]
mkdir -m 700 -- "$stage"
[[ -d "$stage" && ! -L "$stage" && "$(stat -c '%U:%a' -- "$stage")" == pc:700 ]]
printf 'status=pass classification=migration_stage_created stage_count=1 stage_empty=true\n'
