#!/usr/bin/env python3
"""对固定测试服执行至多一次低敏 SSH 连接诊断。"""

import sys

# 普通解释器会把脚本目录和用户包加入搜索路径，必须先失败关闭。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_SSH_DIAGNOSTIC_024=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import base64
import ctypes
import hashlib
import os
import re
import shutil
import stat
import subprocess
import tempfile
import threading
from collections.abc import Callable
from pathlib import Path
from typing import NamedTuple


CHANGE_ID = "CHG-G8-TEST-READONLY-SSH-DIAGNOSTIC-20260816-024"
REMOTE_EXECUTION_AUTHORIZED = False
TARGET = "pc@8.130.9.163"
TARGET_HOST = "8.130.9.163"
TARGET_PORT = "10003"
TARGET_HOST_TOKEN = "[8.130.9.163]:10003"
TARGET_HOST_KEY_FINGERPRINT = "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"
REMOTE_MARKER = b"G8_TEST_READONLY_SSH_DIAGNOSTIC_024_REMOTE=PASS\n"
REMOTE_COMMAND = "printf 'G8_TEST_READONLY_SSH_DIAGNOSTIC_024_REMOTE=PASS\\n'"
RUNNER_RELATIVE = "infra/scripts/run-ai-gateway-g8-test-readonly-ssh-diagnostic-024.py"
MAX_CAPTURE_BYTES = 64 * 1024


class ProbeResult(NamedTuple):
    """保存固定低敏结论，不携带原始 SSH 输出。"""

    reason: str
    exit_code: int


class RunnerFailure(RuntimeError):
    """携带固定低敏本地门禁原因。"""

    def __init__(self, reason: str):
        super().__init__(reason)
        self.reason = reason


class SafeArgumentParser(argparse.ArgumentParser):
    """拒绝 argparse 回显调用方参数。"""

    def error(self, message: str) -> None:
        raise ValueError("invalid_request")


def build_ssh_arguments(known_hosts: Path) -> list[str]:
    """固定目标与安全参数，同时保留开发机现有免交互认证链。"""
    return [
        "-F",
        "none",
        "-T",
        "-p",
        TARGET_PORT,
        "-o",
        f"HostName={TARGET_HOST}",
        "-o",
        "CanonicalizeHostname=no",
        "-o",
        "BatchMode=yes",
        "-o",
        "ConnectionAttempts=1",
        "-o",
        "ConnectTimeout=15",
        "-o",
        "PasswordAuthentication=no",
        "-o",
        "KbdInteractiveAuthentication=no",
        "-o",
        "NumberOfPasswordPrompts=0",
        "-o",
        "StrictHostKeyChecking=yes",
        "-o",
        "GlobalKnownHostsFile=none",
        "-o",
        "KnownHostsCommand=none",
        "-o",
        "VerifyHostKeyDNS=no",
        "-o",
        "HostKeyAlgorithms=ssh-ed25519",
        "-o",
        "ForwardAgent=no",
        "-o",
        "ForwardX11=no",
        "-o",
        "ClearAllForwardings=yes",
        "-o",
        "ControlMaster=no",
        "-o",
        "ControlPath=none",
        "-o",
        "ControlPersist=no",
        "-o",
        "ProxyCommand=none",
        "-o",
        "ProxyJump=none",
        "-o",
        "PermitLocalCommand=no",
        "-o",
        "RemoteCommand=none",
        "-o",
        "RequestTTY=no",
        "-o",
        "LogLevel=ERROR",
        "-o",
        f"UserKnownHostsFile={known_hosts}",
        TARGET,
        REMOTE_COMMAND,
    ]


def classify_result(exit_code: int, stdout: bytes, stderr: bytes) -> ProbeResult:
    """把 SSH 原始结果映射为固定分类，禁止向调用方转发正文。"""
    lowered = stderr.lower()
    if exit_code == 0:
        if stdout == REMOTE_MARKER and not stderr:
            return ProbeResult("pass", exit_code)
        return ProbeResult("remote_marker_failed", exit_code)
    if exit_code != 255:
        return ProbeResult("remote_probe_failed", exit_code)
    if b"permission denied" in lowered or b"no supported authentication methods" in lowered:
        return ProbeResult("authentication_failed", exit_code)
    if b"host key verification failed" in lowered or b"remote host identification has changed" in lowered:
        return ProbeResult("host_key_failed", exit_code)
    if b"timed out" in lowered or b"operation timed out" in lowered:
        return ProbeResult("connect_timeout", exit_code)
    if b"connection refused" in lowered:
        return ProbeResult("connect_refused", exit_code)
    if b"no route to host" in lowered or b"network is unreachable" in lowered:
        return ProbeResult("network_unreachable", exit_code)
    return ProbeResult("transport_failed", exit_code)


def _collect_stream(stream, result: dict[str, object]) -> None:
    """持续排空管道，但只在固定上限内保留用于分类的正文。"""
    captured = bytearray()
    exceeded = False
    try:
        while True:
            chunk = stream.read(8192)
            if not chunk:
                break
            remaining = MAX_CAPTURE_BYTES + 1 - len(captured)
            if remaining > 0:
                captured.extend(chunk[:remaining])
            if len(captured) > MAX_CAPTURE_BYTES:
                exceeded = True
    except Exception:
        result["error"] = True
        return
    result.update({"captured": bytes(captured[:MAX_CAPTURE_BYTES]), "exceeded": exceeded, "error": False})


def run_ssh_probe(
    ssh_command: list[str],
    known_hosts: Path,
    *,
    environment: dict[str, str],
    timeout: int = 25,
    on_started: Callable[[], None] | None = None,
) -> ProbeResult:
    """只启动一次 SSH 子进程并对输出做有界低敏分类。"""
    command = [*ssh_command, *build_ssh_arguments(known_hosts)]
    try:
        process = subprocess.Popen(
            command,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=environment,
        )
    except OSError as error:
        raise RunnerFailure("ssh_client_unavailable") from error
    # 只有操作系统确认子进程已创建后，才允许形成 SSH_ATTEMPTED 证据。
    if on_started is not None:
        try:
            on_started()
        except Exception as error:
            process.kill()
            process.wait()
            raise RunnerFailure("local_output_unavailable") from error
    if process.stdout is None or process.stderr is None:
        process.kill()
        raise RunnerFailure("ssh_capture_unavailable")
    stdout_result: dict[str, object] = {}
    stderr_result: dict[str, object] = {}
    stdout_thread = threading.Thread(target=_collect_stream, args=(process.stdout, stdout_result), daemon=True)
    stderr_thread = threading.Thread(target=_collect_stream, args=(process.stderr, stderr_result), daemon=True)
    stdout_thread.start()
    stderr_thread.start()
    timed_out = False
    try:
        exit_code = process.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()
        exit_code = 255
        timed_out = True
    finally:
        stdout_thread.join(timeout=5)
        stderr_thread.join(timeout=5)
        process.stdout.close()
        process.stderr.close()
    if stdout_thread.is_alive() or stderr_thread.is_alive():
        raise RunnerFailure("ssh_capture_unavailable")
    if stdout_result.get("error") or stderr_result.get("error"):
        raise RunnerFailure("ssh_capture_unavailable")
    if timed_out:
        return ProbeResult("connect_timeout", exit_code)
    if stdout_result.get("exceeded") or stderr_result.get("exceeded"):
        return ProbeResult("output_limit_exceeded", exit_code)
    return classify_result(
        exit_code,
        bytes(stdout_result.get("captured", b"")),
        bytes(stderr_result.get("captured", b"")),
    )


def _known_folder(csidl: int, reason: str) -> Path:
    """使用 Windows Shell API 取得可信用户目录，不接受环境变量覆盖。"""
    buffer = ctypes.create_unicode_buffer(32768)
    result = ctypes.windll.shell32.SHGetFolderPathW(None, csidl, None, 0, buffer)
    if result != 0 or not buffer.value:
        raise RunnerFailure(reason)
    path = Path(buffer.value)
    if not path.is_absolute():
        raise RunnerFailure(reason)
    return path


def _is_reparse(path: Path) -> bool:
    """统一拒绝符号链接、目录联接和其他 reparse 对象。"""
    try:
        info = path.lstat()
    except OSError as error:
        raise RunnerFailure("local_material_unavailable") from error
    attributes = getattr(info, "st_file_attributes", 0)
    return path.is_symlink() or bool(attributes & 0x400)


def _trusted_windows_paths() -> tuple[Path, Path, Path]:
    """从 Windows API 取得固定 OpenSSH、known_hosts 与本地临时目录。"""
    if os.name != "nt":
        raise RunnerFailure("windows_required")
    buffer = ctypes.create_unicode_buffer(32768)
    length = ctypes.windll.kernel32.GetWindowsDirectoryW(buffer, len(buffer))
    if length <= 0 or length >= len(buffer):
        raise RunnerFailure("trusted_windows_path_failed")
    ssh = Path(buffer.value) / "System32" / "OpenSSH" / "ssh.exe"
    profile = _known_folder(0x28, "trusted_profile_unavailable")
    local_appdata = _known_folder(0x1C, "receipt_directory_unavailable")
    ssh_directory = profile / ".ssh"
    known_hosts = ssh_directory / "known_hosts"
    for directory in (profile, ssh_directory, local_appdata):
        if _is_reparse(directory) or not directory.is_dir():
            raise RunnerFailure("local_material_unavailable")
    for path in (ssh, known_hosts):
        try:
            info = path.lstat()
        except OSError as error:
            raise RunnerFailure("local_material_unavailable") from error
        if not stat.S_ISREG(info.st_mode) or _is_reparse(path):
            raise RunnerFailure("local_material_unavailable")
    return ssh, known_hosts, local_appdata


def _approved_known_hosts_line(known_hosts: Path) -> bytes:
    """只接受固定端点唯一 ED25519 条目并独立复算指纹。"""
    try:
        lines = known_hosts.read_bytes().splitlines()
    except OSError as error:
        raise RunnerFailure("known_hosts_failed") from error
    matches = []
    for line in lines:
        if not line or line.startswith(b"#"):
            continue
        fields = line.split()
        if len(fields) == 3 and fields[0] == TARGET_HOST_TOKEN.encode("ascii") and fields[1] == b"ssh-ed25519":
            matches.append(fields)
    if len(matches) != 1:
        raise RunnerFailure("known_hosts_failed")
    try:
        raw_key = base64.b64decode(matches[0][2], validate=True)
    except ValueError as error:
        raise RunnerFailure("known_hosts_failed") from error
    fingerprint = "SHA256:" + base64.b64encode(hashlib.sha256(raw_key).digest()).decode("ascii").rstrip("=")
    if fingerprint != TARGET_HOST_KEY_FINGERPRINT:
        raise RunnerFailure("known_hosts_failed")
    return b" ".join(matches[0]) + b"\n"


def _git_output(repository: Path, arguments: list[str]) -> bytes:
    """使用禁用配置和网络懒加载的 Git 读取本地原始对象。"""
    git = shutil.which("git")
    if git is None:
        raise RunnerFailure("engineering_material_unavailable")
    environment = {key: value for key, value in os.environ.items() if not key.upper().startswith("GIT_")}
    environment.update(
        {
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_NO_LAZY_FETCH": "1",
            "GIT_NO_REPLACE_OBJECTS": "1",
            "GIT_TERMINAL_PROMPT": "0",
        }
    )
    try:
        return subprocess.run(
            [git, "--no-replace-objects", "-c", f"safe.directory={repository}", *arguments],
            cwd=repository,
            env=environment,
            capture_output=True,
            check=True,
            timeout=30,
        ).stdout
    except (OSError, subprocess.SubprocessError) as error:
        raise RunnerFailure("engineering_material_unavailable") from error


def verify_engineering_merge(repository: Path, merge: str) -> None:
    """要求授权对象为双父 merge，且第二父与 merge 中 runner 均未漂移。"""
    raw_commit = _git_output(repository, ["cat-file", "-p", merge])
    header = raw_commit.split(b"\n\n", 1)[0].decode("ascii", "strict").splitlines()
    parents = [line[7:] for line in header if line.startswith("parent ")]
    if len(parents) != 2 or any(not re.fullmatch(r"[0-9a-f]{40}", parent) for parent in parents):
        raise RunnerFailure("engineering_material_not_merge")
    current = Path(__file__).read_bytes()
    for commit in (merge, parents[1]):
        if _git_output(repository, ["show", f"{commit}:{RUNNER_RELATIVE}"]) != current:
            raise RunnerFailure("engineering_material_drift")


def self_test() -> None:
    """纯离线验证固定参数和结果分类，不启动 SSH。"""
    rendered = " ".join(build_ssh_arguments(Path("known_hosts")))
    required = (TARGET, "BatchMode=yes", "ConnectionAttempts=1", "StrictHostKeyChecking=yes", REMOTE_COMMAND)
    forbidden = (" -i ", "IdentitiesOnly", "docker", "sudo", "curl", "mysql")
    if any(value not in rendered for value in required) or any(value in rendered for value in forbidden):
        raise RunnerFailure("invalid_program")
    if classify_result(0, REMOTE_MARKER, b"").reason != "pass":
        raise RunnerFailure("invalid_program")


def execute(arguments: argparse.Namespace) -> int:
    """完成本地冻结门禁后只调用一次 SSH，任何失败均不重试。"""
    repository = Path(__file__).resolve().parents[2]
    verify_engineering_merge(repository, arguments.engineering_merge)
    content = Path(__file__).read_bytes()
    if len(content) != arguments.expected_runner_size or hashlib.sha256(content).hexdigest() != arguments.expected_runner_sha256:
        raise RunnerFailure("engineering_material_drift")
    ssh, known_hosts, local_root = _trusted_windows_paths()
    approved_line = _approved_known_hosts_line(known_hosts)
    temporary_path: Path | None = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="wb", prefix="g8-024-known-hosts-", suffix=".txt", dir=local_root, delete=False
        ) as stream:
            temporary_path = Path(stream.name)
            stream.write(approved_line)
            stream.flush()
            os.fsync(stream.fileno())
        print("G8_TEST_READONLY_SSH_DIAGNOSTIC_024=LOCAL_GATE_PASS")
        result = run_ssh_probe(
            [str(ssh)],
            temporary_path,
            environment=dict(os.environ),
            on_started=lambda: print("G8_TEST_READONLY_SSH_DIAGNOSTIC_024_SSH_ATTEMPTED=YES"),
        )
    finally:
        if temporary_path is not None:
            try:
                temporary_path.unlink(missing_ok=True)
            except OSError:
                pass
    if result.reason != "pass":
        print(f"G8_TEST_READONLY_SSH_DIAGNOSTIC_024=FAILED reason={result.reason}")
        return 2
    print("G8_TEST_READONLY_SSH_DIAGNOSTIC_024=PASS")
    return 0


def main() -> int:
    """解析固定授权绑定；默认与不完整请求都必须在 SSH 前拒绝。"""
    parser = SafeArgumentParser(add_help=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--execute-authorized", action="store_true")
    parser.add_argument("--change-id")
    parser.add_argument("--engineering-merge")
    parser.add_argument("--expected-runner-size", type=int)
    parser.add_argument("--expected-runner-sha256")
    try:
        arguments = parser.parse_args()
        if arguments.self_test:
            if any(
                value is not None
                for value in (
                    arguments.change_id,
                    arguments.engineering_merge,
                    arguments.expected_runner_size,
                    arguments.expected_runner_sha256,
                )
            ) or arguments.execute_authorized:
                raise ValueError("invalid_request")
            self_test()
            print("G8_TEST_READONLY_SSH_DIAGNOSTIC_024_SELF_TEST=PASS")
            return 0
        if not arguments.execute_authorized or arguments.change_id != CHANGE_ID:
            print("G8_TEST_READONLY_SSH_DIAGNOSTIC_024=FAILED reason=remote_not_authorized")
            return 2
        if not re.fullmatch(r"[0-9a-f]{40}", arguments.engineering_merge or ""):
            print("G8_TEST_READONLY_SSH_DIAGNOSTIC_024=FAILED reason=remote_not_authorized")
            return 2
        if not arguments.expected_runner_size or not re.fullmatch(r"[0-9a-f]{64}", arguments.expected_runner_sha256 or ""):
            print("G8_TEST_READONLY_SSH_DIAGNOSTIC_024=FAILED reason=remote_not_authorized")
            return 2
        return execute(arguments)
    except ValueError:
        print("G8_TEST_READONLY_SSH_DIAGNOSTIC_024=FAILED reason=invalid_request")
        return 2
    except RunnerFailure as failure:
        print(f"G8_TEST_READONLY_SSH_DIAGNOSTIC_024=FAILED reason={failure.reason}")
        return 2
    except Exception:
        print("G8_TEST_READONLY_SSH_DIAGNOSTIC_024=FAILED reason=local_gate_failed")
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
