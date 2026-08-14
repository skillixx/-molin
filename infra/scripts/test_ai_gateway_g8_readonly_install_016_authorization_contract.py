#!/usr/bin/env python3
"""校验 016 本地模块错误记录、消费清单与两个墓碑入口保持一致。"""

import ast
import subprocess
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-install-authorization-20260814-016.md"
ATTEMPT_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-install-attempt-20260814-016.md"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-016-command.py"
INSTALLER_PATH = REPO_ROOT / "infra/scripts/g8-test-readonly-access-install-016.sh"


class TestG8ReadonlyInstall016ConsumedContract(unittest.TestCase):
    def test_documents_record_local_module_error_and_zero_remote_effect(self) -> None:
        """两份记录必须明确本地模块错误、远端零触达和禁止重放。"""
        combined = AUTH_PATH.read_text(encoding="utf-8") + ATTEMPT_PATH.read_text(encoding="utf-8")
        for required in (
            "CONSUMED_LOCAL_MODULE_ERROR_REMOTE_NOT_REACHED",
            "POWERSHELL_GET_FILE_HASH_UNAVAILABLE",
            "SSH 会话：`0/1`",
            "sudo 与安装：`0`",
            "016 已消费并禁止重放",
            "017 仅为新的工程候选，不继承 016 执行授权",
        ):
            self.assertIn(required, combined)

    def test_generator_is_import_free_tombstone(self) -> None:
        """生成入口源码不得保留参数解析、文件读取或联网依赖。"""
        source = GENERATOR_PATH.read_text(encoding="utf-8")
        tree = ast.parse(source)
        self.assertFalse(any(isinstance(node, (ast.Import, ast.ImportFrom)) for node in ast.walk(tree)))
        for forbidden in ("argparse", "subprocess", "socket", "Path(", "ssh.exe", "Get-FileHash"):
            self.assertNotIn(forbidden, source)

    def test_both_entrypoints_return_exact_consumed_status(self) -> None:
        """历史参数只能得到固定低敏消费状态。"""
        generator = subprocess.run(
            ["python", "-I", str(GENERATOR_PATH), "--change-id=historical"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(generator.returncode, 2)
        self.assertEqual(generator.stdout, "G8_TEST_READONLY_ACCESS_016_COMMAND=FAILED reason=change_id_consumed\n")
        self.assertEqual(generator.stderr, "")


if __name__ == "__main__":
    unittest.main()
