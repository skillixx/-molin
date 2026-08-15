#!/usr/bin/env python3
"""校验 023 系统免交互认证候选、022 永久墓碑与远端未授权边界。"""

import ast
import hashlib
import os
from pathlib import Path
import subprocess
import unittest


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-runtime-audit-authorization-20260815-023.md"
RUNNER_PATH = REPO_ROOT / "infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-023.py"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-023-command.py"
GENERATOR_TEST_PATH = REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_023_command.py"
RUNNER_TEST_PATH = REPO_ROOT / "infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_023.py"
CHANGE_ID = "CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-023"
ENGINEERING_MERGE = "1eb23c8b87720cceea64dcfc349b0a9b9c04de4b"
ENGINEERING_HEAD = "9a969d4dd2881e659c50ab694a4d35b57adba803"
ENGINEERING_BASE = "0db6d060f4b3763c39f13a030fb7bec2485b546b"


def git_blob(relative: str) -> bytes:
    """只从工程 merge 的 Git 对象读取候选，避免工作树状态替代归档证据。"""
    return subprocess.run(
        ["git", "-c", f"safe.directory={REPO_ROOT}", "show", f"{ENGINEERING_MERGE}:{relative}"],
        cwd=REPO_ROOT,
        capture_output=True,
        check=True,
    ).stdout


def load_constants(path: Path) -> dict[str, object]:
    """只解析顶层常量，不导入候选代码。"""
    tree = ast.parse(path.read_text(encoding="utf-8"))
    constants: dict[str, object] = {}
    for node in tree.body:
        if isinstance(node, ast.Assign) and len(node.targets) == 1 and isinstance(node.targets[0], ast.Name):
            try:
                constants[node.targets[0].id] = ast.literal_eval(node.value)
            except (ValueError, TypeError):
                continue
    return constants


class TestG8ReadonlyRuntimeAudit023AuthorizationContract(unittest.TestCase):
    def test_status_documents_keep_023_pending_user_approval_and_g8_incomplete(self) -> None:
        """共享状态文档必须一致记录 023 待用户授权且软件闭环未完成。"""
        for relative in (
            "README.md",
            "docs/ai-gateway-g8-acceptance.md",
            "docs/ai-gateway-g8-software-closure.md",
            "docs/ai-gateway-g8-test-readonly-access-runbook.md",
            "docs/tools.md",
        ):
            document = (REPO_ROOT / relative).read_text(encoding="utf-8")
            self.assertIn(CHANGE_ID, document, relative)
            self.assertIn("PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED", document, relative)
            self.assertIn("REMOTE_NOT_AUTHORIZED", document, relative)
            self.assertIn("G8_SOFTWARE_CLOSED_LOOP", document, relative)

    def test_document_keeps_remote_execution_unauthorized(self) -> None:
        """工程候选不得被表述为已执行、已安装或软件闭环完成。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        for required in (
            CHANGE_ID,
            "PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED",
            "PR #404",
            "31892659673 completed/success",
            ENGINEERING_MERGE,
            ENGINEERING_HEAD,
            ENGINEERING_BASE,
            "022 因固定客户端私钥、公钥和指纹配对门禁返回",
            "不得再次授权、重试或重放",
            "pc@8.130.9.163:10003",
            "不安装",
            "不使用 sudo",
            "最多一次非交互 SSH",
            "零重试",
            "尚未执行",
            "`G8_SOFTWARE_CLOSED_LOOP` 尚未完成",
        ):
            self.assertIn(required, document)
        for forbidden in ("REMOTE_AUTHORIZED", "测试服运行态已通过", "`G8_SOFTWARE_CLOSED_LOOP` 已完成"):
            self.assertNotIn(forbidden, document)

    def test_execution_files_bind_new_change_id_and_default_closed_state(self) -> None:
        """生成器与固定启动器必须绑定 023，默认不得代表远端授权。"""
        generator = load_constants(GENERATOR_PATH)
        runner = load_constants(RUNNER_PATH)
        self.assertEqual(generator["CHANGE_ID"], CHANGE_ID)
        self.assertFalse(generator["CHANGE_ID_CONSUMED"])
        self.assertFalse(generator["REMOTE_EXECUTION_AUTHORIZED"])
        self.assertEqual(runner["CHANGE_ID"], CHANGE_ID)
        self.assertFalse(runner["REMOTE_EXECUTION_AUTHORIZED"])

    def test_fixed_runner_requires_explicit_execution_flag_and_one_powershell(self) -> None:
        """正式入口必须显式授权且只存在一个固定 PowerShell 子进程调用点。"""
        source = RUNNER_PATH.read_text(encoding="utf-8")
        self.assertIn("--execute-authorized", source)
        self.assertIn("GetWindowsDirectoryW", source)
        self.assertIn("[scriptblock]::Create($source)", source)
        self.assertIn("GIT_NO_LAZY_FETCH", source)
        self.assertIn("--no-replace-objects", source)
        self.assertIn("def git_merge_parents", source)
        self.assertIn("engineering_material_not_merge", source)
        self.assertIn("git_blob(repository, engineering_head, relative)", source)
        self.assertIn("FIXED_CHILD_LINES", source)
        self.assertEqual(source.count("return subprocess.run("), 2)
        self.assertNotIn("ssh.exe", source)
        self.assertNotIn("8.130.9.163", source)

    def test_postmerge_blobs_and_command_match_document(self) -> None:
        """工程 merge 的五个原始 blob 与纯内存命令必须和归档清单一致。"""
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
            self.assertNotIn(b"\r\n", content, path)
            self.assertIn(f"| `{relative}` | {len(content)} | `{digest}` | `{blob}` |", document)
        generator_relative = GENERATOR_PATH.relative_to(REPO_ROOT).as_posix()
        auditor_relative = GENERATOR_PATH.with_name(
            "audit-ai-gateway-g8-test-server-readonly.sh"
        ).relative_to(REPO_ROOT).as_posix()
        namespace = {"__name__": "g8_023_postmerge_freeze", "__file__": "<git-blob>"}
        exec(compile(historical[generator_relative].decode("utf-8"), "<git-blob>", "exec"), namespace)
        command = namespace["build_command"](
            historical[auditor_relative], receipt_path=namespace["TRUSTED_LOCAL_APPDATA_RECEIPT"]
        ).encode("utf-8")
        self.assertIn(f"| 纯内存冻结命令 | {len(command)} | `{hashlib.sha256(command).hexdigest()}` |", document)

    def test_022_tombstones_remain_byte_exact(self) -> None:
        """023 工程不得恢复或修改 022 已消费入口。"""
        expected = {
            "infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-022.py": "75d57053fbf2c9cf60df0599fefe4750d5803dac442dee9df74f6cba9ceb659b",
            "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-022-command.py": "2d63b0e7a3898e144e70e2d4274c8bf612751526aeb12978dc3162d395f788bb",
            "infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_022.py": "5d49bdcd6ee04c11e5347ca8357dddcf451f791e11d7b01848f0df2a96ca9be4",
            "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_022_command.py": "2b395e0e6a5c83acf3ab0f2c4e6dd06d77f4563c8b5af246cc2b179ab32da798",
        }
        for relative, digest in expected.items():
            content = (REPO_ROOT / relative).read_bytes()
            self.assertEqual(hashlib.sha256(content).hexdigest(), digest, relative)
            self.assertNotIn(b"\r\n", content, relative)

    def test_system_auth_contract_removes_fixed_client_identity_gate(self) -> None:
        """系统认证链不得恢复固定客户端密钥、指纹或强制身份文件。"""
        runner_source = RUNNER_PATH.read_text(encoding="utf-8")
        namespace = {"__name__": "g8_023_receipt", "__file__": str(GENERATOR_PATH)}
        exec(compile(GENERATOR_PATH.read_text(encoding="utf-8"), str(GENERATOR_PATH), "exec"), namespace)
        auditor = GENERATOR_PATH.with_name("audit-ai-gateway-g8-test-server-readonly.sh").read_bytes()
        command = namespace["build_command"](
            auditor,
            receipt_path=namespace["TRUSTED_LOCAL_APPDATA_RECEIPT"],
        )
        for required in (
            "-F", "none", "BatchMode=yes", "ConnectionAttempts=1",
            "StrictHostKeyChecking=yes", "PasswordAuthentication=no",
            "KbdInteractiveAuthentication=no", "NumberOfPasswordPrompts=0",
        ):
            self.assertIn(required, command)
        for forbidden in (
            "IdentitiesOnly=yes", "id_ed25519", "identity_pair_failed",
            "identity_pair_mismatch", "$identity", "-y -P '' -f",
        ):
            self.assertNotIn(forbidden, command)
        self.assertNotIn("identity_pair_failed", runner_source)

    def test_workflow_runs_023_on_windows_and_network_none_linux(self) -> None:
        """CI 必须运行生成器、固定启动器和授权契约，且 Linux 保持断网只读。"""
        workflow = (REPO_ROOT / ".github/workflows/ci.yml").read_text(encoding="utf-8")
        for test in (
            GENERATOR_TEST_PATH.name,
            RUNNER_TEST_PATH.name,
            Path(__file__).name,
        ):
            self.assertGreaterEqual(workflow.count(test), 2, test)
        self.assertIn("--network none", workflow)
        self.assertIn(
            "python@sha256:62eafe52c91cad83c2c74e630bfde917da8c253673e695665d454def84fc9a13",
            workflow,
        )
        self.assertIn('docker pull "$g8_bookworm_image"', workflow)


if __name__ == "__main__":
    unittest.main()
