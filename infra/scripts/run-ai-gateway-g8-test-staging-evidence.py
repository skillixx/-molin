#!/usr/bin/env python3
"""对 G8 测试服 003 暂存路径执行一次完全只读的低敏取证。"""

import sys

# 必须先拒绝非隔离解释器，避免脚本目录或 PYTHONPATH 中的同名模块劫持本地校验。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_STAGING_EVIDENCE=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import base64
import hashlib
import os
import re
import subprocess
from pathlib import Path


CHANGE_ID = "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-004"
# 004 已完成唯一一次正式调用并停止；任何普通入口都必须在读取本地身份材料或联网前拒绝重放。
CHANGE_ID_CONSUMED = True
TARGET_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-20260812-003"
TARGET = "pc@8.130.9.163"
TARGET_HOST = "8.130.9.163"
TARGET_PORT = "10003"
TARGET_HOSTNAME = "pc-Z790-UD-AX"
TARGET_MACHINE_ID_SHA256 = "b60555f0d8d48731b657d21b2e54559d263210688125ae56a4d662fc4d7278d4"
TARGET_SSH_ED25519_FINGERPRINT = "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"
LOCAL_IDENTITY_ED25519_FINGERPRINT = "SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0"
TARGET_DEPLOYMENT_ROOT = "/home/pc/molin"
STAGING_PATH = "/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003"
FROZEN_BUNDLE_RECEIPT_SHA256 = "82b18d6040bcd6be72cf170fa066ecd7cf469a53f4901365f379bec5a89c496d"
FROZEN_RECONCILE_SHA256 = "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1"
FROZEN_FILES = {
    "SHA256SUMS": (FROZEN_BUNDLE_RECEIPT_SHA256, 362),
    "ai-gateway-reconcile": (FROZEN_RECONCILE_SHA256, 13_066_129),
    "g8-test-readonly-audit": (
        "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256",
        18_377,
    ),
    "manifest.env": ("726174ea41ecfee69f9d8c1aff7411dc9a8c73f3dc60ca0d5e700eb5f962ea66", 897),
    "molin-g8-test-readonly-audit.sudoers": (
        "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f",
        416,
    ),
}

EXPECTED_REMOTE_KEYS = {
    "EVIDENCE_CHANGE_ID",
    "TARGET_CHANGE_ID",
    "LOGIN_USER",
    "HOSTNAME",
    "MACHINE_ID_SHA256",
    "DEPLOYMENT_ROOT_REALPATH",
    "DEPLOYMENT_ROOT_CHECK",
    "STAGING_STATE",
    "STAGING_INTEGRITY",
    "STAGING_MISMATCH_REASON",
    "EVIDENCE_RESULT",
}

# 远端仅使用隔离 Python 标准库读取固定路径；不接收路径参数，也不包含删除、写入、sudo 或子进程能力。
REMOTE_PROGRAM = f"""import grp
import hashlib
import os
import pwd
import stat

evidence_change_id = {CHANGE_ID!r}
target_change_id = {TARGET_CHANGE_ID!r}
target_hostname = {TARGET_HOSTNAME!r}
target_machine_id_sha256 = {TARGET_MACHINE_ID_SHA256!r}
deployment_root = {TARGET_DEPLOYMENT_ROOT!r}
staging_path = {STAGING_PATH!r}
expected_files = {{
    'SHA256SUMS': {FROZEN_FILES['SHA256SUMS']!r},
    'ai-gateway-reconcile': {FROZEN_FILES['ai-gateway-reconcile']!r},
    'g8-test-readonly-audit': {FROZEN_FILES['g8-test-readonly-audit']!r},
    'manifest.env': {FROZEN_FILES['manifest.env']!r},
    'molin-g8-test-readonly-audit.sudoers': {FROZEN_FILES['molin-g8-test-readonly-audit.sudoers']!r},
}}

def digest(path):
    with open(path, 'rb') as handle:
        return digest_handle(handle)

def digest_handle(handle):
    value = hashlib.sha256()
    while True:
        block = handle.read(1024 * 1024)
        if not block:
            break
        value.update(block)
    return value.hexdigest()

def reject():
    raise SystemExit(41)

account = pwd.getpwnam('pc')
group = grp.getgrnam('pc')
if os.getuid() != account.pw_uid or os.uname().nodename != target_hostname:
    reject()
if digest('/etc/machine-id') != target_machine_id_sha256:
    reject()

root_meta = os.lstat(deployment_root)
if os.path.realpath(deployment_root) != deployment_root:
    reject()
root_descriptor = os.open(
    deployment_root,
    os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
)
try:
    # 固定部署根 inode，后续所有暂存读取都相对此描述符执行，避免父路径被替换后跨目录取证。
    pinned_root = os.fstat(root_descriptor)
    root_mode = stat.S_IMODE(pinned_root.st_mode)
    if (
        (pinned_root.st_dev, pinned_root.st_ino) != (root_meta.st_dev, root_meta.st_ino)
        or not stat.S_ISDIR(pinned_root.st_mode)
        or pinned_root.st_uid != account.pw_uid
        or pinned_root.st_gid != group.gr_gid
        or root_mode & 0o700 != 0o700
        or root_mode & 0o022
        or os.path.dirname(staging_path) != deployment_root
    ):
        reject()

    staging_name = os.path.basename(staging_path)
    staging_state = 'ABSENT'
    staging_integrity = 'NOT_APPLICABLE'
    staging_mismatch_reason = 'NONE'
    try:
        stage_meta = os.stat(staging_name, dir_fd=root_descriptor, follow_symlinks=False)
    except FileNotFoundError:
        stage_meta = None
    except OSError:
        reject()
    if stage_meta is not None:
        staging_state = 'PRESENT'
        staging_integrity = 'MISMATCH'
        staging_mismatch_reason = 'PATH'
        try:
            if (
                stat.S_ISDIR(stage_meta.st_mode)
                and stage_meta.st_uid == account.pw_uid
                and stage_meta.st_gid == group.gr_gid
                and stat.S_IMODE(stage_meta.st_mode) == 0o700
            ):
                stage_descriptor = os.open(
                    staging_name,
                    os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
                    dir_fd=root_descriptor,
                )
                try:
                    pinned_stage = os.fstat(stage_descriptor)
                    if (pinned_stage.st_dev, pinned_stage.st_ino) != (stage_meta.st_dev, stage_meta.st_ino):
                        staging_mismatch_reason = 'PATH'
                    else:
                        staging_mismatch_reason = 'FILE_SET'
                        names = os.listdir(stage_descriptor)
                        if set(names) == set(expected_files) and len(names) == len(expected_files):
                            staging_mismatch_reason = 'FILE_METADATA'
                            metadata_matches = True
                            entries_stable = True
                            content_matches = True
                            opened_files = {{}}
                            for name in names:
                                file_descriptor = os.open(
                                    name,
                                    os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC,
                                    dir_fd=stage_descriptor,
                                )
                                try:
                                    metadata = os.fstat(file_descriptor)
                                    expected_sha256, expected_size = expected_files[name]
                                    opened_files[name] = (
                                        metadata.st_dev,
                                        metadata.st_ino,
                                        metadata.st_mode,
                                        metadata.st_uid,
                                        metadata.st_gid,
                                        metadata.st_size,
                                        metadata.st_mtime_ns,
                                        metadata.st_ctime_ns,
                                    )
                                    current_metadata_matches = not (
                                        not stat.S_ISREG(metadata.st_mode)
                                        or metadata.st_uid != account.pw_uid
                                        or metadata.st_gid != group.gr_gid
                                        or stat.S_IMODE(metadata.st_mode) & 0o022
                                        or metadata.st_size != expected_size
                                    )
                                    if current_metadata_matches:
                                        with os.fdopen(file_descriptor, 'rb', closefd=False) as handle:
                                            actual_digest = digest_handle(handle)
                                    else:
                                        metadata_matches = False
                                        actual_digest = None
                                    after_digest = os.fstat(file_descriptor)
                                    if (
                                        (
                                            after_digest.st_dev,
                                            after_digest.st_ino,
                                            after_digest.st_mode,
                                            after_digest.st_uid,
                                            after_digest.st_gid,
                                            after_digest.st_size,
                                            after_digest.st_mtime_ns,
                                            after_digest.st_ctime_ns,
                                        )
                                        != opened_files[name]
                                    ):
                                        entries_stable = False
                                    if actual_digest is not None and actual_digest != expected_sha256:
                                        content_matches = False
                                finally:
                                    os.close(file_descriptor)
                            if entries_stable:
                                final_names = os.listdir(stage_descriptor)
                                entries_stable = (
                                    set(final_names) == set(expected_files)
                                    and len(final_names) == len(expected_files)
                                )
                            if entries_stable:
                                for name in final_names:
                                    current_file = os.stat(
                                        name,
                                        dir_fd=stage_descriptor,
                                        follow_symlinks=False,
                                    )
                                    current_identity = (
                                        current_file.st_dev,
                                        current_file.st_ino,
                                        current_file.st_mode,
                                        current_file.st_uid,
                                        current_file.st_gid,
                                        current_file.st_size,
                                        current_file.st_mtime_ns,
                                        current_file.st_ctime_ns,
                                    )
                                    if current_identity != opened_files.get(name):
                                        entries_stable = False
                                        break
                            final_stage = os.fstat(stage_descriptor)
                            current_stage = os.stat(
                                staging_name,
                                dir_fd=root_descriptor,
                                follow_symlinks=False,
                            )
                            if (
                                not entries_stable
                                or (
                                    final_stage.st_dev,
                                    final_stage.st_ino,
                                    final_stage.st_mtime_ns,
                                    final_stage.st_ctime_ns,
                                )
                                != (
                                    pinned_stage.st_dev,
                                    pinned_stage.st_ino,
                                    pinned_stage.st_mtime_ns,
                                    pinned_stage.st_ctime_ns,
                                )
                                or (current_stage.st_dev, current_stage.st_ino)
                                != (pinned_stage.st_dev, pinned_stage.st_ino)
                            ):
                                staging_mismatch_reason = 'PATH'
                            elif metadata_matches:
                                staging_mismatch_reason = 'FILE_CONTENT'
                                if content_matches:
                                    staging_integrity = 'PASS'
                                    staging_mismatch_reason = 'NONE'
                finally:
                    os.close(stage_descriptor)
        except OSError:
            staging_mismatch_reason = 'READ_ERROR'

    # 输出任何 PASS 前再次把绝对路径与固定 inode 对齐，父路径漂移必须失败关闭。
    current_root = os.lstat(deployment_root)
    if (
        os.path.realpath(deployment_root) != deployment_root
        or (current_root.st_dev, current_root.st_ino) != (pinned_root.st_dev, pinned_root.st_ino)
    ):
        reject()
finally:
    os.close(root_descriptor)

print('EVIDENCE_CHANGE_ID=' + evidence_change_id)
print('TARGET_CHANGE_ID=' + target_change_id)
print('LOGIN_USER=pc')
print('HOSTNAME=' + target_hostname)
print('MACHINE_ID_SHA256=' + target_machine_id_sha256)
print('DEPLOYMENT_ROOT_REALPATH=' + deployment_root)
print('DEPLOYMENT_ROOT_CHECK=PASS')
print('STAGING_STATE=' + staging_state)
print('STAGING_INTEGRITY=' + staging_integrity)
print('STAGING_MISMATCH_REASON=' + staging_mismatch_reason)
print('EVIDENCE_RESULT=PASS')
"""


class EvidenceError(RuntimeError):
    """表示远端未形成完整、低敏且可验证的暂存证据。"""


class SafeArgumentParser(argparse.ArgumentParser):
    """拒绝 argparse 回显调用方路径或其他参数内容。"""

    def error(self, message: str) -> None:
        """将所有参数错误收敛为固定低敏异常。"""
        raise RuntimeError("invalid_arguments")


def parse_remote_output(stdout: str) -> dict[str, str]:
    """只接受精确键集合和两种可关闭 UNKNOWN 的状态组合。"""
    values: dict[str, str] = {}
    for line in stdout.splitlines():
        match = re.fullmatch(r"([A-Z0-9_]+)=([^\r\n]+)", line)
        if not match or match.group(1) in values:
            raise EvidenceError("invalid_remote_output")
        values[match.group(1)] = match.group(2)
    if set(values) != EXPECTED_REMOTE_KEYS:
        raise EvidenceError("remote_key_set_mismatch")
    expected = {
        "EVIDENCE_CHANGE_ID": CHANGE_ID,
        "TARGET_CHANGE_ID": TARGET_CHANGE_ID,
        "LOGIN_USER": "pc",
        "HOSTNAME": TARGET_HOSTNAME,
        "MACHINE_ID_SHA256": TARGET_MACHINE_ID_SHA256,
        "DEPLOYMENT_ROOT_REALPATH": TARGET_DEPLOYMENT_ROOT,
        "DEPLOYMENT_ROOT_CHECK": "PASS",
        "EVIDENCE_RESULT": "PASS",
    }
    if any(values.get(key) != value for key, value in expected.items()):
        raise EvidenceError("remote_identity_mismatch")
    state = (
        values["STAGING_STATE"],
        values["STAGING_INTEGRITY"],
        values["STAGING_MISMATCH_REASON"],
    )
    valid_states = {
        ("ABSENT", "NOT_APPLICABLE", "NONE"),
        ("PRESENT", "PASS", "NONE"),
        *(('PRESENT', 'MISMATCH', reason) for reason in (
            'PATH', 'FILE_SET', 'FILE_METADATA', 'FILE_CONTENT', 'READ_ERROR'
        )),
    }
    if state not in valid_states:
        raise EvidenceError("invalid_staging_state")
    return values


def openssh_fingerprint(key_blob: bytes) -> str:
    """直接计算 OpenSSH SHA-256 指纹，不依赖调用方 PATH。"""
    return "SHA256:" + base64.b64encode(hashlib.sha256(key_blob).digest()).decode("ascii").rstrip("=")


def validate_known_hosts(known_hosts: Path) -> None:
    """只接受目标地址唯一的明文 ED25519 条目并核对冻结指纹。"""
    if not known_hosts.is_absolute() or not known_hosts.is_file() or known_hosts.is_symlink():
        raise RuntimeError("invalid_known_hosts")
    prefix = f"[{TARGET_HOST}]:{TARGET_PORT} ssh-ed25519 "
    matches = [line for line in known_hosts.read_text(encoding="ascii").splitlines() if line.startswith(prefix)]
    if len(matches) != 1:
        raise RuntimeError("known_host_entry_mismatch")
    fields = matches[0].split()
    if len(fields) < 3 or fields[0] != f"[{TARGET_HOST}]:{TARGET_PORT}" or fields[1] != "ssh-ed25519":
        raise RuntimeError("invalid_known_host_entry")
    try:
        key_blob = base64.b64decode(fields[2], validate=True)
    except ValueError as error:
        raise RuntimeError("invalid_known_host_key") from error
    if openssh_fingerprint(key_blob) != TARGET_SSH_ED25519_FINGERPRINT:
        raise RuntimeError("known_host_fingerprint_mismatch")


def public_key_fingerprint(path: Path) -> str:
    """从固定公钥文件计算指纹，不调用可替换的外部工具。"""
    fields = path.read_text(encoding="ascii").strip().split()
    if len(fields) < 2 or fields[0] != "ssh-ed25519":
        raise RuntimeError("invalid_identity_public_key")
    try:
        key_blob = base64.b64decode(fields[1], validate=True)
    except ValueError as error:
        raise RuntimeError("invalid_identity_public_key") from error
    return openssh_fingerprint(key_blob)


def validate_identity_file(identity_file: Path, identity_public_file: Path, known_hosts: Path) -> None:
    """要求私钥、公钥和 known_hosts 位于同一受控目录并绑定冻结公钥指纹。"""
    paths = (identity_file, identity_public_file, known_hosts)
    if any(not path.is_absolute() or not path.is_file() or path.is_symlink() for path in paths):
        raise RuntimeError("invalid_identity_path")
    if len({path.parent.resolve(strict=True) for path in paths}) != 1:
        raise RuntimeError("identity_directory_mismatch")
    if public_key_fingerprint(identity_public_file) != LOCAL_IDENTITY_ED25519_FINGERPRINT:
        raise RuntimeError("identity_fingerprint_mismatch")


def fixed_ssh_keygen_executable() -> Path:
    """只使用操作系统固定 ssh-keygen 路径验证私钥 ACL 和密钥对。"""
    path = (
        Path(r"C:\Windows\System32\OpenSSH\ssh-keygen.exe")
        if os.name == "nt"
        else Path("/usr/bin/ssh-keygen")
    )
    if not path.is_file():
        raise RuntimeError("ssh_keygen_unavailable")
    return path


def fixed_ssh_executable() -> Path:
    """只使用操作系统固定 OpenSSH 路径。"""
    path = Path(r"C:\Windows\System32\OpenSSH\ssh.exe") if os.name == "nt" else Path("/usr/bin/ssh")
    if not path.is_file():
        raise RuntimeError("ssh_unavailable")
    return path


def fixed_ssh_environment() -> dict[str, str]:
    """仅保留 OpenSSH 加载系统组件所需变量，拒绝代理、AskPass 和 PATH 注入。"""
    environment = {
        "LANG": "C.UTF-8",
        "PATH": r"C:\Windows\System32\OpenSSH;C:\Windows\System32" if os.name == "nt" else "/usr/bin:/bin",
    }
    for name in (
        "SYSTEMROOT",
        "WINDIR",
        "SYSTEMDRIVE",
        "COMSPEC",
        "PROGRAMDATA",
        "PROGRAMFILES",
        "PROGRAMFILES(X86)",
        "USERPROFILE",
        "LOCALAPPDATA",
        "HOMEDRIVE",
        "HOMEPATH",
        "USERNAME",
        "USERDOMAIN",
        "TEMP",
        "TMP",
    ):
        if os.environ.get(name):
            environment[name] = os.environ[name]
    return environment


def validate_identity_pair(identity_file: Path, identity_public_file: Path) -> None:
    """由固定 ssh-keygen 读取私钥公钥；ACL 或密钥对不匹配时失败关闭。"""
    result = subprocess.run(
        [str(fixed_ssh_keygen_executable()), "-y", "-f", str(identity_file)],
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="strict",
        timeout=10,
        env=fixed_ssh_environment(),
        check=False,
    )
    expected = identity_public_file.read_text(encoding="ascii").strip().split()[:2]
    actual = result.stdout.strip().split()[:2]
    if result.returncode != 0 or result.stderr or actual != expected:
        raise RuntimeError("identity_pair_mismatch")


def run_remote_evidence(ssh_executable: Path, known_hosts: Path, identity_file: Path) -> dict[str, str]:
    """只启动一次 SSH；失败、超时或任何 stderr 都低敏停止且绝不重试。"""
    command = [
        str(ssh_executable),
        "-F",
        "none",
        "-o",
        "BatchMode=yes",
        "-o",
        "NumberOfPasswordPrompts=0",
        "-o",
        "ConnectionAttempts=1",
        "-o",
        "StrictHostKeyChecking=yes",
        "-o",
        f"UserKnownHostsFile={known_hosts}",
        "-o",
        "IdentitiesOnly=yes",
        "-o",
        f"IdentityFile={identity_file}",
        "-o",
        "PreferredAuthentications=publickey",
        "-o",
        "PasswordAuthentication=no",
        "-o",
        "KbdInteractiveAuthentication=no",
        "-o",
        "ClearAllForwardings=yes",
        "-o",
        "ForwardAgent=no",
        "-o",
        "ForwardX11=no",
        "-o",
        "PermitLocalCommand=no",
        "-o",
        "RequestTTY=no",
        "-o",
        "ConnectTimeout=10",
        "-p",
        TARGET_PORT,
        TARGET,
        "/usr/bin/env",
        "-i",
        "PATH=/usr/bin:/bin",
        "/usr/bin/python3",
        "-I",
        "-",
    ]
    try:
        result = subprocess.run(
            command,
            input=REMOTE_PROGRAM,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="strict",
            timeout=30,
            env=fixed_ssh_environment(),
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired, UnicodeError) as error:
        raise EvidenceError("ssh_execution_failed") from error
    if result.returncode != 0 or result.stderr:
        raise EvidenceError("remote_evidence_failed")
    return parse_remote_output(result.stdout)


def main() -> int:
    """先完成本地身份门禁，再按授权执行至多一次远端只读取证。"""
    parser = SafeArgumentParser(add_help=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--local-check", action="store_true")
    parser.add_argument("--change-id")
    parser.add_argument("--known-hosts")
    parser.add_argument("--identity-file")
    parser.add_argument("--identity-public-file")
    try:
        arguments = parser.parse_args()
    except RuntimeError:
        print("G8_TEST_READONLY_STAGING_EVIDENCE=FAILED reason=invalid_request")
        return 2
    if arguments.self_test:
        if any(
            marker in REMOTE_PROGRAM
            for marker in ("import subprocess", "os.remove(", "os.unlink(", "os.rmdir(", "import shutil")
        ):
            print("G8_TEST_READONLY_STAGING_EVIDENCE=FAILED reason=unsafe_remote_program")
            return 2
        try:
            compile(REMOTE_PROGRAM, "<g8-staging-evidence>", "exec")
        except SyntaxError:
            print("G8_TEST_READONLY_STAGING_EVIDENCE=FAILED reason=invalid_remote_program")
            return 2
        print("G8_TEST_READONLY_STAGING_EVIDENCE_SELF_TEST=PASS")
        return 0
    if CHANGE_ID_CONSUMED:
        print("G8_TEST_READONLY_STAGING_EVIDENCE=FAILED reason=change_id_consumed")
        return 2
    if (
        arguments.change_id != CHANGE_ID
        or not arguments.known_hosts
        or not arguments.identity_file
        or not arguments.identity_public_file
    ):
        print("G8_TEST_READONLY_STAGING_EVIDENCE=FAILED reason=invalid_request")
        return 2
    known_hosts = Path(arguments.known_hosts)
    identity_file = Path(arguments.identity_file)
    identity_public_file = Path(arguments.identity_public_file)
    try:
        validate_known_hosts(known_hosts)
        validate_identity_file(identity_file, identity_public_file, known_hosts)
        validate_identity_pair(identity_file, identity_public_file)
    except Exception:
        print("G8_TEST_READONLY_STAGING_EVIDENCE=FAILED reason=local_validation_failed")
        return 2
    if arguments.local_check:
        print("G8_TEST_READONLY_STAGING_EVIDENCE_LOCAL_CHECK=PASS")
        return 0
    try:
        values = run_remote_evidence(fixed_ssh_executable(), known_hosts, identity_file)
    except Exception:
        print("G8_TEST_READONLY_STAGING_EVIDENCE=FAILED reason=remote_evidence_failed")
        return 2
    mismatch = values["STAGING_INTEGRITY"] == "MISMATCH"
    print(
        "G8_TEST_READONLY_STAGING_EVIDENCE=MISMATCH"
        if mismatch
        else "G8_TEST_READONLY_STAGING_EVIDENCE=PASS"
    )
    print(f"change_id={CHANGE_ID}")
    print(f"target_change_id={TARGET_CHANGE_ID}")
    print(f"staging_state={values['STAGING_STATE']}")
    print(f"staging_integrity={values['STAGING_INTEGRITY']}")
    print(f"staging_mismatch_reason={values['STAGING_MISMATCH_REASON']}")
    return 3 if mismatch else 0


if __name__ == "__main__":
    raise SystemExit(main())
