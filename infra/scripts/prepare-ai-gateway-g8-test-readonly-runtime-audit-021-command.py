#!/usr/bin/env python3
"""生成 021 无安装、无 sudo 的单次 Docker 只读审计命令。"""

import sys

# 在加载脚本目录可影响的模块前强制隔离解释器，避免本地模块劫持。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_RUNTIME_AUDIT_021_COMMAND=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import base64
import hashlib
import stat
from pathlib import Path, PureWindowsPath


CHANGE_ID = "CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-021"
SOURCE_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
CHANGE_ID_CONSUMED = False
REMOTE_EXECUTION_AUTHORIZED = False
AUDITOR_NAME = "audit-ai-gateway-g8-test-server-readonly.sh"
EXPECTED_AUDITOR_SHA256 = "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256"
EXPECTED_AUDITOR_SIZE = 18377
TRUSTED_PROFILE_RECEIPT = "__G8_TRUSTED_PROFILE_RECEIPT__"
REQUIRED_COLLECTION_KEYS = (
    "deploy_root",
    "git_head",
    "git_dirty_count",
    "api_process_count",
    "api_binary_sha256",
    "api_listener_count",
    "api_health_http",
    "api_ready_http",
    "env_file_meta",
    "docker_access",
    "docker_version",
    "mysql_version",
    "schema",
    "redis_version",
    "redis_ping",
    "rabbitmq_ping",
    "rabbitmq_queue_count",
    "rabbitmq_ready",
    "rabbitmq_unacked",
    "bifrost_container_count",
    "prometheus_ready_http",
    "prometheus_targets_total",
    "prometheus_targets_up",
    "prometheus_targets_down",
    "g8_alert_rule_count",
    "grafana_health_http",
    "g8_grafana_panel_count",
    "alertmanager_container_count",
    "alertmanager_config_sha256",
    "alertmanager_discard_configured",
    "reconcile_exit",
    "backup_latest_sha256",
    "backup_readable",
    "mysql_credential",
    "minio_credential",
    "rabbitmq_credential",
    "redis_credential",
)


class SafeArgumentParser(argparse.ArgumentParser):
    """把参数错误收敛为固定低敏结果，禁止回显调用方输入。"""

    def error(self, message: str) -> None:
        raise ValueError("invalid_request")


def sha256_bytes(content: bytes) -> str:
    """计算冻结字节的 SHA-256。"""
    return hashlib.sha256(content).hexdigest()


def is_safe_local_output_path(value: str) -> bool:
    """在文件系统探测前拒绝 UNC、设备名、ADS 和尾随点空格。"""
    if not value or value.startswith(("\\\\", "//")):
        return False
    try:
        if not Path(value).is_absolute():
            return False
        windows_path = PureWindowsPath(value)
        if windows_path.drive:
            reserved = {"CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$", "CLOCK$"}
            reserved.update(f"{prefix}{suffix}" for prefix in ("COM", "LPT") for suffix in "123456789¹²³")
            for component in windows_path.parts[1:]:
                if ":" in component or component != component.rstrip(" ."):
                    return False
                if component.split(".", 1)[0].upper() in reserved:
                    return False
        return True
    except (OSError, ValueError):
        return False


def read_frozen_auditor() -> bytes:
    """稳定读取既有只读审计源并核对冻结摘要。"""
    path = Path(__file__).with_name(AUDITOR_NAME)
    before = path.lstat()
    if not stat.S_ISREG(before.st_mode) or path.is_symlink():
        raise RuntimeError("invalid_auditor")
    content = path.read_bytes()
    after = path.stat()
    stable = (
        before.st_dev,
        before.st_ino,
        before.st_size,
        before.st_mtime_ns,
        before.st_ctime_ns,
    ) == (
        after.st_dev,
        after.st_ino,
        after.st_size,
        after.st_mtime_ns,
        after.st_ctime_ns,
    )
    if (
        not stable
        or len(content) != EXPECTED_AUDITOR_SIZE
        or sha256_bytes(content) != EXPECTED_AUDITOR_SHA256
        or not content.startswith(b"#!/usr/bin/env bash\n")
        or b"AUDIT_COMPLETE=true" not in content
    ):
        raise RuntimeError("invalid_auditor")
    return content


def replace_exact(source: str, old: str, new: str) -> str:
    """只允许替换唯一冻结片段，防止上游审计源静默漂移。"""
    if source.count(old) != 1:
        raise RuntimeError("invalid_auditor_transform")
    return source.replace(old, new)


def build_remote_audit(auditor: bytes) -> str:
    """把已评审审计源收窄为 pc 直连 Docker、无安装、无 sudo 的单次脚本。"""
    source = auditor.decode("utf-8")
    source = replace_exact(
        source,
        'readonly PRIVILEGED_INSTALL_PATH="/usr/local/libexec/molin/g8-test-readonly-audit"\n'
        'readonly PRIVILEGED_RECONCILE_PATH="/usr/local/libexec/molin/ai-gateway-reconcile"\n',
        "",
    )
    source = replace_exact(
        source,
        "# 固定可信命令搜索路径并清除 Bash 启动注入变量，避免 sudo 调用继承调用者控制的执行环境。",
        "# 固定可信命令搜索路径并清除 Bash 启动注入变量，避免调用者环境改变只读审计语义。",
    )
    old_arguments = r'''if (($# != 1)) || [[ "$1" != --change-id=* ]]; then
  printf 'invalid_arguments=true\n'
  exit 2
fi
readonly CHANGE_ID="${1#--change-id=}"
if [[ ! "$CHANGE_ID" =~ ^CHG-G8-TEST-READONLY-[0-9]{8}-[0-9]{3}$ ]]; then
  printf 'invalid_change_id=true\n'
  exit 2
fi
'''
    new_arguments = fr'''if (($# != 0)); then
  printf 'invalid_arguments=true\n'
  exit 2
fi
readonly CHANGE_ID="{CHANGE_ID}"
'''
    source = replace_exact(source, old_arguments, new_arguments)
    privileged = r'''# 特权执行只接受 root 拥有、固定权限和固定绝对路径的安装副本，避免 pc 替换脚本后借 sudo 提权。
if ((EUID == 0)); then
  installed_path="$(readlink -f -- "$0" 2>/dev/null || true)"
  installed_meta="$(stat -Lc '%U:%G:%a' -- "$PRIVILEGED_INSTALL_PATH" 2>/dev/null || true)"
  if [[ "$installed_path" != "$PRIVILEGED_INSTALL_PATH" || "$installed_meta" != "root:root:755" ]]; then
    printf 'privileged_installation=INVALID\n'
    exit 42
  fi
  printf 'privileged_installation=VERIFIED\n'
  unset G8_LEGACY_TEST_CREDENTIAL_SHA256 || true
fi

'''
    source = replace_exact(source, privileged, "")
    docker_probe = r'''docker_prefix=()
if docker info >/dev/null 2>&1; then
  docker_prefix=(docker)
elif sudo -n docker info >/dev/null 2>&1; then
  docker_prefix=(sudo -n docker)
fi
if ((${#docker_prefix[@]} == 0)); then
  unavailable docker_access
else
  printf 'docker_access=%s\n' "$((${#docker_prefix[@]} == 1))" | sed 's/1/direct/;s/0/sudo-n/'
  printf 'docker_version=%s\n' "$("${docker_prefix[@]}" version --format '{{.Server.Version}}' 2>/dev/null || printf UNAVAILABLE)"
  "${docker_prefix[@]}" ps --format 'container={{.Names}}|image={{.Image}}|status={{.Status}}|ports={{.Ports}}' 2>/dev/null | sort || true
fi
'''
    direct_probe = r'''if ! docker info >/dev/null 2>&1; then
  unavailable docker_access
  exit 42
fi
printf 'docker_access=direct\n'
printf 'docker_version=%s\n' "$(docker version --format '{{.Server.Version}}' 2>/dev/null || printf UNAVAILABLE)"
docker ps --format 'container={{.Names}}|image={{.Image}}|status={{.Status}}|ports={{.Ports}}' 2>/dev/null | sort || true
'''
    source = replace_exact(source, docker_probe, direct_probe)
    source = source.replace('"${docker_prefix[@]}"', "docker")
    source = source.replace('if ((${#docker_prefix[@]} > 0)) && docker inspect', "if docker inspect")
    source = source.replace('if ((${#docker_prefix[@]} > 0)); then', "if true; then")
    reconcile_select = r'''reconcile="$ROOT/ai-gateway-reconcile"
if ((EUID == 0)); then
  reconcile="$PRIVILEGED_RECONCILE_PATH"
  reconcile_meta="$(stat -Lc '%U:%G:%a' -- "$reconcile" 2>/dev/null || true)"
  if [[ "$reconcile_meta" != "root:root:755" ]]; then
    reconcile=""
  fi
fi
'''
    reconcile_direct = fr'''reconcile="/home/pc/molin/.g8-staging-{SOURCE_CHANGE_ID}/ai-gateway-reconcile"
reconcile_meta="$(stat -Lc '%U:%G:%a:%s' -- "$reconcile" 2>/dev/null || true)"
reconcile_sha="$(sha256sum "$reconcile" 2>/dev/null | awk '{{print $1}}' || true)"
if [[ "$reconcile_meta" != "pc:pc:700:13066129" || "$reconcile_sha" != "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1" ]]; then
  reconcile=""
fi
'''
    source = replace_exact(source, reconcile_select, reconcile_direct)
    sshd_probe = r'''if ((EUID == 0)); then
  ssh_password_auth="$(sshd -T 2>/dev/null | awk '$1=="passwordauthentication"{print $2; found=1} END{if(!found) print "UNAVAILABLE"}')"
else
  ssh_password_auth="$(sudo -n sshd -T 2>/dev/null | awk '$1=="passwordauthentication"{print $2; found=1} END{if(!found) print "UNAVAILABLE"}')"
fi
'''
    source = replace_exact(source, sshd_probe, "ssh_password_auth=UNAVAILABLE_NOT_AUTHORIZED\n")
    source = replace_exact(
        source,
        "printf 'AUDIT_MODE=READ_ONLY_SINGLE_SESSION\\n'\n",
        "printf 'AUDIT_MODE=READ_ONLY_SINGLE_SESSION\\n'\n"
        "[[ \"$(id -un 2>/dev/null || true)\" == pc ]] || { printf 'G8_TEST_READONLY_RUNTIME_AUDIT_021=FAILED reason=invalid_user\\n'; exit 42; }\n"
        "printf 'G8_TEST_READONLY_RUNTIME_AUDIT_021=PREFLIGHT_PASS\\n'\n",
    )
    identity_probe = r'''section IDENTITY
printf 'audit_invoker=%s\n' "${SUDO_USER:-$(id -un 2>/dev/null || printf UNAVAILABLE)}"
printf 'effective_user=%s\n' "$(id -un 2>/dev/null || printf UNAVAILABLE)"
printf 'hostname=%s\n' "$(hostname 2>/dev/null || printf UNAVAILABLE)"
if [[ -r /etc/machine-id ]]; then
  printf 'machine_id_sha256=%s\n' "$(sha256sum /etc/machine-id | awk '{print $1}')"
else
  unavailable machine_id_sha256
fi
printf 'passwd_status=%s\n' "$(passwd -S pc 2>/dev/null | awk '{print $2}' || printf UNAVAILABLE)"
if id -nG pc 2>/dev/null | tr ' ' '\n' | grep -Fxq docker; then
  printf 'pc_docker_group_member=true\n'
else
  printf 'pc_docker_group_member=false\n'
fi
'''
    source = replace_exact(source, identity_probe, "section DEPLOYMENT\n")
    source = replace_exact(
        source,
        r'''if GIT_OPTIONAL_LOCKS=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git -C "$ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf 'git_head=%s\n' "$(GIT_OPTIONAL_LOCKS=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git -C "$ROOT" rev-parse HEAD 2>/dev/null || printf UNAVAILABLE)"
  unavailable git_dirty_count_read_only_policy
else
  unavailable git_head
  unavailable git_dirty_count_read_only_policy
fi
''',
        r'''if GIT_OPTIONAL_LOCKS=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git -C "$ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf 'git_head=%s\n' "$(GIT_OPTIONAL_LOCKS=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git -C "$ROOT" rev-parse HEAD 2>/dev/null || printf UNAVAILABLE)"
  printf 'git_dirty_count=%s\n' "$(GIT_OPTIONAL_LOCKS=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git -C "$ROOT" status --porcelain --untracked-files=no 2>/dev/null | wc -l | tr -d ' ')"
else
  unavailable git_head
  unavailable git_dirty_count
fi
''',
    )
    source = replace_exact(
        source,
        r'''printf 'ssh_key_auth=UNVERIFIED_BY_AUDITOR\n'
ssh_password_auth=UNAVAILABLE_NOT_AUTHORIZED
printf 'ssh_password_auth_config=%s\n' "$ssh_password_auth"
printf 'ssh_password_state=%s\n' "$(passwd -S pc 2>/dev/null | awk '{print $2}' || printf UNAVAILABLE)"
''',
        "",
    )
    forbidden = (
        "sudo",
        "/usr/local/libexec",
        "docker run",
        "docker create",
        "docker start",
        "docker stop",
        "docker restart",
        "docker rm",
        "docker cp",
        "docker compose",
        "visudo",
        "chmod ",
        "chown ",
        "mkdir ",
        "rm -",
        "INSERT ",
        "UPDATE ",
        "DELETE ",
        "CREATE ",
        "ALTER ",
        "DROP ",
    )
    if any(value in source for value in forbidden):
        raise RuntimeError("invalid_auditor_capability")
    prefix = "#!/usr/bin/env bash\n# 本脚本仅采集测试服务器低敏聚合事实，不修改文件、服务、数据库、缓存或队列。\nset -uo pipefail\n"
    if not source.startswith(prefix):
        raise RuntimeError("invalid_auditor_transform")
    body = source[len(prefix) :]
    required_keys = " ".join(REQUIRED_COLLECTION_KEYS)
    return prefix + r'''

run_frozen_audit() {
''' + body + r'''
}

# 全部低敏证据只保存在本会话内存中；任一必需探针不可用时整次会话失败关闭。
audit_output="$(run_frozen_audit)" || {
  printf 'G8_TEST_READONLY_RUNTIME_AUDIT_021=FAILED reason=audit_evidence_failed\n'
  exit 42
}
printf '%s\n' "$audit_output"
if grep -Eq '=(UNAVAILABLE|MISSING|INVALID|000)$' <<<"$audit_output"; then
  printf 'G8_TEST_READONLY_RUNTIME_AUDIT_021=FAILED reason=audit_evidence_failed\n'
  exit 42
fi
if grep -Eq '^[A-Za-z0-9_]+=$' <<<"$audit_output"; then
  printf 'G8_TEST_READONLY_RUNTIME_AUDIT_021=FAILED reason=audit_evidence_failed\n'
  exit 42
fi
readonly required_collection_keys=''' + required_keys + r'''
for required_key in $required_collection_keys; do
  if ! grep -Eq "^${required_key}=.+$" <<<"$audit_output"; then
    printf 'G8_TEST_READONLY_RUNTIME_AUDIT_021=FAILED reason=audit_evidence_failed\n'
    exit 42
  fi
done
if ! grep -Fxq 'AUDIT_COMPLETE=true' <<<"$audit_output"; then
  printf 'G8_TEST_READONLY_RUNTIME_AUDIT_021=FAILED reason=audit_evidence_failed\n'
  exit 42
fi
printf 'G8_TEST_READONLY_RUNTIME_AUDIT_021=COLLECTION_PASS\n'
'''


def build_known_hosts_guard() -> str:
    """只接受固定端点唯一批准的 ED25519 key。"""
    return r'''$matches = @(& $sshKeygen -F '[8.130.9.163]:10003' -f $knownHosts 2>$null | Where-Object { $_ -and -not $_.StartsWith('#') })
if ($LASTEXITCODE -ne 0) { throw 'known_hosts_mismatch' }
$entries = @($matches | Where-Object { ($_ -split '\s+')[1] -eq 'ssh-ed25519' })
if ($entries.Count -ne 1) { throw 'known_hosts_mismatch' }
$targetParts = ($entries[0] -split '\s+')
if ($targetParts.Count -ne 3) { throw 'known_hosts_mismatch' }
$sha = [Security.Cryptography.SHA256]::Create()
try { $fingerprint = 'SHA256:' + [Convert]::ToBase64String($sha.ComputeHash([Convert]::FromBase64String($targetParts[2]))).TrimEnd('=') } finally { $sha.Dispose() }
if ($fingerprint -ne 'SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I') { throw 'known_hosts_mismatch' }'''


def receipt_setup(receipt_path: str) -> str:
    """生成 CreateNew 耐久低敏回执初始化段。"""
    if receipt_path == TRUSTED_PROFILE_RECEIPT:
        path_setup = """  $profile = [Environment]::GetFolderPath([Environment+SpecialFolder]::UserProfile)
  if ([string]::IsNullOrWhiteSpace($profile) -or $profile -notmatch '^[A-Za-z]:\\' -or $profile.StartsWith('\\')) { throw 'receipt_unavailable' }
  $profileItem = Get-Item -LiteralPath $profile -Force -ErrorAction Stop
  if (-not $profileItem.PSIsContainer -or ($profileItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'receipt_unavailable' }
  $g8ReceiptPath = Join-Path $profile '.g8-021-runtime-audit-receipt.txt'
"""
    else:
        path_setup = f"  $g8ReceiptPath = '{receipt_path.replace("'", "''")}'\n"
    return f"""$g8ReceiptStream = $null
$g8ReceiptWriter = $null
$g8ReceiptWritable = $true
try {{
{path_setup}  $g8ReceiptStream = [IO.File]::Open($g8ReceiptPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::Read)
  $g8ReceiptWriter = New-Object IO.StreamWriter($g8ReceiptStream, (New-Object Text.UTF8Encoding($false)))
}} catch {{
  if ($null -ne $g8ReceiptWriter) {{ try {{ $g8ReceiptWriter.Dispose() }} catch {{}} }}
  if ($null -ne $g8ReceiptStream) {{ try {{ $g8ReceiptStream.Dispose() }} catch {{}} }}
  [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_021_HOST_RESULT=FAILED reason=receipt_unavailable exit_code=2')
  $global:LASTEXITCODE = 2
  $ErrorActionPreference = $g8PreviousErrorActionPreferenceName
  return
}}
function Write-G8Receipt([string]$line) {{
  try {{
    $g8ReceiptWriter.WriteLine($line)
    $g8ReceiptWriter.Flush()
    $g8ReceiptStream.Flush($true)
    return $true
  }} catch {{ return $false }}
}}
if (-not (Write-G8Receipt 'G8_TEST_READONLY_ACCESS_021_RECEIPT=STARTED')) {{
  [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_021_HOST_RESULT=FAILED reason=receipt_unavailable exit_code=2')
  $global:LASTEXITCODE = 2
  try {{ $g8ReceiptWriter.Dispose() }} catch {{}}
  try {{ $g8ReceiptStream.Dispose() }} catch {{}}
  $ErrorActionPreference = $g8PreviousErrorActionPreferenceName
  return
}}
"""


def build_test_command(receipt_path: str, scenario: str, ssh_path: str | None) -> str:
    """生成仅供本地测试的假 SSH 段，正式 CLI 不暴露此入口。"""
    action = "  $g8FailureReason = 'test_failure'; throw 'test_failure'\n"
    if scenario == "fake_ssh":
        if ssh_path is None:
            raise ValueError("test_ssh_required")
        literal = ssh_path.replace("'", "''")
        action = f"""  [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_021_PRE_SSH_GATE=PASS')
  if (-not (Write-G8Receipt 'G8_TEST_READONLY_ACCESS_021_PRE_SSH_GATE=PASS')) {{ $g8ReceiptWritable = $false; $g8FailureReason = 'receipt_unavailable'; throw 'receipt_unavailable' }}
  [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_021_SSH_ATTEMPTED=YES')
  if (-not (Write-G8Receipt 'G8_TEST_READONLY_ACCESS_021_SSH_ATTEMPTED=YES')) {{ $g8ReceiptWritable = $false; $g8FailureReason = 'receipt_unavailable'; throw 'receipt_unavailable' }}
  & '{literal}'
  if ($LASTEXITCODE -ne 0) {{ throw 'ssh_session_failed' }}
  $g8LocalGatePassed = $true
"""
    return f"""$g8PreviousErrorActionPreferenceName = [string]$ErrorActionPreference
if (@('SilentlyContinue','Stop','Continue','Inquire','Ignore','Suspend') -cnotcontains $g8PreviousErrorActionPreferenceName) {{ $g8PreviousErrorActionPreferenceName = 'Continue' }}
$ErrorActionPreference = 'Stop'
{receipt_setup(receipt_path)}$g8LocalGatePassed = $false
$g8FailureReason = 'ssh_session_failed'
try {{
{action}}} catch {{
  [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_021_LOCAL_GATE=FAILED reason=' + $g8FailureReason)
  if ($g8ReceiptWritable -and -not (Write-G8Receipt ('G8_TEST_READONLY_ACCESS_021_LOCAL_GATE=FAILED reason=' + $g8FailureReason))) {{ $g8ReceiptWritable = $false }}
}} finally {{ $ErrorActionPreference = $g8PreviousErrorActionPreferenceName }}
if ($g8LocalGatePassed) {{ $g8HostExitCode=0; $g8HostResult='G8_TEST_READONLY_ACCESS_021_HOST_RESULT=PASS exit_code=0' }} else {{ $g8HostExitCode=2; $g8HostResult='G8_TEST_READONLY_ACCESS_021_HOST_RESULT=FAILED reason=' + $g8FailureReason + ' exit_code=2' }}
[Console]::Out.WriteLine($g8HostResult)
if ($g8ReceiptWritable -and -not (Write-G8Receipt $g8HostResult)) {{ $g8ReceiptWritable = $false }}
$global:LASTEXITCODE = $g8HostExitCode
try {{ $g8ReceiptWriter.Dispose() }} catch {{}}
try {{ $g8ReceiptStream.Dispose() }} catch {{}}
$global:LASTEXITCODE = $g8HostExitCode
"""


def build_command(
    auditor: bytes,
    *,
    receipt_path: str | None = None,
    test_scenario: str | None = None,
    test_ssh_path: str | None = None,
) -> str:
    """生成固定单会话 PowerShell 命令；仅构造字符串，不连接网络。"""
    if receipt_path is None:
        raise ValueError("receipt_required")
    if test_scenario not in (None, "fail_before_ssh", "fake_ssh"):
        raise ValueError("invalid_test_scenario")
    if test_scenario is not None:
        return build_test_command(receipt_path, test_scenario, test_ssh_path)
    remote = build_remote_audit(auditor)
    payload = base64.b64encode(remote.encode("utf-8")).decode("ascii")
    known_hosts_guard = build_known_hosts_guard()
    return fr"""# 021 尚未获得远端执行授权；本文件只可在清单转为 REMOTE_AUTHORIZED 后执行。
$g8PreviousErrorActionPreferenceName = [string]$ErrorActionPreference
if (@('SilentlyContinue','Stop','Continue','Inquire','Ignore','Suspend') -cnotcontains $g8PreviousErrorActionPreferenceName) {{ $g8PreviousErrorActionPreferenceName = 'Continue' }}
$ErrorActionPreference = 'Stop'
{receipt_setup(receipt_path)}$g8LocalGatePassed = $false
$g8FailureReason = 'trusted_windows_path_failed'
try {{
  $windowsRoot = [Environment]::GetFolderPath([Environment+SpecialFolder]::Windows)
  $profile = [Environment]::GetFolderPath([Environment+SpecialFolder]::UserProfile)
  foreach ($systemPath in @($windowsRoot,$profile)) {{
    if ([string]::IsNullOrWhiteSpace($systemPath) -or $systemPath -notmatch '^[A-Za-z]:\\' -or $systemPath.StartsWith('\\')) {{ throw 'trusted_windows_path_unavailable' }}
    $item = Get-Item -LiteralPath $systemPath -Force -ErrorAction Stop
    if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {{ throw 'trusted_windows_path_unavailable' }}
  }}
  $ssh = Join-Path $windowsRoot 'System32\OpenSSH\ssh.exe'
  $sshKeygen = Join-Path $windowsRoot 'System32\OpenSSH\ssh-keygen.exe'
  $knownHosts = Join-Path $profile '.ssh\known_hosts'
  $identity = Join-Path $profile '.ssh\id_ed25519'
  $identityPublic = Join-Path $profile '.ssh\id_ed25519.pub'
  foreach ($path in @($ssh,$sshKeygen,$knownHosts,$identity,$identityPublic)) {{
    $item = Get-Item -LiteralPath $path -Force -ErrorAction Stop
    if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {{ throw 'material_invalid' }}
  }}
  $g8FailureReason = 'known_hosts_failed'
  {known_hosts_guard}
  $g8FailureReason = 'identity_pair_failed'
  $derived = (& $sshKeygen -y -P '' -f $identity 2>$null)
  if ($LASTEXITCODE -ne 0) {{ throw 'identity_pair_mismatch' }}
  $declaredParts = ((Get-Content -LiteralPath $identityPublic -Raw -ErrorAction Stop).Trim() -split '\s+')
  $derivedParts = (($derived.Trim()) -split '\s+')
  if ($declaredParts.Count -lt 2 -or $derivedParts.Count -lt 2 -or $declaredParts[0] -ne 'ssh-ed25519' -or $declaredParts[0] -ne $derivedParts[0] -or $declaredParts[1] -ne $derivedParts[1]) {{ throw 'identity_pair_mismatch' }}
  $sha = [Security.Cryptography.SHA256]::Create()
  try {{ $clientFingerprint = 'SHA256:' + [Convert]::ToBase64String($sha.ComputeHash([Convert]::FromBase64String($declaredParts[1]))).TrimEnd('=') }} finally {{ $sha.Dispose() }}
  if ($clientFingerprint -ne 'SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0') {{ throw 'identity_pair_mismatch' }}
  $frozenKnownHosts = Join-Path $profile ('.g8-known-hosts-' + [Guid]::NewGuid().ToString('N'))
  $knownHostsStream = $null
  $created = $false
  try {{
    $knownHostsStream = [IO.File]::Open($frozenKnownHosts,[IO.FileMode]::CreateNew,[IO.FileAccess]::ReadWrite,[IO.FileShare]::Read)
    $created = $true
    $bytes = [Text.Encoding]::ASCII.GetBytes("[8.130.9.163]:10003 ssh-ed25519 $($targetParts[2])`n")
    $knownHostsStream.Write($bytes,0,$bytes.Length)
    $knownHostsStream.Flush($true)
    $remotePayload = '{payload}'
    $remoteCommand = "/usr/bin/printf '%s' '$remotePayload' | /usr/bin/base64 -d | /bin/bash"
    $g8FailureReason = 'ssh_session_failed'
    [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_021_PRE_SSH_GATE=PASS')
    if (-not (Write-G8Receipt 'G8_TEST_READONLY_ACCESS_021_PRE_SSH_GATE=PASS')) {{ $g8ReceiptWritable=$false; $g8FailureReason='receipt_unavailable'; throw 'receipt_unavailable' }}
    [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_021_SSH_ATTEMPTED=YES')
    if (-not (Write-G8Receipt 'G8_TEST_READONLY_ACCESS_021_SSH_ATTEMPTED=YES')) {{ $g8ReceiptWritable=$false; $g8FailureReason='receipt_unavailable'; throw 'receipt_unavailable' }}
    & $ssh `
      -F none -T `
      -p 10003 `
      -o BatchMode=yes -o IdentitiesOnly=yes -o ConnectionAttempts=1 -o ConnectTimeout=15 `
      -o PasswordAuthentication=no -o KbdInteractiveAuthentication=no -o NumberOfPasswordPrompts=0 `
      -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 `
      -o ForwardAgent=no -o ClearAllForwardings=yes -o RequestTTY=no `
      -o PermitLocalCommand=no -o LogLevel=QUIET `
      -o UserKnownHostsFile="$frozenKnownHosts" `
      -i "$identity" `
      pc@8.130.9.163 `
      $remoteCommand 2>$null
    if ($LASTEXITCODE -ne 0) {{ throw 'ssh_session_failed' }}
    $g8LocalGatePassed = $true
  }} finally {{
    if ($created) {{ try {{ $knownHostsStream.Dispose() }} finally {{ Remove-Item -LiteralPath $frozenKnownHosts -Force -ErrorAction SilentlyContinue }} }}
  }}
}} catch {{
  [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_021_LOCAL_GATE=FAILED reason=' + $g8FailureReason)
  if ($g8ReceiptWritable -and -not (Write-G8Receipt ('G8_TEST_READONLY_ACCESS_021_LOCAL_GATE=FAILED reason=' + $g8FailureReason))) {{ $g8ReceiptWritable=$false }}
}} finally {{ $ErrorActionPreference = $g8PreviousErrorActionPreferenceName }}
if ($g8LocalGatePassed) {{ $g8HostExitCode=0; $g8HostResult='G8_TEST_READONLY_ACCESS_021_HOST_RESULT=PASS exit_code=0' }} else {{ $g8HostExitCode=2; $g8HostResult='G8_TEST_READONLY_ACCESS_021_HOST_RESULT=FAILED reason=' + $g8FailureReason + ' exit_code=2' }}
[Console]::Out.WriteLine($g8HostResult)
if ($g8ReceiptWritable -and -not (Write-G8Receipt $g8HostResult)) {{ $g8ReceiptWritable=$false }}
$global:LASTEXITCODE=$g8HostExitCode
try {{ $g8ReceiptWriter.Dispose() }} catch {{}}
try {{ $g8ReceiptStream.Dispose() }} catch {{}}
$global:LASTEXITCODE=$g8HostExitCode
"""


def self_test(auditor: bytes) -> None:
    """离线验证固定能力边界，不读取身份材料或启动子进程。"""
    command = build_command(auditor, receipt_path=TRUSTED_PROFILE_RECEIPT)
    remote = build_remote_audit(auditor)
    required = ("BatchMode=yes", "ConnectionAttempts=1", "RequestTTY=no", "docker_access=direct", "AUDIT_COMPLETE=true", "COLLECTION_PASS")
    if any(value not in command + remote for value in required):
        raise RuntimeError("invalid_command")
    if "sudo" in command or "sudo" in remote or "docker run" in remote or "/usr/local/libexec" in remote:
        raise RuntimeError("invalid_command")


def main() -> int:
    """解析固定参数并只创建一个全新本地命令文件。"""
    if CHANGE_ID_CONSUMED:
        print("G8_TEST_READONLY_RUNTIME_AUDIT_021_COMMAND=FAILED reason=change_id_consumed")
        return 2
    parser = SafeArgumentParser(add_help=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--change-id")
    parser.add_argument("--output-file")
    try:
        arguments = parser.parse_args()
        auditor = read_frozen_auditor()
        if arguments.self_test:
            if arguments.change_id is not None or arguments.output_file is not None:
                raise ValueError("invalid_request")
            self_test(auditor)
            print("G8_TEST_READONLY_RUNTIME_AUDIT_021_COMMAND_SELF_TEST=PASS")
            return 0
        if arguments.change_id != CHANGE_ID or not is_safe_local_output_path(arguments.output_file or ""):
            raise ValueError("invalid_request")
        output = Path(arguments.output_file)
        if output.exists():
            raise ValueError("invalid_request")
        command = build_command(auditor, receipt_path=TRUSTED_PROFILE_RECEIPT)
        with output.open("x", encoding="utf-8", newline="\n") as handle:
            handle.write(command)
    except Exception:
        print("G8_TEST_READONLY_RUNTIME_AUDIT_021_COMMAND=FAILED reason=invalid_request")
        return 2
    print("G8_TEST_READONLY_RUNTIME_AUDIT_021_COMMAND=PASS")
    print(f"auditor_source_sha256={sha256_bytes(auditor)}")
    print(f"command_sha256={sha256_bytes(command.encode('utf-8'))}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
