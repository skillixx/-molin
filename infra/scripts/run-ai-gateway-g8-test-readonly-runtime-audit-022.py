#!/usr/bin/env python3
"""复核工程原始对象、生成冻结命令并只启动一次 Windows PowerShell 5.1。"""

import sys

# 隔离模式会移除脚本目录与用户 site-packages，避免同目录模块劫持标准库。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_RUNTIME_AUDIT_022_RUNNER=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import ctypes
import hashlib
import importlib.util
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile


CHANGE_ID = "CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-022"
REMOTE_EXECUTION_AUTHORIZED = False
GENERATOR_NAME = "prepare-ai-gateway-g8-test-readonly-runtime-audit-022-command.py"
AUDITOR_NAME = "audit-ai-gateway-g8-test-server-readonly.sh"
RUNNER_RELATIVE = "infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-022.py"
GENERATOR_RELATIVE = f"infra/scripts/{GENERATOR_NAME}"
AUDITOR_RELATIVE = f"infra/scripts/{AUDITOR_NAME}"
FIXED_FAILURE_REASONS = (
    "identity_pair_failed",
    "known_hosts_failed",
    "receipt_directory_unavailable",
    "receipt_preoccupied",
    "receipt_write_failed",
    "receipt_flush_failed",
    "ssh_session_failed",
    "trusted_windows_path_failed",
)
FIXED_CHILD_LINES = frozenset(
    {
        "AUDIT_COMPLETE=true",
        "G8_TEST_READONLY_RUNTIME_AUDIT_022=PREFLIGHT_PASS",
        "G8_TEST_READONLY_RUNTIME_AUDIT_022=COLLECTION_PASS",
        "G8_TEST_READONLY_RUNTIME_AUDIT_022=FAILED reason=invalid_user",
        "G8_TEST_READONLY_RUNTIME_AUDIT_022=FAILED reason=audit_evidence_failed",
        "G8_TEST_READONLY_ACCESS_022_PRE_SSH_GATE=PASS",
        "G8_TEST_READONLY_ACCESS_022_SSH_ATTEMPTED=YES",
        "G8_TEST_READONLY_ACCESS_022_HOST_RESULT=PASS exit_code=0",
        *(
            f"G8_TEST_READONLY_ACCESS_022_LOCAL_GATE=FAILED reason={reason}"
            for reason in FIXED_FAILURE_REASONS
        ),
        *(
            f"G8_TEST_READONLY_ACCESS_022_HOST_RESULT=FAILED reason={reason} exit_code=2"
            for reason in FIXED_FAILURE_REASONS
        ),
    }
)
POWERSHELL_COMMAND = (
    "$source=[Console]::In.ReadToEnd(); "
    "$script=[scriptblock]::Create($source); "
    "& $script"
)


class SafeArgumentParser(argparse.ArgumentParser):
    """把参数错误收敛为固定低敏状态，禁止回显用户输入。"""

    def error(self, message: str) -> None:
        raise ValueError("invalid_request")


class RunnerFailure(RuntimeError):
    """携带固定低敏阶段原因，不保存或转发原始异常。"""

    def __init__(self, reason: str) -> None:
        super().__init__(reason)
        self.reason = reason


def load_generator():
    """从固定同目录路径加载 022 生成器，不接受调用方提供模块路径。"""
    path = Path(__file__).with_name(GENERATOR_NAME)
    specification = importlib.util.spec_from_file_location("g8_runtime_audit_022_generator", path)
    if specification is None or specification.loader is None:
        raise RuntimeError("generator_unavailable")
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
    return module


def windows_powershell_path() -> Path:
    """通过 Windows API 获取可信系统目录，不读取可伪造的 SystemRoot。"""
    if os.name != "nt":
        raise RuntimeError("windows_required")
    buffer = ctypes.create_unicode_buffer(32768)
    length = ctypes.windll.kernel32.GetWindowsDirectoryW(buffer, len(buffer))
    if length <= 0 or length >= len(buffer):
        raise RuntimeError("trusted_windows_path_failed")
    powershell = Path(buffer.value) / "System32" / "WindowsPowerShell" / "v1.0" / "powershell.exe"
    if not powershell.is_file():
        raise RuntimeError("powershell_unavailable")
    return powershell


def invoke_powershell(source: str, *, timeout: int = 180) -> subprocess.CompletedProcess[str]:
    """只通过固定 PowerShell 5.1 路径执行一个内存脚本，stderr 始终留在本地收敛。"""
    return subprocess.run(
        [str(windows_powershell_path()), "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", POWERSHELL_COMMAND],
        input=source,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="strict",
        check=False,
        timeout=timeout,
    )


def git_output(repository: Path, arguments: list[str]) -> bytes:
    """使用隔离配置的本地 Git 读取对象，禁止提示、替换对象和按需联网。"""
    git = shutil.which("git")
    if git is None or not Path(git).is_absolute() or not Path(git).is_file():
        raise OSError("git_unavailable")
    environment = {key: value for key, value in os.environ.items() if not key.upper().startswith("GIT_")}
    environment.update(
        {
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_NO_LAZY_FETCH": "1",
            "GIT_NO_REPLACE_OBJECTS": "1",
            "GIT_OPTIONAL_LOCKS": "0",
            "GIT_TERMINAL_PROMPT": "0",
        }
    )
    return subprocess.run(
        [git, "--no-replace-objects", "-c", f"safe.directory={repository}", *arguments],
        cwd=repository,
        env=environment,
        capture_output=True,
        check=True,
        timeout=30,
    ).stdout


def git_blob(repository: Path, commit: str, relative: str) -> bytes:
    """只读取得指定提交的原始 blob；任何 Git 错误都由上层固定低敏收敛。"""
    return git_output(repository, ["show", f"{commit}:{relative}"])


def git_merge_parents(repository: Path, commit: str) -> tuple[str, str]:
    """只接受恰有两个父提交的普通 merge commit，并保留父提交顺序。"""
    raw = git_output(repository, ["cat-file", "-p", commit])
    # 提交正文允许中文；只解析固定 ASCII 对象头，避免提交说明影响门禁。
    header = raw.split(b"\n\n", 1)[0].decode("ascii", errors="strict").splitlines()
    if not header or not re.fullmatch(r"tree [0-9a-f]{40}", header[0]):
        raise RunnerFailure("engineering_material_not_merge")
    parents = tuple(line.removeprefix("parent ") for line in header if line.startswith("parent "))
    if len(parents) != 2 or any(not re.fullmatch(r"[0-9a-f]{40}", parent) for parent in parents):
        raise RunnerFailure("engineering_material_not_merge")
    return parents


def verify_engineering_merge(repository: Path, merge: str) -> None:
    """要求双父 merge 及其第二父的三个执行文件都与当前候选逐字节一致。"""
    current = {
        RUNNER_RELATIVE: Path(__file__).read_bytes(),
        GENERATOR_RELATIVE: Path(__file__).with_name(GENERATOR_NAME).read_bytes(),
        AUDITOR_RELATIVE: Path(__file__).with_name(AUDITOR_NAME).read_bytes(),
    }
    try:
        _, engineering_head = git_merge_parents(repository, merge)
        for relative, content in current.items():
            if git_blob(repository, merge, relative) != content:
                raise RunnerFailure("engineering_material_drift")
            if git_blob(repository, engineering_head, relative) != content:
                raise RunnerFailure("engineering_material_drift")
    except RunnerFailure:
        raise
    except (OSError, UnicodeError, subprocess.SubprocessError):
        raise RunnerFailure("engineering_material_unavailable") from None


def allowed_child_line(line: str, required_keys: tuple[str, ...]) -> bool:
    """只转发冻结低敏键和值及固定阶段标志，拒绝异常、路径和凭据旁路输出。"""
    if line in FIXED_CHILD_LINES:
        return True
    key, separator, _ = line.partition("=")
    return separator == "=" and key in required_keys


def self_test() -> None:
    """在 Linux 只做纯离线构造，在 Windows 额外动态覆盖解析失败与假 SSH 成功。"""
    generator = load_generator()
    auditor = generator.read_frozen_auditor()
    generator.self_test(auditor)
    if os.name != "nt":
        return
    with tempfile.TemporaryDirectory(prefix="g8-022-fixed-runner-") as temporary:
        root = Path(temporary).resolve()
        parser_marker = root / "parser-child-attempted.txt"
        parser_child = root / "parser-child.cmd"
        parser_child.write_text(f'@echo attempted>"{parser_marker}"\n', encoding="ascii", newline="\n")
        escaped_parser_child = str(parser_child).replace("'", "''")
        invalid = invoke_powershell(f"& '{escaped_parser_child}'\n)", timeout=15)
        if (
            invalid.returncode == 0
            or parser_marker.exists()
            or "G8_TEST_READONLY_ACCESS_022_SSH_ATTEMPTED=YES" in invalid.stdout
        ):
            raise RuntimeError("invalid_parser_test")
        receipt = root / "receipt.txt"
        fake_ssh = root / "ssh.cmd"
        fake_ssh.write_text("@exit /b 0\n", encoding="ascii", newline="\n")
        command = generator.build_command(
            auditor,
            receipt_path=str(receipt),
            test_scenario="fake_ssh",
            test_ssh_path=str(fake_ssh),
        )
        completed = invoke_powershell(command, timeout=30)
        expected = "G8_TEST_READONLY_ACCESS_022_HOST_RESULT=PASS exit_code=0"
        if completed.returncode != 0 or completed.stderr or expected not in completed.stdout:
            raise RuntimeError("invalid_fixed_runner")
        lines = receipt.read_text(encoding="utf-8").splitlines()
        if lines.count("G8_TEST_READONLY_ACCESS_022_SSH_ATTEMPTED=YES") != 1 or lines[-1] != expected:
            raise RuntimeError("invalid_fixed_runner")


def execute(arguments: argparse.Namespace) -> int:
    """完成全部本地门禁后只启动一次 PowerShell；从不重试。"""
    repository = Path(__file__).resolve().parents[2]
    verify_engineering_merge(repository, arguments.engineering_merge)
    generator = load_generator()
    auditor = generator.read_frozen_auditor()
    command = generator.build_command(auditor, receipt_path=generator.TRUSTED_LOCAL_APPDATA_RECEIPT)
    encoded = command.encode("utf-8")
    if len(encoded) != arguments.expected_command_size:
        raise RuntimeError("command_drift")
    if hashlib.sha256(encoded).hexdigest() != arguments.expected_command_sha256:
        raise RuntimeError("command_drift")
    print("G8_TEST_READONLY_RUNTIME_AUDIT_022_RUNNER=LOCAL_GATE_PASS")
    print("G8_TEST_READONLY_RUNTIME_AUDIT_022_POWERSHELL_ATTEMPTED=YES")
    try:
        completed = invoke_powershell(command)
    except Exception:
        print("G8_TEST_READONLY_RUNTIME_AUDIT_022_RUNNER=FAILED reason=powershell_session_failed")
        return 2
    forwarded = []
    for line in completed.stdout.splitlines():
        if allowed_child_line(line, tuple(generator.REQUIRED_COLLECTION_KEYS)):
            print(line)
            forwarded.append(line)
    success = "G8_TEST_READONLY_ACCESS_022_HOST_RESULT=PASS exit_code=0"
    if completed.returncode != 0 or success not in forwarded:
        print("G8_TEST_READONLY_RUNTIME_AUDIT_022_RUNNER=FAILED reason=powershell_session_failed")
        return 2
    print("G8_TEST_READONLY_RUNTIME_AUDIT_022_RUNNER=PASS")
    return 0


def main() -> int:
    """解析固定参数；缺少显式执行标志时在读取材料和启动子进程前拒绝。"""
    parser = SafeArgumentParser(add_help=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--change-id")
    parser.add_argument("--engineering-merge")
    parser.add_argument("--expected-command-size", type=int)
    parser.add_argument("--expected-command-sha256")
    parser.add_argument("--execute-authorized", action="store_true")
    try:
        arguments = parser.parse_args()
        if arguments.self_test:
            if any(
                value is not None
                for value in (
                    arguments.change_id,
                    arguments.engineering_merge,
                    arguments.expected_command_size,
                    arguments.expected_command_sha256,
                )
            ) or arguments.execute_authorized:
                raise ValueError("invalid_request")
            self_test()
            print("G8_TEST_READONLY_RUNTIME_AUDIT_022_RUNNER_SELF_TEST=PASS")
            return 0
        if not arguments.execute_authorized or arguments.change_id != CHANGE_ID:
            raise ValueError("invalid_request")
        if not re.fullmatch(r"[0-9a-f]{40}", arguments.engineering_merge or ""):
            raise ValueError("invalid_request")
        if arguments.expected_command_size is None or arguments.expected_command_size <= 0:
            raise ValueError("invalid_request")
        if not re.fullmatch(r"[0-9a-f]{64}", arguments.expected_command_sha256 or ""):
            raise ValueError("invalid_request")
        return execute(arguments)
    except ValueError:
        print("G8_TEST_READONLY_RUNTIME_AUDIT_022_RUNNER=FAILED reason=invalid_request")
        return 2
    except RunnerFailure as failure:
        print(f"G8_TEST_READONLY_RUNTIME_AUDIT_022_RUNNER=FAILED reason={failure.reason}")
        return 2
    except Exception:
        print("G8_TEST_READONLY_RUNTIME_AUDIT_022_RUNNER=FAILED reason=local_gate_failed")
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
