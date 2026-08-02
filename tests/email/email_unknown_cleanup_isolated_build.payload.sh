set -Eeuo pipefail

# 正式模式始终使用固定生产构建根和属主；只有脚本自身的 SelfTest 才可注入严格受限的临时根。
resolve_build_identity() {
  local nonce=${1:?nonce_required}
  local resolved_root
  local current_owner
  [[ "$nonce" =~ ^[a-f0-9]{32}$ ]]
  if [[ "${SELFTEST:-0}" == 1 ]]; then
    [[ -n "${SELFTEST_BUILD_ROOT:-}" && "$SELFTEST_BUILD_ROOT" == /* ]]
    [[ "$SELFTEST_BUILD_ROOT" =~ ^/(tmp|var/tmp)/[-._a-zA-Z0-9/]+$ ]]
    [[ -d "$SELFTEST_BUILD_ROOT" && ! -L "$SELFTEST_BUILD_ROOT" ]]
    resolved_root=$(/usr/bin/readlink -f -- "$SELFTEST_BUILD_ROOT")
    [[ "$resolved_root" == "$SELFTEST_BUILD_ROOT" ]]
    current_owner=$(/usr/bin/id -un)
    [[ -n "$current_owner" && "$current_owner" =~ ^[-._a-zA-Z0-9]+$ ]]
    [[ "$(/usr/bin/stat -c '%U' -- "$resolved_root")" == "$current_owner" ]]
    build_root=$resolved_root
    build_owner=$current_owner
    build_root_mode=$(/usr/bin/stat -c '%a' -- "$resolved_root")
    [[ "$build_root_mode" =~ ^[0-7]{3,4}$ ]]
    return 0
  fi
  [[ "${SELFTEST:-0}" == 0 && -z "${SELFTEST_BUILD_ROOT:-}" ]]
  build_root="/home/pc/molin-qa-email-cleanup-build-${nonce}"
  build_owner=pc
  build_root_mode=700
}

# 解析 Go 工具时只使用 Bash 内建命令，随后冻结解析出的绝对普通可执行文件及其摘要。
resolve_go_tool() {
  local tool_name=${1:?tool_name_required}
  local candidate
  local resolved
  candidate=$(command -v -- "$tool_name") || return 1
  if [[ "$candidate" != /* ]]; then
    return 1
  fi
  resolved=$(/usr/bin/readlink -f -- "$candidate") || return 1
  if [[ "$resolved" != /* || ! -f "$resolved" || -L "$resolved" || ! -x "$resolved" || ! "$resolved" =~ ^/[-._a-zA-Z0-9/]+$ ]]; then
    return 1
  fi
  printf '%s\n' "$resolved"
}

# 每次使用工具前复核路径、文件属性和 SHA，防止预检后被替换。
assert_frozen_tool() {
  local tool_path=${1:?tool_path_required}
  local expected_sha=${2:?expected_sha_required}
  local actual_sha
  if [[ "$tool_path" != /* || ! -f "$tool_path" || -L "$tool_path" || ! -x "$tool_path" ]]; then
    return 1
  fi
  if [[ ! "$expected_sha" =~ ^[a-f0-9]{64}$ || "$expected_sha" == 0000000000000000000000000000000000000000000000000000000000000000 ]]; then
    return 1
  fi
  actual_sha=$(/usr/bin/sha256sum -- "$tool_path" | /usr/bin/awk '{print $1}') || return 1
  [[ "$actual_sha" == "$expected_sha" ]] || return 1
}

# 每次 Go 操作前复核归档摘要，防止上传后或构建期间替换源码快照。
assert_frozen_archive() {
  local archive_path=${1:?archive_required}
  local expected_sha=${2:?archive_sha_required}
  [[ -f "$archive_path" && ! -L "$archive_path" ]]
  [[ "$expected_sha" =~ ^[a-f0-9]{64}$ && "$expected_sha" != 0000000000000000000000000000000000000000000000000000000000000000 ]]
  [[ "$(/usr/bin/sha256sum -- "$archive_path" | /usr/bin/awk '{print $1}')" == "$expected_sha" ]]
}

# 解包前拒绝绝对路径、父目录、越界成员、敏感命名以及非普通文件/目录成员。
assert_safe_server_archive() {
  local archive_path=${1:?archive_required}
  local member
  local member_lower
  local type_char
  local member_count=0
  local type_count=0
  /usr/bin/tar -tf "$archive_path" >/dev/null
  while IFS= read -r member; do
    (( member_count += 1 ))
    [[ -n "$member" && "$member" != /* && "$member" == server/* ]]
    [[ "/$member/" != *'/../'* && "/$member/" != *'/./'* ]]
    member_lower=${member,,}
    if [[ "$member_lower" =~ (^|/)(\.env($|\.)|[^/]*(credential|secret)[^/]*|[^/]*\.(pem|key)$) ]]; then return 1; fi
  done < <(/usr/bin/tar -tf "$archive_path")
  (( member_count > 0 ))
  while IFS= read -r type_char; do
    (( type_count += 1 ))
    [[ "$type_char" == - || "$type_char" == d ]]
  done < <(/usr/bin/tar -tvf "$archive_path" | /usr/bin/awk '{print substr($0,1,1)}')
  (( type_count == member_count ))
}

# 启动门禁只接受完全没有后台状态资产的目录，正式 launch 与 SelfTest 共用此实现。
assert_launch_artifacts_absent() {
  local artifact
  for artifact in launch.state launch.state.tmp launch.ready launch.ready.tmp status.final status.tmp marker.final marker.tmp result.tmp error.tmp; do
    if [[ -e "${build_root}/${artifact}" || -L "${build_root}/${artifact}" ]]; then
      return 1
    fi
  done
}

# 轮询只读取固定证据并给出单一分类；正式 poll 与 SelfTest 不得另设分类分支。
read_poll_evidence() {
  local timeout_seconds=${1:?timeout_required}
  local required
  local launch_line
  local worker_pid
  local worker_starttime
  local started_epoch
  local now_epoch
  local observed_starttime
  local status_line
  local binary_sha
  local binary_size
  local binary
  [[ "$timeout_seconds" =~ ^[1-9][0-9]*$ ]]
  [[ -d "$build_root" && ! -L "$build_root" && "$(/usr/bin/stat -c '%U:%a' -- "$build_root")" == "${build_owner}:${build_root_mode}" ]]
  for required in launch.state launch.ready result.tmp error.tmp; do
    [[ -f "${build_root}/${required}" && ! -L "${build_root}/${required}" && "$(/usr/bin/stat -c '%U:%a' -- "${build_root}/${required}")" == "${build_owner}:600" ]]
  done
  launch_line=$(/usr/bin/head -n 2 -- "${build_root}/launch.state")
  [[ "$launch_line" =~ ^pid=([1-9][0-9]*)\ starttime=([1-9][0-9]*)\ started_epoch=([1-9][0-9]*)\ payload_sha256=([a-f0-9]{64})\ archive_sha256=([a-f0-9]{64})$ ]]
  worker_pid=${BASH_REMATCH[1]}
  worker_starttime=${BASH_REMATCH[2]}
  started_epoch=${BASH_REMATCH[3]}
  [[ "$(/usr/bin/cat -- "${build_root}/launch.ready")" == ready ]]
  if [[ -e "${build_root}/marker.tmp" || -L "${build_root}/marker.tmp" || -e "${build_root}/status.tmp" || -L "${build_root}/status.tmp" ]]; then
    printf 'status=unknown reason=partial_evidence retained=true\n'
    return 0
  fi
  if [[ -s "${build_root}/error.tmp" || -s "${build_root}/result.tmp" ]]; then
    printf 'status=unknown reason=unexpected_output retained=true\n'
    return 0
  fi
  if [[ -e "${build_root}/marker.final" || -L "${build_root}/marker.final" ]]; then
    [[ -f "${build_root}/marker.final" && ! -L "${build_root}/marker.final" && "$(/usr/bin/stat -c '%U:%a' -- "${build_root}/marker.final")" == "${build_owner}:600" ]]
    [[ "$(/usr/bin/cat -- "${build_root}/marker.final")" == complete ]]
    [[ -f "${build_root}/status.final" && ! -L "${build_root}/status.final" && "$(/usr/bin/stat -c '%U:%a' -- "${build_root}/status.final")" == "${build_owner}:600" ]]
    status_line=$(/usr/bin/head -n 2 -- "${build_root}/status.final")
    if [[ "$status_line" =~ ^status=pass\ stage=complete\ binary_sha256=([a-f0-9]{64})\ binary_size=([1-9][0-9]*)$ ]]; then
      binary_sha=${BASH_REMATCH[1]}
      binary_size=${BASH_REMATCH[2]}
      binary="${build_root}/email-unknown-restart-cleanup.test"
      [[ -f "$binary" && ! -L "$binary" && "$(/usr/bin/stat -c '%U:%a:%s' -- "$binary")" == "${build_owner}:500:${binary_size}" ]]
      [[ "$(/usr/bin/sha256sum -- "$binary" | /usr/bin/awk '{print $1}')" == "$binary_sha" ]]
      printf 'status=pass stage=complete binary_sha256=%s binary_size=%s retained=true cleanup_executed=false\n' "$binary_sha" "$binary_size"
      return 0
    fi
    [[ "$status_line" =~ ^status=failed\ stage=(preflight|environment_gate|extract|toolchain|gofmt|unit_test|vet|build|summary)$ ]]
    printf 'status=failed stage=%s retained=true cleanup_executed=false\n' "${BASH_REMATCH[1]}"
    return 0
  fi
  [[ ! -e "${build_root}/status.final" && ! -L "${build_root}/status.final" ]]
  now_epoch=$(/usr/bin/date +%s)
  if (( now_epoch - started_epoch >= timeout_seconds )); then
    printf 'status=unknown reason=timeout retained=true\n'
    return 0
  fi
  if [[ ! -r "/proc/${worker_pid}/stat" ]]; then
    printf 'status=unknown reason=worker_missing retained=true\n'
    return 0
  fi
  observed_starttime=$(/usr/bin/awk '{print $22}' "/proc/${worker_pid}/stat")
  if [[ "$observed_starttime" != "$worker_starttime" ]]; then
    printf 'status=unknown reason=pid_reused retained=true\n'
    return 0
  fi
  printf 'status=pending reason=running retained=true\n'
}

# 后台 worker 必须在空环境中运行，任何集成、数据库或 Redis 变量都失败关闭。
assert_clean_worker_environment() {
  if /usr/bin/env | /usr/bin/grep -Eq '^(RUN_EMAIL|EMAIL_UNKNOWN|MYSQL|REDIS)'; then
    return 1
  fi
}

# 离线自检只创建受控临时证据；fixture worker 不执行 Go，也不访问项目或网络。
if [[ "${1:-}" == --self-test ]]; then
  SELFTEST=1
  export SELFTEST
  test_root=$(/usr/bin/mktemp -d)
  [[ "$test_root" =~ ^/(tmp|var/tmp)/[-._a-zA-Z0-9/]+$ && -d "$test_root" && ! -L "$test_root" ]]
  /usr/bin/chmod 700 -- "$test_root"
  cleanup_selftest() {
    if [[ -n "${test_root:-}" && "$test_root" =~ ^/(tmp|var/tmp)/[-._a-zA-Z0-9/]+$ && -d "$test_root" && ! -L "$test_root" ]]; then
      /usr/bin/find "$test_root" -depth -delete
    fi
  }
  trap cleanup_selftest EXIT

  tool_root="${test_root}/tools"
  /usr/bin/mkdir -- "$tool_root"
  /usr/bin/chmod 700 -- "$tool_root"
  printf '#!/bin/sh\nexit 0\n' >"$tool_root/go"
  printf '#!/bin/sh\nexit 0\n' >"$tool_root/gofmt"
  /usr/bin/chmod 500 -- "$tool_root/go" "$tool_root/gofmt"
  PATH="$tool_root" go_path=$(resolve_go_tool go)
  PATH="$tool_root" gofmt_path=$(resolve_go_tool gofmt)
  [[ "$go_path" == "$tool_root/go" && "$gofmt_path" == "$tool_root/gofmt" ]]
  go_sha=$(/usr/bin/sha256sum -- "$go_path" | /usr/bin/awk '{print $1}')
  assert_frozen_tool "$go_path" "$go_sha"
  /usr/bin/chmod 700 -- "$tool_root/go"
  printf '#!/bin/sh\nexit 1\n' >"$tool_root/go"
  /usr/bin/chmod 500 -- "$tool_root/go"
  if assert_frozen_tool "$go_path" "$go_sha" >/dev/null 2>&1; then exit 1; fi
  if PATH="$tool_root" resolve_go_tool missing-go >/dev/null 2>&1; then exit 1; fi
  cases=4

  # 每个 poll fixture 都使用真实目录和证据文件，并通过正式 read_poll_evidence 判定。
  fixture_index=0
  create_poll_fixture() {
    local worker_pid=${1:?pid_required}
    local worker_starttime=${2:?starttime_required}
    local started_epoch=${3:?epoch_required}
    (( fixture_index += 1 ))
    SELFTEST_BUILD_ROOT="${test_root}/fixture-${fixture_index}"
    export SELFTEST_BUILD_ROOT
    /usr/bin/mkdir -- "$SELFTEST_BUILD_ROOT"
    /usr/bin/chmod 700 -- "$SELFTEST_BUILD_ROOT"
    resolve_build_identity 11111111111111111111111111111111
    printf 'pid=%s starttime=%s started_epoch=%s payload_sha256=%064d archive_sha256=%064d\n' "$worker_pid" "$worker_starttime" "$started_epoch" 1 2 >"${build_root}/launch.state"
    printf 'ready\n' >"${build_root}/launch.ready"
    : >"${build_root}/result.tmp"
    : >"${build_root}/error.tmp"
    /usr/bin/chmod 600 -- "${build_root}/launch.state" "${build_root}/launch.ready" "${build_root}/result.tmp" "${build_root}/error.tmp"
  }
  assert_poll_result() {
    local expected=${1:?expected_required}
    local timeout_seconds=${2:?timeout_required}
    local actual
    actual=$(read_poll_evidence "$timeout_seconds")
    [[ "$actual" == "$expected" ]]
  }

  current_pid=$BASHPID
  current_starttime=$(/usr/bin/awk '{print $22}' "/proc/${current_pid}/stat")
  current_epoch=$(/usr/bin/date +%s)
  [[ "$current_starttime" =~ ^[1-9][0-9]*$ && "$current_epoch" =~ ^[1-9][0-9]*$ ]]

  SELFTEST_BUILD_ROOT="${test_root}/duplicate"
  export SELFTEST_BUILD_ROOT
  /usr/bin/mkdir -- "$SELFTEST_BUILD_ROOT"
  /usr/bin/chmod 700 -- "$SELFTEST_BUILD_ROOT"
  resolve_build_identity 22222222222222222222222222222222
  assert_launch_artifacts_absent
  : >"${build_root}/launch.state"
  if assert_launch_artifacts_absent >/dev/null 2>&1; then exit 1; fi
  (( cases += 1 ))

  create_poll_fixture "$current_pid" "$(( current_starttime + 1 ))" "$current_epoch"
  assert_poll_result 'status=unknown reason=pid_reused retained=true' 60
  (( cases += 1 ))

  create_poll_fixture "$current_pid" "$current_starttime" "$current_epoch"
  assert_poll_result 'status=pending reason=running retained=true' 60
  (( cases += 1 ))

  create_poll_fixture "$current_pid" "$current_starttime" "$current_epoch"
  : >"${build_root}/marker.tmp"
  assert_poll_result 'status=unknown reason=partial_evidence retained=true' 60
  (( cases += 1 ))

  create_poll_fixture "$current_pid" "$current_starttime" "$current_epoch"
  printf 'fixture stderr\n' >"${build_root}/error.tmp"
  assert_poll_result 'status=unknown reason=unexpected_output retained=true' 60
  (( cases += 1 ))

  create_poll_fixture "$current_pid" "$current_starttime" "$(( current_epoch - 61 ))"
  assert_poll_result 'status=unknown reason=timeout retained=true' 60
  (( cases += 1 ))

  create_poll_fixture 999999999 1 "$current_epoch"
  assert_poll_result 'status=unknown reason=worker_missing retained=true' 60
  (( cases += 1 ))

  create_poll_fixture "$current_pid" "$current_starttime" "$current_epoch"
  binary="${build_root}/email-unknown-restart-cleanup.test"
  printf 'selftest-binary-fixture\n' >"$binary"
  /usr/bin/chmod 500 -- "$binary"
  binary_sha=$(/usr/bin/sha256sum -- "$binary" | /usr/bin/awk '{print $1}')
  binary_size=$(/usr/bin/stat -c '%s' -- "$binary")
  printf 'status=pass stage=complete binary_sha256=%s binary_size=%s\n' "$binary_sha" "$binary_size" >"${build_root}/status.final"
  printf 'complete\n' >"${build_root}/marker.final"
  /usr/bin/chmod 600 -- "${build_root}/status.final" "${build_root}/marker.final"
  assert_poll_result "status=pass stage=complete binary_sha256=${binary_sha} binary_size=${binary_size} retained=true cleanup_executed=false" 60
  (( cases += 1 ))

  export RUN_EMAIL_UNKNOWN_RESTART_INTEGRATION=1
  if assert_clean_worker_environment; then exit 1; fi
  unset RUN_EMAIL_UNKNOWN_RESTART_INTEGRATION
  assert_clean_worker_environment
  (( cases += 1 ))
  printf 'status=pass mode=selftest cases=%s duplicate_launch_rejected=true pid_reuse_rejected=true running_observed=true partial_marker_rejected=true stderr_rejected=true timeout_unknown=true worker_missing_unknown=true pass_binary_verified=true integration_env_rejected=true fixture_worker_go_executed=false external_access=false\n' "$cases"
  exit 0
fi

# 正式模式忽略并拒绝外部注入的 SelfTest 身份，只允许固定 /home/pc 根和 pc 属主。
SELFTEST=0
unset SELFTEST_BUILD_ROOT

mode=${1:?mode_required}
if [[ "$mode" == --launch ]]; then
  nonce=${2:?nonce_required}
  expected_payload_sha=${3:?payload_sha_required}
  expected_archive_sha=${4:?archive_sha_required}
  [[ "$nonce" =~ ^[a-f0-9]{32}$ ]]
  [[ "$expected_payload_sha" =~ ^[a-f0-9]{64}$ && "$expected_payload_sha" != 0000000000000000000000000000000000000000000000000000000000000000 ]]
  [[ "$expected_archive_sha" =~ ^[a-f0-9]{64}$ && "$expected_archive_sha" != 0000000000000000000000000000000000000000000000000000000000000000 ]]
  resolve_build_identity "$nonce"
  payload="${build_root}/build.payload.sh"
  archive="${build_root}/server-snapshot.tar"
  umask 077
  launch_stage=preflight
  launch_failed() {
    local exit_code=$?
    printf 'status=failed stage=%s exit=%d started=false\n' "$launch_stage" "$exit_code"
    exit "$exit_code"
  }
  trap launch_failed ERR
  [[ -d "$build_root" && ! -L "$build_root" && "$(/usr/bin/stat -c '%U:%a' -- "$build_root")" == pc:700 ]]
  [[ -f "$archive" && ! -L "$archive" ]]
  assert_frozen_archive "$archive" "$expected_archive_sha"
  assert_safe_server_archive "$archive"
  [[ -f "$payload" && ! -L "$payload" && "$(/usr/bin/stat -c '%U:%a' -- "$payload")" == pc:500 ]]
  [[ "$(/usr/bin/sha256sum -- "$payload" | /usr/bin/awk '{print $1}')" == "$expected_payload_sha" ]]
  launch_stage=duplicate_gate
  assert_launch_artifacts_absent
  launch_stage=start
  : >"${build_root}/result.tmp"
  : >"${build_root}/error.tmp"
  /usr/bin/chmod 600 -- "${build_root}/result.tmp" "${build_root}/error.tmp"
  /usr/bin/nohup /usr/bin/env -i \
    PATH=/usr/local/go/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    HOME=/home/pc USER=pc LOGNAME=pc LANG=C.UTF-8 \
    GOMODCACHE=/home/pc/go/pkg/mod GOPROXY=off GOSUMDB=off \
    /bin/bash --noprofile --norc "$payload" --worker "$nonce" "$expected_payload_sha" "$expected_archive_sha" \
    >"${build_root}/result.tmp" 2>"${build_root}/error.tmp" </dev/null &
  worker_pid=$!
  [[ "$worker_pid" =~ ^[1-9][0-9]*$ && -r "/proc/${worker_pid}/stat" ]]
  worker_starttime=$(/usr/bin/awk '{print $22}' "/proc/${worker_pid}/stat")
  started_epoch=$(/usr/bin/date +%s)
  [[ "$worker_starttime" =~ ^[1-9][0-9]*$ && "$started_epoch" =~ ^[1-9][0-9]*$ ]]
  printf 'pid=%s starttime=%s started_epoch=%s payload_sha256=%s archive_sha256=%s\n' "$worker_pid" "$worker_starttime" "$started_epoch" "$expected_payload_sha" "$expected_archive_sha" >"${build_root}/launch.state.tmp"
  /usr/bin/chmod 600 -- "${build_root}/launch.state.tmp"
  /usr/bin/mv -T -- "${build_root}/launch.state.tmp" "${build_root}/launch.state"
  printf 'ready\n' >"${build_root}/launch.ready.tmp"
  /usr/bin/chmod 600 -- "${build_root}/launch.ready.tmp"
  /usr/bin/mv -T -- "${build_root}/launch.ready.tmp" "${build_root}/launch.ready"
  trap - ERR
  printf 'status=started pid=%s starttime=%s retained=true repeated=false\n' "$worker_pid" "$worker_starttime"
  exit 0
fi

if [[ "$mode" == --poll ]]; then
  nonce=${2:?nonce_required}
  timeout_seconds=${3:?timeout_required}
  [[ "$nonce" =~ ^[a-f0-9]{32}$ && "$timeout_seconds" =~ ^[1-9][0-9]*$ ]]
  resolve_build_identity "$nonce"
  read_poll_evidence "$timeout_seconds"
  exit 0
fi

[[ "$mode" == --worker ]]
nonce=${2:?nonce_required}
expected_payload_sha=${3:?payload_sha_required}
expected_archive_sha=${4:?archive_sha_required}
[[ "$nonce" =~ ^[a-f0-9]{32}$ ]]
resolve_build_identity "$nonce"
archive="${build_root}/server-snapshot.tar"
source_root="${build_root}/source/server"
binary="${build_root}/email-unknown-restart-cleanup.test"
export GOCACHE="${build_root}/.gocache"
export GOFLAGS='-mod=readonly'
stage=preflight

fail_closed() {
  local exit_code=$?
  set +e
  printf 'status=failed stage=%s\n' "$stage" >"${build_root}/status.tmp"
  /usr/bin/chmod 600 -- "${build_root}/status.tmp"
  /usr/bin/mv -T -- "${build_root}/status.tmp" "${build_root}/status.final"
  printf 'complete\n' >"${build_root}/marker.tmp"
  /usr/bin/chmod 600 -- "${build_root}/marker.tmp"
  /usr/bin/mv -T -- "${build_root}/marker.tmp" "${build_root}/marker.final"
  exit "$exit_code"
}
trap fail_closed ERR

[[ -d "$build_root" && ! -L "$build_root" ]]
[[ "$(/usr/bin/stat -c '%U:%a' -- "$build_root")" == pc:700 ]]
[[ -f "$archive" && ! -L "$archive" ]]
assert_frozen_archive "$archive" "$expected_archive_sha"
assert_safe_server_archive "$archive"
payload="${build_root}/build.payload.sh"
[[ -f "$payload" && ! -L "$payload" && "$(/usr/bin/stat -c '%U:%a' -- "$payload")" == pc:500 ]]
[[ "$(/usr/bin/sha256sum -- "$payload" | /usr/bin/awk '{print $1}')" == "$expected_payload_sha" ]]
ready_seen=false
for _ in {1..200}; do
  if [[ -f "${build_root}/launch.ready" && ! -L "${build_root}/launch.ready" ]]; then
    ready_seen=true
    break
  fi
  /usr/bin/sleep 0.05
done
[[ "$ready_seen" == true ]]
[[ -f "${build_root}/launch.state" && ! -L "${build_root}/launch.state" && "$(/usr/bin/stat -c '%U:%a' -- "${build_root}/launch.state")" == pc:600 ]]
launch_line=$(/usr/bin/head -n 2 -- "${build_root}/launch.state")
[[ "$launch_line" =~ ^pid=([1-9][0-9]*)\ starttime=([1-9][0-9]*)\ started_epoch=([1-9][0-9]*)\ payload_sha256=([a-f0-9]{64})\ archive_sha256=([a-f0-9]{64})$ ]]
[[ "${BASH_REMATCH[1]}" == "$BASHPID" && "${BASH_REMATCH[4]}" == "$expected_payload_sha" && "${BASH_REMATCH[5]}" == "$expected_archive_sha" ]]
[[ "$(/usr/bin/awk '{print $22}' "/proc/${BASHPID}/stat")" == "${BASH_REMATCH[2]}" ]]
[[ -f "${build_root}/launch.ready" && ! -L "${build_root}/launch.ready" && "$(/usr/bin/cat -- "${build_root}/launch.ready")" == ready ]]

stage=environment_gate
assert_clean_worker_environment

stage=extract
assert_frozen_archive "$archive" "$expected_archive_sha"
assert_safe_server_archive "$archive"
/usr/bin/mkdir -m 700 -- "${build_root}/source"
/usr/bin/tar -xf "$archive" -C "${build_root}/source"
[[ -f "${source_root}/go.mod" && ! -L "${source_root}/go.mod" ]]
cd "$source_root"

stage=toolchain
go_bin=$(resolve_go_tool go)
gofmt_bin=$(resolve_go_tool gofmt)
go_sha=$(/usr/bin/sha256sum -- "$go_bin" | /usr/bin/awk '{print $1}')
gofmt_sha=$(/usr/bin/sha256sum -- "$gofmt_bin" | /usr/bin/awk '{print $1}')
assert_frozen_tool "$go_bin" "$go_sha"
assert_frozen_tool "$gofmt_bin" "$gofmt_sha"
go_version=$($go_bin version | /usr/bin/tr ' ' '_')

stage=gofmt
assert_frozen_archive "$archive" "$expected_archive_sha"
assert_frozen_tool "$gofmt_bin" "$gofmt_sha"
mapfile -t unformatted < <("$gofmt_bin" -l \
  internal/modules/auth/service/email_unknown_restart_integration_test.go \
  internal/modules/auth/service/email_unknown_state_owner_linux_test.go \
  internal/modules/auth/service/email_unknown_state_owner_nonlinux_test.go)
(( ${#unformatted[@]} == 0 ))

stage=unit_test
assert_frozen_archive "$archive" "$expected_archive_sha"
assert_frozen_tool "$go_bin" "$go_sha"
test_pattern='^(TestEmailUnknownRestartStateVersion1Compatibility|TestEmailUnknownRestartStateReaderRejectsSymlinkBeforeRead|TestEmailUnknownRestartStateReaderRejectsOwnerMismatchBeforeRead|TestEmailUnknownRestartStateDecoderRejectsDuplicateAndUnknownFields|TestEmailUnknownRestartCleanupInvalidStateBlocksConnections|TestEmailUnknownRestartCleanupRejectsInvalidStateBeforeExternalAccess|TestEmailUnknownRestartCleanupRejectsExistingRedisKeyWithoutDatabaseWrite|TestEmailUnknownRestartCleanupRejectsOwnershipDriftBeforeDelete|TestEmailUnknownRestartCleanupRejectsEveryMissingDeleteRow|TestEmailUnknownRestartCleanupLaterFailureRollsBackLogicalTransaction|TestEmailUnknownRestartCleanupPostflightFailureRetainsState|TestEmailUnknownRestartCleanupFailureRetainsState|TestEmailUnknownRestartCleanupSuccessRemovesStateOnce|TestEmailUnknownRestartCleanupPredicatesCoverFrozenOwnership)$'
"$go_bin" test ./internal/modules/auth/service -run "$test_pattern" -count=1 -v >"${build_root}/go-test.log" 2>&1
test_count=$(/usr/bin/grep -c '^--- PASS: TestEmailUnknownRestart' "${build_root}/go-test.log")
[[ "$test_count" == 14 ]]
if /usr/bin/grep -Eq 'TestEmailUnknownTombstoneSurvivesRedisRestart|email_unknown_restart=SKIP' "${build_root}/go-test.log"; then
  false
fi

stage=vet
assert_frozen_archive "$archive" "$expected_archive_sha"
assert_frozen_tool "$go_bin" "$go_sha"
"$go_bin" vet ./internal/modules/auth/service >"${build_root}/go-vet.log" 2>&1

stage=build
assert_frozen_archive "$archive" "$expected_archive_sha"
assert_frozen_tool "$go_bin" "$go_sha"
"$go_bin" test -c ./internal/modules/auth/service -o "$binary" >"${build_root}/go-build.log" 2>&1
/usr/bin/chmod 500 -- "$binary"
[[ -f "$binary" && ! -L "$binary" ]]

stage=summary
binary_sha=$(/usr/bin/sha256sum -- "$binary" | /usr/bin/awk '{print $1}')
archive_sha=$(/usr/bin/sha256sum -- "$archive" | /usr/bin/awk '{print $1}')
IFS=: read -r binary_owner binary_uid binary_mode binary_size <<< "$(/usr/bin/stat -c '%U:%u:%a:%s' -- "$binary")"
[[ "$binary_sha" =~ ^[a-f0-9]{64}$ && "$binary_sha" != 0000000000000000000000000000000000000000000000000000000000000000 ]]
[[ "$archive_sha" =~ ^[a-f0-9]{64}$ ]]
[[ "$binary_owner" == pc && "$binary_mode" == 500 && "$binary_size" =~ ^[1-9][0-9]*$ ]]
trap - ERR
printf 'status=pass stage=complete binary_sha256=%s binary_size=%s\n' "$binary_sha" "$binary_size" >"${build_root}/status.tmp"
/usr/bin/chmod 600 -- "${build_root}/status.tmp"
/usr/bin/mv -T -- "${build_root}/status.tmp" "${build_root}/status.final"
printf 'complete\n' >"${build_root}/marker.tmp"
/usr/bin/chmod 600 -- "${build_root}/marker.tmp"
/usr/bin/mv -T -- "${build_root}/marker.tmp" "${build_root}/marker.final"
exit 0
