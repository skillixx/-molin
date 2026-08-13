#!/usr/bin/env python3
"""对 Drop 映射测试入口执行一次完全只读的 003 暂存低敏取证。"""

import sys

# 必须先拒绝非隔离解释器，避免脚本目录或 PYTHONPATH 中的同名模块劫持本地校验。
if not sys.flags.isolated:
    print(
        "G8_TEST_READONLY_DROP_STAGING_EVIDENCE=FAILED "
        "reason=isolated_python_required"
    )
    raise SystemExit(2)

import argparse
import hashlib
import os
import re
import stat
import subprocess
import textwrap
import threading
import types
from dataclasses import dataclass
from pathlib import Path


CHANGE_ID = "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-008"
CHANGE_ID_CONSUMED = False
TARGET_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-20260812-003"
TARGET_DEPLOYMENT_ROOT = "/home/pc/molin"
STAGING_PATH = (
    "/home/pc/molin/"
    ".g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003"
)
FROZEN_FILES = {
    "SHA256SUMS": (
        "82b18d6040bcd6be72cf170fa066ecd7cf469a53f4901365f379bec5a89c496d",
        362,
    ),
    "ai-gateway-reconcile": (
        "37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1",
        13_066_129,
    ),
    "g8-test-readonly-audit": (
        "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256",
        18_377,
    ),
    "manifest.env": (
        "726174ea41ecfee69f9d8c1aff7411dc9a8c73f3dc60ca0d5e700eb5f962ea66",
        897,
    ),
    "molin-g8-test-readonly-audit.sudoers": (
        "1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f",
        416,
    ),
}
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


class EvidenceError(RuntimeError):
    """表示远端输出未形成完整、低敏且可验证的暂存证据。"""


class SafeArgumentParser(argparse.ArgumentParser):
    """拒绝 argparse 回显调用方传入的路径和参数。"""

    def error(self, message: str) -> None:
        """所有参数错误都收敛为固定低敏异常。"""
        raise EvidenceError("invalid_arguments")


FROZEN_HELPER_SHA256 = (
    "599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89"
)
MAX_CAPTURE_BYTES = 64 * 1024


@dataclass(frozen=True)
class StreamCapture:
    """保存有界正文和完整流的低敏聚合信息。"""

    data: bytes
    byte_count: int
    line_count: int
    sha256: str
    exceeded: bool
    error: bool


def collect_stream(stream, limit: int = MAX_CAPTURE_BYTES) -> StreamCapture:
    """有界排空一个管道，同时累计完整字节数、行数和摘要。"""
    kept = bytearray()
    digest = hashlib.sha256()
    byte_count = 0
    line_count = 0
    failed = False
    try:
        while True:
            block = stream.read(8192)
            if not block:
                break
            byte_count += len(block)
            line_count += block.count(b"\n")
            digest.update(block)
            if len(kept) < limit + 1:
                kept.extend(block[: limit + 1 - len(kept)])
    except Exception:
        failed = True
    return StreamCapture(
        data=bytes(kept),
        byte_count=byte_count,
        line_count=line_count,
        sha256="NONE" if byte_count == 0 else digest.hexdigest(),
        exceeded=byte_count > limit,
        error=failed,
    )


def load_frozen_helper(path: Path | None = None):
    """按普通文件、inode、摘要和 Drop 传输契约冻结已消费的 004 helper。"""
    helper_path = path or Path(__file__).with_name(
        "run-ai-gateway-g8-test-staging-evidence.py"
    )
    try:
        before = os.lstat(helper_path)
        if not stat.S_ISREG(before.st_mode):
            raise EvidenceError("helper_type_mismatch")
        descriptor = os.open(helper_path, os.O_RDONLY | getattr(os, "O_BINARY", 0))
        try:
            opened = os.fstat(descriptor)
            if (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
                raise EvidenceError("helper_identity_mismatch")
            with os.fdopen(descriptor, "rb", closefd=False) as stream:
                source = stream.read()
            after = os.fstat(descriptor)
            if (after.st_dev, after.st_ino) != (opened.st_dev, opened.st_ino):
                raise EvidenceError("helper_drift")
        finally:
            os.close(descriptor)
        current = os.lstat(helper_path)
        if (current.st_dev, current.st_ino) != (opened.st_dev, opened.st_ino):
            raise EvidenceError("helper_path_drift")
    except (OSError, EvidenceError) as error:
        raise EvidenceError("helper_load_failed") from error
    if hashlib.sha256(source).hexdigest() != FROZEN_HELPER_SHA256:
        raise EvidenceError("helper_digest_mismatch")
    module = types.ModuleType("g8_drop_frozen_helper")
    module.__file__ = str(helper_path)
    try:
        exec(compile(source, str(helper_path), "exec"), module.__dict__)
    except Exception as error:
        raise EvidenceError("helper_load_failed") from error
    expected = {
        "CHANGE_ID_CONSUMED": True,
        "TARGET": "pc@8.130.9.163",
        "TARGET_HOST": "8.130.9.163",
        "TARGET_PORT": "10003",
        "TARGET_SSH_ED25519_FINGERPRINT": (
            "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"
        ),
        "LOCAL_IDENTITY_ED25519_FINGERPRINT": (
            "SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0"
        ),
    }
    if any(getattr(module, key, object()) != value for key, value in expected.items()):
        raise EvidenceError("helper_contract_mismatch")
    return module


def build_remote_program(
    *,
    deployment_root: str = TARGET_DEPLOYMENT_ROOT,
    staging_path: str = STAGING_PATH,
    expected_files: dict[str, tuple[str, int]] | None = None,
    _test_uid: int | None = None,
    _test_gid: int | None = None,
) -> str:
    """生成目录描述符锚定且不含任何写改删能力的远端只读程序。"""
    files = FROZEN_FILES if expected_files is None else expected_files

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
import stat

evidence_change_id = __CHANGE_ID__
target_change_id = __TARGET_CHANGE_ID__
deployment_root = __DEPLOYMENT_ROOT__
staging_path = __STAGING_PATH__
expected_files = __EXPECTED_FILES__
__IDENTITY_SETUP__

def metadata_identity(value):
    return (
        value.st_dev, value.st_ino, value.st_mode, value.st_uid, value.st_gid,
        value.st_size, value.st_mtime_ns, value.st_ctime_ns,
    )

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
        (pinned_root.st_dev, pinned_root.st_ino) != (root_meta.st_dev, root_meta.st_ino)
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
                    if (pinned_stage.st_dev, pinned_stage.st_ino) == (stage_meta.st_dev, stage_meta.st_ino):
                        names = os.listdir(stage_fd)
                        if set(names) != set(expected_files) or len(names) != len(expected_files):
                            staging_mismatch_reason = 'FILE_SET'
                        else:
                            metadata_matches = True
                            entries_stable = True
                            content_matches = True
                            opened_files = {}
                            for name in names:
                                file_fd = os.open(
                                    name,
                                    os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC,
                                    dir_fd=stage_fd,
                                )
                                try:
                                    before = os.fstat(file_fd)
                                    expected_sha256, expected_size = expected_files[name]
                                    opened_files[name] = metadata_identity(before)
                                    valid_metadata = (
                                        stat.S_ISREG(before.st_mode)
                                        and before.st_uid == uid
                                        and before.st_gid == gid
                                        and not stat.S_IMODE(before.st_mode) & 0o022
                                        and before.st_size == expected_size
                                    )
                                    if valid_metadata:
                                        digest = hashlib.sha256()
                                        while True:
                                            block = os.read(file_fd, 1024 * 1024)
                                            if not block:
                                                break
                                            digest.update(block)
                                        actual_digest = digest.hexdigest()
                                    else:
                                        metadata_matches = False
                                        actual_digest = None
                                    if metadata_identity(os.fstat(file_fd)) != opened_files[name]:
                                        entries_stable = False
                                    if actual_digest is not None and actual_digest != expected_sha256:
                                        content_matches = False
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
                            final_stage = os.fstat(stage_fd)
                            current_stage = os.stat(stage_name, dir_fd=root_fd, follow_symlinks=False)
                            if (
                                not entries_stable
                                or (final_stage.st_dev, final_stage.st_ino, final_stage.st_mtime_ns, final_stage.st_ctime_ns)
                                != (pinned_stage.st_dev, pinned_stage.st_ino, pinned_stage.st_mtime_ns, pinned_stage.st_ctime_ns)
                                or (current_stage.st_dev, current_stage.st_ino)
                                != (pinned_stage.st_dev, pinned_stage.st_ino)
                            ):
                                staging_mismatch_reason = 'PATH'
                            elif not metadata_matches:
                                staging_mismatch_reason = 'FILE_METADATA'
                            elif not content_matches:
                                staging_mismatch_reason = 'FILE_CONTENT'
                            else:
                                staging_integrity = 'PASS'
                                staging_mismatch_reason = 'NONE'
                finally:
                    os.close(stage_fd)
        except OSError:
            staging_mismatch_reason = 'READ_ERROR'

    current_root = os.lstat(deployment_root)
    if (
        os.path.realpath(deployment_root) != deployment_root
        or (current_root.st_dev, current_root.st_ino) != (pinned_root.st_dev, pinned_root.st_ino)
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
    return textwrap.dedent(template).replace("__CHANGE_ID__", repr(CHANGE_ID)).replace(
        "__TARGET_CHANGE_ID__", repr(TARGET_CHANGE_ID)
    ).replace("__DEPLOYMENT_ROOT__", repr(deployment_root)).replace(
        "__STAGING_PATH__", repr(staging_path)
    ).replace("__EXPECTED_FILES__", repr(files)).replace(
        "__IDENTITY_SETUP__", identity_setup
    )


def parse_remote_output(
    stdout: str,
    *,
    expected_deployment_root: str = TARGET_DEPLOYMENT_ROOT,
) -> dict[str, str]:
    """解析九键低敏结果，并拒绝额外身份字段或不一致状态组合。"""
    try:
        stdout.encode("ascii")
    except UnicodeEncodeError as error:
        raise EvidenceError("non_ascii_output") from error

    values: dict[str, str] = {}
    lines = stdout.splitlines()
    if len(lines) != len(EXPECTED_REMOTE_KEYS):
        raise EvidenceError("remote_key_count_mismatch")
    for line in lines:
        match = re.fullmatch(r"([A-Z0-9_]+)=([^\r\n]+)", line)
        if match is None or match.group(1) in values:
            raise EvidenceError("invalid_remote_output")
        values[match.group(1)] = match.group(2)
    if set(values) != EXPECTED_REMOTE_KEYS:
        raise EvidenceError("remote_key_set_mismatch")

    expected = {
        "EVIDENCE_CHANGE_ID": CHANGE_ID,
        "TARGET_CHANGE_ID": TARGET_CHANGE_ID,
        "LOGIN_USER": "pc",
        "DEPLOYMENT_ROOT_REALPATH": expected_deployment_root,
        "DEPLOYMENT_ROOT_CHECK": "PASS",
        "EVIDENCE_RESULT": "PASS",
    }
    if any(values[key] != value for key, value in expected.items()):
        raise EvidenceError("remote_contract_mismatch")

    state = (
        values["STAGING_STATE"],
        values["STAGING_INTEGRITY"],
        values["STAGING_MISMATCH_REASON"],
    )
    valid_states = {
        ("ABSENT", "NOT_APPLICABLE", "NONE"),
        ("PRESENT", "PASS", "NONE"),
        *(("PRESENT", "MISMATCH", reason) for reason in (
            "PATH",
            "FILE_SET",
            "FILE_METADATA",
            "FILE_CONTENT",
            "READ_ERROR",
        )),
    }
    if state not in valid_states:
        raise EvidenceError("invalid_staging_state")
    return values


def run_once(
    helper,
    known_hosts: Path,
    identity_file: Path,
) -> dict[str, str]:
    """使用固定 OpenSSH 参数执行唯一一次 Drop 暂存只读取证。"""
    try:
        ssh_executable = helper.fixed_ssh_executable()
        environment = helper.fixed_ssh_environment()
    except Exception as error:
        raise EvidenceError("ssh_configuration_failed") from error
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
        helper.TARGET_PORT,
        helper.TARGET,
        "/usr/bin/env",
        "-i",
        "PATH=/usr/bin:/bin",
        "/usr/bin/python3",
        "-I",
        "-",
    ]
    try:
        process = subprocess.Popen(
            command,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=environment,
        )
    except OSError as error:
        raise EvidenceError("ssh_execution_failed") from error
    if process.stdin is None or process.stdout is None or process.stderr is None:
        process.kill()
        raise EvidenceError("ssh_pipe_failed")

    captures: dict[str, StreamCapture] = {}

    def capture(name: str, stream) -> None:
        captures[name] = collect_stream(stream)

    threads = (
        threading.Thread(target=capture, args=("stdout", process.stdout), daemon=True),
        threading.Thread(target=capture, args=("stderr", process.stderr), daemon=True),
    )
    for thread in threads:
        thread.start()
    try:
        process.stdin.write(build_remote_program().encode("utf-8"))
        process.stdin.close()
        returncode = process.wait(timeout=30)
    except (OSError, subprocess.TimeoutExpired) as error:
        process.kill()
        process.wait()
        raise EvidenceError("ssh_execution_failed") from error
    finally:
        for thread in threads:
            thread.join()
        process.stdout.close()
        process.stderr.close()
    stdout = captures.get("stdout")
    stderr = captures.get("stderr")
    if stdout is None or stderr is None or stdout.error or stderr.error:
        raise EvidenceError("ssh_pipe_failed")
    if (
        returncode != 0
        or stderr.byte_count != 0
        or stdout.exceeded
        or stderr.exceeded
    ):
        raise EvidenceError("remote_evidence_failed")
    try:
        text = stdout.data.decode("ascii", errors="strict")
    except UnicodeError as error:
        raise EvidenceError("invalid_remote_encoding") from error
    return parse_remote_output(text)


def main() -> int:
    """先执行离线门禁，再按独立授权至多发起一次只读 SSH。"""
    parser = SafeArgumentParser(add_help=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--local-check", action="store_true")
    parser.add_argument("--change-id")
    parser.add_argument("--known-hosts")
    parser.add_argument("--identity-file")
    parser.add_argument("--identity-public-file")
    try:
        arguments = parser.parse_args()
    except EvidenceError:
        print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE=FAILED reason=invalid_request")
        return 2
    if arguments.self_test:
        try:
            load_frozen_helper()
            compile(build_remote_program(), "<g8-drop-staging-evidence>", "exec")
        except Exception:
            print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE=FAILED reason=invalid_program")
            return 2
        print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_SELF_TEST=PASS")
        return 0
    if CHANGE_ID_CONSUMED:
        print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE=FAILED reason=change_id_consumed")
        return 2
    if (
        arguments.change_id != CHANGE_ID
        or not arguments.known_hosts
        or not arguments.identity_file
        or not arguments.identity_public_file
    ):
        print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE=FAILED reason=invalid_request")
        return 2
    try:
        helper = load_frozen_helper()
        known_hosts = Path(arguments.known_hosts)
        identity_file = Path(arguments.identity_file)
        identity_public_file = Path(arguments.identity_public_file)
        helper.validate_known_hosts(known_hosts)
        helper.validate_identity_file(identity_file, identity_public_file, known_hosts)
        helper.validate_identity_pair(identity_file, identity_public_file)
    except Exception:
        print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE=FAILED reason=evidence_unavailable")
        return 2
    if arguments.local_check:
        print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_LOCAL_CHECK=PASS")
        return 0
    try:
        values = run_once(helper, known_hosts, identity_file)
    except Exception:
        print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE=FAILED reason=evidence_unavailable")
        return 2
    print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE=PASS")
    print(f"staging_state={values['STAGING_STATE']}")
    print(f"staging_integrity={values['STAGING_INTEGRITY']}")
    print(f"staging_mismatch_reason={values['STAGING_MISMATCH_REASON']}")
    print("business_requests=0 upstream_requests=0 cost_cny=0")
    if values["STAGING_INTEGRITY"] == "MISMATCH":
        return 3
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
