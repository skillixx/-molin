#!/usr/bin/env python3
"""对 G8 测试服执行一次不泄漏 machine-id 的只读主机身份诊断。"""

import sys

# 必须在导入可替换模块前拒绝普通解释器，避免脚本目录或 PYTHONPATH 模块劫持。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_HOST_IDENTITY_DIAG=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import hashlib
import os
import stat
import subprocess
import threading
import types
from pathlib import Path


CHANGE_ID = "CHG-G8-TEST-READONLY-HOST-IDENTITY-DIAG-20260812-007"
TARGET_CHANGE_ID = "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-006"
# 007 尚未执行正式远端诊断；执行证据提交必须把该值改为 True 以关闭重放入口。
CHANGE_ID_CONSUMED = False
MAX_CAPTURE_BYTES = 64 * 1024
MAX_MACHINE_ID_BYTES = 4096
MACHINE_ID_STATES = frozenset({"READABLE_MATCH", "READABLE_MISMATCH", "UNREADABLE"})
EXPECTED_REMOTE_KEYS = frozenset({"DIAGNOSTIC_CHANGE_ID", "TARGET_CHANGE_ID", "MACHINE_ID_STATE"})
APPROVED_MACHINE_ID_SHA256 = "b60555f0d8d48731b657d21b2e54559d263210688125ae56a4d662fc4d7278d4"
STAGING_HELPER_SHA256 = "599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89"
STAGING_HELPER_EXPECTATIONS = {
    "CHANGE_ID": "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-004",
    "CHANGE_ID_CONSUMED": True,
    "TARGET": "pc@8.130.9.163",
    "TARGET_HOST": "8.130.9.163",
    "TARGET_PORT": "10003",
    "TARGET_MACHINE_ID_SHA256": APPROVED_MACHINE_ID_SHA256,
    "TARGET_SSH_ED25519_FINGERPRINT": "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I",
    "LOCAL_IDENTITY_ED25519_FINGERPRINT": "SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0",
}


class DiagnosticError(RuntimeError):
    """表示本地未能形成可信、低敏且可归属的诊断证据。"""


class SafeArgumentParser(argparse.ArgumentParser):
    """拒绝 argparse 回显调用方传入的路径或其他参数。"""

    def error(self, message: str) -> None:
        """把所有参数错误收敛为固定异常，不输出参数正文。"""
        raise RuntimeError("invalid_arguments")


def load_staging_helper():
    """在执行前冻结并核验 004 helper，复用其 SSH 身份与目标信任边界。"""
    helper_path = Path(__file__).with_name("run-ai-gateway-g8-test-staging-evidence.py")
    try:
        path_stat = os.lstat(helper_path)
        if not stat.S_ISREG(path_stat.st_mode):
            raise DiagnosticError("helper_type_mismatch")
        descriptor = os.open(helper_path, os.O_RDONLY | getattr(os, "O_BINARY", 0))
        try:
            opened_stat = os.fstat(descriptor)
            if (opened_stat.st_dev, opened_stat.st_ino) != (path_stat.st_dev, path_stat.st_ino):
                raise DiagnosticError("helper_identity_mismatch")
            with os.fdopen(descriptor, "rb", closefd=False) as stream:
                source = stream.read()
        finally:
            os.close(descriptor)
    except (OSError, DiagnosticError) as error:
        raise DiagnosticError("helper_load_failed") from error
    if hashlib.sha256(source).hexdigest() != STAGING_HELPER_SHA256:
        raise DiagnosticError("helper_digest_mismatch")
    module = types.ModuleType("g8_consumed_staging_evidence")
    module.__file__ = str(helper_path)
    try:
        exec(compile(source, str(helper_path), "exec"), module.__dict__)
    except Exception as error:
        raise DiagnosticError("helper_load_failed") from error
    if any(getattr(module, key, object()) != expected for key, expected in STAGING_HELPER_EXPECTATIONS.items()):
        raise DiagnosticError("helper_contract_mismatch")
    return module


def build_remote_program(
    machine_id_path: str = "/etc/machine-id", approved_sha256: str = APPROVED_MACHINE_ID_SHA256
) -> str:
    """构造只读取固定文件并仅返回三态的 ASCII 远端程序。"""
    return f"""import sys
import hashlib

if not sys.flags.isolated:
    raise SystemExit(41)

change_id = {CHANGE_ID!r}
target_change_id = {TARGET_CHANGE_ID!r}
machine_id_path = {machine_id_path!r}
approved_sha256 = {approved_sha256!r}
state = 'UNREADABLE'

try:
    stream = open(machine_id_path, 'rb')
    try:
        content = stream.read({MAX_MACHINE_ID_BYTES + 1})
    finally:
        stream.close()
    if content and len(content) <= {MAX_MACHINE_ID_BYTES}:
        try:
            current_sha256 = hashlib.sha256(content).hexdigest()
        except BaseException:
            state = 'UNREADABLE'
        else:
            state = 'READABLE_MATCH' if current_sha256 == approved_sha256 else 'READABLE_MISMATCH'
except BaseException:
    state = 'UNREADABLE'

output = (
    'DIAGNOSTIC_CHANGE_ID=' + change_id + '\\n'
    + 'TARGET_CHANGE_ID=' + target_change_id + '\\n'
    + 'MACHINE_ID_STATE=' + state + '\\n'
)
sys.stdout.buffer.write(output.encode('ascii'))
"""


def parse_ascii_key_values(payload: bytes) -> dict[str, str]:
    """解析严格 ASCII、末尾换行且禁止重复键的固定键值协议。"""
    if not payload or len(payload) > MAX_CAPTURE_BYTES or not payload.endswith(b"\n"):
        raise DiagnosticError("remote_contract_mismatch")
    try:
        text = payload.decode("ascii")
    except UnicodeDecodeError as error:
        raise DiagnosticError("remote_contract_mismatch") from error
    values: dict[str, str] = {}
    lines = text[:-1].split("\n")
    if len(lines) != len(EXPECTED_REMOTE_KEYS):
        raise DiagnosticError("remote_contract_mismatch")
    for line in lines:
        if line.count("=") != 1:
            raise DiagnosticError("remote_contract_mismatch")
        key, value = line.split("=", 1)
        if not key or not value or key in values:
            raise DiagnosticError("remote_contract_mismatch")
        values[key] = value
    return values


def parse_remote_output(stdout: bytes) -> str:
    """验证远端精确三键、证据归属和状态枚举，不接收任何扩展字段。"""
    values = parse_ascii_key_values(stdout)
    if set(values) != EXPECTED_REMOTE_KEYS:
        raise DiagnosticError("remote_contract_mismatch")
    if values["DIAGNOSTIC_CHANGE_ID"] != CHANGE_ID or values["TARGET_CHANGE_ID"] != TARGET_CHANGE_ID:
        raise DiagnosticError("remote_contract_mismatch")
    state = values["MACHINE_ID_STATE"]
    if state not in MACHINE_ID_STATES:
        raise DiagnosticError("remote_contract_mismatch")
    return state


def collect_stream(stream, result: dict[str, object]) -> None:
    """持续排空管道，有界保留正文并累计完整长度、行数和摘要。"""
    captured = bytearray()
    digest = hashlib.sha256()
    total = 0
    line_breaks = 0
    last_byte = b""
    try:
        while True:
            chunk = stream.read(8192)
            if not chunk:
                break
            digest.update(chunk)
            total += len(chunk)
            line_breaks += chunk.count(b"\n")
            last_byte = chunk[-1:]
            if len(captured) <= MAX_CAPTURE_BYTES:
                remaining = MAX_CAPTURE_BYTES + 1 - len(captured)
                captured.extend(chunk[:remaining])
    except Exception:
        result["error"] = True
        return
    result.update(
        {
            "captured": bytes(captured),
            "bytes": total,
            "lines": line_breaks + (1 if total and last_byte != b"\n" else 0),
            "sha256": "NONE" if total == 0 else digest.hexdigest(),
            "exceeded": total > MAX_CAPTURE_BYTES,
            "error": False,
        }
    )


def run_bounded_process(
    command: list[str], environment: dict[str, str]
) -> tuple[int, dict[str, object], dict[str, object]]:
    """并发有界排空 SSH 输出，避免异常远端输出耗尽本机内存。"""
    try:
        process = subprocess.Popen(
            command,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=environment,
        )
    except OSError as error:
        raise DiagnosticError("ssh_execution_failed") from error
    if process.stdin is None or process.stdout is None or process.stderr is None:
        process.kill()
        raise DiagnosticError("ssh_pipe_failed")
    stdout_result: dict[str, object] = {}
    stderr_result: dict[str, object] = {}
    stdout_thread = threading.Thread(target=collect_stream, args=(process.stdout, stdout_result), daemon=True)
    stderr_thread = threading.Thread(target=collect_stream, args=(process.stderr, stderr_result), daemon=True)
    stdout_thread.start()
    stderr_thread.start()
    try:
        process.stdin.write(build_remote_program().encode("ascii"))
        process.stdin.close()
        returncode = process.wait(timeout=30)
    except (OSError, subprocess.TimeoutExpired) as error:
        process.kill()
        process.wait()
        raise DiagnosticError("ssh_execution_failed") from error
    finally:
        stdout_thread.join()
        stderr_thread.join()
        process.stdout.close()
        process.stderr.close()
    if stdout_result.get("error") or stderr_result.get("error"):
        raise DiagnosticError("ssh_pipe_failed")
    return returncode, stdout_result, stderr_result


def run_once(helper, known_hosts: Path, identity_file: Path) -> str:
    """使用固定 OpenSSH 参数执行唯一一次远端只读诊断并返回可信三态。"""
    try:
        ssh_executable = helper.fixed_ssh_executable()
        ssh_environment = helper.fixed_ssh_environment()
    except Exception as error:
        raise DiagnosticError("ssh_configuration_failed") from error
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
    returncode, stdout_result, stderr_result = run_bounded_process(command, ssh_environment)
    if (
        returncode != 0
        or stdout_result.get("error")
        or stderr_result.get("error")
        or stdout_result.get("exceeded")
        or stderr_result.get("exceeded")
        or stderr_result.get("bytes") != 0
    ):
        raise DiagnosticError("diagnostic_unavailable")
    captured = stdout_result.get("captured")
    if not isinstance(captured, bytes) or stdout_result.get("bytes") != len(captured):
        raise DiagnosticError("diagnostic_unavailable")
    return parse_remote_output(captured)


def main() -> int:
    """先完成离线身份门禁，再按未来独立授权执行至多一次只读 SSH。"""
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
        print("G8_TEST_READONLY_HOST_IDENTITY_DIAG=FAILED reason=invalid_request")
        return 2
    if arguments.self_test:
        try:
            remote_program = build_remote_program()
            compile(remote_program, "<g8-host-identity-diagnostic>", "exec")
            helper = load_staging_helper()
        except Exception:
            print("G8_TEST_READONLY_HOST_IDENTITY_DIAG=FAILED reason=invalid_program")
            return 2
        if any(marker in remote_program for marker in ("import os", "import pwd", "import grp", "subprocess", "remove(", "unlink(", "rmdir(", "sudo")) or not helper.CHANGE_ID_CONSUMED:
            print("G8_TEST_READONLY_HOST_IDENTITY_DIAG=FAILED reason=unsafe_program")
            return 2
        print("G8_TEST_READONLY_HOST_IDENTITY_DIAG_SELF_TEST=PASS")
        return 0
    if CHANGE_ID_CONSUMED:
        print("G8_TEST_READONLY_HOST_IDENTITY_DIAG=FAILED reason=change_id_consumed")
        return 2
    if (
        arguments.change_id != CHANGE_ID
        or not arguments.known_hosts
        or not arguments.identity_file
        or not arguments.identity_public_file
    ):
        print("G8_TEST_READONLY_HOST_IDENTITY_DIAG=FAILED reason=invalid_request")
        return 2
    try:
        helper = load_staging_helper()
        known_hosts = Path(arguments.known_hosts)
        identity_file = Path(arguments.identity_file)
        identity_public_file = Path(arguments.identity_public_file)
        helper.validate_known_hosts(known_hosts)
        helper.validate_identity_file(identity_file, identity_public_file, known_hosts)
        helper.validate_identity_pair(identity_file, identity_public_file)
    except Exception:
        print("G8_TEST_READONLY_HOST_IDENTITY_DIAG=FAILED reason=local_validation_failed")
        return 2
    if arguments.local_check:
        print("G8_TEST_READONLY_HOST_IDENTITY_DIAG_LOCAL_CHECK=PASS")
        return 0
    try:
        state = run_once(helper, known_hosts, identity_file)
    except DiagnosticError:
        print("G8_TEST_READONLY_HOST_IDENTITY_DIAG=FAILED reason=diagnostic_unavailable")
        return 2
    print("G8_TEST_READONLY_HOST_IDENTITY_DIAG=PASS" if state == "READABLE_MATCH" else "G8_TEST_READONLY_HOST_IDENTITY_DIAG=BLOCKED")
    print(f"change_id={CHANGE_ID}")
    print(f"target_change_id={TARGET_CHANGE_ID}")
    print(f"machine_id_state={state}")
    return 0 if state == "READABLE_MATCH" else 3


if __name__ == "__main__":
    raise SystemExit(main())
