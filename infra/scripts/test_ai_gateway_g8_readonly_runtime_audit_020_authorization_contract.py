#!/usr/bin/env python3
"""校验 020 失败关闭记录、历史冻结证据与已消费墓碑。"""

import ast
import hashlib
import os
from pathlib import Path
import subprocess
import unittest


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-runtime-audit-authorization-20260815-020.md"
ATTEMPT_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-runtime-audit-attempt-20260815-020.md"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-020-command.py"
TEST_PATH = REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_020_command.py"
CHANGE_ID = "CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-020"
ENGINEERING_MERGE = "3c63539279a34ae2365fc9d7e26e207dd728c4ba"
ENGINEERING_HEAD = "dcb594d33e79bfbb059293e4734e49e62409d51a"
ENGINEERING_BASE = "b9211b8a90610aa2e45873fa9de54575bce58fb5"
STATUS_PATHS = (
    REPO_ROOT / "README.md",
    REPO_ROOT / "docs/ai-gateway-g8-acceptance.md",
    REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-runbook.md",
    REPO_ROOT / "docs/tools.md",
)


def git_blob(relative: str) -> bytes:
    """从工程 merge 原始 Git 对象读取历史候选，不读取当前墓碑。"""
    return subprocess.run(
        ["git", "-c", f"safe.directory={REPO_ROOT}", "show", f"{ENGINEERING_MERGE}:{relative}"],
        cwd=REPO_ROOT,
        capture_output=True,
        check=True,
    ).stdout


class TestG8ReadonlyRuntimeAudit020ConsumedContract(unittest.TestCase):
    def test_documents_record_parse_failure_and_zero_remote_reach(self) -> None:
        """记录必须把本地外层解析失败与远端零触达精确分开。"""
        combined = AUTH_PATH.read_text(encoding="utf-8") + ATTEMPT_PATH.read_text(encoding="utf-8")
        for required in (
            "CONSUMED_LOCAL_WRAPPER_PARSE_FAILED_SSH_NOT_STARTED",
            CHANGE_ID,
            ENGINEERING_MERGE,
            "31c1eaaf6f3916dbabb51447a63d263ac4f73509bb8e535451df28db4e024a3d",
            "ParserError / Unexpected token",
            "SSH 与远端命令：`0 / 0`",
            "全部为 `0`",
            "重试：`0`",
            "020 不得再次授权、重试或重放",
            "`G8_SOFTWARE_CLOSED_LOOP` 尚未完成",
        ):
            self.assertIn(required, combined)
        self.assertNotIn("PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED", combined)

    def test_consumed_entry_rejects_before_parser_materials_and_network(self) -> None:
        """历史参数不得让已消费入口恢复生成或联网能力。"""
        source = GENERATOR_PATH.read_text(encoding="utf-8")
        tree = ast.parse(source)
        self.assertFalse(any(isinstance(node, (ast.Import, ast.ImportFrom)) for node in ast.walk(tree)))
        result = subprocess.run(
            ["python", "-I", str(GENERATOR_PATH), "--change-id=historical", "--output-file=forbidden"],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
            timeout=15,
        )
        self.assertEqual(
            (result.returncode, result.stdout, result.stderr),
            (2, "G8_TEST_READONLY_RUNTIME_AUDIT_020_COMMAND=FAILED reason=change_id_consumed\n", ""),
        )

    def test_status_documents_use_consumed_zero_reach_contract(self) -> None:
        """项目状态必须统一为 020 已消费、远端零触达且闭环未完成。"""
        for path in STATUS_PATHS:
            document = path.read_text(encoding="utf-8")
            for required in (CHANGE_ID, "020 已", "SSH", "0", "G8_SOFTWARE_CLOSED_LOOP"):
                self.assertIn(required, document, path)
            self.assertNotIn("PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED", document, path)
            for stale in ("020 的工程合并与未来远端只读执行仍须分开授权", "020 只允许在工程合并"):
                self.assertNotIn(stale, document, path)

    def test_tombstones_match_attempt_record(self) -> None:
        """当前两个墓碑文件必须与执行记录的大小、摘要和 blob 一致。"""
        document = ATTEMPT_PATH.read_text(encoding="utf-8")
        expected = {
            GENERATOR_PATH: (425, "57acdab38d9eb9fe9adaa34541c8024bd6b70fc2e36f4214a79eeb50b59e405f", "a020e485a8f272848ce612aaafdeea6431d27c54"),
            TEST_PATH: (1878, "9ca44161de7a6b013ddbb374bb1ca074fb86db9b163cf789fcae67a53dfbf5ca", "08f4929ee5aff691c5b184d0b6d7a87197f1e62a"),
        }
        for path, (size, digest, blob) in expected.items():
            content = path.read_bytes()
            self.assertEqual((len(content), hashlib.sha256(content).hexdigest()), (size, digest), path)
            self.assertEqual(hashlib.sha1(b"blob " + str(size).encode("ascii") + b"\0" + content).hexdigest(), blob)
            self.assertNotIn(b"\r\n", content, path)
            self.assertIn(f"| {size} | `{digest}` | `{blob}` |", document)

    def test_historical_merge_blobs_and_command_remain_reproducible(self) -> None:
        """墓碑化后仍只从工程 merge 原始对象复核历史候选与命令摘要。"""
        if os.name != "nt" and (REPO_ROOT / ".git").is_file():
            self.skipTest("linked worktree 的外部 Git 对象库未挂载")
        parents = subprocess.run(
            ["git", "-c", f"safe.directory={REPO_ROOT}", "show", "-s", "--format=%P", ENGINEERING_MERGE],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=True,
        ).stdout.strip()
        self.assertEqual(parents, f"{ENGINEERING_BASE} {ENGINEERING_HEAD}")
        expected = {
            "infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh": (18377, "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256", "27450efc39af7e763ea8df0c59d584433d5e5edd"),
            "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-020-command.py": (27486, "3a286187602277c2255e978712e37cff7d6edf46d292a185e665aaa70654bbae", "212124e085c2f34adf11eae62b0e0119c5d8f44e"),
            "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_020_command.py": (14896, "a156e62417826ce5a8f6347d46edca384f6abfaa5e819aa300dc0dc55b3d5b8b", "c3930bc478b2b05d33822db2996618949384f9f3"),
        }
        historical = {}
        for relative, (size, digest, blob) in expected.items():
            content = git_blob(relative)
            historical[relative] = content
            self.assertEqual((len(content), hashlib.sha256(content).hexdigest()), (size, digest), relative)
            self.assertEqual(hashlib.sha1(b"blob " + str(size).encode("ascii") + b"\0" + content).hexdigest(), blob)
            self.assertNotIn(b"\r\n", content, relative)
        generator_path = "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-020-command.py"
        auditor_path = "infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh"
        namespace = {"__name__": "g8_020_historical_freeze", "__file__": "<git-blob>"}
        exec(compile(historical[generator_path].decode("utf-8"), "<git-blob>", "exec"), namespace)
        command = namespace["build_command"](
            historical[auditor_path], receipt_path=namespace["TRUSTED_PROFILE_RECEIPT"]
        ).encode("utf-8")
        self.assertEqual(
            (len(command), hashlib.sha256(command).hexdigest()),
            (32009, "31c1eaaf6f3916dbabb51447a63d263ac4f73509bb8e535451df28db4e024a3d"),
        )

    def test_019_tombstones_remain_byte_exact(self) -> None:
        """020 消费不得恢复或修改 019 历史入口。"""
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
