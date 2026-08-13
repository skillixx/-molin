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
from pathlib import Path


CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
ROOT_COPY = "/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
INSTALLER_NAME = "g8-test-readonly-access-install-011.sh"


def sha256_bytes(content: bytes) -> str:
    """只对仓库冻结脚本字节计算摘要。"""
    return hashlib.sha256(content).hexdigest()


def build_command(installer: bytes) -> str:
    """生成不包含密码或凭据正文的本地 SSH 命令与远端固定命令块。"""
    encoded = base64.b64encode(installer).decode("ascii")
    digest = sha256_bytes(installer)
    size = len(installer)
    return f"""# 第一步：在 PowerShell 中仅执行下面一条命令，建立唯一交互 SSH 会话。
& \"$env:SystemRoot\\System32\\OpenSSH\\ssh.exe\" `
  -F none -tt -p 10003 `
  -o BatchMode=no -o IdentitiesOnly=yes -o ConnectionAttempts=1 `
  -o PasswordAuthentication=no -o KbdInteractiveAuthentication=no `
  -o ForwardAgent=no -o ClearAllForwardings=yes -o RequestTTY=force `
  -o UserKnownHostsFile=\"$env:USERPROFILE\\.ssh\\known_hosts\" `
  -i \"$env:USERPROFILE\\.ssh\\id_ed25519\" `
  pc@8.130.9.163

# 第二步：进入 pc 会话后，完整粘贴以下命令块；sudo 密码只能响应终端提示人工输入。
set -eu
[ \"$(/usr/bin/id -un)\" = pc ]
[ \"$(/usr/bin/realpath -e /home/pc/molin)\" = /home/pc/molin ]
[ -d /home/pc/molin/.g8-staging-{CHANGE_ID} ]
[ ! -e /usr/local/libexec/molin/g8-test-readonly-audit ]
[ ! -e /usr/local/libexec/molin/ai-gateway-reconcile ]
[ ! -e /etc/sudoers.d/molin-g8-test-readonly-audit ]
if /usr/bin/sudo -n -l >/dev/null 2>&1; then exit 1; fi
sudo -k -v
sudo -n /bin/bash -ceu 'PATH=/usr/sbin:/usr/bin:/sbin:/bin; export PATH; unset BASH_ENV ENV CDPATH PYTHONPATH PYTHONHOME; umask 077; root={ROOT_COPY}; target=$root/{INSTALLER_NAME}; /usr/bin/mkdir -m 0700 -- $root; /usr/bin/chown root:root $root; [ ! -L $root ]; [ \"$(/usr/bin/stat -c %U:%G:%a -- $root)\" = root:root:700 ]; set -o noclobber; exec 3> $target; /usr/bin/base64 -d >&3; exec 3>&-; /usr/bin/chown root:root $target; /usr/bin/chmod 0700 $target; [ \"$(/usr/bin/stat -c %s -- $target)\" = {size} ]; [ \"$(/usr/bin/sha256sum $target | /usr/bin/cut -d\" \" -f1)\" = {digest} ]; exec $target' <<'G8_011_INSTALL_B64'
{encoded}
G8_011_INSTALL_B64
sudo -n /usr/local/libexec/molin/g8-test-readonly-audit --self-test
exit
"""


def main() -> int:
    parser = argparse.ArgumentParser(add_help=True)
    parser.add_argument("--change-id")
    parser.add_argument("--output-file")
    arguments = parser.parse_args()
    try:
        output = Path(arguments.output_file or "")
        installer_path = Path(__file__).with_name(INSTALLER_NAME)
        if arguments.change_id != CHANGE_ID or not output.is_absolute() or output.exists():
            raise RuntimeError("invalid_request")
        installer = installer_path.read_bytes()
        if not installer.startswith(b"#!/bin/bash\n"):
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
