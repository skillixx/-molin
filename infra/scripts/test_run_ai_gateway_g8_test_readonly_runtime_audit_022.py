#!/usr/bin/env python3
"""通过公开 CLI 验证 022 固定启动器的离线失败关闭边界。"""

from pathlib import Path
import importlib.util
import subprocess
import sys
import unittest


RUNNER_PATH = Path(__file__).with_name("run-ai-gateway-g8-test-readonly-runtime-audit-022.py")
CHANGE_ID = "CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-022"


def load_runner():
    """从固定同目录路径加载启动器，便于验证不触发子进程的纯函数门禁。"""
    specification = importlib.util.spec_from_file_location("g8_runtime_audit_022_runner", RUNNER_PATH)
    if specification is None or specification.loader is None:
        raise RuntimeError("runner_unavailable")
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
    return module


class TestG8ReadonlyRuntimeAudit022Runner(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        """只加载本地模块，不运行正式入口或创建网络连接。"""
        cls.runner = load_runner()

    def test_self_test_proves_fixed_launcher_without_network(self) -> None:
        """自检必须从固定入口完成且不得要求真实 SSH、Docker 或身份材料。"""
        result = subprocess.run(
            [sys.executable, "-I", str(RUNNER_PATH), "--self-test"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
            timeout=30,
        )
        self.assertEqual(
            (result.returncode, result.stdout, result.stderr),
            (0, "G8_TEST_READONLY_RUNTIME_AUDIT_022_RUNNER_SELF_TEST=PASS\n", ""),
        )

    def test_formal_mode_fails_before_materials_without_explicit_execution_flag(self) -> None:
        """只有 ChangeId 不得读取工程对象、生成命令或启动子进程。"""
        result = subprocess.run(
            [sys.executable, "-I", str(RUNNER_PATH), f"--change-id={CHANGE_ID}"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
            timeout=15,
        )
        self.assertEqual(
            (result.returncode, result.stdout, result.stderr),
            (2, "G8_TEST_READONLY_RUNTIME_AUDIT_022_RUNNER=FAILED reason=invalid_request\n", ""),
        )

    def test_missing_engineering_object_is_low_sensitive_and_never_attempts_powershell(self) -> None:
        """工程对象不可用时必须给出固定阶段原因，且不得进入 PowerShell。"""
        result = subprocess.run(
            [
                sys.executable,
                "-I",
                str(RUNNER_PATH),
                f"--change-id={CHANGE_ID}",
                "--engineering-merge=0000000000000000000000000000000000000000",
                "--expected-command-size=1",
                "--expected-command-sha256=0000000000000000000000000000000000000000000000000000000000000000",
                "--execute-authorized",
            ],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
            timeout=15,
        )
        self.assertEqual(
            (result.returncode, result.stdout, result.stderr),
            (2, "G8_TEST_READONLY_RUNTIME_AUDIT_022_RUNNER=FAILED reason=engineering_material_unavailable\n", ""),
        )
        self.assertNotIn("POWERSHELL_ATTEMPTED", result.stdout)

    def test_feature_head_is_rejected_before_powershell_as_non_merge(self) -> None:
        """普通工程提交即使三份 blob 一致，也不能冒充授权绑定的 merge commit。"""
        repository = RUNNER_PATH.resolve().parents[2]
        resolved = subprocess.run(
            ["git", "rev-list", "--no-merges", "-n", "1", "HEAD"],
            cwd=repository,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
            timeout=15,
        )
        if resolved.returncode != 0:
            self.skipTest("linked worktree 的外部 Git 对象库未挂载")
        head = resolved.stdout.strip()
        with self.assertRaises(self.runner.RunnerFailure) as caught:
            self.runner.verify_engineering_merge(repository, head)
        self.assertEqual(caught.exception.reason, "engineering_material_not_merge")

    def test_child_output_uses_exact_low_sensitive_marker_allowlist(self) -> None:
        """相似前缀、路径和伪造原因都不得借阶段标志通配规则透传。"""
        required = ("api_health",)
        self.assertTrue(self.runner.allowed_child_line("G8_TEST_READONLY_ACCESS_022_PRE_SSH_GATE=PASS", required))
        self.assertTrue(self.runner.allowed_child_line("G8_TEST_READONLY_RUNTIME_AUDIT_022=PREFLIGHT_PASS", required))
        self.assertTrue(self.runner.allowed_child_line("G8_TEST_READONLY_RUNTIME_AUDIT_022=COLLECTION_PASS", required))
        self.assertTrue(
            self.runner.allowed_child_line(
                "G8_TEST_READONLY_RUNTIME_AUDIT_022=FAILED reason=audit_evidence_failed", required
            )
        )
        self.assertTrue(self.runner.allowed_child_line("api_health=PASS", required))
        for reason in (
            "receipt_directory_unavailable",
            "receipt_preoccupied",
            "receipt_write_failed",
            "receipt_flush_failed",
        ):
            self.assertTrue(
                self.runner.allowed_child_line(
                    f"G8_TEST_READONLY_ACCESS_022_HOST_RESULT=FAILED reason={reason} exit_code=2",
                    required,
                )
            )
        self.assertFalse(
            self.runner.allowed_child_line(
                "G8_TEST_READONLY_ACCESS_022_HOST_RESULT=FAILED reason=receipt_unavailable exit_code=2",
                required,
            )
        )
        self.assertFalse(self.runner.allowed_child_line("G8_TEST_READONLY_ACCESS_022_SECRET=leak", required))
        self.assertFalse(self.runner.allowed_child_line("G8_TEST_READONLY_RUNTIME_AUDIT_022=FAILED path=C:\\secret", required))


if __name__ == "__main__":
    unittest.main()
