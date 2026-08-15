#!/usr/bin/env python3
"""校验 023 失败消费记录、永久墓碑与历史冻结证据。"""

import ast
import hashlib
import os
from pathlib import Path
import subprocess
import sys
import unittest


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-runtime-audit-authorization-20260815-023.md"
ATTEMPT_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-runtime-audit-attempt-20260816-023.md"
RUNNER_PATH = REPO_ROOT / "infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-023.py"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-023-command.py"
GENERATOR_TEST_PATH = REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_023_command.py"
RUNNER_TEST_PATH = REPO_ROOT / "infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_023.py"
CHANGE_ID = "CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-023"
STATUS = "CONSUMED_SSH_SESSION_FAILED_REMOTE_AUDIT_NOT_PROVEN"
ENGINEERING_MERGE = "1eb23c8b87720cceea64dcfc349b0a9b9c04de4b"
ENGINEERING_HEAD = "9a969d4dd2881e659c50ab694a4d35b57adba803"
ENGINEERING_BASE = "0db6d060f4b3763c39f13a030fb7bec2485b546b"


def git_blob(relative: str) -> bytes:
    """只从工程 merge 的 Git 对象读取历史候选，避免恢复普通入口。"""
    return subprocess.run(
        ["git", "-c", f"safe.directory={REPO_ROOT}", "show", f"{ENGINEERING_MERGE}:{relative}"],
        cwd=REPO_ROOT,
        capture_output=True,
        check=True,
    ).stdout


class TestG8ReadonlyRuntimeAudit023ConsumedContract(unittest.TestCase):
    def test_status_documents_record_consumed_failure_and_g8_incomplete(self) -> None:
        """共享状态文档必须如实记录 SSH 会话失败、未知边界与永久消费。"""
        for relative in (
            "README.md",
            "docs/ai-gateway-g8-acceptance.md",
            "docs/ai-gateway-g8-software-closure.md",
            "docs/ai-gateway-g8-test-readonly-access-runbook.md",
            "docs/tools.md",
        ):
            document = (REPO_ROOT / relative).read_text(encoding="utf-8")
            for required in (CHANGE_ID, STATUS, "ssh_session_failed", "G8_SOFTWARE_CLOSED_LOOP"):
                self.assertIn(required, document, relative)
            self.assertNotIn("023 尚未执行、尚未消费", document, relative)

    def test_attempt_keeps_exact_low_sensitive_result_and_conservative_boundary(self) -> None:
        """执行记录必须区分已调用 SSH、会话失败与远端审计未知。"""
        document = ATTEMPT_PATH.read_text(encoding="utf-8")
        for required in (
            STATUS,
            "固定 SSH 调用：`1`",
            "SSH 会话成功：`0`",
            "UNKNOWN / 最多启动 1 次",
            "远端重试：`0`",
            "禁止再次授权、重试或重放",
            "G8_TEST_READONLY_ACCESS_023_PRE_SSH_GATE=PASS",
            "G8_TEST_READONLY_ACCESS_023_SSH_ATTEMPTED=YES",
            "G8_TEST_READONLY_ACCESS_023_HOST_RESULT=FAILED reason=ssh_session_failed exit_code=2",
        ):
            self.assertIn(required, document)
        for forbidden in ("COLLECTION_PASS` 已形成", "测试服运行态已通过", "G8_SOFTWARE_CLOSED_LOOP` 已完成"):
            self.assertNotIn(forbidden, document)

    def test_current_entries_are_no_import_tombstones(self) -> None:
        """默认、自检和历史授权参数都必须在外部能力前固定拒绝。"""
        cases = (
            (RUNNER_PATH, "G8_TEST_READONLY_RUNTIME_AUDIT_023_RUNNER=FAILED reason=change_id_consumed\n"),
            (GENERATOR_PATH, "G8_TEST_READONLY_RUNTIME_AUDIT_023_COMMAND=FAILED reason=change_id_consumed\n"),
        )
        historical = (
            f"--change-id={CHANGE_ID}",
            f"--engineering-merge={ENGINEERING_MERGE}",
            "--expected-command-size=32954",
            "--expected-command-sha256=bb48f5b4baf69eb6f563f021f676b97880e9570eaf5327daaffe69aaa32d6fe6",
            "--execute-authorized",
        )
        for path, expected in cases:
            source = path.read_text(encoding="utf-8")
            tree = ast.parse(source)
            self.assertFalse(any(isinstance(node, (ast.Import, ast.ImportFrom)) for node in ast.walk(tree)), path)
            for arguments in ((), ("--self-test",), historical):
                result = subprocess.run(
                    [sys.executable, "-I", str(path), *arguments],
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    check=False,
                    timeout=15,
                )
                self.assertEqual((result.returncode, result.stdout, result.stderr), (2, expected, ""), path)

    def test_current_tombstone_freeze_matches_document(self) -> None:
        """四个当前墓碑的大小、摘要、Git blob 与 LF 必须固定。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        for path in (RUNNER_PATH, GENERATOR_PATH, RUNNER_TEST_PATH, GENERATOR_TEST_PATH):
            content = path.read_bytes()
            relative = path.relative_to(REPO_ROOT).as_posix()
            digest = hashlib.sha256(content).hexdigest()
            blob = hashlib.sha1(f"blob {len(content)}\0".encode("ascii") + content).hexdigest()
            self.assertNotIn(b"\r\n", content, relative)
            self.assertIn(f"| `{relative}` | {len(content)} | `{digest}` | `{blob}` |", document)

    def test_historical_merge_blobs_and_command_remain_reproducible(self) -> None:
        """墓碑化不得篡改历史工程 merge、父顺序或冻结命令证据。"""
        if os.name != "nt" and (REPO_ROOT / ".git").is_file():
            self.skipTest("linked worktree 的外部 Git 对象库未挂载")
        document = AUTH_PATH.read_text(encoding="utf-8")
        parents = subprocess.run(
            ["git", "-c", f"safe.directory={REPO_ROOT}", "show", "-s", "--format=%P", ENGINEERING_MERGE],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=True,
        ).stdout.strip()
        self.assertEqual(parents, f"{ENGINEERING_BASE} {ENGINEERING_HEAD}")
        paths = (
            RUNNER_PATH,
            GENERATOR_PATH,
            RUNNER_TEST_PATH,
            GENERATOR_TEST_PATH,
            GENERATOR_PATH.with_name("audit-ai-gateway-g8-test-server-readonly.sh"),
        )
        historical: dict[str, bytes] = {}
        for path in paths:
            relative = path.relative_to(REPO_ROOT).as_posix()
            content = git_blob(relative)
            historical[relative] = content
            digest = hashlib.sha256(content).hexdigest()
            blob = hashlib.sha1(f"blob {len(content)}\0".encode("ascii") + content).hexdigest()
            self.assertNotIn(b"\r\n", content, relative)
            self.assertIn(f"| `{relative}` | {len(content)} | `{digest}` | `{blob}` |", document)
        generator_relative = GENERATOR_PATH.relative_to(REPO_ROOT).as_posix()
        auditor_relative = GENERATOR_PATH.with_name("audit-ai-gateway-g8-test-server-readonly.sh").relative_to(REPO_ROOT).as_posix()
        namespace = {"__name__": "g8_023_historical_freeze", "__file__": "<git-blob>"}
        exec(compile(historical[generator_relative].decode("utf-8"), "<git-blob>", "exec"), namespace)
        command = namespace["build_command"](
            historical[auditor_relative], receipt_path=namespace["TRUSTED_LOCAL_APPDATA_RECEIPT"]
        ).encode("utf-8")
        self.assertEqual((len(command), hashlib.sha256(command).hexdigest()), (
            32954,
            "bb48f5b4baf69eb6f563f021f676b97880e9570eaf5327daaffe69aaa32d6fe6",
        ))

    def test_workflow_runs_all_tombstones_on_windows_and_network_none_linux(self) -> None:
        """CI 必须在 Windows 与断网 Linux 中持续验证 023 墓碑。"""
        workflow = (REPO_ROOT / ".github/workflows/ci.yml").read_text(encoding="utf-8")
        for test in (GENERATOR_TEST_PATH.name, RUNNER_TEST_PATH.name, Path(__file__).name):
            self.assertGreaterEqual(workflow.count(test), 2, test)
        self.assertIn("015/016/017/018/019/020/021/022/023 墓碑", workflow)
        self.assertIn("--network none", workflow)


if __name__ == "__main__":
    unittest.main()
