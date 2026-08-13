#!/usr/bin/env python3
"""对 Drop 映射入口执行一次完全只读的 011 暂存低敏取证。"""

from __future__ import annotations

import sys


if not sys.flags.isolated:
    print(
        "G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012=FAILED "
        "reason=isolated_python_required"
    )
    raise SystemExit(2)


import argparse
import base64
import hashlib
import os
import re
import stat
import subprocess
import tempfile
import textwrap
import threading
from dataclasses import dataclass
from pathlib import Path
from typing import BinaryIO


CHANGE_ID = "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-012"
CHANGE_ID_CONSUMED = False
TARGET_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
TARGET_DEPLOYMENT_ROOT = "/home/pc/molin"
STAGING_PATH = (
    "/home/pc/molin/"
    ".g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
)
EXPECTED_REMOTE_KEYS = frozenset(
    {
        "EVIDENCE_CHANGE_ID",
        "TARGET_CHANGE_ID",
        "LOGIN_USER",
        "DEPLOYMENT_ROOT_REALPATH",
        "DEPLOYMENT_ROOT_CHECK",
        "STAGING_STATE",
        "STAGING_INTEGRITY",
        "STAGING_MISMATCH_REASON",
        "EVIDENCE_RESULT",
    }
)
VALID_STATES = {
    ("ABSENT", "NOT_APPLICABLE", "NONE"),
    ("PRESENT", "PASS", "NONE"),
    *(("PRESENT", "MISMATCH", reason) for reason in (
        "PATH",
        "FILE_SET",
        "FILE_METADATA",
        "FILE_CONTENT",
        "MANIFEST",
        "RECEIPT",
        "READ_ERROR",
    )),
}
FROZEN_FILES = {
    "SHA256SUMS": (
        "15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f",
        362,
        0o600,
    ),
    "ai-gateway-reconcile": (
        "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1",
        13_066_129,
        0o700,
    ),
    "g8-test-readonly-audit": (
        "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256",
        18_377,
        0o700,
    ),
    "manifest.env": (
        "763c71547443a125b434071895b9a532fd966896e4ba9486b1c6b80f1541f3c6",
        863,
        0o600,
    ),
    "molin-g8-test-readonly-audit.sudoers": (
        "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f",
        416,
        0o600,
    ),
}
FROZEN_CHECKSUMS = {
    name: FROZEN_FILES[name][0]
    for name in (
        "ai-gateway-reconcile",
        "g8-test-readonly-audit",
        "manifest.env",
        "molin-g8-test-readonly-audit.sudoers",
    )
}
FROZEN_MANIFEST = {
    "BUNDLE_FORMAT_VERSION": "1",
    "CHANGE_ID": TARGET_CHANGE_ID,
    "SOURCE_COMMIT": "099c38ed62ccd62c3c5a3b6811f1369d7f0d3084",
    "SOURCE_TREE": "c2d1252a05d031d842549345128fa7a1ffe53dc8",
    "GO_VERSION": "go1.26.5",
    "GO_BUILDER_HOST": "windows/amd64",
    "GOOS": "linux",
    "GOARCH": "amd64",
    "CGO_ENABLED": "0",
    "GO_BUILD_FLAGS": "-trimpath,-buildvcs=false",
    "AUDITOR_SHA256": FROZEN_FILES["g8-test-readonly-audit"][0],
    "SUDOERS_SHA256": FROZEN_FILES["molin-g8-test-readonly-audit.sudoers"][0],
    "RECONCILE_SHA256": FROZEN_FILES["ai-gateway-reconcile"][0],
    "RECONCILE_SIZE": "13066129",
    "REPRODUCIBLE_BUILD_COUNT": "2",
    "TARGET_SSH": "pc@8.130.9.163:10003",
    "TARGET_SSH_ED25519_FINGERPRINT": (
        "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"
    ),
    "TARGET_TRANSPORT": "DROP_SSH_INTERACTIVE_SUDO",
    "PHYSICAL_HOST_IDENTITY": "NOT_APPLICABLE",
    "TARGET_DEPLOYMENT_ROOT": TARGET_DEPLOYMENT_ROOT,
}
TARGET_HOST = "8.130.9.163"
TARGET_PORT = 10003
TARGET_LOGIN = "pc"
TARGET_HOST_ALIAS = f"[{TARGET_HOST}]:{TARGET_PORT}"
TARGET_HOST_FINGERPRINT = "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"
LOCAL_IDENTITY_FINGERPRINT = "SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0"
STREAM_LIMIT = 64 * 1024
FIXED_SSH = Path("C:/Windows/System32/OpenSSH/ssh.exe") if os.name == "nt" else Path("/usr/bin/ssh")
FIXED_SSH_KEYGEN = Path("C:/Windows/System32/OpenSSH/ssh-keygen.exe") if os.name == "nt" else Path("/usr/bin/ssh-keygen")


class EvidenceError(RuntimeError):
    """表示没有形成完整、低敏且可验证的 012 证据。"""


class SafeArgumentParser(argparse.ArgumentParser):
    """把参数错误折叠为固定异常，禁止回显调用方输入。"""

    def error(self, _message: str) -> None:
        raise EvidenceError("invalid_request")


@dataclass(frozen=True)
class FileEvidence:
    """冻结本地普通文件的身份、元数据和完整摘要。"""

    path: Path
    device: int
    inode: int
    mode: int
    size: int
    mtime_ns: int
    ctime_ns: int
    sha256: str


@dataclass(frozen=True)
class LocalInputs:
    """保存正式调用前已验证的系统工具和客户端身份材料。"""

    ssh: FileEvidence
    ssh_keygen: FileEvidence
    known_hosts: FileEvidence
    identity_file: FileEvidence
    identity_public_file: FileEvidence
    approved_known_hosts_line: str


@dataclass(frozen=True)
class StreamCapture:
    """保存有界正文和完整流的低敏统计，不保存超限正文。"""

    data: bytes
    byte_count: int
    line_count: int
    sha256: str
    exceeded: bool
    error: bool


def collect_stream(stream: BinaryIO, limit: int) -> StreamCapture:
    """持续排空一个二进制流，只保留上限加一字节并统计完整摘要。"""

    retained = bytearray()
    digest = hashlib.sha256()
    byte_count = 0
    newline_count = 0
    last_byte: int | None = None
    error = False
    try:
        while True:
            chunk = stream.read(8192)
            if not chunk:
                break
            if not isinstance(chunk, bytes):
                raise TypeError("binary_stream_required")
            byte_count += len(chunk)
            newline_count += chunk.count(b"\n")
            last_byte = chunk[-1]
            digest.update(chunk)
            remaining = limit + 1 - len(retained)
            if remaining > 0:
                retained.extend(chunk[:remaining])
    except Exception:
        # 采集线程不得向 stderr 打印 traceback；调用方只看到固定低敏失败。
        error = True

    line_count = newline_count
    if byte_count and last_byte != ord("\n"):
        line_count += 1
    return StreamCapture(
        data=bytes(retained),
        byte_count=byte_count,
        line_count=line_count,
        sha256=digest.hexdigest(),
        exceeded=byte_count > limit,
        error=error,
    )


def freeze_file(path: Path) -> FileEvidence:
    """读取同一普通非链接文件并确认读取期间没有发生身份漂移。"""

    resolved = path.absolute()
    try:
        before = resolved.lstat()
        if not stat.S_ISREG(before.st_mode) or resolved.is_symlink():
            raise EvidenceError("local_input_type_mismatch")
        if getattr(before, "st_file_attributes", 0) & 0x400:
            raise EvidenceError("local_input_reparse_point")
        digest = hashlib.sha256()
        with resolved.open("rb") as stream:
            while chunk := stream.read(8192):
                digest.update(chunk)
        after = resolved.lstat()
    except EvidenceError:
        raise
    except (OSError, ValueError) as error:
        raise EvidenceError("local_input_unavailable") from error
    identity_before = (
        before.st_dev, before.st_ino, before.st_mode, before.st_size,
        before.st_mtime_ns, before.st_ctime_ns,
    )
    identity_after = (
        after.st_dev, after.st_ino, after.st_mode, after.st_size,
        after.st_mtime_ns, after.st_ctime_ns,
    )
    if identity_before != identity_after:
        raise EvidenceError("local_input_drift")
    return FileEvidence(
        path=resolved,
        device=before.st_dev,
        inode=before.st_ino,
        mode=before.st_mode,
        size=before.st_size,
        mtime_ns=before.st_mtime_ns,
        ctime_ns=before.st_ctime_ns,
        sha256=digest.hexdigest(),
    )


def assert_file_unchanged(evidence: FileEvidence) -> None:
    """重新冻结路径并精确比较全部身份、元数据和摘要。"""

    if freeze_file(evidence.path) != evidence:
        raise EvidenceError("local_input_drift")


def ssh_fingerprint(public_key_line: str) -> str:
    """直接从 OpenSSH 公钥数据计算 SHA-256 指纹，避免信任文本输出格式。"""

    fields = public_key_line.strip().split()
    if len(fields) < 2 or fields[0] != "ssh-ed25519":
        raise EvidenceError("identity_algorithm_mismatch")
    try:
        raw = base64.b64decode(fields[1], validate=True)
    except (ValueError, TypeError) as error:
        raise EvidenceError("identity_key_invalid") from error
    digest = base64.b64encode(hashlib.sha256(raw).digest()).decode("ascii").rstrip("=")
    return "SHA256:" + digest


def fixed_local_environment() -> dict[str, str]:
    """为本地 OpenSSH 工具提供最小环境，拒绝 Agent、AskPass 和 PATH 注入。"""

    if os.name == "nt":
        return {"SystemRoot": "C:\\Windows"}
    return {"PATH": "/usr/bin:/bin", "LANG": "C"}


def validate_known_hosts(
    known_hosts: Path,
    ssh_keygen: Path,
    *,
    expected_fingerprint: str = TARGET_HOST_FINGERPRINT,
    tool_runner=subprocess.run,
    fingerprint_reader=ssh_fingerprint,
) -> str:
    """枚举明文和哈希端点条目，只接受唯一批准的 ED25519 密钥。"""

    try:
        completed = tool_runner(
            [str(ssh_keygen), "-F", TARGET_HOST_ALIAS, "-f", str(known_hosts)],
            capture_output=True,
            text=True,
            encoding="utf-8",
            timeout=10,
            check=False,
            env=fixed_local_environment(),
        )
    except Exception as error:
        raise EvidenceError("known_hosts_unavailable") from error
    if completed.returncode != 0 or completed.stderr:
        raise EvidenceError("known_hosts_lookup_failed")
    entries = [line for line in completed.stdout.splitlines() if line and not line.startswith("#")]
    if len(entries) != 1:
        raise EvidenceError("known_hosts_entry_count_mismatch")
    fields = entries[0].split()
    if len(fields) < 3 or fields[1] != "ssh-ed25519":
        raise EvidenceError("known_hosts_algorithm_mismatch")
    approved_line = " ".join(fields[:3])
    if fingerprint_reader(" ".join(fields[1:3])) != expected_fingerprint:
        raise EvidenceError("known_hosts_fingerprint_mismatch")
    return approved_line


def _read_ascii_line(path: Path) -> str:
    """读取单行 ASCII 公钥，拒绝多行和非 ASCII 内容。"""

    try:
        text = path.read_text(encoding="ascii")
    except (OSError, UnicodeError) as error:
        raise EvidenceError("identity_public_key_unavailable") from error
    lines = text.splitlines()
    if len(lines) != 1:
        raise EvidenceError("identity_public_key_invalid")
    return lines[0]


def freeze_local_inputs(
    known_hosts: Path,
    identity_file: Path,
    identity_public_file: Path,
    *,
    ssh_path: Path = FIXED_SSH,
    ssh_keygen_path: Path = FIXED_SSH_KEYGEN,
    tool_runner=subprocess.run,
    expected_host_fingerprint: str = TARGET_HOST_FINGERPRINT,
    expected_identity_fingerprint: str = LOCAL_IDENTITY_FINGERPRINT,
) -> LocalInputs:
    """先冻结全部材料，再验证端点和密钥对语义，避免校验到使用的窗口。"""

    evidence = LocalInputs(
        ssh=freeze_file(ssh_path),
        ssh_keygen=freeze_file(ssh_keygen_path),
        known_hosts=freeze_file(known_hosts),
        identity_file=freeze_file(identity_file),
        identity_public_file=freeze_file(identity_public_file),
        approved_known_hosts_line="",
    )
    approved_line = validate_known_hosts(
        evidence.known_hosts.path,
        evidence.ssh_keygen.path,
        expected_fingerprint=expected_host_fingerprint,
        tool_runner=tool_runner,
    )
    public_line = _read_ascii_line(evidence.identity_public_file.path)
    if ssh_fingerprint(public_line) != expected_identity_fingerprint:
        raise EvidenceError("identity_fingerprint_mismatch")
    try:
        derived = tool_runner(
            [str(evidence.ssh_keygen.path), "-y", "-f", str(evidence.identity_file.path)],
            capture_output=True,
            text=True,
            encoding="utf-8",
            timeout=10,
            check=False,
            env=fixed_local_environment(),
        )
    except Exception as error:
        raise EvidenceError("identity_pair_unavailable") from error
    if derived.returncode != 0 or derived.stderr or derived.stdout.strip() != " ".join(public_line.split()[:2]):
        raise EvidenceError("identity_pair_mismatch")
    for item in (
        evidence.ssh, evidence.ssh_keygen, evidence.known_hosts,
        evidence.identity_file, evidence.identity_public_file,
    ):
        assert_file_unchanged(item)
    return LocalInputs(
        ssh=evidence.ssh,
        ssh_keygen=evidence.ssh_keygen,
        known_hosts=evidence.known_hosts,
        identity_file=evidence.identity_file,
        identity_public_file=evidence.identity_public_file,
        approved_known_hosts_line=approved_line,
    )


def _assert_local_inputs_unchanged(inputs: LocalInputs) -> None:
    """在唯一 SSH 的前后复核所有系统工具和身份材料。"""

    for item in (
        inputs.ssh, inputs.ssh_keygen, inputs.known_hosts,
        inputs.identity_file, inputs.identity_public_file,
    ):
        assert_file_unchanged(item)


def run_once(inputs: LocalInputs) -> dict[str, str]:
    """使用固定参数执行唯一一次 OpenSSH，并低敏验证完整九键证据。"""

    _assert_local_inputs_unchanged(inputs)
    with tempfile.TemporaryDirectory(prefix="g8-drop-012-known-hosts-") as directory:
        approved_known_hosts = Path(directory) / "known_hosts"
        try:
            with approved_known_hosts.open("xb") as stream:
                stream.write((inputs.approved_known_hosts_line + "\n").encode("ascii"))
            approved_known_hosts.chmod(0o600)
        except (OSError, UnicodeError) as error:
            raise EvidenceError("approved_known_hosts_unavailable") from error
        approved_evidence = freeze_file(approved_known_hosts)
        command = [
            str(inputs.ssh.path),
            "-F", "none",
            "-p", str(TARGET_PORT),
            "-o", "BatchMode=yes",
            "-o", "IdentitiesOnly=yes",
            "-o", "ConnectionAttempts=1",
            "-o", "StrictHostKeyChecking=yes",
            "-o", "HostKeyAlgorithms=ssh-ed25519",
            "-o", "PasswordAuthentication=no",
            "-o", "KbdInteractiveAuthentication=no",
            "-o", "ForwardAgent=no",
            "-o", "ForwardX11=no",
            "-o", "ClearAllForwardings=yes",
            "-o", "PermitLocalCommand=no",
            "-o", "RequestTTY=no",
            "-o", f"UserKnownHostsFile={approved_known_hosts}",
            "-i", str(inputs.identity_file.path),
            f"{TARGET_LOGIN}@{TARGET_HOST}",
            "/usr/bin/env -i PATH=/usr/bin:/bin /usr/bin/python3 -I -",
        ]
        environment = fixed_local_environment()
        try:
            process = subprocess.Popen(
                command,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                env=environment,
            )
            if process.stdin is None or process.stdout is None or process.stderr is None:
                raise EvidenceError("ssh_pipe_unavailable")
            try:
                process.stdin.write(build_remote_program().encode("utf-8"))
                process.stdin.close()
            except (OSError, ValueError) as error:
                process.kill()
                process.wait(timeout=5)
                raise EvidenceError("ssh_input_failed") from error
            captures: dict[str, StreamCapture] = {}

            def drain(name: str, stream: BinaryIO) -> None:
                captures[name] = collect_stream(stream, STREAM_LIMIT)

            stdout_thread = threading.Thread(
                target=drain, args=("stdout", process.stdout), daemon=True,
            )
            stderr_thread = threading.Thread(
                target=drain, args=("stderr", process.stderr), daemon=True,
            )
            stdout_thread.start()
            stderr_thread.start()
            try:
                returncode = process.wait(timeout=30)
            except subprocess.TimeoutExpired as error:
                process.kill()
                process.wait(timeout=5)
                raise EvidenceError("ssh_timeout") from error
            stdout_thread.join()
            stderr_thread.join()
        except EvidenceError:
            raise
        except Exception as error:
            raise EvidenceError("ssh_unavailable") from error

        _assert_local_inputs_unchanged(inputs)
        assert_file_unchanged(approved_evidence)
        stdout_capture = captures.get("stdout")
        stderr_capture = captures.get("stderr")
        if stdout_capture is None or stderr_capture is None:
            raise EvidenceError("ssh_stream_missing")
        if stdout_capture.error or stderr_capture.error:
            raise EvidenceError("ssh_stream_failed")
        if stdout_capture.exceeded or stderr_capture.exceeded:
            raise EvidenceError("ssh_output_limit_exceeded")
        if returncode != 0:
            raise EvidenceError("ssh_exit_nonzero")
        if stderr_capture.byte_count != 0:
            raise EvidenceError("ssh_stderr_present")
        try:
            stdout = stdout_capture.data.decode("ascii")
        except UnicodeDecodeError as error:
            raise EvidenceError("ssh_stdout_non_ascii") from error
        return parse_remote_output(stdout)


def build_argument_parser() -> SafeArgumentParser:
    """构造不回显参数内容的固定命令行解析器。"""

    parser = SafeArgumentParser(add_help=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--local-check", action="store_true")
    parser.add_argument("--change-id")
    parser.add_argument("--known-hosts", type=Path)
    parser.add_argument("--identity-file", type=Path)
    parser.add_argument("--identity-public-file", type=Path)
    return parser


def main() -> int:
    """执行离线自检、本地检查或未来获批后的唯一正式取证。"""

    if CHANGE_ID_CONSUMED:
        print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012=FAILED reason=change_id_consumed")
        return 2
    try:
        arguments = build_argument_parser().parse_args()
        if arguments.self_test:
            if any((
                arguments.local_check, arguments.change_id, arguments.known_hosts,
                arguments.identity_file, arguments.identity_public_file,
            )):
                raise EvidenceError("invalid_request")
            compile(build_remote_program(), "<g8-drop-staging-evidence-012>", "exec")
            print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012_SELF_TEST=PASS")
            return 0
        if arguments.known_hosts is None or arguments.identity_file is None or arguments.identity_public_file is None:
            raise EvidenceError("invalid_request")
        inputs = freeze_local_inputs(
            arguments.known_hosts,
            arguments.identity_file,
            arguments.identity_public_file,
        )
        if arguments.local_check:
            if arguments.change_id is not None:
                raise EvidenceError("invalid_request")
            print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012_LOCAL_CHECK=PASS")
            return 0
        if arguments.change_id != CHANGE_ID:
            raise EvidenceError("invalid_request")
        values = run_once(inputs)
        if values["STAGING_INTEGRITY"] == "MISMATCH":
            print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012=MISMATCH")
            return 3
        print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012=PASS")
        return 0
    except EvidenceError as error:
        print(f"G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012=FAILED reason={error}")
        return 2


def build_remote_program(
    *,
    deployment_root: str = TARGET_DEPLOYMENT_ROOT,
    staging_path: str = STAGING_PATH,
    expected_files: dict[str, tuple[str, int, int]] | None = None,
    expected_manifest: dict[str, str] | None = None,
    expected_checksums: dict[str, str] | None = None,
    _test_uid: int | None = None,
    _test_gid: int | None = None,
) -> str:
    """生成目录描述符锚定且没有写改删能力的远端只读程序。"""

    files = FROZEN_FILES if expected_files is None else expected_files
    manifest = FROZEN_MANIFEST if expected_manifest is None else expected_manifest
    checksums = FROZEN_CHECKSUMS if expected_checksums is None else expected_checksums
    identity_setup = (
        "account = pwd.getpwnam('pc')\nuid = account.pw_uid\n"
        "group = grp.getgrnam('pc')\ngid = group.gr_gid\n"
        "if os.getuid() != uid:\n    raise SystemExit(41)"
        if _test_uid is None or _test_gid is None
        else f"uid = {_test_uid!r}\ngid = {_test_gid!r}"
    )
    template = r'''
import grp
import hashlib
import os
import pwd
import re
import stat

evidence_change_id = __CHANGE_ID__
target_change_id = __TARGET_CHANGE_ID__
deployment_root = __DEPLOYMENT_ROOT__
staging_path = __STAGING_PATH__
expected_files = __EXPECTED_FILES__
expected_manifest = __EXPECTED_MANIFEST__
expected_checksums = __EXPECTED_CHECKSUMS__
__IDENTITY_SETUP__

def metadata_identity(value):
    return (
        value.st_dev, value.st_ino, value.st_mode, value.st_uid, value.st_gid,
        value.st_size, value.st_mtime_ns, value.st_ctime_ns,
    )

def parse_manifest(content):
    try:
        text = content.decode('ascii')
    except UnicodeDecodeError:
        return None
    values = {}
    for line in text.splitlines():
        match = re.fullmatch(r'([A-Z0-9_]+)=([^\r\n]+)', line)
        if match is None or match.group(1) in values:
            return None
        values[match.group(1)] = match.group(2)
    return values

def parse_checksums(content):
    try:
        text = content.decode('ascii')
    except UnicodeDecodeError:
        return None
    values = {}
    for line in text.splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  ([A-Za-z0-9._-]+)', line)
        if match is None or match.group(2) in values:
            return None
        values[match.group(2)] = match.group(1)
    return values

root_meta = os.lstat(deployment_root)
if os.path.realpath(deployment_root) != deployment_root:
    raise SystemExit(41)
root_fd = os.open(
    deployment_root,
    os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
)
try:
    pinned_root = os.fstat(root_fd)
    root_mode = stat.S_IMODE(pinned_root.st_mode)
    if (
        metadata_identity(pinned_root) != metadata_identity(root_meta)
        or not stat.S_ISDIR(pinned_root.st_mode)
        or pinned_root.st_uid != uid
        or pinned_root.st_gid != gid
        or root_mode & 0o700 != 0o700
        or root_mode & 0o022
        or os.path.dirname(staging_path) != deployment_root
    ):
        raise SystemExit(41)

    stage_name = os.path.basename(staging_path)
    staging_state = 'ABSENT'
    staging_integrity = 'NOT_APPLICABLE'
    staging_mismatch_reason = 'NONE'
    try:
        stage_meta = os.stat(stage_name, dir_fd=root_fd, follow_symlinks=False)
    except FileNotFoundError:
        stage_meta = None
    except OSError:
        raise SystemExit(41)

    if stage_meta is not None:
        staging_state = 'PRESENT'
        staging_integrity = 'MISMATCH'
        staging_mismatch_reason = 'PATH'
        try:
            if (
                stat.S_ISDIR(stage_meta.st_mode)
                and stage_meta.st_uid == uid
                and stage_meta.st_gid == gid
                and stat.S_IMODE(stage_meta.st_mode) == 0o700
            ):
                stage_fd = os.open(
                    stage_name,
                    os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
                    dir_fd=root_fd,
                )
                try:
                    pinned_stage = os.fstat(stage_fd)
                    if metadata_identity(pinned_stage) == metadata_identity(stage_meta):
                        names = os.listdir(stage_fd)
                        if set(names) != set(expected_files) or len(names) != len(expected_files):
                            staging_mismatch_reason = 'FILE_SET'
                        else:
                            metadata_matches = True
                            entries_stable = True
                            artifact_content_matches = True
                            manifest_matches = True
                            receipt_matches = True
                            opened_files = {}
                            captured = {}
                            for name in names:
                                file_fd = os.open(
                                    name,
                                    os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC,
                                    dir_fd=stage_fd,
                                )
                                try:
                                    before = os.fstat(file_fd)
                                    expected_sha256, expected_size, expected_mode = expected_files[name]
                                    opened_files[name] = metadata_identity(before)
                                    valid_metadata = (
                                        stat.S_ISREG(before.st_mode)
                                        and before.st_uid == uid
                                        and before.st_gid == gid
                                        and stat.S_IMODE(before.st_mode) == expected_mode
                                        and before.st_size == expected_size
                                    )
                                    actual_digest = None
                                    content = None
                                    if valid_metadata:
                                        digest = hashlib.sha256()
                                        kept = bytearray()
                                        while True:
                                            block = os.read(file_fd, 1024 * 1024)
                                            if not block:
                                                break
                                            digest.update(block)
                                            if name in ('manifest.env', 'SHA256SUMS'):
                                                kept.extend(block)
                                        actual_digest = digest.hexdigest()
                                        if name in ('manifest.env', 'SHA256SUMS'):
                                            content = bytes(kept)
                                            captured[name] = content
                                    else:
                                        metadata_matches = False
                                    if metadata_identity(os.fstat(file_fd)) != opened_files[name]:
                                        entries_stable = False
                                    if actual_digest is not None and actual_digest != expected_sha256:
                                        if name == 'manifest.env':
                                            manifest_matches = False
                                        elif name == 'SHA256SUMS':
                                            receipt_matches = False
                                        else:
                                            artifact_content_matches = False
                                finally:
                                    os.close(file_fd)

                            final_names = os.listdir(stage_fd)
                            if set(final_names) != set(expected_files) or len(final_names) != len(expected_files):
                                entries_stable = False
                            if entries_stable:
                                for name in final_names:
                                    current = os.stat(name, dir_fd=stage_fd, follow_symlinks=False)
                                    if metadata_identity(current) != opened_files.get(name):
                                        entries_stable = False
                                        break
                            if metadata_matches:
                                manifest_matches = (
                                    manifest_matches
                                    and parse_manifest(captured.get('manifest.env', b'')) == expected_manifest
                                )
                                receipt_matches = (
                                    receipt_matches
                                    and parse_checksums(captured.get('SHA256SUMS', b'')) == expected_checksums
                                )
                            final_stage = os.fstat(stage_fd)
                            current_stage = os.stat(stage_name, dir_fd=root_fd, follow_symlinks=False)
                            if (
                                not entries_stable
                                or metadata_identity(final_stage) != metadata_identity(pinned_stage)
                                or metadata_identity(current_stage) != metadata_identity(pinned_stage)
                            ):
                                staging_mismatch_reason = 'PATH'
                            elif not metadata_matches:
                                staging_mismatch_reason = 'FILE_METADATA'
                            elif not manifest_matches:
                                staging_mismatch_reason = 'MANIFEST'
                            elif not receipt_matches:
                                staging_mismatch_reason = 'RECEIPT'
                            elif not artifact_content_matches:
                                staging_mismatch_reason = 'FILE_CONTENT'
                            else:
                                staging_integrity = 'PASS'
                                staging_mismatch_reason = 'NONE'
                finally:
                    os.close(stage_fd)
        except OSError:
            staging_mismatch_reason = 'READ_ERROR'

    final_root = os.fstat(root_fd)
    current_root = os.lstat(deployment_root)
    if (
        os.path.realpath(deployment_root) != deployment_root
        or metadata_identity(final_root) != metadata_identity(pinned_root)
        or metadata_identity(current_root) != metadata_identity(pinned_root)
    ):
        raise SystemExit(41)
finally:
    os.close(root_fd)

print('EVIDENCE_CHANGE_ID=' + evidence_change_id)
print('TARGET_CHANGE_ID=' + target_change_id)
print('LOGIN_USER=pc')
print('DEPLOYMENT_ROOT_REALPATH=' + deployment_root)
print('DEPLOYMENT_ROOT_CHECK=PASS')
print('STAGING_STATE=' + staging_state)
print('STAGING_INTEGRITY=' + staging_integrity)
print('STAGING_MISMATCH_REASON=' + staging_mismatch_reason)
print('EVIDENCE_RESULT=PASS')
'''
    return (
        textwrap.dedent(template)
        .replace("__CHANGE_ID__", repr(CHANGE_ID))
        .replace("__TARGET_CHANGE_ID__", repr(TARGET_CHANGE_ID))
        .replace("__DEPLOYMENT_ROOT__", repr(deployment_root))
        .replace("__STAGING_PATH__", repr(staging_path))
        .replace("__EXPECTED_FILES__", repr(files))
        .replace("__EXPECTED_MANIFEST__", repr(manifest))
        .replace("__EXPECTED_CHECKSUMS__", repr(checksums))
        .replace("__IDENTITY_SETUP__", identity_setup)
    )


def parse_remote_output(
    stdout: str,
    *,
    expected_deployment_root: str = TARGET_DEPLOYMENT_ROOT,
) -> dict[str, str]:
    """严格解析固定九键和三态组合。"""

    try:
        stdout.encode("ascii")
    except UnicodeEncodeError as error:
        raise EvidenceError("non_ascii_output") from error

    values: dict[str, str] = {}
    for line in stdout.splitlines():
        match = re.fullmatch(r"([A-Z0-9_]+)=([^\r\n]+)", line)
        if match is None or match.group(1) in values:
            raise EvidenceError("invalid_remote_output")
        values[match.group(1)] = match.group(2)

    if set(values) != EXPECTED_REMOTE_KEYS:
        raise EvidenceError("remote_key_set_mismatch")

    fixed = {
        "EVIDENCE_CHANGE_ID": CHANGE_ID,
        "TARGET_CHANGE_ID": TARGET_CHANGE_ID,
        "LOGIN_USER": "pc",
        "DEPLOYMENT_ROOT_REALPATH": expected_deployment_root,
        "DEPLOYMENT_ROOT_CHECK": "PASS",
        "EVIDENCE_RESULT": "PASS",
    }
    if any(values[key] != expected for key, expected in fixed.items()):
        raise EvidenceError("remote_contract_mismatch")

    state = (
        values["STAGING_STATE"],
        values["STAGING_INTEGRITY"],
        values["STAGING_MISMATCH_REASON"],
    )
    if state not in VALID_STATES:
        raise EvidenceError("invalid_staging_state")
    return values


if __name__ == "__main__":
    raise SystemExit(main())
