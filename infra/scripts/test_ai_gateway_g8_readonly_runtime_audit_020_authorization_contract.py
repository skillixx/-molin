#!/usr/bin/env python3
"""校验 020 无安装 Docker 只读审计候选、冻结摘要和远端未授权边界。"""

import base64
import hashlib
import importlib.util
import os
import subprocess
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
AUTH_PATH = REPO_ROOT / "docs/ai-gateway-g8-test-readonly-runtime-audit-authorization-20260815-020.md"
GENERATOR_PATH = REPO_ROOT / "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-020-command.py"
TEST_PATH = REPO_ROOT / "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_020_command.py"
AUDITOR_PATH = REPO_ROOT / "infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh"
CHANGE_ID = "CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-020"
ENGINEERING_HEAD = "dcb594d33e79bfbb059293e4734e49e62409d51a"
ENGINEERING_MERGE = "3c63539279a34ae2365fc9d7e26e207dd728c4ba"
ENGINEERING_BASE = "b9211b8a90610aa2e45873fa9de54575bce58fb5"
STATUS_PATHS = (
    REPO_ROOT / "README.md",
    REPO_ROOT / "docs/ai-gateway-g8-acceptance.md",
    REPO_ROOT / "docs/ai-gateway-g8-test-readonly-access-runbook.md",
    REPO_ROOT / "docs/tools.md",
)


def load_generator():
    """只加载离线生成器纯函数。"""
    specification = importlib.util.spec_from_file_location("g8_runtime_audit_020_contract", GENERATOR_PATH)
    if specification is None or specification.loader is None:
        raise RuntimeError("020 运行态审计生成器无法加载")
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
    return module


class TestG8ReadonlyRuntimeAudit020AuthorizationContract(unittest.TestCase):
    def test_document_states_no_install_and_remote_not_authorized(self) -> None:
        """工程授权不得被解释为 SSH 或运行态审计授权。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        for required in (
            "PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED",
            CHANGE_ID,
            "不安装受控只读审计入口",
            "不执行 sudo",
            "`pc` 直接使用既有 Docker 权限",
            "不授权 SSH、Docker 命令、HTTP、数据库查询或测试服操作",
            "`G8_SOFTWARE_CLOSED_LOOP` 尚未完成",
            "PR #394",
            "31861762018",
            ENGINEERING_HEAD,
            ENGINEERING_MERGE,
        ):
            self.assertIn(required, document)

    def test_generator_disables_execution_and_has_no_install_asset(self) -> None:
        """020 只生成审计命令，不包含安装器或 sudo 能力。"""
        module = load_generator()
        self.assertEqual(module.CHANGE_ID, CHANGE_ID)
        self.assertFalse(module.CHANGE_ID_CONSUMED)
        self.assertFalse(module.REMOTE_EXECUTION_AUTHORIZED)
        self.assertFalse((REPO_ROOT / "infra/scripts/g8-test-readonly-access-install-020.sh").exists())
        self.assertFalse((REPO_ROOT / "infra/scripts/test_g8_test_readonly_access_install_020.py").exists())

    def test_status_documents_use_the_no_install_runtime_audit_contract(self) -> None:
        """项目状态文档必须统一说明无安装、无 sudo、远端未授权和闭环未完成。"""
        for path in STATUS_PATHS:
            document = path.read_text(encoding="utf-8")
            for required in (
                CHANGE_ID,
                "PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED",
                "不使用 sudo",
                "`pc`",
                "Docker",
                "G8_SOFTWARE_CLOSED_LOOP",
            ):
                self.assertIn(required, document, path)
            self.assertNotIn("CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260815-020", document, path)
        runbook = STATUS_PATHS[2].read_text(encoding="utf-8")
        self.assertNotIn("## 4. 安装后的独立只读核验", runbook)
        self.assertNotIn("sudo -n /usr/local/libexec/molin/g8-test-readonly-audit", runbook)
        self.assertNotIn("审计输出 `pc_docker_group_member=false`", runbook)
        self.assertNotIn("后续安装与运行态核验仍需要相互独立的 ChangeId", STATUS_PATHS[1].read_text(encoding="utf-8"))

    def test_frozen_files_and_command_match_document(self) -> None:
        """生成器、测试、审计源与纯内存命令摘要必须可独立重算。"""
        document = AUTH_PATH.read_text(encoding="utf-8")
        attributes = (REPO_ROOT / ".gitattributes").read_text(encoding="utf-8")
        for path in (AUDITOR_PATH, GENERATOR_PATH, TEST_PATH):
            content = path.read_bytes()
            digest = hashlib.sha256(content).hexdigest()
            self.assertNotIn(b"\r\n", content, path)
            self.assertIn(f"{path.relative_to(REPO_ROOT).as_posix()} text eol=lf", attributes)
            self.assertIn(
                f"| `{path.relative_to(REPO_ROOT).as_posix()}` | {len(content)} | `{digest}` | CRLF=0 |",
                document,
            )
        module = load_generator()
        command = module.build_command(AUDITOR_PATH.read_bytes(), receipt_path=module.TRUSTED_PROFILE_RECEIPT).encode("utf-8")
        self.assertIn(
            f"| 纯内存冻结命令 | {len(command)} | `{hashlib.sha256(command).hexdigest()}` | 不写盘 |",
            document,
        )
        payload = command.decode("utf-8").split("$remotePayload = '", 1)[1].split("'\n", 1)[0]
        remote = base64.b64decode(payload, validate=True).decode("utf-8")
        self.assertNotIn("sudo", remote)
        self.assertNotIn("docker run", remote)

    def test_019_tombstones_remain_byte_exact(self) -> None:
        """020 方案变化不得恢复或修改 019 历史入口。"""
        expected = {
            "infra/scripts/g8-test-readonly-access-install-019.sh": "368091106a2b09bcb6353e9030309820ce2a19776d2444016bb3df066a158f78",
            "infra/scripts/prepare-ai-gateway-g8-test-readonly-access-019-command.py": "2fbd91d95b585b694cdebb9013925b38627a03feb00ca455ab61a1894694eaf9",
            "infra/scripts/test_g8_test_readonly_access_install_019.py": "7a6704b66fe6105b751da66bb2f4ca27e5890bab88c8a7aec5c9d6b0f67563d8",
            "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_019_command.py": "fe121f8925352cb7a75f43722a1ec0d480ec09a3dbcf3ad4a7aae8cd8e227594",
        }
        for relative, digest in expected.items():
            self.assertEqual(hashlib.sha256((REPO_ROOT / relative).read_bytes()).hexdigest(), digest, relative)

    def test_merged_main_blobs_and_parent_order_match_archive(self) -> None:
        """归档必须从工程 merge 原始对象复核父顺序与冻结文件。"""
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
            "infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh": (
                18377,
                "308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256",
                "27450efc39af7e763ea8df0c59d584433d5e5edd",
            ),
            "infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-020-command.py": (
                27486,
                "3a286187602277c2255e978712e37cff7d6edf46d292a185e665aaa70654bbae",
                "212124e085c2f34adf11eae62b0e0119c5d8f44e",
            ),
            "infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_020_command.py": (
                14896,
                "a156e62417826ce5a8f6347d46edca384f6abfaa5e819aa300dc0dc55b3d5b8b",
                "c3930bc478b2b05d33822db2996618949384f9f3",
            ),
        }
        for relative, (size, digest, blob) in expected.items():
            content = subprocess.run(
                ["git", "-c", f"safe.directory={REPO_ROOT}", "show", f"{ENGINEERING_MERGE}:{relative}"],
                cwd=REPO_ROOT,
                capture_output=True,
                check=True,
            ).stdout
            self.assertEqual((len(content), hashlib.sha256(content).hexdigest()), (size, digest), relative)
            self.assertEqual(hashlib.sha1(b"blob " + str(size).encode("ascii") + b"\0" + content).hexdigest(), blob)
            self.assertNotIn(b"\r\n", content, relative)


if __name__ == "__main__":
    unittest.main()
