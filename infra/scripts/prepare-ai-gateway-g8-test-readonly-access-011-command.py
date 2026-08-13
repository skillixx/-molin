#!/usr/bin/env python3
"""生成 011 单次交互 SSH 会话中供操作者人工复制的冻结命令。"""

import sys

# 在加载可受脚本目录影响的模块前强制隔离解释器。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_ACCESS_011_COMMAND=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import base64
import hashlib
import stat
from pathlib import Path


CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
CHANGE_ID_CONSUMED = False
ROOT_COPY = "/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
INSTALLER_NAME = "g8-test-readonly-access-install-011.sh"
EXPECTED_INSTALLER_SHA256 = "675eb16e96db9c14dffdbec2dc80f28e1483cbdb7c4b683568ebb85cf7bf1aa0"
EXPECTED_INSTALLER_SIZE = 8964


def sha256_bytes(content: bytes) -> str:
    """只对仓库冻结脚本字节计算摘要。"""
    return hashlib.sha256(content).hexdigest()


def build_command(installer: bytes) -> str:
    """生成不包含密码或凭据正文的本地 SSH 命令与远端固定命令块。"""
    encoded = base64.b64encode(installer).decode("ascii")
    digest = sha256_bytes(installer)
    size = len(installer)
    return f"""# 第一步：在 PowerShell 中先校验固定身份材料，再建立唯一交互 SSH 会话。
$ssh = 'C:\\Windows\\System32\\OpenSSH\\ssh.exe'
$sshKeygen = 'C:\\Windows\\System32\\OpenSSH\\ssh-keygen.exe'
$knownHosts = 'C:\\Users\\skillixx\\.ssh\\known_hosts'
$identity = 'C:\\Users\\skillixx\\.ssh\\id_ed25519'
$identityPublic = 'C:\\Users\\skillixx\\.ssh\\id_ed25519.pub'
foreach ($path in @($ssh, $sshKeygen, $knownHosts, $identity, $identityPublic)) {{
  $item = Get-Item -LiteralPath $path -Force -ErrorAction Stop
  if (-not $item.PSIsContainer -and -not ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {{ continue }}
  throw 'identity_material_invalid'
}}
$hostEvidence = & $sshKeygen -lf $knownHosts 2>$null
if ($LASTEXITCODE -ne 0 -or -not (($hostEvidence -join "`n").Contains('SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I'))) {{
  throw 'known_hosts_mismatch'
}}
$derivedPublic = (& $sshKeygen -y -f $identity 2>$null)
if ($LASTEXITCODE -ne 0) {{ throw 'identity_pair_mismatch' }}
$declaredParts = ((Get-Content -LiteralPath $identityPublic -Raw -ErrorAction Stop).Trim() -split '\\s+')
$derivedParts = (($derivedPublic.Trim()) -split '\\s+')
if ($declaredParts.Count -lt 2 -or $derivedParts.Count -lt 2 -or $declaredParts[0] -ne $derivedParts[0] -or $declaredParts[1] -ne $derivedParts[1]) {{
  throw 'identity_pair_mismatch'
}}
& \"C:\\Windows\\System32\\OpenSSH\\ssh.exe\" `
  -F none -tt -p 10003 `
  -o BatchMode=no -o IdentitiesOnly=yes -o ConnectionAttempts=1 `
  -o PasswordAuthentication=no -o KbdInteractiveAuthentication=no `
  -o StrictHostKeyChecking=yes `
  -o ForwardAgent=no -o ClearAllForwardings=yes -o RequestTTY=force `
  -o UserKnownHostsFile=\"C:\\Users\\skillixx\\.ssh\\known_hosts\" `
  -i \"C:\\Users\\skillixx\\.ssh\\id_ed25519\" `
  pc@8.130.9.163

# 第二步：进入 pc 会话后，完整粘贴以下命令块；sudo 密码只能响应终端提示人工输入。
set -eu
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
unset BASH_ENV ENV CDPATH PYTHONPATH PYTHONHOME
unalias sudo 2>/dev/null || :
unset -f sudo 2>/dev/null || :
staging=/home/pc/molin/.g8-staging-{CHANGE_ID}
/usr/bin/test \"$(/usr/bin/id -un)\" = pc
/usr/bin/test \"$(/usr/bin/realpath -e /home/pc/molin)\" = /home/pc/molin
/usr/bin/test \"$(/usr/bin/realpath -e \"$staging\")\" = \"$staging\"
/usr/bin/test \"$(/usr/bin/stat -c '%U:%G:%a' -- \"$staging\")\" = pc:pc:700
actual=$(/usr/bin/find \"$staging\" -mindepth 1 -maxdepth 1 -printf '%f\\n' | /usr/bin/sort | /usr/bin/tr '\\n' ' ')
/usr/bin/test \"$actual\" = 'SHA256SUMS ai-gateway-reconcile g8-test-readonly-audit manifest.env molin-g8-test-readonly-audit.sudoers '
for entry in SHA256SUMS:600 ai-gateway-reconcile:700 g8-test-readonly-audit:700 manifest.env:600 molin-g8-test-readonly-audit.sudoers:600; do
  name=${{entry%%:*}}; mode=${{entry##*:}}; path=\"$staging/$name\"
  /usr/bin/test -f \"$path\"; /usr/bin/test ! -L \"$path\"
  /usr/bin/test \"$(/usr/bin/stat -c '%U:%G:%a' -- \"$path\")\" = \"pc:pc:$mode\"
done
/usr/bin/test \"$(/usr/bin/sha256sum \"$staging/SHA256SUMS\" | /usr/bin/cut -d' ' -f1)\" = 15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f
(cd \"$staging\" && /usr/bin/sha256sum -c SHA256SUMS >/dev/null 2>&1)
/usr/bin/test \"$(/usr/bin/sha256sum \"$staging/g8-test-readonly-audit\" | /usr/bin/cut -d' ' -f1)\" = 308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256
/usr/bin/test \"$(/usr/bin/sha256sum \"$staging/molin-g8-test-readonly-audit.sudoers\" | /usr/bin/cut -d' ' -f1)\" = 1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f
/usr/bin/test \"$(/usr/bin/sha256sum \"$staging/ai-gateway-reconcile\" | /usr/bin/cut -d' ' -f1)\" = 37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1
/usr/bin/test \"$(/usr/bin/stat -c '%s' -- \"$staging/ai-gateway-reconcile\")\" = 13066129
/usr/bin/grep -qx 'CHANGE_ID={CHANGE_ID}' \"$staging/manifest.env\"
/usr/bin/grep -qx 'TARGET_TRANSPORT=DROP_SSH_INTERACTIVE_SUDO' \"$staging/manifest.env\"
/usr/bin/grep -qx 'PHYSICAL_HOST_IDENTITY=NOT_APPLICABLE' \"$staging/manifest.env\"
for parent in /home /home/pc /home/pc/molin; do
  /usr/bin/test -d \"$parent\"; /usr/bin/test ! -L \"$parent\"
  mode=$((8#$(/usr/bin/stat -c '%a' -- \"$parent\"))); /usr/bin/test $((mode & 0022)) -eq 0
done
/usr/bin/test \"$(/usr/bin/stat -c '%U:%G' -- /home)\" = root:root
/usr/bin/test \"$(/usr/bin/stat -c '%U:%G' -- /home/pc)\" = pc:pc
/usr/bin/test \"$(/usr/bin/stat -c '%U:%G' -- /home/pc/molin)\" = pc:pc
for parent in /usr /usr/local /usr/local/libexec /etc /etc/sudoers.d; do
  /usr/bin/test -d \"$parent\"; /usr/bin/test ! -L \"$parent\"
  /usr/bin/test \"$(/usr/bin/stat -c '%U:%G' -- \"$parent\")\" = root:root
  mode=$((8#$(/usr/bin/stat -c '%a' -- \"$parent\"))); /usr/bin/test $((mode & 0022)) -eq 0
done
for target in /usr/local/libexec/molin/g8-test-readonly-audit /usr/local/libexec/molin/ai-gateway-reconcile /etc/sudoers.d/molin-g8-test-readonly-audit; do
  /usr/bin/test ! -e \"$target\"; /usr/bin/test ! -L \"$target\"
done
if /usr/bin/sudo -n -l >/dev/null 2>&1; then exit 1; fi
/usr/bin/printf '%s\\n' 'PREFLIGHT_011=PASS'
/usr/bin/sudo -k -v
/usr/bin/sudo -n /bin/bash -ceu 'PATH=/usr/sbin:/usr/bin:/sbin:/bin; export PATH; unset BASH_ENV ENV CDPATH PYTHONPATH PYTHONHOME; umask 077; root={ROOT_COPY}; target=$root/{INSTALLER_NAME}; /usr/bin/mkdir -m 0700 -- $root; /usr/bin/chown root:root $root; [ ! -L $root ]; [ \"$(/usr/bin/stat -c %U:%G:%a -- $root)\" = root:root:700 ]; set -o noclobber; exec 3> $target; /usr/bin/base64 -d >&3; exec 3>&-; /usr/bin/chown root:root $target; /usr/bin/chmod 0700 $target; [ \"$(/usr/bin/stat -c %s -- $target)\" = {size} ]; [ \"$(/usr/bin/sha256sum $target | /usr/bin/cut -d\" \" -f1)\" = {digest} ]; exec $target' <<'G8_011_INSTALL_B64'
{encoded}
G8_011_INSTALL_B64
/usr/bin/sudo -n /usr/local/libexec/molin/g8-test-readonly-audit --self-test
exit
"""


def main() -> int:
    parser = argparse.ArgumentParser(add_help=True)
    parser.add_argument("--change-id")
    parser.add_argument("--output-file")
    arguments = parser.parse_args()
    try:
        if CHANGE_ID_CONSUMED:
            print("G8_TEST_READONLY_ACCESS_011_COMMAND=FAILED reason=change_id_consumed")
            return 2
        output = Path(arguments.output_file or "")
        installer_path = Path(__file__).with_name(INSTALLER_NAME)
        if arguments.change_id != CHANGE_ID or not output.is_absolute() or output.exists():
            raise RuntimeError("invalid_request")
        installer_before = installer_path.lstat()
        if not stat.S_ISREG(installer_before.st_mode) or installer_path.is_symlink():
            raise RuntimeError("invalid_installer")
        installer = installer_path.read_bytes()
        installer_after = installer_path.stat()
        stable_identity = (
            installer_before.st_dev,
            installer_before.st_ino,
            installer_before.st_size,
            installer_before.st_mtime_ns,
            installer_before.st_ctime_ns,
        ) == (
            installer_after.st_dev,
            installer_after.st_ino,
            installer_after.st_size,
            installer_after.st_mtime_ns,
            installer_after.st_ctime_ns,
        )
        if (
            not stable_identity
            or len(installer) != EXPECTED_INSTALLER_SIZE
            or sha256_bytes(installer) != EXPECTED_INSTALLER_SHA256
            or not installer.startswith(b"#!/bin/bash\n")
        ):
            raise RuntimeError("invalid_installer")
        command = build_command(installer)
        with output.open("x", encoding="utf-8", newline="\n") as handle:
            handle.write(command)
    except Exception:
        print("G8_TEST_READONLY_ACCESS_011_COMMAND=FAILED reason=invalid_request")
        return 2
    print("G8_TEST_READONLY_ACCESS_011_COMMAND=PASS")
    print(f"root_installer_sha256={sha256_bytes(installer)}")
    print(f"root_installer_size={len(installer)}")
    print(f"command_sha256={sha256_bytes(command.encode('utf-8'))}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
