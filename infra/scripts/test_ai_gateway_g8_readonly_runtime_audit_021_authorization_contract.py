#!/usr/bin/env python3
"""校验 021 回执失败记录、历史冻结证据与已消费墓碑。"""

import ast
import hashlib
import os
from pathlib import Path
import subprocess
import unittest


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-runtime-audit-authorization-20260815-021.md"
ATTEMPT_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-runtime-audit-attempt-20260815-021.md"
RUNNER_PATH = REPO_ROOT / "infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-021.py"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-021-command.py"
RUNNER_TEST_PATH = REPO_ROOT / "infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_021.py"
GENERATOR_TEST_PATH = REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_021_command.py"
CHANGE_ID = "CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-021"
ENGINEERING_MERGE = "8bc05cbf3bc71a8954087dc7f26732f836e5212e"
ENGINEERING_HEAD = "c73ef139721bcfc693ffb31caa6fe803be526286"
ENGINEERING_BASE = "358edfd8e8d5d3293944314d79d503245049649a"
STATUS_PATHS = (
    REPO_ROOT / "README.md",
    REPO_ROOT / "docs/ai-gateway-g8-acceptance.md",
    REPO_ROOT / "docs/ai-gateway-g8-software-closure.md",
    REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-runbook.md",
    REPO_ROOT / "docs/tools.md",
)


def git_blob(relative: str) -> bytes:
    """只从工程 merge 的 Git 对象读取历史候选，避免误用当前墓碑。"""
    return subprocess.run(
        ["git", "-c", f"safe.directory={REPO_ROOT}", "show", f"{ENGINEERING_MERGE}:{relative}"],
        cwd=REPO_ROOT,
        capture_output=True,
        check=True,
    ).stdout


class TestG8ReadonlyRuntimeAudit021ConsumedContract(unittest.TestCase):
    def test_documents_record_receipt_failure_and_zero_remote_reach(self) -> None:
        """记录必须区分本地回执失败、PowerShell 启动与远端零触达。"""
        combined = AUTH_PATH.read_text(encoding="utf-8") + ATTEMPT_PATH.read_text(encoding="utf-8")
        for required in (
            "CONSUMED_LOCAL_RECEIPT_UNAVAILABLE_SSH_NOT_STARTED",
            CHANGE_ID,
            ENGINEERING_MERGE,
            "8407837bc7e9af65dc7d2fe8ad1f8a9728186745ad25d20e802c8793a9740dcd",
            "HOST_RESULT=FAILED reason=receipt_unavailable exit_code=2",
            "POWERSHELL_ATTEMPTED=YES",
            "SSH 与远端命令：`0 / 0`",
            "全部为 `0`",
            "重试：`0`",
            "021 已永久消费",
            "021 不得再次授权、重试或重放",
            "`G8_SOFTWARE_CLOSED_LOOP` 尚未完成",
        ):
            self.assertIn(required, combined)
        self.assertNotIn("PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED", combined)
        for stale in (
            "021 尚未执行、尚未消费",
            "未来若获得新的独立精确执行授权",
            "只有用户对精确 ChangeId",
        ):
            self.assertNotIn(stale, combined)

    def test_consumed_entries_reject_before_parser_materials_and_subprocesses(self) -> None:
        """两个历史入口必须无导入，并对历史参数固定返回消费状态。"""
        cases = (
            (
                RUNNER_PATH,
                ["--change-id=historical", "--engineering-merge=historical", "--execute-authorized"],
                "G8_TEST_READONLY_RUNTIME_AUDIT_021_RUNNER=FAILED reason=change_id_consumed\n",
            ),
            (
                GENERATOR_PATH,
                ["--change-id=historical", "--output-file=forbidden"],
                "G8_TEST_READONLY_RUNTIME_AUDIT_021_COMMAND=FAILED reason=change_id_consumed\n",
            ),
        )
        for path, arguments, expected in cases:
            tree = ast.parse(path.read_text(encoding="utf-8"))
            self.assertFalse(any(isinstance(node, (ast.Import, ast.ImportFrom)) for node in ast.walk(tree)), path)
            result = subprocess.run(
                ["python", "-I", str(path), *arguments],
                cwd=REPO_ROOT,
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
                timeout=15,
            )
            self.assertEqual((result.returncode, result.stdout, result.stderr), (2, expected, ""), path)

    def test_status_documents_use_consumed_zero_reach_contract(self) -> None:
        """共享状态必须统一为 021 已消费、远端零触达且闭环未完成。"""
        for path in STATUS_PATHS:
            document = path.read_text(encoding="utf-8")
            for required in (
                CHANGE_ID,
                "CONSUMED_LOCAL_RECEIPT_UNAVAILABLE_SSH_NOT_STARTED",
                "021 已",
                "SSH",
                "0",
                "G8_SOFTWARE_CLOSED_LOOP",
            ):
                self.assertIn(required, document, path)
            for stale in (
                "021 当前为 `PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED`",
                "状态为 `PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED`；尚无新的测试服运行态证据",
                "正式入口不得在新的独立精确授权前执行",
            ):
                self.assertNotIn(stale, document, path)

    def test_tombstones_match_attempt_record(self) -> None:
        """当前四个墓碑文件必须与执行记录的大小、摘要和 blob 一致。"""
        document = ATTEMPT_PATH.read_text(encoding="utf-8")
        expected = {
            RUNNER_PATH: (433, "db897b1849edd3e5b9af05794fa8520c2efeb03f3a8462240cdb57a66495ea7d", "e02e97703c2b74e29c38e0150a5833734393c974"),
            GENERATOR_PATH: (422, "b5f43b69906b3808f0531e8b796841f53ebcc5df00d8c9a5ba95a1442ab90ca2", "e6632016528e2457b4b957507ca01c68e1c63eec"),
            RUNNER_TEST_PATH: (2222, "d846651fedf420526a332fc6b736f32c241b1956d18178e61d2663cc7a5d6b16", "d3f0f41f1e8e5d0293a5c38b8dca680791e954a9"),
            GENERATOR_TEST_PATH: (1878, "91317a191e79872f30a2e69b0c9bd864a7c134b89ec6cd494988a314e8fb5e10", "cdbff3ac27174a30885745112da9811c9dac2258"),
        }
        for path, (size, digest, blob) in expected.items():
            content = path.read_bytes()
            self.assertEqual((len(content), hashlib.sha256(content).hexdigest()), (size, digest), path)
            self.assertEqual(hashlib.sha1(b"blob " + str(size).encode("ascii") + b"\0" + content).hexdigest(), blob)
            self.assertNotIn(b"\r\n", content, path)
            relative = path.relative_to(REPO_ROOT).as_posix()
            self.assertIn(f"| `{relative}` | {size} | `{digest}` | `{blob}` |", document)

    def test_historical_merge_blobs_and_command_remain_reproducible(self) -> None:
        """墓碑化后仍从工程 merge 原始对象复核候选与冻结命令。"""
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
            "infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-021.py": (13157, "092ebfa2453552a46eda55e91c3db2777e28bb87dcfc191156f7690e472d348f", "8662e3e6558453799245d084e32b8826ec84e969"),
            "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-021-command.py": (27486, "d1d413c3e82ff97de221c611c35c507daeb928e6cba674a4b0843c603724036f", "087683242cae3b3a1696e8815a9102f6650f002b"),
            "infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_021.py": (5404, "47cff939adade4b695ca62869b04e63a4ce0806d9e41071dbfebb3a9008cfc8b", "78b68b48cf18892393f6e71abb89ac2e96c59d6e"),
            "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_021_command.py": (14896, "bf338ef520cd2000991455c1dec8405b4dd2195dfd8f363f3f31f0599d1318ee", "ec8a2e184ea7e1abd5aa1dfe8d3db4d4eee69adc"),
            "infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh": (18377, "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256", "27450efc39af7e763ea8df0c59d584433d5e5edd"),
        }
        historical: dict[str, bytes] = {}
        for relative, (size, digest, blob) in expected.items():
            content = git_blob(relative)
            historical[relative] = content
            self.assertEqual((len(content), hashlib.sha256(content).hexdigest()), (size, digest), relative)
            self.assertEqual(hashlib.sha1(b"blob " + str(size).encode("ascii") + b"\0" + content).hexdigest(), blob)
            self.assertNotIn(b"\r\n", content, relative)
        generator_path = "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-021-command.py"
        auditor_path = "infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh"
        namespace = {"__name__": "g8_021_historical_freeze", "__file__": "<git-blob>"}
        exec(compile(historical[generator_path].decode("utf-8"), "<git-blob>", "exec"), namespace)
        command = namespace["build_command"](
            historical[auditor_path], receipt_path=namespace["TRUSTED_PROFILE_RECEIPT"]
        ).encode("utf-8")
        self.assertEqual(
            (len(command), hashlib.sha256(command).hexdigest()),
            (32009, "8407837bc7e9af65dc7d2fe8ad1f8a9728186745ad25d20e802c8793a9740dcd"),
        )

    def test_020_tombstones_remain_byte_exact(self) -> None:
        """021 消费不得恢复或修改 020 历史入口。"""
        expected = {
            "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-020-command.py": "57acdab38d9eb9fe9adaa34541c8024bd6b70fc2e36f4214a79eeb50b59e405f",
            "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_020_command.py": "9ca44161de7a6b013ddbb374bb1ca074fb86db9b163cf789fcae67a53dfbf5ca",
        }
        for relative, digest in expected.items():
            self.assertEqual(hashlib.sha256((REPO_ROOT / relative).read_bytes()).hexdigest(), digest, relative)

    def test_workflow_runs_015_through_021_tombstones_on_both_platforms(self) -> None:
        """CI 必须在 Windows 与 Linux 断网只读环境运行 015 至 021 墓碑契约。"""
        workflow = (REPO_ROOT / ".github/workflows/ci.yml").read_text(encoding="utf-8")
        for test in (GENERATOR_TEST_PATH.name, RUNNER_TEST_PATH.name, Path(__file__).name):
            self.assertGreaterEqual(workflow.count(test), 2, test)
        self.assertIn("验证 G8 015/016/017/018/019/020/021 墓碑与 022 候选离线门禁", workflow)
        self.assertIn("--network none", workflow)
        self.assertIn("python:3.13-bookworm", workflow)


if __name__ == "__main__":
    unittest.main()
