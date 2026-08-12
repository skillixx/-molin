#!/usr/bin/env python3
"""对 G8 测试服执行一次不读取远端文件的低敏 SSH 传输诊断。"""

import sys

# 必须在加载仓库辅助模块前拒绝普通解释器，避免脚本目录或 PYTHONPATH 模块劫持。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_TRANSPORT_DIAG=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import hashlib
import os
import stat
import subprocess
import threading
import types
from pathlib import Path


CHANGE_ID = "CHG-G8-TEST-READONLY-TRANSPORT-DIAG-20260812-005"
# 005 已完成唯一一次正式调用；普通入口必须在读取身份文件或联网前拒绝重放。
CHANGE_ID_CONSUMED = True
TARGET_CHANGE_ID = "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-004"
REMOTE_MARKER = b"G8_TEST_READONLY_TRANSPORT_REMOTE=PASS\n"
MAX_CAPTURE_BYTES = 64 * 1024
STAGING_HELPER_SHA256 = "599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89"
STAGING_HELPER_EXPECTATIONS = {
    "CHANGE_ID": TARGET_CHANGE_ID,
    "CHANGE_ID_CONSUMED": True,
    "TARGET": "pc@8.130.9.163",
    "TARGET_HOST": "8.130.9.163",
    "TARGET_PORT": "10003",
    "TARGET_SSH_ED25519_FINGERPRINT": "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I",
    "LOCAL_IDENTITY_ED25519_FINGERPRINT": "SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0",
}
REMOTE_PROGRAM = """import sys

if not sys.flags.isolated:
    raise SystemExit(41)
print('G8_TEST_READONLY_TRANSPORT_REMOTE=PASS')
"""


class DiagnosticError(RuntimeError):
    """表示本地未能形成可验证的低敏传输诊断。"""


class SafeArgumentParser(argparse.ArgumentParser):
    """拒绝 argparse 回显调用方提供的路径或其他参数。"""

    def error(self, message: str) -> None:
        """将参数错误收敛为固定低敏异常。"""
        raise RuntimeError("invalid_arguments")


def load_staging_helper():
    """校验并执行冻结的同目录辅助脚本，避免辅助实现漂移后改变 SSH 目标。"""
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
        raise DiagnosticError("helper_load_failed")
    if any(getattr(module, key, object()) != expected for key, expected in STAGING_HELPER_EXPECTATIONS.items()):
        raise DiagnosticError("helper_contract_mismatch")
    return module


def collect_stream(stream, result: dict[str, object]) -> None:
    """持续排空单个管道，只保留固定上限正文，同时流式累计长度、行数和摘要。"""
    digest = hashlib.sha256()
    captured = bytearray()
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


def run_bounded_process(command: list[str], environment: dict[str, str]) -> tuple[int, dict[str, object], dict[str, object]]:
    """并发排空 SSH 输出并限制内存正文，防止异常远端输出耗尽本机内存。"""
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
        process.stdin.write(REMOTE_PROGRAM.encode("ascii"))
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


def classify_stream_result(
    returncode: int, stdout_result: dict[str, object], stderr_result: dict[str, object]
) -> dict[str, str]:
    """把有界流式采集结果收敛为固定分类、计数和不可逆摘要。"""
    if returncode == 0:
        exit_class = "ZERO"
    elif returncode == 255:
        exit_class = "TRANSPORT_255"
    elif 1 <= returncode <= 254:
        exit_class = "REMOTE_NONZERO"
    else:
        exit_class = "OTHER_NONZERO"
    stdout_contract = "EXACT" if stdout_result["captured"] == REMOTE_MARKER else "MISMATCH"
    stderr_state = "EMPTY" if stderr_result["bytes"] == 0 else "PRESENT"
    if stdout_result["exceeded"] or stderr_result["exceeded"]:
        diagnostic = "OUTPUT_LIMIT_EXCEEDED"
    elif exit_class != "ZERO":
        diagnostic = "EXIT_NONZERO"
    elif stderr_state != "EMPTY":
        diagnostic = "STDERR_PRESENT"
    elif stdout_contract != "EXACT":
        diagnostic = "STDOUT_MISMATCH"
    else:
        diagnostic = "PASS"
    return {
        "ssh_exit_class": exit_class,
        "stdout_contract": stdout_contract,
        "stdout_bytes": str(stdout_result["bytes"]),
        "stdout_sha256": str(stdout_result["sha256"]),
        "stderr_state": stderr_state,
        "stderr_lines": str(stderr_result["lines"]),
        "stderr_bytes": str(stderr_result["bytes"]),
        "stderr_sha256": str(stderr_result["sha256"]),
        "diagnostic": diagnostic,
    }


def sha256_or_none(value: bytes) -> str:
    """空输出使用固定枚举，非空输出只保存摘要而不暴露正文。"""
    return "NONE" if not value else hashlib.sha256(value).hexdigest()


def classify_result(result: subprocess.CompletedProcess[bytes]) -> dict[str, str]:
    """把 SSH 原始结果收敛为固定分类、计数和不可逆摘要。"""
    stdout_result = {
        "captured": result.stdout[: MAX_CAPTURE_BYTES + 1],
        "bytes": len(result.stdout),
        "lines": len(result.stdout.splitlines()),
        "sha256": sha256_or_none(result.stdout),
        "exceeded": len(result.stdout) > MAX_CAPTURE_BYTES,
    }
    stderr_result = {
        "captured": result.stderr[: MAX_CAPTURE_BYTES + 1],
        "bytes": len(result.stderr),
        "lines": len(result.stderr.splitlines()),
        "sha256": sha256_or_none(result.stderr),
        "exceeded": len(result.stderr) > MAX_CAPTURE_BYTES,
    }
    return classify_stream_result(result.returncode, stdout_result, stderr_result)


def run_transport_diagnostic(helper, known_hosts: Path, identity_file: Path) -> dict[str, str]:
    """使用固定 OpenSSH 参数只调用一次远端隔离 Python 标记程序。"""
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
    return classify_stream_result(returncode, stdout_result, stderr_result)


def main() -> int:
    """先完成本地身份门禁，再按新授权执行至多一次低敏传输诊断。"""
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
        print("G8_TEST_READONLY_TRANSPORT_DIAG=FAILED reason=invalid_request")
        return 2
    if arguments.self_test:
        try:
            compile(REMOTE_PROGRAM, "<g8-transport-diagnostic>", "exec")
            helper = load_staging_helper()
        except Exception:
            print("G8_TEST_READONLY_TRANSPORT_DIAG=FAILED reason=invalid_program")
            return 2
        if any(
            marker in REMOTE_PROGRAM
            for marker in ("import os", "import pwd", "open(", "subprocess", "remove(", "unlink(", "rmdir(", "sudo")
        ) or not helper.CHANGE_ID_CONSUMED:
            print("G8_TEST_READONLY_TRANSPORT_DIAG=FAILED reason=unsafe_program")
            return 2
        print("G8_TEST_READONLY_TRANSPORT_DIAG_SELF_TEST=PASS")
        return 0
    if CHANGE_ID_CONSUMED:
        print("G8_TEST_READONLY_TRANSPORT_DIAG=FAILED reason=change_id_consumed")
        return 2
    if (
        arguments.change_id != CHANGE_ID
        or not arguments.known_hosts
        or not arguments.identity_file
        or not arguments.identity_public_file
    ):
        print("G8_TEST_READONLY_TRANSPORT_DIAG=FAILED reason=invalid_request")
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
        print("G8_TEST_READONLY_TRANSPORT_DIAG=FAILED reason=local_validation_failed")
        return 2
    if arguments.local_check:
        print("G8_TEST_READONLY_TRANSPORT_DIAG_LOCAL_CHECK=PASS")
        return 0
    try:
        evidence = run_transport_diagnostic(helper, known_hosts, identity_file)
    except DiagnosticError:
        print("G8_TEST_READONLY_TRANSPORT_DIAG=FAILED reason=diagnostic_unavailable")
        return 2
    blocked = evidence["diagnostic"] != "PASS"
    print("G8_TEST_READONLY_TRANSPORT_DIAG=BLOCKED" if blocked else "G8_TEST_READONLY_TRANSPORT_DIAG=PASS")
    print(f"change_id={CHANGE_ID}")
    print(f"target_change_id={TARGET_CHANGE_ID}")
    for key in (
        "ssh_exit_class",
        "stdout_contract",
        "stdout_bytes",
        "stdout_sha256",
        "stderr_state",
        "stderr_lines",
        "stderr_bytes",
        "stderr_sha256",
        "diagnostic",
    ):
        print(f"{key}={evidence[key]}")
    return 3 if blocked else 0


if __name__ == "__main__":
    raise SystemExit(main())
