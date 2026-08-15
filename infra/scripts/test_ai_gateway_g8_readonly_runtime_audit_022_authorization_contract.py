#!/usr/bin/env python3
"""校验 022 工程候选、020 永久墓碑与远端未授权边界。"""

import ast
import hashlib
from pathlib import Path
import subprocess
import unittest


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-runtime-audit-authorization-20260815-022.md"
RUNNER_PATH = REPO_ROOT / "infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-022.py"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-022-command.py"
GENERATOR_TEST_PATH = REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_022_command.py"
RUNNER_TEST_PATH = REPO_ROOT / "infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_022.py"
CHANGE_ID = "CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-022"


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


class TestG8ReadonlyRuntimeAudit022AuthorizationContract(unittest.TestCase):
    def test_status_documents_keep_022_pending_and_g8_incomplete(self) -> None:
        """共享状态文档必须一致记录 022 未授权且软件闭环未完成。"""
        for relative in (
            "README.md",
            "docs/ai-gateway-g8-acceptance.md",
            "docs/ai-gateway-g8-software-closure.md",
            "docs/ai-gateway-g8-test-readonly-access-runbook.md",
            "docs/tools.md",
        ):
            document = (REPO_ROOT / relative).read_text(encoding="utf-8")
            self.assertIn(CHANGE_ID, document, relative)
            self.assertIn("REMOTE_NOT_AUTHORIZED", document, relative)
            self.assertIn("G8_SOFTWARE_CLOSED_LOOP", document, relative)

    def test_document_keeps_remote_execution_unauthorized(self) -> None:
        """工程候选不得被表述为已执行、已安装或软件闭环完成。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        for required in (
            CHANGE_ID,
            "PENDING_ENGINEERING_REVIEW / REMOTE_NOT_AUTHORIZED",
            "021 已永久消费",
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
        """生成器与固定启动器必须绑定 022，默认不得代表远端授权。"""
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
        """四个 022 文件和纯内存命令必须与清单冻结摘要精确一致。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        for path in (RUNNER_PATH, GENERATOR_PATH, RUNNER_TEST_PATH, GENERATOR_TEST_PATH):
            content = path.read_bytes()
            digest = hashlib.sha256(content).hexdigest()
            self.assertNotIn(b"\r\n", content, path)
            self.assertIn(f"| `{path.relative_to(REPO_ROOT).as_posix()}` | {len(content)} | `{digest}` |", document)
        namespace = {"__name__": "g8_022_freeze", "__file__": str(GENERATOR_PATH)}
        exec(compile(GENERATOR_PATH.read_text(encoding="utf-8"), str(GENERATOR_PATH), "exec"), namespace)
        auditor = (GENERATOR_PATH.with_name("audit-ai-gateway-g8-test-server-readonly.sh")).read_bytes()
        command = namespace["build_command"](auditor, receipt_path=namespace["TRUSTED_LOCAL_APPDATA_RECEIPT"]).encode("utf-8")
        self.assertIn(f"| 纯内存冻结命令 | {len(command)} | `{hashlib.sha256(command).hexdigest()}` |", document)

    def test_021_tombstones_remain_byte_exact(self) -> None:
        """022 工程不得恢复或修改 021 已消费入口。"""
        expected = {
            "infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-021.py": "db897b1849edd3e5b9af05794fa8520c2efeb03f3a8462240cdb57a66495ea7d",
            "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-021-command.py": "b5f43b69906b3808f0531e8b796841f53ebcc5df00d8c9a5ba95a1442ab90ca2",
            "infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_021.py": "d846651fedf420526a332fc6b736f32c241b1956d18178e61d2663cc7a5d6b16",
            "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_021_command.py": "91317a191e79872f30a2e69b0c9bd864a7c134b89ec6cd494988a314e8fb5e10",
        }
        for relative, digest in expected.items():
            content = (REPO_ROOT / relative).read_bytes()
            self.assertEqual(hashlib.sha256(content).hexdigest(), digest, relative)
            self.assertNotIn(b"\r\n", content, relative)

    def test_receipt_contract_uses_trusted_unique_path_and_fixed_stage_reasons(self) -> None:
        """回执根因修复必须进入冻结命令，且不能保留 021 的折叠原因。"""
        namespace = {"__name__": "g8_022_receipt", "__file__": str(GENERATOR_PATH)}
        exec(compile(GENERATOR_PATH.read_text(encoding="utf-8"), str(GENERATOR_PATH), "exec"), namespace)
        auditor = GENERATOR_PATH.with_name("audit-ai-gateway-g8-test-server-readonly.sh").read_bytes()
        command = namespace["build_command"](
            auditor,
            receipt_path=namespace["TRUSTED_LOCAL_APPDATA_RECEIPT"],
        )
        for required in (
            "[Environment+SpecialFolder]::LocalApplicationData",
            "[Guid]::NewGuid().ToString('N')",
            "receipt_directory_unavailable",
            "receipt_preoccupied",
            "receipt_write_failed",
            "receipt_flush_failed",
        ):
            self.assertIn(required, command)
        self.assertNotIn("reason=receipt_unavailable", command)

    def test_workflow_runs_022_on_windows_and_network_none_linux(self) -> None:
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
