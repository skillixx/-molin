#!/usr/bin/env python3
"""在测试机上以原进程身份临时采集 API 标准输出，并可恢复原重定向。"""

from __future__ import annotations

import hashlib
import json
import os
import pathlib
import re
import signal
import stat
import sys
import time
from typing import Any

sys.dont_write_bytecode = True

CONFIRM_CAPTURE = "I_CONFIRM_PHASE4_API_LOG_CAPTURE"
CONFIRM_RESTORE = "I_CONFIRM_PHASE4_API_LOG_RESTORE"
CAPTURE_ROOT = pathlib.Path("/home/pc/molin/phase4-runtime-log-captures")
CAPTURE_ID_RE = re.compile(r"[a-f0-9]{32}\Z")
STATE_KEYS = {
    "version", "capture_id", "status", "original_pid", "original_starttime",
    "original_uid", "binary_path", "binary_sha256", "binary_device", "binary_inode",
    "binary_size", "cwd", "stdout_target", "stderr_target", "capture_pid",
    "capture_starttime", "environ_sha256", "cmdline_sha256", "created_utc",
}
# 仅登记本 payload 当前进程直接 fork 的子 PID；非子进程绝不调用 waitpid。
OWNED_CHILDREN: dict[int, int] = {}


class GateFailure(Exception):
    """失败只携带固定分类，禁止包含进程环境或路径正文。"""


class LaunchFailure(GateFailure):
    """携带本次 fork 身份，供上层先精确收敛失败进程再恢复服务。"""

    def __init__(self, classification: str, pid: int, starttime: int) -> None:
        super().__init__(classification)
        self.pid = pid
        self.starttime = starttime


class OperationFailure(GateFailure):
    """只增加低基数服务状态，不向安全摘要传播异常正文。"""

    def __init__(self, service_state: str) -> None:
        super().__init__("operation_failed")
        self.service_state = service_state


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise GateFailure(classification)


def proc_starttime(pid: int) -> int:
    """从 stat 最后一个右括号后读取字段，避免进程名中的空格扰乱列号。"""
    raw = pathlib.Path(f"/proc/{pid}/stat").read_text(encoding="ascii")
    tail = raw[raw.rfind(")") + 2:].split()
    require(len(tail) >= 20, "process_identity")
    return int(tail[19])


def process_matches(pid: int, starttime: int) -> bool:
    try:
        return proc_starttime(pid) == starttime
    except (OSError, ValueError, GateFailure):
        return False


def listening_inodes(port: int = 8080) -> set[str]:
    result: set[str] = set()
    expected = f"{port:04X}"
    for name in ("tcp", "tcp6"):
        try:
            lines = pathlib.Path(f"/proc/net/{name}").read_text(encoding="ascii").splitlines()[1:]
        except OSError as error:
            raise GateFailure("listener_scan") from error
        for line in lines:
            fields = line.split()
            if len(fields) >= 10 and fields[3] == "0A" and fields[1].rsplit(":", 1)[-1].upper() == expected:
                result.add(fields[9])
    return result


def exact_api_process() -> int:
    """8080 监听归属与系统内唯一同名二进制必须指向同一个 PID。"""
    inodes = listening_inodes()
    require(bool(inodes), "listener_missing")
    listener_pids: set[int] = set()
    named_pids: set[int] = set()
    for entry in pathlib.Path("/proc").iterdir():
        if not entry.name.isdigit():
            continue
        pid = int(entry.name)
        try:
            if pathlib.Path(os.readlink(entry / "exe")).name == "molin-api":
                named_pids.add(pid)
            for fd in (entry / "fd").iterdir():
                target = os.readlink(fd)
                match = re.fullmatch(r"socket:\[([0-9]+)\]", target)
                if match and match.group(1) in inodes:
                    listener_pids.add(pid)
                    break
        except (FileNotFoundError, PermissionError, ProcessLookupError):
            continue
    require(len(listener_pids) == 1 and len(named_pids) == 1 and listener_pids == named_pids, "api_not_unique")
    return next(iter(listener_pids))


def named_api_pids() -> set[int]:
    """用于恢复前确认系统内没有遗留或派生出的同名进程。"""
    result: set[int] = set()
    for entry in pathlib.Path("/proc").iterdir():
        if not entry.name.isdigit():
            continue
        try:
            if pathlib.Path(os.readlink(entry / "exe")).name == "molin-api":
                result.add(int(entry.name))
        except (FileNotFoundError, PermissionError, ProcessLookupError):
            continue
    return result


def file_sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        while True:
            chunk = stream.read(1024 * 1024)
            if not chunk:
                return digest.hexdigest()
            digest.update(chunk)


def write_exclusive(path: pathlib.Path, data: bytes, mode: int = 0o600) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, mode)
    try:
        view = memoryview(data)
        written = 0
        while written < len(view):
            count = os.write(descriptor, view[written:])
            require(count > 0, "artifact_short_write")
            written += count
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    require(stat.S_IMODE(path.stat(follow_symlinks=False).st_mode) == mode, "artifact_mode")


def write_state(path: pathlib.Path, state: dict[str, Any]) -> None:
    require(set(state) == STATE_KEYS, "state_contract")
    # 每次状态转换使用唯一临时文件；失败残留本身就是证据，不能阻塞后续恢复记录。
    temporary = path.with_name(".state.next." + os.urandom(8).hex() + ".json")
    write_exclusive(temporary, json.dumps(state, separators=(",", ":"), sort_keys=True).encode("utf-8"))
    os.replace(temporary, path)
    os.chmod(path, 0o600, follow_symlinks=False)


def read_restore_state(path: pathlib.Path, capture_id: str) -> dict[str, Any]:
    """严格解析恢复状态，拒绝重复键、未知字段和类型混淆。"""
    def no_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            require(key not in result, "state_duplicate_key")
            result[key] = value
        return result

    value = json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=no_duplicates)
    require(isinstance(value, dict) and set(value) == STATE_KEYS, "state_contract")
    require(value["version"] == 1 and value["capture_id"] == capture_id and value["status"] == "capturing", "state_contract")
    for key in (
        "original_pid", "original_starttime", "original_uid", "binary_device", "binary_inode",
        "binary_size", "capture_pid", "capture_starttime",
    ):
        require(type(value[key]) is int and value[key] > 0, "state_contract")
    for key in ("binary_sha256", "environ_sha256", "cmdline_sha256"):
        require(isinstance(value[key], str) and re.fullmatch(r"[a-f0-9]{64}", value[key]) is not None, "state_contract")
    require(
        isinstance(value["binary_path"], str) and pathlib.PurePosixPath(value["binary_path"]).is_absolute()
        and pathlib.PurePosixPath(value["binary_path"]).name == "molin-api"
        and isinstance(value["cwd"], str) and pathlib.PurePosixPath(value["cwd"]).is_absolute()
        and value["stdout_target"] == "/dev/null" and value["stderr_target"] == "/dev/null"
        and isinstance(value["created_utc"], str)
        and re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z", value["created_utc"]) is not None,
        "state_contract",
    )
    return value


def parse_nul_snapshot(path: pathlib.Path, allow_empty: bool = False) -> list[bytes]:
    raw = path.read_bytes()
    require((allow_empty or bool(raw)) and (not raw or raw.endswith(b"\0")), "snapshot_contract")
    values = raw[:-1].split(b"\0") if raw else []
    require(all(value and b"\0" not in value for value in values), "snapshot_contract")
    return values


def parse_environment_snapshot(path: pathlib.Path) -> dict[bytes, bytes]:
    """拒绝重复键；恢复保证全部键和值逐字节语义等价，不声称 envp 排序无损。"""
    environment: dict[bytes, bytes] = {}
    for item in parse_nul_snapshot(path):
        key, separator, value = item.partition(b"=")
        require(bool(separator) and bool(key) and b"=" not in key, "environment_contract")
        require(key not in environment, "environment_duplicate_key")
        environment[key] = value
    return environment


def verify_snapshot_artifacts(stage: pathlib.Path, state: dict[str, Any]) -> None:
    """每次启动前复核原始快照仍为 0600 普通文件且字节摘要未变。"""
    for name, digest_key in (("environ.raw", "environ_sha256"), ("cmdline.raw", "cmdline_sha256")):
        path = stage / name
        metadata = path.stat(follow_symlinks=False)
        require(
            stat.S_ISREG(metadata.st_mode) and not path.is_symlink()
            and stat.S_IMODE(metadata.st_mode) == 0o600
            and file_sha256(path) == state[digest_key],
            "snapshot_artifact_changed",
        )


def verify_launched_process(
    pid: int,
    state: dict[str, Any],
    expected_environment: dict[bytes, bytes],
    expected_cmdline: bytes,
) -> int:
    """复核二进制身份及完整环境键值语义，任何缺失、增加或值漂移都失败。"""
    starttime = proc_starttime(pid)
    executable = pathlib.Path(os.readlink(f"/proc/{pid}/exe"))
    metadata = executable.stat()
    actual_environment_path = pathlib.Path(f"/proc/{pid}/environ")
    actual_environment: dict[bytes, bytes] = {}
    raw_environment = actual_environment_path.read_bytes()
    require(bool(raw_environment) and raw_environment.endswith(b"\0"), "environment_recheck")
    for item in raw_environment[:-1].split(b"\0"):
        key, separator, value = item.partition(b"=")
        require(bool(separator) and bool(key) and key not in actual_environment, "environment_recheck")
        actual_environment[key] = value
    require(actual_environment == expected_environment, "environment_recheck")
    require(pathlib.Path(f"/proc/{pid}/cmdline").read_bytes() == expected_cmdline, "cmdline_recheck")
    require(
        os.stat(f"/proc/{pid}").st_uid == state["original_uid"]
        and os.readlink(f"/proc/{pid}/cwd") == state["cwd"]
        and metadata.st_dev == state["binary_device"]
        and metadata.st_ino == state["binary_inode"]
        and metadata.st_size == state["binary_size"]
        and file_sha256(executable) == state["binary_sha256"],
        "process_recheck",
    )
    return starttime


def verify_original_before_stop(
    pid: int,
    starttime: int,
    state: dict[str, Any],
    stage: pathlib.Path,
) -> None:
    """停止前再次逐字节复核全部冻结来源，避免快照与实际原进程之间出现窗口漂移。"""
    require(process_matches(pid, starttime), "process_identity_changed")
    executable = pathlib.Path(os.readlink(f"/proc/{pid}/exe"))
    metadata = executable.stat()
    require(
        pathlib.Path(f"/proc/{pid}/environ").read_bytes() == (stage / "environ.raw").read_bytes()
        and pathlib.Path(f"/proc/{pid}/cmdline").read_bytes() == (stage / "cmdline.raw").read_bytes()
        and os.readlink(f"/proc/{pid}/cwd") == state["cwd"]
        and metadata.st_dev == state["binary_device"]
        and metadata.st_ino == state["binary_inode"]
        and metadata.st_size == state["binary_size"]
        and file_sha256(executable) == state["binary_sha256"],
        "original_snapshot_changed",
    )


def launch(stage: pathlib.Path, state: dict[str, Any], capture: bool) -> tuple[int, int]:
    """每次 fork 都冻结 PID/starttime；失败通过 LaunchFailure 交给状态机精确收敛。"""
    verify_snapshot_artifacts(stage, state)
    cmdline_raw = (stage / "cmdline.raw").read_bytes()
    argv = parse_nul_snapshot(stage / "cmdline.raw")
    environment = parse_environment_snapshot(stage / "environ.raw")
    binary = os.fsencode(state["binary_path"])
    cwd = os.fsencode(state["cwd"])
    log_path = stage / "application.safe-source.log"
    child = os.fork()
    if child == 0:
        try:
            os.setsid()
            target = os.open(log_path if capture else "/dev/null", os.O_WRONLY | (os.O_APPEND if capture else 0) | os.O_NOFOLLOW)
            os.dup2(target, 1)
            os.dup2(target, 2)
            if target > 2:
                os.close(target)
            os.chdir(cwd)
            os.execve(binary, argv, environment)
        except BaseException:
            os._exit(126)
    try:
        child_starttime = proc_starttime(child)
    except (OSError, ValueError, GateFailure):
        child_starttime = 0
    OWNED_CHILDREN[child] = child_starttime
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        if child_starttime == 0:
            try:
                child_starttime = proc_starttime(child)
                OWNED_CHILDREN[child] = child_starttime
            except (OSError, ValueError, GateFailure):
                pass
        # exec 失败的直接子进程必须立即回收，避免 zombie 让 PID/starttime 收敛永久超时。
        require(OWNED_CHILDREN.get(child) == child_starttime, "child_ownership_changed")
        waited_pid, _status = os.waitpid(child, os.WNOHANG)
        if waited_pid == child:
            OWNED_CHILDREN.pop(child, None)
            raise LaunchFailure("restart_exited", child, child_starttime)
        try:
            pid = exact_api_process()
            if pid == child:
                starttime = verify_launched_process(pid, state, environment, cmdline_raw)
                require(child_starttime in {0, starttime}, "child_identity_changed")
                OWNED_CHILDREN[child] = starttime
                return pid, starttime
        except (OSError, GateFailure):
            pass
        time.sleep(0.2)
    raise LaunchFailure("restart_timeout", child, child_starttime)


def terminate(pid: int, starttime: int) -> None:
    """只向冻结的 PID/starttime 发送温和终止信号，并等待其自然退出。"""
    require(process_matches(pid, starttime), "process_identity_changed")
    os.kill(pid, signal.SIGTERM)
    owned_child = OWNED_CHILDREN.get(pid) == starttime
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        if owned_child:
            # 只有本 payload 直接 fork 且身份仍匹配的子 PID 才允许 waitpid 回收 zombie。
            try:
                waited_pid, _status = os.waitpid(pid, os.WNOHANG)
            except ChildProcessError as error:
                raise GateFailure("child_ownership_lost") from error
            if waited_pid == pid:
                OWNED_CHILDREN.pop(pid, None)
                return
        if not process_matches(pid, starttime):
            if owned_child:
                OWNED_CHILDREN.pop(pid, None)
            return
        time.sleep(0.2)
    raise GateFailure("graceful_stop_timeout")


def terminate_attempt(pid: int, starttime: int) -> None:
    """失败启动若仍是同一 PID/starttime 则先收敛；已退出或 PID 已复用时绝不误发信号。"""
    if starttime <= 0:
        require(not pathlib.Path(f"/proc/{pid}").exists(), "attempt_identity_unknown")
        return
    if starttime > 0 and process_matches(pid, starttime):
        terminate(pid, starttime)


def set_active(state: dict[str, Any], status: str, pid: int, starttime: int) -> None:
    state["status"] = status
    state["capture_pid"] = pid
    state["capture_starttime"] = starttime


def seal_log(stage: pathlib.Path) -> None:
    """采集进程退出后把日志封闭为 0400，满足投影只读来源门禁。"""
    log_path = stage / "application.safe-source.log"
    require(log_path.is_file() and not log_path.is_symlink(), "log_contract")
    os.chmod(log_path, 0o400, follow_symlinks=False)
    require(stat.S_IMODE(log_path.stat(follow_symlinks=False).st_mode) == 0o400, "log_mode")


def recover_original(
    stage: pathlib.Path,
    state: dict[str, Any],
    state_path: pathlib.Path,
    success_status: str,
) -> bool:
    """只有确认不存在已知失败子进程后才恢复；恢复失败保留状态和全部证据。"""
    try:
        require(not named_api_pids(), "api_process_remains")
        restored_pid, restored_starttime = launch(stage, state, False)
        set_active(state, success_status, restored_pid, restored_starttime)
        try:
            write_state(state_path, state)
        except (GateFailure, OSError):
            # 新进程已完成全部身份复核；状态落盘失败不应误报停机或再次启动造成双实例。
            return True
        return True
    except LaunchFailure as error:
        try:
            terminate_attempt(error.pid, error.starttime)
        except GateFailure:
            pass
        state["status"] = "recovery_failed"
        try:
            write_state(state_path, state)
        except (GateFailure, OSError):
            pass
        return False
    except (GateFailure, OSError):
        state["status"] = "recovery_failed"
        try:
            write_state(state_path, state)
        except (GateFailure, OSError):
            pass
        return False


def begin_capture_transition(
    stage: pathlib.Path,
    state: dict[str, Any],
    state_path: pathlib.Path,
    pid: int,
    starttime: int,
) -> None:
    """执行可注入测试的 capture 状态机；每个副作用前后均持久化或保留失败证据。"""
    require(process_matches(pid, starttime), "process_identity_changed")
    state["status"] = "stopping_original"
    write_state(state_path, state)
    original_stopped = False
    launched: tuple[int, int] | None = None
    try:
        terminate(pid, starttime)
        original_stopped = True
        state["status"] = "original_stopped"
        write_state(state_path, state)
        capture_pid, capture_starttime = launch(stage, state, True)
        launched = (capture_pid, capture_starttime)
        set_active(state, "capturing", capture_pid, capture_starttime)
        write_state(state_path, state)
    except Exception as error:
        if not original_stopped and not process_matches(pid, starttime):
            original_stopped = True
        attempt = launched
        if isinstance(error, LaunchFailure):
            attempt = (error.pid, error.starttime)
        cleanup_ok = True
        artifact_ok = True
        if attempt is not None:
            try:
                terminate_attempt(*attempt)
                require(not process_matches(*attempt), "capture_attempt_still_running")
            except (GateFailure, OSError):
                cleanup_ok = False
        if original_stopped:
            try:
                seal_log(stage)
            except (GateFailure, OSError):
                artifact_ok = False
        if not original_stopped:
            state["status"] = "original_stop_failed"
            try:
                write_state(state_path, state)
            except (GateFailure, OSError):
                pass
            raise OperationFailure("unchanged_or_unknown") from error
        if not cleanup_ok:
            state["status"] = "capture_cleanup_failed"
            try:
                write_state(state_path, state)
            except (GateFailure, OSError):
                pass
            raise OperationFailure("stopped_or_unknown") from error
        try:
            require(not named_api_pids(), "unexpected_api_process")
        except (GateFailure, OSError):
            state["status"] = "capture_cleanup_failed"
            try:
                write_state(state_path, state)
            except (GateFailure, OSError):
                pass
            raise OperationFailure("stopped_or_unknown") from error
        recovery_status = (
            "recovered_after_capture_failure" if artifact_ok
            else "recovered_after_capture_failure_log_unsealed"
        )
        recovered = recover_original(stage, state, state_path, recovery_status)
        raise OperationFailure("running" if recovered else "stopped_or_unknown") from error


def finish_restore_transition(
    stage: pathlib.Path,
    state: dict[str, Any],
    state_path: pathlib.Path,
    capture_pid: int,
    capture_starttime: int,
) -> None:
    """执行可注入测试的 restore 状态机，失败启动必须先收敛再进行一次恢复尝试。"""
    state["status"] = "stopping_capture"
    write_state(state_path, state)
    capture_stopped = False
    launched: tuple[int, int] | None = None
    try:
        terminate(capture_pid, capture_starttime)
        capture_stopped = True
        seal_log(stage)
        state["status"] = "capture_stopped"
        write_state(state_path, state)
        restored_pid, restored_starttime = launch(stage, state, False)
        launched = (restored_pid, restored_starttime)
        set_active(state, "restored", restored_pid, restored_starttime)
        write_state(state_path, state)
    except Exception as error:
        if not capture_stopped and not process_matches(capture_pid, capture_starttime):
            capture_stopped = True
        attempt = launched
        if isinstance(error, LaunchFailure):
            attempt = (error.pid, error.starttime)
        cleanup_ok = True
        artifact_ok = True
        if attempt is not None:
            try:
                terminate_attempt(*attempt)
                require(not process_matches(*attempt), "restore_attempt_still_running")
            except (GateFailure, OSError):
                cleanup_ok = False
        if not capture_stopped:
            state["status"] = "capture_stop_failed"
            try:
                write_state(state_path, state)
            except (GateFailure, OSError):
                pass
            raise OperationFailure("running_or_unknown") from error
        try:
            seal_log(stage)
        except (GateFailure, OSError):
            artifact_ok = False
        if not cleanup_ok:
            state["status"] = "restore_cleanup_failed"
            try:
                write_state(state_path, state)
            except (GateFailure, OSError):
                pass
            raise OperationFailure("stopped_or_unknown") from error
        try:
            require(not named_api_pids(), "unexpected_api_process")
        except (GateFailure, OSError):
            state["status"] = "restore_cleanup_failed"
            try:
                write_state(state_path, state)
            except (GateFailure, OSError):
                pass
            raise OperationFailure("stopped_or_unknown") from error
        recovery_status = "restored_after_retry" if artifact_ok else "restored_after_retry_log_unsealed"
        recovered = recover_original(stage, state, state_path, recovery_status)
        raise OperationFailure("running" if recovered else "stopped_or_unknown") from error


def capture(capture_id: str) -> None:
    require(os.name == "posix" and os.geteuid() == os.getuid(), "platform_gate")
    require(CAPTURE_ROOT.parent.is_dir() and not CAPTURE_ROOT.parent.is_symlink(), "capture_root")
    CAPTURE_ROOT.mkdir(mode=0o700, exist_ok=True)
    require(
        CAPTURE_ROOT.is_dir() and not CAPTURE_ROOT.is_symlink()
        and CAPTURE_ROOT.stat().st_uid == os.getuid()
        and stat.S_IMODE(CAPTURE_ROOT.stat().st_mode) == 0o700,
        "capture_root",
    )
    pid = exact_api_process()
    starttime = proc_starttime(pid)
    require(os.stat(f"/proc/{pid}").st_uid == os.getuid(), "uid_gate")
    binary = pathlib.Path(os.readlink(f"/proc/{pid}/exe"))
    require(binary.is_absolute() and binary.name == "molin-api" and not binary.is_symlink(), "binary_gate")
    cwd = os.readlink(f"/proc/{pid}/cwd")
    stdout_target = os.readlink(f"/proc/{pid}/fd/1")
    stderr_target = os.readlink(f"/proc/{pid}/fd/2")
    require(stdout_target == "/dev/null" and stderr_target == "/dev/null", "redirect_gate")
    metadata = binary.stat()
    stage = CAPTURE_ROOT / capture_id
    stage.mkdir(mode=0o700)
    state: dict[str, Any] = {
        "version": 1, "capture_id": capture_id, "status": "prepared",
        "original_pid": pid, "original_starttime": starttime, "original_uid": os.getuid(),
        "binary_path": str(binary), "binary_sha256": file_sha256(binary),
        "binary_device": metadata.st_dev, "binary_inode": metadata.st_ino, "binary_size": metadata.st_size,
        "cwd": cwd, "stdout_target": stdout_target, "stderr_target": stderr_target,
        "capture_pid": 0, "capture_starttime": 0,
        "environ_sha256": "", "cmdline_sha256": "",
        "created_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }
    write_exclusive(stage / "environ.raw", pathlib.Path(f"/proc/{pid}/environ").read_bytes())
    write_exclusive(stage / "cmdline.raw", pathlib.Path(f"/proc/{pid}/cmdline").read_bytes())
    # 重复环境键必须在任何进程信号之前失败；cmdline 也先完成严格 NUL 契约验证。
    parse_environment_snapshot(stage / "environ.raw")
    parse_nul_snapshot(stage / "cmdline.raw")
    state["environ_sha256"] = file_sha256(stage / "environ.raw")
    state["cmdline_sha256"] = file_sha256(stage / "cmdline.raw")
    write_exclusive(stage / "application.safe-source.log", b"")
    state_path = stage / "state.json"
    write_state(state_path, state)
    verify_original_before_stop(pid, starttime, state, stage)
    begin_capture_transition(stage, state, state_path, pid, starttime)
    print(f"status=pass mode=capture capture_id={capture_id} api_count=1 log_mode=600 state_mode=600 service_running=true")


def restore(capture_id: str) -> None:
    stage = CAPTURE_ROOT / capture_id
    state_path = stage / "state.json"
    require(stage.is_dir() and not stage.is_symlink(), "stage_gate")
    require(stat.S_IMODE(state_path.stat(follow_symlinks=False).st_mode) == 0o600, "state_mode")
    state = read_restore_state(state_path, capture_id)
    capture_pid = state["capture_pid"]
    capture_starttime = state["capture_starttime"]
    require(exact_api_process() == capture_pid and process_matches(capture_pid, capture_starttime), "api_not_unique")
    verify_snapshot_artifacts(stage, state)
    expected_environment = parse_environment_snapshot(stage / "environ.raw")
    expected_cmdline = (stage / "cmdline.raw").read_bytes()
    require(
        verify_launched_process(capture_pid, state, expected_environment, expected_cmdline) == capture_starttime,
        "capture_process_changed",
    )
    log_path = stage / "application.safe-source.log"
    log_metadata = log_path.stat(follow_symlinks=False)
    require(
        stat.S_ISREG(log_metadata.st_mode) and not log_path.is_symlink()
        and stat.S_IMODE(log_metadata.st_mode) == 0o600,
        "log_contract",
    )
    finish_restore_transition(stage, state, state_path, capture_pid, capture_starttime)
    print(f"status=pass mode=restore capture_id={capture_id} api_count=1 log_mode=400 state_mode=600 service_running=true")


def main() -> int:
    if len(sys.argv) == 1:
        print("status=disabled mode=phase4_api_log external_access=false process_changes=false")
        return 0
    try:
        require(len(sys.argv) == 4, "argument_contract")
        action, confirm, capture_id = sys.argv[1:]
        require(CAPTURE_ID_RE.fullmatch(capture_id) is not None, "capture_id")
        if action == "capture":
            require(confirm == CONFIRM_CAPTURE, "confirmation")
            capture(capture_id)
        elif action == "restore":
            require(confirm == CONFIRM_RESTORE, "confirmation")
            restore(capture_id)
        else:
            raise GateFailure("action")
        return 0
    except OperationFailure as error:
        print(
            "status=failed mode=phase4_api_log classification=closed service_state="
            + error.service_state + " evidence_retained=true"
        )
        return 2
    except (GateFailure, OSError, ValueError, json.JSONDecodeError):
        print("status=failed mode=phase4_api_log classification=closed service_state=unchanged_or_unknown evidence_retained=true")
        return 2
    except Exception:
        print("status=failed mode=phase4_api_log classification=closed service_state=unchanged_or_unknown evidence_retained=true")
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
