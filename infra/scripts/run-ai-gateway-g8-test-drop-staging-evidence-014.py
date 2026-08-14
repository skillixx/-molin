#!/usr/bin/env python3
"""G8 Drop 固定 011 暂存的一次性 014 只读取证包装器。"""

from __future__ import annotations

import argparse
from dataclasses import dataclass
import hashlib
import os
from pathlib import Path, PureWindowsPath
import stat
import subprocess
import sys
import tempfile
import threading
import types


CHANGE_ID = "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260814-014"
TARGET_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
CHANGE_ID_CONSUMED = False
TARGET_HOST = "8.130.9.163"
TARGET_PORT = "10003"
TARGET_USER = "pc"
TARGET_DEPLOYMENT_ROOT = "/home/pc/molin"
TARGET_STAGE_NAME = ".g8-staging-" + TARGET_CHANGE_ID
LOCAL_DIAGNOSTIC_NAME = "diagnose-ai-gateway-g8-local-ssh-materials.py"
# 包装器只加载工程门禁冻结的诊断器字节，摘要变化必须先更新候选并重新完成评审。
LOCAL_DIAGNOSTIC_SHA256 = "3382b66c289c08b54ad36abc78969983ce89a89b7216e84c23b31aec6e34cadf"
MAX_STREAM_BYTES = 64 * 1024

EXPECTED_FILES = {
    "SHA256SUMS": ("15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f", 362, 0o600),
    "ai-gateway-reconcile": ("37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1", 13_066_129, 0o700),
    "g8-test-readonly-audit": ("308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256", 18_377, 0o700),
    "manifest.env": ("763c71547443a125b434071895b9a532fd966896e4ba9486b1c6b80f1541f3c6", 863, 0o600),
    "molin-g8-test-readonly-audit.sudoers": ("1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f", 416, 0o600),
}

EXPECTED_MANIFEST = {
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
    "AUDITOR_SHA256": EXPECTED_FILES["g8-test-readonly-audit"][0],
    "SUDOERS_SHA256": EXPECTED_FILES["molin-g8-test-readonly-audit.sudoers"][0],
    "RECONCILE_SHA256": EXPECTED_FILES["ai-gateway-reconcile"][0],
    "TARGET_SSH": "pc@8.130.9.163:10003",
    "TARGET_SSH_ED25519_FINGERPRINT": "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I",
    "TARGET_TRANSPORT": "DROP_SSH_INTERACTIVE_SUDO",
    "PHYSICAL_HOST_IDENTITY": "NOT_APPLICABLE",
    "TARGET_DEPLOYMENT_ROOT": TARGET_DEPLOYMENT_ROOT,
    "RECONCILE_SIZE": "13066129",
    "REPRODUCIBLE_BUILD_COUNT": "2",
}

REMOTE_KEYS = (
    "EVIDENCE_CHANGE_ID",
    "TARGET_CHANGE_ID",
    "LOGIN_USER",
    "DEPLOYMENT_ROOT_REALPATH",
    "DEPLOYMENT_ROOT_CHECK",
    "STAGING_STATE",
    "STAGING_INTEGRITY",
    "STAGING_MISMATCH_REASON",
    "EVIDENCE_RESULT",
)
MISMATCH_REASONS = {
    "PATH", "FILE_SET", "FILE_METADATA", "FILE_CONTENT",
    "MANIFEST", "RECEIPT", "READ_ERROR",
}


class EvidenceError(Exception):
    """表示不能形成可信远端证据，外层只能输出固定低敏失败。"""


class SafeArgumentParser(argparse.ArgumentParser):
    """禁止 argparse 回显调用方参数。"""

    def error(self, message):
        raise EvidenceError("invalid_request")

    def exit(self, status=0, message=None):
        raise EvidenceError("invalid_request")


@dataclass(frozen=True)
class EvidenceResult:
    state: str
    integrity: str
    reason: str


@dataclass(frozen=True)
class StreamCapture:
    data: bytes
    byte_count: int
    line_count: int
    sha256: str
    exceeded: bool
    error: bool


def build_remote_program(*, _test_root=None, _test_files=None, _test_manifest=None, _test_uid=None, _test_gid=None) -> str:
    """生成只读远端程序；以下测试注入参数不会暴露到正式 CLI。"""
    root = TARGET_DEPLOYMENT_ROOT if _test_root is None else _test_root
    files = EXPECTED_FILES if _test_files is None else _test_files
    manifest = EXPECTED_MANIFEST if _test_manifest is None else _test_manifest
    identity_setup = '''pc = pwd.getpwnam("pc")
    pc_group = grp.getgrnam("pc")''' if _test_uid is None else f'''class Identity:
        pass
    pc = Identity()
    pc.pw_uid = {_test_uid!r}
    pc_group = Identity()
    pc_group.gr_gid = {_test_gid!r}'''
    return f'''import grp
import hashlib
import os
import pwd
import stat

CHANGE_ID = {CHANGE_ID!r}
TARGET_CHANGE_ID = {TARGET_CHANGE_ID!r}
ROOT = {root!r}
STAGE = {TARGET_STAGE_NAME!r}
FILES = {files!r}
MANIFEST = {manifest!r}

def meta(value):
    return (value.st_dev, value.st_ino, value.st_mode, value.st_uid, value.st_gid, value.st_size, value.st_mtime_ns, value.st_ctime_ns)

def emit(state, integrity, reason):
    values = (
        ("EVIDENCE_CHANGE_ID", CHANGE_ID),
        ("TARGET_CHANGE_ID", TARGET_CHANGE_ID),
        ("LOGIN_USER", "pc"),
        ("DEPLOYMENT_ROOT_REALPATH", ROOT),
        ("DEPLOYMENT_ROOT_CHECK", "PASS"),
        ("STAGING_STATE", state),
        ("STAGING_INTEGRITY", integrity),
        ("STAGING_MISMATCH_REASON", reason),
        ("EVIDENCE_RESULT", "PASS"),
    )
    for key, value in values:
        print(key + "=" + value)

def parse_pairs(data):
    result = {{}}
    for raw in data.decode("ascii").splitlines():
        if not raw or "=" not in raw:
            raise ValueError()
        key, value = raw.split("=", 1)
        if key in result:
            raise ValueError()
        result[key] = value
    return result

def mismatch(reason):
    emit("PRESENT", "MISMATCH", reason)
    raise SystemExit(0)

try:
    {identity_setup}
    if os.getuid() != pc.pw_uid:
        raise SystemExit(41)
    root_entry = os.lstat(ROOT)
    if os.path.realpath(ROOT) != ROOT or not stat.S_ISDIR(root_entry.st_mode):
        raise SystemExit(41)
    if root_entry.st_uid != pc.pw_uid or root_entry.st_gid != pc_group.gr_gid or root_entry.st_mode & 0o022:
        raise SystemExit(41)
    root_fd = os.open(ROOT, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        root_pinned = os.fstat(root_fd)
        if meta(root_entry) != meta(root_pinned):
            raise SystemExit(41)
        try:
            stage_entry = os.stat(STAGE, dir_fd=root_fd, follow_symlinks=False)
        except FileNotFoundError:
            if meta(root_pinned) != meta(os.fstat(root_fd)) or meta(root_pinned) != meta(os.lstat(ROOT)):
                raise SystemExit(41)
            emit("ABSENT", "NOT_APPLICABLE", "NONE")
            raise SystemExit(0)
        except OSError:
            raise SystemExit(41)
        if not stat.S_ISDIR(stage_entry.st_mode):
            mismatch("PATH")
        stage_fd = os.open(STAGE, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=root_fd)
        try:
            stage_pinned = os.fstat(stage_fd)
            if meta(stage_entry) != meta(stage_pinned):
                mismatch("PATH")
            if stage_pinned.st_uid != pc.pw_uid or stage_pinned.st_gid != pc_group.gr_gid or stat.S_IMODE(stage_pinned.st_mode) != 0o700:
                mismatch("PATH")
            if set(os.listdir(stage_fd)) != set(FILES):
                mismatch("FILE_SET")
            contents = {{}}
            identities = {{}}
            for name, expected in FILES.items():
                try:
                    entry = os.stat(name, dir_fd=stage_fd, follow_symlinks=False)
                except OSError:
                    mismatch("READ_ERROR")
                if not stat.S_ISREG(entry.st_mode) or entry.st_uid != pc.pw_uid or entry.st_gid != pc_group.gr_gid or stat.S_IMODE(entry.st_mode) != expected[2] or entry.st_size != expected[1]:
                    mismatch("FILE_METADATA")
                try:
                    fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=stage_fd)
                    try:
                        pinned = os.fstat(fd)
                        if meta(entry) != meta(pinned):
                            mismatch("PATH")
                        digest = hashlib.sha256()
                        blocks = []
                        while True:
                            block = os.read(fd, 65536)
                            if not block:
                                break
                            digest.update(block)
                            blocks.append(block)
                        after = os.fstat(fd)
                    finally:
                        os.close(fd)
                except SystemExit:
                    raise
                except OSError:
                    mismatch("READ_ERROR")
                current = os.stat(name, dir_fd=stage_fd, follow_symlinks=False)
                if meta(pinned) != meta(after) or meta(pinned) != meta(current):
                    mismatch("PATH")
                identities[name] = meta(pinned)
                contents[name] = b"".join(blocks)
                if digest.hexdigest() != expected[0]:
                    mismatch("RECEIPT" if name == "SHA256SUMS" else "FILE_CONTENT")
            try:
                receipt = {{}}
                for raw in contents["SHA256SUMS"].decode("ascii").splitlines():
                    digest_value, file_name = raw.split("  ", 1)
                    if file_name in receipt:
                        raise ValueError()
                    receipt[file_name] = digest_value
            except Exception:
                mismatch("RECEIPT")
            expected_receipt = {{name: FILES[name][0] for name in FILES if name != "SHA256SUMS"}}
            if receipt != expected_receipt:
                mismatch("RECEIPT")
            try:
                manifest = parse_pairs(contents["manifest.env"])
            except Exception:
                mismatch("MANIFEST")
            if manifest != MANIFEST:
                mismatch("MANIFEST")
            if set(os.listdir(stage_fd)) != set(FILES):
                mismatch("FILE_SET")
            for name, identity in identities.items():
                if meta(os.stat(name, dir_fd=stage_fd, follow_symlinks=False)) != identity:
                    mismatch("PATH")
            current_stage = os.stat(STAGE, dir_fd=root_fd, follow_symlinks=False)
            if meta(stage_pinned) != meta(os.fstat(stage_fd)) or meta(stage_pinned) != meta(current_stage):
                mismatch("PATH")
            if meta(root_pinned) != meta(os.fstat(root_fd)) or meta(root_pinned) != meta(os.lstat(ROOT)):
                raise SystemExit(41)
            emit("PRESENT", "PASS", "NONE")
        finally:
            os.close(stage_fd)
    finally:
        os.close(root_fd)
except SystemExit:
    raise
except Exception:
    raise SystemExit(41)
'''


def parse_remote_output(data: bytes) -> EvidenceResult:
    try:
        text = data.decode("ascii")
    except UnicodeDecodeError as exc:
        raise EvidenceError("output_contract") from exc
    values = {}
    for line in text.splitlines():
        if not line or "=" not in line:
            raise EvidenceError("output_contract")
        key, value = line.split("=", 1)
        if key in values:
            raise EvidenceError("output_contract")
        values[key] = value
    if tuple(values) != REMOTE_KEYS:
        raise EvidenceError("output_contract")
    if values["EVIDENCE_CHANGE_ID"] != CHANGE_ID or values["TARGET_CHANGE_ID"] != TARGET_CHANGE_ID:
        raise EvidenceError("output_contract")
    if values["LOGIN_USER"] != TARGET_USER or values["DEPLOYMENT_ROOT_REALPATH"] != TARGET_DEPLOYMENT_ROOT:
        raise EvidenceError("output_contract")
    if values["DEPLOYMENT_ROOT_CHECK"] != "PASS" or values["EVIDENCE_RESULT"] != "PASS":
        raise EvidenceError("output_contract")
    state = values["STAGING_STATE"]
    integrity = values["STAGING_INTEGRITY"]
    reason = values["STAGING_MISMATCH_REASON"]
    legal = (
        (state, integrity, reason) == ("ABSENT", "NOT_APPLICABLE", "NONE")
        or (state, integrity, reason) == ("PRESENT", "PASS", "NONE")
        or (state == "PRESENT" and integrity == "MISMATCH" and reason in MISMATCH_REASONS)
    )
    if not legal:
        raise EvidenceError("output_contract")
    return EvidenceResult(state, integrity, reason)


def render_result(result: EvidenceResult):
    status = "MISMATCH" if result.integrity == "MISMATCH" else "PASS"
    code = 3 if status == "MISMATCH" else 0
    text = (
        f"G8_TEST_READONLY_DROP_STAGING_EVIDENCE_014={status}\n"
        f"change_id={CHANGE_ID}\n"
        f"target_change_id={TARGET_CHANGE_ID}\n"
        f"staging_state={result.state}\n"
        f"staging_integrity={result.integrity}\n"
        f"staging_mismatch_reason={result.reason}\n"
    )
    return code, text


def collect_stream(stream, limit: int) -> StreamCapture:
    digest = hashlib.sha256()
    kept = bytearray()
    byte_count = 0
    line_count = 0
    error = False
    try:
        while True:
            chunk = stream.read(8192)
            if not chunk:
                break
            byte_count += len(chunk)
            line_count += chunk.count(b"\n")
            digest.update(chunk)
            remaining = limit + 1 - len(kept)
            if remaining > 0:
                kept.extend(chunk[:remaining])
    except Exception:
        error = True
    return StreamCapture(bytes(kept), byte_count, line_count, digest.hexdigest(), byte_count > limit, error)


def _freeze_helper_file(path: Path):
    if not path.is_absolute():
        raise EvidenceError("helper_unavailable")
    try:
        before = os.lstat(path)
        reparse_flag = getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
        if not stat.S_ISREG(before.st_mode) or getattr(before, "st_file_attributes", 0) & reparse_flag:
            raise EvidenceError("helper_unavailable")
        flags = os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_CLOEXEC", 0)
        fd = os.open(path, flags)
        try:
            pinned = os.fstat(fd)
            data = b""
            while True:
                chunk = os.read(fd, 8192)
                if not chunk:
                    break
                data += chunk
            after = os.fstat(fd)
        finally:
            os.close(fd)
        current = os.lstat(path)
    except EvidenceError:
        raise
    except OSError as exc:
        raise EvidenceError("helper_unavailable") from exc
    full_identity = lambda item: (item.st_dev, item.st_ino, item.st_mode, item.st_size, item.st_mtime_ns, item.st_ctime_ns)
    shared_identity = lambda item: (item.st_dev, item.st_ino, stat.S_IFMT(item.st_mode), item.st_size, item.st_mtime_ns)
    if shared_identity(before) != shared_identity(pinned):
        raise EvidenceError("helper_unavailable")
    if full_identity(pinned) != full_identity(after) or full_identity(before) != full_identity(current):
        raise EvidenceError("helper_unavailable")
    if shared_identity(pinned) != shared_identity(current):
        raise EvidenceError("helper_unavailable")
    if hashlib.sha256(data).hexdigest() != LOCAL_DIAGNOSTIC_SHA256:
        raise EvidenceError("helper_unavailable")
    return data


def load_local_diagnostic():
    """只执行已冻结的本地诊断器字节，避免按路径二次导入。"""
    path = Path(__file__).resolve().with_name(LOCAL_DIAGNOSTIC_NAME)
    data = _freeze_helper_file(path)
    module_name = "g8_local_diagnostic_frozen"
    module = types.ModuleType(module_name)
    module.__file__ = str(path)
    sys.modules[module_name] = module
    try:
        exec(compile(data, str(path), "exec"), module.__dict__)
    except Exception as exc:
        raise EvidenceError("helper_unavailable") from exc
    namespace = module.__dict__
    required = (
        "diagnose_materials", "assert_materials_unchanged", "windows_system_paths",
        "TARGET_HOST", "TARGET_HOST_FINGERPRINT", "LOCAL_IDENTITY_FINGERPRINT",
    )
    if not all(name in namespace for name in required):
        raise EvidenceError("helper_unavailable")
    if namespace["TARGET_HOST"] != "[8.130.9.163]:10003":
        raise EvidenceError("helper_unavailable")
    if namespace["TARGET_HOST_FINGERPRINT"] != "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I":
        raise EvidenceError("helper_unavailable")
    if namespace["LOCAL_IDENTITY_FINGERPRINT"] != "SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0":
        raise EvidenceError("helper_unavailable")
    return type("FrozenLocalDiagnostic", (), namespace)


def fixed_ssh_runtime(helper):
    """从冻结诊断器取得可信系统目录，并一次性冻结 SSH 路径与最小环境。"""
    if os.name == "nt":
        try:
            system_root, program_data = helper.windows_system_paths()
        except Exception as exc:
            raise EvidenceError("ssh_unavailable") from exc
        windows_root = PureWindowsPath(str(system_root))
        common_data = PureWindowsPath(str(program_data))
        if (
            not windows_root.is_absolute()
            or not common_data.is_absolute()
            or str(windows_root).startswith("\\\\")
            or str(common_data).startswith("\\\\")
        ):
            raise EvidenceError("ssh_unavailable")
        path = windows_root / "System32" / "OpenSSH" / "ssh.exe"
        environment = {"SystemRoot": str(windows_root), "PROGRAMDATA": str(common_data)}
    else:
        path = Path("/usr/bin/ssh")
        environment = {"PATH": "/usr/bin:/bin", "LANG": "C"}
    return path, environment


def run_once(helper, materials, ssh_path, ssh_environment) -> EvidenceResult:
    """启动唯一固定 SSH；无重试分支。"""
    remote_program = build_remote_program().encode("utf-8")
    with tempfile.TemporaryDirectory(prefix="g8-014-") as directory:
        approved_hosts = Path(directory) / "known_hosts"
        approved_hosts.write_text(materials.approved_host_line + "\n", encoding="ascii")
        command = [
            str(ssh_path), "-F", "none", "-p", TARGET_PORT,
            "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes",
            "-o", "ConnectionAttempts=1", "-o", "StrictHostKeyChecking=yes",
            "-o", "HostKeyAlgorithms=ssh-ed25519", "-o", "PasswordAuthentication=no",
            "-o", "KbdInteractiveAuthentication=no", "-o", "ForwardAgent=no",
            "-o", "ClearAllForwardings=yes", "-o", "RequestTTY=no",
            "-o", f"UserKnownHostsFile={approved_hosts}", "-i", str(materials.identity_file.path),
            f"{TARGET_USER}@{TARGET_HOST}",
            "/usr/bin/env -i PATH=/usr/bin:/bin /usr/bin/python3 -I -",
        ]
        process = None
        threads = ()
        try:
            process = subprocess.Popen(
                command,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                env=ssh_environment,
            )
            process.stdin.write(remote_program)
            process.stdin.close()
            captures = {}

            def collect(name, stream):
                captures[name] = collect_stream(stream, MAX_STREAM_BYTES)

            threads = (
                threading.Thread(target=collect, args=("stdout", process.stdout)),
                threading.Thread(target=collect, args=("stderr", process.stderr)),
            )
            for thread in threads:
                thread.start()
            try:
                returncode = process.wait(timeout=30)
            except subprocess.TimeoutExpired as exc:
                # 超时后必须终止并回收唯一 SSH，不能让后台进程或排空线程继续存活。
                process.kill()
                process.wait(timeout=5)
                for thread in threads:
                    thread.join(timeout=5)
                raise EvidenceError("ssh_unavailable") from exc
            for thread in threads:
                thread.join(timeout=5)
            if any(thread.is_alive() for thread in threads):
                process.kill()
                process.wait(timeout=5)
                raise EvidenceError("ssh_unavailable")
        except EvidenceError:
            raise
        except Exception as exc:
            if process is not None and process.poll() is None:
                process.kill()
                process.wait(timeout=5)
            for thread in threads:
                thread.join(timeout=5)
            raise EvidenceError("ssh_unavailable") from exc
        helper.assert_materials_unchanged(materials)
        stdout = captures.get("stdout")
        stderr = captures.get("stderr")
        if not stdout or not stderr or stdout.error or stderr.error:
            raise EvidenceError("ssh_unavailable")
        if returncode != 0 or stderr.byte_count != 0 or stdout.exceeded or stderr.exceeded:
            raise EvidenceError("ssh_unavailable")
        return parse_remote_output(stdout.data)


def _parser():
    parser = SafeArgumentParser(add_help=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--change-id")
    parser.add_argument("--known-hosts")
    parser.add_argument("--identity-file")
    parser.add_argument("--identity-public-key")
    return parser


def main() -> int:
    if CHANGE_ID_CONSUMED:
        print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_014=FAILED reason=change_id_consumed")
        return 2
    try:
        arguments = _parser().parse_args()
        if arguments.self_test:
            if any((arguments.change_id, arguments.known_hosts, arguments.identity_file, arguments.identity_public_key)):
                raise EvidenceError("invalid_request")
            compile(build_remote_program(), "<g8-014-remote>", "exec")
            load_local_diagnostic()
            print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_014_SELF_TEST=PASS")
            return 0
        if arguments.change_id != CHANGE_ID:
            raise EvidenceError("invalid_request")
        raw_paths = (arguments.known_hosts, arguments.identity_file, arguments.identity_public_key)
        if not all(raw_paths):
            raise EvidenceError("invalid_request")
        paths = tuple(Path(value) for value in raw_paths)
        if not all(path.is_absolute() for path in paths):
            raise EvidenceError("invalid_request")
        helper = load_local_diagnostic()
        materials = helper.diagnose_materials(*paths)
        # 正式 SSH 使用批准的原始私钥路径；派生 known_hosts 仅包含已批准的单一公钥条目。
        ssh_path, ssh_environment = fixed_ssh_runtime(helper)
        result = run_once(helper, materials, ssh_path, ssh_environment)
        helper.assert_materials_unchanged(materials)
        code, text = render_result(result)
        print(text, end="")
        return code
    except Exception:
        print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_014=FAILED reason=evidence_unavailable")
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
