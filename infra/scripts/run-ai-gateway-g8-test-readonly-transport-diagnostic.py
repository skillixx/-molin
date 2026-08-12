#!/usr/bin/env python3
"""对 G8 测试服执行一次不读取远端文件的低敏 SSH 传输诊断。"""

import sys

# 必须在加载仓库辅助模块前拒绝普通解释器，避免脚本目录或 PYTHONPATH 模块劫持。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_TRANSPORT_DIAG=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import hashlib
import importlib.util
import subprocess
from pathlib import Path


CHANGE_ID = "CHG-G8-TEST-READONLY-TRANSPORT-DIAG-20260812-005"
TARGET_CHANGE_ID = "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-004"
REMOTE_MARKER = b"G8_TEST_READONLY_TRANSPORT_REMOTE=PASS\n"
REMOTE_PROGRAM = """import os
import pwd
import sys

if not sys.flags.isolated or pwd.getpwuid(os.getuid()).pw_name != 'pc':
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
    """从固定同目录文件加载已审计的 SSH 身份校验辅助函数。"""
    helper_path = Path(__file__).with_name("run-ai-gateway-g8-test-staging-evidence.py")
    spec = importlib.util.spec_from_file_location("g8_consumed_staging_evidence", helper_path)
    if spec is None or spec.loader is None:
        raise DiagnosticError("helper_load_failed")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def sha256_or_none(value: bytes) -> str:
    """空输出使用固定枚举，非空输出只保存摘要而不暴露正文。"""
    return "NONE" if not value else hashlib.sha256(value).hexdigest()


def classify_result(result: subprocess.CompletedProcess[bytes]) -> dict[str, str]:
    """把 SSH 原始结果收敛为固定分类、计数和不可逆摘要。"""
    if result.returncode == 0:
        exit_class = "ZERO"
    elif result.returncode == 255:
        exit_class = "TRANSPORT_255"
    elif 1 <= result.returncode <= 254:
        exit_class = "REMOTE_NONZERO"
    else:
        exit_class = "OTHER_NONZERO"

    stdout_contract = "EXACT" if result.stdout == REMOTE_MARKER else "MISMATCH"
    stderr_state = "EMPTY" if not result.stderr else "PRESENT"
    if exit_class != "ZERO":
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
        "stdout_bytes": str(len(result.stdout)),
        "stdout_sha256": sha256_or_none(result.stdout),
        "stderr_state": stderr_state,
        "stderr_lines": str(len(result.stderr.splitlines())),
        "stderr_bytes": str(len(result.stderr)),
        "stderr_sha256": sha256_or_none(result.stderr),
        "diagnostic": diagnostic,
    }


def run_transport_diagnostic(helper, known_hosts: Path, identity_file: Path) -> dict[str, str]:
    """使用固定 OpenSSH 参数只调用一次远端隔离 Python 标记程序。"""
    command = [
        str(helper.fixed_ssh_executable()),
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
        result = subprocess.run(
            command,
            input=REMOTE_PROGRAM.encode("ascii"),
            capture_output=True,
            timeout=30,
            env=helper.fixed_ssh_environment(),
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise DiagnosticError("ssh_execution_failed") from error
    return classify_result(result)


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
            for marker in ("open(", "subprocess", "remove(", "unlink(", "rmdir(", "sudo")
        ) or not helper.CHANGE_ID_CONSUMED:
            print("G8_TEST_READONLY_TRANSPORT_DIAG=FAILED reason=unsafe_program")
            return 2
        print("G8_TEST_READONLY_TRANSPORT_DIAG_SELF_TEST=PASS")
        return 0
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
