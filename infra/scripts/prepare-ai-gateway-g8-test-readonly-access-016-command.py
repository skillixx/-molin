#!/usr/bin/env python3
"""生成 016 单次交互安装会话的冻结命令；本工具本身不连接测试服。"""

import sys

# 在加载脚本目录可影响的模块前强制隔离解释器，避免本地模块劫持。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_ACCESS_016_COMMAND=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import base64
import hashlib
import stat
from pathlib import Path


CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-016"
SOURCE_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
CHANGE_ID_CONSUMED = False
# 该常量只记录冻结清单状态；生成低敏命令不等于获得远端执行授权。
REMOTE_EXECUTION_AUTHORIZED = False
ROOT_COPY = "/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-016"
INSTALLER_NAME = "g8-test-readonly-access-install-016.sh"
EXPECTED_INSTALLER_SHA256 = "dee24046f11de7ba12994b3c93a68c28b5505f73b9dc6085a025f4ea790be85c"
EXPECTED_INSTALLER_SIZE = 9465


class SafeArgumentParser(argparse.ArgumentParser):
    """把所有参数错误收敛为固定低敏结果，禁止回显调用方输入。"""

    def error(self, message: str) -> None:
        raise ValueError("invalid_request")


def sha256_bytes(content: bytes) -> str:
    """只对仓库冻结字节计算 SHA-256。"""
    return hashlib.sha256(content).hexdigest()


def read_frozen_installer() -> bytes:
    """读取一次稳定的普通安装器文件，并严格核对冻结大小与摘要。"""
    installer_path = Path(__file__).with_name(INSTALLER_NAME)
    before = installer_path.lstat()
    if not stat.S_ISREG(before.st_mode) or installer_path.is_symlink():
        raise RuntimeError("invalid_installer")
    installer = installer_path.read_bytes()
    after = installer_path.stat()
    stable_identity = (
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
        not stable_identity
        or len(installer) != EXPECTED_INSTALLER_SIZE
        or sha256_bytes(installer) != EXPECTED_INSTALLER_SHA256
        or not installer.startswith(b"#!/bin/bash\n")
    ):
        raise RuntimeError("invalid_installer")
    return installer


def build_known_hosts_guard() -> str:
    """使用固定 ssh-keygen 枚举明文与哈希端点，只接受唯一批准的 ED25519 key。"""
    return r'''$foundHostMatches = @(& $sshKeygen -F '[8.130.9.163]:10003' -f $knownHosts 2>$null | Where-Object {
  $_ -and -not $_.StartsWith('#')
})
if ($LASTEXITCODE -ne 0) { throw 'known_hosts_mismatch' }
$foundHostEntries = @($foundHostMatches | Where-Object { ($_ -split '\s+')[1] -eq 'ssh-ed25519' })
if ($foundHostEntries.Count -ne 1) { throw 'known_hosts_mismatch' }
$targetParts = ($foundHostEntries[0] -split '\s+')
if ($targetParts.Count -ne 3) { throw 'known_hosts_mismatch' }
$sha = [Security.Cryptography.SHA256]::Create()
try {
  $targetFingerprint = 'SHA256:' + [Convert]::ToBase64String($sha.ComputeHash([Convert]::FromBase64String($targetParts[2]))).TrimEnd('=')
} finally { $sha.Dispose() }
if ($targetFingerprint -ne 'SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I') {
  throw 'known_hosts_mismatch'
}'''


def build_command(installer: bytes) -> str:
    """生成不包含密码或凭据正文的 PowerShell 连接段和远端固定安装段。"""
    encoded = base64.b64encode(installer).decode("ascii")
    digest = sha256_bytes(installer)
    size = len(installer)
    known_hosts_guard = build_known_hosts_guard()
    return f"""# 016 尚未获得远端执行授权。本文件只可在清单转为 REMOTE_AUTHORIZED 后按两段人工执行。
# 第一步：在 PowerShell 中从 Windows API 获取可信系统目录，校验固定身份材料并建立唯一 SSH 会话。
$g8PreviousErrorActionPreference = $ErrorActionPreference
$ErrorActionPreference = 'Stop'
try {{
$windowsRoot = [Environment]::GetFolderPath([Environment+SpecialFolder]::Windows)
$programData = [Environment]::GetFolderPath([Environment+SpecialFolder]::CommonApplicationData)
$profileRoot = [Environment]::GetFolderPath([Environment+SpecialFolder]::UserProfile)
foreach ($systemPath in @($windowsRoot, $programData, $profileRoot)) {{
  if ([string]::IsNullOrWhiteSpace($systemPath) -or $systemPath -notmatch '^[A-Za-z]:\\\\' -or $systemPath.StartsWith('\\')) {{
    throw 'trusted_windows_path_unavailable'
  }}
  if ([IO.Path]::GetFullPath($systemPath).TrimEnd('\\') -cne $systemPath.TrimEnd('\\')) {{ throw 'trusted_windows_path_unavailable' }}
  $systemItem = Get-Item -LiteralPath $systemPath -Force -ErrorAction Stop
  if (-not $systemItem.PSIsContainer -or ($systemItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {{
    throw 'trusted_windows_path_unavailable'
  }}
}}
$ssh = Join-Path $windowsRoot 'System32\\OpenSSH\\ssh.exe'
$sshKeygen = Join-Path $windowsRoot 'System32\\OpenSSH\\ssh-keygen.exe'
$knownHosts = Join-Path $profileRoot '.ssh\\known_hosts'
$identity = Join-Path $profileRoot '.ssh\\id_ed25519'
$identityPublic = Join-Path $profileRoot '.ssh\\id_ed25519.pub'
function Get-FrozenMaterialEvidence([string]$path) {{
  $item = Get-Item -LiteralPath $path -Force -ErrorAction Stop
  if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {{ throw 'identity_material_invalid' }}
  return "$($item.Length):$($item.CreationTimeUtc.Ticks):$($item.LastWriteTimeUtc.Ticks):$((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash)"
}}
$previousSystemRoot = $env:SystemRoot
$previousProgramData = $env:ProgramData
try {{
  $env:SystemRoot = $windowsRoot
  $env:ProgramData = $programData
  $materialEvidence = @{{}}
  foreach ($path in @($ssh, $sshKeygen, $knownHosts, $identity, $identityPublic)) {{
    $materialEvidence[$path] = Get-FrozenMaterialEvidence $path
  }}
  {known_hosts_guard}
  $derivedPublic = (& $sshKeygen -y -f $identity 2>$null)
  if ($LASTEXITCODE -ne 0) {{ throw 'identity_pair_mismatch' }}
  $declaredParts = ((Get-Content -LiteralPath $identityPublic -Raw -ErrorAction Stop).Trim() -split '\\s+')
  $derivedParts = (($derivedPublic.Trim()) -split '\\s+')
  if ($declaredParts.Count -lt 2 -or $derivedParts.Count -lt 2 -or $declaredParts[0] -ne 'ssh-ed25519' -or $declaredParts[0] -ne $derivedParts[0] -or $declaredParts[1] -ne $derivedParts[1]) {{
    throw 'identity_pair_mismatch'
  }}
  $sha = [Security.Cryptography.SHA256]::Create()
  try {{
    $clientFingerprint = 'SHA256:' + [Convert]::ToBase64String($sha.ComputeHash([Convert]::FromBase64String($declaredParts[1]))).TrimEnd('=')
  }} finally {{ $sha.Dispose() }}
  if ($clientFingerprint -ne 'SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0') {{ throw 'identity_pair_mismatch' }}
  foreach ($path in $materialEvidence.Keys) {{
    if ((Get-FrozenMaterialEvidence $path) -ne $materialEvidence[$path]) {{ throw 'identity_material_drift' }}
  }}
  $frozenKnownHosts = [IO.Path]::GetTempFileName()
  try {{
    [IO.File]::WriteAllText($frozenKnownHosts, "[8.130.9.163]:10003 ssh-ed25519 $($targetParts[2])`n", [Text.Encoding]::ASCII)
    & $ssh `
      -F none -tt -p 10003 `
      -o BatchMode=yes -o IdentitiesOnly=yes -o ConnectionAttempts=1 -o ConnectTimeout=15 `
      -o PasswordAuthentication=no -o KbdInteractiveAuthentication=no -o NumberOfPasswordPrompts=0 `
      -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 `
      -o ForwardAgent=no -o ClearAllForwardings=yes -o RequestTTY=force `
      -o PermitLocalCommand=no -o LogLevel=ERROR `
      -o UserKnownHostsFile="$frozenKnownHosts" `
      -i "$identity" `
      pc@8.130.9.163
    if ($LASTEXITCODE -ne 0) {{ throw 'ssh_session_failed' }}
  }} finally {{ Remove-Item -LiteralPath $frozenKnownHosts -Force -ErrorAction SilentlyContinue }}
}} finally {{
  if ($null -eq $previousSystemRoot) {{ Remove-Item Env:SystemRoot -ErrorAction SilentlyContinue }} else {{ $env:SystemRoot = $previousSystemRoot }}
  if ($null -eq $previousProgramData) {{ Remove-Item Env:ProgramData -ErrorAction SilentlyContinue }} else {{ $env:ProgramData = $previousProgramData }}
}}
}} finally {{
  $ErrorActionPreference = $g8PreviousErrorActionPreference
}}

# 第二步：仅在上述 SSH 会话内完整粘贴本段；外层 here-doc 先完整收集脚本，再由非交互 Bash 失败关闭执行。
/bin/bash -s <<'G8_016_REMOTE'
set -euo pipefail
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
unset BASH_ENV ENV CDPATH PYTHONPATH PYTHONHOME
unalias sudo 2>/dev/null || :
unset -f sudo 2>/dev/null || :
auth_change_id={CHANGE_ID}
source_change_id={SOURCE_CHANGE_ID}
staging=/home/pc/molin/.g8-staging-{SOURCE_CHANGE_ID}
root_copy={ROOT_COPY}
/usr/bin/test "$(/usr/bin/id -un)" = pc
/usr/bin/test "$auth_change_id" = 'CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-016'
/usr/bin/test "$(/usr/bin/realpath -e /home/pc/molin)" = /home/pc/molin
/usr/bin/test "$(/usr/bin/realpath -e "$staging")" = "$staging"
/usr/bin/test "$(/usr/bin/stat -c '%U:%G:%a' -- "$staging")" = pc:pc:700
actual=$(/usr/bin/find "$staging" -mindepth 1 -maxdepth 1 -printf '%f\\n' | /usr/bin/sort | /usr/bin/tr '\\n' ' ')
/usr/bin/test "$actual" = 'SHA256SUMS ai-gateway-reconcile g8-test-readonly-audit manifest.env molin-g8-test-readonly-audit.sudoers '
for entry in SHA256SUMS:600 ai-gateway-reconcile:700 g8-test-readonly-audit:700 manifest.env:600 molin-g8-test-readonly-audit.sudoers:600; do
  name=${{entry%%:*}}; mode=${{entry##*:}}; path="$staging/$name"
  /usr/bin/test -f "$path"; /usr/bin/test ! -L "$path"
  /usr/bin/test "$(/usr/bin/stat -c '%U:%G:%a' -- "$path")" = "pc:pc:$mode"
done
/usr/bin/test "$(/usr/bin/sha256sum "$staging/SHA256SUMS" | /usr/bin/cut -d' ' -f1)" = 15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f
(cd "$staging" && /usr/bin/sha256sum -c SHA256SUMS >/dev/null 2>&1)
/usr/bin/test "$(/usr/bin/sha256sum "$staging/g8-test-readonly-audit" | /usr/bin/cut -d' ' -f1)" = 308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256
/usr/bin/test "$(/usr/bin/sha256sum "$staging/molin-g8-test-readonly-audit.sudoers" | /usr/bin/cut -d' ' -f1)" = 1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f
/usr/bin/test "$(/usr/bin/sha256sum "$staging/ai-gateway-reconcile" | /usr/bin/cut -d' ' -f1)" = 37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1
/usr/bin/test "$(/usr/bin/stat -c '%s' -- "$staging/ai-gateway-reconcile")" = 13066129
/usr/bin/grep -Fqx "CHANGE_ID=$source_change_id"$'\\r' "$staging/manifest.env"
/usr/bin/grep -Fqx 'TARGET_TRANSPORT=DROP_SSH_INTERACTIVE_SUDO'$'\\r' "$staging/manifest.env"
/usr/bin/grep -Fqx 'PHYSICAL_HOST_IDENTITY=NOT_APPLICABLE'$'\\r' "$staging/manifest.env"
for parent in /home /home/pc /home/pc/molin; do
  /usr/bin/test -d "$parent"; /usr/bin/test ! -L "$parent"
  mode=$((8#$(/usr/bin/stat -c '%a' -- "$parent"))); /usr/bin/test $((mode & 0022)) -eq 0
done
/usr/bin/test "$(/usr/bin/stat -c '%U:%G' -- /home)" = root:root
/usr/bin/test "$(/usr/bin/stat -c '%U:%G' -- /home/pc)" = pc:pc
/usr/bin/test "$(/usr/bin/stat -c '%U:%G' -- /home/pc/molin)" = pc:pc
for parent in /usr /usr/local /usr/local/libexec /etc /etc/sudoers.d; do
  /usr/bin/test -d "$parent"; /usr/bin/test ! -L "$parent"
  /usr/bin/test "$(/usr/bin/stat -c '%U:%G' -- "$parent")" = root:root
  mode=$((8#$(/usr/bin/stat -c '%a' -- "$parent"))); /usr/bin/test $((mode & 0022)) -eq 0
done
if /usr/bin/test -e /usr/local/libexec/molin || /usr/bin/test -L /usr/local/libexec/molin; then
  /usr/bin/test -d /usr/local/libexec/molin; /usr/bin/test ! -L /usr/local/libexec/molin
  /usr/bin/test "$(/usr/bin/stat -c '%U:%G' -- /usr/local/libexec/molin)" = root:root
  mode=$((8#$(/usr/bin/stat -c '%a' -- /usr/local/libexec/molin))); /usr/bin/test $((mode & 0022)) -eq 0
fi
for target in /usr/local/libexec/molin/g8-test-readonly-audit /usr/local/libexec/molin/ai-gateway-reconcile /etc/sudoers.d/molin-g8-test-readonly-audit "$root_copy"; do
  /usr/bin/test ! -e "$target"; /usr/bin/test ! -L "$target"
done
if /usr/bin/sudo -n -l >/dev/null 2>&1; then exit 1; fi
/usr/bin/printf '%s\\n' 'G8_TEST_READONLY_ACCESS_PREFLIGHT_016=PASS'
/usr/bin/sudo -k -v
/usr/bin/sudo -n /bin/bash -ceu 'PATH=/usr/sbin:/usr/bin:/sbin:/bin; export PATH; unset BASH_ENV ENV CDPATH PYTHONPATH PYTHONHOME; umask 077; root={ROOT_COPY}; target=$root/{INSTALLER_NAME}; /usr/bin/mkdir -m 0700 -- $root; /usr/bin/chown root:root $root; [ ! -L $root ]; [ "$(/usr/bin/stat -c %U:%G:%a -- $root)" = root:root:700 ]; set -o noclobber; exec 3> $target; /usr/bin/base64 -d >&3; exec 3>&-; /usr/bin/chown root:root $target; /usr/bin/chmod 0700 $target; [ "$(/usr/bin/stat -c %s -- $target)" = {size} ]; [ "$(/usr/bin/sha256sum $target | /usr/bin/cut -d" " -f1)" = {digest} ]; exec $target' <<'G8_016_INSTALL_B64'
{encoded}
G8_016_INSTALL_B64
/usr/bin/printf '%s\\n' 'G8_TEST_READONLY_ACCESS_POSTCHECK_016=PASS'
exit
G8_016_REMOTE
g8_remote_status=$?
exit "$g8_remote_status"
"""


def self_test(installer: bytes) -> None:
    """离线验证生成物的关键授权边界，不读取身份材料也不启动子进程。"""
    command = build_command(installer)
    required = (
        CHANGE_ID,
        SOURCE_CHANGE_ID,
        EXPECTED_INSTALLER_SHA256,
        "BatchMode=yes",
        "ConnectionAttempts=1",
        "NumberOfPasswordPrompts=0",
        "G8_TEST_READONLY_ACCESS_PREFLIGHT_016=PASS",
        "G8_TEST_READONLY_ACCESS_POSTCHECK_016=PASS",
        "G8_016_REMOTE",
        "/usr/bin/sudo -k -v",
        "/usr/bin/sudo -n /bin/bash -ceu",
    )
    if any(value not in command for value in required):
        raise RuntimeError("invalid_command")
    if b"G8_TEST_READONLY_ACCESS_INSTALL_016=PASS" not in installer \
            or b"validate_auditor_entry" not in installer \
            or b'/usr/bin/sudo -u pc -- /usr/bin/sudo -n "$AUDITOR_TARGET" --self-test' not in installer:
        raise RuntimeError("invalid_installer")
    for forbidden in ("sudo -S", "SUDO_ASKPASS", "SSH_ASKPASS", "PASSWORD=", "TOKEN=", "PRIVATE KEY"):
        if forbidden in command:
            raise RuntimeError("invalid_command")


def main() -> int:
    # 消费态未来必须在参数解析和安装器读取前拒绝；当前 016 仅处于工程候选态。
    if CHANGE_ID_CONSUMED:
        print("G8_TEST_READONLY_ACCESS_016_COMMAND=FAILED reason=change_id_consumed")
        return 2
    parser = SafeArgumentParser(add_help=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--change-id")
    parser.add_argument("--output-file")
    try:
        arguments = parser.parse_args()
        installer = read_frozen_installer()
        if arguments.self_test:
            if arguments.change_id is not None or arguments.output_file is not None:
                raise ValueError("invalid_request")
            self_test(installer)
            print("G8_TEST_READONLY_ACCESS_016_COMMAND_SELF_TEST=PASS")
            return 0
        output = Path(arguments.output_file or "")
        if arguments.change_id != CHANGE_ID or not output.is_absolute() or output.exists():
            raise ValueError("invalid_request")
        command = build_command(installer)
        with output.open("x", encoding="utf-8", newline="\n") as handle:
            handle.write(command)
    except Exception:
        print("G8_TEST_READONLY_ACCESS_016_COMMAND=FAILED reason=invalid_request")
        return 2
    print("G8_TEST_READONLY_ACCESS_016_COMMAND=PASS")
    print(f"root_installer_sha256={sha256_bytes(installer)}")
    print(f"root_installer_size={len(installer)}")
    print(f"command_sha256={sha256_bytes(command.encode('utf-8'))}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
