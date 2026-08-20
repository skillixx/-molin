#!/usr/bin/env python3
"""验证已消费的 020 生成入口不会解析历史参数或触达外部进程。"""

import ast
import subprocess
import sys
from pathlib import Path
import unittest


SCRIPT_PATH = Path(__file__).with_name("prepare-ai-gateway-g8-test-readonly-runtime-audit-020-command.py")


class ConsumedReadonlyRuntimeAudit020CommandTests(unittest.TestCase):
    def test_every_invocation_returns_exact_consumed_status(self) -> None:
        """自检、历史 ChangeId 和输出参数都只能得到同一固定墓碑结果。"""
        invocations = (
            (),
            ("--self-test",),
            ("--change-id=historical", "--output-file=forbidden"),
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
                    (2, "G8_TEST_READONLY_RUNTIME_AUDIT_020_COMMAND=FAILED reason=change_id_consumed\n", ""),
                )

    def test_tombstone_has_no_import_or_execution_capability(self) -> None:
        """墓碑源码不得保留文件、参数、SSH 或 Docker 能力。"""
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        tree = ast.parse(source)
        self.assertFalse(any(isinstance(node, (ast.Import, ast.ImportFrom)) for node in ast.walk(tree)))
        for forbidden in ("argparse", "subprocess", "Path(", "ssh", "docker", "8.130.9.163"):
            self.assertNotIn(forbidden, source.lower())


if __name__ == "__main__":
    unittest.main()
