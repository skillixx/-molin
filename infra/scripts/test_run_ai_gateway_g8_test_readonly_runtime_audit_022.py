#!/usr/bin/env python3
"""验证已消费的 022 固定启动入口不会解析历史参数或启动任何子进程。"""

import ast
import subprocess
import sys
from pathlib import Path
import unittest


SCRIPT_PATH = Path(__file__).with_name("run-ai-gateway-g8-test-readonly-runtime-audit-022.py")


class ConsumedReadonlyRuntimeAudit022RunnerTests(unittest.TestCase):
    def test_every_invocation_returns_exact_consumed_status(self) -> None:
        """自检和完整历史授权参数都只能得到同一固定墓碑结果。"""
        invocations = (
            (),
            ("--self-test",),
            (
                "--change-id=CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-022",
                "--engineering-merge=84ae5b0ad87958ee63fbfa709c4f164baca39a1b",
                "--expected-command-size=34027",
                "--expected-command-sha256=d649d2f896a224f3c1063b4bbb49953de1a7330d36b1db0cbaaf2bbfdea2e9e9",
                "--execute-authorized",
            ),
        )
        for arguments in invocations:
            with self.subTest(arguments=arguments):
                result = subprocess.run(
                    [sys.executable, "-I", str(SCRIPT_PATH), *arguments],
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    check=False,
                    timeout=15,
                )
                self.assertEqual(
                    (result.returncode, result.stdout, result.stderr),
                    (2, "G8_TEST_READONLY_RUNTIME_AUDIT_022_RUNNER=FAILED reason=change_id_consumed\n", ""),
                )

    def test_tombstone_has_no_import_or_execution_capability(self) -> None:
        """墓碑源码不得保留参数、Git、PowerShell、SSH 或 Docker 能力。"""
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        tree = ast.parse(source)
        self.assertFalse(any(isinstance(node, (ast.Import, ast.ImportFrom)) for node in ast.walk(tree)))
        for forbidden in ("argparse", "subprocess", "path(", "git", "powershell", "ssh", "docker", "8.130.9.163"):
            self.assertNotIn(forbidden, source.lower())


if __name__ == "__main__":
    unittest.main()
