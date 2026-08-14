#!/usr/bin/env python3
"""校验 016 授权清单、冻结文件和生成命令保持同一份工程事实。"""

import hashlib
import importlib.util
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
DOC_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-install-authorization-20260814-016.md"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-016-command.py"
INSTALLER_PATH = REPO_ROOT / "infra/scripts/g8-test-readonly-access-install-016.sh"


def digest(path: Path) -> str:
    """读取仓库普通文件并计算 SHA-256。"""
    return hashlib.sha256(path.read_bytes()).hexdigest()


def load_generator():
    """加载生成器以复核内存命令，不执行 main 或任何外部动作。"""
    specification = importlib.util.spec_from_file_location("g8_install_016_contract", GENERATOR_PATH)
    module = importlib.util.module_from_spec(specification)
    assert specification and specification.loader
    specification.loader.exec_module(module)
    return module


class TestG8ReadonlyInstall016AuthorizationContract(unittest.TestCase):
    def setUp(self) -> None:
        self.document = DOC_PATH.read_text(encoding="utf-8")
        self.generator = load_generator()

    def test_state_waits_for_user_approval_and_remote_execution_is_disabled(self) -> None:
        self.assertIn("`PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED`", self.document)
        self.assertFalse(self.generator.CHANGE_ID_CONSUMED)
        self.assertFalse(self.generator.REMOTE_EXECUTION_AUTHORIZED)
        self.assertIn("当前仍禁止运行生成命令中的 SSH、交互 sudo 或安装段", self.document)
        self.assertIn("PR：`#381`", self.document)
        self.assertIn("2f407cbf3a9c5fea987eeb2f82ebb41630db9e35", self.document)
        self.assertIn("合并后 Git blob", self.document)
        self.assertIn("016 仍未消费", self.document)

    def test_frozen_file_sizes_and_hashes_match_document(self) -> None:
        expected = {
            INSTALLER_PATH: (9465, "dee24046f11de7ba12994b3c93a68c28b5505f73b9dc6085a025f4ea790be85c"),
            GENERATOR_PATH: (15805, "a1d96f8cc3d7abc1fa2ea04ab198133e2f60281d4664af83c93e378ac80dedbd"),
            REPO_ROOT / "infra/scripts/test_g8_test_readonly_access_install_016.py": (
                14481,
                "9427886d06e8adc4577e838839f1d1890d29880b6ab1d829aadcea9fb6d213cf",
            ),
            REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_016_command.py": (
                13920,
                "bb81a134882c0c6bad2b2531137877b05d41e33cc3cf0402cc2759867be6d226",
            ),
        }
        for path, (size, sha256) in expected.items():
            self.assertEqual(path.stat().st_size, size, path)
            self.assertEqual(digest(path), sha256, path)
            self.assertIn(f"| {size} | `{sha256}` |", self.document)

    def test_generated_command_hash_matches_document(self) -> None:
        installer = self.generator.read_frozen_installer()
        command = self.generator.build_command(installer).encode("utf-8")
        self.assertEqual(len(command), 22967)
        self.assertEqual(hashlib.sha256(command).hexdigest(), "0173d043baa4d60a96659a77a8387f8d1de1a8fc9b77928f0abdf9d2793008fb")
        self.assertIn("| 22967 | `0173d043baa4d60a96659a77a8387f8d1de1a8fc9b77928f0abdf9d2793008fb` |", self.document)

    def test_scope_and_stop_conditions_are_explicit(self) -> None:
        for required in (
            "SSH 交互会话",
            "连接重试 0",
            "SFTP、SCP、下载",
            "业务请求、上游请求和费用固定为 `0 / 0 / 0 CNY`",
            "root-only 016 副本作为低敏执行证据保留",
            "其后续清理均须新 ChangeId 和独立授权",
            "016 成功只证明最小只读入口安装及 self-test 通过",
        ):
            self.assertIn(required, self.document)


if __name__ == "__main__":
    unittest.main()
