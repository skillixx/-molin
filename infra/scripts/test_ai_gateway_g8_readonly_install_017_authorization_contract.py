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

    def test_state_waits_for_user_approval_and_remote_execution_is_disabled(self) -> None:
        self.assertIn("`PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED`", self.document)
        self.assertFalse(self.generator.CHANGE_ID_CONSUMED)
        self.assertFalse(self.generator.REMOTE_EXECUTION_AUTHORIZED)
        self.assertIn("当前仍禁止运行生成命令中的 SSH、交互 sudo 或安装段", self.document)
        self.assertIn("PR #384、精确 HEAD CI、独立代码安全/QA/产品复评", self.document)
        self.assertIn("31791430839", self.document)
        self.assertIn("e2a7e4f89c4115b3e32dc27292b0bc11d7d09a57", self.document)
        self.assertIn("ee947fd61919215500ef516488d56e01ad2ea72d", self.document)
        self.assertIn("纯 .NET 流式 SHA-256", self.document)
        command = self.generator.build_command(self.generator.read_frozen_installer())
        self.assertNotIn("Get-FileHash", command)
        self.assertIn("$sshKeygen -y -P '' -f $identity", command)
        self.assertIn("LogLevel=QUIET", command)
        self.assertIn("G8_TEST_READONLY_ACCESS_017_LOCAL_GATE=FAILED reason=local_gate_failed", command)
        self.assertIn("017 仍未消费", self.document)
        for blob in (
            "429b73bb7b5487d6539e1c604ef9410b34c3b0c1",
            "74a5c63d18c001154a36c1c22003bab433855c36",
            "4122ce7915acd71e917326f9237be16c8d07fd69",
            "ed9711c9ac7cdf5d8bb3e87a8a428ce8fe31d14f",
        ):
            self.assertIn(blob, self.document)

    def test_frozen_file_sizes_and_hashes_match_document(self) -> None:
        expected = {
            INSTALLER_PATH: (10977, "4deb5a26c27e83a2afe766dd815e4b611b5bc0c3c19eed9afb1bfe0e1d0b1188"),
            GENERATOR_PATH: (16696, "b9b552a71118560e5a2d18789ac9a1bc3c312fd80666b50d318cc08994fac669"),
            REPO_ROOT / "infra/scripts/test_g8_test_readonly_access_install_017.py": (
                18254,
                "90ed63db0d0caacd38ecc3f292ea393aeae9575cc9aa8be586da5eaa722dbc34",
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
        self.assertEqual(len(command), 25862)
        self.assertEqual(hashlib.sha256(command).hexdigest(), "6acc63972cb779eea18df49dcaec271c7d50223000d96f2a1c1d57364d4cc98e")
        self.assertIn("| 25862 | `6acc63972cb779eea18df49dcaec271c7d50223000d96f2a1c1d57364d4cc98e` |", self.document)

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
