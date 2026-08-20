#!/usr/bin/env python3
"""校验 019 失败关闭记录、历史冻结证据与两个墓碑入口保持一致。"""

import ast
import hashlib
import os
import subprocess
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-install-authorization-20260815-019.md"
ATTEMPT_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-install-attempt-20260815-019.md"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-019-command.py"
INSTALLER_PATH = REPO_ROOT / "infra/scripts/g8-test-readonly-access-install-019.sh"
HISTORICAL_MERGE = "70485d893fd86db00be4dbb9e324f9d4322d55b0"


class TestG8ReadonlyInstall019ConsumedContract(unittest.TestCase):
    def test_documents_record_restore_failure_and_unknown_execution_boundary(self) -> None:
        """记录必须保守区分唯一会话上限与不可恢复的实际执行位置。"""
        combined = AUTH_PATH.read_text(encoding="utf-8") + ATTEMPT_PATH.read_text(encoding="utf-8")
        for required in (
            "CONSUMED_POWERSHELL_PREFERENCE_RESTORE_FAILED_EXECUTION_REACHABILITY_UNKNOWN",
            "恢复 `$ErrorActionPreference` 时因保存值为 `Null` 失败",
            "SSH 启动与连接：`UNKNOWN / 最多 1`",
            "远端预检、sudo、安装器与 post-check：`UNKNOWN / 最多 1 / 最多 1 / 最多 1`",
            "019 按失败关闭规则消费并禁止重放",
            "业务请求、上游请求、费用：`0 / 0 / 0 CNY`",
            "019 不得再次授权、重试或重放",
            "31831396476",
            "752ca9d7705e9f6ba6d0652d6c0f34f580ce66ce",
        ):
            self.assertIn(required, combined)

    def test_generator_is_import_free_tombstone(self) -> None:
        """生成入口不得保留参数解析、文件读取或联网依赖。"""
        source = GENERATOR_PATH.read_text(encoding="utf-8")
        tree = ast.parse(source)
        self.assertFalse(any(isinstance(node, (ast.Import, ast.ImportFrom)) for node in ast.walk(tree)))
        for forbidden in ("argparse", "subprocess", "socket", "Path(", "ssh.exe", "ssh-keygen"):
            self.assertNotIn(forbidden, source)

    def test_both_entrypoints_return_exact_consumed_status(self) -> None:
        """任何历史参数只能得到固定低敏消费状态。"""
        generator = subprocess.run(
            ["python", "-I", str(GENERATOR_PATH), "--change-id=historical"],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(
            (generator.returncode, generator.stdout, generator.stderr),
            (2, "G8_TEST_READONLY_ACCESS_019_COMMAND=FAILED reason=change_id_consumed\n", ""),
        )
        bash = Path(r"C:\Program Files\Git\bin\bash.exe")
        executable = str(bash) if bash.exists() else "bash"
        installer = subprocess.run(
            [executable, str(INSTALLER_PATH), "historical"],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(
            (installer.returncode, installer.stdout, installer.stderr),
            (2, "G8_TEST_READONLY_ACCESS_INSTALL_019=FAILED reason=change_id_consumed\n", ""),
        )

    def test_consumed_tombstones_match_attempt_record(self) -> None:
        """当前四个墓碑文件必须与执行记录的大小、摘要和 blob 完全一致。"""
        document = ATTEMPT_PATH.read_text(encoding="utf-8")
        expected = {
            INSTALLER_PATH: (182, "368091106a2b09bcb6353e9030309820ce2a19776d2444016bb3df066a158f78", "db6b3babf107fff414ba513cb15a4cedb6c51b88"),
            GENERATOR_PATH: (425, "2fbd91d95b585b694cdebb9013925b38627a03feb00ca455ab61a1894694eaf9", "148d98a91cfc8f2328fba50aeb72fb0281b36903"),
            REPO_ROOT / "infra/scripts/test_g8_test_readonly_access_install_019.py": (1195, "7a6704b66fe6105b751da66bb2f4ca27e5890bab88c8a7aec5c9d6b0f67563d8", "50162acb6b028a69a6acb2dd4a669d6d49838979"),
            REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_019_command.py": (1463, "fe121f8925352cb7a75f43722a1ec0d480ec09a3dbcf3ad4a7aae8cd8e227594", "e048406b9a07c1e35896fc44c76690d93c1e088b"),
        }
        for path, (size, digest, blob) in expected.items():
            content = path.read_bytes()
            self.assertEqual((len(content), hashlib.sha256(content).hexdigest()), (size, digest), path)
            self.assertEqual(hashlib.sha1(b"blob " + str(size).encode("ascii") + b"\0" + content).hexdigest(), blob, path)
            self.assertIn(f"| {size} | `{digest}` | `{blob}` |", document)

    def test_historical_merge_blobs_still_match_019_freeze(self) -> None:
        """墓碑化后仍从工程合并对象复核四个历史候选摘要。"""
        if os.name != "nt" and (REPO_ROOT / ".git").is_file():
            self.skipTest("linked worktree 的外部 Git 对象库未挂载")
        expected = {
            "infra/scripts/g8-test-readonly-access-install-019.sh": (10977, "c1178bbc5b566357b5862484fab62dc9f267d8e341792eb8aa6871602e212935"),
            "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-019-command.py": (21450, "7f994bd1be28e4b9d56a7aad600765325e1385c9bb2eaa6e26a08c72af626556"),
            "infra/scripts/test_g8_test_readonly_access_install_019.py": (18254, "40b3997d0bcef8e122258a025485ee8bc2d751affb1f93dd049798712e1c3203"),
            "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_019_command.py": (36093, "255ab1f5be646d94dd88f5d2a2b531db132bf195f4fdbfc1d7c931381412698d"),
        }
        for path, (size, digest) in expected.items():
            content = subprocess.run(
                ["git", "-c", f"safe.directory={REPO_ROOT}", "show", f"{HISTORICAL_MERGE}:{path}"],
                cwd=REPO_ROOT,
                capture_output=True,
                check=True,
            ).stdout
            self.assertEqual((len(content), hashlib.sha256(content).hexdigest()), (size, digest), path)


if __name__ == "__main__":
    unittest.main()
