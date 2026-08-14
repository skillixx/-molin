#!/usr/bin/env python3
"""校验 015 授权清单、冻结文件和生成命令保持同一份工程事实。"""

import hashlib
import importlib.util
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
DOC_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-install-authorization-20260814-015.md"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-015-command.py"
INSTALLER_PATH = REPO_ROOT / "infra/scripts/g8-test-readonly-access-install-015.sh"


def digest(path: Path) -> str:
    """读取仓库普通文件并计算 SHA-256。"""
    return hashlib.sha256(path.read_bytes()).hexdigest()


def load_generator():
    """加载生成器以复核内存命令，不执行 main 或任何外部动作。"""
    specification = importlib.util.spec_from_file_location("g8_install_015_contract", GENERATOR_PATH)
    module = importlib.util.module_from_spec(specification)
    assert specification and specification.loader
    specification.loader.exec_module(module)
    return module


class TestG8ReadonlyInstall015AuthorizationContract(unittest.TestCase):
    def setUp(self) -> None:
        self.document = DOC_PATH.read_text(encoding="utf-8")
        self.generator = load_generator()

    def test_state_is_pending_and_remote_execution_is_disabled(self) -> None:
        self.assertIn("`PENDING_ENGINEERING_REVIEW / REMOTE_NOT_AUTHORIZED`", self.document)
        self.assertFalse(self.generator.CHANGE_ID_CONSUMED)
        self.assertFalse(self.generator.REMOTE_EXECUTION_AUTHORIZED)
        self.assertIn("当前禁止运行生成命令中的 SSH、交互 sudo 或安装段", self.document)

    def test_frozen_file_sizes_and_hashes_match_document(self) -> None:
        expected = {
            INSTALLER_PATH: (9465, "ed2af4cbd7d102d120d9b2af59b0f60867c83eb79c655c01b45332455617829e"),
            GENERATOR_PATH: (15629, "5e1adc70bf5de967afa01400b7c1b358fff47649096d56c4c08d3f92ebc255a8"),
            REPO_ROOT / "infra/scripts/test_g8_test_readonly_access_install_015.py": (
                14481,
                "486e4626642814e70f90bf87e3a39cab2fa7de5fca5c4e1230fd6fcd4513ed67",
            ),
            REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_015_command.py": (
                11091,
                "666dd67ef5b38edaf18c2b5c8c51cda5c894b30fcaf2d11aa79c8981937fdb4a",
            ),
        }
        for path, (size, sha256) in expected.items():
            self.assertEqual(path.stat().st_size, size, path)
            self.assertEqual(digest(path), sha256, path)
            self.assertIn(f"| {size} | `{sha256}` |", self.document)

    def test_generated_command_hash_matches_document(self) -> None:
        installer = self.generator.read_frozen_installer()
        command = self.generator.build_command(installer).encode("utf-8")
        self.assertEqual(len(command), 22796)
        self.assertEqual(hashlib.sha256(command).hexdigest(), "fc6b095b5167c6cf65cd049b04a4033274a90a44011963014fcadcbf502b917a")
        self.assertIn("| 22796 | `fc6b095b5167c6cf65cd049b04a4033274a90a44011963014fcadcbf502b917a` |", self.document)

    def test_scope_and_stop_conditions_are_explicit(self) -> None:
        for required in (
            "SSH 交互会话",
            "连接重试 0",
            "SFTP、SCP、下载",
            "业务请求、上游请求和费用固定为 `0 / 0 / 0 CNY`",
            "root-only 015 副本作为低敏执行证据保留",
            "其后续清理均须新 ChangeId 和独立授权",
            "015 成功只证明最小只读入口安装及 self-test 通过",
        ):
            self.assertIn(required, self.document)


if __name__ == "__main__":
    unittest.main()
