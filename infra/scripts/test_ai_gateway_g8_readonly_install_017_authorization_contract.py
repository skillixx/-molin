#!/usr/bin/env python3
"""校验 017 授权清单、冻结文件和生成命令保持同一份工程事实。"""

import hashlib
import importlib.util
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
DOC_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-install-authorization-20260814-017.md"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-017-command.py"
INSTALLER_PATH = REPO_ROOT / "infra/scripts/g8-test-readonly-access-install-017.sh"


def digest(path: Path) -> str:
    """读取仓库普通文件并计算 SHA-256。"""
    return hashlib.sha256(path.read_bytes()).hexdigest()


def load_generator():
    """加载生成器以复核内存命令，不执行 main 或任何外部动作。"""
    specification = importlib.util.spec_from_file_location("g8_install_017_contract", GENERATOR_PATH)
    module = importlib.util.module_from_spec(specification)
    assert specification and specification.loader
    specification.loader.exec_module(module)
    return module


class TestG8ReadonlyInstall017AuthorizationContract(unittest.TestCase):
    def setUp(self) -> None:
        self.document = DOC_PATH.read_text(encoding="utf-8")
        self.generator = load_generator()

    def test_state_waits_for_engineering_review_and_remote_execution_is_disabled(self) -> None:
        self.assertIn("`PENDING_ENGINEERING_REVIEW / REMOTE_NOT_AUTHORIZED`", self.document)
        self.assertFalse(self.generator.CHANGE_ID_CONSUMED)
        self.assertFalse(self.generator.REMOTE_EXECUTION_AUTHORIZED)
        self.assertIn("当前仍禁止运行生成命令中的 SSH、交互 sudo 或安装段", self.document)
        self.assertIn("工程候选正在进行 PR、精确 HEAD CI 与独立复评", self.document)
        self.assertIn("纯 .NET 流式 SHA-256", self.document)
        command = self.generator.build_command(self.generator.read_frozen_installer())
        self.assertNotIn("Get-FileHash", command)
        self.assertIn("$sshKeygen -y -P '' -f $identity", command)
        self.assertIn("LogLevel=QUIET", command)
        self.assertIn("G8_TEST_READONLY_ACCESS_017_LOCAL_GATE=FAILED reason=local_gate_failed", command)
        self.assertIn("017 仍未消费", self.document)

    def test_frozen_file_sizes_and_hashes_match_document(self) -> None:
        expected = {
            INSTALLER_PATH: (10676, "ccdc81212ae29ca1fccec97f5c2b6e1b3480ea5615a56cd3b45910cc8d289cc9"),
            GENERATOR_PATH: (16696, "723b72b59804eb6a639713212d7665fd3b2fd5178a9119218887edb1fa712343"),
            REPO_ROOT / "infra/scripts/test_g8_test_readonly_access_install_017.py": (
                16185,
                "81436efa74549d3b06e7399df3bf814ca693c0a8c3c8ab71b908a49970493036",
            ),
            REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_017_command.py": (
                21493,
                "7b8cd85bdb5917dea6fdb0b86e0f055bb98946e09028ddd87c0bd456c906eb7d",
            ),
        }
        for path, (size, sha256) in expected.items():
            self.assertEqual(path.stat().st_size, size, path)
            self.assertEqual(digest(path), sha256, path)
            self.assertIn(f"| {size} | `{sha256}` |", self.document)

    def test_generated_command_hash_matches_document(self) -> None:
        installer = self.generator.read_frozen_installer()
        command = self.generator.build_command(installer).encode("utf-8")
        self.assertEqual(len(command), 25462)
        self.assertEqual(hashlib.sha256(command).hexdigest(), "91f703722d200df3d8f8fd1564c09b4842475b392abbc55f773d80bcb57f7fa2")
        self.assertIn("| 25462 | `91f703722d200df3d8f8fd1564c09b4842475b392abbc55f773d80bcb57f7fa2` |", self.document)

    def test_scope_and_stop_conditions_are_explicit(self) -> None:
        for required in (
            "SSH 交互会话",
            "连接重试 0",
            "SFTP、SCP、下载",
            "业务请求、上游请求和费用固定为 `0 / 0 / 0 CNY`",
            "root-only 017 副本作为低敏执行证据保留",
            "其后续清理均须新 ChangeId 和独立授权",
            "017 成功只证明最小只读入口安装及 self-test 通过",
        ):
            self.assertIn(required, self.document)


if __name__ == "__main__":
    unittest.main()
