#!/usr/bin/env python3
"""校验 021 工程候选、020 永久墓碑与远端未授权边界。"""

import ast
import hashlib
from pathlib import Path
import subprocess
import unittest


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-runtime-audit-authorization-20260815-021.md"
RUNNER_PATH = REPO_ROOT / "infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-021.py"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-021-command.py"
GENERATOR_TEST_PATH = REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_021_command.py"
RUNNER_TEST_PATH = REPO_ROOT / "infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_021.py"
CHANGE_ID = "CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-021"
ENGINEERING_HEAD = "c73ef139721bcfc693ffb31caa6fe803be526286"
ENGINEERING_MERGE = "8bc05cbf3bc71a8954087dc7f26732f836e5212e"
ENGINEERING_BASE = "358edfd8e8d5d3293944314d79d503245049649a"


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


class TestG8ReadonlyRuntimeAudit021AuthorizationContract(unittest.TestCase):
    def test_document_keeps_remote_execution_unauthorized(self) -> None:
        """工程候选不得被表述为已执行、已安装或软件闭环完成。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        for required in (
            CHANGE_ID,
            "PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED",
            "PR #398",
            "31867790659 completed/success",
            ENGINEERING_HEAD,
            ENGINEERING_MERGE,
            "020 已永久消费",
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
        self.assertNotIn("PENDING_ENGINEERING_REVIEW", document)

    def test_execution_files_bind_new_change_id_and_default_closed_state(self) -> None:
        """生成器与固定启动器必须绑定 021，默认不得代表远端授权。"""
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

    def test_frozen_files_and_command_match_document(self) -> None:
        """四个 021 文件和纯内存命令必须与清单冻结摘要精确一致。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        for path in (RUNNER_PATH, GENERATOR_PATH, RUNNER_TEST_PATH, GENERATOR_TEST_PATH):
            content = path.read_bytes()
            digest = hashlib.sha256(content).hexdigest()
            self.assertNotIn(b"\r\n", content, path)
            self.assertIn(f"| `{path.relative_to(REPO_ROOT).as_posix()}` | {len(content)} | `{digest}` |", document)
        namespace = {"__name__": "g8_021_freeze", "__file__": str(GENERATOR_PATH)}
        exec(compile(GENERATOR_PATH.read_text(encoding="utf-8"), str(GENERATOR_PATH), "exec"), namespace)
        auditor = (GENERATOR_PATH.with_name("audit-ai-gateway-g8-test-server-readonly.sh")).read_bytes()
        command = namespace["build_command"](auditor, receipt_path=namespace["TRUSTED_PROFILE_RECEIPT"]).encode("utf-8")
        self.assertIn(f"| 纯内存冻结命令 | {len(command)} | `{hashlib.sha256(command).hexdigest()}` |", document)

    def test_020_tombstones_remain_byte_exact(self) -> None:
        """021 工程不得恢复或修改 020 已消费入口。"""
        expected = {
            "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-020-command.py": "57acdab38d9eb9fe9adaa34541c8024bd6b70fc2e36f4214a79eeb50b59e405f",
            "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_020_command.py": "9ca44161de7a6b013ddbb374bb1ca074fb86db9b163cf789fcae67a53dfbf5ca",
        }
        for relative, digest in expected.items():
            self.assertEqual(hashlib.sha256((REPO_ROOT / relative).read_bytes()).hexdigest(), digest, relative)

    def test_postmerge_objects_match_archived_evidence(self) -> None:
        """合并提交父顺序和五个原始 blob 必须与归档证据逐字节一致。"""
        expected = {
            RUNNER_PATH: "8662e3e6558453799245d084e32b8826ec84e969",
            GENERATOR_PATH: "087683242cae3b3a1696e8815a9102f6650f002b",
            RUNNER_TEST_PATH: "78b68b48cf18892393f6e71abb89ac2e96c59d6e",
            GENERATOR_TEST_PATH: "ec8a2e184ea7e1abd5aa1dfe8d3db4d4eee69adc",
            GENERATOR_PATH.with_name("audit-ai-gateway-g8-test-server-readonly.sh"): "27450efc39af7e763ea8df0c59d584433d5e5edd",
        }
        parents = subprocess.run(
            ["git", "show", "-s", "--format=%P", ENGINEERING_MERGE],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
            timeout=15,
        )
        if parents.returncode != 0:
            self.skipTest("linked worktree 的外部 Git 对象库未挂载")
        self.assertEqual(parents.stdout.strip(), f"{ENGINEERING_BASE} {ENGINEERING_HEAD}")
        for path, blob in expected.items():
            relative = path.relative_to(REPO_ROOT).as_posix()
            actual_blob = subprocess.run(
                ["git", "rev-parse", f"{ENGINEERING_MERGE}:{relative}"],
                cwd=REPO_ROOT,
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=True,
                timeout=15,
            ).stdout.strip()
            frozen = subprocess.run(
                ["git", "show", f"{ENGINEERING_MERGE}:{relative}"],
                cwd=REPO_ROOT,
                capture_output=True,
                check=True,
                timeout=15,
            ).stdout
            self.assertEqual(actual_blob, blob, relative)
            self.assertEqual(frozen, path.read_bytes(), relative)

    def test_workflow_runs_021_on_windows_and_network_none_linux(self) -> None:
        """CI 必须运行生成器、固定启动器和授权契约，且 Linux 保持断网只读。"""
        workflow = (REPO_ROOT / ".github/workflows/ci.yml").read_text(encoding="utf-8")
        for test in (
            GENERATOR_TEST_PATH.name,
            RUNNER_TEST_PATH.name,
            Path(__file__).name,
        ):
            self.assertGreaterEqual(workflow.count(test), 2, test)
        self.assertIn("--network none", workflow)
        self.assertIn("python:3.13-bookworm", workflow)


if __name__ == "__main__":
    unittest.main()
