#!/usr/bin/env python3
"""按固定顺序执行 G8 测试服只读预检与单次原子暂存上传。"""

import sys

# 必须先拒绝非隔离解释器，避免脚本目录或 PYTHONPATH 中的同名模块劫持本地预检。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_ACCESS_STAGE=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import base64
import hashlib
import os
import re
import subprocess
from pathlib import Path


CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-20260812-003"
SOURCE_COMMIT = "8ec878572f62ef2584c38aaadc1bca1cb802b13f"
SOURCE_TREE = "988bdcdc8017322264733ebe68876e4811b01412"
TARGET = "pc@8.130.9.163"
TARGET_HOST = "8.130.9.163"
TARGET_PORT = "10003"
TARGET_HOSTNAME = "pc-Z790-UD-AX"
TARGET_MACHINE_ID_SHA256 = "b60555f0d8d48731b657d21b2e54559d263210688125ae56a4d662fc4d7278d4"
TARGET_SSH_ED25519_FINGERPRINT = "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"
LOCAL_IDENTITY_ED25519_FINGERPRINT = "SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0"
TARGET_DEPLOYMENT_ROOT = "/home/pc/molin"
STAGING_PATH = "/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003"
EXPECTED_BUNDLE_RECEIPT_SHA256 = "82b18d6040bcd6be72cf170fa066ecd7cf469a53f4901365f379bec5a89c496d"
FROZEN_AUDITOR_SHA256 = "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256"
FROZEN_SUDOERS_SHA256 = "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f"
FROZEN_RECONCILE_SHA256 = "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1"
FROZEN_RECONCILE_SIZE = 13_066_129
EXPECTED_FILES = {
    "SHA256SUMS",
    "ai-gateway-reconcile",
    "g8-test-readonly-audit",
    "manifest.env",
    "molin-g8-test-readonly-audit.sudoers",
}
EXPECTED_REMOTE_KEYS = {
    "PREFLIGHT_CHANGE_ID",
    "LOGIN_USER",
    "HOSTNAME",
    "MACHINE_ID_SHA256",
    "DEPLOYMENT_ROOT_REALPATH",
    "DEPLOYMENT_ROOT_META",
    "STAGING_ABSENT",
    "INSTALL_TARGETS_ABSENT",
    "PREFLIGHT_RESULT",
}

# 不使用 cut、awk 或嵌套引号提取摘要，避免 Windows OpenSSH 到 POSIX shell 的二次解析改变参数。
REMOTE_SCRIPT = f"""set -eu
unset ENV BASH_ENV CDPATH
PATH=/usr/bin:/bin
export PATH
login_user=$(/usr/bin/id -un)
host_name=$(/usr/bin/hostname)
machine_id_line=$(/usr/bin/sha256sum /etc/machine-id)
machine_id_sha256=${{machine_id_line%% *}}
deployment_root_realpath=$(/usr/bin/realpath -e {TARGET_DEPLOYMENT_ROOT})
deployment_root_meta=$(/usr/bin/stat -c '%U:%G:%a' {TARGET_DEPLOYMENT_ROOT})
staging_path={STAGING_PATH}
if [ -e "$staging_path" ] || [ -L "$staging_path" ]; then exit 41; fi
for target in /usr/local/libexec/molin/g8-test-readonly-audit /usr/local/libexec/molin/ai-gateway-reconcile /etc/sudoers.d/molin-g8-test-readonly-audit; do
  if [ -e "$target" ] || [ -L "$target" ]; then exit 42; fi
done
printf 'PREFLIGHT_CHANGE_ID=%s\\n' '{CHANGE_ID}'
printf 'LOGIN_USER=%s\\n' "$login_user"
printf 'HOSTNAME=%s\\n' "$host_name"
printf 'MACHINE_ID_SHA256=%s\\n' "$machine_id_sha256"
printf 'DEPLOYMENT_ROOT_REALPATH=%s\\n' "$deployment_root_realpath"
printf 'DEPLOYMENT_ROOT_META=%s\\n' "$deployment_root_meta"
printf 'STAGING_ABSENT=true\\n'
printf 'INSTALL_TARGETS_ABSENT=true\\n'
printf 'PREFLIGHT_RESULT=PASS\\n'
"""


class RemotePreflightError(RuntimeError):
    """表示唯一一次远端只读预检未形成完整有效证据。"""


def sha256(path: Path) -> str:
    """流式计算文件摘要，避免将二进制整体载入内存。"""
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def parse_manifest(path: Path) -> dict[str, str]:
    """严格解析低敏清单，拒绝空行、重复键和非 ASCII 内容。"""
    values: dict[str, str] = {}
    for line in path.read_text(encoding="ascii").splitlines():
        if not re.fullmatch(r"[A-Z0-9_]+=[ -~]+", line):
            raise RuntimeError("invalid_manifest")
        key, value = line.split("=", 1)
        if key in values:
            raise RuntimeError("duplicate_manifest_key")
        values[key] = value
    return values


def validate_candidate(candidate_dir: Path) -> None:
    """在联网前验证候选五文件白名单、清单身份、摘要和大小。"""
    if not candidate_dir.is_absolute() or not candidate_dir.is_dir() or candidate_dir.is_symlink():
        raise RuntimeError("invalid_candidate_directory")
    entries = list(candidate_dir.iterdir())
    if {entry.name for entry in entries} != EXPECTED_FILES:
        raise RuntimeError("candidate_file_set_mismatch")
    if any(not entry.is_file() or entry.is_symlink() for entry in entries):
        raise RuntimeError("invalid_candidate_file")
    if sha256(candidate_dir / "SHA256SUMS") != EXPECTED_BUNDLE_RECEIPT_SHA256:
        raise RuntimeError("bundle_receipt_mismatch")

    expected_checksums: dict[str, str] = {}
    for line in (candidate_dir / "SHA256SUMS").read_text(encoding="ascii").splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9._-]+)", line)
        if not match or match.group(2) in expected_checksums:
            raise RuntimeError("invalid_checksum_manifest")
        expected_checksums[match.group(2)] = match.group(1)
    if set(expected_checksums) != EXPECTED_FILES - {"SHA256SUMS"}:
        raise RuntimeError("checksum_file_set_mismatch")
    for name, expected in expected_checksums.items():
        if sha256(candidate_dir / name) != expected:
            raise RuntimeError("candidate_checksum_mismatch")

    values = parse_manifest(candidate_dir / "manifest.env")
    expected_values = {
        "CHANGE_ID": CHANGE_ID,
        "SOURCE_COMMIT": SOURCE_COMMIT,
        "SOURCE_TREE": SOURCE_TREE,
        "TARGET_DEPLOYMENT_ROOT": TARGET_DEPLOYMENT_ROOT,
        "AUDITOR_SHA256": FROZEN_AUDITOR_SHA256,
        "SUDOERS_SHA256": FROZEN_SUDOERS_SHA256,
        "RECONCILE_SHA256": FROZEN_RECONCILE_SHA256,
        "RECONCILE_SIZE": str(FROZEN_RECONCILE_SIZE),
    }
    if any(values.get(key) != value for key, value in expected_values.items()):
        raise RuntimeError("candidate_identity_mismatch")


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
    fingerprint = "SHA256:" + base64.b64encode(hashlib.sha256(key_blob).digest()).decode("ascii").rstrip("=")
    if fingerprint != TARGET_SSH_ED25519_FINGERPRINT:
        raise RuntimeError("known_host_fingerprint_mismatch")


def public_key_fingerprint(path: Path) -> str:
    """直接从 OpenSSH 公钥 blob 计算 SHA-256 指纹，不调用 PATH 中的工具。"""
    fields = path.read_text(encoding="ascii").strip().split()
    if len(fields) < 2 or fields[0] != "ssh-ed25519":
        raise RuntimeError("invalid_identity_public_key")
    try:
        key_blob = base64.b64decode(fields[1], validate=True)
    except ValueError as error:
        raise RuntimeError("invalid_identity_public_key") from error
    return "SHA256:" + base64.b64encode(hashlib.sha256(key_blob).digest()).decode("ascii").rstrip("=")


def validate_identity_file(identity_file: Path, identity_public_file: Path, known_hosts: Path) -> None:
    """冻结显式密钥对及其目录范围，禁止代理或隐式密钥发现。"""
    if not identity_file.is_absolute() or not identity_file.is_file() or identity_file.is_symlink():
        raise RuntimeError("invalid_identity_file")
    if not identity_public_file.is_absolute() or not identity_public_file.is_file() or identity_public_file.is_symlink():
        raise RuntimeError("invalid_identity_public_file")
    if (
        identity_file.name != "id_ed25519"
        or identity_public_file.name != "id_ed25519.pub"
        or identity_file.parent.resolve() != known_hosts.parent.resolve()
        or identity_public_file.parent.resolve() != known_hosts.parent.resolve()
    ):
        raise RuntimeError("identity_file_scope_mismatch")
    if public_key_fingerprint(identity_public_file) != LOCAL_IDENTITY_ED25519_FINGERPRINT:
        raise RuntimeError("identity_public_fingerprint_mismatch")


def validate_identity_pair(identity_file: Path, identity_public_file: Path) -> None:
    """由固定 ssh-keygen 读取私钥并比对公钥；OpenSSH 同时执行本机私钥 ACL 门禁。"""
    path = (
        Path(r"C:\Windows\System32\OpenSSH\ssh-keygen.exe")
        if os.name == "nt"
        else Path("/usr/bin/ssh-keygen")
    )
    if not path.is_file():
        raise RuntimeError("ssh_keygen_unavailable")
    try:
        result = subprocess.run(
            [str(path), "-y", "-f", str(identity_file)],
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
            encoding="ascii",
            errors="strict",
            timeout=10,
            env=fixed_ssh_environment(),
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired, UnicodeError) as error:
        raise RuntimeError("identity_validation_failed") from error
    expected = identity_public_file.read_text(encoding="ascii").strip().split()[:2]
    actual = result.stdout.strip().split()[:2]
    if result.returncode != 0 or result.stderr or actual != expected:
        raise RuntimeError("identity_pair_mismatch")


def parse_remote_output(stdout: str) -> dict[str, str]:
    """要求远端输出精确键集合，拒绝警告、额外行和重复字段。"""
    values: dict[str, str] = {}
    for line in stdout.splitlines():
        match = re.fullmatch(r"([A-Z0-9_]+)=([^\r\n]+)", line)
        if not match or match.group(1) in values:
            raise RemotePreflightError("invalid_remote_output")
        values[match.group(1)] = match.group(2)
    if set(values) != EXPECTED_REMOTE_KEYS:
        raise RemotePreflightError("remote_key_set_mismatch")
    expected = {
        "PREFLIGHT_CHANGE_ID": CHANGE_ID,
        "LOGIN_USER": "pc",
        "HOSTNAME": TARGET_HOSTNAME,
        "MACHINE_ID_SHA256": TARGET_MACHINE_ID_SHA256,
        "DEPLOYMENT_ROOT_REALPATH": TARGET_DEPLOYMENT_ROOT,
        "STAGING_ABSENT": "true",
        "INSTALL_TARGETS_ABSENT": "true",
        "PREFLIGHT_RESULT": "PASS",
    }
    if any(values.get(key) != value for key, value in expected.items()):
        raise RemotePreflightError("remote_identity_mismatch")
    meta = re.fullmatch(r"pc:pc:([0-7]{3})", values["DEPLOYMENT_ROOT_META"])
    if not meta:
        raise RemotePreflightError("deployment_root_owner_mismatch")
    mode = int(meta.group(1), 8)
    if mode & 0o700 != 0o700 or mode & 0o022:
        raise RemotePreflightError("deployment_root_mode_unsafe")
    return values


def fixed_ssh_executable() -> Path:
    """仅使用操作系统固定 OpenSSH 路径，不信任调用方 PATH。"""
    path = Path(r"C:\Windows\System32\OpenSSH\ssh.exe") if os.name == "nt" else Path("/usr/bin/ssh")
    if not path.is_file():
        raise RuntimeError("ssh_unavailable")
    return path


def fixed_sftp_executable() -> Path:
    """仅使用操作系统固定 SFTP 路径。"""
    path = Path(r"C:\Windows\System32\OpenSSH\sftp.exe") if os.name == "nt" else Path("/usr/bin/sftp")
    if not path.is_file():
        raise RuntimeError("sftp_unavailable")
    return path


def fixed_ssh_environment() -> dict[str, str]:
    """只保留 OpenSSH 加载系统组件所需变量，拒绝代理、AskPass 和配置环境注入。"""
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


def run_remote_preflight(ssh_executable: Path, known_hosts: Path, identity_file: Path) -> dict[str, str]:
    """只启动一次 SSH；失败、超时或任何 stderr 都直接停止且绝不重试。"""
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
        "/bin/sh",
        "-s",
    ]
    try:
        result = subprocess.run(
            command,
            input=REMOTE_SCRIPT,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="strict",
            timeout=20,
            env=fixed_ssh_environment(),
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired, UnicodeError) as error:
        raise RemotePreflightError("ssh_execution_failed") from error
    if result.returncode != 0 or result.stderr:
        raise RemotePreflightError("ssh_preflight_failed")
    return parse_remote_output(result.stdout)


def run_atomic_sftp_upload(
    sftp_executable: Path, known_hosts: Path, identity_file: Path, candidate_dir: Path
) -> None:
    """以单次 SFTP 原子创建暂存目录；目录已存在时批处理立即失败，不合并也不覆盖。"""
    batch = "\n".join(
        (
            f"mkdir {STAGING_PATH}",
            f"chmod 700 {STAGING_PATH}",
            *(f"put {name} {STAGING_PATH}/{name}" for name in sorted(EXPECTED_FILES)),
            "quit",
        )
    ) + "\n"
    command = [
        str(sftp_executable),
        "-b",
        "-",
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
        "-P",
        TARGET_PORT,
        TARGET,
    ]
    try:
        result = subprocess.run(
            command,
            cwd=candidate_dir,
            input=batch,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="strict",
            timeout=30,
            env=fixed_ssh_environment(),
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired, UnicodeError) as error:
        raise RemotePreflightError("sftp_execution_failed") from error
    if result.returncode != 0:
        raise RemotePreflightError("sftp_upload_failed")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--local-check", action="store_true")
    parser.add_argument("--change-id")
    parser.add_argument("--candidate-dir")
    parser.add_argument("--known-hosts")
    parser.add_argument("--identity-file")
    parser.add_argument("--identity-public-file")
    arguments = parser.parse_args()
    if arguments.self_test:
        if "cut" in REMOTE_SCRIPT or "${machine_id_line%% *}" not in REMOTE_SCRIPT:
            print("G8_TEST_READONLY_ACCESS_STAGE=FAILED reason=unsafe_remote_script")
            return 2
        print("G8_TEST_READONLY_ACCESS_STAGE_SELF_TEST=PASS")
        return 0
    # 003 的唯一远端执行机会已消费；必须在读取候选或调用 SSH/SFTP 前拒绝重放。
    if arguments.change_id == CHANGE_ID:
        print("G8_TEST_READONLY_ACCESS_STAGE=FAILED reason=change_id_consumed")
        return 2
    try:
        if (
            arguments.change_id != CHANGE_ID
            or not arguments.candidate_dir
            or not arguments.known_hosts
            or not arguments.identity_file
            or not arguments.identity_public_file
        ):
            raise RuntimeError("invalid_arguments")
        candidate_dir = Path(arguments.candidate_dir)
        known_hosts = Path(arguments.known_hosts)
        identity_file = Path(arguments.identity_file)
        identity_public_file = Path(arguments.identity_public_file)
        validate_candidate(candidate_dir)
        validate_known_hosts(known_hosts)
        validate_identity_file(identity_file, identity_public_file, known_hosts)
        validate_identity_pair(identity_file, identity_public_file)
        if arguments.local_check:
            print("G8_TEST_READONLY_ACCESS_STAGE_LOCAL_CHECK=PASS")
            return 0
        values = run_remote_preflight(fixed_ssh_executable(), known_hosts, identity_file)
        run_atomic_sftp_upload(fixed_sftp_executable(), known_hosts, identity_file, candidate_dir)
    except RemotePreflightError:
        print("G8_TEST_READONLY_ACCESS_STAGE=FAILED reason=remote_stage_failed")
        return 2
    except Exception:
        print("G8_TEST_READONLY_ACCESS_STAGE=FAILED reason=invalid_request")
        return 2
    print("G8_TEST_READONLY_ACCESS_STAGE=PASS")
    print(f"change_id={CHANGE_ID}")
    print(f"target={TARGET}:{TARGET_PORT}")
    print(f"deployment_root_meta={values['DEPLOYMENT_ROOT_META']}")
    print("staging_absent=true")
    print("install_targets_absent=true")
    print("staging_uploaded=true")
    return 0


if __name__ == "__main__":
    sys.exit(main())
