#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

# 本脚本只负责固定候选的暂存、静态校验和关闭态只读预检，不允许进入实际服务切换模式。
change_id='__CHANGE_ID__'
expected_sha256='__EXPECTED_SHA256__'
candidate_root='/home/pc/molin/rollback/sms-phase5'
staging_parent="$candidate_root/runtime-drill-staging"
staging_dir="$staging_parent/$change_id"
runner="$staging_dir/run-sms-phase5-test-server-rollback-drill.sh"
evidence_dir="$candidate_root/runtime-drills/drill-$change_id"

fail() {
  printf 'rollback_runtime_staging=failed\n'
  printf 'failure_stage=%s\n' "$1"
  printf 'service_restarts=0\n'
  printf 'notification_posts=0\n'
  printf 'business_endpoint_posts=0\n'
  printf 'real_sms_sent=0\n'
  exit 2
}

verify_directory() {
  local path="$1"
  [ -d "$path" ] && [ ! -L "$path" ]
  [ "$(realpath -- "$path")" = "$path" ]
  [ "$(stat -c '%U:%a' "$path")" = pc:700 ]
}

case "${1:-}" in
  prepare)
    directories_created=1
    [ "$(id -un)" = pc ] || fail operator_identity
    verify_directory "$candidate_root" || fail candidate_root
    if [ -e "$staging_parent" ]; then
      verify_directory "$staging_parent" || fail staging_parent
    else
      mkdir -- "$staging_parent" || fail staging_parent_create
      chmod 700 "$staging_parent" || fail staging_parent_permissions
      directories_created=2
    fi
    [ ! -e "$staging_dir" ] || fail staging_exists
    mkdir -- "$staging_dir" || fail staging_create
    chmod 700 "$staging_dir" || fail staging_permissions
    printf 'rollback_runtime_staging_prepared=true\n'
    printf 'change_id=%s\n' "$change_id"
    printf 'remote_directories_created=%s\n' "$directories_created"
    printf 'remote_files_written=0\n'
    printf 'service_restarts=0\n'
    printf 'real_sms_sent=0\n'
    ;;
  verify)
    verify_directory "$candidate_root" || fail candidate_root
    verify_directory "$staging_parent" || fail staging_parent
    verify_directory "$staging_dir" || fail staging_dir
    [ -f "$runner" ] && [ ! -L "$runner" ] || fail runner_identity
    [ "$(stat -c '%U' "$runner")" = pc ] || fail runner_owner
    chmod 600 "$runner" || fail runner_permissions
    [ "$(stat -c '%a' "$runner")" = 600 ] || fail runner_mode
    [ "$(stat -c '%h' "$runner")" = 1 ] || fail runner_hardlink
    [ "$(sha256sum "$runner" | awk '{print $1}')" = "$expected_sha256" ] || fail runner_hash
    bash -n "$runner" || fail runner_syntax
    bash "$runner" --self-test || fail runner_self_test
    [ ! -e "$evidence_dir" ] || fail evidence_preexists
    bash "$runner" --preflight || fail runner_preflight
    [ ! -e "$evidence_dir" ] || fail preflight_created_evidence
    printf 'rollback_runtime_staging_validation=passed\n'
    printf 'change_id=%s\n' "$change_id"
    printf 'runner_sha256=%s\n' "$expected_sha256"
    printf 'runner_owner_mode=pc:600\n'
    printf 'bash_syntax=passed\n'
    printf 'runner_self_test=passed\n'
    printf 'closed_state_readonly_preflight=passed\n'
    printf 'remote_stage_directory_present=true\n'
    printf 'remote_files_written=1\n'
    printf 'service_restarts=0\n'
    printf 'notification_posts=0\n'
    printf 'business_endpoint_posts=0\n'
    printf 'real_sms_sent=0\n'
    ;;
  cleanup)
    # 只清理由本次 prepare 排他创建的精确目录；不使用递归删除或通配符。
    if [ -d "$staging_dir" ] && [ ! -L "$staging_dir" ] &&
       [ "$(realpath -- "$staging_dir")" = "$staging_dir" ]; then
      rm -f -- "$runner"
      rmdir -- "$staging_dir" 2>/dev/null || true
    fi
    printf 'rollback_runtime_staging_cleanup=completed\n'
    printf 'service_restarts=0\n'
    printf 'real_sms_sent=0\n'
    ;;
  *)
    fail invalid_mode
    ;;
esac
