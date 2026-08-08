param(
    [string]$ChangeId = "",
    [string]$OutputDirectory = "",
    [switch]$ExportCandidate,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
$FixedServerHost = "8.130.9.163"
$FixedSSHPort = 10003
$FixedSSHUser = "pc"
$LegacyKeys = @("SMS_ACCESS_KEY", "SMS_ACCESS_SECRET", "SMS_SIGN_NAME")

function Assert-LocalFileSystemPathInput {
    param([Parameter(Mandatory = $true)][string]$Path)

    # 生成阶段只能写入本机绝对路径，避免 UNC、Provider 路径或网络映射盘产生外部动作。
    if ([string]::IsNullOrWhiteSpace($Path) -or $Path -match '^(?:\\\\|//)' -or $Path.Contains("::")) {
        throw "候选输出目录必须是本地文件系统绝对路径"
    }
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        if ($Path -cnotmatch '^[A-Za-z]:[\\/]') { throw "Windows 候选目录必须使用本地盘符绝对路径" }
        $drive = Get-PSDrive -Name $Path.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
        if (([string]$drive.Root).StartsWith("\\") -or ([string]$drive.DisplayRoot).StartsWith("\\")) {
            throw "候选目录不得使用网络映射盘"
        }
    }
    elseif (-not [IO.Path]::IsPathRooted($Path)) {
        throw "候选目录必须使用本地绝对路径"
    }
}

function Assert-BashSyntax {
    param([Parameter(Mandatory = $true)][string]$Script)

    # 只把负载写入本机 Bash 标准输入执行 -n，不执行负载，也不创建临时脚本文件。
    $isWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
    if ($isWindowsPlatform) {
        # Windows System32 的 bash.exe 可能只是未安装 WSL 发行版的转发器；固定优先使用 Git Bash。
        $gitBash = "C:\Program Files\Git\bin\bash.exe"
        if (-not (Test-Path -LiteralPath $gitBash -PathType Leaf)) { throw "缺少 Bash，无法完成远端负载语法检查" }
        $bashPath = $gitBash
    }
    else {
        $bash = @(Get-Command bash -CommandType Application -All -ErrorAction SilentlyContinue) | Select-Object -First 1
        if ($null -eq $bash) { throw "缺少 Bash，无法完成远端负载语法检查" }
        $bashPath = $bash.Source
    }
    $utf8 = New-Object Text.UTF8Encoding($false)
    $bytes = $utf8.GetBytes($Script.Replace("`r`n", "`n").Replace("`r", "`n"))
    $startInfo = New-Object Diagnostics.ProcessStartInfo
    $startInfo.FileName = $bashPath
    $startInfo.Arguments = "-n"
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.CreateNoWindow = $true
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $startInfo
    try {
        if (-not $process.Start()) { throw "无法启动 Bash 语法检查进程" }
        try {
            $process.StandardInput.BaseStream.Write($bytes, 0, $bytes.Length)
            $process.StandardInput.BaseStream.Flush()
        }
        finally {
            [Array]::Clear($bytes, 0, $bytes.Length)
            $process.StandardInput.Close()
        }
        $stderr = $process.StandardError.ReadToEnd()
        $process.WaitForExit()
        if ($process.ExitCode -ne 0) { throw "远端负载 Bash 语法检查失败：$stderr" }
    }
    finally {
        $process.Dispose()
    }
}

if (-not $ExportCandidate -and -not $SelfTest) {
    Write-Output "legacy_cleanup_candidate_authorized=false"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExportCandidate -and $SelfTest) { throw "ExportCandidate 与 SelfTest 必须互斥" }

if ($SelfTest) {
    if ($FixedServerHost -cne "8.130.9.163" -or $FixedSSHPort -ne 10003 -or $FixedSSHUser -cne "pc") {
        throw "固定测试服 SSH 目标发生漂移"
    }
    if (($LegacyKeys -join ",") -cne "SMS_ACCESS_KEY,SMS_ACCESS_SECRET,SMS_SIGN_NAME") {
        throw "旧短信键精确集合发生漂移"
    }
    Write-Output "legacy_cleanup_candidate_self_test=passed"
    Write-Output "fixed_ssh_target_frozen=true"
    Write-Output "exact_legacy_key_set_frozen=true"
    Write-Output "automatic_rollback_contract=true"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ChangeId -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') { throw "ChangeId 必须使用 UTC 基本格式" }
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) { throw "导出候选必须提供全新的输出目录" }
Assert-LocalFileSystemPathInput -Path $OutputDirectory
$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
$outputParent = Split-Path -Parent $outputPath
if ([string]::IsNullOrWhiteSpace($outputParent) -or -not (Test-Path -LiteralPath $outputParent -PathType Container)) {
    throw "候选输出目录的父目录必须已存在"
}
if (Test-Path -LiteralPath $outputPath) { throw "候选输出目录已存在，禁止覆盖" }

# 远端负载只会在未来取得独立配置变更授权后运行；本轮仅冻结为本地默认关闭候选。
$remotePayload = @'
set -Eeuo pipefail
umask 077

if [ "${1:-}" = "--self-test" ]; then
  python3 - <<'PY'
import os
import re
import tempfile

legacy = {"SMS_ACCESS_KEY", "SMS_ACCESS_SECRET", "SMS_SIGN_NAME"}
source = "SMS_ENABLED=false\nSMS_TEST_MODE=true\nSMS_PROVIDER=aliyun\nSMS_ACCESS_KEY=old-a\nSMS_ACCESS_SECRET=old-b\nSMS_SIGN_NAME=old-c\nSMS_ALIYUN_ACCESS_KEY_ID=new-a\nSMS_ALIYUN_ACCESS_KEY_SECRET=new-b\nSMS_ALIYUN_SIGN_NAME=new-c\n"
kept = []
removed = 0
for line in source.splitlines(keepends=True):
    match = re.fullmatch(r"(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=.*(?:\n)?", line)
    if match and match.group(1) in legacy:
        removed += 1
    else:
        kept.append(line)
result = "".join(kept)
assert removed == 3
assert all((key + "=") not in result for key in legacy)
assert "SMS_ALIYUN_ACCESS_KEY_ID=new-a" in result
assert "SMS_ALIYUN_ACCESS_KEY_SECRET=new-b" in result
assert "SMS_ALIYUN_SIGN_NAME=new-c" in result
print("legacy_cleanup_payload_self_test=passed")
print("exact_legacy_keys_removed=true")
print("aliyun_keys_preserved=true")
print("network_connections=0")
print("configuration_mutations=0")
print("service_signals=0")
print("service_restarts=0")
print("business_posts=0")
print("emails_sent=0")
print("real_sms_sent=0")
PY
  exit 0
fi

change_id='__CHANGE_ID__'
api_path='/home/pc/molin/molin-api'
env_file='/home/pc/molin/infra/.env.test'
rollback_root='/home/pc/molin/rollback/sms-phase5/legacy-config-cleanup'
change_dir="$rollback_root/change-$change_id"
lock_dir='/home/pc/molin/rollback/sms-phase5/legacy-config-cleanup.lock'
alertmanager_config='/home/pc/molin-alertmanager-phase5/20260805T084215Z/alertmanager.closed.yml'
alertmanager_container='molin-alertmanager-phase5-closed'
alertmanager_port=19093

rollback_armed=false
lock_acquired=false
change_dir_created=false
original_pid=''
binary_sha256=''
backup_env=''
original_env_snapshot=''
clean_file=''
clean_process_env=''
launcher_path=''
service_stops=0
service_starts=0
configuration_mutations=0
failure_stage='none'

fail() {
  failure_stage="$1"
  printf 'legacy_cleanup=failed\n'
  printf 'failure_stage=%s\n' "$failure_stage"
  exit 2
}

api_pids() {
  pgrep -f "^${api_path}$" 2>/dev/null || true
}

wait_for_exit() {
  local pid="$1" index
  for index in $(seq 1 40); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.25
  done
  return 1
}

stop_exact_api() {
  local pid="$1"
  [ "$(tr '\0' ' ' < "/proc/${pid}/cmdline" 2>/dev/null | sed 's/ $//')" = "$api_path" ] || return 1
  [ "$(sha256sum "/proc/${pid}/exe" 2>/dev/null | awk '{print $1}')" = "$binary_sha256" ] || return 1
  kill -TERM "$pid"
  service_stops=$((service_stops + 1))
  if ! wait_for_exit "$pid"; then
    [ "$(tr '\0' ' ' < "/proc/${pid}/cmdline" 2>/dev/null | sed 's/ $//')" = "$api_path" ] || return 1
    [ "$(sha256sum "/proc/${pid}/exe" 2>/dev/null | awk '{print $1}')" = "$binary_sha256" ] || return 1
    kill -KILL "$pid"
    wait_for_exit "$pid" || return 1
  fi
}

write_launcher() {
  launcher_path="$change_dir/start-api.py"
  cat > "$launcher_path" <<'PY'
#!/usr/bin/env python3
import os
import sys

environment_path, binary_path = sys.argv[1:]
environment = {}
for item in open(environment_path, "rb").read().split(b"\0"):
    if not item:
        continue
    key, separator, value = item.partition(b"=")
    if not separator:
        raise SystemExit(2)
    environment[os.fsdecode(key)] = os.fsdecode(value)
os.execve(binary_path, [binary_path], environment)
PY
  chmod 700 "$launcher_path"
}

start_api() {
  local environment_path="$1" log_path="$2" pid
  nohup python3 "$launcher_path" "$environment_path" "$api_path" </dev/null >>"$log_path" 2>&1 &
  pid="$!"
  printf '%s' "$pid"
}

wait_for_closed_api() {
  local pid="$1" index
  for index in $(seq 1 40); do
    if kill -0 "$pid" 2>/dev/null &&
       [ "$(sha256sum "/proc/${pid}/exe" 2>/dev/null | awk '{print $1}')" = "$binary_sha256" ] &&
       tr '\0' '\n' < "/proc/${pid}/environ" | grep -qx 'SMS_ENABLED=false' &&
       tr '\0' '\n' < "/proc/${pid}/environ" | grep -qx 'SMS_TEST_MODE=true' &&
       [ "$(curl -sS --max-time 2 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/ready 2>/dev/null || true)" = 200 ]; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

verify_alertmanager_discard() {
  [ -f "$alertmanager_config" ] && [ ! -L "$alertmanager_config" ] || return 1
  python3 - "$alertmanager_config" <<'PY'
import re
import sys

text = open(sys.argv[1], encoding="utf-8").read()
route = re.search(r"(?ms)^route:\s*\n(?P<body>(?:^[ \t]+.*\n?)*)", text)
body = route.group("body") if route else ""
valid = re.search(r"(?m)^\s+receiver:\s*[\"']?discard[\"']?\s*$", body) and not re.search(r"(?m)^\s+routes:\s*$", body)
raise SystemExit(0 if valid else 2)
PY
  [ "$(docker inspect "$alertmanager_container" --format '{{.State.Running}}' 2>/dev/null)" = true ] || return 1
  [ "$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:${alertmanager_port}/-/ready" 2>/dev/null || true)" = 200 ]
}

verify_sms_environment() {
  local pid="$1" expected_legacy="$2"
  python3 - "$pid" "$env_file" "$expected_legacy" <<'PY'
import pathlib
import re
import sys

pid, env_path, expected_legacy = sys.argv[1:]
sms_keys = (
    "SMS_ENABLED", "SMS_TEST_MODE", "SMS_PROVIDER", "SMS_ALIYUN_ACCESS_KEY_ID",
    "SMS_ALIYUN_ACCESS_KEY_SECRET", "SMS_ALIYUN_SIGN_NAME", "SMS_ALIYUN_ENDPOINT",
    "SMS_PHONE_HMAC_SECRET", "SMS_TEST_PHONE_WHITELIST",
)
legacy_keys = ("SMS_ACCESS_KEY", "SMS_ACCESS_SECRET", "SMS_SIGN_NAME")

def process_environment():
    values, counts = {}, {}
    for item in pathlib.Path(f"/proc/{pid}/environ").read_bytes().split(b"\0"):
        if not item:
            continue
        key, separator, value = item.partition(b"=")
        if not separator:
            raise SystemExit(2)
        name = key.decode("utf-8", "strict")
        values[name] = value.decode("utf-8", "strict")
        counts[name] = counts.get(name, 0) + 1
    return values, counts

def file_environment():
    raw = pathlib.Path(env_path).read_bytes()
    if raw.startswith(b"\xef\xbb\xbf") or b"\r" in raw:
        raise SystemExit(2)
    values, counts = {}, {}
    for raw_line in raw.decode("utf-8", "strict").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        match = re.fullmatch(r"(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)", line)
        if match is None:
            raise SystemExit(2)
        key, value = match.groups()
        value = value.strip()
        if len(value) > 1 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        values[key] = value
        counts[key] = counts.get(key, 0) + 1
    return values, counts

def valid(values, counts):
    if any(counts.get(key, 0) != 1 for key in sms_keys):
        return False
    if values["SMS_ENABLED"].strip().lower() != "false" or values["SMS_TEST_MODE"].strip().lower() != "true":
        return False
    if values["SMS_PROVIDER"].strip().lower() != "aliyun":
        return False
    raw_endpoint = values["SMS_ALIYUN_ENDPOINT"]
    endpoint = "dysmsapi.aliyuncs.com" if raw_endpoint == "" else raw_endpoint.strip()
    hmac_secret = values["SMS_PHONE_HMAC_SECRET"]
    required = (
        values["SMS_ALIYUN_ACCESS_KEY_ID"], values["SMS_ALIYUN_ACCESS_KEY_SECRET"],
        values["SMS_ALIYUN_SIGN_NAME"], endpoint, hmac_secret,
    )
    if any(value.strip() == "" for value in required):
        return False
    if "://" in endpoint or "/" in endpoint or " " in endpoint or len(hmac_secret.encode("utf-8")) < 32:
        return False
    return any(item.strip() for item in values["SMS_TEST_PHONE_WHITELIST"].split(","))

process, process_counts = process_environment()
file_values, file_counts = file_environment()
if not valid(process, process_counts) or not valid(file_values, file_counts):
    raise SystemExit(2)
if any(process.get(key) != file_values.get(key) for key in sms_keys + legacy_keys):
    raise SystemExit(2)
process_legacy = {key for key in legacy_keys if key in process}
file_legacy = {key for key in legacy_keys if key in file_values}
if process_legacy != file_legacy:
    raise SystemExit(2)
if any(process_counts.get(key, 0) > 1 or file_counts.get(key, 0) > 1 for key in legacy_keys):
    raise SystemExit(2)
if expected_legacy == "present" and not process_legacy:
    raise SystemExit(2)
if expected_legacy == "absent" and process_legacy:
    raise SystemExit(2)
PY
}

make_clean_candidates() {
  python3 - "$env_file" "$clean_file" "$original_env_snapshot" "$clean_process_env" <<'PY'
import os
import re
import sys

source_file, target_file, source_process, target_process = sys.argv[1:]
legacy = {b"SMS_ACCESS_KEY", b"SMS_ACCESS_SECRET", b"SMS_SIGN_NAME"}

raw = open(source_file, "rb").read()
if raw.startswith(b"\xef\xbb\xbf") or b"\r" in raw:
    raise SystemExit(2)
kept_lines = []
removed_file = set()
for line in raw.splitlines(keepends=True):
    match = re.fullmatch(rb"(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=.*(?:\n)?", line)
    if match and match.group(1) in legacy:
        if match.group(1) in removed_file:
            raise SystemExit(2)
        removed_file.add(match.group(1))
    else:
        kept_lines.append(line)

items = open(source_process, "rb").read().split(b"\0")
kept_items = []
removed_process = set()
for item in items:
    if not item:
        continue
    key, separator, value = item.partition(b"=")
    if not separator:
        raise SystemExit(2)
    if key in legacy:
        if key in removed_process:
            raise SystemExit(2)
        removed_process.add(key)
    else:
        kept_items.append(item)
if not removed_file or removed_file != removed_process:
    raise SystemExit(2)

file_fd = os.open(target_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(file_fd, "wb") as stream:
    stream.write(b"".join(kept_lines))
process_fd = os.open(target_process, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(process_fd, "wb") as stream:
    stream.write(b"\0".join(kept_items) + b"\0")
PY
}

restore_original() {
  local current=() restored_pid stage
  mapfile -t current < <(api_pids)
  [ "${#current[@]}" -le 1 ] || return 1
  if [ "${#current[@]}" -eq 1 ]; then
    stop_exact_api "${current[0]}" || return 1
  fi
  stage="${env_file}.restore-${change_id}"
  rm -f -- "$stage"
  cp --no-dereference --preserve=mode,ownership,timestamps -- "$backup_env" "$stage" || return 1
  mv -fT -- "$stage" "$env_file" || return 1
  configuration_mutations=$((configuration_mutations + 1))
  restored_pid="$(start_api "$original_env_snapshot" "$change_dir/rollback-api.log")" || return 1
  service_starts=$((service_starts + 1))
  wait_for_closed_api "$restored_pid" || return 1
  verify_sms_environment "$restored_pid" present || return 1
  verify_alertmanager_discard || return 1
}

finalize_safe_materials() {
  # 成功或已恢复时删除全部敏感环境副本，但保留当前 API 正在写入的受控日志文件。
  rm -f -- "$backup_env" "$original_env_snapshot" "$clean_process_env" "$launcher_path" "$clean_file"
  rmdir -- "$lock_dir"
  lock_acquired=false
}

cleanup_unarmed_failure() {
  # 尚未替换环境文件时仅删除本 ChangeId 的精确暂存文件和空目录，不触碰业务配置。
  if [ "$change_dir_created" = true ]; then
    rm -f -- "$backup_env" "$original_env_snapshot" "$clean_file" "$clean_process_env" "$launcher_path" \
      "$change_dir/current-api.log" "$change_dir/rollback-api.log"
    rmdir -- "$change_dir" 2>/dev/null || true
    change_dir_created=false
  fi
  if [ "$lock_acquired" = true ]; then
    rmdir -- "$lock_dir" 2>/dev/null || true
    lock_acquired=false
  fi
}

handle_exit() {
  local status="$1" rollback_completed=false recovery_failed=false
  set +e
  trap '' INT TERM HUP
  if [ "$status" -ne 0 ] && [ "$rollback_armed" = true ]; then
    if restore_original; then
      rollback_completed=true
      rollback_armed=false
      finalize_safe_materials >/dev/null 2>&1 || true
    else
      recovery_failed=true
    fi
  elif [ "$status" -eq 0 ] && [ "$rollback_armed" = false ]; then
    finalize_safe_materials >/dev/null 2>&1 || status=4
  elif [ "$status" -ne 0 ] && [ "$rollback_armed" = false ]; then
    cleanup_unarmed_failure >/dev/null 2>&1 || true
  fi
  printf 'automatic_rollback_completed=%s\n' "$rollback_completed"
  printf 'recovery_failed=%s\n' "$recovery_failed"
  printf 'recovery_materials_retained=%s\n' "$recovery_failed"
  printf 'lock_retained=%s\n' "$recovery_failed"
  printf 'runtime_log_retained=%s\n' "$([ "$change_dir_created" = true ] && printf true || printf false)"
  printf 'service_stops=%s\n' "$service_stops"
  printf 'service_starts=%s\n' "$service_starts"
  printf 'configuration_mutations=%s\n' "$configuration_mutations"
  printf 'business_posts=0\n'
  printf 'emails_sent=0\n'
  printf 'sms_submission_requests=0\n'
  printf 'real_sms_sent=0\n'
  if [ "$recovery_failed" = true ]; then exit 5; fi
  exit "$status"
}

trap 'handle_exit $?' EXIT
trap 'exit 130' INT TERM HUP

mapfile -t initial_pids < <(api_pids)
[ "${#initial_pids[@]}" -eq 1 ] || fail api_process_identity
original_pid="${initial_pids[0]}"
[ -f "$api_path" ] && [ ! -L "$api_path" ] || fail api_binary_identity
binary_sha256="$(sha256sum "$api_path" | awk '{print $1}')"
[ "$(sha256sum "/proc/${original_pid}/exe" | awk '{print $1}')" = "$binary_sha256" ] || fail api_binary_hash
[ -f "$env_file" ] && [ ! -L "$env_file" ] || fail environment_identity
[ "$(stat -c '%U:%a' "$env_file")" = pc:600 ] || fail environment_permissions
wait_for_closed_api "$original_pid" || fail closed_api_ready
verify_sms_environment "$original_pid" present || fail legacy_config_preflight
verify_alertmanager_discard || fail alertmanager_discard

mkdir -p -- "$rollback_root"
[ "$(stat -c '%U:%a' "$rollback_root")" = pc:700 ] || fail rollback_root_permissions
mkdir -- "$lock_dir" || fail exclusive_lock
lock_acquired=true
chmod 700 "$lock_dir"
mkdir -- "$change_dir" || fail change_directory
change_dir_created=true
chmod 700 "$change_dir"

backup_env="$change_dir/original.env"
original_env_snapshot="$change_dir/original.environ"
clean_file="$change_dir/clean.env"
clean_process_env="$change_dir/clean.environ"
cp --no-dereference --preserve=mode,ownership,timestamps -- "$env_file" "$backup_env"
chmod 600 "$backup_env"
tr '\0' '\0' < "/proc/${original_pid}/environ" > "$original_env_snapshot"
chmod 600 "$original_env_snapshot"
make_clean_candidates || fail clean_candidate_generation
write_launcher
rollback_armed=true

mv -fT -- "$clean_file" "$env_file"
configuration_mutations=1
stop_exact_api "$original_pid" || fail api_stop
new_pid="$(start_api "$clean_process_env" "$change_dir/current-api.log")" || fail api_start
service_starts=$((service_starts + 1))
wait_for_closed_api "$new_pid" || fail closed_api_ready_after_cleanup
verify_sms_environment "$new_pid" absent || fail legacy_config_removed
verify_alertmanager_discard || fail alertmanager_discard_after_cleanup
sleep 10
wait_for_closed_api "$new_pid" || fail closed_api_stability
verify_sms_environment "$new_pid" absent || fail legacy_config_stability
verify_alertmanager_discard || fail alertmanager_stability

rollback_armed=false
printf 'legacy_cleanup=passed\n'
printf 'change_id=%s\n' "$change_id"
printf 'sms_enabled=false\n'
printf 'sms_test_mode=true\n'
printf 'exact_legacy_keys_absent=true\n'
printf 'aliyun_keys_preserved=true\n'
printf 'file_process_sms_config_parity=true\n'
printf 'current_closed_api_ready=true\n'
printf 'closed_state_stability_verified=true\n'
printf 'alertmanager_discard=true\n'
printf 'automatic_rollback_protected=true\n'
'@
$payloadText = $remotePayload.Replace("__CHANGE_ID__", $ChangeId)
$payloadBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($payloadText))
$sshHelperPath = Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1"
$sshHelperSHA256 = (Get-FileHash -LiteralPath $sshHelperPath -Algorithm SHA256).Hash.ToLowerInvariant()
$repoScripts = [IO.Path]::GetFullPath($PSScriptRoot)

$runnerTemplate = @'
param([switch]$ExecuteChange, [switch]$SelfTest, [string]$ExpectedRunnerSHA256 = "")
$ErrorActionPreference = "Stop"
$ChangeId = "__CHANGE_ID__"
$RemotePayloadBase64 = "__PAYLOAD_BASE64__"
$ExpectedSSHHelperSHA256 = "__SSH_HELPER_SHA256__"
$ResultPath = Join-Path (Split-Path -Parent $PSCommandPath) "result-$ChangeId.txt"

if (-not $ExecuteChange -and -not $SelfTest) {
    Write-Output "legacy_cleanup_execution_authorized=false"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExecuteChange -and $SelfTest) { throw "ExecuteChange 与 SelfTest 必须互斥" }

if ($SelfTest) {
    $payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64))
    foreach ($marker in @("SMS_ACCESS_KEY", "SMS_ACCESS_SECRET", "SMS_SIGN_NAME", "restore_original", "rollback_armed=true", "SMS_ENABLED=false", "real_sms_sent=0")) {
        if (-not $payload.Contains($marker)) { throw "旧键清理 runner 缺少安全标记：$marker" }
    }
    Write-Output "legacy_cleanup_runner_self_test=passed"
    Write-Output "exact_legacy_key_set_frozen=true"
    Write-Output "automatic_rollback_contract=true"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ExpectedRunnerSHA256 -cnotmatch '^[0-9a-f]{64}$') { throw "配置变更执行必须提供获批的完整 runner SHA-256" }
$actualRunnerSHA256 = (Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualRunnerSHA256 -cne $ExpectedRunnerSHA256) { throw "runner SHA-256 与批准值不匹配" }
if (Test-Path -LiteralPath $ResultPath) { throw "低敏结果文件已存在，禁止重复执行" }

$sshHelperPath = Join-Path "__REPO_SCRIPTS__" "sms-phase5-test-server-ssh.ps1"
$actualSSHHelperSHA256 = (Get-FileHash -LiteralPath $sshHelperPath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualSSHHelperSHA256 -cne $ExpectedSSHHelperSHA256) { throw "固定 SSH 身份辅助脚本摘要不匹配" }
. $sshHelperPath
$knownHosts = Assert-SmsPhase5FixedTestServerIdentity -ServerHost '8.130.9.163' -SSHPort 10003 -SSHUser 'pc'

$utf8 = New-Object Text.UTF8Encoding($false)
$payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64))
$inputBytes = $utf8.GetBytes($payload.Replace("`r`n", "`n").Replace("`r", "`n"))
$startInfo = New-Object Diagnostics.ProcessStartInfo
$startInfo.FileName = "ssh.exe"
$startInfo.UseShellExecute = $false
$startInfo.RedirectStandardInput = $true
$startInfo.RedirectStandardOutput = $true
$startInfo.RedirectStandardError = $true
$startInfo.CreateNoWindow = $true
$startInfo.StandardOutputEncoding = $utf8
$startInfo.StandardErrorEncoding = $utf8
$startInfo.Arguments = "-p 10003 -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 -o `"UserKnownHostsFile=$knownHosts`" -- pc@8.130.9.163 bash -s"
$process = New-Object Diagnostics.Process
$process.StartInfo = $startInfo
try {
    if (-not $process.Start()) { throw "无法启动固定 SSH 配置变更进程" }
    $stdoutTask = $process.StandardOutput.ReadToEndAsync()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    try {
        $process.StandardInput.BaseStream.Write($inputBytes, 0, $inputBytes.Length)
        $process.StandardInput.BaseStream.Flush()
    }
    finally {
        [Array]::Clear($inputBytes, 0, $inputBytes.Length)
        $process.StandardInput.Close()
    }
    $process.WaitForExit()
    $stdout = $stdoutTask.Result
    $stderr = $stderrTask.Result
    $remoteExitCode = $process.ExitCode
}
finally {
    $process.Dispose()
    $payload = $null
    $inputBytes = $null
}

$safeKeys = @(
    "legacy_cleanup", "failure_stage", "change_id", "sms_enabled", "sms_test_mode",
    "exact_legacy_keys_absent", "aliyun_keys_preserved", "file_process_sms_config_parity",
    "current_closed_api_ready", "closed_state_stability_verified", "alertmanager_discard", "automatic_rollback_protected",
    "automatic_rollback_completed", "recovery_failed", "recovery_materials_retained", "lock_retained",
    "runtime_log_retained", "service_stops", "service_starts", "configuration_mutations", "business_posts", "emails_sent",
    "sms_submission_requests", "real_sms_sent"
)
$safeLines = @()
foreach ($line in @($stdout -split "`r?`n" | Where-Object { $_ -ne "" })) {
    if ($line -cnotmatch '^(?<key>[a-z][a-z0-9_]*)=[A-Za-z0-9_.:,-]+$' -or $safeKeys -cnotcontains $Matches['key']) {
        throw "远端输出不符合低敏字段白名单"
    }
    $safeLines += $line
}
if ($safeLines -cnotcontains "business_posts=0" -or $safeLines -cnotcontains "emails_sent=0" -or
    $safeLines -cnotcontains "sms_submission_requests=0" -or $safeLines -cnotcontains "real_sms_sent=0") {
    throw "远端零业务副作用证据不完整"
}
$safeLines += "network_connections=1"
$safeLines += "uploads=0"
$safeLines += "remote_stderr_present=$(((-not [string]::IsNullOrWhiteSpace($stderr))).ToString().ToLowerInvariant())"
$safeLines += "change_exit_code=$remoteExitCode"
$content = ($safeLines -join "`r`n") + "`r`n"
$bytes = [Text.Encoding]::UTF8.GetBytes($content)
$stream = [IO.File]::Open($ResultPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
try { $stream.Write($bytes, 0, $bytes.Length) }
finally { $stream.Dispose(); [Array]::Clear($bytes, 0, $bytes.Length) }
$safeLines | Write-Output
Write-Output "network_connections=1"
Write-Output "uploads=0"
Write-Output "remote_stderr_present=$(((-not [string]::IsNullOrWhiteSpace($stderr))).ToString().ToLowerInvariant())"
Write-Output "change_exit_code=$remoteExitCode"
Write-Output "low_sensitivity_result_persisted=true"
Write-Output "result_sha256=$((Get-FileHash -LiteralPath $ResultPath -Algorithm SHA256).Hash.ToLowerInvariant())"
if ($remoteExitCode -ne 0) { throw "固定测试服旧短信键精确清理未通过，退出码：$remoteExitCode" }
'@

$runnerText = $runnerTemplate.Replace("__CHANGE_ID__", $ChangeId).
    Replace("__PAYLOAD_BASE64__", $payloadBase64).
    Replace("__SSH_HELPER_SHA256__", $sshHelperSHA256).
    Replace("__REPO_SCRIPTS__", $repoScripts)
$runnerPath = Join-Path $outputPath "run-sms-phase5-legacy-config-cleanup-$ChangeId.ps1"
$directoryCreated = $false
$fileCreated = $false
try {
    $null = New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop
    $directoryCreated = $true
    [IO.File]::WriteAllText($runnerPath, $runnerText, (New-Object Text.UTF8Encoding($true)))
    $fileCreated = $true

    # 本地仅验证语法、默认关闭、自测和精确变更边界，不进入 ExecuteChange 分支。
    $tokens = $null
    $parseErrors = $null
    $null = [Management.Automation.Language.Parser]::ParseFile($runnerPath, [ref]$tokens, [ref]$parseErrors)
    if (@($parseErrors).Count -ne 0) { throw "runner PowerShell 语法校验失败" }
    $decodedPayload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($payloadBase64))
    foreach ($marker in @("SMS_ACCESS_KEY", "SMS_ACCESS_SECRET", "SMS_SIGN_NAME", "restore_original", "rollback_armed=true", "SMS_ENABLED=false")) {
        if (-not $decodedPayload.Contains($marker)) { throw "远端负载缺少精确清理标记：$marker" }
    }
    foreach ($forbidden in @("SMS_ENABLED=true", "curl -X POST", "INSERT ", "UPDATE ", "DELETE ", "scp ", "sftp ", "send_scene")) {
        if ($decodedPayload.Contains($forbidden)) { throw "远端负载包含禁止动作：$forbidden" }
    }
    Assert-BashSyntax -Script $decodedPayload
    $closedOutput = @(& $runnerPath)
    $selfTestOutput = @(& $runnerPath -SelfTest)
    if ($closedOutput -cnotcontains "legacy_cleanup_execution_authorized=false" -or
        $selfTestOutput -cnotcontains "legacy_cleanup_runner_self_test=passed") {
        throw "runner 默认关闭或离线自测失败"
    }

    $runnerSHA256 = (Get-FileHash -LiteralPath $runnerPath -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Output "legacy_cleanup_candidate=passed"
    Write-Output "change_id=$ChangeId"
    Write-Output "runner_sha256=$runnerSHA256"
    Write-Output "runner_path=$runnerPath"
    Write-Output "candidate_files_written=1"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
}
catch {
    if ($fileCreated -and (Test-Path -LiteralPath $runnerPath -PathType Leaf)) {
        Remove-Item -LiteralPath $runnerPath -Force
    }
    if ($directoryCreated -and (Test-Path -LiteralPath $outputPath -PathType Container) -and
        @(Get-ChildItem -LiteralPath $outputPath -Force).Count -eq 0) {
        Remove-Item -LiteralPath $outputPath -Force
    }
    throw
}
