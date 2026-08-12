#!/usr/bin/env python3
"""对 003 暂存路径执行一次带固定门禁分类的完全只读取证。"""

import sys

# 必须在导入仓库辅助代码前拒绝普通解释器，防止脚本目录或 PYTHONPATH 模块劫持。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_STAGING_EVIDENCE_V2=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import hashlib
import os
import stat
import subprocess
import threading
import types
from pathlib import Path


CHANGE_ID = "CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-006"
TARGET_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-20260812-003"
HELPER_SHA256 = "599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89"
MAX_CAPTURE_BYTES = 64 * 1024
BLOCKED_KEYS = {
    "EVIDENCE_CHANGE_ID",
    "TARGET_CHANGE_ID",
    "GATE_RESULT",
    "GATE_REASON",
}
GATE_REASONS = {
    "IDENTITY",
    "MACHINE_ID",
    "DEPLOYMENT_ROOT_PATH",
    "DEPLOYMENT_ROOT_METADATA",
    "STAGING_LOOKUP",
    "DEPLOYMENT_ROOT_DRIFT",
}


class EvidenceError(RuntimeError):
    """表示未形成完整、低敏且可验证的暂存证据。"""


class SafeArgumentParser(argparse.ArgumentParser):
    """拒绝 argparse 回显调用方传入的路径和参数。"""

    def error(self, message: str) -> None:
        """把所有参数错误收敛为固定低敏异常。"""
        raise RuntimeError("invalid_arguments")


def load_frozen_helper():
    """在执行前按普通文件、inode 和摘要冻结 004 竞态安全取证实现。"""
    helper_path = Path(__file__).with_name("run-ai-gateway-g8-test-staging-evidence.py")
    try:
        path_stat = os.lstat(helper_path)
        if not stat.S_ISREG(path_stat.st_mode):
            raise EvidenceError("helper_type_mismatch")
        descriptor = os.open(helper_path, os.O_RDONLY | getattr(os, "O_BINARY", 0))
        try:
            opened_stat = os.fstat(descriptor)
            if (opened_stat.st_dev, opened_stat.st_ino) != (path_stat.st_dev, path_stat.st_ino):
                raise EvidenceError("helper_identity_mismatch")
            with os.fdopen(descriptor, "rb", closefd=False) as stream:
                source = stream.read()
        finally:
            os.close(descriptor)
    except (OSError, EvidenceError) as error:
        raise EvidenceError("helper_load_failed") from error
    if hashlib.sha256(source).hexdigest() != HELPER_SHA256:
        raise EvidenceError("helper_digest_mismatch")
    module = types.ModuleType("g8_frozen_staging_evidence")
    module.__file__ = str(helper_path)
    try:
        exec(compile(source, str(helper_path), "exec"), module.__dict__)
    except Exception as error:
        raise EvidenceError("helper_load_failed") from error
    expectations = {
        "CHANGE_ID_CONSUMED": True,
        "TARGET_CHANGE_ID": TARGET_CHANGE_ID,
        "TARGET": "pc@8.130.9.163",
        "TARGET_PORT": "10003",
        "TARGET_DEPLOYMENT_ROOT": "/home/pc/molin",
        "STAGING_PATH": "/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003",
    }
    if any(getattr(module, key, object()) != value for key, value in expectations.items()):
        raise EvidenceError("helper_contract_mismatch")
    return module


def build_remote_program(helper) -> str:
    """从冻结 004 程序派生 006，只增加固定门禁原因，不改变暂存读取算法。"""
    program = helper.REMOTE_PROGRAM.replace(
        f"evidence_change_id = {helper.CHANGE_ID!r}",
        f"evidence_change_id = {CHANGE_ID!r}",
        1,
    )
    program = program.replace(
        "def reject():\n    raise SystemExit(41)",
        "def reject(reason):\n"
        "    print('EVIDENCE_CHANGE_ID=' + evidence_change_id)\n"
        "    print('TARGET_CHANGE_ID=' + target_change_id)\n"
        "    print('GATE_RESULT=BLOCKED')\n"
        "    print('GATE_REASON=' + reason)\n"
        "    raise SystemExit(0)",
        1,
    )
    reasons = (
        "IDENTITY",
        "MACHINE_ID",
        "DEPLOYMENT_ROOT_PATH",
        "DEPLOYMENT_ROOT_METADATA",
        "STAGING_LOOKUP",
        "DEPLOYMENT_ROOT_DRIFT",
    )
    for reason in reasons:
        if "reject()" not in program:
            raise EvidenceError("helper_reject_shape_mismatch")
        program = program.replace("reject()", f"reject('{reason}')", 1)
    if "reject()" in program:
        raise EvidenceError("helper_reject_count_mismatch")
    compile(program, "<g8-staging-evidence-v2>", "exec")
    return program


def parse_output(helper, stdout: bytes) -> tuple[str, dict[str, str]]:
    """只接受固定门禁阻断或 004 已证明的三态证据键集合。"""
    try:
        text = stdout.decode("utf-8", errors="strict")
    except UnicodeError as error:
        raise EvidenceError("invalid_remote_encoding") from error
    values: dict[str, str] = {}
    for line in text.splitlines():
        if "=" not in line:
            raise EvidenceError("invalid_remote_output")
        key, value = line.split("=", 1)
        if not key or key in values or not key.replace("_", "").isalnum() or not key.isupper() or not value:
            raise EvidenceError("invalid_remote_output")
        values[key] = value
    if set(values) == BLOCKED_KEYS:
        if (
            values["EVIDENCE_CHANGE_ID"] != CHANGE_ID
            or values["TARGET_CHANGE_ID"] != TARGET_CHANGE_ID
            or values["GATE_RESULT"] != "BLOCKED"
            or values["GATE_REASON"] not in GATE_REASONS
        ):
            raise EvidenceError("invalid_gate_result")
        return "BLOCKED", values
    original = dict(values)
    original["EVIDENCE_CHANGE_ID"] = helper.CHANGE_ID
    normalized = "\n".join(f"{key}={value}" for key, value in original.items()) + "\n"
    try:
        parsed = helper.parse_remote_output(normalized)
    except Exception as error:
        raise EvidenceError("invalid_evidence_result") from error
    parsed["EVIDENCE_CHANGE_ID"] = CHANGE_ID
    return "EVIDENCE", parsed


def collect_stream(stream, result: dict[str, object]) -> None:
    """有界排空单个管道，只保留 64 KiB 加 1 字节并累计完整摘要。"""
    digest = hashlib.sha256()
    captured = bytearray()
    total = 0
    try:
        while True:
            chunk = stream.read(8192)
            if not chunk:
                break
            digest.update(chunk)
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
            "sha256": "NONE" if total == 0 else digest.hexdigest(),
            "exceeded": total > MAX_CAPTURE_BYTES,
            "error": False,
        }
    )


def run_once(helper, remote_program: str, known_hosts: Path, identity_file: Path) -> tuple[int, dict[str, object], dict[str, object]]:
    """使用固定 OpenSSH 参数执行唯一一次只读远端程序。"""
    try:
        ssh_executable = helper.fixed_ssh_executable()
        environment = helper.fixed_ssh_environment()
    except Exception as error:
        raise EvidenceError("ssh_configuration_failed") from error
    command = [
        str(ssh_executable), "-F", "none",
        "-o", "BatchMode=yes", "-o", "NumberOfPasswordPrompts=0",
        "-o", "ConnectionAttempts=1", "-o", "StrictHostKeyChecking=yes",
        "-o", f"UserKnownHostsFile={known_hosts}", "-o", "IdentitiesOnly=yes",
        "-o", f"IdentityFile={identity_file}", "-o", "PreferredAuthentications=publickey",
        "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no",
        "-o", "ClearAllForwardings=yes", "-o", "ForwardAgent=no", "-o", "ForwardX11=no",
        "-o", "PermitLocalCommand=no", "-o", "RequestTTY=no", "-o", "ConnectTimeout=10",
        "-p", helper.TARGET_PORT, helper.TARGET,
        "/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "/usr/bin/python3", "-I", "-",
    ]
    try:
        process = subprocess.Popen(
            command, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=environment
        )
    except OSError as error:
        raise EvidenceError("ssh_execution_failed") from error
    if process.stdin is None or process.stdout is None or process.stderr is None:
        process.kill()
        raise EvidenceError("ssh_pipe_failed")
    stdout_result: dict[str, object] = {}
    stderr_result: dict[str, object] = {}
    threads = (
        threading.Thread(target=collect_stream, args=(process.stdout, stdout_result), daemon=True),
        threading.Thread(target=collect_stream, args=(process.stderr, stderr_result), daemon=True),
    )
    for thread in threads:
        thread.start()
    try:
        process.stdin.write(remote_program.encode("utf-8"))
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
    if stdout_result.get("error") or stderr_result.get("error"):
        raise EvidenceError("ssh_pipe_failed")
    return returncode, stdout_result, stderr_result


def main() -> int:
    """先完成本地身份门禁，再按新授权执行至多一次远端只读取证。"""
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
        print("G8_TEST_READONLY_STAGING_EVIDENCE_V2=FAILED reason=invalid_request")
        return 2
    try:
        helper = load_frozen_helper()
        remote_program = build_remote_program(helper)
    except Exception:
        print("G8_TEST_READONLY_STAGING_EVIDENCE_V2=FAILED reason=invalid_program")
        return 2
    if arguments.self_test:
        print("G8_TEST_READONLY_STAGING_EVIDENCE_V2_SELF_TEST=PASS")
        return 0
    if (
        arguments.change_id != CHANGE_ID
        or not arguments.known_hosts
        or not arguments.identity_file
        or not arguments.identity_public_file
    ):
        print("G8_TEST_READONLY_STAGING_EVIDENCE_V2=FAILED reason=invalid_request")
        return 2
    known_hosts = Path(arguments.known_hosts)
    identity_file = Path(arguments.identity_file)
    identity_public_file = Path(arguments.identity_public_file)
    try:
        helper.validate_known_hosts(known_hosts)
        helper.validate_identity_file(identity_file, identity_public_file, known_hosts)
        helper.validate_identity_pair(identity_file, identity_public_file)
    except Exception:
        print("G8_TEST_READONLY_STAGING_EVIDENCE_V2=FAILED reason=local_validation_failed")
        return 2
    if arguments.local_check:
        print("G8_TEST_READONLY_STAGING_EVIDENCE_V2_LOCAL_CHECK=PASS")
        return 0
    try:
        returncode, stdout_result, stderr_result = run_once(helper, remote_program, known_hosts, identity_file)
        if (
            returncode != 0
            or stderr_result["bytes"] != 0
            or stdout_result["exceeded"]
            or stderr_result["exceeded"]
        ):
            raise EvidenceError("remote_evidence_failed")
        result_type, values = parse_output(helper, stdout_result["captured"])
    except Exception:
        print("G8_TEST_READONLY_STAGING_EVIDENCE_V2=FAILED reason=remote_evidence_failed")
        return 2
    if result_type == "BLOCKED":
        print("G8_TEST_READONLY_STAGING_EVIDENCE_V2=BLOCKED")
        print(f"change_id={CHANGE_ID}")
        print(f"target_change_id={TARGET_CHANGE_ID}")
        print(f"gate_reason={values['GATE_REASON']}")
        return 3
    mismatch = values["STAGING_INTEGRITY"] == "MISMATCH"
    print("G8_TEST_READONLY_STAGING_EVIDENCE_V2=MISMATCH" if mismatch else "G8_TEST_READONLY_STAGING_EVIDENCE_V2=PASS")
    print(f"change_id={CHANGE_ID}")
    print(f"target_change_id={TARGET_CHANGE_ID}")
    print(f"staging_state={values['STAGING_STATE']}")
    print(f"staging_integrity={values['STAGING_INTEGRITY']}")
    print(f"staging_mismatch_reason={values['STAGING_MISMATCH_REASON']}")
    return 3 if mismatch else 0


if __name__ == "__main__":
    raise SystemExit(main())
