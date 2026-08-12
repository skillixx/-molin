#!/usr/bin/env python3
"""验证 G8 测试服主机身份低敏诊断 007 的三态、单次调用和失败关闭。"""

import contextlib
import hashlib
import importlib.util
import io
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("run-ai-gateway-g8-test-host-identity-diagnostic.py")


def load_module():
    """从精确文件加载候选，避免通过 PATH 或可替换模块名寻找实现。"""
    spec = importlib.util.spec_from_file_location("g8_host_identity_diagnostic", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("module_load_failed")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


MODULE = load_module()


class TestHostIdentityDiagnostic(unittest.TestCase):
    @staticmethod
    def stream_result(content: bytes) -> dict[str, object]:
        """构造与真实有界采集器一致的结果，断言对象仍是生产解析行为。"""
        return {
            "captured": content[: MODULE.MAX_CAPTURE_BYTES + 1],
            "bytes": len(content),
            "exceeded": len(content) > MODULE.MAX_CAPTURE_BYTES,
            "error": False,
        }

    def run_remote_fixture(self, content: bytes | None) -> subprocess.CompletedProcess[bytes]:
        """在临时目录执行真实远端程序，只把外部 SSH 替换为本地隔离解释器。"""
        with tempfile.TemporaryDirectory(prefix="g8-host-id-") as directory:
            machine_id_path = Path(directory) / "machine-id"
            if content is not None:
                machine_id_path.write_bytes(content)
            approved = hashlib.sha256(b"approved\n").hexdigest()
            program = MODULE.build_remote_program(str(machine_id_path), approved)
            return subprocess.run(
                [sys.executable, "-I", "-"],
                input=program.encode("ascii"),
                capture_output=True,
                check=False,
            )

    def test_interpreter_and_self_test_are_fail_closed(self) -> None:
        """普通解释器必须拒绝，隔离解释器自检必须通过。"""
        ordinary = subprocess.run(
            [sys.executable, str(SCRIPT_PATH), "--self-test"], capture_output=True, text=True, check=False
        )
        self.assertEqual(ordinary.returncode, 2)
        self.assertEqual(
            ordinary.stdout.strip(),
            "G8_TEST_READONLY_HOST_IDENTITY_DIAG=FAILED reason=isolated_python_required",
        )
        isolated = subprocess.run(
            [sys.executable, "-I", str(SCRIPT_PATH), "--self-test"],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(isolated.returncode, 0, isolated.stderr)
        self.assertEqual(isolated.stdout.strip(), "G8_TEST_READONLY_HOST_IDENTITY_DIAG_SELF_TEST=PASS")

    def test_remote_program_returns_three_states_without_identifier_or_digest(self) -> None:
        """匹配、漂移和不可读必须形成三态，且不得泄漏文件正文或摘要。"""
        cases = (
            (b"approved\n", "READABLE_MATCH"),
            (b"changed\n", "READABLE_MISMATCH"),
            (None, "UNREADABLE"),
        )
        for content, expected in cases:
            with self.subTest(expected=expected):
                completed = self.run_remote_fixture(content)
                self.assertEqual(completed.returncode, 0, completed.stderr)
                self.assertEqual(MODULE.parse_remote_output(completed.stdout), expected)
                output = completed.stdout.decode("ascii")
                self.assertNotIn("approved", output)
                self.assertNotIn("changed", output)
                self.assertNotRegex(output, r"(?<![0-9a-f])[0-9a-f]{64}(?![0-9a-f])")

    def test_remote_program_treats_empty_and_oversized_files_as_unreadable(self) -> None:
        """空文件和超过 4096 字节的文件不能被误判为可读匹配。"""
        for content in (b"", b"X" * 4097):
            with self.subTest(size=len(content)):
                completed = self.run_remote_fixture(content)
                self.assertEqual(completed.returncode, 0, completed.stderr)
                self.assertEqual(MODULE.parse_remote_output(completed.stdout), "UNREADABLE")

    def test_parser_rejects_wrong_missing_duplicate_extra_and_unknown_values(self) -> None:
        """精确三键、ChangeId 和状态枚举任一漂移都必须失败关闭。"""
        valid = (
            f"DIAGNOSTIC_CHANGE_ID={MODULE.CHANGE_ID}\n"
            f"TARGET_CHANGE_ID={MODULE.TARGET_CHANGE_ID}\n"
            "MACHINE_ID_STATE=READABLE_MATCH\n"
        ).encode("ascii")
        invalid = (
            valid.replace(MODULE.CHANGE_ID.encode("ascii"), b"WRONG", 1),
            valid.replace(MODULE.TARGET_CHANGE_ID.encode("ascii"), b"WRONG", 1),
            valid.replace(b"MACHINE_ID_STATE=READABLE_MATCH\n", b""),
            valid + b"EXTRA=1\n",
            valid + b"MACHINE_ID_STATE=READABLE_MATCH\n",
            valid.replace(b"READABLE_MATCH", b"UNKNOWN"),
            valid[:-1],
            valid + b"\xff",
        )
        for payload in invalid:
            with self.subTest(payload=payload[-32:]):
                with self.assertRaises(MODULE.DiagnosticError):
                    MODULE.parse_remote_output(payload)

    def test_remote_call_is_exactly_once_with_fixed_ssh(self) -> None:
        """正式路径只能执行一次固定 OpenSSH，并通过 stdin 发送远端程序。"""
        helper = SimpleNamespace(
            TARGET_PORT="10003",
            TARGET="pc@8.130.9.163",
            fixed_ssh_executable=lambda: Path("/usr/bin/ssh"),
            fixed_ssh_environment=lambda: {"PATH": "/usr/bin:/bin"},
        )
        stdout = (
            f"DIAGNOSTIC_CHANGE_ID={MODULE.CHANGE_ID}\n"
            f"TARGET_CHANGE_ID={MODULE.TARGET_CHANGE_ID}\n"
            "MACHINE_ID_STATE=READABLE_MATCH\n"
        ).encode("ascii")
        stdout_result = self.stream_result(stdout)
        stderr_result = self.stream_result(b"")
        with mock.patch.object(MODULE, "run_bounded_process", return_value=(0, stdout_result, stderr_result)) as run:
            state = MODULE.run_once(helper, Path("/fixed/known_hosts"), Path("/fixed/key"))
        self.assertEqual(state, "READABLE_MATCH")
        self.assertEqual(run.call_count, 1)
        command = run.call_args.args[0]
        self.assertIn("ConnectionAttempts=1", command)
        self.assertIn("PasswordAuthentication=no", command)
        self.assertEqual(command[-6:], ["/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "/usr/bin/python3", "-I", "-"])
        self.assertEqual(run.call_args.args[1], {"PATH": "/usr/bin:/bin"})

    def test_transport_anomalies_do_not_become_machine_id_states(self) -> None:
        """非零退出、stderr、超限和管道异常均不得伪装成三态证据。"""
        valid_stdout = (
            f"DIAGNOSTIC_CHANGE_ID={MODULE.CHANGE_ID}\n"
            f"TARGET_CHANGE_ID={MODULE.TARGET_CHANGE_ID}\n"
            "MACHINE_ID_STATE=READABLE_MATCH\n"
        ).encode("ascii")
        cases = (
            (41, valid_stdout, b""),
            (0, valid_stdout, b"notice\n"),
            (0, b"X" * (MODULE.MAX_CAPTURE_BYTES + 1), b""),
        )
        helper = SimpleNamespace(
            TARGET_PORT="10003",
            TARGET="pc@8.130.9.163",
            fixed_ssh_executable=lambda: Path("/usr/bin/ssh"),
            fixed_ssh_environment=lambda: {"PATH": "/usr/bin:/bin"},
        )
        for returncode, stdout, stderr in cases:
            with self.subTest(returncode=returncode, stderr=bool(stderr), stdout_size=len(stdout)):
                result = (
                    returncode,
                    self.stream_result(stdout),
                    self.stream_result(stderr),
                )
                with mock.patch.object(MODULE, "run_bounded_process", return_value=result):
                    with self.assertRaises(MODULE.DiagnosticError):
                        MODULE.run_once(helper, Path("/fixed/known_hosts"), Path("/fixed/key"))

    def test_local_check_never_invokes_ssh(self) -> None:
        """离线本地检查仅核验身份材料，不得调用正式 SSH。"""
        helper = SimpleNamespace(
            validate_known_hosts=mock.Mock(),
            validate_identity_file=mock.Mock(),
            validate_identity_pair=mock.Mock(),
        )
        arguments = [
            str(SCRIPT_PATH),
            "--local-check",
            "--change-id",
            MODULE.CHANGE_ID,
            "--known-hosts",
            "/fixed/known_hosts",
            "--identity-file",
            "/fixed/key",
            "--identity-public-file",
            "/fixed/key.pub",
        ]
        output = io.StringIO()
        with (
            mock.patch.object(MODULE, "CHANGE_ID_CONSUMED", False),
            mock.patch.object(MODULE, "load_staging_helper", return_value=helper),
            mock.patch.object(MODULE, "run_once") as remote,
            mock.patch.object(sys, "argv", arguments),
            contextlib.redirect_stdout(output),
        ):
            self.assertEqual(MODULE.main(), 0)
        remote.assert_not_called()
        self.assertEqual(output.getvalue().strip(), "G8_TEST_READONLY_HOST_IDENTITY_DIAG_LOCAL_CHECK=PASS")

    def test_consumed_change_rejects_before_helper_identity_or_network(self) -> None:
        """消费门禁必须在加载 helper、读取身份文件或联网前拒绝重放。"""
        output = io.StringIO()
        with (
            mock.patch.object(MODULE, "CHANGE_ID_CONSUMED", True),
            mock.patch.object(MODULE, "load_staging_helper") as helper,
            mock.patch.object(MODULE, "run_once") as remote,
            mock.patch.object(sys, "argv", [str(SCRIPT_PATH)]),
            contextlib.redirect_stdout(output),
        ):
            self.assertEqual(MODULE.main(), 2)
        helper.assert_not_called()
        remote.assert_not_called()
        self.assertEqual(
            output.getvalue().strip(),
            "G8_TEST_READONLY_HOST_IDENTITY_DIAG=FAILED reason=change_id_consumed",
        )

    def test_helper_digest_and_target_contract_are_frozen(self) -> None:
        """004 helper 摘要或 SSH 目标漂移时，007 必须在联网前失败关闭。"""
        helper = MODULE.load_staging_helper()
        self.assertEqual(helper.TARGET, "pc@8.130.9.163")
        self.assertEqual(helper.TARGET_PORT, "10003")
        self.assertTrue(helper.CHANGE_ID_CONSUMED)


if __name__ == "__main__":
    unittest.main()
