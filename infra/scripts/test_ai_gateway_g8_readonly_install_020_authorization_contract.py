#!/usr/bin/env python3
"""校验 020 工程候选、永久 019 墓碑、冻结摘要与远端未授权边界。"""

import hashlib
import importlib.util
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-install-authorization-20260815-020.md"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-020-command.py"
INSTALLER_PATH = REPO_ROOT / "infra/scripts/g8-test-readonly-access-install-020.sh"
CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260815-020"


def load_generator():
    """只加载离线生成器纯函数，不生成文件、不连接网络。"""
    specification = importlib.util.spec_from_file_location("g8_command_020_contract", GENERATOR_PATH)
    if specification is None or specification.loader is None:
        raise RuntimeError("020 生成器无法加载")
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
    return module


class TestG8ReadonlyInstall020AuthorizationContract(unittest.TestCase):
    def test_document_states_engineering_only_and_remote_not_authorized(self) -> None:
        """工程合并授权不得被解释为 SSH、sudo、安装或运行态授权。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        for required in (
            "PENDING_ENGINEERING_REVIEW",
            "REMOTE_NOT_AUTHORIZED",
            CHANGE_ID,
            "019 永久墓碑",
            "固定可信用户目录耐久低敏回执",
            "精确已安装 / 完全未安装 / 部分或漂移",
            "不授权 SSH、sudo、安装器、post-check 或任何测试服操作",
            "`G8_SOFTWARE_CLOSED_LOOP` 尚未完成",
        ):
            self.assertIn(required, document)

    def test_generator_constants_keep_execution_disabled(self) -> None:
        """候选可生成不等于已获远端执行授权。"""
        module = load_generator()
        self.assertEqual(module.CHANGE_ID, CHANGE_ID)
        self.assertFalse(module.CHANGE_ID_CONSUMED)
        self.assertFalse(module.REMOTE_EXECUTION_AUTHORIZED)
        self.assertEqual(module.TRUSTED_PROFILE_RECEIPT, "__G8_TRUSTED_PROFILE_RECEIPT__")

    def test_project_status_documents_keep_020_remote_disabled(self) -> None:
        """项目入口文档必须同步声明 020 仍是远端未授权工程候选。"""
        for relative in (
            "README.md",
            "docs/ai-gateway-g8-acceptance.md",
            "docs/ai-gateway-g8-test-readonly-access-runbook.md",
            "docs/tools.md",
        ):
            document = (REPO_ROOT / relative).read_text(encoding="utf-8")
            self.assertIn(CHANGE_ID, document, relative)
            self.assertIn("PENDING_ENGINEERING_REVIEW / REMOTE_NOT_AUTHORIZED", document, relative)
            self.assertIn("不授权", document, relative)

    def test_frozen_files_and_command_match_document(self) -> None:
        """五个工程文件与固定回执模式生成的命令摘要必须可独立重算。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        paths = (
            INSTALLER_PATH,
            GENERATOR_PATH,
            REPO_ROOT / "infra/scripts/test_g8_test_readonly_access_install_020.py",
            REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_020_command.py",
            REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_020_security_controls.py",
        )
        for path in paths:
            content = path.read_bytes()
            digest = hashlib.sha256(content).hexdigest()
            self.assertNotIn(b"\r\n", content, path)
            self.assertIn(f"| `{path.relative_to(REPO_ROOT).as_posix()}` | {len(content)} | `{digest}` | CRLF=0 |", document)
        module = load_generator()
        command = module.build_command(
            INSTALLER_PATH.read_bytes(),
            receipt_path=module.TRUSTED_PROFILE_RECEIPT,
        ).encode("utf-8")
        self.assertIn(
            f"| 纯内存冻结命令 | {len(command)} | `{hashlib.sha256(command).hexdigest()}` | 不写盘 |",
            document,
        )

    def test_019_tombstones_remain_byte_exact(self) -> None:
        """020 不得恢复、修改或复用 019 历史入口。"""
        expected = {
            "infra/scripts/g8-test-readonly-access-install-019.sh": "368091106a2b09bcb6353e9030309820ce2a19776d2444016bb3df066a158f78",
            "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-019-command.py": "2fbd91d95b585b694cdebb9013925b38627a03feb00ca455ab61a1894694eaf9",
            "infra/scripts/test_g8_test_readonly_access_install_019.py": "7a6704b66fe6105b751da66bb2f4ca27e5890bab88c8a7aec5c9d6b0f67563d8",
            "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_019_command.py": "fe121f8925352cb7a75f43722a1ec0d480ec09a3dbcf3ad4a7aae8cd8e227594",
        }
        for relative, digest in expected.items():
            self.assertEqual(hashlib.sha256((REPO_ROOT / relative).read_bytes()).hexdigest(), digest, relative)


if __name__ == "__main__":
    unittest.main()
