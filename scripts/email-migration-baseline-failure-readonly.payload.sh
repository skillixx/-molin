#!/usr/bin/env bash
# 只读分类基线生成失败现场，不输出捕获文件原文或任何随机标识符。
set -Eeuo pipefail
umask 077
exec 2>/dev/null
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

readonly stat_bin=/usr/bin/stat
readonly find_bin=/usr/bin/find
readonly wc_bin=/usr/bin/wc

fail() {
  printf 'status=failed mode=email_migration_baseline_failure_readonly classification=%s writes=false database_access=false docker_access=false retries=0\n' "${1:?classification_required}"
  exit 2
}

[[ $# -eq 1 && $1 =~ ^[a-f0-9]{32}$ ]] || fail argument_gate
readonly stage="/home/pc/molin-runtime/email-migration-matrix-$1"
readonly baselines="$stage/baselines"
readonly output="$stage/output"
readonly stdout_file="$output/baseline.stdout"
readonly stderr_file="$output/baseline.stderr"

for tool in "$stat_bin" "$find_bin" "$wc_bin"; do
  [[ -x "$tool" && ! -L "$tool" ]] || fail host_tool
done
[[ -d "$stage" && ! -L "$stage" && "$($stat_bin -c '%U:%a' -- "$stage")" = pc:700 ]] || fail stage_identity
[[ -d "$baselines" && ! -L "$baselines" && -d "$output" && ! -L "$output" ]] || fail artifact_identity
for capture in "$stdout_file" "$stderr_file"; do
  [[ -f "$capture" && ! -L "$capture" && "$($stat_bin -c '%U' -- "$capture")" = pc ]] || fail capture_identity
done
readonly stdout_length=$($stat_bin -c '%s' -- "$stdout_file")
readonly stderr_length=$($stat_bin -c '%s' -- "$stderr_file")
readonly baseline_count=$($find_bin "$baselines" -mindepth 1 -maxdepth 1 -type f -printf '.' | "$wc_bin" -c)

classification=unclassified
generator_stage=none
generator_classification=none
check_fingerprints=none
if [[ $stdout_length -eq 0 && $stderr_length -eq 0 ]]; then
  classification=silent_guard_failure
elif [[ $stderr_length -eq 0 ]]; then
  IFS= read -r summary < "$stdout_file" || true
  if [[ "$summary" =~ ^status=failed\ mode=email_migration_baseline_generation\ stage=([a-z0-9_]+)\ classification=migration_sql\ mysql_error_code=([0-9]{4})\ sqlstate=([A-Z0-9]{5})\ sql_line=([0-9]{1,6})\ check_fingerprints=([0-9]+:[a-f0-9]{64}(,[0-9]+:[a-f0-9]{64}){7})\ outputs_created=false\ retained=false$ ]]; then
    generator_stage=${BASH_REMATCH[1]}
    generator_classification=migration_sql_e${BASH_REMATCH[2]}_${BASH_REMATCH[3]}_l${BASH_REMATCH[4]}
    check_fingerprints=${BASH_REMATCH[5]}
    classification=generator_sql_fingerprint
  elif [[ "$summary" =~ ^status=failed\ mode=email_migration_baseline_generation\ stage=([a-z0-9_]+)\ classification=migration_sql\ mysql_error_code=([0-9]{4})\ sqlstate=([A-Z0-9]{5})\ sql_line=([0-9]{1,6})\ outputs_created=false\ retained=false$ ]]; then
    generator_stage=${BASH_REMATCH[1]}
    generator_classification=migration_sql_e${BASH_REMATCH[2]}_${BASH_REMATCH[3]}_l${BASH_REMATCH[4]}
    classification=generator_sql_failure
  elif [[ "$summary" =~ ^status=failed\ mode=email_migration_baseline_generation\ stage=([a-z0-9_]+)\ classification=([a-z0-9_]+)\ outputs_created=false\ retained=false$ ]]; then
    generator_stage=${BASH_REMATCH[1]}
    generator_classification=${BASH_REMATCH[2]}
    case "$generator_stage:$generator_classification" in
      local_preflight:image_identity|local_preflight:output_not_empty|container_start:container_start|container_start:container_identity|mysql_ready:mysql_ready|mysql_ready:mysql_version|schema54_build:database_create|schema54_build:migration_sql|schema54_build:version_seed|schema54_build:dump_failed|schema54_build:dump_executable_comment|schema54_build:dump_scope|schema54_legacy:*|schema55_build:migration_sql|schema56_build:migration_sql) classification=generator_fixed_failure ;;
      *) classification=generator_unknown_summary ;;
    esac
  else
    classification=stdout_shape_unknown
  fi
else
  classification=stderr_nonempty
fi

printf 'status=pass mode=email_migration_baseline_failure_readonly classification=%s generator_stage=%s generator_classification=%s check_fingerprints=%s stdout_length=%s stderr_length=%s baseline_count=%s retained=true writes=false database_access=false docker_access=false retries=0\n' \
  "$classification" "$generator_stage" "$generator_classification" "$check_fingerprints" "$stdout_length" "$stderr_length" "$baseline_count"
