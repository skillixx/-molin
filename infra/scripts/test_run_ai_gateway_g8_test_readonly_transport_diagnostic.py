#!/usr/bin/env python3
"""验证 G8 测试服低敏 SSH 传输诊断严格单次、无正文泄露且失败关闭。"""

import importlib.util
import subprocess
import sys
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("run-ai-gateway-g8-test-readonly-transport-diagnostic.py")


def load_module():
    """从精确脚本路径加载模块，避免从 PATH 搜索替代实现。"""
    spec = importlib.util.spec_from_file_location("g8_transport_diagnostic", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("module_load_failed")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


MODULE = load_module()


class TestTransportDiagnostic(unittest.TestCase):
    def test_interpreter_and_self_test_are_fail_closed(self) -> None:
        """普通解释器必须拒绝，隔离解释器自检必须通过。"""
        ordinary = subprocess.run(
            ["python", str(SCRIPT_PATH), "--self-test"], capture_output=True, text=True, check=False
        )
        self.assertEqual(ordinary.returncode, 2)
        self.assertEqual(
            ordinary.stdout.strip(),
            "G8_TEST_READONLY_TRANSPORT_DIAG=FAILED reason=isolated_python_required",
        )
        isolated = subprocess.run(
            ["python", "-I", str(SCRIPT_PATH), "--self-test"],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(isolated.returncode, 0, isolated.stderr)
        self.assertEqual(isolated.stdout.strip(), "G8_TEST_READONLY_TRANSPORT_DIAG_SELF_TEST=PASS")

    def test_remote_program_has_no_file_or_process_capability(self) -> None:
        """远端标记程序不得读取文件、启动子进程或改变状态。"""
        self.assertIn("sys.flags.isolated", MODULE.REMOTE_PROGRAM)
        self.assertIn("pwd.getpwuid", MODULE.REMOTE_PROGRAM)
        for forbidden in ("open(", "subprocess", "remove(", "unlink(", "rmdir(", "sudo"):
            self.assertNotIn(forbidden, MODULE.REMOTE_PROGRAM)

    def test_classification_never_returns_output_body(self) -> None:
        """分类只能包含固定枚举、计数和摘要，不能携带 stdout/stderr 正文。"""
        secret = b"SENSITIVE_REMOTE_ERROR_BODY\n"
        result = MODULE.classify_result(subprocess.CompletedProcess([], 255, b"unexpected", secret))
        self.assertEqual(result["ssh_exit_class"], "TRANSPORT_255")
        self.assertEqual(result["diagnostic"], "EXIT_NONZERO")
        self.assertEqual(result["stderr_state"], "PRESENT")
        self.assertEqual(result["stderr_lines"], "1")
        self.assertNotIn("SENSITIVE", repr(result))

    def test_classification_distinguishes_four_fixed_outcomes(self) -> None:
        """成功、非零、stderr 和 stdout 不匹配必须得到不同固定结论。"""
        cases = (
            (subprocess.CompletedProcess([], 0, MODULE.REMOTE_MARKER, b""), "PASS"),
            (subprocess.CompletedProcess([], 41, b"", b""), "EXIT_NONZERO"),
            (subprocess.CompletedProcess([], 0, MODULE.REMOTE_MARKER, b"notice\n"), "STDERR_PRESENT"),
            (subprocess.CompletedProcess([], 0, b"other\n", b""), "STDOUT_MISMATCH"),
        )
        for completed, expected in cases:
            with self.subTest(expected=expected):
                self.assertEqual(MODULE.classify_result(completed)["diagnostic"], expected)

    def test_remote_call_is_exactly_once_with_fixed_ssh(self) -> None:
        """正式诊断只能调用一次固定 SSH，远端程序只经 stdin 传递。"""
        helper = SimpleNamespace(
            TARGET_PORT="10003",
            TARGET="pc@8.130.9.163",
            fixed_ssh_executable=lambda: Path("/usr/bin/ssh"),
            fixed_ssh_environment=lambda: {"PATH": "/usr/bin:/bin"},
        )
        completed = subprocess.CompletedProcess([], 0, MODULE.REMOTE_MARKER, b"")
        with mock.patch.object(MODULE.subprocess, "run", return_value=completed) as run:
            result = MODULE.run_transport_diagnostic(helper, Path("/fixed/known_hosts"), Path("/fixed/key"))
        self.assertEqual(run.call_count, 1)
        command = run.call_args.args[0]
        self.assertEqual(command[-6:], ["/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "/usr/bin/python3", "-I", "-"])
        self.assertIn("ConnectionAttempts=1", command)
        self.assertIn("PasswordAuthentication=no", command)
        self.assertEqual(run.call_args.kwargs["input"], MODULE.REMOTE_PROGRAM.encode("ascii"))
        self.assertEqual(result["diagnostic"], "PASS")

    def test_invalid_change_rejects_before_helper_or_network(self) -> None:
        """未知 ChangeId 必须在加载身份辅助模块和联网前失败。"""
        arguments = [str(SCRIPT_PATH), "--change-id", "INVALID", "--known-hosts", "missing", "--identity-file", "missing", "--identity-public-file", "missing"]
        with (
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(MODULE, "load_staging_helper") as helper,
            mock.patch.object(MODULE, "run_transport_diagnostic") as remote,
            mock.patch("builtins.print") as output,
        ):
            self.assertEqual(MODULE.main(), 2)
        helper.assert_not_called()
        remote.assert_not_called()
        output.assert_called_once_with("G8_TEST_READONLY_TRANSPORT_DIAG=FAILED reason=invalid_request")

    def test_cli_local_check_and_formal_output(self) -> None:
        """本地检查不联网；正式模式只输出低敏固定字段。"""
        arguments = [str(SCRIPT_PATH), "--change-id", MODULE.CHANGE_ID, "--known-hosts", "/fixed/known_hosts", "--identity-file", "/fixed/key", "--identity-public-file", "/fixed/key.pub"]
        helper = SimpleNamespace(
            validate_known_hosts=mock.Mock(),
            validate_identity_file=mock.Mock(),
            validate_identity_pair=mock.Mock(),
        )
        with (
            mock.patch.object(sys, "argv", arguments + ["--local-check"]),
            mock.patch.object(MODULE, "load_staging_helper", return_value=helper),
            mock.patch.object(MODULE, "run_transport_diagnostic") as remote,
            mock.patch("builtins.print") as output,
        ):
            self.assertEqual(MODULE.main(), 0)
        remote.assert_not_called()
        output.assert_called_once_with("G8_TEST_READONLY_TRANSPORT_DIAG_LOCAL_CHECK=PASS")

        evidence = MODULE.classify_result(subprocess.CompletedProcess([], 0, MODULE.REMOTE_MARKER, b"notice\n"))
        with (
            mock.patch.object(sys, "argv", arguments),
            mock.patch.object(MODULE, "load_staging_helper", return_value=helper),
            mock.patch.object(MODULE, "run_transport_diagnostic", return_value=evidence) as remote,
            mock.patch("builtins.print") as output,
        ):
            self.assertEqual(MODULE.main(), 3)
        remote.assert_called_once()
        output.assert_any_call("G8_TEST_READONLY_TRANSPORT_DIAG=BLOCKED")
        output.assert_any_call("stderr_state=PRESENT")
        self.assertFalse(any("notice" in str(call) for call in output.call_args_list))


if __name__ == "__main__":
    unittest.main()
