#!/usr/bin/env python3
"""校验 019 单会话工程候选的授权边界与冻结摘要。"""

import base64
import hashlib
import importlib.util
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-install-authorization-20260815-019.md"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-019-command.py"
INSTALLER_PATH = REPO_ROOT / "infra/scripts/g8-test-readonly-access-install-019.sh"
CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-019"
ENGINEERING_HEAD = "a62a3a4d271055aa563b147319a0eceab30f4821"
MERGE_COMMIT = "70485d893fd86db00be4dbb9e324f9d4322d55b0"
BASE_PARENT = "04ffc663f85c03efb995b35d06ac2b3a96b1e053"


def load_generator():
    """只加载生成器并调用纯内存函数，不读取身份材料或联网。"""
    specification = importlib.util.spec_from_file_location("g8_auth_019", GENERATOR_PATH)
    module = importlib.util.module_from_spec(specification)
    assert specification and specification.loader
    specification.loader.exec_module(module)
    return module


class TestG8ReadonlyInstall019AuthorizationContract(unittest.TestCase):
    def test_new_change_id_is_unconsumed_and_remote_disabled(self) -> None:
        """019 必须是未消费的新候选，且明确不继承 018 授权。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        module = load_generator()
        self.assertIn("PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED", document)
        self.assertIn("018 已按", document)
        self.assertIn("失败关闭、消费并墓碑化", document)
        self.assertEqual(module.CHANGE_ID, CHANGE_ID)
        self.assertFalse(module.CHANGE_ID_CONSUMED)
        self.assertFalse(module.REMOTE_EXECUTION_AUTHORIZED)

    def test_postmerge_archive_keeps_remote_execution_disabled(self) -> None:
        """合并证据必须精确归档，且不得把工程完成外推为远端授权。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        self.assertIn("PR：`#390`", document)
        self.assertIn("`31829691838`", document)
        self.assertIn(f"merge commit：`{MERGE_COMMIT}`", document)
        self.assertIn(f"`{BASE_PARENT}`、`{ENGINEERING_HEAD}`", document)
        self.assertIn("远端工程分支 `feature/backend-d-ai-gateway-g8-install-019-single-session` 已删除", document)
        self.assertIn("不构成 SSH、sudo、安装或测试服操作授权", document)
        self.assertIn("也不证明测试服已安装或 `G8_SOFTWARE_CLOSED_LOOP` 已完成", document)

    def test_single_tty_session_and_persistent_parent_are_frozen(self) -> None:
        """唯一 SSH 必须携带远端脚本，TTY 留给 sudo，父 PowerShell 不退出。"""
        command = load_generator().build_command(INSTALLER_PATH.read_bytes())
        payload = command.split("$remotePayload = '", 1)[1].split("'\n", 1)[0]
        remote = base64.b64decode(payload, validate=True).decode("utf-8")
        self.assertEqual(command.count("\n    & $ssh `"), 1)
        self.assertEqual(command.count("  -tt `"), 1)
        self.assertNotIn("# 第二步", command)
        self.assertNotIn("exit 2", command)
        self.assertIn("$global:LASTEXITCODE = $g8HostExitCode", command)
        self.assertIn("G8_TEST_READONLY_ACCESS_019_HOST_RESULT=FAILED exit_code=2", command)
        self.assertIn("/usr/bin/sudo -k -v", remote)
        self.assertIn("G8_TEST_READONLY_ACCESS_POSTCHECK_019=PASS", remote)

    def test_four_files_match_frozen_summary_and_lf(self) -> None:
        """四个工程文件必须逐字节匹配清单且保持 LF。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        expected = {
            INSTALLER_PATH: (10977, "c1178bbc5b566357b5862484fab62dc9f267d8e341792eb8aa6871602e212935", "dd550edf20aa913fa793754e6500604a95960f3a"),
            GENERATOR_PATH: (21450, "7f994bd1be28e4b9d56a7aad600765325e1385c9bb2eaa6e26a08c72af626556", "4605209a84d825301906f86bdce720d746c91cfd"),
            REPO_ROOT / "infra/scripts/test_g8_test_readonly_access_install_019.py": (18254, "40b3997d0bcef8e122258a025485ee8bc2d751affb1f93dd049798712e1c3203", "64e51a2c0407c22b4694f4d4b57ce364af1d08fa"),
            REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_019_command.py": (36093, "255ab1f5be646d94dd88f5d2a2b531db132bf195f4fdbfc1d7c931381412698d", "c2a4c2b6e3b4994dc6605f6042c1a586e44e6120"),
        }
        for path, (size, digest, blob) in expected.items():
            content = path.read_bytes()
            actual_blob = hashlib.sha1(b"blob " + str(size).encode("ascii") + b"\0" + content).hexdigest()
            self.assertEqual((len(content), hashlib.sha256(content).hexdigest(), actual_blob, content.count(b"\r\n")), (size, digest, blob, 0), path)
            self.assertIn(f"| {size} | `{digest}` | `{blob}` / CRLF=0 |", document)

    def test_in_memory_command_matches_freeze(self) -> None:
        """冻结命令只在内存重建并核对，不写盘、不执行。"""
        command = load_generator().build_command(INSTALLER_PATH.read_bytes()).encode("utf-8")
        self.assertEqual((len(command), hashlib.sha256(command).hexdigest()), (33675, "b731b656e79e506b470bd3e1074bc965983b789a2a4f547e3df3c86505622087"))


if __name__ == "__main__":
    unittest.main()
