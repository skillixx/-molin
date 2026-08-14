#!/usr/bin/env python3
"""校验 017 失败关闭记录、历史冻结证据与两个墓碑入口保持一致。"""

import ast
import hashlib
import os
import subprocess
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-install-authorization-20260814-017.md"
ATTEMPT_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-install-attempt-20260814-017.md"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-017-command.py"
INSTALLER_PATH = REPO_ROOT / "infra/scripts/g8-test-readonly-access-install-017.sh"
HISTORICAL_MERGE = "e2a7e4f89c4115b3e32dc27292b0bc11d7d09a57"


class TestG8ReadonlyInstall017ConsumedContract(unittest.TestCase):
    def test_documents_record_uncertain_ssh_boundary_and_zero_install(self) -> None:
        """记录必须区分未知 SSH 到达边界与确定未执行的远端安装段。"""
        combined = AUTH_PATH.read_text(encoding="utf-8") + ATTEMPT_PATH.read_text(encoding="utf-8")
        self.assertNotIn("017 仅为新的工程候选", AUTH_PATH.read_text(encoding="utf-8"))
        for required in (
            "CONSUMED_LOCAL_GATE_FAILED_SSH_REACHABILITY_UNKNOWN",
            "G8_TEST_READONLY_ACCESS_017_LOCAL_GATE=FAILED reason=local_gate_failed",
            "SSH 启动与连接：`UNKNOWN / 最多 1`",
            "远端安装段：`0/1`",
            "sudo、安装器与 post-check：`0 / 0 / 0`",
            "017 按失败关闭规则消费并禁止重放",
            "业务请求、上游请求、费用：`0 / 0 / 0 CNY`",
        ):
            self.assertIn(required, combined)

    def test_generator_is_import_free_tombstone(self) -> None:
        """生成入口源码不得保留参数解析、文件读取或联网依赖。"""
        source = GENERATOR_PATH.read_text(encoding="utf-8")
        tree = ast.parse(source)
        self.assertFalse(any(isinstance(node, (ast.Import, ast.ImportFrom)) for node in ast.walk(tree)))
        for forbidden in ("argparse", "subprocess", "socket", "Path(", "ssh.exe", "ssh-keygen"):
            self.assertNotIn(forbidden, source)

    def test_both_entrypoints_return_exact_consumed_status(self) -> None:
        """历史参数只能得到固定低敏消费状态。"""
        generator = subprocess.run(
            ["python", "-I", str(GENERATOR_PATH), "--change-id=historical"],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )
        self.assertEqual(generator.returncode, 2)
        self.assertEqual(generator.stdout, "G8_TEST_READONLY_ACCESS_017_COMMAND=FAILED reason=change_id_consumed\n")
        self.assertEqual(generator.stderr, "")

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
        self.assertEqual(installer.returncode, 2)
        self.assertEqual(installer.stdout, "G8_TEST_READONLY_ACCESS_INSTALL_017=FAILED reason=change_id_consumed\n")
        self.assertEqual(installer.stderr, "")

    def test_consumed_tombstone_hashes_match_attempt_record(self) -> None:
        """当前四个墓碑文件必须与执行记录的大小、摘要和 blob 完全一致。"""
        document = ATTEMPT_PATH.read_text(encoding="utf-8")
        expected = {
            INSTALLER_PATH: (182, "4013529da1e7e9c9a883aa1f9cc77f7dfa194b913976bac342aee03955c4bffc", "9b689b7c2cbff2f2ab678d4501630751eb87bdeb"),
            GENERATOR_PATH: (410, "fcc8ed7c6ed503fa0b4ee4108516d7c52550580b59dbebfe5b9eb191507e9ec9", "887ff92ba6cbd2cc1d9387f724c453a073e44738"),
            REPO_ROOT / "infra/scripts/test_g8_test_readonly_access_install_017.py": (1230, "7c1dd8a6c8b4c6095bf694f32e8c502387a6190d2403954022f2fc8e14efcd97", "c54c5ae45526bd6aa6f08f1c8835999e9c909b01"),
            REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_017_command.py": (1444, "c8055c0d48566d5f7ecef2e2208e1226ece91baab3426b76e4b59f3de3ff1abc", "bce0a4842056823ebc79c2e2275dff98706b4ba9"),
        }
        for path, (size, sha256, blob) in expected.items():
            content = path.read_bytes()
            self.assertEqual(len(content), size, path)
            self.assertEqual(hashlib.sha256(content).hexdigest(), sha256, path)
            self.assertEqual(hashlib.sha1(b"blob " + str(size).encode("ascii") + b"\0" + content).hexdigest(), blob, path)
            self.assertIn(f"| {size} | `{sha256}` | `{blob}` |", document)

    def test_historical_merge_blobs_match_frozen_document(self) -> None:
        """墓碑化后仍从工程合并对象复核四个历史候选摘要。"""
        # linked worktree 只读挂载到 Linux 容器时，.git 指向容器外的 Windows 对象库；普通 CI checkout 不跳过。
        if os.name != "nt" and (REPO_ROOT / ".git").is_file():
            self.skipTest("linked worktree 的外部 Git 对象库未挂载")
        document = AUTH_PATH.read_text(encoding="utf-8")
        expected = {
            "infra/scripts/g8-test-readonly-access-install-017.sh": (
                10977,
                "4deb5a26c27e83a2afe766dd815e4b611b5bc0c3c19eed9afb1bfe0e1d0b1188",
            ),
            "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-017-command.py": (
                16696,
                "b9b552a71118560e5a2d18789ac9a1bc3c312fd80666b50d318cc08994fac669",
            ),
            "infra/scripts/test_g8_test_readonly_access_install_017.py": (
                18254,
                "90ed63db0d0caacd38ecc3f292ea393aeae9575cc9aa8be586da5eaa722dbc34",
            ),
            "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_017_command.py": (
                21493,
                "7b8cd85bdb5917dea6fdb0b86e0f055bb98946e09028ddd87c0bd456c906eb7d",
            ),
        }
        for path, (size, sha256) in expected.items():
            result = subprocess.run(
                ["git", "-c", f"safe.directory={REPO_ROOT}", "show", f"{HISTORICAL_MERGE}:{path}"],
                cwd=REPO_ROOT,
                capture_output=True,
                check=True,
            )
            self.assertEqual(len(result.stdout), size, path)
            self.assertEqual(hashlib.sha256(result.stdout).hexdigest(), sha256, path)
            self.assertIn(f"| {size} | `{sha256}` |", document)


if __name__ == "__main__":
    unittest.main()
