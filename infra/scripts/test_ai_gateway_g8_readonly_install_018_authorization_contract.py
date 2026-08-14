#!/usr/bin/env python3
"""校验 018 工程候选的授权边界、低敏诊断和冻结摘要。"""

import hashlib
import importlib.util
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-install-authorization-20260814-018.md"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-018-command.py"
INSTALLER_PATH = REPO_ROOT / "infra/scripts/g8-test-readonly-access-install-018.sh"
CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-018"


def load_generator():
    """以隔离解释器加载生成器，只调用不联网的纯内存构造函数。"""
    specification = importlib.util.spec_from_file_location("g8_auth_018", GENERATOR_PATH)
    module = importlib.util.module_from_spec(specification)
    assert specification and specification.loader
    specification.loader.exec_module(module)
    return module


class TestG8ReadonlyInstall018AuthorizationContract(unittest.TestCase):
    def test_state_and_change_id_are_new_and_remote_disabled(self) -> None:
        """018 必须是未消费的新候选，但远端执行仍未获授权。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        module = load_generator()
        self.assertIn("PENDING_ENGINEERING_REVIEW / REMOTE_NOT_AUTHORIZED", document)
        self.assertIn(CHANGE_ID, document)
        self.assertIn("017 已按", document)
        self.assertIn("永久消费并墓碑化", document)
        self.assertFalse(module.CHANGE_ID_CONSUMED)
        self.assertFalse(module.REMOTE_EXECUTION_AUTHORIZED)
        self.assertEqual(module.CHANGE_ID, CHANGE_ID)

    def test_low_sensitive_diagnostic_contract_is_complete(self) -> None:
        """清单与命令必须同时冻结六类原因和两个 SSH 边界标志。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        command = load_generator().build_command(INSTALLER_PATH.read_bytes())
        for reason in (
            "trusted_windows_path_failed",
            "material_evidence_failed",
            "known_hosts_failed",
            "identity_pair_failed",
            "material_drift_failed",
            "ssh_session_failed",
        ):
            self.assertIn(reason, document)
            self.assertIn(reason, command)
        self.assertEqual(command.count("G8_TEST_READONLY_ACCESS_018_PRE_SSH_GATE=PASS"), 1)
        self.assertEqual(command.count("G8_TEST_READONLY_ACCESS_018_SSH_ATTEMPTED=YES"), 1)
        self.assertEqual(command.count("pc@"), 1)
        self.assertNotIn("Get-FileHash", command)

    def test_four_files_match_frozen_size_hash_blob_and_lf(self) -> None:
        """四个工程文件必须逐字节匹配授权清单，且禁止 CRLF 漂移。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        expected = {
            INSTALLER_PATH: (10977, "3232f3265da00d0a8f531798c32917bc77efd4725c30b4c9a99022d91484de85", "635002eac75a1300cb69df57cfa1006288092cae"),
            GENERATOR_PATH: (19006, "dc5037b22555c500e152985edf231da4e44931ec4470bb645dd254a9d6e44db9", "4530158756d75712f5e01f36d209660df853e622"),
            REPO_ROOT / "infra/scripts/test_g8_test_readonly_access_install_018.py": (18254, "a7575710f402aa26b4f6a37fc9bc499c6dd0f982b34ef7dd42f3e4207d1f95d6", "e6688b0e1fff300cd9b94ecd41f267b78ee9f237"),
            REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_018_command.py": (33386, "2519059d48b7dd0a9273476cb033d23d3aaa546b8991dccccef90f7c6baa5fb0", "fc202a28cce7484086a96966da6e4f00b15582c8"),
        }
        for path, (size, sha256, blob) in expected.items():
            content = path.read_bytes()
            actual_blob = hashlib.sha1(b"blob " + str(len(content)).encode("ascii") + b"\0" + content).hexdigest()
            self.assertEqual(len(content), size, path)
            self.assertEqual(hashlib.sha256(content).hexdigest(), sha256, path)
            self.assertEqual(actual_blob, blob, path)
            self.assertEqual(content.count(b"\r\n"), 0, path)
            self.assertIn(f"| {size} | `{sha256}` | `{blob}` / CRLF=0 |", document)

    def test_in_memory_command_matches_frozen_summary(self) -> None:
        """双段命令只在内存重建并核对摘要，不写出或执行。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        command = load_generator().build_command(INSTALLER_PATH.read_bytes()).encode("utf-8")
        self.assertEqual(len(command), 26932)
        self.assertEqual(hashlib.sha256(command).hexdigest(), "7cf503dd0a32a43fa716680b0287838a5d0b8d7a2bb31b15c39195698da09500")
        self.assertIn("| 26932 | `7cf503dd0a32a43fa716680b0287838a5d0b8d7a2bb31b15c39195698da09500` | 不写盘 |", document)


if __name__ == "__main__":
    unittest.main()
