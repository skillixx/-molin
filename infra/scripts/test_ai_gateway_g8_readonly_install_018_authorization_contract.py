#!/usr/bin/env python3
"""校验 018 失败关闭记录、历史冻结证据与两个墓碑入口保持一致。"""

import ast
import hashlib
import os
import subprocess
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-install-authorization-20260814-018.md"
ATTEMPT_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-install-attempt-20260815-018.md"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-018-command.py"
INSTALLER_PATH = REPO_ROOT / "infra/scripts/g8-test-readonly-access-install-018.sh"
HISTORICAL_MERGE = "ef9f65f851cc657eaa6fba7df866ba3c4c7a0912"


class TestG8ReadonlyInstall018ConsumedContract(unittest.TestCase):
    def test_documents_record_window_close_and_zero_install(self) -> None:
        """记录必须区分未知 SSH 到达边界与确定未观察到的远端安装影响。"""
        combined = AUTH_PATH.read_text(encoding="utf-8") + ATTEMPT_PATH.read_text(encoding="utf-8")
        for required in (
            "CONSUMED_HOST_WINDOW_CLOSED_NO_OUTPUT_SSH_REACHABILITY_UNKNOWN",
            "窗口直接关闭且没有可见输出",
            "SSH 启动与连接：`UNKNOWN / 最多 1`",
            "远端固定段、sudo、安装器与 post-check：`0 / 0 / 0 / 0`",
            "018 按失败关闭规则消费并禁止重放",
            "业务请求、上游请求、费用：`0 / 0 / 0 CNY`",
            "018 不得再次授权、重试或重放",
            "本段历史上限不构成 019 或任何后续 ChangeId 的授权",
            "019 现已执行失败并永久消费，不得再次授权、重试或重放",
        ):
            self.assertIn(required, combined)
        for stale_authorization in (
            "才可对 018 作出新的独立精确授权",
            "即使将来获得独立安装授权",
            "等待新的独立远端安装授权",
            "后续 019 必须独立完成工程门禁、合并后复核并取得新的精确授权",
        ):
            self.assertNotIn(stale_authorization, AUTH_PATH.read_text(encoding="utf-8"))

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
            cwd=REPO_ROOT, capture_output=True, text=True, encoding="utf-8", check=False,
        )
        self.assertEqual((generator.returncode, generator.stdout, generator.stderr), (2, "G8_TEST_READONLY_ACCESS_018_COMMAND=FAILED reason=change_id_consumed\n", ""))
        bash = Path(r"C:\Program Files\Git\bin\bash.exe")
        executable = str(bash) if bash.exists() else "bash"
        installer = subprocess.run([executable, str(INSTALLER_PATH), "historical"], cwd=REPO_ROOT, capture_output=True, text=True, encoding="utf-8", check=False)
        self.assertEqual((installer.returncode, installer.stdout, installer.stderr), (2, "G8_TEST_READONLY_ACCESS_INSTALL_018=FAILED reason=change_id_consumed\n", ""))

    def test_consumed_tombstones_match_attempt_record(self) -> None:
        """当前四个墓碑文件必须与执行记录的大小、摘要和 blob 完全一致。"""
        document = ATTEMPT_PATH.read_text(encoding="utf-8")
        expected = {
            INSTALLER_PATH: (182, "dd0e3f1a563772be2c6961d15fb8c1622a9c2f09e0a392e59e8ee1cf31038dd5", "7261b9a6e6fbbaed12bec9fe9eeb43dd338a95f7"),
            GENERATOR_PATH: (416, "6a3b304057b0e569d4dc07ad95607c5042bc6df00a55448eb42b77fe31c34fe5", "945cc9614024dd8a138a59c57e5ca1998827abe6"),
            REPO_ROOT / "infra/scripts/test_g8_test_readonly_access_install_018.py": (1195, "4ff08707407db6a11dd8844d5ba2207f46983d26e0bee0b4a5253ed170d14221", "67b8c8cc94e273a14dded15eb4c7325077a0e276"),
            REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_018_command.py": (1409, "296066323cc9f5355fcfd71cd8b0da3e0ff9df6044a8ec92093f1b9cc1416cfc", "2c27001ae9424744b16eaa4a773ec0ecc05ae016"),
        }
        for path, (size, digest, blob) in expected.items():
            content = path.read_bytes()
            self.assertEqual((len(content), hashlib.sha256(content).hexdigest()), (size, digest), path)
            self.assertEqual(hashlib.sha1(b"blob " + str(size).encode("ascii") + b"\0" + content).hexdigest(), blob, path)
            self.assertIn(f"| {size} | `{digest}` | `{blob}` |", document)

    def test_historical_merge_blobs_still_match_018_freeze(self) -> None:
        """墓碑化后仍从工程合并对象复核四个历史候选摘要。"""
        if os.name != "nt" and (REPO_ROOT / ".git").is_file():
            self.skipTest("linked worktree 的外部 Git 对象库未挂载")
        expected = {
            "infra/scripts/g8-test-readonly-access-install-018.sh": (10977, "3232f3265da00d0a8f531798c32917bc77efd4725c30b4c9a99022d91484de85"),
            "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-018-command.py": (19006, "dc5037b22555c500e152985edf231da4e44931ec4470bb645dd254a9d6e44db9"),
            "infra/scripts/test_g8_test_readonly_access_install_018.py": (18254, "a7575710f402aa26b4f6a37fc9bc499c6dd0f982b34ef7dd42f3e4207d1f95d6"),
            "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_018_command.py": (33386, "2519059d48b7dd0a9273476cb033d23d3aaa546b8991dccccef90f7c6baa5fb0"),
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
