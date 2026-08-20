#!/usr/bin/env python3
"""校验 022 身份配对失败记录、历史冻结证据与已消费墓碑。"""

import ast
import hashlib
import os
from pathlib import Path
import subprocess
import unittest


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-runtime-audit-authorization-20260815-022.md"
ATTEMPT_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-runtime-audit-attempt-20260815-022.md"
RUNNER_PATH = REPO_ROOT / "infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-022.py"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-022-command.py"
RUNNER_TEST_PATH = REPO_ROOT / "infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_022.py"
GENERATOR_TEST_PATH = REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_022_command.py"
CHANGE_ID = "CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-022"
ENGINEERING_MERGE = "84ae5b0ad87958ee63fbfa709c4f164baca39a1b"
ENGINEERING_HEAD = "fc0344283813bd873aa70520e0b8fcd1da424500"
ENGINEERING_BASE = "dc035aec34903bbaf2a991cd64c6109db52fbdeb"
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


class TestG8ReadonlyRuntimeAudit022ConsumedContract(unittest.TestCase):
    def test_documents_record_identity_failure_replay_violation_and_zero_remote_reach(self) -> None:
        """记录必须如实区分授权调用、误重放、本地失败与远端零触达。"""
        combined = AUTH_PATH.read_text(encoding="utf-8") + ATTEMPT_PATH.read_text(encoding="utf-8")
        for required in (
            "CONSUMED_LOCAL_IDENTITY_PAIR_FAILED_SSH_NOT_STARTED",
            CHANGE_ID,
            ENGINEERING_MERGE,
            "d649d2f896a224f3c1063b4bbb49953de1a7330d36b1db0cbaaf2bbfdea2e9e9",
            "HOST_RESULT=FAILED reason=identity_pair_failed exit_code=2",
            "POWERSHELL_ATTEMPTED=YES",
            "本地正式入口与 PowerShell 总调用数：`2 / 2`",
            "未授权本地重放：`1`",
            "SSH 与远端命令：`0 / 0`",
            "两份非空耐久回执",
            "全部为 `0`",
            "022 已永久消费",
            "022 不得再次授权、重试或重放",
            "`G8_SOFTWARE_CLOSED_LOOP` 尚未完成",
        ):
            self.assertIn(required, combined)
        self.assertNotIn("PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED", combined)
        for stale in (
            "022 尚未执行、尚未消费",
            "只有用户对精确 ChangeId",
            "才允许调用固定正式入口",
        ):
            self.assertNotIn(stale, combined)

    def test_consumed_entries_reject_before_parser_materials_and_subprocesses(self) -> None:
        """两个历史入口必须无导入，并对历史参数固定返回消费状态。"""
        cases = (
            (
                RUNNER_PATH,
                ["--change-id=historical", "--engineering-merge=historical", "--execute-authorized"],
                "G8_TEST_READONLY_RUNTIME_AUDIT_022_RUNNER=FAILED reason=change_id_consumed\n",
            ),
            (
                GENERATOR_PATH,
                ["--change-id=historical", "--output-file=forbidden"],
                "G8_TEST_READONLY_RUNTIME_AUDIT_022_COMMAND=FAILED reason=change_id_consumed\n",
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
        """共享状态必须统一为 022 已消费、远端零触达且闭环未完成。"""
        for path in STATUS_PATHS:
            document = path.read_text(encoding="utf-8")
            for required in (
                CHANGE_ID,
                "CONSUMED_LOCAL_IDENTITY_PAIR_FAILED_SSH_NOT_STARTED",
                "022 已",
                "SSH",
                "0",
                "G8_SOFTWARE_CLOSED_LOOP",
            ):
                self.assertIn(required, document, path)
            for stale in (
                "022 当前为 `PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED`",
                "当前状态为 `PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED`；022 尚未执行",
                "022 尚未执行、尚未消费",
            ):
                self.assertNotIn(stale, document, path)

    def test_tombstones_match_attempt_record(self) -> None:
        """当前四个墓碑文件必须与执行记录的大小、摘要和 blob 一致。"""
        document = ATTEMPT_PATH.read_text(encoding="utf-8")
        expected = {
            RUNNER_PATH: (421, "75d57053fbf2c9cf60df0599fefe4750d5803dac442dee9df74f6cba9ceb659b", "908969597ac07273d8ab312f717abd0a035fc19b"),
            GENERATOR_PATH: (399, "2d63b0e7a3898e144e70e2d4274c8bf612751526aeb12978dc3162d395f788bb", "b8f2ae4e450727d455b4d90a85ab2a79ef76b8ba"),
            RUNNER_TEST_PATH: (2222, "5d49bdcd6ee04c11e5347ca8357dddcf451f791e11d7b01848f0df2a96ca9be4", "4da204189296744750fc5f83514855f9b1b03704"),
            GENERATOR_TEST_PATH: (1878, "2b395e0e6a5c83acf3ab0f2c4e6dd06d77f4563c8b5af246cc2b179ab32da798", "999515e9b05f234598469ef80bb9cf59f64311c1"),
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
            "infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-022.py": (13256, "3f9d4dfbb283a4275556d6c3949bbfd790dd06eeaf2c9b88ece0e0db29e2f65f", "0a3e88fd1830cf2a1da328b9dc342d28bc125c67"),
            "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-022-command.py": (30098, "4ecf224f848f6597c59db705f122c5e4ffe8593ac48395451f2e11e8973fba00", "931947ed15128004b80fd16cde04fb3d4e8921b4"),
            "infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_022.py": (6079, "12b6855db5a186d821376d961ad0210c567e17a239221d6637c653a557c4f6d1", "1025f1722d80f6d6dc0956e9da2b2e25f66625aa"),
            "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_022_command.py": (33218, "b043ec28f936e7cc700982291f481e8d529a6dc3cfae2d3998157279dd70ab12", "feb27b095a6c9cf787bdf05e92fb37d2cb2a8a27"),
            "infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh": (18377, "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256", "27450efc39af7e763ea8df0c59d584433d5e5edd"),
        }
        historical: dict[str, bytes] = {}
        for relative, (size, digest, blob) in expected.items():
            content = git_blob(relative)
            historical[relative] = content
            self.assertEqual((len(content), hashlib.sha256(content).hexdigest()), (size, digest), relative)
            self.assertEqual(hashlib.sha1(b"blob " + str(size).encode("ascii") + b"\0" + content).hexdigest(), blob)
            self.assertNotIn(b"\r\n", content, relative)
        generator_path = "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-022-command.py"
        auditor_path = "infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh"
        namespace = {"__name__": "g8_022_historical_freeze", "__file__": "<git-blob>"}
        exec(compile(historical[generator_path].decode("utf-8"), "<git-blob>", "exec"), namespace)
        command = namespace["build_command"](
            historical[auditor_path], receipt_path=namespace["TRUSTED_LOCAL_APPDATA_RECEIPT"]
        ).encode("utf-8")
        self.assertEqual(
            (len(command), hashlib.sha256(command).hexdigest()),
            (34027, "d649d2f896a224f3c1063b4bbb49953de1a7330d36b1db0cbaaf2bbfdea2e9e9"),
        )

    def test_021_tombstones_remain_byte_exact(self) -> None:
        """022 消费不得恢复或修改 021 历史入口。"""
        expected = {
            "infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-021.py": "db897b1849edd3e5b9af05794fa8520c2efeb03f3a8462240cdb57a66495ea7d",
            "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-021-command.py": "b5f43b69906b3808f0531e8b796841f53ebcc5df00d8c9a5ba95a1442ab90ca2",
            "infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_021.py": "d846651fedf420526a332fc6b736f32c241b1956d18178e61d2663cc7a5d6b16",
            "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_021_command.py": "91317a191e79872f30a2e69b0c9bd864a7c134b89ec6cd494988a314e8fb5e10",
        }
        for relative, digest in expected.items():
            self.assertEqual(hashlib.sha256((REPO_ROOT / relative).read_bytes()).hexdigest(), digest, relative)

    def test_workflow_runs_015_through_022_tombstones_on_both_platforms(self) -> None:
        """CI 必须在 Windows 与 Linux 断网只读环境运行 015 至 022 墓碑契约。"""
        workflow = (REPO_ROOT / ".github/workflows/ci.yml").read_text(encoding="utf-8")
        for test in (GENERATOR_TEST_PATH.name, RUNNER_TEST_PATH.name, Path(__file__).name):
            self.assertGreaterEqual(workflow.count(test), 2, test)
        self.assertIn("验证 G8 015/016/017/018/019/020/021/022/023 墓碑", workflow)
        self.assertIn("--network none", workflow)
        self.assertIn(
            "python@sha256:62eafe52c91cad83c2c74e630bfde917da8c253673e695665d454def84fc9a13",
            workflow,
        )
        self.assertIn('docker pull "$g8_bookworm_image"', workflow)
        self.assertIn("docker run --rm --pull=never --network none", workflow)


if __name__ == "__main__":
    unittest.main()
