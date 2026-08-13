#!/usr/bin/env python3
"""重复执行的 G8 本地 SSH 身份材料诊断器，不包含远端访问能力。"""

from __future__ import annotations

import argparse
from dataclasses import dataclass
import hashlib
import os
from pathlib import Path
import stat
import subprocess
import sys
from typing import Sequence


TARGET_HOST = "[8.130.9.163]:10003"
TARGET_HOST_FINGERPRINT = "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"
LOCAL_IDENTITY_FINGERPRINT = "SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0"
MAX_TOOL_OUTPUT = 64 * 1024


class DiagnosticError(Exception):
    """表示必须收敛为固定低敏结果的本地诊断失败。"""


class SafeArgumentParser(argparse.ArgumentParser):
    """禁止 argparse 把调用方参数或路径写入 stderr。"""

    def error(self, message):
        raise DiagnosticError("invalid_request")

    def exit(self, status=0, message=None):
        raise DiagnosticError("invalid_request")


@dataclass(frozen=True)
class FileEvidence:
    path: Path
    device: int
    inode: int
    mode: int
    size: int
    mtime_ns: int
    ctime_ns: int
    sha256: str
    data: bytes


@dataclass(frozen=True)
class CommandResult:
    returncode: int
    stdout: bytes
    stderr: bytes


@dataclass(frozen=True)
class MaterialsEvidence:
    ssh_keygen: FileEvidence
    known_hosts: FileEvidence
    identity_file: FileEvidence
    identity_public_key: FileEvidence
    approved_host_line: str


def _metadata_identity(metadata):
    return (
        metadata.st_dev,
        metadata.st_ino,
        stat.S_IFMT(metadata.st_mode),
        metadata.st_size,
        metadata.st_mtime_ns,
        metadata.st_ctime_ns,
    )


def _is_reparse(metadata) -> bool:
    attributes = getattr(metadata, "st_file_attributes", 0)
    flag = getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
    return bool(attributes & flag)


def freeze_file(path: Path) -> FileEvidence:
    """通过同一只读文件描述符冻结文件，阻断路径替换和链接竞态。"""
    path = Path(path)
    if not path.is_absolute():
        raise DiagnosticError("invalid_request")
    try:
        before_entry = os.lstat(path)
        if not stat.S_ISREG(before_entry.st_mode) or _is_reparse(before_entry):
            raise DiagnosticError("materials_unavailable")
        flags = os.O_RDONLY | getattr(os, "O_BINARY", 0)
        flags |= getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_CLOEXEC", 0)
        descriptor = os.open(path, flags)
        try:
            before_fd = os.fstat(descriptor)
            if _metadata_identity(before_entry) != _metadata_identity(before_fd):
                raise DiagnosticError("materials_drift")
            digest = hashlib.sha256()
            chunks = []
            while True:
                chunk = os.read(descriptor, 8192)
                if not chunk:
                    break
                digest.update(chunk)
                chunks.append(chunk)
            after_fd = os.fstat(descriptor)
        finally:
            os.close(descriptor)
        after_entry = os.lstat(path)
    except DiagnosticError:
        raise
    except (OSError, ValueError) as exc:
        raise DiagnosticError("materials_unavailable") from exc
    if _metadata_identity(before_fd) != _metadata_identity(after_fd):
        raise DiagnosticError("materials_drift")
    if _metadata_identity(before_fd) != _metadata_identity(after_entry):
        raise DiagnosticError("materials_drift")
    return FileEvidence(
        path=path,
        device=before_fd.st_dev,
        inode=before_fd.st_ino,
        mode=before_fd.st_mode,
        size=before_fd.st_size,
        mtime_ns=before_fd.st_mtime_ns,
        ctime_ns=before_fd.st_ctime_ns,
        sha256=digest.hexdigest(),
        data=b"".join(chunks),
    )


def fixed_ssh_keygen_path() -> Path:
    """只选择操作系统固定位置，避免继承调用方 PATH。"""
    if os.name == "nt":
        system_root = os.environ.get("SystemRoot")
        if not system_root:
            raise DiagnosticError("tool_unavailable")
        path = Path(system_root) / "System32" / "OpenSSH" / "ssh-keygen.exe"
    else:
        path = Path("/usr/bin/ssh-keygen")
    if not path.is_absolute():
        raise DiagnosticError("tool_unavailable")
    return path


def _minimal_environment():
    if os.name == "nt":
        system_root = os.environ.get("SystemRoot")
        if not system_root:
            raise DiagnosticError("tool_unavailable")
        return {"SystemRoot": system_root}
    return {"PATH": "/usr/bin:/bin", "LANG": "C"}


def run_ssh_keygen(executable: Path, arguments: Sequence[str], input_data: bytes | None = None) -> CommandResult:
    """仅调用本地密钥检查工具，任何正文异常都由上层低敏收敛。"""
    try:
        completed = subprocess.run(
            [str(executable), *arguments],
            input=input_data,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=10,
            check=False,
            env=_minimal_environment(),
        )
    except (OSError, subprocess.SubprocessError) as exc:
        raise DiagnosticError("tool_unavailable") from exc
    if len(completed.stdout) > MAX_TOOL_OUTPUT or len(completed.stderr) > MAX_TOOL_OUTPUT:
        raise DiagnosticError("tool_unavailable")
    return CommandResult(completed.returncode, completed.stdout, completed.stderr)


def _require_clean_command(result: CommandResult, reason: str) -> bytes:
    if result.returncode != 0 or result.stderr or len(result.stdout) > MAX_TOOL_OUTPUT:
        raise DiagnosticError(reason)
    return result.stdout


def find_approved_host_key(known_hosts_path: Path, ssh_keygen_path: Path) -> str:
    """只选取固定端点唯一批准的 ED25519 行，忽略不会参与协商的其他算法。"""
    lookup = run_ssh_keygen(ssh_keygen_path, ("-F", TARGET_HOST, "-f", str(known_hosts_path)))
    output = _require_clean_command(lookup, "known_hosts_unavailable")
    try:
        lines = [line for line in output.decode("ascii").splitlines() if line and not line.startswith("#")]
    except UnicodeDecodeError as exc:
        raise DiagnosticError("known_hosts_unavailable") from exc
    ed25519_lines = []
    for line in lines:
        fields = line.split()
        if len(fields) < 3:
            raise DiagnosticError("known_hosts_unavailable")
        if fields[1] == "ssh-ed25519":
            ed25519_lines.append(line)
    if len(ed25519_lines) != 1:
        raise DiagnosticError("known_hosts_unavailable")
    approved_line = ed25519_lines[0]
    fingerprint = run_ssh_keygen(ssh_keygen_path, ("-lf", "-"), (approved_line + "\n").encode("ascii"))
    fingerprint_output = _require_clean_command(fingerprint, "known_hosts_unavailable")
    try:
        fields = fingerprint_output.decode("ascii").strip().split()
    except UnicodeDecodeError as exc:
        raise DiagnosticError("known_hosts_unavailable") from exc
    if len(fields) < 2 or fields[1] != TARGET_HOST_FINGERPRINT:
        raise DiagnosticError("known_hosts_unavailable")
    return approved_line


def _public_key_identity(data: bytes):
    try:
        fields = data.decode("ascii").strip().split()
    except UnicodeDecodeError as exc:
        raise DiagnosticError("identity_unavailable") from exc
    if len(fields) < 2 or fields[0] != "ssh-ed25519":
        raise DiagnosticError("identity_unavailable")
    return fields[0], fields[1]


def validate_identity_pair(identity_file: Path, public_key_data: bytes, ssh_keygen_path: Path) -> None:
    """验证公私钥配对和批准客户端指纹，不复制或修改私钥。"""
    derived = run_ssh_keygen(ssh_keygen_path, ("-y", "-f", str(identity_file)))
    derived_data = _require_clean_command(derived, "identity_unavailable")
    if _public_key_identity(derived_data) != _public_key_identity(public_key_data):
        raise DiagnosticError("identity_unavailable")
    fingerprint = run_ssh_keygen(ssh_keygen_path, ("-lf", "-"), public_key_data)
    fingerprint_data = _require_clean_command(fingerprint, "identity_unavailable")
    try:
        fields = fingerprint_data.decode("ascii").strip().split()
    except UnicodeDecodeError as exc:
        raise DiagnosticError("identity_unavailable") from exc
    if len(fields) < 2 or fields[1] != LOCAL_IDENTITY_FINGERPRINT:
        raise DiagnosticError("identity_unavailable")


def diagnose_materials(known_hosts: Path, identity_file: Path, identity_public_key: Path) -> MaterialsEvidence:
    """冻结并验证全部本地材料；该函数本身不含任何远端访问。"""
    ssh_keygen_path = fixed_ssh_keygen_path()
    ssh_keygen = freeze_file(ssh_keygen_path)
    known_hosts_evidence = freeze_file(known_hosts)
    identity_evidence = freeze_file(identity_file)
    public_evidence = freeze_file(identity_public_key)
    approved_line = find_approved_host_key(known_hosts, ssh_keygen_path)
    validate_identity_pair(identity_file, public_evidence.data, ssh_keygen_path)
    evidence = MaterialsEvidence(
        ssh_keygen=ssh_keygen,
        known_hosts=known_hosts_evidence,
        identity_file=identity_evidence,
        identity_public_key=public_evidence,
        approved_host_line=approved_line,
    )
    assert_materials_unchanged(evidence)
    return evidence


def assert_materials_unchanged(evidence: MaterialsEvidence) -> None:
    """在调用边界再次复核全部材料，永久漂移必须失败关闭。"""
    for original in (
        evidence.ssh_keygen,
        evidence.known_hosts,
        evidence.identity_file,
        evidence.identity_public_key,
    ):
        current = freeze_file(original.path)
        if (
            current.device,
            current.inode,
            current.mode,
            current.size,
            current.mtime_ns,
            current.ctime_ns,
            current.sha256,
        ) != (
            original.device,
            original.inode,
            original.mode,
            original.size,
            original.mtime_ns,
            original.ctime_ns,
            original.sha256,
        ):
            raise DiagnosticError("materials_drift")


def _build_parser() -> SafeArgumentParser:
    parser = SafeArgumentParser(add_help=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--known-hosts")
    parser.add_argument("--identity-file")
    parser.add_argument("--identity-public-key")
    return parser


def main() -> int:
    try:
        arguments = _build_parser().parse_args()
        if arguments.self_test:
            if arguments.known_hosts or arguments.identity_file or arguments.identity_public_key:
                raise DiagnosticError("invalid_request")
            print("G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC_SELF_TEST=PASS")
            return 0
        if not (arguments.known_hosts and arguments.identity_file and arguments.identity_public_key):
            raise DiagnosticError("invalid_request")
        paths = tuple(Path(value) for value in (
            arguments.known_hosts,
            arguments.identity_file,
            arguments.identity_public_key,
        ))
        if not all(path.is_absolute() for path in paths):
            raise DiagnosticError("invalid_request")
        diagnose_materials(*paths)
        print("G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=PASS")
        return 0
    except DiagnosticError as exc:
        reason = str(exc)
        if reason not in {
            "invalid_request",
            "tool_unavailable",
            "known_hosts_unavailable",
            "identity_unavailable",
            "materials_drift",
        }:
            reason = "tool_unavailable"
        print(f"G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=FAILED reason={reason}")
        return 2
    except Exception:
        print("G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=FAILED reason=tool_unavailable")
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
