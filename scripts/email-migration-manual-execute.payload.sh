#!/usr/bin/env bash
# DirectMail Phase 4 手工上传迁移包的完整隔离矩阵执行入口。
set -Eeuo pipefail
exec 2>/dev/null
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
phase=argument_gate

fail() {
  trap - ERR
  printf 'status=failed mode=email_migration_manual_execute stage=%s stage_retained=true writes=false database_access=false docker_access=false retries=0\n' "${1:?stage_required}"
  exit 2
}

trap 'fail "$phase"' ERR
[[ $# -eq 4 ]] || fail argument_gate
nonce=$1
archive_sha=$2
payload_sha=$3
generator_sha=$4
[[ "$nonce" =~ ^[a-f0-9]{32}$ && "$archive_sha" =~ ^[a-f0-9]{64}$ && "$payload_sha" =~ ^[A-F0-9]{64}$ && "$generator_sha" =~ ^[A-F0-9]{64}$ ]] || fail argument_gate
stage="/home/pc/molin-runtime/email-migration-matrix-${nonce}"
archive="$stage/package.tar.gz"
phase=stage_identity
[[ -d "$stage" && ! -L "$stage" && "$(stat -c '%U:%a' -- "$stage")" == pc:700 ]] || fail stage_identity
[[ -f "$archive" && ! -L "$archive" && "$(stat -c '%U' -- "$archive")" == pc ]] || fail package_identity
mapfile -t stage_entries < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n' | sort)
[[ ${#stage_entries[@]} -eq 1 && "${stage_entries[0]}" = package.tar.gz ]] || fail stage_contents
[[ "$(sha256sum -- "$archive")" == "$archive_sha  $archive" ]] || fail package_hash
phase=archive_extract
tar -xzf "$archive" -C "$stage" || fail archive_extract
phase=source_identity
[[ -f "$stage/source-manifest.sha256" && ! -L "$stage/source-manifest.sha256" ]] || fail source_identity
[[ "$(wc -l < "$stage/source-manifest.sha256")" -eq 66 ]] || fail source_identity
(cd "$stage" && sha256sum --check --strict --status source-manifest.sha256) || fail source_hash
[[ -z "$(find "$stage" -type l -print -quit)" ]] || fail source_symlink
payload="$stage/source/scripts/email-migration-matrix-remote.payload.sh"
generator="$stage/source/scripts/generate-email-migration-baselines.sh"
[[ "$(sha256sum "$payload" | awk '{print toupper($1)}')" == "$payload_sha" ]] || fail payload_hash
[[ "$(sha256sum "$generator" | awk '{print toupper($1)}')" == "$generator_sha" ]] || fail generator_hash
chmod 500 "$payload" "$generator" || fail executable_mode
phase=payload_execute
trap - ERR
set +e
"$payload" --execute I_CONFIRM_EMAIL_MIGRATION_FULL_ISOLATION_MATRIX_ONCE "$nonce"
payload_exit=$?
set -e
[[ $payload_exit -eq 0 ]] || exit "$payload_exit"
trap 'fail "$phase"' ERR
phase=stage_cleanup
resolved=$(realpath -e -- "$stage")
[[ "$resolved" == "$stage" && "$resolved" =~ ^/home/pc/molin-runtime/email-migration-matrix-[a-f0-9]{32}$ ]] || fail cleanup_identity
[[ -z "$(find "$stage" -type l -print -quit)" ]] || fail cleanup_symlink
rm -rf --one-file-system -- "$stage" || fail cleanup_remove
[[ ! -e "$stage" && ! -L "$stage" ]] || fail cleanup_verify
printf 'status=pass stage=remote_stage_removed\n'
