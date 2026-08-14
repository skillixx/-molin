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
        self.assertIn("尚未创建 PR、运行精确 HEAD CI 或完成独立复评", self.document)
        self.assertIn("纯 .NET 流式 SHA-256", self.document)
        self.assertNotIn("Get-FileHash", self.generator.build_command(self.generator.read_frozen_installer()))
        self.assertIn("017 仍未消费", self.document)

    def test_frozen_file_sizes_and_hashes_match_document(self) -> None:
        expected = {
            INSTALLER_PATH: (9465, "9e5123ca798f8198b8e55fe7ba155b781e4f657b745df0fb401e3b309e348976"),
            GENERATOR_PATH: (16230, "be8d271ae3a103284453b83057e4091e1c12842b4f3c174601041a78b9924717"),
            REPO_ROOT / "infra/scripts/test_g8_test_readonly_access_install_017.py": (
                14481,
                "04f21997d43f0b714023a9a7dca8957d9765e49e374066d0cf6376b8f7398fc3",
            ),
            REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_017_command.py": (
                18413,
                "ec07679bcde8bb84c3bf5352f58253cade1f331bb16893eb3e222b2dd12eed62",
            ),
        }
        for path, (size, sha256) in expected.items():
            self.assertEqual(path.stat().st_size, size, path)
            self.assertEqual(digest(path), sha256, path)
            self.assertIn(f"| {size} | `{sha256}` |", self.document)

    def test_generated_command_hash_matches_document(self) -> None:
        installer = self.generator.read_frozen_installer()
        command = self.generator.build_command(installer).encode("utf-8")
        self.assertEqual(len(command), 23384)
        self.assertEqual(hashlib.sha256(command).hexdigest(), "b45c3001c88539a3b84fbdf99f85b5ea8c4db889e5e21bf9b015cdac5bc23f83")
        self.assertIn("| 23384 | `b45c3001c88539a3b84fbdf99f85b5ea8c4db889e5e21bf9b015cdac5bc23f83` |", self.document)

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
