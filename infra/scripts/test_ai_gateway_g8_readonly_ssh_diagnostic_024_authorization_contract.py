#!/usr/bin/env python3
"""锁定 024 最小 SSH 诊断候选的授权、能力与历史墓碑边界。"""

import ast
import hashlib
import importlib.util
import re
import subprocess
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
RUNNER = ROOT / "infra/scripts/run-ai-gateway-g8-test-readonly-ssh-diagnostic-024.py"
RUNNER_TEST = ROOT / "infra/scripts/test_run_ai_gateway_g8_test_readonly_ssh_diagnostic_024.py"
DOCUMENT = ROOT / "docs/ai-gateway-g8-test-readonly-ssh-diagnostic-authorization-20260816-024.md"
CHANGE_ID = "CHG-G8-TEST-READONLY-SSH-DIAGNOSTIC-20260816-024"


class G8ReadonlySshDiagnostic024AuthorizationContractTests(unittest.TestCase):
    """验证 024 当前只能作为未授权工程候选存在。"""

    def test_document_locks_scope_and_low_sensitivity(self):
        """清单必须锁定单 SSH、固定标记、分类输出和全部禁止能力。"""
        document = DOCUMENT.read_text(encoding="utf-8")
        for expected in (
            CHANGE_ID,
            "PENDING_ENGINEERING_REVIEW / REMOTE_NOT_AUTHORIZED",
            "pc@8.130.9.163:10003",
            "最多 1 个非交互 SSH 会话",
            "固定 `printf` 回执",
            "以 `-F none` 隔离",
            "默认身份文件和当前 `SSH_AUTH_SOCK`",
            "临时单条 known_hosts 是唯一主机密钥信任源",
            "BatchMode=yes",
            "ConnectionAttempts=1",
            "StrictHostKeyChecking=yes",
            "authentication_failed",
            "host_key_failed",
            "connect_timeout",
            "connect_refused",
            "network_unreachable",
            "transport_failed",
            "remote_probe_failed",
            "零重试",
            "不授权执行 024",
            "G8_SOFTWARE_CLOSED_LOOP` 尚未完成",
        ):
            self.assertIn(expected, document)
        for forbidden in ("docker ps", "docker exec", "curl ", "mysql ", "sudo ", "IdentityFile=", " -i "):
            self.assertNotIn(forbidden, document)

    def test_runner_has_one_fixed_remote_marker_and_no_forbidden_capability(self):
        """候选只含一次 SSH 调用和固定远端回执，不得混入审计或写操作。"""
        source = RUNNER.read_text(encoding="utf-8")
        tree = ast.parse(source)
        self.assertIn("REMOTE_EXECUTION_AUTHORIZED = False", source)
        self.assertEqual(1, source.count("process = subprocess.Popen("))
        self.assertEqual(1, source.count("REMOTE_COMMAND ="))
        specification = importlib.util.spec_from_file_location("g8_ssh_diagnostic_024_contract", RUNNER)
        self.assertIsNotNone(specification)
        self.assertIsNotNone(specification.loader)
        module = importlib.util.module_from_spec(specification)
        specification.loader.exec_module(module)
        rendered = " ".join(module.build_ssh_arguments(Path("known_hosts")))
        for expected in (
            "-F none",
            "GlobalKnownHostsFile=none",
            "KnownHostsCommand=none",
            "VerifyHostKeyDNS=no",
            "ControlMaster=no",
            "ControlPath=none",
            "ControlPersist=no",
        ):
            self.assertIn(expected, rendered)
        for forbidden in ("docker exec", "docker ps", "sudo -", "curl ", "mysql ", "IdentityFile=", "IdentitiesOnly"):
            self.assertNotIn(forbidden, rendered)
        imports = {node.names[0].name for node in tree.body if isinstance(node, ast.Import)}
        self.assertNotIn("socket", imports)

    def test_document_frozen_values_match_current_files(self):
        """清单中的大小、SHA-256、Git blob 与 LF 状态必须可独立复算。"""
        document = DOCUMENT.read_text(encoding="utf-8")
        for path in (RUNNER, RUNNER_TEST):
            content = path.read_bytes()
            relative = path.relative_to(ROOT).as_posix()
            size = len(content)
            digest = hashlib.sha256(content).hexdigest()
            blob_header = f"blob {len(content)}\0".encode("ascii")
            blob = hashlib.sha1(blob_header + content, usedforsecurity=False).hexdigest()
            row = rf"`{re.escape(relative)}` \| {size} \| `{digest}` \| `{blob}` \| 0"
            self.assertRegex(document, row)
            self.assertEqual(0, content.count(b"\r\n"))

    def test_023_remains_a_no_import_tombstone(self):
        """新增 024 不能恢复 023 的启动器、生成器或历史参数能力。"""
        for name in (
            "run-ai-gateway-g8-test-readonly-runtime-audit-023.py",
            "prepare-ai-gateway-g8-test-readonly-runtime-audit-023-command.py",
        ):
            path = ROOT / "infra/scripts" / name
            source = path.read_text(encoding="utf-8")
            tree = ast.parse(source)
            self.assertFalse(any(isinstance(node, (ast.Import, ast.ImportFrom)) for node in ast.walk(tree)))
            completed = subprocess.run(
                ["python", "-I", str(path), "--self-test"],
                cwd=ROOT,
                capture_output=True,
                text=True,
                encoding="utf-8",
                timeout=15,
            )
            self.assertEqual(2, completed.returncode)
            self.assertIn("change_id_consumed", completed.stdout)


if __name__ == "__main__":
    unittest.main()
