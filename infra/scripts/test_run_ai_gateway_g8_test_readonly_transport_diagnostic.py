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
        for forbidden in ("import os", "import pwd", "open(", "subprocess", "remove(", "unlink(", "rmdir(", "sudo"):
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

        oversized = subprocess.CompletedProcess([], 0, b"X" * (MODULE.MAX_CAPTURE_BYTES + 1), b"")
        self.assertEqual(MODULE.classify_result(oversized)["diagnostic"], "OUTPUT_LIMIT_EXCEEDED")

    def test_remote_call_is_exactly_once_with_fixed_ssh(self) -> None:
        """正式诊断只能调用一次固定 SSH，远端程序只经 stdin 传递。"""
        helper = SimpleNamespace(
            TARGET_PORT="10003",
            TARGET="pc@8.130.9.163",
            fixed_ssh_executable=lambda: Path("/usr/bin/ssh"),
            fixed_ssh_environment=lambda: {"PATH": "/usr/bin:/bin"},
        )
        stdout_result = {
            "captured": MODULE.REMOTE_MARKER,
            "bytes": len(MODULE.REMOTE_MARKER),
            "lines": 1,
            "sha256": MODULE.sha256_or_none(MODULE.REMOTE_MARKER),
            "exceeded": False,
        }
        stderr_result = {"captured": b"", "bytes": 0, "lines": 0, "sha256": "NONE", "exceeded": False}
        with mock.patch.object(MODULE, "run_bounded_process", return_value=(0, stdout_result, stderr_result)) as run:
            result = MODULE.run_transport_diagnostic(helper, Path("/fixed/known_hosts"), Path("/fixed/key"))
        self.assertEqual(run.call_count, 1)
        command = run.call_args.args[0]
        self.assertEqual(command[-6:], ["/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "/usr/bin/python3", "-I", "-"])
        self.assertIn("ConnectionAttempts=1", command)
        self.assertIn("PasswordAuthentication=no", command)
        self.assertEqual(run.call_args.args[1], {"PATH": "/usr/bin:/bin"})
        self.assertEqual(result["diagnostic"], "PASS")

    def test_helper_digest_and_contract_are_frozen(self) -> None:
        """辅助脚本摘要或目标常量漂移时，必须在执行辅助代码前失败关闭。"""
        helper = MODULE.load_staging_helper()
        self.assertEqual(helper.TARGET, "pc@8.130.9.163")
        self.assertEqual(helper.TARGET_PORT, "10003")
        with mock.patch.object(MODULE, "STAGING_HELPER_SHA256", "0" * 64):
            with self.assertRaises(MODULE.DiagnosticError):
                MODULE.load_staging_helper()

    def test_bounded_process_streams_large_output_without_retaining_body(self) -> None:
        """异常子进程输出超过上限时，只保留固定前缀并继续流式计算完整摘要。"""
        payload_size = MODULE.MAX_CAPTURE_BYTES * 3
        command = [sys.executable, "-I", "-c", f"import sys;sys.stdout.buffer.write(b'X'*{payload_size})"]
        returncode, stdout_result, stderr_result = MODULE.run_bounded_process(command, {})
        self.assertEqual(returncode, 0)
        self.assertEqual(stdout_result["bytes"], payload_size)
        self.assertEqual(len(stdout_result["captured"]), MODULE.MAX_CAPTURE_BYTES + 1)
        self.assertTrue(stdout_result["exceeded"])
        self.assertEqual(stderr_result["bytes"], 0)
        evidence = MODULE.classify_stream_result(returncode, stdout_result, stderr_result)
        self.assertEqual(evidence["diagnostic"], "OUTPUT_LIMIT_EXCEEDED")

    def test_stream_read_error_is_collected_without_thread_traceback(self) -> None:
        """管道读取异常只能写入内部错误标志，不能从线程打印 traceback。"""
        result: dict[str, object] = {}
        stream = mock.Mock()
        stream.read.side_effect = OSError("SENSITIVE_PIPE_PATH")
        with mock.patch.object(sys, "stderr") as stderr:
            MODULE.collect_stream(stream, result)
        self.assertEqual(result, {"error": True})
        stderr.write.assert_not_called()

    def test_ssh_configuration_error_is_low_sensitivity(self) -> None:
        """固定 SSH 路径或环境解析失败时必须收敛为内部诊断异常。"""
        helper = SimpleNamespace(
            fixed_ssh_executable=mock.Mock(side_effect=RuntimeError("SENSITIVE_LOCAL_PATH")),
            fixed_ssh_environment=mock.Mock(),
        )
        with self.assertRaisesRegex(MODULE.DiagnosticError, "ssh_configuration_failed"):
            MODULE.run_transport_diagnostic(helper, Path("/fixed/known_hosts"), Path("/fixed/key"))

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
