#!/usr/bin/env bash
# 只读核验独立 MySQL 8 矩阵保留 Stage 的固定输出状态，不读取或回显输出原文。
set -Eeuo pipefail
umask 077
exec 2>/dev/null
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

readonly stat_bin=/usr/bin/stat
readonly find_bin=/usr/bin/find
readonly sha256sum_bin=/usr/bin/sha256sum
readonly realpath_bin=/usr/bin/realpath

fail() {
  printf 'status=failed mode=email_migration_matrix_stage_readonly classification=%s writes=false database_access=false docker_access=false retries=0\n' "${1:?classification_required}"
  exit 2
}

[[ $# -eq 2 && $1 =~ ^[a-f0-9]{32}$ && $2 =~ ^[a-f0-9]{64}$ ]] || fail argument_gate
readonly nonce=$1
readonly archive_sha=$2
readonly stage="/home/pc/molin-runtime/email-migration-matrix-${nonce}"
readonly package="$stage/package.tar.gz"
readonly output="$stage/output"

for tool in "$stat_bin" "$find_bin" "$sha256sum_bin" "$realpath_bin"; do
  [[ -x "$tool" && ! -L "$tool" ]] || fail host_tool
done
[[ -d "$stage" && ! -L "$stage" && "$($stat_bin -c '%U:%a' -- "$stage")" = pc:700 ]] || fail stage_identity
[[ "$($realpath_bin -e -- "$stage")" = "$stage" ]] || fail stage_identity
[[ -f "$package" && ! -L "$package" && "$($stat_bin -c '%U' -- "$package")" = pc ]] || fail package_identity
read -r actual_archive_sha _ < <("$sha256sum_bin" -- "$package")
[[ "$actual_archive_sha" = "$archive_sha" ]] || fail package_hash
[[ -d "$output" && ! -L "$output" && "$($stat_bin -c '%U:%a' -- "$output")" = pc:700 ]] || fail output_identity
[[ -z "$($find_bin "$stage" -type l -print -quit)" ]] || fail symlink_present

file_state() {
  local name=${1:?name_required}
  local expected_sha=${2:?sha_required}
  local file="$output/$name" size actual_sha
  [[ "$name" =~ ^(baseline|matrix55|partial55|matrix56|partial56)\.stdout$ ]]
  [[ "$expected_sha" =~ ^[A-F0-9]{64}$ ]]
  if [[ ! -e "$file" && ! -L "$file" ]]; then printf absent; return; fi
  [[ -f "$file" && ! -L "$file" && "$($stat_bin -c '%U' -- "$file")" = pc ]] || fail output_file_identity
  size=$($stat_bin -c '%s' -- "$file")
  if [[ "$size" -eq 0 ]]; then printf empty; return; fi
  read -r actual_sha _ < <("$sha256sum_bin" -- "$file")
  if [[ "${actual_sha^^}" = "$expected_sha" ]]; then printf expected; else printf other; fi
}

stderr_state() {
  local name=${1:?name_required}
  local file="$output/$name" size
  [[ "$name" =~ ^(baseline|matrix-container|matrix55|partial55|matrix56|partial56)\.stderr$ ]]
  if [[ ! -e "$file" && ! -L "$file" ]]; then printf absent; return; fi
  [[ -f "$file" && ! -L "$file" && "$($stat_bin -c '%U' -- "$file")" = pc ]] || fail output_file_identity
  size=$($stat_bin -c '%s' -- "$file")
  if [[ "$size" -eq 0 ]]; then printf empty; else printf nonempty; fi
}

baseline=$(file_state baseline.stdout BF12EDE2B73010EDA1939CB8A113ED970B2E9E202058B9A86038AD7347D02319) || fail state_probe
baseline_stderr=$(stderr_state baseline.stderr) || fail state_probe
container_stderr=$(stderr_state matrix-container.stderr) || fail state_probe
matrix55=$(file_state matrix55.stdout 2B351B710CBBEA5FD24E7FE0739F0107866ABD30225EC7F8653BDB45139AD3E1) || fail state_probe
matrix55_stderr=$(stderr_state matrix55.stderr) || fail state_probe
partial55=$(file_state partial55.stdout A0EA13852C7C77EBD978F1192EF23DF253287F63EFB46232E75E2929399E2B45) || fail state_probe
partial55_stderr=$(stderr_state partial55.stderr) || fail state_probe
matrix56=$(file_state matrix56.stdout 91BFDA21D0A13FFFB1B7F01586D7C5751BA05444699E666707388506B4B7A6A3) || fail state_probe
matrix56_stderr=$(stderr_state matrix56.stderr) || fail state_probe
partial56=$(file_state partial56.stdout 6A52CB921A53B4E27DEF000AAEB23C850AFEF65A149DE0E0B34C05A86BD62E9F) || fail state_probe
partial56_stderr=$(stderr_state partial56.stderr) || fail state_probe
readonly baseline baseline_stderr container_stderr matrix55 matrix55_stderr partial55 partial55_stderr matrix56 matrix56_stderr partial56 partial56_stderr
for state_value in "$baseline" "$baseline_stderr" "$container_stderr" "$matrix55" "$matrix55_stderr" "$partial55" "$partial55_stderr" "$matrix56" "$matrix56_stderr" "$partial56" "$partial56_stderr"; do
  [[ "$state_value" =~ ^(absent|empty|expected|other|nonempty)$ ]] || fail state_shape
done

classification=matrix_stage_classified
printf 'status=pass mode=email_migration_matrix_stage_readonly classification=%s baseline=%s baseline_stderr=%s container_stderr=%s matrix55=%s matrix55_stderr=%s partial55=%s partial55_stderr=%s matrix56=%s matrix56_stderr=%s partial56=%s partial56_stderr=%s writes=false database_access=false docker_access=false retries=0\n' \
  "$classification" "$baseline" "$baseline_stderr" "$container_stderr" "$matrix55" "$matrix55_stderr" "$partial55" "$partial55_stderr" "$matrix56" "$matrix56_stderr" "$partial56" "$partial56_stderr"
