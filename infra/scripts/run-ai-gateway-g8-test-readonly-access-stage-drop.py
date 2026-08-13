#!/usr/bin/env python3
"""为 009 Drop 候选执行本地门禁、一次只读 SSH 预检和一次原子 SFTP 暂存。"""

import sys

# 必须在加载任何可被脚本目录或 PYTHONPATH 替换的模块前拒绝非隔离解释器。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_ACCESS_STAGE_DROP=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import base64
import hashlib
import os
import re
import shutil
import subprocess
import tempfile
import threading
from pathlib import Path


CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009"
CHANGE_ID_CONSUMED = False
SOURCE_COMMIT = "7f3325e2d6801567fea34a2049a2f3ada114e348"
SOURCE_TREE = "4563feb59850dca87789adfb5eea820f78b1a209"
TARGET = "pc@8.130.9.163"
TARGET_HOST = "8.130.9.163"
TARGET_PORT = "10003"
TARGET_TRANSPORT = "DROP_SSH"
PHYSICAL_HOST_IDENTITY = "NOT_APPLICABLE"
TARGET_SSH_ED25519_FINGERPRINT = "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"
LOCAL_IDENTITY_ED25519_FINGERPRINT = "SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0"
TARGET_DEPLOYMENT_ROOT = "/home/pc/molin"
STAGING_PATH = "/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009"
EXPECTED_BUNDLE_RECEIPT_SHA256 = "840bdbed48edab6d70d351fa232b7426903bf3f3098f682e2884f513b9cd0efd"
FROZEN_AUDITOR_SHA256 = "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256"
FROZEN_SUDOERS_SHA256 = "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f"
FROZEN_RECONCILE_SHA256 = "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1"
FROZEN_RECONCILE_SIZE = 13_066_129
MAX_CAPTURE_BYTES = 64 * 1024
EXPECTED_FILES = {
    "SHA256SUMS",
    "ai-gateway-reconcile",
    "g8-test-readonly-audit",
    "manifest.env",
    "molin-g8-test-readonly-audit.sudoers",
}
EXPECTED_MANIFEST_KEYS = {
    "BUNDLE_FORMAT_VERSION",
    "CHANGE_ID",
    "SOURCE_COMMIT",
    "SOURCE_TREE",
    "GO_VERSION",
    "GO_BUILDER_HOST",
    "GOOS",
    "GOARCH",
    "CGO_ENABLED",
    "GO_BUILD_FLAGS",
    "AUDITOR_SHA256",
    "SUDOERS_SHA256",
    "RECONCILE_SHA256",
    "RECONCILE_SIZE",
    "REPRODUCIBLE_BUILD_COUNT",
    "TARGET_SSH",
    "TARGET_SSH_ED25519_FINGERPRINT",
    "TARGET_TRANSPORT",
    "PHYSICAL_HOST_IDENTITY",
    "TARGET_DEPLOYMENT_ROOT",
}
EXPECTED_REMOTE_KEYS = {
    "PREFLIGHT_CHANGE_ID",
    "LOGIN_USER",
    "LOGIN_GROUP",
    "DEPLOYMENT_ROOT_REALPATH",
    "DEPLOYMENT_ROOT_META",
    "STAGING_ABSENT",
    "INSTALL_TARGETS_ABSENT",
    "PREFLIGHT_RESULT",
}

# Drop 入口只核对登录身份、部署根和目标不存在；不读取物理 hostname 或 machine-id。
REMOTE_SCRIPT = f"""set -eu
unset ENV BASH_ENV CDPATH
PATH=/usr/bin:/bin
export PATH
login_user=$(/usr/bin/id -un)
login_group=$(/usr/bin/id -gn)
deployment_root_realpath=$(/usr/bin/realpath -e {TARGET_DEPLOYMENT_ROOT})
deployment_root_meta=$(/usr/bin/stat -c '%U:%G:%a:%F' {TARGET_DEPLOYMENT_ROOT})
staging_path={STAGING_PATH}
if [ -e "$staging_path" ] || [ -L "$staging_path" ]; then exit 41; fi
for target in /usr/local/libexec/molin/g8-test-readonly-audit /usr/local/libexec/molin/ai-gateway-reconcile /etc/sudoers.d/molin-g8-test-readonly-audit; do
  if [ -e "$target" ] || [ -L "$target" ]; then exit 42; fi
done
printf 'PREFLIGHT_CHANGE_ID=%s\n' '{CHANGE_ID}'
printf 'LOGIN_USER=%s\n' "$login_user"
printf 'LOGIN_GROUP=%s\n' "$login_group"
printf 'DEPLOYMENT_ROOT_REALPATH=%s\n' "$deployment_root_realpath"
printf 'DEPLOYMENT_ROOT_META=%s\n' "$deployment_root_meta"
printf 'STAGING_ABSENT=true\n'
printf 'INSTALL_TARGETS_ABSENT=true\n'
printf 'PREFLIGHT_RESULT=PASS\n'
"""


class RemoteStageError(RuntimeError):
    """表示唯一远端步骤没有形成完整、低敏且可信的证据。"""


class SafeArgumentParser(argparse.ArgumentParser):
    """拒绝 argparse 回显调用方路径或其他参数。"""

    def error(self, message: str) -> None:
        raise RuntimeError("invalid_arguments")


def sha256(path: Path) -> str:
    """流式计算文件摘要，避免把二进制整体载入内存。"""
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def captured_result(value: bytes) -> dict[str, object]:
    """为测试和固定小输出建立与有界采集一致的统计结构。"""
    return {
        "captured": value[: MAX_CAPTURE_BYTES + 1],
        "bytes": len(value),
        "exceeded": len(value) > MAX_CAPTURE_BYTES,
        "error": False,
    }


def collect_stream(stream, result: dict[str, object]) -> None:
    """持续排空单个管道，同时只保留固定上限正文。"""
    captured = bytearray()
    total = 0
    try:
        while True:
            chunk = stream.read(8192)
            if not chunk:
                break
            total += len(chunk)
            if len(captured) <= MAX_CAPTURE_BYTES:
                captured.extend(chunk[: MAX_CAPTURE_BYTES + 1 - len(captured)])
    except Exception:
        result["error"] = True
        return
    result.update(
        {
            "captured": bytes(captured),
            "bytes": total,
            "exceeded": total > MAX_CAPTURE_BYTES,
            "error": False,
        }
    )


def run_bounded_process(
    command: list[str],
    environment: dict[str, str],
    *,
    input_data: bytes,
    timeout: int,
    cwd: Path | None = None,
) -> tuple[int, dict[str, object], dict[str, object]]:
    """单次启动子进程，并发有界排空 stdout 与 stderr。"""
    try:
        process = subprocess.Popen(
            command,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=environment,
            cwd=cwd,
        )
    except OSError as error:
        raise RemoteStageError("process_start_failed") from error
    if process.stdin is None or process.stdout is None or process.stderr is None:
        process.kill()
        raise RemoteStageError("process_pipe_failed")
    stdout_result: dict[str, object] = {}
    stderr_result: dict[str, object] = {}
    stdout_thread = threading.Thread(target=collect_stream, args=(process.stdout, stdout_result), daemon=True)
    stderr_thread = threading.Thread(target=collect_stream, args=(process.stderr, stderr_result), daemon=True)
    stdout_thread.start()
    stderr_thread.start()
    try:
        process.stdin.write(input_data)
        process.stdin.close()
        returncode = process.wait(timeout=timeout)
    except (OSError, subprocess.TimeoutExpired) as error:
        process.kill()
        process.wait()
        raise RemoteStageError("process_execution_failed") from error
    finally:
        stdout_thread.join()
        stderr_thread.join()
        process.stdout.close()
        process.stderr.close()
    if stdout_result.get("error") or stderr_result.get("error"):
        raise RemoteStageError("process_pipe_failed")
    return returncode, stdout_result, stderr_result


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
    """联网前验证候选五文件白名单、清单身份、摘要和大小。"""
    if not candidate_dir.is_absolute() or not candidate_dir.is_dir() or candidate_dir.is_symlink():
        raise RuntimeError("invalid_candidate_directory")
    entries = list(candidate_dir.iterdir())
    if {entry.name for entry in entries} != EXPECTED_FILES:
        raise RuntimeError("candidate_file_set_mismatch")
    if any(not entry.is_file() or entry.is_symlink() for entry in entries):
        raise RuntimeError("invalid_candidate_file")
    if sha256(candidate_dir / "SHA256SUMS") != EXPECTED_BUNDLE_RECEIPT_SHA256:
        raise RuntimeError("bundle_receipt_mismatch")
    checksums: dict[str, str] = {}
    for line in (candidate_dir / "SHA256SUMS").read_text(encoding="ascii").splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9._-]+)", line)
        if not match or match.group(2) in checksums:
            raise RuntimeError("invalid_checksum_manifest")
        checksums[match.group(2)] = match.group(1)
    if set(checksums) != EXPECTED_FILES - {"SHA256SUMS"}:
        raise RuntimeError("checksum_file_set_mismatch")
    if any(sha256(candidate_dir / name) != digest for name, digest in checksums.items()):
        raise RuntimeError("candidate_checksum_mismatch")
    values = parse_manifest(candidate_dir / "manifest.env")
    if set(values) != EXPECTED_MANIFEST_KEYS:
        raise RuntimeError("manifest_key_set_mismatch")
    expected_values = {
        "CHANGE_ID": CHANGE_ID,
        "SOURCE_COMMIT": SOURCE_COMMIT,
        "SOURCE_TREE": SOURCE_TREE,
        "TARGET_TRANSPORT": TARGET_TRANSPORT,
        "PHYSICAL_HOST_IDENTITY": PHYSICAL_HOST_IDENTITY,
        "TARGET_DEPLOYMENT_ROOT": TARGET_DEPLOYMENT_ROOT,
        "TARGET_SSH": f"pc@{TARGET_HOST}:{TARGET_PORT}",
        "TARGET_SSH_ED25519_FINGERPRINT": TARGET_SSH_ED25519_FINGERPRINT,
        "AUDITOR_SHA256": FROZEN_AUDITOR_SHA256,
        "SUDOERS_SHA256": FROZEN_SUDOERS_SHA256,
        "RECONCILE_SHA256": FROZEN_RECONCILE_SHA256,
        "RECONCILE_SIZE": str(FROZEN_RECONCILE_SIZE),
    }
    if any(values.get(key) != value for key, value in expected_values.items()):
        raise RuntimeError("candidate_identity_mismatch")
    if "TARGET_HOSTNAME" in values or "TARGET_MACHINE_ID_SHA256" in values:
        raise RuntimeError("physical_identity_not_allowed")


def validate_known_hosts(known_hosts: Path) -> None:
    """只接受目标端点唯一的明文 ED25519 条目并核对冻结指纹。"""
    if not known_hosts.is_absolute() or not known_hosts.is_file() or known_hosts.is_symlink():
        raise RuntimeError("invalid_known_hosts")
    prefix = f"[{TARGET_HOST}]:{TARGET_PORT} ssh-ed25519 "
    matches = [line for line in known_hosts.read_text(encoding="ascii").splitlines() if line.startswith(prefix)]
    if len(matches) != 1:
        raise RuntimeError("known_host_entry_mismatch")
    fields = matches[0].split()
    if len(fields) < 3:
        raise RuntimeError("invalid_known_host_entry")
    try:
        key_blob = base64.b64decode(fields[2], validate=True)
    except ValueError as error:
        raise RuntimeError("invalid_known_host_key") from error
    fingerprint = "SHA256:" + base64.b64encode(hashlib.sha256(key_blob).digest()).decode("ascii").rstrip("=")
    if fingerprint != TARGET_SSH_ED25519_FINGERPRINT:
        raise RuntimeError("known_host_fingerprint_mismatch")


def public_key_fingerprint(path: Path) -> str:
    """直接从 OpenSSH 公钥计算 SHA-256 指纹。"""
    fields = path.read_text(encoding="ascii").strip().split()
    if len(fields) < 2 or fields[0] != "ssh-ed25519":
        raise RuntimeError("invalid_identity_public_key")
    try:
        key_blob = base64.b64decode(fields[1], validate=True)
    except ValueError as error:
        raise RuntimeError("invalid_identity_public_key") from error
    return "SHA256:" + base64.b64encode(hashlib.sha256(key_blob).digest()).decode("ascii").rstrip("=")


def fixed_ssh_environment() -> dict[str, str]:
    """只保留 OpenSSH 加载系统组件所需变量，拒绝代理和 AskPass 注入。"""
    environment = {
        "LANG": "C.UTF-8",
        "PATH": r"C:\Windows\System32\OpenSSH;C:\Windows\System32" if os.name == "nt" else "/usr/bin:/bin",
    }
    for name in (
        "SYSTEMROOT", "WINDIR", "SYSTEMDRIVE", "COMSPEC", "PROGRAMDATA", "PROGRAMFILES",
        "PROGRAMFILES(X86)", "USERPROFILE", "LOCALAPPDATA", "HOMEDRIVE", "HOMEPATH",
        "USERNAME", "USERDOMAIN", "TEMP", "TMP",
    ):
        if os.environ.get(name):
            environment[name] = os.environ[name]
    return environment


def fixed_tool(name: str) -> Path:
    """只接受系统固定 OpenSSH 路径，不信任调用方 PATH。"""
    if os.name == "nt":
        path = Path(rf"C:\Windows\System32\OpenSSH\{name}.exe")
    else:
        path = Path(f"/usr/bin/{name}")
    if not path.is_file():
        raise RuntimeError("ssh_tool_unavailable")
    return path


def validate_identity_files(identity_file: Path, identity_public_file: Path, known_hosts: Path) -> None:
    """冻结显式密钥对及其目录范围，禁止代理或隐式密钥发现。"""
    for path in (identity_file, identity_public_file):
        if not path.is_absolute() or not path.is_file() or path.is_symlink():
            raise RuntimeError("invalid_identity_file")
    if (
        identity_file.name != "id_ed25519"
        or identity_public_file.name != "id_ed25519.pub"
        or identity_file.parent.resolve() != known_hosts.parent.resolve()
        or identity_public_file.parent.resolve() != known_hosts.parent.resolve()
        or public_key_fingerprint(identity_public_file) != LOCAL_IDENTITY_ED25519_FINGERPRINT
    ):
        raise RuntimeError("identity_scope_mismatch")
    result = subprocess.run(
        [str(fixed_tool("ssh-keygen")), "-y", "-f", str(identity_file)],
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
        encoding="ascii",
        errors="strict",
        timeout=10,
        env=fixed_ssh_environment(),
        check=False,
    )
    expected = identity_public_file.read_text(encoding="ascii").strip().split()[:2]
    actual = result.stdout.strip().split()[:2]
    if result.returncode != 0 or result.stderr or actual != expected:
        raise RuntimeError("identity_pair_mismatch")


def validate_local_inputs(
    candidate_dir: Path,
    known_hosts: Path,
    identity_file: Path,
    identity_public_file: Path,
) -> None:
    """按固定顺序完成全部本地门禁，成功前不得发起网络连接。"""
    validate_candidate(candidate_dir)
    validate_known_hosts(known_hosts)
    validate_identity_files(identity_file, identity_public_file, known_hosts)


def create_frozen_candidate_snapshot(candidate_dir: Path, snapshot_dir: Path) -> None:
    """在联网前复制并复核五文件快照，SFTP 只读取不再变化的受控临时目录。"""
    snapshot_dir.mkdir(mode=0o700)
    for name in sorted(EXPECTED_FILES):
        source = candidate_dir / name
        target = snapshot_dir / name
        with source.open("rb") as source_handle, target.open("xb") as target_handle:
            shutil.copyfileobj(source_handle, target_handle, length=1024 * 1024)
    validate_candidate(snapshot_dir)


def create_frozen_local_snapshot(
    candidate_dir: Path,
    known_hosts: Path,
    identity_file: Path,
    identity_public_file: Path,
    snapshot_root: Path,
) -> tuple[Path, Path, Path, Path]:
    """冻结候选和 SSH 信任材料，避免远程步骤再次按调用方可变路径读取。"""
    candidate_snapshot = snapshot_root / "candidate"
    trust_snapshot = snapshot_root / "ssh"
    create_frozen_candidate_snapshot(candidate_dir, candidate_snapshot)
    trust_snapshot.mkdir(mode=0o700)
    frozen_known_hosts = trust_snapshot / "known_hosts"
    frozen_identity = trust_snapshot / "id_ed25519"
    frozen_identity_public = trust_snapshot / "id_ed25519.pub"
    for source, target in (
        (known_hosts, frozen_known_hosts),
        (identity_file, frozen_identity),
        (identity_public_file, frozen_identity_public),
    ):
        with source.open("rb") as source_handle, target.open("xb") as target_handle:
            shutil.copyfileobj(source_handle, target_handle, length=1024 * 1024)
    # OpenSSH 对私钥权限敏感；临时副本仅授予当前账户读写权限。
    os.chmod(frozen_identity, 0o600)
    os.chmod(frozen_known_hosts, 0o600)
    os.chmod(frozen_identity_public, 0o600)
    validate_local_inputs(
        candidate_snapshot,
        frozen_known_hosts,
        frozen_identity,
        frozen_identity_public,
    )
    return candidate_snapshot, frozen_known_hosts, frozen_identity, frozen_identity_public


def parse_remote_output(stdout: bytes) -> dict[str, str]:
    """要求远端输出精确键集合，拒绝额外行、重复字段和身份漂移。"""
    try:
        text = stdout.decode("ascii")
    except UnicodeError as error:
        raise RemoteStageError("invalid_remote_output") from error
    values: dict[str, str] = {}
    for line in text.splitlines():
        match = re.fullmatch(r"([A-Z0-9_]+)=([ -~]+)", line)
        if not match or match.group(1) in values:
            raise RemoteStageError("invalid_remote_output")
        values[match.group(1)] = match.group(2)
    if set(values) != EXPECTED_REMOTE_KEYS:
        raise RemoteStageError("remote_key_set_mismatch")
    expected = {
        "PREFLIGHT_CHANGE_ID": CHANGE_ID,
        "LOGIN_USER": "pc",
        "LOGIN_GROUP": "pc",
        "DEPLOYMENT_ROOT_REALPATH": TARGET_DEPLOYMENT_ROOT,
        "STAGING_ABSENT": "true",
        "INSTALL_TARGETS_ABSENT": "true",
        "PREFLIGHT_RESULT": "PASS",
    }
    if any(values.get(key) != value for key, value in expected.items()):
        raise RemoteStageError("remote_contract_mismatch")
    meta = re.fullmatch(r"pc:pc:([0-7]{3,4}):directory", values["DEPLOYMENT_ROOT_META"])
    if not meta:
        raise RemoteStageError("deployment_root_metadata_mismatch")
    mode = int(meta.group(1), 8)
    if mode & 0o700 != 0o700 or mode & 0o022:
        raise RemoteStageError("deployment_root_mode_unsafe")
    return values


def ssh_options(known_hosts: Path, identity_file: Path) -> list[str]:
    """集中冻结 SSH 与 SFTP 共用的无交互、无转发参数。"""
    return [
        "-F", "none", "-o", "BatchMode=yes", "-o", "NumberOfPasswordPrompts=0",
        "-o", "ConnectionAttempts=1", "-o", "StrictHostKeyChecking=yes",
        "-o", f"UserKnownHostsFile={known_hosts}", "-o", "IdentitiesOnly=yes",
        "-o", f"IdentityFile={identity_file}", "-o", "PreferredAuthentications=publickey",
        "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no",
        "-o", "ClearAllForwardings=yes", "-o", "ForwardAgent=no", "-o", "ForwardX11=no",
        "-o", "PermitLocalCommand=no", "-o", "RequestTTY=no", "-o", "ConnectTimeout=10",
    ]


def run_remote_preflight(ssh_executable: Path, known_hosts: Path, identity_file: Path) -> dict[str, str]:
    """仅启动一次只读 SSH；非零、stderr、超限或契约漂移均立即停止。"""
    command = [
        str(ssh_executable),
        *ssh_options(known_hosts, identity_file),
        "-p", TARGET_PORT, TARGET,
        "/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "/bin/sh", "-s",
    ]
    returncode, stdout_result, stderr_result = run_bounded_process(
        command,
        fixed_ssh_environment(),
        input_data=REMOTE_SCRIPT.encode("ascii"),
        timeout=20,
    )
    if (
        returncode != 0
        or stderr_result["bytes"] != 0
        or stdout_result["exceeded"]
        or stderr_result["exceeded"]
    ):
        raise RemoteStageError("ssh_preflight_failed")
    return parse_remote_output(stdout_result["captured"])


def run_atomic_sftp_upload(
    sftp_executable: Path,
    known_hosts: Path,
    identity_file: Path,
    candidate_dir: Path,
) -> None:
    """以一次 SFTP 独占创建暂存目录；已存在即失败，不合并也不覆盖。"""
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
        "-q",
        "-b", "-",
        *ssh_options(known_hosts, identity_file),
        "-P", TARGET_PORT,
        TARGET,
    ]
    returncode, stdout_result, stderr_result = run_bounded_process(
        command,
        fixed_ssh_environment(),
        input_data=batch.encode("ascii"),
        timeout=30,
        cwd=candidate_dir,
    )
    if (
        returncode != 0
        or stderr_result["bytes"] != 0
        or stdout_result["exceeded"]
        or stderr_result["exceeded"]
    ):
        raise RemoteStageError("sftp_upload_failed")


def main() -> int:
    """先完成本地检查，再按独立授权执行一次 SSH 与一次 SFTP。"""
    parser = SafeArgumentParser(add_help=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--local-check", action="store_true")
    parser.add_argument("--change-id")
    parser.add_argument("--candidate-dir")
    parser.add_argument("--known-hosts")
    parser.add_argument("--identity-file")
    parser.add_argument("--identity-public-file")
    try:
        arguments = parser.parse_args()
    except RuntimeError:
        print("G8_TEST_READONLY_ACCESS_STAGE_DROP=FAILED reason=invalid_request")
        return 2
    if CHANGE_ID_CONSUMED:
        print("G8_TEST_READONLY_ACCESS_STAGE_DROP=FAILED reason=change_id_consumed")
        return 2
    if arguments.self_test:
        if (
            "hostname" in REMOTE_SCRIPT
            or "machine-id" in REMOTE_SCRIPT
            or REMOTE_SCRIPT.count("PREFLIGHT_RESULT=PASS") != 1
        ):
            print("G8_TEST_READONLY_ACCESS_STAGE_DROP=FAILED reason=self_test_failed")
            return 2
        print("G8_TEST_READONLY_ACCESS_STAGE_DROP_SELF_TEST=PASS")
        return 0
    if arguments.change_id != CHANGE_ID:
        print("G8_TEST_READONLY_ACCESS_STAGE_DROP=FAILED reason=invalid_request")
        return 2
    try:
        if not all(
            (
                arguments.candidate_dir,
                arguments.known_hosts,
                arguments.identity_file,
                arguments.identity_public_file,
            )
        ):
            raise RuntimeError("missing_argument")
        candidate_dir = Path(arguments.candidate_dir)
        known_hosts = Path(arguments.known_hosts)
        identity_file = Path(arguments.identity_file)
        identity_public_file = Path(arguments.identity_public_file)
        validate_local_inputs(candidate_dir, known_hosts, identity_file, identity_public_file)
        if arguments.local_check:
            print("G8_TEST_READONLY_ACCESS_STAGE_DROP_LOCAL_CHECK=PASS")
            return 0
        with tempfile.TemporaryDirectory(prefix="molin-g8-access-stage-drop-") as temporary:
            snapshot_root = Path(temporary)
            snapshot_dir, frozen_known_hosts, frozen_identity, frozen_identity_public = (
                create_frozen_local_snapshot(
                    candidate_dir,
                    known_hosts,
                    identity_file,
                    identity_public_file,
                    snapshot_root,
                )
            )
            values = run_remote_preflight(fixed_tool("ssh"), frozen_known_hosts, frozen_identity)
            # SSH 与 SFTP 之间再次核对原输入和冻结副本；远程进程不再读取调用方可变路径。
            validate_local_inputs(candidate_dir, known_hosts, identity_file, identity_public_file)
            validate_local_inputs(
                snapshot_dir,
                frozen_known_hosts,
                frozen_identity,
                frozen_identity_public,
            )
            run_atomic_sftp_upload(
                fixed_tool("sftp"),
                frozen_known_hosts,
                frozen_identity,
                snapshot_dir,
            )
            # SFTP 返回后复核原输入与冻结副本，传输期间任何持久漂移都不得形成成功回执。
            validate_local_inputs(candidate_dir, known_hosts, identity_file, identity_public_file)
            validate_local_inputs(
                snapshot_dir,
                frozen_known_hosts,
                frozen_identity,
                frozen_identity_public,
            )
    except RemoteStageError as error:
        reason = "remote_preflight_failed" if str(error) != "sftp_upload_failed" else "sftp_upload_failed"
        print(f"G8_TEST_READONLY_ACCESS_STAGE_DROP=FAILED reason={reason}")
        return 2
    except Exception:
        print("G8_TEST_READONLY_ACCESS_STAGE_DROP=FAILED reason=invalid_request")
        return 2
    print("G8_TEST_READONLY_ACCESS_STAGE_DROP=PASS")
    print(f"change_id={CHANGE_ID}")
    print(f"target={TARGET}:{TARGET_PORT}")
    print(f"deployment_root_meta={values['DEPLOYMENT_ROOT_META']}")
    print("staging_absent=true")
    print("install_targets_absent=true")
    print("staging_uploaded=true")
    print("business_requests=0 upstream_requests=0 cost_cny=0")
    return 0


if __name__ == "__main__":
    sys.exit(main())
