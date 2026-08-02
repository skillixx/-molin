#!/usr/bin/env python3
"""离线验证 Phase 4 日志采集与前端导出资产的关闭门禁和纯函数契约。"""

from __future__ import annotations

import hashlib
import importlib.util
import io
import os
import pathlib
import signal
import subprocess
import sys
import tarfile
import tempfile
import time

sys.dont_write_bytecode = True

HERE = pathlib.Path(__file__).resolve().parent
API = HERE / "email-phase4-api-log-capture.payload.py"
FRONTEND = HERE / "email-phase4-frontend-export.payload.py"
LAUNCHERS = (
    HERE / "run-email-phase4-api-log-capture.ps1",
    HERE / "run-email-phase4-frontend-export.ps1",
)


class ContractFailure(Exception):
    """离线失败仅输出固定分类。"""


def require(condition: bool, classification: str) -> None:
    if not condition:
        raise ContractFailure(classification)


def load_module(path: pathlib.Path, name: str):
    spec = importlib.util.spec_from_file_location(name, path)
    require(spec is not None and spec.loader is not None, "module_spec")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


def run_default(path: pathlib.Path, optimized: bool) -> str:
    command = [sys.executable]
    if optimized:
        command.append("-O")
    command.extend(["-B", str(path)])
    result = subprocess.run(
        command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        text=True, check=False, timeout=20,
    )
    require(result.returncode == 0 and result.stderr == "", "default_execution")
    return result.stdout


def reference_identity(root: pathlib.Path) -> tuple[str, int, int]:
    """独立复刻准备器的 LIFO 遍历，防止被测函数自证。"""
    digest = hashlib.sha256()
    count = 0
    total = 0
    stack = [root]
    while stack:
        current = stack.pop()
        for target in sorted(current.iterdir(), key=lambda value: value.name.encode("utf-8")):
            relative = target.relative_to(root).as_posix()
            if target.is_dir():
                digest.update(b"D\0" + relative.encode() + b"\0")
                stack.append(target)
            else:
                data = target.read_bytes()
                count += 1
                total += len(data)
                digest.update(
                    b"F\0" + relative.encode() + b"\0" + str(len(data)).encode("ascii")
                    + b"\0" + hashlib.sha256(data).digest()
                )
    return digest.hexdigest(), count, total


def assert_closed_fixture_tree(root: pathlib.Path) -> None:
    """确认真实 projection 输入树已完整封闭，目录 0555、文件 0444。"""
    require(root.is_dir() and not root.is_symlink(), "fixture_close_root")
    for current, directories, names in os.walk(root, topdown=True, followlinks=False):
        current_path = pathlib.Path(current)
        require(not current_path.is_symlink() and (current_path.stat().st_mode & 0o777) == 0o555, "fixture_close_directory")
        for directory in directories:
            require(not (current_path / directory).is_symlink(), "fixture_close_symlink")
        for name in names:
            target = current_path / name
            require(not target.is_symlink() and (target.stat().st_mode & 0o777) == 0o444, "fixture_close_file")


def fixture_tree_is_safe_for_cleanup(root: pathlib.Path, temporary_root: pathlib.Path) -> bool:
    """仅在树真实存在、不是 symlink 且解析后仍位于本次临时根时允许恢复权限。"""
    if not root.exists() or root.is_symlink():
        return False
    try:
        resolved_root = root.resolve(strict=True)
        resolved_temporary = temporary_root.resolve(strict=True)
        return (
            os.path.commonpath((str(resolved_root), str(resolved_temporary))) == str(resolved_temporary)
            and resolved_root != resolved_temporary
        )
    except (OSError, RuntimeError, ValueError):
        return False


def reopen_fixture_tree_for_cleanup(root: pathlib.Path, temporary_root: pathlib.Path) -> None:
    """仅恢复本 contract 临时树的清理权限；路径逃逸或 symlink 一律拒绝。"""
    require(fixture_tree_is_safe_for_cleanup(root, temporary_root), "fixture_cleanup_scope")
    for current, directories, names in os.walk(root, topdown=False, followlinks=False):
        current_path = pathlib.Path(current)
        require(not current_path.is_symlink(), "fixture_cleanup_symlink")
        for name in names:
            target = current_path / name
            require(not target.is_symlink(), "fixture_cleanup_symlink")
            os.chmod(target, 0o600, follow_symlinks=False)
        for directory in directories:
            target = current_path / directory
            require(not target.is_symlink(), "fixture_cleanup_symlink")
            os.chmod(target, 0o700, follow_symlinks=False)
        os.chmod(current_path, 0o700, follow_symlinks=False)
    for current, directories, names in os.walk(root, topdown=True, followlinks=False):
        current_path = pathlib.Path(current)
        require((current_path.stat().st_mode & 0o777) == 0o700, "fixture_cleanup_directory_mode")
        for directory in directories:
            require(not (current_path / directory).is_symlink(), "fixture_cleanup_symlink")
        for name in names:
            target = current_path / name
            require(not target.is_symlink() and (target.stat().st_mode & 0o777) == 0o600, "fixture_cleanup_file_mode")


def main() -> int:
    cases = 0
    for optimized in (False, True):
        require(
            run_default(API, optimized)
            == "status=disabled mode=phase4_api_log external_access=false process_changes=false\n",
            "api_default",
        )
        require(
            run_default(FRONTEND, optimized)
            == "status=disabled mode=frontend_export docker_access=false persistent_writes=false\n",
            "frontend_default",
        )
        cases += 2

    # 危险能力扫描覆盖 payload 与启动器；只允许精确 SIGTERM、docker inspect/cp 和固定 SSH。
    forbidden = (
        b"SIGKILL", b"kill -9", b"pkill", b"/bin/rm ", b"rmtree", b"git ",
        b"mysql", b"redis-cli", b"SingleSendMail", b"curl ", b"docker exec",
    )
    for path in (API, FRONTEND, *LAUNCHERS):
        raw = path.read_bytes()
        require(all(token not in raw for token in forbidden), "dangerous_capability")
        cases += 1
    api_raw = API.read_bytes()
    require(b"signal.SIGTERM" in api_raw and b"os.kill(pid, signal.SIGTERM)" in api_raw, "term_only")
    require(b"os.waitpid(child, os.WNOHANG)" in api_raw, "child_reaping")
    frontend_raw = FRONTEND.read_bytes()
    require(b'"docker", "inspect"' in frontend_raw and b'"docker", "cp"' in frontend_raw, "docker_readonly")
    fixed_projection = b"/home/pc/molin-runtime/phase4-ops-linux-0346ff54/tests/email/phase4_runtime_source_projection.py"
    require(
        fixed_projection in frontend_raw and b"len(sys.argv) == 4" in frontend_raw
        and b"verify_projection_path(sys.argv[3])" in frontend_raw,
        "projection_argv_binding",
    )
    frontend_launcher_raw = LAUNCHERS[1].read_bytes()
    require(
        fixed_projection in frontend_launcher_raw and b"$ExportId $projectionPath" in frontend_launcher_raw
        and b"projection_bound=true" in frontend_launcher_raw,
        "projection_launcher_binding",
    )
    cases += 5
    for launcher in LAUNCHERS:
        raw = launcher.read_bytes()
        require(b"TimeoutMilliseconds 120000" in raw and b"DateTime]::UtcNow" in raw, "launcher_timeout")
        require(b"Length -gt 512" in raw and b"payload_encoding_invalid" in raw, "launcher_output_encoding")
        require(
            b"RedirectStandardInput $stdinPath" in raw and b"Test-ExactStdinTransport" in raw
            and b"StandardInput.BaseStream" not in raw and b"StandardInputEncoding" not in raw,
            "launcher_exact_stdin",
        )
        require(b"if ($result.ExitCode -eq 2) { exit 2 }" in raw, "launcher_failure_exit")
        cases += 4

    api = load_module(API, "_phase4_api_log_contract")
    frontend = load_module(FRONTEND, "_phase4_frontend_export_contract")
    # Windows 仅为纯函数短写夹具补零值常量；真实 payload 仍由 POSIX 平台门禁限制。
    if not hasattr(os, "O_NOFOLLOW"):
        os.O_NOFOLLOW = 0
    with tempfile.TemporaryDirectory(prefix="molin-phase4-frontend-contract-") as temporary:
        temporary_root = pathlib.Path(temporary)
        if os.name == "posix":
            require((temporary_root.stat().st_mode & 0o777) == 0o700, "temporary_root_mode")
            cases += 1
        environment_path = temporary_root / "environ.raw"
        environment_path.write_bytes(b"A=1\0B=two\0")
        require(api.parse_environment_snapshot(environment_path) == {b"A": b"1", b"B": b"two"}, "environment_parse")
        environment_path.write_bytes(b"A=1\0A=2\0")
        try:
            api.parse_environment_snapshot(environment_path)
        except api.GateFailure as error:
            require(error.args == ("environment_duplicate_key",), "environment_duplicate_classification")
            cases += 2
        else:
            raise ContractFailure("environment_duplicate_accepted")

        # 两套写入函数都必须在内核短写时循环到完整字节，不能把裁剪文件当成成功。
        for module, writer_name in ((api, "write_exclusive"), (frontend, "write_all")):
            saved_write = module.os.write
            saved_imode = module.stat.S_IMODE
            module.os.write = lambda descriptor, data, _saved=saved_write: _saved(descriptor, bytes(data[:2]))
            if os.name != "posix" and writer_name == "write_exclusive":
                module.stat.S_IMODE = lambda _mode: 0o600
            short_path = temporary_root / (writer_name + ".bin")
            try:
                if writer_name == "write_exclusive":
                    module.write_exclusive(short_path, b"abcdef")
                else:
                    descriptor = os.open(short_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                    try:
                        module.write_all(descriptor, b"abcdef")
                    finally:
                        os.close(descriptor)
                require(short_path.read_bytes() == b"abcdef", "short_write_content")
                cases += 1
            finally:
                module.os.write = saved_write
                module.stat.S_IMODE = saved_imode

        zero_path = temporary_root / "zero-write.bin"
        zero_descriptor = os.open(zero_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        saved_write = frontend.os.write
        frontend.os.write = lambda _descriptor, _data: 0
        try:
            try:
                frontend.write_all(zero_descriptor, b"x")
            except frontend.ExportFailure as error:
                require(error.args == ("short_write",), "zero_write_classification")
                cases += 1
            else:
                raise ContractFailure("zero_write_accepted")
        finally:
            frontend.os.write = saved_write
            os.close(zero_descriptor)

        log_stage = temporary_root / "log-stage"
        log_stage.mkdir()
        log_path = log_stage / "application.safe-source.log"
        log_path.write_bytes(b"safe")
        os.chmod(log_path, 0o600)
        if os.name == "posix":
            api.seal_log(log_stage)
            require((log_path.stat().st_mode & 0o777) == 0o400, "log_seal_mode")
        else:
            require(b"os.chmod(log_path, 0o400" in API.read_bytes(), "log_seal_static")
        cases += 1

        # 非本 payload 直接 fork 的 PID 即使退出，也绝不允许调用 waitpid。
        saved_process_matches = api.process_matches
        saved_kill = api.os.kill
        saved_waitpid = api.os.waitpid
        waitpid_calls = 0
        match_calls = 0
        def nonchild_matches(_pid, _start):
            nonlocal match_calls
            match_calls += 1
            return match_calls == 1
        def forbidden_waitpid(_pid, _options):
            nonlocal waitpid_calls
            waitpid_calls += 1
            return 0, 0
        api.process_matches = nonchild_matches
        api.os.kill = lambda _pid, _signal: None
        api.os.waitpid = forbidden_waitpid
        try:
            api.OWNED_CHILDREN.clear()
            api.terminate(777, 7)
            require(waitpid_calls == 0, "nonchild_waitpid")
            cases += 1
        finally:
            api.process_matches = saved_process_matches
            api.os.kill = saved_kill
            api.os.waitpid = saved_waitpid

        zombie_reap = "skipped_nonposix"
        if os.name == "posix":
            child = os.fork()
            if child == 0:
                try:
                    signal.pause()
                finally:
                    os._exit(0)
            child_start = 0
            try:
                deadline = time.monotonic() + 5
                while time.monotonic() < deadline and child_start == 0:
                    try:
                        child_start = api.proc_starttime(child)
                    except (OSError, ValueError, api.GateFailure):
                        time.sleep(0.01)
                require(child_start > 0, "posix_child_identity")
                api.OWNED_CHILDREN[child] = child_start
                api.terminate(child, child_start)
                require(
                    child not in api.OWNED_CHILDREN and not pathlib.Path(f"/proc/{child}").exists(),
                    "posix_zombie_reap",
                )
                zombie_reap = "pass"
                cases += 1
            finally:
                api.OWNED_CHILDREN.pop(child, None)
                if pathlib.Path(f"/proc/{child}").exists():
                    try:
                        os.kill(child, signal.SIGTERM)
                    except ProcessLookupError:
                        pass
                    try:
                        os.waitpid(child, 0)
                    except ChildProcessError:
                        pass

        # 状态机攻击：capture 启动失败、capturing 状态落盘失败、restore 启动失败都必须先清理已知 PID 再恢复。
        state = {key: 0 for key in api.STATE_KEYS}
        state.update({"version": 1, "capture_id": "0" * 32, "status": "prepared"})
        saved = {name: getattr(api, name) for name in (
            "process_matches", "terminate", "terminate_attempt", "launch", "write_state", "seal_log",
            "recover_original", "named_api_pids",
        )}
        try:
            live = {(10, 100)}
            statuses: list[str] = []
            api.process_matches = lambda pid, start: (pid, start) in live
            api.terminate = lambda pid, start: live.discard((pid, start))
            api.terminate_attempt = lambda pid, start: live.discard((pid, start))
            api.write_state = lambda _path, value: statuses.append(value["status"])
            api.seal_log = lambda _stage: None
            api.named_api_pids = lambda: set()
            launch_calls = 0
            def capture_launch(_stage, _state, _capture):
                nonlocal launch_calls
                launch_calls += 1
                live.add((20, 200))
                raise api.LaunchFailure("restart_timeout", 20, 200)
            api.launch = capture_launch
            api.recover_original = lambda _stage, value, _path, success: value.update({"status": success}) is None
            try:
                api.begin_capture_transition(log_stage, state, temporary_root / "state.json", 10, 100)
            except api.OperationFailure as error:
                require(error.service_state == "running" and (20, 200) not in live, "capture_failure_cleanup")
                require(isinstance(error.__cause__, api.LaunchFailure) and error.__cause__.args == ("restart_timeout",), "launch_timeout_classification")
                require(state["status"] == "recovered_after_capture_failure", "capture_failure_recovery")
                cases += 3
            else:
                raise ContractFailure("capture_failure_accepted")

            # capture 子进程已通过身份复核、但最终状态落盘失败时，也必须先收敛该 PID 再恢复。
            live = {(11, 110)}
            state["status"] = "prepared"
            api.process_matches = lambda pid, start: (pid, start) in live
            api.terminate = lambda pid, start: live.discard((pid, start))
            api.terminate_attempt = lambda pid, start: live.discard((pid, start))
            persisted_failure = False
            def fail_capturing_state(_path, value):
                nonlocal persisted_failure
                if value["status"] == "capturing" and not persisted_failure:
                    persisted_failure = True
                    raise OSError("injected")
            api.write_state = fail_capturing_state
            def successful_capture_launch(_stage, _state, _capture):
                live.add((21, 210))
                return 21, 210
            api.launch = successful_capture_launch
            api.recover_original = lambda _stage, value, _path, success: value.update({"status": success}) is None
            try:
                api.begin_capture_transition(log_stage, state, temporary_root / "state.json", 11, 110)
            except api.OperationFailure as error:
                require(error.service_state == "running" and (21, 210) not in live, "capture_state_failure_cleanup")
                cases += 1
            else:
                raise ContractFailure("capture_state_failure_accepted")

            live = {(12, 120)}
            state["status"] = "prepared"
            api.process_matches = lambda pid, start: (pid, start) in live
            api.terminate = lambda _pid, _start: (_ for _ in ()).throw(api.GateFailure("graceful_stop_timeout"))
            recovered_called = False
            def forbidden_recovery(*_args):
                nonlocal recovered_called
                recovered_called = True
                return True
            api.recover_original = forbidden_recovery
            api.write_state = lambda _path, _value: None
            try:
                api.begin_capture_transition(log_stage, state, temporary_root / "state.json", 12, 120)
            except api.OperationFailure as error:
                require(error.service_state == "unchanged_or_unknown" and not recovered_called, "capture_stop_failure")
                cases += 1
            else:
                raise ContractFailure("capture_stop_failure_accepted")

            live = {(30, 300)}
            state["status"] = "capturing"
            api.process_matches = lambda pid, start: (pid, start) in live
            api.terminate = lambda pid, start: live.discard((pid, start))
            api.terminate_attempt = lambda pid, start: live.discard((pid, start))
            def restore_launch(_stage, _state, _capture):
                live.add((40, 400))
                raise api.LaunchFailure("restart_timeout", 40, 400)
            api.launch = restore_launch
            api.recover_original = lambda _stage, value, _path, success: value.update({"status": success}) is None
            try:
                api.finish_restore_transition(log_stage, state, temporary_root / "state.json", 30, 300)
            except api.OperationFailure as error:
                require(error.service_state == "running" and (40, 400) not in live, "restore_failure_cleanup")
                require(state["status"] == "restored_after_retry", "restore_failure_recovery")
                cases += 2
            else:
                raise ContractFailure("restore_failure_accepted")

            # restore 子进程已启动但 restored 状态落盘失败时，必须收敛该 PID 后再恢复一次。
            live = {(32, 320)}
            state["status"] = "capturing"
            api.process_matches = lambda pid, start: (pid, start) in live
            api.terminate = lambda pid, start: live.discard((pid, start))
            api.terminate_attempt = lambda pid, start: live.discard((pid, start))
            restored_state_failed = False
            def fail_restored_state(_path, value):
                nonlocal restored_state_failed
                if value["status"] == "restored" and not restored_state_failed:
                    restored_state_failed = True
                    raise OSError("injected")
            api.write_state = fail_restored_state
            def successful_restore_launch(_stage, _state, _capture):
                live.add((42, 420))
                return 42, 420
            api.launch = successful_restore_launch
            api.recover_original = lambda _stage, value, _path, success: value.update({"status": success}) is None
            try:
                api.finish_restore_transition(log_stage, state, temporary_root / "state.json", 32, 320)
            except api.OperationFailure as error:
                require(
                    error.service_state == "running" and (42, 420) not in live
                    and state["status"] == "restored_after_retry",
                    "restore_state_failure_cleanup",
                )
                cases += 1
            else:
                raise ContractFailure("restore_state_failure_accepted")

            live = {(31, 310)}
            state["status"] = "capturing"
            api.process_matches = lambda pid, start: (pid, start) in live
            api.terminate = lambda pid, start: live.discard((pid, start))
            api.terminate_attempt = lambda _pid, _start: (_ for _ in ()).throw(api.GateFailure("graceful_stop_timeout"))
            api.launch = lambda _stage, _state, _capture: (
                live.add((41, 410)),
                (_ for _ in ()).throw(api.LaunchFailure("restart_failed", 41, 410)),
            )[1]
            api.write_state = lambda _path, _value: None
            api.recover_original = forbidden_recovery
            recovered_called = False
            try:
                api.finish_restore_transition(log_stage, state, temporary_root / "state.json", 31, 310)
            except api.OperationFailure as error:
                require(
                    error.service_state == "stopped_or_unknown"
                    and state["status"] == "restore_cleanup_failed" and not recovered_called,
                    "restore_cleanup_failure",
                )
                cases += 1
            else:
                raise ContractFailure("restore_cleanup_failure_accepted")
        finally:
            for name, value in saved.items():
                setattr(api, name, value)

        projection = HERE.parent / "tests" / "email" / "phase4_runtime_source_projection.py"
        require(hashlib.sha256(projection.read_bytes()).hexdigest() == frontend.PROJECTION_SHA256, "projection_sha_binding")
        cases += 1

        if os.name == "posix":
            # companion 门禁攻击夹具覆盖缺失、错摘要、symlink 与任意路径注入。
            companion = temporary_root / "projection-companion.py"
            companion.write_bytes(projection.read_bytes())
            os.chmod(companion, 0o400, follow_symlinks=False)
            require(
                frontend.verify_projection_file(companion, companion, os.getuid()) == companion,
                "projection_companion_valid",
            )
            cases += 1

            projection_attacks = []
            missing_projection = temporary_root / "projection-missing.py"
            projection_attacks.append((missing_projection, missing_projection, "projection_binding"))
            wrong_projection = temporary_root / "projection-wrong.py"
            wrong_projection.write_bytes(b"wrong projection")
            os.chmod(wrong_projection, 0o400, follow_symlinks=False)
            projection_attacks.append((wrong_projection, wrong_projection, "projection_binding"))
            symlink_projection = temporary_root / "projection-link.py"
            symlink_projection.symlink_to(companion)
            projection_attacks.append((symlink_projection, symlink_projection, "projection_binding"))
            projection_attacks.append((companion, wrong_projection, "projection_path"))
            for candidate, expected_path, classification in projection_attacks:
                try:
                    frontend.verify_projection_file(candidate, expected_path, os.getuid())
                except frontend.ExportFailure as error:
                    require(error.args == (classification,), "projection_attack_classification")
                    cases += 1
                else:
                    raise ContractFailure("projection_attack_accepted")
            try:
                frontend.verify_projection_path(str(companion))
            except frontend.ExportFailure as error:
                require(error.args == ("projection_path",), "projection_argument_injection")
                cases += 1
            else:
                raise ContractFailure("projection_argument_injection_accepted")

            def require_projection_binding_failure(action, classification: str) -> None:
                """所有 companion 身份攻击必须收敛为固定分类，不泄露文件系统细节。"""
                try:
                    action()
                except frontend.ExportFailure as error:
                    require(error.args == (classification,), "projection_identity_attack_classification")
                else:
                    raise ContractFailure("projection_identity_attack_accepted")

            # owner 不匹配无需 root：使用与当前 UID 不同的期望值即可触发同一门禁。
            require_projection_binding_failure(
                lambda: frontend.verify_projection_file(companion, companion, os.getuid() + 1),
                "projection_binding",
            )
            cases += 1

            # 即使摘要函数会伪装返回固定 SHA，错误大小也必须在读取前关闭。
            truncated_projection = temporary_root / "projection-truncated.py"
            truncated_projection.write_bytes(projection.read_bytes()[:-1])
            os.chmod(truncated_projection, 0o400, follow_symlinks=False)
            saved_descriptor_sha256 = frontend.descriptor_sha256
            forged_digest_calls = 0

            def forged_matching_digest(_descriptor):
                nonlocal forged_digest_calls
                forged_digest_calls += 1
                return frontend.PROJECTION_SHA256

            try:
                frontend.descriptor_sha256 = forged_matching_digest
                require_projection_binding_failure(
                    lambda: frontend.verify_projection_file(
                        truncated_projection, truncated_projection, os.getuid()
                    ),
                    "projection_binding",
                )
            finally:
                frontend.descriptor_sha256 = saved_descriptor_sha256
            require(forged_digest_calls == 0, "projection_size_precedes_digest")
            cases += 1

            # 文件打开并完成摘要读取后把 mode 漂移到 0644，最终身份复核必须拒绝。
            mode_drift_projection = temporary_root / "projection-mode-drift.py"
            mode_drift_projection.write_bytes(projection.read_bytes())
            os.chmod(mode_drift_projection, 0o400, follow_symlinks=False)
            mode_drift_triggered = False

            def digest_then_drift_mode(descriptor):
                nonlocal mode_drift_triggered
                digest = saved_descriptor_sha256(descriptor)
                os.chmod(mode_drift_projection, 0o644, follow_symlinks=False)
                mode_drift_triggered = True
                return digest

            try:
                frontend.descriptor_sha256 = digest_then_drift_mode
                require_projection_binding_failure(
                    lambda: frontend.verify_projection_file(
                        mode_drift_projection, mode_drift_projection, os.getuid()
                    ),
                    "projection_binding",
                )
            finally:
                frontend.descriptor_sha256 = saved_descriptor_sha256
                os.chmod(mode_drift_projection, 0o400, follow_symlinks=False)
            require(mode_drift_triggered, "projection_mode_drift_triggered")
            cases += 1

            # fd 打开后用同内容、合法权限的另一 inode 原子替换路径，真实触发 fd/path 不一致。
            inode_target = temporary_root / "projection-inode-target.py"
            inode_replacement = temporary_root / "projection-inode-replacement.py"
            inode_target.write_bytes(projection.read_bytes())
            inode_replacement.write_bytes(projection.read_bytes())
            os.chmod(inode_target, 0o400, follow_symlinks=False)
            os.chmod(inode_replacement, 0o400, follow_symlinks=False)
            require(
                inode_target.parent == temporary_root and inode_replacement.parent == temporary_root
                and inode_target.stat().st_ino != inode_replacement.stat().st_ino,
                "projection_inode_fixture_scope",
            )
            inode_swap_triggered = False

            def digest_then_swap_inode(descriptor):
                nonlocal inode_swap_triggered
                digest = saved_descriptor_sha256(descriptor)
                os.replace(inode_replacement, inode_target)
                inode_swap_triggered = True
                return digest

            try:
                frontend.descriptor_sha256 = digest_then_swap_inode
                require_projection_binding_failure(
                    lambda: frontend.verify_projection_file(inode_target, inode_target, os.getuid()),
                    "projection_binding",
                )
            finally:
                frontend.descriptor_sha256 = saved_descriptor_sha256
            require(
                inode_swap_triggered and inode_target.is_file() and not inode_target.is_symlink()
                and inode_target.parent == temporary_root,
                "projection_inode_swap_triggered",
            )
            cases += 1

        root = pathlib.Path(temporary) / "tree"
        (root / "assets" / "nested").mkdir(parents=True)
        (root / "index.html").write_bytes(b"index")
        (root / "assets" / "app.js").write_bytes(b"script")
        (root / "assets" / "app.css").write_bytes(b"style")
        (root / "assets" / "nested" / "z.txt").write_bytes(b"nested")
        actual = frontend.tree_identity(root)
        expected = reference_identity(root)
        require((actual.tree_sha256, actual.file_count, actual.byte_count) == expected, "tree_identity")
        cases += 1
        if os.name == "posix":
            projection_module = load_module(projection, "_phase4_real_projection_contract")
            try:
                frontend.close_tree(root)
                assert_closed_fixture_tree(root)
                projection_identity = projection_module.frontend_identity(root)
                require(
                    (projection_identity.tree_sha256, projection_identity.file_count, projection_identity.byte_count)
                    == (actual.tree_sha256, actual.file_count, actual.byte_count),
                    "real_projection_crosscheck",
                )
                cases += 1
            finally:
                require(fixture_tree_is_safe_for_cleanup(root, temporary_root), "fixture_cleanup_scope")
                reopen_fixture_tree_for_cleanup(root, temporary_root)

            # 注入 close_tree 中途 chmod 失败，finally 仍只能恢复本次子树，不能改动树外哨兵。
            fault_root = temporary_root / "close-fault-tree"
            (fault_root / "assets").mkdir(parents=True)
            (fault_root / "index.html").write_bytes(b"index")
            (fault_root / "assets" / "app.js").write_bytes(b"script")
            outside_sentinel = temporary_root / "close-fault-outside.guard"
            outside_sentinel.write_bytes(b"outside")
            os.chmod(outside_sentinel, 0o400, follow_symlinks=False)
            saved_chmod = frontend.os.chmod
            chmod_calls = 0
            close_failed = False

            def fail_mid_close(path, mode, *, follow_symlinks=True):
                nonlocal chmod_calls
                chmod_calls += 1
                if chmod_calls == 2:
                    raise OSError("injected_close_tree_chmod_failure")
                return saved_chmod(path, mode, follow_symlinks=follow_symlinks)

            try:
                frontend.os.chmod = fail_mid_close
                try:
                    frontend.close_tree(fault_root)
                except OSError as error:
                    require(str(error) == "injected_close_tree_chmod_failure", "close_tree_fault_classification")
                    close_failed = True
            finally:
                frontend.os.chmod = saved_chmod
                require(fixture_tree_is_safe_for_cleanup(fault_root, temporary_root), "close_tree_fault_cleanup_scope")
                reopen_fixture_tree_for_cleanup(fault_root, temporary_root)
            require(close_failed and chmod_calls == 2, "close_tree_fault_injected")
            require((outside_sentinel.stat().st_mode & 0o777) == 0o400, "close_tree_fault_no_escape")
            cases += 1

        empty = pathlib.Path(temporary) / "empty"
        empty.mkdir()
        try:
            frontend.tree_identity(empty)
        except frontend.ExportFailure as error:
            require(error.args == ("frontend_incomplete",), "empty_classification")
            cases += 1
        else:
            raise ContractFailure("empty_accepted")

        if hasattr(os, "symlink"):
            symlink_tree = pathlib.Path(temporary) / "symlink"
            (symlink_tree / "assets").mkdir(parents=True)
            (symlink_tree / "index.html").write_bytes(b"index")
            (symlink_tree / "assets" / "app.css").write_bytes(b"style")
            try:
                os.symlink(root / "assets" / "app.js", symlink_tree / "assets" / "app.js")
            except OSError:
                pass
            else:
                try:
                    frontend.tree_identity(symlink_tree)
                except frontend.ExportFailure as error:
                    require(error.args == ("symlink",), "symlink_classification")
                    cases += 1
                else:
                    raise ContractFailure("symlink_accepted")

        # 截断 tar 必须在成员 EOF 前失败，不能产生可接受的完整树。
        tar_buffer = io.BytesIO()
        with tarfile.open(fileobj=tar_buffer, mode="w") as archive:
            info = tarfile.TarInfo("index.html")
            info.size = 10
            archive.addfile(info, io.BytesIO(b"0123456789"))
        truncated = tar_buffer.getvalue()[:515]
        class FakeProcess:
            def __init__(self) -> None:
                self.stdout = io.BytesIO(truncated)
            def wait(self, timeout=None):
                return 0
            def poll(self):
                return 0
            def terminate(self):
                return None
        saved_popen = frontend.subprocess.Popen
        frontend.subprocess.Popen = lambda *args, **kwargs: FakeProcess()
        try:
            try:
                frontend.export_tree("molin-admin", temporary_root / "truncated")
            except (frontend.ExportFailure, tarfile.TarError, OSError):
                cases += 1
            else:
                raise ContractFailure("truncated_archive_accepted")
        finally:
            frontend.subprocess.Popen = saved_popen

        frozen = {"molin-admin": ("a" * 64, "sha256:" + "b" * 64), "molin-user": ("c" * 64, "sha256:" + "d" * 64)}
        bindings = (("molin-admin", "admin_frontend", "admin"), ("molin-user", "user_frontend", "user"))
        saved_inspect = frontend.inspect_container
        frontend.inspect_container = lambda name: frozen[name] if name == "molin-user" else ("e" * 64, "sha256:" + "b" * 64)
        try:
            try:
                frontend.verify_container_bindings(bindings, frozen)
            except frontend.ExportFailure as error:
                require(error.args == ("container_changed",), "container_drift_classification")
                cases += 1
            else:
                raise ContractFailure("container_drift_accepted")
        finally:
            frontend.inspect_container = saved_inspect

    crosscheck = "pass" if os.name == "posix" else "skipped_nonposix"
    print(
        f"status=pass mode=phase4_ops_assets_contract cases={cases} "
        f"projection_crosscheck={crosscheck} zombie_reap={zombie_reap} "
        "external_access=false process_changes=false container_access=false"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ContractFailure, OSError, subprocess.SubprocessError):
        print("status=failed mode=phase4_ops_assets_contract classification=closed")
        raise SystemExit(1)
